package modelconfig_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/modelconfig"
)

func TestParseToolDisclosurePolicyEmptyIsUnset(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`  {}  `)} {
		doc, normalized, err := modelconfig.ParseToolDisclosurePolicy(raw)
		if err != nil {
			t.Fatalf("empty parse: %v", err)
		}
		if doc.Mode != "" || string(normalized) != "{}" {
			t.Fatalf("expected unset, got doc=%+v raw=%s", doc, normalized)
		}
		if !modelconfig.IsUnsetToolDisclosurePolicy(raw) {
			t.Fatal("empty must be unset")
		}
	}
}

func TestParseToolDisclosurePolicyValidAndInvalid(t *testing.T) {
	for _, mode := range []string{
		modelconfig.DisclosureModePlatformOnDemand,
		modelconfig.DisclosureModeCarryAll,
	} {
		raw := `{"schemaVersion":"tool-disclosure.v1","mode":"` + mode + `"}`
		doc, normalized, err := modelconfig.ParseToolDisclosurePolicy(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("valid %s: %v", mode, err)
		}
		if doc.SchemaVersion != modelconfig.ToolDisclosureSchemaV1 || doc.Mode != mode {
			t.Fatalf("unexpected doc: %+v", doc)
		}
		if !strings.Contains(string(normalized), mode) {
			t.Fatalf("normalized missing mode: %s", normalized)
		}
		if modelconfig.IsUnsetToolDisclosurePolicy(json.RawMessage(raw)) {
			t.Fatal("valid policy must not be unset")
		}
	}

	rejects := []string{
		`null`,
		`[]`,
		`{"schemaVersion":"tool-disclosure.v1"}`,
		`{"mode":"carry_all"}`,
		`{"schemaVersion":"tool-disclosure.v2","mode":"carry_all"}`,
		`{"schemaVersion":"tool-disclosure.v1","mode":"client_bounded"}`,
		`{"schemaVersion":"tool-disclosure.v1","mode":"Carry_All"}`,
		`{"schemaVersion":"tool-disclosure.v1","mode":"carry_all","extra":1}`,
		`{"schemaVersion":null,"mode":"carry_all"}`,
		`{"schemaVersion":"tool-disclosure.v1","schemaVersion":"tool-disclosure.v1","mode":"carry_all"}`,
	}
	for _, raw := range rejects {
		_, _, err := modelconfig.ParseToolDisclosurePolicy(json.RawMessage(raw))
		if err == nil || !errors.Is(err, modelconfig.ErrToolDisclosureInvalid) {
			t.Fatalf("expected ErrToolDisclosureInvalid for %s, got %v", raw, err)
		}
	}
}

func TestDeriveToolDisclosureUI(t *testing.T) {
	if got := modelconfig.DeriveToolDisclosureUI(modelconfig.StatusUnverified, json.RawMessage(`{}`)); got != modelconfig.ToolDisclosureUIUnverified {
		t.Fatalf("unverified: %s", got)
	}
	if got := modelconfig.DeriveToolDisclosureUI(modelconfig.StatusError, json.RawMessage(`{}`)); got != modelconfig.ToolDisclosureUIUnverified {
		t.Fatalf("error: %s", got)
	}
}
