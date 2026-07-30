package protocolevent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"actweave/backend/internal/protocolevent"
	"github.com/google/uuid"
)

func TestBuildContextCompactionItemT4B(t *testing.T) {
	id := uuid.NewString()
	body := "稳定事实:\n- order ready\n"
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])

	// false → zero body
	item, err := protocolevent.BuildContextCompactionItem(protocolevent.ContextCompactionPayloadInput{
		ID: id, Status: protocolevent.ItemStatusCompleted, Result: "completed",
		IncludeSummary: false, InjectedSummary: body, SummaryDigest: digest,
	})
	if err != nil || item.ContentIncluded || item.Summary != "" {
		t.Fatalf("false path: %+v err=%v", item, err)
	}

	// true + completed → permanent body
	item, err = protocolevent.BuildContextCompactionItem(protocolevent.ContextCompactionPayloadInput{
		ID: id, Status: protocolevent.ItemStatusCompleted, Result: "completed",
		IncludeSummary: true, InjectedSummary: body, SummaryDigest: digest,
		BeforeTokens: 100, AfterTokens: 50,
	})
	if err != nil || !item.ContentIncluded || item.Summary != body || item.SummaryDigest != digest {
		t.Fatalf("true path: %+v err=%v", item, err)
	}

	// fallback never has body even if include true
	item, err = protocolevent.BuildContextCompactionItem(protocolevent.ContextCompactionPayloadInput{
		ID: id, Status: protocolevent.ItemStatusFailed, Result: "fallback",
		IncludeSummary: true, InjectedSummary: body,
		FallbackFrom: "rolling_summary", FallbackTo: "token_window",
	})
	if err != nil || item.ContentIncluded || item.Summary != "" {
		t.Fatalf("fallback: %+v err=%v", item, err)
	}

	// digest mismatch
	if _, err := protocolevent.BuildContextCompactionItem(protocolevent.ContextCompactionPayloadInput{
		ID: id, Status: protocolevent.ItemStatusCompleted, Result: "completed",
		IncludeSummary: true, InjectedSummary: body, SummaryDigest: strings.Repeat("0", 64),
	}); err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestDecodeContextCompactionItem(t *testing.T) {
	id := uuid.NewString()
	raw, _ := json.Marshal(map[string]any{
		"id": id, "type": "context_compaction", "status": "completed",
		"result": "completed", "contentIncluded": true, "summary": "hello",
	})
	item, err := protocolevent.DecodeItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	cc, ok := item.(protocolevent.ContextCompactionItem)
	if !ok || cc.Summary != "hello" || !cc.ContentIncluded {
		t.Fatalf("%+v", item)
	}
	// fallback strips body on decode
	raw2, _ := json.Marshal(map[string]any{
		"id": id, "type": "context_compaction", "status": "failed",
		"result": "fallback", "contentIncluded": false, "summary": "leak",
	})
	item2, err := protocolevent.DecodeItem(raw2)
	if err != nil {
		t.Fatal(err)
	}
	cc2 := item2.(protocolevent.ContextCompactionItem)
	if cc2.Summary != "" {
		t.Fatalf("expected strip: %+v", cc2)
	}
}
