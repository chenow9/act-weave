package einoruntime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/workflowtranslator"
)

type stubToolInvoker struct {
	mu      sync.Mutex
	calls   int
	results map[string]map[string]any
}

func (s *stubToolInvoker) Invoke(_ context.Context, call einoruntime.WorkflowToolCall) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.results != nil {
		if r, ok := s.results[call.ToolID]; ok {
			return r, nil
		}
	}
	return map[string]any{"ok": true}, nil
}

func (s *stubToolInvoker) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{data: make(map[string][]byte)}
}

func (m *memStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[id]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true, nil
}

func (m *memStore) Set(_ context.Context, id string, checkPoint []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byte, len(checkPoint))
	copy(out, checkPoint)
	m.data[id] = out
	return nil
}

func corePlanStartToolEnd() domain.CompiledExecutionPlan {
	return domain.CompiledExecutionPlan{
		WorkflowID: "wf-core-simple",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "tool", Type: "Tool", Dependencies: []string{"start"}, Config: map[string]any{
				"toolId": "order.status.query",
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"tool"}, Config: map[string]any{
				"output": map[string]any{"kind": "ref", "path": "nodeOutputs.tool.status"},
			}},
		},
	}
}

func TestCoreGraphInvokeSmallGraph(t *testing.T) {
	t.Parallel()

	invoker := &stubToolInvoker{
		results: map[string]map[string]any{
			"order.status.query": {"status": "paid"},
		},
	}
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		Invoker:         invoker,
		CheckPointStore: newMemStore(),
	})

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        corePlanStartToolEnd(),
		Input:       map[string]any{"orderId": "A1"},
		UserID:      "user-1",
		WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Interrupted {
		t.Fatal("expected no interrupt")
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s", out.Execution.Status, out.Execution.ErrorMessage)
	}
	if !strings.Contains(out.Execution.OutputSummary, "paid") {
		t.Fatalf("output=%s", out.Execution.OutputSummary)
	}
	if invoker.callCount() != 1 {
		t.Fatalf("tool calls=%d", invoker.callCount())
	}
	// Steps should include Runtime Call aligned with executors_core.
	foundTool := false
	for _, step := range out.Execution.Steps {
		if step.NodeID == "tool" && step.Status == domain.ExecutionStepPassed {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("missing tool step: %#v", out.Execution.Steps)
	}
}

func TestCoreGraphApprovalInterruptThenResume(t *testing.T) {
	t.Parallel()

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-approval",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "approval", Type: "Approval", Dependencies: []string{"start"}, Config: map[string]any{
				"reason": "manual check",
			}},
			{NodeID: "transform", Type: "Transform", Dependencies: []string{"approval"}, Config: map[string]any{
				"template": "approved {{input.orderId}}",
			}},
			{NodeID: "end", Type: "End", Dependencies: []string{"transform"}, Config: map[string]any{
				"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.result"},
			}},
		},
	}

	store := newMemStore()
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		Invoker:         &stubToolInvoker{},
		CheckPointStore: store,
	})

	req := einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"orderId": "ORD-9"},
		UserID:      "user-chen",
		WorkspaceID: "ws-order",
	}

	first, err := runner.Invoke(context.Background(), req)
	if err != nil {
		t.Fatalf("first Invoke: %v", err)
	}
	if !first.Interrupted {
		t.Fatalf("expected Approval interrupt, got status=%s", first.Execution.Status)
	}
	if first.Execution.Status != domain.ExecutionApproval {
		t.Fatalf("status=%s want Approval", first.Execution.Status)
	}
	if first.CheckPointID == "" {
		t.Fatal("expected checkpoint id")
	}
	if len(first.InterruptIDs) == 0 {
		t.Fatal("expected interrupt ids for resume")
	}
	if first.Approval == nil || first.Approval.NodeID != "approval" {
		t.Fatalf("approval state=%#v", first.Approval)
	}
	// WaitingApproval step present.
	waiting := false
	for _, step := range first.Execution.Steps {
		if step.NodeType == "Approval" && step.Status == domain.ExecutionStepWaitingApproval {
			waiting = true
		}
	}
	if !waiting {
		t.Fatalf("missing WaitingApproval step: %#v", first.Execution.Steps)
	}

	// Compose resume — not whole-plan re-run.
	second, err := runner.ResumeApproval(
		context.Background(),
		req,
		first.CheckPointID,
		einoruntime.ApprovalDecision{
			Decision:   einoruntime.ApprovalDecisionConfirmed,
			ResolvedBy: "approver-1",
		},
		first.InterruptIDs...,
	)
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	if second.Interrupted {
		t.Fatalf("unexpected re-interrupt: ids=%v status=%s", second.InterruptIDs, second.Execution.Status)
	}
	if second.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("resume status=%s err=%s steps=%#v", second.Execution.Status, second.Execution.ErrorMessage, second.Execution.Steps)
	}
	if !strings.Contains(second.Execution.OutputSummary, "ORD-9") {
		t.Fatalf("output=%s", second.Execution.OutputSummary)
	}
}

func TestCoreGraphCacheHit(t *testing.T) {
	t.Parallel()

	plan := corePlanStartToolEnd()
	store := newMemStore()
	cache := einoruntime.NewGraphCache(einoruntime.GraphBuildConfig{
		Invoker:         &stubToolInvoker{results: map[string]map[string]any{"order.status.query": {"status": "ok"}}},
		CheckPointStore: store,
		Engine:          workflowtranslator.EngineEinoCore,
	})
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		Invoker:         &stubToolInvoker{results: map[string]map[string]any{"order.status.query": {"status": "ok"}}},
		CheckPointStore: store,
		Cache:           cache,
	})

	key := einoruntime.CacheKeyFor("ws-1", "rev-1", "hash-abc", workflowtranslator.EngineEinoCore)
	req := einoruntime.WorkflowRunRequest{
		Plan:        plan,
		WorkspaceID: "ws-1",
		CacheKey:    key,
		UserID:      "u1",
		Input:       map[string]any{},
	}

	if _, err := runner.Invoke(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache len=%d after first build", cache.Len())
	}
	if _, err := runner.Invoke(context.Background(), req); err != nil {
		t.Fatalf("second: %v", err)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache len=%d after second invoke (want hit, not second entry)", cache.Len())
	}
	// Direct Get confirms key.
	if _, ok := cache.Get(key); !ok {
		t.Fatal("expected cache hit for key")
	}
}

func TestCoreGraphRejectsUnknownNodes(t *testing.T) {
	t.Parallel()

	// ForEach is native after PR13d; unknown types still reject under eino_core.
	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-unknown",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{NodeID: "llm", Type: "LLM", Dependencies: []string{"start"}},
			{NodeID: "end", Type: "End", Dependencies: []string{"llm"}},
		},
	}
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		CheckPointStore: newMemStore(),
	})
	_, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan: plan,
	})
	if err == nil {
		t.Fatal("expected error for unknown LLM node under eino_core")
	}
	if !strings.Contains(err.Error(), "LLM") && !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error=%v", err)
	}
}

// TestCoreGraphForEachScopedSuccess mirrors plan_runner foreach loop success:
// Start → ForEach → Transform({{foreach.item}}) → End(ref transform.count).
func TestCoreGraphForEachScopedSuccess(t *testing.T) {
	t.Parallel()

	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		CheckPointStore: newMemStore(),
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-scoped",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "foreach",
				Type:         "ForEach",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"collection": map[string]any{"kind": "ref", "path": "input.items"},
					"itemAlias":  "item",
				},
			},
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.item}}",
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"foreach", "transform"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"},
				},
			},
		},
	}

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"items": []any{"line-1", "line-2"}},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-foreach",
	})
	if err != nil {
		t.Fatalf("Invoke ForEach plan: %v", err)
	}
	if out.Interrupted {
		t.Fatal("expected no interrupt for ForEach success path")
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s steps=%#v", out.Execution.Status, out.Execution.ErrorMessage, out.Execution.Steps)
	}
	if !strings.Contains(out.Execution.OutputSummary, "2") {
		t.Fatalf("expected End to expose transform.count=2, got %q", out.Execution.OutputSummary)
	}
	if out.State == nil || out.State.Scope.NodeOutputs["foreach"] == nil {
		t.Fatal("expected ForEach nodeOutputs on GraphState")
	}
	foreachOut := out.State.Scope.NodeOutputs["foreach"]
	if foreachOut["count"] != 2 {
		t.Fatalf("foreach count=%v want 2", foreachOut["count"])
	}
	if foreachOut["itemAlias"] != "item" {
		t.Fatalf("foreach itemAlias=%v", foreachOut["itemAlias"])
	}
	transformOut := out.State.Scope.NodeOutputs["transform"]
	if transformOut == nil {
		t.Fatal("expected transform loop nodeOutputs")
	}
	if transformOut["count"] != 2 {
		t.Fatalf("transform count=%v want 2", transformOut["count"])
	}
	items, _ := transformOut["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("transform items=%#v", transformOut["items"])
	}
}

// TestCoreGraphForEachAliasAndOutputMapping mirrors plan_runner
// TestPlanRunnerSupportsForeachAliasAndLoopOutputMapping.
func TestCoreGraphForEachAliasAndOutputMapping(t *testing.T) {
	t.Parallel()

	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		CheckPointStore: newMemStore(),
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-foreach-alias-output",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "foreach",
				Type:         "ForEach",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"collection":  map[string]any{"kind": "ref", "path": "input.items"},
					"itemAlias":   "lineItem",
					"concurrency": 3,
					"output": map[string]any{
						"items": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.items"},
						"count": map[string]any{"kind": "ref", "path": "nodeOutputs.transform.count"},
					},
				},
			},
			{
				NodeID:       "transform",
				Type:         "Transform",
				Dependencies: []string{"foreach"},
				Config: map[string]any{
					"template": "{{foreach.lineItem.id}}",
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"foreach", "transform"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.foreach.items"},
				},
			},
		},
	}

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan: plan,
		Input: map[string]any{
			"items": []any{
				map[string]any{"id": "line-1"},
				map[string]any{"id": "line-2"},
			},
		},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-foreach-alias",
	})
	if err != nil {
		t.Fatalf("Invoke ForEach alias plan: %v", err)
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s steps=%#v", out.Execution.Status, out.Execution.ErrorMessage, out.Execution.Steps)
	}
	if !strings.Contains(out.Execution.OutputSummary, "line-1") || !strings.Contains(out.Execution.OutputSummary, "line-2") {
		t.Fatalf("expected foreach output mapping to surface per-item results, got %q", out.Execution.OutputSummary)
	}

	var foreachStep *domain.ExecutionStepRecord
	for i := range out.Execution.Steps {
		step := &out.Execution.Steps[i]
		if step.NodeID == "foreach" && step.Status == domain.ExecutionStepPassed {
			foreachStep = step
			break
		}
	}
	if foreachStep == nil {
		t.Fatalf("missing ForEach Passed step: %#v", out.Execution.Steps)
	}
	if !strings.Contains(foreachStep.OutputSummary, "alias=lineItem") || !strings.Contains(foreachStep.OutputSummary, "concurrency=3") {
		t.Fatalf("expected foreach trace to explain alias and concurrency, got %#v", foreachStep)
	}
}

type stubRevisionResolver struct {
	revisions map[string]domain.WorkflowRevision
}

func (s stubRevisionResolver) ResolvePublishedRevision(workflowID string) (domain.WorkflowRevision, error) {
	rev, ok := s.revisions[workflowID]
	if !ok {
		return domain.WorkflowRevision{}, errors.New("workflow revision not found: " + workflowID)
	}
	return rev, nil
}

// TestCoreGraphSubWorkflowNestedSuccess: parent Start → SubWorkflow → End
// runs child Start → End via recursive CoreGraphRunner (plan_runner-aligned outputs).
func TestCoreGraphSubWorkflowNestedSuccess(t *testing.T) {
	t.Parallel()

	resolver := stubRevisionResolver{
		revisions: map[string]domain.WorkflowRevision{
			"wf-fulfillment": {
				WorkflowID: "wf-fulfillment",
				RevisionID: "rev-child-1",
				Plan: domain.CompiledExecutionPlan{
					WorkflowID: "wf-fulfillment",
					Nodes: []domain.ExecutionPlanNode{
						{NodeID: "start", Type: "Start"},
						{
							NodeID:       "child-end",
							Type:         "End",
							Dependencies: []string{"start"},
							Config: map[string]any{
								"output": map[string]any{"kind": "ref", "path": "input.orderId"},
							},
						},
					},
				},
			},
		},
	}
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		RevisionResolver: resolver,
		CheckPointStore:  newMemStore(),
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parent-sub",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"workflowId": "wf-fulfillment",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"sub"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.sub.status"},
				},
			},
		},
	}

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"orderId": "A10293"},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-sub",
	})
	if err != nil {
		t.Fatalf("Invoke nested SubWorkflow: %v", err)
	}
	if out.Interrupted {
		t.Fatal("expected no interrupt for nested success path")
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s steps=%#v", out.Execution.Status, out.Execution.ErrorMessage, out.Execution.Steps)
	}
	if !strings.Contains(out.Execution.OutputSummary, "Success") {
		t.Fatalf("expected End to expose sub.status=Success, got %q", out.Execution.OutputSummary)
	}

	foundSub := false
	foundChildEnd := false
	for _, step := range out.Execution.Steps {
		if step.NodeID == "sub" && step.Status == domain.ExecutionStepPassed {
			foundSub = true
			if !strings.Contains(step.OutputSummary, "rev-child-1") {
				t.Fatalf("sub step should mention child revision, got %q", step.OutputSummary)
			}
		}
		if step.NodeID == "child-end" {
			foundChildEnd = true
		}
	}
	if !foundSub {
		t.Fatalf("missing SubWorkflow Passed step: %#v", out.Execution.Steps)
	}
	if !foundChildEnd {
		t.Fatalf("missing merged child-end step: %#v", out.Execution.Steps)
	}
	if out.State == nil || out.State.Scope.NodeOutputs["sub"] == nil {
		t.Fatal("expected SubWorkflow nodeOutputs on GraphState")
	}
	subOut := out.State.Scope.NodeOutputs["sub"]
	if subOut["workflowId"] != "wf-fulfillment" || subOut["revisionId"] != "rev-child-1" {
		t.Fatalf("sub nodeOutputs=%#v", subOut)
	}
	if subOut["status"] != string(domain.ExecutionSuccess) {
		t.Fatalf("sub status=%v", subOut["status"])
	}
}

// TestCoreGraphSubWorkflowNestedApprovalBubblesViaCompositeInterrupt proves
// strategy C: child Approval interrupt bubbles to parent Invoke as soft
// Interrupted with root-cause Approval + interrupt IDs (PR13c).
func TestCoreGraphSubWorkflowNestedApprovalBubblesViaCompositeInterrupt(t *testing.T) {
	t.Parallel()

	resolver := stubRevisionResolver{
		revisions: map[string]domain.WorkflowRevision{
			"wf-child-approval": {
				WorkflowID: "wf-child-approval",
				RevisionID: "rev-child-appr",
				Plan: domain.CompiledExecutionPlan{
					WorkflowID: "wf-child-approval",
					Nodes: []domain.ExecutionPlanNode{
						{NodeID: "start", Type: "Start"},
						{
							NodeID:       "approval",
							Type:         "Approval",
							Dependencies: []string{"start"},
							Config:       map[string]any{"reason": "nested manager sign-off"},
						},
						{
							NodeID:       "end",
							Type:         "End",
							Dependencies: []string{"approval"},
							Config: map[string]any{
								"output": map[string]any{"kind": "literal", "value": "approved"},
							},
						},
					},
				},
			},
		},
	}
	store := newMemStore()
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		RevisionResolver: resolver,
		CheckPointStore:  store,
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parent-nested-appr",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "sub",
				Type:         "SubWorkflow",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"workflowId": "wf-child-approval",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"sub"},
				Config: map[string]any{
					"output": map[string]any{"kind": "literal", "value": "done"},
				},
			},
		},
	}

	first, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"orderId": "A10293"},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-nested-appr",
	})
	if err != nil {
		t.Fatalf("Invoke nested Approval: %v", err)
	}
	if !first.Interrupted {
		t.Fatalf("expected nested Approval to bubble interrupt, status=%s", first.Execution.Status)
	}
	if first.Execution.Status != domain.ExecutionApproval {
		t.Fatalf("status=%s want Approval", first.Execution.Status)
	}
	if len(first.InterruptIDs) == 0 {
		t.Fatal("expected bubbled interrupt IDs for CompositeInterrupt root cause")
	}
	if first.Approval == nil {
		t.Fatal("expected root-cause ApprovalInterruptState from nested child")
	}
	if first.Approval.NodeID != "approval" {
		t.Fatalf("approval node id=%q want approval (child)", first.Approval.NodeID)
	}
	if !strings.Contains(first.Approval.Reason, "nested manager") {
		t.Fatalf("approval reason=%q", first.Approval.Reason)
	}
	if first.InterruptErr == nil {
		t.Fatal("expected InterruptErr for nested CompositeInterrupt funnel")
	}

	// WaitingApproval step from child should be merged into parent execution.
	foundWaiting := false
	for _, step := range first.Execution.Steps {
		if step.NodeType == "Approval" && step.Status == domain.ExecutionStepWaitingApproval {
			foundWaiting = true
			break
		}
	}
	if !foundWaiting {
		t.Fatalf("missing nested WaitingApproval step: %#v", first.Execution.Steps)
	}

	// Resume via parent graph (CompositeInterrupt + SubWorkflowInterruptState path).
	second, err := runner.ResumeApproval(
		context.Background(),
		einoruntime.WorkflowRunRequest{
			Plan:                plan,
			Input:               map[string]any{"orderId": "A10293"},
			UserID:              "user-chen-ops",
			WorkspaceID:         "ws-nested-appr",
			WorkflowExecutionID: first.Execution.ID,
		},
		first.CheckPointID,
		einoruntime.ApprovalDecision{
			Decision:   einoruntime.ApprovalDecisionConfirmed,
			ResolvedBy: "manager-1",
		},
		first.InterruptIDs...,
	)
	if err != nil {
		t.Fatalf("ResumeApproval nested: %v", err)
	}
	if second.Interrupted {
		t.Fatalf("unexpected re-interrupt after nested resume: ids=%v status=%s", second.InterruptIDs, second.Execution.Status)
	}
	if second.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("after nested resume status=%s err=%s steps=%#v",
			second.Execution.Status, second.Execution.ErrorMessage, second.Execution.Steps)
	}
}

// TestCoreGraphHTTPNativeSimulation mirrors plan_runner executeHTTPNode:
// Start → HTTP → End(ref nodeOutputs.http.status). No real network; status=ok.
func TestCoreGraphHTTPNativeSimulation(t *testing.T) {
	t.Parallel()

	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		CheckPointStore: newMemStore(),
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-http-native",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "http",
				Type:         "HTTP",
				Dependencies: []string{"start"},
				Config: map[string]any{
					"method":   "POST",
					"endpoint": "/orders/check",
					"input": map[string]any{
						"orderId": map[string]any{"kind": "ref", "path": "input.orderId"},
					},
				},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"http"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.http.status"},
				},
			},
		},
	}

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"orderId": "A10293"},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-http",
	})
	if err != nil {
		t.Fatalf("Invoke HTTP plan: %v", err)
	}
	if out.Interrupted {
		t.Fatal("expected no interrupt for HTTP-only plan")
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s steps=%#v", out.Execution.Status, out.Execution.ErrorMessage, out.Execution.Steps)
	}
	if !strings.Contains(out.Execution.OutputSummary, "ok") {
		t.Fatalf("expected End to expose http.status=ok, got %q", out.Execution.OutputSummary)
	}

	var httpStep *domain.ExecutionStepRecord
	for i := range out.Execution.Steps {
		step := &out.Execution.Steps[i]
		if step.NodeID == "http" && step.Status == domain.ExecutionStepPassed {
			httpStep = step
			break
		}
	}
	if httpStep == nil {
		t.Fatalf("missing HTTP Passed step: %#v", out.Execution.Steps)
	}
	if !strings.Contains(httpStep.InputSummary, "POST") || !strings.Contains(httpStep.InputSummary, "/orders/check") {
		t.Fatalf("HTTP step input summary want METHOD endpoint, got %q", httpStep.InputSummary)
	}
	if !strings.Contains(httpStep.OutputSummary, "A10293") {
		t.Fatalf("HTTP step output should summarize resolved request, got %q", httpStep.OutputSummary)
	}
	if out.State == nil || out.State.Scope.NodeOutputs["http"] == nil {
		t.Fatal("expected HTTP nodeOutputs on GraphState")
	}
	httpOut := out.State.Scope.NodeOutputs["http"]
	if httpOut["method"] != "POST" || httpOut["endpoint"] != "/orders/check" || httpOut["status"] != "ok" {
		t.Fatalf("HTTP nodeOutputs=%#v", httpOut)
	}
	req, _ := httpOut["request"].(map[string]any)
	if req == nil || req["orderId"] != "A10293" {
		t.Fatalf("HTTP request should resolve orderId, got %#v", httpOut["request"])
	}
}

// TestCoreGraphParallelFanOutJoin mirrors plan_runner_test Parallel shape:
// Start → Parallel → (risk-check | inventory-sync) → End(join).
// Branch tools must both invoke; End reads nodeOutputs.parallel.branchCount.
func TestCoreGraphParallelFanOutJoin(t *testing.T) {
	t.Parallel()

	invoker := &stubToolInvoker{
		results: map[string]map[string]any{
			"risk.check":     {"status": "ok"},
			"inventory.sync": {"status": "queued"},
		},
	}
	runner := einoruntime.NewCoreGraphRunner(einoruntime.CoreGraphRunnerConfig{
		Invoker:         invoker,
		CheckPointStore: newMemStore(),
	})

	plan := domain.CompiledExecutionPlan{
		WorkflowID: "wf-parallel-native",
		Nodes: []domain.ExecutionPlanNode{
			{NodeID: "start", Type: "Start"},
			{
				NodeID:       "parallel",
				Type:         "Parallel",
				Dependencies: []string{"start"},
				Config:       map[string]any{"branches": []any{"risk-check", "inventory-sync"}},
			},
			{
				NodeID:         "risk-check",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "risk-check",
				Config:         map[string]any{"toolId": "risk.check"},
			},
			{
				NodeID:         "inventory-sync",
				Type:           "Tool",
				Dependencies:   []string{"parallel"},
				IncomingBranch: "inventory-sync",
				Config:         map[string]any{"toolId": "inventory.sync"},
			},
			{
				NodeID:       "end",
				Type:         "End",
				Dependencies: []string{"risk-check", "inventory-sync"},
				Config: map[string]any{
					"output": map[string]any{"kind": "ref", "path": "nodeOutputs.parallel.branchCount"},
				},
			},
		},
	}

	out, err := runner.Invoke(context.Background(), einoruntime.WorkflowRunRequest{
		Plan:        plan,
		Input:       map[string]any{"orderId": "A10293"},
		UserID:      "user-chen-ops",
		WorkspaceID: "ws-parallel",
	})
	if err != nil {
		t.Fatalf("Invoke Parallel plan: %v", err)
	}
	if out.Interrupted {
		t.Fatal("expected no interrupt for Parallel-only plan")
	}
	if out.Execution.Status != domain.ExecutionSuccess {
		t.Fatalf("status=%s err=%s steps=%#v", out.Execution.Status, out.Execution.ErrorMessage, out.Execution.Steps)
	}
	if invoker.callCount() != 2 {
		t.Fatalf("expected both Parallel branch tools to run, calls=%d", invoker.callCount())
	}
	if !strings.Contains(out.Execution.OutputSummary, "2") {
		t.Fatalf("expected End to expose branchCount=2, got %q", out.Execution.OutputSummary)
	}

	// Parallel step records branch labels (aligned with plan_runner Parallel trace).
	var parallelOut string
	foundBranches := map[string]bool{}
	for _, step := range out.Execution.Steps {
		if step.NodeID == "parallel" && step.Status == domain.ExecutionStepPassed {
			parallelOut = step.OutputSummary
		}
		if step.NodeID == "risk-check" && step.Status == domain.ExecutionStepPassed {
			foundBranches["risk-check"] = true
		}
		if step.NodeID == "inventory-sync" && step.Status == domain.ExecutionStepPassed {
			foundBranches["inventory-sync"] = true
		}
	}
	if !strings.Contains(parallelOut, "risk-check") || !strings.Contains(parallelOut, "inventory-sync") {
		t.Fatalf("expected Parallel step branch trace, got %q", parallelOut)
	}
	if !foundBranches["risk-check"] || !foundBranches["inventory-sync"] {
		t.Fatalf("missing branch tool steps: %#v steps=%#v", foundBranches, out.Execution.Steps)
	}
	if out.State == nil || out.State.Scope.NodeOutputs["parallel"] == nil {
		t.Fatal("expected Parallel nodeOutputs on GraphState")
	}
	mode, _ := out.State.Scope.NodeOutputs["parallel"]["mode"].(string)
	if mode != "graph-fanout" {
		t.Fatalf("Parallel mode=%q want graph-fanout", mode)
	}
}

func TestBuildWorkflowGraphFromIR(t *testing.T) {
	t.Parallel()

	plan := corePlanStartToolEnd()
	ir, err := workflowtranslator.BuildGraphIR(plan, workflowtranslator.EngineEinoCore)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := einoruntime.BuildWorkflowGraphFromIR(context.Background(), ir, einoruntime.GraphBuildConfig{
		Invoker: &stubToolInvoker{results: map[string]map[string]any{"order.status.query": {"status": "x"}}},
	})
	if err != nil {
		t.Fatalf("BuildWorkflowGraphFromIR: %v", err)
	}
	if compiled.Runnable == nil {
		t.Fatal("nil runnable")
	}
}

func TestApprovalInterruptStateIDsOnly(t *testing.T) {
	t.Parallel()
	if einoruntime.ApprovalInterruptRegisterName != "actweave_workflow_approval_v1" {
		t.Fatalf("register name changed: %s", einoruntime.ApprovalInterruptRegisterName)
	}
}

func TestSubWorkflowInterruptRegisterNameStable(t *testing.T) {
	t.Parallel()
	if einoruntime.SubWorkflowInterruptRegisterName != "actweave_workflow_subworkflow_v1" {
		t.Fatalf("register name changed: %s", einoruntime.SubWorkflowInterruptRegisterName)
	}
	if einoruntime.SubWorkflowInterruptSchemaVersion == "" {
		t.Fatal("SubWorkflowInterruptSchemaVersion must be non-empty")
	}
}
