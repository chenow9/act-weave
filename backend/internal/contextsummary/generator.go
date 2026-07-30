// Package contextsummary owns rolling summary claim/storage and LLM compact (ZKL-81).
package contextsummary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"actweave/backend/internal/contextwindow"
)

// Generator produces restricted rolling summaries (no tools, no SYSTEM elevation).
// ZKL-81: success path is LLMCompactor only. Local extractive is never READY success.
type Generator struct {
	repo *Repository
	// Compactor is required for READY success. When nil, claim fails with stable code.
	Compactor *LLMCompactor
	// PutObject stores encrypted permanent summary body; when nil, MarkReady is skipped.
	PutObject func(ctx context.Context, workspaceID, objectID string, body []byte) (sha256Hex string, length int64, err error)
	TemplateVersion string
	TemplateHash    string
}

// GenerateInput is continuous prefix coverage only (never includes current USER).
type GenerateInput struct {
	WorkspaceID            string
	SessionID              string
	CoverageStartMessageID string
	CoverageEndMessageID   string
	Turns                  []contextwindow.Turn
	Parent                 *Summary
	PolicyFingerprint      string
	OwnerToken             string
	// GenerationMethod must be LLM for product path; legacy not used for new READY.
	GenerationMethod string
	// Parent summary body text for rolling compact (optional).
	ParentBody string
	// SummarizerSnapshot is required for LLM claims.
	SummarizerSnapshot []byte
	EstimatedInputTokens  int64
	EstimatedOutputTokens int64
	EstimatorVersion      string
}

// GenerateResult is body-free for logs; body is only in permanent object when stored.
type GenerateResult struct {
	Summary      Summary
	Claimed      bool
	FallbackOnly bool
	BodySHA256   string
}

// Generate builds or reuses a READY LLM summary for the idempotency key.
// Extractive concatenation is never used as a successful READY path (T7-A).
func (g *Generator) Generate(ctx context.Context, in GenerateInput) (GenerateResult, error) {
	if g == nil || g.repo == nil {
		return GenerateResult{}, ErrInvalid
	}
	if len(in.Turns) == 0 || strings.TrimSpace(in.CoverageEndMessageID) == "" {
		return GenerateResult{}, ErrInvalid
	}
	method := strings.TrimSpace(in.GenerationMethod)
	if method == "" {
		method = GenerationLLM
	}
	if method != GenerationLLM {
		// Product path rejects legacy extractive as success.
		return GenerateResult{FallbackOnly: true}, ErrInvalid
	}
	if g.Compactor == nil || g.Compactor.Model == nil {
		return GenerateResult{FallbackOnly: true}, ErrCompactorInvalid
	}

	sourceDigest := SourceChainDigest("", turnsToTuples(in.Turns))
	if in.Parent != nil {
		sourceDigest = SourceChainDigest(in.Parent.SourceDigest, turnsToTuples(in.Turns))
	}
	tmplVer := g.TemplateVersion
	if tmplVer == "" {
		tmplVer = CompactionTemplateVersion
	}
	tmplHash := g.TemplateHash
	if tmplHash == "" {
		tmplHash = CompactTemplateHash()
	}
	count := countTurnMessages(in.Turns)
	if in.Parent != nil {
		count = CumulativeSourceMessageCount(in.Parent.SourceMessageCount, count)
	}
	claim := ClaimInput{
		WorkspaceID:            in.WorkspaceID,
		SessionID:              in.SessionID,
		GenerationMethod:       GenerationLLM,
		CoverageStartMessageID: in.CoverageStartMessageID,
		CoverageEndMessageID:   in.CoverageEndMessageID,
		SourceMessageCount:     count,
		SourceDigest:           sourceDigest,
		PolicyFingerprint:      in.PolicyFingerprint,
		PromptTemplateVersion:  tmplVer,
		PromptTemplateHash:     tmplHash,
		OwnerToken:             in.OwnerToken,
		LeaseTTL:               45 * time.Second,
		SummarizerSnapshot:     in.SummarizerSnapshot,
		EstimatedInputTokens:   in.EstimatedInputTokens,
		EstimatedOutputTokens:  in.EstimatedOutputTokens,
		EstimatorVersion:       in.EstimatorVersion,
	}
	if in.Parent != nil {
		claim.ParentSummaryID = &in.Parent.ID
		parentDig, digErr := ParentContentDigest(in.Parent)
		if digErr != nil {
			return GenerateResult{}, digErr
		}
		claim.ParentSummaryDigest = &parentDig
	}
	sum, claimed, err := g.repo.ClaimOrGet(ctx, claim)
	if err != nil {
		return GenerateResult{}, err
	}
	if sum.Status == StatusReady {
		return GenerateResult{Summary: sum, Claimed: false}, nil
	}
	if !claimed {
		return GenerateResult{Summary: sum, Claimed: false, FallbackOnly: true}, nil
	}

	// Real LLM compact — never extractive success.
	compacted, err := g.Compactor.Compact(ctx, CompactInput{
		ParentSummary: in.ParentBody,
		Turns:         in.Turns,
	})
	if err != nil {
		_, _ = g.repo.MarkFailed(ctx, in.WorkspaceID, sum.ID, in.OwnerToken, "SUMMARY_LLM_FAILED")
		return GenerateResult{FallbackOnly: true}, err
	}

	if g.PutObject == nil {
		failed, markErr := g.repo.MarkFailed(ctx, in.WorkspaceID, sum.ID, in.OwnerToken, "SUMMARY_STORE_UNAVAILABLE")
		if markErr != nil {
			return GenerateResult{}, markErr
		}
		return GenerateResult{Summary: failed, Claimed: true, FallbackOnly: true}, nil
	}
	objID := sum.ID
	sha, length, err := g.PutObject(ctx, in.WorkspaceID, objID, compacted.Body)
	if err != nil {
		_, _ = g.repo.MarkFailed(ctx, in.WorkspaceID, sum.ID, in.OwnerToken, "SUMMARY_OBJECT_PUT_FAILED")
		return GenerateResult{FallbackOnly: true}, err
	}
	if sha == "" {
		sha = compacted.ContentSHA256
	}
	ready, err := g.repo.MarkReadyWith(ctx, MarkReadyInput{
		WorkspaceID:           in.WorkspaceID,
		SummaryID:             sum.ID,
		OwnerToken:            in.OwnerToken,
		ObjectID:              objID,
		ContentSHA:            sha,
		ContentLen:            length,
		EstimatedInputTokens:  in.EstimatedInputTokens,
		EstimatedOutputTokens: in.EstimatedOutputTokens,
		EstimatorVersion:      in.EstimatorVersion,
		SummarizerSnapshot:    in.SummarizerSnapshot,
	})
	if err != nil {
		return GenerateResult{}, err
	}
	return GenerateResult{Summary: ready, Claimed: true, BodySHA256: sha}, nil
}

func turnsToTuples(turns []contextwindow.Turn) []MessageSourceTuple {
	out := make([]MessageSourceTuple, 0, countTurnMessages(turns))
	for _, t := range turns {
		out = append(out, MessageSourceTuple{
			ID: t.User.ID, Role: "USER", ContentHash: t.User.ContentHash,
		})
		for _, a := range t.Assistants {
			out = append(out, MessageSourceTuple{
				ID: a.ID, Role: "ASSISTANT", ContentHash: a.ContentHash,
			})
		}
	}
	return out
}

func countTurnMessages(turns []contextwindow.Turn) int {
	n := 0
	for _, t := range turns {
		n += 1 + len(t.Assistants)
	}
	return n
}

// digestTurns retained for tests only — not a product success digest.
func digestTurns(turns []contextwindow.Turn) string {
	return SourceChainDigest("", turnsToTuples(turns))
}

// buildExtractiveSummary is intentionally unexported and unused by Generate.
// Kept only so old references fail closed if reintroduced; do not call for READY.
func buildExtractiveSummary(turns []contextwindow.Turn) string {
	_ = turns
	return ""
}

// Ensure sha256 import used by tests that may call digests.
var _ = sha256.Sum256
var _ = hex.EncodeToString
