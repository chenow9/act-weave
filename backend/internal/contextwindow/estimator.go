package contextwindow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAI chat framing overhead (approximate, fixed per message / request).
// These constants are deliberately conservative and versioned with EstimatorVersion.
const (
	// tokensPerMessage is the per-message framing overhead (role/name separators).
	tokensPerMessage int64 = 4
	// tokensPerReplyPriming is the assistant reply priming overhead on the request.
	tokensPerReplyPriming int64 = 3
	// fixedRequestOverhead covers chat format bookkeeping beyond per-message costs.
	fixedRequestOverhead int64 = 3
)

// MessageRole is a chat message role for estimation.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// Message is a single estimated prompt message (no persistence).
type Message struct {
	Role    MessageRole
	Content string
	Name    string
}

// ToolSchema is a tool definition estimated from its JSON schema payload.
type ToolSchema struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// EstimateResult is a versioned, body-free estimation outcome.
type EstimateResult struct {
	Profile          string
	TokenizerVersion string
	EstimatorVersion string
	// Component token counts (never include plaintext).
	SystemTokens      int64
	MessagesTokens    int64
	ToolsTokens       int64
	FramingTokens     int64
	FixedOverhead     int64
	TotalTokens       int64
	MessageCount      int
	ToolCount         int
	PerMessageTokens  []int64
}

// Estimator estimates prompt budgets for a fixed tokenizer profile.
type Estimator struct {
	tokenizer Tokenizer
}

// NewEstimator builds an estimator for a registered profile.
func NewEstimator(profile string) (*Estimator, error) {
	tok, err := LookupTokenizer(profile)
	if err != nil {
		return nil, err
	}
	return &Estimator{tokenizer: tok}, nil
}

// EstimateRequest estimates a full chat request: system + tools + messages.
func (e *Estimator) EstimateRequest(system string, tools []ToolSchema, messages []Message) (EstimateResult, error) {
	if e == nil || e.tokenizer == nil {
		return EstimateResult{}, ErrUnavailableProfile
	}
	result := EstimateResult{
		Profile:          e.tokenizer.Profile(),
		TokenizerVersion: e.tokenizer.Version(),
		EstimatorVersion: EstimatorVersion,
		FixedOverhead:    fixedRequestOverhead + tokensPerReplyPriming,
		MessageCount:     len(messages),
		ToolCount:        len(tools),
		PerMessageTokens: make([]int64, 0, len(messages)),
	}

	sysTokens, err := e.estimateMessage(Message{Role: RoleSystem, Content: system})
	if err != nil {
		return EstimateResult{}, err
	}
	// Only count system framing if non-empty system content.
	if strings.TrimSpace(system) != "" {
		result.SystemTokens = sysTokens
	}

	var msgTotal int64
	for _, msg := range messages {
		n, err := e.estimateMessage(msg)
		if err != nil {
			return EstimateResult{}, err
		}
		result.PerMessageTokens = append(result.PerMessageTokens, n)
		msgTotal += n
	}
	result.MessagesTokens = msgTotal

	var toolTotal int64
	for _, tool := range tools {
		n, err := e.estimateTool(tool)
		if err != nil {
			return EstimateResult{}, err
		}
		toolTotal += n
	}
	result.ToolsTokens = toolTotal

	// Framing: per-message overhead already inside estimateMessage; track aggregate.
	framing := int64(0)
	if strings.TrimSpace(system) != "" {
		framing += tokensPerMessage
	}
	framing += tokensPerMessage * int64(len(messages))
	framing += tokensPerMessage * int64(len(tools)) // tool defs treated like messages in envelope
	result.FramingTokens = framing

	result.TotalTokens = result.SystemTokens + result.MessagesTokens + result.ToolsTokens + result.FixedOverhead
	if result.TotalTokens < 0 {
		return EstimateResult{}, fmt.Errorf("negative token total")
	}
	return result, nil
}

// EstimateText counts tokens for a plain string (no framing).
func (e *Estimator) EstimateText(text string) (int64, error) {
	if e == nil || e.tokenizer == nil {
		return 0, ErrUnavailableProfile
	}
	return e.tokenizer.CountText(text)
}

func (e *Estimator) estimateMessage(msg Message) (int64, error) {
	content := msg.Content
	if msg.Name != "" {
		content = msg.Name + "\n" + content
	}
	// Include role label so framing is not underestimated.
	content = string(msg.Role) + "\n" + content
	n, err := e.tokenizer.CountText(content)
	if err != nil {
		return 0, err
	}
	n += tokensPerMessage
	if n < 0 {
		return 0, fmt.Errorf("negative message token count")
	}
	return n, nil
}

func (e *Estimator) estimateTool(tool ToolSchema) (int64, error) {
	var b strings.Builder
	b.WriteString("tool\n")
	b.WriteString(tool.Name)
	b.WriteByte('\n')
	b.WriteString(tool.Description)
	b.WriteByte('\n')
	if len(tool.Parameters) > 0 {
		b.Write(tool.Parameters)
	}
	n, err := e.tokenizer.CountText(b.String())
	if err != nil {
		return 0, err
	}
	n += tokensPerMessage
	if n < 0 {
		return 0, fmt.Errorf("negative tool token count")
	}
	return n, nil
}
