package einoruntime

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"testing"
)

// TestInterruptStateGobRoundTrip is the Appendix B CI check name.
// Verifies ToolConfirmInterruptState survives gob encode/decode (checkpoint path).
func TestInterruptStateGobRoundTrip(t *testing.T) {
	t.Parallel()

	original := &ToolConfirmInterruptState{
		SchemaVersion: ToolConfirmInterruptSchemaVersion,
		InvocationID:  "inv-1",
		ReleaseID:     "rel-1",
		CapabilityID:  "cap-1",
		StepID:        "step-1",
	}

	// Encode via interface so gob uses the registered type name
	// (schema.RegisterName[*ToolConfirmInterruptState] in init).
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(original); err != nil {
		t.Fatalf("gob encode concrete: %v", err)
	}

	var decoded ToolConfirmInterruptState
	dec := gob.NewDecoder(bytes.NewReader(buf.Bytes()))
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("gob decode concrete: %v", err)
	}
	assertToolConfirmStateEqual(t, &decoded, original)

	// Interface round-trip (how checkpoint state maps store `any` payloads).
	buf.Reset()
	enc = gob.NewEncoder(&buf)
	var asAny any = original
	if err := enc.Encode(&asAny); err != nil {
		t.Fatalf("gob encode any: %v", err)
	}
	var outAny any
	dec = gob.NewDecoder(bytes.NewReader(buf.Bytes()))
	if err := dec.Decode(&outAny); err != nil {
		t.Fatalf("gob decode any: %v", err)
	}
	st, ok := outAny.(*ToolConfirmInterruptState)
	if !ok {
		t.Fatalf("decoded any type = %T, want *ToolConfirmInterruptState", outAny)
	}
	assertToolConfirmStateEqual(t, st, original)
}

func TestToolConfirmInterruptRegisterNameStable(t *testing.T) {
	t.Parallel()
	// Checkpoint compatibility: never rename.
	if ToolConfirmInterruptRegisterName != "actweave_tool_confirm_v1" {
		t.Fatalf("register name = %q, want actweave_tool_confirm_v1", ToolConfirmInterruptRegisterName)
	}
	if ToolConfirmInterruptSchemaVersion == "" {
		t.Fatal("schema version must be non-empty")
	}
}

func TestToolConfirmInterruptStateFieldsAreIDsOnly(t *testing.T) {
	t.Parallel()
	// Guard against accidental secret-bearing fields.
	rt := reflect.TypeOf(ToolConfirmInterruptState{})
	allowed := map[string]struct{}{
		"SchemaVersion": {},
		"InvocationID":  {},
		"ReleaseID":     {},
		"CapabilityID":  {},
		"StepID":        {},
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if _, ok := allowed[name]; !ok {
			t.Fatalf("unexpected field %q on ToolConfirmInterruptState (IDs only; no secrets)", name)
		}
		if rt.Field(i).Type.Kind() != reflect.String {
			t.Fatalf("field %q must be string (IDs only)", name)
		}
	}
}

func assertToolConfirmStateEqual(t *testing.T, got, want *ToolConfirmInterruptState) {
	t.Helper()
	if got.SchemaVersion != want.SchemaVersion ||
		got.InvocationID != want.InvocationID ||
		got.ReleaseID != want.ReleaseID ||
		got.CapabilityID != want.CapabilityID ||
		got.StepID != want.StepID {
		t.Fatalf("state mismatch:\n got %+v\nwant %+v", got, want)
	}
}
