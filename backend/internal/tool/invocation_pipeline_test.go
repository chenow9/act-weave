package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/toolruntime"
	"actweave/backend/internal/workspace"
)

const toolInvocationID = "098f1f2e-7b5a-7c3d-8e9f-1234567890e1"

func TestInvokeResolvesPublishedReleaseAndExplicitConnectionNotLatestDraft(t *testing.T) {
	repository, db := newDualModeRepositoryTest(t)
	insertToolPublishMembers(t, db)
	publishedSource := prepareTestedToolVersionWithDB(t, repository, db)
	events := newTestPublishEventWriter(t, db)
	publishService := newToolPublishService(t, repository, db, events)
	published, err := publishService.Publish(context.Background(), PublishToolInput{
		ReleaseID: toolPublishReleaseID, EventID: toolPublishEventID,
		WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: publishedSource.ID, CallableName: "orders_get", PublishedBy: toolPublishEditorID,
		ExpectedVersionLock: publishedSource.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	newDraft, err := repository.CreateDraftFromPublished(context.Background(), repositoryWorkspaceID,
		repositoryToolID, published.Version.ID, repositoryVersionTwoID, toolPublishEditorID)
	if err != nil {
		t.Fatal(err)
	}
	draftSpec := validDraftSpec()
	draftSpec.ActionConfig = json.RawMessage(`{"method":"GET","path":"/draft-only/{orderId}"}`)
	if _, err := repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID,
		newDraft.ID, DraftUpdate{Spec: draftSpec, LifecycleStatus: "REVIEW",
			UpdatedBy: toolPublishEditorID, ExpectedLockVersion: newDraft.LockVersion}); err != nil {
		t.Fatal(err)
	}

	var mutex sync.Mutex
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		receivedPath = request.URL.EscapedPath()
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"published-release"}`))
	}))
	defer upstream.Close()
	endpointConfig, _ := json.Marshal(map[string]any{
		"baseUrl": upstream.URL,
		"egress":  map[string]any{"allowedCIDRs": []string{"127.0.0.0/8"}},
	})
	if _, err := db.Exec(`UPDATE capability_providers SET endpoint_config=$2 WHERE id=$1`,
		repositoryProviderID, endpointConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE service_connections
		SET status='VERIFIED',policy='{}',migration_state='NONE',
			outbound_identity=$3::jsonb,outbound_identity_policy_version=1
		WHERE id IN ($1,$2)
	`, repositoryConnectionID, repositoryConnectionTwoID, dualModeConnectionIdentity()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE capability_providers SET driver_config=$2::jsonb WHERE id=$1
	`, repositoryProviderID, dualModeDriverConfig()); err != nil {
		t.Fatal(err)
	}

	pipeline, recorder := newPublishedInvocationPipeline(t, db, upstream.Client())
	result, err := pipeline.Invoke(context.Background(), execution.InvokeRequest{
		InvocationID: toolInvocationID, WorkspaceID: repositoryWorkspaceID,
		CapabilityID: repositoryToolID, ReleaseID: toolPublishReleaseID,
		ActorType: "USER", ActorID: toolPublishEditorID, TraceID: "published-release-trace",
		ExplicitConnectionID: repositoryConnectionTwoID,
		Input:                json.RawMessage(`{"orderId":"A-100"}`),
	})
	if err != nil {
		t.Fatalf("invoke exact published release: %v", err)
	}
	if result.Attempts != 1 || string(result.Output) != `{"status":"published-release"}` {
		t.Fatalf("unexpected published invocation result: %+v", result)
	}
	mutex.Lock()
	path := receivedPath
	mutex.Unlock()
	if path != "/orders/A-100" {
		t.Fatalf("invocation read mutable latest draft instead of release snapshot: %s", path)
	}
	if recorder.finished.ReleaseID != toolPublishReleaseID ||
		recorder.finished.ToolVersionID != published.Version.ID ||
		recorder.finished.ConnectionID != repositoryConnectionTwoID ||
		recorder.finished.RetentionMode != execution.InvocationRetentionMode {
		t.Fatalf("invocation record did not retain resolved release/connection: %+v", recorder.finished)
	}
}

// Workflow trial / direct invoke often have no ExplicitConnectionID. When the
// published version left default_connection_id NULL (OpenAPI generate without
// connectionId) but tools.default_connection_id is set, resolve must still work.
func TestInvokeFallsBackToToolDefaultConnection(t *testing.T) {
	repository, db := newDualModeRepositoryTest(t)
	insertToolPublishMembers(t, db)
	publishedSource := prepareTestedToolVersionWithDB(t, repository, db)
	// Simulate generate-without-connection: version default cleared, tool default kept.
	if _, err := db.Exec(`
		UPDATE tool_versions SET default_connection_id=NULL WHERE id=$1
	`, publishedSource.ID); err != nil {
		t.Fatal(err)
	}
	events := newTestPublishEventWriter(t, db)
	publishService := newToolPublishService(t, repository, db, events)
	published, err := publishService.Publish(context.Background(), PublishToolInput{
		ReleaseID: toolPublishReleaseID, EventID: toolPublishEventID,
		WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: publishedSource.ID, CallableName: "orders_get", PublishedBy: toolPublishEditorID,
		ExpectedVersionLock: publishedSource.LockVersion,
	})
	if err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	endpointConfig, _ := json.Marshal(map[string]any{
		"baseUrl": upstream.URL,
		"egress":  map[string]any{"allowedCIDRs": []string{"127.0.0.0/8"}},
	})
	if _, err := db.Exec(`UPDATE capability_providers SET endpoint_config=$2 WHERE id=$1`,
		repositoryProviderID, endpointConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE service_connections
		SET status='VERIFIED',policy='{}',migration_state='NONE',
			outbound_identity=$2::jsonb,outbound_identity_policy_version=1
		WHERE id=$1
	`, repositoryConnectionID, dualModeConnectionIdentity()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE capability_providers SET driver_config=$2::jsonb WHERE id=$1`,
		repositoryProviderID, dualModeDriverConfig()); err != nil {
		t.Fatal(err)
	}

	pipeline, recorder := newPublishedInvocationPipeline(t, db, upstream.Client())
	result, err := pipeline.Invoke(context.Background(), execution.InvokeRequest{
		InvocationID: toolInvocationID, WorkspaceID: repositoryWorkspaceID,
		CapabilityID: repositoryToolID, ReleaseID: published.Release.ID,
		ActorType: "USER", ActorID: toolPublishEditorID, TraceID: "tool-default-conn",
		Input: json.RawMessage(`{"orderId":"A-100"}`),
	})
	if err != nil {
		t.Fatalf("invoke with tool-level default connection: %v", err)
	}
	if result.Attempts != 1 || string(result.Output) != `{"ok":true}` {
		t.Fatalf("unexpected result: %+v", result)
	}
	if recorder.finished.ConnectionID != repositoryConnectionID {
		t.Fatalf("expected tool default connection %s, got %s",
			repositoryConnectionID, recorder.finished.ConnectionID)
	}
}

func newPublishedInvocationPipeline(t *testing.T, db *sql.DB, client *http.Client) (*execution.InvocationPipeline, *capturingInvocationRecorder) {
	t.Helper()
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	authorizationService, err := authz.NewService(workspaceRepository)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewWorkspaceInvocationAuthorizer(authorizationService)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewInvocationResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := toolruntime.NewExecutorRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	// Dual-mode fixtures use REQUEST_PASSTHROUGH; production injects via Vault.
	// This suite asserts resolution/routing of published releases, not credential
	// acquisition — callback-only injector does not pull Secrets/Vault/Broker.
	injector := dualModeTestInjector{}
	recorder := &capturingInvocationRecorder{}
	pipeline, err := execution.NewInvocationPipeline(
		authorizer, resolver, allowConfirmation{}, &memoryInvocationIdempotency{},
		allowInvocationLimiter{}, injector, registry, recorder,
		execution.RetryWaiterFunc(func(context.Context, int) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline, recorder
}

type allowConfirmation struct{}

func (allowConfirmation) VerifyInvocationConfirmation(context.Context, execution.ConfirmationCheck) error {
	return nil
}

type memoryInvocationIdempotency struct{}

func (*memoryInvocationIdempotency) BeginInvocation(context.Context, execution.IdempotencyRequest) (execution.IdempotencyDecision, error) {
	return execution.IdempotencyDecision{State: execution.IdempotencyNew}, nil
}
func (*memoryInvocationIdempotency) CompleteInvocation(context.Context, execution.IdempotencyRequest, execution.InvocationResult) error {
	return nil
}
func (*memoryInvocationIdempotency) FailInvocation(context.Context, execution.IdempotencyRequest, string) error {
	return nil
}

type allowInvocationLimiter struct{}

func (allowInvocationLimiter) AllowInvocation(context.Context, execution.LimitRequest) error {
	return nil
}

// dualModeTestInjector is test-only: clones the connection without credentials.
// Production uses execution.OutboundIdentityInjector (Broker/Vault after confirmation).
type dualModeTestInjector struct{}

func (dualModeTestInjector) WithInjectedConnection(
	_ context.Context,
	connection execution.ConnectionSnapshot,
	_ execution.CredentialReference,
	invoke func(execution.ConnectionSnapshot) error,
) error {
	if invoke == nil {
		return errors.New("invoke required")
	}
	return invoke(connection)
}

type unusedSecretSource struct{}

func (unusedSecretSource) WithActiveSecret(context.Context, string, string, func([]byte) error) error {
	return errors.New("NONE auth must not resolve a secret")
}

type capturingInvocationRecorder struct {
	started  execution.InvocationRecord
	finished execution.InvocationRecord
}

func (recorder *capturingInvocationRecorder) InvocationStarted(_ context.Context, record execution.InvocationRecord) error {
	recorder.started = record
	return nil
}
func (recorder *capturingInvocationRecorder) InvocationFinished(_ context.Context, record execution.InvocationRecord) error {
	recorder.finished = record
	return nil
}
