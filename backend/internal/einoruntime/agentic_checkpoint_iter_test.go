package einoruntime

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// agenticHITLTool interrupts on first call and returns resume data on second.
type agenticHITLTool struct {
	name string
}

func (t *agenticHITLTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "HITL interrupt tool",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"q": {Type: schema.String, Required: true},
		}),
	}, nil
}

func (t *agenticHITLTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	wasInterrupted, _, _ := tool.GetInterruptState[any](ctx)
	if !wasInterrupted {
		return "", tool.Interrupt(ctx, "need_approval")
	}
	isResume, hasData, data := tool.GetResumeContext[string](ctx)
	if isResume && hasData {
		return "approved:" + data, nil
	}
	return "resumed_no_data", nil
}

// TestAgenticCheckpoint_ConsumedIterationsDoNotReset proves that
// RemainingIterations in pinned Eino agentic state survives checkpoint resume.
// If iterations reset, total model calls after interrupt+resume+exhaust would
// exceed MaxIterations (8); with correct persistence they equal 8.
func TestAgenticCheckpoint_ConsumedIterationsDoNotReset(t *testing.T) {
	ctx := context.Background()

	hitl := &agenticHITLTool{name: "hitl_tool"}
	echo := &stubTool{name: "echo_tool", desc: "echo", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: hitl, Exposure: ToolExposureDeferred},
		{Tool: echo, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Script: first model call → HITL interrupt. After resume, always call echo
	// until MaxIterations exhausts.
	responses := make([]*schema.AgenticMessage, 0, 20)
	responses = append(responses, agenticFunctionCall("hitl_tool", "hitl-1", `{"q":"need"}`))
	for i := 0; i < 16; i++ {
		responses = append(responses, agenticFunctionCall("echo_tool", fmt.Sprintf("e-%d", i), `{"q":"x"}`))
	}
	mdl := &scriptedAgenticModel{responses: responses}

	// Track actual Generate/Stream count.
	counting := &countingAgenticModel{inner: mdl}

	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(counting, []tool.BaseTool{hitl, echo}, cat))
	if err != nil {
		t.Fatal(err)
	}

	store := newMemCheckPointStore()
	cpID, err := EnsureAgentRunCheckpointID("ws-iter", "run-iter", "")
	if err != nil {
		t.Fatal(err)
	}

	runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: false,
		CheckPointStore: store,
	})

	// --- First run until interrupt ---
	iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("start")}, adk.WithCheckPointID(cpID))
	var interruptIDs []string
	var firstErr error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			firstErr = ev.Err
			break
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			for _, ic := range ev.Action.Interrupted.InterruptContexts {
				if ic != nil && ic.ID != "" {
					interruptIDs = append(interruptIDs, ic.ID)
				}
			}
		}
	}
	if firstErr != nil {
		t.Fatalf("first run hard error: %v", firstErr)
	}
	if len(interruptIDs) == 0 {
		t.Fatal("expected HITL interrupt")
	}
	callsAfterInterrupt := counting.calls.Load()
	if callsAfterInterrupt != 1 {
		t.Fatalf("model calls at interrupt = %d, want 1", callsAfterInterrupt)
	}

	// --- Resume: continue until max iterations ---
	targets := map[string]any{}
	for _, id := range interruptIDs {
		targets[id] = "yes"
	}
	iter2, err := runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{Targets: targets})
	if err != nil {
		t.Fatalf("ResumeWithParams: %v", err)
	}
	var resumeErr error
	for {
		ev, ok := iter2.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			resumeErr = ev.Err
			break
		}
	}
	totalCalls := counting.calls.Load()

	// After 1 pre-interrupt model call, RemainingIterations should be 7.
	// Resume must not reset to 8. Exhausting should yield total model calls == 8
	// and the exact max-iterations / iteration-exhausted error family.
	if resumeErr == nil {
		t.Fatalf("expected max-iterations error after resume; totalCalls=%d", totalCalls)
	}
	// Hard assertion: errors.Is against adk.ErrExceedMaxIterations (raw or Join-mapped).
	// mapEngineError uses errors.Join so both sentinels remain matchable.
	if !IsMaxIterationsExceeded(resumeErr) && !IsMaxIterationsExceeded(mapEngineError(resumeErr)) {
		t.Fatalf("resumeErr=%v want errors.Is adk.ErrExceedMaxIterations (max-iteration family); totalCalls=%d",
			resumeErr, totalCalls)
	}

	// Cumulative call count must equal MaxIterations (8), not more.
	// If checkpoint reset RemainingIterations, we would see 1 + 8 = 9 calls.
	if totalCalls != DefaultMaxIterations {
		t.Fatalf("total model calls = %d, want %d (iterations likely reset on resume if >8)",
			totalCalls, DefaultMaxIterations)
	}
}

// countingAgenticModel wraps an AgenticModel and counts Generate/Stream calls.
type countingAgenticModel struct {
	inner model.AgenticModel
	calls atomic.Int64
}

func (m *countingAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.calls.Add(1)
	return m.inner.Generate(ctx, input, opts...)
}

func (m *countingAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.calls.Add(1)
	return m.inner.Stream(ctx, input, opts...)
}
