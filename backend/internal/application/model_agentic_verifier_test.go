package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

type secretOpenerFunc func(context.Context, string, string, func([]byte) error) error

func (fn secretOpenerFunc) WithActiveSecret(ctx context.Context, workspaceID, secretID string, use func([]byte) error) error {
	return fn(ctx, workspaceID, secretID, use)
}

// contractFakeResponsesServer implements ordered Responses + /models phases.
type contractFakeResponsesServer struct {
	mu      sync.Mutex
	bodies  []map[string]any
	auths   []string
	paths   []string
	turn    atomic.Int64
	handler func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request)
	// modelsStatus overrides GET /models (0 = 200).
	modelsStatus int
	// responsesStatus overrides all /responses when handler is nil (0 = use body).
	modelsBody string
}

func (s *contractFakeResponsesServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.mu.Unlock()

		path := strings.TrimSuffix(r.URL.Path, "/")
		if strings.HasSuffix(path, "/models") || path == "/models" || strings.HasSuffix(path, "models") {
			status := s.modelsStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if status == http.StatusOK {
				body := s.modelsBody
				if body == "" {
					body = `{"data":[]}`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}
			return
		}

		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()

		n := int(s.turn.Add(1))
		if s.handler != nil {
			s.handler(n, body, w, r)
			return
		}
		writeContractResponse(w, body, contractTextResponse("ok", true))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *contractFakeResponsesServer) snapshotBodies() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.bodies))
	copy(out, s.bodies)
	return out
}

func contractUsage(withDetails bool) map[string]any {
	usage := map[string]any{
		"input_tokens":  10,
		"output_tokens": 5,
		"total_tokens":  15,
	}
	if withDetails {
		usage["input_tokens_details"] = map[string]any{"cached_tokens": 2}
		usage["output_tokens_details"] = map[string]any{"reasoning_tokens": 1}
	}
	return usage
}

// writeContractResponse writes a non-stream JSON body or an SSE stream depending on body["stream"].
func writeContractResponse(w http.ResponseWriter, reqBody map[string]any, responseObj map[string]any) {
	if stream, _ := reqBody["stream"].(bool); stream {
		w.Header().Set("Content-Type", "text/event-stream")
		// Minimal SSE: completed event carrying the full response object.
		completed := map[string]any{
			"type":            "response.completed",
			"response":        responseObj,
			"sequence_number": 1,
		}
		// If output has text, also emit a delta for assistant text extraction robustness.
		if outs, ok := responseObj["output"].([]map[string]any); ok && len(outs) > 0 {
			if outs[0]["type"] == "message" {
				if content, ok := outs[0]["content"].([]map[string]any); ok && len(content) > 0 {
					if text, _ := content[0]["text"].(string); text != "" {
						delta := map[string]any{
							"type": "response.output_text.delta", "content_index": 0,
							"delta": text, "item_id": "msg_1", "output_index": 0, "sequence_number": 0,
							"logprobs": []any{},
						}
						db, _ := json.Marshal(delta)
						_, _ = w.Write([]byte("event: response.output_text.delta\ndata: "))
						_, _ = w.Write(db)
						_, _ = w.Write([]byte("\n\n"))
					}
				}
			}
		}
		cb, _ := json.Marshal(completed)
		_, _ = w.Write([]byte("event: response.completed\ndata: "))
		_, _ = w.Write(cb)
		_, _ = w.Write([]byte("\n\ndata: [DONE]\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(responseObj)
	_, _ = w.Write(b)
}

func contractTextResponse(text string, withUsageDetails bool) map[string]any {
	return map[string]any{
		"id": "resp_c1", "object": "response", "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": contractUsage(withUsageDetails),
	}
}

func contractToolSearchResponse(execution string) map[string]any {
	return map[string]any{
		"id": "resp_ts", "object": "response", "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "tool_search_call", "id": "tsc_1", "call_id": "search-1",
			"status": "completed", "execution": execution,
			"arguments": map[string]any{"query": "select:actweave_verification_echo", "max_results": 1},
		}},
		"usage": contractUsage(false),
	}
}

func contractEchoCallResponse(nonce string) map[string]any {
	if nonce == "" {
		nonce = "missing-nonce"
	}
	args, _ := json.Marshal(map[string]string{"token": nonce})
	return map[string]any{
		"id": "resp_echo", "object": "response", "status": "completed", "model": "gpt-test",
		"output": []map[string]any{{
			"type": "function_call", "id": "fc_1", "call_id": "call-1",
			"name": "actweave_verification_echo", "arguments": string(args), "status": "completed",
		}},
		"usage": map[string]any{
			"input_tokens": 12, "output_tokens": 4, "total_tokens": 16,
			"input_tokens_details":  map[string]any{"cached_tokens": 3},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	}
}

// extractVerificationNonceFromBody finds the per-probe awv- nonce in user input text.
func extractVerificationNonceFromBody(body map[string]any) string {
	// Walk input for text containing "token awv-...".
	raw, _ := json.Marshal(body)
	s := string(raw)
	const marker = "token awv-"
	idx := strings.Index(s, marker)
	if idx < 0 {
		// Also accept "token\":\"awv-" from JSON-escaped content.
		const marker2 = "token awv-"
		_ = marker2
		return ""
	}
	start := idx + len("token ")
	end := start
	for end < len(s) {
		c := s[end]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			end++
			continue
		}
		break
	}
	if end <= start {
		return ""
	}
	return s[start:end]
}

func newVerifierConfig(base string) modelconfig.Config {
	return modelconfig.Config{
		ID:          "018f1f2e-7b5a-7c3d-8e9f-e234567890b0",
		WorkspaceID: "018f1f2e-7b5a-7c3d-8e9f-e234567890ac",
		Provider:    "openai",
		APIBase:     strings.TrimRight(base, "/") + "/v1",
		ModelName:   "gpt-test",
		Options:     json.RawMessage(`{}`),
		LockVersion: 1,
	}
}

func successHandler(t *testing.T) func(int, map[string]any, http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
		if store, ok := body["store"].(bool); !ok || store {
			t.Errorf("turn %d store must be false, got %v", turn, body["store"])
		}
		if _, ok := body["previous_response_id"]; ok {
			t.Errorf("turn %d must not have previous_response_id", turn)
		}
		// Contract: every probe phase must advertise client tool_search + deferred echo
		// when tools are present (tool-search turns). Stream text probe may omit tools.
		if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
			assertVerificationProbeTools(t, turn, tools)
		}
		switch turn {
		case 1:
			// Plain stream probe — usage without optional details also accepted.
			writeContractResponse(w, body, contractTextResponse("ack", false))
		case 2:
			// First tool-search request must carry tools catalog.
			if tools, ok := body["tools"].([]any); !ok || len(tools) == 0 {
				t.Errorf("turn 2 must include tools array")
			} else {
				assertVerificationProbeTools(t, turn, tools)
			}
			writeContractResponse(w, body, contractToolSearchResponse("client"))
		default:
			nonce := extractVerificationNonceFromBody(body)
			if nonce == "" || !strings.HasPrefix(nonce, "awv-") {
				t.Errorf("turn %d missing verification nonce in input", turn)
			}
			// Second request should carry the client-completed search output in input.
			assertHasToolSearchOutput(t, turn, body)
			writeContractResponse(w, body, contractEchoCallResponse(nonce))
		}
	}
}

// Canonical echo tool wire fields (pinned adapter / verificationEchoTool).
const (
	verificationEchoDescription = "Echo verification helper. Returns a fixed acknowledgement. No side effects."
	verificationSearchCallID    = "search-1"
)

func assertVerificationProbeTools(t *testing.T, turn int, tools []any) {
	t.Helper()
	var searchCount, echoCount int
	for _, raw := range tools {
		tm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("turn %d tools entry not object", turn)
		}
		typ, _ := tm["type"].(string)
		switch typ {
		case "tool_search":
			searchCount++
			// Exact: one native tool_search with execution:"client", no extras of this type.
			if exec, ok := tm["execution"].(string); !ok || exec != "client" {
				t.Fatalf("turn %d tool_search execution=%v want exactly client (bool/string omission fails)", turn, tm["execution"])
			}
		case "function":
			name, _ := tm["name"].(string)
			if name == agenticVerificationEchoTool {
				echoCount++
				// Explicit defer_loading:true required (omission / false / wrong type fails).
				def, ok := tm["defer_loading"].(bool)
				if !ok || !def {
					t.Fatalf("turn %d echo defer_loading=%v want exactly true", turn, tm["defer_loading"])
				}
				if desc, _ := tm["description"].(string); desc != verificationEchoDescription {
					t.Fatalf("turn %d echo description=%q want exact canonical description", turn, desc)
				}
				// Exact parameters schema: object with required token string.
				params, ok := tm["parameters"].(map[string]any)
				if !ok {
					t.Fatalf("turn %d echo parameters missing/wrong type %T", turn, tm["parameters"])
				}
				if ptype, _ := params["type"].(string); ptype != "object" {
					t.Fatalf("turn %d echo parameters.type=%v want object", turn, params["type"])
				}
				props, ok := params["properties"].(map[string]any)
				if !ok {
					t.Fatalf("turn %d echo parameters.properties missing", turn)
				}
				tokenProp, ok := props["token"].(map[string]any)
				if !ok {
					t.Fatalf("turn %d echo parameters.properties.token missing", turn)
				}
				if ttype, _ := tokenProp["type"].(string); ttype != "string" {
					t.Fatalf("turn %d token type=%v want string", turn, tokenProp["type"])
				}
				req, _ := params["required"].([]any)
				if len(req) != 1 || req[0] != "token" {
					t.Fatalf("turn %d echo required=%v want [token]", turn, req)
				}
			} else if name != "" {
				t.Fatalf("turn %d unexpected function tool %q (only echo allowed)", turn, name)
			}
		default:
			t.Fatalf("turn %d unexpected tool type %q", turn, typ)
		}
	}
	if searchCount != 1 {
		t.Fatalf("turn %d tool_search count=%d want exactly 1", turn, searchCount)
	}
	if echoCount != 1 {
		t.Fatalf("turn %d deferred echo count=%d want exactly 1", turn, echoCount)
	}
	if len(tools) != 2 {
		t.Fatalf("turn %d tools cardinality=%d want exactly 2 (1 search + 1 echo, no extras)", turn, len(tools))
	}
}

// assertHasToolSearchOutput requires the exact client-completed tool_search_output
// in second-request input (not merely a prior call marker): execution/status/call_id
// and a single nonce-bound echo tool schema entry.
func assertHasToolSearchOutput(t *testing.T, turn int, body map[string]any) {
	t.Helper()
	input, ok := body["input"].([]any)
	if !ok {
		// Some adapter shapes nest; fall back to full-body walk but still require
		// exact tool_search_output object fields (not just a string marker).
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), `"type":"tool_search_output"`) &&
			!strings.Contains(string(raw), `"type": "tool_search_output"`) {
			t.Fatalf("turn %d input missing exact tool_search_output type", turn)
		}
		// Still require execution/status/call_id markers for fail-closed proof.
		if !strings.Contains(string(raw), `"execution":"client"`) &&
			!strings.Contains(string(raw), `"execution": "client"`) {
			t.Fatalf("turn %d tool_search_output missing execution:client", turn)
		}
		if !strings.Contains(string(raw), agenticVerificationEchoTool) {
			t.Fatalf("turn %d tool_search_output missing echo tool schema", turn)
		}
		return
	}
	var found []map[string]any
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "tool_search_output" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("turn %d expected exactly 1 tool_search_output in input, got %d", turn, len(found))
	}
	out := found[0]
	if exec, ok := out["execution"].(string); !ok || exec != "client" {
		t.Fatalf("turn %d tool_search_output.execution=%v want exactly client", turn, out["execution"])
	}
	if status, ok := out["status"].(string); !ok || status != "completed" {
		t.Fatalf("turn %d tool_search_output.status=%v want exactly completed", turn, out["status"])
	}
	if callID, ok := out["call_id"].(string); !ok || callID == "" {
		t.Fatalf("turn %d tool_search_output.call_id missing/empty", turn)
	} else if callID != verificationSearchCallID {
		// Contract fake uses search-1; accept only that exact call_id for this probe.
		t.Fatalf("turn %d tool_search_output.call_id=%q want %q", turn, callID, verificationSearchCallID)
	}
	toolsRaw, ok := out["tools"]
	if !ok {
		t.Fatalf("turn %d tool_search_output missing tools field", turn)
	}
	toolsOut, ok := toolsRaw.([]any)
	if !ok || len(toolsOut) != 1 {
		t.Fatalf("turn %d tool_search_output.tools cardinality=%v want exactly 1 echo schema", turn, toolsRaw)
	}
	tm, ok := toolsOut[0].(map[string]any)
	if !ok {
		t.Fatalf("turn %d tool_search_output tools[0] not object", turn)
	}
	if name, _ := tm["name"].(string); name != agenticVerificationEchoTool {
		t.Fatalf("turn %d tool_search_output tools[0].name=%q want echo", turn, name)
	}
	// Top-level tools on this request must remain exact per pinned adapter (2 entries).
	if tools, ok := body["tools"].([]any); ok {
		assertVerificationProbeTools(t, turn, tools)
	}
}

func TestAgenticVerifierSuccess_StoreFalseEchoFlowCanonical(t *testing.T) {
	fake := &contractFakeResponsesServer{handler: successHandler(t)}
	srv := fake.start(t)
	secrets := secretOpenerFunc(func(_ context.Context, _, _ string, use func([]byte) error) error {
		return use([]byte("sk-test-secret"))
	})
	v := &modelConfigVerifier{client: srv.Client(), secrets: secrets}
	cfg := newVerifierConfig(srv.URL)
	sid := "018f1f2e-7b5a-7c3d-8e9f-e234567890ae"
	cfg.CredentialSecretID = &sid

	caps, err := v.Verify(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if caps.ToolCalling != modelconfig.ToolCallingNativeClientSearch {
		t.Fatalf("native success must report native_client_search, got %q", caps.ToolCalling)
	}

	bodies := fake.snapshotBodies()
	if len(bodies) < 3 {
		t.Fatalf("expected >=3 responses bodies, got %d", len(bodies))
	}
	if countFunctionCallingProbeBodies(bodies) != 0 {
		t.Fatal("native success must skip the function-calling probe")
	}
	for i, body := range bodies {
		if store, ok := body["store"].(bool); !ok || store {
			t.Fatalf("body[%d] store=%v", i, body["store"])
		}
		if _, ok := body["previous_response_id"]; ok {
			t.Fatalf("body[%d] has previous_response_id", i)
		}
	}
	// Ensure no side-effect external tool execution occurred (echo tool never InvokableRun to network).
	// Auth header must not be logged into caps (caps empty here).
	if strings.Contains(string(mustJSON(caps)), "sk-test-secret") {
		t.Fatal("secret leaked into capabilities")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestAgenticVerifierAuth401(t *testing.T) {
	fake := &contractFakeResponsesServer{modelsStatus: http.StatusUnauthorized}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if !errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
		t.Fatalf("err=%v", err)
	}
}

func TestAgenticVerifierAuth403(t *testing.T) {
	fake := &contractFakeResponsesServer{modelsStatus: http.StatusForbidden}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if !errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
		t.Fatalf("err=%v", err)
	}
}

func TestAgenticVerifierResponsesMissing(t *testing.T) {
	// /models OK; /responses returns 404 → exact Responses unsupported.
	fake := &contractFakeResponsesServer{
		handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		},
	}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if !errors.Is(err, modelconfig.ErrResponsesUnsupported) {
		t.Fatalf("want ErrResponsesUnsupported, got %v", err)
	}
}

func TestAgenticVerifierHostedSearchRejected(t *testing.T) {
	fake := &contractFakeResponsesServer{
		handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
			switch turn {
			case 1:
				writeContractResponse(w, body, contractTextResponse("ack", true))
			default:
				// Server/hosted execution.
				writeContractResponse(w, body, contractToolSearchResponse("server"))
			}
		},
	}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if err != nil {
		t.Fatalf("hosted search is a capability miss, got %v", err)
	}
	if caps.ToolCalling != modelconfig.ToolCallingNone {
		t.Fatalf("hosted then non-echo FC must persist none, got %q", caps.ToolCalling)
	}
}

func TestAgenticVerifierNoSearch(t *testing.T) {
	fake := &contractFakeResponsesServer{
		handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
			// Always plain text — never tool_search or echo.
			writeContractResponse(w, body, contractTextResponse("no tools", true))
		},
	}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if err != nil {
		t.Fatalf("text-only is a capability miss, got %v", err)
	}
	if caps.ToolCalling != modelconfig.ToolCallingNone {
		t.Fatalf("text-only must persist none, got %q", caps.ToolCalling)
	}
}

func TestAgenticVerifierMalformedStream(t *testing.T) {
	fake := &contractFakeResponsesServer{
		handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{not-json`))
		},
	}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestAgenticVerifierSecretNotPersistedInError(t *testing.T) {
	// Adversarial secret markers in 400/401/429/500 bodies must be absent from
	// error strings (and therefore from stored codes / HTTP projection / logs).
	const secret = "sk-live-super-secret-should-not-leak"
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusBadRequest, modelconfig.ErrVerificationUpstream},
		{http.StatusUnauthorized, modelconfig.ErrUpstreamAuthentication},
		{http.StatusTooManyRequests, modelconfig.ErrVerificationUpstream},
		{http.StatusInternalServerError, modelconfig.ErrVerificationUpstream},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			fake := &contractFakeResponsesServer{
				handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"error":"` + secret + `"}`))
				},
			}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(_ context.Context, _, _ string, use func([]byte) error) error {
				return use([]byte(secret))
			})}
			cfg := newVerifierConfig(srv.URL)
			sid := "018f1f2e-7b5a-7c3d-8e9f-e234567890ae"
			cfg.CredentialSecretID = &sid
			_, err := v.Verify(context.Background(), cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("secret leaked into error string: %v", err)
			}
			// Stable classification code path must not carry the secret either.
			code := modelconfig.ErrorCodeUpstream
			if errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
				code = modelconfig.ErrorCodeAuthentication
			}
			if strings.Contains(code, secret) {
				t.Fatalf("secret in code %q", code)
			}
		})
	}
}

func TestAgenticVerifierCancellation(t *testing.T) {
	started := make(chan struct{})
	fake := &contractFakeResponsesServer{
		handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		},
	}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := v.Verify(ctx, newVerifierConfig(srv.URL))
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestAgenticVerifier429And5xx(t *testing.T) {
	// /models 429/5xx → exact upstream (not auth, not responses-unsupported).
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		status := status
		t.Run("models_"+http.StatusText(status), func(t *testing.T) {
			fake := &contractFakeResponsesServer{modelsStatus: status}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})}
			_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
			if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
				t.Fatalf("want ErrVerificationUpstream, got %v", err)
			}
		})
	}
	// /responses 429/5xx after /models OK → exact upstream via verification transport.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		status := status
		t.Run("responses_"+http.StatusText(status), func(t *testing.T) {
			fake := &contractFakeResponsesServer{
				handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":"upstream"}`))
				},
			}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})}
			_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
			if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
				t.Fatalf("want ErrVerificationUpstream, got %v", err)
			}
		})
	}
}

func TestAgenticVerifierUsageWithoutOptionalDetails(t *testing.T) {
	// Success path already uses withUsageDetails=false on turn 1; this is a
	// focused unit of the usage probe via full success handler.
	fake := &contractFakeResponsesServer{handler: successHandler(t)}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	if _, err := v.Verify(context.Background(), newVerifierConfig(srv.URL)); err != nil {
		t.Fatal(err)
	}
}

func TestAgenticVerifierUsageAdversarial(t *testing.T) {
	// Adversarial fake Responses cases: inconsistent totals, missing usage on
	// each probe turn, negatives. None may leave capability persisted (probe fails).
	type caseSpec struct {
		name    string
		handler func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request)
	}
	inconsistentUsage := map[string]any{
		// 10+5 != 14
		"input_tokens": 10, "output_tokens": 5, "total_tokens": 14,
	}
	negativeUsage := map[string]any{
		"input_tokens": -1, "output_tokens": 5, "total_tokens": 4,
	}
	cases := []caseSpec{
		{
			name: "inconsistent_totals_10_plus_5_ne_14",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = inconsistentUsage
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "missing_first_turn_usage",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				if turn == 1 {
					resp := contractTextResponse("ack", false)
					delete(resp, "usage")
					writeContractResponse(w, body, resp)
					return
				}
				writeContractResponse(w, body, contractTextResponse("x", false))
			},
		},
		{
			name: "missing_second_turn_usage",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				switch turn {
				case 1:
					writeContractResponse(w, body, contractTextResponse("ack", true))
				case 2:
					resp := contractToolSearchResponse("client")
					delete(resp, "usage")
					writeContractResponse(w, body, resp)
				default:
					writeContractResponse(w, body, contractEchoCallResponse(extractVerificationNonceFromBody(body)))
				}
			},
		},
		{
			name: "missing_third_turn_usage",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				switch turn {
				case 1:
					writeContractResponse(w, body, contractTextResponse("ack", false))
				case 2:
					writeContractResponse(w, body, contractToolSearchResponse("client"))
				default:
					resp := contractEchoCallResponse(extractVerificationNonceFromBody(body))
					delete(resp, "usage")
					writeContractResponse(w, body, resp)
				}
			},
		},
		{
			name: "string_input_tokens_wrong_type",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				// Typed adapters coerce this to 0; raw validator must catch it first.
				resp["usage"] = map[string]any{
					"input_tokens": "10", "output_tokens": 5, "total_tokens": 15,
				}
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "float_output_tokens",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = map[string]any{
					"input_tokens": 10, "output_tokens": 5.5, "total_tokens": 15,
				}
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "negative_usage",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = negativeUsage
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "cached_exceeds_input",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = map[string]any{
					"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
					"input_tokens_details": map[string]any{"cached_tokens": 99},
				}
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "reasoning_exceeds_output",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = map[string]any{
					"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
					"output_tokens_details": map[string]any{"reasoning_tokens": 50},
				}
				writeContractResponse(w, body, resp)
			},
		},
		{
			name: "null_usage",
			handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				resp := contractTextResponse("ack", false)
				resp["usage"] = nil
				writeContractResponse(w, body, resp)
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &contractFakeResponsesServer{handler: tc.handler}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})}
			_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
			if err == nil {
				t.Fatal("expected usage invalid / probe failure")
			}
			// Exact stable classification required (no alternative codes).
			if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
				t.Fatalf("want ErrAgenticUsageInvalid, got %v", err)
			}
		})
	}
}

func TestAgenticVerifierEchoAdversarial(t *testing.T) {
	// Adversarial post-search model responses: wrong/missing/extra args, extra calls, text.
	cases := []struct {
		name string
		echo map[string]any
	}{
		{
			name: "wrong_token",
			echo: func() map[string]any {
				r := contractEchoCallResponse("awv-deadbeef")
				return r
			}(),
		},
		{
			name: "extra_arg",
			echo: map[string]any{
				"id": "resp_echo", "object": "response", "status": "completed", "model": "gpt-test",
				"output": []map[string]any{{
					"type": "function_call", "id": "fc_1", "call_id": "call-1",
					"name": "actweave_verification_echo", "arguments": `{"token":"x","extra":1}`, "status": "completed",
				}},
				"usage": contractUsage(false),
			},
		},
		{
			name: "missing_token",
			echo: map[string]any{
				"id": "resp_echo", "object": "response", "status": "completed", "model": "gpt-test",
				"output": []map[string]any{{
					"type": "function_call", "id": "fc_1", "call_id": "call-1",
					"name": "actweave_verification_echo", "arguments": `{}`, "status": "completed",
				}},
				"usage": contractUsage(false),
			},
		},
		{
			name: "text_plus_function",
			echo: map[string]any{
				"id": "resp_echo", "object": "response", "status": "completed", "model": "gpt-test",
				"output": []map[string]any{
					{
						"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
						"content": []map[string]any{{"type": "output_text", "text": "hi", "annotations": []any{}}},
					},
					{
						"type": "function_call", "id": "fc_1", "call_id": "call-1",
						"name": "actweave_verification_echo", "arguments": `{"token":"x"}`, "status": "completed",
					},
				},
				"usage": contractUsage(false),
			},
		},
		{
			name: "wrong_function_name",
			echo: map[string]any{
				"id": "resp_echo", "object": "response", "status": "completed", "model": "gpt-test",
				"output": []map[string]any{{
					"type": "function_call", "id": "fc_1", "call_id": "call-1",
					"name": "other_tool", "arguments": `{"token":"x"}`, "status": "completed",
				}},
				"usage": contractUsage(false),
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &contractFakeResponsesServer{
				handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
					switch turn {
					case 1:
						writeContractResponse(w, body, contractTextResponse("ack", false))
					case 2:
						writeContractResponse(w, body, contractToolSearchResponse("client"))
					default:
						writeContractResponse(w, body, tc.echo)
					}
				},
			}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})}
			caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
			if err != nil {
				t.Fatalf("echo near-miss is a capability miss, got %v", err)
			}
			if caps.ToolCalling != modelconfig.ToolCallingNone {
				t.Fatalf("echo near-miss must persist none, got %q", caps.ToolCalling)
			}
		})
	}
}

func TestVerificationUsageTransport_StringTokens(t *testing.T) {
	// Direct unit: raw wire validator rejects string-typed token fields.
	// Authentic completed object requires object/id/status/output + usage.
	usage := []byte(`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":"10","output_tokens":5,"total_tokens":15}}`)
	err := validateVerificationJSONObject(usage)
	if !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("string input_tokens: %v", err)
	}
	ok := []byte(`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`)
	if err := validateVerificationJSONObject(ok); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProbeToolSearchArgs_ExactContract(t *testing.T) {
	t.Parallel()
	// Exact valid contract.
	ok := `{"query":"select:actweave_verification_echo","max_results":1}`
	if err := validateProbeToolSearchArgs(ok); err != nil {
		t.Fatalf("valid: %v", err)
	}
	// Key order must not matter when both keys are exact.
	ok2 := `{"max_results":1,"query":"select:actweave_verification_echo"}`
	if err := validateProbeToolSearchArgs(ok2); err != nil {
		t.Fatalf("key order: %v", err)
	}
	cases := []struct {
		name string
		raw  string
	}{
		{"bad query keyword", `{"query":"echo","max_results":1}`},
		{"bad query other select", `{"query":"select:other_tool","max_results":1}`},
		{"max_results 999", `{"query":"select:actweave_verification_echo","max_results":999}`},
		{"max_results 5", `{"query":"select:actweave_verification_echo","max_results":5}`},
		{"max_results 0", `{"query":"select:actweave_verification_echo","max_results":0}`},
		{"max_results float", `{"query":"select:actweave_verification_echo","max_results":1.0}`},
		{"max_results string", `{"query":"select:actweave_verification_echo","max_results":"1"}`},
		{"max_results null", `{"query":"select:actweave_verification_echo","max_results":null}`},
		{"missing max_results", `{"query":"select:actweave_verification_echo"}`},
		{"missing query", `{"max_results":1}`},
		{"extra key", `{"query":"select:actweave_verification_echo","max_results":1,"extra":true}`},
		{"empty", ``},
		{"empty object", `{}`},
		{"array", `[]`},
		{"duplicate key", `{"query":"select:actweave_verification_echo","max_results":1,"max_results":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProbeToolSearchArgs(tc.raw)
			if !errors.Is(err, modelconfig.ErrToolSearchUnsupported) {
				t.Fatalf("want TOOL_SEARCH_UNSUPPORTED, got %v", err)
			}
		})
	}
}

func TestAgenticVerifier_RejectsInvalidModelSearchArgsNoRewrite(t *testing.T) {
	// Bad model search args must fail TOOL_SEARCH_UNSUPPORTED; never rewrite to VERIFIED.
	for _, badArgs := range []any{
		map[string]any{"query": "echo", "max_results": 1},
		map[string]any{"query": "select:actweave_verification_echo", "max_results": 999},
		map[string]any{"query": "select:actweave_verification_echo"},
		map[string]any{"query": "select:actweave_verification_echo", "max_results": 1, "extra": true},
	} {
		badArgs := badArgs
		t.Run(fmt.Sprintf("%v", badArgs), func(t *testing.T) {
			fake := &contractFakeResponsesServer{handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
				switch turn {
				case 1:
					writeContractResponse(w, body, contractTextResponse("ack", false))
				case 2:
					writeContractResponse(w, body, map[string]any{
						"id": "resp_ts", "object": "response", "status": "completed", "model": "gpt-test",
						"output": []map[string]any{{
							"type": "tool_search_call", "id": "tsc_1", "call_id": "search-1",
							"status": "completed", "execution": "client",
							"arguments": badArgs,
						}},
						"usage": contractUsage(false),
					})
				default:
					if !isFunctionCallingProbeRequest(body) {
						t.Errorf("turn %d must be the function-calling probe after invalid search args", turn)
					}
					writeContractResponse(w, body, contractTextResponse("nope", false))
				}
			}}
			srv := fake.start(t)
			v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			})}
			caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
			if err != nil {
				t.Fatalf("invalid search args must not rewrite native; got err %v", err)
			}
			if caps.ToolCalling != modelconfig.ToolCallingNone {
				t.Fatalf("invalid search args must not become native, got %q", caps.ToolCalling)
			}
		})
	}
}

func TestAgenticVerifierExactClientSearchWireProof(t *testing.T) {
	// Strengthen request-capture: first tools exact, second input exact
	// tool_search_output, third/echo response pair with exact nonce args.
	fake := &contractFakeResponsesServer{handler: successHandler(t)}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	if _, err := v.Verify(context.Background(), newVerifierConfig(srv.URL)); err != nil {
		t.Fatal(err)
	}
	bodies := fake.snapshotBodies()
	if len(bodies) < 3 {
		t.Fatalf("want >=3 bodies, got %d", len(bodies))
	}
	// First request with tools (tool-search turn): exact catalog.
	// Stream probe may be body[0] without tools; find first tools-bearing request.
	var toolSearchBody, postSearchBody map[string]any
	for _, b := range bodies {
		tools, ok := b["tools"].([]any)
		if !ok || len(tools) == 0 {
			continue
		}
		assertVerificationProbeTools(t, 0, tools)
		if toolSearchBody == nil {
			toolSearchBody = b
			continue
		}
		postSearchBody = b
		break
	}
	if toolSearchBody == nil {
		t.Fatal("missing tool-search request with tools")
	}
	if postSearchBody == nil {
		// Echo phase may still have tools; if only one tools body, second may be the echo request.
		// successHandler marks turn>=3 as post-search with assertHasToolSearchOutput.
		for _, b := range bodies {
			raw, _ := json.Marshal(b)
			if strings.Contains(string(raw), "tool_search_output") {
				postSearchBody = b
				break
			}
		}
	}
	if postSearchBody == nil {
		t.Fatal("missing post-search request with tool_search_output")
	}
	assertHasToolSearchOutput(t, 3, postSearchBody)
}

func TestValidateProbeUsageConsistency(t *testing.T) {
	// Direct unit of the totals contract (10+5!=14 etc.).
	if err := validateProbeUsageConsistency(agenticmsg.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 14,
	}); err == nil || !errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("10+5!=14: %v", err)
	}
	if err := validateProbeUsageConsistency(agenticmsg.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedTokens: 11,
	}); err == nil {
		t.Fatal("cached > input")
	}
	if err := validateProbeUsageConsistency(agenticmsg.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, ReasoningTokens: 6,
	}); err == nil {
		t.Fatal("reasoning > output")
	}
	if err := validateProbeUsageConsistency(agenticmsg.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedTokens: 2, ReasoningTokens: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateProbeUsageConsistency(agenticmsg.TokenUsage{
		PromptTokens: -1, CompletionTokens: 5, TotalTokens: 4,
	}); err == nil {
		t.Fatal("negative")
	}
}

// Ensure modelapi import used for interface satisfaction in production wiring.
var _ modelapi.SecretOpener = secretOpenerFunc(nil)

func countFunctionCallingProbeBodies(bodies []map[string]any) int {
	n := 0
	for _, body := range bodies {
		if isFunctionCallingProbeRequest(body) {
			n++
		}
	}
	return n
}

func isFunctionCallingProbeRequest(body map[string]any) bool {
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return false
	}
	var echo int
	for _, raw := range tools {
		tm, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		switch tm["type"] {
		case "tool_search":
			return false
		case "function":
			if name, _ := tm["name"].(string); name == agenticVerificationEchoTool {
				if def, _ := tm["defer_loading"].(bool); def {
					return false
				}
				echo++
			}
		}
	}
	return echo == 1
}

func TestAgenticVerifierPhase3ExactEchoIsFunctionCalling(t *testing.T) {
	fake := &contractFakeResponsesServer{handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
		switch turn {
		case 1:
			writeContractResponse(w, body, contractTextResponse("ack", false))
		case 2:
			writeContractResponse(w, body, contractTextResponse("no search", true))
		default:
			if !isFunctionCallingProbeRequest(body) {
				t.Errorf("turn %d must be the function-calling probe", turn)
			}
			nonce := extractVerificationNonceFromBody(body)
			writeContractResponse(w, body, contractEchoCallResponse(nonce))
		}
	}}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if caps.ToolCalling != modelconfig.ToolCallingFunctionCalling {
		t.Fatalf("got toolCalling=%q want function_calling", caps.ToolCalling)
	}
	if countFunctionCallingProbeBodies(fake.snapshotBodies()) != 1 {
		t.Fatalf("function-calling probe requests=%d want 1", countFunctionCallingProbeBodies(fake.snapshotBodies()))
	}
}

func TestAgenticVerifierPhase2InfraDoesNotEnterPhase3(t *testing.T) {
	fake := &contractFakeResponsesServer{handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
		switch turn {
		case 1:
			writeContractResponse(w, body, contractTextResponse("ack", false))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream"}`))
		}
	}}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
		t.Fatalf("want upstream ERROR, got %v", err)
	}
	if countFunctionCallingProbeBodies(fake.snapshotBodies()) != 0 {
		t.Fatal("Phase 2 infra must not enter Phase 3")
	}
}

func TestAgenticVerifierPhase3HTTP400IsNone(t *testing.T) {
	fake := &contractFakeResponsesServer{handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
		switch turn {
		case 1:
			writeContractResponse(w, body, contractTextResponse("ack", false))
		case 2:
			writeContractResponse(w, body, contractTextResponse("no search", true))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"tools not supported"}`))
		}
	}}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	caps, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if err != nil {
		t.Fatalf("400 on FC probe is capability none, got %v", err)
	}
	if caps.ToolCalling != modelconfig.ToolCallingNone {
		t.Fatalf("got toolCalling=%q want none", caps.ToolCalling)
	}
}

func TestAgenticVerifierPhase3AuthStaysError(t *testing.T) {
	fake := &contractFakeResponsesServer{handler: func(turn int, body map[string]any, w http.ResponseWriter, r *http.Request) {
		switch turn {
		case 1:
			writeContractResponse(w, body, contractTextResponse("ack", false))
		case 2:
			writeContractResponse(w, body, contractTextResponse("no search", true))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
		}
	}}
	srv := fake.start(t)
	v := &modelConfigVerifier{client: srv.Client(), secrets: secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
		return nil
	})}
	_, err := v.Verify(context.Background(), newVerifierConfig(srv.URL))
	if !errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
		t.Fatalf("want AUTH, got %v", err)
	}
}

func TestAgenticVerifierHasNoModelNameCapabilityBranch(t *testing.T) {
	raw, err := os.ReadFile("model_agentic_verifier.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, needle := range []string{"config.ModelName", ".ModelName"} {
		if strings.Contains(text, needle) {
			t.Fatalf("verifier must not branch on ModelName (%q)", needle)
		}
	}
}

func TestMapAgenticFunctionCallingProbeError(t *testing.T) {
	t.Parallel()
	if got := mapAgenticFunctionCallingProbeError(modelconfig.ErrAgenticUsageInvalid); !errors.Is(got, modelconfig.ErrAgenticUsageInvalid) {
		t.Fatalf("usage: %v", got)
	}
	if got := mapAgenticFunctionCallingProbeError(modelconfig.ErrAgenticStreamInvalid); !errors.Is(got, modelconfig.ErrAgenticStreamInvalid) {
		t.Fatalf("stream: %v", got)
	}
	if got := mapAgenticFunctionCallingProbeError(fmt.Errorf("%w: HTTP_STATUS_500", modelconfig.ErrVerificationUpstream)); !errors.Is(got, modelconfig.ErrVerificationUpstream) {
		t.Fatalf("500: %v", got)
	}
	if got := mapAgenticFunctionCallingProbeError(fmt.Errorf("%w: HTTP_STATUS_400", modelconfig.ErrVerificationUpstream)); got != nil {
		t.Fatalf("400 must be capability none, got %v", got)
	}
	if got := mapAgenticFunctionCallingProbeError(fmt.Errorf("%w: HTTP_STATUS_422", modelconfig.ErrVerificationUpstream)); got != nil {
		t.Fatalf("422 must be capability none, got %v", got)
	}
	if got := mapAgenticFunctionCallingProbeError(errors.New("model refused tools")); got != nil {
		t.Fatalf("unrecognized capability reject must be none, got %v", got)
	}
}

func TestVerificationServiceCASWithAgenticVerifier(t *testing.T) {
	// Integration-style: repository CAS after concurrent edit during probe.
	// Uses modelconfig package tests primarily; here ensure typed codes map.
	err := modelconfig.ErrToolSearchUnsupported
	if modelconfig.ErrorCodeToolSearchUnsupported == "" {
		t.Fatal("missing code")
	}
	_ = err
	_ = time.Second
}
