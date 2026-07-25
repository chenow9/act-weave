package openapiimport

import (
	"encoding/json"
	"testing"
)

func TestEvaluateIntegrity_Complete(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	endpoints := make([]Endpoint, 8)
	for i := range endpoints {
		endpoints[i] = Endpoint{
			ID: "e" + string(rune('1'+i)), Method: "GET", Path: "/p" + string(rune('0'+i)),
			InputSchema: schema, Ready: true,
		}
		// Fix IDs to valid-looking strings
		endpoints[i].ID = "00000000-0000-4000-8000-00000000000" + string(rune('1'+i))
	}
	// simpler unique ids
	for i := range endpoints {
		endpoints[i].ID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeee0" + string(rune('0'+i))
		endpoints[i].Path = "/items/" + string(rune('a'+i))
	}
	imp := Import{TotalEndpoints: 8, ReadyEndpoints: 8}
	report := EvaluateIntegrity(imp, endpoints)
	if report.Status != IntegrityComplete {
		t.Fatalf("status=%s issues=%+v", report.Status, report.Issues)
	}
	if report.ActualTotalEndpoints != 8 || report.ActualReadyEndpoints != 8 {
		t.Fatalf("totals=%d ready=%d", report.ActualTotalEndpoints, report.ActualReadyEndpoints)
	}
}

func TestEvaluateIntegrity_EmptyListVsSummary(t *testing.T) {
	t.Parallel()
	imp := Import{TotalEndpoints: 8, ReadyEndpoints: 8}
	report := EvaluateIntegrity(imp, nil)
	if report.Status != IntegrityIncomplete {
		t.Fatalf("status=%s", report.Status)
	}
	if report.ActualTotalEndpoints != 0 {
		t.Fatalf("actual=%d", report.ActualTotalEndpoints)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == "ENDPOINT_COUNT_MISMATCH" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues=%+v", report.Issues)
	}
}

func TestEvaluateIntegrity_LegalEmptyObjectSchema(t *testing.T) {
	t.Parallel()
	ep := Endpoint{
		ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeee01", Method: "POST", Path: "/ping",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), Ready: true,
	}
	report := EvaluateIntegrity(Import{TotalEndpoints: 1, ReadyEndpoints: 1}, []Endpoint{ep})
	if report.Status != IntegrityComplete {
		t.Fatalf("empty object schema must be complete, got %s issues=%+v", report.Status, report.Issues)
	}
}

func TestEvaluateIntegrity_InvalidSchema(t *testing.T) {
	t.Parallel()
	ep := Endpoint{
		ID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeee01", Method: "GET", Path: "/x",
		InputSchema: json.RawMessage(`"not-an-object"`), Ready: true,
	}
	report := EvaluateIntegrity(Import{TotalEndpoints: 1, ReadyEndpoints: 1}, []Endpoint{ep})
	if report.Status != IntegrityIncomplete {
		t.Fatal("expected incomplete")
	}
}

func TestIsValidJSONSchemaObject(t *testing.T) {
	t.Parallel()
	if !isValidJSONSchemaObject(json.RawMessage(`{"type":"object","properties":{}}`)) {
		t.Fatal("legal empty object")
	}
	if isValidJSONSchemaObject(nil) || isValidJSONSchemaObject(json.RawMessage(`null`)) {
		t.Fatal("null/missing invalid")
	}
	if isValidJSONSchemaObject(json.RawMessage(`[]`)) {
		t.Fatal("array invalid")
	}
	if isValidJSONSchemaObject(json.RawMessage(`{"type":"string"}`)) {
		t.Fatal("string type invalid for tool input")
	}
}

func TestAssertImportComplete(t *testing.T) {
	t.Parallel()
	if err := AssertImportComplete(Import{TotalEndpoints: 1, ReadyEndpoints: 0}, nil); err == nil {
		t.Fatal("want ErrImportIncomplete")
	}
}
