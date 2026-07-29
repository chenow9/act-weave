// Package contextsummary owns rolling summary claim/storage (ZKL-74 IC-11+).
package contextsummary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("context summary not found")
	ErrConflict = errors.New("context summary conflict")
	ErrInvalid  = errors.New("invalid context summary")
)

const (
	StatusBuilding = "BUILDING"
	StatusReady    = "READY"
	StatusFailed   = "FAILED"
)

// Summary is metadata only; body lives in encrypted stored_objects.
type Summary struct {
	ID                    string
	WorkspaceID           string
	SessionID             string
	Status                string
	OwnerToken            *string
	LeaseExpiresAt        *time.Time
	CoverageStartMessageID *string
	CoverageEndMessageID  *string
	SourceMessageCount    int
	SourceDigest          string
	ParentSummaryID       *string
	ParentSummaryDigest   *string
	PolicyFingerprint     string
	SummarizerSnapshot    json.RawMessage
	PromptTemplateVersion string
	PromptTemplateHash    string
	ContentObjectID       *string
	ContentSHA256         *string
	ContentLength         *int64
	EstimatedInputTokens  int64
	EstimatedOutputTokens int64
	EstimatorVersion      string
	AttemptCount          int
	NextRetryAt           *time.Time
	FailureCode           *string
	CreatedAt             time.Time
	ReadyAt               *time.Time
}

// ClaimInput identifies a summary build attempt by approved idempotency key.
type ClaimInput struct {
	ID                    string
	WorkspaceID           string
	SessionID             string
	CoverageEndMessageID  string
	CoverageStartMessageID string
	SourceMessageCount    int
	SourceDigest          string
	PolicyFingerprint     string
	PromptTemplateVersion string
	PromptTemplateHash    string
	ParentSummaryID       *string
	ParentSummaryDigest   *string
	OwnerToken            string
	LeaseTTL              time.Duration
}

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("context summary repository database is required")
	}
	return &Repository{db: db}, nil
}

// ClaimOrGet starts a BUILDING lease or returns existing READY for the same idempotency key.
func (r *Repository) ClaimOrGet(ctx context.Context, input ClaimInput) (Summary, bool, error) {
	input = normalizeClaim(input)
	if err := validateClaim(input); err != nil {
		return Summary{}, false, err
	}
	// Existing ready?
	existing, err := r.GetByIdempotency(ctx, input)
	if err == nil {
		if existing.Status == StatusReady {
			return existing, false, nil
		}
		// Take over expired BUILDING lease or failed with backoff.
		if existing.Status == StatusBuilding && existing.LeaseExpiresAt != nil &&
			existing.LeaseExpiresAt.After(time.Now().UTC()) {
			return existing, false, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Summary{}, false, err
	}

	leaseUntil := time.Now().UTC().Add(input.LeaseTTL)
	if input.LeaseTTL <= 0 {
		leaseUntil = time.Now().UTC().Add(30 * time.Second)
	}
	id := input.ID
	if id == "" {
		u, genErr := uuid.NewV7()
		if genErr != nil {
			return Summary{}, false, genErr
		}
		id = u.String()
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO chat_context_summaries(
			id,workspace_id,session_id,status,owner_token,lease_expires_at,
			coverage_start_message_id,coverage_end_message_id,source_message_count,
			source_digest,parent_summary_id,parent_summary_digest,policy_fingerprint,
			prompt_template_version,prompt_template_hash,attempt_count
		) VALUES (
			$1,$2,$3,'BUILDING',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,1
		)
		ON CONFLICT (workspace_id, session_id, coverage_end_message_id, source_digest, policy_fingerprint, prompt_template_hash)
		DO NOTHING
	`, id, input.WorkspaceID, input.SessionID, input.OwnerToken, leaseUntil,
		nullable(input.CoverageStartMessageID), input.CoverageEndMessageID, input.SourceMessageCount,
		input.SourceDigest, nullablePtr(input.ParentSummaryID), nullablePtr(input.ParentSummaryDigest),
		input.PolicyFingerprint, input.PromptTemplateVersion, input.PromptTemplateHash)
	if err != nil {
		return Summary{}, false, fmt.Errorf("claim summary: %w", err)
	}
	got, err := r.GetByIdempotency(ctx, input)
	if err != nil {
		return Summary{}, false, err
	}
	claimed := got.Status == StatusBuilding && got.OwnerToken != nil && *got.OwnerToken == input.OwnerToken
	return got, claimed, nil
}

// MarkReady finalizes a BUILDING summary with encrypted object reference.
func (r *Repository) MarkReady(ctx context.Context, workspaceID, summaryID, ownerToken, objectID, contentSHA string, contentLen int64) (Summary, error) {
	workspaceID, summaryID, ownerToken = strings.TrimSpace(workspaceID), strings.TrimSpace(summaryID), strings.TrimSpace(ownerToken)
	if !validUUID(workspaceID) || !validUUID(summaryID) || ownerToken == "" || !validUUID(objectID) || len(contentSHA) != 64 {
		return Summary{}, ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE chat_context_summaries SET
			status='READY', ready_at=clock_timestamp(),
			content_object_id=$4, content_sha256=$5, content_length=$6,
			owner_token=NULL, lease_expires_at=NULL
		WHERE workspace_id=$1 AND id=$2 AND status='BUILDING' AND owner_token=$3
	`, workspaceID, summaryID, ownerToken, objectID, contentSHA, contentLen)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "55000" {
			return Summary{}, ErrConflict
		}
		return Summary{}, fmt.Errorf("mark summary ready: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Summary{}, ErrConflict
	}
	return r.Get(ctx, workspaceID, summaryID)
}

// MarkFailed records a safe failure code for a BUILDING claim.
func (r *Repository) MarkFailed(ctx context.Context, workspaceID, summaryID, ownerToken, failureCode string) (Summary, error) {
	workspaceID, summaryID, ownerToken = strings.TrimSpace(workspaceID), strings.TrimSpace(summaryID), strings.TrimSpace(ownerToken)
	failureCode = strings.TrimSpace(failureCode)
	if !validUUID(workspaceID) || !validUUID(summaryID) || ownerToken == "" || failureCode == "" {
		return Summary{}, ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE chat_context_summaries SET
			status='FAILED', failure_code=$4,
			owner_token=NULL, lease_expires_at=NULL
		WHERE workspace_id=$1 AND id=$2 AND status='BUILDING' AND owner_token=$3
	`, workspaceID, summaryID, ownerToken, failureCode)
	if err != nil {
		return Summary{}, fmt.Errorf("mark summary failed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Summary{}, ErrConflict
	}
	return r.Get(ctx, workspaceID, summaryID)
}

func (r *Repository) Get(ctx context.Context, workspaceID, id string) (Summary, error) {
	workspaceID, id = strings.TrimSpace(workspaceID), strings.TrimSpace(id)
	if !validUUID(workspaceID) || !validUUID(id) {
		return Summary{}, ErrInvalid
	}
	return r.scanOne(r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,session_id,status,owner_token,lease_expires_at,
			coverage_start_message_id,coverage_end_message_id,source_message_count,source_digest,
			parent_summary_id,parent_summary_digest,policy_fingerprint,summarizer_snapshot,
			prompt_template_version,prompt_template_hash,content_object_id,content_sha256,content_length,
			estimated_input_tokens,estimated_output_tokens,estimator_version,attempt_count,
			next_retry_at,failure_code,created_at,ready_at
		FROM chat_context_summaries WHERE workspace_id=$1 AND id=$2
	`, workspaceID, id))
}

func (r *Repository) GetByIdempotency(ctx context.Context, input ClaimInput) (Summary, error) {
	return r.scanOne(r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,session_id,status,owner_token,lease_expires_at,
			coverage_start_message_id,coverage_end_message_id,source_message_count,source_digest,
			parent_summary_id,parent_summary_digest,policy_fingerprint,summarizer_snapshot,
			prompt_template_version,prompt_template_hash,content_object_id,content_sha256,content_length,
			estimated_input_tokens,estimated_output_tokens,estimator_version,attempt_count,
			next_retry_at,failure_code,created_at,ready_at
		FROM chat_context_summaries
		WHERE workspace_id=$1 AND session_id=$2 AND coverage_end_message_id=$3
		  AND source_digest=$4 AND policy_fingerprint=$5 AND prompt_template_hash=$6
	`, input.WorkspaceID, input.SessionID, input.CoverageEndMessageID,
		input.SourceDigest, input.PolicyFingerprint, input.PromptTemplateHash))
}

func (r *Repository) scanOne(row *sql.Row) (Summary, error) {
	var s Summary
	var owner, covStart, covEnd, parentID, parentDig, objID, sha, fail sql.NullString
	var lease, nextRetry, ready sql.NullTime
	var contentLen sql.NullInt64
	var snap []byte
	err := row.Scan(
		&s.ID, &s.WorkspaceID, &s.SessionID, &s.Status, &owner, &lease,
		&covStart, &covEnd, &s.SourceMessageCount, &s.SourceDigest,
		&parentID, &parentDig, &s.PolicyFingerprint, &snap,
		&s.PromptTemplateVersion, &s.PromptTemplateHash, &objID, &sha, &contentLen,
		&s.EstimatedInputTokens, &s.EstimatedOutputTokens, &s.EstimatorVersion, &s.AttemptCount,
		&nextRetry, &fail, &s.CreatedAt, &ready,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	if err != nil {
		return Summary{}, err
	}
	s.SummarizerSnapshot = append(json.RawMessage(nil), snap...)
	if owner.Valid {
		s.OwnerToken = &owner.String
	}
	if lease.Valid {
		s.LeaseExpiresAt = &lease.Time
	}
	if covStart.Valid {
		s.CoverageStartMessageID = &covStart.String
	}
	if covEnd.Valid {
		s.CoverageEndMessageID = &covEnd.String
	}
	if parentID.Valid {
		s.ParentSummaryID = &parentID.String
	}
	if parentDig.Valid {
		s.ParentSummaryDigest = &parentDig.String
	}
	if objID.Valid {
		s.ContentObjectID = &objID.String
	}
	if sha.Valid {
		s.ContentSHA256 = &sha.String
	}
	if contentLen.Valid {
		s.ContentLength = &contentLen.Int64
	}
	if nextRetry.Valid {
		s.NextRetryAt = &nextRetry.Time
	}
	if fail.Valid {
		s.FailureCode = &fail.String
	}
	if ready.Valid {
		s.ReadyAt = &ready.Time
	}
	return s, nil
}

func normalizeClaim(in ClaimInput) ClaimInput {
	in.WorkspaceID = strings.TrimSpace(in.WorkspaceID)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.CoverageEndMessageID = strings.TrimSpace(in.CoverageEndMessageID)
	in.CoverageStartMessageID = strings.TrimSpace(in.CoverageStartMessageID)
	in.SourceDigest = strings.ToLower(strings.TrimSpace(in.SourceDigest))
	in.PolicyFingerprint = strings.ToLower(strings.TrimSpace(in.PolicyFingerprint))
	in.PromptTemplateHash = strings.ToLower(strings.TrimSpace(in.PromptTemplateHash))
	in.PromptTemplateVersion = strings.TrimSpace(in.PromptTemplateVersion)
	in.OwnerToken = strings.TrimSpace(in.OwnerToken)
	return in
}

func validateClaim(in ClaimInput) error {
	if !validUUID(in.WorkspaceID) || !validUUID(in.SessionID) || !validUUID(in.CoverageEndMessageID) ||
		!validUUID(in.OwnerToken) || len(in.SourceDigest) != 64 || len(in.PolicyFingerprint) != 64 ||
		len(in.PromptTemplateHash) != 64 {
		return ErrInvalid
	}
	return nil
}

func validUUID(v string) bool {
	_, err := uuid.Parse(v)
	return err == nil
}

func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullablePtr(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}
