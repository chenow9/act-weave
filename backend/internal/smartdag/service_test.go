package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"
	"actweave/backend/internal/workflowcompiler"
)

const (
	testWorkspaceID = "118f1f2e-7b5a-7c3d-8e9f-123456789001"
	testCreatorID   = "118f1f2e-7b5a-7c3d-8e9f-123456789002"
	testWorkflowID  = "118f1f2e-7b5a-7c3d-8e9f-123456789003"
	testDraftID     = "118f1f2e-7b5a-7c3d-8e9f-123456789004"
	testToolID      = "118f1f2e-7b5a-7c3d-8e9f-123456789005"
	testToolRelease = "118f1f2e-7b5a-7c3d-8e9f-123456789006"
)

func TestGenerateCreatesCanonicalV1WorkflowDraftUsingOnlyMatchingPublishedTools(t *testing.T) {
	releaseID := testToolRelease
	tools := &fakeToolCatalog{values: []tool.Tool{
		{CapabilityID: testToolID, WorkspaceID: testWorkspaceID, Name: "查询支付状态", Slug: "payment-status", Description: "查询订单支付状态", Status: "ACTIVE", ActiveReleaseID: &releaseID},
		{CapabilityID: "118f1f2e-7b5a-7c3d-8e9f-123456789007", WorkspaceID: testWorkspaceID, Name: "未发布退款", Slug: "refund-draft", Status: "ACTIVE"},
	}}
	creator := &fakeWorkflowCreator{}
	service := newTestService(t, tools, creator)

	result, err := service.Generate(context.Background(), GenerateRequest{
		WorkspaceID: testWorkspaceID, CreatedBy: testCreatorID, Goal: "查询支付状态并整理结果",
	})
	if err != nil {
		t.Fatal(err)
	}
	if creator.input.SchemaVersion != SchemaVersion || creator.input.WorkspaceID != testWorkspaceID || creator.input.CreatedBy != testCreatorID {
		t.Fatalf("unexpected create input: %+v", creator.input)
	}
	var graph domain.WorkflowGraphDraft
	if err := json.Unmarshal(creator.input.Graph, &graph); err != nil {
		t.Fatal(err)
	}
	if graph.SchemaVersion != SchemaVersion || graph.UI["generatedBy"] != "smart-dag.v1" {
		t.Fatalf("unexpected graph contract: %+v", graph)
	}
	if graph.UI["confidence"] != float64(result.Confidence) {
		t.Fatalf("generation confidence was not persisted in graph UI: graph=%+v result=%+v", graph.UI, result)
	}
	if len(result.SelectedToolIDs) != 1 || result.SelectedToolIDs[0] != testToolID || len(result.AvailableToolIDs) != 1 {
		t.Fatalf("unexpected tool selection: %+v", result)
	}
	if len(result.MissingCapabilities) != 0 {
		t.Fatalf("matched generation must not invent a missing capability: %+v", result.MissingCapabilities)
	}
	toolNodeFound := false
	for _, node := range graph.Nodes {
		if node.Type == "Tool" {
			toolNodeFound = node.Data["toolId"] == testToolID
		}
	}
	if !toolNodeFound {
		t.Fatalf("generated graph did not bind the published tool: %+v", graph.Nodes)
	}
	compiled := workflowcompiler.New().Compile(testWorkflowID, "1", graph)
	if compiled.Status != domain.WorkflowCompilationValid || len(compiled.Issues) != 0 {
		t.Fatalf("generated graph is not compiler-ready: %+v", compiled)
	}
}

func TestGenerateReturnsCapabilityGapAndStillCreatesCompilableTrialSafeDraft(t *testing.T) {
	creator := &fakeWorkflowCreator{}
	service := newTestService(t, &fakeToolCatalog{}, creator)
	result, err := service.Generate(context.Background(), GenerateRequest{
		WorkspaceID: testWorkspaceID, CreatedBy: testCreatorID, Goal: "完成供应商准入评审",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MissingCapabilities) != 1 || len(result.SelectedToolIDs) != 0 {
		t.Fatalf("expected an explicit capability gap: %+v", result)
	}
	var graph domain.WorkflowGraphDraft
	if err := json.Unmarshal(creator.input.Graph, &graph); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Type == "Tool" || node.Type == "Approval" {
			t.Fatalf("unmatched generation invented a blocking executable node: %+v", node)
		}
	}
	compiled := workflowcompiler.New().Compile(testWorkflowID, "1", graph)
	if compiled.Status != domain.WorkflowCompilationValid {
		t.Fatalf("fallback graph must remain a formal compiler-ready draft: %+v", compiled)
	}
}

func TestGenerateRejectsInvalidRequestsAndDoesNotPersist(t *testing.T) {
	creator := &fakeWorkflowCreator{}
	service := newTestService(t, &fakeToolCatalog{}, creator)
	for _, request := range []GenerateRequest{
		{},
		{WorkspaceID: testWorkspaceID, CreatedBy: testCreatorID},
		{WorkspaceID: "other", CreatedBy: testCreatorID, Goal: "goal"},
		{WorkspaceID: testWorkspaceID, CreatedBy: "other", Goal: "goal"},
	} {
		if _, err := service.Generate(context.Background(), request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
	if creator.calls != 0 {
		t.Fatalf("invalid generation persisted %d drafts", creator.calls)
	}
}

type fakeToolCatalog struct {
	values []tool.Tool
	err    error
}

func (catalog *fakeToolCatalog) List(context.Context, string) ([]tool.Tool, error) {
	return append([]tool.Tool(nil), catalog.values...), catalog.err
}

type fakeWorkflowCreator struct {
	input workflow.CreateInput
	calls int
}

func (creator *fakeWorkflowCreator) Create(_ context.Context, input workflow.CreateInput) (workflow.Workflow, workflow.Draft, error) {
	creator.input = input
	creator.calls++
	return workflow.Workflow{CapabilityID: input.CapabilityID, WorkspaceID: input.WorkspaceID, CurrentDraftID: input.DraftID, Name: input.Name, Slug: input.Slug},
		workflow.Draft{ID: input.DraftID, WorkspaceID: input.WorkspaceID, CapabilityID: input.CapabilityID, DraftVersion: 1, SchemaVersion: input.SchemaVersion, Graph: input.Graph, LockVersion: 1}, nil
}

func newTestService(t *testing.T, tools ToolCatalog, workflows WorkflowCreator) *Service {
	t.Helper()
	ids := []string{testWorkflowID, testDraftID}
	service, err := NewService(tools, workflows, func() (string, error) {
		value := ids[0]
		ids = ids[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
