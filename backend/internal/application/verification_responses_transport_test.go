package application

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"actweave/backend/internal/modelconfig"
)

func TestValidateVerificationSSE_MissingUsage(t *testing.T) {
	resp := map[string]any{
		"id": "resp_c1", "object": "response", "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": "ack", "annotations": []any{}}},
		}},
	}
	// no usage
	completed := map[string]any{
		"type": "response.completed", "response": resp, "sequence_number": 1,
	}
	cb, _ := json.Marshal(completed)
	sse := "event: response.completed\ndata: " + string(cb) + "\n\ndata: [DONE]\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateVerificationJSON_MissingUsage(t *testing.T) {
	raw := []byte(`{"id":"r","object":"response","status":"completed","output":[]}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateVerificationJSON_InProgressWithUsageRejected(t *testing.T) {
	// Non-stream must be an actual completed response — not in_progress with valid usage.
	raw := []byte(`{"id":"r","object":"response","status":"in_progress","output":[],` +
		`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for in_progress non-stream, got %v", err)
	}
}

func TestValidateVerificationJSON_CompletedWithUsageOK(t *testing.T) {
	raw := []byte(`{"id":"r","object":"response","status":"completed","output":[],` +
		`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	if err := validateVerificationJSONObject(raw); err != nil {
		t.Fatalf("completed+usage must accept: %v", err)
	}
}

func TestValidateVerificationJSON_ForgedStatusUsageMapRejected(t *testing.T) {
	// Mere status+usage without authentic Responses object fields.
	raw := []byte(`{"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("forged status+usage map must reject, got %v", err)
	}
}

func TestValidateVerificationJSON_MissingObjectRejected(t *testing.T) {
	raw := []byte(`{"id":"r","status":"completed","output":[],` +
		`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("missing object:response must reject, got %v", err)
	}
}

func TestValidateVerificationJSON_MissingIDRejected(t *testing.T) {
	raw := []byte(`{"object":"response","status":"completed","output":[],` +
		`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("missing id must reject, got %v", err)
	}
}

func TestValidateVerificationJSON_MissingOutputRejected(t *testing.T) {
	raw := []byte(`{"id":"r","object":"response","status":"completed",` +
		`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	err := validateVerificationJSONObject(raw)
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("missing output must reject, got %v", err)
	}
}

func TestVerificationUsageTransport_RoundTripMissingUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","output":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := wrapClientWithVerificationUsageValidator(srv.Client())
	resp, err := c.Get(srv.URL + "/v1/responses")
	if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("err=%v resp=%v", err, resp)
	}
	if resp != nil {
		t.Fatalf("expected nil resp, got %v", resp.StatusCode)
	}
}

// Realistic multi-event SSE: created(usage null) → in_progress → output item/content
// deltas → completed(valid usage). Must accept; intermediate null/absent is not failure.
func TestValidateVerificationSSE_RealisticLifecycle(t *testing.T) {
	createdResp := map[string]any{
		"id": "resp_1", "object": "response", "status": "in_progress", "model": "gpt-test",
		"output": []any{}, "usage": nil,
	}
	created := map[string]any{"type": "response.created", "response": createdResp, "sequence_number": 0}
	inProg := map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": "resp_1", "object": "response", "status": "in_progress", "output": []any{},
			// usage absent
		},
		"sequence_number": 1,
	}
	itemAdded := map[string]any{
		"type": "response.output_item.added", "output_index": 0, "sequence_number": 2,
		"item": map[string]any{
			"type": "message", "id": "msg_1", "status": "in_progress", "role": "assistant", "content": []any{},
		},
	}
	delta := map[string]any{
		"type": "response.output_text.delta", "content_index": 0, "delta": "ack",
		"item_id": "msg_1", "output_index": 0, "sequence_number": 3, "logprobs": []any{},
	}
	completedResp := map[string]any{
		"id": "resp_1", "object": "response", "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": "ack", "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
	}
	completed := map[string]any{"type": "response.completed", "response": completedResp, "sequence_number": 4}

	var b strings.Builder
	for _, ev := range []struct {
		name string
		obj  map[string]any
	}{
		{"response.created", created},
		{"response.in_progress", inProg},
		{"response.output_item.added", itemAdded},
		{"response.output_text.delta", delta},
		{"response.completed", completed},
	} {
		raw, _ := json.Marshal(ev.obj)
		b.WriteString("event: " + ev.name + "\ndata: " + string(raw) + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")

	if err := validateVerificationResponsesPayload([]byte(b.String()), "text/event-stream"); err != nil {
		t.Fatalf("realistic lifecycle must accept: %v", err)
	}
}

func TestValidateVerificationSSE_CreatedNullUsageOK_CompletedMissingFails(t *testing.T) {
	// created with usage:null alone is not enough; without completed → stream invalid.
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r", "object": "response", "status": "in_progress", "output": []any{}, "usage": nil,
		},
	}
	cb, _ := json.Marshal(created)
	sse := "event: response.created\ndata: " + string(cb) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid (no completed), got %v", err)
	}
}

func TestValidateVerificationSSE_MalformedTerminal(t *testing.T) {
	// response.failed is a terminal non-success shape.
	failed := map[string]any{"type": "response.failed", "sequence_number": 1}
	fb, _ := json.Marshal(failed)
	sse := "event: response.failed\ndata: " + string(fb) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid, got %v", err)
	}
}

func TestValidateVerificationSSE_UnknownEventRejected(t *testing.T) {
	unknown := map[string]any{"type": "response.totally_unknown", "sequence_number": 1}
	ub, _ := json.Marshal(unknown)
	// Need a completed with valid usage so failure is specifically the unknown event.
	cb, _ := json.Marshal(validCompletedEnvelope("r"))
	sse := "event: response.totally_unknown\ndata: " + string(ub) + "\n\n" +
		"event: response.completed\ndata: " + string(cb) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for unknown event, got %v", err)
	}
}

func TestValidateVerificationSSE_IntermediateUsageObjectValidated(t *testing.T) {
	// If intermediate carries a non-null usage object with wrong totals, fail usage.
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r", "object": "response", "status": "in_progress", "output": []any{},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 14},
		},
	}
	cb, _ := json.Marshal(created)
	compB, _ := json.Marshal(validCompletedEnvelope("r"))
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(compB) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("want usage invalid on intermediate bad usage, got %v", err)
	}
}

func TestVerificationTransport_Non2xxSanitizesBodyNoSecretLeak(t *testing.T) {
	const secret = "sk-live-super-secret-LEAK-MARKER-xyz"
	for _, status := range []int{400, 401, 403, 429, 500, 502} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"` + secret + `"}}`))
			}))
			t.Cleanup(srv.Close)
			c := wrapClientWithVerificationUsageValidator(srv.Client())
			resp, err := c.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{}`))
			if resp != nil {
				// Transport must return nil response with typed error for non-2xx Responses.
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected nil resp, got status=%d body=%q err=%v", resp.StatusCode, body, err)
			}
			if err == nil {
				t.Fatal("expected error")
			}
			// Exact classification.
			switch status {
			case 401, 403:
				if !errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
					t.Fatalf("want auth, got %v", err)
				}
			case 429, 500, 502, 400:
				if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
					t.Fatalf("want upstream, got %v", err)
				}
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("secret leaked into error: %v", err)
			}
		})
	}
}

func TestVerificationTransport_404ResponsesUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such route sk-should-not-leak"}`))
	}))
	t.Cleanup(srv.Close)
	c := wrapClientWithVerificationUsageValidator(srv.Client())
	_, err := c.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{}`))
	if !errors.Is(err, modelconfig.ErrResponsesUnsupported) {
		t.Fatalf("want responses unsupported, got %v", err)
	}
	if strings.Contains(err.Error(), "sk-should-not-leak") {
		t.Fatalf("secret leaked: %v", err)
	}
}

func validCompletedEnvelope(id string) map[string]any {
	return map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": id, "object": "response", "status": "completed", "model": "gpt-test",
			"output": []any{},
			"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		},
		"sequence_number": 99,
	}
}

// validLifecycleResponse builds a minimal official lifecycle response shape.
func validLifecycleResponse(id, status string) map[string]any {
	return map[string]any{
		"id": id, "object": "response", "status": status,
		"output": []any{},
	}
}

// validPinnedMessageItem is a complete ResponseOutputMessage shape (pinned required fields).
func validPinnedMessageItem(id, status string) map[string]any {
	return map[string]any{
		"type": "message", "id": id, "status": status, "role": "assistant",
		"content": []any{},
	}
}

// validPinnedFunctionCallItem is a complete ResponseFunctionToolCall shape.
func validPinnedFunctionCallItem(callID, name string) map[string]any {
	return map[string]any{
		"type": "function_call", "call_id": callID, "name": name, "arguments": `{}`,
	}
}

// validPinnedMCPCallItem is a complete ResponseOutputItemMcpCall shape.
func validPinnedMCPCallItem(id string) map[string]any {
	return map[string]any{
		"type": "mcp_call", "id": id, "name": "tool", "server_label": "srv", "arguments": `{}`,
	}
}

// validPinnedOutputTextPart is a complete ResponseOutputText content part.
func validPinnedOutputTextPart(text string) map[string]any {
	return map[string]any{
		"type": "output_text", "text": text, "annotations": []any{},
	}
}

// validPinnedURLCitationAnnotation is a complete url_citation annotation.
func validPinnedURLCitationAnnotation() map[string]any {
	return map[string]any{
		"type": "url_citation", "url": "https://example.com", "title": "ex",
		"start_index": 0, "end_index": 1,
	}
}

// validPinnedFileCitationAnnotation is a complete file_citation annotation.
func validPinnedFileCitationAnnotation() map[string]any {
	return map[string]any{
		"type": "file_citation", "file_id": "f1", "filename": "a.txt", "index": 0,
	}
}

// withSeq sets sequence_number on an event map (mutates and returns it).
func withSeq(ev map[string]any, n int) map[string]any {
	ev["sequence_number"] = n
	return ev
}

func TestValidateVerificationSSE_EventTypeMismatch(t *testing.T) {
	// event: response.completed but data.type is response.in_progress — cross-spoof.
	data := map[string]any{
		"type":            "response.in_progress",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r1", "object": "response", "status": "in_progress", "output": []any{}, "usage": nil,
		},
	}
	db, _ := json.Marshal(data)
	sse := "event: response.completed\ndata: " + string(db) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for event/data.type mismatch, got %v", err)
	}
}

func TestValidateVerificationSSE_CreatedClaimsCompletedStatus(t *testing.T) {
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r1", "object": "response", "status": "completed", "output": []any{},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		},
	}
	cb, _ := json.Marshal(created)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid when created claims completed, got %v", err)
	}
}

func TestValidateVerificationSSE_CompletedMissingStatus(t *testing.T) {
	completed := map[string]any{
		"type":            "response.completed",
		"sequence_number": 1,
		"response": map[string]any{
			"id": "r1", "object": "response", "output": []any{},
			// status omitted
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		},
	}
	cb, _ := json.Marshal(completed)
	sse := "event: response.completed\ndata: " + string(cb) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for completed without status, got %v", err)
	}
}

func TestValidateVerificationSSE_DuplicateTerminal(t *testing.T) {
	c1, _ := json.Marshal(validCompletedEnvelope("r1"))
	c2, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.completed\ndata: " + string(c1) + "\n\n" +
		"event: response.completed\ndata: " + string(c2) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for duplicate terminal, got %v", err)
	}
}

func TestValidateVerificationSSE_ResponseIDConflict(t *testing.T) {
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r1", "object": "response", "status": "in_progress", "output": []any{}, "usage": nil,
		},
	}
	completed := validCompletedEnvelope("r2") // different id
	cb, _ := json.Marshal(created)
	comp, _ := json.Marshal(completed)
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid for response id conflict, got %v", err)
	}
}

func TestValidateVerificationSSE_MissingTerminalNoStatusFallback(t *testing.T) {
	// data-only line with status completed but no event:/type — must not accept via fallback.
	raw := []byte("data: {\"id\":\"r\",\"object\":\"response\",\"status\":\"completed\"," +
		"\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}\n\n")
	err := validateVerificationResponsesPayload(raw, "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("want stream invalid without authentic completed event, got %v", err)
	}
}

func TestValidateVerificationSSE_MissingEventHeaderRejected(t *testing.T) {
	// data.type is response.completed but event: header absent — reject.
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "data: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("missing event: header must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_NullResponseRejected(t *testing.T) {
	created := map[string]any{
		"type": "response.created", "response": nil, "sequence_number": 0,
	}
	cb, _ := json.Marshal(created)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("null response must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_CreatedMissingStatusRejected(t *testing.T) {
	// created requires status in_progress or queued (absent fails).
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r1", "object": "response", "output": []any{}, "usage": nil,
		},
	}
	cb, _ := json.Marshal(created)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("created without status in_progress/queued must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_CreatedQueuedAccepted(t *testing.T) {
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response":        validLifecycleResponse("r1", "queued"),
	}
	cb, _ := json.Marshal(created)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.created\ndata: " + string(cb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	if err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream"); err != nil {
		t.Fatalf("created+queued must accept (DashScope compatible-mode): %v", err)
	}
}

func TestValidateVerificationSSE_VendorEventTypeRejected(t *testing.T) {
	// Non-response vendor event types are not ignored — fail closed.
	vendor := map[string]any{"type": "vendor.custom_metric", "value": 1}
	vb, _ := json.Marshal(vendor)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: vendor.custom_metric\ndata: " + string(vb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("vendor event must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_CancelledNeverSuccessfulTerminal(t *testing.T) {
	// response.cancelled is fabricated — absent from openai-go/v3@v3.35.0
	// ResponseStreamEventUnion. Reject as unknown event type (fail closed).
	cancelled := map[string]any{"type": "response.cancelled", "sequence_number": 1}
	fb, _ := json.Marshal(cancelled)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.cancelled\ndata: " + string(fb) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("fabricated response.cancelled must fail stream, got %v", err)
	}
	if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "response.cancelled") {
		t.Fatalf("error should identify unknown/fabricated type, got %v", err)
	}
}

// TestValidateVerificationSSE_AdapterHostedToolFamilies covers MCP, web_search,
// file_search, code_interpreter, and image_generation events from the pinned
// agenticopenai adapter + openai-go/v3 ResponseStreamEventUnion.
func TestValidateVerificationSSE_AdapterHostedToolFamilies(t *testing.T) {
	phaseOK := func(typ string) map[string]any {
		return map[string]any{
			"type": typ, "item_id": "item_1", "output_index": 0,
		}
	}
	cases := []struct {
		name string
		ev   map[string]any
		ok   bool
	}{
		// Positive: adapter-handled phase families with required fields.
		{name: "mcp_list_tools.in_progress ok", ev: phaseOK("response.mcp_list_tools.in_progress"), ok: true},
		{name: "mcp_list_tools.completed ok", ev: phaseOK("response.mcp_list_tools.completed"), ok: true},
		{name: "mcp_list_tools.failed ok", ev: phaseOK("response.mcp_list_tools.failed"), ok: true},
		{name: "mcp_call.in_progress ok", ev: phaseOK("response.mcp_call.in_progress"), ok: true},
		{name: "mcp_call.completed ok", ev: phaseOK("response.mcp_call.completed"), ok: true},
		{name: "mcp_call.failed ok", ev: phaseOK("response.mcp_call.failed"), ok: true},
		{
			name: "mcp_call_arguments.delta ok",
			ev: map[string]any{
				"type": "response.mcp_call_arguments.delta", "item_id": "item_1",
				"output_index": 0, "delta": `{"a":1}`,
			},
			ok: true,
		},
		{
			name: "mcp_call_arguments.done ok",
			ev: map[string]any{
				"type": "response.mcp_call_arguments.done", "item_id": "item_1",
				"output_index": 0, "arguments": `{"a":1}`,
			},
			ok: true,
		},
		{name: "web_search.in_progress ok", ev: phaseOK("response.web_search_call.in_progress"), ok: true},
		{name: "web_search.searching ok", ev: phaseOK("response.web_search_call.searching"), ok: true},
		{name: "web_search.completed ok", ev: phaseOK("response.web_search_call.completed"), ok: true},
		{name: "file_search.in_progress ok", ev: phaseOK("response.file_search_call.in_progress"), ok: true},
		{name: "file_search.searching ok", ev: phaseOK("response.file_search_call.searching"), ok: true},
		{name: "file_search.completed ok", ev: phaseOK("response.file_search_call.completed"), ok: true},
		{name: "code_interpreter.in_progress ok", ev: phaseOK("response.code_interpreter_call.in_progress"), ok: true},
		{name: "code_interpreter.interpreting ok", ev: phaseOK("response.code_interpreter_call.interpreting"), ok: true},
		{name: "code_interpreter.completed ok", ev: phaseOK("response.code_interpreter_call.completed"), ok: true},
		{
			name: "code_interpreter_code.delta ok",
			ev: map[string]any{
				"type": "response.code_interpreter_call_code.delta", "item_id": "item_1",
				"output_index": 0, "delta": "print(1)",
			},
			ok: true,
		},
		{
			name: "code_interpreter_code.done ok",
			ev: map[string]any{
				"type": "response.code_interpreter_call_code.done", "item_id": "item_1",
				"output_index": 0, "code": "print(1)",
			},
			ok: true,
		},
		{name: "image_gen.in_progress ok", ev: phaseOK("response.image_generation_call.in_progress"), ok: true},
		{name: "image_gen.generating ok", ev: phaseOK("response.image_generation_call.generating"), ok: true},
		{name: "image_gen.completed ok", ev: phaseOK("response.image_generation_call.completed"), ok: true},
		{
			name: "image_gen.partial_image ok",
			ev: map[string]any{
				"type": "response.image_generation_call.partial_image", "item_id": "item_1",
				"output_index": 0, "partial_image_b64": "abc", "partial_image_index": 0,
			},
			ok: true,
		},
		// Adversarial: missing/null/wrong-type required fields.
		{
			name: "mcp_call missing item_id",
			ev:   map[string]any{"type": "response.mcp_call.in_progress", "output_index": 0},
		},
		{
			name: "mcp_call null item_id",
			ev: map[string]any{
				"type": "response.mcp_call.in_progress", "item_id": nil, "output_index": 0,
			},
		},
		{
			name: "web_search missing output_index",
			ev:   map[string]any{"type": "response.web_search_call.searching", "item_id": "item_1"},
		},
		{
			name: "file_search wrong-type output_index",
			ev: map[string]any{
				"type": "response.file_search_call.completed", "item_id": "item_1", "output_index": "0",
			},
		},
		{
			name: "mcp_args.delta missing delta",
			ev: map[string]any{
				"type": "response.mcp_call_arguments.delta", "item_id": "item_1", "output_index": 0,
			},
		},
		{
			name: "code.delta null delta",
			ev: map[string]any{
				"type": "response.code_interpreter_call_code.delta", "item_id": "item_1",
				"output_index": 0, "delta": nil,
			},
		},
		{
			name: "partial_image missing b64",
			ev: map[string]any{
				"type": "response.image_generation_call.partial_image", "item_id": "item_1",
				"output_index": 0, "partial_image_index": 0,
			},
		},
		{
			name: "partial_image missing index",
			ev: map[string]any{
				"type": "response.image_generation_call.partial_image", "item_id": "item_1",
				"output_index": 0, "partial_image_b64": "x",
			},
		},
		// Empty phase object rejected.
		{
			name: "mcp bare type only",
			ev:   map[string]any{"type": "response.mcp_call.completed"},
		},
		// SDK union but not adapter-required fabricated still rejected when unknown.
		{
			name: "audio.delta not accepted",
			ev: map[string]any{
				"type": "response.audio.delta", "item_id": "a1", "output_index": 0, "delta": "x",
			},
		},
		// Output item / content part union shape.
		{
			name: "output_item empty object {}",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0, "item": map[string]any{},
			},
		},
		{
			name: "output_item wrong union type",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "not_a_real_item", "id": "x"},
			},
		},
		{
			name: "output_item missing type",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"id": "m1"},
			},
		},
		{
			name: "output_item function_call ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": validPinnedFunctionCallItem("fc1", "echo"),
			},
			ok: true,
		},
		{
			name: "output_item mcp_call ok",
			ev: map[string]any{
				"type": "response.output_item.done", "output_index": 0,
				"item": validPinnedMCPCallItem("mcp1"),
			},
			ok: true,
		},
		{
			name: "content_part empty object {}",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "part": map[string]any{},
			},
		},
		{
			name: "content_part wrong union type",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "image", "text": "nope"},
			},
		},
		{
			name: "content_part refusal ok",
			ev: map[string]any{
				"type": "response.content_part.done", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "refusal", "refusal": "no"},
			},
			ok: true,
		},
		{
			name: "content_part reasoning_text ok",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "reasoning_text", "text": "why"},
			},
			ok: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(tc.ev)), "text/event-stream")
			if tc.ok {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				return
			}
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want stream invalid, got %v", err)
			}
			// Stable errors must not embed secret-like provider bodies.
			if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "Bearer ") {
				t.Fatalf("error leaked secret material: %v", err)
			}
		})
	}
}

func TestValidateVerificationSSE_PinnedValidLifecycle(t *testing.T) {
	// Valid pinned fixture: queued → created → in_progress → delta → completed, same id.
	queued := map[string]any{
		"type":            "response.queued",
		"sequence_number": 0,
		"response":        validLifecycleResponse("resp_pin", "queued"),
	}
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 1,
		"response": map[string]any{
			"id": "resp_pin", "object": "response", "status": "in_progress",
			"output": []any{}, "usage": nil,
		},
	}
	inProg := map[string]any{
		"type":            "response.in_progress",
		"sequence_number": 2,
		"response": map[string]any{
			"id": "resp_pin", "object": "response", "status": "in_progress", "output": []any{},
		},
	}
	delta := map[string]any{
		"type": "response.output_text.delta", "delta": "ok", "item_id": "m1",
		"output_index": 0, "content_index": 0, "sequence_number": 3, "logprobs": []any{},
	}
	completed := validCompletedEnvelope("resp_pin")
	var b strings.Builder
	for _, obj := range []map[string]any{queued, created, inProg, delta, completed} {
		raw, _ := json.Marshal(obj)
		typ, _ := obj["type"].(string)
		b.WriteString("event: " + typ + "\ndata: " + string(raw) + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	if err := validateVerificationResponsesPayload([]byte(b.String()), "text/event-stream"); err != nil {
		t.Fatalf("pinned valid lifecycle: %v", err)
	}
}

// sseWithCompleted wraps intermediate events plus a valid terminal completed.
// Auto-fills sequence_number when absent so per-event field tests stay focused.
func sseWithCompleted(events ...map[string]any) string {
	var b strings.Builder
	for i, obj := range events {
		if _, ok := obj["sequence_number"]; !ok {
			obj["sequence_number"] = i
		}
		raw, _ := json.Marshal(obj)
		typ, _ := obj["type"].(string)
		b.WriteString("event: " + typ + "\ndata: " + string(raw) + "\n\n")
	}
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	b.WriteString("event: response.completed\ndata: " + string(comp) + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestValidateVerificationSSE_EmptyOutputTextDeltaRejected(t *testing.T) {
	// Bare empty intermediate object must fail (missing item_id/indexes/delta).
	empty := map[string]any{"type": "response.output_text.delta"}
	err := validateVerificationResponsesPayload([]byte(sseWithCompleted(empty)), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("empty output_text.delta must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_PerEventRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		ev   map[string]any
		ok   bool
	}{
		{
			name: "output_item.added missing item",
			ev:   map[string]any{"type": "response.output_item.added", "output_index": 0},
		},
		{
			name: "output_item.added null item",
			ev:   map[string]any{"type": "response.output_item.added", "output_index": 0, "item": nil},
		},
		{
			name: "output_item.added ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": validPinnedMessageItem("m1", "in_progress"),
			},
			ok: true,
		},
		{
			name: "output_item.done missing output_index",
			ev: map[string]any{
				"type": "response.output_item.done",
				"item": validPinnedMessageItem("m1", "completed"),
			},
		},
		{
			name: "content_part.added missing part",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
			},
		},
		{
			name: "content_part.done ok",
			ev: map[string]any{
				"type": "response.content_part.done", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": validPinnedOutputTextPart("hi"),
			},
			ok: true,
		},
		{
			name: "output_text.delta null delta",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": nil, "logprobs": []any{},
			},
		},
		{
			name: "output_text.delta numeric delta",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": 1, "logprobs": []any{},
			},
		},
		{
			name: "output_text.delta missing item_id",
			ev: map[string]any{
				"type": "response.output_text.delta", "delta": "x",
				"output_index": 0, "content_index": 0, "logprobs": []any{},
			},
		},
		{
			name: "output_text.delta missing logprobs",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "x",
			},
		},
		{
			name: "output_text.delta empty string delta ok",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "", "logprobs": []any{},
			},
			ok: true,
		},
		{
			name: "output_text.done missing text",
			ev: map[string]any{
				"type": "response.output_text.done", "item_id": "m1",
				"output_index": 0, "content_index": 0, "logprobs": []any{},
			},
		},
		{
			name: "output_text.done ok",
			ev: map[string]any{
				"type": "response.output_text.done", "item_id": "m1",
				"output_index": 0, "content_index": 0, "text": "full", "logprobs": []any{},
			},
			ok: true,
		},
		{
			name: "function_call_arguments.delta missing delta",
			ev: map[string]any{
				"type":    "response.function_call_arguments.delta",
				"item_id": "fc1", "output_index": 0,
			},
		},
		{
			name: "function_call_arguments.done missing name",
			ev: map[string]any{
				"type":    "response.function_call_arguments.done",
				"item_id": "fc1", "output_index": 0, "arguments": `{"q":1}`,
			},
		},
		{
			name: "function_call_arguments.done ok",
			ev: map[string]any{
				"type":    "response.function_call_arguments.done",
				"item_id": "fc1", "output_index": 0, "arguments": `{"q":1}`, "name": "lookup",
			},
			ok: true,
		},
		{
			name: "reasoning_summary_text.delta missing summary_index",
			ev: map[string]any{
				"type":    "response.reasoning_summary_text.delta",
				"item_id": "rs1", "output_index": 0, "delta": "think",
			},
		},
		{
			name: "reasoning_summary_text.done ok",
			ev: map[string]any{
				"type":    "response.reasoning_summary_text.done",
				"item_id": "rs1", "output_index": 0, "summary_index": 0, "text": "done",
			},
			ok: true,
		},
		{
			name: "reasoning_summary_part.added missing part",
			ev: map[string]any{
				"type":    "response.reasoning_summary_part.added",
				"item_id": "rs1", "output_index": 0, "summary_index": 0,
			},
		},
		{
			name: "reasoning_summary_part.done ok",
			ev: map[string]any{
				"type":    "response.reasoning_summary_part.done",
				"item_id": "rs1", "output_index": 0, "summary_index": 0,
				"part": map[string]any{"type": "summary_text", "text": "x"},
			},
			ok: true,
		},
		{
			name: "refusal.delta ok",
			ev: map[string]any{
				"type": "response.refusal.delta", "item_id": "m1",
				"output_index": 0, "content_index": 0, "delta": "no",
			},
			ok: true,
		},
		{
			name: "refusal.done missing refusal",
			ev: map[string]any{
				"type": "response.refusal.done", "item_id": "m1",
				"output_index": 0, "content_index": 0,
			},
		},
		{
			name: "refusal.done ok",
			ev: map[string]any{
				"type": "response.refusal.done", "item_id": "m1",
				"output_index": 0, "content_index": 0, "refusal": "cannot help",
			},
			ok: true,
		},
		{
			name: "reasoning_text.delta ok",
			ev: map[string]any{
				"type": "response.reasoning_text.delta", "item_id": "r1",
				"output_index": 0, "content_index": 0, "delta": "think",
			},
			ok: true,
		},
		{
			name: "reasoning_text.done ok",
			ev: map[string]any{
				"type": "response.reasoning_text.done", "item_id": "r1",
				"output_index": 0, "content_index": 0, "text": "full think",
			},
			ok: true,
		},
		{
			name: "annotation.added missing annotation",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
			},
		},
		{
			name: "annotation.added ok",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": validPinnedURLCitationAnnotation(),
			},
			ok: true,
		},
		{
			name: "output_index float rejected",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "m1",
				"output_index": 0.5, "content_index": 0, "delta": "x", "logprobs": []any{},
			},
		},
		{
			name: "item_id empty string rejected",
			ev: map[string]any{
				"type": "response.output_text.delta", "item_id": "",
				"output_index": 0, "content_index": 0, "delta": "x", "logprobs": []any{},
			},
		},
		{
			name: "sequence_number missing rejected",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item":            validPinnedMessageItem("m1", "in_progress"),
				"sequence_number": nil, // will still be present as null → fail int parse; also explicit missing test below
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(tc.ev)), "text/event-stream")
			if tc.ok {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				return
			}
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want stream invalid, got %v", err)
			}
		})
	}
}

func TestValidateVerificationJSON_EnvelopeTypeAdversarial(t *testing.T) {
	validNested := map[string]any{
		"id": "r", "object": "response", "status": "completed", "output": []any{},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	}
	// Plain authentic object (no type) still accepted.
	plain, _ := json.Marshal(validNested)
	if err := validateVerificationJSONObject(plain); err != nil {
		t.Fatalf("plain: %v", err)
	}
	// Envelope with correct string type.
	envOK, _ := json.Marshal(map[string]any{"type": "response.completed", "response": validNested})
	if err := validateVerificationJSONObject(envOK); err != nil {
		t.Fatalf("envelope ok: %v", err)
	}
	// Envelope without type but with nested response — still accepted if nested is completed.
	envNoType, _ := json.Marshal(map[string]any{"response": validNested})
	if err := validateVerificationJSONObject(envNoType); err != nil {
		t.Fatalf("envelope no type: %v", err)
	}

	// Adversarial type values must reject (not coerce to empty/absent).
	for _, bad := range []string{
		`{"type":null,"response":` + string(plain) + `}`,
		`{"type":123,"response":` + string(plain) + `}`,
		`{"type":true,"response":` + string(plain) + `}`,
		`{"type":{"x":1},"response":` + string(plain) + `}`,
		`{"type":["response.completed"],"response":` + string(plain) + `}`,
		`{"type":"","response":` + string(plain) + `}`,
		`{"type":"response.in_progress","response":` + string(plain) + `}`,
		`{"type":null}`,
		`{"type":42}`,
		`{"type":"response.completed"}`, // missing nested response
	} {
		err := validateVerificationJSONObject([]byte(bad))
		if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
			t.Fatalf("bad type payload %s: want stream invalid, got %v", bad, err)
		}
	}
}

func TestValidateVerificationSSE_MissingSequenceNumberRejected(t *testing.T) {
	// Explicitly omit sequence_number (sseWithCompleted would auto-fill; build manually).
	ev := map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": validPinnedMessageItem("m1", "in_progress"),
	}
	raw, _ := json.Marshal(ev)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.output_item.added\ndata: " + string(raw) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("missing sequence_number must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_QueuedWrongStatusRejected(t *testing.T) {
	queued := map[string]any{
		"type":            "response.queued",
		"sequence_number": 0,
		"response":        validLifecycleResponse("r1", "in_progress"), // wrong status
	}
	raw, _ := json.Marshal(queued)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.queued\ndata: " + string(raw) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("queued with non-queued status must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_LifecycleMissingOutputRejected(t *testing.T) {
	created := map[string]any{
		"type":            "response.created",
		"sequence_number": 0,
		"response": map[string]any{
			"id": "r1", "object": "response", "status": "in_progress",
			// output omitted
		},
	}
	raw, _ := json.Marshal(created)
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	sse := "event: response.created\ndata: " + string(raw) + "\n\n" +
		"event: response.completed\ndata: " + string(comp) + "\n\n"
	err := validateVerificationResponsesPayload([]byte(sse), "text/event-stream")
	if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("lifecycle missing output must reject, got %v", err)
	}
}

func TestValidateVerificationSSE_AllAllowlistedValidFixtures(t *testing.T) {
	// One valid fixture per allowlisted intermediate/lifecycle type (plus terminal completed).
	fixtures := []map[string]any{
		withSeq(map[string]any{
			"type": "response.queued", "response": validLifecycleResponse("r1", "queued"),
		}, 0),
		withSeq(map[string]any{
			"type": "response.created", "response": validLifecycleResponse("r1", "in_progress"),
		}, 1),
		withSeq(map[string]any{
			"type": "response.in_progress", "response": validLifecycleResponse("r1", "in_progress"),
		}, 2),
		withSeq(map[string]any{
			"type": "response.output_item.added", "output_index": 0,
			"item": validPinnedMessageItem("m1", "in_progress"),
		}, 3),
		withSeq(map[string]any{
			"type": "response.content_part.added", "item_id": "m1",
			"output_index": 0, "content_index": 0,
			"part": validPinnedOutputTextPart(""),
		}, 4),
		withSeq(map[string]any{
			"type": "response.output_text.delta", "item_id": "m1",
			"output_index": 0, "content_index": 0, "delta": "hi", "logprobs": []any{},
		}, 5),
		withSeq(map[string]any{
			"type": "response.output_text.done", "item_id": "m1",
			"output_index": 0, "content_index": 0, "text": "hi", "logprobs": []any{},
		}, 6),
		withSeq(map[string]any{
			"type": "response.output_text.annotation.added", "item_id": "m1",
			"output_index": 0, "content_index": 0, "annotation_index": 0,
			"annotation": validPinnedFileCitationAnnotation(),
		}, 7),
		withSeq(map[string]any{
			"type": "response.refusal.delta", "item_id": "m1",
			"output_index": 0, "content_index": 0, "delta": "no",
		}, 8),
		withSeq(map[string]any{
			"type": "response.refusal.done", "item_id": "m1",
			"output_index": 0, "content_index": 0, "refusal": "no",
		}, 9),
		withSeq(map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": "fc1",
			"output_index": 1, "delta": `{"q":`,
		}, 10),
		withSeq(map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "fc1",
			"output_index": 1, "arguments": `{"q":1}`, "name": "lookup",
		}, 11),
		withSeq(map[string]any{
			"type": "response.reasoning_text.delta", "item_id": "rr1",
			"output_index": 2, "content_index": 0, "delta": "r",
		}, 12),
		withSeq(map[string]any{
			"type": "response.reasoning_text.done", "item_id": "rr1",
			"output_index": 2, "content_index": 0, "text": "r",
		}, 13),
		withSeq(map[string]any{
			"type": "response.reasoning_summary_text.delta", "item_id": "rr1",
			"output_index": 2, "summary_index": 0, "delta": "s",
		}, 14),
		withSeq(map[string]any{
			"type": "response.reasoning_summary_text.done", "item_id": "rr1",
			"output_index": 2, "summary_index": 0, "text": "s",
		}, 15),
		withSeq(map[string]any{
			"type": "response.reasoning_summary_part.added", "item_id": "rr1",
			"output_index": 2, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": "s"},
		}, 16),
		withSeq(map[string]any{
			"type": "response.reasoning_summary_part.done", "item_id": "rr1",
			"output_index": 2, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": "s"},
		}, 17),
		withSeq(map[string]any{
			"type": "response.output_item.done", "output_index": 0,
			"item": validPinnedMessageItem("m1", "completed"),
		}, 18),
		withSeq(map[string]any{
			"type": "response.content_part.done", "item_id": "m1",
			"output_index": 0, "content_index": 0,
			"part": validPinnedOutputTextPart("hi"),
		}, 19),
	}
	var b strings.Builder
	for _, obj := range fixtures {
		raw, _ := json.Marshal(obj)
		typ, _ := obj["type"].(string)
		b.WriteString("event: " + typ + "\ndata: " + string(raw) + "\n\n")
	}
	comp, _ := json.Marshal(validCompletedEnvelope("r1"))
	b.WriteString("event: response.completed\ndata: " + string(comp) + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	if err := validateVerificationResponsesPayload([]byte(b.String()), "text/event-stream"); err != nil {
		t.Fatalf("all allowlisted fixtures must accept: %v", err)
	}
}

// TestValidateVerificationSSE_DeepNestedUnionMembers covers member-specific
// required fields for output items, content parts, reasoning summary parts, and
// annotations derived from openai-go/v3@v3.35.0 pinned structs.
func TestValidateVerificationSSE_DeepNestedUnionMembers(t *testing.T) {
	cases := []struct {
		name string
		ev   map[string]any
		ok   bool
	}{
		// --- message item ---
		{
			name: "message missing id",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "message", "status": "in_progress", "content": []any{}},
			},
		},
		{
			name: "message missing content",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "message", "id": "m1", "status": "in_progress"},
			},
		},
		{
			name: "message missing status",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "message", "id": "m1", "content": []any{}},
			},
		},
		{
			name: "message wrong status domain",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "message", "id": "m1", "status": "failed", "content": []any{}},
			},
		},
		{
			name: "message null content",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "message", "id": "m1", "status": "completed", "content": nil},
			},
		},
		{
			name: "message ok completed",
			ev: map[string]any{
				"type": "response.output_item.done", "output_index": 0,
				"item": validPinnedMessageItem("m1", "completed"),
			},
			ok: true,
		},
		// --- function_call ---
		{
			name: "function_call missing call_id",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "function_call", "name": "echo", "arguments": `{}`},
			},
		},
		{
			name: "function_call ok",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": validPinnedFunctionCallItem("c1", "echo"),
			},
			ok: true,
		},
		// --- content part output_text ---
		{
			name: "output_text missing text",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "annotations": []any{}},
			},
		},
		{
			name: "output_text missing annotations",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "hi"},
			},
		},
		{
			name: "output_text null annotations",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "hi", "annotations": nil},
			},
		},
		{
			name: "output_text ok empty annotations",
			ev: map[string]any{
				"type": "response.content_part.done", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": validPinnedOutputTextPart("hi"),
			},
			ok: true,
		},
		// --- reasoning summary part ---
		{
			name: "summary part empty object",
			ev: map[string]any{
				"type": "response.reasoning_summary_part.added", "item_id": "r1",
				"output_index": 0, "summary_index": 0, "part": map[string]any{},
			},
		},
		{
			name: "summary part missing text",
			ev: map[string]any{
				"type": "response.reasoning_summary_part.added", "item_id": "r1",
				"output_index": 0, "summary_index": 0,
				"part": map[string]any{"type": "summary_text"},
			},
		},
		{
			name: "summary part wrong type",
			ev: map[string]any{
				"type": "response.reasoning_summary_part.added", "item_id": "r1",
				"output_index": 0, "summary_index": 0,
				"part": map[string]any{"type": "output_text", "text": "x"},
			},
		},
		{
			name: "summary part ok",
			ev: map[string]any{
				"type": "response.reasoning_summary_part.done", "item_id": "r1",
				"output_index": 0, "summary_index": 0,
				"part": map[string]any{"type": "summary_text", "text": "sum"},
			},
			ok: true,
		},
		// --- annotations ---
		{
			name: "annotation fabricated type",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{"type": "magic_link", "url": "https://x"},
			},
		},
		{
			name: "annotation url_citation missing indices",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{"type": "url_citation", "url": "https://x", "title": "t"},
			},
		},
		{
			name: "annotation file_citation missing filename",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{"type": "file_citation", "file_id": "f1", "index": 0},
			},
		},
		{
			name: "annotation file_path ok",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{"type": "file_path", "file_id": "f1", "index": 0},
			},
			ok: true,
		},
		{
			name: "annotation container_file_citation ok",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{
					"type": "container_file_citation", "container_id": "c1", "file_id": "f1",
					"filename": "a.txt", "start_index": 0, "end_index": 2,
				},
			},
			ok: true,
		},
		{
			name: "annotation empty object",
			ev: map[string]any{
				"type": "response.output_text.annotation.added", "item_id": "m1",
				"output_index": 0, "content_index": 0, "annotation_index": 0,
				"annotation": map[string]any{},
			},
		},
		// Nested annotation inside output_text.annotations array.
		{
			name: "output_text annotations fabricated member",
			ev: map[string]any{
				"type": "response.content_part.added", "item_id": "m1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{
					"type": "output_text", "text": "x",
					"annotations": []any{map[string]any{"type": "not_real"}},
				},
			},
		},
		// Non-adapter output item type rejected even if in full SDK union.
		{
			name: "computer_call not adapter-supported",
			ev: map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"type": "computer_call", "id": "c1", "call_id": "c1", "status": "completed",
					"pending_safety_checks": []any{}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVerificationResponsesPayload([]byte(sseWithCompleted(tc.ev)), "text/event-stream")
			if tc.ok {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				return
			}
			if !errors.Is(err, modelconfig.ErrAgenticStreamInvalid) {
				t.Fatalf("want ErrAgenticStreamInvalid, got %v", err)
			}
			if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "Bearer ") {
				t.Fatalf("error leaked secret material: %v", err)
			}
		})
	}
}
