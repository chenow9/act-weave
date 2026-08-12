package httptransport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/audit"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1AgentRoutesPersistPreviewAndAcceptedEnhancement(t *testing.T) {
	f := newAgentCapabilityFixture(t)
	created := f.createAgent(t, "assistant")
	if created.ToolsCount != 0 || created.WorkflowsCount != 0 || created.CurrentPromptRevisionID == nil {
		t.Fatalf("created agent=%+v", created)
	}
	preview := f.request(t, http.MethodPost, f.base+"/agents/"+created.ID+":enhance-prompt", map[string]any{
		"input": "Preview an improved prompt", "preview": true,
	}, f.token, nil)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"preview":true`) ||
		!strings.Contains(preview.Body.String(), "Generated: Preview an improved prompt") {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewRunCount, acceptedPreviewCount int
	if err := f.db.QueryRow(`SELECT COUNT(*),COUNT(accepted_revision_id) FROM prompt_runs WHERE workspace_id=$1 AND agent_id=$2 AND operation_type='PREVIEW'`, f.workspaceID, created.ID).Scan(&previewRunCount, &acceptedPreviewCount); err != nil {
		t.Fatal(err)
	}
	if previewRunCount != 1 || acceptedPreviewCount != 0 || f.objects.putCount() != 2 {
		t.Fatalf("preview persistence runs=%d accepted=%d objects=%d", previewRunCount, acceptedPreviewCount, f.objects.putCount())
	}

	enhanced := f.request(t, http.MethodPost, f.base+"/agents/"+created.ID+":enhance-prompt", map[string]any{
		"input": "Adopt an improved prompt", "preview": false, "lockVersion": created.LockVersion,
	}, f.token, nil)
	if enhanced.Code != http.StatusOK || !strings.Contains(enhanced.Body.String(), "acceptedRevisionId") {
		t.Fatalf("enhance status=%d body=%s", enhanced.Code, enhanced.Body.String())
	}
	detail := f.request(t, http.MethodGet, f.base+"/agents/"+created.ID, nil, f.token, nil)
	var current agentDTO
	decodeResponse(t, detail.Body.Bytes(), &current)
	if current.LockVersion != created.LockVersion+1 || current.CurrentPromptRevisionID == nil || *current.CurrentPromptRevisionID == *created.CurrentPromptRevisionID {
		t.Fatalf("enhanced agent=%+v", current)
	}
	escalation := f.request(t, http.MethodPatch, f.base+"/agents/"+created.ID, map[string]any{
		"workspaceId": f.workspaceID, "name": "invalid", "lockVersion": current.LockVersion,
	}, f.token, nil)
	assertErrorResponse(t, escalation, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestV1CapabilityBindingRoutesDeriveCounts(t *testing.T) {
	f := newAgentCapabilityFixture(t)
	created := f.createAgent(t, "binding-agent")
	toolItem := f.seedCapability(t, "TOOL", "lookup-orders")
	workflowItem := f.seedCapability(t, "WORKFLOW", "process-order")

	for _, item := range []capability.CatalogItem{toolItem, workflowItem} {
		bound := f.request(t, http.MethodPut, f.base+"/agents/"+created.ID+"/capabilities/"+item.ID, map[string]any{
			"versionPolicy": "FOLLOW_ACTIVE", "enabled": true, "configOverrides": map[string]any{}, "lockVersion": 0,
		}, f.token, nil)
		if bound.Code != http.StatusOK || !strings.Contains(bound.Body.String(), `"lockVersion":1`) {
			t.Fatalf("bind %s status=%d body=%s", item.Kind, bound.Code, bound.Body.String())
		}
	}
	detail := f.request(t, http.MethodGet, f.base+"/agents/"+created.ID, nil, f.token, nil)
	var summary agentDTO
	decodeResponse(t, detail.Body.Bytes(), &summary)
	if summary.ToolsCount != 1 || summary.WorkflowsCount != 1 {
		t.Fatalf("derived counts=%+v", summary)
	}
	bindings := f.request(t, http.MethodGet, f.base+"/agents/"+created.ID+"/capabilities", nil, f.token, nil)
	if bindings.Code != http.StatusOK || strings.Count(bindings.Body.String(), `"resolvedRelease"`) != 2 {
		t.Fatalf("bindings status=%d body=%s", bindings.Code, bindings.Body.String())
	}
	if !strings.Contains(bindings.Body.String(), `"capabilityId"`) || strings.Contains(bindings.Body.String(), `"CapabilityID"`) {
		t.Fatalf("binding descriptors must use v1 lower-camel fields: %s", bindings.Body.String())
	}
	catalog := f.request(t, http.MethodGet, f.base+"/capabilities", nil, f.token, nil)
	if catalog.Code != http.StatusOK || strings.Count(catalog.Body.String(), `"boundAgentCount":1`) != 2 {
		t.Fatalf("catalog status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	if strings.Contains(catalog.Body.String(), `"ReleaseID"`) {
		t.Fatalf("catalog descriptors must not expose Go field names: %s", catalog.Body.String())
	}
	removed := f.request(t, http.MethodDelete, f.base+"/agents/"+created.ID+"/capabilities/"+toolItem.ID+"?lockVersion=1", nil, f.token, nil)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("unbind status=%d body=%s", removed.Code, removed.Body.String())
	}
	detail = f.request(t, http.MethodGet, f.base+"/agents/"+created.ID, nil, f.token, nil)
	decodeResponse(t, detail.Body.Bytes(), &summary)
	if summary.ToolsCount != 0 || summary.WorkflowsCount != 1 {
		t.Fatalf("counts after unbind=%+v", summary)
	}
}

type promptMemoryObjects struct {
	mu     sync.Mutex
	values map[string][]byte
	puts   int
	db     *sql.DB
}

func (s *promptMemoryObjects) PutPermanent(ctx context.Context, workspaceID, kind string, content []byte, createdBy string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	objectKind := "PROMPT_RUN_INPUT"
	if kind == "PROMPT_OUTPUT" {
		objectKind = "PROMPT_RUN_OUTPUT"
	}
	digest := sha256.Sum256(content)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO stored_objects(
		id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		encryption_key_id,classification,retention_mode,created_by_type,created_by_id
	)VALUES($1,$2,'prompt-test',$3,$4,'text/plain',$5,$6,'test-key','SENSITIVE','PERMANENT','USER',$7)`,
		id, workspaceID, "prompts/"+id, objectKind, len(content), hex.EncodeToString(digest[:]), createdBy); err != nil {
		return "", err
	}
	s.values[id] = append([]byte(nil), content...)
	s.puts++
	return id, nil
}
func (s *promptMemoryObjects) PutPreview(
	ctx context.Context, workspaceID, kind string, content []byte, createdBy string, retentionUntil time.Time,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	objectKind := "PROMPT_PREVIEW_INPUT"
	if kind == "PROMPT_OUTPUT" {
		objectKind = "PROMPT_PREVIEW_OUTPUT"
	}
	digest := sha256.Sum256(content)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO stored_objects(
		id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
		encryption_key_id,classification,retention_mode,retention_until,created_by_type,created_by_id
	)VALUES($1,$2,'prompt-test',$3,$4,'text/plain',$5,$6,'test-key','SENSITIVE','EXPIRING',$7,'USER',$8)`,
		id, workspaceID, "prompt-previews/"+id, objectKind, len(content), hex.EncodeToString(digest[:]),
		retentionUntil.UTC(), createdBy); err != nil {
		return "", err
	}
	s.values[id] = append([]byte(nil), content...)
	s.puts++
	return id, nil
}
func (s *promptMemoryObjects) GetPermanent(_ context.Context, _, id, _ string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.values[id]...), nil
}
func (s *promptMemoryObjects) putCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.puts }

type agentCapabilityFixture struct {
	*v1AuthFixture
	workspaceID, modelConfigID, base, token string
	objects                                 *promptMemoryObjects
	agents                                  *agent.Repository
	capabilities                            *capability.Repository
}

func newAgentCapabilityFixture(t *testing.T) *agentCapabilityFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	ctx := context.Background()
	workspaceID := uuid.NewString()
	workspaces, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{ID: workspaceID, Slug: "agents-" + workspaceID[:8], DisplayName: "Agents", Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewService(workspaces)
	if err != nil {
		t.Fatal(err)
	}
	models, err := modelconfig.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	modelConfigID := uuid.NewString()
	if _, err = models.Create(ctx, modelconfig.NewConfig{ID: modelConfigID, WorkspaceID: workspaceID, Name: "agent model", Provider: modelconfig.ProviderOpenAICompatible, APIBase: "https://models.example/v1", ModelName: "agent-model", Options: json.RawMessage(`{}`), CreatedBy: v1AdminUserID}); err != nil {
		t.Fatal(err)
	}
	agents, err := agent.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	objects := &promptMemoryObjects{values: map[string][]byte{}, db: authFixture.db}
	prompts, err := agent.NewPromptService(agents, objects, agentSnapshot{}, agent.PromptGeneratorFunc(func(_ context.Context, request agent.PromptGenerationRequest) (string, error) {
		return "Generated: " + request.Input, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := capability.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := capability.NewBindingService(capabilities, capability.ConnectionCompatibilityFunc(func(context.Context, string, string, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capability.NewCatalog(capabilities, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAgentCapabilityRoutes(authorizer, agents, prompts, capabilities, catalog, bindings)
	if err != nil {
		t.Fatal(err)
	}
	currentPrompt, err := agent.NewCurrentPromptQuery(agents, &fixturePromptAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	objectRepo, err := storedobject.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	creation, err := agent.NewCreationService(agents, objectRepo)
	if err != nil {
		t.Fatal(err)
	}
	routes = routes.WithCurrentPromptReader(currentPrompt).WithCreationService(creation)
	router, err := NewRouter(Config{Authenticator: authFixture.auth, Registrars: []V1RouteRegistrar{authFixture.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	authFixture.router = router
	login := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"username": v1AdminName, "password": v1AdminPass}, "", nil)
	return &agentCapabilityFixture{v1AuthFixture: authFixture, workspaceID: workspaceID, modelConfigID: modelConfigID, base: "/api/v1/workspaces/" + workspaceID, token: decodeTokenResponse(t, login).AccessToken, objects: objects, agents: agents, capabilities: capabilities}
}

type agentSnapshot struct{}

func (agentSnapshot) Snapshot(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"provider":"test","model":"agent-model"}`), nil
}

func (agentSnapshot) AvailableSnapshot(ctx context.Context, workspaceID, modelConfigID string) (json.RawMessage, error) {
	return agentSnapshot{}.Snapshot(ctx, workspaceID, modelConfigID)
}

type fixturePromptAuditor struct{}

func (fixturePromptAuditor) Record(_ context.Context, input audit.ManagementEventInput) (audit.Event, error) {
	if strings.TrimSpace(input.ActorDisplay) == "" {
		return audit.Event{}, errors.New("actor display required")
	}
	return audit.Event{}, nil
}

func (f *agentCapabilityFixture) createAgent(t *testing.T, name string) agentDTO {
	t.Helper()
	response := f.request(t, http.MethodPost, f.base+"/agents", map[string]any{"name": name, "roleDescription": "Helpful assistant", "modelConfigId": f.modelConfigID, "systemPrompt": "You are helpful."}, f.token, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create agent status=%d body=%s", response.Code, response.Body.String())
	}
	var value agentDTO
	decodeResponse(t, response.Body.Bytes(), &value)
	return value
}

func (f *agentCapabilityFixture) seedCapability(t *testing.T, kind, name string) capability.CatalogItem {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	created, err := f.capabilities.Create(ctx, capability.NewCapability{ID: id, WorkspaceID: f.workspaceID, Kind: kind, Name: name, Slug: name, Description: name, CreatedBy: v1AdminUserID})
	if err != nil {
		t.Fatal(err)
	}
	sourceType := "TOOL_VERSION"
	if kind == "WORKFLOW" {
		sourceType = "WORKFLOW_REVISION"
	}
	_, release, err := f.capabilities.Publish(ctx, capability.PublishRelease{ID: uuid.NewString(), WorkspaceID: f.workspaceID, CapabilityID: id, SourceType: sourceType, SourceID: uuid.NewString(), CallableName: strings.ReplaceAll(name, "-", "_"), CallableDescription: name, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: strings.Repeat("b", 64), PublishedBy: v1AdminUserID})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{CapabilityID: id, ReleaseID: release.ID, Kind: kind, CallableName: release.CallableName}
	return capability.CatalogItem{Capability: created, ActiveRelease: &descriptor}
}

// P3.3 transport: unpublished WORKFLOW bind → 4xx CAPABILITY_UNAVAILABLE;
// after publish, bind with default agent succeeds (P3.1 / P3.2).
func TestV1BindUnpublishedWorkflowRejectedThenPublishedSucceeds(t *testing.T) {
	f := newAgentCapabilityFixture(t)
	created := f.createAgent(t, "workflow-bind-agent")
	ctx := context.Background()
	workflowID := uuid.NewString()
	if _, err := f.capabilities.Create(ctx, capability.NewCapability{
		ID: workflowID, WorkspaceID: f.workspaceID, Kind: "WORKFLOW",
		Name: "Draft Workflow", Slug: "draft-workflow", Description: "draft",
		CreatedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}

	rejected := f.request(t, http.MethodPut, f.base+"/agents/"+created.ID+"/capabilities/"+workflowID, map[string]any{
		"versionPolicy": "FOLLOW_ACTIVE", "enabled": true, "configOverrides": map[string]any{}, "lockVersion": 0,
	}, f.token, nil)
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "CAPABILITY_UNAVAILABLE") {
		t.Fatalf("unpublished WORKFLOW bind status=%d body=%s", rejected.Code, rejected.Body.String())
	}

	if _, _, err := f.capabilities.Publish(ctx, capability.PublishRelease{
		ID: uuid.NewString(), WorkspaceID: f.workspaceID, CapabilityID: workflowID,
		SourceType: "WORKFLOW_REVISION", SourceID: uuid.NewString(),
		CallableName: "draft_workflow", CallableDescription: "Draft Workflow",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel: "LOW", SideEffectLevel: "READ", Checksum: strings.Repeat("c", 64), PublishedBy: v1AdminUserID,
	}); err != nil {
		t.Fatal(err)
	}

	bound := f.request(t, http.MethodPut, f.base+"/agents/"+created.ID+"/capabilities/"+workflowID, map[string]any{
		"versionPolicy": "FOLLOW_ACTIVE", "enabled": true, "configOverrides": map[string]any{}, "lockVersion": 0,
	}, f.token, nil)
	if bound.Code != http.StatusOK || !strings.Contains(bound.Body.String(), `"lockVersion":1`) {
		t.Fatalf("published WORKFLOW bind status=%d body=%s", bound.Code, bound.Body.String())
	}

	bindings := f.request(t, http.MethodGet, f.base+"/agents/"+created.ID+"/capabilities", nil, f.token, nil)
	if bindings.Code != http.StatusOK || !strings.Contains(bindings.Body.String(), workflowID) ||
		!strings.Contains(bindings.Body.String(), `"kind":"WORKFLOW"`) {
		t.Fatalf("agent capabilities after WORKFLOW bind status=%d body=%s", bindings.Code, bindings.Body.String())
	}
	detail := f.request(t, http.MethodGet, f.base+"/agents/"+created.ID, nil, f.token, nil)
	var summary agentDTO
	decodeResponse(t, detail.Body.Bytes(), &summary)
	if summary.WorkflowsCount != 1 {
		t.Fatalf("workflowsCount after published WORKFLOW bind=%+v", summary)
	}
}
