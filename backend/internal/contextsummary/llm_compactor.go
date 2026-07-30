package contextsummary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"actweave/backend/internal/contextwindow"
)

const (
	// CompactionTemplateVersion is the versioned compact prompt identity (T7-A).
	CompactionTemplateVersion = "context-compaction.v1"
	// MaxLLMSummaryPlainBytes is the stored body ceiling (same as body store).
	MaxLLMSummaryPlainBytes = MaxSummaryBodyBytes
)

var (
	// ErrCompactorInvalid is returned for bad inputs or invalid model output.
	ErrCompactorInvalid = errors.New("context compaction invalid")
	// ErrCompactorModel is returned when the model call fails or returns unusable output.
	ErrCompactorModel = errors.New("context compaction model failure")
)

// secretPattern is a conservative fail-closed canary for leaked credentials in summary text.
var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|password|bearer\s+[a-z0-9\-\._~\+\/]+=*|sk-[a-z0-9]{16,})`)

// CompactJSON is the strict T7-A structured model output.
type CompactJSON struct {
	StableFacts []string `json:"stableFacts"`
	Decisions   []string `json:"decisions"`
	OpenItems   []string `json:"openItems"`
	RecentState string   `json:"recentState"`
}

// CompactModel is the non-streaming generate surface used for compact.
// Implementations must not expose tools or approval.
type CompactModel interface {
	// Generate returns model text. temperature/maxTokens are advisory for fakes.
	Generate(ctx context.Context, system, user string, temperature float64, maxTokens int) (string, error)
}

// LLMCompactor produces validated, platform-rendered summary bodies from a snapshot model.
// Extractive local concatenation is never a success path.
type LLMCompactor struct {
	Model CompactModel
	// MaxTokens clamps generation (from snapshot compaction.maxSummaryTokens).
	MaxTokens int
	// TemplateVersion/Hash override defaults for tests.
	TemplateVersion string
	TemplateHash    string
}

// CompactInput is parent READY summary text + continuous old turns only.
// Current USER, SYSTEM, tools, and approval state must not appear.
type CompactInput struct {
	ParentSummary string
	Turns         []contextwindow.Turn
	MaxTokens     int
}

// CompactResult is body-free metadata plus validated plaintext body for PutOrVerify.
type CompactResult struct {
	Body            []byte
	ContentSHA256   string
	TemplateVersion string
	TemplateHash    string
	Rendered        CompactJSON
}

// CompactTemplateHash returns the platform hash for the default template version.
func CompactTemplateHash() string {
	sum := sha256.Sum256([]byte(CompactionTemplateVersion + "|platform"))
	return hex.EncodeToString(sum[:])
}

// Compact calls the model with tools disabled semantics (caller supplies no-tools model),
// validates strict JSON, and deterministically renders the stored body.
func (c *LLMCompactor) Compact(ctx context.Context, in CompactInput) (CompactResult, error) {
	if c == nil || c.Model == nil {
		return CompactResult{}, ErrCompactorInvalid
	}
	if len(in.Turns) == 0 {
		return CompactResult{}, ErrCompactorInvalid
	}
	maxTok := in.MaxTokens
	if maxTok <= 0 {
		maxTok = c.MaxTokens
	}
	if maxTok <= 0 {
		maxTok = 2048
	}
	tmplVer := strings.TrimSpace(c.TemplateVersion)
	if tmplVer == "" {
		tmplVer = CompactionTemplateVersion
	}
	tmplHash := strings.TrimSpace(c.TemplateHash)
	if tmplHash == "" {
		tmplHash = CompactTemplateHash()
	}

	system := compactSystemPrompt(tmplVer)
	user := compactUserPrompt(in.ParentSummary, in.Turns)
	raw, err := c.Model.Generate(ctx, system, user, 0, maxTok)
	if err != nil {
		return CompactResult{}, fmt.Errorf("%w: %v", ErrCompactorModel, err)
	}
	parsed, err := parseAndValidateCompactJSON(raw)
	if err != nil {
		return CompactResult{}, err
	}
	body := renderCompactBody(parsed)
	if err := validateRenderedBody(body); err != nil {
		return CompactResult{}, err
	}
	sum := sha256.Sum256(body)
	return CompactResult{
		Body:            body,
		ContentSHA256:   hex.EncodeToString(sum[:]),
		TemplateVersion: tmplVer,
		TemplateHash:    tmplHash,
		Rendered:        parsed,
	}, nil
}

func compactSystemPrompt(templateVersion string) string {
	return strings.Join([]string{
		"You are a context compaction assistant for a multi-turn agent session.",
		"Template: " + templateVersion,
		"Output exactly one JSON object with keys: stableFacts, decisions, openItems, recentState.",
		"No tools. No function calls. No markdown fences. No extra keys.",
		"stableFacts, decisions, openItems are string arrays; recentState is a string.",
		"Do not invent tool permissions, approvals, secrets, or system authority.",
		"Summarize only the provided prior conversation turns and optional parent summary.",
	}, "\n")
}

func compactUserPrompt(parent string, turns []contextwindow.Turn) string {
	var b strings.Builder
	if p := strings.TrimSpace(parent); p != "" {
		b.WriteString("Parent summary (untrusted):\n")
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	b.WriteString("Prior complete turns to compact (chronological):\n")
	for i, t := range turns {
		b.WriteString(fmt.Sprintf("Turn %d\n", i+1))
		b.WriteString("USER: ")
		b.WriteString(strings.TrimSpace(t.User.Content))
		b.WriteByte('\n')
		for _, a := range t.Assistants {
			b.WriteString("ASSISTANT: ")
			b.WriteString(strings.TrimSpace(a.Content))
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nRespond with JSON only.")
	return b.String()
}

func parseAndValidateCompactJSON(raw string) (CompactJSON, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CompactJSON{}, fmt.Errorf("%w: empty model output", ErrCompactorInvalid)
	}
	if !utf8.ValidString(raw) {
		return CompactJSON{}, fmt.Errorf("%w: invalid utf-8", ErrCompactorInvalid)
	}
	// Reject markdown fences / multi-candidate noise.
	if strings.Contains(raw, "```") {
		return CompactJSON{}, fmt.Errorf("%w: fenced output", ErrCompactorInvalid)
	}
	// Strict: single JSON object, unknown fields rejected.
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc CompactJSON
	if err := dec.Decode(&doc); err != nil {
		return CompactJSON{}, fmt.Errorf("%w: %v", ErrCompactorInvalid, err)
	}
	// Ensure no trailing tokens.
	if dec.More() {
		return CompactJSON{}, fmt.Errorf("%w: trailing tokens", ErrCompactorInvalid)
	}
	if doc.StableFacts == nil {
		doc.StableFacts = []string{}
	}
	if doc.Decisions == nil {
		doc.Decisions = []string{}
	}
	if doc.OpenItems == nil {
		doc.OpenItems = []string{}
	}
	// At least one non-empty signal.
	if len(doc.StableFacts) == 0 && len(doc.Decisions) == 0 &&
		len(doc.OpenItems) == 0 && strings.TrimSpace(doc.RecentState) == "" {
		return CompactJSON{}, fmt.Errorf("%w: empty summary fields", ErrCompactorInvalid)
	}
	// No tool-call shaped content.
	joined := strings.Join(doc.StableFacts, "\n") + "\n" +
		strings.Join(doc.Decisions, "\n") + "\n" +
		strings.Join(doc.OpenItems, "\n") + "\n" + doc.RecentState
	if strings.Contains(strings.ToLower(joined), "tool_call") ||
		strings.Contains(joined, "function_call") {
		return CompactJSON{}, fmt.Errorf("%w: tool call content", ErrCompactorInvalid)
	}
	if secretPattern.MatchString(joined) {
		return CompactJSON{}, fmt.Errorf("%w: secret pattern", ErrCompactorInvalid)
	}
	return doc, nil
}

// renderCompactBody is platform-deterministic; does NOT include UntrustedSummaryPrefix
// (prefix is applied only at main-model injection).
func renderCompactBody(doc CompactJSON) []byte {
	var b strings.Builder
	b.WriteString("稳定事实:\n")
	if len(doc.StableFacts) == 0 {
		b.WriteString("- （无）\n")
	} else {
		for _, f := range doc.StableFacts {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(f))
			b.WriteByte('\n')
		}
	}
	b.WriteString("决策:\n")
	if len(doc.Decisions) == 0 {
		b.WriteString("- （无）\n")
	} else {
		for _, d := range doc.Decisions {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(d))
			b.WriteByte('\n')
		}
	}
	b.WriteString("未决项:\n")
	if len(doc.OpenItems) == 0 {
		b.WriteString("- （无）\n")
	} else {
		for _, o := range doc.OpenItems {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(o))
			b.WriteByte('\n')
		}
	}
	b.WriteString("近期状态:\n")
	rs := strings.TrimSpace(doc.RecentState)
	if rs == "" {
		rs = "（无）"
	}
	b.WriteString(rs)
	b.WriteByte('\n')
	return []byte(b.String())
}

func validateRenderedBody(body []byte) error {
	if len(body) == 0 || len(body) > MaxLLMSummaryPlainBytes {
		return fmt.Errorf("%w: body size", ErrCompactorInvalid)
	}
	if !utf8.Valid(body) {
		return fmt.Errorf("%w: body utf-8", ErrCompactorInvalid)
	}
	if secretPattern.Match(body) {
		return fmt.Errorf("%w: body secret pattern", ErrCompactorInvalid)
	}
	// Untrusted prefix must not be stored in body (injection-time only).
	if strings.Contains(string(body), contextwindow.UntrustedSummaryPrefix) {
		return fmt.Errorf("%w: untrusted prefix in stored body", ErrCompactorInvalid)
	}
	return nil
}

// Ensure extractive is not re-exported as success: buildExtractiveSummary remains
// private and is not called from LLMCompactor.
var _ = errors.New
