package einoruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

func TestAgenticEngine_TextOnly(t *testing.T) {
	ctx := context.Background()
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{agenticmsg.AssistantText("Hello, world")},
	}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name:           "text-agent",
		Instruction:    "test",
		Model:          mdl,
		PromptCacheKey: "k-text",
		MaxIterations:  8,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{})
	result, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-text",
		Messages:    []*schema.AgenticMessage{agenticmsg.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatal("text path must not interrupt")
	}
	if result.FinalAssistantText != "Hello, world" {
		t.Fatalf("text = %q", result.FinalAssistantText)
	}
	if result.CheckpointID == "" {
		t.Fatal("expected checkpoint ID")
	}
	if mdl.calls.Load() < 1 {
		t.Fatal("expected model call")
	}
}

func TestAgenticEngine_NilTypedIteratorFailsClosed(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	result, err := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n1", nil, nil)
	if !errors.Is(err, ErrNilTypedEventIterator) {
		t.Fatalf("err = %v, want ErrNilTypedEventIterator", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result with Err set")
	}
	if !errors.Is(result.Err, ErrNilTypedEventIterator) {
		t.Fatalf("result.Err = %v", result.Err)
	}
	if result.CheckpointID != "ws/ws/agent_run/r/n1" {
		t.Fatalf("checkpoint ID = %q", result.CheckpointID)
	}
	// Must not look like a successful empty run.
	if result.Interrupted || result.FinalAssistantText != "" {
		t.Fatalf("unexpected success-shaped result: %+v", result)
	}
}

func TestAgenticEngine_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mdl := &scriptedAgenticModel{responses: []*schema.AgenticMessage{agenticmsg.AssistantText("x")}}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name: "v", Model: mdl, PromptCacheKey: "k", MaxIterations: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{})

	if _, err := engine.Run(ctx, nil, AgenticRunInput{
		WorkspaceID: "ws", RunID: "r",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("hi")},
	}); err == nil {
		t.Fatal("nil agent")
	}
	if _, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws", RunID: "r",
	}); err == nil {
		t.Fatal("empty messages")
	}
	if _, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws", RunID: "r",
		Messages: []*schema.AgenticMessage{nil},
	}); !errors.Is(err, agenticmsg.ErrNilMessage) {
		t.Fatalf("nil msg: %v", err)
	}
	// Malformed conversation: unpaired tool result.
	bad := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: "c1", Name: "t",
				Content: []*schema.FunctionToolResultContentBlock{
					{Text: &schema.UserInputText{Text: "x"}},
				},
			}),
		},
	}
	if _, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws", RunID: "r",
		Messages: []*schema.AgenticMessage{bad},
	}); err == nil {
		t.Fatal("expected conversation validation error")
	}
}

func TestAgenticEngine_ResumeRequiresTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mdl := &scriptedAgenticModel{}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name: "r", Model: mdl, PromptCacheKey: "k", MaxIterations: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	_, err = engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws",
		RunID:        "run",
		CheckpointID: "ws/ws/agent_run/run/n1",
		Targets:      nil,
	})
	if err == nil || !strings.Contains(err.Error(), "Targets") {
		// Checkpoint ID may fail parse first — either way resume without targets fails.
		if err == nil {
			t.Fatal("expected error")
		}
	}
	// Empty targets with valid-looking ID format.
	cp, err := EnsureAgentRunCheckpointID("ws-1", "run-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-1",
		RunID:        "run-1",
		CheckpointID: cp,
		Targets:      map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "Targets") {
		t.Fatalf("empty targets: %v", err)
	}
}

func TestAgenticEngine_ResumeRequiresWorkspaceAndRunOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mdl := &scriptedAgenticModel{}
	agent, err := BuildAgenticAgent(ctx, AgenticAgentBuildConfig{
		Name: "r", Model: mdl, PromptCacheKey: "k", MaxIterations: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	cp, err := EnsureAgentRunCheckpointID("ws-own", "run-own", "")
	if err != nil {
		t.Fatal(err)
	}
	// Missing workspace.
	_, err = engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "",
		RunID:        "run-own",
		CheckpointID: cp,
		Targets:      map[string]any{"x": "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("missing workspace: %v", err)
	}
	// Missing run.
	_, err = engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-own",
		RunID:        "",
		CheckpointID: cp,
		Targets:      map[string]any{"x": "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "run ID") {
		t.Fatalf("missing run: %v", err)
	}
	// Mismatched ownership.
	_, err = engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-other",
		RunID:        "run-own",
		CheckpointID: cp,
		Targets:      map[string]any{"x": "y"},
	})
	if err == nil {
		t.Fatal("expected ownership mismatch error")
	}
}

func TestAgenticEngine_MalformedNonAssistantEventFailsClosed(t *testing.T) {
	// Direct unit test of appendAgenticFinalText / Validate fail-closed contract.
	t.Parallel()
	// Malformed user message (empty role) fails Validate.
	bad := &schema.AgenticMessage{
		Role: "",
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.UserInputText{Text: "x"}),
		},
	}
	if err := agenticmsg.Validate(bad); err == nil {
		t.Fatal("expected validate failure for empty role")
	}
	// Valid tool-result style user message is ignored for final text (no error).
	// Use a simple valid user message.
	user := agenticmsg.UserText("hi")
	var parts []string
	if err := appendAgenticFinalText(&parts, user); err != nil {
		t.Fatalf("valid non-assistant: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("non-assistant must not contribute text: %v", parts)
	}
	// Function-only assistant: ErrNoAssistantText is allowed skip.
	fn := agenticFunctionCall("echo", "c1", `{"q":"x"}`)
	if err := appendAgenticFinalText(&parts, fn); err != nil {
		t.Fatalf("function turn: %v", err)
	}
	// Assistant text contributes.
	if err := appendAgenticFinalText(&parts, agenticmsg.AssistantText("hello")); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0] != "hello" {
		t.Fatalf("parts=%v", parts)
	}
}

func TestAgenticEngine_ToolReActLoop(t *testing.T) {
	ctx := context.Background()
	// Ordinary function tool (not search).
	echo := &stubTool{name: "echo_tool", desc: "echo input", params: testParams()}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: echo, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Scripted ReAct without tool-search: direct function call (immediate empty, deferred has tool).
	// Model first returns ordinary function call for echo_tool — but with deferred tools
	// the model would normally search first. For this engine smoke test we call the tool
	// directly (ToolsNode still has the executable).
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("echo_tool", "call-1", `{"q":"hi"}`),
			agenticmsg.AssistantText("done"),
		},
	}
	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{echo}, cat))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	result, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-1",
		RunID:       "run-tool",
		Messages:    []*schema.AgenticMessage{agenticmsg.UserText("use echo")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Interrupted {
		t.Fatalf("unexpected interrupt: %v", result.InterruptContextIDs)
	}
	if result.FinalAssistantText != "done" {
		t.Fatalf("text = %q", result.FinalAssistantText)
	}
	if mdl.calls.Load() < 2 {
		t.Fatalf("want ≥2 model calls, got %d", mdl.calls.Load())
	}
}

func agenticFunctionCall(name, callID, args string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.FunctionToolCall{
				Name:      name,
				CallID:    callID,
				Arguments: args,
			}),
		},
	}
}

// --- Malformed interrupt fail-closed (typed iterator adversarial) ---

func TestCollectInterruptResumeTargets_Adversarial(t *testing.T) {
	t.Parallel()

	// Nil payload.
	if _, _, err := collectInterruptResumeTargets(nil); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("nil info: %v", err)
	}
	// Empty contexts.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("empty slice: %v", err)
	}
	// Nil context entry.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{nil},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("nil entry: %v", err)
	}
	// Empty ID.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{{ID: "", IsRootCause: true}},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("empty ID: %v", err)
	}
	// Whitespace-only ID.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{{ID: "   "}},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("whitespace ID: %v", err)
	}
	// Non-canonical padded ID.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{{ID: " agent:x "}},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("padded ID: %v", err)
	}
	// Mixture: valid then nil — must fail closed (no partial IDs).
	if ids, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "agent:a;tool:t1", IsRootCause: true},
			nil,
		},
	}); err == nil || !errors.Is(err, ErrMalformedInterrupt) || ids != nil {
		t.Fatalf("mixture valid+nil: ids=%v err=%v", ids, err)
	}
	// Mixture: valid then empty ID — fail closed.
	if ids, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "agent:a;tool:t1", IsRootCause: true},
			{ID: ""},
		},
	}); err == nil || !errors.Is(err, ErrMalformedInterrupt) || ids != nil {
		t.Fatalf("mixture valid+empty: ids=%v err=%v", ids, err)
	}
	// Order: invalid first fails immediately.
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: ""},
			{ID: "agent:a;tool:t1", IsRootCause: true},
		},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("invalid first: %v", err)
	}
	// Valid multi-context: exact IDs preserved in order, roots exact.
	ids, roots, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "agent:a", IsRootCause: false},
			{ID: "agent:a;tool:t1", IsRootCause: true},
			{ID: "agent:a;tool:t2", IsRootCause: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "agent:a" || ids[1] != "agent:a;tool:t1" || ids[2] != "agent:a;tool:t2" {
		t.Fatalf("ids=%v", ids)
	}
	if len(roots) != 2 || roots[0] != "agent:a;tool:t1" || roots[1] != "agent:a;tool:t2" {
		t.Fatalf("roots=%v", roots)
	}

	// Duplicate IDs collapse in ResumeParams.Targets map — reject as malformed.
	if ids, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "dup-id", IsRootCause: true},
			{ID: "dup-id", IsRootCause: false},
		},
	}); err == nil || !errors.Is(err, ErrMalformedInterrupt) || ids != nil {
		t.Fatalf("duplicate IDs: ids=%v err=%v", ids, err)
	}
	// Duplicate after a distinct valid ID — still fail closed (no partial list).
	if ids, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "first", IsRootCause: true},
			{ID: "second"},
			{ID: "first"},
		},
	}); err == nil || !errors.Is(err, ErrMalformedInterrupt) || ids != nil {
		t.Fatalf("duplicate later: ids=%v err=%v", ids, err)
	}
	// Whitespace variants remain malformed (not "duplicates" of trimmed form).
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: " id "},
			{ID: "id"},
		},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("whitespace padded first: %v", err)
	}
	if _, _, err := collectInterruptResumeTargets(&adk.InterruptInfo{
		InterruptContexts: []*adk.InterruptCtx{
			{ID: "id"},
			{ID: "  "},
		},
	}); !errors.Is(err, ErrMalformedInterrupt) {
		t.Fatalf("whitespace second: %v", err)
	}
}

// TestAgenticEngine_MalformedInterruptEventFailsClosed drives consumeTypedIterator
// with synthetic malformed interrupt events and proves Interrupted is never true
// without a valid resume target.
func TestAgenticEngine_MalformedInterruptEventFailsClosed(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})

	cases := []struct {
		name string
		info *adk.InterruptInfo
	}{
		{"nil_contexts", &adk.InterruptInfo{InterruptContexts: nil}},
		{"empty_contexts", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{}}},
		{"nil_entry", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{nil}}},
		{"empty_id", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{{ID: ""}}}},
		{"whitespace_id", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{{ID: " \t "}}}},
		{"padded_id", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{{ID: " id "}}}},
		{"mixture", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{
			{ID: "good-id", IsRootCause: true},
			nil,
		}}},
		{"duplicate_ids", &adk.InterruptInfo{InterruptContexts: []*adk.InterruptCtx{
			{ID: "same", IsRootCause: true},
			{ID: "same"},
		}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Build a one-shot iterator that yields a malformed interrupt event.
			iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
				{
					Action: &adk.AgentAction{
						Interrupted: tc.info,
					},
				},
			})
			res, err := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n1", iter, nil)
			if !errors.Is(err, ErrMalformedInterrupt) {
				t.Fatalf("err=%v want ErrMalformedInterrupt", err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}
			if res.Interrupted {
				t.Fatalf("Interrupted must be false on malformed interrupt: %+v", res)
			}
			if len(res.InterruptContextIDs) != 0 || len(res.RootCauseInterruptIDs) != 0 {
				t.Fatalf("must not expose partial resume IDs: %+v", res)
			}
			if !errors.Is(res.Err, ErrMalformedInterrupt) {
				t.Fatalf("result.Err=%v", res.Err)
			}
		})
	}
}

// TestAgenticEngine_ValidInterruptPreservesExactIDs ensures a well-formed interrupt
// still yields Interrupted=true with exact IDs (no trim/rewrite).
func TestAgenticEngine_ValidInterruptPreservesExactIDs(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	wantID := "agent:hitl;tool:call-xyz"
	iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Action: &adk.AgentAction{
				Interrupted: &adk.InterruptInfo{
					InterruptContexts: []*adk.InterruptCtx{
						{ID: wantID, IsRootCause: true},
					},
				},
			},
		},
	})
	res, err := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n1", iter, nil)
	if err != nil {
		t.Fatalf("valid interrupt must not error: %v", err)
	}
	if !res.Interrupted {
		t.Fatal("expected Interrupted=true")
	}
	if len(res.InterruptContextIDs) != 1 || res.InterruptContextIDs[0] != wantID {
		t.Fatalf("IDs=%v want exact %q", res.InterruptContextIDs, wantID)
	}
	if len(res.RootCauseInterruptIDs) != 1 || res.RootCauseInterruptIDs[0] != wantID {
		t.Fatalf("roots=%v", res.RootCauseInterruptIDs)
	}
}

// TestAgenticEngine_HITLInterruptCheckpointResume is an end-to-end checkpoint
// resume proof that valid interrupt IDs from a real tool interrupt can resume.
func TestAgenticEngine_HITLInterruptCheckpointResume(t *testing.T) {
	ctx := context.Background()
	hitl := &agenticHITLTool{name: "hitl_tool"}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: hitl, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	mdl := &scriptedAgenticModel{
		responses: []*schema.AgenticMessage{
			agenticFunctionCall("hitl_tool", "h-1", `{"q":"need"}`),
			agenticmsg.AssistantText("resumed-ok"),
		},
	}
	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{hitl}, cat))
	if err != nil {
		t.Fatal(err)
	}
	store := newMemCheckPointStore()
	engine := NewAgenticEngine(AgenticEngineConfig{Store: store})
	r1, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-hitl-malform",
		RunID:       "run-hitl-malform",
		Messages:    []*schema.AgenticMessage{agenticmsg.UserText("start")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r1.Interrupted || len(r1.InterruptContextIDs) == 0 {
		t.Fatalf("expected valid interrupt: %+v", r1)
	}
	// Every ID must be non-empty and non-whitespace (engine contract).
	for _, id := range r1.InterruptContextIDs {
		if id == "" || strings.TrimSpace(id) != id {
			t.Fatalf("invalid resume ID from valid path: %q", id)
		}
	}
	targets := map[string]any{}
	for _, id := range r1.InterruptContextIDs {
		targets[id] = "yes"
	}
	r2, err := engine.Resume(ctx, agent, AgenticResumeInput{
		WorkspaceID:  "ws-hitl-malform",
		RunID:        "run-hitl-malform",
		CheckpointID: r1.CheckpointID,
		Targets:      targets,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if r2.Err != nil {
		t.Fatalf("resume result.Err=%v", r2.Err)
	}
	if r2.FinalAssistantText != "resumed-ok" {
		t.Fatalf("text=%q", r2.FinalAssistantText)
	}
}

func newSyntheticTypedIterator(events []*adk.TypedAgentEvent[*schema.AgenticMessage]) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.AgenticMessage]] {
	// Use the real ADK pipe so consumeTypedIterator type-checks.
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.AgenticMessage]]()
	go func() {
		for _, ev := range events {
			gen.Send(ev)
		}
		gen.Close()
	}()
	return iter
}

// pipeAgenticStream builds a real Pipe-backed stream (array streams no-op Close).
// Pipe Close is observable: a second Close panics on the underlying closed channel.
func pipeAgenticStream(chunks ...*schema.AgenticMessage) *schema.StreamReader[*schema.AgenticMessage] {
	sr, sw := schema.Pipe[*schema.AgenticMessage](len(chunks) + 1)
	go func() {
		defer sw.Close()
		for _, c := range chunks {
			if closed := sw.Send(c, nil); closed {
				return
			}
		}
	}()
	return sr
}

// assertStreamClosedOnce proves sr was already Closed exactly once: a further
// Close on a Pipe-backed reader panics (double-close of the closed signal chan).
// Array streams cannot be used — their Close is a no-op.
func assertStreamClosedOnce(t *testing.T, sr *schema.StreamReader[*schema.AgenticMessage]) {
	t.Helper()
	if sr == nil {
		t.Fatal("nil stream")
	}
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		sr.Close()
	}()
	if !panicked {
		t.Fatal("second Close did not panic; stream was not Closed by the owner path")
	}
}

// TestConsumeTypedIterator_MalformedVariantClosesStreamAndFailsClosed covers
// IsStreaming=false with MessageStream, simultaneous Message+MessageStream,
// and event.Err with a flag-mismatched attached stream.
func TestConsumeTypedIterator_MalformedVariantClosesStreamAndFailsClosed(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})

	// event.Err with IsStreaming=false but MessageStream set — must Close, return event err.
	errStream := pipeAgenticStream(agenticmsg.AssistantText("unused"))
	iterErr := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
		Err: errors.New("boom"),
		Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
			IsStreaming:   false,
			MessageStream: errStream,
		}},
	}})
	res, err := engine.consumeTypedIterator(context.Background(), "cp", iterErr, nil)
	if err == nil || res.Err == nil {
		t.Fatal("expected event error")
	}
	if errors.Is(err, ErrMalformedMessageVariant) {
		// event.Err path surfaces the event error, not the variant error.
		t.Fatalf("event.Err should surface primary error, got %v", err)
	}
	assertStreamClosedOnce(t, errStream)

	// Non-streaming variant with extraneous MessageStream — Close + ErrMalformedMessageVariant.
	leakStream := pipeAgenticStream(agenticmsg.AssistantText("unused"))
	iterLeak := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
		Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
			IsStreaming:   false,
			Message:       agenticmsg.AssistantText("ok"),
			MessageStream: leakStream,
		}},
	}})
	res2, err2 := engine.consumeTypedIterator(context.Background(), "cp", iterLeak, nil)
	if !errors.Is(err2, ErrMalformedMessageVariant) {
		t.Fatalf("err=%v want ErrMalformedMessageVariant", err2)
	}
	if res2 == nil || !errors.Is(res2.Err, ErrMalformedMessageVariant) {
		t.Fatalf("result.Err=%v", res2)
	}
	assertStreamClosedOnce(t, leakStream)

	// Simultaneous Message + MessageStream with IsStreaming=true — fail closed, Close once.
	bothStream := pipeAgenticStream(agenticmsg.AssistantText("unused"))
	iterBoth := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
		Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
			IsStreaming:   true,
			Message:       agenticmsg.AssistantText("also"),
			MessageStream: bothStream,
		}},
	}})
	res3, err3 := engine.consumeTypedIterator(context.Background(), "cp", iterBoth, nil)
	if !errors.Is(err3, ErrMalformedMessageVariant) {
		t.Fatalf("err=%v want ErrMalformedMessageVariant", err3)
	}
	if res3 == nil || !errors.Is(res3.Err, ErrMalformedMessageVariant) {
		t.Fatalf("result.Err=%v", res3)
	}
	assertStreamClosedOnce(t, bothStream)

	// IsStreaming=false, Message=nil, MessageStream set — still malformed + Close.
	flagStream := pipeAgenticStream(agenticmsg.AssistantText("unused"))
	iterFlag := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
		Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
			IsStreaming:   false,
			MessageStream: flagStream,
		}},
	}})
	res4, err4 := engine.consumeTypedIterator(context.Background(), "cp", iterFlag, nil)
	if !errors.Is(err4, ErrMalformedMessageVariant) {
		t.Fatalf("err=%v want ErrMalformedMessageVariant", err4)
	}
	if res4 == nil || !errors.Is(res4.Err, ErrMalformedMessageVariant) {
		t.Fatalf("result.Err=%v", res4)
	}
	assertStreamClosedOnce(t, flagStream)
}

// TestConsumeTypedIterator_MultiEventInterruptAccumulation accumulates exact
// IDs across multiple interrupt events and rejects cross-event duplicates.
func TestConsumeTypedIterator_MultiEventInterruptAccumulation(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})

	mk := func(id string, root bool) *adk.TypedAgentEvent[*schema.AgenticMessage] {
		return &adk.TypedAgentEvent[*schema.AgenticMessage]{
			Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{{ID: id, IsRootCause: root}},
			}},
		}
	}

	// Valid multi-event: both IDs preserved in encounter order.
	res, err := engine.consumeTypedIterator(context.Background(), "cp", newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		mk("id-1", true),
		mk("id-2", true),
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted {
		t.Fatal("expected Interrupted")
	}
	if len(res.InterruptContextIDs) != 2 || res.InterruptContextIDs[0] != "id-1" || res.InterruptContextIDs[1] != "id-2" {
		t.Fatalf("targets=%v; first valid interrupt target was overwritten", res.InterruptContextIDs)
	}
	if len(res.RootCauseInterruptIDs) != 2 || res.RootCauseInterruptIDs[0] != "id-1" || res.RootCauseInterruptIDs[1] != "id-2" {
		t.Fatalf("roots=%v", res.RootCauseInterruptIDs)
	}
	// ResumeParams map must be buildable with all exact IDs.
	targets := map[string]any{}
	for _, id := range res.InterruptContextIDs {
		targets[id] = "ack"
	}
	if len(targets) != 2 {
		t.Fatalf("targets map collapsed: %v", targets)
	}

	// Cross-event duplicate context ID → ErrMalformedInterrupt, no partial success.
	res2, err2 := engine.consumeTypedIterator(context.Background(), "cp", newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		mk("dup", true),
		mk("dup", true),
	}), nil)
	if !errors.Is(err2, ErrMalformedInterrupt) || res2.Interrupted {
		t.Fatalf("err=%v result=%+v; duplicate resume IDs across events must not silently collapse", err2, res2)
	}
	if len(res2.InterruptContextIDs) != 0 || len(res2.RootCauseInterruptIDs) != 0 {
		t.Fatalf("must not expose partial IDs: %+v", res2)
	}

	// Mixed message + interrupt: text then interrupt IDs accumulated.
	msgStream := pipeAgenticStream(agenticmsg.AssistantText("partial-"))
	res3, err3 := engine.consumeTypedIterator(context.Background(), "cp", newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: msgStream,
			}},
		},
		mk("after-msg", true),
		mk("after-msg-2", false),
	}), nil)
	if err3 != nil {
		t.Fatal(err3)
	}
	assertStreamClosedOnce(t, msgStream)
	if res3.FinalAssistantText != "partial-" {
		t.Fatalf("text=%q", res3.FinalAssistantText)
	}
	if !res3.Interrupted || len(res3.InterruptContextIDs) != 2 {
		t.Fatalf("mixed: %+v", res3)
	}
	if res3.InterruptContextIDs[0] != "after-msg" || res3.InterruptContextIDs[1] != "after-msg-2" {
		t.Fatalf("ids=%v", res3.InterruptContextIDs)
	}
	if len(res3.RootCauseInterruptIDs) != 1 || res3.RootCauseInterruptIDs[0] != "after-msg" {
		t.Fatalf("roots=%v", res3.RootCauseInterruptIDs)
	}

	// Multi-context single event then another event with distinct IDs.
	res4, err4 := engine.consumeTypedIterator(context.Background(), "cp", newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{
					{ID: "a", IsRootCause: false},
					{ID: "b", IsRootCause: true},
				},
			}},
		},
		mk("c", true),
	}), nil)
	if err4 != nil {
		t.Fatal(err4)
	}
	if len(res4.InterruptContextIDs) != 3 || res4.InterruptContextIDs[0] != "a" || res4.InterruptContextIDs[1] != "b" || res4.InterruptContextIDs[2] != "c" {
		t.Fatalf("ids=%v", res4.InterruptContextIDs)
	}
	if len(res4.RootCauseInterruptIDs) != 2 || res4.RootCauseInterruptIDs[0] != "b" || res4.RootCauseInterruptIDs[1] != "c" {
		t.Fatalf("roots=%v", res4.RootCauseInterruptIDs)
	}
}

// TestConsumeTypedIterator_InterruptThenDuplicateRootAcrossEvents: second
// event reuses a prior root ID → fail closed.
func TestConsumeTypedIterator_InterruptThenDuplicateRootAcrossEvents(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	// First event: id-root is root. Second: same id as non-root would be context
	// duplicate. Use same ID again (context-level dup catches it).
	res, err := engine.consumeTypedIterator(context.Background(), "cp", newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{{ID: "root-1", IsRootCause: true}},
			}},
		},
		{
			Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
				InterruptContexts: []*adk.InterruptCtx{{ID: "root-1", IsRootCause: true}},
			}},
		},
	}), nil)
	if !errors.Is(err, ErrMalformedInterrupt) || res.Interrupted {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

func TestDrainAndCloseAgenticMessageStream_SuccessClosesOnce(t *testing.T) {
	t.Parallel()
	msg := agenticmsg.AssistantText("hello")
	sr := pipeAgenticStream(msg)
	chunks, err := drainAndCloseAgenticMessageStream(sr, nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks=%d", len(chunks))
	}
	assertStreamClosedOnce(t, sr)
}

func TestDrainAndCloseAgenticMessageStream_NilChunkClosesOnce(t *testing.T) {
	t.Parallel()
	sr, sw := schema.Pipe[*schema.AgenticMessage](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(nil, nil)
	}()
	_, err := drainAndCloseAgenticMessageStream(sr, nil)
	if !errors.Is(err, agenticmsg.ErrNilChunk) {
		t.Fatalf("err=%v want ErrNilChunk", err)
	}
	assertStreamClosedOnce(t, sr)
}

func TestDrainAndCloseAgenticMessageStream_ValidationErrorClosesOnce(t *testing.T) {
	t.Parallel()
	bad := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeFunctionToolCall}, // nil payload → malformed
		},
	}
	sr := pipeAgenticStream(bad)
	_, err := drainAndCloseAgenticMessageStream(sr, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	assertStreamClosedOnce(t, sr)
}

func TestDrainAndCloseAgenticMessageStream_RecvErrorClosesOnce(t *testing.T) {
	t.Parallel()
	sr, sw := schema.Pipe[*schema.AgenticMessage](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(nil, errors.New("decode boom"))
	}()
	_, err := drainAndCloseAgenticMessageStream(sr, nil)
	if err == nil || err.Error() != "decode boom" {
		t.Fatalf("err=%v", err)
	}
	assertStreamClosedOnce(t, sr)
}

func TestConsumeTypedIterator_StreamingSuccessAndEarlyPathsClose(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})

	// Success path: stream Closed after drain.
	okStream := pipeAgenticStream(agenticmsg.AssistantText("ok-text"))
	iterOK := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{
				MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: okStream,
				},
			},
		},
	})
	res, err := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n1", iterOK, nil)
	if err != nil {
		t.Fatalf("success path: %v", err)
	}
	if res.FinalAssistantText != "ok-text" {
		t.Fatalf("text=%q", res.FinalAssistantText)
	}
	assertStreamClosedOnce(t, okStream)

	// Event.Err path with attached stream: must Close without drain.
	errStream := pipeAgenticStream(agenticmsg.AssistantText("x"))
	iterErr := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Err: errors.New("boom"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{
				MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: errStream,
				},
			},
		},
	})
	res2, err2 := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n2", iterErr, nil)
	if err2 == nil || res2.Err == nil {
		t.Fatal("expected event error")
	}
	assertStreamClosedOnce(t, errStream)

	// Interrupt path with attached stream: must Close.
	intStream := pipeAgenticStream(agenticmsg.AssistantText("y"))
	iterInt := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Action: &adk.AgentAction{
				Interrupted: &adk.InterruptInfo{
					InterruptContexts: []*adk.InterruptCtx{{ID: "resume-1", IsRootCause: true}},
				},
			},
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{
				MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: intStream,
				},
			},
		},
	})
	res3, err3 := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n3", iterInt, nil)
	if err3 != nil {
		t.Fatalf("interrupt: %v", err3)
	}
	if !res3.Interrupted {
		t.Fatal("expected interrupt")
	}
	assertStreamClosedOnce(t, intStream)

	// Validation error mid-stream: Close once.
	bad := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			{Type: schema.ContentBlockTypeFunctionToolCall}, // nil FunctionToolCall
		},
	}
	valStream := pipeAgenticStream(bad)
	iterVal := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
		{
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{
				MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: valStream,
				},
			},
		},
	})
	res4, err4 := engine.consumeTypedIterator(context.Background(), "ws/ws/agent_run/r/n4", iterVal, nil)
	if err4 == nil || res4.Err == nil {
		t.Fatal("expected validation failure")
	}
	assertStreamClosedOnce(t, valStream)
}

// validInterruptAction builds a well-formed interrupt action with one resume ID.
func validInterruptAction(id string) *adk.AgentAction {
	return &adk.AgentAction{
		Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{{ID: id, IsRootCause: true}},
		},
	}
}

// TestConsumeTypedIterator_InterruptMalformedPayloadsFailClosed proves the
// interrupt branch cannot fail-open: every malformed TypedMessageVariant shape
// and invalid message role/union is rejected before Interrupted is set, with
// Pipe-backed Close-once ownership where a stream is present.
func TestConsumeTypedIterator_InterruptMalformedPayloadsFailClosed(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	cp := "ws/ws/agent_run/r/int-malform"

	assertFailClosed := func(t *testing.T, res *AgenticRunResult, err error, wantIs error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected hard error")
		}
		if wantIs != nil && !errors.Is(err, wantIs) {
			t.Fatalf("err=%v want errors.Is %v", err, wantIs)
		}
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Interrupted {
			t.Fatalf("Interrupted must be false on malformed interrupt payload: %+v", res)
		}
		if len(res.InterruptContextIDs) != 0 || len(res.RootCauseInterruptIDs) != 0 {
			t.Fatalf("must not expose resume IDs: %+v", res)
		}
	}

	t.Run("non_stream_flag_with_nonnil_stream", func(t *testing.T) {
		t.Parallel()
		sr := pipeAgenticStream(agenticmsg.AssistantText("unused"))
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-leak"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   false,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, ErrMalformedMessageVariant)
		assertStreamClosedOnce(t, sr)
	})

	t.Run("simultaneous_message_and_stream", func(t *testing.T) {
		t.Parallel()
		sr := pipeAgenticStream(agenticmsg.AssistantText("unused"))
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-both"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				Message:       agenticmsg.AssistantText("also"),
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, ErrMalformedMessageVariant)
		assertStreamClosedOnce(t, sr)
	})

	t.Run("streaming_flag_with_nil_stream", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-nil-stream"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: true,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, ErrNilMessageStream)
	})

	t.Run("streaming_plus_message_no_stream", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-stream-msg"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: true,
				Message:     agenticmsg.AssistantText("side-channel"),
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, ErrNilMessageStream)
	})

	t.Run("malformed_stream_chunk", func(t *testing.T) {
		t.Parallel()
		bad := &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				{Type: schema.ContentBlockTypeFunctionToolCall}, // nil FunctionToolCall
			},
		}
		sr := pipeAgenticStream(bad)
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-bad-chunk"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err == nil {
			t.Fatal("expected stream chunk validation error")
		}
		assertFailClosed(t, res, err, nil)
		assertStreamClosedOnce(t, sr)
	})

	t.Run("nil_stream_chunk", func(t *testing.T) {
		t.Parallel()
		sr, sw := schema.Pipe[*schema.AgenticMessage](1)
		go func() {
			defer sw.Close()
			_ = sw.Send(nil, nil)
		}()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-nil-chunk"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, agenticmsg.ErrNilChunk)
		assertStreamClosedOnce(t, sr)
	})

	t.Run("invalid_complete_message_role", func(t *testing.T) {
		t.Parallel()
		bad := &schema.AgenticMessage{
			Role: "",
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.UserInputText{Text: "x"}),
			},
		}
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-bad-role"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: false,
				Message:     bad,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, agenticmsg.ErrInvalidRole)
	})

	t.Run("invalid_complete_message_union", func(t *testing.T) {
		t.Parallel()
		// Function tool call type with nil payload — union/malformed block.
		bad := &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				{Type: schema.ContentBlockTypeFunctionToolCall},
			},
		}
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-bad-union"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: false,
				Message:     bad,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err == nil {
			t.Fatal("expected complete-message validation error")
		}
		assertFailClosed(t, res, err, nil)
	})

	t.Run("unsupported_media_on_interrupt_message", func(t *testing.T) {
		t.Parallel()
		bad := &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x/i.png"}),
			},
		}
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("resume-media"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: false,
				Message:     bad,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertFailClosed(t, res, err, agenticmsg.ErrUnsupportedBlock)
	})

	t.Run("event_err_still_primary_with_stream", func(t *testing.T) {
		t.Parallel()
		// event.Err remains authoritative even when the variant is also malformed;
		// stream is still Closed exactly once.
		sr := pipeAgenticStream(agenticmsg.AssistantText("unused"))
		primary := errors.New("primary-event-err")
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Err:    primary,
			Action: validInterruptAction("should-not-collect"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   false,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err == nil {
			t.Fatal("expected primary event error")
		}
		if errors.Is(err, ErrMalformedMessageVariant) {
			t.Fatalf("event.Err must not be reclassified as variant error: %v", err)
		}
		if !errors.Is(err, primary) && (res == nil || res.Err == nil || res.Err.Error() != primary.Error()) {
			// mapEngineError may wrap; surface string match as fallback.
			if res == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "primary-event-err") {
				t.Fatalf("err=%v res.Err=%v want primary event error", err, res)
			}
		}
		if res != nil && res.Interrupted {
			t.Fatal("event.Err path must not set Interrupted")
		}
		assertStreamClosedOnce(t, sr)
	})
}

// TestConsumeTypedIterator_ValidInterruptPayloadsProcessThenInterrupt covers
// interrupt-only, valid attached complete Message, and valid attached stream —
// all must set Interrupted=true after consistent validation/process, with
// Close-once for Pipe-backed streams.
func TestConsumeTypedIterator_ValidInterruptPayloadsProcessThenInterrupt(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	cp := "ws/ws/agent_run/r/int-valid"

	t.Run("interrupt_only_no_payload", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("only-id"),
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err != nil {
			t.Fatalf("interrupt-only: %v", err)
		}
		if !res.Interrupted || len(res.InterruptContextIDs) != 1 || res.InterruptContextIDs[0] != "only-id" {
			t.Fatalf("result=%+v", res)
		}
		if res.FinalAssistantText != "" {
			t.Fatalf("text=%q", res.FinalAssistantText)
		}
	})

	t.Run("valid_attached_complete_message", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("with-msg"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming: false,
				Message:     agenticmsg.AssistantText("pre-interrupt"),
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err != nil {
			t.Fatalf("valid message+interrupt: %v", err)
		}
		if !res.Interrupted || res.InterruptContextIDs[0] != "with-msg" {
			t.Fatalf("result=%+v", res)
		}
		if res.FinalAssistantText != "pre-interrupt" {
			t.Fatalf("text=%q want pre-interrupt", res.FinalAssistantText)
		}
	})

	t.Run("valid_attached_stream", func(t *testing.T) {
		t.Parallel()
		sr := pipeAgenticStream(agenticmsg.AssistantText("streamed-"))
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("with-stream"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err != nil {
			t.Fatalf("valid stream+interrupt: %v", err)
		}
		if !res.Interrupted || res.InterruptContextIDs[0] != "with-stream" {
			t.Fatalf("result=%+v", res)
		}
		if res.FinalAssistantText != "streamed-" {
			t.Fatalf("text=%q want streamed-", res.FinalAssistantText)
		}
		assertStreamClosedOnce(t, sr)
	})
}

// TestConsumeTypedIterator_EmptyStreamFailClosed proves zero-chunk
// IsStreaming=true MessageStreams fail closed with agenticmsg.ErrEmptyConcat
// (strict ConcatStream semantics) for ordinary and interrupt-attached events.
// Pipe-backed and array-backed streams; Close exactly once when observable;
// no final text and no interrupt target IDs on error.
func TestConsumeTypedIterator_EmptyStreamFailClosed(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	cp := "ws/ws/agent_run/r/empty-stream"

	// Pipe-backed ordinary empty stream.
	t.Run("pipe_ordinary_empty", func(t *testing.T) {
		t.Parallel()
		sr := pipeAgenticStream() // zero chunks, writer Close → EOF
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if !errors.Is(err, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("err=%v want ErrEmptyConcat", err)
		}
		if res == nil || !errors.Is(res.Err, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("result.Err=%v", res)
		}
		if res.Interrupted || len(res.InterruptContextIDs) != 0 || len(res.RootCauseInterruptIDs) != 0 {
			t.Fatalf("must not return interrupt IDs on empty stream: %+v", res)
		}
		if res.FinalAssistantText != "" {
			t.Fatalf("text=%q want empty", res.FinalAssistantText)
		}
		assertStreamClosedOnce(t, sr)
	})

	// Array-backed ordinary empty stream (Close is a no-op; still fail closed).
	t.Run("array_ordinary_empty", func(t *testing.T) {
		t.Parallel()
		sr := schema.StreamReaderFromArray([]*schema.AgenticMessage{})
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if !errors.Is(err, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("err=%v want ErrEmptyConcat", err)
		}
		if res.Interrupted || len(res.InterruptContextIDs) != 0 {
			t.Fatalf("interrupt IDs on empty array stream: %+v", res)
		}
		if res.FinalAssistantText != "" {
			t.Fatalf("text=%q", res.FinalAssistantText)
		}
	})

	// Pipe-backed empty stream attached to a legitimate interrupt — still fail
	// closed (variant validation before interrupt accumulation).
	t.Run("pipe_interrupt_attached_empty", func(t *testing.T) {
		t.Parallel()
		sr := pipeAgenticStream()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("must-not-leak"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if !errors.Is(err, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("err=%v want ErrEmptyConcat", err)
		}
		if res.Interrupted || len(res.InterruptContextIDs) != 0 || len(res.RootCauseInterruptIDs) != 0 {
			t.Fatalf("empty interrupt-attached stream must not yield interrupt IDs: %+v", res)
		}
		if res.FinalAssistantText != "" {
			t.Fatalf("text=%q", res.FinalAssistantText)
		}
		assertStreamClosedOnce(t, sr)
	})

	// Array-backed empty stream + interrupt.
	t.Run("array_interrupt_attached_empty", func(t *testing.T) {
		t.Parallel()
		sr := schema.StreamReaderFromArray([]*schema.AgenticMessage{})
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{{
			Action: validInterruptAction("array-must-not-leak"),
			Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
				IsStreaming:   true,
				MessageStream: sr,
			}},
		}})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if !errors.Is(err, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("err=%v want ErrEmptyConcat", err)
		}
		if res.Interrupted || len(res.InterruptContextIDs) != 0 {
			t.Fatalf("array empty+interrupt must not yield IDs: %+v", res)
		}
	})
}

// TestConsumeTypedIterator_HardTerminalClearsPriorInterrupt proves that once
// valid interrupt IDs have accumulated, a later hard terminal (event.Err, nil
// event, malformed variant, stream recv error) clears all recoverable interrupt
// state so hard error is exclusive of resumable Interrupted/IDs. Clean iterator
// end after a valid interrupt still yields Interrupted=true.
func TestConsumeTypedIterator_HardTerminalClearsPriorInterrupt(t *testing.T) {
	t.Parallel()
	engine := NewAgenticEngine(AgenticEngineConfig{})
	cp := "ws/ws/agent_run/r/hard-term"

	assertHardExclusiveOfInterrupt := func(t *testing.T, res *AgenticRunResult, err error, wantIs error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected hard error")
		}
		if wantIs != nil && !errors.Is(err, wantIs) {
			// mapEngineError may pass through plain errors without wrapping.
			if res == nil || res.Err == nil || (wantIs.Error() != "" && !strings.Contains(res.Err.Error(), wantIs.Error()) && !errors.Is(res.Err, wantIs)) {
				t.Fatalf("err=%v res.Err=%v want errors.Is %v", err, res, wantIs)
			}
		}
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Interrupted {
			t.Fatalf("hard error must clear Interrupted: %+v", res)
		}
		if len(res.InterruptContextIDs) != 0 || len(res.RootCauseInterruptIDs) != 0 {
			t.Fatalf("hard error must clear interrupt IDs: %+v", res)
		}
		if res.Err == nil {
			t.Fatal("result.Err must be set on hard terminal")
		}
	}

	// interrupt -> event.Err
	t.Run("interrupt_then_event_err", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("late-event-err")
		sr := pipeAgenticStream(agenticmsg.AssistantText("unused"))
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
			{Action: validInterruptAction("prior-id-err")},
			{
				Err: primary,
				Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: sr,
				}},
			},
		})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertHardExclusiveOfInterrupt(t, res, err, primary)
		assertStreamClosedOnce(t, sr)
	})

	// interrupt -> nil event
	t.Run("interrupt_then_nil_event", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
			{Action: validInterruptAction("prior-id-nil")},
			nil,
		})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertHardExclusiveOfInterrupt(t, res, err, ErrNilTypedEvent)
	})

	// interrupt -> malformed variant
	t.Run("interrupt_then_malformed_variant", func(t *testing.T) {
		t.Parallel()
		leak := pipeAgenticStream(agenticmsg.AssistantText("unused"))
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
			{Action: validInterruptAction("prior-id-malform")},
			{
				Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   false,
					MessageStream: leak, // flag mismatch
				}},
			},
		})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertHardExclusiveOfInterrupt(t, res, err, ErrMalformedMessageVariant)
		assertStreamClosedOnce(t, leak)
	})

	// interrupt -> stream recv/iterator error (later message stream fails mid-drain)
	t.Run("interrupt_then_stream_recv_error", func(t *testing.T) {
		t.Parallel()
		recvBoom := errors.New("stream-recv-boom")
		sr, sw := schema.Pipe[*schema.AgenticMessage](1)
		go func() {
			defer sw.Close()
			_ = sw.Send(nil, recvBoom)
		}()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
			{Action: validInterruptAction("prior-id-recv")},
			{
				Output: &adk.TypedAgentOutput[*schema.AgenticMessage]{MessageOutput: &adk.TypedMessageVariant[*schema.AgenticMessage]{
					IsStreaming:   true,
					MessageStream: sr,
				}},
			},
		})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		assertHardExclusiveOfInterrupt(t, res, err, recvBoom)
		assertStreamClosedOnce(t, sr)
	})

	// Clean interrupt end: hard terminal must NOT clear valid interrupted result.
	t.Run("clean_interrupt_end_preserves_state", func(t *testing.T) {
		t.Parallel()
		iter := newSyntheticTypedIterator([]*adk.TypedAgentEvent[*schema.AgenticMessage]{
			{Action: validInterruptAction("clean-keep")},
		})
		res, err := engine.consumeTypedIterator(context.Background(), cp, iter, nil)
		if err != nil {
			t.Fatalf("clean interrupt end: %v", err)
		}
		if res == nil || res.Err != nil {
			t.Fatalf("result.Err must be nil on clean interrupt: %+v", res)
		}
		if !res.Interrupted {
			t.Fatal("expected Interrupted=true on clean interrupt end")
		}
		if len(res.InterruptContextIDs) != 1 || res.InterruptContextIDs[0] != "clean-keep" {
			t.Fatalf("IDs=%v want [clean-keep]", res.InterruptContextIDs)
		}
		if len(res.RootCauseInterruptIDs) != 1 || res.RootCauseInterruptIDs[0] != "clean-keep" {
			t.Fatalf("roots=%v", res.RootCauseInterruptIDs)
		}
	})
}
