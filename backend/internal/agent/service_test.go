package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"github.com/google/uuid"
)

const (
	serviceOwnerID      = "048f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	serviceWorkspaceID  = "048f1f2e-7b5a-7c3d-8e9f-1234567890a2"
	serviceOtherSpaceID = "048f1f2e-7b5a-7c3d-8e9f-1234567890a3"
	serviceModelID      = "048f1f2e-7b5a-7c3d-8e9f-1234567890a4"
	serviceOtherModelID = "048f1f2e-7b5a-7c3d-8e9f-1234567890a5"
	serviceAgentID      = "048f1f2e-7b5a-7c3d-8e9f-1234567890a6"
	serviceSecondID     = "048f1f2e-7b5a-7c3d-8e9f-1234567890a7"
	serviceRevisionID   = "048f1f2e-7b5a-7c3d-8e9f-1234567890a8"
	serviceRevision2ID  = "048f1f2e-7b5a-7c3d-8e9f-1234567890a9"
	serviceSecondRevID  = "048f1f2e-7b5a-7c3d-8e9f-1234567890aa"
)

func TestAgentCRUDDefaultTransactionAndImmutablePromptVersions(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	created, initial, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Primary Agent",
		RoleDescription: "Initial role", ModelConfigID: serviceModelID, IsDefault: true,
		InitialRevisionID: serviceRevisionID, InitialPrompt: "Initial immutable prompt",
		PromptSource: "MANUAL", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if !created.IsDefault || created.CurrentPromptRevisionID == nil || *created.CurrentPromptRevisionID != initial.ID || initial.RevisionNo != 1 {
		t.Fatalf("unexpected created agent/revision: %+v %+v", created, initial)
	}
	if _, err := repository.Get(context.Background(), serviceOtherSpaceID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected scoped agent miss, got %v", err)
	}
	updated, err := repository.Update(context.Background(), serviceWorkspaceID, created.ID, UpdateAgent{
		Name: "Primary Agent Updated", RoleDescription: "Updated role", ModelConfigID: serviceModelID,
		Status: StatusActive, UpdatedBy: serviceOwnerID, ExpectedLockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if _, err := repository.Update(context.Background(), serviceWorkspaceID, created.ID, UpdateAgent{
		Name: "Stale", ModelConfigID: serviceModelID, Status: StatusActive,
		UpdatedBy: serviceOwnerID, ExpectedLockVersion: created.LockVersion,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale update conflict, got %v", err)
	}
	withPrompt, secondRevision, err := repository.UpdatePrompt(context.Background(), serviceWorkspaceID,
		created.ID, serviceRevision2ID, "Second immutable prompt", "MANUAL", serviceOwnerID, updated.LockVersion)
	if err != nil {
		t.Fatalf("update prompt: %v", err)
	}
	if secondRevision.RevisionNo != 2 || withPrompt.CurrentPromptRevisionID == nil || *withPrompt.CurrentPromptRevisionID != secondRevision.ID {
		t.Fatalf("unexpected prompt update: agent=%+v revision=%+v", withPrompt, secondRevision)
	}
	revisions, err := repository.ListPromptRevisions(context.Background(), serviceWorkspaceID, created.ID)
	if err != nil || len(revisions) != 2 || revisions[0].SystemPrompt != "Initial immutable prompt" {
		t.Fatalf("prompt history was not append-only: %+v err=%v", revisions, err)
	}
	if _, err := db.Exec(`DELETE FROM agent_prompt_revisions WHERE id=$1`, initial.ID); err == nil {
		t.Fatal("database allowed prompt revision deletion")
	}

	second, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceSecondID, WorkspaceID: serviceWorkspaceID, Name: "Secondary Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceSecondRevID,
		InitialPrompt: "Secondary prompt", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	second, err = repository.SetDefault(context.Background(), serviceWorkspaceID, second.ID, serviceOwnerID, second.LockVersion)
	if err != nil || !second.IsDefault {
		t.Fatalf("set second default: %+v err=%v", second, err)
	}
	var workspaceDefault string
	if err := db.QueryRow(`SELECT default_agent_id FROM workspaces WHERE id=$1`, serviceWorkspaceID).Scan(&workspaceDefault); err != nil {
		t.Fatal(err)
	}
	if workspaceDefault != second.ID {
		t.Fatalf("workspace default mismatch: %s", workspaceDefault)
	}
	formerDefault, err := repository.Get(context.Background(), serviceWorkspaceID, created.ID)
	if err != nil || formerDefault.IsDefault {
		t.Fatalf("former default was not cleared: %+v err=%v", formerDefault, err)
	}
	if err := repository.SoftDelete(context.Background(), serviceWorkspaceID, second.ID, serviceOwnerID, second.LockVersion); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected default agent delete protection, got %v", err)
	}
	if err := repository.SoftDelete(context.Background(), serviceWorkspaceID, formerDefault.ID, serviceOwnerID, formerDefault.LockVersion); err != nil {
		t.Fatalf("soft delete non-default agent: %v", err)
	}
}

func TestPromptPreviewRecordsRunAndAcceptsOutputAsNewRevision(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	created, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Prompt Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "Original prompt", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := newMemoryPromptObjects(db)
	snapshots := staticModelSnapshot{value: json.RawMessage(`{"provider":"OPENAI_COMPATIBLE","model":"agent-model"}`)}
	service, err := NewPromptService(repository, objects, snapshots, PromptGeneratorFunc(func(ctx context.Context, request PromptGenerationRequest) (string, error) {
		probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if _, err := db.ExecContext(probeCtx, `UPDATE agents SET updated_at=updated_at WHERE id=$1`, request.Agent.ID); err != nil {
			return "", err
		}
		return "Generated prompt that may be accepted", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.Run(context.Background(), serviceWorkspaceID, created.ID,
		"PREVIEW", "Improve the prompt", "trace-preview-1", serviceOwnerID)
	if err != nil {
		t.Fatalf("preview prompt: %v", err)
	}
	if run.Status != "SUCCEEDED" || run.OutputObjectID == nil || output != "Generated prompt that may be accepted" {
		t.Fatalf("unexpected preview run/output: %+v output=%q", run, output)
	}
	revisions, err := repository.ListPromptRevisions(context.Background(), serviceWorkspaceID, created.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("preview changed prompt revisions: %+v err=%v", revisions, err)
	}
	accepted, revision, err := service.Accept(context.Background(), serviceWorkspaceID, run.ID, serviceOwnerID, created.LockVersion)
	if err != nil {
		t.Fatalf("accept prompt output: %v", err)
	}
	if accepted.AcceptedRevisionID == nil || *accepted.AcceptedRevisionID != revision.ID ||
		revision.RevisionNo != 2 || revision.Source != "ENHANCED" || revision.SystemPrompt != output {
		t.Fatalf("unexpected accepted prompt: run=%+v revision=%+v", accepted, revision)
	}
	if _, _, err := service.Accept(context.Background(), serviceWorkspaceID, run.ID, serviceOwnerID, created.LockVersion+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate acceptance conflict, got %v", err)
	}
}

func TestRunCreatePreviewWithoutAgentID(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	var heldTx bool
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"preview-model","status":"VERIFIED"}`)},
		PromptGeneratorFunc(func(ctx context.Context, request PromptGenerationRequest) (string, error) {
			if request.AgentID != nil || request.WorkspaceID != serviceWorkspaceID ||
				request.ModelConfigID != serviceModelID || request.OperationType != PromptOperationCreatePreview {
				return "", fmt.Errorf("unexpected generation target: %+v", request)
			}
			// Prove model call is outside a DB transaction: a nested query must work.
			var one int
			if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
				return "", err
			}
			// Concurrent write should not block on a held service transaction.
			probe, err := db.BeginTx(ctx, nil)
			if err != nil {
				return "", err
			}
			if _, err := probe.ExecContext(ctx, `SELECT 1`); err != nil {
				_ = probe.Rollback()
				return "", err
			}
			_ = probe.Commit()
			heldTx = false
			return "  Refined create-preview prompt  ", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	beforeAgents, err := countRows(db, `SELECT count(*) FROM agents WHERE workspace_id=$1`, serviceWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	run, output, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft system prompt", "trace-create-preview-1", serviceOwnerID)
	if err != nil {
		t.Fatalf("RunCreatePreview: %v", err)
	}
	if heldTx {
		t.Fatal("model call appeared to hold a transaction")
	}
	if run.Status != "SUCCEEDED" || run.OperationType != PromptOperationCreatePreview ||
		run.AgentID != nil || run.ExpiresAt == nil || output != "Refined create-preview prompt" {
		t.Fatalf("unexpected create preview: run=%+v output=%q", run, output)
	}
	wantExpires := run.CreatedAt.Add(30 * 24 * time.Hour)
	if !run.ExpiresAt.Equal(wantExpires) && !run.ExpiresAt.Truncate(time.Microsecond).Equal(wantExpires.Truncate(time.Microsecond)) {
		t.Fatalf("expires_at=%v want created+30d=%v", run.ExpiresAt, wantExpires)
	}
	afterAgents, err := countRows(db, `SELECT count(*) FROM agents WHERE workspace_id=$1`, serviceWorkspaceID)
	if err != nil || afterAgents != beforeAgents {
		t.Fatalf("create preview mutated agents: before=%d after=%d err=%v", beforeAgents, afterAgents, err)
	}
	var objectKind string
	var retention string
	if err := db.QueryRow(`
		SELECT kind, retention_mode FROM stored_objects WHERE id=$1
	`, run.InputObjectID).Scan(&objectKind, &retention); err != nil {
		t.Fatal(err)
	}
	if objectKind != "PROMPT_PREVIEW_INPUT" || retention != "EXPIRING" {
		t.Fatalf("input object kind/retention=%s/%s", objectKind, retention)
	}

	// Empty output fails with stable code and no apply-able success.
	emptyService, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"preview-model"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "   ", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	failed, _, err := emptyService.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Another draft", "trace-create-preview-empty", serviceOwnerID)
	if !errors.Is(err, ErrPromptOutputInvalid) || failed.Status != "FAILED" ||
		failed.ErrorCode == nil || *failed.ErrorCode != ErrorCodePromptOutputInvalid {
		t.Fatalf("empty output run=%+v err=%v", failed, err)
	}

	// Each retry creates an independent Run (no reuse).
	run2, _, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"Draft system prompt", "trace-create-preview-2", serviceOwnerID)
	if err != nil || run2.ID == run.ID {
		t.Fatalf("second preview must be independent: run2=%+v err=%v first=%s", run2, err, run.ID)
	}
}

func countRows(db *sql.DB, query string, args ...any) (int, error) {
	var count int
	err := db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func TestPromptGenerationFailureRecordsStableRedactedRun(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	created, _, err := repository.Create(context.Background(), NewAgent{
		ID: serviceAgentID, WorkspaceID: serviceWorkspaceID, Name: "Failure Agent",
		ModelConfigID: serviceModelID, InitialRevisionID: serviceRevisionID,
		InitialPrompt: "Original prompt", CreatedBy: serviceOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPromptService(repository, newMemoryPromptObjects(db),
		staticModelSnapshot{value: json.RawMessage(`{"model":"agent-model"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "", errors.New("upstream body contained raw-prompt-secret")
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.Run(context.Background(), serviceWorkspaceID, created.ID,
		"ENHANCE", "Improve", "trace-failure-1", serviceOwnerID)
	if !errors.Is(err, ErrPromptGeneration) || run.Status != "FAILED" || run.ErrorCode == nil || *run.ErrorCode != ErrorCodePromptGenerationFailed {
		t.Fatalf("unexpected failed run: %+v err=%v", run, err)
	}
	var storedCode string
	if err := db.QueryRow(`SELECT error_code FROM prompt_runs WHERE id=$1`, run.ID).Scan(&storedCode); err != nil {
		t.Fatal(err)
	}
	if storedCode != ErrorCodePromptGenerationFailed || strings.Contains(storedCode, "raw-prompt-secret") {
		t.Fatalf("unsafe prompt run error persistence: %q", storedCode)
	}
}

func newAgentServiceTest(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDatabase := dbtest.New(t)
	// PromptRun scan includes create-preview retention columns from migration 61.
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number < 1 || version.Dirty {
		t.Fatalf("unexpected migration: %+v", version)
	}
	db := testDatabase.Open(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'agent.service.owner','Agent Service Owner')`, serviceOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES
		($1,'agent-service','Agent Service','PRODUCTION',$3,$3,$3),
		($2,'agent-service-other','Agent Service Other','SANDBOX',$3,$3,$3)
	`, serviceWorkspaceID, serviceOtherSpaceID, serviceOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES
		($1,$3,'Agent Service Model','OPENAI_COMPATIBLE','https://models.example/v1','agent-model',$5,$5),
		($2,$4,'Agent Other Model','OPENAI_COMPATIBLE','https://models.example/v1','other-model',$5,$5)
	`, serviceModelID, serviceOtherModelID, serviceWorkspaceID, serviceOtherSpaceID, serviceOwnerID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}

type staticModelSnapshot struct{ value json.RawMessage }

func (s staticModelSnapshot) Snapshot(context.Context, string, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), s.value...), nil
}

type memoryPromptObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	db      *sql.DB
}

func newMemoryPromptObjects(db *sql.DB) *memoryPromptObjects {
	return &memoryPromptObjects{objects: make(map[string][]byte), db: db}
}

func (s *memoryPromptObjects) PutPermanent(_ context.Context, workspaceID, kind string, content []byte, createdBy string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	objectKind := "PROMPT_RUN_INPUT"
	if kind == "PROMPT_OUTPUT" {
		objectKind = "PROMPT_RUN_OUTPUT"
	}
	digest := sha256.Sum256(content)
	if _, err := s.db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,'actweave-executions',$3,$4,'text/plain',$5,$6,
		 'agent-test-key-v1','SENSITIVE','PERMANENT','USER',$7)
	`, id.String(), workspaceID, workspaceID+"/prompt/"+id.String(), objectKind,
		len(content), hex.EncodeToString(digest[:]), createdBy); err != nil {
		return "", err
	}
	s.objects[id.String()] = append([]byte(nil), content...)
	return id.String(), nil
}

func (s *memoryPromptObjects) PutPreview(
	_ context.Context, workspaceID, kind string, content []byte, createdBy string, retentionUntil time.Time,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	objectKind := "PROMPT_PREVIEW_INPUT"
	if kind == "PROMPT_OUTPUT" {
		objectKind = "PROMPT_PREVIEW_OUTPUT"
	}
	digest := sha256.Sum256(content)
	if _, err := s.db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,retention_until,
		 created_by_type,created_by_id
		) VALUES($1,$2,'actweave-executions',$3,$4,'text/plain',$5,$6,
		 'agent-test-key-v1','SENSITIVE','EXPIRING',$7,'USER',$8)
	`, id.String(), workspaceID, workspaceID+"/prompt-preview/"+id.String(), objectKind,
		len(content), hex.EncodeToString(digest[:]), retentionUntil.UTC(), createdBy); err != nil {
		return "", err
	}
	s.objects[id.String()] = append([]byte(nil), content...)
	return id.String(), nil
}

func (s *memoryPromptObjects) GetPermanent(_ context.Context, _ string, id string, _ string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, exists := s.objects[id]
	if !exists {
		return nil, fmt.Errorf("object %s not found", id)
	}
	return append([]byte(nil), content...), nil
}

func (s staticModelSnapshot) AvailableSnapshot(ctx context.Context, workspaceID, modelConfigID string) (json.RawMessage, error) {
	return s.Snapshot(ctx, workspaceID, modelConfigID)
}
