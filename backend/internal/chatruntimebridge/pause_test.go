package chatruntimebridge

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/principal"
)

type pauseConfirmations struct {
	mu       sync.Mutex
	prepared chat.PreparedChatConfirmation
	input    chat.PrepareChatConfirmationInput
}

func (p *pauseConfirmations) Prepare(_ context.Context, input chat.PrepareChatConfirmationInput) (chat.PreparedChatConfirmation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.input = input
	expires := time.Now().UTC().Add(10 * time.Minute)
	p.prepared = chat.PreparedChatConfirmation{
		Confirmation: chat.ChatConfirmation{
			ID: input.ID, WorkspaceID: input.WorkspaceID, SessionID: input.SessionID,
			RunID: input.Resume.Confirmation.RunID, ExpiresAt: expires,
			Status: "PENDING", RiskLevel: input.RiskLevel,
		},
		Prepared: execution.PreparedConfirmationResume{
			Checkpoint: execution.ConfirmationResumeCheckpoint{
				ConfirmationID:  input.Resume.Confirmation.ID,
				RequestSnapshot: input.Resume.RequestSnapshot,
			},
		},
	}
	p.prepared.Confirmation.ExpiresAt = expires
	return p.prepared, nil
}

type touchTTL struct {
	mu        sync.Mutex
	calls     int
	id        string
	expiresAt time.Time
}

func (t *touchTTL) TouchExpiresAt(_ context.Context, checkPointID string, expiresAt time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	t.id = checkPointID
	t.expiresAt = expiresAt
	return nil
}

type pauseSessions struct{}

func (pauseSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}
func (pauseSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return nil, nil
}
func (pauseSessions) ListMessagesReversePage(
	context.Context, string, string, int, *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	return chat.MessagePage{}, nil
}
func (pauseSessions) GetMessage(context.Context, string, string) (chat.Message, error) {
	return chat.Message{}, chat.ErrNotFound
}

type pauseRuns struct {
	run execution.AgentRun
}

func (r *pauseRuns) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	out := r.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

type pauseEvents struct {
	mu      sync.Mutex
	records []chatruntime.ProtocolRecord
}

func (e *pauseEvents) Record(_ context.Context, record chatruntime.ProtocolRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, record)
	return nil
}

type pauseToolInvoker struct{}

const (
	pauseWorkspaceID = "11000000-0000-4000-8000-000000000001"
	pauseUserID      = "22000000-0000-4000-8000-000000000001"
	pauseRunID       = "33000000-0000-4000-8000-000000000001"
	pauseSessionID   = "44000000-0000-4000-8000-000000000001"
	pauseMsgID       = "55000000-0000-4000-8000-000000000001"
)

func (pauseToolInvoker) ResolveInvocation(_ context.Context, req execution.ResolveRequest) (execution.ResolvedInvocation, error) {
	ws := req.WorkspaceID
	if ws == "" {
		ws = pauseWorkspaceID
	}
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: ws, CapabilityID: "cap-1", ReleaseID: "rel-1",
			ProviderID: "provider-1",
		},
		Connection: execution.ConnectionSnapshot{
			ID: "conn-1", WorkspaceID: ws, Environment: "TEST",
			ProviderID: "provider-1",
		},
		RequiresConfirmation: true,
		RiskLevel:            "HIGH",
		SideEffectLevel:      "WRITE",
	}, nil
}

func (pauseToolInvoker) InvokeResolved(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error) {
	return execution.PipelineResult{}, nil
}

func TestPauseForInterrupt_EmbedsEinoChatResumeAndTouchesTTL(t *testing.T) {
	t.Parallel()
	capSnap := json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"cap-1","releaseId":"rel-1","kind":"TOOL",
			"callableName":"demo_tool","callableDescription":"demo",
			"inputSchema":{"type":"object"},"riskLevel":"HIGH",
			"sideEffectLevel":"WRITE","requiresConfirmation":true,"connectionId":"conn-1"
		}]
	}`)
	confirms := &pauseConfirmations{}
	ttl := &touchTTL{}
	events := &pauseEvents{}
	principalSnap, err := principal.NewInternalExecutionSnapshot(pauseWorkspaceID, principal.TypeUser, pauseUserID)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	runs := &pauseRuns{run: execution.AgentRun{
		ID: pauseRunID, WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID,
		Status: "RUNNING", CapabilitySnapshot: capSnap, LockVersion: 3,
		TriggeredByType: "USER", TriggeredByID: pauseUserID, TraceID: "trace-1",
		PrincipalSnapshot: principalSnap,
	}}

	// Minimal bridge: only deps used by pauseForInterrupt.
	b := &Bridge{
		sessions:        pauseSessions{},
		runs:            runs,
		events:          events,
		toolInvoker:     pauseToolInvoker{},
		confirmations:   confirms,
		checkpointTTL:   ttl,
		now:             func() time.Time { return time.Now().UTC() },
		pendingConfirms: make(map[string][]einoruntime.PendingConfirmInterrupt),
	}
	b.recordPending(pendingConfirmKey(pauseWorkspaceID, pauseRunID), einoruntime.PendingConfirmInterrupt{
		ToolName: "demo_tool", CapabilityID: "cap-1", InvocationID: "inv-fixed",
		StepID: "step-1", ArgsJSON: `{"x":1}`,
	})

	cpID := "ws/" + pauseWorkspaceID + "/agent_run/" + pauseRunID + "/nonce-pause"
	interruptID := "agent:hitl-agent;tool:call_confirm"
	result := &einoruntime.RunResult{
		CheckpointID:          cpID,
		Interrupted:           true,
		InterruptContextIDs:   []string{interruptID},
		RootCauseInterruptIDs: []string{interruptID},
	}
	job := agentrun.Job{
		WorkspaceID: pauseWorkspaceID, SessionID: pauseSessionID, RunID: pauseRunID,
		UserMessageID: pauseMsgID, ActorID: pauseUserID,
	}
	if err := b.pauseForInterrupt(context.Background(), job, runs.run, result, RuntimeGenerationClassic); err != nil {
		t.Fatalf("pauseForInterrupt: %v", err)
	}

	// Outer schema + nested einoChatResume.
	snap := confirms.input.Resume.RequestSnapshot
	var object map[string]any
	if err := json.Unmarshal(snap, &object); err != nil {
		t.Fatal(err)
	}
	if object["schemaVersion"] != "tool-resume-request.v1" {
		t.Fatalf("outer schema = %v", object["schemaVersion"])
	}
	if _, has := object["chatLoop"]; has {
		t.Fatal("chatLoop must not be present after eino embed")
	}
	meta, ok := ExtractEinoChatResume(snap)
	if !ok {
		t.Fatalf("expected nested einoChatResume in %s", string(snap))
	}
	if meta.EinoCheckpointID != cpID {
		t.Fatalf("checkpoint = %q", meta.EinoCheckpointID)
	}
	if meta.RootInterruptID != interruptID {
		t.Fatalf("rootInterrupt = %q", meta.RootInterruptID)
	}
	if meta.SessionID != pauseSessionID || meta.UserMessageID != pauseMsgID {
		t.Fatalf("session/msg = %+v", meta)
	}

	// D15: TouchExpiresAt called with confirmation ExpiresAt.
	ttl.mu.Lock()
	defer ttl.mu.Unlock()
	if ttl.calls != 1 {
		t.Fatalf("TouchExpiresAt calls = %d", ttl.calls)
	}
	if ttl.id != cpID {
		t.Fatalf("TouchExpiresAt id = %q", ttl.id)
	}
	if !ttl.expiresAt.Equal(confirms.prepared.Confirmation.ExpiresAt) {
		t.Fatalf("TouchExpiresAt expiresAt = %v, want %v",
			ttl.expiresAt, confirms.prepared.Confirmation.ExpiresAt)
	}

	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.records) != 1 || events.records[0].Kind != chatruntime.ProtocolRecordInteractionRequested {
		t.Fatalf("protocol records = %+v", events.records)
	}
}
