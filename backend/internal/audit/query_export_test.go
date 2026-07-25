package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

const auditQueryPayloadID = "b38f1f2e-7b5a-7c3d-8e9f-123456789001"

func TestQueryFiltersTrimsFieldsAndProtectsPayload(t *testing.T) {
	db, recorder, objects := newAuditQueryExportFixture(t)
	ctx := audit.WithRequestContext(context.Background(), audit.RequestContext{
		RequestID: "request-query-1", TraceID: "trace-query-1",
		SourceIP: "203.0.113.31", UserAgent: "audit-query-test",
	})
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	for index, input := range []audit.ManagementEventInput{
		{
			EventID: uuid.NewString(), OccurredAt: base, WorkspaceID: auditMigrationWorkspaceID,
			ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Owner",
			Action: "workspace.member.changed", ResourceType: "WORKSPACE_MEMBER",
			ResourceID: uuid.NewString(), Result: "SUCCESS", PayloadObjectID: auditQueryPayloadID,
			Metadata: map[string]any{"sequence": 1},
		},
		{
			EventID: uuid.NewString(), OccurredAt: base.Add(time.Second), WorkspaceID: auditMigrationWorkspaceID,
			ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Owner",
			Action: "configuration.changed", ResourceType: "MODEL_CONFIG",
			ResourceID: uuid.NewString(), Result: "FAILURE", Metadata: map[string]any{"sequence": 2},
		},
		{
			EventID: uuid.NewString(), OccurredAt: base.Add(2 * time.Second), WorkspaceID: auditMigrationWorkspaceID,
			ActorType: "USER", ActorID: auditMigrationUserID, ActorDisplay: "Owner",
			Action: "authorization.denied", ResourceType: "WORKSPACE",
			ResourceID: auditMigrationWorkspaceID, Result: "DENIED", Metadata: map[string]any{"sequence": 3},
		},
	} {
		if _, err := recorder.Record(ctx, input); err != nil {
			t.Fatalf("record query fixture %d: %v", index, err)
		}
	}
	queries, err := audit.NewQueryService(db, objects)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := queries.Query(ctx, workspace.RoleViewer, audit.QueryInput{
		WorkspaceID: auditMigrationWorkspaceID, ActorType: "user",
		ActorID: auditMigrationUserID, Action: "workspace.member.changed",
		Results: []string{"success"}, RequestID: "request-query-1", TraceID: "trace-query-1",
		OccurredFrom: base.Add(-time.Second), OccurredUntil: base.Add(time.Second), Limit: 10,
	})
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filtered audit query: %+v err=%v", filtered, err)
	}
	if filtered[0].SourceIP.IsValid() || filtered[0].UserAgent != "" || filtered[0].PayloadObjectID != "" {
		t.Fatalf("viewer received sensitive audit fields: %+v", filtered[0])
	}
	if _, err := queries.Query(ctx, workspace.RoleViewer, audit.QueryInput{
		WorkspaceID: auditMigrationWorkspaceID, IncludeSensitive: true,
	}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("viewer sensitive query error = %v", err)
	}
	sensitive, err := queries.Query(ctx, workspace.RoleAdmin, audit.QueryInput{
		WorkspaceID: auditMigrationWorkspaceID, Action: "workspace.member.changed",
		IncludeSensitive: true,
	})
	if err != nil || len(sensitive) != 1 || sensitive[0].SourceIP.String() != "203.0.113.31" ||
		sensitive[0].UserAgent != "audit-query-test" || sensitive[0].PayloadObjectID != auditQueryPayloadID {
		t.Fatalf("administrator sensitive audit query: %+v err=%v", sensitive, err)
	}
	if _, err := queries.OpenPayload(ctx, auditMigrationWorkspaceID, sensitive[0].ID,
		auditMigrationUserID, workspace.RoleEditor); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("editor payload error = %v", err)
	}
	opened, err := queries.OpenPayload(ctx, auditMigrationWorkspaceID, sensitive[0].ID,
		auditMigrationUserID, workspace.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if string(payload) != `{"redacted":"detail"}` {
		t.Fatalf("opened audit payload = %s", payload)
	}
	page, err := queries.Query(ctx, workspace.RoleViewer, audit.QueryInput{
		WorkspaceID: auditMigrationWorkspaceID, Limit: 2,
	})
	if err != nil || len(page) != 2 {
		t.Fatalf("first audit query page: %+v err=%v", page, err)
	}
	next, err := queries.Query(ctx, workspace.RoleViewer, audit.QueryInput{
		WorkspaceID: auditMigrationWorkspaceID, Limit: 2,
		BeforeOccurredAt: page[1].OccurredAt, BeforeID: page[1].ID,
	})
	if err != nil || len(next) != 1 || next[0].ID == page[0].ID || next[0].ID == page[1].ID {
		t.Fatalf("second audit query page: %+v err=%v", next, err)
	}
	isolated, err := queries.Query(ctx, workspace.RoleOwner, audit.QueryInput{
		WorkspaceID: "b38f1f2e-7b5a-7c3d-8e9f-123456789099", IncludeSensitive: true,
	})
	if err != nil || len(isolated) != 0 {
		t.Fatalf("cross-workspace audit query: %+v err=%v", isolated, err)
	}
}

func TestExportIsAsynchronousAuditedAndOwnerAdminOnly(t *testing.T) {
	db, recorder, objects := newAuditQueryExportFixture(t)
	ctx := audit.WithRequestContext(context.Background(), audit.RequestContext{
		RequestID: "request-export-1", TraceID: "trace-export-1",
	})
	if _, err := recorder.Record(ctx, phaseOneManagementInput(
		audit.ActionAgentChanged, "AGENT", "SUCCESS",
	)); err != nil {
		t.Fatal(err)
	}
	queries, _ := audit.NewQueryService(db, objects)
	exports, err := audit.NewExportService(db, queries, recorder, objects)
	if err != nil {
		t.Fatal(err)
	}
	exportID := uuid.NewString()
	request := audit.CreateExportInput{
		ID: exportID, WorkspaceID: auditMigrationWorkspaceID,
		RequestedBy: auditMigrationUserID, Role: workspace.RoleAdmin,
		Filter:    audit.QueryInput{Results: []string{"SUCCESS"}},
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if _, err := exports.Create(ctx, audit.CreateExportInput{
		ID: uuid.NewString(), WorkspaceID: auditMigrationWorkspaceID,
		RequestedBy: auditMigrationUserID, Role: workspace.RoleEditor,
		ExpiresAt: request.ExpiresAt,
	}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("editor export create error = %v", err)
	}
	created, err := exports.Create(ctx, request)
	if err != nil || created.Status != "PENDING" || created.ObjectID != "" {
		t.Fatalf("create audit export: %+v err=%v", created, err)
	}
	processed, err := exports.ProcessNext(ctx)
	if err != nil || !processed {
		t.Fatalf("process audit export: processed=%v err=%v", processed, err)
	}
	if processed, err := exports.ProcessNext(ctx); err != nil || processed {
		t.Fatalf("empty audit export poll: processed=%v err=%v", processed, err)
	}
	completed, err := exports.Get(ctx, auditMigrationWorkspaceID, exportID, workspace.RoleOwner)
	if err != nil || completed.Status != "SUCCEEDED" || completed.ObjectID != exportID ||
		completed.CompletedAt == nil {
		t.Fatalf("completed audit export: %+v err=%v", completed, err)
	}
	if _, err := exports.Get(ctx, auditMigrationWorkspaceID, exportID, workspace.RoleViewer); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("viewer export get error = %v", err)
	}
	if _, err := exports.Get(ctx, "b38f1f2e-7b5a-7c3d-8e9f-123456789099",
		exportID, workspace.RoleOwner); !errors.Is(err, audit.ErrNotFound) {
		t.Fatalf("cross-workspace export get error = %v", err)
	}
	if !bytes.Contains(objects.bodies[exportID], []byte(`"Action":"agent.changed"`)) ||
		bytes.Contains(objects.bodies[exportID], []byte("must-never-appear")) {
		t.Fatalf("audit export content = %s", objects.bodies[exportID])
	}
	signed, err := exports.DownloadURL(ctx, auditMigrationWorkspaceID, exportID,
		auditMigrationUserID, workspace.RoleAdmin, 5*time.Minute)
	if err != nil || signed.Host != "downloads.example.test" || objects.lastTTL != 5*time.Minute {
		t.Fatalf("audit export download: url=%v ttl=%v err=%v", signed, objects.lastTTL, err)
	}
	if _, err := exports.DownloadURL(ctx, auditMigrationWorkspaceID, exportID,
		auditMigrationUserID, workspace.RoleViewer, time.Minute); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("viewer download error = %v", err)
	}
	var requested, completedCount int
	if err := db.QueryRow(`SELECT
		count(*) FILTER (WHERE action='audit.export.requested'),
		count(*) FILTER (WHERE action='audit.export.completed')
		FROM audit_events WHERE workspace_id=$1 AND request_id='request-export-1' AND trace_id='trace-export-1'`,
		auditMigrationWorkspaceID).Scan(&requested, &completedCount); err != nil {
		t.Fatal(err)
	}
	if requested != 1 || completedCount != 1 {
		t.Fatalf("audit export lifecycle events requested/completed=%d/%d", requested, completedCount)
	}
}

func TestExportFailureStoresStableCodeAndAuditEvent(t *testing.T) {
	db, recorder, objects := newAuditQueryExportFixture(t)
	objects.failPut = true
	queries, _ := audit.NewQueryService(db, objects)
	exports, _ := audit.NewExportService(db, queries, recorder, objects)
	ctx := audit.WithRequestContext(context.Background(), audit.RequestContext{
		RequestID: "request-export-failure", TraceID: "trace-export-failure",
	})
	created, err := exports.Create(ctx, audit.CreateExportInput{
		ID: uuid.NewString(), WorkspaceID: auditMigrationWorkspaceID,
		RequestedBy: auditMigrationUserID, Role: workspace.RoleOwner,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := exports.ProcessNext(ctx)
	if !processed || err == nil || !strings.Contains(err.Error(), "simulated object failure") {
		t.Fatalf("failed export processing: processed=%v err=%v", processed, err)
	}
	failed, err := exports.Get(ctx, auditMigrationWorkspaceID, created.ID, workspace.RoleOwner)
	if err != nil || failed.Status != "FAILED" || failed.ErrorCode != "AUDIT_EXPORT_STORAGE_FAILED" {
		t.Fatalf("failed audit export: %+v err=%v", failed, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events
		WHERE action='audit.export.failed' AND result='FAILURE'
		AND request_id='request-export-failure' AND trace_id='trace-export-failure'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("failed export audit event count = %d", count)
	}
}

type auditObjectStore struct {
	repository *storedobject.Repository
	bodies     map[string][]byte
	failPut    bool
	lastTTL    time.Duration
}

func (store *auditObjectStore) Put(
	ctx context.Context,
	input storedobject.PutInput,
) (storedobject.StoredObject, error) {
	if store.failPut {
		return storedobject.StoredObject{}, errors.New("simulated object failure containing no credentials")
	}
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	created, err := store.repository.Create(ctx, storedobject.CreateInput{
		ID: input.ID, WorkspaceID: input.WorkspaceID,
		Bucket: "actweave-audit-packages", ObjectKey: input.WorkspaceID + "/audit-export/" + input.ID,
		Kind: input.Kind, ContentType: input.ContentType, SizeBytes: input.SizeBytes,
		SHA256: input.SHA256, Classification: input.Classification,
		RetentionMode: input.RetentionMode, RetentionUntil: input.RetentionUntil,
		CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID,
	})
	if err == nil {
		store.bodies[input.ID] = append([]byte(nil), body...)
	}
	return created, err
}

func (store *auditObjectStore) Open(
	ctx context.Context,
	request storedobject.ReadRequest,
) (storedobject.OpenedObject, error) {
	metadata, err := store.repository.Get(ctx, request.WorkspaceID, request.ObjectID)
	if err != nil {
		return storedobject.OpenedObject{}, err
	}
	body, exists := store.bodies[request.ObjectID]
	if !exists {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	return storedobject.OpenedObject{Metadata: metadata, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (store *auditObjectStore) PresignDownload(
	_ context.Context,
	request storedobject.ReadRequest,
	ttl time.Duration,
) (*url.URL, error) {
	if _, exists := store.bodies[request.ObjectID]; !exists {
		return nil, storedobject.ErrNotFound
	}
	store.lastTTL = ttl
	return url.Parse("https://downloads.example.test/" + request.ObjectID)
}

func newAuditQueryExportFixture(t *testing.T) (*sql.DB, *audit.Recorder, *auditObjectStore) {
	t.Helper()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	insertAuditMigrationFixtures(t, db)
	objectsRepository, err := storedobject.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	objects := &auditObjectStore{repository: objectsRepository, bodies: map[string][]byte{
		auditQueryPayloadID: []byte(`{"redacted":"detail"}`),
	}}
	if _, err := objectsRepository.Create(context.Background(), storedobject.CreateInput{
		ID: auditQueryPayloadID, WorkspaceID: auditMigrationWorkspaceID,
		Bucket:    "actweave-audit-packages",
		ObjectKey: auditMigrationWorkspaceID + "/audit-event/" + auditQueryPayloadID,
		Kind:      storedobject.KindAuditEventPayload, ContentType: "application/json",
		SizeBytes: int64(len(objects.bodies[auditQueryPayloadID])), SHA256: strings.Repeat("f", 64),
		EncryptionKeyID: "audit-query-key-v1", Classification: storedobject.ClassificationRestricted,
		RetentionMode: storedobject.RetentionPermanent,
		CreatedByType: storedobject.CreatorUser, CreatedByID: auditMigrationUserID,
	}); err != nil {
		t.Fatal(err)
	}
	repository, _ := audit.NewRepository(db)
	outbox, _ := audit.NewOutboxRepository(db)
	builder, _ := audit.NewBuilder(0, "must-never-appear")
	recorder, err := audit.NewRecorder(repository, outbox, builder)
	if err != nil {
		t.Fatal(err)
	}
	return db, recorder, objects
}
