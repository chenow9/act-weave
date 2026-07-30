package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	// StepTypeContextCompaction is the permanent per-run compact lifecycle fact (T3-A).
	StepTypeContextCompaction = "CONTEXT_COMPACTION"

	// Deterministic UUIDv5 domains for compact step/item identities (tech design §10.2).
	compactStepDomain = "context-compaction.v1"
	compactItemDomain = "context-compaction-item.v1"

	// Compact step terminal result strings stored in output_summary.
	// fallback remains step status FAILED permanently (D6-A).
	CompactResultCompleted = "completed"
	CompactResultFallback  = "fallback"
	CompactResultFailed    = "failed"

	stepStatusRunning   = "RUNNING"
	stepStatusSucceeded = "SUCCEEDED"
	stepStatusFailed    = "FAILED"
)

// Compact error codes and stages (tech design §9.4).
const (
	ErrCodeCompactionInsufficientEvictable = "CONTEXT_COMPACTION_INSUFFICIENT_EVICTABLE_TURNS"
	ErrCodeCompactionClaimBusy             = "CONTEXT_COMPACTION_CLAIM_BUSY"
	ErrCodeCompactionModelTimeout          = "CONTEXT_COMPACTION_MODEL_TIMEOUT"
	ErrCodeCompactionModelFailed           = "CONTEXT_COMPACTION_MODEL_FAILED"
	ErrCodeCompactionOutputInvalid         = "CONTEXT_COMPACTION_OUTPUT_INVALID"
	ErrCodeCompactionObjectPutFailed       = "CONTEXT_COMPACTION_OBJECT_PUT_FAILED"
	ErrCodeCompactionTargetNotMet          = "CONTEXT_COMPACTION_TARGET_NOT_MET"
	ErrCodeCompactionEvidencePersistFailed = "CONTEXT_COMPACTION_EVIDENCE_PERSIST_FAILED"
	ErrCodeSummaryScopeMismatch            = "CONTEXT_SUMMARY_SCOPE_MISMATCH"
	ErrCodeSummaryIntegrityFailed          = "CONTEXT_SUMMARY_INTEGRITY_FAILED"

	// CompactStage enumerates protocol/audit/log stage values only.
	CompactStageSnapshot  = "snapshot"
	CompactStagePreflight = "preflight"
	CompactStageLoad      = "load"
	CompactStagePlan      = "plan"
	CompactStageClaim     = "claim"
	CompactStageModel     = "model"
	CompactStageValidate  = "validate"
	CompactStageStore     = "store"
	CompactStageAssemble  = "assemble"
	CompactStageProject   = "project"
)

// CompactError is a stable, body-free compact failure for AAP/audit/logs.
type CompactError struct {
	Code      string
	Stage     string
	Retryable bool
	// UserMessage is safe for clients (no provider body/secrets).
	UserMessage string
}

func (e CompactError) Error() string {
	if e.Code == "" {
		return "context compaction error"
	}
	return e.Code
}

// MapCompactError returns §9.4 stable code/stage/safe message.
func MapCompactError(code string) CompactError {
	code = strings.TrimSpace(code)
	switch code {
	case ErrCodeContextRequiredInputTooLarge:
		return CompactError{Code: code, Stage: CompactStagePreflight, UserMessage: "Required context exceeds the model input limit."}
	case ErrCodeCompactionInsufficientEvictable:
		return CompactError{Code: code, Stage: CompactStagePlan, Retryable: false,
			UserMessage: "Context compaction could not free enough history; using a bounded window."}
	case ErrCodeCompactionClaimBusy:
		return CompactError{Code: code, Stage: CompactStageClaim, Retryable: true,
			UserMessage: "Context compaction is busy; falling back to a bounded window."}
	case ErrCodeCompactionModelTimeout:
		return CompactError{Code: code, Stage: CompactStageModel, Retryable: true,
			UserMessage: "Context compaction timed out; using a bounded window."}
	case ErrCodeCompactionModelFailed:
		return CompactError{Code: code, Stage: CompactStageModel, Retryable: true,
			UserMessage: "Context compaction failed; using a bounded window."}
	case ErrCodeCompactionOutputInvalid:
		return CompactError{Code: code, Stage: CompactStageValidate, Retryable: false,
			UserMessage: "Context compaction output was invalid; using a bounded window."}
	case ErrCodeCompactionObjectPutFailed:
		return CompactError{Code: code, Stage: CompactStageStore, Retryable: true,
			UserMessage: "Context compaction could not store the summary; using a bounded window."}
	case ErrCodeCompactionTargetNotMet:
		return CompactError{Code: code, Stage: CompactStageAssemble, Retryable: false,
			UserMessage: "Context compaction did not reach the target size; using a bounded window."}
	case ErrCodeSummaryScopeMismatch, ErrCodeSummaryIntegrityFailed:
		return CompactError{Code: code, Stage: CompactStageLoad, Retryable: false,
			UserMessage: "Context summary could not be verified; using a bounded window."}
	case ErrCodeCompactionEvidencePersistFailed:
		return CompactError{Code: code, Stage: CompactStageProject, Retryable: false,
			UserMessage: "Context compaction evidence could not be recorded; the run was stopped."}
	case ErrCodeContextSnapshotUnsupported:
		return CompactError{Code: code, Stage: CompactStageSnapshot, Retryable: false,
			UserMessage: "This run's context snapshot is not supported."}
	default:
		return CompactError{Code: code, Stage: CompactStageProject, Retryable: false,
			UserMessage: "Context compaction failed."}
	}
}

// CompactStepInputSummary is body-free step input (no messages/summary/secrets).
type CompactStepInputSummary struct {
	TriggerBps         int64  `json:"triggerBps"`
	TargetBps          int64  `json:"targetBps"`
	TriggerInputTokens int64  `json:"triggerInputTokens"`
	EffectiveCeiling   int64  `json:"effectiveMaxInputTokens"`
	PlannedCoverageEnd string `json:"plannedCoverageEndMessageId,omitempty"`
	EstimatorVersion   string `json:"estimatorVersion,omitempty"`
	TemplateHash       string `json:"templateHash,omitempty"`
	ModelSnapshotHash  string `json:"modelSnapshotHash,omitempty"`
}

// CompactStepOutputSummary is body-free terminal output.
type CompactStepOutputSummary struct {
	Result             string `json:"result"` // completed|fallback|failed
	BeforeTokens       int64  `json:"beforeTokens,omitempty"`
	AfterTokens        int64  `json:"afterTokens,omitempty"`
	CoverageStartID    string `json:"coverageStartMessageId,omitempty"`
	CoverageEndID      string `json:"coverageEndMessageId,omitempty"`
	SourceMessageCount int    `json:"sourceMessageCount,omitempty"`
	Passes             int    `json:"passes,omitempty"`
	Reused             bool   `json:"reused,omitempty"`
	SummaryID          string `json:"summaryId,omitempty"`
	SummaryDigest      string `json:"summaryDigest,omitempty"`
	FallbackFrom       string `json:"fallbackFrom,omitempty"`
	FallbackTo         string `json:"fallbackTo,omitempty"`
	FallbackStage      string `json:"fallbackStage,omitempty"`
	ErrorCode          string `json:"errorCode,omitempty"`
	Degraded           bool   `json:"degraded,omitempty"`
}

// DeterministicCompactStepID returns UUIDv5 for run-scoped compact step.
func DeterministicCompactStepID(runID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(compactStepDomain+"|"+strings.TrimSpace(runID))).String()
}

// DeterministicCompactItemID returns UUIDv5 for run-scoped compact AAP item.
func DeterministicCompactItemID(runID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(compactItemDomain+"|"+strings.TrimSpace(runID))).String()
}

// CompactStepLifecycle writes/reads CONTEXT_COMPACTION steps via the run repository.
type CompactStepLifecycle struct {
	Runs *RunRepository
}

// FindCompactStep returns the unique compact step for a run if present.
func (l *CompactStepLifecycle) FindCompactStep(ctx context.Context, workspaceID, runID string) (AgentRunStep, error) {
	if l == nil || l.Runs == nil {
		return AgentRunStep{}, errors.New("compact step lifecycle repository required")
	}
	steps, err := l.Runs.ListAgentRunSteps(ctx, workspaceID, runID)
	if err != nil {
		return AgentRunStep{}, err
	}
	wantID := DeterministicCompactStepID(runID)
	for _, s := range steps {
		if s.ID == wantID || s.StepType == StepTypeContextCompaction {
			return s, nil
		}
	}
	return AgentRunStep{}, ErrRunNotFound
}

// EnsureStarted creates or reuses the unique RUNNING compact step for a run.
// Evidence start failure should hard-fail before main model (T8-A).
func (l *CompactStepLifecycle) EnsureStarted(
	ctx context.Context,
	workspaceID, runID string,
	input CompactStepInputSummary,
) (AgentRunStep, error) {
	if l == nil || l.Runs == nil {
		return AgentRunStep{}, errors.New("compact step lifecycle repository required")
	}
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if workspaceID == "" || runID == "" {
		return AgentRunStep{}, ErrRunInvalid
	}
	if existing, err := l.FindCompactStep(ctx, workspaceID, runID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrRunNotFound) {
		return AgentRunStep{}, err
	}
	stepID := DeterministicCompactStepID(runID)
	inJSON, err := json.Marshal(input)
	if err != nil {
		return AgentRunStep{}, err
	}
	if len(inJSON) > 16<<10 {
		return AgentRunStep{}, errors.New("compact input summary too large")
	}
	if containsForbiddenCompactBody(inJSON) {
		return AgentRunStep{}, errors.New("compact input summary must be body-free")
	}
	step, err := l.Runs.AppendAgentRunStep(ctx, AppendAgentRunStepInput{
		ID:           stepID,
		WorkspaceID:  workspaceID,
		RunID:        runID,
		StepType:     StepTypeContextCompaction,
		InputSummary: inJSON,
	})
	if err != nil {
		if existing, getErr := l.FindCompactStep(ctx, workspaceID, runID); getErr == nil {
			return existing, nil
		}
		return AgentRunStep{}, fmt.Errorf("ensure compact step: %w", err)
	}
	return step, nil
}

// FinalizeCompleted marks SUCCEEDED with body-free completed output.
func (l *CompactStepLifecycle) FinalizeCompleted(
	ctx context.Context,
	workspaceID, runID string,
	out CompactStepOutputSummary,
) (AgentRunStep, error) {
	out.Result = CompactResultCompleted
	out.Degraded = false
	out.FallbackFrom, out.FallbackTo, out.FallbackStage, out.ErrorCode = "", "", "", ""
	return l.finalize(ctx, workspaceID, runID, stepStatusSucceeded, out, "")
}

// FinalizeFallback marks FAILED permanently with rolling_summary → token_window.
func (l *CompactStepLifecycle) FinalizeFallback(
	ctx context.Context,
	workspaceID, runID string,
	out CompactStepOutputSummary,
) (AgentRunStep, error) {
	out.Result = CompactResultFallback
	out.Degraded = true
	if out.FallbackFrom == "" {
		out.FallbackFrom = "rolling_summary"
	}
	if out.FallbackTo == "" {
		out.FallbackTo = "token_window"
	}
	code := out.ErrorCode
	if code == "" {
		code = ErrCodeCompactionModelFailed
	}
	mapped := MapCompactError(code)
	out.ErrorCode = mapped.Code
	if out.FallbackStage == "" {
		out.FallbackStage = mapped.Stage
	}
	return l.finalize(ctx, workspaceID, runID, stepStatusFailed, out, code)
}

// FinalizeFailed marks FAILED without successful compact semantics.
func (l *CompactStepLifecycle) FinalizeFailed(
	ctx context.Context,
	workspaceID, runID string,
	out CompactStepOutputSummary,
) (AgentRunStep, error) {
	out.Result = CompactResultFailed
	out.Degraded = true
	code := out.ErrorCode
	if code == "" {
		code = ErrCodeCompactionEvidencePersistFailed
	}
	mapped := MapCompactError(code)
	out.ErrorCode = mapped.Code
	if out.FallbackStage == "" {
		out.FallbackStage = mapped.Stage
	}
	return l.finalize(ctx, workspaceID, runID, stepStatusFailed, out, code)
}

func (l *CompactStepLifecycle) finalize(
	ctx context.Context,
	workspaceID, runID, newStatus string,
	out CompactStepOutputSummary,
	errorCode string,
) (AgentRunStep, error) {
	if l == nil || l.Runs == nil {
		return AgentRunStep{}, errors.New("compact step lifecycle repository required")
	}
	existing, err := l.FindCompactStep(ctx, workspaceID, runID)
	if err != nil {
		return AgentRunStep{}, err
	}
	// Terminal steps are immutable: do not rewrite completed/fallback after success of main run.
	if existing.Status == stepStatusSucceeded || existing.Status == stepStatusFailed {
		return existing, nil
	}
	outJSON, err := json.Marshal(out)
	if err != nil {
		return AgentRunStep{}, err
	}
	if containsForbiddenCompactBody(outJSON) {
		return AgentRunStep{}, errors.New("compact output summary must be body-free")
	}
	updated, err := l.Runs.TransitionAgentRunStep(ctx, workspaceID, existing.ID, StepTransition{
		ExpectedStatus: stepStatusRunning,
		NewStatus:      newStatus,
		OutputSummary:  outJSON,
		ErrorCode:      errorCode,
	})
	if err != nil {
		if cur, getErr := l.FindCompactStep(ctx, workspaceID, runID); getErr == nil &&
			(cur.Status == stepStatusSucceeded || cur.Status == stepStatusFailed) {
			return cur, nil
		}
		return AgentRunStep{}, err
	}
	return updated, nil
}

func containsForbiddenCompactBody(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, `"summary":"`) || strings.Contains(s, `"body":"`) ||
		strings.Contains(s, "sk-") || strings.Contains(s, "BEGIN PRIVATE")
}

// HashPrefix returns first 16 hex chars of sha256 for logs (no body).
func HashPrefix(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		return sum[:16]
	}
	return sum
}
