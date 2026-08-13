package chatruntimebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/sessioncontext"
	"actweave/backend/internal/tooltranslator"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// ErrWaitingConfirmation signals the run paused for human confirmation
// (same sentinel semantics as chatruntime.ErrWaitingConfirmation).
var ErrWaitingConfirmation = chatruntime.ErrWaitingConfirmation

// ErrRunCancelled mirrors chatruntime cancel cause.
var ErrRunCancelled = chatruntime.ErrRunCancelled

var (
	errInterruptIDMissing = errors.New("eino interrupt id missing (EINO_INTERRUPT_ID_MISSING)")
	errBridgeInvalid      = errors.New("chatruntimebridge is invalid")
)

// ContinueTimeout is the hard deadline for one async continue drive.
const ContinueTimeout = chatruntime.ContinueTimeout

// CheckpointTTL renews expires_at to match confirmation expiry (D15).
type CheckpointTTL interface {
	TouchExpiresAt(ctx context.Context, checkPointID string, expiresAt time.Time) error
}

// Dependencies wires the bridge to platform services.
type Dependencies struct {
	Sessions chatruntime.SessionReader
	Results  chatruntime.AssistantRecorder
	Content  chatruntime.ContentReader
	Agents   chatruntime.AgentReader
	Models   chatruntime.ModelReader
	Runs     chatruntime.RunReader
	Events   chatruntime.EventRecorder
	Steps    chatruntime.StepStore
	// ModelTurns stores permanent MODEL step evidence (agent audit timeline).
	// Optional: when nil, model turns are not persisted (tests / degraded).
	ModelTurns    chatruntime.ModelTurnRecorder
	ToolInvoker   chatruntime.ToolInvoker
	Confirmations chatruntime.ConfirmationPreparer
	// AgenticEngine drives Typed Agentic Chat (initial + confirmation resume).
	AgenticEngine *einoruntime.AgenticEngine
	// CheckpointTTL renews expires_at after confirmation prepare (D15).
	CheckpointTTL CheckpointTTL
	// BuildAgenticModel constructs the production AgenticModel.
	// Required for Enqueue/Execute and Agentic resume. No classic fallback.
	BuildAgenticModel AgenticModelBuilder
	// TextSinkFactory builds ProtocolMessageTextSink (or test recording sink) for
	// true Stream → item.delta projection (D14 / A.5). When set, drive always
	// wires StreamDeltaRecorder.Sink so deltas leave the in-memory buffer.
	TextSinkFactory TextSinkFactory
	// MaxIterations overrides the model-round budget (0 / negative → default).
	// Agentic initial path always uses einoruntime.DefaultMaxIterations (8).
	MaxIterations int
	// MaxToolInvocations hard-caps tool InvokableRun calls per run.
	// Contract (aligned with einoruntime / config; no silent clamp):
	//   - 0 → einoruntime.DefaultMaxToolInvocations (16)
	//   - 1..16 accepted as-is
	//   - negative or >16 → NewBridge error
	MaxToolInvocations int
	Logger             *slog.Logger
	Now                func() time.Time
	// AgentAuditDebug enables richer MODEL_TURN payloads (reasoning bodies).
	// Loaded once from process config; default false. Required for audit UI
	// to show LLM thinking (output_summary.reasoning).
	AgentAuditDebug bool
	// Assemblies persists immutable context assembly manifests (ZKL-74).
	// Required for every successful Agentic initial run (Task 4A); nil fails closed.
	// *execution.ContextAssemblyRepository implements ContextAssemblyInserter.
	Assemblies ContextAssemblyInserter
	// Compact wires LLM rolling compact for session-context.v2 (ZKL-81).
	// Optional in tests; when nil/incomplete, triggered compact falls back to token_window
	// after best-effort evidence persistence (or hard-fails on evidence errors).
	Compact *CompactDependencies
	// Multimodal assembles aap.message-content.v1 (+ optional READY input_file)
	// into model schema messages (IC-08). When nil, text-only legacy/v1 text works;
	// input_file parts fail closed with MODEL_CONTENT_UNSUPPORTED.
	Multimodal *chatruntime.MultimodalAssembler
	// Delegation wires internal Agent→Agent (Eino AgentTool) and optional A2A remotes.
	// When nil, existing single-agent tool behavior is unchanged.
	Delegation *DelegationDeps
}

// Bridge implements agentrun.Runtime on the eino engine path.
type Bridge struct {
	sessions          chatruntime.SessionReader
	results           chatruntime.AssistantRecorder
	content           chatruntime.ContentReader
	agents            chatruntime.AgentReader
	models            chatruntime.ModelReader
	runs              chatruntime.RunReader
	events            chatruntime.EventRecorder
	steps             chatruntime.StepStore
	modelTurns        chatruntime.ModelTurnRecorder
	toolInvoker       chatruntime.ToolInvoker
	confirmations     chatruntime.ConfirmationPreparer
	agenticEngine     *einoruntime.AgenticEngine
	checkpointTTL     CheckpointTTL
	buildAgenticModel AgenticModelBuilder
	textSinkFactory   TextSinkFactory
	maxIterations     int
	maxTools          int
	logger            *slog.Logger
	now               func() time.Time
	agentAuditDebug   bool
	assemblies        ContextAssemblyInserter
	compact           *CompactDependencies
	multimodal        *chatruntime.MultimodalAssembler
	delegation        *DelegationDeps

	activeMu   sync.Mutex
	activeRuns map[string]*activeRunExecution

	// pendingConfirms captures tool confirm metadata from PipelineTool hooks.
	pendingMu       sync.Mutex
	pendingConfirms map[string][]einoruntime.PendingConfirmInterrupt
}

type activeRunExecution struct {
	cancel context.CancelCauseFunc
	// gen increments on re-register for the same slot so a stale unregister
	// (generation-aware) cannot wipe a newer reclaim registration.
	gen int64
}

// Compile-time: Bridge is a production Runtime.
var _ agentrun.Runtime = (*Bridge)(nil)

// NewBridge constructs a chatruntimebridge Runtime.
func NewBridge(deps Dependencies) (*Bridge, error) {
	if deps.Sessions == nil || deps.Results == nil || deps.Agents == nil ||
		deps.Models == nil || deps.Runs == nil || deps.Events == nil {
		return nil, errors.New("chatruntimebridge: core dependencies are required")
	}
	if deps.AgenticEngine == nil {
		return nil, errors.New("chatruntimebridge: AgenticEngine is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	maxIter := deps.MaxIterations
	if maxIter <= 0 {
		maxIter = einoruntime.DefaultMaxIterations
	}
	// Fail closed for invalid tool budget at the bridge boundary (same contract
	// as einoruntime.normalizeMaxToolInvocations / config.validateEinoMaxToolInvocations).
	maxTools, err := normalizeBridgeMaxToolInvocations(deps.MaxToolInvocations)
	if err != nil {
		return nil, err
	}
	return &Bridge{
		sessions: deps.Sessions, results: deps.Results, content: deps.Content,
		agents: deps.Agents, models: deps.Models, runs: deps.Runs,
		events: deps.Events, steps: deps.Steps, modelTurns: deps.ModelTurns,
		toolInvoker:       deps.ToolInvoker,
		confirmations:     deps.Confirmations,
		agenticEngine:     deps.AgenticEngine,
		checkpointTTL:     deps.CheckpointTTL,
		buildAgenticModel: deps.BuildAgenticModel,
		textSinkFactory:   deps.TextSinkFactory,
		maxIterations:     maxIter, maxTools: maxTools,
		logger: logger, now: now, agentAuditDebug: deps.AgentAuditDebug,
		assemblies:      deps.Assemblies,
		compact:         deps.Compact,
		multimodal:      deps.Multimodal,
		delegation:      deps.Delegation,
		activeRuns:      make(map[string]*activeRunExecution),
		pendingConfirms: make(map[string][]einoruntime.PendingConfirmInterrupt),
	}, nil
}

// normalizeBridgeMaxToolInvocations enforces the production-wide tool budget
// contract at the bridge construction boundary:
//   - 0 → einoruntime.DefaultMaxToolInvocations (16)
//   - 1..16 accepted as-is
//   - negative or >16 → error (never silently defaulted or clamped)
func normalizeBridgeMaxToolInvocations(max int) (int, error) {
	if max == 0 {
		return einoruntime.DefaultMaxToolInvocations, nil
	}
	if max < 0 || max > einoruntime.DefaultMaxToolInvocations {
		return 0, fmt.Errorf(
			"chatruntimebridge: MaxToolInvocations must be 0 (default %d) or 1..%d, got %d",
			einoruntime.DefaultMaxToolInvocations, einoruntime.DefaultMaxToolInvocations, max,
		)
	}
	return max, nil
}

// Enqueue starts asynchronous execution for a CHAT AgentRun.
func (b *Bridge) Enqueue(job agentrun.Job) {
	job = normalizeJob(job)
	if !jobReady(job) {
		return
	}
	timeoutContext, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	runContext, runCancel := context.WithCancelCause(timeoutContext)
	active, registered := b.registerActiveRun(job.WorkspaceID, job.RunID, "initial", runCancel)
	if !registered {
		runCancel(context.Canceled)
		timeoutCancel()
		return
	}
	go func() {
		defer timeoutCancel()
		defer runCancel(context.Canceled)
		defer b.unregisterActiveRun(job.WorkspaceID, job.RunID, "initial", active)
		// Durable fail path must not use the cancelled run context (timeout/cancel
		// otherwise leaves agent_runs stuck in RUNNING forever).
		failCtx := context.WithoutCancel(runContext)
		if err := b.Execute(runContext, job); err != nil {
			if errors.Is(err, ErrWaitingConfirmation) {
				b.logger.Info("eino chat run waiting confirmation",
					"event", "chatruntimebridge.run.waiting_confirmation",
					"workspace_id", job.WorkspaceID, "run_id", job.RunID)
				return
			}
			if errors.Is(context.Cause(runContext), ErrRunCancelled) {
				// Cancel path: durable CAS is owned by the cancel API; do not force FAILED.
				return
			}
			b.logger.Error("eino chat run execution failed",
				"event", "chatruntimebridge.run.failed",
				"workspace_id", job.WorkspaceID, "run_id", job.RunID, "error", err.Error())
			// Execute's failRun may have raced a cancelled ctx; ensure terminal status.
			b.ensureRunNotLeftRunning(failCtx, job, err)
			return
		}
		// Success path should have completed; belt-and-suspenders for stuck RUNNING.
		b.ensureRunNotLeftRunning(failCtx, job, errors.New("eino chat run finished without terminal status"))
	}()
}

// CancelRun interrupts an active in-process bridge job.
func (b *Bridge) CancelRun(workspaceID, runID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	runID = strings.TrimSpace(runID)
	if b == nil || workspaceID == "" || runID == "" {
		return errBridgeInvalid
	}
	runKey := activeRunKey(workspaceID, runID)
	b.activeMu.Lock()
	active := []*activeRunExecution{
		b.activeRuns[activeRunSlotKey(runKey, "initial")],
		b.activeRuns[activeRunSlotKey(runKey, "continue")],
	}
	b.activeMu.Unlock()
	for _, execution := range active {
		if execution != nil {
			execution.cancel(ErrRunCancelled)
		}
	}
	return nil
}

// EnqueueContinueWithLifecycle schedules ResumeWithParams after Dispatch Invoke.
func (b *Bridge) EnqueueContinueWithLifecycle(
	job agentrun.Job,
	requestSnapshot, toolResult json.RawMessage,
	lifecycle agentrun.ContinueLifecycle,
) {
	job = normalizeJob(job)
	if !jobReady(job) {
		return
	}
	timeoutContext, timeoutCancel := context.WithTimeout(context.Background(), ContinueTimeout)
	runContext, runCancel := context.WithCancelCause(timeoutContext)
	active, registered := b.registerActiveRun(job.WorkspaceID, job.RunID, "continue", runCancel)
	if !registered {
		runCancel(context.Canceled)
		timeoutCancel()
		if lifecycle != nil {
			_ = lifecycle.Complete(context.Background())
		}
		return
	}
	go func() {
		defer timeoutCancel()
		defer runCancel(context.Canceled)
		defer b.unregisterActiveRun(job.WorkspaceID, job.RunID, "continue", active)
		if lifecycle != nil {
			defer func() { _ = lifecycle.Complete(context.Background()) }()
			stopRenew := startContinueLeaseRenew(runContext, lifecycle)
			defer stopRenew()
		}
		if err := b.ContinueAfterConfirmation(runContext, job, requestSnapshot, toolResult); err != nil {
			if errors.Is(err, ErrWaitingConfirmation) {
				return
			}
			if errors.Is(context.Cause(runContext), ErrRunCancelled) {
				return
			}
			b.logger.Error("eino chat run continue failed",
				"event", "chatruntimebridge.run.continue_failed",
				"workspace_id", job.WorkspaceID, "run_id", job.RunID, "error", err.Error())
		}
	}()
}

// Execute runs one CHAT AgentRun to terminal success, failure, or confirmation pause.
func (b *Bridge) Execute(ctx context.Context, job agentrun.Job) error {
	run, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return fmt.Errorf("load agent run: %w", err)
	}
	if run.Status != "RUNNING" {
		return fmt.Errorf("chat run %s is not executable (status=%s)", job.RunID, run.Status)
	}
	if !job.InitialEventsCommitted {
		if err := b.recordProtocol(ctx, chatruntime.ProtocolRecord{
			Kind: chatruntime.ProtocolRecordRunStarted, Job: job, Run: run,
			OccurredAt: b.now().UTC(),
		}); err != nil {
			return err
		}
	}

	// The initial chat turn is Agentic, and it says so here rather than being
	// dispatched from inside the classic seam. Passing through a function whose
	// first lines choose the runtime generation is what let live agent and model
	// reads sit textually above the Agentic path: anything added to the top of
	// the classic seam silently ran on the frozen path too.
	content, streamMessageID, runErr := b.driveAgenticInitial(ctx, job, run)
	// Persist terminal status even when the turn timed out. User cancel is
	// different: durable CANCELLED is owned by the cancel API, so a
	// WithoutCancel failRun must not race it into FAILED.
	persistCtx := context.WithoutCancel(ctx)
	if runErr != nil {
		if errors.Is(runErr, ErrWaitingConfirmation) {
			return runErr
		}
		if userCancelledRun(ctx, runErr) {
			return runErr
		}
		return b.failRun(persistCtx, job, run, runErr)
	}
	return b.completeRun(persistCtx, job, run, content, streamMessageID, false)
}

// ContinueAfterConfirmation resumes via ResumeWithParams (design §3.6.3).
//
// toolResult is the Dispatch ResultSnapshot — already the product of the sole
// successful InvokeResolved. PipelineTool must not invoke again.
func (b *Bridge) ContinueAfterConfirmation(
	ctx context.Context,
	job agentrun.Job,
	requestSnapshot, toolResult json.RawMessage,
) error {
	meta, ok := ExtractEinoChatResume(requestSnapshot)
	if !ok {
		return errors.New("einoChatResume missing from confirmation checkpoint")
	}
	// The CheckPointStore is shared; RuntimeGeneration is the only discriminator.
	// Classic (and unmarked→classic) checkpoints are fail-closed after Task 9.
	generation := meta.EffectiveRuntimeGeneration()
	switch generation {
	case RuntimeGenerationAgentic:
	case RuntimeGenerationClassic:
		return fmt.Errorf("%w: drain classic confirmations before cutover", ErrClassicResumeRemoved)
	default:
		return fmt.Errorf("%w: %q", ErrAgenticResumeGenerationMismatch, generation)
	}

	run, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}
	if run.Status != "RUNNING" {
		return fmt.Errorf("chat run %s is not resumable (status=%s)", job.RunID, run.Status)
	}

	targets, err := buildResumeTargets(meta, toolResult)
	if err != nil {
		return err
	}

	content, streamMessageID, runErr := b.driveAgenticResume(
		ctx, job, run, meta.EinoCheckpointID, targets)
	persistCtx := context.WithoutCancel(ctx)
	if runErr != nil {
		if errors.Is(runErr, ErrWaitingConfirmation) {
			return runErr
		}
		if userCancelledRun(ctx, runErr) {
			return runErr
		}
		return b.failRun(persistCtx, job, run, runErr)
	}
	return b.completeRun(persistCtx, job, run, content, streamMessageID, true)
}

func (b *Bridge) buildPipelineTools(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	pendingKey string,
) ([]tool.BaseTool, error) {
	capabilities, err := chatruntime.ParseCapabilitySnapshot(run.CapabilitySnapshot)
	if err != nil {
		return nil, err
	}
	return b.buildPipelineToolsFrom(ctx, job, run, pendingKey, capabilities)
}

// buildPipelineToolsFrom materializes PipelineTools from an already-parsed
// capability list. The Agentic initial path passes the strict-parsed freeze
// (parseRunCapabilitySnapshotStrict) so no lax normalization or silent drop can
// change the executable tool set behind the frozen catalog digest.
func (b *Bridge) buildPipelineToolsFrom(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	pendingKey string,
	capabilities []chatruntime.SnapshotCapability,
) ([]tool.BaseTool, error) {
	if len(capabilities) == 0 {
		return nil, nil
	}
	if b.toolInvoker == nil {
		return nil, errors.New("tool invoker is not configured")
	}

	out := make([]tool.BaseTool, 0, len(capabilities))
	for _, cap := range capabilities {
		// P3.4: TOOL and bound published WORKFLOW capabilities become InvokableTools.
		// Other kinds (future) are skipped. ResolveInvocation validates WORKFLOW
		// has an active/pinned published revision; Invoke routes WORKFLOW via
		// PublishedRevisionRunner (chatToolInvoker), not HTTP tool_invocations.
		kind := strings.ToUpper(strings.TrimSpace(cap.Kind))
		if kind == "" {
			kind = "TOOL"
		}
		if kind != "TOOL" && kind != "WORKFLOW" {
			continue
		}
		info, err := tooltranslator.ToToolInfo(tooltranslator.NewCapability(
			cap.CallableName, cap.CallableDescription, cap.InputSchema,
		))
		if err != nil {
			return nil, fmt.Errorf("translate tool %q: %w", cap.CallableName, err)
		}
		// ZKL-56 UX-02: do NOT ResolveInvocation at build time. Pure-text runs
		// must not fail because an unrelated bound Tool has a bad Connection.
		// Resolution happens on first actual PipelineTool call (lazy).
		// Capture for closure.
		pk := pendingKey
		// SERVICE_PRINCIPAL (AAP) requires PrincipalSnapshot on InvokeRequest;
		// USER Console chat may omit it (pipeline synthesizes internal snapshot).
		// Resume/confirm path already passes run.PrincipalSnapshot (see pause.go).
		principalSnap := run.PrincipalSnapshot
		pt, err := einoruntime.NewPipelineTool(einoruntime.PipelineToolConfig{
			Info:     info,
			Pipeline: b.toolInvoker,
			Resolver: b.toolInvoker,
			// Capability-snapshot flag only; OR with resolved.RequiresConfirmation
			// inside InvokableRun after the single lazy resolve.
			RequiresConfirmation: cap.RequiresConfirmation,
			WorkspaceID:          job.WorkspaceID,
			CapabilityID:         cap.CapabilityID,
			ReleaseID:            cap.ReleaseID,
			ActorType:            run.TriggeredByType,
			ActorID:              firstNonEmpty(job.ActorID, run.TriggeredByID),
			TraceID:              run.TraceID,
			AgentRunID:           job.RunID,
			BindingConnectionID:  cap.ConnectionID,
			PrincipalSnapshot:    &principalSnap,
			AuthorizationSnapshot: json.RawMessage(
				`{"source":"chatruntimebridge","parent":"agent_run"}`,
			),
			OnConfirmInterrupt: func(_ context.Context, pending einoruntime.PendingConfirmInterrupt) {
				b.recordPending(pk, pending)
			},
			// Persist TOOL agent_run_steps so Agent 审计中心 shows tool args/results
			// (timeline only reads agent_run_steps, not tool_invocations alone).
			OnToolComplete: func(hookCtx context.Context, event einoruntime.ToolCompleteEvent) {
				if err := b.recordToolStep(hookCtx, event); err != nil {
					b.logger.Error("record TOOL agent_run_step failed",
						"event", "chatruntimebridge.tool_step.record_failed",
						"workspace_id", event.WorkspaceID,
						"run_id", event.AgentRunID,
						"tool_name", event.ToolName,
						"error", err.Error(),
					)
				}
			},
		})
		if err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	return out, nil
}

func (b *Bridge) systemPrompt(ctx context.Context, job agentrun.Job, configuredAgent agent.Agent) string {
	const defaultSystemPrompt = "You are a helpful workspace agent. Answer clearly and concisely."
	systemPrompt := defaultSystemPrompt
	if configuredAgent.CurrentPromptRevisionID == nil {
		return systemPrompt
	}
	revisions, err := b.agents.ListPromptRevisions(ctx, job.WorkspaceID, configuredAgent.ID)
	if err != nil {
		return systemPrompt
	}
	for _, revision := range revisions {
		if revision.ID == *configuredAgent.CurrentPromptRevisionID &&
			strings.TrimSpace(revision.SystemPrompt) != "" {
			return strings.TrimSpace(revision.SystemPrompt)
		}
	}
	return systemPrompt
}

// buildInitialMessages chooses legacy full-history or session-context.v1 assembly.
// Resume path must never call this.
func (b *Bridge) buildInitialMessages(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	configuredAgent agent.Agent,
	instruction string,
	toolSchemas []contextwindow.ToolSchema,
) ([]*schema.Message, error) {
	if sessioncontext.IsLegacySnapshot(run.ContextPolicySnapshot) ||
		run.SnapshotSchemaVersion == "" ||
		run.SnapshotSchemaVersion == execution.RunSnapshotSchemaV1 {
		return b.buildMessages(ctx, job, configuredAgent, instruction)
	}
	// Explicit unknown run schema → safe fail (do not fall back to full history).
	if run.SnapshotSchemaVersion != execution.RunSnapshotSchemaV2 {
		return nil, execution.NewContextError(execution.ErrCodeContextSnapshotUnsupported)
	}
	resolved, err := sessioncontext.ParseResolvedSnapshot(run.ContextPolicySnapshot)
	if err != nil {
		if errors.Is(err, sessioncontext.ErrUnsupportedSnapshot) {
			return nil, execution.NewContextError(execution.ErrCodeContextSnapshotUnsupported)
		}
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if resolved.Mode == sessioncontext.ModeLegacy {
		return b.buildMessages(ctx, job, configuredAgent, instruction)
	}
	if resolved.ModelContextWindowTokens <= 0 || resolved.TokenizerProfile == "" {
		return nil, execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	return b.buildMessagesTokenWindow(ctx, job, run, instruction, resolved, toolSchemas)
}

func (b *Bridge) buildMessages(
	ctx context.Context,
	job agentrun.Job,
	configuredAgent agent.Agent,
	instruction string,
) ([]*schema.Message, error) {
	_ = configuredAgent
	history, err := b.sessions.ListMessages(ctx, job.WorkspaceID, job.SessionID)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	messages := make([]*schema.Message, 0, len(history)+1)
	if strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	for _, msg := range history {
		role := modelRole(msg.Role)
		if role == "" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" && msg.ContentObjectID != "" && b.content != nil {
			loaded, readErr := b.content.ReadPermanentChat(
				ctx, job.WorkspaceID, msg.ContentObjectID, job.ActorID,
			)
			if readErr != nil {
				return nil, fmt.Errorf("read permanent chat content: %w", readErr)
			}
			content = strings.TrimSpace(loaded)
		}
		if content == "" {
			continue
		}
		switch role {
		case "user":
			userMsg, userErr := b.assembleUserSchemaMessage(ctx, job.WorkspaceID, configuredAgent.ID, content)
			if userErr != nil {
				return nil, userErr
			}
			messages = append(messages, userMsg)
		case "assistant":
			// KD-10: model history is text-only; omit a2ui surface / raw envelope.
			content = assistantModelHistoryText(content)
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			messages = append(messages, schema.AssistantMessage(content, nil))
		case "system":
			// Instruction already injected; skip history system rows.
		case "tool":
			// Tool turns live in gob checkpoint on resume path.
		}
	}
	if len(messages) < 2 {
		return nil, errors.New("chat history has no user content for model turn")
	}
	return messages, nil
}

func (b *Bridge) buildMessagesTokenWindow(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	instruction string,
	policy sessioncontext.ResolvedSnapshot,
	toolSchemas []contextwindow.ToolSchema,
) ([]*schema.Message, error) {
	// Prefer instruction from agent_snapshot prompt revision when bound (run.v2).
	system := instruction
	if revID := agentSnapshotPromptRevisionID(run.AgentSnapshot); revID != "" && b.agents != nil {
		if prompt, ok := loadPromptRevision(ctx, b.agents, job.WorkspaceID, run.AgentID, revID); ok {
			system = prompt
		}
	}

	// Bounded reverse history: page newest→older, decrypt only candidates still
	// needed for assembly; stop once a full AssembleTokenWindow omits further turns.
	current, priorTurns, err := b.loadBoundedHistoryForAssembly(ctx, job, system, policy, toolSchemas)
	if err != nil {
		if errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
			return nil, execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	// IC-10: v2 compaction gate path — pure preflight; safe token-window fallback when
	// coordinator not fully wired. Resume never reaches here (targets != nil).
	compact := b.maybeCompactForInitialRun(ctx, agentrunJob{
		WorkspaceID: job.WorkspaceID, SessionID: job.SessionID, RunID: job.RunID,
		UserMessageID: job.UserMessageID, ActorID: job.ActorID,
	}, run, policy, system, toolSchemas, current, priorTurns)
	if compact.HardFail != nil {
		return nil, compact.HardFail
	}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		PolicyMode:               policy.Mode,
		ModelContextWindowTokens: policy.ModelContextWindowTokens,
		OutputReserveTokens:      policy.OutputReserveTokens,
		SafetyMarginTokens:       policy.SafetyMarginTokens,
		MaxInputTokens:           policy.EffectiveMaxInputTokens,
		MaxRecentTurns:           policy.MaxRecentTurns,
		TokenizerProfile:         policy.TokenizerProfile,
		SystemPrompt:             system,
		Tools:                    toolSchemas,
		PriorTurns:               priorTurns,
		CurrentUser:              current,
		OptionalSummary:          compact.OptionalSummary,
	})
	if err != nil {
		if errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
			return nil, execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if b.assemblies != nil {
		segments := make([]map[string]any, 0, len(plan.IncludedMessages))
		for _, m := range plan.IncludedMessages {
			segments = append(segments, map[string]any{
				"messageId": m.ID, "role": m.Role, "contentHash": m.ContentHash,
			})
		}
		segJSON, _ := json.Marshal(segments)
		rec := execution.ContextAssemblyRecord{
			WorkspaceID: job.WorkspaceID, RunID: job.RunID, SessionID: job.SessionID,
			Mode:                   plan.Mode,
			PolicySnapshotHash:     execution.HashJSONObject(run.ContextPolicySnapshot),
			ModelSnapshotHash:      execution.HashJSONObject(run.ModelSnapshot),
			CapabilitySnapshotHash: execution.HashJSONObject(run.CapabilitySnapshot),
			AgentSnapshotHash:      execution.HashJSONObject(run.AgentSnapshot),
			EstimatorProfile:       plan.EstimatorProfile,
			EstimatorVersion:       plan.EstimatorVersion,
			HardInputCeilingTokens: plan.HardInputCeilingTokens,
			OutputReserveTokens:    plan.OutputReserveTokens,
			SafetyMarginTokens:     plan.SafetyMarginTokens,
			ToolsOverheadTokens:    plan.ToolsOverheadTokens,
			SystemPromptHash:       sha256Hex(system),
			IncludedSegments:       segJSON,
			OmittedPrefixCount:     plan.OmittedTurnCount,
			EstimatedTotalTokens:   plan.EstimatedTotalTokens,
		}
		rec.AssemblyDigest = execution.ComputeAssemblyDigest(rec)
		if _, err := b.assemblies.InsertImmutable(ctx, rec); err != nil {
			return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
		}
	}
	out := make([]*schema.Message, 0, len(plan.PromptMessages))
	for _, m := range plan.PromptMessages {
		switch m.Role {
		case contextwindow.RoleSystem:
			out = append(out, schema.SystemMessage(m.Content))
		case contextwindow.RoleUser:
			userMsg, userErr := b.assembleUserSchemaMessage(ctx, job.WorkspaceID, run.AgentID, m.Content)
			if userErr != nil {
				return nil, userErr
			}
			out = append(out, userMsg)
		case contextwindow.RoleAssistant:
			// KD-10: join text parts only; never feed raw surface JSON to the model.
			assistantText := strings.TrimSpace(assistantModelHistoryText(m.Content))
			if assistantText == "" {
				continue
			}
			out = append(out, schema.AssistantMessage(assistantText, nil))
		}
	}
	if len(out) < 2 {
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	return out, nil
}

// assembleUserSchemaMessage maps durable user content to a model schema message.
// AAP createRun stores aap.message-content.v1; input_file parts are assembled
// when Multimodal.RuntimeMultimodal is true, else fail with MODEL_CONTENT_UNSUPPORTED.
func (b *Bridge) assembleUserSchemaMessage(
	ctx context.Context,
	workspaceID, agentID, content string,
) (*schema.Message, error) {
	assembler := b.multimodal
	if assembler == nil {
		// No multimodal wiring: still decode v1 text; fail closed on input_file.
		assembler = &chatruntime.MultimodalAssembler{RuntimeMultimodal: false}
	}
	msg, err := assembler.AssembleUserMessage(ctx, workspaceID, agentID, content)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

// historyPageSize is the reverse-page resource bound for session-context assembly.
// It is not a semantic turn limit.
const historyPageSize = 50

// loadBoundedHistoryForAssembly reverse-pages message metadata (no body decrypt)
// and decrypts bodies newest→older only while AssembleTokenWindow still selects
// every loaded prior turn. Once any loaded turn is omitted, older bodies are not
// decrypted and further pages are not fetched.
func (b *Bridge) loadBoundedHistoryForAssembly(
	ctx context.Context,
	job agentrun.Job,
	system string,
	policy sessioncontext.ResolvedSnapshot,
	toolSchemas []contextwindow.ToolSchema,
) (contextwindow.HistoryMessage, []contextwindow.Turn, error) {
	currentID := strings.TrimSpace(job.UserMessageID)
	if currentID == "" {
		return contextwindow.HistoryMessage{}, nil, contextwindow.ErrCurrentUserMissing
	}
	curMsg, err := b.sessions.GetMessage(ctx, job.WorkspaceID, currentID)
	if err != nil {
		return contextwindow.HistoryMessage{}, nil, err
	}
	if curMsg.SessionID != "" && job.SessionID != "" && curMsg.SessionID != job.SessionID {
		return contextwindow.HistoryMessage{}, nil, contextwindow.ErrCurrentUserSessionMismatch
	}
	curContent, err := b.resolveMessageContent(ctx, job, curMsg)
	if err != nil {
		return contextwindow.HistoryMessage{}, nil, err
	}
	current := contextwindow.HistoryMessage{
		ID: curMsg.ID, SessionID: curMsg.SessionID, Role: curMsg.Role, Content: curContent,
		ContentHash: curMsg.ContentSHA256, RunID: curMsg.RunID, CreatedAt: curMsg.CreatedAt,
	}

	var (
		cursor *chat.MessagePageCursor
		// histNewestFirst holds decrypted history strictly before current, newest first.
		histNewestFirst []contextwindow.HistoryMessage
		priorTurns      []contextwindow.Turn
		pagesFetched    int
	)
	const maxPages = 200
	stopDecrypt := false
	for pagesFetched < maxPages && !stopDecrypt {
		page, pageErr := b.sessions.ListMessagesReversePage(
			ctx, job.WorkspaceID, job.SessionID, historyPageSize, cursor,
		)
		if pageErr != nil {
			return contextwindow.HistoryMessage{}, nil, pageErr
		}
		pagesFetched++
		if len(page.Messages) == 0 {
			break
		}
		// Page is newest-first; decrypt in that order so budget stops early.
		for _, msg := range page.Messages {
			if msg.ID == currentID {
				continue
			}
			if msg.CreatedAt.After(current.CreatedAt) ||
				(msg.CreatedAt.Equal(current.CreatedAt) && msg.ID >= current.ID) {
				continue
			}
			content, cErr := b.resolveMessageContent(ctx, job, msg)
			if cErr != nil {
				return contextwindow.HistoryMessage{}, nil, cErr
			}
			// KD-10: assistant durable multi-part must not inflate the token window
			// with surface JSON, and must not re-enter the model as raw envelope.
			if strings.EqualFold(strings.TrimSpace(msg.Role), "ASSISTANT") {
				content = assistantModelHistoryText(content)
			}
			histNewestFirst = append(histNewestFirst, contextwindow.HistoryMessage{
				ID: msg.ID, SessionID: msg.SessionID, Role: msg.Role, Content: content,
				ContentHash: msg.ContentSHA256, RunID: msg.RunID, CreatedAt: msg.CreatedAt,
			})

			// Rebuild chronological history from newest-first buffer.
			histMsgs := make([]contextwindow.HistoryMessage, len(histNewestFirst))
			for i, m := range histNewestFirst {
				histMsgs[len(histNewestFirst)-1-i] = m
			}
			allForNorm := append(append([]contextwindow.HistoryMessage{}, histMsgs...), current)
			turns, cur, normErr := contextwindow.NormalizeTurns(allForNorm, currentID, job.SessionID)
			if normErr != nil {
				return contextwindow.HistoryMessage{}, nil, normErr
			}
			current = cur
			plan, planErr := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
				PolicyMode:               policy.Mode,
				ModelContextWindowTokens: policy.ModelContextWindowTokens,
				OutputReserveTokens:      policy.OutputReserveTokens,
				SafetyMarginTokens:       policy.SafetyMarginTokens,
				MaxInputTokens:           policy.EffectiveMaxInputTokens,
				MaxRecentTurns:           policy.MaxRecentTurns,
				TokenizerProfile:         policy.TokenizerProfile,
				SystemPrompt:             system,
				Tools:                    toolSchemas,
				PriorTurns:               turns,
				CurrentUser:              current,
			})
			if planErr != nil {
				return contextwindow.HistoryMessage{}, nil, planErr
			}
			priorTurns = turns
			// Continuous recent-suffix selection: once any loaded turn is omitted,
			// older bodies cannot enter the window — stop decrypting/fetching.
			if plan.OmittedTurnCount > 0 {
				stopDecrypt = true
				break
			}
			if policy.MaxRecentTurns > 0 && int64(plan.SelectedTurnCount) >= policy.MaxRecentTurns {
				stopDecrypt = true
				break
			}
		}
		if stopDecrypt || !page.HasMore || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	return current, priorTurns, nil
}

func (b *Bridge) resolveMessageContent(ctx context.Context, job agentrun.Job, msg chat.Message) (string, error) {
	content := strings.TrimSpace(msg.Content)
	if content == "" && msg.ContentObjectID != "" && b.content != nil {
		loaded, readErr := b.content.ReadPermanentChat(
			ctx, job.WorkspaceID, msg.ContentObjectID, job.ActorID,
		)
		if readErr != nil {
			return "", readErr
		}
		content = strings.TrimSpace(loaded)
	}
	return content, nil
}

func agentSnapshotPromptRevisionID(raw json.RawMessage) string {
	var doc struct {
		PromptRevisionID string `json:"promptRevisionId"`
	}
	_ = json.Unmarshal(raw, &doc)
	return strings.TrimSpace(doc.PromptRevisionID)
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func modelRole(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "USER":
		return "user"
	case "ASSISTANT":
		return "assistant"
	case "SYSTEM":
		return "system"
	case "TOOL":
		return "tool"
	default:
		return ""
	}
}

func (b *Bridge) completeRun(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	content string,
	streamMessageID string,
	resumed bool,
) error {
	// Prefer the stream item/message ID so item.delta and item.completed share
	// one identity (AAP text golden / SDK RunReducer). Fall back only when the
	// drive never opened a text sink (tests / no TextSinkFactory).
	assistantID := strings.TrimSpace(streamMessageID)
	if assistantID == "" {
		var err error
		assistantID, err = newRuntimeID()
		if err != nil {
			return b.failRun(ctx, job, run, err)
		}
	}
	// Terminal extract only (KD-3/4/16): never mid-tool intermediate. Stream
	// text_delta remains raw (may include fence fragments); item.completed is authoritative.
	content, _ = materializeAssistantTerminalContent(content, run.ContextPolicySnapshot, assistantID)

	run, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return b.failRun(ctx, job, run, err)
	}
	summary := []byte(`{"source":"chatruntimebridge"}`)
	if resumed {
		summary = []byte(`{"source":"chatruntimebridge","resumed":true}`)
	}
	result, err := b.results.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
		AssistantMessageID: assistantID, WorkspaceID: job.WorkspaceID,
		SessionID: job.SessionID, UserMessageID: job.UserMessageID, RunID: job.RunID,
		Content: content, ExpectedRunStatus: "RUNNING", ExpectedRunLock: run.LockVersion,
		RunStatus: "SUCCEEDED", RunOutputSummary: summary,
	})
	if err != nil {
		return b.failRun(ctx, job, run, err)
	}
	finished, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil {
		return err
	}
	return b.recordProtocol(ctx, chatruntime.ProtocolRecord{
		Kind: chatruntime.ProtocolRecordRunCompleted, Job: job, Run: finished,
		Message: &result.Message, ActorID: job.ActorID, OccurredAt: b.now().UTC(),
	})
}

func (b *Bridge) failRun(ctx context.Context, job agentrun.Job, run execution.AgentRun, cause error) error {
	// Cancel API owns durable CANCELLED. WithoutCancel persist must not rewrite
	// that race into FAILED. Check the incoming ctx/cause before stripping cancel.
	if userCancelledRun(ctx, cause) {
		return cause
	}
	// Never rely on a cancelled drive context for durable status transitions.
	ctx = context.WithoutCancel(ctx)
	errorCode := executionErrorCode(cause)

	var failedMessage *chat.Message
	var finished execution.AgentRun
	persisted := false

	if b.results != nil {
		// Refresh lock; prefer the same RecordAssistantResult path as legacy so the
		// session unlocks and the user sees a failed assistant message.
		if current, getErr := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID); getErr == nil {
			run = current
		}
		if run.Status == "RUNNING" {
			assistantID, idErr := newRuntimeID()
			if idErr == nil {
				assistantContent := "抱歉，当前无法完成回复：" + userSafeBridgeError(cause)
				if result, recordErr := b.results.RecordAssistantResult(ctx, chat.RecordAssistantResultInput{
					AssistantMessageID: assistantID, WorkspaceID: job.WorkspaceID,
					SessionID: job.SessionID, UserMessageID: job.UserMessageID, RunID: job.RunID,
					Content: assistantContent, ExpectedRunStatus: "RUNNING", ExpectedRunLock: run.LockVersion,
					RunStatus: "FAILED", RunOutputSummary: json.RawMessage(`{"source":"chatruntimebridge","failed":true}`),
					RunErrorCode: errorCode,
				}); recordErr == nil {
					// ZKL-56 UX-03: persistence order is fixed —
					// FAILED Run + failed assistant message (committed) →
					// reload finished Run → append existing run.failed protocol.
					// Protocol failure must NOT roll back the committed FAILED Run.
					msg := result.Message
					failedMessage = &msg
					persisted = true
					if reloaded, reloadErr := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID); reloadErr == nil {
						finished = reloaded
					} else {
						// Commit already happened; use best-effort run view for protocol.
						finished = run
						finished.Status = "FAILED"
						finished.ErrorCode = errorCode
					}
				}
			}
		}
	}
	// Fallback: direct status CAS when assistant recording is unavailable.
	if !persisted && b.steps != nil && run.Status == "RUNNING" {
		if updated, transErr := b.steps.TransitionAgentRun(ctx, job.WorkspaceID, job.RunID, execution.RunTransition{
			ExpectedStatus: "RUNNING", NewStatus: "FAILED",
			ErrorCode:     errorCode,
			OutputSummary: json.RawMessage(`{"source":"chatruntimebridge","failed":true}`),
		}); transErr == nil {
			finished = updated
			persisted = true
		}
	}

	// Project run.failed only when we have the required terminal message identity
	// (NativeProtocolRecorder.recordTerminal requires Message + ActorID).
	if persisted && failedMessage != nil && strings.TrimSpace(finished.ID) != "" {
		if projErr := b.recordProtocol(ctx, chatruntime.ProtocolRecord{
			Kind: chatruntime.ProtocolRecordRunFailed, Job: job, Run: finished,
			Message: failedMessage, ActorID: firstNonEmpty(job.ActorID, finished.TriggeredByID),
			OccurredAt: b.now().UTC(),
		}); projErr != nil {
			b.logger.Error("run.failed protocol projection failed after durable FAILED commit",
				"event", "chatruntimebridge.run_failed.protocol_projection_failed",
				"workspace_id", job.WorkspaceID,
				"run_id", job.RunID,
				"error_code", errorCode,
				"error", projErr.Error(),
			)
			// Do not return projErr: durable FAILED + message are the recovery SoT via GET.
		}
	}
	return cause
}

// ensureRunNotLeftRunning forces FAILED when Execute returned but the run is still RUNNING
// (except the caller must skip this for waiting-confirmation).
func (b *Bridge) ensureRunNotLeftRunning(ctx context.Context, job agentrun.Job, cause error) {
	if b == nil || b.runs == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	run, err := b.runs.GetAgentRun(ctx, job.WorkspaceID, job.RunID)
	if err != nil || run.Status != "RUNNING" {
		return
	}
	_ = b.failRun(ctx, job, run, cause)
}

// userCancelledRun reports an in-process CancelRun. context.WithoutCancel
// drops Cause, so callers must pass the drive ctx (or ErrRunCancelled itself).
// DeadlineExceeded (Enqueue timeout) is not user cancel and still fails the run.
func userCancelledRun(ctx context.Context, err error) bool {
	if errors.Is(err, ErrRunCancelled) {
		return true
	}
	if ctx == nil {
		return false
	}
	return errors.Is(context.Cause(ctx), ErrRunCancelled)
}

func userSafeBridgeError(err error) string {
	if err == nil {
		return "未知错误"
	}
	var ctxErr *execution.ContextError
	if errors.As(err, &ctxErr) && ctxErr != nil && strings.TrimSpace(ctxErr.Message) != "" {
		return ctxErr.Message
	}
	// Known context codes may wrap plain errors.New("CONTEXT_*").
	code := executionErrorCode(err)
	switch code {
	case execution.ErrCodeContextSnapshotUnsupported,
		execution.ErrCodeContextModelLimitUnknown,
		execution.ErrCodeContextRequiredInputTooLarge,
		execution.ErrCodeContextAssemblyFailed,
		execution.ErrCodeContextWindowExceededUpstream:
		return execution.NewContextError(code).Message
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "未知错误"
	}
	// Keep user-facing text short; avoid dumping huge internal frames.
	if len(msg) > 240 {
		return msg[:240] + "…"
	}
	return msg
}

func (b *Bridge) recordProtocol(ctx context.Context, record chatruntime.ProtocolRecord) error {
	if b.events == nil {
		return nil
	}
	return b.events.Record(ctx, record)
}

func (b *Bridge) recordPending(key string, pending einoruntime.PendingConfirmInterrupt) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	b.pendingConfirms[key] = append(b.pendingConfirms[key], pending)
}

func (b *Bridge) takePending(key string) []einoruntime.PendingConfirmInterrupt {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	items := b.pendingConfirms[key]
	delete(b.pendingConfirms, key)
	return items
}

func (b *Bridge) clearPending(key string) {
	b.pendingMu.Lock()
	delete(b.pendingConfirms, key)
	b.pendingMu.Unlock()
}

func (b *Bridge) registerActiveRun(
	workspaceID, runID, slot string,
	cancel context.CancelCauseFunc,
) (*activeRunExecution, bool) {
	if b == nil || cancel == nil {
		return nil, false
	}
	key := activeRunSlotKey(activeRunKey(workspaceID, runID), slot)
	b.activeMu.Lock()
	defer b.activeMu.Unlock()
	var gen int64 = 1
	if prev, exists := b.activeRuns[key]; exists && prev != nil {
		// Reclaim/re-entry: interrupt previous holder and bump generation so its
		// deferred unregister cannot clear this registration (pointer+gen match).
		gen = prev.gen + 1
		if prev.cancel != nil {
			prev.cancel(ErrRunCancelled)
		}
	}
	active := &activeRunExecution{cancel: cancel, gen: gen}
	b.activeRuns[key] = active
	return active, true
}

func (b *Bridge) unregisterActiveRun(
	workspaceID, runID, slot string,
	active *activeRunExecution,
) {
	key := activeRunSlotKey(activeRunKey(workspaceID, runID), slot)
	b.activeMu.Lock()
	// Generation-aware: only the matching generation clears the slot.
	if cur := b.activeRuns[key]; cur == active && (active == nil || cur.gen == active.gen) {
		delete(b.activeRuns, key)
	}
	b.activeMu.Unlock()
}

func startContinueLeaseRenew(ctx context.Context, lifecycle agentrun.ContinueLifecycle) func() {
	if lifecycle == nil || ctx == nil {
		return func() {}
	}
	ticker := time.NewTicker(2 * time.Minute)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := lifecycle.Renew(ctx); err != nil {
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// recordToolStep appends a TOOL agent_run_step with arguments + result so the
// platform-admin audit timeline can show "工具调用: {name}".
// Shapes match agentaudit.loadSteps: input_summary.toolName/arguments/toolCallId,
// output_summary as tool result body.
// When steps is not wired, this is a no-op (unit tests).
func (b *Bridge) recordToolStep(ctx context.Context, event einoruntime.ToolCompleteEvent) error {
	if b == nil || b.steps == nil {
		return nil
	}
	if strings.TrimSpace(event.WorkspaceID) == "" || strings.TrimSpace(event.AgentRunID) == "" {
		return nil
	}
	stepID, err := newRuntimeID()
	if err != nil {
		return err
	}
	argsAny := decodeToolJSON(event.ArgsJSON)
	inputSummary, err := json.Marshal(map[string]any{
		"source":       "chatruntimebridge",
		"toolName":     strings.TrimSpace(event.ToolName),
		"callableName": strings.TrimSpace(event.ToolName),
		"arguments":    argsAny,
		"toolCallId":   strings.TrimSpace(event.InvocationID),
		"releaseId":    strings.TrimSpace(event.ReleaseID),
		"capabilityId": strings.TrimSpace(event.CapabilityID),
	})
	if err != nil {
		return err
	}
	runID := event.AgentRunID
	stepIn := execution.AppendAgentRunStepInput{
		ID: stepID, WorkspaceID: event.WorkspaceID, RunID: runID,
		StepType: "TOOL", CapabilityReleaseID: strings.TrimSpace(event.ReleaseID),
		InputSummary: inputSummary,
	}
	if rc, ok := agentdelegation.RunContextFrom(ctx); ok && rc != nil {
		// Nested agent attribution: agent_id = executing agent; run may be TASK child.
		if rc.RunID != "" {
			stepIn.RunID = rc.RunID
		}
		stepIn.AgentID = rc.CallerAgentID
		if rc.ParentDelegationID != nil {
			stepIn.DelegationID = *rc.ParentDelegationID
		}
		// Same-run parent_step_id only (TASK child nests via delegation_id).
		if sameRunParentStep(rc) {
			stepIn.ParentStepID = *rc.ParentStepID
		}
	}
	if _, err := b.steps.AppendAgentRunStep(ctx, stepIn); err != nil {
		return fmt.Errorf("append TOOL step: %w", err)
	}
	outputSummary, err := json.Marshal(buildToolStepOutputSummary(event))
	if err != nil {
		return err
	}
	newStatus := "SUCCEEDED"
	errorCode := ""
	if !event.OK {
		newStatus = "FAILED"
		errorCode = firstNonEmpty(event.ErrorCode, "TOOL_INVOKE_FAILED")
	}
	if _, err := b.steps.TransitionAgentRunStep(ctx, event.WorkspaceID, stepID, execution.StepTransition{
		ExpectedStatus: "RUNNING", NewStatus: newStatus,
		OutputSummary: outputSummary, ErrorCode: errorCode,
	}); err != nil {
		return fmt.Errorf("transition TOOL step: %w", err)
	}
	return nil
}

func decodeToolJSON(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if json.Unmarshal([]byte(trimmed), &value) != nil {
		return map[string]any{"raw": trimmed}
	}
	return value
}

func buildToolStepOutputSummary(event einoruntime.ToolCompleteEvent) map[string]any {
	body := map[string]any{
		"ok":           event.OK,
		"invocationId": strings.TrimSpace(event.InvocationID),
		"source":       "chatruntimebridge",
	}
	if code := strings.TrimSpace(event.ErrorCode); code != "" {
		body["errorCode"] = code
	}
	// Prefer structured tool result (ok/error envelope from PipelineTool).
	if decoded := decodeToolJSON(event.ResultJSON); decoded != nil {
		if object, ok := decoded.(map[string]any); ok {
			for key, value := range object {
				// Keep top-level ok/errorCode from event when already set.
				if key == "ok" || key == "errorCode" {
					if _, exists := body[key]; exists {
						continue
					}
				}
				body[key] = value
			}
			return body
		}
		body["result"] = decoded
		return body
	}
	if text := strings.TrimSpace(event.ResultJSON); text != "" {
		body["result"] = text
	}
	return body
}

// recordModelTurn appends a MODEL agent_run_step and stores permanent MODEL_TURN
// evidence so the platform-admin audit timeline can show "大模型推理".
//
// Reasoning is only written into the permanent payload and output_summary when
// AgentAuditDebug is enabled.
// When steps/modelTurns are not wired, this is a no-op so unit tests stay offline.
func (b *Bridge) recordModelTurn(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	turn einoruntime.ModelTurn,
) error {
	if b == nil || b.steps == nil || b.modelTurns == nil {
		return nil
	}
	stepID, err := newRuntimeID()
	if err != nil {
		return err
	}
	reasoningForAudit := ""
	if b.agentAuditDebug {
		reasoningForAudit = reasoningTextForAudit(turn)
	}
	inputSummary, _ := json.Marshal(map[string]any{
		"source":          "chatruntimebridge",
		"hasReasoning":    strings.TrimSpace(reasoningForAudit) != "",
		"contentLength":   len(strings.TrimSpace(turn.Content)),
		"reasoningTokens": turn.ReasoningTokens,
		"hasToolCalls":    turn.HasToolCalls,
		"tokensKnown":     turn.TokensKnown,
		// Prompt-cache hits are the only observable proof that the frozen,
		// cache-stable assembly is being rewarded upstream; nothing else in the
		// audit trail can be used to reconstruct it after the fact.
		"cachedPromptTokens": turn.CachedPromptTokens,
	})
	modelStep := execution.AppendAgentRunStepInput{
		ID: stepID, WorkspaceID: job.WorkspaceID, RunID: job.RunID,
		StepType: "MODEL", InputSummary: inputSummary, AgentID: run.AgentID,
	}
	var parentDelID string
	if rc, ok := agentdelegation.RunContextFrom(ctx); ok && rc != nil {
		if rc.RunID != "" {
			modelStep.RunID = rc.RunID
		}
		if rc.CallerAgentID != "" {
			modelStep.AgentID = rc.CallerAgentID
		}
		if rc.ParentDelegationID != nil {
			modelStep.DelegationID = *rc.ParentDelegationID
			parentDelID = *rc.ParentDelegationID
		}
		if sameRunParentStep(rc) {
			modelStep.ParentStepID = *rc.ParentStepID
		}
	}
	if _, err := b.steps.AppendAgentRunStep(ctx, modelStep); err != nil {
		return fmt.Errorf("append MODEL step: %w", err)
	}
	payload, err := json.Marshal(buildModelTurnAuditPayload(turn, true, b.agentAuditDebug))
	if err != nil {
		return err
	}
	if _, err := b.modelTurns.Record(ctx, chatruntime.ModelTurnRecordInput{
		WorkspaceID: job.WorkspaceID, StepID: stepID,
		Content: payload, CreatedByType: run.TriggeredByType, CreatedByID: run.TriggeredByID,
		ExpectedStatus: "RUNNING", NewStatus: "SUCCEEDED",
		Reasoning: reasoningForAudit,
	}); err != nil {
		return fmt.Errorf("record MODEL turn evidence: %w", err)
	}
	// Aggregate tokens onto the parent AGENT_DELEGATION when nested (fail-closed).
	if turn.TokensKnown && parentDelID != "" && b.delegation != nil && b.delegation.Audit != nil {
		if aerr := b.delegation.Audit.AccumulateModelTokens(ctx, job.WorkspaceID, parentDelID, agentdelegation.TokenUsage{
			PromptTokens: turn.PromptTokens, CompletionTokens: turn.CompletionTokens,
			TotalTokens: turn.TotalTokens, Known: true,
		}); aerr != nil {
			return fmt.Errorf("accumulate model tokens: %w", aerr)
		}
	}
	return nil
}

// reasoningTextForAudit prefers provider reasoning_content; when the upstream
// only reports usage.reasoning_tokens (common on some gpt-5 gateways with tools),
// surface a debug note so the audit timeline is not empty "无推理数据".
func reasoningTextForAudit(turn einoruntime.ModelTurn) string {
	if text := strings.TrimSpace(turn.Reasoning); text != "" {
		return text
	}
	if turn.ReasoningTokens > 0 {
		return fmt.Sprintf(
			"[upstream] reasoning_tokens=%d；未返回 reasoning_content 正文（网关在部分 tool/stream 请求下只计 token 不暴露文本）",
			turn.ReasoningTokens,
		)
	}
	return ""
}

// buildModelTurnAuditPayload: compact fields always; reasoning only when
// agentAuditDebug is on. Usage/toolCalls always recorded for call-chain evidence.
func buildModelTurnAuditPayload(turn einoruntime.ModelTurn, ok, agentAuditDebug bool) map[string]any {
	payload := map[string]any{
		"content":      strings.TrimSpace(turn.Content),
		"ok":           ok,
		"source":       "chatruntimebridge",
		"hasToolCalls": turn.HasToolCalls,
	}
	if len(turn.ToolCallIDs) > 0 {
		payload["toolCallIds"] = turn.ToolCallIDs
	}
	if turn.TokensKnown {
		usage := map[string]any{
			"promptTokens": turn.PromptTokens, "completionTokens": turn.CompletionTokens,
			"totalTokens": turn.TotalTokens,
		}
		if turn.CachedPromptTokens > 0 {
			usage["cachedPromptTokens"] = turn.CachedPromptTokens
		}
		payload["usage"] = usage
	}
	if turn.ReasoningTokens > 0 {
		payload["reasoningTokens"] = turn.ReasoningTokens
	}
	if !agentAuditDebug {
		return payload
	}
	if text := reasoningTextForAudit(turn); text != "" {
		payload["reasoning"] = text
	}
	return payload
}

func normalizeJob(job agentrun.Job) agentrun.Job {
	job.WorkspaceID = strings.TrimSpace(job.WorkspaceID)
	job.SessionID = strings.TrimSpace(job.SessionID)
	job.RunID = strings.TrimSpace(job.RunID)
	job.UserMessageID = strings.TrimSpace(job.UserMessageID)
	job.ActorID = strings.TrimSpace(job.ActorID)
	return job
}

func jobReady(job agentrun.Job) bool {
	return job.WorkspaceID != "" && job.SessionID != "" && job.RunID != "" &&
		job.UserMessageID != "" && job.ActorID != ""
}

func activeRunKey(workspaceID, runID string) string {
	return strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(runID)
}

func activeRunSlotKey(runKey, slot string) string {
	return runKey + "\x00" + slot
}

func pendingConfirmKey(workspaceID, runID string) string {
	return activeRunKey(workspaceID, runID)
}

func newRuntimeID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// sameRunParentStep is true when parent_step_id would reference a step on the
// same agent_run as the nested step (INLINE). TASK child runs must not set it.
func sameRunParentStep(rc *agentdelegation.RunContext) bool {
	if rc == nil || rc.ParentStepID == nil || strings.TrimSpace(*rc.ParentStepID) == "" {
		return false
	}
	runID := strings.TrimSpace(rc.RunID)
	parentRun := strings.TrimSpace(rc.ParentRunID)
	if runID == "" || parentRun == "" {
		return false
	}
	return runID == parentRun
}

func executionErrorCode(err error) string {
	if err == nil {
		return "INVOCATION_FAILED"
	}
	if errors.Is(err, chatruntime.ErrModelContentUnsupported) {
		return chatruntime.ErrCodeModelContentUnsupported
	}
	if errors.Is(err, ErrAgenticModelSnapshotRequired) {
		return "AGENTIC_MODEL_SNAPSHOT_REQUIRED"
	}
	if errors.Is(err, ErrAgenticGraphSnapshotRequired) {
		return "AGENTIC_GRAPH_SNAPSHOT_REQUIRED"
	}
	if errors.Is(err, ErrAgenticProviderTupleUnsupported) {
		return "AGENTIC_PROVIDER_TUPLE_UNSUPPORTED"
	}
	if errors.Is(err, ErrAgenticAgentSnapshotRequired) {
		return "AGENTIC_AGENT_SNAPSHOT_REQUIRED"
	}
	if errors.Is(err, ErrAgenticCapabilitySnapshotRequired) {
		return "AGENTIC_CAPABILITY_SNAPSHOT_REQUIRED"
	}
	if errors.Is(err, ErrAgenticPromptRevisionMismatch) {
		return "AGENTIC_PROMPT_REVISION_MISMATCH"
	}
	if errors.Is(err, ErrAgenticResumeGenerationMismatch) {
		return "AGENTIC_RESUME_GENERATION_MISMATCH"
	}
	if errors.Is(err, ErrAgenticResumeClassicOnFrozenRun) {
		return "AGENTIC_RESUME_CLASSIC_ON_FROZEN_RUN"
	}
	if errors.Is(err, ErrClassicResumeRemoved) {
		return "CLASSIC_RESUME_REMOVED"
	}
	var ctxErr *execution.ContextError
	if errors.As(err, &ctxErr) && ctxErr != nil && strings.TrimSpace(ctxErr.Code) != "" {
		return ctxErr.Code
	}
	// Plain sentinel strings used by contextwindow / sessioncontext.
	msg := err.Error()
	for _, code := range []string{
		execution.ErrCodeContextSnapshotUnsupported,
		execution.ErrCodeContextModelLimitUnknown,
		execution.ErrCodeContextRequiredInputTooLarge,
		execution.ErrCodeContextAssemblyFailed,
		execution.ErrCodeContextWindowExceededUpstream,
		chatruntime.ErrCodeModelContentUnsupported,
		"AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		"AGENTIC_GRAPH_SNAPSHOT_REQUIRED",
		"AGENTIC_PROVIDER_TUPLE_UNSUPPORTED",
		"AGENTIC_AGENT_SNAPSHOT_REQUIRED",
		"AGENTIC_CAPABILITY_SNAPSHOT_REQUIRED",
		"AGENTIC_PROMPT_REVISION_MISMATCH",
		"AGENTIC_RESUME_GENERATION_MISMATCH",
		"AGENTIC_RESUME_CLASSIC_ON_FROZEN_RUN",
	} {
		if strings.Contains(msg, code) {
			return code
		}
	}
	if code := execution.ErrorCode(err); code != "" {
		return code
	}
	return "INVOCATION_FAILED"
}
