package agentaccess

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type ExternalSubject struct {
	ID, WorkspaceID, ClientID, Issuer string
	SubjectHash                       []byte `json:"-"`
	DisplayRef                        string
	Status                            Status
	FirstSeenAt                       time.Time
	LastSeenAt                        time.Time
	DisabledAt                        *time.Time
	CreatedAt                         time.Time
	UpdatedAt                         time.Time
	LockVersion                       int64
}

type CreateExternalSubjectInput struct {
	ID, WorkspaceID, ClientID, Issuer string
	SubjectHash                       []byte
	DisplayRef                        string
	SeenAt                            time.Time
}

func (repository *Repository) CreateExternalSubject(
	ctx context.Context,
	input CreateExternalSubjectInput,
) (ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(input.ID) || !validRepositoryUUID(input.WorkspaceID) ||
		!validRepositoryUUID(input.ClientID) || input.Issuer == "" ||
		len(input.SubjectHash) != 32 || input.SeenAt.IsZero() {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	value, err := scanExternalSubject(repository.db.QueryRowContext(ctx, `
		INSERT INTO external_subjects(
		 id,workspace_id,client_id,issuer,subject_hash,display_ref,
		 first_seen_at,last_seen_at,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7,$7)
		RETURNING id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
		 first_seen_at,last_seen_at,disabled_at,created_at,updated_at,lock_version
	`, input.ID, input.WorkspaceID, input.ClientID, input.Issuer,
		input.SubjectHash, nullableRepositoryString(input.DisplayRef), input.SeenAt.UTC()))
	return value, mapRepositoryWrite("create External Subject", err)
}

func (repository *Repository) FindExternalSubject(
	ctx context.Context,
	workspaceID, clientID, issuer string,
	subjectHash []byte,
) (ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		issuer == "" || len(subjectHash) != 32 {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	value, err := scanExternalSubject(repository.db.QueryRowContext(ctx, externalSubjectSelect+`
		WHERE workspace_id=$1 AND client_id=$2 AND issuer=$3 AND subject_hash=$4`,
		workspaceID, clientID, issuer, subjectHash))
	return value, mapRepositoryRead("find External Subject", err)
}

func (repository *Repository) GetExternalSubject(
	ctx context.Context,
	workspaceID, clientID, subjectID string,
) (ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(subjectID) {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	value, err := scanExternalSubject(repository.db.QueryRowContext(ctx, externalSubjectSelect+`
		WHERE workspace_id=$1 AND client_id=$2 AND id=$3`, workspaceID, clientID, subjectID))
	return value, mapRepositoryRead("get External Subject", err)
}

type UpdateExternalSubjectInput struct {
	DisplayRef          string
	Status              Status
	LastSeenAt          time.Time
	ExpectedLockVersion int64
}

func (repository *Repository) UpdateExternalSubjectCAS(
	ctx context.Context,
	workspaceID, clientID, subjectID string,
	input UpdateExternalSubjectInput,
) (ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		!validRepositoryUUID(subjectID) ||
		!knownRepositoryStatus(input.Status) || input.LastSeenAt.IsZero() ||
		input.ExpectedLockVersion < 1 {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	current, err := repository.GetExternalSubject(ctx, workspaceID, clientID, subjectID)
	if err != nil {
		return ExternalSubject{}, err
	}
	if current.LockVersion != input.ExpectedLockVersion {
		return ExternalSubject{}, ErrRepositoryConflict
	}
	value, err := scanExternalSubject(repository.db.QueryRowContext(ctx, `
		UPDATE external_subjects
		SET display_ref=$3,status=$4,last_seen_at=$5,updated_at=clock_timestamp(),
		 disabled_at=CASE WHEN $4='DISABLED' THEN coalesce(disabled_at,clock_timestamp()) ELSE NULL END,
		 lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND client_id=$7 AND lock_version=$6
		RETURNING id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
		 first_seen_at,last_seen_at,disabled_at,created_at,updated_at,lock_version
	`, workspaceID, subjectID, nullableRepositoryString(input.DisplayRef), input.Status,
		input.LastSeenAt.UTC(), input.ExpectedLockVersion, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubject{}, ErrRepositoryConflict
	}
	return value, mapRepositoryWrite("update External Subject", err)
}

// ResolveOrCreateExternalSubject maps a trusted Subject Token identity to a
// durable External Subject row. Existing DISABLED subjects are returned so the
// Token Exchange path can deny without creating a parallel identity.
func (repository *Repository) ResolveOrCreateExternalSubject(
	ctx context.Context,
	workspaceID, clientID, issuer string,
	subjectHash []byte,
	seenAt time.Time,
) (ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) ||
		issuer == "" || len(subjectHash) != 32 || seenAt.IsZero() {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	existing, err := repository.FindExternalSubject(ctx, workspaceID, clientID, issuer, subjectHash)
	if err == nil {
		if existing.Status == StatusActive {
			updated, updateErr := repository.UpdateExternalSubjectCAS(ctx, workspaceID, clientID, existing.ID,
				UpdateExternalSubjectInput{
					DisplayRef: existing.DisplayRef, Status: StatusActive,
					LastSeenAt: seenAt.UTC(), ExpectedLockVersion: existing.LockVersion,
				})
			if updateErr == nil {
				return updated, nil
			}
			// Concurrent last-seen updates still return the active identity.
			if errors.Is(updateErr, ErrRepositoryConflict) {
				return repository.GetExternalSubject(ctx, workspaceID, clientID, existing.ID)
			}
			return ExternalSubject{}, updateErr
		}
		return existing, nil
	}
	if !errors.Is(err, ErrRepositoryNotFound) {
		return ExternalSubject{}, err
	}
	created, err := repository.CreateExternalSubject(ctx, CreateExternalSubjectInput{
		ID: uuid.NewString(), WorkspaceID: workspaceID, ClientID: clientID, Issuer: issuer,
		SubjectHash: append([]byte(nil), subjectHash...), SeenAt: seenAt.UTC(),
	})
	if err == nil {
		return created, nil
	}
	if errors.Is(err, ErrRepositoryConflict) {
		return repository.FindExternalSubject(ctx, workspaceID, clientID, issuer, subjectHash)
	}
	return ExternalSubject{}, err
}

func (repository *Repository) ListExternalSubjects(
	ctx context.Context,
	workspaceID, clientID string,
) ([]ExternalSubject, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		!validRepositoryUUID(workspaceID) || !validRepositoryUUID(clientID) {
		return nil, ErrRepositoryInvalid
	}
	rows, err := repository.db.QueryContext(ctx, externalSubjectSelect+`
		WHERE workspace_id=$1 AND client_id=$2
		ORDER BY last_seen_at DESC, id`, workspaceID, clientID)
	if err != nil {
		return nil, mapRepositoryRead("list External Subjects", err)
	}
	defer rows.Close()
	values := make([]ExternalSubject, 0)
	for rows.Next() {
		value, err := scanExternalSubject(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, mapRepositoryRead("list External Subjects", err)
	}
	return values, nil
}

// DeleteExternalSubject is intentionally unavailable: v1 retention is permanent.
// Callers must disable subjects instead of deleting identity evidence.
func (repository *Repository) DeleteExternalSubject(
	context.Context, string, string, string,
) error {
	return ErrRepositoryInvalid
}

const externalSubjectSelect = `
	SELECT id,workspace_id,client_id,issuer,subject_hash,display_ref,status,
	 first_seen_at,last_seen_at,disabled_at,created_at,updated_at,lock_version
	FROM external_subjects `

func scanExternalSubject(scanner repositoryScanner) (ExternalSubject, error) {
	var value ExternalSubject
	var displayRef sql.NullString
	var status string
	var disabled sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ClientID, &value.Issuer,
		&value.SubjectHash, &displayRef, &status, &value.FirstSeenAt,
		&value.LastSeenAt, &disabled, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion,
	)
	if err != nil {
		return ExternalSubject{}, err
	}
	parsed, ok := ParseStatus(status)
	if !ok {
		return ExternalSubject{}, ErrRepositoryInvalid
	}
	value.DisplayRef, value.Status = displayRef.String, parsed
	value.SubjectHash = append([]byte(nil), value.SubjectHash...)
	value.DisabledAt = repositoryTimePointer(disabled)
	return value, nil
}
