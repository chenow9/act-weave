package einoruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// PR8 golden fixtures (design Appendix A.1–A.2 + §7.2 DryRun CI).
//
// Offline, deterministic, CI-friendly: scripted true-Stream model + DryRun
// invoker (no network, no DB writes). Picked up by standard `go test ./...`.
//
// Protocolschema JSONL golden remains the AAP envelope source of truth;
// these tests assert eino engine projection semantics that must stay
// equivalent (delta order/concatenation, tool name/args order, final text).

// goldenTextFinal is the A.1 / protocolschema text.jsonl completed content.
const goldenTextFinal = "你好，欢迎使用 ActWeave。"

// goldenTextChunks are multi-chunk true Stream pieces that concatenate to
// goldenTextFinal (mirrors protocolschema text.jsonl item.delta fragments).
var goldenTextChunks = []string{"你好，", "欢迎使用 ActWeave。"}

// goldenToolFinal is the A.2 / protocolschema tool_success final assistant text.
const goldenToolFinal = "Singapore 当前 29°C，多云。"

var goldenToolFinalChunks = []string{"Singapore 当前 ", "29°C，多云。"}

const (
	goldenWeatherCapID = "cap-weather"
	goldenWeatherRelID = "rel-weather"
	goldenWeatherName  = "weather.lookup"
	goldenWeatherArgs  = `{"city":"Singapore"}`
	goldenWeatherOut   = `{"temperatureC":29,"condition":"cloudy"}`
)

// TestGoldenA1_TextTrueStreamMultiDelta asserts Appendix A.1 under eino:
// true multi-chunk Stream → ≥1 item.delta equivalent; concatenation matches
// final assistant content and the protocolschema text golden body.
func TestGoldenA1_TextTrueStreamMultiDelta(t *testing.T) {
	ctx := context.Background()
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{contentChunks: append([]string(nil), goldenTextChunks...)},
		},
	}
	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:        "golden-text-agent",
		Instruction: "You are a test agent.",
		Model:       fake,
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	rec := &RecordingProjector{}
	engine := NewEngine(EngineConfig{})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-golden-text",
		RunID:       "run-golden-text",
		Messages:    []*schema.Message{schema.UserMessage("你好")},
		Projector:   rec,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatal("A.1 text path must not interrupt")
	}
	if result.Err != nil {
		t.Fatalf("result.Err: %v", result.Err)
	}

	// D14: true Stream, not Generate pseudo-stream.
	if fake.streamCalls.Load() < 1 {
		t.Fatal("expected true Stream to be called (EnableStreaming)")
	}
	if fake.generateCalls.Load() != 0 {
		t.Fatalf("Generate must not be the streaming path; generateCalls=%d", fake.generateCalls.Load())
	}

	// A.1 验收: non-empty reply yields ≥1 item.delta equivalent.
	if len(rec.Deltas) < 1 {
		t.Fatalf("want ≥1 text delta for non-empty reply, got %v", rec.Deltas)
	}
	// Multi-chunk true stream must preserve multi-delta order (not a single Generate dump).
	if len(rec.Deltas) < 2 {
		t.Fatalf("want multi item.delta from true multi-chunk Stream, got %v", rec.Deltas)
	}
	if !reflect.DeepEqual(rec.Deltas, goldenTextChunks) {
		t.Fatalf("delta order/content = %#v, want %#v", rec.Deltas, goldenTextChunks)
	}

	joined := rec.JoinedDeltas()
	if joined != goldenTextFinal {
		t.Fatalf("joined deltas = %q, want golden %q", joined, goldenTextFinal)
	}
	if result.FinalAssistantText != goldenTextFinal {
		t.Fatalf("FinalAssistantText = %q, want golden %q", result.FinalAssistantText, goldenTextFinal)
	}
	if len(rec.Completed) != 1 || rec.Completed[0] != goldenTextFinal {
		t.Fatalf("OnTextComplete = %#v, want [%q]", rec.Completed, goldenTextFinal)
	}
}

// TestGoldenA2_ToolSuccessNoConfirm asserts Appendix A.2 under eino:
// tool item path without confirmation — exactly one DryRun invoke, tool name
// + args recorded, final assistant multi-delta text matches golden body.
func TestGoldenA2_ToolSuccessNoConfirm(t *testing.T) {
	ctx := context.Background()

	dry := &DryRunToolInvoker{
		NameByCapability: map[string]string{
			goldenWeatherCapID: goldenWeatherName,
		},
		OutputsByCapability: map[string]json.RawMessage{
			goldenWeatherCapID: json.RawMessage(goldenWeatherOut),
		},
	}
	pt, err := NewPipelineTool(PipelineToolConfig{
		Info: &schema.ToolInfo{
			Name: goldenWeatherName,
			Desc: "Lookup weather for a city",
		},
		Pipeline:             dry,
		RequiresConfirmation: false,
		WorkspaceID:          "ws-golden-tool",
		CapabilityID:         goldenWeatherCapID,
		ReleaseID:            goldenWeatherRelID,
		ActorType:            "USER",
		ActorID:              "user-golden",
		TraceID:              "golden-tool",
		AgentRunID:           "run-golden-tool",
		InvocationID:         "inv-golden-weather",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID:  "ws-golden-tool",
				CapabilityID: goldenWeatherCapID,
				ReleaseID:    goldenWeatherRelID,
			},
			RequiresConfirmation: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{{
					ID:   "call_weather",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      goldenWeatherName,
						Arguments: goldenWeatherArgs,
					},
				}},
			},
			{contentChunks: append([]string(nil), goldenToolFinalChunks...)},
		},
	}

	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:  "golden-tool-agent",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	rec := &RecordingProjector{}
	engine := NewEngine(EngineConfig{Store: newMemCheckPointStore()})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-golden-tool",
		RunID:       "run-golden-tool",
		Messages:    []*schema.Message{schema.UserMessage("Singapore weather?")},
		Projector:   rec,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatalf("A.2 tool success must not interrupt; ids=%v", result.InterruptContextIDs)
	}
	if result.Err != nil {
		t.Fatalf("result.Err: %v", result.Err)
	}

	// No confirmation path: exactly one InvokeResolved (DryRun, no pipeline).
	if got := dry.CallCount(); got != 1 {
		t.Fatalf("InvokeResolved = %d, want 1 (no confirmation)", got)
	}
	calls := dry.RecordedCalls()
	if calls[0].ToolName != goldenWeatherName {
		t.Fatalf("tool name = %q, want %q", calls[0].ToolName, goldenWeatherName)
	}
	if !jsonRawEqual(t, calls[0].Args, json.RawMessage(goldenWeatherArgs)) {
		t.Fatalf("tool args = %s, want %s", calls[0].Args, goldenWeatherArgs)
	}
	if calls[0].CapabilityID != goldenWeatherCapID || calls[0].ReleaseID != goldenWeatherRelID {
		t.Fatalf("capability/release = %s/%s", calls[0].CapabilityID, calls[0].ReleaseID)
	}

	// Final assistant text: true multi-delta stream after tool success.
	if fake.streamCalls.Load() < 1 {
		t.Fatal("expected true Stream for post-tool assistant turn")
	}
	if len(rec.Deltas) < 1 {
		t.Fatalf("want ≥1 post-tool text delta, got %v", rec.Deltas)
	}
	if rec.JoinedDeltas() != goldenToolFinal {
		t.Fatalf("joined deltas = %q, want golden %q", rec.JoinedDeltas(), goldenToolFinal)
	}
	if result.FinalAssistantText != goldenToolFinal {
		t.Fatalf("FinalAssistantText = %q, want golden %q", result.FinalAssistantText, goldenToolFinal)
	}
}

// TestDryRunCI_OfflineFixtureCompare is design §7.2 CI offline fixture compare:
// fixed capability snapshot + fake model + DryRunToolInvoker — compare tool
// names/args order/final text. Not production dual-run.
func TestDryRunCI_OfflineFixtureCompare(t *testing.T) {
	ctx := context.Background()

	// Fixed capability snapshot (run-pinned shape; not re-resolved FOLLOW_ACTIVE).
	const (
		capA  = "cap-a"
		capB  = "cap-b"
		relA  = "rel-a"
		relB  = "rel-b"
		nameA = "alpha.lookup"
		nameB = "beta.write"
	)
	// Expected call order: serial ToolsNode preserves model tool_call order.
	type expectedCall struct {
		name string
		args string
	}
	wantCalls := []expectedCall{
		{name: nameA, args: `{"q":"one"}`},
		{name: nameB, args: `{"q":"two"}`},
	}
	const wantFinal = "done: alpha then beta"

	dry := &DryRunToolInvoker{
		NameByCapability: map[string]string{
			capA: nameA,
			capB: nameB,
		},
		OutputsByCapability: map[string]json.RawMessage{
			capA: json.RawMessage(`{"v":1}`),
			capB: json.RawMessage(`{"v":2}`),
		},
	}

	toolA, err := NewPipelineTool(PipelineToolConfig{
		Info:                 &schema.ToolInfo{Name: nameA, Desc: "alpha"},
		Pipeline:             dry,
		RequiresConfirmation: false,
		WorkspaceID:          "ws-dryrun",
		CapabilityID:         capA,
		ReleaseID:            relA,
		ActorType:            "USER",
		ActorID:              "user-dry",
		TraceID:              "dryrun-ci",
		AgentRunID:           "run-dryrun",
		InvocationID:         "inv-a",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-dryrun", CapabilityID: capA, ReleaseID: relA,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolB, err := NewPipelineTool(PipelineToolConfig{
		Info:                 &schema.ToolInfo{Name: nameB, Desc: "beta"},
		Pipeline:             dry,
		RequiresConfirmation: false,
		WorkspaceID:          "ws-dryrun",
		CapabilityID:         capB,
		ReleaseID:            relB,
		ActorType:            "USER",
		ActorID:              "user-dry",
		TraceID:              "dryrun-ci",
		AgentRunID:           "run-dryrun",
		InvocationID:         "inv-b",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-dryrun", CapabilityID: capB, ReleaseID: relB,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// One model turn emits both tool calls (serial ToolsNode executes A then B),
	// then a final multi-delta assistant reply.
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{
					{
						ID: "call_a", Type: "function",
						Function: schema.FunctionCall{Name: nameA, Arguments: wantCalls[0].args},
					},
					{
						ID: "call_b", Type: "function",
						Function: schema.FunctionCall{Name: nameB, Arguments: wantCalls[1].args},
					},
				},
			},
			{contentChunks: []string{"done: ", "alpha then beta"}},
		},
	}

	agent, err := BuildChatModelAgent(ctx, AgentBuildConfig{
		Name:  "dryrun-ci-agent",
		Model: fake,
		Tools: []tool.BaseTool{toolA, toolB},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	rec := &RecordingProjector{}
	engine := NewEngine(EngineConfig{Store: newMemCheckPointStore()})
	result, err := engine.Run(ctx, agent, RunInput{
		WorkspaceID: "ws-dryrun",
		RunID:       "run-dryrun",
		Messages:    []*schema.Message{schema.UserMessage("run both tools")},
		Projector:   rec,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatal("DryRun success path must not interrupt")
	}
	if result.Err != nil {
		t.Fatalf("result.Err: %v", result.Err)
	}

	// §7.2: compare tool names / args order.
	gotCalls := dry.RecordedCalls()
	if len(gotCalls) != len(wantCalls) {
		t.Fatalf("call count = %d, want %d; calls=%+v", len(gotCalls), len(wantCalls), gotCalls)
	}
	for i, want := range wantCalls {
		if gotCalls[i].ToolName != want.name {
			t.Fatalf("call[%d] name = %q, want %q", i, gotCalls[i].ToolName, want.name)
		}
		if !jsonRawEqual(t, gotCalls[i].Args, json.RawMessage(want.args)) {
			t.Fatalf("call[%d] args = %s, want %s", i, gotCalls[i].Args, want.args)
		}
	}
	// Names helper parity.
	if names := dry.RecordedToolNames(); !reflect.DeepEqual(names, []string{nameA, nameB}) {
		t.Fatalf("RecordedToolNames = %v", names)
	}

	// Final text parity (concatenated true stream deltas).
	if result.FinalAssistantText != wantFinal {
		t.Fatalf("final text = %q, want %q", result.FinalAssistantText, wantFinal)
	}
	if rec.JoinedDeltas() != wantFinal {
		t.Fatalf("joined deltas = %q, want %q", rec.JoinedDeltas(), wantFinal)
	}
	if len(rec.Deltas) < 2 {
		t.Fatalf("want multi-delta final reply from true Stream, got %v", rec.Deltas)
	}

	// Guard: DryRun must not look like a real pipeline (no confirmation, 2 invokes).
	if dry.CallCount() != 2 {
		t.Fatalf("DryRun CallCount = %d, want 2", dry.CallCount())
	}
}

// TestDryRunToolInvoker_NeverNeedsPipeline is a unit guard for §7.2:
// DryRun returns synthetic output without requiring a live pipeline.
func TestDryRunToolInvoker_NeverNeedsPipeline(t *testing.T) {
	t.Parallel()
	dry := &DryRunToolInvoker{
		NameByCapability: map[string]string{"c1": "tool.one"},
		OutputsByCapability: map[string]json.RawMessage{
			"c1": json.RawMessage(`{"ok":1}`),
		},
	}
	result, err := dry.InvokeResolved(context.Background(), execution.InvokeRequest{
		InvocationID: "i1",
		CapabilityID: "c1",
		ReleaseID:    "r1",
		Input:        json.RawMessage(`{"x":true}`),
	}, execution.ResolvedInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != `{"ok":1}` {
		t.Fatalf("output = %s", result.Output)
	}
	if dry.CallCount() != 1 {
		t.Fatalf("calls = %d", dry.CallCount())
	}
	if dry.RecordedCalls()[0].ToolName != "tool.one" {
		t.Fatalf("name = %q", dry.RecordedCalls()[0].ToolName)
	}
}

func jsonRawEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("unmarshal a: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("unmarshal b: %v (%s)", err, b)
	}
	return reflect.DeepEqual(va, vb)
}
