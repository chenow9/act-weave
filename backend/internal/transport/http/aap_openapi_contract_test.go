package httptransport

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"actweave/backend/internal/protocolschema"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
)

func TestAAPDataPlaneAcceptanceOpenAPIContract(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile(filepath.Join(
		"..", "..", "..", "..", "docs", "openapi", "agent-access-v1.yaml",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if document.Info.Version != protocolschema.ProtocolVersion {
		t.Fatalf("OpenAPI version=%s want=%s", document.Info.Version, protocolschema.ProtocolVersion)
	}

	engine := gin.New()
	root := engine.Group("/api/agent-access/v1")
	routes := AgentAccessV1Routes{Public: root, Protected: root}
	registrars := []AgentAccessV1RouteRegistrar{
		&AgentAccessTokenRoutes{}, &AAPAgentProfileRoutes{}, &AAPConversationRoutes{},
		&AAPRunRoutes{canceller: &aapCancelRunApplication{}, decider: &aapInteractionDecisionApplication{}},
		&AAPFileRoutes{},
	}
	for _, registrar := range registrars {
		registrar.RegisterAgentAccessV1(routes)
	}

	want := map[string]struct{}{}
	for path, item := range document.Paths.Map() {
		for method := range item.Operations() {
			registeredPath := aapOpenAPIRegisteredPath(path)
			want[method+" "+registeredPath] = struct{}{}
		}
	}
	got := map[string]struct{}{}
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = struct{}{}
	}
	if missing, extra := routeSetDifference(want, got), routeSetDifference(got, want); len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("AAP/OpenAPI route mismatch\nmissing=%v\nextra=%v", missing, extra)
	}

	allowlists := map[string]struct {
		value any
		want  []string
	}{
		"oauth success": {oauthTokenSuccess{}, []string{"access_token", "expires_in", "issued_token_type", "scope", "token_type"}},
		"profile": {aapAgentProfileDTO{}, []string{
			"capabilities", "description", "id", "interactionRequirements", "name", "object", "supportedContent", "version",
		}},
		"conversation create": {aapCreateConversationRequest{}, []string{"title"}},
		"conversation": {aapConversationDTO{}, []string{
			"agentId", "createdAt", "id", "latestRunId", "object", "runs", "status", "title", "updatedAt", "version",
		}},
		"run create": {AAPCreateRunRequest{}, []string{"conversationId", "input", "metadata", "stream"}},
		"run": {aapRunResourceDTO{}, []string{
			"agentId", "completedAt", "conversationId", "error", "id", "items", "links", "object", "startedAt", "status", "version",
		}},
		"interaction decide": {aapInteractionDecisionRequest{}, []string{"decision"}},
		"file create request": {aapCreateFileRequest{}, []string{"filename", "mediaType", "sizeBytes", "sha256", "purpose"}},
		"file resource": {aapFileDTO{}, []string{
			"object", "id", "agentId", "status", "filename", "mediaType", "detectedMediaType",
			"sizeBytes", "sha256", "purpose", "error", "processing", "artifacts", "links",
			"createdAt", "updatedAt", "readyAt",
		}},
		"file create response": {aapCreateFileResponse{}, []string{"file", "upload", "idempotent"}},
		"file get response": {aapGetFileResponse{}, []string{"file"}},
		"file mint download": {aapMintDownloadResponse{}, []string{"token", "expiresAt", "url"}},
	}
	for name, contract := range allowlists {
		actual := make([]string, 0)
		for tag := range contractJSONTags(contract.value) {
			actual = append(actual, tag)
		}
		sort.Strings(actual)
		sort.Strings(contract.want)
		if !reflect.DeepEqual(actual, contract.want) {
			t.Errorf("%s JSON fields=%v want=%v", name, actual, contract.want)
		}
	}
	for name, value := range map[string]any{
		"profile": aapAgentProfileDTO{}, "conversation": aapConversationDTO{},
		"run": aapRunResourceDTO{}, "decision": aapInteractionDecisionRequest{},
	} {
		tags := contractJSONTags(value)
		for _, forbidden := range []string{
			"accessToken", "authorization", "authorizationSnapshot", "clientSecret",
			"grantId", "principalSnapshot", "resumeToken", "tokenId",
		} {
			if _, exists := tags[forbidden]; exists {
				t.Errorf("%s exposes forbidden field %q", name, forbidden)
			}
		}
	}
}

func aapOpenAPIRegisteredPath(path string) string {
	replacements := map[string]string{
		"{workspaceId}": ":wid", "{agentId}": ":aid", "{conversationId}": ":cid",
		"{runId}": ":rid", "{interactionId}": ":iid",
		"{fileId}": ":fid", "{tokenId}": ":tid",
	}
	for source, target := range replacements {
		path = strings.ReplaceAll(path, source, target)
	}
	return "/api/agent-access/v1" + contractRegisteredPath(path)
}

// Compile-time checks keep the optional command route stubs aligned with the
// production interfaces used above.
var _ AAPRunCancellationApplication = (*aapCancelRunApplication)(nil)
var _ AAPInteractionDecisionApplication = (*aapInteractionDecisionApplication)(nil)
