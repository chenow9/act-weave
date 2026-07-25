package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/workflowruntime"
)

// P3.4: chatToolInvoker routes WORKFLOW ExecutorType to the published runner
// (does not call the HTTP tool pipeline).
func TestChatToolInvokerInvokesWorkflowRunner(t *testing.T) {
	runner := &stubWorkflowRunner{
		result: workflowruntime.PublishedRunResult{
			Snapshot: workflowruntime.RevisionSnapshot{
				WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
				RevisionID: "rev-1", PlanHash: "hash",
			},
			Execution: domain.Execution{
				Status: domain.ExecutionSuccess, OutputSummary: `{"ok":true}`,
			},
		},
	}
	pipeline := &countingPipeline{}
	invoker := &chatToolInvoker{
		pipeline:   nil, // unused for WORKFLOW
		workflows:  runner,
		authorizer: authorizeAll{},
	}
	// Avoid nil pipeline panic on TOOL path — only WORKFLOW exercised.
	_ = pipeline

	resolved := execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
			ExecutorType: execution.ExecutorTypeWORKFLOW, ToolVersionID: "rev-1",
		},
		Connection: execution.ConnectionSnapshot{WorkspaceID: "ws-1"},
		Credential: execution.CredentialReference{WorkspaceID: "ws-1", AuthMode: "NONE"},
	}
	result, err := invoker.InvokeResolved(context.Background(), execution.InvokeRequest{
		InvocationID: "inv-1", WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
		ActorType: "USER", ActorID: "user-1", TraceID: "trace-1",
		Input: json.RawMessage(`{"orderId":"A-1"}`), AgentRunID: "run-1",
	}, resolved)
	if err != nil {
		t.Fatalf("invoke workflow: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("workflow runner calls=%d want 1", runner.calls)
	}
	if runner.last.Input["orderId"] != "A-1" {
		t.Fatalf("input not passed to runner: %+v", runner.last.Input)
	}
	if result.Attempts != 1 || len(result.Output) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	var body map[string]any
	if err := json.Unmarshal(result.Output, &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != string(domain.ExecutionSuccess) || body["revisionId"] != "rev-1" {
		t.Fatalf("output body: %+v", body)
	}
}

func TestChatToolInvokerWorkflowRequiresRunner(t *testing.T) {
	invoker := &chatToolInvoker{authorizer: authorizeAll{}}
	_, err := invoker.InvokeResolved(context.Background(), execution.InvokeRequest{
		InvocationID: "inv-1", WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
		ActorType: "USER", ActorID: "user-1", TraceID: "trace-1",
		Input: json.RawMessage(`{}`),
	}, execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: "ws-1", CapabilityID: "cap-wf", ReleaseID: "rel-wf",
			ExecutorType: execution.ExecutorTypeWORKFLOW,
		},
	})
	if err == nil {
		t.Fatal("expected error when workflow runner is nil")
	}
}

type stubWorkflowRunner struct {
	calls  int
	last   workflowruntime.PublishedRunRequest
	result workflowruntime.PublishedRunResult
	err    error
}

func (s *stubWorkflowRunner) Run(_ context.Context, request workflowruntime.PublishedRunRequest) (workflowruntime.PublishedRunResult, error) {
	s.calls++
	s.last = request
	if s.err != nil {
		return workflowruntime.PublishedRunResult{}, s.err
	}
	return s.result, nil
}

type authorizeAll struct{}

func (authorizeAll) AuthorizeInvocation(context.Context, string, string) error { return nil }

type countingPipeline struct {
	calls int
}

func (p *countingPipeline) InvokeResolved(
	context.Context, execution.InvokeRequest, execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	p.calls++
	return execution.PipelineResult{}, errors.New("pipeline should not be used for WORKFLOW")
}
