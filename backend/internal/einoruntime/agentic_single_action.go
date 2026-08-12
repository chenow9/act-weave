package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// ErrMultiActionModelTurn is returned when a single assistant/model turn
// contains more than one executable action (ordinary function_tool_call and/or
// native client tool-search call). Production agents must never hand multi-action
// turns to ToolsNode: pinned Eino ToolsNode.Stream races on a shared err capture
// even with ExecuteSequentially=true when >=2 EnhancedStreamable tools run.
//
// One executable action is allowed; zero actions (final text / reasoning) is allowed.
// errors.Is matchable.
var ErrMultiActionModelTurn = errors.New("einoruntime agentic: multi-action model turn rejected")

// ErrInvalidModelOutput is returned when a model Generate path yields a nil
// message with a nil error (invalid success-shaped output). Wrapped with
// agenticmsg.ErrNilMessage so Task 1 typed causes remain errors.Is matchable.
var ErrInvalidModelOutput = errors.New("einoruntime agentic: invalid model output")

// wrapSingleActionAgenticModel is the production AgenticModel boundary used
// unconditionally by BuildAgenticAgent. It fails closed before ToolsNode sees
// multi-action Generate/Stream outputs, regardless of which concrete model
// implementation the caller supplies.
func wrapSingleActionAgenticModel(inner model.AgenticModel) model.AgenticModel {
	if inner == nil {
		return nil
	}
	// Avoid double-wrapping when tests/helpers re-wrap an already-guarded model.
	if _, ok := inner.(*singleActionAgenticModel); ok {
		return inner
	}
	return &singleActionAgenticModel{inner: inner}
}

// singleActionAgenticModel enforces at most one executable action per model turn
// on both Generate and Stream paths. Generate is equivalent in strictness to
// Stream: multi-action block-count first, then full agenticmsg protocol
// validation before any payload reaches Eino / ToolsNode.
type singleActionAgenticModel struct {
	inner model.AgenticModel
}

func (m *singleActionAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if m == nil || m.inner == nil {
		return nil, errors.New("einoruntime agentic: nil model")
	}
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	// (nil, nil) is invalid model output — never return success-shaped nil.
	if msg == nil {
		return nil, fmt.Errorf("%w: model Generate returned (nil, nil): %w", ErrInvalidModelOutput, agenticmsg.ErrNilMessage)
	}
	// Order matches Stream validateConcatAndRejectMultiAction:
	//  1. count executable action content blocks (not CallID identity);
	//     ErrMultiActionModelTurn for n>1 so colliding/empty-ID multi-actions
	//     keep the structural classification rather than a later Validate miss;
	//  2. strict agenticmsg.Validate for all remaining content/role/union/
	//     protocol invariants (nil blocks, unsupported server/MCP/media/refusal,
	//     union conflicts, malformed single calls/arguments/extensions).
	if err := rejectMultiActionMessage(msg); err != nil {
		return nil, err
	}
	if err := agenticmsg.Validate(msg); err != nil {
		// Preserve typed agenticmsg causes for errors.Is.
		return nil, err
	}
	return msg, nil
}

func (m *singleActionAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m == nil || m.inner == nil {
		return nil, errors.New("einoruntime agentic: nil model")
	}
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		// Underlying Stream may legally return (non-nil reader, non-nil error).
		// Own and Close any non-nil reader exactly once before propagating the
		// original Stream error; do not read or expose chunks. Preserve error
		// identity (errors.Is / ==) as far as the interface permits.
		if sr != nil {
			sr.Close()
		}
		return nil, err
	}
	if sr == nil {
		return nil, errors.New("einoruntime agentic: nil model stream")
	}
	// Fail closed before Eino / ToolsNode observes any chunk: fully buffer the
	// inner stream, Close it promptly, validate+concat via agenticmsg, then
	// count final executable action content blocks (not unique CallIDs).
	return bufferValidateSingleActionStream(sr)
}

// rejectMultiActionMessage fails closed when msg carries more than one
// executable action content block. Nil message is not multi-action
// (caller/engine handles nil). Counting is by content-block cardinality only —
// identical/colliding/empty CallIDs do not collapse actions.
func rejectMultiActionMessage(msg *schema.AgenticMessage) error {
	n, err := countExecutableActions(msg)
	if err != nil {
		return err
	}
	if n > 1 {
		return fmt.Errorf("%w: got %d executable actions in one model turn", ErrMultiActionModelTurn, n)
	}
	return nil
}

// countExecutableActions counts ordinary function_tool_call blocks and native
// client tool-search calls (both are ContentBlockTypeFunctionToolCall; search is
// marked via agenticopenai Extra but remains a function-tool-call block).
// Nil blocks are ignored for counting (malformed content is handled by agenticmsg
// validation elsewhere). A nil message is zero actions.
//
// Deliberately does NOT dedupe by CallID: two blocks with the same CallID (or
// empty IDs) are two executable actions.
func countExecutableActions(msg *schema.AgenticMessage) (int, error) {
	if msg == nil {
		return 0, nil
	}
	n := 0
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		if block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil {
			n++
		}
	}
	return n, nil
}

// bufferValidateSingleActionStream fully reads and buffers the inner model
// stream, Closes the inner reader exactly once, validates and concatenates
// chunks with the Task 1 agenticmsg protocol, then rejects multi-action turns
// by counting final executable action content blocks (never unique CallIDs).
//
// Tradeoff: intentional full buffering adds latency and peak memory equal to
// one model-turn stream. That is accepted for structural safety — ToolsNode
// must never observe a multi-action stream (pinned Eino races on >=2
// EnhancedStreamable tools), and progressive per-chunk identity heuristics are
// bypassable via CallID collisions across StreamingMeta indexes.
//
// On success, returns a replay reader over the original valid chunks. The
// upstream reader is already Closed, so consumer abandonment of the replay
// cannot retain upstream resources. On any error path the upstream is still
// Closed exactly once; failures surface as a reader whose first Recv yields the
// error and never a content chunk (Eino observes no multi-action payload).
//
// Contract: do not infinite-drain after Close. The pinned StreamReader contract
// is Recv-until-EOF-or-error then Close exactly once; Close unblocks a blocked
// producer Send. There is no secondary post-reject drain loop.
func bufferValidateSingleActionStream(inner *schema.StreamReader[*schema.AgenticMessage]) (*schema.StreamReader[*schema.AgenticMessage], error) {
	chunks, err := drainAndCloseModelStream(inner)
	if err != nil {
		// Upstream already Closed; expose error on Recv so Stream setup itself
		// is not the only surface (both Stream-err and first-Recv-err consumers
		// fail closed without seeing content).
		return errorOnlyAgenticStream(err), nil
	}
	if err := validateConcatAndRejectMultiAction(chunks); err != nil {
		return errorOnlyAgenticStream(err), nil
	}
	// Replay original chunks (array reader; Close is a no-op; no upstream).
	return schema.StreamReaderFromArray(chunks), nil
}

// errorOnlyAgenticStream returns a reader that yields err on the first Recv
// and never delivers a content chunk. Used so multi-action / validation
// failures fail closed before any model payload is observed.
func errorOnlyAgenticStream(err error) *schema.StreamReader[*schema.AgenticMessage] {
	if err == nil {
		err = errors.New("einoruntime agentic: empty stream error")
	}
	sr, sw := schema.Pipe[*schema.AgenticMessage](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(nil, err)
	}()
	return sr
}

// drainAndCloseModelStream receives every chunk until EOF/error and always
// Closes the reader exactly once. Nil chunks fail closed. Does not attempt a
// second drain after Close (producers that require Close to terminate are
// released by Close, not by infinite Recv).
func drainAndCloseModelStream(sr *schema.StreamReader[*schema.AgenticMessage]) (chunks []*schema.AgenticMessage, err error) {
	if sr == nil {
		return nil, errors.New("einoruntime agentic: nil model stream")
	}
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			sr.Close()
		}
	}
	defer closeOnce()

	for {
		chunk, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			return chunks, nil
		}
		if rerr != nil {
			return nil, rerr
		}
		if chunk == nil {
			return nil, errors.New("einoruntime agentic: nil stream chunk from model")
		}
		chunks = append(chunks, chunk)
	}
}

// validateConcatAndRejectMultiAction applies the strict agenticmsg stream
// protocol then rejects multi-action turns by final content-block count.
//
// Order:
//  1. Empty stream → agenticmsg.ErrEmptyConcat (fail closed; strict-equivalent
//     to Generate (nil,nil) in that no success-shaped empty payload is
//     observed; Stream surfaces ErrEmptyConcat on first Recv via
//     errorOnlyAgenticStream, never a clean empty replay).
//  2. ValidateStreamChunk each chunk (fail closed on malformed fragments).
//  3. schema.ConcatAgenticMessages to materialize final content blocks
//     (StreamingMeta.Index groups progressive fragments of one action;
//     distinct indexes remain distinct actions even with colliding CallIDs).
//  4. Count executable action content blocks; reject n > 1 as
//     ErrMultiActionModelTurn before complete Validate (so multi-action is not
//     masked by a later completeness error on empty/partial IDs).
//  5. agenticmsg.ConcatStream for full protocol (stream validate + concat +
//     complete Validate / stream-only index normalization).
func validateConcatAndRejectMultiAction(chunks []*schema.AgenticMessage) error {
	// Fail closed on zero chunks — never treat empty model stream as valid
	// zero-action EOF. Preserve errors.Is(..., agenticmsg.ErrEmptyConcat).
	if len(chunks) == 0 {
		return agenticmsg.ErrEmptyConcat
	}
	for i, c := range chunks {
		if c == nil {
			return fmt.Errorf("%w: at index %d", agenticmsg.ErrNilChunk, i)
		}
		if err := agenticmsg.ValidateStreamChunk(c); err != nil {
			return fmt.Errorf("einoruntime agentic: stream chunk %d: %w", i, err)
		}
	}
	raw, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		return fmt.Errorf("%w: %v", agenticmsg.ErrConcat, err)
	}
	if err := rejectMultiActionMessage(raw); err != nil {
		return err
	}
	// Full Task 1 protocol on the same chunks (complete-message Validate).
	if _, err := agenticmsg.ConcatStream(chunks); err != nil {
		return err
	}
	return nil
}
