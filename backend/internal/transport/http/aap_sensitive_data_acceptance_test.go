package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"actweave/backend/internal/protocolevent"
)

// TestAAPSensitiveDataAcceptance is the M10-T2 gate for AAP HTTP traces and DTOs:
// golden SSE payloads stay clean, error paths do not echo secrets, DTO allowlists
// exclude credential fields, and Artifact bytes are not embedded in Run items.
func TestAAPSensitiveDataAcceptance(t *testing.T) {
	t.Run("GoldenHTTPTracesScanClean", testSensitiveHTTPGoldenTraces)
	t.Run("ErrorPathsDoNotEchoSecrets", testSensitiveHTTPErrorNoEcho)
	t.Run("DTOAllowlistsExcludeCredentialFields", testSensitiveHTTPDTOAllowlists)
	t.Run("RunItemsDoNotEmbedArtifactSignedURLs", testSensitiveHTTPArtifactBoundary)
	t.Run("TokenQueryRejectionDoesNotReflectToken", testSensitiveHTTPTokenQuery)
}

func testSensitiveHTTPGoldenTraces(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"text", "tool_success", "workflow_tool", "approval_resume"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router, path, source := buildSDKContractGoldenRouter(t, name)
			response := requestGoldenHTTPTrace(router, path, "aap-access-token")
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			for index, raw := range source {
				if err := protocolevent.ScanPublicJSON(raw); err != nil {
					t.Fatalf("source event %d sensitive: %v", index+1, err)
				}
			}
			for index, raw := range extractGoldenSSEData(t, response.Body.Bytes()) {
				if err := protocolevent.ScanPublicJSON(raw); err != nil {
					t.Fatalf("sse event %d sensitive: %v body=%s", index+1, err, raw)
				}
			}
			// Wire body must not contain classic secret patterns.
			body := strings.ToLower(response.Body.String())
			for _, pattern := range []string{
				"bearer ", "awsk_", "resume_token", "chain of thought",
				"x-amz-signature=", "-----begin private key-----",
			} {
				if strings.Contains(body, pattern) {
					t.Fatalf("SSE body contains forbidden pattern %q", pattern)
				}
			}
		})
	}
}

func testSensitiveHTTPErrorNoEcho(t *testing.T) {
	t.Parallel()
	router, path, _ := buildSDKContractGoldenRouter(t, "text")
	// Unauthorized with a secret-looking bearer must not reflect the token.
	secret := "super-secret-bearer-token-should-not-leak-zzzz"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("unexpected success with invalid token")
	}
	if strings.Contains(response.Body.String(), secret) ||
		strings.Contains(strings.ToLower(response.Body.String()), "super-secret-bearer") {
		t.Fatalf("error reflected secret: %s", response.Body.String())
	}
}

func testSensitiveHTTPDTOAllowlists(t *testing.T) {
	t.Parallel()
	// Credential / secret JSON tags must never appear on public AAP DTOs.
	forbiddenTags := []string{
		"password", "secret", "client_secret", "private_key", "authorization",
		"cookie", "resume_token", "resumeToken", "access_token", "refresh_token",
		"chainOfThought", "signedUrl", "signed_url",
	}
	dtos := []any{
		oauthTokenSuccess{},
		aapAgentProfileDTO{},
		aapCreateConversationRequest{},
		aapConversationDTO{},
		AAPCreateRunRequest{},
		aapRunResourceDTO{},
		aapInteractionDecisionRequest{},
	}
	for _, dto := range dtos {
		tags := contractJSONTags(dto)
		typeName := reflect.TypeOf(dto).Name()
		for _, forbidden := range forbiddenTags {
			if _, ok := tags[forbidden]; ok {
				// access_token is intentionally present only on OAuth token success
				// (writeOnly short-lived credential response). All other DTOs forbid it.
				if typeName == "oauthTokenSuccess" && forbidden == "access_token" {
					continue
				}
				t.Fatalf("%s exposes forbidden field %q", typeName, forbidden)
			}
		}
		// Nested secret-like names in tags.
		for tag := range tags {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(tag))
			if normalized == "password" || normalized == "clientsecret" ||
				normalized == "privatekey" || normalized == "resumetoken" ||
				normalized == "chainofthought" {
				t.Fatalf("%s exposes sensitive tag %q", typeName, tag)
			}
		}
	}
	// OAuth success may include access_token but must be the only secret-ish field.
	oauthTags := contractJSONTags(oauthTokenSuccess{})
	if _, ok := oauthTags["access_token"]; !ok {
		t.Fatal("oauth success missing access_token (expected short-lived writeOnly field)")
	}
	if _, ok := oauthTags["refresh_token"]; ok {
		t.Fatal("oauth success must not include refresh_token")
	}
}

func testSensitiveHTTPArtifactBoundary(t *testing.T) {
	t.Parallel()
	// Run resource DTO carries Item snapshots as generic objects — they must not
	// declare signed URL / artifact blob fields on the DTO itself. Artifact bytes
	// require a separate authorized fetch path (not embedded in event/item JSON).
	runType := reflect.TypeOf(aapRunResourceDTO{})
	for i := 0; i < runType.NumField(); i++ {
		field := runType.Field(i)
		name := strings.ToLower(field.Name + field.Tag.Get("json"))
		for _, forbidden := range []string{"signedurl", "artifactbytes", "blob", "downloadurl", "presigned"} {
			if strings.Contains(strings.ReplaceAll(name, "_", ""), forbidden) {
				t.Fatalf("run DTO embeds artifact transport field %s", field.Name)
			}
		}
	}
	// Item snapshots from golden fixtures must also scan clean (no signed URLs).
	for _, name := range []string{"text", "tool_success", "workflow_tool", "approval_resume"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "protocolschema", "testdata", "aap", "v1", name+".snapshot.json"))
		if err != nil {
			t.Fatal(err)
		}
		var snapshot map[string]any
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		items, _ := snapshot["items"].([]any)
		for _, item := range items {
			encoded, _ := json.Marshal(item)
			if err := protocolevent.ScanPublicJSON(encoded); err != nil {
				t.Fatalf("%s item snapshot sensitive: %v", name, err)
			}
			lower := strings.ToLower(string(encoded))
			if strings.Contains(lower, "x-amz-signature=") || strings.Contains(lower, "signedurl") {
				t.Fatalf("%s item embeds signed URL material", name)
			}
		}
	}
}

func testSensitiveHTTPTokenQuery(t *testing.T) {
	t.Parallel()
	router, path, _ := buildSDKContractGoldenRouter(t, "text")
	leaked := "leaked-query-token-value-should-not-reflect"
	request := httptest.NewRequest(http.MethodGet, path+"?access_token="+leaked, nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("token query accepted")
	}
	if strings.Contains(response.Body.String(), leaked) {
		t.Fatalf("query token reflected: %s", response.Body.String())
	}
}

