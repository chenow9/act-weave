package einoruntime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/schema/openai"

	"actweave/backend/internal/agenticmsg"
)

// fixedResponseAgenticModel returns canned Generate/Stream outputs for adversarial tests.
type fixedResponseAgenticModel struct {
	gen *schema.AgenticMessage
	// streamChunks when non-nil drives Stream; otherwise Stream yields gen as one chunk.
	streamChunks []*schema.AgenticMessage
	genErr       error
	streamErr    error
}

func (m *fixedResponseAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if m.genErr != nil {
		return nil, m.genErr
	}
	return m.gen, nil
}

func (m *fixedResponseAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.streamChunks != nil {
		return schema.StreamReaderFromArray(m.streamChunks), nil
	}
	if m.gen == nil {
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{}), nil
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{m.gen}), nil
}

func multiFunctionCallMsg(calls ...schema.FunctionToolCall) *schema.AgenticMessage {
	blocks := make([]*schema.ContentBlock, 0, len(calls))
	for i := range calls {
		c := calls[i]
		blocks = append(blocks, schema.NewContentBlock(&c))
	}
	return &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: blocks,
	}
}

func markToolSearchBlock(msg *schema.AgenticMessage) *schema.AgenticMessage {
	if msg == nil {
		return nil
	}
	for _, b := range msg.ContentBlocks {
		if b != nil && b.Type == schema.ContentBlockTypeFunctionToolCall {
			if b.Extra == nil {
				b.Extra = map[string]any{}
			}
			b.Extra["openai-tool-search-tool-call"] = true
		}
	}
	return msg
}

func TestSingleActionAgenticModel_GenerateTwoCallsRejected(t *testing.T) {
	t.Parallel()
	inner := &fixedResponseAgenticModel{
		gen: multiFunctionCallMsg(
			schema.FunctionToolCall{Name: "a", CallID: "c1", Arguments: `{"q":"1"}`},
			schema.FunctionToolCall{Name: "b", CallID: "c2", Arguments: `{"q":"2"}`},
		),
	}
	m := wrapSingleActionAgenticModel(inner)
	_, err := m.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if !errors.Is(err, ErrMultiActionModelTurn) {
		t.Fatalf("err=%v want ErrMultiActionModelTurn", err)
	}
}

func TestSingleActionAgenticModel_StreamSameChunkTwoCallsRejected(t *testing.T) {
	t.Parallel()
	inner := &fixedResponseAgenticModel{
		streamChunks: []*schema.AgenticMessage{
			multiFunctionCallMsg(
				schema.FunctionToolCall{Name: "a", CallID: "c1", Arguments: `{"q":"1"}`},
				schema.FunctionToolCall{Name: "b", CallID: "c2", Arguments: `{"q":"2"}`},
			),
		},
	}
	m := wrapSingleActionAgenticModel(inner)
	// Buffer-before-observe: first Recv is ErrMultiActionModelTurn; no content chunks.
	sr, err := m.Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatalf("Stream setup: %v", err)
	}
	defer sr.Close()
	chunk, rerr := sr.Recv()
	if chunk != nil {
		t.Fatal("expected no content chunk on multi-action reject")
	}
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("Recv err=%v want ErrMultiActionModelTurn", rerr)
	}
}

func TestSingleActionAgenticModel_StreamCallsSplitAcrossChunksRejected(t *testing.T) {
	t.Parallel()
	// Two complete single-call chunks with different CallIDs / stream indexes —
	// multi-action across stream. Guard buffers fully and rejects before any chunk.
	chunk1 := multiFunctionCallMsg(schema.FunctionToolCall{Name: "a", CallID: "c1", Arguments: `{"q":"1"}`})
	chunk1.ContentBlocks[0].StreamingMeta = &schema.StreamingMeta{Index: 0}
	chunk2 := multiFunctionCallMsg(schema.FunctionToolCall{Name: "b", CallID: "c2", Arguments: `{"q":"2"}`})
	chunk2.ContentBlocks[0].StreamingMeta = &schema.StreamingMeta{Index: 1}

	inner := &fixedResponseAgenticModel{streamChunks: []*schema.AgenticMessage{chunk1, chunk2}}
	m := wrapSingleActionAgenticModel(inner)
	sr, err := m.Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatalf("Stream setup: %v", err)
	}
	defer sr.Close()
	chunk, rerr := sr.Recv()
	if chunk != nil {
		t.Fatal("expected no content chunk on multi-action reject")
	}
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("Recv err=%v want ErrMultiActionModelTurn", rerr)
	}
}

func TestSingleActionAgenticModel_MixedSearchAndFunctionRejected(t *testing.T) {
	t.Parallel()
	search := multiFunctionCallMsg(schema.FunctionToolCall{
		Name: ClientToolSearchToolName, CallID: "ts-1", Arguments: `{"query":"x"}`,
	})
	markToolSearchBlock(search)
	// Combine search + ordinary function in one turn.
	mixed := multiFunctionCallMsg(
		schema.FunctionToolCall{Name: ClientToolSearchToolName, CallID: "ts-1", Arguments: `{"query":"x"}`},
		schema.FunctionToolCall{Name: "echo", CallID: "e-1", Arguments: `{"q":"hi"}`},
	)
	// Mark first block as tool-search.
	if mixed.ContentBlocks[0].Extra == nil {
		mixed.ContentBlocks[0].Extra = map[string]any{}
	}
	mixed.ContentBlocks[0].Extra["openai-tool-search-tool-call"] = true

	inner := &fixedResponseAgenticModel{gen: mixed}
	m := wrapSingleActionAgenticModel(inner)
	_, err := m.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if !errors.Is(err, ErrMultiActionModelTurn) {
		t.Fatalf("err=%v want ErrMultiActionModelTurn", err)
	}

	// Stream form of the same mixed turn — rejected before any chunk is observed.
	inner2 := &fixedResponseAgenticModel{streamChunks: []*schema.AgenticMessage{mixed}}
	m2 := wrapSingleActionAgenticModel(inner2)
	sr, err := m2.Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	_, rerr := sr.Recv()
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("stream err=%v want ErrMultiActionModelTurn", rerr)
	}
}

func TestSingleActionAgenticModel_ValidOneCallAndFinalText(t *testing.T) {
	t.Parallel()
	// One function call is allowed.
	one := multiFunctionCallMsg(schema.FunctionToolCall{Name: "echo", CallID: "c1", Arguments: `{"q":"x"}`})
	m := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{gen: one})
	got, err := m.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatalf("one call: %v", err)
	}
	if got == nil || len(got.ContentBlocks) != 1 {
		t.Fatalf("got=%v", got)
	}

	// Final text only is allowed.
	final := agenticmsg.AssistantText("done")
	m2 := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{gen: final})
	got2, err := m2.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatalf("final text: %v", err)
	}
	text, err := agenticmsg.ExtractAssistantText(got2)
	if err != nil || text != "done" {
		t.Fatalf("text=%q err=%v", text, err)
	}

	// Stream text + one function call (distinct StreamingMeta indexes; one action).
	textChunk := agenticmsg.AssistantText("hel")
	if len(textChunk.ContentBlocks) > 0 {
		textChunk.ContentBlocks[0].StreamingMeta = &schema.StreamingMeta{Index: 0}
	}
	callChunk := multiFunctionCallMsg(schema.FunctionToolCall{Name: "echo", CallID: "c9", Arguments: `{"q":"x"}`})
	callChunk.ContentBlocks[0].StreamingMeta = &schema.StreamingMeta{Index: 1}
	m3 := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{
		streamChunks: []*schema.AgenticMessage{textChunk, callChunk},
	})
	sr, err := m3.Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	n := 0
	for {
		_, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("valid stream Recv: %v", err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("chunks=%d want 2", n)
	}
}

// TestSingleActionGuard_RealToolsNodeNeverExecutesOnRejectedMultiAction builds a
// real agent via BuildAgenticAgent and proves multi-action model turns fail closed
// with ErrMultiActionModelTurn before any tool executes (ToolsNode/TypedRunner).
func TestSingleActionGuard_RealToolsNodeNeverExecutesOnRejectedMultiAction(t *testing.T) {
	ctx := context.Background()
	var calls int64
	bt := &countingBudgetTool{name: "echo_multi", calls: &calls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
	if err != nil {
		t.Fatal(err)
	}
	// Adversarial model emits two function calls in one Generate/Stream turn.
	multi := multiFunctionCallMsg(
		schema.FunctionToolCall{Name: "echo_multi", CallID: "m1", Arguments: `{"q":"a"}`},
		schema.FunctionToolCall{Name: "echo_multi", CallID: "m2", Arguments: `{"q":"b"}`},
	)
	mdl := &scriptedAgenticModel{responses: []*schema.AgenticMessage{multi, agenticmsg.AssistantText("should-not")}}
	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()})
	res, err := engine.Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-multi", RunID: "run-multi",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("go")},
	})
	var hard error
	if err != nil {
		hard = err
	} else if res != nil {
		hard = res.Err
	}
	if hard == nil {
		t.Fatalf("expected multi-action rejection; res=%+v", res)
	}
	if !errors.Is(hard, ErrMultiActionModelTurn) {
		t.Fatalf("hard=%v want ErrMultiActionModelTurn", hard)
	}
	if atomic.LoadInt64(&calls) != 0 {
		t.Fatalf("tool must not execute on rejected multi-action; calls=%d", calls)
	}
}

// TestSingleActionGuard_Race exercises concurrent Generate/Stream multi-action rejection.
func TestSingleActionGuard_Race(t *testing.T) {
	t.Parallel()
	inner := &fixedResponseAgenticModel{
		gen: multiFunctionCallMsg(
			schema.FunctionToolCall{Name: "a", CallID: "c1", Arguments: `{}`},
			schema.FunctionToolCall{Name: "b", CallID: "c2", Arguments: `{}`},
		),
		streamChunks: []*schema.AgenticMessage{
			multiFunctionCallMsg(
				schema.FunctionToolCall{Name: "a", CallID: "c1", Arguments: `{}`},
				schema.FunctionToolCall{Name: "b", CallID: "c2", Arguments: `{}`},
			),
		},
	}
	m := wrapSingleActionAgenticModel(inner)
	const n = 8
	errCh := make(chan error, n*2)
	for i := 0; i < n; i++ {
		go func() {
			_, err := m.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("g")})
			errCh <- err
		}()
		go func() {
			sr, err := m.Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("s")})
			if err != nil {
				errCh <- err
				return
			}
			_, rerr := sr.Recv()
			sr.Close()
			errCh <- rerr
		}()
	}
	for i := 0; i < n*2; i++ {
		err := <-errCh
		if !errors.Is(err, ErrMultiActionModelTurn) {
			t.Fatalf("race err=%v want ErrMultiActionModelTurn", err)
		}
	}
}

// actionChunk builds a streaming function-call fragment with explicit index.
func actionChunk(name, id string, index int, search bool) *schema.AgenticMessage {
	c := schema.FunctionToolCall{Name: name, CallID: id, Arguments: `{}`}
	b := schema.NewContentBlockChunk(&c, &schema.StreamingMeta{Index: index})
	if search {
		b.Extra = map[string]any{"openai-tool-search-tool-call": true}
	}
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{b}}
}

// TestSingleActionGuard_CollidingCallIDsDistinctIndexesRejected proves the
// guard does not trust CallID uniqueness: two different functions with the
// same CallID but different StreamingMeta indexes are multi-action.
func TestSingleActionGuard_CollidingCallIDsDistinctIndexesRejected(t *testing.T) {
	t.Parallel()
	inner := &fixedResponseAgenticModel{streamChunks: []*schema.AgenticMessage{
		actionChunk("a", "collision", 0, false),
		actionChunk("b", "collision", 1, false),
	}}
	sr, err := wrapSingleActionAgenticModel(inner).Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	chunk, rerr := sr.Recv()
	if chunk != nil {
		t.Fatal("expected no content chunk")
	}
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("err=%v want ErrMultiActionModelTurn", rerr)
	}
}

// TestSingleActionGuard_CollidingMixedSearchAndFunctionRejected covers native
// tool-search + business function sharing a CallID across stream indexes.
func TestSingleActionGuard_CollidingMixedSearchAndFunctionRejected(t *testing.T) {
	t.Parallel()
	inner := &fixedResponseAgenticModel{streamChunks: []*schema.AgenticMessage{
		actionChunk(ClientToolSearchToolName, "collision", 0, true),
		actionChunk("business", "collision", 1, false),
	}}
	sr, err := wrapSingleActionAgenticModel(inner).Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	chunk, rerr := sr.Recv()
	if chunk != nil {
		t.Fatal("expected no content chunk")
	}
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("err=%v want ErrMultiActionModelTurn", rerr)
	}
}

// TestSingleActionGuard_EmptyAndMissingCallIDsStillCountBlocks ensures empty
// CallIDs do not collapse two function blocks into one action.
func TestSingleActionGuard_EmptyAndMissingCallIDsStillCountBlocks(t *testing.T) {
	t.Parallel()
	// Generate path: two blocks with empty CallIDs.
	msg := multiFunctionCallMsg(
		schema.FunctionToolCall{Name: "a", CallID: "", Arguments: `{}`},
		schema.FunctionToolCall{Name: "b", CallID: "", Arguments: `{}`},
	)
	_, err := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{gen: msg}).
		Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("go")})
	if !errors.Is(err, ErrMultiActionModelTurn) {
		t.Fatalf("Generate empty IDs: err=%v want ErrMultiActionModelTurn", err)
	}

	// Stream path: two indexes, empty CallIDs (stream fragments allow empty IDs
	// under StreamingMeta; after concat they remain two action blocks).
	c1 := schema.FunctionToolCall{Name: "a", CallID: "", Arguments: `{}`}
	c2 := schema.FunctionToolCall{Name: "b", CallID: "", Arguments: `{}`}
	b1 := schema.NewContentBlockChunk(&c1, &schema.StreamingMeta{Index: 0})
	b2 := schema.NewContentBlockChunk(&c2, &schema.StreamingMeta{Index: 1})
	chunks := []*schema.AgenticMessage{
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{b1}},
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{b2}},
	}
	sr, err := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{streamChunks: chunks}).
		Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("go")})
	if err != nil {
		t.Fatalf("Stream setup: %v", err)
	}
	defer sr.Close()
	// Multi-action is preferred over a later complete-Validate failure on empty IDs.
	_, rerr := sr.Recv()
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("Stream empty IDs: err=%v want ErrMultiActionModelTurn", rerr)
	}
}

// TestSingleActionGuard_SameIndexProgressiveSingleActionAllowed ensures one
// physical action split across progressive stream fragments (same index) is
// allowed and replayed intact.
func TestSingleActionGuard_SameIndexProgressiveSingleActionAllowed(t *testing.T) {
	t.Parallel()
	// Progressive OpenAI-style: name+id first, then arguments delta (empty identity).
	part1 := schema.NewContentBlockChunk(
		&schema.FunctionToolCall{Name: "echo", CallID: "c1", Arguments: ``},
		&schema.StreamingMeta{Index: 0},
	)
	part2 := schema.NewContentBlockChunk(
		&schema.FunctionToolCall{Name: "", CallID: "", Arguments: `{"q":"x"}`},
		&schema.StreamingMeta{Index: 0},
	)
	chunks := []*schema.AgenticMessage{
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{part1}},
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{part2}},
	}
	sr, err := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{streamChunks: chunks}).
		Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("go")})
	if err != nil {
		t.Fatalf("single progressive action: %v", err)
	}
	defer sr.Close()
	n := 0
	for {
		_, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			t.Fatalf("replay Recv: %v", rerr)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("replay chunks=%d want 2", n)
	}
}

// TestSingleActionGuard_ClosesInnerOnRecvErrorAndMultiAction verifies the
// inner Pipe-backed stream is Closed exactly once on recv error and multi-action.
func TestSingleActionGuard_ClosesInnerOnRecvErrorAndMultiAction(t *testing.T) {
	t.Parallel()

	// Recv error path.
	errInner, errW := schema.Pipe[*schema.AgenticMessage](1)
	go func() {
		defer errW.Close()
		_ = errW.Send(nil, errors.New("model decode boom"))
	}()
	// Wrap via a model that returns this pipe.
	mdl := &streamOnlyAgenticModel{sr: errInner}
	sr, err := wrapSingleActionAgenticModel(mdl).Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("x")})
	if err != nil {
		t.Fatalf("Stream setup: %v", err)
	}
	_, rerr := sr.Recv()
	sr.Close()
	if rerr == nil || rerr.Error() != "model decode boom" {
		t.Fatalf("recv err path: %v", rerr)
	}
	assertStreamClosedOnce(t, errInner)

	// Multi-action reject path: pipe-backed two-action stream must be Closed.
	multiInner, multiW := schema.Pipe[*schema.AgenticMessage](2)
	go func() {
		defer multiW.Close()
		_ = multiW.Send(actionChunk("a", "id", 0, false), nil)
		_ = multiW.Send(actionChunk("b", "id", 1, false), nil)
	}()
	mdl2 := &streamOnlyAgenticModel{sr: multiInner}
	sr2, err := wrapSingleActionAgenticModel(mdl2).Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("x")})
	if err != nil {
		t.Fatalf("Stream setup: %v", err)
	}
	_, rerr = sr2.Recv()
	sr2.Close()
	if !errors.Is(rerr, ErrMultiActionModelTurn) {
		t.Fatalf("multi-action: %v", rerr)
	}
	assertStreamClosedOnce(t, multiInner)
}

// TestSingleActionGuard_BlockingProducerReleasedByClose proves we do not
// infinite-drain a producer that requires reader Close to finish: with a
// capacity-1 pipe, the second Send blocks until the guard Recvs/Closes; the
// producer must exit without a secondary post-reject drain hang.
func TestSingleActionGuard_BlockingProducerReleasedByClose(t *testing.T) {
	t.Parallel()
	// Capacity 1: first Send succeeds; second blocks until reader Recv/Close.
	inner, w := schema.Pipe[*schema.AgenticMessage](1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.Close()
		// Progressive same-index fragments → one action (allowed).
		part1 := schema.NewContentBlockChunk(
			&schema.FunctionToolCall{Name: "echo", CallID: "c1", Arguments: ``},
			&schema.StreamingMeta{Index: 0},
		)
		part2 := schema.NewContentBlockChunk(
			&schema.FunctionToolCall{Name: "", CallID: "", Arguments: `{"q":"x"}`},
			&schema.StreamingMeta{Index: 0},
		)
		m1 := &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{part1}}
		m2 := &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{part2}}
		if closed := w.Send(m1, nil); closed {
			return
		}
		// Blocks until guard Recvs the first chunk (frees buffer) or Closes.
		_ = w.Send(m2, nil)
	}()
	sr, err := wrapSingleActionAgenticModel(&streamOnlyAgenticModel{sr: inner}).
		Stream(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("x")})
	if err != nil {
		t.Fatalf("expected single progressive action allowed: %v", err)
	}
	// Drain replay; upstream is already Closed by the guard.
	for {
		_, rerr := sr.Recv()
		if rerr != nil {
			break
		}
	}
	sr.Close()
	// Producer goroutine must finish (Recv+Close released any blocked Send).
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocking producer was not released by guard Close")
	}
}

// collidingStreamModel emits two tools with the same CallID on the first Stream call.
type collidingStreamModel struct{ calls atomic.Int32 }

func (m *collidingStreamModel) Generate(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return agenticmsg.AssistantText("done"), nil
}

func (m *collidingStreamModel) Stream(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m.calls.Add(1) == 1 {
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{
			actionChunk("tool_a", "collision", 0, false),
			actionChunk("tool_b", "collision", 1, false),
		}), nil
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{agenticmsg.AssistantText("done")}), nil
}

// TestSingleActionGuard_RealAgentCollidingCallIDsZeroToolExecutions builds a
// real BuildAgenticAgent → AgenticEngine → ToolsNode path and proves colliding
// multi-action is rejected with ErrMultiActionModelTurn and zero tool runs.
func TestSingleActionGuard_RealAgentCollidingCallIDsZeroToolExecutions(t *testing.T) {
	ctx := context.Background()
	var toolCalls int64
	a := &countingBudgetTool{name: "tool_a", calls: &toolCalls}
	b := &countingBudgetTool{name: "tool_b", calls: &toolCalls}
	cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
		{Tool: a, Exposure: ToolExposureDeferred},
		{Tool: b, Exposure: ToolExposureDeferred},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(&collidingStreamModel{}, []tool.BaseTool{a, b}, cat))
	if err != nil {
		t.Fatal(err)
	}
	res, runErr := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()}).Run(ctx, agent, AgenticRunInput{
		WorkspaceID: "ws-collide", RunID: "run-collide",
		Messages: []*schema.AgenticMessage{agenticmsg.UserText("go")},
	})
	var hard error
	if runErr != nil {
		hard = runErr
	} else if res != nil {
		hard = res.Err
	}
	if !errors.Is(hard, ErrMultiActionModelTurn) || atomic.LoadInt64(&toolCalls) != 0 {
		t.Fatalf("hard=%v toolCalls=%d; want ErrMultiActionModelTurn before any execution", hard, toolCalls)
	}
}

// streamOnlyAgenticModel returns a pre-built stream from Stream().
type streamOnlyAgenticModel struct {
	sr *schema.StreamReader[*schema.AgenticMessage]
}

func (m *streamOnlyAgenticModel) Generate(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return agenticmsg.AssistantText("unused"), nil
}

func (m *streamOnlyAgenticModel) Stream(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return m.sr, nil
}

// Ensure fakes implement model.AgenticModel.
var (
	_ model.AgenticModel = (*fixedResponseAgenticModel)(nil)
	_ model.AgenticModel = (*collidingStreamModel)(nil)
	_ model.AgenticModel = (*streamOnlyAgenticModel)(nil)
)

// --- Generate path strictness (equivalent to Stream) ---

func TestSingleActionAgenticModel_GenerateNilNilRejected(t *testing.T) {
	t.Parallel()
	m := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{gen: nil})
	got, err := m.Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
	if got != nil {
		t.Fatalf("got=%v want nil message", got)
	}
	if !errors.Is(err, ErrInvalidModelOutput) {
		t.Fatalf("err=%v want ErrInvalidModelOutput", err)
	}
	if !errors.Is(err, agenticmsg.ErrNilMessage) {
		t.Fatalf("err=%v want wrapped agenticmsg.ErrNilMessage", err)
	}
}

func TestSingleActionAgenticModel_GenerateMalformedRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		msg    *schema.AgenticMessage
		wantIs error
		// multi-action structural class preferred over later Validate errors
		wantMulti bool
	}{
		{
			name: "nil_block",
			msg: &schema.AgenticMessage{
				Role:          schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{nil},
			},
			wantIs: agenticmsg.ErrNilBlock,
		},
		{
			name: "invalid_role",
			msg: &schema.AgenticMessage{
				Role: "",
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.AssistantGenText{Text: "x"}),
				},
			},
			wantIs: agenticmsg.ErrInvalidRole,
		},
		{
			name: "unsupported_image",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x/i.png"}),
				},
			},
			wantIs: agenticmsg.ErrUnsupportedBlock,
		},
		{
			name: "unsupported_server_tool",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.ServerToolCall{Name: "web_search", CallID: "c1"}),
				},
			},
			wantIs: agenticmsg.ErrUnsupportedBlock,
		},
		{
			name: "unsupported_mcp",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.MCPToolCall{ServerLabel: "srv", Name: "m", CallID: "c1"}),
				},
			},
			wantIs: agenticmsg.ErrUnsupportedBlock,
		},
		{
			name: "refusal_extension",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.AssistantGenText{
						Text: "no",
						OpenAIExtension: &openai.AssistantGenTextExtension{
							Refusal: &openai.OutputRefusal{Reason: "policy"},
						},
					}),
				},
			},
			wantIs: agenticmsg.ErrUnsupportedBlock,
		},
		{
			name: "union_conflict_nil_function_payload",
			msg: &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					{Type: schema.ContentBlockTypeFunctionToolCall},
				},
			},
			// Validate/malformed — any typed fail is enough
		},
		{
			name: "malformed_single_call_empty_args",
			msg: multiFunctionCallMsg(schema.FunctionToolCall{
				Name: "echo", CallID: "c1", Arguments: "",
			}),
			wantIs: agenticmsg.ErrInvalidToolArguments,
		},
		{
			name: "malformed_single_call_non_object_args",
			msg: multiFunctionCallMsg(schema.FunctionToolCall{
				Name: "echo", CallID: "c1", Arguments: `"not-object"`,
			}),
			wantIs: agenticmsg.ErrInvalidToolArguments,
		},
		{
			name: "colliding_id_multi_action",
			msg: multiFunctionCallMsg(
				schema.FunctionToolCall{Name: "a", CallID: "same", Arguments: `{}`},
				schema.FunctionToolCall{Name: "b", CallID: "same", Arguments: `{}`},
			),
			wantMulti: true,
		},
		{
			name: "empty_id_multi_action",
			msg: multiFunctionCallMsg(
				schema.FunctionToolCall{Name: "a", CallID: "", Arguments: `{}`},
				schema.FunctionToolCall{Name: "b", CallID: "", Arguments: `{}`},
			),
			wantMulti: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := wrapSingleActionAgenticModel(&fixedResponseAgenticModel{gen: tc.msg}).
				Generate(context.Background(), []*schema.AgenticMessage{agenticmsg.UserText("hi")})
			if err == nil {
				t.Fatal("expected Generate rejection")
			}
			if tc.wantMulti {
				if !errors.Is(err, ErrMultiActionModelTurn) {
					t.Fatalf("err=%v want ErrMultiActionModelTurn", err)
				}
				return
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err=%v want errors.Is %v", err, tc.wantIs)
			}
		})
	}
}

// TestSingleActionGuard_GenerateRealTypedRunnerNonStreaming proves the
// production BuildAgenticAgent → TypedRunner (EnableStreaming=false) path:
// nil/malformed Generate outputs fail closed with zero tool executions; valid
// final text and single function call still work.
func TestSingleActionGuard_GenerateRealTypedRunnerNonStreaming(t *testing.T) {
	ctx := context.Background()

	runNonStreaming := func(t *testing.T, mdl model.AgenticModel, toolName string) (hard error, toolCalls int64) {
		t.Helper()
		var calls int64
		bt := &countingBudgetTool{name: toolName, calls: &calls}
		cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{{Tool: bt, Exposure: ToolExposureDeferred}})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat))
		if err != nil {
			t.Fatal(err)
		}
		store := newMemCheckPointStore()
		cpID, err := EnsureAgentRunCheckpointID("ws-gen-ns", "run-"+toolName, "")
		if err != nil {
			t.Fatal(err)
		}
		runner := adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
			Agent:           agent,
			EnableStreaming: false,
			CheckPointStore: store,
		})
		iter := runner.Run(ctx, []*schema.AgenticMessage{agenticmsg.UserText("go")}, adk.WithCheckPointID(cpID))
		for {
			ev, ok := iter.Next()
			if !ok {
				break
			}
			if ev == nil {
				continue
			}
			if ev.Err != nil {
				hard = ev.Err
				break
			}
		}
		return hard, atomic.LoadInt64(&calls)
	}

	t.Run("nil_generate_zero_tools", func(t *testing.T) {
		// Model always returns (nil, nil) from Generate; Stream unused when non-streaming.
		hard, calls := runNonStreaming(t, &fixedResponseAgenticModel{gen: nil}, "nil_tool")
		if hard == nil {
			t.Fatal("expected hard error for nil Generate output")
		}
		if !errors.Is(hard, ErrInvalidModelOutput) && !errors.Is(hard, agenticmsg.ErrNilMessage) {
			t.Fatalf("hard=%v want ErrInvalidModelOutput or ErrNilMessage", hard)
		}
		if calls != 0 {
			t.Fatalf("tool calls=%d want 0", calls)
		}
	})

	t.Run("malformed_generate_zero_tools", func(t *testing.T) {
		bad := &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenImage{URL: "http://x/i.png"}),
			},
		}
		hard, calls := runNonStreaming(t, &fixedResponseAgenticModel{gen: bad}, "mal_tool")
		if hard == nil {
			t.Fatal("expected hard error for malformed Generate output")
		}
		if !errors.Is(hard, agenticmsg.ErrUnsupportedBlock) {
			t.Fatalf("hard=%v want ErrUnsupportedBlock", hard)
		}
		if calls != 0 {
			t.Fatalf("tool calls=%d want 0", calls)
		}
	})

	t.Run("multi_action_generate_zero_tools", func(t *testing.T) {
		multi := multiFunctionCallMsg(
			schema.FunctionToolCall{Name: "multi_tool", CallID: "m1", Arguments: `{"q":"a"}`},
			schema.FunctionToolCall{Name: "multi_tool", CallID: "m2", Arguments: `{"q":"b"}`},
		)
		hard, calls := runNonStreaming(t, &fixedResponseAgenticModel{gen: multi}, "multi_tool")
		if hard == nil {
			t.Fatal("expected multi-action rejection")
		}
		if !errors.Is(hard, ErrMultiActionModelTurn) {
			t.Fatalf("hard=%v want ErrMultiActionModelTurn", hard)
		}
		if calls != 0 {
			t.Fatalf("tool calls=%d want 0", calls)
		}
	})

	t.Run("valid_final_text", func(t *testing.T) {
		hard, calls := runNonStreaming(t, &fixedResponseAgenticModel{
			gen: agenticmsg.AssistantText("all-good"),
		}, "final_tool")
		if hard != nil {
			t.Fatalf("valid final text: %v", hard)
		}
		if calls != 0 {
			t.Fatalf("final text must not invoke tools; calls=%d", calls)
		}
	})

	t.Run("valid_one_call", func(t *testing.T) {
		one := multiFunctionCallMsg(schema.FunctionToolCall{
			Name: "one_tool", CallID: "c1", Arguments: `{"q":"x"}`,
		})
		// After one tool result, model must finish with text (scripted via dual-response model).
		mdl := &scriptedAgenticModel{responses: []*schema.AgenticMessage{
			one,
			agenticmsg.AssistantText("after-tool"),
		}}
		hard, calls := runNonStreaming(t, mdl, "one_tool")
		if hard != nil {
			t.Fatalf("valid one call: %v", hard)
		}
		if calls != 1 {
			t.Fatalf("tool calls=%d want 1", calls)
		}
	})
}

// TestSingleActionGuard_EmptyModelStreamFailClosed proves empty model streams
// fail closed with agenticmsg.ErrEmptyConcat (strict ConcatStream / Generate
// nil-output equivalence): first Recv surfaces the error, no content chunks,
// zero tool executions. Covers Pipe-backed and array-backed streams via the
// direct wrapper and a real BuildAgenticAgent → TypedRunner path.
func TestSingleActionGuard_EmptyModelStreamFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := []*schema.AgenticMessage{agenticmsg.UserText("go")}

	// Direct wrapper: array-backed empty stream.
	t.Run("array_direct_wrapper", func(t *testing.T) {
		t.Parallel()
		inner := &fixedResponseAgenticModel{streamChunks: []*schema.AgenticMessage{}}
		sr, err := wrapSingleActionAgenticModel(inner).Stream(ctx, input)
		if err != nil {
			t.Fatalf("Stream setup: %v", err)
		}
		defer sr.Close()
		chunk, rerr := sr.Recv()
		if chunk != nil {
			t.Fatalf("content chunk on empty stream: %+v", chunk)
		}
		if !errors.Is(rerr, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("Recv err=%v want ErrEmptyConcat", rerr)
		}
		// No further content; second Recv after error may be EOF or same err.
	})

	// Direct wrapper: Pipe-backed empty stream; upstream Closed exactly once.
	t.Run("pipe_direct_wrapper_close_once", func(t *testing.T) {
		t.Parallel()
		emptyPipe := pipeAgenticStream() // zero chunks
		mdl := &streamOnlyAgenticModel{sr: emptyPipe}
		sr, err := wrapSingleActionAgenticModel(mdl).Stream(ctx, input)
		if err != nil {
			t.Fatalf("Stream setup: %v", err)
		}
		chunk, rerr := sr.Recv()
		sr.Close()
		if chunk != nil {
			t.Fatalf("content chunk on empty pipe stream: %+v", chunk)
		}
		if !errors.Is(rerr, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("Recv err=%v want ErrEmptyConcat", rerr)
		}
		assertStreamClosedOnce(t, emptyPipe)
	})

	// Real BuildAgenticAgent + streaming TypedRunner / AgenticEngine: empty
	// model stream must hard-fail with ErrEmptyConcat and execute zero tools.
	t.Run("real_agent_empty_stream_zero_tools", func(t *testing.T) {
		var toolCalls int64
		bt := &countingBudgetTool{name: "empty_stream_tool", calls: &toolCalls}
		cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
			{Tool: bt, Exposure: ToolExposureDeferred},
		})
		if err != nil {
			t.Fatal(err)
		}
		// streamChunks non-nil empty slice → Stream returns empty array reader.
		// gen set so Generate path is not accidentally used as nil.
		mdl := &fixedResponseAgenticModel{
			gen:          agenticmsg.AssistantText("must-not-reach-generate"),
			streamChunks: []*schema.AgenticMessage{},
		}
		agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(mdl, []tool.BaseTool{bt}, cat))
		if err != nil {
			t.Fatal(err)
		}
		res, runErr := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()}).Run(ctx, agent, AgenticRunInput{
			WorkspaceID: "ws-empty-stream",
			RunID:       "run-empty-stream",
			Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
		})
		var hard error
		if runErr != nil {
			hard = runErr
		} else if res != nil {
			hard = res.Err
		}
		if !errors.Is(hard, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("hard=%v want ErrEmptyConcat; res=%+v runErr=%v", hard, res, runErr)
		}
		if atomic.LoadInt64(&toolCalls) != 0 {
			t.Fatalf("toolCalls=%d want 0", toolCalls)
		}
		if res != nil && (res.Interrupted || len(res.InterruptContextIDs) != 0) {
			t.Fatalf("must not interrupt on empty stream: %+v", res)
		}
	})

	// Real agent with Pipe-backed empty model stream (Close-once on upstream).
	t.Run("real_agent_pipe_empty_stream_zero_tools", func(t *testing.T) {
		var toolCalls int64
		bt := &countingBudgetTool{name: "pipe_empty_tool", calls: &toolCalls}
		cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
			{Tool: bt, Exposure: ToolExposureDeferred},
		})
		if err != nil {
			t.Fatal(err)
		}
		emptyPipe := pipeAgenticStream()
		agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(&streamOnlyAgenticModel{sr: emptyPipe}, []tool.BaseTool{bt}, cat))
		if err != nil {
			t.Fatal(err)
		}
		res, runErr := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()}).Run(ctx, agent, AgenticRunInput{
			WorkspaceID: "ws-pipe-empty",
			RunID:       "run-pipe-empty",
			Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
		})
		var hard error
		if runErr != nil {
			hard = runErr
		} else if res != nil {
			hard = res.Err
		}
		if !errors.Is(hard, agenticmsg.ErrEmptyConcat) {
			t.Fatalf("hard=%v want ErrEmptyConcat; res=%+v runErr=%v", hard, res, runErr)
		}
		if atomic.LoadInt64(&toolCalls) != 0 {
			t.Fatalf("toolCalls=%d want 0", toolCalls)
		}
		assertStreamClosedOnce(t, emptyPipe)
	})
}

// dualReturnAgenticModel returns a configured (reader, error) pair from Stream
// to exercise the legal (non-nil reader, non-nil error) model contract.
type dualReturnAgenticModel struct {
	sr  *schema.StreamReader[*schema.AgenticMessage]
	err error
}

func (m *dualReturnAgenticModel) Generate(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return agenticmsg.AssistantText("unused"), nil
}

func (m *dualReturnAgenticModel) Stream(ctx context.Context, in []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return m.sr, m.err
}

var _ model.AgenticModel = (*dualReturnAgenticModel)(nil)

// TestSingleActionGuard_StreamClosesUpstreamOnReaderPlusError proves the guard
// owns a non-nil upstream reader returned with a non-nil Stream error: Close
// exactly once, propagate the original error identity, expose no chunks, and
// never hand the leaked reader to callers. Also covers nil-reader+error and
// real BuildAgenticAgent propagation with zero tool executions.
func TestSingleActionGuard_StreamClosesUpstreamOnReaderPlusError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := []*schema.AgenticMessage{agenticmsg.UserText("go")}
	streamBoom := errors.New("underlying stream setup boom")

	// Pipe-backed reader + error: Close once; original error preserved; no reader returned.
	t.Run("pipe_reader_plus_error_close_once", func(t *testing.T) {
		t.Parallel()
		// Capacity-1 pipe with a blocked/pending chunk so an unclosed reader would leak.
		upstream, w := schema.Pipe[*schema.AgenticMessage](1)
		// Leave a sendable chunk; writer not closed — reader must still be Closed by guard.
		go func() {
			// Best-effort send; Close of reader unblocks if Send ever blocks.
			_ = w.Send(agenticmsg.AssistantText("must-not-be-read"), nil)
			// Do not Close writer; ownership is on the returned reader.
		}()
		mdl := &dualReturnAgenticModel{sr: upstream, err: streamBoom}
		sr, err := wrapSingleActionAgenticModel(mdl).Stream(ctx, input)
		if sr != nil {
			t.Fatalf("Stream must not return reader on error path, got %v", sr)
		}
		if !errors.Is(err, streamBoom) {
			t.Fatalf("err=%v want errors.Is streamBoom", err)
		}
		if err != streamBoom {
			// Prefer exact identity preservation when the interface permits.
			t.Fatalf("err identity lost: got %v (%T) want exact streamBoom", err, err)
		}
		assertStreamClosedOnce(t, upstream)
	})

	// Nil reader + error: no panic; original error propagates; nil reader returned.
	t.Run("nil_reader_plus_error", func(t *testing.T) {
		t.Parallel()
		mdl := &dualReturnAgenticModel{sr: nil, err: streamBoom}
		sr, err := wrapSingleActionAgenticModel(mdl).Stream(ctx, input)
		if sr != nil {
			t.Fatalf("Stream must return nil reader, got %v", sr)
		}
		if !errors.Is(err, streamBoom) || err != streamBoom {
			t.Fatalf("err=%v want exact streamBoom", err)
		}
	})

	// Real builder path: Stream (reader, error) surfaces as hard failure with zero tools.
	t.Run("real_agent_reader_plus_error_zero_tools", func(t *testing.T) {
		var toolCalls int64
		bt := &countingBudgetTool{name: "dual_ret_tool", calls: &toolCalls}
		cat, err := BuildToolCatalog(ctx, []ToolCatalogBuildEntry{
			{Tool: bt, Exposure: ToolExposureDeferred},
		})
		if err != nil {
			t.Fatal(err)
		}
		upstream, w := schema.Pipe[*schema.AgenticMessage](1)
		go func() {
			_ = w.Send(agenticmsg.AssistantText("must-not-execute-tools"), nil)
		}()
		agent, err := BuildAgenticAgent(ctx, baseAgenticCfg(&dualReturnAgenticModel{sr: upstream, err: streamBoom}, []tool.BaseTool{bt}, cat))
		if err != nil {
			t.Fatal(err)
		}
		res, runErr := NewAgenticEngine(AgenticEngineConfig{Store: newMemCheckPointStore()}).Run(ctx, agent, AgenticRunInput{
			WorkspaceID: "ws-dual-ret",
			RunID:       "run-dual-ret",
			Messages:    []*schema.AgenticMessage{agenticmsg.UserText("go")},
		})
		var hard error
		if runErr != nil {
			hard = runErr
		} else if res != nil {
			hard = res.Err
		}
		if hard == nil {
			t.Fatalf("expected hard error from Stream (reader,error); res=%+v", res)
		}
		if !errors.Is(hard, streamBoom) && !strings.Contains(hard.Error(), streamBoom.Error()) {
			t.Fatalf("hard=%v want streamBoom family", hard)
		}
		if atomic.LoadInt64(&toolCalls) != 0 {
			t.Fatalf("toolCalls=%d want 0", toolCalls)
		}
		if res != nil && (res.Interrupted || len(res.InterruptContextIDs) != 0) {
			t.Fatalf("must not interrupt on Stream error: %+v", res)
		}
		assertStreamClosedOnce(t, upstream)
	})
}
