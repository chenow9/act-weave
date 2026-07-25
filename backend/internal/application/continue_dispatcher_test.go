package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/execution"
)

// stubRuntime records continue calls for dispatcher branching tests.
type stubRuntime struct {
	mu        sync.Mutex
	continued []continuedCall
}

type continuedCall struct {
	job             agentrun.Job
	requestSnapshot json.RawMessage
	toolResult      json.RawMessage
}

func (s *stubRuntime) Enqueue(agentrun.Job) {}
func (s *stubRuntime) CancelRun(_, _ string) error {
	return nil
}
func (s *stubRuntime) EnqueueContinueWithLifecycle(
	job agentrun.Job,
	requestSnapshot, toolResult json.RawMessage,
	_ agentrun.ContinueLifecycle,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continued = append(s.continued, continuedCall{
		job: job, requestSnapshot: requestSnapshot, toolResult: toolResult,
	})
}

func (s *stubRuntime) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.continued)
}

func (s *stubRuntime) last() continuedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.continued) == 0 {
		return continuedCall{}
	}
	return s.continued[len(s.continued)-1]
}

type stubRuns struct {
	run execution.AgentRun
}

func (s *stubRuns) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	run := s.run
	run.WorkspaceID = workspaceID
	run.ID = runID
	if run.Status == "" {
		run.Status = "RUNNING"
	}
	return run, nil
}

type stubProtocol struct {
	mu     sync.Mutex
	called int
	last   struct {
		interactionID, invocationID, toolName string
	}
}

func (s *stubProtocol) RecordApprovedInteraction(
	_ context.Context,
	_ execution.AgentRun,
	interactionID, invocationID, toolName string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called++
	s.last.interactionID = interactionID
	s.last.invocationID = invocationID
	s.last.toolName = toolName
	return nil
}

func einoMetaFixture() chatruntimebridge.EinoChatResume {
	return chatruntimebridge.EinoChatResume{
		ResumeSchemaVersion: chatruntimebridge.EinoChatResumeSchemaVersion,
		SessionID:           "sess-eino",
		UserMessageID:       "msg-eino",
		ActorID:             "actor-eino",
		EinoCheckpointID:    "ws/ws-1/agent_run/run-1/n1",
		InterruptIDs:        []string{"agent:a;tool:c1"},
		RootInterruptID:     "agent:a;tool:c1",
		GatedToolCallID:     "c1",
		InterruptKind:       chatruntimebridge.InterruptKindToolConfirmation,
	}
}

func embedEinoSnap(t *testing.T, meta chatruntimebridge.EinoChatResume) json.RawMessage {
	t.Helper()
	outer := json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"invocationId":"inv-1",
		"chatLoop":{"schemaVersion":"chat-tool-loop.v1","sessionId":"legacy-sess","userMessageId":"legacy-msg","actorId":"legacy-actor"}
	}`)
	// Dual presence input: Embed strips chatLoop (production mutual exclusion).
	embedded, err := chatruntimebridge.EmbedEinoChatResume(outer, meta)
	if err != nil {
		t.Fatal(err)
	}
	return embedded
}

func dualPresenceSnap(t *testing.T, meta chatruntimebridge.EinoChatResume) json.RawMessage {
	t.Helper()
	// Manually keep both keys so dispatcher preference (eino first) is proven
	// even when dual authority somehow appears on the wire.
	object := map[string]any{
		"schemaVersion": "tool-resume-request.v1",
		"invocationId":  "inv-1",
		"chatLoop": map[string]any{
			"schemaVersion":  "chat-tool-loop.v1",
			"sessionId":      "legacy-sess",
			"userMessageId":  "legacy-msg",
			"actorId":        "legacy-actor",
			"modelRounds":    1,
			"toolAttempts":   1,
			"messages":       []any{},
			"pendingCalls":   []any{},
			"pendingIndex":   0,
			"completedTools": []any{},
			"gatedToolCall": map[string]any{
				"id": "call-legacy", "type": "function",
				"function": map[string]any{"name": "legacy_tool", "arguments": "{}"},
			},
			"gatedStepId": "step-legacy",
		},
		"einoChatResume": meta,
	}
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func legacyLoopSnap() json.RawMessage {
	return json.RawMessage(`{
		"schemaVersion":"tool-resume-request.v1",
		"invocationId":"inv-legacy",
		"chatLoop":{
			"schemaVersion":"chat-tool-loop.v1",
			"sessionId":"sess-legacy",
			"userMessageId":"msg-legacy",
			"actorId":"actor-legacy",
			"modelRounds":1,
			"toolAttempts":1,
			"messages":[],
			"pendingCalls":[],
			"pendingIndex":0,
			"completedTools":[],
			"gatedToolCall":{"id":"call-1","type":"function","function":{"name":"demo","arguments":"{}"}},
			"gatedStepId":"step-1"
		}
	}`)
}

func decisionWithSnap(snap, result json.RawMessage) execution.InteractionDecisionResult {
	return execution.InteractionDecisionResult{
		Confirmation: execution.ExecutionConfirmation{
			ID: "conf-1", WorkspaceID: "ws-1", RunID: "run-1",
		},
		Checkpoint: execution.ConfirmationResumeCheckpoint{
			ConfirmationID:  "conf-1",
			WorkspaceID:     "ws-1",
			RunID:           "run-1",
			TargetItemID:    "inv-1",
			RequestSnapshot: snap,
			ResultSnapshot:  result,
		},
		Decision: execution.InteractionDecisionApprove,
	}
}

func TestContinueApprovedInteraction_EinoFirst(t *testing.T) {
	t.Parallel()
	eino := &stubRuntime{}
	protocol := &stubProtocol{}
	meta := einoMetaFixture()
	snap := embedEinoSnap(t, meta)
	resultSnap := json.RawMessage(`{"invocationId":"inv-1","output":{"ok":true}}`)

	svc := &aapInteractionContinuation{
		runs:     &stubRuns{run: execution.AgentRun{Status: "RUNNING", SessionID: "sess-eino"}},
		protocol: protocol,
		eino:     eino,
	}
	err := svc.ContinueApprovedInteraction(context.Background(), decisionWithSnap(snap, resultSnap))
	if err != nil {
		t.Fatalf("ContinueApprovedInteraction: %v", err)
	}
	if eino.count() != 1 {
		t.Fatalf("eino continued = %d, want 1", eino.count())
	}
	got := eino.last()
	if got.job.SessionID != "sess-eino" || got.job.UserMessageID != "msg-eino" {
		t.Fatalf("job = %+v", got.job)
	}
	if protocol.called != 1 {
		t.Fatalf("protocol.RecordApprovedInteraction called = %d", protocol.called)
	}
}

func TestContinueApprovedInteraction_ChatLoopOnlyInvalid(t *testing.T) {
	t.Parallel()
	eino := &stubRuntime{}
	protocol := &stubProtocol{}
	snap := legacyLoopSnap()

	svc := &aapInteractionContinuation{
		runs:     &stubRuns{run: execution.AgentRun{Status: "RUNNING", SessionID: "sess-legacy"}},
		protocol: protocol,
		eino:     eino,
	}
	err := svc.ContinueApprovedInteraction(context.Background(), decisionWithSnap(snap, nil))
	if !errors.Is(err, execution.ErrInteractionDecisionInvalid) {
		t.Fatalf("err = %v, want ErrInteractionDecisionInvalid (PR16 no legacy continue)", err)
	}
	if eino.count() != 0 {
		t.Fatal("eino must not continue without einoChatResume")
	}
	if protocol.called != 0 {
		t.Fatal("protocol must not record approved interaction for invalid resume")
	}
}

func TestContinueApprovedInteraction_Invalid(t *testing.T) {
	t.Parallel()
	svc := &aapInteractionContinuation{
		runs: &stubRuns{}, protocol: &stubProtocol{},
		eino: &stubRuntime{},
	}
	err := svc.ContinueApprovedInteraction(context.Background(), decisionWithSnap(
		json.RawMessage(`{"schemaVersion":"tool-resume-request.v1"}`), nil,
	))
	if !errors.Is(err, execution.ErrInteractionDecisionInvalid) {
		t.Fatalf("err = %v, want ErrInteractionDecisionInvalid", err)
	}
}

func TestContinueApprovedInteraction_DualPresencePrefersEino(t *testing.T) {
	t.Parallel()
	eino := &stubRuntime{}
	meta := einoMetaFixture()
	snap := dualPresenceSnap(t, meta)

	// Guard: fixture still has both keys.
	if !chatruntimebridge.HasChatLoop(snap) {
		t.Fatal("dual fixture must retain chatLoop")
	}
	if _, ok := chatruntimebridge.ExtractEinoChatResume(snap); !ok {
		t.Fatal("dual fixture must have valid einoChatResume")
	}

	svc := &aapInteractionContinuation{
		runs:     &stubRuns{run: execution.AgentRun{Status: "RUNNING"}},
		protocol: &stubProtocol{},
		eino:     eino,
	}
	if err := svc.ContinueApprovedInteraction(context.Background(), decisionWithSnap(snap, nil)); err != nil {
		t.Fatal(err)
	}
	if eino.count() != 1 {
		t.Fatalf("eino=%d, want 1", eino.count())
	}
	if eino.last().job.SessionID != "sess-eino" {
		t.Fatalf("preferred session = %q", eino.last().job.SessionID)
	}
}

func TestContinueApprovedInteraction_EinoMetaButEinoRuntimeNil(t *testing.T) {
	t.Parallel()
	snap := embedEinoSnap(t, einoMetaFixture())
	svc := &aapInteractionContinuation{
		runs:     &stubRuns{run: execution.AgentRun{Status: "RUNNING"}},
		protocol: &stubProtocol{},
		eino:     nil,
	}
	err := svc.ContinueApprovedInteraction(context.Background(), decisionWithSnap(snap, nil))
	if !errors.Is(err, execution.ErrInteractionDecisionInvalid) {
		t.Fatalf("err = %v, want ErrInteractionDecisionInvalid", err)
	}
}

// --- chatConfirmationContinue.Confirm dispatcher (shipped Confirm path) ---

type stubChatConfirm struct {
	result chat.ConfirmedChatConfirmation
	err    error
}

func (s *stubChatConfirm) Confirm(
	_ context.Context, _ chat.ConfirmChatConfirmationInput,
) (chat.ConfirmedChatConfirmation, error) {
	return s.result, s.err
}

func (s *stubChatConfirm) Cancel(
	_ context.Context, _ chat.CancelChatConfirmationInput,
) (chat.CancelledChatConfirmation, error) {
	return chat.CancelledChatConfirmation{}, nil
}

func TestChatConfirmationContinue_ConfirmEinoFirst(t *testing.T) {
	t.Parallel()
	eino := &stubRuntime{}
	meta := einoMetaFixture()
	snap := embedEinoSnap(t, meta)
	inner := &stubChatConfirm{result: chat.ConfirmedChatConfirmation{
		Confirmation: chat.ChatConfirmation{
			ID: "chat-conf-1", WorkspaceID: "ws-1", RunID: "run-1",
			ExecutionConfirmationID: "exec-conf-1",
		},
		Resume: execution.ConfirmationResumeResult{
			Checkpoint: execution.ConfirmationResumeCheckpoint{
				ConfirmationID: "exec-conf-1", RequestSnapshot: snap,
			},
			Result: json.RawMessage(`{"invocationId":"inv-1","output":{}}`),
		},
	}}
	svc := &chatConfirmationContinue{
		inner: inner, eino: eino,
	}
	_, err := svc.Confirm(context.Background(), chat.ConfirmChatConfirmationInput{
		WorkspaceID: "ws-1", ConfirmationID: "chat-conf-1", ActorID: "actor-eino",
	})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if eino.count() != 1 {
		t.Fatalf("eino=%d", eino.count())
	}
	if eino.last().job.SessionID != "sess-eino" {
		t.Fatalf("session = %q", eino.last().job.SessionID)
	}
}

func TestChatConfirmationContinue_ConfirmEinoNilErrors(t *testing.T) {
	t.Parallel()
	snap := embedEinoSnap(t, einoMetaFixture())
	inner := &stubChatConfirm{result: chat.ConfirmedChatConfirmation{
		Confirmation: chat.ChatConfirmation{
			ID: "chat-conf-1", WorkspaceID: "ws-1", RunID: "run-1",
		},
		Resume: execution.ConfirmationResumeResult{
			Checkpoint: execution.ConfirmationResumeCheckpoint{RequestSnapshot: snap},
		},
	}}
	svc := &chatConfirmationContinue{inner: inner, eino: nil}
	_, err := svc.Confirm(context.Background(), chat.ConfirmChatConfirmationInput{
		WorkspaceID: "ws-1", ConfirmationID: "chat-conf-1", ActorID: "a",
	})
	if !errors.Is(err, execution.ErrInteractionDecisionInvalid) {
		t.Fatalf("err = %v, want ErrInteractionDecisionInvalid (no silent no-op)", err)
	}
}

func TestChatConfirmationContinue_ConfirmChatLoopOnlyErrors(t *testing.T) {
	t.Parallel()
	eino := &stubRuntime{}
	snap := legacyLoopSnap()
	inner := &stubChatConfirm{result: chat.ConfirmedChatConfirmation{
		Confirmation: chat.ChatConfirmation{
			ID: "chat-conf-1", WorkspaceID: "ws-1", RunID: "run-1",
		},
		Resume: execution.ConfirmationResumeResult{
			Checkpoint: execution.ConfirmationResumeCheckpoint{RequestSnapshot: snap},
		},
	}}
	svc := &chatConfirmationContinue{inner: inner, eino: eino}
	_, err := svc.Confirm(context.Background(), chat.ConfirmChatConfirmationInput{
		WorkspaceID: "ws-1", ConfirmationID: "chat-conf-1", ActorID: "a",
	})
	if !errors.Is(err, execution.ErrInteractionDecisionInvalid) {
		t.Fatalf("err = %v, want ErrInteractionDecisionInvalid for chatLoop-only", err)
	}
	if eino.count() != 0 {
		t.Fatal("must not enqueue continue for legacy chatLoop snapshot")
	}
}

type stubCheckpointDeleter struct {
	mu      sync.Mutex
	deleted []string
}

func (s *stubCheckpointDeleter) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

func TestChatConfirmationContinue_CancelDeletesEinoCheckpoint(t *testing.T) {
	t.Parallel()
	meta := einoMetaFixture()
	snap := embedEinoSnap(t, meta)
	deleter := &stubCheckpointDeleter{}
	inner := &stubChatConfirmCancel{
		result: chat.CancelledChatConfirmation{
			Confirmation: chat.ChatConfirmation{ID: "c1", WorkspaceID: "ws-1", RunID: "run-1"},
			Checkpoint: execution.ConfirmationResumeCheckpoint{
				RequestSnapshot: snap,
			},
		},
	}
	svc := &chatConfirmationContinue{
		inner: inner, eino: &stubRuntime{}, checkpoints: deleter,
	}
	_, err := svc.Cancel(context.Background(), chat.CancelChatConfirmationInput{
		WorkspaceID: "ws-1", ConfirmationID: "c1", ActorID: "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	deleter.mu.Lock()
	defer deleter.mu.Unlock()
	if len(deleter.deleted) != 1 || deleter.deleted[0] != meta.EinoCheckpointID {
		t.Fatalf("deleted = %v, want [%s]", deleter.deleted, meta.EinoCheckpointID)
	}
}

// stubChatConfirmCancel only implements Cancel for the cancel-delete test.
type stubChatConfirmCancel struct {
	result chat.CancelledChatConfirmation
}

func (s *stubChatConfirmCancel) Confirm(
	_ context.Context, _ chat.ConfirmChatConfirmationInput,
) (chat.ConfirmedChatConfirmation, error) {
	return chat.ConfirmedChatConfirmation{}, errors.New("not used")
}

func (s *stubChatConfirmCancel) Cancel(
	_ context.Context, _ chat.CancelChatConfirmationInput,
) (chat.CancelledChatConfirmation, error) {
	return s.result, nil
}
