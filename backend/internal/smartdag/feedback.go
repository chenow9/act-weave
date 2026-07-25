package smartdag

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// FailureFeedback source values (D14 / R4).
const (
	FailureSourceCompile    = "compile"
	FailureSourceTrial      = "trial"
	FailureSourceProduction = "production"
	FailureSourceAgentRun   = "agent_run"
	FailureSourceGuard      = "guard"
)

// SuggestedAction values for FailureIssue (optional product hints).
const (
	SuggestedActionEditMapping = "edit_mapping"
	SuggestedActionReplaceTool = "replace_tool"
	SuggestedActionAddApproval = "add_approval"
	SuggestedActionImportTool  = "import_tool"
	SuggestedActionRegenerate  = "regenerate"
)

// MaxFailureFeedbackRawSummaryRunes caps rawSummary (no secrets / permanent full text).
const MaxFailureFeedbackRawSummaryRunes = 2000

// MaxFailureFeedbackIssues is a defensive cap on issue list size.
const MaxFailureFeedbackIssues = 64

// FailureFeedback is compile/trial/production failure context for draft revise (D5/D14).
// Feedback turns produce a new Draft version only — never auto-publish.
type FailureFeedback struct {
	Source              string              `json:"source"`
	WorkflowID          string              `json:"workflowId"`
	CompilationID       string              `json:"compilationId,omitempty"`
	ExecutionID         string              `json:"executionId,omitempty"`
	RunID               string              `json:"runId,omitempty"`
	Issues              []FailureIssue      `json:"issues"`
	MissingCapabilities []MissingCapability `json:"missingCapabilities,omitempty"`
	// RawSummary is a truncated human summary; must not carry secrets.
	RawSummary string `json:"rawSummary,omitempty"`
}

// FailureIssue is one structured failure reason for model revision context.
type FailureIssue struct {
	Code            string `json:"code"`
	NodeID          string `json:"nodeId,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggestedAction,omitempty"`
}

// validFailureSources is the closed set of FailureFeedback.source values.
var validFailureSources = map[string]struct{}{
	FailureSourceCompile:    {},
	FailureSourceTrial:      {},
	FailureSourceProduction: {},
	FailureSourceAgentRun:   {},
	FailureSourceGuard:      {},
}

// validSuggestedActions is the closed set of optional issue actions.
var validSuggestedActions = map[string]struct{}{
	SuggestedActionEditMapping: {},
	SuggestedActionReplaceTool: {},
	SuggestedActionAddApproval: {},
	SuggestedActionImportTool:  {},
	SuggestedActionRegenerate:  {},
	"":                         {},
}

// ParseFailureFeedback decodes optional feedback JSON.
// Empty / null raw returns (nil, nil). Invalid JSON or shape returns ErrInvalid.
func ParseFailureFeedback(raw json.RawMessage) (*FailureFeedback, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var feedback FailureFeedback
	if err := json.Unmarshal(raw, &feedback); err != nil {
		return nil, fmt.Errorf("%w: failure feedback json: %v", ErrInvalid, err)
	}
	if err := feedback.Validate(); err != nil {
		return nil, err
	}
	feedback.Normalize()
	return &feedback, nil
}

// Validate checks required fields and closed enums.
func (f *FailureFeedback) Validate() error {
	if f == nil {
		return nil
	}
	source := strings.TrimSpace(f.Source)
	if source == "" {
		return fmt.Errorf("%w: failure feedback source is required", ErrInvalid)
	}
	if _, ok := validFailureSources[source]; !ok {
		return fmt.Errorf("%w: failure feedback source %q is not supported", ErrInvalid, source)
	}
	if !validUUID(strings.TrimSpace(f.WorkflowID)) {
		return fmt.Errorf("%w: failure feedback workflowId must be a uuid", ErrInvalid)
	}
	if len(f.Issues) > MaxFailureFeedbackIssues {
		return fmt.Errorf("%w: failure feedback issues exceed limit", ErrInvalid)
	}
	for i, issue := range f.Issues {
		if strings.TrimSpace(issue.Code) == "" && strings.TrimSpace(issue.Message) == "" {
			return fmt.Errorf("%w: failure feedback issues[%d] needs code or message", ErrInvalid, i)
		}
		action := strings.TrimSpace(issue.SuggestedAction)
		if _, ok := validSuggestedActions[action]; !ok {
			return fmt.Errorf("%w: failure feedback issues[%d] suggestedAction %q is not supported", ErrInvalid, i, action)
		}
	}
	if utf8.RuneCountInString(f.RawSummary) > MaxFailureFeedbackRawSummaryRunes {
		return fmt.Errorf("%w: failure feedback rawSummary exceeds limit", ErrInvalid)
	}
	for _, id := range []string{f.CompilationID, f.ExecutionID, f.RunID} {
		id = strings.TrimSpace(id)
		if id != "" && !validUUID(id) {
			return fmt.Errorf("%w: failure feedback optional id must be a uuid when set", ErrInvalid)
		}
	}
	return nil
}

// Normalize trims fields and truncates rawSummary defensively.
func (f *FailureFeedback) Normalize() {
	if f == nil {
		return
	}
	f.Source = strings.TrimSpace(f.Source)
	f.WorkflowID = strings.TrimSpace(f.WorkflowID)
	f.CompilationID = strings.TrimSpace(f.CompilationID)
	f.ExecutionID = strings.TrimSpace(f.ExecutionID)
	f.RunID = strings.TrimSpace(f.RunID)
	f.RawSummary = truncateRunes(strings.TrimSpace(f.RawSummary), MaxFailureFeedbackRawSummaryRunes)
	if f.Issues == nil {
		f.Issues = []FailureIssue{}
	}
	for i := range f.Issues {
		f.Issues[i].Code = strings.TrimSpace(f.Issues[i].Code)
		f.Issues[i].NodeID = strings.TrimSpace(f.Issues[i].NodeID)
		f.Issues[i].Message = strings.TrimSpace(f.Issues[i].Message)
		f.Issues[i].SuggestedAction = strings.TrimSpace(f.Issues[i].SuggestedAction)
	}
}

// MarshalJSON ensures stable empty-slice encoding for Issues.
func (f FailureFeedback) MarshalJSON() ([]byte, error) {
	type alias FailureFeedback
	out := alias(f)
	if out.Issues == nil {
		out.Issues = []FailureIssue{}
	}
	return json.Marshal(out)
}

// FormatRevisionMessage builds a user-message seed when the client omits one
// (seed + feedback revise path, D14).
func FormatRevisionMessage(feedback *FailureFeedback) string {
	if feedback == nil {
		return "请根据失败反馈修订流程草稿。"
	}
	var b strings.Builder
	b.WriteString("请根据以下")
	switch feedback.Source {
	case FailureSourceCompile:
		b.WriteString("编译")
	case FailureSourceTrial:
		b.WriteString("试运行")
	case FailureSourceProduction:
		b.WriteString("生产执行")
	case FailureSourceAgentRun:
		b.WriteString("Agent 运行")
	case FailureSourceGuard:
		b.WriteString("图校验")
	default:
		b.WriteString("失败")
	}
	b.WriteString("问题修订流程草稿，只产出新 Draft，不要发布。")
	if summary := strings.TrimSpace(feedback.RawSummary); summary != "" {
		b.WriteString("\n摘要：")
		b.WriteString(summary)
	}
	for i, issue := range feedback.Issues {
		if i >= 8 {
			b.WriteString(fmt.Sprintf("\n…另有 %d 条问题", len(feedback.Issues)-i))
			break
		}
		b.WriteString("\n- ")
		if issue.Code != "" {
			b.WriteString(issue.Code)
			b.WriteString(": ")
		}
		if issue.Message != "" {
			b.WriteString(issue.Message)
		}
		if issue.NodeID != "" {
			b.WriteString(" (node=")
			b.WriteString(issue.NodeID)
			b.WriteString(")")
		}
	}
	for _, cap := range feedback.MissingCapabilities {
		b.WriteString("\n- 缺失能力：")
		b.WriteString(cap.Name)
		if cap.Reason != "" {
			b.WriteString(" — ")
			b.WriteString(cap.Reason)
		}
	}
	msg := b.String()
	if utf8.RuneCountInString(msg) > MaxTurnMessageRunes {
		return truncateRunes(msg, MaxTurnMessageRunes)
	}
	return msg
}

// RevisedFromUI builds the draft.ui.revisedFrom stamp for audit linkage (D5).
func RevisedFromUI(feedback *FailureFeedback) map[string]any {
	if feedback == nil {
		return nil
	}
	out := map[string]any{
		"source":     feedback.Source,
		"workflowId": feedback.WorkflowID,
	}
	if feedback.CompilationID != "" {
		out["compilationId"] = feedback.CompilationID
	}
	if feedback.ExecutionID != "" {
		out["executionId"] = feedback.ExecutionID
	}
	if feedback.RunID != "" {
		out["runId"] = feedback.RunID
	}
	if len(feedback.Issues) > 0 {
		codes := make([]string, 0, len(feedback.Issues))
		for _, issue := range feedback.Issues {
			if issue.Code != "" {
				codes = append(codes, issue.Code)
			}
		}
		if len(codes) > 0 {
			out["issueCodes"] = codes
		}
	}
	return out
}

// ContextForModel is a compact text block for GraphModelInput (multi-turn revise).
func (f *FailureFeedback) ContextForModel() string {
	if f == nil {
		return ""
	}
	return FormatRevisionMessage(f)
}
