package secret

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("secret not found")
	ErrConflict = errors.New("secret conflict")
	ErrInvalid  = errors.New("invalid secret")
)

const readColumns = `
	s.id,
	s.workspace_id,
	s.name::TEXT,
	s.kind,
	(v.id IS NOT NULL AND v.revoked_at IS NULL),
	CASE WHEN v.revoked_at IS NULL THEN v.fingerprint ELSE NULL END,
	CASE WHEN v.revoked_at IS NULL THEN v.version_no ELSE NULL END,
	s.created_by,
	s.updated_by,
	s.created_at,
	s.updated_at,
	s.lock_version
`

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("secret repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) create(
	ctx context.Context,
	input CreateInput,
	secretID string,
	version protectedVersion,
) (ReadDTO, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ReadDTO{}, fmt.Errorf("begin create secret transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO secrets (
			id, workspace_id, name, kind, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $5)
	`, secretID, input.WorkspaceID, input.Name, input.Kind, input.ActorUserID); err != nil {
		return ReadDTO{}, mapWriteError("create secret", err)
	}
	if err := insertVersion(ctx, tx, version, 1); err != nil {
		return ReadDTO{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE secrets
		SET active_version_id = $3
		WHERE workspace_id = $1 AND id = $2
	`, input.WorkspaceID, secretID, version.ID); err != nil {
		return ReadDTO{}, mapWriteError("activate initial secret version", err)
	}
	dto, err := getReadDTO(ctx, tx, input.WorkspaceID, secretID)
	if err != nil {
		return ReadDTO{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadDTO{}, mapWriteError("commit create secret transaction", err)
	}
	return dto, nil
}

func (r *Repository) rotate(
	ctx context.Context,
	input RotateInput,
	version protectedVersion,
) (ReadDTO, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ReadDTO{}, fmt.Errorf("begin rotate secret transaction: %w", err)
	}
	defer tx.Rollback()
	activeVersionID, lockVersion, err := lockSecret(ctx, tx, input.WorkspaceID, input.SecretID)
	if err != nil {
		return ReadDTO{}, err
	}
	if lockVersion != input.ExpectedLockVersion {
		return ReadDTO{}, ErrConflict
	}
	var nextVersionNo int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) + 1
		FROM secret_versions
		WHERE workspace_id = $1 AND secret_id = $2
	`, input.WorkspaceID, input.SecretID).Scan(&nextVersionNo); err != nil {
		return ReadDTO{}, fmt.Errorf("select next secret version: %w", err)
	}
	if err := insertVersion(ctx, tx, version, nextVersionNo); err != nil {
		return ReadDTO{}, err
	}
	if activeVersionID != nil {
		result, err := tx.ExecContext(ctx, `
			UPDATE secret_versions
			SET revoked_at = clock_timestamp()
			WHERE workspace_id = $1
			  AND secret_id = $2
			  AND id = $3
			  AND revoked_at IS NULL
		`, input.WorkspaceID, input.SecretID, *activeVersionID)
		if err != nil {
			return ReadDTO{}, mapWriteError("revoke previous secret version", err)
		}
		if !exactlyOneRow(result) {
			return ReadDTO{}, ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE secrets
		SET active_version_id = $3,
			updated_by = $4,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE workspace_id = $1 AND id = $2 AND lock_version = $5
	`, input.WorkspaceID, input.SecretID, version.ID, input.ActorUserID, input.ExpectedLockVersion)
	if err != nil {
		return ReadDTO{}, mapWriteError("activate rotated secret version", err)
	}
	if !exactlyOneRow(result) {
		return ReadDTO{}, ErrConflict
	}
	if err := invalidateConnectionsForSecret(ctx, tx, input.WorkspaceID, input.SecretID, "CREDENTIAL_ROTATED"); err != nil {
		return ReadDTO{}, err
	}
	dto, err := getReadDTO(ctx, tx, input.WorkspaceID, input.SecretID)
	if err != nil {
		return ReadDTO{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadDTO{}, mapWriteError("commit rotate secret transaction", err)
	}
	return dto, nil
}

func (r *Repository) Revoke(ctx context.Context, input RevokeInput) (ReadDTO, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ReadDTO{}, fmt.Errorf("begin revoke secret transaction: %w", err)
	}
	defer tx.Rollback()
	activeVersionID, lockVersion, err := lockSecret(ctx, tx, input.WorkspaceID, input.SecretID)
	if err != nil {
		return ReadDTO{}, err
	}
	if lockVersion != input.ExpectedLockVersion || activeVersionID == nil {
		return ReadDTO{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE secret_versions
		SET revoked_at = clock_timestamp()
		WHERE workspace_id = $1
		  AND secret_id = $2
		  AND id = $3
		  AND revoked_at IS NULL
	`, input.WorkspaceID, input.SecretID, *activeVersionID)
	if err != nil {
		return ReadDTO{}, mapWriteError("revoke active secret version", err)
	}
	if !exactlyOneRow(result) {
		return ReadDTO{}, ErrConflict
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE secrets
		SET active_version_id = NULL,
			updated_by = $3,
			updated_at = clock_timestamp(),
			lock_version = lock_version + 1
		WHERE workspace_id = $1 AND id = $2 AND lock_version = $4
	`, input.WorkspaceID, input.SecretID, input.ActorUserID, input.ExpectedLockVersion)
	if err != nil {
		return ReadDTO{}, mapWriteError("clear active secret version", err)
	}
	if !exactlyOneRow(result) {
		return ReadDTO{}, ErrConflict
	}
	if err := invalidateConnectionsForSecret(ctx, tx, input.WorkspaceID, input.SecretID, "CREDENTIAL_REVOKED"); err != nil {
		return ReadDTO{}, err
	}
	dto, err := getReadDTO(ctx, tx, input.WorkspaceID, input.SecretID)
	if err != nil {
		return ReadDTO{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadDTO{}, mapWriteError("commit revoke secret transaction", err)
	}
	return dto, nil
}

func invalidateConnectionsForSecret(ctx context.Context, tx *sql.Tx, workspaceID, secretID, code string) error {
	var connectionsTableExists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.service_connections') IS NOT NULL`).Scan(&connectionsTableExists); err != nil {
		return fmt.Errorf("inspect service connections table: %w", err)
	}
	if !connectionsTableExists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE service_connections
		SET status='UNVERIFIED',last_verified_at=NULL,last_error_code=$3,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND credential_secret_id=$2 AND deleted_at IS NULL
	`, workspaceID, secretID, code); err != nil {
		return mapWriteError("invalidate connections for secret", err)
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, workspaceID string, secretID string) (ReadDTO, error) {
	return getReadDTO(ctx, r.db, workspaceID, secretID)
}

func (r *Repository) activeVersion(ctx context.Context, workspaceID string, secretID string) (protectedVersion, error) {
	var version protectedVersion
	err := r.db.QueryRowContext(ctx, `
		SELECT v.id,v.workspace_id,v.secret_id,v.ciphertext,v.nonce,v.key_id,v.fingerprint,v.created_by
		FROM secrets AS s
		JOIN secret_versions AS v
		  ON v.workspace_id=s.workspace_id AND v.secret_id=s.id AND v.id=s.active_version_id
		WHERE s.workspace_id=$1 AND s.id=$2 AND v.revoked_at IS NULL
	`, workspaceID, secretID).Scan(
		&version.ID, &version.WorkspaceID, &version.SecretID,
		&version.Encrypted.Ciphertext, &version.Encrypted.Nonce, &version.Encrypted.KeyID,
		&version.Fingerprint, &version.CreatedBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return protectedVersion{}, ErrNotFound
	}
	if err != nil {
		return protectedVersion{}, fmt.Errorf("read active secret version: %w", err)
	}
	return version, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getReadDTO(
	ctx context.Context,
	queryer queryRower,
	workspaceID string,
	secretID string,
) (ReadDTO, error) {
	var dto ReadDTO
	var fingerprint sql.NullString
	var activeVersionNo sql.NullInt64
	err := queryer.QueryRowContext(ctx, `
		SELECT `+readColumns+`
		FROM secrets AS s
		LEFT JOIN secret_versions AS v
		  ON v.workspace_id = s.workspace_id
		 AND v.secret_id = s.id
		 AND v.id = s.active_version_id
		WHERE s.workspace_id = $1 AND s.id = $2
	`, workspaceID, secretID).Scan(
		&dto.ID,
		&dto.WorkspaceID,
		&dto.Name,
		&dto.Kind,
		&dto.Configured,
		&fingerprint,
		&activeVersionNo,
		&dto.CreatedBy,
		&dto.UpdatedBy,
		&dto.CreatedAt,
		&dto.UpdatedAt,
		&dto.LockVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadDTO{}, ErrNotFound
	}
	if err != nil {
		return ReadDTO{}, fmt.Errorf("get secret read model: %w", err)
	}
	if fingerprint.Valid {
		dto.Fingerprint = fingerprint.String
	}
	if activeVersionNo.Valid {
		value := activeVersionNo.Int64
		dto.ActiveVersionNo = &value
	}
	return dto, nil
}

func insertVersion(
	ctx context.Context,
	tx *sql.Tx,
	version protectedVersion,
	versionNo int64,
) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO secret_versions (
			id, workspace_id, secret_id, version_no, ciphertext, nonce,
			key_id, fingerprint, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		version.ID,
		version.WorkspaceID,
		version.SecretID,
		versionNo,
		version.Encrypted.Ciphertext,
		version.Encrypted.Nonce,
		version.Encrypted.KeyID,
		version.Fingerprint,
		version.CreatedBy,
	); err != nil {
		return mapWriteError("create secret version", err)
	}
	return nil
}

func lockSecret(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	secretID string,
) (*string, int64, error) {
	var activeVersionID *string
	var lockVersion int64
	err := tx.QueryRowContext(ctx, `
		SELECT active_version_id, lock_version
		FROM secrets
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, secretID).Scan(&activeVersionID, &lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("lock secret: %w", err)
	}
	return activeVersionID, lockVersion, nil
}

func exactlyOneRow(result sql.Result) bool {
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
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
