package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// MaxExecutableActionsPerTurn is how many function/tool-search calls from one
// model output are executed (in order). ToolsNode still sees one call per visit
// so HITL can pause before later calls and Stream races on ≥2 EnhancedStreamable
// tools are avoided. Extra calls above this cap are dropped. Each queued replay
// still consumes one agent iteration but does not call the inner model.
const MaxExecutableActionsPerTurn = 8

const queuedActionsRunLocalKey = "einoruntime.agentic.queued_actions"

// Gob names for checkpointed packed-call leftovers (run-local Extra).
const (
	queuedActionRegisterName  = "actweave_queued_exec_action_v1"
	queuedActionsRegisterName = "actweave_queued_exec_actions_v1"
)

// ErrMultiActionModelTurn is returned when a model turn cannot be serialized
// into one-call-per-ToolsNode visits (duplicate CallIDs). Unique-CallID extras
// are queued for later visits instead.
var ErrMultiActionModelTurn = errors.New("einoruntime agentic: multi-action model turn rejected")

// errQueuedActionsNeedAgentRun is returned when extras cannot be checkpointed
// because Generate/Stream ran outside an agent Run (wrapper unit tests).
var errQueuedActionsNeedAgentRun = errors.New("einoruntime agentic: queued actions require agent run-local state")

// ErrInvalidModelOutput is returned when a model Generate path yields a nil
// message with a nil error (invalid success-shaped output). Wrapped with
// agenticmsg.ErrNilMessage so Task 1 typed causes remain errors.Is matchable.
var ErrInvalidModelOutput = errors.New("einoruntime agentic: invalid model output")

func init() {
	schema.RegisterName[queuedExecutableAction](queuedActionRegisterName)
	schema.RegisterName[[]queuedExecutableAction](queuedActionsRegisterName)
}

// queuedExecutableAction is gob-safe remaining work from a packed model turn.
type queuedExecutableAction struct {
	Name      string
	CallID    string
	Arguments string
	Search    bool
}

func wrapSingleActionAgenticModel(inner model.AgenticModel) model.AgenticModel {
	if inner == nil {
		return nil
	}
	if _, ok := inner.(*singleActionAgenticModel); ok {
		return inner
	}
	return &singleActionAgenticModel{inner: inner}
}

// singleActionAgenticModel keeps ToolsNode at one executable action per visit.
// Packed unique-CallID extras are queued in run-local state and replayed on
// later Generate/Stream without calling the inner model.
type singleActionAgenticModel struct {
	inner model.AgenticModel
}

func (m *singleActionAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if m == nil || m.inner == nil {
		return nil, errors.New("einoruntime agentic: nil model")
	}
	if queued, ok, err := popQueuedAction(ctx); err != nil {
		return nil, err
	} else if ok {
		return queued.validatedMessage()
	}
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("%w: model Generate returned (nil, nil): %w", ErrInvalidModelOutput, agenticmsg.ErrNilMessage)
	}
	if err := agenticmsg.Validate(msg); err != nil {
		return nil, err
	}
	return serializePackedActions(ctx, msg)
}

func (m *singleActionAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if m == nil || m.inner == nil {
		return nil, errors.New("einoruntime agentic: nil model")
	}
	if queued, ok, err := popQueuedAction(ctx); err != nil {
		return nil, err
	} else if ok {
		msg, verr := queued.validatedMessage()
		if verr != nil {
			return nil, verr
		}
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{msg}), nil
	}
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		if sr != nil {
			sr.Close()
		}
		return nil, err
	}
	if sr == nil {
		return nil, errors.New("einoruntime agentic: nil model stream")
	}
	return bufferValidateSingleActionStream(ctx, sr)
}

func serializePackedActions(ctx context.Context, msg *schema.AgenticMessage) (*schema.AgenticMessage, error) {
	if err := rejectDuplicateActionCallIDs(msg); err != nil {
		return nil, err
	}
	kept, queued, dropped := splitExecutableActions(msg)
	if len(queued) > 0 {
		if err := enqueueQueuedActions(ctx, queued); err != nil {
			// Wrapper unit tests call Generate/Stream without an agent Run.
			// Keep the first action so the split stays observable; extras drop.
			if !errors.Is(err, errQueuedActionsNeedAgentRun) {
				return nil, fmt.Errorf("einoruntime agentic: cannot persist queued actions: %w", err)
			}
		}
	}
	if dropped > 0 {
		slog.Warn("einoruntime agentic: dropping extra executable actions over per-turn cap",
			"kept", 1, "queued", len(queued), "dropped", dropped, "cap", MaxExecutableActionsPerTurn)
	}
	return kept, nil
}

func rejectDuplicateActionCallIDs(msg *schema.AgenticMessage) error {
	seen := map[string]struct{}{}
	for _, block := range executableActionBlocks(msg) {
		id := block.FunctionToolCall.CallID
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: duplicate CallID %q", ErrMultiActionModelTurn, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func executableActionBlocks(msg *schema.AgenticMessage) []*schema.ContentBlock {
	if msg == nil {
		return nil
	}
	var out []*schema.ContentBlock
	for _, block := range msg.ContentBlocks {
		if isExecutableActionBlock(block) {
			out = append(out, block)
		}
	}
	return out
}

func isExecutableActionBlock(block *schema.ContentBlock) bool {
	return block != nil && block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil
}

func splitExecutableActions(msg *schema.AgenticMessage) (kept *schema.AgenticMessage, queued []queuedExecutableAction, dropped int) {
	if msg == nil {
		return nil, nil, 0
	}
	out := make([]*schema.ContentBlock, 0, len(msg.ContentBlocks))
	keptAction := false
	for _, block := range msg.ContentBlocks {
		if !isExecutableActionBlock(block) {
			out = append(out, block)
			continue
		}
		if !keptAction {
			out = append(out, block)
			keptAction = true
			continue
		}
		if len(queued) < MaxExecutableActionsPerTurn-1 {
			queued = append(queued, queuedFromBlock(block))
			continue
		}
		dropped++
	}
	clone := *msg
	clone.ContentBlocks = out
	return &clone, queued, dropped
}

func queuedFromBlock(block *schema.ContentBlock) queuedExecutableAction {
	q := queuedExecutableAction{
		Name:      block.FunctionToolCall.Name,
		CallID:    block.FunctionToolCall.CallID,
		Arguments: block.FunctionToolCall.Arguments,
	}
	if agenticopenai.GetToolSearchToolCall(block) {
		q.Search = true
	}
	return q
}

func (q queuedExecutableAction) toMessage() *schema.AgenticMessage {
	call := schema.FunctionToolCall{Name: q.Name, CallID: q.CallID, Arguments: q.Arguments}
	block := schema.NewContentBlock(&call)
	if q.Search {
		block.Extra = map[string]any{"openai-tool-search-tool-call": true}
	}
	return &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{block},
	}
}

func (q queuedExecutableAction) validatedMessage() (*schema.AgenticMessage, error) {
	msg := q.toMessage()
	if err := agenticmsg.Validate(msg); err != nil {
		return nil, fmt.Errorf("einoruntime agentic: queued action replay: %w", err)
	}
	return msg, nil
}

func enqueueQueuedActions(ctx context.Context, extra []queuedExecutableAction) error {
	if len(extra) == 0 {
		return nil
	}
	cur, inRun, err := loadQueuedActions(ctx)
	if err != nil {
		return err
	}
	if !inRun {
		return errQueuedActionsNeedAgentRun
	}
	out := make([]queuedExecutableAction, 0, len(cur)+len(extra))
	out = append(out, cur...)
	out = append(out, extra...)
	return adk.SetRunLocalValue(ctx, queuedActionsRunLocalKey, out)
}

func popQueuedAction(ctx context.Context) (queuedExecutableAction, bool, error) {
	cur, inRun, err := loadQueuedActions(ctx)
	if err != nil {
		return queuedExecutableAction{}, false, err
	}
	if !inRun || len(cur) == 0 {
		return queuedExecutableAction{}, false, nil
	}
	next := cur[0]
	rest := cloneQueuedActions(cur[1:])
	if rest == nil {
		rest = []queuedExecutableAction{}
	}
	if err := adk.SetRunLocalValue(ctx, queuedActionsRunLocalKey, rest); err != nil {
		return queuedExecutableAction{}, false, err
	}
	return next, true, nil
}

func loadQueuedActions(ctx context.Context) ([]queuedExecutableAction, bool, error) {
	v, found, err := adk.GetRunLocalValue(ctx, queuedActionsRunLocalKey)
	if err != nil {
		// Outside an agent Run there is no queue (wrapper unit tests).
		return nil, false, nil
	}
	if !found || v == nil {
		return nil, true, nil
	}
	switch q := v.(type) {
	case []queuedExecutableAction:
		return cloneQueuedActions(q), true, nil
	default:
		return nil, true, fmt.Errorf("einoruntime agentic: corrupt queued actions type %T", v)
	}
}

func cloneQueuedActions(in []queuedExecutableAction) []queuedExecutableAction {
	if len(in) == 0 {
		if in == nil {
			return nil
		}
		return []queuedExecutableAction{}
	}
	out := make([]queuedExecutableAction, len(in))
	copy(out, in)
	return out
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
// chunks with the Task 1 agenticmsg protocol, then serializes packed actions
// so ToolsNode sees one call (extras queued for later visits).
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
func bufferValidateSingleActionStream(ctx context.Context, inner *schema.StreamReader[*schema.AgenticMessage]) (*schema.StreamReader[*schema.AgenticMessage], error) {
	chunks, err := drainAndCloseModelStream(inner)
	if err != nil {
		return errorOnlyAgenticStream(err), nil
	}
	msg, err := validateConcatAndSerializePackedActions(ctx, chunks)
	if err != nil {
		return errorOnlyAgenticStream(err), nil
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{msg}), nil
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

func validateConcatAndSerializePackedActions(ctx context.Context, chunks []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
	if len(chunks) == 0 {
		return nil, agenticmsg.ErrEmptyConcat
	}
	for i, c := range chunks {
		if c == nil {
			return nil, fmt.Errorf("%w: at index %d", agenticmsg.ErrNilChunk, i)
		}
		if err := agenticmsg.ValidateStreamChunk(c); err != nil {
			return nil, fmt.Errorf("einoruntime agentic: stream chunk %d: %w", i, err)
		}
	}
	msg, err := agenticmsg.ConcatStream(chunks)
	if err != nil {
		return nil, err
	}
	return serializePackedActions(ctx, msg)
}
