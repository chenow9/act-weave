package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/config"
	"actweave/backend/internal/sessioncontext"
)

const (
	aapProfileWorkspaceID = "b28f1f2e-7b5a-7c3d-8e9f-123456789001"
	aapProfileAgentID     = "b28f1f2e-7b5a-7c3d-8e9f-123456789002"
	aapProfilePromptID    = "b28f1f2e-7b5a-7c3d-8e9f-123456789003"
	aapProfileCapability1 = "b28f1f2e-7b5a-7c3d-8e9f-123456789004"
	aapProfileRelease1    = "b28f1f2e-7b5a-7c3d-8e9f-123456789005"
	aapProfileCapability2 = "b28f1f2e-7b5a-7c3d-8e9f-123456789006"
	aapProfileRelease2    = "b28f1f2e-7b5a-7c3d-8e9f-123456789007"
)

func TestAAPAgentProfile(t *testing.T) {
	authorizer := &aapProfileAuthorizer{}
	store := &aapProfileStore{value: aapPublishedAgentSummary()}
	catalog := &aapProfileCatalog{values: aapProfileDescriptors()}
	routes, err := NewAAPAgentProfileRoutes(authorizer, store, catalog)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapProfileTokenAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + aapProfileWorkspaceID +
		"/agents/" + aapProfileAgentID + "/profile"

	t.Run("returns versioned allowlisted published profile", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapProfileRequest(path, "aap-profile-token"))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var profile aapAgentProfileDTO
		if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
			t.Fatal(err)
		}
		if profile.Object != "agent_profile" || profile.ID != aapProfileAgentID ||
			profile.Name != "Published Support Agent" || profile.Description != "Helps with published support tasks." ||
			!strings.HasPrefix(profile.Version, "sha256:") || len(profile.Version) != len("sha256:")+64 ||
			len(profile.SupportedContent) != 1 || profile.SupportedContent[0].Type != "message" ||
			len(profile.SupportedContent[0].Parts) != 1 || profile.SupportedContent[0].Parts[0] != "text" ||
			len(profile.Capabilities) != 2 || profile.Capabilities[0].Kind != "tool" ||
			profile.Capabilities[0].Count != 1 || !profile.Capabilities[0].MayRequireConfirmation ||
			profile.Capabilities[1].Kind != "workflow" || profile.Capabilities[1].Count != 1 ||
			profile.Capabilities[1].MayRequireConfirmation ||
			!profile.InteractionRequirements.Approval.Supported ||
			!profile.InteractionRequirements.Approval.MayBeRequired ||
			profile.InteractionRequirements.Approval.RequiredScope != "interaction:decide" {
			t.Fatalf("profile=%+v", profile)
		}
		if response.Header().Get("ETag") != `"`+profile.Version+`"` ||
			response.Header().Get("Cache-Control") != "private, max-age=60" {
			t.Fatalf("headers=%v", response.Header())
		}
		assertAAPProfileAllowlist(t, response.Body.Bytes())
		for _, forbidden := range []string{
			"system-prompt-secret", "model-config-secret", "connection-internal-secret",
			"refund_internal_tool", "workflow_private_topology", "inputSchema", "outputSchema",
			"capabilityId", "releaseId", "connectionId", "currentPromptRevisionId",
			"modelConfigId", "workspaceId", "createdBy", "updatedBy", "lockVersion",
		} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("profile leaked %q: %s", forbidden, response.Body.String())
			}
		}
		if authorizer.last.Action != agentaccessauth.ActionAgentProfileRead ||
			authorizer.last.Resource != (agentaccessauth.AAPAuthorizationResource{}) ||
			authorizer.last.Principal.WorkspaceID != aapProfileWorkspaceID {
			t.Fatalf("authorization request=%+v", authorizer.last)
		}
	})

	t.Run("supports strong weak and wildcard conditional reads", func(t *testing.T) {
		first := httptest.NewRecorder()
		router.ServeHTTP(first, aapProfileRequest(path, "aap-profile-token"))
		etag := first.Header().Get("ETag")
		for _, condition := range []string{etag, "W/" + etag, `"unrelated", ` + etag, "*"} {
			request := aapProfileRequest(path, "aap-profile-token")
			request.Header.Set("If-None-Match", condition)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotModified || response.Body.Len() != 0 ||
				response.Header().Get("ETag") != etag {
				t.Fatalf("condition=%q status=%d headers=%v body=%s",
					condition, response.Code, response.Header(), response.Body.String())
			}
		}
	})

	t.Run("changes opaque version for behavioral publication changes", func(t *testing.T) {
		base := aapPublishedAgentSummary()
		first, _, err := projectAAPAgentProfile(base, aapProfileDescriptors(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		changedPrompt := "b28f1f2e-7b5a-7c3d-8e9f-123456789099"
		base.CurrentPromptRevisionID = &changedPrompt
		second, _, err := projectAAPAgentProfile(base, aapProfileDescriptors(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		descriptors := aapProfileDescriptors()
		descriptors[0].ConnectionID = "connection-private-change"
		third, _, err := projectAAPAgentProfile(aapPublishedAgentSummary(), descriptors, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if first.Version == second.Version || first.Version == third.Version ||
			first.Description != second.Description || first.Description != third.Description {
			t.Fatalf("versions first=%q second=%q third=%q", first.Version, second.Version, third.Version)
		}
	})

	t.Run("conceals wrong scope before authorization and storage", func(t *testing.T) {
		beforeAuthorization, beforeRead := authorizer.calls, store.calls
		wrongPath := "/api/agent-access/v1/workspaces/b28f1f2e-7b5a-7c3d-8e9f-123456789088/agents/" +
			aapProfileAgentID + "/profile"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapProfileRequest(wrongPath, "aap-profile-token"))
		assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		if authorizer.calls != beforeAuthorization || store.calls != beforeRead {
			t.Fatalf("cross-scope request reached dependencies: authorization=%d store=%d",
				authorizer.calls-beforeAuthorization, store.calls-beforeRead)
		}
	})

	t.Run("conceals denied missing disabled and unpublished agents", func(t *testing.T) {
		tests := []struct {
			name         string
			authorizeErr error
			storeErr     error
			mutate       func(*agent.Summary)
		}{
			{name: "authorization", authorizeErr: agentaccessauth.ErrAAPAuthorizationNotVisible},
			{name: "missing", storeErr: agent.ErrNotFound},
			{name: "disabled", mutate: func(value *agent.Summary) { value.Status = agent.StatusDisabled }},
			{name: "unpublished", mutate: func(value *agent.Summary) { value.CurrentPromptRevisionID = nil }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authorizer.err = test.authorizeErr
				store.err = test.storeErr
				store.value = aapPublishedAgentSummary()
				if test.mutate != nil {
					test.mutate(&store.value)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, aapProfileRequest(path, "aap-profile-token"))
				assertAAPRouterError(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
				authorizer.err, store.err = nil, nil
			})
		}
		store.value = aapPublishedAgentSummary()
	})

	t.Run("fails closed on an unknown internal capability kind", func(t *testing.T) {
		catalog.values = []capability.Descriptor{{
			CapabilityID: aapProfileCapability1, ReleaseID: aapProfileRelease1, Kind: "INTERNAL_AGENT_SKILL",
		}}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapProfileRequest(path, "aap-profile-token"))
		body := assertAAPRouterError(t, response, http.StatusServiceUnavailable, "INTERNAL_ERROR")
		if !body.Error.Retryable || strings.Contains(response.Body.String(), "INTERNAL_AGENT_SKILL") {
			t.Fatalf("error=%+v body=%s", body.Error, response.Body.String())
		}
		catalog.values = aapProfileDescriptors()
	})

	if _, err := NewAAPAgentProfileRoutes(nil, store, catalog); err == nil {
		t.Fatal("expected nil authorizer rejection")
	}
	if _, err := NewAAPAgentProfileRoutes(authorizer, nil, catalog); err == nil {
		t.Fatal("expected nil store rejection")
	}
	if _, err := NewAAPAgentProfileRoutes(authorizer, store, nil); err == nil {
		t.Fatal("expected nil catalog rejection")
	}
}

// TestAAPAgentProfileA2UIAdvertisement covers KD-15 parts composition and ETag seed.
func TestAAPAgentProfileA2UIAdvertisement(t *testing.T) {
	gate := &config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, MaxBytes: 10 << 20,
		AllowedMediaTypes: []string{"image/png", "application/pdf"},
	}

	t.Run("parts composition files x a2ui", func(t *testing.T) {
		cases := []struct {
			name            string
			files           bool
			a2ui            bool
			wantParts       []string
			wantA2UIObj     bool
			wantConstraints bool
		}{
			{name: "default", files: false, a2ui: false, wantParts: []string{"text"}},
			{name: "files only", files: true, a2ui: false, wantParts: []string{"text", "input_file"}, wantConstraints: true},
			{name: "a2ui only", files: false, a2ui: true, wantParts: []string{"text", "a2ui"}, wantA2UIObj: true},
			{name: "files + a2ui", files: true, a2ui: true, wantParts: []string{"text", "input_file", "a2ui"}, wantA2UIObj: true, wantConstraints: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				summary := aapPublishedAgentSummary()
				if tc.a2ui {
					summary.ContextPolicy = aapEnableA2UIPolicyJSON(true)
				}
				profile, _, err := projectAAPAgentProfile(summary, aapProfileDescriptors(), tc.files, gate)
				if err != nil {
					t.Fatal(err)
				}
				if len(profile.SupportedContent) == 0 || profile.SupportedContent[0].Type != "message" {
					t.Fatalf("supportedContent=%+v", profile.SupportedContent)
				}
				if !reflect.DeepEqual(profile.SupportedContent[0].Parts, tc.wantParts) {
					t.Fatalf("parts=%v want=%v", profile.SupportedContent[0].Parts, tc.wantParts)
				}
				if tc.wantConstraints {
					if len(profile.SupportedContent) < 2 || profile.SupportedContent[1].Type != "input_file_constraints" {
						t.Fatalf("expected input_file_constraints: %+v", profile.SupportedContent)
					}
				} else if len(profile.SupportedContent) != 1 {
					t.Fatalf("unexpected extra content entries: %+v", profile.SupportedContent)
				}
				if tc.wantA2UIObj {
					if profile.A2UI == nil || !profile.A2UI.Enabled ||
						profile.A2UI.Delivery != aapA2UIDelivery ||
						profile.A2UI.Streaming || profile.A2UI.Actions ||
						profile.A2UI.MaxSurfaceBytes != a2ui.MaxSurfaceBytes ||
						profile.A2UI.SpecHint != a2ui.EnvelopeVersionV1 {
						t.Fatalf("a2ui=%+v", profile.A2UI)
					}
					// A client must be told which catalog it needs to render.
					if !reflect.DeepEqual(profile.A2UI.CatalogIDs, a2ui.RegisteredCatalogIDs()) {
						t.Fatalf("catalogIds=%v", profile.A2UI.CatalogIDs)
					}
				} else if profile.A2UI != nil {
					t.Fatalf("a2ui must be omitted when disabled: %+v", profile.A2UI)
				}
			})
		}
	})

	t.Run("enableA2UI flips ETag and omits a2ui when false", func(t *testing.T) {
		off := aapPublishedAgentSummary()
		off.ContextPolicy = aapEnableA2UIPolicyJSON(false)
		on := aapPublishedAgentSummary()
		on.ContextPolicy = aapEnableA2UIPolicyJSON(true)
		first, firstETag, err := projectAAPAgentProfile(off, aapProfileDescriptors(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		second, secondETag, err := projectAAPAgentProfile(on, aapProfileDescriptors(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if first.Version == second.Version || firstETag == secondETag {
			t.Fatalf("enableA2UI must change version first=%q second=%q", first.Version, second.Version)
		}
		if first.A2UI != nil {
			t.Fatalf("disabled profile leaked a2ui: %+v", first.A2UI)
		}
		if second.A2UI == nil || !second.A2UI.Enabled {
			t.Fatalf("enabled profile missing a2ui: %+v", second.A2UI)
		}
		if !reflect.DeepEqual(second.SupportedContent[0].Parts, []string{"text", "a2ui"}) {
			t.Fatalf("parts=%v", second.SupportedContent[0].Parts)
		}
	})

	t.Run("a2ui seed metadata alone changes ETag", func(t *testing.T) {
		// Same supportedContent parts; only a2ui maxSurfaceBytes differs → version must change.
		base := aapAgentProfileVersionSeed{
			Schema: "aap.agent-profile.v1", AgentID: aapProfileAgentID,
			Name: "n", Description: "d", AgentLockVersion: 1,
			CurrentPromptRevisionID: aapProfilePromptID,
			SupportedContent: []aapSupportedContentDTO{
				{Type: "message", Parts: []string{"text", "a2ui"}},
			},
			A2UI: aapA2UIAdvertisement(),
		}
		alt := base
		altA2UI := *base.A2UI
		altA2UI.MaxSurfaceBytes = a2ui.MaxSurfaceBytes + 1
		alt.A2UI = &altA2UI
		hash := func(seed aapAgentProfileVersionSeed) string {
			raw, err := json.Marshal(seed)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			return hex.EncodeToString(sum[:])
		}
		if hash(base) == hash(alt) {
			t.Fatal("seed a2ui metadata change must alter hash")
		}
	})

	t.Run("HTTP profile advertises a2ui from ContextPolicy", func(t *testing.T) {
		authorizer := &aapProfileAuthorizer{}
		store := &aapProfileStore{value: aapPublishedAgentSummary()}
		store.value.ContextPolicy = aapEnableA2UIPolicyJSON(true)
		catalog := &aapProfileCatalog{values: aapProfileDescriptors()}
		routes, err := NewAAPAgentProfileRoutes(authorizer, store, catalog)
		if err != nil {
			t.Fatal(err)
		}
		router, err := NewRouter(Config{
			AgentAccessAuthenticator: aapProfileTokenAuthenticator{},
			AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/agent-access/v1/workspaces/" + aapProfileWorkspaceID +
			"/agents/" + aapProfileAgentID + "/profile"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, aapProfileRequest(path, "aap-profile-token"))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var profile aapAgentProfileDTO
		if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
			t.Fatal(err)
		}
		if profile.A2UI == nil || !profile.A2UI.Enabled ||
			!reflect.DeepEqual(profile.SupportedContent[0].Parts, []string{"text", "a2ui"}) {
			t.Fatalf("profile=%+v", profile)
		}
		assertAAPProfileAllowlist(t, response.Body.Bytes())
		// Ensure raw JSON includes a2ui and no accidental enabled:false when off path is separate.
		if !strings.Contains(response.Body.String(), `"a2ui"`) {
			t.Fatalf("body missing a2ui: %s", response.Body.String())
		}
	})
}

func aapEnableA2UIPolicyJSON(enabled bool) json.RawMessage {
	// Valid agent v2 policy with aap.enableA2UI.
	doc := map[string]any{
		"schemaVersion": sessioncontext.PolicySchemaV2,
		"mode":          sessioncontext.ModeTokenWindow,
		"aap": map[string]any{
			"enableA2UI": enabled,
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	// Round-trip through ParsePolicy so storage shape matches production.
	_, normalized, err := sessioncontext.ParsePolicy(raw)
	if err != nil {
		panic(err)
	}
	return normalized
}

type aapProfileAuthorizer struct {
	last  agentaccessauth.AAPAuthorizationRequest
	err   error
	calls int
}

func (authorizer *aapProfileAuthorizer) Authorize(
	_ context.Context,
	request agentaccessauth.AAPAuthorizationRequest,
) (agentaccessauth.AAPAuthorizationDecision, error) {
	authorizer.calls++
	authorizer.last = request
	if authorizer.err != nil {
		return agentaccessauth.AAPAuthorizationDecision{}, authorizer.err
	}
	return agentaccessauth.AAPAuthorizationDecision{}, nil
}

type aapProfileStore struct {
	value agent.Summary
	err   error
	calls int
}

func (store *aapProfileStore) GetSummary(
	_ context.Context,
	workspaceID, agentID string,
) (agent.Summary, error) {
	store.calls++
	if workspaceID != aapProfileWorkspaceID || agentID != aapProfileAgentID {
		return agent.Summary{}, agent.ErrNotFound
	}
	if store.err != nil {
		return agent.Summary{}, store.err
	}
	return store.value, nil
}

type aapProfileCatalog struct {
	values []capability.Descriptor
	err    error
}

func (catalog *aapProfileCatalog) ListForAgent(
	_ context.Context,
	workspaceID, agentID string,
) ([]capability.Descriptor, error) {
	if workspaceID != aapProfileWorkspaceID || agentID != aapProfileAgentID {
		return nil, capability.ErrNotFound
	}
	if catalog.err != nil {
		return nil, catalog.err
	}
	return append([]capability.Descriptor(nil), catalog.values...), nil
}

type aapProfileTokenAuthenticator struct{}

func (aapProfileTokenAuthenticator) VerifyAccessToken(
	_ context.Context,
	value string,
) (agentaccessauth.AAPAccessTokenPrincipal, error) {
	if value != "aap-profile-token" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, errors.New("invalid AAP profile token")
	}
	now := time.Now().UTC()
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        "b28f1f2e-7b5a-7c3d-8e9f-123456789010",
		ServicePrincipalID: "b28f1f2e-7b5a-7c3d-8e9f-123456789010",
		AuthorizedParty:    "awcl_aap_profile_client", WorkspaceID: aapProfileWorkspaceID,
		AgentID: aapProfileAgentID, Scopes: []string{"agent:read"}, SecurityVersion: 1,
		TokenID:  "b28f1f2e-7b5a-7c3d-8e9f-123456789011",
		IssuedAt: now.Add(-time.Minute), NotBefore: now.Add(-time.Minute),
		ExpiresAt: now.Add(10 * time.Minute),
	}, nil
}

func aapPublishedAgentSummary() agent.Summary {
	promptID := aapProfilePromptID
	return agent.Summary{Agent: agent.Agent{
		ID: aapProfileAgentID, WorkspaceID: aapProfileWorkspaceID,
		Name: "Published Support Agent", RoleDescription: "Helps with published support tasks.",
		CurrentPromptRevisionID: &promptID, ModelConfigID: "model-config-secret",
		Status: agent.StatusActive, CreatedBy: "private-creator", UpdatedBy: "private-updater",
		LockVersion: 7,
	}}
}

func aapProfileDescriptors() []capability.Descriptor {
	return []capability.Descriptor{
		{
			CapabilityID: aapProfileCapability1, ReleaseID: aapProfileRelease1, Kind: "TOOL",
			CallableName: "refund_internal_tool", CallableDescription: "private tool description",
			InputSchema:  json.RawMessage(`{"type":"object","secret":"system-prompt-secret"}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`), RiskLevel: "HIGH",
			SideEffectLevel: "WRITE", RequiresConfirmation: true,
			ConnectionID: "connection-internal-secret",
		},
		{
			CapabilityID: aapProfileCapability2, ReleaseID: aapProfileRelease2, Kind: "WORKFLOW",
			CallableName: "workflow_private_topology", CallableDescription: "private workflow description",
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			RiskLevel: "LOW", SideEffectLevel: "READ", RequiresConfirmation: false,
		},
	}
}

func aapProfileRequest(path, token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func assertAAPProfileAllowlist(t *testing.T, body []byte) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	required := []string{
		"object", "id", "name", "description", "version", "supportedContent",
		"capabilities", "interactionRequirements",
	}
	// Optional: a2ui is present only when enableA2UI is true (omit when disabled).
	allowedOptional := map[string]struct{}{"a2ui": {}}
	for _, field := range required {
		if _, ok := raw[field]; !ok {
			t.Fatalf("profile missing field %q", field)
		}
	}
	for field := range raw {
		found := false
		for _, req := range required {
			if field == req {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if _, ok := allowedOptional[field]; !ok {
			t.Fatalf("profile unexpected field %q fields=%v", field, raw)
		}
	}
}
