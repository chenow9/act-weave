package chatruntimebridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/schema"
)

// captureStepStoreWithTransitions records append + transition for TOOL audit tests.
type captureStepStoreWithTransitions struct {
	appended    []execution.AppendAgentRunStepInput
	transitions []execution.StepTransition
}

func (s *captureStepStoreWithTransitions) AppendAgentRunStep(
	_ context.Context,
	input execution.AppendAgentRunStepInput,
) (execution.AgentRunStep, error) {
	s.appended = append(s.appended, input)
	return execution.AgentRunStep{ID: input.ID, StepType: input.StepType, Status: "RUNNING"}, nil
}

func (s *captureStepStoreWithTransitions) TransitionAgentRunStep(
	_ context.Context, _, stepID string, transition execution.StepTransition,
) (execution.AgentRunStep, error) {
	s.transitions = append(s.transitions, transition)
	return execution.AgentRunStep{ID: stepID, Status: transition.NewStatus}, nil
}

func (s *captureStepStoreWithTransitions) TransitionAgentRun(
	context.Context, string, string, execution.RunTransition,
) (execution.AgentRun, error) {
	return execution.AgentRun{}, nil
}

func (s *captureStepStoreWithTransitions) StartWorkflowExecution(
	context.Context, execution.StartWorkflowExecutionInput,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{}, nil
}

func (s *captureStepStoreWithTransitions) TransitionWorkflowExecution(
	context.Context, string, string, execution.RunTransition,
) (execution.WorkflowExecution, error) {
	return execution.WorkflowExecution{}, nil
}

func (s *captureStepStoreWithTransitions) GetAgentRun(context.Context, string, string) (execution.AgentRun, error) {
	return execution.AgentRun{}, nil
}

func TestRecordToolStep_PersistsArgsAndResultForAudit(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	bridge := &Bridge{steps: store, logger: slog.Default()}

	const (
		workspaceID  = "019f8f43-5b4d-7ac5-acb2-c74434338e97"
		runID        = "019f8f43-5bbd-736e-b27f-5ef4e7ed63fe"
		releaseID    = "019f8f43-cccc-7ac5-acb2-c74434338e03"
		capabilityID = "019f8f43-dddd-7ac5-acb2-c74434338e04"
		invocationID = "019f8f43-eeee-7ac5-acb2-c74434338e05"
	)
	err := bridge.recordToolStep(context.Background(), einoruntime.ToolCompleteEvent{
		WorkspaceID:  workspaceID,
		AgentRunID:   runID,
		CapabilityID: capabilityID,
		ReleaseID:    releaseID,
		InvocationID: invocationID,
		ToolName:     "createticket",
		ArgsJSON:     `{"sku":"SKU-PHONE-X","qty":1,"priority":"high"}`,
		ResultJSON:   `{"ok":true,"invocationId":"` + invocationID + `","output":{"id":"T-1"}}`,
		OK:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.appended) != 1 {
		t.Fatalf("appended = %d, want 1", len(store.appended))
	}
	app := store.appended[0]
	if app.StepType != "TOOL" || app.CapabilityReleaseID != releaseID {
		t.Fatalf("append = %+v", app)
	}
	var input map[string]any
	if err := json.Unmarshal(app.InputSummary, &input); err != nil {
		t.Fatal(err)
	}
	if input["toolName"] != "createticket" || input["toolCallId"] != invocationID {
		t.Fatalf("input_summary = %#v", input)
	}
	args, ok := input["arguments"].(map[string]any)
	if !ok || args["sku"] != "SKU-PHONE-X" {
		t.Fatalf("arguments = %#v", input["arguments"])
	}
	if len(store.transitions) != 1 || store.transitions[0].NewStatus != "SUCCEEDED" {
		t.Fatalf("transitions = %+v", store.transitions)
	}
	var output map[string]any
	if err := json.Unmarshal(store.transitions[0].OutputSummary, &output); err != nil {
		t.Fatal(err)
	}
	if output["ok"] != true {
		t.Fatalf("output_summary = %#v", output)
	}
}

func TestRecordToolStep_FailedMapsErrorCode(t *testing.T) {
	t.Parallel()
	store := &captureStepStoreWithTransitions{}
	bridge := &Bridge{steps: store, logger: slog.Default()}
	err := bridge.recordToolStep(context.Background(), einoruntime.ToolCompleteEvent{
		WorkspaceID:  "019f8f43-5b4d-7ac5-acb2-c74434338e97",
		AgentRunID:   "019f8f43-5bbd-736e-b27f-5ef4e7ed63fe",
		ReleaseID:    "019f8f43-cccc-7ac5-acb2-c74434338e03",
		InvocationID: "019f8f43-eeee-7ac5-acb2-c74434338e05",
		ToolName:     "reserveinventory",
		ArgsJSON:     `{"sku":"SKU-PHONE-X","qty":1}`,
		ResultJSON:   `{"ok":false,"errorCode":"UPSTREAM_FAIL","message":"boom"}`,
		OK:           false,
		ErrorCode:    "UPSTREAM_FAIL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.transitions) != 1 ||
		store.transitions[0].NewStatus != "FAILED" ||
		store.transitions[0].ErrorCode != "UPSTREAM_FAIL" {
		t.Fatalf("transition = %+v", store.transitions)
	}
}

func TestPipelineToolOnToolComplete_ForwardsArgs(t *testing.T) {
	t.Parallel()
	var seen *einoruntime.ToolCompleteEvent
	pipeline := &toolCompleteSpyInvoker{
		fn: func(_ context.Context, request execution.InvokeRequest, _ execution.ResolvedInvocation) (execution.PipelineResult, error) {
			return execution.PipelineResult{
				InvocationResult: execution.InvocationResult{
					InvocationID: request.InvocationID,
					Output:       json.RawMessage(`{"ticketId":"T-9"}`),
				},
			}, nil
		},
	}
	pt, err := einoruntime.NewPipelineTool(einoruntime.PipelineToolConfig{
		Info:         &schema.ToolInfo{Name: "createticket"},
		Pipeline:     pipeline,
		WorkspaceID:  "ws-1",
		CapabilityID: "cap-1",
		ReleaseID:    "rel-1",
		ActorType:    "USER",
		ActorID:      "user-1",
		TraceID:      "trace-1",
		AgentRunID:   "run-1",
		InvocationID: "inv-1",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-1", CapabilityID: "cap-1", ReleaseID: "rel-1",
			},
		},
		OnToolComplete: func(_ context.Context, event einoruntime.ToolCompleteEvent) {
			copyEvent := event
			seen = &copyEvent
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := pt.InvokableRun(context.Background(), `{"sku":"X","qty":1}`)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if seen == nil {
		t.Fatal("OnToolComplete not called")
	}
	if seen.ToolName != "createticket" || !seen.OK || seen.InvocationID != "inv-1" {
		t.Fatalf("event = %+v", seen)
	}
	if seen.ArgsJSON != `{"sku":"X","qty":1}` {
		t.Fatalf("ArgsJSON = %q", seen.ArgsJSON)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil || body["ok"] != true {
		t.Fatalf("result = %s err=%v", out, err)
	}
}

type toolCompleteSpyInvoker struct {
	fn func(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error)
}

func (s *toolCompleteSpyInvoker) InvokeResolved(
	ctx context.Context,
	request execution.InvokeRequest,
	resolved execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	if s.fn != nil {
		return s.fn(ctx, request, resolved)
	}
	return execution.PipelineResult{}, nil
}
