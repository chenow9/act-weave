package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"actweave/backend/internal/modelconfig"
)

// Verification-only response body cap. Probes are low-token; this bounds memory
// while allowing full JSON/SSE capture for strict usage validation. The raw body
// is never logged or stored beyond the in-flight restore for the adapter.
const verificationResponsesBodyCap = 512 * 1024

// wrapClientWithVerificationUsageValidator returns a client whose Transport
// validates successful Responses JSON/SSE payloads for strict usage shapes
// before the typed adapter projects them. Wrong JSON types (e.g. string
// "input_tokens") classify as MODEL_CONFIG_AGENTIC_USAGE_INVALID immediately.
//
// Non-2xx Responses responses never pass the provider body to the adapter:
// the transport drains/discards the body and returns a typed status error so
// secrets in 400/401/429/500 bodies cannot appear in error strings, logs, or
// stored codes. Network net.Error maps to MODEL_CONFIG_NETWORK_ERROR.
//
// Verification-only: do not use on production runtime traffic.
func wrapClientWithVerificationUsageValidator(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	// Shallow copy so callers' clients are not mutated.
	cp := *client
	base := cp.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*verificationUsageTransport); ok {
		return &cp
	}
	cp.Transport = &verificationUsageTransport{base: base}
	return &cp
}

type verificationUsageTransport struct {
	base http.RoundTripper
}

func (t *verificationUsageTransport) WrappedRoundTripper() http.RoundTripper { return t.base }

func (t *verificationUsageTransport) WithWrappedRoundTripper(base http.RoundTripper) http.RoundTripper {
	return &verificationUsageTransport{base: base}
}

func (t *verificationUsageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		// Network failures on any Responses phase => MODEL_CONFIG_NETWORK_ERROR.
		// Timeout/cancel remain as context errors for classifyVerificationError.
		return nil, mapVerificationTransportNetworkError(err)
	}
	if resp == nil {
		return nil, fmt.Errorf("%w: empty HTTP response", modelconfig.ErrVerificationNetwork)
	}

	// Non-2xx: never let provider body reach the adapter (RequestError embeds Body).
	// Classify by status while discarding body content entirely.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isResponsesAPIPath(req) {
			discardAndCloseBody(resp.Body)
			resp.Body = nil
			return nil, classifyResponsesHTTPStatus(resp.StatusCode)
		}
		// Non-Responses paths (e.g. /models) keep status but sanitize body so
		// accidental adapter reuse cannot leak provider error text.
		sanitizeNon2xxBody(resp)
		return resp, nil
	}

	// Only validate successful Responses bodies.
	if !isResponsesAPIPath(req) {
		return resp, nil
	}
	if resp.Body == nil {
		return resp, fmt.Errorf("%w: empty responses body", modelconfig.ErrAgenticUsageInvalid)
	}
	limited := io.LimitReader(resp.Body, verificationResponsesBodyCap+1)
	buf, readErr := io.ReadAll(limited)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read responses body", modelconfig.ErrAgenticStreamInvalid)
	}
	if len(buf) > verificationResponsesBodyCap {
		// Cap exceeded — fail closed without retaining body.
		// RoundTrip contract: non-nil error must not pair with a response.
		return nil, fmt.Errorf("%w: responses body exceeds verification cap", modelconfig.ErrAgenticStreamInvalid)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if err := validateVerificationResponsesPayload(buf, ct); err != nil {
		// Return nil response so callers cannot ignore the validation error.
		return nil, err
	}
	// Restore body for the typed adapter; never log/store raw body.
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	resp.ContentLength = int64(len(buf))
	return resp, nil
}

func discardAndCloseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 8<<10))
	_ = body.Close()
}

// sanitizeNon2xxBody replaces any non-2xx response body with an empty safe payload
// so downstream code cannot embed provider error text.
func sanitizeNon2xxBody(resp *http.Response) {
	if resp == nil {
		return
	}
	discardAndCloseBody(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(nil))
	resp.ContentLength = 0
	resp.Header.Del("Content-Length")
}

// classifyResponsesHTTPStatus maps provider HTTP statuses to stable typed errors
// without embedding status text or body. Never returns responses-unsupported for
// generic upstream failures — only 404 / route-missing style conditions.
func classifyResponsesHTTPStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return modelconfig.ErrUpstreamAuthentication
	case status == http.StatusNotFound, status == http.StatusMethodNotAllowed:
		// Missing /responses route or method: protocol unsupported.
		return modelconfig.ErrResponsesUnsupported
	case status == http.StatusTooManyRequests || status >= 500:
		return fmt.Errorf("%w: HTTP_STATUS_%d", modelconfig.ErrVerificationUpstream, status)
	case status >= 400 && status < 500:
		// Other client errors (400 etc.): stable upstream, not protocol unsupported.
		return fmt.Errorf("%w: HTTP_STATUS_%d", modelconfig.ErrVerificationUpstream, status)
	default:
		return fmt.Errorf("%w: HTTP_STATUS_%d", modelconfig.ErrVerificationUpstream, status)
	}
}

func mapVerificationTransportNetworkError(err error) error {
	if err == nil {
		return nil
	}
	// Preserve timeout/cancel for classifyVerificationError.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		// Do not embed dial strings / host details into returned errors.
		return modelconfig.ErrVerificationNetwork
	}
	// Unknown transport failure: treat as network without embedding body/text.
	return modelconfig.ErrVerificationNetwork
}

func isResponsesAPIPath(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.TrimSuffix(req.URL.Path, "/")
	return strings.HasSuffix(path, "/responses") || strings.Contains(path, "/responses/")
}

// validateVerificationResponsesPayload strictly validates JSON or SSE Responses
// payloads for usage field types and consistency. Duplicate keys and wrong
// scalar types fail as USAGE_INVALID (not deferred to tool-search errors).
//
// OpenAI Responses SSE lifecycle (pinned openai-go/v3 + agenticopenai adapter) —
// fail closed against the documented allowlist and type-specific required fields:
//   - Every SSE data: JSON event (except exact terminal [DONE]) requires an
//     explicit nonempty event: header that exactly equals data.type.
//   - Every event requires a valid nonnegative integer sequence_number.
//   - Lifecycle events (queued/created/in_progress/completed) require non-null
//     response object with official shape object/id/status/output and exact
//     status map: queued→queued, created/in_progress→in_progress,
//     completed→completed + strict usage.
//   - failed/incomplete/cancelled/error lifecycle never counts as successful terminal.
//   - Event types outside the documented allowlist are rejected (no vendor ignore).
//   - Intermediate content deltas need no usage; non-null usage is shape-validated.
//   - Non-stream JSON requires authentic Responses object (object:"response",
//     nonempty id, status:"completed", output collection, terminal usage).
func validateVerificationResponsesPayload(raw []byte, contentType string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty responses payload", modelconfig.ErrAgenticUsageInvalid)
	}
	if strings.Contains(contentType, "text/event-stream") || looksLikeSSE(raw) {
		return validateVerificationSSE(raw)
	}
	// Non-stream JSON body is a complete response object: require terminal
	// completed status + valid usage. No event-header / type-string fallback.
	return validateVerificationJSONObject(raw)
}

func looksLikeSSE(raw []byte) bool {
	// Probe SSE always uses "event:" / "data:" lines.
	s := string(raw)
	return strings.Contains(s, "\ndata:") || strings.HasPrefix(s, "data:") || strings.Contains(s, "\nevent:")
}

// sseStreamState tracks authenticity across an SSE Responses lifecycle.
type sseStreamState struct {
	responseID             string
	terminalCompletedCount int
	sawAnyLifecycle        bool
}

func validateVerificationSSE(raw []byte) error {
	// Parse SSE frames; require event:/data.type authenticity and lifecycle rules.
	lines := strings.Split(string(raw), "\n")
	var dataBuf strings.Builder
	var eventType string
	sawData := false
	state := &sseStreamState{}
	flush := func() error {
		if !sawData {
			eventType = ""
			return nil
		}
		data := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		sawData = false
		et := strings.TrimSpace(eventType)
		eventType = ""
		if data == "" || data == "[DONE]" {
			return nil
		}
		return validateVerificationSSEDataEvent(et, []byte(data), state)
	}
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if sawData {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
			sawData = true
			continue
		}
		// Other SSE fields (id:, retry:) ignored.
	}
	if err := flush(); err != nil {
		return err
	}
	// Exactly one authentic terminal completed is required. No loose substring
	// fallback on status/type when event: was missing or mismatched.
	if state.terminalCompletedCount == 0 {
		return fmt.Errorf("%w: missing response.completed terminal event", modelconfig.ErrAgenticStreamInvalid)
	}
	if state.terminalCompletedCount > 1 {
		return fmt.Errorf("%w: multiple response.completed terminal events", modelconfig.ErrAgenticStreamInvalid)
	}
	return nil
}

// pinnedResponsesSSEEventTypes is the closed allowlist of OpenAI Responses SSE
// event types accepted by verification probes.
//
// Exact sources (pinned local modules — do not invent names from memory/docs):
//   - github.com/openai/openai-go/v3@v3.35.0 responses.ResponseStreamEventUnion.AsAny
//   - github.com/cloudwego/eino-ext/components/model/agenticopenai@v0.2.2
//     responses_event_convertor.go switch (adapter-handled families)
//
// Includes every event type the pinned adapter explicitly handles, plus the
// lifecycle/error terminals and intermediate text/function/reasoning events that
// appear in ResponseStreamEventUnion and are required for verification probes.
// Fabricated names (e.g. response.cancelled) and union members the adapter does
// not handle and probes do not require (audio.*, custom_tool_call_input.*) are
// rejected fail-closed.
var pinnedResponsesSSEEventTypes = map[string]struct{}{
	// Lifecycle (SDK union + adapter: created/in_progress/completed/failed/incomplete;
	// queued is in SDK union and used by lifecycle probes).
	"response.queued":      {},
	"response.created":     {},
	"response.in_progress": {},
	"response.completed":   {},
	"response.failed":      {},
	"response.incomplete":  {},
	"error":                {},
	// Output item / content part (adapter-handled).
	"response.output_item.added":  {},
	"response.output_item.done":   {},
	"response.content_part.added": {},
	"response.content_part.done":  {},
	// Text / refusal / annotations (adapter handles deltas + annotation; done variants in SDK).
	"response.output_text.delta":            {},
	"response.output_text.done":             {},
	"response.output_text.annotation.added": {},
	"response.refusal.delta":                {},
	"response.refusal.done":                 {},
	// Function-call arguments (adapter delta; done in SDK).
	"response.function_call_arguments.delta": {},
	"response.function_call_arguments.done":  {},
	// Reasoning text + summary (adapter deltas; done/part in SDK).
	"response.reasoning_text.delta":         {},
	"response.reasoning_text.done":          {},
	"response.reasoning_summary_text.delta": {},
	"response.reasoning_summary_text.done":  {},
	"response.reasoning_summary_part.added": {},
	"response.reasoning_summary_part.done":  {},
	// MCP (adapter-handled: list_tools + call phase + args delta).
	"response.mcp_list_tools.in_progress": {},
	"response.mcp_list_tools.failed":      {},
	"response.mcp_list_tools.completed":   {},
	"response.mcp_call_arguments.delta":   {},
	"response.mcp_call_arguments.done":    {}, // SDK union; args done companion
	"response.mcp_call.in_progress":       {},
	"response.mcp_call.completed":         {},
	"response.mcp_call.failed":            {},
	// Web search (adapter-handled).
	"response.web_search_call.in_progress": {},
	"response.web_search_call.searching":   {},
	"response.web_search_call.completed":   {},
	// File search (adapter-handled).
	"response.file_search_call.in_progress": {},
	"response.file_search_call.searching":   {},
	"response.file_search_call.completed":   {},
	// Code interpreter (adapter-handled phases + code delta; code.done in SDK).
	"response.code_interpreter_call.in_progress":  {},
	"response.code_interpreter_call.interpreting": {},
	"response.code_interpreter_call_code.delta":   {},
	"response.code_interpreter_call_code.done":    {},
	"response.code_interpreter_call.completed":    {},
	// Image generation (adapter-handled).
	"response.image_generation_call.in_progress":   {},
	"response.image_generation_call.generating":    {},
	"response.image_generation_call.partial_image": {},
	"response.image_generation_call.completed":     {},
}

// Nested union registries (output items, content parts, annotations, logprobs)
// live in verification_responses_nested_unions.go as closed discriminator→validator
// maps. There is no separate permissive allowlist.

// validateVerificationSSEDataEvent validates one SSE data JSON object under the
// pinned Responses event lifecycle with mandatory event:/data.type authenticity.
func validateVerificationSSEDataEvent(eventType string, raw []byte, state *sseStreamState) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	// Fail closed: every data: JSON event requires an explicit nonempty event: header.
	if eventType == "" {
		return fmt.Errorf("%w: SSE data event missing event header", modelconfig.ErrAgenticStreamInvalid)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%w: invalid JSON in SSE data", modelconfig.ErrAgenticStreamInvalid)
	}
	if raw[0] != '{' {
		return fmt.Errorf("%w: SSE data must be a JSON object", modelconfig.ErrAgenticStreamInvalid)
	}
	// Duplicate keys in an SSE event envelope (or any nested protocol payload inside
	// it) are stream-protocol parse failures, not usage-contract failures.
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%w: SSE data decode", modelconfig.ErrAgenticStreamInvalid)
	}

	// data.type is mandatory and must exactly equal event:.
	dataType := ""
	if top["type"] != nil {
		dataType = rawJSONString(top["type"])
		if dataType == "" {
			return fmt.Errorf("%w: event type must be a string", modelconfig.ErrAgenticStreamInvalid)
		}
	}
	if dataType == "" {
		return fmt.Errorf("%w: SSE event %q missing data.type", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	if eventType != dataType {
		return fmt.Errorf("%w: SSE event %q mismatches data.type %q", modelconfig.ErrAgenticStreamInvalid, eventType, dataType)
	}
	typ := eventType

	// Reject every event type outside the pinned allowlist (no vendor ignore path).
	if _, ok := pinnedResponsesSSEEventTypes[typ]; !ok {
		return fmt.Errorf("%w: unknown Responses event type %q", modelconfig.ErrAgenticStreamInvalid, typ)
	}

	// Globally required on every pinned Responses stream event (openai-go/v3).
	if _, err := requireStrictNonNegJSONIntField(top, "sequence_number"); err != nil {
		return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
	}

	switch typ {
	case "response.queued", "response.created", "response.in_progress":
		state.sawAnyLifecycle = true
		respRaw, err := requireLifecycleResponseObject(typ, top)
		if err != nil {
			return err
		}
		if err := validateLifecycleResponseShape(typ, respRaw); err != nil {
			return err
		}
		if err := trackSSEResponseID(state, respRaw); err != nil {
			return err
		}
		return validateUsageInObject(respRaw, false /* requireUsage */)
	case "response.completed":
		if state.terminalCompletedCount > 0 {
			return fmt.Errorf("%w: duplicate response.completed terminal event", modelconfig.ErrAgenticStreamInvalid)
		}
		state.terminalCompletedCount++
		state.sawAnyLifecycle = true
		respRaw, err := requireLifecycleResponseObject(typ, top)
		if err != nil {
			return err
		}
		if err := validateLifecycleResponseShape(typ, respRaw); err != nil {
			return err
		}
		if err := trackSSEResponseID(state, respRaw); err != nil {
			return err
		}
		return validateUsageInObject(respRaw, true /* requireUsage */)
	case "response.failed", "response.incomplete", "error":
		// Terminal non-success lifecycle: stable stream-invalid classification.
		// Never count as successful terminal even if a later completed appears.
		// error event: require pinned code/message/param strings.
		// Note: response.cancelled is NOT in ResponseStreamEventUnion@v3.35.0 — rejected by allowlist.
		if typ == "error" {
			if err := validatePinnedErrorEventPayload(top); err != nil {
				return err
			}
		}
		return fmt.Errorf("%w: terminal non-success event %q", modelconfig.ErrAgenticStreamInvalid, typ)
	default:
		// All other allowlisted intermediate / tool-host events.
		if err := validatePinnedIntermediateEventPayload(typ, top); err != nil {
			return err
		}
		// Reject if a nested response spoofs completed status on a non-terminal event.
		if respRaw, ok := top["response"]; ok {
			if isJSONNullOrEmpty(respRaw) {
				return fmt.Errorf("%w: nested response must be object when present", modelconfig.ErrAgenticStreamInvalid)
			}
			if status := nestedResponseStatus(respRaw); status == "completed" {
				return fmt.Errorf("%w: non-terminal event %q claims completed response", modelconfig.ErrAgenticStreamInvalid, typ)
			}
			if err := trackSSEResponseID(state, respRaw); err != nil {
				return err
			}
		}
		if usageRaw, ok := top["usage"]; ok && !isJSONNullOrEmpty(usageRaw) {
			return validateStrictUsageObject(usageRaw)
		}
		return nil
	}
}

// validatePinnedIntermediateEventPayload enforces exact type-specific required
// fields for allowlisted non-lifecycle Responses SSE events from pinned
// openai-go/v3 event structs (api:"required" fields only). Missing/null/wrong
// types and wrong union member shapes fail as STREAM_INVALID.
//
//	output_item.added/done     → output_index, item (non-empty object + valid union type)
//	content_part.added/done    → item_id, output_index, content_index, part (valid part union)
//	output_text.delta          → item_id, output_index, content_index, delta (string), logprobs (array)
//	output_text.done           → item_id, output_index, content_index, text (string), logprobs (array)
//	output_text.annotation.added → item_id, output_index, content_index, annotation_index, annotation
//	refusal.delta              → item_id, output_index, content_index, delta (string)
//	refusal.done               → item_id, output_index, content_index, refusal (string)
//	function_call_arguments.delta → item_id, output_index, delta (string)
//	function_call_arguments.done  → item_id, output_index, arguments (string), name (nonempty string)
//	reasoning_text.delta/done  → item_id, output_index, content_index, delta|text (string)
//	reasoning_summary_text.*   → item_id, output_index, summary_index, delta|text
//	reasoning_summary_part.*   → item_id, output_index, summary_index, part (object)
//	mcp_list_tools.* / mcp_call.* (phase) → item_id, output_index
//	mcp_call_arguments.delta   → item_id, output_index, delta (string)
//	mcp_call_arguments.done    → item_id, output_index, arguments (string)
//	web_search_call.* / file_search_call.* → item_id, output_index
//	code_interpreter_call.* (phase) → item_id, output_index
//	code_interpreter_call_code.delta → item_id, output_index, delta (string)
//	code_interpreter_call_code.done  → item_id, output_index, code (string)
//	image_generation_call.* (phase) → item_id, output_index
//	image_generation_call.partial_image → item_id, output_index, partial_image_b64, partial_image_index
func validatePinnedIntermediateEventPayload(typ string, top map[string]json.RawMessage) error {
	switch typ {
	case "response.output_item.added", "response.output_item.done":
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if err := requirePinnedOutputItemField(top, "item"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.content_part.added", "response.content_part.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if err := requirePinnedContentPartField(top, "part"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.output_text.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		// delta must be a JSON string (empty string is allowed; missing/null/object fails).
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if err := requirePinnedLogprobsField(top, "logprobs"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.output_text.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "text"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if err := requirePinnedLogprobsField(top, "logprobs"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.output_text.annotation.added":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "annotation_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		// annotation is a closed union with member-specific required fields.
		if err := requirePinnedAnnotationField(top, "annotation"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.refusal.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.refusal.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "refusal"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.function_call_arguments.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.function_call_arguments.done":
		// Pinned ResponseFunctionCallArgumentsDoneEvent: arguments, item_id, name, output_index.
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "arguments"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireNonemptyJSONStringField(top, "name"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.reasoning_text.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.reasoning_text.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "content_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "text"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.reasoning_summary_text.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "summary_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.reasoning_summary_text.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "summary_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "text"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "summary_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if err := requirePinnedReasoningSummaryPartField(top, "part"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	// --- MCP / web_search / file_search / code_interpreter / image_generation ---
	// Phase events: item_id + output_index (pinned api:"required").
	case "response.mcp_list_tools.in_progress", "response.mcp_list_tools.failed", "response.mcp_list_tools.completed",
		"response.mcp_call.in_progress", "response.mcp_call.completed", "response.mcp_call.failed",
		"response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed",
		"response.file_search_call.in_progress", "response.file_search_call.searching", "response.file_search_call.completed",
		"response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting", "response.code_interpreter_call.completed",
		"response.image_generation_call.in_progress", "response.image_generation_call.generating", "response.image_generation_call.completed":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.mcp_call_arguments.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.mcp_call_arguments.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "arguments"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.code_interpreter_call_code.delta":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "delta"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.code_interpreter_call_code.done":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "code"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	case "response.image_generation_call.partial_image":
		if _, err := requireNonemptyJSONStringField(top, "item_id"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "output_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireJSONStringField(top, "partial_image_b64"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		if _, err := requireStrictNonNegJSONIntField(top, "partial_image_index"); err != nil {
			return fmt.Errorf("%w: %s: %v", modelconfig.ErrAgenticStreamInvalid, typ, err)
		}
		return nil
	default:
		// Allowlist gate above should have caught unknown types; no silent accept.
		return fmt.Errorf("%w: unhandled allowlisted event type %q", modelconfig.ErrAgenticStreamInvalid, typ)
	}
}

// validatePinnedErrorEventPayload enforces ResponseErrorEvent required fields:
// code, message, param (strings; may be empty per provider), sequence_number already checked.
func validatePinnedErrorEventPayload(top map[string]json.RawMessage) error {
	for _, key := range []string{"code", "message", "param"} {
		if _, err := requireJSONStringField(top, key); err != nil {
			return fmt.Errorf("%w: error: %v", modelconfig.ErrAgenticStreamInvalid, err)
		}
	}
	return nil
}

// requireJSONStringField requires key present as a JSON string (may be empty).
// Missing, null, number, bool, object, array all fail.
func requireJSONStringField(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("%s empty", key)
	}
	if bytes.Equal(raw, []byte("null")) {
		return "", fmt.Errorf("%s must not be null", key)
	}
	if raw[0] != '"' {
		return "", fmt.Errorf("%s must be a JSON string", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s must be a JSON string", key)
	}
	return s, nil
}

// requireNonemptyJSONStringField is requireJSONStringField plus nonempty after decode.
func requireNonemptyJSONStringField(fields map[string]json.RawMessage, key string) (string, error) {
	s, err := requireJSONStringField(fields, key)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", fmt.Errorf("%s must be nonempty", key)
	}
	return s, nil
}

// requireStrictNonNegJSONIntField requires key present as a nonnegative JSON integer
// (no float/string/null/bool/object/array).
func requireStrictNonNegJSONIntField(fields map[string]json.RawMessage, key string) (int, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	n, err := parseStrictNonNegJSONInt(raw, key)
	if err != nil {
		// Strip ErrAgenticUsageInvalid wrapper for stream field errors; callers re-wrap.
		return 0, fmt.Errorf("%s must be a nonnegative JSON integer", key)
	}
	return n, nil
}

// requireNonNullJSONObjectField requires key present as a non-null JSON object.
func requireNonNullJSONObjectField(fields map[string]json.RawMessage, key string) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("%s empty", key)
	}
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("%s must not be null", key)
	}
	if raw[0] != '{' {
		return fmt.Errorf("%s must be a JSON object", key)
	}
	return nil
}

// requireJSONArrayField requires key present as a JSON array (may be empty).
func requireJSONArrayField(fields map[string]json.RawMessage, key string) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("%s empty", key)
	}
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("%s must not be null", key)
	}
	if raw[0] != '[' {
		return fmt.Errorf("%s must be a JSON array", key)
	}
	return nil
}

// requirePresentNonNullField requires key present and not JSON null.
func requirePresentNonNullField(fields map[string]json.RawMessage, key string) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("%s empty", key)
	}
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("%s must not be null", key)
	}
	return nil
}

// requireLifecycleResponseObject requires a non-null JSON object under "response".
func requireLifecycleResponseObject(eventType string, top map[string]json.RawMessage) (json.RawMessage, error) {
	respRaw, ok := top["response"]
	if !ok {
		return nil, fmt.Errorf("%w: lifecycle event %q missing nested response", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	respRaw = bytes.TrimSpace(respRaw)
	if len(respRaw) == 0 || bytes.Equal(respRaw, []byte("null")) {
		return nil, fmt.Errorf("%w: lifecycle event %q response must be non-null object", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	if respRaw[0] != '{' {
		return nil, fmt.Errorf("%w: lifecycle event %q response must be object", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	return respRaw, nil
}

// validateLifecycleResponseShape enforces the official lifecycle response shape
// used by verification probes: object:"response", nonempty id, exact status for
// the event type, and an output array. Usage is validated separately.
//
// Status map (pinned ResponseStatus + event contract):
//
//	response.queued      → status queued
//	response.created     → status in_progress
//	response.in_progress → status in_progress
//	response.completed   → status completed
func validateLifecycleResponseShape(eventType string, respRaw json.RawMessage) error {
	respRaw = bytes.TrimSpace(respRaw)
	if len(respRaw) == 0 || respRaw[0] != '{' {
		return fmt.Errorf("%w: %s response must be object", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	if err := rejectDuplicateJSONKeys(respRaw); err != nil {
		// Nested lifecycle response is stream protocol, not usage.
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(respRaw, &obj); err != nil {
		return fmt.Errorf("%w: %s response decode", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	if rawJSONString(obj["object"]) != "response" {
		return fmt.Errorf("%w: %s response.object must be \"response\"", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	if id := rawJSONString(obj["id"]); id == "" {
		return fmt.Errorf("%w: %s response missing nonempty id", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	outRaw, ok := obj["output"]
	if !ok || isJSONNullOrEmpty(outRaw) {
		return fmt.Errorf("%w: %s response missing output collection", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	outRaw = bytes.TrimSpace(outRaw)
	if len(outRaw) == 0 || outRaw[0] != '[' {
		return fmt.Errorf("%w: %s response output must be array", modelconfig.ErrAgenticStreamInvalid, eventType)
	}
	status := rawJSONString(obj["status"])
	switch eventType {
	case "response.queued":
		if status != "queued" {
			return fmt.Errorf("%w: response.queued requires response.status queued, got %q", modelconfig.ErrAgenticStreamInvalid, status)
		}
	case "response.created", "response.in_progress":
		if status != "in_progress" {
			return fmt.Errorf("%w: %s requires response.status in_progress, got %q", modelconfig.ErrAgenticStreamInvalid, eventType, status)
		}
	case "response.completed":
		if status != "completed" {
			return fmt.Errorf("%w: response.completed requires response.status completed, got %q", modelconfig.ErrAgenticStreamInvalid, status)
		}
	}
	return nil
}

// validateLifecycleResponseStatus enforces exact event/status mapping for
// non-stream envelopes (shape validated by validateAuthenticCompletedResponseObject).
func validateLifecycleResponseStatus(eventType string, respRaw json.RawMessage) error {
	status := nestedResponseStatus(respRaw)
	switch eventType {
	case "response.queued":
		if status != "queued" {
			return fmt.Errorf("%w: response.queued requires response.status queued, got %q", modelconfig.ErrAgenticStreamInvalid, status)
		}
		return nil
	case "response.created", "response.in_progress":
		if status != "in_progress" {
			return fmt.Errorf("%w: %s requires response.status in_progress, got %q", modelconfig.ErrAgenticStreamInvalid, eventType, status)
		}
		return nil
	case "response.completed":
		if status != "completed" {
			return fmt.Errorf("%w: response.completed requires response.status completed, got %q", modelconfig.ErrAgenticStreamInvalid, status)
		}
		return nil
	default:
		return nil
	}
}

func nestedResponseStatus(respRaw json.RawMessage) string {
	respRaw = bytes.TrimSpace(respRaw)
	if len(respRaw) == 0 || respRaw[0] != '{' {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(respRaw, &obj); err != nil {
		return ""
	}
	return rawJSONString(obj["status"])
}

func nestedResponseID(respRaw json.RawMessage) string {
	respRaw = bytes.TrimSpace(respRaw)
	if len(respRaw) == 0 || respRaw[0] != '{' {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(respRaw, &obj); err != nil {
		return ""
	}
	return rawJSONString(obj["id"])
}

// trackSSEResponseID enforces response ID continuity when IDs are present.
// Conflicting IDs across lifecycle events fail closed.
func trackSSEResponseID(state *sseStreamState, respRaw json.RawMessage) error {
	if state == nil {
		return nil
	}
	id := nestedResponseID(respRaw)
	if id == "" {
		return nil
	}
	if state.responseID == "" {
		state.responseID = id
		return nil
	}
	if state.responseID != id {
		return fmt.Errorf("%w: response id conflict %q vs %q", modelconfig.ErrAgenticStreamInvalid, state.responseID, id)
	}
	return nil
}

// validateVerificationJSONObject validates a non-stream Responses JSON body.
// Requires an authentic completed Responses object:
//   - object:"response" (mandatory)
//   - nonempty canonical response id
//   - status:"completed"
//   - output collection present (array)
//   - terminal usage
//
// Mere status+usage maps and in_progress bodies are rejected. No event header.
func validateVerificationJSONObject(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty responses payload", modelconfig.ErrAgenticUsageInvalid)
	}
	if !json.Valid(raw) {
		// Malformed JSON is a stream/protocol defect, not usage.
		return fmt.Errorf("%w: invalid JSON in responses payload", modelconfig.ErrAgenticStreamInvalid)
	}
	if raw[0] != '{' {
		return fmt.Errorf("%w: responses payload must be a JSON object", modelconfig.ErrAgenticStreamInvalid)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		// Non-stream protocol envelope/object duplicate keys are stream-invalid.
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}

	// Envelope form: {"type":"response.completed","response":{...}}.
	// If "type" is present it MUST be a JSON string exactly "response.completed".
	// Numeric/null/object/array type values reject (never treated as empty/absent).
	// Absence of "type" is allowed only for the authentic plain Responses object shape
	// (handled below when "response" is also absent).
	if respRaw, ok := top["response"]; ok {
		if typRaw, hasType := top["type"]; hasType {
			typ, err := requireEnvelopeTypeString(typRaw)
			if err != nil {
				return err
			}
			if typ != "response.completed" {
				return fmt.Errorf("%w: non-stream envelope type %q is not terminal completed", modelconfig.ErrAgenticStreamInvalid, typ)
			}
			if err := validateLifecycleResponseStatus("response.completed", respRaw); err != nil {
				return err
			}
		}
		// Nested body must itself be an authentic completed Responses object.
		return validateAuthenticCompletedResponseObject(respRaw)
	}

	// Bare / plain response object: "type" key must not be present as a non-string
	// envelope spoof. If present without nested response, require exact completed
	// envelope type and still fail as non-authentic completed (missing nested body
	// fields). Non-string type rejects immediately.
	if typRaw, hasType := top["type"]; hasType {
		typ, err := requireEnvelopeTypeString(typRaw)
		if err != nil {
			return err
		}
		if typ != "response.completed" {
			return fmt.Errorf("%w: non-stream envelope type %q is not terminal completed", modelconfig.ErrAgenticStreamInvalid, typ)
		}
		// type=response.completed without nested response is not authentic.
		return fmt.Errorf("%w: non-stream envelope missing nested response", modelconfig.ErrAgenticStreamInvalid)
	}

	// Authentic plain Responses object shape (no event type key).
	return validateAuthenticCompletedResponseObject(raw)
}

// requireEnvelopeTypeString requires a non-null JSON string for envelope "type".
// Numbers, null, bools, objects, and arrays reject — they must not coerce to empty.
func requireEnvelopeTypeString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: non-stream envelope type empty", modelconfig.ErrAgenticStreamInvalid)
	}
	if bytes.Equal(raw, []byte("null")) {
		return "", fmt.Errorf("%w: non-stream envelope type must be a string, got null", modelconfig.ErrAgenticStreamInvalid)
	}
	if raw[0] != '"' {
		return "", fmt.Errorf("%w: non-stream envelope type must be a JSON string", modelconfig.ErrAgenticStreamInvalid)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: non-stream envelope type must be a JSON string", modelconfig.ErrAgenticStreamInvalid)
	}
	if s == "" {
		return "", fmt.Errorf("%w: non-stream envelope type must be nonempty", modelconfig.ErrAgenticStreamInvalid)
	}
	return s, nil
}

// validateAuthenticCompletedResponseObject requires object:"response", nonempty
// id, status:"completed", present output array, and strictly valid usage.
func validateAuthenticCompletedResponseObject(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("%w: non-stream response must be object", modelconfig.ErrAgenticStreamInvalid)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		// Protocol response object (not the usage sub-object) → stream-invalid.
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%w: non-stream response decode", modelconfig.ErrAgenticStreamInvalid)
	}

	objType := rawJSONString(top["object"])
	if objType != "response" {
		return fmt.Errorf("%w: non-stream object must be \"response\", got %q", modelconfig.ErrAgenticStreamInvalid, objType)
	}
	id := rawJSONString(top["id"])
	if id == "" {
		return fmt.Errorf("%w: non-stream response missing nonempty id", modelconfig.ErrAgenticStreamInvalid)
	}
	status := rawJSONString(top["status"])
	if status != "completed" {
		return fmt.Errorf("%w: non-stream response status %q is not completed", modelconfig.ErrAgenticStreamInvalid, status)
	}
	// Output collection is required (array; may be empty for tool-search probes).
	outRaw, ok := top["output"]
	if !ok || isJSONNullOrEmpty(outRaw) {
		return fmt.Errorf("%w: non-stream response missing output collection", modelconfig.ErrAgenticStreamInvalid)
	}
	outRaw = bytes.TrimSpace(outRaw)
	if len(outRaw) == 0 || outRaw[0] != '[' {
		return fmt.Errorf("%w: non-stream response output must be array", modelconfig.ErrAgenticStreamInvalid)
	}
	return validateUsageInObject(raw, true)
}

func isJSONNullOrEmpty(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

// validateUsageInObject validates a response-like object.
// requireUsage=true: usage must be a present, strictly valid object (terminal completed).
// requireUsage=false: usage may be absent or null; if present as object, validate shape.
func validateUsageInObject(raw json.RawMessage, requireUsage bool) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		if requireUsage {
			return fmt.Errorf("%w: missing completed response object", modelconfig.ErrAgenticUsageInvalid)
		}
		return nil
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticUsageInvalid, err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	usageRaw, hasUsage := obj["usage"]
	if !hasUsage || isJSONNullOrEmpty(usageRaw) {
		if requireUsage {
			return fmt.Errorf("%w: missing usage on completed response", modelconfig.ErrAgenticUsageInvalid)
		}
		// Intermediate: null/absent is not terminal failure.
		return nil
	}
	// Usage present as non-null: always strictly validate (intermediate or terminal).
	return validateStrictUsageObject(usageRaw)
}

func rawJSONString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// validateStrictUsageObject requires integer (non-float) token fields, rejects
// wrong types/nulls/strings, and enforces totals consistency.
func validateStrictUsageObject(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("%w: usage must be a JSON object", modelconfig.ErrAgenticUsageInvalid)
	}
	if raw[0] != '{' {
		return fmt.Errorf("%w: usage must be a JSON object", modelconfig.ErrAgenticUsageInvalid)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticUsageInvalid, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("%w: usage decode failed", modelconfig.ErrAgenticUsageInvalid)
	}

	input, err := requireStrictNonNegIntField(fields, "input_tokens", true)
	if err != nil {
		return err
	}
	output, err := requireStrictNonNegIntField(fields, "output_tokens", true)
	if err != nil {
		return err
	}
	total, err := requireStrictNonNegIntField(fields, "total_tokens", true)
	if err != nil {
		return err
	}
	// Also accept prompt_tokens/completion_tokens aliases if present (must be consistent).
	if _, has := fields["prompt_tokens"]; has {
		pt, err := requireStrictNonNegIntField(fields, "prompt_tokens", true)
		if err != nil {
			return err
		}
		if pt != input {
			return fmt.Errorf("%w: prompt_tokens/input_tokens mismatch", modelconfig.ErrAgenticUsageInvalid)
		}
	}
	if _, has := fields["completion_tokens"]; has {
		ct, err := requireStrictNonNegIntField(fields, "completion_tokens", true)
		if err != nil {
			return err
		}
		if ct != output {
			return fmt.Errorf("%w: completion_tokens/output_tokens mismatch", modelconfig.ErrAgenticUsageInvalid)
		}
	}

	if input > math.MaxInt-output {
		return fmt.Errorf("%w: usage overflow", modelconfig.ErrAgenticUsageInvalid)
	}
	if input+output != total {
		return fmt.Errorf("%w: input+output != total", modelconfig.ErrAgenticUsageInvalid)
	}

	// Optional details: when present, validate nested integers.
	if det, ok := fields["input_tokens_details"]; ok {
		cached, err := requireNestedNonNegInt(det, "cached_tokens")
		if err != nil {
			return err
		}
		if cached > input {
			return fmt.Errorf("%w: cached_tokens exceed input_tokens", modelconfig.ErrAgenticUsageInvalid)
		}
	}
	if det, ok := fields["output_tokens_details"]; ok {
		reasoning, err := requireNestedNonNegInt(det, "reasoning_tokens")
		if err != nil {
			return err
		}
		if reasoning > output {
			return fmt.Errorf("%w: reasoning_tokens exceed output_tokens", modelconfig.ErrAgenticUsageInvalid)
		}
	}
	return nil
}

func requireStrictNonNegIntField(fields map[string]json.RawMessage, key string, required bool) (int, error) {
	raw, ok := fields[key]
	if !ok {
		if required {
			return 0, fmt.Errorf("%w: missing %s", modelconfig.ErrAgenticUsageInvalid, key)
		}
		return 0, nil
	}
	return parseStrictNonNegJSONInt(raw, key)
}

func requireNestedNonNegInt(objRaw json.RawMessage, key string) (int, error) {
	objRaw = bytes.TrimSpace(objRaw)
	if len(objRaw) == 0 || bytes.Equal(objRaw, []byte("null")) {
		return 0, fmt.Errorf("%w: %s parent must be object", modelconfig.ErrAgenticUsageInvalid, key)
	}
	if objRaw[0] != '{' {
		return 0, fmt.Errorf("%w: %s parent must be object", modelconfig.ErrAgenticUsageInvalid, key)
	}
	if err := rejectDuplicateJSONKeys(objRaw); err != nil {
		return 0, fmt.Errorf("%w: %v", modelconfig.ErrAgenticUsageInvalid, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(objRaw, &fields); err != nil {
		return 0, fmt.Errorf("%w: %s parent decode", modelconfig.ErrAgenticUsageInvalid, key)
	}
	raw, ok := fields[key]
	if !ok {
		// Absent optional nested field is zero.
		return 0, nil
	}
	return parseStrictNonNegJSONInt(raw, key)
}

// parseStrictNonNegJSONInt accepts only JSON integers (no strings, floats,
// null, bool, objects). Rejects overflow and negatives.
func parseStrictNonNegJSONInt(raw json.RawMessage, key string) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, fmt.Errorf("%w: %s empty", modelconfig.ErrAgenticUsageInvalid, key)
	}
	if bytes.Equal(raw, []byte("null")) {
		return 0, fmt.Errorf("%w: %s must not be null", modelconfig.ErrAgenticUsageInvalid, key)
	}
	switch raw[0] {
	case '"', '{', '[', 't', 'f':
		return 0, fmt.Errorf("%w: %s must be a JSON integer", modelconfig.ErrAgenticUsageInvalid, key)
	}
	// Reject floats / scientific notation.
	s := string(raw)
	if strings.ContainsAny(s, ".eE+") {
		return 0, fmt.Errorf("%w: %s must be a JSON integer", modelconfig.ErrAgenticUsageInvalid, key)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a JSON integer", modelconfig.ErrAgenticUsageInvalid, key)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %s must be nonnegative", modelconfig.ErrAgenticUsageInvalid, key)
	}
	if n > int64(math.MaxInt) {
		return 0, fmt.Errorf("%w: %s overflow", modelconfig.ErrAgenticUsageInvalid, key)
	}
	return int(n), nil
}

// rejectDuplicateJSONKeys scans JSON and rejects duplicate object keys at any level.
func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return rejectDupJSONValue(dec)
}

func rejectDupJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDupJSONValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != '}' {
			return fmt.Errorf("expected end of object")
		}
		return nil
	case '[':
		for dec.More() {
			if err := rejectDupJSONValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != ']' {
			return fmt.Errorf("expected end of array")
		}
		return nil
	default:
		return fmt.Errorf("unexpected delimiter %v", delim)
	}
}
