package audit_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

const (
	outboxAggregateID = "a58f1f2e-7b5a-7c3d-8e9f-123456789001"
	outboxCommittedID = "a58f1f2e-7b5a-7c3d-8e9f-123456789002"
)

func TestTransactionalOutbox(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 2 || version.Dirty {
		t.Fatalf("transactional outbox migration = %+v", version)
	}
	db := testDatabase.Open(t)
	db.SetMaxOpenConns(16)
	insertAuditMigrationFixtures(t, db)
	if _, err := db.Exec(`CREATE TABLE outbox_business_probe(
		id UUID PRIMARY KEY, workspace_id UUID NOT NULL, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	repository, _ := audit.NewOutboxRepository(db)
	ctx := context.Background()
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)
	input := audit.AppendOutboxInput{
		ID: outboxCommittedID, WorkspaceID: auditMigrationWorkspaceID,
		AggregateType: "TOOL", AggregateID: outboxAggregateID,
		EventType:     "tool.release.published",
		Payload:       json.RawMessage(`{"schemaVersion":"tool.release.v1","releaseNo":1}`),
		SchemaVersion: "tool.release.v1", IdempotencyKey: "tool-release:" + outboxAggregateID,
		OccurredAt: occurredAt,
	}

	rolledBack, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rolledBack.ExecContext(ctx, `INSERT INTO outbox_business_probe(id,workspace_id,value)
		VALUES($1,$2,'rollback')`, uuid.NewString(), auditMigrationWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if result, err := repository.AppendInTransaction(ctx, rolledBack, input); err != nil || !result.Created {
		t.Fatalf("append rollback outbox: %+v err=%v", result, err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertProbeAndOutboxCounts(t, db, 0, 0)

	committed, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	probeID := uuid.NewString()
	if _, err := committed.ExecContext(ctx, `INSERT INTO outbox_business_probe(id,workspace_id,value)
		VALUES($1,$2,'committed')`, probeID, auditMigrationWorkspaceID); err != nil {
		t.Fatal(err)
	}
	created, err := repository.AppendInTransaction(ctx, committed, input)
	if err != nil || !created.Created || created.Event.ID != input.ID {
		t.Fatalf("append committed outbox: %+v err=%v", created, err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatal(err)
	}
	assertProbeAndOutboxCounts(t, db, 1, 1)

	retry := input
	retry.ID = uuid.NewString()
	idempotent, err := repository.Append(ctx, retry)
	if err != nil || idempotent.Created || idempotent.Event.ID != input.ID {
		t.Fatalf("idempotent outbox retry: %+v err=%v", idempotent, err)
	}
	changed := retry
	changed.Payload = json.RawMessage(`{"schemaVersion":"tool.release.v1","releaseNo":2}`)
	if _, err := repository.Append(ctx, changed); !errors.Is(err, audit.ErrConflict) {
		t.Fatalf("idempotency contract drift error = %v", err)
	}
	wrongSchema := input
	wrongSchema.ID, wrongSchema.IdempotencyKey = uuid.NewString(), "wrong-schema"
	wrongSchema.Payload = json.RawMessage(`{"schemaVersion":"tool.release.v2"}`)
	if _, err := repository.Append(ctx, wrongSchema); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("payload schema mismatch error = %v", err)
	}

	concurrent := input
	concurrent.IdempotencyKey = "tool-release-concurrent:" + outboxAggregateID
	concurrent.OccurredAt = occurredAt.Add(time.Second)
	const workers = 8
	var wait sync.WaitGroup
	type result struct {
		value audit.AppendOutboxResult
		err   error
	}
	results := make(chan result, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value := concurrent
			value.ID = uuid.NewString()
			created, err := repository.Append(ctx, value)
			results <- result{value: created, err: err}
		}()
	}
	wait.Wait()
	close(results)
	createdCount := 0
	var canonicalID string
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent outbox append: %v", result.err)
		}
		if result.value.Created {
			createdCount++
		}
		if canonicalID == "" {
			canonicalID = result.value.Event.ID
		} else if result.value.Event.ID != canonicalID {
			t.Fatalf("concurrent outbox IDs diverged: %s/%s", canonicalID, result.value.Event.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent outbox created count = %d", createdCount)
	}

	byAggregate, err := repository.ListByAggregate(ctx, audit.ListOutboxInput{
		WorkspaceID: auditMigrationWorkspaceID, AggregateType: "TOOL",
		AggregateID: outboxAggregateID, Limit: 10,
	})
	if err != nil || len(byAggregate) != 2 {
		t.Fatalf("list outbox by aggregate: count=%d err=%v values=%+v", len(byAggregate), err, byAggregate)
	}
	byEvent, err := repository.ListByEventType(ctx, audit.ListOutboxInput{
		WorkspaceID: auditMigrationWorkspaceID, EventType: "tool.release.published", Limit: 10,
	})
	if err != nil || len(byEvent) != 2 {
		t.Fatalf("list outbox by event: count=%d err=%v", len(byEvent), err)
	}
	isolated, err := repository.ListByEventType(ctx, audit.ListOutboxInput{
		WorkspaceID: "a58f1f2e-7b5a-7c3d-8e9f-123456789099",
		EventType:   "tool.release.published", Limit: 10,
	})
	if err != nil || len(isolated) != 0 {
		t.Fatalf("outbox workspace isolation: %+v err=%v", isolated, err)
	}

}

func assertProbeAndOutboxCounts(t *testing.T, db *sql.DB, wantProbe, wantOutbox int) {
	t.Helper()
	var probe, outbox int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_business_probe`).Scan(&probe); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if probe != wantProbe || outbox != wantOutbox {
		t.Fatal(fmt.Sprintf("business/outbox counts=%d/%d want=%d/%d", probe, outbox, wantProbe, wantOutbox))
	}
}
