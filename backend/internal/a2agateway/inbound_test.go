package a2agateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"actweave/backend/internal/agentdelegation"

	"github.com/google/uuid"
)

type memInboundAudit struct {
	mu      sync.Mutex
	created []agentdelegation.CreateDelegationInput
	final   []agentdelegation.FinalizeDelegationInput
	rows    map[string]agentdelegation.Delegation
}

func (m *memInboundAudit) CreateDelegationAndStep(_ context.Context, in agentdelegation.CreateDelegationInput) (agentdelegation.Delegation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]agentdelegation.Delegation{}
	}
	if existing, ok := m.rows[in.IdempotencyKey]; ok {
		return existing, true, nil
	}
	d := agentdelegation.Delegation{
		ID: in.ID, WorkspaceID: in.WorkspaceID, ParentRunID: in.ParentRunID,
		CallerAgentID: in.CallerAgentID, Mode: in.Mode, Protocol: in.Protocol,
		Origin: in.Origin, Depth: in.Depth, ToolCallID: in.ToolCallID,
		IdempotencyKey: in.IdempotencyKey, Status: agentdelegation.StatusRunning, StepID: in.StepID,
	}
	m.rows[in.IdempotencyKey] = d
	m.created = append(m.created, in)
	return d, false, nil
}

func (m *memInboundAudit) FinalizeDelegation(_ context.Context, in agentdelegation.FinalizeDelegationInput) (agentdelegation.Delegation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.final = append(m.final, in)
	for k, d := range m.rows {
		if d.ID == in.DelegationID {
			d.Status = in.Status
			m.rows[k] = d
			return d, nil
		}
	}
	return agentdelegation.Delegation{}, agentdelegation.ErrNotFound
}

func (m *memInboundAudit) SetChildRunID(context.Context, string, string, string) error { return nil }
func (m *memInboundAudit) RecordDispatchAttempt(context.Context, string, string) error { return nil }
func (m *memInboundAudit) AccumulateModelTokens(context.Context, string, string, agentdelegation.TokenUsage) error {
	return nil
}

func TestAgentAccessAuth_RejectsMissingToken(t *testing.T) {
	t.Parallel()
	auth := AgentAccessAuth{Verifier: AccessTokenVerifierFunc(func(context.Context, string) (AccessTokenClaims, error) {
		return AccessTokenClaims{}, ErrAuthRejected
	})}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	exp := Exposure{AuthMode: AuthModeAgentAccess, WorkspaceID: "ws", AgentID: "ag"}
	if _, _, err := auth.Authorize(context.Background(), req, exp); err == nil {
		t.Fatal("expected reject")
	}
}

func TestAgentAccessAuth_AcceptsValidToken(t *testing.T) {
	t.Parallel()
	ws, ag := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	auth := AgentAccessAuth{Verifier: AccessTokenVerifierFunc(func(_ context.Context, value string) (AccessTokenClaims, error) {
		if value != "tok" {
			return AccessTokenClaims{}, ErrAuthRejected
		}
		return AccessTokenClaims{
			PrincipalID: "p1", ServicePrincipalID: "sp1", WorkspaceID: ws, AgentID: ag,
			Scopes: []string{"run:create"},
		}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	exp := Exposure{AuthMode: AuthModeAgentAccess, WorkspaceID: ws, AgentID: ag, ID: "e1"}
	actorType, actorID, err := auth.Authorize(context.Background(), req, exp)
	if err != nil {
		t.Fatal(err)
	}
	if actorType != "SERVICE_PRINCIPAL" || actorID != "sp1" {
		t.Fatalf("%s %s", actorType, actorID)
	}
}

func TestAgentAccessAuth_RejectsEmptyClaims(t *testing.T) {
	t.Parallel()
	ws, ag := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	auth := AgentAccessAuth{Verifier: AccessTokenVerifierFunc(func(context.Context, string) (AccessTokenClaims, error) {
		// Empty workspace/agent must fail closed.
		return AccessTokenClaims{ServicePrincipalID: "sp", Scopes: []string{"run:create"}}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer x")
	exp := Exposure{AuthMode: AuthModeAgentAccess, WorkspaceID: ws, AgentID: ag}
	if _, _, err := auth.Authorize(context.Background(), req, exp); err == nil {
		t.Fatal("expected empty claims reject")
	}
}

func TestAgentAccessAuth_RejectsMissingScope(t *testing.T) {
	t.Parallel()
	ws, ag := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	auth := AgentAccessAuth{Verifier: AccessTokenVerifierFunc(func(context.Context, string) (AccessTokenClaims, error) {
		return AccessTokenClaims{
			ServicePrincipalID: "sp", WorkspaceID: ws, AgentID: ag, Scopes: []string{"other"},
		}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer x")
	exp := Exposure{AuthMode: AuthModeAgentAccess, WorkspaceID: ws, AgentID: ag}
	if _, _, err := auth.Authorize(context.Background(), req, exp); err == nil {
		t.Fatal("expected missing scope reject")
	}
}

func TestAgentAccessAuth_NONE_RequiresGate(t *testing.T) {
	t.Parallel()
	auth := AgentAccessAuth{}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	exp := Exposure{AuthMode: AuthModeNone, ID: "e"}
	if _, _, err := auth.Authorize(context.Background(), req, exp); err == nil {
		t.Fatal("NONE without AllowAuthNone must reject")
	}
	auth.AllowAuthNone = true
	if _, _, err := auth.Authorize(context.Background(), req, exp); err != nil {
		t.Fatal(err)
	}
}

func TestAgentAccessAuth_WorkspaceMismatch(t *testing.T) {
	t.Parallel()
	auth := AgentAccessAuth{Verifier: AccessTokenVerifierFunc(func(context.Context, string) (AccessTokenClaims, error) {
		return AccessTokenClaims{WorkspaceID: "other", AgentID: "ag", Scopes: []string{"run:create"}, ServicePrincipalID: "sp"}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer x")
	exp := Exposure{AuthMode: AuthModeAgentAccess, WorkspaceID: "ws", AgentID: "ag"}
	if _, _, err := auth.Authorize(context.Background(), req, exp); err == nil {
		t.Fatal("expected workspace mismatch reject")
	}
}

func TestStaticReplyRunner_PrepareThenExecute(t *testing.T) {
	t.Parallel()
	r := &StaticReplyRunner{Reply: "hello-a2a"}
	req := InboundRunRequest{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(),
		AgentID:     uuid.Must(uuid.NewV7()).String(),
		UserText:    "hi", TraceID: uuid.Must(uuid.NewV7()).String(),
	}
	runID, err := r.PrepareRun(context.Background(), req)
	if err != nil || runID == "" {
		t.Fatalf("prepare: %v %s", err, runID)
	}
	res, err := r.ExecuteRun(context.Background(), req, runID)
	if err != nil {
		t.Fatal(err)
	}
	if res.AssistantText != "hello-a2a" || res.RunID != runID {
		t.Fatalf("%+v", res)
	}
}

func TestInboundAuditPrewritePayload(t *testing.T) {
	t.Parallel()
	audit := &memInboundAudit{}
	runID := uuid.Must(uuid.NewV7()).String()
	ws := uuid.Must(uuid.NewV7()).String()
	agentID := uuid.Must(uuid.NewV7()).String()
	del, replay, err := audit.CreateDelegationAndStep(context.Background(), agentdelegation.CreateDelegationInput{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: ws, ParentRunID: runID,
		CallerAgentID: agentID, Mode: agentdelegation.ModeTask,
		Protocol: agentdelegation.ProtocolA2A, Origin: agentdelegation.OriginExternal,
		Depth: 0, BindingVersion: 1, ToolCallID: "msg-1",
		IdempotencyKey: agentdelegation.IdempotencyKey(runID, "msg-1", 1, "exp"),
		InputSummary:   json.RawMessage(`{"source":"a2agateway.inbound"}`),
		InputPayload:   json.RawMessage(`{"request":"hi"}`),
		StepID:         uuid.Must(uuid.NewV7()).String(), AgentID: agentID,
	})
	if err != nil || replay {
		t.Fatalf("err=%v replay=%v", err, replay)
	}
	_, err = audit.FinalizeDelegation(context.Background(), agentdelegation.FinalizeDelegationInput{
		WorkspaceID: ws, DelegationID: del.ID, StepID: del.StepID,
		Status:        agentdelegation.StatusSucceeded,
		OutputSummary: json.RawMessage(`{"ok":true}`),
		OutputPayload: json.RawMessage(`{"result":"ok"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.final) != 1 || audit.final[0].Status != agentdelegation.StatusSucceeded {
		t.Fatalf("%+v", audit.final)
	}
}
