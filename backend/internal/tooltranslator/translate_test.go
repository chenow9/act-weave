package tooltranslator_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/tooltranslator"
)

func TestToToolInfoValidObjectSchema(t *testing.T) {
	t.Parallel()

	cap := tooltranslator.NewCapability(
		"get_order",
		"Fetch an order by id",
		json.RawMessage(`{
			"type":"object",
			"properties":{"id":{"type":"string","description":"order id"}},
			"required":["id"]
		}`),
	)

	tool, err := tooltranslator.ToToolInfo(cap)
	if err != nil {
		t.Fatalf("ToToolInfo: %v", err)
	}
	if tool.Name != "get_order" {
		t.Fatalf("Name=%q", tool.Name)
	}
	if tool.Desc != "Fetch an order by id" {
		t.Fatalf("Desc=%q", tool.Desc)
	}
	if tool.ParamsOneOf == nil {
		t.Fatal("expected ParamsOneOf")
	}
	if tool.Extra != nil {
		t.Fatalf("Extra must be nil, got %#v", tool.Extra)
	}

	js, err := tool.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema: %v", err)
	}
	if js == nil || js.Type != "object" {
		t.Fatalf("schema type=%v", js)
	}
	if js.Properties == nil {
		t.Fatal("expected properties")
	}
	prop, ok := js.Properties.Get("id")
	if !ok || prop == nil || prop.Type != "string" {
		t.Fatalf("id property missing/wrong: ok=%v prop=%v", ok, prop)
	}
	if len(js.Required) != 1 || js.Required[0] != "id" {
		t.Fatalf("required=%v", js.Required)
	}
}

func TestToToolInfoEmptyAndNilSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		schema json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage(``)},
		{"null", json.RawMessage(`null`)},
		{"whitespace", json.RawMessage(`   `)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool, err := tooltranslator.ToToolInfo(tooltranslator.NewCapability("noop", "does nothing", tc.schema))
			if err != nil {
				t.Fatalf("ToToolInfo: %v", err)
			}
			if tool.ParamsOneOf == nil {
				t.Fatal("expected default empty-object ParamsOneOf")
			}
			js, err := tool.ParamsOneOf.ToJSONSchema()
			if err != nil {
				t.Fatalf("ToJSONSchema: %v", err)
			}
			if js == nil || js.Type != "object" {
				t.Fatalf("expected type=object, got %#v", js)
			}
		})
	}
}

func TestToToolInfoEmptyObjectAndBooleanSchema(t *testing.T) {
	t.Parallel()
	// WORKFLOW releases may store input_schema as {}; JSON Schema libs may
	// emit boolean true — both must become a concrete OpenAI object schema.
	for _, schema := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`true`),
		json.RawMessage(`false`),
	} {
		tool, err := tooltranslator.ToToolInfo(tooltranslator.NewCapability("aftersales_r3_9399", "workflow", schema))
		if err != nil {
			t.Fatalf("schema %s: %v", schema, err)
		}
		js, err := tool.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatalf("ToJSONSchema: %v", err)
		}
		if js == nil || js.Type != "object" {
			t.Fatalf("schema %s: expected type=object, got %#v", schema, js)
		}
		raw, err := json.Marshal(js)
		if err != nil {
			t.Fatal(err)
		}
		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Fatalf("schema %s must marshal to object map: %v raw=%s", schema, err, raw)
		}
	}
}

func TestToToolInfoInvalidSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		schema json.RawMessage
	}{
		{"truncated", json.RawMessage(`{not-json`)},
		// bare string / array roots are coerced to empty object (LLM-safe), not hard errors
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tooltranslator.ToToolInfo(tooltranslator.NewCapability("bad", "x", tc.schema))
			if err == nil {
				t.Fatal("expected error for invalid schema")
			}
			if !strings.Contains(err.Error(), "input schema") {
				t.Fatalf("error should mention input schema: %v", err)
			}
		})
	}
}

func TestToToolInfoRequiresName(t *testing.T) {
	t.Parallel()

	_, err := tooltranslator.ToToolInfo(tooltranslator.Capability{
		CallableDescription: "no name",
		InputSchema:         json.RawMessage(`{"type":"object"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "callable name") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestBuildModelToolsPreservesOrder(t *testing.T) {
	t.Parallel()

	caps := []tooltranslator.Capability{
		tooltranslator.NewCapability("alpha", "A", json.RawMessage(`{"type":"object"}`)),
		tooltranslator.NewCapability("beta", "B", json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`)),
		tooltranslator.NewCapability("gamma", "C", nil),
	}
	tools, err := tooltranslator.BuildModelTools(caps)
	if err != nil {
		t.Fatalf("BuildModelTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("len=%d", len(tools))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, name := range want {
		if tools[i] == nil || tools[i].Name != name {
			t.Fatalf("tools[%d]=%v want name %q", i, tools[i], name)
		}
	}
}

func TestBuildModelToolsEmpty(t *testing.T) {
	t.Parallel()

	tools, err := tooltranslator.BuildModelTools(nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if tools != nil {
		t.Fatalf("expected nil, got %#v", tools)
	}
	tools, err = tooltranslator.BuildModelTools([]tooltranslator.Capability{})
	if err != nil || tools != nil {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
}

func TestExtractCapabilityDropsSensitiveFields(t *testing.T) {
	t.Parallel()

	// Broader fake expanded capability object (as if from a release+binding join).
	expanded := map[string]any{
		"callableName":        "charge_card",
		"callableDescription": "Charge a payment method",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"amount": map[string]any{"type": "number"},
			},
		},
		// Sensitive platform fields that must never enter ToolInfo.
		"connectionId":       "conn_live_abc",
		"credentialSecretId": "sec_xyz",
		"secretId":           "sec_xyz",
		"secret":             "sk_live_super_secret",
		"bearerToken":        "eyJhbGciOiJIUzI1NiJ9.fake",
		"accessToken":        "tok_abc",
		"apiKey":             "ak_secret",
		"authorization":      "Bearer tok",
		"egress":             []string{"https://payments.internal"},
		"egressHosts":        []string{"payments.internal"},
		"allowedHosts":       []string{"payments.internal"},
		"provider":           "stripe",
		"providerConfig":     map[string]any{"baseUrl": "https://api.stripe.com"},
		"rawCredentials":     map[string]any{"password": "hunter2"},
		// Non-sensitive but non-allowlisted metadata must also be dropped.
		"capabilityId":    "cap_1",
		"releaseId":       "rel_1",
		"riskLevel":       "HIGH",
		"sideEffectLevel": "WRITE",
	}

	cap, err := tooltranslator.ExtractCapability(expanded)
	if err != nil {
		t.Fatalf("ExtractCapability: %v", err)
	}
	if cap.CallableName != "charge_card" {
		t.Fatalf("name=%q", cap.CallableName)
	}
	if cap.CallableDescription != "Charge a payment method" {
		t.Fatalf("desc=%q", cap.CallableDescription)
	}
	if len(cap.InputSchema) == 0 {
		t.Fatal("expected input schema")
	}

	// Capability itself must not retain sensitive material as residual JSON.
	capJSON, err := json.Marshal(cap)
	if err != nil {
		t.Fatalf("marshal cap: %v", err)
	}
	assertNoSensitive(t, string(capJSON))

	tool, err := tooltranslator.ToToolInfo(cap)
	if err != nil {
		t.Fatalf("ToToolInfo: %v", err)
	}
	assertToolInfoSanitized(t, tool)
}

func TestBuildModelToolsFromSourcesDropsSensitive(t *testing.T) {
	t.Parallel()

	type expandedSnapshot struct {
		CallableName        string          `json:"callableName"`
		CallableDescription string          `json:"callableDescription"`
		InputSchema         json.RawMessage `json:"inputSchema"`
		ConnectionID        string          `json:"connectionId"`
		CredentialSecretID  string          `json:"credentialSecretId"`
		EgressHosts         []string        `json:"egressHosts"`
		BearerToken         string          `json:"bearerToken"`
		Provider            string          `json:"provider"`
	}

	sources := []any{
		expandedSnapshot{
			CallableName:        "get_order",
			CallableDescription: "Get order",
			InputSchema:         json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
			ConnectionID:        "conn-1",
			CredentialSecretID:  "sec-1",
			EgressHosts:         []string{"orders.internal"},
			BearerToken:         "tok-secret",
			Provider:            "http",
		},
		map[string]any{
			"name":        "cancel_flow",
			"description": "Cancel flow",
			"inputSchema": map[string]any{"type": "object"},
			"secret":      "must-not-leak",
			"egress":      "https://evil.example",
		},
	}

	tools, err := tooltranslator.BuildModelToolsFromSources(sources)
	if err != nil {
		t.Fatalf("BuildModelToolsFromSources: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("len=%d", len(tools))
	}
	if tools[0].Name != "get_order" || tools[1].Name != "cancel_flow" {
		t.Fatalf("names=%q,%q", tools[0].Name, tools[1].Name)
	}
	for _, tool := range tools {
		assertToolInfoSanitized(t, tool)
	}
}

func TestNewCapabilityTrimsAndClonesSchema(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"object"}`)
	cap := tooltranslator.NewCapability("  tool  ", "  desc  ", raw)
	if cap.CallableName != "tool" || cap.CallableDescription != "desc" {
		t.Fatalf("cap=%+v", cap)
	}
	raw[2] = 'X' // mutate original
	if string(cap.InputSchema) == string(raw) {
		t.Fatal("InputSchema must be cloned")
	}
	if string(cap.InputSchema) != `{"type":"object"}` {
		t.Fatalf("schema=%s", cap.InputSchema)
	}
}

func assertToolInfoSanitized(t *testing.T, tool *schema.ToolInfo) {
	t.Helper()
	if tool == nil {
		t.Fatal("nil tool")
	}
	if tool.Extra != nil {
		t.Fatalf("Extra must be nil, got %#v", tool.Extra)
	}

	// Round-trip ToolInfo JSON (includes params schema when present).
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}
	blob := string(encoded)
	assertNoSensitive(t, blob)

	// Also inspect ParamsOneOf → JSON Schema surface.
	if tool.ParamsOneOf != nil {
		js, err := tool.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatalf("ToJSONSchema: %v", err)
		}
		paramsJSON, err := json.Marshal(js)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		assertNoSensitive(t, string(paramsJSON))
	}

	// Name/Desc themselves must not be platform secret values we planted.
	for _, s := range []string{tool.Name, tool.Desc} {
		assertNoSensitive(t, s)
	}
}

func assertNoSensitive(t *testing.T, blob string) {
	t.Helper()
	lower := strings.ToLower(blob)
	forbidden := []string{
		"connectionid",
		"connection_id",
		"conn_live_abc",
		"conn-1",
		"credentialsecretid",
		"credential_secret_id",
		"sec_xyz",
		"sec-1",
		"sk_live_super_secret",
		"bearertoken",
		"bearer_token",
		"eyjhbGcioijiuzi1nij9.fake",
		"tok_abc",
		"tok-secret",
		"ak_secret",
		"must-not-leak",
		"egresshosts",
		"egress_hosts",
		"payments.internal",
		"orders.internal",
		"evil.example",
		"rawcredentials",
		"hunter2",
		"providerconfig",
		`"provider"`,
		"stripe",
	}
	for _, token := range forbidden {
		if strings.Contains(lower, strings.ToLower(token)) {
			t.Fatalf("sensitive material %q leaked into: %s", token, blob)
		}
	}
}
