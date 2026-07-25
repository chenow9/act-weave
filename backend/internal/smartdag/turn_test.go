package smartdag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"
)

func TestApplyTurnMissingModelDoesNotCreateDraft(t *testing.T) {
	t.Parallel()
	drafts := &fakeDraftStore{}
	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{validD8Graph(testToolID)}}
	svc := newTestTurnService(t, turnTestDeps{
		model:  model,
		drafts: drafts,
		agents: &fakeAgentLookup{byKey: map[string]agent.Agent{
			testWorkspaceID + "/" + testAgentID: {
				ID: testAgentID, WorkspaceID: testWorkspaceID,
				ModelConfigID: "", Status: agent.StatusActive,
			},
		}},
		models: &fakeModelLookup{},
		tools:  publishedToolCatalog(),
	})

	_, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		Message:     "生成支付查询流程",
		CreatedBy:   testCreatorID,
	})
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
	if drafts.createCalls != 0 || drafts.updateCalls != 0 {
		t.Fatalf("missing model must not write draft: create=%d update=%d", drafts.createCalls, drafts.updateCalls)
	}
	if model.GenerateCalls != 0 {
		t.Fatal("LLM must not be called when agent model is required")
	}
}

func TestApplyTurnRejectsModelConfigBypass(t *testing.T) {
	t.Parallel()
	drafts := &fakeDraftStore{}
	svc := newTestTurnService(t, turnTestDeps{
		model:  &scriptedGraphModel{},
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})
	_, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID:          testWorkspaceID,
		AgentID:              testAgentID,
		RequestModelConfigID: testModelConfigID,
		Message:              "goal",
		CreatedBy:            testCreatorID,
	})
	if !errors.Is(err, ErrModelConfigBypassRejected) {
		t.Fatalf("want bypass rejected, got %v", err)
	}
	if drafts.createCalls != 0 {
		t.Fatal("bypass attempt must not create draft")
	}
}

func TestApplyTurnGuardFailureDoesNotClobberPriorDraft(t *testing.T) {
	t.Parallel()
	// Prior good draft at version N.
	priorVersion := int64(3)
	priorLock := int64(5)
	goodGraph := validD8Graph(testToolID)
	goodGraph.UI = map[string]any{"generatedBy": GeneratedByV2, "marker": "prior-good"}

	// Fake LLM returns hallucinated toolId on this turn.
	badGraph := validD8Graph("118f1f2e-7b5a-7c3d-8e9f-aaaaaaaaaaaa")
	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{badGraph}}
	drafts := &fakeDraftStore{
		// seed prior as if already persisted
		lastDraft: workflow.Draft{
			ID: testDraftID, WorkspaceID: testWorkspaceID, CapabilityID: testWorkflowID,
			DraftVersion: priorVersion, SchemaVersion: SchemaVersion, LockVersion: priorLock,
		},
	}

	svc := newTestTurnService(t, turnTestDeps{
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	result, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		Message:     "改成调用不存在的工具",
		CreatedBy:   testCreatorID,
		Prior: &PriorDraft{
			WorkflowID:   testWorkflowID,
			DraftID:      testDraftID,
			DraftVersion: priorVersion,
			LockVersion:  priorLock,
			Graph:        goodGraph,
		},
	})
	if !errors.Is(err, ErrGuardRejected) {
		t.Fatalf("want ErrGuardRejected, got %v", err)
	}
	var guardErr *GuardError
	if !errors.As(err, &guardErr) || guardErr.Report.OK {
		t.Fatalf("want GuardError with failed report, got %v", err)
	}
	if !hasViolationCode(guardErr.Report, "HALLUCINATED_TOOL_ID") {
		t.Fatalf("want HALLUCINATED_TOOL_ID, got %+v", guardErr.Report)
	}
	// Critical: UpdateDraft/Create must NOT be called; version stays N.
	if drafts.createCalls != 0 || drafts.updateCalls != 0 {
		t.Fatalf("guard failure clobbered draft: create=%d update=%d", drafts.createCalls, drafts.updateCalls)
	}
	if drafts.lastDraft.DraftVersion != priorVersion || drafts.lastDraft.LockVersion != priorLock {
		t.Fatalf("prior draft version must stay %d/%d, got version=%d lock=%d",
			priorVersion, priorLock, drafts.lastDraft.DraftVersion, drafts.lastDraft.LockVersion)
	}
	if result.Audit.PromptID == "" || result.Audit.PromptHash == "" {
		t.Fatalf("audit prompt fields required even on guard reject: %+v", result.Audit)
	}
}

func TestApplyTurnSuccessCreatesDraftWithAuditMetadata(t *testing.T) {
	t.Parallel()
	good := validD8Graph(testToolID)
	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{good}}
	drafts := &fakeDraftStore{}
	svc := newTestTurnService(t, turnTestDeps{
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	result, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID:  testWorkspaceID,
		AgentID:      testAgentID,
		Message:      "查询支付状态",
		CreatedBy:    testCreatorID,
		SessionID:    "118f1f2e-7b5a-7c3d-8e9f-1234567890a1",
		GenerationID: "118f1f2e-7b5a-7c3d-8e9f-1234567890a2",
		TraceID:      "trace-audit-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if drafts.createCalls != 1 || drafts.updateCalls != 0 {
		t.Fatalf("first success should Create once: create=%d update=%d", drafts.createCalls, drafts.updateCalls)
	}
	if result.GeneratedBy != GeneratedByV2 {
		t.Fatalf("generatedBy=%q", result.GeneratedBy)
	}
	if result.Audit.PromptID != DefaultSystemPromptID || result.Audit.PromptHash == "" {
		t.Fatalf("audit meta: %+v", result.Audit)
	}
	if result.Graph.UI["promptHash"] != result.Audit.PromptHash {
		t.Fatalf("graph UI must carry promptHash: %+v", result.Graph.UI)
	}
	if result.Graph.UI["agentId"] != testAgentID {
		t.Fatalf("graph UI agentId: %+v", result.Graph.UI)
	}
	if result.Graph.UI["sessionId"] != "118f1f2e-7b5a-7c3d-8e9f-1234567890a1" {
		t.Fatalf("graph UI sessionId: %+v", result.Graph.UI)
	}
	if result.Graph.UI["traceId"] != "trace-audit-1" {
		t.Fatalf("graph UI traceId: %+v", result.Graph.UI)
	}
	if result.Graph.UI["generationId"] != "118f1f2e-7b5a-7c3d-8e9f-1234567890a2" {
		t.Fatalf("graph UI generationId: %+v", result.Graph.UI)
	}
	if result.ModelConfigID != testModelConfigID {
		t.Fatalf("model must come from agent: %s", result.ModelConfigID)
	}
	if !result.GuardReport.OK {
		t.Fatalf("guard should pass: %+v", result.GuardReport)
	}
}

// trackingWorkflowService is a DraftStore that also exposes Publish/Activate counters.
// ApplyTurn only receives DraftStore — Publish/Activate must never be invoked (D5/P4.4).
type trackingWorkflowService struct {
	fakeDraftStore
	publishCalls  int
	activateCalls int
}

func (s *trackingWorkflowService) Publish(context.Context, string, string) error {
	s.publishCalls++
	return nil
}

func (s *trackingWorkflowService) Activate(context.Context, string, string) error {
	s.activateCalls++
	return nil
}

func TestApplyTurnWithFeedbackBumpsDraftVersionNeverPublishes(t *testing.T) {
	t.Parallel()
	// UT-FEEDBACK-TURN: feedback → new draft version; no Publish/Activate (D5/D14).
	priorVersion := int64(2)
	priorLock := int64(4)
	priorGraph := validD8Graph(testToolID)
	priorGraph.UI = map[string]any{"generatedBy": GeneratedByV2, "marker": "prior"}

	nextGraph := validD8Graph(testToolID)
	nextGraph.Nodes = append(nextGraph.Nodes[:2], append([]domain.WorkflowGraphNode{{
		ID: "approval-1", Type: "Approval", Label: "审批",
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Direction: "input"},
			{Key: "output", Direction: "output"},
		},
		Data: map[string]any{}, UI: map[string]any{},
	}}, nextGraph.Nodes[2:]...)...)
	// Wire Approval into the path so connectivity guard passes.
	nextGraph.Edges = []domain.WorkflowGraphEdge{
		{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "tool-1", TargetPort: "input"},
		{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "approval-1", TargetPort: "input"},
		{ID: "e3", SourceNodeID: "approval-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
	}

	model := &recordingGraphModel{graphs: []domain.WorkflowGraphDraft{nextGraph}}
	drafts := &trackingWorkflowService{
		fakeDraftStore: fakeDraftStore{
			lastDraft: workflow.Draft{
				ID: testDraftID, WorkspaceID: testWorkspaceID, CapabilityID: testWorkflowID,
				DraftVersion: priorVersion, LockVersion: priorLock, SchemaVersion: SchemaVersion,
			},
		},
	}
	svc := newTestTurnService(t, turnTestDeps{
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	feedback := &FailureFeedback{
		Source:        FailureSourceCompile,
		WorkflowID:    testWorkflowID,
		CompilationID: "118f1f2e-7b5a-7c3d-8e9f-1234567890c1",
		Issues: []FailureIssue{
			{Code: "MAPPING_INVALID", NodeID: "tool-1", Message: "mapping missing orderId", SuggestedAction: SuggestedActionEditMapping},
		},
		RawSummary: "compile failed",
	}

	result, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		// Empty message: seed from feedback (D14).
		Message:   "",
		CreatedBy: testCreatorID,
		Prior: &PriorDraft{
			WorkflowID:   testWorkflowID,
			DraftID:      testDraftID,
			DraftVersion: priorVersion,
			LockVersion:  priorLock,
			Graph:        priorGraph,
		},
		Feedback: feedback,
	})
	if err != nil {
		t.Fatal(err)
	}
	if drafts.createCalls != 0 || drafts.updateCalls != 1 {
		t.Fatalf("feedback revise must UpdateDraft only: create=%d update=%d", drafts.createCalls, drafts.updateCalls)
	}
	if result.Draft.DraftVersion != priorVersion+1 {
		t.Fatalf("draftVersion want %d got %d", priorVersion+1, result.Draft.DraftVersion)
	}
	// P4.4: never auto-publish / activate.
	if drafts.publishCalls != 0 || drafts.activateCalls != 0 {
		t.Fatalf("feedback path must never Publish/Activate: publish=%d activate=%d", drafts.publishCalls, drafts.activateCalls)
	}
	revised, ok := result.Graph.UI["revisedFrom"].(map[string]any)
	if !ok || revised["source"] != FailureSourceCompile {
		t.Fatalf("ui.revisedFrom missing or wrong: %+v", result.Graph.UI)
	}
	if revised["compilationId"] != feedback.CompilationID {
		t.Fatalf("revisedFrom.compilationId: %+v", revised)
	}
	if len(model.inputs) != 1 || model.inputs[0].Feedback == nil {
		t.Fatalf("model must receive feedback: %+v", model.inputs)
	}
	if model.inputs[0].Message == "" {
		t.Fatal("synthesized revision message required")
	}
	if !strings.Contains(model.inputs[0].Message, "MAPPING_INVALID") && !strings.Contains(model.inputs[0].Message, "编译") {
		t.Fatalf("message should carry feedback context: %s", model.inputs[0].Message)
	}
}

func TestApplyTurnRejectsInvalidFeedback(t *testing.T) {
	t.Parallel()
	drafts := &trackingWorkflowService{}
	svc := newTestTurnService(t, turnTestDeps{
		model:  &scriptedGraphModel{},
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})
	_, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		Message:     "revise",
		CreatedBy:   testCreatorID,
		Feedback: &FailureFeedback{
			Source:     "not-a-source",
			WorkflowID: testWorkflowID,
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if drafts.createCalls != 0 || drafts.updateCalls != 0 || drafts.publishCalls != 0 {
		t.Fatal("invalid feedback must not write draft or publish")
	}
}

func TestApplyTurnSuccessUpdatesPriorDraft(t *testing.T) {
	t.Parallel()
	priorVersion := int64(1)
	priorLock := int64(1)
	nextGraph := validD8Graph(testToolID)
	// Add Approval for second turn shape difference (still D8).
	nextGraph.Nodes = append(nextGraph.Nodes[:2], append([]domain.WorkflowGraphNode{{
		ID: "approval-1", Type: "Approval", Label: "审批",
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Direction: "input"},
			{Key: "output", Direction: "output"},
		},
		Data: map[string]any{}, UI: map[string]any{},
	}}, nextGraph.Nodes[2:]...)...)
	nextGraph.Edges = []domain.WorkflowGraphEdge{
		{ID: "e1", SourceNodeID: "start", SourcePort: "output", TargetNodeID: "tool-1", TargetPort: "input"},
		{ID: "e2", SourceNodeID: "tool-1", SourcePort: "output", TargetNodeID: "approval-1", TargetPort: "input"},
		{ID: "e3", SourceNodeID: "approval-1", SourcePort: "output", TargetNodeID: "end", TargetPort: "input"},
	}

	model := &scriptedGraphModel{graphs: []domain.WorkflowGraphDraft{nextGraph}}
	drafts := &fakeDraftStore{
		lastDraft: workflow.Draft{
			ID: testDraftID, WorkspaceID: testWorkspaceID, CapabilityID: testWorkflowID,
			DraftVersion: priorVersion, LockVersion: priorLock, SchemaVersion: SchemaVersion,
		},
	}
	svc := newTestTurnService(t, turnTestDeps{
		model:  model,
		drafts: drafts,
		agents: usableAgentLookup(),
		models: usableModelLookup(),
		tools:  publishedToolCatalog(),
	})

	result, err := svc.ApplyTurn(context.Background(), ApplyTurnRequest{
		WorkspaceID: testWorkspaceID,
		AgentID:     testAgentID,
		Message:     "加审批节点",
		CreatedBy:   testCreatorID,
		Prior: &PriorDraft{
			WorkflowID:   testWorkflowID,
			DraftID:      testDraftID,
			DraftVersion: priorVersion,
			LockVersion:  priorLock,
			Graph:        validD8Graph(testToolID),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if drafts.createCalls != 0 || drafts.updateCalls != 1 {
		t.Fatalf("revise path should UpdateDraft only: create=%d update=%d", drafts.createCalls, drafts.updateCalls)
	}
	if result.Draft.DraftVersion != priorVersion+1 {
		t.Fatalf("draft version want %d got %d", priorVersion+1, result.Draft.DraftVersion)
	}
}

// --- fakes ---

type scriptedGraphModel struct {
	graphs        []domain.WorkflowGraphDraft
	GenerateCalls int
	err           error
}

func (m *scriptedGraphModel) GenerateGraph(context.Context, GraphModelInput) (domain.WorkflowGraphDraft, error) {
	m.GenerateCalls++
	if m.err != nil {
		return domain.WorkflowGraphDraft{}, m.err
	}
	if len(m.graphs) == 0 {
		return domain.WorkflowGraphDraft{}, errors.New("no scripted graphs")
	}
	g := m.graphs[0]
	if len(m.graphs) > 1 {
		m.graphs = m.graphs[1:]
	}
	return g, nil
}

type fakeDraftStore struct {
	createCalls int
	updateCalls int
	lastDraft   workflow.Draft
	createErr   error
	updateErr   error
}

func (s *fakeDraftStore) Create(_ context.Context, input workflow.CreateInput) (workflow.Workflow, workflow.Draft, error) {
	s.createCalls++
	if s.createErr != nil {
		return workflow.Workflow{}, workflow.Draft{}, s.createErr
	}
	s.lastDraft = workflow.Draft{
		ID: input.DraftID, WorkspaceID: input.WorkspaceID, CapabilityID: input.CapabilityID,
		DraftVersion: 1, SchemaVersion: input.SchemaVersion, Graph: input.Graph, LockVersion: 1,
	}
	return workflow.Workflow{
			CapabilityID: input.CapabilityID, WorkspaceID: input.WorkspaceID,
			CurrentDraftID: input.DraftID, Name: input.Name, Slug: input.Slug,
		},
		s.lastDraft, nil
}

func (s *fakeDraftStore) UpdateDraft(_ context.Context, workspaceID, capabilityID string, input workflow.DraftUpdate) (workflow.Draft, error) {
	s.updateCalls++
	if s.updateErr != nil {
		return workflow.Draft{}, s.updateErr
	}
	s.lastDraft = workflow.Draft{
		ID: s.lastDraft.ID, WorkspaceID: workspaceID, CapabilityID: capabilityID,
		DraftVersion: input.ExpectedDraftVersion + 1, SchemaVersion: input.SchemaVersion,
		Graph: input.Graph, LockVersion: input.ExpectedLockVersion + 1,
	}
	return s.lastDraft, nil
}

type turnTestDeps struct {
	model  GraphModel
	drafts DraftStore
	agents *fakeAgentLookup
	models *fakeModelLookup
	tools  ToolCatalog
}

func newTestTurnService(t *testing.T, deps turnTestDeps) *TurnService {
	t.Helper()
	gate, err := NewAgentModelGate(deps.agents, deps.models)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{testWorkflowID, testDraftID}
	svc, err := NewTurnService(TurnServiceDeps{
		Model:   deps.model,
		Drafts:  deps.drafts,
		Prompts: NewMemorySystemPromptStore(),
		Gate:    gate,
		Tools:   deps.tools,
		NextID: func() (string, error) {
			if len(ids) == 0 {
				return "", errors.New("no more ids")
			}
			v := ids[0]
			ids = ids[1:]
			return v, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func usableAgentLookup() *fakeAgentLookup {
	return &fakeAgentLookup{byKey: map[string]agent.Agent{
		testWorkspaceID + "/" + testAgentID: {
			ID: testAgentID, WorkspaceID: testWorkspaceID,
			ModelConfigID: testModelConfigID, Status: agent.StatusActive,
		},
	}}
}

func usableModelLookup() *fakeModelLookup {
	return &fakeModelLookup{cfg: modelconfig.Config{
		ID: testModelConfigID, WorkspaceID: testWorkspaceID,
		APIBase: "https://example.com/v1", ModelName: "gpt-test",
		Status: modelconfig.StatusVerified,
	}}
}

func publishedToolCatalog() *fakeToolCatalog {
	releaseID := testToolRelease
	return &fakeToolCatalog{values: []tool.Tool{
		{
			CapabilityID: testToolID, WorkspaceID: testWorkspaceID,
			Name: "查询支付状态", Status: "ACTIVE", ActiveReleaseID: &releaseID,
		},
	}}
}
