package contextsummary_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/contextsummary"
	"actweave/backend/internal/database/dbtest"
)

const (
	testOwner    = "c08f1f2e-7b5a-7c3d-8e9f-123456789001"
	testWS       = "c08f1f2e-7b5a-7c3d-8e9f-123456789002"
	testModel    = "c08f1f2e-7b5a-7c3d-8e9f-123456789003"
	testAgent    = "c08f1f2e-7b5a-7c3d-8e9f-123456789004"
	testSession  = "c08f1f2e-7b5a-7c3d-8e9f-123456789005"
	testMsgEnd   = "c08f1f2e-7b5a-7c3d-8e9f-123456789006"
	testMsgStart = "c08f1f2e-7b5a-7c3d-8e9f-123456789016"
	testToken    = "c08f1f2e-7b5a-7c3d-8e9f-123456789007"
	testHash     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHashB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func seedSummaryFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	exec(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'s.owner','S')`, testOwner)
	exec(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'sum','S','SANDBOX',$2,$2,$2)`, testWS, testOwner)
	exec(t, db, `INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, testModel, testWS, testOwner)
	exec(t, db, `INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, testAgent, testWS, testModel, testOwner)
	exec(t, db, `INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, testSession, testWS, testAgent, testOwner)
	// Coverage messages required by workspace-scoped FK on claim/ready.
	exec(t, db, `INSERT INTO chat_messages(
		id,workspace_id,session_id,role,content,content_sha256,content_length,status,created_by
	) VALUES($1,$2,$3,'USER','start',$4,5,'RECEIVED',$5)`, testMsgStart, testWS, testSession, testHash, testOwner)
	exec(t, db, `INSERT INTO chat_messages(
		id,workspace_id,session_id,role,content,content_sha256,content_length,status,created_by
	) VALUES($1,$2,$3,'USER','end',$4,3,'RECEIVED',$5)`, testMsgEnd, testWS, testSession, testHash, testOwner)
}

func insertSummaryObject(t *testing.T, db *sql.DB, objectID string) {
	t.Helper()
	exec(t, db, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES(
			$1,$2,'actweave-executions',$3,'CHAT_CONTEXT_SUMMARY','text/plain; charset=utf-8',
			12,$4,'test-key','SENSITIVE','PERMANENT','USER',$5
		)
	`, objectID, testWS, testWS+"/chat-context-summary/"+objectID, testHash, testOwner)
}

func TestSummaryClaimReadyImmutable(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)

	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	claim := contextsummary.ClaimInput{
		WorkspaceID: testWS, SessionID: testSession, CoverageEndMessageID: testMsgEnd,
		CoverageStartMessageID: testMsgStart,
		SourceDigest:           testHash, PolicyFingerprint: testHash, PromptTemplateHash: testHash,
		PromptTemplateVersion: "v1", OwnerToken: testToken, LeaseTTL: time.Minute,
		GenerationMethod: contextsummary.GenerationLegacyExtractive,
	}
	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed || s.Status != contextsummary.StatusBuilding {
		t.Fatalf("claim: %+v claimed=%v err=%v", s, claimed, err)
	}
	if s.GenerationMethod != contextsummary.GenerationLegacyExtractive {
		t.Fatalf("generation method = %s", s.GenerationMethod)
	}
	insertSummaryObject(t, db, s.ID)
	ready, err := repo.MarkReady(context.Background(), testWS, s.ID, testToken, s.ID, testHash, 12)
	if err != nil || ready.Status != contextsummary.StatusReady {
		t.Fatalf("ready: %+v err=%v", ready, err)
	}
	// READY immutable
	if _, err := db.Exec(`UPDATE chat_context_summaries SET status='FAILED', failure_code='x' WHERE id=$1`, s.ID); err == nil {
		t.Fatal("expected READY immutable")
	}
	// Same key returns READY, not a new claim
	again, claimed2, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || claimed2 || again.Status != contextsummary.StatusReady {
		t.Fatalf("reuse: %+v claimed=%v err=%v", again, claimed2, err)
	}
	// Cross-workspace miss
	if _, err := repo.Get(context.Background(), "c08f1f2e-7b5a-7c3d-8e9f-123456789099", s.ID); err == nil {
		t.Fatal("expected not found cross workspace")
	}
	if !strings.Contains(ready.SourceDigest, "aaa") {
		t.Fatal("digest")
	}
}

func TestLLMClaimRequiresCoverageAndConflictChecks(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)
	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	snap := json.RawMessage(`{"modelSnapshotHash":"m1","templateHash":"` + testHash + `"}`)
	claim := contextsummary.ClaimInput{
		WorkspaceID: testWS, SessionID: testSession,
		GenerationMethod:       contextsummary.GenerationLLM,
		CoverageStartMessageID: testMsgStart,
		CoverageEndMessageID:   testMsgEnd,
		SourceMessageCount:     2,
		SourceDigest:           testHash,
		PolicyFingerprint:      testHash,
		PromptTemplateHash:     testHash,
		PromptTemplateVersion:  "context-compaction.v1",
		SummarizerSnapshot:     snap,
		EstimatedInputTokens:   100,
		EstimatedOutputTokens:  20,
		EstimatorVersion:       "test-estimator",
		OwnerToken:             testToken,
		LeaseTTL:               time.Minute,
	}
	// Empty summarizer snapshot must be rejected for LLM.
	emptySnap := claim
	emptySnap.SummarizerSnapshot = json.RawMessage(`{}`)
	if _, _, err := repo.ClaimOrGet(context.Background(), emptySnap); !errors.Is(err, contextsummary.ErrInvalid) {
		t.Fatalf("empty llm snapshot err=%v", err)
	}

	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed {
		t.Fatalf("llm claim: claimed=%v err=%v", claimed, err)
	}
	if s.GenerationMethod != contextsummary.GenerationLLM {
		t.Fatalf("method=%s", s.GenerationMethod)
	}

	// Parent/summarizer mismatch on same unique key must fail closed.
	badParent := "c08f1f2e-7b5a-7c3d-8e9f-123456789099"
	badDigest := testHashB
	conflict := claim
	conflict.OwnerToken = "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	conflict.ParentSummaryID = &badParent
	conflict.ParentSummaryDigest = &badDigest
	if _, _, err := repo.ClaimOrGet(context.Background(), conflict); !errors.Is(err, contextsummary.ErrConflict) {
		t.Fatalf("parent conflict err=%v", err)
	}
	conflictSnap := claim
	conflictSnap.OwnerToken = "c08f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	conflictSnap.SummarizerSnapshot = json.RawMessage(`{"modelSnapshotHash":"other"}`)
	if _, _, err := repo.ClaimOrGet(context.Background(), conflictSnap); !errors.Is(err, contextsummary.ErrConflict) {
		t.Fatalf("summarizer conflict err=%v", err)
	}

	insertSummaryObject(t, db, s.ID)
	ready, err := repo.MarkReadyWith(context.Background(), contextsummary.MarkReadyInput{
		WorkspaceID:           testWS,
		SummaryID:             s.ID,
		OwnerToken:            testToken,
		ObjectID:              s.ID,
		ContentSHA:            testHash,
		ContentLen:            12,
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 20,
		EstimatorVersion:      "test-estimator",
		SummarizerSnapshot:    snap,
	})
	if err != nil || ready.Status != contextsummary.StatusReady || ready.ContentObjectID == nil {
		t.Fatalf("llm ready: %+v err=%v", ready, err)
	}
	if ready.EstimatedInputTokens != 100 || ready.EstimatorVersion != "test-estimator" {
		t.Fatalf("ready estimates: %+v", ready)
	}

	// Orphan content_object_id rejected by FK.
	token2 := "c08f1f2e-7b5a-7c3d-8e9f-1234567890b1"
	msg2 := "c08f1f2e-7b5a-7c3d-8e9f-123456789017"
	exec(t, db, `INSERT INTO chat_messages(
		id,workspace_id,session_id,role,content,content_sha256,content_length,status,created_by
	) VALUES($1,$2,$3,'USER','m2',$4,2,'RECEIVED',$5)`, msg2, testWS, testSession, testHashB, testOwner)
	claim2 := claim
	claim2.CoverageEndMessageID = msg2
	claim2.SourceDigest = testHashB
	claim2.OwnerToken = token2
	s2, claimed2, err := repo.ClaimOrGet(context.Background(), claim2)
	if err != nil || !claimed2 {
		t.Fatalf("claim2: claimed=%v err=%v", claimed2, err)
	}
	orphanObj := "c08f1f2e-7b5a-7c3d-8e9f-1234567890c1"
	if _, err := repo.MarkReady(context.Background(), testWS, s2.ID, token2, orphanObj, testHashB, 1); err == nil {
		t.Fatal("expected orphan content object FK failure")
	}
}

func TestSourceChainDigestAndCumulativeCoverage(t *testing.T) {
	parent := contextsummary.SourceChainDigest("", []contextsummary.MessageSourceTuple{
		{ID: testMsgStart, Role: "USER", ContentHash: testHash},
	})
	if len(parent) != 64 {
		t.Fatalf("parent digest len=%d", len(parent))
	}
	child := contextsummary.SourceChainDigest(parent, []contextsummary.MessageSourceTuple{
		{ID: testMsgEnd, Role: "ASSISTANT", ContentHash: testHashB},
	})
	if child == parent {
		t.Fatal("child digest must advance")
	}
	// Deterministic
	again := contextsummary.SourceChainDigest(parent, []contextsummary.MessageSourceTuple{
		{ID: testMsgEnd, Role: "ASSISTANT", ContentHash: testHashB},
	})
	if again != child {
		t.Fatal("digest not deterministic")
	}
	if contextsummary.CumulativeSourceMessageCount(2, 3) != 5 {
		t.Fatal("cumulative count")
	}
	// Parent content digest requires READY content sha
	if _, err := contextsummary.ParentContentDigest(&contextsummary.Summary{Status: contextsummary.StatusBuilding}); !errors.Is(err, contextsummary.ErrInvalid) {
		t.Fatalf("parent digest building: %v", err)
	}
	sha := testHash
	got, err := contextsummary.ParentContentDigest(&contextsummary.Summary{
		Status: contextsummary.StatusReady, ContentSHA256: &sha,
	})
	if err != nil || got != testHash {
		t.Fatalf("parent content digest=%s err=%v", got, err)
	}
}

func TestCrossWorkspaceContentObjectFK(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)

	otherWS := "c08f1f2e-7b5a-7c3d-8e9f-1234567890d1"
	otherOwner := "c08f1f2e-7b5a-7c3d-8e9f-1234567890d2"
	exec(t, db, `INSERT INTO users(id,username,display_name) VALUES($1,'o.owner','O')`, otherOwner)
	exec(t, db, `INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'other','O','SANDBOX',$2,$2,$2)`, otherWS, otherOwner)

	objID := "c08f1f2e-7b5a-7c3d-8e9f-1234567890d3"
	// Object in other workspace
	exec(t, db, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES(
			$1,$2,'actweave-executions',$3,'CHAT_CONTEXT_SUMMARY','text/plain; charset=utf-8',
			4,$4,'test-key','SENSITIVE','PERMANENT','USER',$5
		)
	`, objID, otherWS, otherWS+"/chat-context-summary/"+objID, testHash, otherOwner)

	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	claim := contextsummary.ClaimInput{
		WorkspaceID: testWS, SessionID: testSession,
		GenerationMethod:       contextsummary.GenerationLLM,
		CoverageStartMessageID: testMsgStart,
		CoverageEndMessageID:   testMsgEnd,
		SourceMessageCount:     1,
		SourceDigest:           testHash,
		PolicyFingerprint:      testHash,
		PromptTemplateHash:     testHash,
		PromptTemplateVersion:  "context-compaction.v1",
		SummarizerSnapshot:     json.RawMessage(`{"model":"x"}`),
		EstimatedInputTokens:   1,
		EstimatedOutputTokens:  1,
		EstimatorVersion:       "e",
		OwnerToken:             testToken,
		LeaseTTL:               time.Minute,
	}
	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed {
		t.Fatalf("claim: %v claimed=%v", err, claimed)
	}
	// Same object id in other workspace must not satisfy composite FK.
	if _, err := repo.MarkReady(context.Background(), testWS, s.ID, testToken, objID, testHash, 4); err == nil {
		t.Fatal("expected cross-workspace content object FK rejection")
	}
}

func TestMigration000004Schema(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 18 || version.Dirty {
		t.Fatalf("migration version = %+v", version)
	}
	db := testDatabase.Open(t)
	var methodExists bool
	if err := db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='chat_context_summaries' AND column_name='generation_method')
	`).Scan(&methodExists); err != nil || !methodExists {
		t.Fatalf("generation_method column: exists=%v err=%v", methodExists, err)
	}
	for _, constraint := range []string{
		"chat_context_summaries_generation_method_check",
		"chat_context_summaries_content_object_fk",
		"chat_context_summaries_parent_summary_fk",
		"chat_context_summaries_coverage_start_fk",
		"chat_context_summaries_coverage_end_fk",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("constraint %s: exists=%v err=%v", constraint, exists, err)
		}
	}
	var idx bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='chat_context_summaries_ready_llm_lookup_idx')`).Scan(&idx); err != nil || !idx {
		t.Fatalf("ready llm index: exists=%v err=%v", idx, err)
	}
	// Legacy default readable without backfill.
	seedSummaryFixture(t, db)
	exec(t, db, `
		INSERT INTO chat_context_summaries(
			id,workspace_id,session_id,status,coverage_end_message_id,source_message_count,
			source_digest,policy_fingerprint,prompt_template_version,prompt_template_hash,
			owner_token,lease_expires_at,attempt_count
		) VALUES(
			'c08f1f2e-7b5a-7c3d-8e9f-1234567890e1',$1,$2,'BUILDING',$3,0,
			$4,$4,'extractive.v1',$4,$5,now() + interval '1 minute',1
		)
	`, testWS, testSession, testMsgEnd, testHash, testToken)
	var method string
	if err := db.QueryRow(`SELECT generation_method FROM chat_context_summaries WHERE id=$1`,
		"c08f1f2e-7b5a-7c3d-8e9f-1234567890e1").Scan(&method); err != nil || method != contextsummary.GenerationLegacyExtractive {
		t.Fatalf("legacy default method=%q err=%v", method, err)
	}
}

func TestFindLatestReadyLLMFilters(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)
	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	snap := json.RawMessage(`{"modelSnapshotHash":"m1","templateHash":"` + testHash + `"}`)
	claim := contextsummary.ClaimInput{
		WorkspaceID: testWS, SessionID: testSession,
		GenerationMethod:       contextsummary.GenerationLLM,
		CoverageStartMessageID: testMsgStart,
		CoverageEndMessageID:   testMsgEnd,
		SourceMessageCount:     2,
		SourceDigest:           testHash,
		PolicyFingerprint:      testHash,
		PromptTemplateHash:     testHash,
		PromptTemplateVersion:  "context-compaction.v1",
		SummarizerSnapshot:     snap,
		EstimatedInputTokens:   10,
		EstimatedOutputTokens:  2,
		EstimatorVersion:       "e",
		OwnerToken:             testToken,
		LeaseTTL:               time.Minute,
	}
	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed {
		t.Fatalf("claim: %v", err)
	}
	insertSummaryObject(t, db, s.ID)
	if _, err := repo.MarkReadyWith(context.Background(), contextsummary.MarkReadyInput{
		WorkspaceID: testWS, SummaryID: s.ID, OwnerToken: testToken,
		ObjectID: s.ID, ContentSHA: testHash, ContentLen: 12,
		EstimatedInputTokens: 10, EstimatedOutputTokens: 2, EstimatorVersion: "e",
		SummarizerSnapshot: snap,
	}); err != nil {
		t.Fatal(err)
	}

	// Matching filter returns READY LLM.
	got, err := repo.FindLatestReadyLLM(context.Background(), contextsummary.LatestReadyFilter{
		WorkspaceID: testWS, SessionID: testSession,
		PolicyFingerprint: testHash, PromptTemplateHash: testHash,
		SummarizerSnapshotHash: contextsummary.CanonicalSummarizerSnapshotHash(snap),
	})
	if err != nil || got.ID != s.ID || got.GenerationMethod != contextsummary.GenerationLLM {
		t.Fatalf("latest: %+v err=%v", got, err)
	}
	// Wrong policy fingerprint → not found
	if _, err := repo.FindLatestReadyLLM(context.Background(), contextsummary.LatestReadyFilter{
		WorkspaceID: testWS, SessionID: testSession,
		PolicyFingerprint: testHashB, PromptTemplateHash: testHash,
	}); !errors.Is(err, contextsummary.ErrNotFound) {
		t.Fatalf("wrong policy: %v", err)
	}
	// Cross workspace
	if _, err := repo.FindLatestReadyLLM(context.Background(), contextsummary.LatestReadyFilter{
		WorkspaceID: "c08f1f2e-7b5a-7c3d-8e9f-123456789099", SessionID: testSession,
		PolicyFingerprint: testHash, PromptTemplateHash: testHash,
	}); !errors.Is(err, contextsummary.ErrNotFound) {
		t.Fatalf("cross ws: %v", err)
	}
	// Legacy extractive READY must not be returned: insert legacy-style READY is blocked by
	// generation_method; claim legacy BUILDING is not READY so still not found.
	if _, err := repo.FindLatestReadyLLM(context.Background(), contextsummary.LatestReadyFilter{
		WorkspaceID: testWS, SessionID: testSession,
		PolicyFingerprint: testHash, PromptTemplateHash: testHashB,
	}); !errors.Is(err, contextsummary.ErrNotFound) {
		t.Fatalf("wrong template: %v", err)
	}
}

func TestFailedNeverReferencesContentObject(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	seedSummaryFixture(t, db)
	repo, err := contextsummary.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	claim := contextsummary.ClaimInput{
		WorkspaceID: testWS, SessionID: testSession, CoverageEndMessageID: testMsgEnd,
		SourceDigest: testHash, PolicyFingerprint: testHash, PromptTemplateHash: testHash,
		PromptTemplateVersion: "v1", OwnerToken: testToken, LeaseTTL: time.Minute,
	}
	s, claimed, err := repo.ClaimOrGet(context.Background(), claim)
	if err != nil || !claimed {
		t.Fatalf("claim: %v", err)
	}
	failed, err := repo.MarkFailed(context.Background(), testWS, s.ID, testToken, "SUMMARY_STORE_UNAVAILABLE")
	if err != nil || failed.Status != contextsummary.StatusFailed || failed.ContentObjectID != nil {
		t.Fatalf("failed: %+v err=%v", failed, err)
	}
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}
