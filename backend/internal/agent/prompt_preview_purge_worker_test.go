package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestPreviewPurgeWorkerRunOncePurgesExpiredObjects(t *testing.T) {
	repository, db := newAgentServiceTest(t)
	objects := newMemoryPromptObjects(db)
	service, err := NewPromptService(repository, objects,
		staticModelSnapshot{value: json.RawMessage(`{"model":"m"}`)},
		PromptGeneratorFunc(func(context.Context, PromptGenerationRequest) (string, error) {
			return "preview output body", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.RunCreatePreview(context.Background(), serviceWorkspaceID, serviceModelID,
		"draft", "trace-purge", serviceOwnerID)
	if err != nil {
		t.Fatal(err)
	}

	expirePreviewObject(t, db, run.InputObjectID)
	if run.OutputObjectID != nil {
		expirePreviewObject(t, db, *run.OutputObjectID)
	}

	worker, err := NewPreviewPurgeWorker(db, &sqlPreviewPurger{db: db}, PreviewPurgeConfig{
		Interval: 10 * time.Second, BatchLimit: 10, ClaimLease: 30 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least one purge, got %d", n)
	}
	var purgedAt sql.NullTime
	if err := db.QueryRow(`SELECT body_purged_at FROM stored_objects WHERE id=$1`, run.InputObjectID).
		Scan(&purgedAt); err != nil || !purgedAt.Valid {
		t.Fatalf("input body_purged_at valid=%v err=%v", purgedAt.Valid, err)
	}
	// Second pass is idempotent (nothing left to claim or already purged).
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("idempotent RunOnce: %v", err)
	}
}

type sqlPreviewPurger struct{ db *sql.DB }

func (p *sqlPreviewPurger) PurgeBody(ctx context.Context, workspaceID, objectID string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE stored_objects
		SET body_purged_at=clock_timestamp(),
			purge_claim_token=NULL,
			purge_claim_expires_at=NULL,
			purge_next_attempt_at=NULL,
			purge_last_error_code=NULL
		WHERE workspace_id=$1 AND id=$2
		  AND kind IN ('PROMPT_PREVIEW_INPUT','PROMPT_PREVIEW_OUTPUT')
		  AND retention_mode='EXPIRING'
		  AND retention_until IS NOT NULL
		  AND retention_until <= clock_timestamp()
		  AND body_purged_at IS NULL
	`, workspaceID, objectID)
	return err
}

func expirePreviewObject(t *testing.T, db *sql.DB, objectID string) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE stored_objects DISABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE stored_objects
		SET created_at=clock_timestamp() - interval '2 hours',
			retention_until=clock_timestamp() - interval '1 hour'
		WHERE id=$1 AND kind IN ('PROMPT_PREVIEW_INPUT','PROMPT_PREVIEW_OUTPUT')
	`, objectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE stored_objects ENABLE TRIGGER stored_objects_metadata_guard`); err != nil {
		t.Fatal(err)
	}
}
