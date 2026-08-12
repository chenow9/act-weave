package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
)

// AgenticEngine drives a TypedChatModelAgent[*schema.AgenticMessage] via
// adk.NewTypedRunner with true streaming. It is the production-target engine
// boundary for the Agentic migration (design §6.4). Classic Engine remains for
// un-migrated callers; this type contains no classic schema.Message APIs.
type AgenticEngine struct {
	store adk.CheckPointStore
}

// AgenticEngineConfig constructs an AgenticEngine.
type AgenticEngineConfig struct {
	// Store is the ADK checkpoint store. Optional for text-only runs that
	// never interrupt; required for HITL interrupt persistence.
	Store adk.CheckPointStore
}

// NewAgenticEngine builds an AgenticEngine.
func NewAgenticEngine(cfg AgenticEngineConfig) *AgenticEngine {
	return &AgenticEngine{store: cfg.Store}
}

// AgenticRunInput is one Agentic agent execution request.
type AgenticRunInput struct {
	// WorkspaceID is required for checkpoint ID allocation.
	WorkspaceID string
	// RunID is the agent_run owner segment (OwnerID).
	RunID string
	// CheckpointID, when non-empty and valid for this workspace/run, is reused
	// (stable once-per-run). Empty → allocate a new nonce once.
	CheckpointID string
	// Messages is the Agentic conversation input. Validated with
	// agenticmsg.ValidateConversation (complete conversation rules).
	Messages []*schema.AgenticMessage
}

// AgenticResumeInput continues an interrupted Agentic agent run.
// Initial messages are not accepted or reloaded on resume.
type AgenticResumeInput struct {
	// WorkspaceID is required for checkpoint ID validation.
	WorkspaceID string
	// RunID is the agent_run owner segment.
	RunID string
	// CheckpointID is the stable once-per-run ID from the prior pause (required).
	CheckpointID string
	// Targets is adk.ResumeParams.Targets (interruptId → tool result).
	// Empty Targets is rejected so v1 never silent-resume-all without tool results.
	Targets map[string]any
}

// Sentinel errors for AgenticEngine fail-closed paths (errors.Is).
var (
	// ErrNilTypedEventIterator is returned when the typed ADK event iterator is nil.
	ErrNilTypedEventIterator = errors.New("einoruntime agentic engine: nil typed event iterator")
	// ErrNilTypedEvent is returned when a nil *TypedAgentEvent is yielded.
	ErrNilTypedEvent = errors.New("einoruntime agentic engine: nil typed event")
	// ErrNilMessageStream is returned when a streaming message output has a nil stream.
	ErrNilMessageStream = errors.New("einoruntime agentic engine: nil message stream")
	// ErrMalformedMessageVariant is returned when a TypedMessageVariant is
	// internally inconsistent: MessageStream present while IsStreaming=false,
	// or Message and MessageStream both non-nil. The engine still owns and
	// Closes any non-nil MessageStream exactly once before failing closed.
	ErrMalformedMessageVariant = errors.New("einoruntime agentic engine: malformed message variant")
	// ErrMalformedInterrupt is returned when an interrupt event lacks a usable
	// resume identity (nil context, empty/whitespace ID, empty contexts, etc.).
	// Never returns Interrupted=true without a valid resume target.
	// Also returned when the same context/root ID appears more than once across
	// the typed iterator (ResumeParams.Targets is a map; duplicates collapse).
	ErrMalformedInterrupt = errors.New("einoruntime agentic engine: malformed interrupt")
)

// AgenticRunResult is the outcome of AgenticEngine.Run / Resume.
// Interrupt is not a hard failure: Interrupted=true with InterruptContextIDs.
type AgenticRunResult struct {
	// CheckpointID is the stable ID used for this run (allocated or ensured).
	CheckpointID string
	// Interrupted is true when the agent paused for HITL (tool confirmation).
	Interrupted bool
	// InterruptContextIDs are InterruptCtx.ID values for ResumeWithParams Targets.
	InterruptContextIDs []string
	// RootCauseInterruptIDs are IsRootCause interrupt IDs (preferred resume targets).
	RootCauseInterruptIDs []string
	// FinalAssistantText is concatenated assistant text for this run
	// (via agenticmsg.ExtractAssistantText / ConcatStream).
	FinalAssistantText string
	// Err is a hard failure (budget / model / validation / internal).
	// Nil on success or clean interrupt.
	Err error
}

// Run executes agent with EnableStreaming and a once-per-run checkpoint ID.
//
// Messages are validated with agenticmsg.ValidateConversation before the runner
// starts. Nil messages/events/streams and malformed content fail closed.
// On tool HITL interrupt, captures InterruptContext IDs and returns
// (result, nil) — callers must not treat interrupt as a hard error.
func (e *AgenticEngine) Run(
	ctx context.Context,
	agent adk.TypedAgent[*schema.AgenticMessage],
	in AgenticRunInput,
) (*AgenticRunResult, error) {
	if agent == nil {
		return nil, errors.New("einoruntime agentic engine: agent is required")
	}
	if len(in.Messages) == 0 {
		return nil, errors.New("einoruntime agentic engine: messages are required")
	}
	// Reject nil message slots before conversation validation.
	for i, m := range in.Messages {
		if m == nil {
			return nil, fmt.Errorf("einoruntime agentic engine: messages[%d] is nil: %w", i, agenticmsg.ErrNilMessage)
		}
	}
	if err := agenticmsg.ValidateConversation(in.Messages); err != nil {
		return nil, fmt.Errorf("einoruntime agentic engine: invalid conversation: %w", err)
	}

	cpID, err := EnsureAgentRunCheckpointID(in.WorkspaceID, in.RunID, in.CheckpointID)
	if err != nil {
		return nil, err
	}

	runner := e.newTypedRunner(ctx, agent)
	iter := runner.Run(ctx, in.Messages, adk.WithCheckPointID(cpID))
	return e.consumeTypedIterator(ctx, cpID, iter)
}

// Resume continues from a checkpoint with explicit Targets.
// Does not accept or reload initial messages.
//
// WorkspaceID and RunID are always required; ownership is validated via
// EnsureAgentRunCheckpointID unconditionally (missing either fails closed).
func (e *AgenticEngine) Resume(
	ctx context.Context,
	agent adk.TypedAgent[*schema.AgenticMessage],
	in AgenticResumeInput,
) (*AgenticRunResult, error) {
	if agent == nil {
		return nil, errors.New("einoruntime agentic engine: agent is required")
	}
	cpID := strings.TrimSpace(in.CheckpointID)
	if cpID == "" {
		return nil, errors.New("einoruntime agentic engine: checkpoint ID is required for resume")
	}
	ws := strings.TrimSpace(in.WorkspaceID)
	run := strings.TrimSpace(in.RunID)
	if ws == "" {
		return nil, errors.New("einoruntime agentic engine: workspace ID is required for resume")
	}
	if run == "" {
		return nil, errors.New("einoruntime agentic engine: run ID is required for resume")
	}
	ensured, err := EnsureAgentRunCheckpointID(ws, run, cpID)
	if err != nil {
		return nil, err
	}
	cpID = ensured
	if len(in.Targets) == 0 {
		return nil, errors.New("einoruntime agentic engine: resume Targets are required (EINO_INTERRUPT_ID_MISSING)")
	}

	runner := e.newTypedRunner(ctx, agent)
	iter, err := runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{Targets: in.Targets})
	if err != nil {
		return nil, err
	}
	return e.consumeTypedIterator(ctx, cpID, iter)
}

func (e *AgenticEngine) newTypedRunner(
	ctx context.Context,
	agent adk.TypedAgent[*schema.AgenticMessage],
) *adk.TypedRunner[*schema.AgenticMessage] {
	var store adk.CheckPointStore
	if e != nil {
		store = e.store
	}
	return adk.NewTypedRunner(adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: true, // D14 — true Stream path
		CheckPointStore: store,
	})
}

// consumeTypedIterator drains typed Agentic events without type erasure to
// classic schema.Message. Uses agenticmsg helpers for text extraction/concat.
// A nil iterator fails closed (never success).
//
// Ownership: every non-nil event MessageStream is Closed exactly once —
// regardless of IsStreaming — on EOF success, decode/validation error, early
// return, interrupt, event.Err, and malformed variant paths.
//
// Event order (fail-closed, no payload bypass):
//  1. event.Err — close any attached reader; primary event error is authoritative
//     (variant shape is not reclassified).
//  2. Uniform TypedMessageVariant validation/ownership for any present
//     MessageOutput (same rules for interrupt-carrying and non-interrupt events).
//  3. Only after variant validation/process succeeds may interrupt resume
//     targets be accumulated. Malformed flag/payload shapes never yield
//     Interrupted=true with Err=nil.
//
// Interrupt-only events (no MessageOutput / no message payload) remain valid.
// A legitimate interrupt that also carries a structurally valid complete
// Message or stream is validated and processed consistently, then interrupt
// targets are collected.
//
// Interrupts: resume targets accumulate across the entire typed iterator in
// encounter order. Duplicate context IDs or root IDs across events fail closed
// as ErrMalformedInterrupt (ResumeParams.Targets is a map).
//
// Hard-terminal exclusivity: every hard-error exit (nil event, event.Err,
// malformed variant/message/stream/interrupt, stream recv error, etc.) clears
// recoverable interrupt state so a hard error is never co-reported with a
// resumable Interrupted result. Clean iterator end preserves a valid
// interrupted result accumulated earlier.
func (e *AgenticEngine) consumeTypedIterator(
	ctx context.Context,
	cpID string,
	iter *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.AgenticMessage]],
) (*AgenticRunResult, error) {
	result := &AgenticRunResult{CheckpointID: cpID}
	if iter == nil {
		result.Err = ErrNilTypedEventIterator
		return result, result.Err
	}

	var textParts []string
	// Iterator-global interrupt accumulation (encounter order).
	var accIDs, accRoots []string
	seenIDs := make(map[string]struct{})
	seenRoots := make(map[string]struct{})

	// hardTerminal centralizes hard-error exit: clear recoverable interrupt
	// state/IDs, set Err, finalize text so far. Hard error is exclusive of a
	// resumable Interrupted result.
	hardTerminal := func(err error) (*AgenticRunResult, error) {
		clearHardTerminalInterruptState(result)
		result.Err = err
		result.FinalAssistantText = strings.Join(textParts, "")
		return result, result.Err
	}

	for {
		event, ok := iter.Next()
		if !ok {
			// Clean iterator end: keep valid interrupted result if any.
			break
		}
		if event == nil {
			// Nil typed events fail closed.
			return hardTerminal(ErrNilTypedEvent)
		}

		if event.Err != nil {
			// Close any attached stream (flag-independent) before surfacing the
			// event error. Primary event error is preserved via mapEngineError;
			// do not reclassify as ErrMalformedMessageVariant.
			closeTypedEventMessageStream(event)
			return hardTerminal(mapEngineError(event.Err))
		}

		// Uniform variant validation/ownership BEFORE interrupt accumulation.
		// Applies to interrupt-carrying events too so malformed payloads cannot
		// return Interrupted=true, Err=nil. Interrupt-only (no MessageOutput)
		// skips this block and remains valid.
		if event.Output != nil && event.Output.MessageOutput != nil {
			if err := processTypedMessageVariant(event, &textParts); err != nil {
				return hardTerminal(err)
			}
		}

		// Interrupt targets only after variant validation/process succeeded.
		if event.Action != nil && event.Action.Interrupted != nil {
			// Fail closed: an interrupt event must expose at least one valid
			// resume identity. Nil contexts, nil entries, empty/whitespace IDs,
			// and duplicate IDs never yield Interrupted=true without usable targets.
			ids, roots, ierr := collectInterruptResumeTargets(event.Action.Interrupted)
			if ierr != nil {
				return hardTerminal(ierr)
			}
			// Merge into iterator-global sets; cross-event duplicates fail closed.
			for _, id := range ids {
				if _, dup := seenIDs[id]; dup {
					return hardTerminal(fmt.Errorf("%w: duplicate interrupt context ID %q across events", ErrMalformedInterrupt, id))
				}
				seenIDs[id] = struct{}{}
				accIDs = append(accIDs, id)
			}
			for _, rid := range roots {
				if _, dup := seenRoots[rid]; dup {
					return hardTerminal(fmt.Errorf("%w: duplicate root-cause interrupt ID %q across events", ErrMalformedInterrupt, rid))
				}
				seenRoots[rid] = struct{}{}
				accRoots = append(accRoots, rid)
			}
			result.Interrupted = true
			result.InterruptContextIDs = accIDs
			result.RootCauseInterruptIDs = accRoots
			// Continue draining remaining events; keep all exact IDs so far.
			continue
		}
	}

	result.FinalAssistantText = strings.Join(textParts, "")
	return result, nil
}

// clearHardTerminalInterruptState clears recoverable interrupt state so a hard
// error cannot be co-reported with Interrupted=true / resume IDs. Call on every
// hard-error exit from consumeTypedIterator; do not call on clean iterator end.
func clearHardTerminalInterruptState(result *AgenticRunResult) {
	if result == nil {
		return
	}
	result.Interrupted = false
	result.InterruptContextIDs = nil
	result.RootCauseInterruptIDs = nil
}

// processTypedMessageVariant applies one uniform ownership + validation path for
// TypedMessageVariant on every event that carries MessageOutput (including
// interrupt-carrying events). Callers must invoke this before accumulating
// interrupt targets so malformed payloads cannot fail-open as soft interrupts.
//
// Rejects (Close+detach exactly once when a stream is present):
//   - IsStreaming=false with non-nil MessageStream
//   - simultaneous Message + MessageStream
//   - IsStreaming=true with nil MessageStream
//   - IsStreaming=true with non-nil Message (and nil stream)
//   - malformed/nil stream chunks
//   - invalid complete messages for any role/union
//
// Interrupt-only events omit MessageOutput (or omit Output). A present
// MessageOutput is validated uniformly. An interrupt event may carry an empty
// non-streaming shell (no Message, no stream) as "no message payload"; pure
// non-interrupt empty MessageOutput still fails closed as ErrNilMessage.
func processTypedMessageVariant(
	event *adk.TypedAgentEvent[*schema.AgenticMessage],
	textParts *[]string,
) error {
	if event == nil || event.Output == nil || event.Output.MessageOutput == nil {
		return nil
	}
	mv := event.Output.MessageOutput

	// Engine owns every non-nil MessageStream regardless of IsStreaming.
	// Malformed / inconsistent variants: Close+detach exactly once, fail closed.
	if mv.MessageStream != nil {
		if !mv.IsStreaming || mv.Message != nil {
			closeTypedEventMessageStream(event)
			return fmt.Errorf("%w: IsStreaming=%v Message=%v MessageStream set",
				ErrMalformedMessageVariant, mv.IsStreaming, mv.Message != nil)
		}
		// Normal streaming path: IsStreaming=true, Message==nil, stream set.
		// Drain+validate (not close-without-drain) so interrupt-attached streams
		// cannot bypass chunk validation. Zero chunks fail closed via
		// agenticmsg.ConcatStream → ErrEmptyConcat (never success with empty
		// stream payload), for ordinary events and interrupt-attached streams.
		chunks, streamErr := drainAndCloseAgenticMessageStream(mv.MessageStream)
		// Detach so a later closeTypedEventMessageStream cannot double-Close.
		mv.MessageStream = nil
		if streamErr != nil {
			return streamErr
		}
		// Strict: empty drained stream is ErrEmptyConcat (errors.Is), same as
		// agenticmsg.ConcatStream. Do not return success/nil text/interrupt IDs
		// from a zero-chunk stream (stream already Closed exactly once above).
		concatenated, err := agenticmsg.ConcatStream(chunks)
		if err != nil {
			return err
		}
		// Any Validate failure already failed closed in ConcatStream / chunks.
		// Extract final assistant text; function/search-only turns may return
		// ErrNoAssistantText which is the only allowed skip for final text.
		return appendAgenticFinalText(textParts, concatenated)
	}

	// No MessageStream attached.
	if mv.IsStreaming {
		// IsStreaming claimed but stream is nil — fail closed. Also covers
		// streaming flag with a simultaneous non-nil Message (and nil stream).
		return ErrNilMessageStream
	}

	// Non-streaming message path.
	msg := mv.Message
	if msg == nil {
		// Try GetMessage if available via helper path.
		got, err := mv.GetMessage()
		if err != nil {
			return err
		}
		msg = got
	}
	if msg == nil {
		// Interrupt + empty shell = no message payload (still valid interrupt).
		// Non-interrupt empty MessageOutput fails closed.
		if event.Action != nil && event.Action.Interrupted != nil {
			return nil
		}
		return agenticmsg.ErrNilMessage
	}
	// Any Validate failure for any role fails closed — never silently skip
	// malformed user/system/tool-result messages.
	if err := agenticmsg.Validate(msg); err != nil {
		return err
	}
	// Valid non-assistant intermediate messages may be ignored for final
	// text only after validation succeeds.
	return appendAgenticFinalText(textParts, msg)
}

// drainAndCloseAgenticMessageStream fully drains sr with strict chunk validation
// and always Closes the reader exactly once — on EOF, Recv error, nil chunk, or
// validation error. StreamReader.Close has no error return; the primary drain
// error is preserved.
func drainAndCloseAgenticMessageStream(sr *schema.StreamReader[*schema.AgenticMessage]) (chunks []*schema.AgenticMessage, err error) {
	if sr == nil {
		return nil, ErrNilMessageStream
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
			return chunks, rerr
		}
		if chunk == nil {
			return chunks, agenticmsg.ErrNilChunk
		}
		if verr := agenticmsg.ValidateStreamChunk(chunk); verr != nil {
			return chunks, verr
		}
		chunks = append(chunks, chunk)
	}
}

// closeTypedEventMessageStream Closes any non-nil MessageStream on an event
// that will not be drained by drainAndCloseAgenticMessageStream (error /
// interrupt / malformed-variant paths). Ownership is flag-independent: a stream
// present with IsStreaming=false is still Closed exactly once. Safe no-op when
// absent or already detached (nil).
func closeTypedEventMessageStream(event *adk.TypedAgentEvent[*schema.AgenticMessage]) {
	if event == nil || event.Output == nil || event.Output.MessageOutput == nil {
		return
	}
	mv := event.Output.MessageOutput
	if mv.MessageStream == nil {
		return
	}
	mv.MessageStream.Close()
	mv.MessageStream = nil
}

// collectInterruptResumeTargets validates InterruptInfo and returns exact
// resume IDs (and root-cause IDs) in encounter order. Any unusable identity
// fails closed with ErrMalformedInterrupt — never partial success.
//
// Duplicate IDs in the same event/result are rejected as malformed: ResumeParams.Targets
// is a map, so duplicates would silently collapse and drop resume data.
func collectInterruptResumeTargets(info *adk.InterruptInfo) (ids, roots []string, err error) {
	if info == nil {
		return nil, nil, fmt.Errorf("%w: nil interrupt payload", ErrMalformedInterrupt)
	}
	ics := info.InterruptContexts
	if len(ics) == 0 {
		return nil, nil, fmt.Errorf("%w: empty interrupt contexts", ErrMalformedInterrupt)
	}
	ids = make([]string, 0, len(ics))
	roots = make([]string, 0, len(ics))
	seen := make(map[string]struct{}, len(ics))
	for i, ic := range ics {
		if ic == nil {
			return nil, nil, fmt.Errorf("%w: InterruptContexts[%d] is nil", ErrMalformedInterrupt, i)
		}
		// Exact ID required for ResumeWithParams Targets; empty or whitespace-only
		// cannot be a resume key. Preserve the exact ID string for valid events.
		if ic.ID == "" {
			return nil, nil, fmt.Errorf("%w: InterruptContexts[%d] empty ID", ErrMalformedInterrupt, i)
		}
		if strings.TrimSpace(ic.ID) == "" {
			return nil, nil, fmt.Errorf("%w: InterruptContexts[%d] whitespace-only ID %q", ErrMalformedInterrupt, i, ic.ID)
		}
		if strings.TrimSpace(ic.ID) != ic.ID {
			return nil, nil, fmt.Errorf("%w: InterruptContexts[%d] non-canonical ID %q", ErrMalformedInterrupt, i, ic.ID)
		}
		if _, dup := seen[ic.ID]; dup {
			return nil, nil, fmt.Errorf("%w: InterruptContexts[%d] duplicate ID %q", ErrMalformedInterrupt, i, ic.ID)
		}
		seen[ic.ID] = struct{}{}
		ids = append(ids, ic.ID)
		if ic.IsRootCause {
			roots = append(roots, ic.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("%w: no valid resume targets", ErrMalformedInterrupt)
	}
	return ids, roots, nil
}

// appendAgenticFinalText extracts assistant text into parts. Valid non-assistant
// messages are ignored for final text. Assistant messages without text that are
// valid function/search turns (ErrNoAssistantText) are ignored. Any other
// extraction failure fails closed.
func appendAgenticFinalText(parts *[]string, msg *schema.AgenticMessage) error {
	if msg == nil {
		return agenticmsg.ErrNilMessage
	}
	if msg.Role != schema.AgenticRoleTypeAssistant {
		// Valid non-assistant intermediate (e.g. tool-result) — ignore for final text.
		return nil
	}
	text, err := agenticmsg.ExtractAssistantText(msg)
	if err == nil {
		if text != "" {
			*parts = append(*parts, text)
		}
		return nil
	}
	// Explicit valid case: assistant function/search turn with no public text.
	if errors.Is(err, agenticmsg.ErrNoAssistantText) {
		return nil
	}
	// Any other extraction failure (wrong validation residual, unsupported
	// blocks, stream-only fields, etc.) fails closed.
	return err
}
