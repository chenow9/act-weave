package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/workspace"
)

const (
	toolPublishEditorID   = "098f1f2e-7b5a-7c3d-8e9f-1234567890d1"
	toolPublishOperatorID = "098f1f2e-7b5a-7c3d-8e9f-1234567890d2"
	toolPublishReleaseID  = "098f1f2e-7b5a-7c3d-8e9f-1234567890d3"
	toolPublishEventID    = "098f1f2e-7b5a-7c3d-8e9f-1234567890d4"
	toolPublishDeniedRel  = "098f1f2e-7b5a-7c3d-8e9f-1234567890d5"
	toolPublishDeniedEvt  = "098f1f2e-7b5a-7c3d-8e9f-1234567890d6"
	toolPublishFailedRel  = "098f1f2e-7b5a-7c3d-8e9f-1234567890d7"
	toolPublishFailedEvt  = "098f1f2e-7b5a-7c3d-8e9f-1234567890d8"
	toolPublishRaceRelOne = "098f1f2e-7b5a-7c3d-8e9f-1234567890e2"
	toolPublishRaceEvtOne = "098f1f2e-7b5a-7c3d-8e9f-1234567890e3"
	toolPublishRaceRelTwo = "098f1f2e-7b5a-7c3d-8e9f-1234567890e4"
	toolPublishRaceEvtTwo = "098f1f2e-7b5a-7c3d-8e9f-1234567890e5"
)

func TestPublishToolAtomicallyFreezesReleaseVersionAndEvent(t *testing.T) {
	repository, db := newRepositoryTest(t)
	insertToolPublishMembers(t, db)
	version := prepareTestedToolVersion(t, repository)
	eventWriter := newTestPublishEventWriter(t, db)
	service := newToolPublishService(t, repository, db, eventWriter)
	result, err := service.Publish(context.Background(), PublishToolInput{
		ReleaseID: toolPublishReleaseID, EventID: toolPublishEventID,
		WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: version.ID, CallableName: "orders_get", CallableDescription: "Get an order",
		PublishedBy: toolPublishEditorID, ExpectedVersionLock: version.LockVersion,
	})
	if err != nil {
		t.Fatalf("publish tested tool as editor: %v", err)
	}
	if result.Release.ID != toolPublishReleaseID || result.Release.ReleaseNo != 1 ||
		result.Release.SourceType != "TOOL_VERSION" || result.Release.SourceID != version.ID ||
		result.Release.Checksum != version.Checksum || result.Release.PublishedBy != toolPublishEditorID {
		t.Fatalf("unexpected published release: %+v", result.Release)
	}
	if string(result.Release.InputSchema) != string(version.InputSchema) ||
		string(result.Release.OutputSchema) != string(version.OutputSchema) ||
		result.Release.RiskLevel != version.RiskLevel ||
		result.Release.RequiresConfirmation != version.RequiresConfirmation {
		t.Fatalf("release did not freeze version contract: release=%+v version=%+v", result.Release, version)
	}
	if result.Version.LifecycleStatus != "PUBLISHED" || result.Version.PublishedAt == nil ||
		result.Version.LockVersion != version.LockVersion+1 || result.Test.ID != toolTestSuccessID {
		t.Fatalf("version/test publish state mismatch: %+v test=%+v", result.Version, result.Test)
	}
	capabilityValue, err := dbCapabilityActiveRelease(db, repositoryToolID)
	if err != nil || capabilityValue != toolPublishReleaseID {
		t.Fatalf("capability active release not switched: %q err=%v", capabilityValue, err)
	}
	var eventType, eventChecksum string
	var eventSchema, eventReleaseNo int
	if err := db.QueryRow(`
		SELECT event_type,checksum,release_no,schema_version
		FROM test_tool_publish_events WHERE id=$1
	`, toolPublishEventID).Scan(&eventType, &eventChecksum, &eventReleaseNo, &eventSchema); err != nil {
		t.Fatalf("read transactional publish event: %v", err)
	}
	if eventType != "tool.release.published" || eventChecksum != version.Checksum ||
		eventReleaseNo != 1 || eventSchema != 1 {
		t.Fatalf("unexpected transactional publish event: type=%s checksum=%s no=%d schema=%d",
			eventType, eventChecksum, eventReleaseNo, eventSchema)
	}
	if _, err := repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, version.ID, DraftUpdate{
		Spec: validDraftSpec(), LifecycleStatus: "DRAFT", UpdatedBy: toolPublishEditorID,
		ExpectedLockVersion: result.Version.LockVersion,
	}); !errors.Is(err, ErrImmutable) {
		t.Fatalf("published tool snapshot remained mutable: %v", err)
	}
	if _, err := db.Exec(`UPDATE capability_releases SET callable_description='changed' WHERE id=$1`, toolPublishReleaseID); err == nil {
		t.Fatal("published capability release remained mutable")
	}
}

func TestPublishToolRequiresEditorAndRollsBackEventFailure(t *testing.T) {
	repository, db := newRepositoryTest(t)
	insertToolPublishMembers(t, db)
	version := prepareTestedToolVersion(t, repository)
	eventWriter := newTestPublishEventWriter(t, db)
	service := newToolPublishService(t, repository, db, eventWriter)
	_, err := service.Publish(context.Background(), PublishToolInput{
		ReleaseID: toolPublishDeniedRel, EventID: toolPublishDeniedEvt,
		WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: version.ID, CallableName: "orders_get", PublishedBy: toolPublishOperatorID,
		ExpectedVersionLock: version.LockVersion,
	})
	if !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("expected operator publish denial, got %v", err)
	}
	assertNoPublishedToolState(t, db, version.ID)

	eventWriter.failAfterInsert = true
	_, err = service.Publish(context.Background(), PublishToolInput{
		ReleaseID: toolPublishFailedRel, EventID: toolPublishFailedEvt,
		WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: version.ID, CallableName: "orders_get", PublishedBy: toolPublishEditorID,
		ExpectedVersionLock: version.LockVersion,
	})
	if err == nil {
		t.Fatal("expected transactional event failure")
	}
	assertNoPublishedToolState(t, db, version.ID)
	var eventCount int
	if err := db.QueryRow(`SELECT count(*) FROM test_tool_publish_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("failed publish left event row behind: %d", eventCount)
	}
}

func TestHTTPToolConcurrentPublishAcceptance(t *testing.T) {
	repository, db := newRepositoryTest(t)
	insertToolPublishMembers(t, db)
	version := prepareTestedToolVersion(t, repository)
	eventWriter := newTestPublishEventWriter(t, db)
	service := newToolPublishService(t, repository, db, eventWriter)
	inputs := []PublishToolInput{
		{ReleaseID: toolPublishRaceRelOne, EventID: toolPublishRaceEvtOne},
		{ReleaseID: toolPublishRaceRelTwo, EventID: toolPublishRaceEvtTwo},
	}
	results := make(chan error, len(inputs))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, input := range inputs {
		input := input
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			input.WorkspaceID, input.CapabilityID = repositoryWorkspaceID, repositoryToolID
			input.VersionID, input.CallableName = version.ID, "orders_get"
			input.PublishedBy, input.ExpectedVersionLock = toolPublishEditorID, version.LockVersion
			_, err := service.Publish(context.Background(), input)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var succeeded, rejected int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrImmutable), errors.Is(err, ErrConflict):
			rejected++
		default:
			t.Fatalf("unexpected concurrent publish result: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("expected one atomic publish winner, success=%d rejected=%d", succeeded, rejected)
	}
	var releases, events int
	if err := db.QueryRow(`SELECT count(*) FROM capability_releases`).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM test_tool_publish_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if releases != 1 || events != 1 {
		t.Fatalf("concurrent publish produced partial/duplicate state: releases=%d events=%d", releases, events)
	}
}

func prepareTestedToolVersion(t *testing.T, repository *Repository) Version {
	t.Helper()
	return prepareTestedToolVersionWithDB(t, repository, nil)
}

func prepareTestedToolVersionWithDB(t *testing.T, repository *Repository, db *sql.DB) Version {
	t.Helper()
	_, version, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	if db != nil {
		// Latest schema requires permanent TOOL_TEST_PAYLOAD stored_objects rows.
		insertPermanentToolPayload(t, db, toolTestArtifactOneID, "TOOL_TEST_PAYLOAD")
	}
	artifacts := &memoryToolTestArtifacts{ids: []string{toolTestArtifactOneID}}
	testService := newToolTestService(t, repository, server.Client(), artifacts)
	input := toolTestRunInput(
		toolTestSuccessID, version.ID, server.URL, json.RawMessage(`{"orderId":"A-100"}`),
	)
	// Dual-mode injector path: treat as non-HTTP credential bypass for test fixture setup.
	input.Credential = execution.CredentialReference{
		WorkspaceID: repositoryWorkspaceID, BypassOutboundIdentity: true,
	}
	if _, err := testService.Run(context.Background(), input); err != nil {
		t.Fatalf("prepare passing tool test: %v", err)
	}
	tested, err := repository.GetVersion(context.Background(), repositoryWorkspaceID, repositoryToolID, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tested
}

func insertPermanentToolPayload(t *testing.T, db *sql.DB, objectID, kind string) {
	t.Helper()
	sha := strings.Repeat("a", 64)
	if _, err := db.Exec(`
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES ($1,$2,'tool-test-payloads',$3,$4,'application/json',2,$5,'test-key','SENSITIVE','PERMANENT','USER',$6)
		ON CONFLICT DO NOTHING
	`, objectID, repositoryWorkspaceID, "objects/"+objectID, kind, sha, repositoryOwnerID); err != nil {
		t.Fatalf("insert permanent payload %s: %v", objectID, err)
	}
}

func insertToolPublishMembers(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users(id,username,display_name) VALUES
		($1,'tool.publish.editor','Tool Publish Editor'),
		($2,'tool.publish.operator','Tool Publish Operator')
	`, toolPublishEditorID, toolPublishOperatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by) VALUES
		($1,$2,'EDITOR',$4),($1,$3,'OPERATOR',$4)
	`, repositoryWorkspaceID, toolPublishEditorID, toolPublishOperatorID, repositoryOwnerID); err != nil {
		t.Fatal(err)
	}
}

func newToolPublishService(
	t *testing.T,
	repository *Repository,
	db *sql.DB,
	events PublishEventWriter,
) *PublishService {
	t.Helper()
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaceRepository)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPublishService(repository, authorizer, events)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type testPublishEventWriter struct {
	failAfterInsert bool
}

func newTestPublishEventWriter(t *testing.T, db *sql.DB) *testPublishEventWriter {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE test_tool_publish_events(
			id UUID PRIMARY KEY,event_type TEXT NOT NULL,workspace_id UUID NOT NULL,
			capability_id UUID NOT NULL,tool_version_id UUID NOT NULL,tool_test_id UUID NOT NULL,
			release_id UUID NOT NULL,release_no INTEGER NOT NULL,checksum TEXT NOT NULL,
			published_by UUID NOT NULL,occurred_at TIMESTAMPTZ NOT NULL,schema_version INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	return &testPublishEventWriter{}
}

func (writer *testPublishEventWriter) AppendToolReleasePublished(
	ctx context.Context,
	tx *sql.Tx,
	event ToolReleasePublishedEvent,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO test_tool_publish_events(
			id,event_type,workspace_id,capability_id,tool_version_id,tool_test_id,
			release_id,release_no,checksum,published_by,occurred_at,schema_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, event.ID, event.Type, event.WorkspaceID, event.CapabilityID, event.ToolVersionID,
		event.ToolTestID, event.ReleaseID, event.ReleaseNo, event.Checksum,
		event.PublishedBy, event.OccurredAt, event.SchemaVersion); err != nil {
		return err
	}
	if writer.failAfterInsert {
		return errors.New("forced event append failure")
	}
	return nil
}

func assertNoPublishedToolState(t *testing.T, db *sql.DB, versionID string) {
	t.Helper()
	var releaseCount, eventCount int
	var lifecycle string
	var activeRelease sql.NullString
	if err := db.QueryRow(`SELECT count(*) FROM capability_releases`).Scan(&releaseCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM test_tool_publish_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT lifecycle_status FROM tool_versions WHERE id=$1`, versionID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, repositoryToolID).Scan(&activeRelease); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 0 || eventCount != 0 || lifecycle != "TESTED" || activeRelease.Valid {
		t.Fatalf("publish state was not atomic: releases=%d events=%d lifecycle=%s active=%v",
			releaseCount, eventCount, lifecycle, activeRelease)
	}
}

func dbCapabilityActiveRelease(db *sql.DB, capabilityID string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT active_release_id FROM capabilities WHERE id=$1`, capabilityID).Scan(&value)
	return value, err
}
