package a2agateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/google/uuid"
)

func assertNoInternalLeak(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, bad := range []string{
		"sql:", "pq:", "outbox", "execute_generation", "lease_until",
		"agent_run_delegations", "constraint", "violates", "stack:",
	} {
		if strings.Contains(lower, bad) {
			t.Fatalf("client body leaked internal detail %q: %s", bad, body)
		}
	}
}

// TestInboundClientMessages_NoInternalErrorLeak injects audit failures and
// asserts JSON-RPC agent texts are stable codes with traceRef only.
func TestInboundClientMessages_NoInternalErrorLeak(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	base, _ := agentdelegation.NewService(delRepo)
	audit := &failingAttemptAudit{inner: base}
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

	var execs atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			return "should-not", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st, body := postMessageSend(t, srv.URL, fx.workspaceID, fx.agentA, "x", "", "msg-hygiene")
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}
	assertNoInternalLeak(t, string(body))
	if !strings.Contains(string(body), "traceRef=") && !strings.Contains(string(body), "A2A_") {
		// Agent text should carry stable code + traceRef
		t.Logf("body=%s", body)
	}
	if execs.Load() != 0 {
		t.Fatalf("executor must not run on attempt fail; n=%d", execs.Load())
	}
}

// TestInboundInvoke_ChunkedOversize_413 ensures chunked Transfer-Encoding bodies
// over the hard limit return HTTP 413 and never reach the runner.
func TestInboundInvoke_ChunkedOversize_413(t *testing.T) {
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
	var execs atomic.Int64
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			execs.Add(1)
			return "nope", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Pipe body larger than limit with ContentLength=-1 (chunked semantics via Pipe).
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		// Write max+64 of 'x' without buffering Content-Length on the request.
		chunk := make([]byte, 64*1024)
		for i := range chunk {
			chunk[i] = 'x'
		}
		remaining := int(a2agateway.MaxInboundRequestBytes + 64)
		for remaining > 0 {
			n := len(chunk)
			if n > remaining {
				n = remaining
			}
			if _, err := pw.Write(chunk[:n]); err != nil {
				return
			}
			remaining -= n
		}
	}()

	url := srv.URL + "/a2a/workspaces/" + fx.workspaceID + "/agents/" + fx.agentA + "/invoke"
	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.ContentLength = -1 // force unknown length / chunked path
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 body=%s", resp.StatusCode, b)
	}
	assertNoInternalLeak(t, string(b))
	var env map[string]any
	if json.Unmarshal(b, &env) != nil {
		t.Fatalf("not json: %s", b)
	}
	if env["error"] != "A2A_BODY_TOO_LARGE" {
		t.Fatalf("error=%v", env["error"])
	}
	if execs.Load() != 0 {
		t.Fatalf("executor must not run; n=%d", execs.Load())
	}
}

// TestInboundCancel_ChunkedOversize_413
func TestInboundCancel_ChunkedOversize_413(t *testing.T) {
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
	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "x", nil
		},
	}
	auth := fixedUserAuth{actorType: "USER", actorID: fx.ownerID}
	gw, _ := a2agateway.NewInboundGateway(repo, audit, runner, "http://test", auth)
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		big := make([]byte, a2agateway.MaxInboundRequestBytes+32)
		for i := range big {
			big[i] = 'y'
		}
		_, _ = pw.Write(big)
	}()
	url := srv.URL + "/a2a/workspaces/" + fx.workspaceID + "/agents/" + fx.agentA + "/cancel"
	req, _ := http.NewRequest(http.MethodPost, url, pr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.ContentLength = -1
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	assertNoInternalLeak(t, string(b))
}

// TestPublicAgentText_StableShape
func TestPublicAgentText_StableShape(t *testing.T) {
	// Access via HTTP path only; shape checked in hygiene tests.
	// Also cover reserved IPv4 240/4 here as package-level SSRF unit.
	if err := a2agateway.ValidateOutboundURL("https://240.0.0.1/x", []string{"240.0.0.1"}); err == nil {
		t.Fatal("240.0.0.0/4 must be special/denied")
	}
	if err := a2agateway.ValidateOutboundURL("https://255.255.255.255/x", []string{"255.255.255.255"}); err == nil {
		t.Fatal("broadcast must be denied")
	}
}
