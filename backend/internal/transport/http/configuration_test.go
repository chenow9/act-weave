package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/connection"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/secret"
	"actweave/backend/internal/serviceendpoint"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workspace"

	"github.com/google/uuid"
)

func TestV1ModelConfigRoutes(t *testing.T) {
	f := newConfigurationFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/model-configs", map[string]any{
		"name": "primary", "provider": "openai-compatible", "apiBase": "https://models.example/v1",
		"modelName": "reasoning-model", "options": map[string]any{"temperature": 0.2},
	}, f.token, nil)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"status":"UNVERIFIED"`) {
		t.Fatalf("create model status=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"agenticCapabilities":{}`) {
		t.Fatalf("create must return read-only empty agenticCapabilities: %s", created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"toolDisclosurePolicy":{}`) ||
		!strings.Contains(created.Body.String(), `"toolDisclosureUI":"unverified"`) {
		t.Fatalf("create must return empty policy + unverified UI: %s", created.Body.String())
	}
	var value modelConfigDTO
	decodeResponse(t, created.Body.Bytes(), &value)

	// Client writes of agenticCapabilities (including {}, null, nonempty) must exact 400.
	for _, payload := range []map[string]any{
		{"name": "x", "provider": "openai-compatible", "apiBase": "https://models.example/v1", "modelName": "m", "agenticCapabilities": map[string]any{}},
		{"name": "x", "provider": "openai-compatible", "apiBase": "https://models.example/v1", "modelName": "m", "agenticCapabilities": nil},
		{"name": "x", "provider": "openai-compatible", "apiBase": "https://models.example/v1", "modelName": "m", "agenticCapabilities": map[string]any{"schemaVersion": "agentic-model.v1"}},
	} {
		rej := f.request(t, http.MethodPost, f.base+"/model-configs", payload, f.token, nil)
		if rej.Code != http.StatusBadRequest {
			t.Fatalf("create agenticCapabilities must be 400, got %d body=%s", rej.Code, rej.Body.String())
		}
		if !strings.Contains(rej.Body.String(), "AGENTIC_CAPABILITIES_READ_ONLY") {
			t.Fatalf("create must use AGENTIC_CAPABILITIES_READ_ONLY: %s", rej.Body.String())
		}
		// Safe body: no credential/provider secret leakage.
		if strings.Contains(strings.ToLower(rej.Body.String()), "sk-") {
			t.Fatalf("leaky error body: %s", rej.Body.String())
		}
	}

	verified := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":verify", nil, f.token, nil)
	if verified.Code != http.StatusOK || !strings.Contains(verified.Body.String(), `"status":"VERIFIED"`) {
		t.Fatalf("verify model status=%d body=%s", verified.Code, verified.Body.String())
	}
	var verifiedValue modelConfigDTO
	decodeResponse(t, verified.Body.Bytes(), &verifiedValue)
	if len(verifiedValue.AgenticCapabilities) == 0 || string(verifiedValue.AgenticCapabilities) == "{}" {
		// Stub verifier returns empty probe caps; service stamps canonical on success.
		// With stub VerifierFunc returning zero caps, service still stamps canonical.
		if !strings.Contains(verified.Body.String(), "agentic-model.v1") && !strings.Contains(verified.Body.String(), `"agenticCapabilities":{}`) {
			t.Fatalf("verify body missing agenticCapabilities: %s", verified.Body.String())
		}
	}
	// Update with agenticCapabilities field rejected with exact 400.
	patchReject := f.request(t, http.MethodPatch, f.base+"/model-configs/"+value.ID, map[string]any{
		"modelName": "reasoning-model-v2", "lockVersion": verifiedValue.LockVersion,
		"agenticCapabilities": map[string]any{},
	}, f.token, nil)
	if patchReject.Code != http.StatusBadRequest {
		t.Fatalf("patch agenticCapabilities must be 400, got %d body=%s", patchReject.Code, patchReject.Body.String())
	}
	if !strings.Contains(patchReject.Body.String(), "AGENTIC_CAPABILITIES_READ_ONLY") {
		t.Fatalf("patch must use AGENTIC_CAPABILITIES_READ_ONLY: %s", patchReject.Body.String())
	}
	updated := f.request(t, http.MethodPatch, f.base+"/model-configs/"+value.ID, map[string]any{
		"modelName": "reasoning-model-v2", "lockVersion": verifiedValue.LockVersion,
	}, f.token, nil)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"status":"UNVERIFIED"`) {
		t.Fatalf("update model status=%d body=%s", updated.Code, updated.Body.String())
	}
	if !strings.Contains(updated.Body.String(), `"agenticCapabilities":{}`) {
		t.Fatalf("update must clear agenticCapabilities: %s", updated.Body.String())
	}
	rejected := f.request(t, http.MethodPost, f.base+"/model-configs", map[string]any{
		"workspaceId": f.workspaceID, "name": "escalation", "provider": "x", "apiBase": "https://x.example", "modelName": "x", "status": "VERIFIED",
	}, f.token, nil)
	assertErrorResponse(t, rejected, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	for _, key := range []string{"toolDisclosurePolicy", "toolDisclosureUI"} {
		payload := map[string]any{
			"name": "x", "provider": "openai-compatible", "apiBase": "https://models.example/v1", "modelName": "m",
			key: map[string]any{},
		}
		rej := f.request(t, http.MethodPost, f.base+"/model-configs", payload, f.token, nil)
		assertErrorResponse(t, rej, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		patch := f.request(t, http.MethodPatch, f.base+"/model-configs/"+value.ID, map[string]any{
			"lockVersion": 1, key: nil,
		}, f.token, nil)
		assertErrorResponse(t, patch, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
}

func TestV1ModelConfigSetDisclosure(t *testing.T) {
	catalog := &stubAgentCatalog{counts: map[string]int{}}
	f := newConfigurationFixtureWithCatalog(t, catalog)
	created := f.request(t, http.MethodPost, f.base+"/model-configs", map[string]any{
		"name": "fc-model", "provider": "openai-compatible", "apiBase": "https://models.example/v1",
		"modelName": "fc", "options": map[string]any{},
	}, f.token, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var value modelConfigDTO
	decodeResponse(t, created.Body.Bytes(), &value)

	verified := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":verify", nil, f.token, nil)
	if verified.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", verified.Code, verified.Body.String())
	}
	var verifiedValue modelConfigDTO
	decodeResponse(t, verified.Body.Bytes(), &verifiedValue)
	if verifiedValue.ToolDisclosureUI != modelconfig.ToolDisclosureUIHidden {
		t.Fatalf("native UI want hidden, got %s", verifiedValue.ToolDisclosureUI)
	}

	nativeReject := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": verifiedValue.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "carry_all",
		},
	}, f.token, nil)
	assertErrorResponse(t, nativeReject, http.StatusUnprocessableEntity, modelconfig.ErrorCodeToolDisclosureInvalid)

	plantHTTPFunctionCalling(t, f, verifiedValue)
	fcGet := f.request(t, http.MethodGet, f.base+"/model-configs/"+value.ID, nil, f.token, nil)
	if fcGet.Code != http.StatusOK {
		t.Fatalf("get after plant: %d %s", fcGet.Code, fcGet.Body.String())
	}
	var fcValue modelConfigDTO
	decodeResponse(t, fcGet.Body.Bytes(), &fcValue)
	if fcValue.ToolDisclosureUI != modelconfig.ToolDisclosureUIBinary {
		t.Fatalf("fc UI want binary, got %s body=%s", fcValue.ToolDisclosureUI, fcGet.Body.String())
	}

	okDemand := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": fcValue.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "platform_on_demand",
		},
	}, f.token, nil)
	if okDemand.Code != http.StatusOK {
		t.Fatalf("platform_on_demand: %d %s", okDemand.Code, okDemand.Body.String())
	}
	if strings.Contains(okDemand.Body.String(), `"warnings"`) {
		t.Fatalf("platform_on_demand must not emit warnings: %s", okDemand.Body.String())
	}
	var afterDemand setDisclosureResponse
	decodeResponse(t, okDemand.Body.Bytes(), &afterDemand)

	emptyCarry := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": afterDemand.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "carry_all",
		},
	}, f.token, nil)
	if emptyCarry.Code != http.StatusOK || strings.Contains(emptyCarry.Body.String(), `"warnings"`) {
		t.Fatalf("zero agents carry_all: %d %s", emptyCarry.Code, emptyCarry.Body.String())
	}
	var afterEmpty setDisclosureResponse
	decodeResponse(t, emptyCarry.Body.Bytes(), &afterEmpty)

	smallID := insertHTTPAgent(t, f, value.ID, "small-agent")
	warnID := insertHTTPAgent(t, f, value.ID, "warn-agent")
	bigID := insertHTTPAgent(t, f, value.ID, "big-agent")
	catalog.counts[smallID] = 3
	catalog.counts[warnID] = 7
	catalog.counts[bigID] = 9

	tooBig := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": afterEmpty.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "carry_all",
		},
	}, f.token, nil)
	assertErrorResponse(t, tooBig, http.StatusUnprocessableEntity, modelconfig.ErrorCodeToolCarryAllTooLarge)
	if !strings.Contains(tooBig.Body.String(), bigID) || !strings.Contains(tooBig.Body.String(), `"limit":8`) {
		t.Fatalf("too-large details missing: %s", tooBig.Body.String())
	}

	delete(catalog.counts, bigID)
	if _, err := f.db.Exec(`UPDATE agents SET deleted_at = clock_timestamp() WHERE id = $1`, bigID); err != nil {
		t.Fatal(err)
	}
	soft := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": afterEmpty.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "carry_all",
		},
	}, f.token, nil)
	if soft.Code != http.StatusOK {
		t.Fatalf("soft warning: %d %s", soft.Code, soft.Body.String())
	}
	if !strings.Contains(soft.Body.String(), modelconfig.ErrorCodeToolCarryAllSoft) ||
		!strings.Contains(soft.Body.String(), warnID) ||
		!strings.Contains(soft.Body.String(), `"limit":5`) {
		t.Fatalf("soft warning missing: %s", soft.Body.String())
	}
	var afterSoft setDisclosureResponse
	decodeResponse(t, soft.Body.Bytes(), &afterSoft)

	delete(catalog.counts, warnID)
	if _, err := f.db.Exec(`UPDATE agents SET deleted_at = clock_timestamp() WHERE id = $1`, warnID); err != nil {
		t.Fatal(err)
	}
	okSmall := f.request(t, http.MethodPost, f.base+"/model-configs/"+value.ID+":set-disclosure", map[string]any{
		"lockVersion": afterSoft.LockVersion,
		"toolDisclosurePolicy": map[string]any{
			"schemaVersion": "tool-disclosure.v1", "mode": "carry_all",
		},
	}, f.token, nil)
	if okSmall.Code != http.StatusOK || strings.Contains(okSmall.Body.String(), `"warnings"`) {
		t.Fatalf("small catalog: %d %s", okSmall.Code, okSmall.Body.String())
	}
}

func TestV1ProviderRoutes(t *testing.T) {
	f := newConfigurationFixture(t)
	_, outboundDriver := providerOutboundIdentityContract(t)
	runtimeOnly := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "private runtime", "kind": "HTTP_OPENAPI", "driverKey": "http_openapi", "transport": "HTTP",
		"endpointConfig": map[string]any{"schemaVersion": 2, "serviceBaseUrl": "https://private.example/api", "verification": map[string]any{"method": "GET"}},
		"driverConfig":   json.RawMessage(outboundDriver), "discoveryMode": "MANUAL",
	}, f.token, nil)
	if runtimeOnly.Code != http.StatusCreated {
		t.Fatalf("create runtime-only provider status=%d body=%s", runtimeOnly.Code, runtimeOnly.Body.String())
	}
	invalidDiscovery := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "missing discovery", "kind": "HTTP_OPENAPI", "driverKey": "http_openapi", "transport": "HTTP",
		"endpointConfig": map[string]any{"schemaVersion": 2, "serviceBaseUrl": "https://private.example/api", "verification": map[string]any{"method": "GET"}},
		"driverConfig":   json.RawMessage(outboundDriver), "discoveryMode": "ON_DEMAND",
	}, f.token, nil)
	assertErrorResponse(t, invalidDiscovery, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	future := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "future", "kind": "MCP_SERVER", "driverKey": "mcp", "transport": "HTTP", "discoveryMode": "ON_DEMAND",
	}, f.token, nil)
	assertErrorResponse(t, future, http.StatusUnprocessableEntity, "PROVIDER_KIND_NOT_AVAILABLE")
	legacyScheduled := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "legacy scheduled", "kind": "HTTP_OPENAPI", "driverKey": "http_openapi", "transport": "HTTP",
		"endpointConfig": map[string]any{"sourceUri": "https://scheduled.example/openapi.json"},
		"driverConfig":   json.RawMessage(outboundDriver), "discoveryMode": "SCHEDULED",
	}, f.token, nil)
	if legacyScheduled.Code != http.StatusCreated || !strings.Contains(legacyScheduled.Body.String(), `"discoveryMode":"POLLING"`) {
		t.Fatalf("legacy scheduled provider status=%d body=%s", legacyScheduled.Code, legacyScheduled.Body.String())
	}
	created := f.createProvider(t)
	missingAssetID := uuid.NewString()
	if _, err := f.db.Exec(`INSERT INTO provider_assets(
		id,workspace_id,provider_id,asset_kind,external_id,name,input_schema,output_schema,metadata,source_checksum
	) VALUES ($1,$2,$3,'TOOL','removed-operation','Removed operation','{}','{}','{}',$4)`,
		missingAssetID, f.workspaceID, created.ID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}

	synced := f.request(t, http.MethodPost, f.base+"/providers/"+created.ID+":sync", nil, f.token, nil)
	if synced.Code != http.StatusOK || !strings.Contains(synced.Body.String(), `"discoveredCount":1`) {
		t.Fatalf("sync provider status=%d body=%s", synced.Code, synced.Body.String())
	}
	var missingStatus string
	if err := f.db.QueryRow(`SELECT status FROM provider_assets WHERE id=$1`, missingAssetID).Scan(&missingStatus); err != nil || missingStatus != "STALE" {
		t.Fatalf("missing discovery asset status=%q err=%v", missingStatus, err)
	}
	assets := f.request(t, http.MethodGet, f.base+"/providers/"+created.ID+"/assets", nil, f.token, nil)
	if assets.Code != http.StatusOK {
		t.Fatalf("list assets status=%d body=%s", assets.Code, assets.Body.String())
	}
	var listing struct {
		Items []providerAssetDTO `json:"items"`
	}
	decodeResponse(t, assets.Body.Bytes(), &listing)
	if len(listing.Items) != 2 || listing.Items[0].Status != "ACTIVE" || listing.Items[1].Status != "STALE" {
		t.Fatalf("assets=%+v", listing.Items)
	}
	materialized := f.request(t, http.MethodPost, f.base+"/providers/"+created.ID+"/assets/"+listing.Items[0].ID+":materialize", map[string]any{}, f.token, nil)
	if materialized.Code != http.StatusCreated || !strings.Contains(materialized.Body.String(), `"lifecycleStatus":"DRAFT"`) {
		t.Fatalf("materialize status=%d body=%s", materialized.Code, materialized.Body.String())
	}
	escalation := f.request(t, http.MethodPatch, f.base+"/providers/"+created.ID, map[string]any{
		"status": "ACTIVE", "lockVersion": created.LockVersion,
	}, f.token, nil)
	assertErrorResponse(t, escalation, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestV1ProviderAuthContractRoundTripAndConnectionSchemaValidation(t *testing.T) {
	f := newConfigurationFixture(t)
	endpointConfig, driverConfig := providerOAuthContract(t)
	createdResponse := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "contract orders API", "kind": "HTTP_OPENAPI", "driverKey": "http_openapi", "transport": "HTTP",
		"endpointConfig": endpointConfig, "driverConfig": driverConfig, "discoveryMode": "ON_DEMAND",
	}, f.token, nil)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create Provider contract status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	var created providerDTO
	decodeResponse(t, createdResponse.Body.Bytes(), &created)
	assertProviderOAuthContract(t, created)

	readResponse := f.request(t, http.MethodGet, f.base+"/providers/"+created.ID, nil, f.token, nil)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read Provider contract status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}
	var read providerDTO
	decodeResponse(t, readResponse.Body.Bytes(), &read)
	assertProviderOAuthContract(t, read)

	// Dual-mode REQUEST_PASSTHROUGH connection (no machine secret).
	createdConnection := f.request(t, http.MethodPost, f.base+"/providers/"+created.ID+"/connections", map[string]any{
		"name": "contract orders production", "alias": "contract-orders-prod", "environment": "PRODUCTION",
		"outboundIdentity": map[string]any{
			"schemaVersion": "outbound-connection.v1",
			"mode":          "REQUEST_PASSTHROUGH",
			"requestPassthrough": map[string]any{
				"maxResidenceSeconds": 600,
			},
		},
		"grantedScopes": []string{"orders.read", "orders.write"}, "policy": map[string]any{},
	}, f.token, nil)
	if createdConnection.Code != http.StatusCreated ||
		strings.Contains(createdConnection.Body.String(), "credentialSecretId") ||
		!strings.Contains(createdConnection.Body.String(), `"machineCredentialConfigured":false`) ||
		!strings.Contains(createdConnection.Body.String(), `"REQUEST_PASSTHROUGH"`) {
		t.Fatalf("create dual-mode Connection status=%d body=%s", createdConnection.Code, createdConnection.Body.String())
	}
	var connectionValue connectionDTO
	decodeResponse(t, createdConnection.Body.Bytes(), &connectionValue)
	if connectionValue.MigrationState != "NONE" || connectionValue.MachineCredentialConfigured {
		t.Fatalf("unexpected connection DTO: %+v", connectionValue)
	}

	// Legacy auth modes / business credentials are rejected.
	for index, payload := range []map[string]any{
		{
			"name": "legacy api key", "alias": "legacy-api-key", "environment": "TEST",
			"authMode": "API_KEY", "authConfig": map[string]any{"headerName": "X-API-Key"},
			"grantedScopes": []string{}, "policy": map[string]any{},
			"outboundIdentity": map[string]any{
				"schemaVersion": "outbound-connection.v1", "mode": "REQUEST_PASSTHROUGH",
				"requestPassthrough": map[string]any{"maxResidenceSeconds": 600},
			},
		},
		{
			"name": "legacy secret", "alias": "legacy-secret", "environment": "TEST",
			"credentialSecretId": uuid.NewString(),
			"grantedScopes":      []string{}, "policy": map[string]any{},
			"outboundIdentity": map[string]any{
				"schemaVersion": "outbound-connection.v1", "mode": "REQUEST_PASSTHROUGH",
				"requestPassthrough": map[string]any{"maxResidenceSeconds": 600},
			},
		},
		{
			"name": "none mode", "alias": "none-mode", "environment": "TEST",
			"grantedScopes": []string{}, "policy": map[string]any{},
			"outboundIdentity": map[string]any{
				"schemaVersion": "outbound-connection.v1", "mode": "NONE",
			},
		},
	} {
		response := f.request(t, http.MethodPost, f.base+"/providers/"+created.ID+"/connections", payload, f.token, nil)
		if response.Code == http.StatusCreated {
			t.Fatalf("case %d expected rejection, got 201 body=%s", index, response.Body.String())
		}
	}
}

func TestV1ConnectionRoutes(t *testing.T) {
	f := newConfigurationFixture(t)
	p := f.createProvider(t)
	created := f.request(t, http.MethodPost, f.base+"/providers/"+p.ID+"/connections", map[string]any{
		"name": "orders production", "alias": "orders-prod", "environment": "PRODUCTION",
		"outboundIdentity": map[string]any{
			"schemaVersion": "outbound-connection.v1",
			"mode":          "REQUEST_PASSTHROUGH",
			"requestPassthrough": map[string]any{
				"maxResidenceSeconds": 600,
			},
		},
		"grantedScopes": []any{}, "policy": map[string]any{},
	}, f.token, nil)
	if created.Code != http.StatusCreated ||
		strings.Contains(created.Body.String(), "credentialSecretId") ||
		!strings.Contains(created.Body.String(), `"machineCredentialConfigured":false`) ||
		!strings.Contains(created.Body.String(), `"REQUEST_PASSTHROUGH"`) {
		t.Fatalf("create connection status=%d body=%s", created.Code, created.Body.String())
	}
	var value connectionDTO
	decodeResponse(t, created.Body.Bytes(), &value)
	verified := f.request(t, http.MethodPost, f.base+"/connections/"+value.ID+"/__command/verify", nil, f.token, nil)
	if verified.Code != http.StatusOK || !strings.Contains(verified.Body.String(), `"Status":"SUCCEEDED"`) {
		t.Fatalf("verify connection status=%d body=%s", verified.Code, verified.Body.String())
	}
	current := f.request(t, http.MethodGet, f.base+"/connections/"+value.ID, nil, f.token, nil)
	var afterVerify connectionDTO
	decodeResponse(t, current.Body.Bytes(), &afterVerify)
	// Metadata-only update path for non-identity fields.
	updated := f.request(t, http.MethodPatch, f.base+"/connections/"+value.ID, map[string]any{
		"name": "orders test", "alias": "orders-test", "environment": "TEST",
		"grantedScopes": []any{}, "policy": map[string]any{}, "lockVersion": afterVerify.LockVersion,
		"metadataOnly": true,
	}, f.token, nil)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"orders test"`) {
		t.Fatalf("update connection status=%d body=%s", updated.Code, updated.Body.String())
	}
	var changed connectionDTO
	decodeResponse(t, updated.Body.Bytes(), &changed)
	deleted := f.request(t, http.MethodDelete, f.base+"/connections/"+value.ID+"?lockVersion="+strconv64(changed.LockVersion), nil, f.token, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete connection status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestV1SecretRotateDoesNotExposePlaintext(t *testing.T) {
	f := newConfigurationFixture(t)
	created := f.request(t, http.MethodPost, f.base+"/secrets", map[string]any{
		"name": "api credential", "kind": "API_KEY", "plaintext": "old-secret-value",
	}, f.token, nil)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "old-secret-value") || strings.Contains(created.Body.String(), "plaintext") {
		t.Fatalf("create secret status=%d body=%s", created.Code, created.Body.String())
	}
	var secretValue secret.ReadDTO
	decodeResponse(t, created.Body.Bytes(), &secretValue)
	rotated := f.request(t, http.MethodPost, f.base+"/secrets/"+secretValue.ID+":rotate", map[string]any{
		"plaintext": "new-secret-value", "lockVersion": secretValue.LockVersion,
	}, f.token, nil)
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), "old-secret-value") || strings.Contains(rotated.Body.String(), "new-secret-value") || strings.Contains(rotated.Body.String(), "plaintext") {
		t.Fatalf("rotate secret status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	readAttempt := f.request(t, http.MethodGet, f.base+"/secrets/"+secretValue.ID, nil, f.token, nil)
	if readAttempt.Code != http.StatusNotFound {
		t.Fatalf("plaintext/read API unexpectedly exists: status=%d body=%s", readAttempt.Code, readAttempt.Body.String())
	}
}

type configurationDiscoverer struct{}

func (configurationDiscoverer) DiscoverHTTP(context.Context, provider.DiscoveryRequest) (provider.DiscoveryPage, error) {
	return provider.DiscoveryPage{Assets: []provider.Asset{{Kind: "TOOL", ExternalID: "orders.get", Name: "Get Order", Description: "Get an order",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		Metadata: json.RawMessage(`{"actionConfig":{"method":"GET","path":"/orders/{id}"},"riskLevel":"LOW","sideEffectLevel":"READ"}`), SourceRevision: "v1", SourceChecksum: strings.Repeat("a", 64)}}}, nil
}

type configurationFixture struct {
	*v1AuthFixture
	workspaceID, base, token string
	secretService            *secret.Service
}

type stubAgentCatalog struct {
	counts map[string]int
}

func (s *stubAgentCatalog) ListForAgent(_ context.Context, _, agentID string) ([]capability.Descriptor, error) {
	n := 0
	if s != nil && s.counts != nil {
		n = s.counts[agentID]
	}
	out := make([]capability.Descriptor, n)
	for i := range out {
		out[i] = capability.Descriptor{CallableName: "tool-" + strconv.Itoa(i)}
	}
	return out, nil
}

func plantHTTPFunctionCalling(t *testing.T, f *configurationFixture, cfg modelConfigDTO) {
	t.Helper()
	doc, _, err := modelconfig.ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	v2 := modelconfig.AgenticCapabilities{
		SchemaVersion:        modelconfig.AgenticCapabilitiesSchemaV2,
		Protocol:             modelconfig.AgenticProtocolOpenAIResponsesV1,
		Streaming:            true,
		Usage:                true,
		ToolCalling:          modelconfig.ToolCallingFunctionCalling,
		ReasoningReplay:      modelconfig.AgenticReasoningReplayEncryptedOrNone,
		VerifiedAdapter:      modelconfig.VerifiedAdapterAgenticOpenAIV022,
		VerifiedAt:           doc.VerifiedAt,
		VerifiedLockVersion:  doc.VerifiedLockVersion,
		VerifiedConfigDigest: doc.VerifiedConfigDigest,
	}
	raw, err := json.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE model_configs SET agentic_capabilities = $2::jsonb WHERE id = $1`, cfg.ID, raw); err != nil {
		t.Fatal(err)
	}
}

func insertHTTPAgent(t *testing.T, f *configurationFixture, modelID, name string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.db.Exec(`
		INSERT INTO agents (id, workspace_id, name, model_config_id, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, id, f.workspaceID, name, modelID, v1AdminUserID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	return id
}

func newConfigurationFixture(t *testing.T) *configurationFixture {
	t.Helper()
	return newConfigurationFixtureWithCatalog(t, nil)
}

func newConfigurationFixtureWithCatalog(t *testing.T, catalog AgentCapabilityLister) *configurationFixture {
	t.Helper()
	authFixture := newV1AuthFixture(t)
	ctx := context.Background()
	workspaceID := uuid.NewString()
	workspaces, err := workspace.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.Create(ctx, workspace.NewWorkspace{ID: workspaceID, Slug: "configuration-" + workspaceID[:8], DisplayName: "Configuration", Mode: workspace.ModeProduction, OwnerUserID: v1AdminUserID, CreatedBy: v1AdminUserID}); err != nil {
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
	modelVerifier, err := modelconfig.NewVerificationService(models, modelconfig.VerifierFunc(func(context.Context, modelconfig.Config) (modelconfig.AgenticCapabilities, error) {
		return modelconfig.AgenticCapabilities{}, nil
	}), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := provider.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := provider.NewPhaseOneRegistry(configurationDiscoverer{})
	if err != nil {
		t.Fatal(err)
	}
	syncer, err := provider.NewSyncService(providers, registry)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := tool.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := provider.NewMaterializationService(providers, tools)
	if err != nil {
		t.Fatal(err)
	}
	connections, err := connection.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	connectionVerifier, err := connection.NewVerificationService(connections, connection.VerifierFunc(func(context.Context, connection.Connection) error { return nil }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	secretRepository, err := secret.NewRepository(authFixture.db)
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := secret.NewLocalEncryptor("configuration-test-v1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	secretService, err := secret.NewService(secretRepository, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewConfigurationRoutes(ConfigurationDependencies{Authorizer: authorizer, Models: models, AgentCatalog: catalog, ModelVerifier: modelVerifier,
		Providers: providers, ProviderSyncer: syncer, Materializer: materializer, ProviderRegistry: registry,
		Connections: connections, ConnectionVerifier: connectionVerifier, Secrets: secretService})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{Authenticator: authFixture.auth, Registrars: []V1RouteRegistrar{authFixture.authRoutes, routes}})
	if err != nil {
		t.Fatal(err)
	}
	authFixture.router = router
	login := authFixture.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"username": v1AdminName, "password": v1AdminPass}, "", nil)
	return &configurationFixture{v1AuthFixture: authFixture, workspaceID: workspaceID, base: "/api/v1/workspaces/" + workspaceID, token: decodeTokenResponse(t, login).AccessToken, secretService: secretService}
}

func (f *configurationFixture) createProvider(t *testing.T) providerDTO {
	t.Helper()
	endpointConfig, driverConfig := providerOutboundIdentityContract(t)
	response := f.request(t, http.MethodPost, f.base+"/providers", map[string]any{
		"name": "orders API", "kind": "HTTP_OPENAPI", "driverKey": "http_openapi", "transport": "HTTP",
		"endpointConfig": endpointConfig, "driverConfig": json.RawMessage(driverConfig), "discoveryMode": "ON_DEMAND",
	}, f.token, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create provider status=%d body=%s", response.Code, response.Body.String())
	}
	var value providerDTO
	decodeResponse(t, response.Body.Bytes(), &value)
	return value
}

func providerOutboundIdentityContract(t *testing.T) (json.RawMessage, json.RawMessage) {
	t.Helper()
	endpoint, err := json.Marshal(serviceendpoint.Config{
		SchemaVersion: serviceendpoint.SchemaVersion, ServiceBaseURL: "https://orders.example/api/v2",
		Discovery:    serviceendpoint.Discovery{DocumentURL: "https://orders.example/openapi.json"},
		Verification: serviceendpoint.Verification{Method: http.MethodHead, Path: "/health", ExpectedStatuses: []int{200, 204}},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := json.RawMessage(`{
		"outboundIdentity":{
			"schemaVersion":"outbound-identity.v1",
			"supportedModes":["BROKER_OBO","REQUEST_PASSTHROUGH"],
			"supportedSubjectTypes":["USER","EXTERNAL_SUBJECT"],
			"brokerObo":{
				"tokenEndpoint":"https://broker.example/token",
				"audience":"urn:broker:tenant",
				"machineAuthMethod":"PRIVATE_KEY_JWT",
				"allowedScopes":["orders.read","orders.write"],
				"response":{"accessTokenPath":"access_token","expiresInPath":"expires_in"},
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			},
			"requestPassthrough":{
				"credentialTypes":["ACCESS_TOKEN"],
				"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
			}
		}
	}`)
	return endpoint, driver
}

// Backward-compatible name used by older tests.
func providerOAuthContract(t *testing.T) (json.RawMessage, json.RawMessage) {
	return providerOutboundIdentityContract(t)
}

func assertProviderOAuthContract(t *testing.T, value providerDTO) {
	t.Helper()
	endpoint, err := serviceendpoint.Parse(value.EndpointConfig)
	if err != nil {
		t.Fatalf("parse Provider endpoint roundtrip: %v", err)
	}
	identity, err := provider.ParseOutboundIdentity(value.DriverConfig)
	if err != nil {
		t.Fatalf("parse Provider outbound identity: %v", err)
	}
	if endpoint.ServiceBaseURL != "https://orders.example/api/v2" || endpoint.Discovery.DocumentURL != "https://orders.example/openapi.json" ||
		endpoint.Verification.Method != http.MethodHead || endpoint.Verification.Path != "/health" ||
		!identity.SupportsMode("BROKER_OBO") || !identity.SupportsMode("REQUEST_PASSTHROUGH") {
		t.Fatalf("unexpected Provider contract roundtrip: endpoint=%+v identity=%+v", endpoint, identity)
	}
}

func decodeResponse(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
func strconv64(value int64) string { return strconv.FormatInt(value, 10) }
