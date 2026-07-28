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

	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/toolruntime"
)

const (
	toolTestSuccessID             = "098f1f2e-7b5a-7c3d-8e9f-1234567890b4"
	toolTestFailureID             = "098f1f2e-7b5a-7c3d-8e9f-1234567890b5"
	toolTestInputFailureID        = "098f1f2e-7b5a-7c3d-8e9f-1234567890b6"
	toolTestDriftID               = "098f1f2e-7b5a-7c3d-8e9f-1234567890b7"
	toolTestCredentialID          = "098f1f2e-7b5a-7c3d-8e9f-1234567890b8"
	toolTestPassthroughID         = "098f1f2e-7b5a-7c3d-8e9f-1234567890b9"
	toolTestArtifactOneID         = "098f1f2e-7b5a-7c3d-8e9f-1234567890c1"
	toolTestArtifactTwoID         = "098f1f2e-7b5a-7c3d-8e9f-1234567890c2"
	toolTestArtifactThreeID       = "098f1f2e-7b5a-7c3d-8e9f-1234567890c3"
	toolTestArtifactFourID        = "098f1f2e-7b5a-7c3d-8e9f-1234567890c4"
	toolTestArtifactFiveID        = "098f1f2e-7b5a-7c3d-8e9f-1234567890c5"
	toolTestPassthroughArtifactID = "098f1f2e-7b5a-7c3d-8e9f-1234567890c6"
)

func TestToolTestRecordsExactVersionAndLatestPassingAttempt(t *testing.T) {
	repository, db := newRepositoryTest(t)
	create := validCreateInput()
	create.Draft.OutputSchema = json.RawMessage(`{
		"type":"object","required":["status"],
		"properties":{"status":{"type":"string"}}
	}`)
	_, version, err := repository.Create(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	responseMode := "success"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/orders/customer-sensitive-order" {
			t.Errorf("unexpected tested request path: %s", request.URL.EscapedPath())
		}
		writer.Header().Set("Content-Type", "application/json")
		if responseMode == "success" {
			_, _ = writer.Write([]byte(`{"status":"ok","private":"upstream-sensitive-value"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"private":"upstream-sensitive-failure"}`))
	}))
	defer server.Close()
	artifacts := &memoryToolTestArtifacts{db: db, ids: []string{
		toolTestArtifactOneID, toolTestArtifactTwoID, toolTestArtifactThreeID,
	}}
	service := newToolTestService(t, repository, server.Client(), artifacts)

	succeeded, err := service.Run(context.Background(), toolTestRunInput(
		toolTestSuccessID, version.ID, server.URL, json.RawMessage(`{"orderId":"customer-sensitive-order"}`),
	))
	if err != nil {
		t.Fatalf("run successful tool test: %v", err)
	}
	if succeeded.Status != "SUCCEEDED" || !succeeded.ConnectivityPassed ||
		!succeeded.ResponseSchemaPassed || !succeeded.ErrorMappingPassed ||
		!succeeded.RuntimePolicyPassed || succeeded.ErrorCode != nil ||
		succeeded.RawObjectID == nil || *succeeded.RawObjectID != toolTestArtifactOneID {
		t.Fatalf("unexpected successful test record: %+v", succeeded)
	}
	for _, summary := range []json.RawMessage{succeeded.RequestSummary, succeeded.ResponseSummary} {
		for _, forbidden := range []string{"customer-sensitive-order", "upstream-sensitive-value"} {
			if strings.Contains(string(summary), forbidden) {
				t.Fatalf("tool test summary leaked %q: %s", forbidden, summary)
			}
		}
	}
	testedVersion, err := repository.GetVersion(context.Background(), repositoryWorkspaceID, repositoryToolID, version.ID)
	if err != nil || testedVersion.LifecycleStatus != "TESTED" || testedVersion.LockVersion != 2 {
		t.Fatalf("successful test did not mark exact version tested: %+v err=%v", testedVersion, err)
	}
	latest, err := repository.LatestSuccessfulTest(context.Background(), repositoryWorkspaceID, version.ID)
	if err != nil || latest.ID != succeeded.ID {
		t.Fatalf("resolve latest passing test: %+v err=%v", latest, err)
	}

	responseMode = "failure"
	failed, err := service.Run(context.Background(), toolTestRunInput(
		toolTestFailureID, version.ID, server.URL, json.RawMessage(`{"orderId":"customer-sensitive-order"}`),
	))
	if err != nil {
		t.Fatalf("record response schema failure: %v", err)
	}
	if failed.Status != "FAILED" || failed.ErrorCode == nil || *failed.ErrorCode != TestErrorResponseSchema ||
		!failed.ConnectivityPassed || failed.ResponseSchemaPassed || !failed.ErrorMappingPassed || !failed.RuntimePolicyPassed {
		t.Fatalf("unexpected failed test record: %+v", failed)
	}
	if strings.Contains(string(failed.ResponseSummary), "upstream-sensitive-failure") {
		t.Fatalf("failed test summary leaked response: %s", failed.ResponseSummary)
	}
	if _, err := repository.LatestSuccessfulTest(context.Background(), repositoryWorkspaceID, version.ID); !errors.Is(err, ErrNoPassingTest) {
		t.Fatalf("later failure should invalidate older success, got %v", err)
	}

	invalidInput, err := service.Run(context.Background(), toolTestRunInput(
		toolTestInputFailureID, version.ID, server.URL, json.RawMessage(`{"different":"value"}`),
	))
	if err != nil {
		t.Fatalf("record input schema failure: %v", err)
	}
	if invalidInput.Status != "FAILED" || invalidInput.ConnectivityPassed ||
		invalidInput.ErrorCode == nil || *invalidInput.ErrorCode != TestErrorInputSchema {
		t.Fatalf("unexpected input validation record: %+v", invalidInput)
	}
	var storedCount int
	if err := db.QueryRow(`SELECT count(*) FROM tool_tests WHERE workspace_id=$1 AND tool_version_id=$2`,
		repositoryWorkspaceID, version.ID).Scan(&storedCount); err != nil {
		t.Fatal(err)
	}
	if storedCount != 3 {
		t.Fatalf("expected three exact-version test attempts, got %d", storedCount)
	}
	storedArtifacts := artifacts.snapshot()
	if len(storedArtifacts) != 3 || storedArtifacts[0].RetentionMode != TestRetentionPermanent ||
		!strings.Contains(string(storedArtifacts[0].Request), "customer-sensitive-order") ||
		!strings.Contains(string(storedArtifacts[1].Response), "upstream-sensitive-failure") {
		t.Fatalf("raw test artifacts were not permanently retained: %+v", storedArtifacts)
	}
}

func TestToolTestRejectsConcurrentDraftDrift(t *testing.T) {
	repository, db := newRepositoryTest(t)
	_, version, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	requestArrived := make(chan struct{})
	releaseResponse := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseResponse) }) }
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(requestArrived)
		<-releaseResponse
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	defer release()
	artifacts := &memoryToolTestArtifacts{db: db, ids: []string{toolTestArtifactFourID}}
	service := newToolTestService(t, repository, server.Client(), artifacts)
	errorChannel := make(chan error, 1)
	go func() {
		_, err := service.Run(context.Background(), toolTestRunInput(
			toolTestDriftID, version.ID, server.URL, json.RawMessage(`{"orderId":"A-100"}`),
		))
		errorChannel <- err
	}()
	<-requestArrived
	changedSpec := validDraftSpec()
	changedSpec.ActionConfig = json.RawMessage(`{"method":"GET","path":"/changed/{orderId}"}`)
	if _, err := repository.UpdateDraft(context.Background(), repositoryWorkspaceID, repositoryToolID, version.ID, DraftUpdate{
		Spec: changedSpec, LifecycleStatus: "REVIEW", UpdatedBy: repositoryOwnerID,
		ExpectedLockVersion: version.LockVersion,
	}); err != nil {
		t.Fatalf("concurrently edit tested draft: %v", err)
	}
	release()
	if err := <-errorChannel; !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale test result conflict, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM tool_tests WHERE id=$1`, toolTestDriftID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale test result was persisted: count=%d", count)
	}
	if len(artifacts.snapshot()) != 1 || artifacts.snapshot()[0].RetentionMode != TestRetentionPermanent {
		t.Fatalf("stale external execution raw artifact should remain permanent for audit")
	}
}

func TestToolTestUsesTheSameCredentialInjectionBoundaryAsRuntime(t *testing.T) {
	repository, db := newRepositoryTest(t)
	_, version, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer draft-test-token" {
			t.Errorf("tool test request did not receive injected credentials: %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	registry, err := toolruntime.NewExecutorRegistry(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	injector := &toolTestCredentialInjector{}
	service, err := NewTestServiceWithInjector(repository, registry,
		&memoryToolTestArtifacts{db: db, ids: []string{toolTestArtifactFiveID}}, injector)
	if err != nil {
		t.Fatal(err)
	}
	input := toolTestRunInput(toolTestCredentialID, version.ID, server.URL, json.RawMessage(`{"orderId":"A-100"}`))
	input.Credential = execution.CredentialReference{
		WorkspaceID: repositoryWorkspaceID,
		SecretID:    "credential-secret",
		AuthMode:    "BEARER",
	}
	result, err := service.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("run credential-bearing Tool test: %v", err)
	}
	if result.Status != "SUCCEEDED" || injector.calls != 1 ||
		injector.reference.SecretID != "credential-secret" {
		t.Fatalf("credential injection was not applied exactly once: result=%+v injector=%+v", result, injector)
	}
}

func TestToolTestRequestPassthroughEnvelopeReachesUpstream(t *testing.T) {
	repository, db := newRepositoryTest(t)
	_, version, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}

	const canary = "CANARY-TOOL-TEST-PASSTHROUGH"
	var sawAuth string
	var hit int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit++
		sawAuth = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	bootID := "tool-test-passthrough-boot"
	vault, err := outboundidentity.NewRuntimeCredentialVault(bootID, nil, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	attacher, err := outboundidentity.NewBindingAttacher(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	injector, err := execution.NewOutboundIdentityInjector(execution.OutboundIdentityInjectorConfig{
		Vault: vault, BootID: bootID,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := toolruntime.NewExecutorRegistry(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTestServiceWithInjector(repository, registry,
		&memoryToolTestArtifacts{db: db, ids: []string{toolTestPassthroughArtifactID}}, injector)
	if err != nil {
		t.Fatal(err)
	}
	service = service.WithBindingAttacher(attacher, bootID)

	reqJSON, err := json.Marshal(map[string]any{
		"schemaVersion": "outbound-requirements.v1",
		"connections": []map[string]any{{
			"connectionId": repositoryConnectionID, "providerId": repositoryProviderID,
			"mode": "REQUEST_PASSTHROUGH", "providerContractVersion": 1, "connectionPolicyVersion": 1,
			"requiredScopes": []string{}, "credentialRequired": true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := json.Marshal(map[string]any{
		"outboundIdentity": map[string]any{
			"schemaVersion": "outbound-identity.v1", "supportedModes": []string{"REQUEST_PASSTHROUGH"},
			"supportedSubjectTypes": []string{"USER"},
			"requestPassthrough": map[string]any{
				"credentialTypes":   []string{"ACCESS_TOKEN"},
				"businessInjection": map[string]string{"headerName": "Authorization", "prefix": "Bearer"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{
		"schemaVersion": "outbound-credentials.v1",
		"bindings": []map[string]any{{
			"connectionId": repositoryConnectionID, "credentialType": "ACCESS_TOKEN",
			"value": canary, "expiresAt": "2099-01-01T00:00:00Z",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	input := toolTestRunInput(toolTestPassthroughID, version.ID, server.URL, json.RawMessage(`{"orderId":"A-100"}`))
	input.Credential = execution.CredentialReference{
		WorkspaceID:          repositoryWorkspaceID,
		AuthMode:             string(outboundidentity.ModeRequestPassthrough),
		OutboundMode:         string(outboundidentity.ModeRequestPassthrough),
		OutboundRequirements: reqJSON,
		ProviderDriverConfig: driver,
	}
	input.CredentialsRaw = envelope

	result, err := service.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("request-passthrough tool test: %v", err)
	}
	if result.Status != "SUCCEEDED" || !result.ConnectivityPassed {
		t.Fatalf("result=%+v", result)
	}
	if hit != 1 {
		t.Fatalf("upstream hits=%d want 1", hit)
	}
	if sawAuth != "Bearer "+canary {
		t.Fatalf("upstream Authorization=%q", sawAuth)
	}
	// Token must not leak into durable test summaries / error codes.
	if strings.Contains(string(result.RequestSummary), canary) ||
		strings.Contains(string(result.ResponseSummary), canary) {
		t.Fatalf("canary leaked into test record summaries: %+v", result)
	}
	// Vault root cleaned after Run.
	_, borrowErr := vault.Borrow(outboundidentity.VaultKey{
		BootID: bootID, WorkspaceID: repositoryWorkspaceID,
		SubjectType: outboundidentity.SubjectTypeUser, SubjectID: repositoryOwnerID,
		RootScopeType: outboundidentity.RootScopeToolTest, RootScopeID: toolTestPassthroughID,
		ConnectionID: repositoryConnectionID, ConnectionPolicyVersion: 1,
	})
	if borrowErr == nil {
		t.Fatal("expected vault root cleaned after tool test")
	}
}

func TestToolTestRequestPassthroughRequiresEnvelope(t *testing.T) {
	repository, db := newRepositoryTest(t)
	_, version, err := repository.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	bootID := "tool-test-missing-envelope"
	vault, err := outboundidentity.NewRuntimeCredentialVault(bootID, nil, outboundidentity.VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	attacher, err := outboundidentity.NewBindingAttacher(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	injector, err := execution.NewOutboundIdentityInjector(execution.OutboundIdentityInjectorConfig{
		Vault: vault, BootID: bootID,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := toolruntime.NewExecutorRegistry(http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTestServiceWithInjector(repository, registry,
		&memoryToolTestArtifacts{db: db, ids: []string{toolTestArtifactFiveID}}, injector)
	if err != nil {
		t.Fatal(err)
	}
	service = service.WithBindingAttacher(attacher, bootID)

	reqJSON, _ := json.Marshal(map[string]any{
		"schemaVersion": "outbound-requirements.v1",
		"connections": []map[string]any{{
			"connectionId": repositoryConnectionID, "providerId": repositoryProviderID,
			"mode": "REQUEST_PASSTHROUGH", "providerContractVersion": 1, "connectionPolicyVersion": 1,
			"requiredScopes": []string{}, "credentialRequired": true,
		}},
	})
	input := toolTestRunInput(toolTestPassthroughID, version.ID, "http://127.0.0.1:9", json.RawMessage(`{}`))
	input.Credential = execution.CredentialReference{
		WorkspaceID:          repositoryWorkspaceID,
		OutboundMode:         string(outboundidentity.ModeRequestPassthrough),
		AuthMode:             string(outboundidentity.ModeRequestPassthrough),
		OutboundRequirements: reqJSON,
	}
	_, err = service.Run(context.Background(), input)
	if !errors.Is(err, outboundidentity.ErrCredentialRequired) {
		t.Fatalf("want OUTBOUND_CREDENTIAL_REQUIRED, got %v", err)
	}
}

func newToolTestService(
	t *testing.T,
	repository *Repository,
	client *http.Client,
	artifacts ToolTestArtifactStore,
) *TestService {
	t.Helper()
	registry, err := toolruntime.NewExecutorRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewTestService(repository, registry, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func toolTestRunInput(testID, versionID, baseURL string, input json.RawMessage) RunToolTestInput {
	return RunToolTestInput{
		TestID: testID, WorkspaceID: repositoryWorkspaceID, CapabilityID: repositoryToolID,
		VersionID: versionID, TraceID: "tool-test-trace", TestedBy: repositoryOwnerID,
		Connection: execution.ConnectionSnapshot{
			ID: repositoryConnectionID, WorkspaceID: repositoryWorkspaceID,
			ProviderID: repositoryProviderID, BaseURL: baseURL,
			EgressPolicy: execution.EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}},
		},
		Input: input,
	}
}

type memoryToolTestArtifacts struct {
	mutex     sync.Mutex
	db        *sql.DB
	ids       []string
	artifacts []ToolTestArtifact
}

type toolTestCredentialInjector struct {
	calls     int
	reference execution.CredentialReference
}

func (injector *toolTestCredentialInjector) WithInjectedConnection(
	_ context.Context,
	connection execution.ConnectionSnapshot,
	reference execution.CredentialReference,
	invoke func(execution.ConnectionSnapshot) error,
) error {
	injector.calls++
	injector.reference = reference
	headers := make(map[string]string, len(connection.Headers)+1)
	for name, value := range connection.Headers {
		headers[name] = value
	}
	headers["Authorization"] = "Bearer draft-test-token"
	connection.Headers = headers
	return invoke(connection)
}

func (store *memoryToolTestArtifacts) WriteToolTestArtifact(_ context.Context, artifact ToolTestArtifact) (string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if len(store.ids) == 0 {
		return "", errors.New("artifact id exhausted")
	}
	id := store.ids[0]
	store.ids = store.ids[1:]
	artifact.Request = cloneRaw(artifact.Request)
	artifact.Response = cloneRaw(artifact.Response)
	store.artifacts = append(store.artifacts, artifact)
	// Baseline schema requires permanent TOOL_TEST_PAYLOAD metadata before tool_tests insert.
	if store.db != nil {
		if _, err := store.db.Exec(`
			INSERT INTO stored_objects(
				id, workspace_id, bucket, object_key, kind, content_type, size_bytes, sha256,
				encryption_key_id, classification, retention_mode, created_by_type, created_by_id
			) VALUES (
				$1, $2, 'actweave-test', $3, 'TOOL_TEST_PAYLOAD', 'application/json', 2,
				'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
				'test-key', 'SENSITIVE', 'PERMANENT', 'USER', $4
			)
			ON CONFLICT DO NOTHING
		`, id, artifact.WorkspaceID, "tool-test/"+id, artifact.TestedBy); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (store *memoryToolTestArtifacts) snapshot() []ToolTestArtifact {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	values := make([]ToolTestArtifact, len(store.artifacts))
	copy(values, store.artifacts)
	return values
}
