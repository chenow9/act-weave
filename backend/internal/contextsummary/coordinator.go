package contextsummary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/sessioncontext"
)

// CoordinatorResult is body-free for lifecycle/bridge consumers.
type CoordinatorResult struct {
	Status           string // completed | fallback | failed
	Summary          *Summary
	Reused           bool
	Passes           int
	FallbackCode     string
	FallbackStage    string
	BeforeTokens     int64
	AfterTokens      int64
	CoverageStartID  string
	CoverageEndID    string
	SourceMessageCount int
}

// CoordinatorInput wires planner + LLM + claim/store for one run compact attempt.
type CoordinatorInput struct {
	WorkspaceID string
	SessionID   string
	// Snapshot compaction knobs (from session-context.v2).
	Snapshot sessioncontext.ResolvedSnapshot
	// Precomputed plan from PlanCompaction (coverage turns to compact).
	Plan contextwindow.CompactionPlan
	// Parent READY summary if any.
	Parent *Summary
	ParentBody string
	// Policy / owner identity for claim.
	PolicyFingerprint string
	OwnerToken        string
	// Estimator identity for claim metadata.
	EstimatorVersion string
	// Summarizer snapshot JSON for claim conflict checks.
	SummarizerSnapshot []byte
}

// Coordinator owns multi-pass claim/LLM/store until target or bounded failure.
type Coordinator struct {
	Repo      *Repository
	Compactor *LLMCompactor
	// PutObject stores body; typically SummaryBodyStore.PutOrVerify wrapper.
	PutObject func(ctx context.Context, workspaceID, objectID string, body []byte) (sha string, length int64, err error)
	// Now for tests.
	Now func() time.Time
	// ClaimWait is max wait when another owner holds lease (default 1s from snapshot).
	ClaimWait time.Duration
	// TotalTimeout caps the whole compact attempt (default 45s).
	TotalTimeout time.Duration
}

// Run executes bounded multi-pass compact. Does not hold DB locks during provider calls.
func (c *Coordinator) Run(ctx context.Context, in CoordinatorInput) (CoordinatorResult, error) {
	if c == nil || c.Repo == nil || c.Compactor == nil {
		return CoordinatorResult{Status: "failed", FallbackCode: "COORDINATOR_INVALID"}, ErrInvalid
	}
	if !in.Plan.Triggered || len(in.Plan.CoverageTurns) == 0 {
		return CoordinatorResult{Status: "failed", FallbackCode: "NO_PLAN"}, ErrInvalid
	}
	deadline := c.timeouts(in)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	before := in.Plan.TriggerInputTokens
	parent := in.Parent
	parentBody := in.ParentBody
	passes := 0
	maxPasses := int64(2)
	if in.Snapshot.Compaction != nil && in.Snapshot.Compaction.MaxGenerationPasses > 0 {
		maxPasses = in.Snapshot.Compaction.MaxGenerationPasses
	}

	// Single pass over planned coverage for IC-07 baseline; multi-pass extends
	// by re-planning when target not met (bounded by maxPasses).
	coverage := in.Plan.CoverageTurns
	for passes < int(maxPasses) {
		passes++
		if err := ctx.Err(); err != nil {
			return CoordinatorResult{
				Status: "fallback", FallbackCode: "CONTEXT_COMPACTION_TIMEOUT",
				FallbackStage: "pass", Passes: passes, BeforeTokens: before,
			}, nil
		}
		startID := coverage[0].User.ID
		endTurn := coverage[len(coverage)-1]
		endID := endTurn.User.ID
		if n := len(endTurn.Assistants); n > 0 {
			endID = endTurn.Assistants[n-1].ID
		}
		genIn := GenerateInput{
			WorkspaceID:            in.WorkspaceID,
			SessionID:              in.SessionID,
			CoverageStartMessageID: firstCoverageStart(parent, startID),
			CoverageEndMessageID:   endID,
			Turns:                  coverage,
			Parent:                 parent,
			ParentBody:             parentBody,
			PolicyFingerprint:      in.PolicyFingerprint,
			OwnerToken:             in.OwnerToken,
			GenerationMethod:       GenerationLLM,
			SummarizerSnapshot:     in.SummarizerSnapshot,
			EstimatedInputTokens:   before,
			EstimatorVersion:       in.EstimatorVersion,
		}
		// Generator owns ClaimOrGet / LLM / Put / MarkReady without holding tx during LLM.
		g := &Generator{
			repo:      c.Repo,
			Compactor: c.Compactor,
			PutObject: c.PutObject,
		}
		res, err := g.Generate(ctx, genIn)
		if err != nil || res.FallbackOnly {
			code := "SUMMARY_LLM_FAILED"
			if err != nil && strings.Contains(err.Error(), "STORE") {
				code = "SUMMARY_OBJECT_PUT_FAILED"
			}
			return CoordinatorResult{
				Status: "fallback", FallbackCode: code, FallbackStage: "generate",
				Passes: passes, BeforeTokens: before, Summary: &res.Summary,
			}, nil
		}
		if res.Summary.Status == StatusReady {
			// Re-estimate target using planner TargetMet with rough after tokens:
			// use plan target budget as stand-in when full re-estimate not available.
			after := in.Plan.EstimatedMandatoryTokens + int64(len(res.BodySHA256)) // body-free placeholder
			// Prefer summary estimated tokens when set.
			if res.Summary.EstimatedOutputTokens > 0 {
				after = in.Plan.EstimatedMandatoryTokens + res.Summary.EstimatedOutputTokens
			}
			met, metErr := contextwindow.TargetMet(after, in.Plan.EffectiveInputCeiling)
			if metErr == nil && met {
				return CoordinatorResult{
					Status: "completed", Summary: &res.Summary, Reused: !res.Claimed,
					Passes: passes, BeforeTokens: before, AfterTokens: after,
					CoverageStartID: ptrStr(res.Summary.CoverageStartMessageID),
					CoverageEndID:   ptrStr(res.Summary.CoverageEndMessageID),
					SourceMessageCount: res.Summary.SourceMessageCount,
				}, nil
			}
			// Not met: promote parent and continue if turns remain beyond plan suffix.
			parent = &res.Summary
			parentBody = "" // body not in memory for next pass without Open
			if passes >= int(maxPasses) {
				return CoordinatorResult{
					Status: "fallback", FallbackCode: "CONTEXT_COMPACTION_TARGET_NOT_MET",
					FallbackStage: "target", Passes: passes, BeforeTokens: before,
					AfterTokens: after, Summary: &res.Summary,
				}, nil
			}
			// No additional uncovered turns in this simplified coordinator — stop.
			return CoordinatorResult{
				Status: "fallback", FallbackCode: "CONTEXT_COMPACTION_TARGET_NOT_MET",
				FallbackStage: "target", Passes: passes, BeforeTokens: before,
				AfterTokens: after, Summary: &res.Summary,
			}, nil
		}
		return CoordinatorResult{
			Status: "fallback", FallbackCode: "SUMMARY_NOT_READY", FallbackStage: "generate",
			Passes: passes, BeforeTokens: before,
		}, nil
	}
	return CoordinatorResult{
		Status: "fallback", FallbackCode: "CONTEXT_COMPACTION_MAX_PASSES",
		FallbackStage: "pass", Passes: passes, BeforeTokens: before,
	}, nil
}

func (c *Coordinator) timeouts(in CoordinatorInput) time.Time {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	total := c.TotalTimeout
	if total <= 0 && in.Snapshot.Compaction != nil && in.Snapshot.Compaction.TotalTimeoutMs > 0 {
		total = time.Duration(in.Snapshot.Compaction.TotalTimeoutMs) * time.Millisecond
	}
	if total <= 0 {
		total = 45 * time.Second
	}
	return now.Add(total)
}

func firstCoverageStart(parent *Summary, fallback string) string {
	if parent != nil && parent.CoverageStartMessageID != nil {
		return *parent.CoverageStartMessageID
	}
	return fallback
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Ensure errors package used when wrapping later.
var _ = fmt.Errorf
var _ = errors.New
