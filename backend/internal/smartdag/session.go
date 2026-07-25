package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflow"
)

// Session status values (migration 000059).
const (
	SessionStatusOpen   = "OPEN"
	SessionStatusClosed = "CLOSED"
)

// Turn status values (migration 000059).
const (
	TurnStatusSucceeded     = "SUCCEEDED"
	TurnStatusGuardRejected = "GUARD_REJECTED"
	TurnStatusFailed        = "FAILED"
)

// MaxTurnMessageRunes is the API message length limit (1..2000).
const MaxTurnMessageRunes = 2000

// GenerateSession is a Console-only multi-turn generation context (D15).
// Independent of ChatSession / AAP Conversation.
type GenerateSession struct {
	ID            string
	WorkspaceID   string
	AgentID       string
	WorkflowID    *string
	ModelConfigID string
	Status        string
	PromptID      string
	PromptHash    string
	Constraints   json.RawMessage
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ClosedAt      *time.Time
	LockVersion   int64
}

// GenerateTurn is one user message + generation attempt within a session.
type GenerateTurn struct {
	ID               string
	WorkspaceID      string
	SessionID        string
	TurnIndex        int
	UserMessage      string
	AssistantMessage string
	GenerationID     string
	GuardOK          bool
	GuardReport      GuardReport
	DraftVersion     *int64
	Status           string
	ErrorCode        string
	PromptID         string
	PromptHash       string
	CreatedAt        time.Time
}

// SessionStore persists generate sessions and turns.
type SessionStore interface {
	CreateSession(ctx context.Context, session GenerateSession) (GenerateSession, error)
	GetSession(ctx context.Context, workspaceID, sessionID string) (GenerateSession, error)
	CloseSession(ctx context.Context, workspaceID, sessionID string, closedAt time.Time) (GenerateSession, error)
	SetSessionWorkflow(ctx context.Context, workspaceID, sessionID, workflowID string) (GenerateSession, error)
	CreateTurn(ctx context.Context, turn GenerateTurn) (GenerateTurn, error)
	ListTurns(ctx context.Context, workspaceID, sessionID string) ([]GenerateTurn, error)
	NextTurnIndex(ctx context.Context, workspaceID, sessionID string) (int, error)
	// ClaimSessionLockVersion CAS-advances lock_version from expected→expected+1.
	// When expected is nil, uses the current version (old-client compatibility).
	// Returns ErrSessionVersionConflict on mismatch.
	ClaimSessionLockVersion(ctx context.Context, workspaceID, sessionID string, expected *int64) (GenerateSession, error)
	// AdvanceSessionLockVersion CAS-advances lock_version from expected→expected+1
	// after a terminal turn commit (claimed N+1 → final N+2).
	AdvanceSessionLockVersion(ctx context.Context, workspaceID, sessionID string, expected int64) (GenerateSession, error)
}

// DraftReader loads the current workflow draft for multi-turn Prior state.
type DraftReader interface {
	GetDraft(ctx context.Context, workspaceID, capabilityID string) (workflow.Draft, error)
	Get(ctx context.Context, workspaceID, capabilityID string) (workflow.Workflow, error)
}

// SessionService owns create/turn/get/close for SmartGenerateSession.
type SessionService struct {
	sessions SessionStore
	turns    *TurnService
	gate     *AgentModelGate
	prompts  SystemPromptStore
	drafts   DraftReader
	locker   SessionLocker
	nextID   IDGenerator
	now      func() time.Time
}

// SessionServiceDeps wires session orchestration.
type SessionServiceDeps struct {
	Sessions SessionStore
	Turns    *TurnService
	Gate     *AgentModelGate
	Prompts  SystemPromptStore
	Drafts   DraftReader
	// Locker is optional; when nil, MemorySessionLocker is used (safe for single-process tests).
	Locker SessionLocker
	NextID IDGenerator
	Now    func() time.Time
}

// NewSessionService constructs a session orchestrator.
func NewSessionService(deps SessionServiceDeps) (*SessionService, error) {
	if deps.Sessions == nil || deps.Turns == nil || deps.Gate == nil || deps.Prompts == nil || deps.NextID == nil {
		return nil, errors.New("session service dependencies are required")
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	locker := deps.Locker
	if locker == nil {
		locker = NewMemorySessionLocker()
	}
	return &SessionService{
		sessions: deps.Sessions,
		turns:    deps.Turns,
		gate:     deps.Gate,
		prompts:  deps.Prompts,
		drafts:   deps.Drafts,
		locker:   locker,
		nextID:   deps.NextID,
		now:      now,
	}, nil
}

// CreateSessionRequest creates an OPEN generate session after agent model gate.
type CreateSessionRequest struct {
	WorkspaceID string
	AgentID     string
	// RequestModelConfigID must be empty (no body bypass).
	RequestModelConfigID string
	WorkflowID           string
	Constraints          json.RawMessage
	CreatedBy            string
}

// CreateSession validates agent+model and persists an OPEN session.
// On AGENT_MODEL_REQUIRED: no session row is created.
func (s *SessionService) CreateSession(ctx context.Context, request CreateSessionRequest) (GenerateSession, error) {
	if s == nil {
		return GenerateSession{}, errors.New("session service is nil")
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.WorkflowID = strings.TrimSpace(request.WorkflowID)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if !validUUID(request.WorkspaceID) || !validUUID(request.AgentID) || !validUUID(request.CreatedBy) {
		return GenerateSession{}, ErrInvalid
	}
	if request.WorkflowID != "" && !validUUID(request.WorkflowID) {
		return GenerateSession{}, ErrInvalid
	}

	resolved, err := s.gate.Resolve(ctx, request.WorkspaceID, request.AgentID, request.RequestModelConfigID)
	if err != nil {
		return GenerateSession{}, err
	}

	prompt, err := s.prompts.Active(ctx)
	if err != nil {
		return GenerateSession{}, fmt.Errorf("load smart orchestration system prompt: %w", err)
	}
	audit := AuditMetaFromPrompt(prompt)

	sessionID, err := s.nextID()
	if err != nil {
		return GenerateSession{}, fmt.Errorf("generate session id: %w", err)
	}
	if !validUUID(sessionID) {
		return GenerateSession{}, errors.New("id generator returned invalid UUID")
	}

	constraints := request.Constraints
	if len(constraints) == 0 {
		constraints = json.RawMessage(`{}`)
	}
	now := s.now()
	session := GenerateSession{
		ID:            sessionID,
		WorkspaceID:   request.WorkspaceID,
		AgentID:       resolved.Agent.ID,
		ModelConfigID: resolved.ModelConfig.ID,
		Status:        SessionStatusOpen,
		PromptID:      audit.PromptID,
		PromptHash:    audit.PromptHash,
		Constraints:   constraints,
		CreatedBy:     request.CreatedBy,
		CreatedAt:     now,
		UpdatedAt:     now,
		LockVersion:   1,
	}
	if request.WorkflowID != "" {
		// Ensure workflow exists in workspace when continuing multi-turn on draft.
		if s.drafts != nil {
			if _, err := s.drafts.Get(ctx, request.WorkspaceID, request.WorkflowID); err != nil {
				return GenerateSession{}, err
			}
		}
		wfID := request.WorkflowID
		session.WorkflowID = &wfID
	}

	created, err := s.sessions.CreateSession(ctx, session)
	if err != nil {
		return GenerateSession{}, fmt.Errorf("create generate session: %w", err)
	}
	return created, nil
}

// ApplySessionTurnRequest is one natural-language generation turn.
type ApplySessionTurnRequest struct {
	WorkspaceID string
	SessionID   string
	Message     string
	// RequestModelConfigID must be empty.
	RequestModelConfigID string
	CreatedBy            string
	// TraceID correlates generate path audit with the HTTP request (P6.3).
	TraceID string
	// Feedback is optional FailureFeedback JSON (D14 seed+feedback revise).
	// On success: draftVersion bump only; never auto-publish (D5).
	Feedback json.RawMessage
	// ExpectedSessionLockVersion is optional for old clients (ZKL-56).
	// When set, must match current lock_version before claim (N → N+1).
	ExpectedSessionLockVersion *int64
}

// ApplySessionTurnResult is the HTTP-facing turn success payload.
type ApplySessionTurnResult struct {
	SessionID           string
	TurnID              string
	GenerationID        string
	Workflow            workflow.Workflow
	Draft               workflow.Draft
	Graph               domain.WorkflowGraphDraft
	AssistantMessage    string
	ReasoningSteps      []ReasoningStep
	MissingCapabilities []MissingCapability
	NodeExplanations    []NodeExplanation
	AvailableToolIDs    []string
	SelectedToolIDs     []string
	Confidence          int
	GuardReport         GuardReport
	DraftVersion        int64
	Audit               GenerationAuditMeta
	AgentID             string
	ModelConfigID       string
	GeneratedBy         string
	// TraceID echoes the request correlation id for FE/audit (P6.3).
	TraceID string
}

// ApplySessionTurn runs gate → model → guard → Create/UpdateDraft and records the turn.
// ZKL-56: holds session advisory lock, claims lock_version N→N+1, advances to N+2 on
// terminal turn persist. Model/parse/guard run outside DB transactions.
func (s *SessionService) ApplySessionTurn(ctx context.Context, request ApplySessionTurnRequest) (ApplySessionTurnResult, error) {
	if s == nil {
		return ApplySessionTurnResult{}, errors.New("session service is nil")
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Message = strings.TrimSpace(request.Message)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if !validUUID(request.WorkspaceID) || !validUUID(request.SessionID) || !validUUID(request.CreatedBy) {
		return ApplySessionTurnResult{}, ErrInvalid
	}
	feedback, err := ParseFailureFeedback(request.Feedback)
	if err != nil {
		return ApplySessionTurnResult{}, err
	}
	// Message is required unless feedback alone seeds the revise turn (D14).
	if request.Message == "" && feedback != nil {
		request.Message = FormatRevisionMessage(feedback)
	}
	if request.Message == "" || utf8.RuneCountInString(request.Message) > MaxTurnMessageRunes {
		return ApplySessionTurnResult{}, ErrInvalid
	}

	// Session-scoped try-lock: concurrent turn/close returns busy immediately.
	lock, err := s.locker.TryLock(ctx, request.WorkspaceID, request.SessionID)
	if err != nil {
		return ApplySessionTurnResult{}, err
	}
	defer func() { _ = lock.Unlock(context.WithoutCancel(ctx)) }()

	session, err := s.sessions.GetSession(ctx, request.WorkspaceID, request.SessionID)
	if err != nil {
		return ApplySessionTurnResult{}, err
	}
	if session.Status == SessionStatusClosed {
		return ApplySessionTurnResult{}, ErrSessionClosed
	}

	// Claim lock version N→N+1 (old clients omit expected → use current).
	session, err = s.sessions.ClaimSessionLockVersion(
		ctx, request.WorkspaceID, request.SessionID, request.ExpectedSessionLockVersion,
	)
	if err != nil {
		return ApplySessionTurnResult{}, err
	}
	claimedVersion := session.LockVersion // N+1

	// Mid-session model revalidation (D2) — outside DB tx.
	if _, err := s.gate.Resolve(ctx, session.WorkspaceID, session.AgentID, request.RequestModelConfigID); err != nil {
		return ApplySessionTurnResult{}, err
	}

	historyTurns, err := s.sessions.ListTurns(ctx, session.WorkspaceID, session.ID)
	if err != nil {
		return ApplySessionTurnResult{}, fmt.Errorf("list session turns: %w", err)
	}
	history := historyForModel(historyTurns)

	var prior *PriorDraft
	if session.WorkflowID != nil && *session.WorkflowID != "" {
		if s.drafts == nil {
			return ApplySessionTurnResult{}, errors.New("draft reader is required for multi-turn revision")
		}
		draft, draftErr := s.drafts.GetDraft(ctx, session.WorkspaceID, *session.WorkflowID)
		if draftErr != nil {
			return ApplySessionTurnResult{}, fmt.Errorf("load prior draft: %w", draftErr)
		}
		var graph domain.WorkflowGraphDraft
		if len(draft.Graph) > 0 {
			if unmarshalErr := json.Unmarshal(draft.Graph, &graph); unmarshalErr != nil {
				return ApplySessionTurnResult{}, fmt.Errorf("decode prior draft graph: %w", unmarshalErr)
			}
		}
		prior = &PriorDraft{
			WorkflowID:           *session.WorkflowID,
			DraftID:              draft.ID,
			DraftVersion:         draft.DraftVersion,
			LockVersion:          draft.LockVersion,
			Graph:                graph,
			ExpectedDraftVersion: draft.DraftVersion,
			ExpectedLockVersion:  draft.LockVersion,
		}
	}

	turnIndex, err := s.sessions.NextTurnIndex(ctx, session.WorkspaceID, session.ID)
	if err != nil {
		return ApplySessionTurnResult{}, fmt.Errorf("next turn index: %w", err)
	}
	turnID, err := s.nextID()
	if err != nil {
		return ApplySessionTurnResult{}, fmt.Errorf("generate turn id: %w", err)
	}
	generationID, err := s.nextID()
	if err != nil {
		return ApplySessionTurnResult{}, fmt.Errorf("generate generation id: %w", err)
	}
	if !validUUID(turnID) || !validUUID(generationID) {
		return ApplySessionTurnResult{}, errors.New("id generator returned invalid UUID")
	}

	// Model + guard + draft write (draft CAS lives in TurnService; long model call
	// stays outside a DB transaction while the advisory lock is held).
	result, err := s.turns.ApplyTurn(ctx, ApplyTurnRequest{
		WorkspaceID:          session.WorkspaceID,
		AgentID:              session.AgentID,
		RequestModelConfigID: request.RequestModelConfigID,
		Message:              request.Message,
		CreatedBy:            request.CreatedBy,
		Prior:                prior,
		History:              history,
		SessionID:            session.ID,
		GenerationID:         generationID,
		TraceID:              strings.TrimSpace(request.TraceID),
		Feedback:             feedback,
	})

	now := s.now()
	if err != nil {
		turn := GenerateTurn{
			ID:           turnID,
			WorkspaceID:  session.WorkspaceID,
			SessionID:    session.ID,
			TurnIndex:    turnIndex,
			UserMessage:  request.Message,
			GenerationID: generationID,
			CreatedAt:    now,
			PromptID:     result.Audit.PromptID,
			PromptHash:   result.Audit.PromptHash,
		}
		var guardErr *GuardError
		if errors.As(err, &guardErr) {
			turn.Status = TurnStatusGuardRejected
			turn.GuardOK = false
			turn.GuardReport = guardErr.Report
			turn.ErrorCode = CodeGuardRejected
			turn.AssistantMessage = "生成结果未通过图校验，已保留上一轮合法草稿。"
			if _, createErr := s.sessions.CreateTurn(ctx, turn); createErr == nil {
				_, _ = s.sessions.AdvanceSessionLockVersion(ctx, session.WorkspaceID, session.ID, claimedVersion)
			}
			return ApplySessionTurnResult{
				SessionID:     session.ID,
				TurnID:        turnID,
				GenerationID:  generationID,
				GuardReport:   guardErr.Report,
				Audit:         result.Audit,
				AgentID:       result.AgentID,
				ModelConfigID: result.ModelConfigID,
				GeneratedBy:   GeneratedByV2,
				TraceID:       strings.TrimSpace(request.TraceID),
			}, err
		}
		// Model missing / other: do not write draft; record failed turn when not model-gate only before LLM.
		if errors.Is(err, ErrAgentModelRequired) || errors.Is(err, ErrModelConfigBypassRejected) {
			return ApplySessionTurnResult{}, err
		}
		turn.Status = TurnStatusFailed
		turn.GuardOK = false
		turn.ErrorCode = stableFailedTurnCode(err)
		turn.AssistantMessage = "本轮生成失败，未覆盖草稿。"
		if _, createErr := s.sessions.CreateTurn(ctx, turn); createErr == nil {
			_, _ = s.sessions.AdvanceSessionLockVersion(ctx, session.WorkspaceID, session.ID, claimedVersion)
		}
		// Prefer typed TurnFailure when available.
		if tf, ok := AsTurnFailure(err); ok {
			return ApplySessionTurnResult{
				SessionID: session.ID, TurnID: turnID, GenerationID: generationID,
				TraceID: strings.TrimSpace(request.TraceID),
			}, tf
		}
		return ApplySessionTurnResult{
			SessionID: session.ID, TurnID: turnID, GenerationID: generationID,
			TraceID: strings.TrimSpace(request.TraceID),
		}, err
	}

	// Short success unit: bind first workflow (if needed) + succeeded turn, then
	// advance lock version. Draft already committed by ApplyTurn via CAS; a later
	// failure here surfaces as DRAFT_PERSIST without claiming a false history.
	if session.WorkflowID == nil || *session.WorkflowID == "" {
		if _, setErr := s.sessions.SetSessionWorkflow(ctx, session.WorkspaceID, session.ID, result.Workflow.CapabilityID); setErr != nil {
			return ApplySessionTurnResult{}, NewTurnFailure(CodeDraftPersistFailed, setErr)
		}
	}

	draftVersion := result.Draft.DraftVersion
	assistant := fmt.Sprintf("已根据意图更新流程草稿（draftVersion=%d）。", draftVersion)
	turn := GenerateTurn{
		ID:               turnID,
		WorkspaceID:      session.WorkspaceID,
		SessionID:        session.ID,
		TurnIndex:        turnIndex,
		UserMessage:      request.Message,
		AssistantMessage: assistant,
		GenerationID:     generationID,
		GuardOK:          true,
		GuardReport:      result.GuardReport,
		DraftVersion:     &draftVersion,
		Status:           TurnStatusSucceeded,
		PromptID:         result.Audit.PromptID,
		PromptHash:       result.Audit.PromptHash,
		CreatedAt:        now,
	}
	if _, createErr := s.sessions.CreateTurn(ctx, turn); createErr != nil {
		return ApplySessionTurnResult{}, NewTurnFailure(CodeDraftPersistFailed, createErr)
	}
	if _, advErr := s.sessions.AdvanceSessionLockVersion(ctx, session.WorkspaceID, session.ID, claimedVersion); advErr != nil {
		// Turn is durable; version advance failure is non-fatal for read recovery via GET.
		_ = advErr
	}

	selected := selectedToolIDsFromGraph(result.Graph)
	return ApplySessionTurnResult{
		SessionID:        session.ID,
		TurnID:           turnID,
		GenerationID:     generationID,
		Workflow:         result.Workflow,
		Draft:            result.Draft,
		Graph:            result.Graph,
		AssistantMessage: assistant,
		AvailableToolIDs: nil, // filled by transport if needed
		SelectedToolIDs:  selected,
		Confidence:       90,
		GuardReport:      result.GuardReport,
		DraftVersion:     draftVersion,
		Audit:            result.Audit,
		AgentID:          result.AgentID,
		ModelConfigID:    result.ModelConfigID,
		GeneratedBy:      GeneratedByV2,
		TraceID:          strings.TrimSpace(request.TraceID),
		NodeExplanations: explanationsFromGraph(result.Graph),
		ReasoningSteps: []ReasoningStep{
			{ID: "context", Label: "组装多轮上下文", Status: "COMPLETED", Detail: "System Prompt + Agent + Tool catalog + 当前图 + 历史轮次"},
			{ID: "model", Label: "模型生成候选图", Status: "COMPLETED", Detail: "PlatformChatModel → workflow.graph.v1"},
			{ID: "guard", Label: "确定性 Guard", Status: "COMPLETED", Detail: "catalog toolId / D8 / Start-End"},
			{ID: "draft", Label: "写入正式 Draft", Status: "COMPLETED", Detail: fmt.Sprintf("draftVersion=%d generatedBy=%s", draftVersion, GeneratedByV2)},
		},
	}, nil
}

// GetSessionDetail returns session metadata, turns, and optional draft summary.
type SessionDetail struct {
	Session      GenerateSession
	Turns        []GenerateTurn
	Workflow     *workflow.Workflow
	Draft        *workflow.Draft
	DraftVersion *int64
}

// GetSession loads session + turns + current draft summary when bound.
func (s *SessionService) GetSession(ctx context.Context, workspaceID, sessionID string) (SessionDetail, error) {
	if s == nil {
		return SessionDetail{}, errors.New("session service is nil")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	sessionID = strings.TrimSpace(sessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) {
		return SessionDetail{}, ErrInvalid
	}
	session, err := s.sessions.GetSession(ctx, workspaceID, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	turns, err := s.sessions.ListTurns(ctx, workspaceID, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	detail := SessionDetail{Session: session, Turns: turns}
	if session.WorkflowID != nil && *session.WorkflowID != "" && s.drafts != nil {
		wf, wfErr := s.drafts.Get(ctx, workspaceID, *session.WorkflowID)
		if wfErr == nil {
			detail.Workflow = &wf
		}
		draft, draftErr := s.drafts.GetDraft(ctx, workspaceID, *session.WorkflowID)
		if draftErr == nil {
			detail.Draft = &draft
			v := draft.DraftVersion
			detail.DraftVersion = &v
		}
	}
	return detail, nil
}

// CloseSessionRequest closes an OPEN generate session.
type CloseSessionRequest struct {
	WorkspaceID                string
	SessionID                  string
	ExpectedSessionLockVersion *int64
}

// CloseSession marks session CLOSED; further turns return ErrSessionClosed.
// Acquires the same session advisory lock so close vs in-flight turn returns busy.
func (s *SessionService) CloseSession(ctx context.Context, workspaceID, sessionID string) (GenerateSession, error) {
	return s.CloseSessionWith(ctx, CloseSessionRequest{WorkspaceID: workspaceID, SessionID: sessionID})
}

// CloseSessionWith supports optional expectedSessionLockVersion (ZKL-56).
func (s *SessionService) CloseSessionWith(ctx context.Context, request CloseSessionRequest) (GenerateSession, error) {
	if s == nil {
		return GenerateSession{}, errors.New("session service is nil")
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	sessionID := strings.TrimSpace(request.SessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) {
		return GenerateSession{}, ErrInvalid
	}
	lock, err := s.locker.TryLock(ctx, workspaceID, sessionID)
	if err != nil {
		return GenerateSession{}, err
	}
	defer func() { _ = lock.Unlock(context.WithoutCancel(ctx)) }()

	session, err := s.sessions.GetSession(ctx, workspaceID, sessionID)
	if err != nil {
		return GenerateSession{}, err
	}
	if session.Status == SessionStatusClosed {
		return session, nil
	}
	if request.ExpectedSessionLockVersion != nil &&
		session.LockVersion != *request.ExpectedSessionLockVersion {
		return GenerateSession{}, ErrSessionVersionConflict
	}
	return s.sessions.CloseSession(ctx, workspaceID, sessionID, s.now())
}

// stableFailedTurnCode maps a generate failure to a durable turn error_code.
func stableFailedTurnCode(err error) string {
	if tf, ok := AsTurnFailure(err); ok && tf != nil {
		return tf.Code
	}
	switch {
	case errors.Is(err, ErrOutputInvalid):
		return CodeOutputInvalid
	case errors.Is(err, ErrModelTimeout):
		return CodeModelTimeout
	case errors.Is(err, ErrModelUnavailable):
		return CodeModelUnavailable
	case errors.Is(err, ErrDraftConflict):
		return CodeDraftConflict
	case errors.Is(err, ErrDraftPersistFailed):
		return CodeDraftPersistFailed
	default:
		// Keep historical generic code for unclassified failures (GET → UNKNOWN).
		return CodeHistoricalFailed
	}
}

func historyForModel(turns []GenerateTurn) []TurnHistoryItem {
	items := make([]TurnHistoryItem, 0, len(turns)*2)
	for _, turn := range turns {
		items = append(items, TurnHistoryItem{Role: "user", Content: turn.UserMessage})
		if strings.TrimSpace(turn.AssistantMessage) != "" {
			items = append(items, TurnHistoryItem{Role: "assistant", Content: turn.AssistantMessage})
		}
	}
	return items
}

func selectedToolIDsFromGraph(graph domain.WorkflowGraphDraft) []string {
	ids := make([]string, 0)
	seen := map[string]struct{}{}
	for _, node := range graph.Nodes {
		if !strings.EqualFold(node.Type, "Tool") || node.Data == nil {
			continue
		}
		raw, ok := node.Data["toolId"]
		if !ok {
			continue
		}
		id, ok := raw.(string)
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func explanationsFromGraph(graph domain.WorkflowGraphDraft) []NodeExplanation {
	out := make([]NodeExplanation, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		reason := ""
		if node.UI != nil {
			if r, ok := node.UI["reason"].(string); ok {
				reason = r
			}
		}
		out = append(out, NodeExplanation{NodeID: node.ID, Title: node.Label, Reason: reason})
	}
	return out
}
