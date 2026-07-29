package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/principal"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("chat resource not found")
	ErrConflict = errors.New("chat resource conflict")
	ErrInvalid  = errors.New("invalid chat resource")
)

const sessionColumns = `
	cs.id,cs.workspace_id,cs.agent_id,cs.title,cs.status,cs.created_by,
	cs.latest_run_id,cs.pending_confirmation_id,cs.created_at,cs.updated_at,
	cs.lock_version,cs.actor_type,cs.actor_id,cs.subject_type,cs.subject_id,
	cs.client_id,cs.ownership_mode,cs.ownership_policy_version
`

const messageColumns = `
	cm.id,cm.workspace_id,cm.session_id,cm.role,cm.content,cm.content_object_id,
	cm.content_sha256,cm.content_length,cm.status,cm.run_id,cm.confirmation_id,
	cm.created_by,cm.created_at,cm.actor_type,cm.actor_id,cm.subject_type,
	cm.subject_id,cm.client_id,cm.ownership_mode,cm.ownership_policy_version
`

const sessionVisibilityPredicate = `
	cs.workspace_id=$1 AND cs.actor_type=$2 AND cs.actor_id=$3
	AND cs.client_id IS NOT DISTINCT FROM NULLIF($4,'')::UUID
	AND (
		(cs.ownership_mode='SUBJECT_OWNED'
		 AND cs.subject_type IS NOT DISTINCT FROM NULLIF($5,'')
		 AND cs.subject_id IS NOT DISTINCT FROM NULLIF($6,'')::UUID)
		OR
		(cs.ownership_mode='POLICY_SHARED' AND ($6='' OR $7))
	)
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("chat repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) CreateSession(
	ctx context.Context,
	input CreateSessionInput,
) (Session, error) {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.AgentID, input.CreatedBy = strings.TrimSpace(input.AgentID), strings.TrimSpace(input.CreatedBy)
	input.Title = strings.TrimSpace(input.Title)
	if !validUUID(input.ID) || !validUUID(input.WorkspaceID) || !validUUID(input.AgentID) {
		return Session{}, ErrInvalid
	}
	ownership, err := createSessionOwnership(input)
	if err != nil {
		return Session{}, err
	}
	actorType, actorID, subjectType, subjectID, clientID, mode, policyVersion := ownershipArguments(ownership)
	createdBy := ""
	if ownership.Identity.Actor.Type == principal.TypeUser {
		createdBy = ownership.Identity.Actor.ID
	}
	value, err := scanSession(r.db.QueryRowContext(ctx, `
		INSERT INTO chat_sessions AS cs(
		 id,workspace_id,agent_id,title,created_by,actor_type,actor_id,
		 subject_type,subject_id,client_id,ownership_mode,ownership_policy_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+sessionColumns,
		input.ID, input.WorkspaceID, input.AgentID, input.Title, nullableString(createdBy),
		actorType, actorID, nullableString(subjectType), nullableString(subjectID),
		nullableString(clientID), mode, policyVersion))
	if err != nil {
		return Session{}, mapWrite("create chat session", err)
	}
	return value, nil
}

func (r *Repository) ListSessions(
	ctx context.Context,
	workspaceID, createdBy string,
	limit int,
) ([]Session, error) {
	access, err := NewUserAccess(workspaceID, createdBy)
	if err != nil {
		return nil, ErrInvalid
	}
	return r.ListSessionsForPrincipal(ctx, access, limit)
}

// ListSessionsForPrincipal is the required entry point for machine and
// delegated callers. It applies ownership in SQL so callers cannot enumerate
// hidden Session IDs and then bypass a Handler-level comparison.
func (r *Repository) ListSessionsForPrincipal(
	ctx context.Context,
	access Access,
	limit int,
) ([]Session, error) {
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil {
		return nil, ErrInvalid
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+sessionColumns+` FROM chat_sessions cs
		WHERE `+sessionVisibilityPredicate+`
		ORDER BY cs.updated_at DESC,cs.id DESC
		LIMIT $8
	`, access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, limit)
	if err != nil {
		return nil, fmt.Errorf("list chat sessions: %w", err)
	}
	defer rows.Close()
	values := make([]Session, 0)
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chat session: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) GetSessionForPrincipal(
	ctx context.Context,
	access Access,
	sessionID string,
) (Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil || !validUUID(sessionID) {
		return Session{}, ErrInvalid
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	value, err := scanSession(r.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+` FROM chat_sessions cs
		WHERE `+sessionVisibilityPredicate+` AND cs.id=$8
	`, access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, sessionID))
	return value, mapRead("get visible chat session", err)
}

func (r *Repository) GetSession(
	ctx context.Context,
	workspaceID, sessionID string,
) (Session, error) {
	workspaceID, sessionID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) {
		return Session{}, ErrInvalid
	}
	value, err := scanSession(r.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+` FROM chat_sessions cs
		WHERE cs.workspace_id=$1 AND cs.id=$2
	`, workspaceID, sessionID))
	return value, mapRead("get chat session", err)
}

func (r *Repository) ArchiveSession(
	ctx context.Context,
	workspaceID, sessionID string,
	expectedLockVersion int64,
) (Session, error) {
	workspaceID, sessionID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) || expectedLockVersion <= 0 {
		return Session{}, ErrInvalid
	}
	value, err := scanSession(r.db.QueryRowContext(ctx, `
		UPDATE chat_sessions cs SET status='ARCHIVED',updated_at=clock_timestamp(),
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='ACTIVE' AND lock_version=$3
		RETURNING `+sessionColumns,
		workspaceID, sessionID, expectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, r.classifySession(ctx, workspaceID, sessionID)
	}
	if err != nil {
		return Session{}, mapWrite("archive chat session", err)
	}
	return value, nil
}

func (r *Repository) ArchiveSessionForPrincipal(
	ctx context.Context,
	access Access,
	sessionID string,
	expectedLockVersion int64,
) (Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil || !validUUID(sessionID) || expectedLockVersion <= 0 {
		return Session{}, ErrInvalid
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	value, err := scanSession(r.db.QueryRowContext(ctx, `
		UPDATE chat_sessions cs SET status='ARCHIVED',updated_at=clock_timestamp(),
		 lock_version=lock_version+1
		WHERE `+sessionVisibilityPredicate+`
		 AND cs.id=$8 AND cs.status='ACTIVE' AND cs.lock_version=$9
		RETURNING `+sessionColumns,
		access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, sessionID, expectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, r.classifyVisibleSession(ctx, access, sessionID)
	}
	if err != nil {
		return Session{}, mapWrite("archive visible chat session", err)
	}
	return value, nil
}

func (r *Repository) GetMessage(
	ctx context.Context,
	workspaceID, messageID string,
) (Message, error) {
	workspaceID, messageID = strings.TrimSpace(workspaceID), strings.TrimSpace(messageID)
	if !validUUID(workspaceID) || !validUUID(messageID) {
		return Message{}, ErrInvalid
	}
	value, err := scanMessage(r.db.QueryRowContext(ctx, `
		SELECT `+messageColumns+` FROM chat_messages cm
		WHERE cm.workspace_id=$1 AND cm.id=$2
	`, workspaceID, messageID))
	return value, mapReadMessage("get chat message", err)
}

func (r *Repository) ListMessages(
	ctx context.Context,
	workspaceID, sessionID string,
) ([]Message, error) {
	workspaceID, sessionID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+messageColumns+` FROM chat_messages cm
		WHERE cm.workspace_id=$1 AND cm.session_id=$2
		ORDER BY cm.created_at,cm.id
	`, workspaceID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()
	values := make([]Message, 0)
	for rows.Next() {
		value, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chat message: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) ListMessagesForPrincipal(
	ctx context.Context,
	access Access,
	sessionID string,
) ([]Message, error) {
	sessionID = strings.TrimSpace(sessionID)
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil || !validUUID(sessionID) {
		return nil, ErrInvalid
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+messageColumns+` FROM chat_messages cm
		JOIN chat_sessions cs ON cs.workspace_id=cm.workspace_id AND cs.id=cm.session_id
		WHERE `+sessionVisibilityPredicate+` AND cs.id=$8
		ORDER BY cm.created_at,cm.id
	`, access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list visible chat messages: %w", err)
	}
	defer rows.Close()
	values := make([]Message, 0)
	for rows.Next() {
		value, scanErr := scanMessage(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan visible chat message: %w", scanErr)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		if _, err := r.GetSessionForPrincipal(ctx, access, sessionID); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// ListMessagesReversePage returns up to limit messages newest-first for a
// workspace-scoped session using a stable (created_at, id) reverse cursor.
// Does not decrypt permanent object bodies. Used by runtime assembly after the
// run is already authorized for the session.
func (r *Repository) ListMessagesReversePage(
	ctx context.Context,
	workspaceID, sessionID string,
	limit int,
	cursor *MessagePageCursor,
) (MessagePage, error) {
	workspaceID, sessionID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID)
	if !validUUID(workspaceID) || !validUUID(sessionID) {
		return MessagePage{}, ErrInvalid
	}
	if limit < 1 || limit > 500 {
		return MessagePage{}, ErrInvalid
	}
	args := []any{workspaceID, sessionID}
	cursorClause := ""
	if cursor != nil && !cursor.CreatedAt.IsZero() && validUUID(cursor.ID) {
		args = append(args, cursor.CreatedAt.UTC(), cursor.ID)
		cursorClause = fmt.Sprintf(
			` AND (cm.created_at, cm.id) < ($%d::timestamptz, $%d::uuid)`,
			len(args)-1, len(args),
		)
	}
	args = append(args, limit+1)
	limitArg := len(args)
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+messageColumns+` FROM chat_messages cm
		WHERE cm.workspace_id=$1 AND cm.session_id=$2`+cursorClause+`
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT $`+fmt.Sprintf("%d", limitArg)+`
	`, args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("list reverse page chat messages: %w", err)
	}
	defer rows.Close()
	values := make([]Message, 0, limit)
	for rows.Next() {
		value, scanErr := scanMessage(rows)
		if scanErr != nil {
			return MessagePage{}, fmt.Errorf("scan reverse page chat message: %w", scanErr)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{HasMore: len(values) > limit}
	if page.HasMore {
		values = values[:limit]
	}
	page.Messages = values
	if page.HasMore && len(values) > 0 {
		last := values[len(values)-1]
		page.NextCursor = &MessagePageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// ListMessagesForPrincipalReversePage returns up to limit messages newest-first
// for an authorized session, using a stable (created_at, id) reverse cursor.
// limit is a resource bound only; it does not change semantic selection.
// Does not decrypt permanent object bodies.
func (r *Repository) ListMessagesForPrincipalReversePage(
	ctx context.Context,
	access Access,
	sessionID string,
	limit int,
	cursor *MessagePageCursor,
) (MessagePage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil || !validUUID(sessionID) {
		return MessagePage{}, ErrInvalid
	}
	if limit < 1 || limit > 500 {
		return MessagePage{}, ErrInvalid
	}
	// Ensure session is visible before paging (IDOR protection).
	if _, err := r.GetSessionForPrincipal(ctx, access, sessionID); err != nil {
		return MessagePage{}, err
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	args := []any{
		access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, sessionID,
	}
	cursorClause := ""
	if cursor != nil && !cursor.CreatedAt.IsZero() && validUUID(cursor.ID) {
		args = append(args, cursor.CreatedAt.UTC(), cursor.ID)
		// Fetch strictly older than cursor in reverse order.
		cursorClause = fmt.Sprintf(
			` AND (cm.created_at, cm.id) < ($%d::timestamptz, $%d::uuid)`,
			len(args)-1, len(args),
		)
	}
	args = append(args, limit+1)
	limitArg := len(args)
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+messageColumns+` FROM chat_messages cm
		JOIN chat_sessions cs ON cs.workspace_id=cm.workspace_id AND cs.id=cm.session_id
		WHERE `+sessionVisibilityPredicate+` AND cs.id=$8`+cursorClause+`
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT $`+fmt.Sprintf("%d", limitArg)+`
	`, args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("list reverse page chat messages: %w", err)
	}
	defer rows.Close()
	values := make([]Message, 0, limit)
	for rows.Next() {
		value, scanErr := scanMessage(rows)
		if scanErr != nil {
			return MessagePage{}, fmt.Errorf("scan reverse page chat message: %w", scanErr)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{HasMore: len(values) > limit}
	if page.HasMore {
		values = values[:limit]
	}
	page.Messages = values
	if page.HasMore && len(values) > 0 {
		last := values[len(values)-1]
		page.NextCursor = &MessagePageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// CountMessagesForPrincipal returns the message count for a visible session.
func (r *Repository) CountMessagesForPrincipal(
	ctx context.Context,
	access Access,
	sessionID string,
) (int64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if access.Validate(access.Identity.Actor.WorkspaceID) != nil || !validUUID(sessionID) {
		return 0, ErrInvalid
	}
	if _, err := r.GetSessionForPrincipal(ctx, access, sessionID); err != nil {
		return 0, err
	}
	actorType, actorID, subjectType, subjectID, clientID, shared := accessArguments(access)
	var count int64
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM chat_messages cm
		JOIN chat_sessions cs ON cs.workspace_id=cm.workspace_id AND cs.id=cm.session_id
		WHERE `+sessionVisibilityPredicate+` AND cs.id=$8
	`, access.Identity.Actor.WorkspaceID, actorType, actorID, clientID,
		subjectType, subjectID, shared, sessionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count visible chat messages: %w", err)
	}
	return count, nil
}

func (r *Repository) insertMessageInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	value Message,
) (Message, error) {
	created, err := scanMessage(tx.QueryRowContext(ctx, `
		INSERT INTO chat_messages AS cm(
		 id,workspace_id,session_id,role,content,content_object_id,content_sha256,
		 content_length,status,run_id,confirmation_id,created_by,actor_type,actor_id,
		 subject_type,subject_id,client_id,ownership_mode,ownership_policy_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING `+messageColumns,
		value.ID, value.WorkspaceID, value.SessionID, value.Role,
		nullableString(value.Content), nullableString(value.ContentObjectID),
		value.ContentSHA256, value.ContentLength, value.Status, nullableString(value.RunID),
		nullableString(value.ConfirmationID), nullableString(value.CreatedBy),
		value.Identity.Actor.Type, value.Identity.Actor.ID,
		nullablePrincipalType(value.Identity.Subject), nullablePrincipalID(value.Identity.Subject),
		nullableString(value.ClientID), value.OwnershipMode, value.PolicyVersion))
	if err != nil {
		return Message{}, mapWrite("insert chat message", err)
	}
	return created, nil
}

func (r *Repository) setLatestRunInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, sessionID, runID string,
	expectedLockVersion int64,
) (Session, error) {
	value, err := scanSession(tx.QueryRowContext(ctx, `
		UPDATE chat_sessions cs SET latest_run_id=$4,updated_at=clock_timestamp(),
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='ACTIVE' AND lock_version=$3
		RETURNING `+sessionColumns,
		workspaceID, sessionID, expectedLockVersion, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrConflict
	}
	if err != nil {
		return Session{}, mapWrite("set chat session latest run", err)
	}
	return value, nil
}

func (r *Repository) updateUserMessageInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, sessionID, messageID, runID, status string,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE chat_messages SET status=$5
		WHERE workspace_id=$1 AND session_id=$2 AND id=$3 AND run_id=$4
		 AND role='USER' AND status='PROCESSING'
	`, workspaceID, sessionID, messageID, runID, status)
	if err != nil {
		return mapWrite("update chat user message status", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read chat message update count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) classifySession(ctx context.Context, workspaceID, sessionID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM chat_sessions WHERE workspace_id=$1 AND id=$2)
	`, workspaceID, sessionID).Scan(&exists); err != nil {
		return fmt.Errorf("classify chat session: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (r *Repository) classifyVisibleSession(ctx context.Context, access Access, sessionID string) error {
	if _, err := r.GetSessionForPrincipal(ctx, access, sessionID); err != nil {
		return err
	}
	return ErrConflict
}

type scanner interface{ Scan(...any) error }

func scanSession(row scanner) (Session, error) {
	var value Session
	var createdBy, latestRunID, pendingConfirmationID sql.NullString
	var actorType, actorID, ownershipMode string
	var subjectType, subjectID, clientID sql.NullString
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.AgentID, &value.Title, &value.Status,
		&createdBy, &latestRunID, &pendingConfirmationID, &value.CreatedAt,
		&value.UpdatedAt, &value.LockVersion, &actorType, &actorID,
		&subjectType, &subjectID, &clientID, &ownershipMode,
		&value.Ownership.PolicyVersion,
	)
	if err != nil {
		return Session{}, err
	}
	value.CreatedBy = createdBy.String
	value.LatestRunID = latestRunID.String
	value.PendingConfirmationID = pendingConfirmationID.String
	identity, err := scannedIdentity(value.WorkspaceID, actorType, actorID, subjectType, subjectID)
	if err != nil {
		return Session{}, err
	}
	value.Ownership.Identity = identity
	value.Ownership.ClientID = clientID.String
	value.Ownership.Mode = OwnershipMode(ownershipMode)
	if value.Ownership.Validate(value.WorkspaceID) != nil {
		return Session{}, ErrInvalid
	}
	return value, nil
}

func scanMessage(row scanner) (Message, error) {
	var value Message
	var content, contentObjectID, runID, confirmationID, createdBy sql.NullString
	var actorType, actorID, ownershipMode string
	var subjectType, subjectID, clientID sql.NullString
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.SessionID, &value.Role, &content,
		&contentObjectID, &value.ContentSHA256, &value.ContentLength, &value.Status, &runID,
		&confirmationID, &createdBy, &value.CreatedAt, &actorType, &actorID,
		&subjectType, &subjectID, &clientID, &ownershipMode, &value.PolicyVersion,
	)
	if err != nil {
		return Message{}, err
	}
	value.Content, value.ContentObjectID = content.String, contentObjectID.String
	value.RunID, value.ConfirmationID = runID.String, confirmationID.String
	value.CreatedBy = createdBy.String
	identity, err := scannedIdentity(value.WorkspaceID, actorType, actorID, subjectType, subjectID)
	if err != nil {
		return Message{}, err
	}
	value.Identity = identity
	value.ClientID = clientID.String
	value.OwnershipMode = OwnershipMode(ownershipMode)
	return value, nil
}

func createSessionOwnership(input CreateSessionInput) (Ownership, error) {
	if input.Ownership == nil {
		return NewUserOwnership(input.WorkspaceID, input.CreatedBy)
	}
	value := *input.Ownership
	if value.Validate(input.WorkspaceID) != nil ||
		(input.CreatedBy != "" &&
			(value.Identity.Actor.Type != principal.TypeUser || input.CreatedBy != value.Identity.Actor.ID)) {
		return Ownership{}, ErrInvalid
	}
	return value, nil
}

func ownershipArguments(value Ownership) (string, string, string, string, string, string, int64) {
	subjectType, subjectID := "", ""
	if value.Identity.Subject != nil {
		subjectType, subjectID = value.Identity.Subject.LegacyPair()
	}
	return string(value.Identity.Actor.Type), value.Identity.Actor.ID, subjectType, subjectID,
		value.ClientID, string(value.Mode), value.PolicyVersion
}

func accessArguments(value Access) (string, string, string, string, string, bool) {
	subjectType, subjectID := "", ""
	if value.Identity.Subject != nil {
		subjectType, subjectID = value.Identity.Subject.LegacyPair()
	}
	return string(value.Identity.Actor.Type), value.Identity.Actor.ID, subjectType, subjectID,
		value.ClientID, value.AllowPolicyShared
}

func scannedIdentity(
	workspaceID, actorType, actorID string,
	subjectType, subjectID sql.NullString,
) (principal.InvocationIdentity, error) {
	actor, err := principal.RefFromLegacy(workspaceID, actorType, actorID)
	if err != nil {
		return principal.InvocationIdentity{}, err
	}
	var subject *principal.Ref
	if subjectType.Valid || subjectID.Valid {
		if !subjectType.Valid || !subjectID.Valid {
			return principal.InvocationIdentity{}, ErrInvalid
		}
		value, refErr := principal.RefFromLegacy(workspaceID, subjectType.String, subjectID.String)
		if refErr != nil {
			return principal.InvocationIdentity{}, refErr
		}
		subject = &value
	}
	return principal.NewInvocationIdentity(actor, subject)
}

func nullablePrincipalType(value *principal.Ref) any {
	if value == nil {
		return nil
	}
	return value.Type
}

func nullablePrincipalID(value *principal.Ref) any {
	if value == nil {
		return nil
	}
	return value.ID
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapReadMessage(operation string, err error) error { return mapRead(operation, err) }

func mapWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505", "40001", "55000":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
