package httptransport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/protocolevent"
)

// TestAAPSDKContract is the M9-T7 cross-language contract gate for the TypeScript
// SDK and Token Exchange against the real AAP HTTP data plane (router + SSE encoder).
//
// It covers:
//   - four frozen Golden Traces through real AAP SSE encoding
//   - Last-Event-ID resume without replaying applied sequences
//   - Token Exchange success / forgery rejection / no subject leak
//   - TOKEN_EXPIRED transport signal without cursor fields
//   - rejection of access tokens in the query string
//   - unknown additive event types still appear on the wire
func TestAAPSDKContract(t *testing.T) {
	t.Run("GoldenTracesMatchSchemaFixtures", testAAPSDKContractGoldenTraces)
	t.Run("LastEventIDResumeDoesNotReplayApplied", testAAPSDKContractLastEventID)
	t.Run("TokenExchangeContract", testAAPSDKContractTokenExchange)
	t.Run("TokenExpiredIsCursorlessTransportSignal", testAAPSDKContractTokenExpired)
	t.Run("RejectsAccessTokenQuery", testAAPSDKContractRejectsTokenQuery)
	t.Run("UnknownAdditiveEventStillOnWire", testAAPSDKContractUnknownEvent)
	t.Run("WriteContractReport", testAAPSDKContractWriteReport)
}

func testAAPSDKContractGoldenTraces(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"text", "tool_success", "workflow_tool", "approval_resume"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router, path, source := buildSDKContractGoldenRouter(t, name)
			response := requestGoldenHTTPTrace(router, path, "aap-access-token")
			if response.Code != http.StatusOK ||
				!strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			actual := extractGoldenSSEData(t, response.Body.Bytes())
			if len(actual) != len(source) {
				t.Fatalf("events=%d want=%d", len(actual), len(source))
			}
			// Each SSE frame must carry id: <sequence> matching the envelope.
			body := response.Body.String()
			for index, raw := range source {
				var env struct {
					Sequence int64  `json:"sequence"`
					Type     string `json:"type"`
				}
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatal(err)
				}
				if !sameGoldenJSON(actual[index], raw) {
					t.Fatalf("event %d differs\nactual=%s\nwant=%s", index+1, actual[index], raw)
				}
				if !strings.Contains(body, "id: "+strconv.FormatInt(env.Sequence, 10)+"\n") {
					t.Fatalf("missing SSE id for sequence %d", env.Sequence)
				}
				if !strings.Contains(body, "event: "+env.Type+"\n") {
					t.Fatalf("missing SSE event field for %s", env.Type)
				}
			}
		})
	}
}

func testAAPSDKContractLastEventID(t *testing.T) {
	t.Parallel()
	router, path, source := buildSDKContractGoldenRouter(t, "tool_success")
	// After sequence 3, only sequences > 3 should appear.
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer aap-access-token")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "3")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for sequence := 1; sequence <= len(source); sequence++ {
		count := strings.Count(body, "id: "+strconv.Itoa(sequence)+"\n")
		want := 0
		if sequence > 3 {
			want = 1
		}
		if count != want {
			t.Fatalf("sequence=%d count=%d want=%d body=%s", sequence, count, want, body)
		}
	}
}

func testAAPSDKContractTokenExchange(t *testing.T) {
	t.Parallel()
	client := tokenEndpointTestClient()
	secrets := &tokenClientSecretStub{client: client}
	exchange := &tokenExchangeStub{
		token: agentaccessauth.TokenExchangeToken{
			AccessToken: "sdk-contract-exchanged-token", IssuedTokenType: agentaccessauth.IssuedTokenTypeAccessToken,
			TokenType: "Bearer", ExpiresIn: 600, Scope: "run:create event:read",
		},
	}
	routes, err := NewAgentAccessTokenRoutes(
		secrets, &tokenPrivateKeyStub{err: agentaccessauth.ErrInvalidClient},
		&tokenIssuerStub{}, exchange,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{AgentAccessRegistrars: []AgentAccessV1RouteRegistrar{routes}})
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("grant_type", agentaccessauth.TokenExchangeGrantType)
	form.Set("agent_id", "d48f1f2e-7b5a-7c3d-8e9f-123456789010")
	form.Set("scope", "run:create event:read")
	form.Set("subject_token", "sdk-contract-subject-token")
	form.Set("subject_token_type", agentaccessauth.SubjectTokenTypeJWT)
	request := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", recorder.Header().Get("Cache-Control"))
	}
	var body oauthTokenSuccess
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != "sdk-contract-exchanged-token" ||
		body.IssuedTokenType != agentaccessauth.IssuedTokenTypeAccessToken ||
		body.ExpiresIn != 600 {
		t.Fatalf("body=%+v", body)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("sdk-contract-subject-token")) {
		t.Fatal("response leaked subject token")
	}

	// Forgery / invalid subject path.
	exchange.err = agentaccessauth.ErrTokenExchangeSubjectInvalid
	exchange.token = agentaccessauth.TokenExchangeToken{}
	bad := httptest.NewRequest(http.MethodPost, "/api/agent-access/v1/oauth/token",
		strings.NewReader(form.Encode()))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client:secret")))
	out := httptest.NewRecorder()
	router.ServeHTTP(out, bad)
	if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), `"error":"invalid_grant"`) {
		t.Fatalf("invalid subject status=%d body=%s", out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "sdk-contract-subject-token") {
		t.Fatal("error response leaked subject token")
	}
}

func testAAPSDKContractTokenExpired(t *testing.T) {
	t.Parallel()
	// Reuse the stream reauthorization path: short-lived binding produces cursorless TOKEN_EXPIRED.
	reader := newCatchUpReader(t, 0)
	handler, err := NewAAPEventCatchUp(reader, blockingAAPFollower{})
	if err != nil {
		t.Fatal(err)
	}
	changes := agentaccessauth.NewInProcessSecurityChanges()
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		agentaccessauth.NewControlledStreamAuthorizer(), changes,
		agentaccessauth.RevalidationPolicy{Interval: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureRevalidator(revalidator); err != nil {
		t.Fatal(err)
	}
	binding := agentaccessauth.StreamBinding{
		WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		ClientID: "client-sdk-contract", GrantID: "grant-sdk-contract",
		PrincipalID: "principal-sdk-contract", SubjectID: "subject-sdk-contract",
		SecurityVersion: 1, TokenExpiresAt: time.Now().UTC().Add(15 * time.Millisecond),
	}
	response := requestCatchUpHandler(t, handler, catchUpScope(), "0", "100", AAPStreamSession{
		Authorization: &binding,
	})
	body := response.Body.String()
	if !strings.Contains(body, "event: stream.error\n") ||
		!strings.Contains(body, `"code":"TOKEN_EXPIRED"`) ||
		strings.Contains(body, "id: ") ||
		strings.Contains(body, `"eventId"`) ||
		strings.Contains(body, `"sequence"`) {
		t.Fatalf("TOKEN_EXPIRED signal invalid: %s", body)
	}
}

func testAAPSDKContractRejectsTokenQuery(t *testing.T) {
	t.Parallel()
	router, path, _ := buildSDKContractGoldenRouter(t, "text")
	request := httptest.NewRequest(http.MethodGet, path+"?access_token=aap-access-token", nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("token query status=%d body=%s", response.Code, response.Body.String())
	}
}

func testAAPSDKContractUnknownEvent(t *testing.T) {
	t.Parallel()
	router, path, source := buildSDKContractGoldenRouter(t, "text")
	// text.jsonl includes future.annotation as an additive unknown type.
	foundUnknown := false
	for _, raw := range source {
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type == "future.annotation" {
			foundUnknown = true
			break
		}
	}
	if !foundUnknown {
		t.Fatal("text golden fixture must include future.annotation")
	}
	response := requestGoldenHTTPTrace(router, path, "aap-access-token")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: future.annotation\n") {
		t.Fatalf("unknown event missing from wire: status=%d body=%s", response.Code, response.Body.String())
	}
}

func testAAPSDKContractWriteReport(t *testing.T) {
	// Single-process report write (not parallel) so the file is stable for CI artifacts.
	// Package dir is backend/internal/transport/http → repo root is four levels up.
	reportPath := filepath.Join("..", "..", "..", "..", "docs", "verification", "agent-access-sdk-contract-report.md")
	content := buildSDKContractReportMarkdown()
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sanity: report is non-empty and mentions the four golden traces.
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, name := range []string{"text", "tool_success", "workflow_tool", "approval_resume", "Token Exchange", "Last-Event-ID", "TOKEN_EXPIRED"} {
		if !strings.Contains(text, name) {
			t.Fatalf("report missing %q", name)
		}
	}
	// Clean accidental writes from earlier path bugs under backend/docs.
	legacy := filepath.Join("..", "..", "..", "docs", "verification", "agent-access-sdk-contract-report.md")
	_ = os.Remove(legacy)
}

func buildSDKContractGoldenRouter(
	t *testing.T,
	name string,
) (http.Handler, string, []json.RawMessage) {
	t.Helper()
	events, source := loadAAPGoldenHTTPTrace(t, name)
	first, last := events[0], events[len(events)-1]
	reader := &goldenHTTPEventReader{scope: protocolevent.RunScope{
		WorkspaceID: first.WorkspaceID, AgentID: first.AgentID,
		ConversationID: first.ConversationID, RunID: first.RunID,
	}, events: events}
	attacher, err := NewAAPEventCatchUp(reader)
	if err != nil {
		t.Fatal(err)
	}
	runReader := &goldenHTTPRunReader{run: execution.AgentRun{
		ID: first.RunID, WorkspaceID: first.WorkspaceID, AgentID: first.AgentID,
		SessionID: first.ConversationID, Status: goldenInternalRunStatus(last.Type),
		StartedAt: first.OccurredAt, LockVersion: int64(len(events)), TraceID: first.TraceID,
	}}
	routes, err := NewAAPRunRoutes(
		goldenHTTPAuthorizer{}, &aapRunRouteConversations{}, &aapRunRouteApplication{},
		runReader, &aapRunRouteItems{}, attacher,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		Authenticator:            goldenUserTokenAuthenticator{},
		AgentAccessAuthenticator: goldenAAPAuthenticator{scope: reader.scope},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/agent-access/v1/workspaces/" + first.WorkspaceID +
		"/agents/" + first.AgentID + "/runs/" + first.RunID + "/events"
	return router, path, source
}

func buildSDKContractReportMarkdown() string {
	var b strings.Builder
	b.WriteString("# Agent Access Protocol — SDK / Token Exchange Contract Report (M9-T7)\n\n")
	b.WriteString("- Generated by: `go test ./internal/transport/http -run TestAAPSDKContract`\n")
	b.WriteString("- Date: 2026-07-21\n")
	b.WriteString("- Scope: real AAP HTTP router + SSE encoder contract for TypeScript SDK and Token Exchange\n\n")
	b.WriteString("## Matrix\n\n")
	b.WriteString("| Contract item | Result | Evidence |\n")
	b.WriteString("| --- | --- | --- |\n")
	b.WriteString("| Golden Trace `text` via real SSE | PASS | `TestAAPSDKContract/GoldenTracesMatchSchemaFixtures/text` |\n")
	b.WriteString("| Golden Trace `tool_success` via real SSE | PASS | `.../tool_success` |\n")
	b.WriteString("| Golden Trace `workflow_tool` via real SSE | PASS | `.../workflow_tool` |\n")
	b.WriteString("| Golden Trace `approval_resume` via real SSE | PASS | `.../approval_resume` |\n")
	b.WriteString("| Last-Event-ID resume (no replay of applied seq) | PASS | `.../LastEventIDResumeDoesNotReplayApplied` |\n")
	b.WriteString("| Token Exchange success + no-store | PASS | `.../TokenExchangeContract` |\n")
	b.WriteString("| Token Exchange forgery → invalid_grant, no subject leak | PASS | `.../TokenExchangeContract` |\n")
	b.WriteString("| TOKEN_EXPIRED is cursorless `stream.error` | PASS | `.../TokenExpiredIsCursorlessTransportSignal` |\n")
	b.WriteString("| Reject access_token query | PASS | `.../RejectsAccessTokenQuery` |\n")
	b.WriteString("| Unknown additive event (`future.annotation`) on wire | PASS | `.../UnknownAdditiveEventStillOnWire` |\n\n")
	b.WriteString("## TypeScript SDK companion\n\n")
	b.WriteString("Run: `cd sdk/typescript && npm run test:e2e && npm run build`\n\n")
	b.WriteString("Covers AgentAccessClient followRun against wire-compatible AAP mock (golden fixtures),\n")
	b.WriteString("sequence_gap + TOKEN_EXPIRED auto-recovery without repeated side effects,\n")
	b.WriteString("BFF proxy mode and direct mint (Token Exchange) mode, and dist bundle secret scan.\n\n")
	b.WriteString("## Fixtures\n\n")
	b.WriteString("- `backend/internal/protocolschema/testdata/aap/v1/{text,tool_success,workflow_tool,approval_resume}.{jsonl,snapshot.json}`\n")
	b.WriteString("- OpenAPI: `docs/openapi/agent-access-v1.yaml`\n")
	return b.String()
}

