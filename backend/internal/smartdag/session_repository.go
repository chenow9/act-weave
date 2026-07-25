package smartdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// SQLSessionRepository persists generate sessions/turns via Postgres (migration 000059).
type SQLSessionRepository struct {
	db *sql.DB
}

// NewSQLSessionRepository constructs a SQL-backed session store.
func NewSQLSessionRepository(db *sql.DB) (*SQLSessionRepository, error) {
	if db == nil {
		return nil, errors.New("session repository database is required")
	}
	return &SQLSessionRepository{db: db}, nil
}

// CreateSession inserts a workflow_generate_sessions row.
func (r *SQLSessionRepository) CreateSession(ctx context.Context, session GenerateSession) (GenerateSession, error) {
	if r == nil || r.db == nil {
		return GenerateSession{}, errors.New("session repository is not configured")
	}
	constraints := session.Constraints
	if len(constraints) == 0 {
		constraints = json.RawMessage(`{}`)
	}
	var workflowID any
	if session.WorkflowID != nil && strings.TrimSpace(*session.WorkflowID) != "" {
		workflowID = *session.WorkflowID
	}
	var promptID, promptHash any
	if session.PromptID != "" {
		promptID = session.PromptID
	}
	if session.PromptHash != "" {
		promptHash = session.PromptHash
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO workflow_generate_sessions (
			id, workspace_id, agent_id, workflow_id, model_config_id, status,
			prompt_id, prompt_hash, constraints, created_by, created_at, updated_at, lock_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, workspace_id, agent_id, workflow_id, model_config_id, status,
			prompt_id, prompt_hash, constraints, created_by, created_at, updated_at, closed_at, lock_version
	`, session.ID, session.WorkspaceID, session.AgentID, workflowID, session.ModelConfigID, SessionStatusOpen,
		promptID, promptHash, []byte(constraints), session.CreatedBy, session.CreatedAt, session.UpdatedAt, session.LockVersion)
	return scanSession(row)
}

// GetSession loads a session scoped by workspace.
func (r *SQLSessionRepository) GetSession(ctx context.Context, workspaceID, sessionID string) (GenerateSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, agent_id, workflow_id, model_config_id, status,
			prompt_id, prompt_hash, constraints, created_by, created_at, updated_at, closed_at, lock_version
		FROM workflow_generate_sessions
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sessionID)
	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GenerateSession{}, ErrSessionNotFound
		}
		return GenerateSession{}, err
	}
	return session, nil
}

// CloseSession sets status CLOSED.
func (r *SQLSessionRepository) CloseSession(ctx context.Context, workspaceID, sessionID string, closedAt time.Time) (GenerateSession, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE workflow_generate_sessions
		SET status='CLOSED', closed_at=$3, updated_at=$3, lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='OPEN'
		RETURNING id, workspace_id, agent_id, workflow_id, model_config_id, status,
			prompt_id, prompt_hash, constraints, created_by, created_at, updated_at, closed_at, lock_version
	`, workspaceID, sessionID, closedAt)
	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Already closed or missing — re-read.
			existing, getErr := r.GetSession(ctx, workspaceID, sessionID)
			if getErr != nil {
				return GenerateSession{}, getErr
			}
			if existing.Status == SessionStatusClosed {
				return existing, nil
			}
			return GenerateSession{}, ErrSessionNotFound
		}
		return GenerateSession{}, mapSessionWrite("close generate session", err)
	}
	return session, nil
}

// SetSessionWorkflow binds workflow_id after first successful turn.
func (r *SQLSessionRepository) SetSessionWorkflow(ctx context.Context, workspaceID, sessionID, workflowID string) (GenerateSession, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE workflow_generate_sessions
		SET workflow_id=$3, updated_at=CURRENT_TIMESTAMP, lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2
		RETURNING id, workspace_id, agent_id, workflow_id, model_config_id, status,
			prompt_id, prompt_hash, constraints, created_by, created_at, updated_at, closed_at, lock_version
	`, workspaceID, sessionID, workflowID)
	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GenerateSession{}, ErrSessionNotFound
		}
		return GenerateSession{}, mapSessionWrite("set session workflow", err)
	}
	return session, nil
}

// CreateTurn inserts a turn row.
func (r *SQLSessionRepository) CreateTurn(ctx context.Context, turn GenerateTurn) (GenerateTurn, error) {
	report, err := json.Marshal(turn.GuardReport)
	if err != nil {
		return GenerateTurn{}, fmt.Errorf("marshal guard report: %w", err)
	}
	var draftVersion any
	if turn.DraftVersion != nil {
		draftVersion = *turn.DraftVersion
	}
	var promptID, promptHash, errorCode, assistant any
	if turn.PromptID != "" {
		promptID = turn.PromptID
	}
	if turn.PromptHash != "" {
		promptHash = turn.PromptHash
	}
	if turn.ErrorCode != "" {
		errorCode = turn.ErrorCode
	}
	if turn.AssistantMessage != "" {
		assistant = turn.AssistantMessage
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO workflow_generate_turns (
			id, workspace_id, session_id, turn_index, user_message, assistant_message,
			generation_id, guard_ok, guard_report, draft_version, status, error_code,
			prompt_id, prompt_hash, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, workspace_id, session_id, turn_index, user_message, assistant_message,
			generation_id, guard_ok, guard_report, draft_version, status, error_code,
			prompt_id, prompt_hash, created_at
	`, turn.ID, turn.WorkspaceID, turn.SessionID, turn.TurnIndex, turn.UserMessage, assistant,
		turn.GenerationID, turn.GuardOK, report, draftVersion, turn.Status, errorCode,
		promptID, promptHash, turn.CreatedAt)
	return scanTurn(row)
}

// ListTurns returns turns ordered by turn_index.
func (r *SQLSessionRepository) ListTurns(ctx context.Context, workspaceID, sessionID string) ([]GenerateTurn, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, workspace_id, session_id, turn_index, user_message, assistant_message,
			generation_id, guard_ok, guard_report, draft_version, status, error_code,
			prompt_id, prompt_hash, created_at
		FROM workflow_generate_turns
		WHERE workspace_id=$1 AND session_id=$2
		ORDER BY turn_index ASC, id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list generate turns: %w", err)
	}
	defer rows.Close()
	values := make([]GenerateTurn, 0)
	for rows.Next() {
		value, scanErr := scanTurn(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// NextTurnIndex returns max(turn_index)+1 for the session.
func (r *SQLSessionRepository) NextTurnIndex(ctx context.Context, workspaceID, sessionID string) (int, error) {
	var max sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT MAX(turn_index) FROM workflow_generate_turns
		WHERE workspace_id=$1 AND session_id=$2
	`, workspaceID, sessionID).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("next turn index: %w", err)
	}
	if !max.Valid {
		return 1, nil
	}
	return int(max.Int64) + 1, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSession(row scannable) (GenerateSession, error) {
	var (
		session                          GenerateSession
		workflowID, promptID, promptHash sql.NullString
		closedAt                         sql.NullTime
		constraints                      []byte
	)
	err := row.Scan(
		&session.ID, &session.WorkspaceID, &session.AgentID, &workflowID, &session.ModelConfigID, &session.Status,
		&promptID, &promptHash, &constraints, &session.CreatedBy, &session.CreatedAt, &session.UpdatedAt,
		&closedAt, &session.LockVersion,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GenerateSession{}, ErrSessionNotFound
		}
		return GenerateSession{}, fmt.Errorf("scan generate session: %w", err)
	}
	if workflowID.Valid {
		v := workflowID.String
		session.WorkflowID = &v
	}
	if promptID.Valid {
		session.PromptID = promptID.String
	}
	if promptHash.Valid {
		session.PromptHash = promptHash.String
	}
	if closedAt.Valid {
		t := closedAt.Time.UTC()
		session.ClosedAt = &t
	}
	if len(constraints) == 0 {
		session.Constraints = json.RawMessage(`{}`)
	} else {
		session.Constraints = json.RawMessage(constraints)
	}
	session.CreatedAt = session.CreatedAt.UTC()
	session.UpdatedAt = session.UpdatedAt.UTC()
	return session, nil
}

func scanTurn(row scannable) (GenerateTurn, error) {
	var (
		turn                                       GenerateTurn
		assistant, errorCode, promptID, promptHash sql.NullString
		draftVersion                               sql.NullInt64
		report                                     []byte
	)
	err := row.Scan(
		&turn.ID, &turn.WorkspaceID, &turn.SessionID, &turn.TurnIndex, &turn.UserMessage, &assistant,
		&turn.GenerationID, &turn.GuardOK, &report, &draftVersion, &turn.Status, &errorCode,
		&promptID, &promptHash, &turn.CreatedAt,
	)
	if err != nil {
		return GenerateTurn{}, fmt.Errorf("scan generate turn: %w", err)
	}
	if assistant.Valid {
		turn.AssistantMessage = assistant.String
	}
	if errorCode.Valid {
		turn.ErrorCode = errorCode.String
	}
	if promptID.Valid {
		turn.PromptID = promptID.String
	}
	if promptHash.Valid {
		turn.PromptHash = promptHash.String
	}
	if draftVersion.Valid {
		v := draftVersion.Int64
		turn.DraftVersion = &v
	}
	if len(report) > 0 {
		_ = json.Unmarshal(report, &turn.GuardReport)
	}
	turn.CreatedAt = turn.CreatedAt.UTC()
	return turn, nil
}

func mapSessionWrite(op string, err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23503": // foreign_key_violation
			return fmt.Errorf("%s: %w", op, ErrInvalid)
		case "23505": // unique_violation
			return fmt.Errorf("%s: %w", op, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
