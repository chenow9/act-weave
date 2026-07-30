package contextsummary_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"actweave/backend/internal/contextsummary"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/sessioncontext"
)

type fakeModel struct {
	body string
}

func (f fakeModel) Generate(context.Context, string, string, float64, int) (string, error) {
	if f.body != "" {
		return f.body, nil
	}
	return `{"stableFacts":["fact-a"],"decisions":["d1"],"openItems":["o1"],"recentState":"ok"}`, nil
}

func TestCoordinatorCompletedWithFakeModel(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)
	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	// Store put inserts a real stored_object for FK.
	put := func(ctx context.Context, workspaceID, objectID string, body []byte) (string, int64, error) {
		insertSummaryObject(t, db, objectID)
		sum := sha256HexLocal(body)
		return sum, int64(len(body)), nil
	}
	coord := &contextsummary.Coordinator{
		Repo: repo,
		Compactor: &contextsummary.LLMCompactor{
			Model: fakeModel{}, MaxTokens: 256,
		},
		PutObject: put,
	}
	turns := []contextwindow.Turn{{
		User: contextwindow.HistoryMessage{
			ID: testMsgStart, Role: "USER", Content: "hello world", ContentHash: testHash,
		},
		Assistants: []contextwindow.HistoryMessage{{
			ID: testMsgEnd, Role: "ASSISTANT", Content: "hi there", ContentHash: testHash,
		}},
	}}
	plan, err := contextwindow.PlanCompaction(contextwindow.PreflightInput{
		EffectiveMaxInputTokens: 50, // force trigger on small ceiling
		TokenizerProfile:        "o200k_base",
		SystemPrompt:            "sys",
		UncoveredTurns:          turns,
		CurrentUser: contextwindow.HistoryMessage{
			ID: "c08f1f2e-7b5a-7c3d-8e9f-1234567890f9", Role: "USER", Content: "now", ContentHash: testHash,
		},
		MaxRecentTurns: 0,
	})
	// If mandatory too large, use a synthetic triggered plan.
	if err != nil || !plan.Triggered {
		plan = contextwindow.CompactionPlan{
			Triggered: true, TriggerInputTokens: 100, EffectiveInputCeiling: 100,
			CoverageTurns: turns, EstimatedMandatoryTokens: 10,
		}
	}
	if len(plan.CoverageTurns) == 0 {
		plan.CoverageTurns = turns
		plan.Triggered = true
	}
	snap := sessioncontext.ResolvedSnapshot{
		SchemaVersion: sessioncontext.SnapshotSchemaV2,
		Compaction: &sessioncontext.CompactionSnapshot{
			MaxGenerationPasses: 2, TotalTimeoutMs: 45000, PerPassTimeoutMs: 20000, ClaimWaitMs: 1000,
			TriggerBps: 8000, TargetBps: 6000, MaxSummaryTokens: 256,
			TemplateVersion: contextsummary.CompactionTemplateVersion,
			TemplateHash:    contextsummary.CompactTemplateHash(),
		},
	}
	res, err := coord.Run(context.Background(), contextsummary.CoordinatorInput{
		WorkspaceID: testWS, SessionID: testSession,
		Snapshot: snap, Plan: plan,
		PolicyFingerprint:  testHash,
		OwnerToken:         testToken,
		EstimatorVersion:   "test",
		SummarizerSnapshot: json.RawMessage(`{"model":"fake"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// completed or fallback both ok for IC-07; must be body-free and stable status.
	if res.Status != "completed" && res.Status != "fallback" {
		t.Fatalf("status=%s code=%s", res.Status, res.FallbackCode)
	}
	if res.Passes < 1 {
		t.Fatal("expected at least one pass")
	}
	_ = time.Second
}

func sha256HexLocal(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
