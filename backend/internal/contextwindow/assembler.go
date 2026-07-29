package contextwindow

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrRequiredInputTooLarge is returned when SYSTEM+tools+current USER exceed the budget.
	ErrRequiredInputTooLarge = errors.New("CONTEXT_REQUIRED_INPUT_TOO_LARGE")
	// ErrInvalidAssemblerInput is returned for incomplete assembler inputs.
	ErrInvalidAssemblerInput = errors.New("invalid assembler input")
)

// AssemblerInput is pure input for token_window assembly (no DB/provider).
type AssemblerInput struct {
	PolicyMode               string
	ModelContextWindowTokens int64
	OutputReserveTokens      int64
	SafetyMarginTokens       int64
	// MaxInputTokens 0 means no additional clamp beyond hard ceiling.
	MaxInputTokens int64
	// MaxRecentTurns 0 means no turn-count cap.
	MaxRecentTurns   int64
	TokenizerProfile string
	SystemPrompt     string
	Tools            []ToolSchema
	// PriorTurns are complete historical turns in chronological ascending order.
	PriorTurns  []Turn
	CurrentUser HistoryMessage
}

// AssemblyPlan is the body-free plan produced by the assembler.
type AssemblyPlan struct {
	Mode                       string
	EstimatorProfile           string
	EstimatorVersion           string
	HardInputCeilingTokens     int64
	EffectiveInputTokens       int64
	OutputReserveTokens        int64
	SafetyMarginTokens         int64
	ToolsOverheadTokens        int64
	SystemTokens               int64
	SelectedTurnCount          int
	OmittedTurnCount           int
	EstimatedTotalTokens       int64
	EffectiveOutputLimitTokens int64
	// IncludedMessages chronological dialogue messages (no SYSTEM).
	IncludedMessages []HistoryMessage
	// PromptMessages includes SYSTEM (if any) + selected history + current USER.
	PromptMessages []Message
}

// AssembleTokenWindow implements pure token_window selection.
// Mandatory context is SYSTEM + tools + current USER. History is taken as a
// continuous recent complete-turn suffix (newest-first fill, chronological emit).
func AssembleTokenWindow(input AssemblerInput) (AssemblyPlan, error) {
	if input.ModelContextWindowTokens <= 0 || input.OutputReserveTokens <= 0 ||
		input.OutputReserveTokens >= input.ModelContextWindowTokens {
		return AssemblyPlan{}, ErrInvalidAssemblerInput
	}
	if strings.TrimSpace(input.CurrentUser.ID) == "" {
		return AssemblyPlan{}, ErrInvalidAssemblerInput
	}
	if role := strings.ToUpper(strings.TrimSpace(input.CurrentUser.Role)); role != "" && role != "USER" {
		return AssemblyPlan{}, ErrInvalidAssemblerInput
	}
	profile := strings.TrimSpace(input.TokenizerProfile)
	if profile == "" {
		return AssemblyPlan{}, ErrInvalidAssemblerInput
	}
	est, err := NewEstimator(profile)
	if err != nil {
		return AssemblyPlan{}, err
	}

	hardCeiling := input.ModelContextWindowTokens - input.OutputReserveTokens - input.SafetyMarginTokens
	if hardCeiling <= 0 {
		return AssemblyPlan{}, ErrInvalidAssemblerInput
	}
	effectiveCeiling := hardCeiling
	if input.MaxInputTokens > 0 && input.MaxInputTokens < effectiveCeiling {
		effectiveCeiling = input.MaxInputTokens
	}

	mandatory, err := est.EstimateRequest(input.SystemPrompt, input.Tools, []Message{
		{Role: RoleUser, Content: input.CurrentUser.Content},
	})
	if err != nil {
		return AssemblyPlan{}, err
	}
	if mandatory.TotalTokens > effectiveCeiling {
		return AssemblyPlan{}, ErrRequiredInputTooLarge
	}

	remaining := effectiveCeiling - mandatory.TotalTokens
	selected := make([]Turn, 0)
	for i := len(input.PriorTurns) - 1; i >= 0; i-- {
		if input.MaxRecentTurns > 0 && int64(len(selected)) >= input.MaxRecentTurns {
			break
		}
		turn := input.PriorTurns[i]
		turnMsgs := make([]Message, 0, 1+len(turn.Assistants))
		for _, m := range turn.Messages() {
			turnMsgs = append(turnMsgs, Message{
				Role:    MessageRole(strings.ToLower(m.Role)),
				Content: m.Content,
			})
		}
		turnEst, err := est.EstimateRequest("", nil, turnMsgs)
		if err != nil {
			return AssemblyPlan{}, err
		}
		cost := turnEst.MessagesTokens
		if cost > remaining {
			break // continuous suffix only
		}
		selected = append([]Turn{turn}, selected...)
		remaining -= cost
	}

	included := make([]HistoryMessage, 0)
	promptMsgs := make([]Message, 0)
	if strings.TrimSpace(input.SystemPrompt) != "" {
		promptMsgs = append(promptMsgs, Message{Role: RoleSystem, Content: input.SystemPrompt})
	}
	for _, turn := range selected {
		for _, m := range turn.Messages() {
			included = append(included, m)
			promptMsgs = append(promptMsgs, Message{
				Role:    MessageRole(strings.ToLower(m.Role)),
				Content: m.Content,
			})
		}
	}
	included = append(included, input.CurrentUser)
	promptMsgs = append(promptMsgs, Message{Role: RoleUser, Content: input.CurrentUser.Content})

	dialogue := make([]Message, 0, len(promptMsgs))
	for _, m := range promptMsgs {
		if m.Role != RoleSystem {
			dialogue = append(dialogue, m)
		}
	}
	finalEst, err := est.EstimateRequest(input.SystemPrompt, input.Tools, dialogue)
	if err != nil {
		return AssemblyPlan{}, err
	}
	if finalEst.TotalTokens > effectiveCeiling {
		return AssemblyPlan{}, fmt.Errorf("%w: final estimate exceeds ceiling", ErrRequiredInputTooLarge)
	}

	mode := input.PolicyMode
	if mode == "" {
		mode = "token_window"
	}
	return AssemblyPlan{
		Mode:                       mode,
		EstimatorProfile:           finalEst.Profile,
		EstimatorVersion:           finalEst.EstimatorVersion,
		HardInputCeilingTokens:     hardCeiling,
		EffectiveInputTokens:       effectiveCeiling,
		OutputReserveTokens:        input.OutputReserveTokens,
		SafetyMarginTokens:         input.SafetyMarginTokens,
		ToolsOverheadTokens:        finalEst.ToolsTokens,
		SystemTokens:               finalEst.SystemTokens,
		SelectedTurnCount:          len(selected),
		OmittedTurnCount:           len(input.PriorTurns) - len(selected),
		EstimatedTotalTokens:       finalEst.TotalTokens,
		EffectiveOutputLimitTokens: input.OutputReserveTokens,
		IncludedMessages:           included,
		PromptMessages:             promptMsgs,
	}, nil
}
