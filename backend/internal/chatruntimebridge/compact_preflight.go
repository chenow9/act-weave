package chatruntimebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/contextsummary"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// compactAssemblyResult is body-free outcome before main model messages are built.
type compactAssemblyResult struct {
	// OptionalSummary is injected via assembler when non-empty (untrusted ASSISTANT).
	OptionalSummary string
	// UsedTokenWindowOnly is true when compact fell back or was not triggered.
	UsedTokenWindowOnly bool
	// CompactAttempted is true when occupancy >= 80% and lifecycle was considered.
	CompactAttempted bool
	// HardFail stops the run before main model (T8-A evidence failures).
	HardFail error
}

// CompactDependencies wires full compact orchestration (optional in unit tests).
// When nil or incomplete, triggered compact fails closed to token_window only after
// attempting evidence persistence when lifecycle is available.
type CompactDependencies struct {
	Summaries *contextsummary.Repository
	// PutObject stores encrypted permanent summary body (SummaryBodyStore).
	PutObject func(ctx context.Context, workspaceID, objectID string, body []byte) (sha string, length int64, err error)
	// OpenSummary decrypts READY summary body for injection / parent chain.
	OpenSummary func(ctx context.Context, workspaceID, objectID, actorType, actorID string) ([]byte, error)
	// Runs backs CompactStepLifecycle.
	Runs *execution.RunRepository
	// Protocol projects AAP context_compaction items/events.
	Protocol *protocolevent.ProtocolUnitOfWork
	// NewCompactModel builds a no-tools model from the run snapshot model.
	NewCompactModel func(ctx context.Context, run execution.AgentRun) (contextsummary.CompactModel, error)
	// IsShadow is process-level T6-A shadow mode (plan only; no LLM/step/item body).
	IsShadow bool
}

// maybeCompactForInitialRun runs T2-A preflight when session-context.v2 + compaction gate.
// Gate-off / v1 / legacy: no-op. Resume must never call this (caller responsibility).
func (b *Bridge) maybeCompactForInitialRun(
	ctx context.Context,
	job agentrunJob,
	run execution.AgentRun,
	policy sessioncontext.ResolvedSnapshot,
	system string,
	tools []contextwindow.ToolSchema,
	current contextwindow.HistoryMessage,
	priorTurns []contextwindow.Turn,
) compactAssemblyResult {
	if policy.SchemaVersion != sessioncontext.SnapshotSchemaV2 ||
		policy.Compaction == nil ||
		!policy.Sources.CompactionGateEnabled {
		return compactAssemblyResult{UsedTokenWindowOnly: true}
	}

	plan, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens:  policy.EffectiveMaxInputTokens,
		ModelContextWindowTokens: policy.ModelContextWindowTokens,
		OutputReserveTokens:      policy.OutputReserveTokens,
		SafetyMarginTokens:       policy.SafetyMarginTokens,
		MaxRecentTurns:           policy.MaxRecentTurns,
		TokenizerProfile:         policy.TokenizerProfile,
		SystemPrompt:             system,
		Tools:                    tools,
		UncoveredTurns:           priorTurns,
		CurrentUser:              current,
	})
	if err != nil {
		if errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
			return compactAssemblyResult{HardFail: execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)}
		}
		if errors.Is(err, contextwindow.ErrInsufficientEvictableTurns) {
			return b.compactFallback(ctx, job, run, policy, plan, execution.ErrCodeCompactionInsufficientEvictable, "plan", 0)
		}
		return compactAssemblyResult{UsedTokenWindowOnly: true}
	}
	if !plan.Triggered {
		return compactAssemblyResult{UsedTokenWindowOnly: true}
	}

	// Shadow mode: plan-only, no LLM/step/item/body (T6-A).
	if b.compact != nil && b.compact.IsShadow {
		return compactAssemblyResult{UsedTokenWindowOnly: true, CompactAttempted: true}
	}

	if b.compact == nil || b.compact.Summaries == nil || b.compact.PutObject == nil ||
		b.compact.Runs == nil || b.compact.NewCompactModel == nil {
		// Dependencies missing: still try to record evidence when possible, then token_window.
		return b.compactFallback(ctx, job, run, policy, plan, execution.ErrCodeCompactionModelFailed, "model", 0)
	}

	life := &execution.CompactStepLifecycle{Runs: b.compact.Runs}
	inputSum := execution.CompactStepInputSummary{
		TriggerBps:         sessioncontext.TriggerBps,
		TargetBps:          sessioncontext.TargetBps,
		TriggerInputTokens: plan.TriggerInputTokens,
		EffectiveCeiling:   plan.EffectiveInputCeiling,
		EstimatorVersion:   plan.EstimatorVersion,
		TemplateHash:       policy.Compaction.TemplateHash,
		ModelSnapshotHash:  execution.HashJSONObject(run.ModelSnapshot),
	}
	if len(plan.CoverageTurns) > 0 {
		end := plan.CoverageTurns[len(plan.CoverageTurns)-1]
		if n := len(end.Assistants); n > 0 {
			inputSum.PlannedCoverageEnd = end.Assistants[n-1].ID
		} else {
			inputSum.PlannedCoverageEnd = end.User.ID
		}
	}
	if _, err := life.EnsureStarted(ctx, job.WorkspaceID, job.RunID, inputSum); err != nil {
		// T8-A: evidence start failure hard-fails before main model.
		return compactAssemblyResult{
			CompactAttempted: true,
			HardFail:         fmt.Errorf("%s: %w", execution.ErrCodeCompactionEvidencePersistFailed, err),
		}
	}

	// Optional AAP item.started (metadata only).
	itemID := execution.DeterministicCompactItemID(job.RunID)
	if b.compact.Protocol != nil {
		proj := &protocolevent.ContextCompactionProjector{UoW: b.compact.Protocol}
		_ = proj.ProjectStarted(ctx, protocolevent.RunScope{
			WorkspaceID: job.WorkspaceID, AgentID: run.AgentID,
			ConversationID: job.SessionID, RunID: job.RunID,
		}, itemID, nextCompactOrdinal(ctx, b, run), protocolevent.ContextCompactionPayloadInput{
			Result: "building", TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			BeforeTokens: plan.TriggerInputTokens, EffectiveMaxInput: plan.EffectiveInputCeiling,
		})
	}

	// Parent READY LLM for rolling.
	policyFP := policyFingerprint(policy)
	var parent *contextsummary.Summary
	var parentBody string
	if latest, err := b.compact.Summaries.FindLatestReadyLLM(ctx, contextsummary.LatestReadyFilter{
		WorkspaceID:        job.WorkspaceID,
		SessionID:          job.SessionID,
		PolicyFingerprint:  policyFP,
		PromptTemplateHash: strings.ToLower(strings.TrimSpace(policy.Compaction.TemplateHash)),
	}); err == nil {
		parent = &latest
		if b.compact.OpenSummary != nil && latest.ContentObjectID != nil {
			// ActorID must be a UUID; reuse summary object id (same as Put creator).
			if body, oerr := b.compact.OpenSummary(ctx, job.WorkspaceID, *latest.ContentObjectID, "SYSTEM", *latest.ContentObjectID); oerr == nil {
				parentBody = string(body)
			}
		}
	}

	compactModel, err := b.compact.NewCompactModel(ctx, run)
	if err != nil || compactModel == nil {
		return b.finalizeCompactFallback(ctx, job, run, life, plan, itemID,
			execution.ErrCodeCompactionModelFailed, "model", 0, nil)
	}
	maxTok := int(policy.Compaction.MaxSummaryTokens)
	if maxTok <= 0 {
		maxTok = 2048
	}
	owner, _ := uuid.NewV7()
	summarizerSnap, _ := json.Marshal(map[string]any{
		"modelSnapshotHash": execution.HashJSONObject(run.ModelSnapshot),
		"templateHash":      policy.Compaction.TemplateHash,
		"templateVersion":   policy.Compaction.TemplateVersion,
		"policyFingerprint": policyFP,
	})
	coord := &contextsummary.Coordinator{
		Repo: b.compact.Summaries,
		Compactor: &contextsummary.LLMCompactor{
			Model:           compactModel,
			MaxTokens:       maxTok,
			TemplateVersion: policy.Compaction.TemplateVersion,
			TemplateHash:    policy.Compaction.TemplateHash,
		},
		PutObject:    b.compact.PutObject,
		TotalTimeout: timeDurationMs(policy.Compaction.TotalTimeoutMs),
		ClaimWait:    timeDurationMs(policy.Compaction.ClaimWaitMs),
	}
	result, err := coord.Run(ctx, contextsummary.CoordinatorInput{
		WorkspaceID: job.WorkspaceID, SessionID: job.SessionID,
		Snapshot: policy, Plan: plan, Parent: parent, ParentBody: parentBody,
		PolicyFingerprint:  policyFP,
		OwnerToken:         owner.String(),
		EstimatorVersion:   plan.EstimatorVersion,
		SummarizerSnapshot: summarizerSnap,
	})
	if err != nil || result.Status != "completed" || result.Summary == nil {
		code := result.FallbackCode
		if code == "" {
			code = execution.ErrCodeCompactionModelFailed
		}
		// Normalize non-§9.4 codes from coordinator.
		code = normalizeCompactCode(code)
		stage := result.FallbackStage
		if stage == "" {
			stage = execution.MapCompactError(code).Stage
		}
		return b.finalizeCompactFallback(ctx, job, run, life, plan, itemID, code, stage, result.Passes, result.Summary)
	}

	// Success: load body for injection; finalize step+item completed.
	body := ""
	if b.compact.OpenSummary != nil && result.Summary.ContentObjectID != nil {
		objID := *result.Summary.ContentObjectID
		if raw, oerr := b.compact.OpenSummary(ctx, job.WorkspaceID, objID, "SYSTEM", objID); oerr == nil {
			body = string(raw)
		}
	}
	if strings.TrimSpace(body) == "" {
		return b.finalizeCompactFallback(ctx, job, run, life, plan, itemID,
			execution.ErrCodeSummaryIntegrityFailed, "load", result.Passes, result.Summary)
	}

	// Re-verify ≤60% with summary injection.
	replan, rerr := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		PolicyMode:               policy.Mode,
		ModelContextWindowTokens: policy.ModelContextWindowTokens,
		OutputReserveTokens:      policy.OutputReserveTokens,
		SafetyMarginTokens:       policy.SafetyMarginTokens,
		MaxInputTokens:           policy.EffectiveMaxInputTokens,
		MaxRecentTurns:           policy.MaxRecentTurns,
		TokenizerProfile:         policy.TokenizerProfile,
		SystemPrompt:             system,
		Tools:                    tools,
		PriorTurns:               plan.RawSuffixTurns,
		CurrentUser:              current,
		OptionalSummary:          body,
	})
	if rerr != nil || !targetMetTokens(replan.EstimatedTotalTokens, plan.EffectiveInputCeiling) {
		return b.finalizeCompactFallback(ctx, job, run, life, plan, itemID,
			execution.ErrCodeCompactionTargetNotMet, "assemble", result.Passes, result.Summary)
	}

	out := execution.CompactStepOutputSummary{
		Result:             execution.CompactResultCompleted,
		BeforeTokens:       plan.TriggerInputTokens,
		AfterTokens:        replan.EstimatedTotalTokens,
		CoverageStartID:    result.CoverageStartID,
		CoverageEndID:      result.CoverageEndID,
		SourceMessageCount: result.SourceMessageCount,
		Passes:             result.Passes,
		Reused:             result.Reused,
		SummaryID:          result.Summary.ID,
	}
	if result.Summary.ContentSHA256 != nil {
		out.SummaryDigest = *result.Summary.ContentSHA256
	}
	if _, err := life.FinalizeCompleted(ctx, job.WorkspaceID, job.RunID, out); err != nil {
		return compactAssemblyResult{
			CompactAttempted: true,
			HardFail:         fmt.Errorf("%s: %w", execution.ErrCodeCompactionEvidencePersistFailed, err),
		}
	}
	if b.compact.Protocol != nil {
		include := sessioncontext.IncludeCompactionSummaryFromSnapshot(run.ContextPolicySnapshot)
		if policy.AAP != nil {
			include = policy.AAP.IncludeCompactionSummary
		}
		proj := &protocolevent.ContextCompactionProjector{UoW: b.compact.Protocol}
		if err := proj.ProjectTerminal(ctx, protocolevent.RunScope{
			WorkspaceID: job.WorkspaceID, AgentID: run.AgentID,
			ConversationID: job.SessionID, RunID: job.RunID,
		}, itemID, protocolevent.ContextCompactionPayloadInput{
			Result: "completed", TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			BeforeTokens: plan.TriggerInputTokens, AfterTokens: replan.EstimatedTotalTokens,
			EffectiveMaxInput: plan.EffectiveInputCeiling,
			CoverageStartID:   result.CoverageStartID, CoverageEndID: result.CoverageEndID,
			SourceMessageCount: result.SourceMessageCount, Passes: result.Passes, Reused: result.Reused,
			SummaryID: result.Summary.ID, SummaryDigest: out.SummaryDigest,
			IncludeSummary: include, InjectedSummary: body,
		}); err != nil {
			return compactAssemblyResult{
				CompactAttempted: true,
				HardFail:         fmt.Errorf("%s: %w", execution.ErrCodeCompactionEvidencePersistFailed, err),
			}
		}
	}
	return compactAssemblyResult{
		OptionalSummary:     body,
		UsedTokenWindowOnly: false,
		CompactAttempted:    true,
	}
}

func (b *Bridge) compactFallback(
	ctx context.Context,
	job agentrunJob,
	run execution.AgentRun,
	policy sessioncontext.ResolvedSnapshot,
	plan contextwindow.CompactionPlan,
	code, stage string,
	passes int,
) compactAssemblyResult {
	if b.compact != nil && b.compact.Runs != nil {
		life := &execution.CompactStepLifecycle{Runs: b.compact.Runs}
		itemID := execution.DeterministicCompactItemID(job.RunID)
		// Best-effort ensure so fallback has a step identity.
		_, _ = life.EnsureStarted(ctx, job.WorkspaceID, job.RunID, execution.CompactStepInputSummary{
			TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			TriggerInputTokens: plan.TriggerInputTokens, EffectiveCeiling: plan.EffectiveInputCeiling,
		})
		return b.finalizeCompactFallback(ctx, job, run, life, plan, itemID, code, stage, passes, nil)
	}
	return compactAssemblyResult{UsedTokenWindowOnly: true, CompactAttempted: true}
}

func (b *Bridge) finalizeCompactFallback(
	ctx context.Context,
	job agentrunJob,
	run execution.AgentRun,
	life *execution.CompactStepLifecycle,
	plan contextwindow.CompactionPlan,
	itemID, code, stage string,
	passes int,
	summary *contextsummary.Summary,
) compactAssemblyResult {
	mapped := execution.MapCompactError(code)
	out := execution.CompactStepOutputSummary{
		Result:        execution.CompactResultFallback,
		BeforeTokens:  plan.TriggerInputTokens,
		FallbackFrom:  "rolling_summary",
		FallbackTo:    "token_window",
		FallbackStage: stage,
		ErrorCode:     mapped.Code,
		Degraded:      true,
		Passes:        passes,
	}
	if summary != nil {
		out.SummaryID = summary.ID
		if summary.ContentSHA256 != nil {
			out.SummaryDigest = *summary.ContentSHA256
		}
	}
	if life != nil {
		if _, err := life.FinalizeFallback(ctx, job.WorkspaceID, job.RunID, out); err != nil {
			// Fallback evidence failure is still T8-A hard fail (product: AAP/audit must see attempt).
			return compactAssemblyResult{
				CompactAttempted: true,
				HardFail:         fmt.Errorf("%s: %w", execution.ErrCodeCompactionEvidencePersistFailed, err),
			}
		}
	}
	if b.compact != nil && b.compact.Protocol != nil && itemID != "" {
		proj := &protocolevent.ContextCompactionProjector{UoW: b.compact.Protocol}
		_ = proj.ProjectTerminal(ctx, protocolevent.RunScope{
			WorkspaceID: job.WorkspaceID, AgentID: run.AgentID,
			ConversationID: job.SessionID, RunID: job.RunID,
		}, itemID, protocolevent.ContextCompactionPayloadInput{
			Result: "fallback", TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			BeforeTokens: plan.TriggerInputTokens, EffectiveMaxInput: plan.EffectiveInputCeiling,
			FallbackFrom: "rolling_summary", FallbackTo: "token_window",
			FallbackStage: stage, ErrorCode: mapped.Code, IncludeSummary: false,
		})
	}
	return compactAssemblyResult{UsedTokenWindowOnly: true, CompactAttempted: true}
}

func targetMetTokens(final, ceiling int64) bool {
	ok, err := contextwindow.TargetMet(final, ceiling)
	return err == nil && ok
}

func normalizeCompactCode(code string) string {
	switch strings.TrimSpace(code) {
	case "CONTEXT_COMPACTION_TIMEOUT", "SUMMARY_LLM_FAILED", "CONTEXT_COMPACTION_LLM_FAILED":
		return execution.ErrCodeCompactionModelFailed
	case "SUMMARY_OBJECT_PUT_FAILED", "CONTEXT_COMPACTION_STORE_FAILED":
		return execution.ErrCodeCompactionObjectPutFailed
	case "CONTEXT_COMPACTION_MAX_PASSES", "CONTEXT_COMPACTION_TARGET_NOT_MET":
		return execution.ErrCodeCompactionTargetNotMet
	case "SUMMARY_NOT_READY":
		return execution.ErrCodeCompactionClaimBusy
	default:
		return code
	}
}

func policyFingerprint(policy sessioncontext.ResolvedSnapshot) string {
	raw, _ := json.Marshal(map[string]any{
		"mode": policy.Mode, "effective": policy.EffectiveMaxInputTokens,
		"reserve": policy.OutputReserveTokens, "margin": policy.SafetyMarginTokens,
		"maxRecent": policy.MaxRecentTurns, "tokenizer": policy.TokenizerProfile,
		"template": policy.Compaction,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func timeDurationMs(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// nextCompactOrdinal returns a stable early ordinal for pre-model compact items.
// Compact runs before main model messages, so ordinal 1 is correct for initial runs.
func nextCompactOrdinal(_ context.Context, _ *Bridge, _ execution.AgentRun) int {
	return 1
}

// agentrunJob is a minimal job surface for compact orchestration.
type agentrunJob struct {
	WorkspaceID   string
	SessionID     string
	RunID         string
	UserMessageID string
	ActorID       string
}

// einoCompactModel adapts BaseChatModel.Generate as a no-tools CompactModel.
type einoCompactModel struct {
	inner model.BaseChatModel
}

func (m *einoCompactModel) Generate(ctx context.Context, system, user string, _ float64, _ int) (string, error) {
	if m == nil || m.inner == nil {
		return "", contextsummary.ErrCompactorInvalid
	}
	out, err := m.inner.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", contextsummary.ErrCompactorModel
	}
	return out.Content, nil
}

// NewEinoCompactModel wraps a BaseChatModel for compact (tools must be absent).
func NewEinoCompactModel(m model.BaseChatModel) contextsummary.CompactModel {
	return &einoCompactModel{inner: m}
}

// NewCompactModelFromSnapshot builds a no-tools CompactModel from the run model snapshot.
// Used by application DI; tools/approval/streaming are never attached.
func NewCompactModelFromSnapshot(
	ctx context.Context,
	build ChatModelBuilder,
	run execution.AgentRun,
) (contextsummary.CompactModel, error) {
	if build == nil {
		return nil, errors.New("chat model builder is required for compact")
	}
	cfg, _, err := parseModelSnapshot(run.ModelSnapshot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, errors.New("model snapshot missing for compact")
	}
	if cfg.WorkspaceID == "" {
		cfg.WorkspaceID = run.WorkspaceID
	}
	if cfg.Status == "" {
		cfg.Status = modelconfig.StatusVerified
	}
	// Force no tools at construction: BuildChatModel returns BaseChatModel without tools.
	m, err := build(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewEinoCompactModel(m), nil
}
