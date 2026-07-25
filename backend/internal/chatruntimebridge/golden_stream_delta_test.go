package chatruntimebridge_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// PR8 bridge golden: true Stream → StreamDeltaRecorder → TextDeltaSink
// (item.delta equivalent). Offline, no DB / network.
//
// Complements einoruntime A.1/A.2 goldens by proving the bridge projector
// surface used in production drive() for D14 multi-delta delivery.

// Matches protocolschema/testdata/aap/v1/text.jsonl completed content.
const bridgeGoldenTextFinal = "你好，欢迎使用 ActWeave。"

var bridgeGoldenTextChunks = []string{"你好，", "欢迎使用 ActWeave。"}

func TestGoldenA1_BridgeStreamDeltaRecorder_ItemDeltaOrder(t *testing.T) {
	ctx := context.Background()
	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{contentChunks: append([]string(nil), bridgeGoldenTextChunks...)},
		},
	}
	agent, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name:  "bridge-golden-text",
		Model: fake,
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	projector := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{})
	result, err := engine.Run(ctx, agent, einoruntime.RunInput{
		WorkspaceID: "ws-bridge-golden",
		RunID:       "run-bridge-golden-text",
		Messages:    []*schema.Message{schema.UserMessage("你好")},
		Projector:   projector,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted || result.Err != nil {
		t.Fatalf("unexpected result: interrupted=%v err=%v", result.Interrupted, result.Err)
	}

	// item.delta equivalent emissions on the TextDeltaSink (production path).
	emitted := sink.EmissionTexts()
	if len(emitted) < 1 {
		t.Fatalf("want ≥1 item.delta emission for non-empty reply, got %v", emitted)
	}
	if len(emitted) < 2 {
		t.Fatalf("want multi item.delta from true multi-chunk Stream, got %v", emitted)
	}
	if !reflect.DeepEqual(emitted, bridgeGoldenTextChunks) {
		t.Fatalf("item.delta order = %#v, want %#v", emitted, bridgeGoldenTextChunks)
	}
	if joined := strings.Join(emitted, ""); joined != bridgeGoldenTextFinal {
		t.Fatalf("joined item.delta = %q, want %q", joined, bridgeGoldenTextFinal)
	}
	if projector.Joined() != bridgeGoldenTextFinal {
		t.Fatalf("recorder joined = %q", projector.Joined())
	}
	if result.FinalAssistantText != bridgeGoldenTextFinal {
		t.Fatalf("FinalAssistantText = %q", result.FinalAssistantText)
	}
	if sink.Completion == nil || sink.Completion.Text != bridgeGoldenTextFinal {
		t.Fatalf("CompleteText = %+v, want %q", sink.Completion, bridgeGoldenTextFinal)
	}
	if sink.Failure != nil {
		t.Fatalf("unexpected FailText: %+v", sink.Failure)
	}
	// TextDelta.Index is the message content-part index (always 0 for single
	// assistant text part), matching legacy TextDeltaBatcher — not a stream counter.
	for i, e := range sink.Emissions {
		if e.Index != 0 {
			t.Fatalf("emission[%d].Index = %d, want 0 (content part index)", i, e.Index)
		}
	}
}

func TestGoldenA2_BridgeToolSuccess_DryRunNoConfirm(t *testing.T) {
	ctx := context.Background()

	const (
		capID    = "cap-weather"
		relID    = "rel-weather"
		toolName = "weather.lookup"
		args     = `{"city":"Singapore"}`
		out      = `{"temperatureC":29,"condition":"cloudy"}`
		final    = "Singapore 当前 29°C，多云。"
	)

	dry := &einoruntime.DryRunToolInvoker{
		NameByCapability: map[string]string{capID: toolName},
		OutputsByCapability: map[string]json.RawMessage{
			capID: json.RawMessage(out),
		},
	}
	pt, err := einoruntime.NewPipelineTool(einoruntime.PipelineToolConfig{
		Info:                 &schema.ToolInfo{Name: toolName, Desc: "weather"},
		Pipeline:             dry,
		RequiresConfirmation: false,
		WorkspaceID:          "ws-bridge-golden",
		CapabilityID:         capID,
		ReleaseID:            relID,
		ActorType:            "USER",
		ActorID:              "user-1",
		TraceID:              "golden-tool",
		AgentRunID:           "run-bridge-golden-tool",
		InvocationID:         "inv-weather",
		Resolved: execution.ResolvedInvocation{
			Snapshot: execution.ReleaseSnapshot{
				WorkspaceID: "ws-bridge-golden", CapabilityID: capID, ReleaseID: relID,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	fake := &scriptedStreamModel{
		turns: []scriptedTurn{
			{
				toolCalls: []schema.ToolCall{{
					ID: "call_weather", Type: "function",
					Function: schema.FunctionCall{Name: toolName, Arguments: args},
				}},
			},
			{contentChunks: []string{"Singapore 当前 ", "29°C，多云。"}},
		},
	}
	agent, err := einoruntime.BuildChatModelAgent(ctx, einoruntime.AgentBuildConfig{
		Name:  "bridge-golden-tool",
		Model: fake,
		Tools: []tool.BaseTool{pt},
	})
	if err != nil {
		t.Fatalf("BuildChatModelAgent: %v", err)
	}

	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	projector := &chatruntimebridge.StreamDeltaRecorder{Sink: sink}
	engine := einoruntime.NewEngine(einoruntime.EngineConfig{Store: newMemStore()})
	result, err := engine.Run(ctx, agent, einoruntime.RunInput{
		WorkspaceID: "ws-bridge-golden",
		RunID:       "run-bridge-golden-tool",
		Messages:    []*schema.Message{schema.UserMessage("weather?")},
		Projector:   projector,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatal("tool success must not interrupt")
	}
	if dry.CallCount() != 1 {
		t.Fatalf("DryRun invokes = %d, want 1", dry.CallCount())
	}
	if names := dry.RecordedToolNames(); len(names) != 1 || names[0] != toolName {
		t.Fatalf("tool names = %v, want [%s]", names, toolName)
	}
	if got := dry.RecordedArgsJSON(); len(got) != 1 || !jsonArgsEqual(t, got[0], args) {
		t.Fatalf("tool args = %v, want [%s]", got, args)
	}
	if result.FinalAssistantText != final {
		t.Fatalf("final text = %q, want %q", result.FinalAssistantText, final)
	}
	if joined := strings.Join(sink.EmissionTexts(), ""); joined != final {
		t.Fatalf("item.delta join = %q, want %q", joined, final)
	}
	if len(sink.EmissionTexts()) < 2 {
		t.Fatalf("want multi item.delta post-tool, got %v", sink.EmissionTexts())
	}
}

func jsonArgsEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		t.Fatalf("b: %v", err)
	}
	return reflect.DeepEqual(va, vb)
}

// Compile-time: RecordingTextDeltaSink is a TextDeltaSink.
var _ chatruntime.TextDeltaSink = (*chatruntimebridge.RecordingTextDeltaSink)(nil)
