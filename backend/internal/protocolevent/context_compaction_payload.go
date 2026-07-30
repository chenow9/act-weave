package protocolevent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxContextCompactionSummaryBytes is the permanent protocol plaintext ceiling (64 KiB).
const MaxContextCompactionSummaryBytes = 64 << 10

// ContextCompactionPayloadInput builds a strict context_compaction item snapshot.
type ContextCompactionPayloadInput struct {
	ID                 string
	Status             ItemStatus
	Result             string // completed|fallback|failed|building
	TriggerBps         int64
	TargetBps          int64
	BeforeTokens       int64
	AfterTokens        int64
	EffectiveMaxInput  int64
	CoverageStartID    string
	CoverageEndID      string
	SourceMessageCount int
	Passes             int
	Reused             bool
	SummaryID          string
	SummaryDigest      string
	FallbackFrom       string
	FallbackTo         string
	FallbackStage      string
	ErrorCode          string
	// IncludeSummary is the frozen run snapshot aap.includeCompactionSummary.
	IncludeSummary bool
	// InjectedSummary is the actual normalized body injected into the main model.
	// Only dual-written when IncludeSummary && Result==completed.
	InjectedSummary string
}

// BuildContextCompactionItem constructs a T4-B-safe item.
// false/building/fallback/failed: zero summary body.
// true+completed: permanent plaintext matching digest.
func BuildContextCompactionItem(in ContextCompactionPayloadInput) (ContextCompactionItem, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return ContextCompactionItem{}, errors.New("context compaction item id required")
	}
	item := ContextCompactionItem{
		ID:                 id,
		Type:               ItemTypeContextCompaction,
		Status:             in.Status,
		Result:             strings.TrimSpace(in.Result),
		TriggerBps:         in.TriggerBps,
		TargetBps:          in.TargetBps,
		BeforeTokens:       in.BeforeTokens,
		AfterTokens:        in.AfterTokens,
		EffectiveMaxInput:  in.EffectiveMaxInput,
		CoverageStartID:    strings.TrimSpace(in.CoverageStartID),
		CoverageEndID:      strings.TrimSpace(in.CoverageEndID),
		SourceMessageCount: in.SourceMessageCount,
		Passes:             in.Passes,
		Reused:             in.Reused,
		SummaryID:          strings.TrimSpace(in.SummaryID),
		SummaryDigest:      strings.ToLower(strings.TrimSpace(in.SummaryDigest)),
		FallbackFrom:       strings.TrimSpace(in.FallbackFrom),
		FallbackTo:         strings.TrimSpace(in.FallbackTo),
		FallbackStage:      strings.TrimSpace(in.FallbackStage),
		ErrorCode:          strings.TrimSpace(in.ErrorCode),
		ContentIncluded:    false,
		Summary:            "",
	}
	// T4-B: only successful completed + snapshot true may carry body.
	if in.IncludeSummary && item.Result == "completed" && item.Status == ItemStatusCompleted {
		body := in.InjectedSummary
		if body == "" {
			return ContextCompactionItem{}, errors.New("completed includeSummary requires injected body")
		}
		if len(body) > MaxContextCompactionSummaryBytes {
			return ContextCompactionItem{}, errors.New("summary exceeds 64 KiB")
		}
		if !utf8.ValidString(body) {
			return ContextCompactionItem{}, errors.New("summary invalid utf-8")
		}
		sum := sha256.Sum256([]byte(body))
		digest := hex.EncodeToString(sum[:])
		if item.SummaryDigest != "" && item.SummaryDigest != digest {
			return ContextCompactionItem{}, errors.New("summary digest mismatch")
		}
		item.SummaryDigest = digest
		item.ContentIncluded = true
		item.Summary = body
	}
	return item, nil
}
