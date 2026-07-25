package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflow"
)

func TestSessionTurnWithFeedbackRevisesDraftNeverPublishes(t *testing.T) {
	t.Parallel()
	// Session seed + feedback path (D14): draftVersion increments; no publish surface used.
	graph1 := validD8Graph(testToolID)
	graph2 := validD8Graph(testToolID)
	graph2.Nodes = append(graph2.Nodes[:2], append([]domain.WorkflowGraphNode{{
		ID: "approval-1", Type: "Approval", Label: "审批",
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Direction: "input"},
			{Key: "output", Direction: "output"},
		},
		Data: map[string]any{}, UI: map[string]any{"generated": true},
	}}, graph2.Nodes[2:]...)...)
	graph2.Edges = []domain.WorkflowGraphEdge{
		{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "tool-1", TargetPort: "input"},
		{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "approval-1", TargetPort: "input"},
		{ID: "e3", SourceNodeID: "approval-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
	}

	model := &recordingGraphModel{graphs: []domain.WorkflowGraphDraft{graph1, graph2}}
	store := &trackingWorkflowService{}
	drafts := &fakeSessionDrafts{store: &store.fakeDraftStore}
	svc := newTestSessionService(t, sessionTestDeps{
		store:  NewMemorySessionStore(),
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})
	// Re-wire turn drafts to tracking store so publish counters are observable.
	// newTestSessionService already uses drafts.store for TurnService DraftStore.

	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	turn1, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "查询支付状态",
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn1.DraftVersion != 1 {
		t.Fatalf("turn1 draftVersion=%d", turn1.DraftVersion)
	}

	feedbackRaw, err := json.Marshal(FailureFeedback{
		Source:      FailureSourceTrial,
		WorkflowID:  turn1.Workflow.CapabilityID,
		ExecutionID: "118f1f2e-7b5a-7c3d-8e9f-1234567890e1",
		Issues:      []FailureIssue{{Code: "TIMEOUT", Message: "tool timed out", NodeID: "tool-1"}},
		RawSummary:  "trial failed at tool-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Feedback-only body: message synthesized server-side.
	turn2, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "",
		CreatedBy:   testCreatorID,
		Feedback:    feedbackRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn2.DraftVersion != 2 {
		t.Fatalf("feedback turn draftVersion want 2 got %d", turn2.DraftVersion)
	}
	if store.publishCalls != 0 || store.activateCalls != 0 {
		t.Fatalf("session feedback path must not Publish/Activate: p=%d a=%d", store.publishCalls, store.activateCalls)
	}
	if store.createCalls != 1 || store.updateCalls != 1 {
		t.Fatalf("expect create once then update: create=%d update=%d", store.createCalls, store.updateCalls)
	}
	revised, ok := turn2.Graph.UI["revisedFrom"].(map[string]any)
	if !ok || revised["source"] != FailureSourceTrial {
		t.Fatalf("revisedFrom: %+v", turn2.Graph.UI)
	}
	if len(model.inputs) < 2 || model.inputs[1].Feedback == nil {
		t.Fatalf("second model call must include feedback: %+v", model.inputs)
	}
}

func TestSessionTurnRejectsMalformedFeedback(t *testing.T) {
	t.Parallel()
	svc := newTestSessionService(t, sessionTestDeps{
		model:  &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{validD8Graph(testToolID)}},
		agents: usableAgentLookup(),
		models: usableModelLookup(),
	})
	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "revise please",
		CreatedBy:   testCreatorID,
		Feedback:    json.RawMessage(`{"source":"compile"}`),
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestCreateSessionRequiresAgentModel(t *testing.T) {
	t.Parallel()
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store:  store,
		model:  &scriptedGraphModel{},
		agents: noModelAgentLookup(),
		models: &fakeModelLookup{},
	})

	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		CreatedBy:   testCreatorID,
	})
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
	if store.CountSessions() != 0 {
		t.Fatalf("must not create session when agent has no model: count=%d", store.CountSessions())
	}
}

func TestCreateSessionRejectsModelBypass(t *testing.T) {
	t.Parallel()
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store:  store,
		model:  &scriptedGraphModel{},
		agents: usableAgentLookup(),
		models: usableModelLookup(),
	})
	_, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID:          testWorkspaceID,
		AgentID:              testAgentID,
		RequestModelConfigID: testModelConfigID,
		CreatedBy:            testCreatorID,
	})
	if !errors.Is(err, ErrModelConfigBypassRejected) {
		t.Fatalf("want bypass rejected, got %v", err)
	}
	if store.CountSessions() != 0 {
		t.Fatal("bypass must not create session")
	}
}

func TestSessionMultiTurnIncrementsDraftVersion(t *testing.T) {
	t.Parallel()
	// UT-GEN-MULTI-TURN: fake model turn1 → turn2 approval; draftVersion increments.
	graph1 := validD8Graph(testToolID)
	graph2 := validD8Graph(testToolID)
	graph2.Nodes = append(graph2.Nodes[:2], append([]domain.WorkflowGraphNode{{
		ID: "approval-1", Type: "Approval", Label: "审批",
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Direction: "input"},
			{Key: "output", Direction: "output"},
		},
		Data: map[string]any{}, UI: map[string]any{"generated": true},
	}}, graph2.Nodes[2:]...)...)
	graph2.Edges = []domain.WorkflowGraphEdge{
		{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "tool-1", TargetPort: "input"},
		{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "approval-1", TargetPort: "input"},
		{ID: "e3", SourceNodeID: "approval-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
	}

	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{graph1, graph2}}
	drafts := &fakeSessionDrafts{store: &fakeDraftStore{}}
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store:  store,
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusOpen || session.ModelConfigID != testModelConfigID {
		t.Fatalf("session: %+v", session)
	}

	turn1, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "查询支付状态",
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn1.DraftVersion != 1 {
		t.Fatalf("turn1 draftVersion=%d", turn1.DraftVersion)
	}
	if turn1.GeneratedBy != GeneratedByV2 {
		t.Fatalf("generatedBy=%q", turn1.GeneratedBy)
	}
	if turn1.TurnID == "" || turn1.GenerationID == "" {
		t.Fatalf("turn ids missing: %+v", turn1)
	}
	if turn1.Workflow.CapabilityID == "" {
		t.Fatal("workflow id required after first turn")
	}
	if turn1.Graph.UI["sessionId"] != session.ID {
		t.Fatalf("sessionId stamp: %+v", turn1.Graph.UI)
	}

	// Sync fake draft reader with persisted draft for turn2 Prior.
	drafts.store.lastDraft = turn1.Draft
	drafts.workflow = turn1.Workflow

	turn2, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "加审批节点",
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn2.DraftVersion != 2 {
		t.Fatalf("turn2 draftVersion want 2 got %d", turn2.DraftVersion)
	}
	if turn2.Workflow.CapabilityID != turn1.Workflow.CapabilityID {
		t.Fatalf("workflow id changed: %s vs %s", turn1.Workflow.CapabilityID, turn2.Workflow.CapabilityID)
	}
	if drafts.store.createCalls != 1 || drafts.store.updateCalls != 1 {
		t.Fatalf("create=%d update=%d", drafts.store.createCalls, drafts.store.updateCalls)
	}
	if model.GenerateCalls != 2 {
		t.Fatalf("model calls=%d", model.GenerateCalls)
	}

	detail, err := svc.GetSession(context.Background(), testWorkspaceID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(detail.Turns))
	}
	if detail.Turns[0].Status != TurnStatusSucceeded || detail.Turns[1].Status != TurnStatusSucceeded {
		t.Fatalf("turn statuses: %+v", detail.Turns)
	}
}

func TestSessionCloseThenTurnConflicts(t *testing.T) {
	t.Parallel()
	// UT-SESSION-CLOSE
	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{validD8Graph(testToolID)}}
	drafts := &fakeSessionDrafts{store: &fakeDraftStore{}}
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store:  store,
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	turn1, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "生成流程",
		CreatedBy:   testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	drafts.store.lastDraft = turn1.Draft
	drafts.workflow = turn1.Workflow

	closed, err := svc.CloseSession(context.Background(), testWorkspaceID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != SessionStatusClosed || closed.ClosedAt == nil {
		t.Fatalf("closed session: %+v", closed)
	}

	_, err = svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID,
		SessionID:   session.ID,
		Message:     "再改一版",
		CreatedBy:   testCreatorID,
	})
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("want ErrSessionClosed, got %v", err)
	}
	detail, err := svc.GetSession(context.Background(), testWorkspaceID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Status != SessionStatusClosed {
		t.Fatalf("status=%s", detail.Session.Status)
	}
	if drafts.store.updateCalls != 0 {
		t.Fatalf("closed turn must not update draft: update=%d", drafts.store.updateCalls)
	}
}

func TestSessionGuardRejectDoesNotClobberDraft(t *testing.T) {
	t.Parallel()
	good := validD8Graph(testToolID)
	bad := validD8Graph("118f1f2e-7b5a-7c3d-8e9f-aaaaaaaaaaaa")
	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{good, bad}}
	drafts := &fakeSessionDrafts{store: &fakeDraftStore{}}
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store:  store,
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID, AgentID: testAgentID, CreatedBy: testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	turn1, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID, SessionID: session.ID, Message: "查询支付", CreatedBy: testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	drafts.store.lastDraft = turn1.Draft
	drafts.workflow = turn1.Workflow
	versionAfterTurn1 := turn1.DraftVersion

	_, err = svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID, SessionID: session.ID, Message: "改坏工具", CreatedBy: testCreatorID,
	})
	if !errors.Is(err, ErrGuardRejected) {
		t.Fatalf("want guard reject, got %v", err)
	}
	if drafts.store.updateCalls != 0 {
		t.Fatalf("guard must not update draft: updates=%d", drafts.store.updateCalls)
	}
	if drafts.store.lastDraft.DraftVersion != versionAfterTurn1 {
		t.Fatalf("draft version clobbered: %d", drafts.store.lastDraft.DraftVersion)
	}
	detail, err := svc.GetSession(context.Background(), testWorkspaceID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Turns) != 2 || detail.Turns[1].Status != TurnStatusGuardRejected {
		t.Fatalf("turns: %+v", detail.Turns)
	}
	if detail.Session.Status != SessionStatusOpen {
		t.Fatal("session must stay OPEN after guard reject")
	}
}

func TestApplySessionTurnPassesHistoryToModel(t *testing.T) {
	t.Parallel()
	model := &recordingGraphModel{graphs: []domain.WorkflowGraphDraft{
		validD8Graph(testToolID),
		validD8Graph(testToolID),
	}}
	drafts := &fakeSessionDrafts{store: &fakeDraftStore{}}
	store := NewMemorySessionStore()
	svc := newTestSessionService(t, sessionTestDeps{
		store: store, model: model, drafts: drafts,
		agents: usableAgentLookup(), models: usableModelLookup(), tools: publishedToolCatalog(),
	})
	session, err := svc.CreateSession(context.Background(), CreateSessionRequest{
		WorkspaceID: testWorkspaceID, AgentID: testAgentID, CreatedBy: testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t1, err := svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID, SessionID: session.ID, Message: "第一轮意图", CreatedBy: testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	drafts.store.lastDraft = t1.Draft
	drafts.workflow = t1.Workflow
	if len(model.inputs[0].History) != 0 {
		t.Fatalf("first turn history should be empty: %+v", model.inputs[0].History)
	}
	if model.inputs[0].SystemPrompt.ID == "" || model.inputs[0].SystemPrompt.Content == "" {
		t.Fatal("system prompt required in model input")
	}
	if len(model.inputs[0].CatalogToolIDs) == 0 {
		t.Fatal("catalog tool ids required")
	}

	_, err = svc.ApplySessionTurn(context.Background(), ApplySessionTurnRequest{
		WorkspaceID: testWorkspaceID, SessionID: session.ID, Message: "第二轮修订", CreatedBy: testCreatorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 2 {
		t.Fatalf("inputs=%d", len(model.inputs))
	}
	if len(model.inputs[1].History) < 1 {
		t.Fatalf("second turn must include history: %+v", model.inputs[1].History)
	}
	if model.inputs[1].History[0].Content != "第一轮意图" {
		t.Fatalf("history user message: %+v", model.inputs[1].History)
	}
	if model.inputs[1].CurrentGraph == nil {
		t.Fatal("second turn must pass current graph")
	}
}

// --- fakes for session tests ---

type sessionTestDeps struct {
	store  *MemorySessionStore
	model  GraphModel
	drafts *fakeSessionDrafts
	agents *fakeAgentLookup
	models *fakeModelLookup
	tools  ToolCatalog
}

func newTestSessionService(t *testing.T, deps sessionTestDeps) *SessionService {
	t.Helper()
	if deps.store == nil {
		deps.store = NewMemorySessionStore()
	}
	if deps.tools == nil {
		deps.tools = publishedToolCatalog()
	}
	if deps.drafts == nil {
		deps.drafts = &fakeSessionDrafts{store: &fakeDraftStore{}}
	}
	if deps.agents == nil {
		deps.agents = usableAgentLookup()
	}
	if deps.models == nil {
		deps.models = usableModelLookup()
	}
	gate, err := NewAgentModelGate(deps.agents, deps.models)
	if err != nil {
		t.Fatal(err)
	}
	// Shared id pool: session, turn, gen, workflow, draft, turn2, gen2, ...
	ids := []string{
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a2",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a3",
		testWorkflowID,
		testDraftID,
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a4",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a5",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a6",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a7",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a8",
		"118f1f2e-7b5a-7c3d-8e9f-1234567890a9",
	}
	nextID := func() (string, error) {
		if len(ids) == 0 {
			return "", errors.New("no more ids")
		}
		v := ids[0]
		ids = ids[1:]
		return v, nil
	}
	turnSvc, err := NewTurnService(TurnServiceDeps{
		Model: deps.model, Drafts: deps.drafts.store, Prompts: NewMemorySystemPromptStore(),
		Gate: gate, Tools: deps.tools, NextID: nextID,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewSessionService(SessionServiceDeps{
		Sessions: deps.store, Turns: turnSvc, Gate: gate,
		Prompts: NewMemorySystemPromptStore(), Drafts: deps.drafts, NextID: nextID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func noModelAgentLookup() *fakeAgentLookup {
	return &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID,
			ModelConfigID: "", Status: agent.StatusActive,
		},
	}}
}

type fakeSessionDrafts struct {
	store    *fakeDraftStore
	workflow workflow.Workflow
}

func (f *fakeSessionDrafts) GetDraft(_ context.Context, workspaceID, capabilityID string) (workflow.Draft, error) {
	if f.store.lastDraft.ID == "" {
		return workflow.Draft{}, workflow.ErrNotFound
	}
	d := f.store.lastDraft
	d.WorkspaceID = workspaceID
	d.CapabilityID = capabilityID
	if len(d.Graph) == 0 {
		encoded, _ := json.Marshal(validD8Graph(testToolID))
		d.Graph = encoded
	}
	return d, nil
}

func (f *fakeSessionDrafts) Get(_ context.Context, workspaceID, capabilityID string) (workflow.Workflow, error) {
	if f.workflow.CapabilityID == "" {
		return workflow.Workflow{
			CapabilityID: capabilityID, WorkspaceID: workspaceID,
			CurrentDraftID: f.store.lastDraft.ID,
		}, nil
	}
	return f.workflow, nil
}

type recordingGraphModel struct {
	graphs []domain.WorkflowGraphDraft
	inputs []GraphModelInput
}

func (m *recordingGraphModel) GenerateGraph(_ context.Context, input GraphModelInput) (domain.WorkflowGraphDraft, error) {
	m.inputs = append(m.inputs, input)
	if len(m.graphs) == 0 {
		return domain.WorkflowGraphDraft{}, errors.New("no graphs")
	}
	g := m.graphs[0]
	if len(m.graphs) > 1 {
		m.graphs = m.graphs[1:]
	}
	return g, nil
}
