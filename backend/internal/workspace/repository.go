package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound  = errors.New("workspace record not found")
	ErrConflict  = errors.New("workspace record conflict")
	ErrInvalid   = errors.New("invalid workspace record")
	ErrLastOwner = errors.New("workspace must retain an active owner")
)

const workspaceColumns = `
	id,
	slug::TEXT,
	display_name,
	mode,
	status,
	owner_user_id,
	default_agent_id,
	default_model_config_id,
	settings,
	created_by,
	updated_by,
	created_at,
	updated_at,
	lock_version,
	deleted_at
`

const memberColumns = `
	workspace_id,
	user_id,
	role,
	invited_by,
	joined_at,
	disabled_at
`

// CreationHook lets the Agent/configuration domains create defaults in the
// exact transaction that creates a Workspace. It is intentionally an
// interface only: those domain tables and implementations do not exist yet.
type CreationHook interface {
	CreateDefaults(context.Context, *sql.Tx, Workspace) (CreationDefaults, error)
}

type CreationHookFunc func(context.Context, *sql.Tx, Workspace) (CreationDefaults, error)

func (f CreationHookFunc) CreateDefaults(
	ctx context.Context,
	tx *sql.Tx,
	workspace Workspace,
) (CreationDefaults, error) {
	return f(ctx, tx, workspace)
}

// Repository performs row-oriented Workspace persistence and does not own the
// supplied connection pool.
type Repository struct {
	db           *sql.DB
	creationHook CreationHook
}

func NewRepository(db *sql.DB, creationHook ...CreationHook) (*Repository, error) {
	if db == nil {
		return nil, errors.New("workspace repository database is required")
	}
	if len(creationHook) > 1 {
		return nil, errors.New("workspace repository accepts at most one creation hook")
	}
	repository := &Repository{db: db}
	if len(creationHook) == 1 {
		if creationHook[0] == nil {
			return nil, errors.New("workspace creation hook cannot be nil")
		}
		repository.creationHook = creationHook[0]
	}
	return repository, nil
}

// Create atomically inserts the Workspace, its OWNER member, and any defaults
// produced by the optional creation hook.
func (r *Repository) Create(ctx context.Context, input NewWorkspace) (Workspace, error) {
	input = defaultNewWorkspace(input)
	if err := validateNewWorkspace(input); err != nil {
		return Workspace{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, fmt.Errorf("begin workspace transaction: %w", err)
	}
	defer tx.Rollback()

	created, err := scanWorkspace(tx.QueryRowContext(ctx, `
		INSERT INTO workspaces (
			id, slug, display_name, mode, owner_user_id, settings,
			created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING `+workspaceColumns,
		input.ID,
		input.Slug,
		input.DisplayName,
		input.Mode,
		input.OwnerUserID,
		[]byte(input.Settings),
		input.CreatedBy,
	))
	if err != nil {
		return Workspace{}, mapWriteError("create workspace", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_members (
			workspace_id, user_id, role, invited_by
		) VALUES ($1, $2, $3, $4)
	`, created.ID, input.OwnerUserID, RoleOwner, input.CreatedBy); err != nil {
		return Workspace{}, mapWriteError("create workspace owner member", err)
	}

	if r.creationHook != nil {
		defaults, err := r.creationHook.CreateDefaults(ctx, tx, created)
		if err != nil {
			return Workspace{}, fmt.Errorf("create workspace defaults: %w", err)
		}
		if err := validateCreationDefaults(defaults); err != nil {
			return Workspace{}, err
		}
		created, err = scanWorkspace(tx.QueryRowContext(ctx, `
			UPDATE workspaces
			SET default_agent_id = $2,
				default_model_config_id = $3
			WHERE id = $1
			RETURNING `+workspaceColumns,
			created.ID,
			defaults.DefaultAgentID,
			defaults.DefaultModelConfigID,
		))
		if err != nil {
			return Workspace{}, mapWriteError("set workspace defaults", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Workspace{}, mapWriteError("commit workspace transaction", err)
	}
	return created, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID string) (Workspace, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if !validUUID(workspaceID) {
		return Workspace{}, ErrInvalid
	}
	value, err := scanWorkspace(r.db.QueryRowContext(ctx, `SELECT `+workspaceColumns+`
		FROM workspaces WHERE id=$1 AND deleted_at IS NULL`, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	return value, nil
}

func (r *Repository) ListAccessible(
	ctx context.Context,
	userID string,
	limit int,
) ([]Workspace, error) {
	userID = strings.TrimSpace(userID)
	if !validUUID(userID) || limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT w.id,w.slug::TEXT,w.display_name,w.mode,w.status,w.owner_user_id,
			w.default_agent_id,w.default_model_config_id,w.settings,w.created_by,w.updated_by,
			w.created_at,w.updated_at,w.lock_version,w.deleted_at
		FROM workspaces w
		JOIN workspace_members m ON m.workspace_id=w.id AND m.user_id=$1 AND m.disabled_at IS NULL
		JOIN users u ON u.id=m.user_id AND u.status='ACTIVE'
		WHERE w.deleted_at IS NULL
		ORDER BY w.updated_at DESC,w.id LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list accessible workspaces: %w", err)
	}
	defer rows.Close()
	values := make([]Workspace, 0)
	for rows.Next() {
		value, err := scanWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan accessible workspace: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) Update(
	ctx context.Context,
	workspaceID string,
	input UpdateWorkspaceInput,
) (Workspace, error) {
	workspaceID, input.UpdatedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(input.UpdatedBy)
	if !validUUID(workspaceID) || !validUUID(input.UpdatedBy) || input.ExpectedLockVersion < 1 ||
		(input.DisplayName == nil && input.Mode == nil && len(input.Settings) == 0) ||
		(input.DisplayName != nil && strings.TrimSpace(*input.DisplayName) == "") ||
		(input.Mode != nil && !validMode(*input.Mode)) {
		return Workspace{}, ErrInvalid
	}
	var settings any
	if len(input.Settings) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(input.Settings, &object) != nil || object == nil {
			return Workspace{}, ErrInvalid
		}
		settings = []byte(input.Settings)
	}
	value, err := scanWorkspace(r.db.QueryRowContext(ctx, `
		UPDATE workspaces SET display_name=COALESCE($2,display_name),mode=COALESCE($3,mode),
			settings=COALESCE($4,settings),updated_by=$5,updated_at=clock_timestamp(),
			lock_version=lock_version+1
		WHERE id=$1 AND deleted_at IS NULL AND lock_version=$6
		RETURNING `+workspaceColumns,
		workspaceID, input.DisplayName, input.Mode, settings, input.UpdatedBy, input.ExpectedLockVersion))
	return r.workspaceMutationResult(ctx, workspaceID, value, err, "update workspace")
}

func (r *Repository) SetStatus(
	ctx context.Context,
	workspaceID string,
	status Status,
	updatedBy string,
	expectedLockVersion int64,
) (Workspace, error) {
	workspaceID, updatedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(updatedBy)
	if !validUUID(workspaceID) || !validUUID(updatedBy) || expectedLockVersion < 1 ||
		(status != StatusActive && status != StatusDisabled) {
		return Workspace{}, ErrInvalid
	}
	value, err := scanWorkspace(r.db.QueryRowContext(ctx, `UPDATE workspaces
		SET status=$2,updated_by=$3,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE id=$1 AND deleted_at IS NULL AND lock_version=$4
		RETURNING `+workspaceColumns, workspaceID, status, updatedBy, expectedLockVersion))
	return r.workspaceMutationResult(ctx, workspaceID, value, err, "set workspace status")
}

func (r *Repository) SoftDelete(
	ctx context.Context,
	workspaceID, deletedBy string,
	expectedLockVersion int64,
) error {
	workspaceID, deletedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(deletedBy)
	if !validUUID(workspaceID) || !validUUID(deletedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `UPDATE workspaces
		SET deleted_at=clock_timestamp(),updated_by=$2,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE id=$1 AND deleted_at IS NULL AND lock_version=$3`, workspaceID, deletedBy, expectedLockVersion)
	if err != nil {
		return mapWriteError("soft delete workspace", err)
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	_, err = r.Get(ctx, workspaceID)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func (r *Repository) workspaceMutationResult(
	ctx context.Context,
	workspaceID string,
	value Workspace,
	err error,
	operation string,
) (Workspace, error) {
	if errors.Is(err, sql.ErrNoRows) {
		_, readErr := r.Get(ctx, workspaceID)
		if errors.Is(readErr, ErrNotFound) {
			return Workspace{}, ErrNotFound
		}
		if readErr != nil {
			return Workspace{}, readErr
		}
		return Workspace{}, ErrConflict
	}
	if err != nil {
		return Workspace{}, mapWriteError(operation, err)
	}
	return value, nil
}

func (r *Repository) AddMember(ctx context.Context, input NewMember) (Member, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.InvitedBy = strings.TrimSpace(input.InvitedBy)
	if !validUUID(input.WorkspaceID) || !validUUID(input.UserID) ||
		!validUUID(input.InvitedBy) || !validRole(input.Role) {
		return Member{}, ErrInvalid
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin add workspace member transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := lockWorkspace(ctx, tx, input.WorkspaceID); err != nil {
		return Member{}, err
	}
	member, err := scanMember(tx.QueryRowContext(ctx, `
		INSERT INTO workspace_members (
			workspace_id, user_id, role, invited_by
		) VALUES ($1, $2, $3, $4)
		RETURNING `+memberColumns,
		input.WorkspaceID,
		input.UserID,
		input.Role,
		input.InvitedBy,
	))
	if err != nil {
		return Member{}, mapWriteError("add workspace member", err)
	}
	if err := tx.Commit(); err != nil {
		return Member{}, mapWriteError("commit add workspace member transaction", err)
	}
	return member, nil
}

func (r *Repository) ChangeMemberRole(
	ctx context.Context,
	workspaceID string,
	userID string,
	role Role,
	changedBy string,
) (Member, error) {
	workspaceID, userID, changedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(userID), strings.TrimSpace(changedBy)
	if !validUUID(workspaceID) || !validUUID(userID) || !validUUID(changedBy) || !validRole(role) {
		return Member{}, ErrInvalid
	}
	return r.mutateMember(ctx, workspaceID, userID, changedBy, func(
		ctx context.Context,
		tx *sql.Tx,
		primaryOwnerID string,
		current Member,
	) (Member, error) {
		if current.DisabledAt != nil {
			return Member{}, ErrConflict
		}
		if current.Role == role {
			return current, nil
		}
		if current.Role == RoleOwner && role != RoleOwner {
			if err := ensureAnotherActiveOwner(ctx, tx, workspaceID, userID); err != nil {
				return Member{}, err
			}
			if err := transferPrimaryOwner(ctx, tx, workspaceID, primaryOwnerID, userID, changedBy); err != nil {
				return Member{}, err
			}
		}
		updated, err := scanMember(tx.QueryRowContext(ctx, `
			UPDATE workspace_members
			SET role = $3
			WHERE workspace_id = $1 AND user_id = $2
			RETURNING `+memberColumns,
			workspaceID,
			userID,
			role,
		))
		if err != nil {
			return Member{}, mapWriteError("change workspace member role", err)
		}
		return updated, nil
	})
}

func (r *Repository) DisableMember(
	ctx context.Context,
	workspaceID string,
	userID string,
	changedBy string,
) (Member, error) {
	workspaceID, userID, changedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(userID), strings.TrimSpace(changedBy)
	if !validUUID(workspaceID) || !validUUID(userID) || !validUUID(changedBy) {
		return Member{}, ErrInvalid
	}
	return r.mutateMember(ctx, workspaceID, userID, changedBy, func(
		ctx context.Context,
		tx *sql.Tx,
		primaryOwnerID string,
		current Member,
	) (Member, error) {
		if current.DisabledAt != nil {
			return current, nil
		}
		if current.Role == RoleOwner {
			if err := ensureAnotherActiveOwner(ctx, tx, workspaceID, userID); err != nil {
				return Member{}, err
			}
			if err := transferPrimaryOwner(ctx, tx, workspaceID, primaryOwnerID, userID, changedBy); err != nil {
				return Member{}, err
			}
		}
		updated, err := scanMember(tx.QueryRowContext(ctx, `
			UPDATE workspace_members
			SET disabled_at = clock_timestamp()
			WHERE workspace_id = $1 AND user_id = $2
			RETURNING `+memberColumns,
			workspaceID,
			userID,
		))
		if err != nil {
			return Member{}, mapWriteError("disable workspace member", err)
		}
		return updated, nil
	})
}

func (r *Repository) RemoveMember(
	ctx context.Context,
	workspaceID string,
	userID string,
	changedBy string,
) error {
	workspaceID, userID, changedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(userID), strings.TrimSpace(changedBy)
	if !validUUID(workspaceID) || !validUUID(userID) || !validUUID(changedBy) {
		return ErrInvalid
	}
	_, err := r.mutateMember(ctx, workspaceID, userID, changedBy, func(
		ctx context.Context,
		tx *sql.Tx,
		primaryOwnerID string,
		current Member,
	) (Member, error) {
		if current.Role == RoleOwner && current.DisabledAt == nil {
			if err := ensureAnotherActiveOwner(ctx, tx, workspaceID, userID); err != nil {
				return Member{}, err
			}
		}
		if current.Role == RoleOwner {
			if err := transferPrimaryOwner(ctx, tx, workspaceID, primaryOwnerID, userID, changedBy); err != nil {
				return Member{}, err
			}
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_members
			WHERE workspace_id = $1 AND user_id = $2
		`, workspaceID, userID)
		if err != nil {
			return Member{}, mapWriteError("remove workspace member", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return Member{}, fmt.Errorf("read removed workspace member count: %w", err)
		}
		if rowsAffected != 1 {
			return Member{}, ErrNotFound
		}
		return current, nil
	})
	return err
}

func (r *Repository) ListMembers(
	ctx context.Context,
	workspaceID string,
	includeDisabled bool,
) ([]Member, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM workspaces
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check workspace before listing members: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+memberColumns+`
		FROM workspace_members
		WHERE workspace_id = $1
		  AND ($2 OR disabled_at IS NULL)
		ORDER BY joined_at, user_id
	`, workspaceID, includeDisabled)
	if err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}
	defer rows.Close()
	members := make([]Member, 0)
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace members: %w", err)
	}
	return members, nil
}

// SearchMemberCandidates returns ACTIVE users who do not already have a
// membership row in the requested Workspace. Authorization remains the HTTP
// layer's responsibility because this repository only owns scoped data access.
func (r *Repository) SearchMemberCandidates(
	ctx context.Context,
	workspaceID string,
	query string,
	limit int,
) ([]MemberCandidate, error) {
	workspaceID, query = strings.TrimSpace(workspaceID), strings.TrimSpace(query)
	if !validUUID(workspaceID) || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM workspaces
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check workspace before searching member candidates: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT u.id, u.username::TEXT, u.display_name, u.platform_role
		FROM users AS u
		WHERE u.status = 'ACTIVE'
		  AND NOT EXISTS (
			SELECT 1 FROM workspace_members AS m
			WHERE m.workspace_id = $1 AND m.user_id = u.id
		  )
		  AND (
			$2 = '' OR u.username::TEXT ILIKE '%' || $2 || '%'
			OR u.display_name ILIKE '%' || $2 || '%'
		  )
		ORDER BY lower(u.display_name), lower(u.username::TEXT), u.id
		LIMIT $3
	`, workspaceID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search workspace member candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]MemberCandidate, 0)
	for rows.Next() {
		var candidate MemberCandidate
		if err := rows.Scan(
			&candidate.UserID,
			&candidate.Username,
			&candidate.DisplayName,
			&candidate.PlatformRole,
		); err != nil {
			return nil, fmt.Errorf("scan workspace member candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace member candidates: %w", err)
	}
	return candidates, nil
}

// ResolveAccess reads the current account, Workspace, and membership state in
// one query whose leading predicate is the tenant boundary. A user who is not
// a member of the requested Workspace is indistinguishable from a missing or
// deleted Workspace.
func (r *Repository) ResolveAccess(
	ctx context.Context,
	workspaceID string,
	userID string,
) (AccessRecord, error) {
	workspaceID, userID = strings.TrimSpace(workspaceID), strings.TrimSpace(userID)
	if !validUUID(workspaceID) || !validUUID(userID) {
		return AccessRecord{}, ErrInvalid
	}
	var record AccessRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT
			w.id,
			w.status,
			u.id,
			u.status,
			m.role,
			(m.disabled_at IS NOT NULL)
		FROM workspaces AS w
		JOIN workspace_members AS m
		  ON m.workspace_id = w.id
		 AND m.user_id = $2
		JOIN users AS u
		  ON u.id = m.user_id
		WHERE w.id = $1
		  AND w.deleted_at IS NULL
	`, workspaceID, userID).Scan(
		&record.WorkspaceID,
		&record.WorkspaceStatus,
		&record.UserID,
		&record.UserStatus,
		&record.Role,
		&record.MemberDisabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessRecord{}, ErrNotFound
	}
	if err != nil {
		return AccessRecord{}, fmt.Errorf("resolve workspace access: %w", err)
	}
	return record, nil
}

type memberMutation func(context.Context, *sql.Tx, string, Member) (Member, error)

func (r *Repository) mutateMember(
	ctx context.Context,
	workspaceID string,
	userID string,
	changedBy string,
	mutation memberMutation,
) (Member, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, fmt.Errorf("begin workspace member transaction: %w", err)
	}
	defer tx.Rollback()
	primaryOwnerID, err := lockWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return Member{}, err
	}
	current, err := scanMember(tx.QueryRowContext(ctx, `
		SELECT `+memberColumns+`
		FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2
		FOR UPDATE
	`, workspaceID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("lock workspace member: %w", err)
	}
	updated, err := mutation(ctx, tx, primaryOwnerID, current)
	if err != nil {
		return Member{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, mapWriteError("commit workspace member transaction", err)
	}
	return updated, nil
}

func lockWorkspace(ctx context.Context, tx *sql.Tx, workspaceID string) (string, error) {
	var primaryOwnerID string
	err := tx.QueryRowContext(ctx, `
		SELECT owner_user_id
		FROM workspaces
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, workspaceID).Scan(&primaryOwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock workspace: %w", err)
	}
	return primaryOwnerID, nil
}

func ensureAnotherActiveOwner(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	excludedUserID string,
) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM workspace_members
			WHERE workspace_id = $1
			  AND role = 'OWNER'
			  AND disabled_at IS NULL
			  AND user_id <> $2
		)
	`, workspaceID, excludedUserID).Scan(&exists); err != nil {
		return fmt.Errorf("check remaining workspace owner: %w", err)
	}
	if !exists {
		return ErrLastOwner
	}
	return nil
}

func transferPrimaryOwner(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	primaryOwnerID string,
	departingUserID string,
	changedBy string,
) error {
	if primaryOwnerID != departingUserID {
		return nil
	}
	var replacementUserID string
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id
		FROM workspace_members
		WHERE workspace_id = $1
		  AND role = 'OWNER'
		  AND disabled_at IS NULL
		  AND user_id <> $2
		ORDER BY joined_at, user_id
		LIMIT 1
	`, workspaceID, departingUserID).Scan(&replacementUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLastOwner
		}
		return fmt.Errorf("select replacement workspace owner: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspaces
		SET owner_user_id = $2,
			updated_by = $3,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE id = $1
	`, workspaceID, replacementUserID, changedBy); err != nil {
		return mapWriteError("transfer primary workspace owner", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanWorkspace(row rowScanner) (Workspace, error) {
	var workspace Workspace
	var settings []byte
	err := row.Scan(
		&workspace.ID,
		&workspace.Slug,
		&workspace.DisplayName,
		&workspace.Mode,
		&workspace.Status,
		&workspace.OwnerUserID,
		&workspace.DefaultAgentID,
		&workspace.DefaultModelConfigID,
		&settings,
		&workspace.CreatedBy,
		&workspace.UpdatedBy,
		&workspace.CreatedAt,
		&workspace.UpdatedAt,
		&workspace.LockVersion,
		&workspace.DeletedAt,
	)
	workspace.Settings = append(json.RawMessage(nil), settings...)
	return workspace, err
}

func scanMember(row rowScanner) (Member, error) {
	var member Member
	err := row.Scan(
		&member.WorkspaceID,
		&member.UserID,
		&member.Role,
		&member.InvitedBy,
		&member.JoinedAt,
		&member.DisabledAt,
	)
	return member, err
}

func defaultNewWorkspace(input NewWorkspace) NewWorkspace {
	input.ID = strings.TrimSpace(input.ID)
	input.Slug = strings.TrimSpace(input.Slug)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	if input.Mode == "" {
		input.Mode = ModeProduction
	}
	if len(input.Settings) == 0 {
		input.Settings = json.RawMessage(`{}`)
	} else {
		input.Settings = append(json.RawMessage(nil), input.Settings...)
	}
	return input
}

func validateNewWorkspace(input NewWorkspace) error {
	if !validUUID(input.ID) || input.Slug == "" || input.DisplayName == "" ||
		!validUUID(input.OwnerUserID) || !validUUID(input.CreatedBy) || !validMode(input.Mode) {
		return ErrInvalid
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(input.Settings, &settings); err != nil || settings == nil {
		return ErrInvalid
	}
	return nil
}

func validateCreationDefaults(defaults CreationDefaults) error {
	if defaults.DefaultAgentID != nil && !validUUID(*defaults.DefaultAgentID) {
		return ErrInvalid
	}
	if defaults.DefaultModelConfigID != nil && !validUUID(*defaults.DefaultModelConfigID) {
		return ErrInvalid
	}
	return nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validMode(mode Mode) bool {
	return mode == ModeProduction || mode == ModeSandbox
}

func validRole(role Role) bool {
	switch role {
	case RoleOwner, RoleAdmin, RoleEditor, RoleOperator, RoleViewer:
		return true
	default:
		return false
	}
}

func mapWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var postgresError *pq.Error
	if errors.As(err, &postgresError) {
		switch postgresError.Code.Class() {
		case "22", "23":
			if postgresError.Code == "23505" {
				return fmt.Errorf("%s: %w", operation, ErrConflict)
			}
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
