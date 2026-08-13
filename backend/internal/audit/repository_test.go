package audit_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/database/dbtest"
)

const auditPayloadObjectID = "a48f1f2e-7b5a-7c3d-8e9f-123456789001"

func TestInsertAuditEventIsRedactedScopedAndInsertOnly(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 21 || version.Dirty {
		t.Fatalf("audit payload migration = %+v", version)
	}
	db := testDatabase.Open(t)
	insertAuditMigrationFixtures(t, db)
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
		 id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		 encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,'actweave-audit-packages',$3,'AUDIT_EVENT_PAYLOAD',
		 'application/json',1024,$4,'audit-payload-key-v1','RESTRICTED','PERMANENT','USER',$5)
	`, auditPayloadObjectID, auditMigrationWorkspaceID,
		auditMigrationWorkspaceID+"/audit-event/"+auditPayloadObjectID,
		strings.Repeat("e", 64), auditMigrationUserID); err != nil {
		t.Fatal(err)
	}
	builder, _ := audit.NewBuilder(256)
	event, err := builder.Build(audit.BuildInput{
		ID: auditMigrationEventID, WorkspaceID: auditMigrationWorkspaceID,
		ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Owner",
		Action: "workspace.member.changed", ResourceType: "WORKSPACE_MEMBER",
		ResourceID: auditMigrationUserID, Result: "SUCCESS", RequestID: "request-insert-1",
		TraceID: "trace-insert-1", SourceIP: "2001:db8::10", UserAgent: "audit-repository-test",
		Before: map[string]any{"role": "VIEWER"}, After: map[string]any{"role": "EDITOR"},
		Metadata:        map[string]any{"detail": strings.Repeat("safe-overflow-", 100)},
		PayloadObjectID: auditPayloadObjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := audit.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.Insert(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != event.ID || created.WorkspaceID != event.WorkspaceID ||
		created.PayloadObjectID != auditPayloadObjectID || created.SourceIP.String() != "2001:db8::10" ||
		!strings.Contains(string(created.Changes), `"overflow": true`) &&
			!strings.Contains(string(created.Changes), `"overflow":true`) {
		t.Fatalf("inserted audit event mismatch: %+v", created)
	}
	if _, err := repository.Insert(context.Background(), event); !errors.Is(err, audit.ErrConflict) {
		t.Fatalf("duplicate audit event error = %v", err)
	}
	unsafe := event
	unsafe.ID = "a48f1f2e-7b5a-7c3d-8e9f-123456789003"
	unsafe.OccurredAt = time.Now().UTC()
	unsafe.Metadata = []byte(`{"password":"must-not-enter-audit"}`)
	if _, err := repository.Insert(context.Background(), unsafe); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unsafe direct audit insert error = %v", err)
	}

	otherWorkspace := event
	otherWorkspace.ID = "a48f1f2e-7b5a-7c3d-8e9f-123456789002"
	otherWorkspace.WorkspaceID = "a48f1f2e-7b5a-7c3d-8e9f-123456789099"
	otherWorkspace.OccurredAt = time.Now().UTC()
	if _, err := repository.Insert(context.Background(), otherWorkspace); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unknown workspace audit error = %v", err)
	}

	repositoryType := reflect.TypeOf(repository)
	for _, forbidden := range []string{"Update", "Delete", "Upsert"} {
		if _, exists := repositoryType.MethodByName(forbidden); exists {
			t.Fatalf("audit repository exposes %s", forbidden)
		}
	}
	if _, err := db.Exec(`UPDATE audit_events SET metadata='{}' WHERE id=$1`, event.ID); err == nil {
		t.Fatal("database allowed audit event update")
	}

}
