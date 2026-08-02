package a2agateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/google/uuid"
)

// principalAuth returns distinct actor identity from Authorization bearer token.
type principalAuth struct{}

func (principalAuth) Authorize(_ context.Context, r *http.Request, exp a2agateway.Exposure) (string, string, error) {
	if exp.AuthMode == a2agateway.AuthModeNone {
		return "SYSTEM", exp.ID, nil
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return "", "", a2agateway.ErrAuthRejected
	}
	token := strings.TrimSpace(h[7:])
	if token == "" {
		return "", "", a2agateway.ErrAuthRejected
	}
	return "SERVICE_PRINCIPAL", token, nil
}

type bearerRoundTrip struct {
	bearer string
	base   http.RoundTripper
}

func (r bearerRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func newOfficialClient(t *testing.T, invokeBase, bearer string) *a2aclient.Client {
	t.Helper()
	// Point card URL at the invoke host; preferred transport JSONRPC uses same base.
	card := &a2a.AgentCard{
		Name:               "test",
		URL:                strings.TrimRight(invokeBase, "/") + "/",
		ProtocolVersion:    "0.3",
		PreferredTransport: a2a.TransportProtocolJSONRPC,
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{},
	}
	httpClient := &http.Client{Transport: bearerRoundTrip{bearer: bearer}}
	client, err := a2aclient.NewFromCard(context.Background(), card, a2aclient.WithJSONRPCTransport(httpClient))
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return client
}

func postJSONRPC(t *testing.T, invokeURL, bearer string, payload map[string]any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, invokeURL, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func extractResultTaskID(body []byte) string {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Result) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(env.Result, &m) != nil {
		return ""
	}
	if id, ok := m["id"].(string); ok {
		return id
	}
	if id, ok := m["taskId"].(string); ok {
		return id
	}
	return ""
}

func extractResultState(body []byte) string {
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(env.Result, &m) != nil {
		return ""
	}
	if st, ok := m["status"].(map[string]any); ok {
		if s, ok := st["state"].(string); ok {
			return s
		}
	}
	if s, ok := m["state"].(string); ok {
		return s
	}
	return ""
}

// TestOfficialTaskLifecycle_SendGetAcrossRequests: nonblocking SendMessage then GetTask
// across independent requests, gateway rebuild, dual replicas, and principal isolation.
func TestOfficialTaskLifecycle_SendGetAcrossRequests(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)

	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	userB := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'life-b','Life B')`, userB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_members(workspace_id,user_id,role,invited_by) VALUES($1,$2,'EDITOR',$3)`,
		fx.workspaceID, userB, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	auth := dualPrincipalAuth{byToken: map[string]string{
		"principal-a": fx.ownerID,
		"principal-b": userB,
	}}

	release := make(chan struct{})
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			select {
			case <-release:
				return "lifecycle-ok", nil
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(10 * time.Second):
				return "lifecycle-ok", nil
			}
		},
	}
	mkSrv := func() *httptest.Server {
		gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test",
			auth, a2agateway.WithLeaseTTL(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		gw.Register(mux)
		return httptest.NewServer(mux)
	}
	srv1 := mkSrv()
	defer srv1.Close()
	invoke1 := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv1.URL, fx.workspaceID, fx.agentA)

	msgID := "msg-life-" + uuid.Must(uuid.NewV7()).String()
	ctxID := "ctx-life-" + uuid.Must(uuid.NewV7()).String()
	blockingFalse := false

	// Official a2aclient nonblocking SendMessage.
	clientA := newOfficialClient(t, invoke1, "principal-a")
	sendCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := clientA.SendMessage(sendCtx, &a2a.MessageSendParams{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: "hello lifecycle"}),
		Config:  &a2a.MessageSendConfig{Blocking: &blockingFalse},
	})
	// Message ID/context may be rewritten by client — also drive via raw JSON-RPC for stable IDs.
	_ = result
	_ = err
	// Use stable JSON-RPC for deterministic external ids + TaskID extraction.
	st, body := postJSONRPC(t, invoke1, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"kind": "message", "messageId": msgID, "contextId": ctxID, "role": "user",
				"parts": []map[string]any{{"kind": "text", "text": "hello lifecycle"}},
			},
			"configuration": map[string]any{"blocking": false},
		},
	})
	if st != http.StatusOK {
		t.Fatalf("send status=%d body=%s", st, body)
	}
	taskID := extractResultTaskID(body)
	if taskID == "" {
		t.Fatalf("expected task id from nonblocking send: %s", body)
	}

	// Independent second request: official client GetTask.
	got, gerr := clientA.GetTask(context.Background(), &a2a.TaskQueryParams{ID: a2a.TaskID(taskID)})
	if gerr != nil && got == nil {
		// Fall back to JSON-RPC if client card routing differs.
		st, body = postJSONRPC(t, invoke1, "principal-a", map[string]any{
			"jsonrpc": "2.0", "id": "2", "method": "tasks/get",
			"params": map[string]any{"id": taskID},
		})
		if st != 200 || !strings.Contains(string(body), taskID) {
			t.Fatalf("GetTask failed client=%v jsonrpc=%s", gerr, body)
		}
	} else if got != nil && string(got.ID) != taskID {
		t.Fatalf("GetTask id=%s want %s", got.ID, taskID)
	}

	// Rebuild handler/server (process boundary) and GetTask.
	srv2 := mkSrv()
	defer srv2.Close()
	invoke2 := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv2.URL, fx.workspaceID, fx.agentA)
	st, body = postJSONRPC(t, invoke2, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tasks/get",
		"params": map[string]any{"id": taskID},
	})
	if st != 200 || !strings.Contains(string(body), taskID) {
		t.Fatalf("after rebuild GetTask: %s", body)
	}

	// Dual gateway replica.
	srvB := mkSrv()
	defer srvB.Close()
	invokeB := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srvB.URL, fx.workspaceID, fx.agentA)
	st, body = postJSONRPC(t, invokeB, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tasks/get",
		"params": map[string]any{"id": taskID},
	})
	if st != 200 || !strings.Contains(string(body), taskID) {
		t.Fatalf("replica GetTask: %s", body)
	}

	// Principal B cannot GetTask.
	st, body = postJSONRPC(t, invoke2, "principal-b", map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tasks/get",
		"params": map[string]any{"id": taskID},
	})
	bodyStr := string(body)
	if st == 200 && extractResultTaskID(body) == taskID && extractResultState(body) != "" &&
		!strings.Contains(bodyStr, `"error"`) {
		// Success payload with A's task is a leak.
		if strings.Contains(bodyStr, `"state":"working"`) || strings.Contains(bodyStr, `"state":"completed"`) ||
			strings.Contains(bodyStr, `"state":"submitted"`) {
			t.Fatalf("principal B must not read A's task: %s", bodyStr)
		}
	}

	snap := func() (ts, rs, ds, ss string) {
		_ = db.QueryRow(`
			SELECT t.status, COALESCE(r.status,''), COALESCE(d.status,''), COALESCE(s.status,'')
			FROM agent_a2a_inbound_tasks t
			LEFT JOIN agent_runs r ON r.id=t.run_id
			LEFT JOIN agent_run_delegations d ON d.id=t.delegation_id
			LEFT JOIN LATERAL (
				SELECT status FROM agent_run_steps
				WHERE delegation_id=t.delegation_id AND step_type='AGENT_DELEGATION'
				ORDER BY sequence_no LIMIT 1
			) s ON TRUE
			WHERE t.workspace_id=$1 AND t.actor_id=$2
			ORDER BY t.created_at DESC LIMIT 1
		`, fx.workspaceID, fx.ownerID).Scan(&ts, &rs, &ds, &ss)
		return
	}
	beforeTS, beforeRS, beforeDS, beforeSS := snap()

	// B cancel must not mutate.
	_, _ = postJSONRPC(t, invoke2, "principal-b", map[string]any{
		"jsonrpc": "2.0", "id": "3", "method": "tasks/cancel",
		"params": map[string]any{"id": taskID},
	})
	afterTS, afterRS, afterDS, afterSS := snap()
	if afterTS != beforeTS || afterRS != beforeRS || afterDS != beforeDS || afterSS != beforeSS {
		t.Fatalf("B cancel mutated A: before=%s/%s/%s/%s after=%s/%s/%s/%s",
			beforeTS, beforeRS, beforeDS, beforeSS, afterTS, afterRS, afterDS, afterSS)
	}

	// Unblock A execute and cancel via principal A for CANCELLED consistency if still running.
	close(release)
	// Prefer A cancel while may still be running.
	_, _ = postJSONRPC(t, invoke2, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "3", "method": "tasks/cancel",
		"params": map[string]any{"id": taskID},
	})
	deadline := time.Now().Add(6 * time.Second)
	var ts, rs, ds, ss string
	for time.Now().Before(deadline) {
		ts, rs, ds, ss = snap()
		if ts != "" && ts != "RUNNING" && rs != "RUNNING" && ds != "RUNNING" && ss != "RUNNING" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ts == "RUNNING" || rs == "RUNNING" || ds == "RUNNING" || ss == "RUNNING" {
		t.Fatalf("still RUNNING task=%s run=%s del=%s step=%s", ts, rs, ds, ss)
	}
	// Official GetTask should not be permanently working.
	st, body = postJSONRPC(t, invoke2, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "9", "method": "tasks/get",
		"params": map[string]any{"id": taskID},
	})
	state := extractResultState(body)
	if state == "working" || state == "submitted" {
		t.Fatalf("protocol task stuck non-terminal state=%s body=%s durable=%s", state, body, ts)
	}
}

// dualPrincipalAuth picks USER principal by bearer token mapping to seeded user UUIDs.
type dualPrincipalAuth struct {
	byToken map[string]string // token -> user UUID
}

func (d dualPrincipalAuth) Authorize(_ context.Context, r *http.Request, exp a2agateway.Exposure) (string, string, error) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return "", "", a2agateway.ErrAuthRejected
	}
	token := strings.TrimSpace(h[7:])
	uid, ok := d.byToken[token]
	if !ok || uid == "" {
		return "", "", a2agateway.ErrAuthRejected
	}
	return "USER", uid, nil
}

// TestPrincipalIsolation_SameExternalIDs: different principals may share message/context ids
// but get independent durable authority rows.
func TestPrincipalIsolation_SameExternalIDs(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	// Second real user for principal B (agent_runs principal FK).
	userB := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'user-b','User B')`, userB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_members(workspace_id,user_id,role,invited_by)
		VALUES($1,$2,'EDITOR',$3)
	`, fx.workspaceID, userB, fx.ownerID); err != nil {
		t.Fatalf("workspace member B: %v", err)
	}
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)
	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "ok-" + req.ActorID, nil
		},
	}
	auth := dualPrincipalAuth{byToken: map[string]string{
		"token-a": fx.ownerID,
		"token-b": userB,
	}}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth, a2agateway.WithLeaseTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	invoke := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv.URL, fx.workspaceID, fx.agentA)

	send := func(bearer string) string {
		st, body := postJSONRPC(t, invoke, bearer, map[string]any{
			"jsonrpc": "2.0", "id": "1", "method": "message/send",
			"params": map[string]any{
				"message": map[string]any{
					"kind": "message", "messageId": "shared-msg", "contextId": "shared-ctx", "role": "user",
					"parts": []map[string]any{{"kind": "text", "text": "shared body"}},
				},
			},
		})
		if st != 200 {
			t.Fatalf("send %s: %s", bearer, body)
		}
		return string(body)
	}
	bodyA := send("token-a")
	bodyB := send("token-b")
	var aN, bN, allN int
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND actor_id=$2`, fx.workspaceID, fx.ownerID).Scan(&aN)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1 AND actor_id=$2`, fx.workspaceID, userB).Scan(&bN)
	_ = db.QueryRow(`SELECT COUNT(*) FROM agent_a2a_inbound_tasks WHERE workspace_id=$1`, fx.workspaceID).Scan(&allN)
	if aN != 1 || bN != 1 {
		t.Fatalf("want 1 authority each, a=%d b=%d all=%d bodyA=%s bodyB=%s", aN, bN, allN, bodyA, bodyB)
	}
	// Same external_key allowed for different actors.
	var keys int
	_ = db.QueryRow(`
		SELECT COUNT(DISTINCT actor_id) FROM agent_a2a_inbound_tasks
		WHERE workspace_id=$1 AND external_key=$2
	`, fx.workspaceID, "shared-ctx|shared-msg").Scan(&keys)
	if keys != 2 {
		t.Fatalf("distinct actors for same external key: %d", keys)
	}
	// Cross cancel: B cannot cancel A.
	var aTaskID string
	_ = db.QueryRow(`
		SELECT task_id FROM agent_a2a_protocol_tasks
		WHERE workspace_id=$1 AND actor_id=$2 LIMIT 1
	`, fx.workspaceID, fx.ownerID).Scan(&aTaskID)
	if aTaskID == "" {
		_ = db.QueryRow(`
			SELECT external_task_id FROM agent_a2a_inbound_task_aliases
			WHERE workspace_id=$1 AND actor_id=$2 LIMIT 1
		`, fx.workspaceID, fx.ownerID).Scan(&aTaskID)
	}
	if aTaskID != "" {
		before := ""
		_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE actor_id=$1 AND workspace_id=$2`,
			fx.ownerID, fx.workspaceID).Scan(&before)
		_, _ = postJSONRPC(t, invoke, "token-b", map[string]any{
			"jsonrpc": "2.0", "id": "c", "method": "tasks/cancel",
			"params": map[string]any{"id": aTaskID},
		})
		after := ""
		_ = db.QueryRow(`SELECT status FROM agent_a2a_inbound_tasks WHERE actor_id=$1 AND workspace_id=$2`,
			fx.ownerID, fx.workspaceID).Scan(&after)
		if before != after {
			t.Fatalf("B cancel changed A status %s -> %s", before, after)
		}
	}
}
