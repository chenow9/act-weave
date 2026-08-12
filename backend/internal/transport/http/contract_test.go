package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/secret"

	"github.com/gin-gonic/gin"
)

type v1ContractRoute struct {
	method string
	path   string
	public bool
}

func TestV1ContractRouteSurface(t *testing.T) {
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	groups := V1Routes{Public: v1, Protected: v1}
	for _, registrar := range allV1ContractRegistrars() {
		registrar.RegisterV1(groups)
	}

	want := make(map[string]struct{}, len(v1ContractRoutes()))
	for _, route := range v1ContractRoutes() {
		key := route.method + " " + contractRegisteredPath(route.path)
		if _, duplicate := want[key]; duplicate {
			t.Fatalf("duplicate route in contract: %s", key)
		}
		want[key] = struct{}{}
	}
	got := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = struct{}{}
	}
	if missing, extra := routeSetDifference(want, got), routeSetDifference(got, want); len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("v1 route contract mismatch\nmissing=%v\nextra=%v", missing, extra)
	}
}

func TestV1ContractAuthenticationAndPublicCommandPaths(t *testing.T) {
	router, err := NewRouter(Config{Registrars: allV1ContractRegistrars()})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range v1ContractRoutes() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			want := http.StatusUnauthorized
			switch {
			case route.public && route.path == "/api/v1/auth/login":
				want = http.StatusUnprocessableEntity
			// A public document must be readable with no credentials at all;
			// anything else here is a route that only looks public.
			case route.public && route.method == http.MethodGet:
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("route status=%d want=%d body=%s", response.Code, want, response.Body.String())
			}
		})
	}
}

func TestV1ContractFutureSensitiveAndOriginalDeletionRoutesAreAbsent(t *testing.T) {
	router, err := NewRouter(Config{
		Authenticator: contractAuthenticator{}, Registrars: allV1ContractRegistrars(),
	})
	if err != nil {
		t.Fatal(err)
	}
	wid, id := "d38f1f2e-7b5a-7c3d-8e9f-123456789010", "d38f1f2e-7b5a-7c3d-8e9f-123456789011"
	absent := []struct{ method, path string }{
		{http.MethodDelete, "/api/v1/workspaces/" + wid + "/chat/sessions/" + id},
		{http.MethodDelete, "/api/v1/workspaces/" + wid + "/chat/sessions/" + id + "/messages/" + id},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/secrets/" + id},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/secrets/" + id + ":rotate"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/memory-namespaces"},
		{http.MethodPost, "/api/v1/workspaces/" + wid + "/memory-records:forget"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/agents/" + id + "/profile"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/agent-peers"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/service-principals"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/agent-tasks"},
		{http.MethodGet, "/api/v1/workspaces/" + wid + "/execution-profiles"},
	}
	for _, route := range absent {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer contract-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("absent route status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestV1ContractDTOAllowlistsExcludeLegacyAndSecretFields(t *testing.T) {
	writeDTOs := map[string]any{
		"tool create": createToolRequest{}, "tool update": updateToolRequest{},
		"OpenAPI import create": createImportRequest{},
		"workflow create":       createWorkflowRequest{},
		"workflow generate":     generateWorkflowRequest{},
		"workflow update":       updateWorkflowRequest{},
		"workflow draft update": updateWorkflowDraftRequest{},
	}
	for name, value := range writeDTOs {
		tags := contractJSONTags(value)
		for _, forbidden := range []string{"agentId", "owner", "dsl", "canvasGraph"} {
			if _, exists := tags[forbidden]; exists {
				t.Errorf("%s exposes legacy write field %q", name, forbidden)
			}
		}
	}

	readDTOs := map[string]any{
		"model config": modelConfigDTO{}, "connection": connectionDTO{},
		"secret": secret.ReadDTO{}, "tool": toolDTO{}, "workflow": workflowDTO{},
		"OpenAPI import": importDTO{},
	}
	for name, value := range readDTOs {
		tags := contractJSONTags(value)
		for _, forbidden := range []string{
			"plaintext", "password", "passwordHash", "credentialSecretId", "agentId",
			"owner", "dsl", "canvasGraph",
		} {
			if _, exists := tags[forbidden]; exists {
				t.Errorf("%s read DTO exposes forbidden field %q", name, forbidden)
			}
		}
	}
}

func TestV1ContractCrossWorkspaceGuessingIsNotVisibleAcrossDomains(t *testing.T) {
	denied := contractNotVisibleAuthorizer{}
	router, err := NewRouter(Config{Authenticator: contractAuthenticator{}, Registrars: []V1RouteRegistrar{
		&WorkspaceRoutes{authorizer: denied},
		&ConfigurationRoutes{authorizer: denied},
		&AgentCapabilityRoutes{authorizer: denied},
		&ToolOpenAPIRoutes{authorizer: denied},
		&WorkflowRoutes{authorizer: denied},
		&ChatExecutionRoutes{authorizer: denied},
		&AuditRoutes{authorizer: denied},
	}})
	if err != nil {
		t.Fatal(err)
	}
	wid, id := "d38f1f2e-7b5a-7c3d-8e9f-123456789020", "d38f1f2e-7b5a-7c3d-8e9f-123456789021"
	paths := []string{
		"/api/v1/workspaces/" + wid,
		"/api/v1/workspaces/" + wid + "/model-configs/" + id,
		"/api/v1/workspaces/" + wid + "/agents/" + id,
		"/api/v1/workspaces/" + wid + "/tools/" + id,
		"/api/v1/workspaces/" + wid + "/workflows/" + id,
		"/api/v1/workspaces/" + wid + "/chat/sessions/" + id,
		"/api/v1/workspaces/" + wid + "/audit-events/" + id,
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer contract-token")
			request.Header.Set("X-Request-ID", "request-contract-isolation")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertErrorResponse(t, response, http.StatusNotFound, "NOT_FOUND")
		})
	}
}

type contractAuthenticator struct{}

func (contractAuthenticator) AuthenticateAccessToken(context.Context, string) (Principal, error) {
	return Principal{UserID: transportTestUserID, SessionID: transportTestSessionID}, nil
}

type contractNotVisibleAuthorizer struct{}

func (contractNotVisibleAuthorizer) AuthorizeWorkspace(
	context.Context, string, string, authz.Action,
) (authz.WorkspaceContext, error) {
	return authz.WorkspaceContext{}, authz.ErrNotVisible
}

func allV1ContractRegistrars() []V1RouteRegistrar {
	catalogRoutes, err := NewA2UICatalogRoutes()
	if err != nil {
		panic(err)
	}
	return []V1RouteRegistrar{
		&AuthUserRoutes{}, &WorkspaceRoutes{}, &ConfigurationRoutes{}, &AgentCapabilityRoutes{},
		&ToolOpenAPIRoutes{}, &WorkflowRoutes{}, &GenerateSessionRoutes{}, &ChatExecutionRoutes{}, &AuditRoutes{},
		&AgentAccessManagementRoutes{}, catalogRoutes,
	}
}

func contractRegisteredPath(path string) string {
	lastSlash := strings.LastIndex(path, "/")
	colon := strings.LastIndex(path[lastSlash+1:], ":")
	if colon <= 0 {
		return path
	}
	colon += lastSlash + 1
	return path[:colon] + "/__command/" + path[colon+1:]
}

func routeSetDifference(left, right map[string]struct{}) []string {
	values := make([]string, 0)
	for value := range left {
		if _, exists := right[value]; !exists {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}

func contractJSONTags(value any) map[string]struct{} {
	tags := make(map[string]struct{})
	typeOf := reflect.TypeOf(value)
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			tags[name] = struct{}{}
		}
	}
	return tags
}

func v1ContractRoutes() []v1ContractRoute {
	const root = "/api/v1"
	return []v1ContractRoute{
		{http.MethodPost, root + "/auth/login", true},
		{http.MethodPost, root + "/auth/refresh", true},
		{http.MethodPost, root + "/auth/logout", false},
		{http.MethodGet, root + "/users/me", false},
		{http.MethodPatch, root + "/users/me", false},
		{http.MethodPost, root + "/users/me:change-password", false},
		{http.MethodGet, root + "/admin/users", false},
		{http.MethodPost, root + "/admin/users", false},
		{http.MethodGet, root + "/admin/users/:uid/workspaces", false},
		{http.MethodPatch, root + "/admin/users/:uid", false},
		{http.MethodPost, root + "/admin/users/:uid:change-platform-role", false},
		{http.MethodPost, root + "/admin/users/:uid:reset-password", false},
		{http.MethodPost, root + "/admin/users/:uid:unlock", false},

		{http.MethodGet, root + "/workspaces", false},
		{http.MethodPost, root + "/workspaces", false},
		{http.MethodGet, root + "/workspaces/:wid", false},
		{http.MethodPatch, root + "/workspaces/:wid", false},
		{http.MethodDelete, root + "/workspaces/:wid", false},
		{http.MethodPost, root + "/workspaces/:wid:enable", false},
		{http.MethodPost, root + "/workspaces/:wid:disable", false},
		{http.MethodGet, root + "/workspaces/:wid/members", false},
		{http.MethodGet, root + "/workspaces/:wid/member-candidates", false},
		{http.MethodPost, root + "/workspaces/:wid/members", false},
		{http.MethodPatch, root + "/workspaces/:wid/members/:uid", false},
		{http.MethodDelete, root + "/workspaces/:wid/members/:uid", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients/:cid", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid:enable", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid:disable", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid:update-trusted-subject-issuer", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients/:cid/credentials", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/credentials", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/credentials/:kid:revoke", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients/:cid/grants", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/grants", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/grants/:gid:revoke", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients/:cid/external-subjects", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-access/clients/:cid/external-subjects/:sid", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/external-subjects/:sid:enable", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/external-subjects/:sid:disable", false},
		{http.MethodPost, root + "/workspaces/:wid/agent-access/clients/:cid/external-subjects/:sid:update-display-ref", false},

		{http.MethodGet, root + "/workspaces/:wid/model-configs", false},
		{http.MethodPost, root + "/workspaces/:wid/model-configs", false},
		{http.MethodGet, root + "/workspaces/:wid/model-configs/:id", false},
		{http.MethodPatch, root + "/workspaces/:wid/model-configs/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/model-configs/:id", false},
		{http.MethodPost, root + "/workspaces/:wid/model-configs/:id:verify", false},
		{http.MethodGet, root + "/workspaces/:wid/providers", false},
		{http.MethodPost, root + "/workspaces/:wid/providers", false},
		{http.MethodGet, root + "/workspaces/:wid/providers/:pid", false},
		{http.MethodPatch, root + "/workspaces/:wid/providers/:pid", false},
		{http.MethodDelete, root + "/workspaces/:wid/providers/:pid", false},
		{http.MethodPost, root + "/workspaces/:wid/providers/:pid:sync", false},
		{http.MethodGet, root + "/workspaces/:wid/providers/:pid/assets", false},
		{http.MethodPost, root + "/workspaces/:wid/providers/:pid/assets/:aid:materialize", false},
		{http.MethodGet, root + "/workspaces/:wid/providers/:pid/connections", false},
		{http.MethodPost, root + "/workspaces/:wid/providers/:pid/connections", false},
		{http.MethodGet, root + "/workspaces/:wid/connections/:id", false},
		{http.MethodPatch, root + "/workspaces/:wid/connections/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/connections/:id", false},
		{http.MethodPost, root + "/workspaces/:wid/connections/:id:verify", false},
		{http.MethodPost, root + "/workspaces/:wid/connections/:id/__command/impact", false},
		{http.MethodPost, root + "/workspaces/:wid/service-connections/:id/__command/impact", false},
		{http.MethodPost, root + "/workspaces/:wid/secrets", false},
		{http.MethodPost, root + "/workspaces/:wid/secrets/:id:rotate", false},

		{http.MethodGet, root + "/workspaces/:wid/agents", false},
		{http.MethodPost, root + "/workspaces/:wid/agents", false},
		{http.MethodGet, root + "/workspaces/:wid/agents/:id", false},
		{http.MethodPatch, root + "/workspaces/:wid/agents/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/agents/:id", false},
		{http.MethodPost, root + "/workspaces/:wid/agents/:id:enhance-prompt", false},
		{http.MethodPost, root + "/workspaces/:wid/agents:preview-prompt-enhancement", false},
		{http.MethodGet, root + "/workspaces/:wid/agents/:id/prompt-revisions/current", false},
		{http.MethodGet, root + "/workspaces/:wid/agents/:id/capabilities", false},
		{http.MethodPut, root + "/workspaces/:wid/agents/:id/capabilities/:capabilityId", false},
		{http.MethodDelete, root + "/workspaces/:wid/agents/:id/capabilities/:capabilityId", false},
		{http.MethodGet, root + "/workspaces/:wid/capabilities", false},

		{http.MethodGet, root + "/workspaces/:wid/tools", false},
		{http.MethodPost, root + "/workspaces/:wid/tools", false},
		{http.MethodGet, root + "/workspaces/:wid/tools/:id", false},
		{http.MethodPatch, root + "/workspaces/:wid/tools/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/tools/:id", false},
		{http.MethodGet, root + "/workspaces/:wid/tools/:id/versions", false},
		{http.MethodPost, root + "/workspaces/:wid/tools/:id/versions", false},
		{http.MethodGet, root + "/workspaces/:wid/tools/:id/versions/:vid", false},
		{http.MethodPatch, root + "/workspaces/:wid/tools/:id/versions/:vid", false},
		{http.MethodPost, root + "/workspaces/:wid/tools/:id/versions/:vid:test", false},
		{http.MethodPost, root + "/workspaces/:wid/tools/:id/versions/:vid:publish", false},
		{http.MethodPost, root + "/workspaces/:wid/tools/:id/versions/:vid/__command/force-publish", false},
		{http.MethodPost, root + "/workspaces/:wid/tools/:id:invoke", false},
		{http.MethodGet, root + "/workspaces/:wid/openapi-imports", false},
		{http.MethodPost, root + "/workspaces/:wid/openapi-imports", false},
		{http.MethodPost, root + "/workspaces/:wid/openapi-imports/__command/upload", false},
		{http.MethodGet, root + "/workspaces/:wid/openapi-imports/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/openapi-imports/:id", false},
		{http.MethodPost, root + "/workspaces/:wid/openapi-imports/:id:generate-tools", false},

		{http.MethodGet, root + "/workspaces/:wid/workflows", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows:generate", false},
		{http.MethodPost, root + "/workspaces/:wid/workflow-generate-sessions", false},
		{http.MethodGet, root + "/workspaces/:wid/workflow-generate-sessions/:sid", false},
		{http.MethodPost, root + "/workspaces/:wid/workflow-generate-sessions/:sid/turns", false},
		{http.MethodPost, root + "/workspaces/:wid/workflow-generate-sessions/:sid:close", false},
		{http.MethodGet, root + "/workspaces/:wid/workflows/:id", false},
		{http.MethodPatch, root + "/workspaces/:wid/workflows/:id", false},
		{http.MethodDelete, root + "/workspaces/:wid/workflows/:id", false},
		{http.MethodGet, root + "/workspaces/:wid/workflows/:id/draft", false},
		{http.MethodPut, root + "/workspaces/:wid/workflows/:id/draft", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/draft:compile", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/compilations/:cid:trial", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/compilations/:cid:publish", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/compilations/:cid/__command/force-publish", false},
		{http.MethodGet, root + "/workspaces/:wid/workflows/:id/revisions", false},
		{http.MethodGet, root + "/workspaces/:wid/workflows/:id/revisions:diff", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/revisions/:rid:activate", false},
		{http.MethodPost, root + "/workspaces/:wid/workflows/:id/revisions/:rid:execute", false},
		{http.MethodGet, root + "/workspaces/:wid/workflows/:id/readiness", false},

		{http.MethodGet, root + "/workspaces/:wid/chat/sessions", false},
		{http.MethodPost, root + "/workspaces/:wid/chat/sessions", false},
		{http.MethodGet, root + "/workspaces/:wid/chat/sessions/:sid", false},
		{http.MethodPost, root + "/workspaces/:wid/chat/sessions/:sid:archive", false},
		{http.MethodPost, root + "/workspaces/:wid/chat/sessions/:sid/messages", false},
		{http.MethodPost, root + "/workspaces/:wid/chat/sessions/:sid/outbound-credentials", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-runs/:rid", false},
		{http.MethodGet, root + "/workspaces/:wid/agent-runs/:rid/events", false},
		{http.MethodGet, root + "/workspaces/:wid/executions", false},
		{http.MethodGet, root + "/workspaces/:wid/executions/:id", false},
		{http.MethodGet, root + "/workspaces/:wid/executions/:id/events", false},
		{http.MethodPost, root + "/workspaces/:wid/confirmations/:id:confirm", false},
		{http.MethodPost, root + "/workspaces/:wid/confirmations/:id:cancel", false},
		{http.MethodPost, root + "/workspaces/:wid/execution-confirmations/:cid:confirm", false},
		{http.MethodPost, root + "/workspaces/:wid/execution-confirmations/:cid:cancel", false},

		{http.MethodGet, root + "/workspaces/:wid/audit-events", false},
		{http.MethodGet, root + "/workspaces/:wid/audit-events/:id", false},
		{http.MethodPost, root + "/workspaces/:wid/audit-exports", false},
		{http.MethodGet, root + "/workspaces/:wid/audit-exports/:id", false},

		// A2UI schemas: the contract a client reads before it holds any token.
		{http.MethodGet, root + "/a2ui/catalogs/standard/v1/catalog.json", true},
		{http.MethodGet, root + "/a2ui/catalogs/standard/v1/surface.schema.json", true},
	}
}
