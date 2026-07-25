package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflow"
)

// GeneratedByV2 is the product-path generatedBy marker (D6).
const GeneratedByV2 = "smart-dag.v2"

// GraphModel is the injectable LLM boundary for graph generation.
// Production uses PlatformChatModel behind an adapter; tests inject fakes.
type GraphModel interface {
	// GenerateGraph returns a candidate workflow.graph.v1 draft from model output.
	// Implementations must not write Drafts; persist is gated by GuardGraph.
	GenerateGraph(ctx context.Context, input GraphModelInput) (domain.WorkflowGraphDraft, error)
}

// GraphModelInput is structured context for one generation turn (no user system prompt).
// Multi-turn context (D15): System Prompt + Agent + published Tool catalog + current graph + history + message.
// Feedback (D14) carries compile/trial/production failure context for draft-only revise.
type GraphModelInput struct {
	SystemPrompt SystemPrompt
	AgentID      string
	WorkspaceID  string
	Message      string
	// CatalogToolIDs are published tool IDs the model may reference.
	CatalogToolIDs []string
	// CurrentGraph is the prior draft graph if any (nil for first turn).
	CurrentGraph *domain.WorkflowGraphDraft
	// History is prior user/assistant turns in this generate session (oldest first).
	History []TurnHistoryItem
	// ModelConfigID is informational (already resolved via Agent); not a bypass.
	ModelConfigID string
	// Feedback is optional FailureFeedback for revise-from-failure (D14).
	// Models must treat this as revision context; persist path never publishes.
	Feedback *FailureFeedback
}

// TurnHistoryItem is one prior generate-session turn for multi-turn model context.
type TurnHistoryItem struct {
	Role    string // "user" | "assistant"
	Content string
}

// DraftStore is the minimal workflow write surface for generate turns.
type DraftStore interface {
	Create(ctx context.Context, input workflow.CreateInput) (workflow.Workflow, workflow.Draft, error)
	UpdateDraft(ctx context.Context, workspaceID, capabilityID string, input workflow.DraftUpdate) (workflow.Draft, error)
}

// TurnService applies one smart-dag.v2 generation turn with guard-before-persist (D3).
type TurnService struct {
	model    GraphModel
	drafts   DraftStore
	prompts  SystemPromptStore
	gate     *AgentModelGate
	tools    ToolCatalog
	nextID   IDGenerator
	maxNodes int
}

// TurnServiceDeps wires turn application dependencies.
type TurnServiceDeps struct {
	Model    GraphModel
	Drafts   DraftStore
	Prompts  SystemPromptStore
	Gate     *AgentModelGate
	Tools    ToolCatalog
	NextID   IDGenerator
	MaxNodes int
}

// NewTurnService constructs a turn applicator. All deps except MaxNodes are required.
func NewTurnService(deps TurnServiceDeps) (*TurnService, error) {
	if deps.Model == nil || deps.Drafts == nil || deps.Prompts == nil || deps.Gate == nil || deps.Tools == nil || deps.NextID == nil {
		return nil, errors.New("turn service dependencies are required")
	}
	maxNodes := deps.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	return &TurnService{
		model:    deps.Model,
		drafts:   deps.Drafts,
		prompts:  deps.Prompts,
		gate:     deps.Gate,
		tools:    deps.Tools,
		nextID:   deps.NextID,
		maxNodes: maxNodes,
	}, nil
}

// ApplyTurnRequest is one multi-turn generation step (domain only; HTTP is T-P1b).
type ApplyTurnRequest struct {
	WorkspaceID string
	AgentID     string
	// RequestModelConfigID must be empty; non-empty → ErrModelConfigBypassRejected.
	RequestModelConfigID string
	Message              string
	CreatedBy            string
	// Prior holds the last good draft state when revising; nil means create new workflow.
	Prior *PriorDraft
	// History is prior user/assistant messages for multi-turn context (D15).
	History []TurnHistoryItem
	// SessionID stamps graph UI for audit (optional).
	SessionID string
	// GenerationID stamps graph UI / turn audit linkage (optional).
	GenerationID string
	// TraceID stamps graph UI for request correlation (P6.3).
	TraceID string
	// Feedback is optional FailureFeedback for seed+feedback revise (D14).
	// On success: only Create/UpdateDraft (draftVersion bump); never Publish/Activate (D5).
	Feedback *FailureFeedback
	// MaxNodes overrides service default when > 0.
	MaxNodes int
}

// PriorDraft is the last persisted good draft (for non-clobber assertions).
type PriorDraft struct {
	WorkflowID           string
	DraftID              string
	DraftVersion         int64
	LockVersion          int64
	Graph                domain.WorkflowGraphDraft
	ExpectedDraftVersion int64
	ExpectedLockVersion  int64
}

// ApplyTurnResult is success payload after guard + persist.
type ApplyTurnResult struct {
	Workflow      workflow.Workflow
	Draft         workflow.Draft
	Graph         domain.WorkflowGraphDraft
	GuardReport   GuardReport
	Audit         GenerationAuditMeta
	AgentID       string
	ModelConfigID string
	GeneratedBy   string
}

// ApplyTurn resolves agent/model, loads prompt, calls LLM, guards, then persists.
// On guard failure: returns *GuardError and does not call Create/UpdateDraft.
// On missing model: returns ErrAgentModelRequired and does not persist.
func (s *TurnService) ApplyTurn(ctx context.Context, request ApplyTurnRequest) (ApplyTurnResult, error) {
	if s == nil {
		return ApplyTurnResult{}, errors.New("turn service is nil")
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Message = strings.TrimSpace(request.Message)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if request.Feedback != nil {
		if err := request.Feedback.Validate(); err != nil {
			return ApplyTurnResult{}, err
		}
		request.Feedback.Normalize()
		// Seed+feedback path (D14): synthesize message when client only sent feedback.
		if request.Message == "" {
			request.Message = FormatRevisionMessage(request.Feedback)
		}
	}
	if !validUUID(request.WorkspaceID) || !validUUID(request.AgentID) || !validUUID(request.CreatedBy) || request.Message == "" {
		return ApplyTurnResult{}, ErrInvalid
	}

	resolved, err := s.gate.Resolve(ctx, request.WorkspaceID, request.AgentID, request.RequestModelConfigID)
	if err != nil {
		return ApplyTurnResult{}, err
	}

	prompt, err := s.prompts.Active(ctx)
	if err != nil {
		return ApplyTurnResult{}, fmt.Errorf("load smart orchestration system prompt: %w", err)
	}
	audit := AuditMetaFromPrompt(prompt)

	tools, err := s.tools.List(ctx, request.WorkspaceID)
	if err != nil {
		return ApplyTurnResult{}, fmt.Errorf("list tools for generation: %w", err)
	}
	available := publishedTools(tools)
	catalogIDs := toolIDs(available)

	var current *domain.WorkflowGraphDraft
	if request.Prior != nil {
		g := request.Prior.Graph
		current = &g
	}

	candidate, err := s.model.GenerateGraph(ctx, GraphModelInput{
		SystemPrompt:   prompt,
		AgentID:        resolved.Agent.ID,
		WorkspaceID:    request.WorkspaceID,
		Message:        request.Message,
		CatalogToolIDs: catalogIDs,
		CurrentGraph:   current,
		History:        request.History,
		ModelConfigID:  resolved.ModelConfig.ID,
		Feedback:       request.Feedback,
	})
	if err != nil {
		return ApplyTurnResult{}, fmt.Errorf("generate graph from model: %w", err)
	}
	// Deterministic repair of LLM layout/edge aliases before guard (D3: still no persist on fail).
	candidate = NormalizeCandidateGraph(candidate)

	maxNodes := s.maxNodes
	if request.MaxNodes > 0 {
		maxNodes = request.MaxNodes
	}
	report := GuardGraph(candidate, GuardOptions{
		CatalogToolIDs: CatalogToolIDSet(catalogIDs),
		MaxNodes:       maxNodes,
	})
	if !report.OK {
		// D3: do not Create/UpdateDraft — prior draft version stays put.
		return ApplyTurnResult{
			GuardReport:   report,
			Audit:         audit,
			AgentID:       resolved.Agent.ID,
			ModelConfigID: resolved.ModelConfig.ID,
			GeneratedBy:   GeneratedByV2,
		}, NewGuardError(report)
	}

	// Stamp product-path UI metadata (D6 / D16 / D15).
	// Feedback path (D5/D14): stamp revisedFrom only — never Publish/Activate after guard.
	if candidate.UI == nil {
		candidate.UI = map[string]any{}
	}
	candidate.UI["generatedBy"] = GeneratedByV2
	candidate.UI["agentId"] = resolved.Agent.ID
	candidate.UI["modelConfigId"] = resolved.ModelConfig.ID
	candidate.UI["promptId"] = audit.PromptID
	candidate.UI["promptHash"] = audit.PromptHash
	candidate.UI["businessGoal"] = request.Message
	if request.SessionID != "" {
		candidate.UI["sessionId"] = request.SessionID
	}
	if request.GenerationID != "" {
		candidate.UI["generationId"] = request.GenerationID
	}
	if request.TraceID != "" {
		candidate.UI["traceId"] = request.TraceID
	}
	if request.Feedback != nil {
		if revised := RevisedFromUI(request.Feedback); revised != nil {
			candidate.UI["revisedFrom"] = revised
		}
	}
	if candidate.SchemaVersion == "" {
		candidate.SchemaVersion = SchemaVersion
	}

	encoded, err := json.Marshal(candidate)
	if err != nil {
		return ApplyTurnResult{}, fmt.Errorf("marshal guarded graph: %w", err)
	}

	var (
		wf    workflow.Workflow
		draft workflow.Draft
	)
	if request.Prior == nil {
		capabilityID, idErr := s.nextID()
		if idErr != nil {
			return ApplyTurnResult{}, fmt.Errorf("generate workflow id: %w", idErr)
		}
		draftID, idErr := s.nextID()
		if idErr != nil {
			return ApplyTurnResult{}, fmt.Errorf("generate draft id: %w", idErr)
		}
		if !validUUID(capabilityID) || !validUUID(draftID) {
			return ApplyTurnResult{}, errors.New("id generator returned invalid UUID")
		}
		wf, draft, err = s.drafts.Create(ctx, workflow.CreateInput{
			CapabilityID:  capabilityID,
			DraftID:       draftID,
			WorkspaceID:   request.WorkspaceID,
			Name:          generatedName(request.Message),
			Slug:          "ai-workflow-" + strings.ReplaceAll(capabilityID[:8], "-", ""),
			Description:   "由智能编排根据业务目标生成：" + request.Message,
			SchemaVersion: SchemaVersion,
			Graph:         encoded,
			CreatedBy:     request.CreatedBy,
		})
		if err != nil {
			return ApplyTurnResult{}, fmt.Errorf("create smart dag v2 workflow draft: %w", err)
		}
	} else {
		expectedDraftVersion := request.Prior.ExpectedDraftVersion
		if expectedDraftVersion == 0 {
			expectedDraftVersion = request.Prior.DraftVersion
		}
		expectedLock := request.Prior.ExpectedLockVersion
		if expectedLock == 0 {
			expectedLock = request.Prior.LockVersion
		}
		draft, err = s.drafts.UpdateDraft(ctx, request.WorkspaceID, request.Prior.WorkflowID, workflow.DraftUpdate{
			SchemaVersion:        SchemaVersion,
			Graph:                encoded,
			UpdatedBy:            request.CreatedBy,
			ExpectedDraftVersion: expectedDraftVersion,
			ExpectedLockVersion:  expectedLock,
		})
		if err != nil {
			return ApplyTurnResult{}, fmt.Errorf("update smart dag v2 workflow draft: %w", err)
		}
		wf = workflow.Workflow{
			CapabilityID:   request.Prior.WorkflowID,
			WorkspaceID:    request.WorkspaceID,
			CurrentDraftID: request.Prior.DraftID,
		}
	}

	return ApplyTurnResult{
		Workflow:      wf,
		Draft:         draft,
		Graph:         candidate,
		GuardReport:   report,
		Audit:         audit,
		AgentID:       resolved.Agent.ID,
		ModelConfigID: resolved.ModelConfig.ID,
		GeneratedBy:   GeneratedByV2,
	}, nil
}
