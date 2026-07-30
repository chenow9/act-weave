package contextwindow

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Platform-frozen basis points (must match sessioncontext.TriggerBps/TargetBps).
const (
	PreflightTriggerBps = int64(8000) // 80.00%
	PreflightTargetBps  = int64(6000) // 60.00%
)

var (
	// ErrBudgetOverflow is returned when checked integer arithmetic would overflow.
	ErrBudgetOverflow = errors.New("CONTEXT_BUDGET_INVALID")
	// ErrInsufficientEvictableTurns means occupancy >= 80% but no complete old turn can be compacted.
	ErrInsufficientEvictableTurns = errors.New("CONTEXT_COMPACTION_INSUFFICIENT_EVICTABLE")
	// ErrPreflightInvalid is malformed planner input.
	ErrPreflightInvalid = errors.New("invalid context preflight input")
)

// PreflightInput is pure (no DB/provider) occupancy scan input.
// Trigger candidate = parent summary + all uncovered complete turns + mandatory.
// maxRecentTurns is NOT applied for trigger; it only constrains final raw suffix.
type PreflightInput struct {
	EffectiveMaxInputTokens int64
	// ModelContextWindowTokens / reserves only used when EffectiveMaxInputTokens==0
	// to compute hard ceiling (optional). Prefer EffectiveMaxInputTokens from snapshot.
	ModelContextWindowTokens int64
	OutputReserveTokens      int64
	SafetyMarginTokens       int64
	MaxRecentTurns           int64
	TokenizerProfile         string
	SystemPrompt             string
	Tools                    []ToolSchema
	// ParentSummary is optional untrusted summary text already covering a prefix.
	ParentSummary string
	// UncoveredTurns are complete turns after parent coverage, chronological.
	// Must not include current USER turn.
	UncoveredTurns []Turn
	CurrentUser    HistoryMessage
}

// CompactionPlan is a pure plan: whether to compact and how to split suffix/coverage.
type CompactionPlan struct {
	Triggered              bool
	TriggerInputTokens     int64
	EffectiveInputCeiling  int64
	TriggerBps             int64
	TargetBps              int64
	// OccupancyBps is floor(trigger*10000/ceiling); compare with TriggerBps.
	OccupancyBps int64
	// CoverageTurns are uncovered turns that should enter compact (continuous prefix).
	CoverageTurns []Turn
	// RawSuffixTurns remain as raw dialogue after compact (respect maxRecent + 60% reserve).
	RawSuffixTurns []Turn
	// EstimatedMandatoryTokens is SYSTEM+tools+current USER (+ framing).
	EstimatedMandatoryTokens int64
	// EstimatedSuffixBudget is tokens available for summary+raw suffix under target.
	EstimatedSuffixBudget int64
	EstimatorProfile      string
	EstimatorVersion      string
}

// PlanCompaction implements T2-A pure preflight: trigger on full uncovered history
// (no maxRecent for trigger); final raw suffix constrained by maxRecent and 60%.
func PlanCompaction(input PreflightInput) (CompactionPlan, error) {
	if strings.TrimSpace(input.TokenizerProfile) == "" {
		return CompactionPlan{}, ErrPreflightInvalid
	}
	if strings.TrimSpace(input.CurrentUser.ID) == "" {
		return CompactionPlan{}, ErrPreflightInvalid
	}
	ceiling := input.EffectiveMaxInputTokens
	if ceiling <= 0 {
		if input.ModelContextWindowTokens <= 0 || input.OutputReserveTokens <= 0 {
			return CompactionPlan{}, ErrPreflightInvalid
		}
		ceiling = input.ModelContextWindowTokens - input.OutputReserveTokens - input.SafetyMarginTokens
	}
	if ceiling <= 0 {
		return CompactionPlan{}, ErrPreflightInvalid
	}

	est, err := NewEstimator(input.TokenizerProfile)
	if err != nil {
		return CompactionPlan{}, err
	}

	// Mandatory = SYSTEM + tools + current USER (+ framing inside EstimateRequest).
	mandatory, err := est.EstimateRequest(input.SystemPrompt, input.Tools, []Message{
		{Role: RoleUser, Content: input.CurrentUser.Content},
	})
	if err != nil {
		return CompactionPlan{}, err
	}
	if mandatory.TotalTokens > ceiling {
		return CompactionPlan{}, ErrRequiredInputTooLarge
	}

	// Build trigger dialogue: optional parent summary as ASSISTANT + all uncovered turns + current.
	var dialogue []Message
	parent := strings.TrimSpace(input.ParentSummary)
	if parent != "" {
		dialogue = append(dialogue, Message{Role: RoleAssistant, Content: UntrustedSummaryPrefix + parent})
	}
	for _, turn := range input.UncoveredTurns {
		dialogue = append(dialogue, messagesFromTurn(turn)...)
	}
	dialogue = append(dialogue, Message{Role: RoleUser, Content: input.CurrentUser.Content})

	triggerEst, err := est.EstimateRequest(input.SystemPrompt, input.Tools, dialogue)
	if err != nil {
		return CompactionPlan{}, err
	}
	occupancyBps, err := mulDivBps(triggerEst.TotalTokens, ceiling)
	if err != nil {
		return CompactionPlan{}, err
	}

	plan := CompactionPlan{
		Triggered:                occupancyBps >= PreflightTriggerBps,
		TriggerInputTokens:       triggerEst.TotalTokens,
		EffectiveInputCeiling:    ceiling,
		TriggerBps:               PreflightTriggerBps,
		TargetBps:                PreflightTargetBps,
		OccupancyBps:             occupancyBps,
		EstimatedMandatoryTokens: mandatory.TotalTokens,
		EstimatorProfile:         triggerEst.Profile,
		EstimatorVersion:         triggerEst.EstimatorVersion,
	}
	if !plan.Triggered {
		// No compact lifecycle: raw path uses existing token window (caller applies maxRecent).
		return plan, nil
	}
	if len(input.UncoveredTurns) == 0 {
		return CompactionPlan{}, ErrInsufficientEvictableTurns
	}

	// Target: final <= 60% of ceiling.
	targetCeiling, err := mulDivFloor(ceiling, PreflightTargetBps, 10000)
	if err != nil {
		return CompactionPlan{}, err
	}
	// Reserve room for summary roughly by remaining after mandatory under target.
	suffixBudget := targetCeiling - mandatory.TotalTokens
	if suffixBudget < 0 {
		// Mandatory alone already above 60% — cannot complete compact success.
		// Still plan: cover all uncovered, empty raw suffix; caller may multi-pass or fallback.
		suffixBudget = 0
	}
	plan.EstimatedSuffixBudget = suffixBudget

	// Select raw suffix: newest complete turns under maxRecent and token budget.
	// Remaining older uncovered turns become coverage for compact.
	maxRecent := input.MaxRecentTurns
	suffix := selectRawSuffix(input.UncoveredTurns, maxRecent, suffixBudget, est, input.SystemPrompt, input.Tools, input.CurrentUser)
	coverEnd := len(input.UncoveredTurns) - len(suffix)
	if coverEnd < 0 {
		coverEnd = 0
	}
	if coverEnd == 0 {
		// Need at least one turn to compact when triggered; if all kept as suffix, still
		// insufficient eviction relative to trigger semantics → force oldest one into coverage.
		if len(input.UncoveredTurns) == 0 {
			return CompactionPlan{}, ErrInsufficientEvictableTurns
		}
		coverEnd = 1
		suffix = input.UncoveredTurns[1:]
		if maxRecent > 0 && int64(len(suffix)) > maxRecent {
			suffix = suffix[len(suffix)-int(maxRecent):]
		}
	}
	plan.CoverageTurns = append([]Turn(nil), input.UncoveredTurns[:coverEnd]...)
	plan.RawSuffixTurns = append([]Turn(nil), suffix...)
	return plan, nil
}

// TargetMet reports whether final tokens are within 60.00% inclusive.
func TargetMet(finalTokens, effectiveCeiling int64) (bool, error) {
	bps, err := mulDivBps(finalTokens, effectiveCeiling)
	if err != nil {
		return false, err
	}
	return bps <= PreflightTargetBps, nil
}

// Triggered reports whether occupancy is at or above 80.00%.
func Triggered(triggerTokens, effectiveCeiling int64) (bool, error) {
	bps, err := mulDivBps(triggerTokens, effectiveCeiling)
	if err != nil {
		return false, err
	}
	return bps >= PreflightTriggerBps, nil
}

func messagesFromTurn(turn Turn) []Message {
	out := make([]Message, 0, 1+len(turn.Assistants))
	out = append(out, Message{Role: RoleUser, Content: turn.User.Content})
	for _, a := range turn.Assistants {
		out = append(out, Message{Role: RoleAssistant, Content: a.Content})
	}
	return out
}

func selectRawSuffix(
	turns []Turn,
	maxRecent, suffixBudget int64,
	est *Estimator,
	system string,
	tools []ToolSchema,
	current HistoryMessage,
) []Turn {
	if len(turns) == 0 {
		return nil
	}
	// Newest-first fill under maxRecent and token budget (mandatory already excluded).
	var picked []Turn
	for i := len(turns) - 1; i >= 0; i-- {
		if maxRecent > 0 && int64(len(picked)) >= maxRecent {
			break
		}
		candidate := append([]Turn{turns[i]}, picked...)
		// Estimate suffix + current only (summary separate).
		var dialogue []Message
		for _, t := range candidate {
			dialogue = append(dialogue, messagesFromTurn(t)...)
		}
		dialogue = append(dialogue, Message{Role: RoleUser, Content: current.Content})
		e, err := est.EstimateRequest(system, tools, dialogue)
		if err != nil {
			break
		}
		// Compare suffix-only: total - system - tools framing is hard; use total vs mandatory+budget.
		// Approximated: allow if e.TotalTokens - mandatory-ish <= suffixBudget + mandatory.
		// Simpler: if e.TotalTokens <= mandatoryTokens + suffixBudget where mandatory from system+tools+current.
		// We recompute mandatory loosely: if over budget with this candidate, stop.
		mandatory, merr := est.EstimateRequest(system, tools, []Message{
			{Role: RoleUser, Content: current.Content},
		})
		if merr != nil {
			break
		}
		if suffixBudget >= 0 && e.TotalTokens > mandatory.TotalTokens+suffixBudget {
			if len(picked) == 0 {
				// Always keep at least zero suffix rather than oversize; do not take this turn.
				break
			}
			break
		}
		picked = candidate
	}
	return picked
}

// mulDivBps computes floor(tokens * 10000 / ceiling) with overflow checks.
func mulDivBps(tokens, ceiling int64) (int64, error) {
	if ceiling <= 0 || tokens < 0 {
		return 0, ErrBudgetOverflow
	}
	if tokens == 0 {
		return 0, nil
	}
	if tokens > math.MaxInt64/10000 {
		// Reduce first: tokens/ceiling * 10000 with care.
		q := tokens / ceiling
		if q > math.MaxInt64/10000 {
			return 0, ErrBudgetOverflow
		}
		r := tokens % ceiling
		bps := q*10000 + (r*10000)/ceiling
		return bps, nil
	}
	return (tokens * 10000) / ceiling, nil
}

func mulDivFloor(a, num, den int64) (int64, error) {
	if den <= 0 || a < 0 || num < 0 {
		return 0, ErrBudgetOverflow
	}
	if a > 0 && num > math.MaxInt64/a {
		return 0, fmt.Errorf("%w: mul", ErrBudgetOverflow)
	}
	return (a * num) / den, nil
}
