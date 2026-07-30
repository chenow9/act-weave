// Package contextsummary owns rolling summary claim/storage (ZKL-74 IC-11+, ZKL-81 IC-01+).
package contextsummary

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

	GenerationLegacyExtractive = "LEGACY_EXTRACTIVE"
	GenerationLLM              = "LLM"

	// SourceChainDomain separates rolling source digests from ad-hoc hashes.
	SourceChainDomain = "context-source-chain.v1"
)

// Summary is metadata only; body lives in encrypted stored_objects.
type Summary struct {
	ID                     string
	WorkspaceID            string
	SessionID              string
	Status                 string
	GenerationMethod       string
	OwnerToken             *string
	LeaseExpiresAt         *time.Time
	CoverageStartMessageID *string
	CoverageEndMessageID   *string
	SourceMessageCount     int
	SourceDigest           string
	ParentSummaryID        *string
	ParentSummaryDigest    *string
	PolicyFingerprint      string
	SummarizerSnapshot     json.RawMessage
	PromptTemplateVersion  string
	PromptTemplateHash     string
	ContentObjectID        *string
	ContentSHA256          *string
	ContentLength          *int64
	EstimatedInputTokens   int64
	EstimatedOutputTokens  int64
	EstimatorVersion       string
	AttemptCount           int
	NextRetryAt            *time.Time
	FailureCode            *string
	CreatedAt              time.Time
	ReadyAt                *time.Time
}

// ClaimInput identifies a summary build attempt by approved idempotency key.
type ClaimInput struct {
	ID                     string
	WorkspaceID            string
	SessionID              string
	GenerationMethod       string
	CoverageEndMessageID   string
	CoverageStartMessageID string
	SourceMessageCount     int
	SourceDigest           string
	PolicyFingerprint      string
	PromptTemplateVersion  string
	PromptTemplateHash     string
	ParentSummaryID        *string
	ParentSummaryDigest    *string
	SummarizerSnapshot     json.RawMessage
	EstimatedInputTokens   int64
	EstimatedOutputTokens  int64
	EstimatorVersion       string
	OwnerToken             string
	LeaseTTL               time.Duration
}

// MarkReadyInput finalizes a BUILDING summary with encrypted object reference.
type MarkReadyInput struct {
	WorkspaceID           string
	SummaryID             string
	OwnerToken            string
	ObjectID              string
	ContentSHA            string
	ContentLen            int64
	EstimatedInputTokens  int64
	EstimatedOutputTokens int64
	EstimatorVersion      string
	SummarizerSnapshot    json.RawMessage
}

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("context summary repository database is required")
	}
	return &Repository{db: db}, nil
}

// ClaimOrGet starts a BUILDING lease or returns existing READY for the same idempotency key.
// After a unique-key hit, parent identity and summarizer snapshot must match or conflict.
func (r *Repository) ClaimOrGet(ctx context.Context, input ClaimInput) (Summary, bool, error) {
	input = normalizeClaim(input)
	if err := validateClaim(input); err != nil {
		return Summary{}, false, err
	}
	existing, err := r.GetByIdempotency(ctx, input)
	if err == nil {
		if err := validateClaimConflict(existing, input); err != nil {
			return Summary{}, false, err
		}
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
	snap := input.SummarizerSnapshot
	if len(snap) == 0 {
		snap = json.RawMessage(`{}`)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO chat_context_summaries(
			id,workspace_id,session_id,status,generation_method,owner_token,lease_expires_at,
			coverage_start_message_id,coverage_end_message_id,source_message_count,
			source_digest,parent_summary_id,parent_summary_digest,policy_fingerprint,
			summarizer_snapshot,prompt_template_version,prompt_template_hash,
			estimated_input_tokens,estimated_output_tokens,estimator_version,attempt_count
		) VALUES (
			$1,$2,$3,'BUILDING',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18,$19,1
		)
		ON CONFLICT (workspace_id, session_id, coverage_end_message_id, source_digest, policy_fingerprint, prompt_template_hash)
		DO NOTHING
	`, id, input.WorkspaceID, input.SessionID, input.GenerationMethod, input.OwnerToken, leaseUntil,
		nullable(input.CoverageStartMessageID), input.CoverageEndMessageID, input.SourceMessageCount,
		input.SourceDigest, nullablePtr(input.ParentSummaryID), nullablePtr(input.ParentSummaryDigest),
		input.PolicyFingerprint, []byte(snap), input.PromptTemplateVersion, input.PromptTemplateHash,
		input.EstimatedInputTokens, input.EstimatedOutputTokens, input.EstimatorVersion)
	if err != nil {
		return Summary{}, false, fmt.Errorf("claim summary: %w", err)
	}
	got, err := r.GetByIdempotency(ctx, input)
	if err != nil {
		return Summary{}, false, err
	}
	if err := validateClaimConflict(got, input); err != nil {
		return Summary{}, false, err
	}
	claimed := got.Status == StatusBuilding && got.OwnerToken != nil && *got.OwnerToken == input.OwnerToken
	return got, claimed, nil
}

// MarkReady finalizes a BUILDING summary with encrypted object reference.
// LLM rows require coverage bounds, summarizer snapshot, and token estimates.
func (r *Repository) MarkReady(ctx context.Context, workspaceID, summaryID, ownerToken, objectID, contentSHA string, contentLen int64) (Summary, error) {
	return r.MarkReadyWith(ctx, MarkReadyInput{
		WorkspaceID: workspaceID,
		SummaryID:   summaryID,
		OwnerToken:  ownerToken,
		ObjectID:    objectID,
		ContentSHA:  contentSHA,
		ContentLen:  contentLen,
	})
}

// MarkReadyWith finalizes a BUILDING summary with full ready fields.
func (r *Repository) MarkReadyWith(ctx context.Context, input MarkReadyInput) (Summary, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SummaryID = strings.TrimSpace(input.SummaryID)
	input.OwnerToken = strings.TrimSpace(input.OwnerToken)
	input.ObjectID = strings.TrimSpace(input.ObjectID)
	input.ContentSHA = strings.ToLower(strings.TrimSpace(input.ContentSHA))
	input.EstimatorVersion = strings.TrimSpace(input.EstimatorVersion)
	if !validUUID(input.WorkspaceID) || !validUUID(input.SummaryID) || input.OwnerToken == "" ||
		!validUUID(input.ObjectID) || len(input.ContentSHA) != 64 || input.ContentLen < 0 {
		return Summary{}, ErrInvalid
	}

	current, err := r.Get(ctx, input.WorkspaceID, input.SummaryID)
	if err != nil {
		return Summary{}, err
	}
	if current.Status != StatusBuilding {
		return Summary{}, ErrConflict
	}
	if current.OwnerToken == nil || *current.OwnerToken != input.OwnerToken {
		return Summary{}, ErrConflict
	}
	if current.GenerationMethod == GenerationLLM {
		if current.CoverageStartMessageID == nil || strings.TrimSpace(*current.CoverageStartMessageID) == "" ||
			current.CoverageEndMessageID == nil || strings.TrimSpace(*current.CoverageEndMessageID) == "" {
			return Summary{}, ErrInvalid
		}
		if input.EstimatedInputTokens < 0 || input.EstimatedOutputTokens < 0 ||
			input.EstimatorVersion == "" {
			// Allow claim-time estimates when MarkReady omits them.
			if current.EstimatedInputTokens < 0 || current.EstimatedOutputTokens < 0 ||
				strings.TrimSpace(current.EstimatorVersion) == "" {
				return Summary{}, ErrInvalid
			}
		}
		snap := input.SummarizerSnapshot
		if len(snap) == 0 {
			snap = current.SummarizerSnapshot
		}
		if err := validateSummarizerSnapshot(snap); err != nil {
			return Summary{}, err
		}
		if len(input.SummarizerSnapshot) == 0 {
			input.SummarizerSnapshot = snap
		}
	}
	if len(input.SummarizerSnapshot) == 0 {
		input.SummarizerSnapshot = json.RawMessage(`{}`)
	}
	estIn := input.EstimatedInputTokens
	estOut := input.EstimatedOutputTokens
	estVer := input.EstimatorVersion
	if estVer == "" {
		estVer = current.EstimatorVersion
		estIn = current.EstimatedInputTokens
		estOut = current.EstimatedOutputTokens
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE chat_context_summaries SET
			status='READY', ready_at=clock_timestamp(),
			content_object_id=$4, content_sha256=$5, content_length=$6,
			summarizer_snapshot=$7::jsonb,
			estimated_input_tokens=$8, estimated_output_tokens=$9, estimator_version=$10,
			owner_token=NULL, lease_expires_at=NULL
		WHERE workspace_id=$1 AND id=$2 AND status='BUILDING' AND owner_token=$3
	`, input.WorkspaceID, input.SummaryID, input.OwnerToken, input.ObjectID, input.ContentSHA, input.ContentLen,
		[]byte(input.SummarizerSnapshot), estIn, estOut, estVer)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && (pqErr.Code == "55000" || pqErr.Code == "23503") {
			return Summary{}, ErrConflict
		}
		return Summary{}, fmt.Errorf("mark summary ready: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Summary{}, ErrConflict
	}
	return r.Get(ctx, input.WorkspaceID, input.SummaryID)
}

// MarkFailed records a safe failure code for a BUILDING claim.
// FAILED rows must never reference a content object (enforced by ready_state_check).
func (r *Repository) MarkFailed(ctx context.Context, workspaceID, summaryID, ownerToken, failureCode string) (Summary, error) {
	workspaceID, summaryID, ownerToken = strings.TrimSpace(workspaceID), strings.TrimSpace(summaryID), strings.TrimSpace(ownerToken)
	failureCode = strings.TrimSpace(failureCode)
	if !validUUID(workspaceID) || !validUUID(summaryID) || ownerToken == "" || failureCode == "" {
		return Summary{}, ErrInvalid
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE chat_context_summaries SET
			status='FAILED', failure_code=$4,
			content_object_id=NULL, content_sha256=NULL, content_length=NULL,
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
	return r.scanOne(r.db.QueryRowContext(ctx, summarySelect+`
		FROM chat_context_summaries WHERE workspace_id=$1 AND id=$2
	`, workspaceID, id))
}

func (r *Repository) GetByIdempotency(ctx context.Context, input ClaimInput) (Summary, error) {
	return r.scanOne(r.db.QueryRowContext(ctx, summarySelect+`
		FROM chat_context_summaries
		WHERE workspace_id=$1 AND session_id=$2 AND coverage_end_message_id=$3
		  AND source_digest=$4 AND policy_fingerprint=$5 AND prompt_template_hash=$6
	`, input.WorkspaceID, input.SessionID, input.CoverageEndMessageID,
		input.SourceDigest, input.PolicyFingerprint, input.PromptTemplateHash))
}

// LatestReadyFilter selects a reusable READY LLM summary for a session.
// Legacy extractive rows are never returned.
type LatestReadyFilter struct {
	WorkspaceID         string
	SessionID           string
	PolicyFingerprint   string
	PromptTemplateHash  string
	// Optional: when set, require matching canonical summarizer snapshot hash.
	SummarizerSnapshotHash string
}

// FindLatestReadyLLM returns the newest READY generation_method=LLM summary for
// the session that matches policy/template (and optional summarizer) fingerprints.
// Cross-workspace/session misses return ErrNotFound.
func (r *Repository) FindLatestReadyLLM(ctx context.Context, filter LatestReadyFilter) (Summary, error) {
	filter.WorkspaceID = strings.TrimSpace(filter.WorkspaceID)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	filter.PolicyFingerprint = strings.ToLower(strings.TrimSpace(filter.PolicyFingerprint))
	filter.PromptTemplateHash = strings.ToLower(strings.TrimSpace(filter.PromptTemplateHash))
	filter.SummarizerSnapshotHash = strings.ToLower(strings.TrimSpace(filter.SummarizerSnapshotHash))
	if !validUUID(filter.WorkspaceID) || !validUUID(filter.SessionID) ||
		len(filter.PolicyFingerprint) != 64 || len(filter.PromptTemplateHash) != 64 {
		return Summary{}, ErrInvalid
	}
	// Index-friendly partial lookup: READY + LLM, newest ready_at first.
	rows, err := r.db.QueryContext(ctx, summarySelect+`
		FROM chat_context_summaries
		WHERE workspace_id=$1 AND session_id=$2
		  AND status='READY' AND generation_method='LLM'
		  AND policy_fingerprint=$3 AND prompt_template_hash=$4
		ORDER BY ready_at DESC, id DESC
		LIMIT 32
	`, filter.WorkspaceID, filter.SessionID, filter.PolicyFingerprint, filter.PromptTemplateHash)
	if err != nil {
		return Summary{}, fmt.Errorf("find latest ready llm: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, scanErr := r.scanRow(rows)
		if scanErr != nil {
			return Summary{}, scanErr
		}
		if filter.SummarizerSnapshotHash != "" {
			if CanonicalSummarizerSnapshotHash(s.SummarizerSnapshot) != filter.SummarizerSnapshotHash {
				continue
			}
		}
		// Integrity: READY LLM must have content object + coverage bounds.
		if s.ContentObjectID == nil || s.ContentSHA256 == nil ||
			s.CoverageStartMessageID == nil || s.CoverageEndMessageID == nil {
			continue
		}
		return s, nil
	}
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}
	return Summary{}, ErrNotFound
}

func (r *Repository) scanRow(rows *sql.Rows) (Summary, error) {
	var s Summary
	var owner, covStart, covEnd, parentID, parentDig, objID, sha, fail sql.NullString
	var lease, nextRetry, ready sql.NullTime
	var contentLen sql.NullInt64
	var snap []byte
	err := rows.Scan(
		&s.ID, &s.WorkspaceID, &s.SessionID, &s.Status, &s.GenerationMethod, &owner, &lease,
		&covStart, &covEnd, &s.SourceMessageCount, &s.SourceDigest,
		&parentID, &parentDig, &s.PolicyFingerprint, &snap,
		&s.PromptTemplateVersion, &s.PromptTemplateHash, &objID, &sha, &contentLen,
		&s.EstimatedInputTokens, &s.EstimatedOutputTokens, &s.EstimatorVersion, &s.AttemptCount,
		&nextRetry, &fail, &s.CreatedAt, &ready,
	)
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

const summarySelect = `
		SELECT id,workspace_id,session_id,status,generation_method,owner_token,lease_expires_at,
			coverage_start_message_id,coverage_end_message_id,source_message_count,source_digest,
			parent_summary_id,parent_summary_digest,policy_fingerprint,summarizer_snapshot,
			prompt_template_version,prompt_template_hash,content_object_id,content_sha256,content_length,
			estimated_input_tokens,estimated_output_tokens,estimator_version,attempt_count,
			next_retry_at,failure_code,created_at,ready_at
`

func (r *Repository) scanOne(row *sql.Row) (Summary, error) {
	var s Summary
	var owner, covStart, covEnd, parentID, parentDig, objID, sha, fail sql.NullString
	var lease, nextRetry, ready sql.NullTime
	var contentLen sql.NullInt64
	var snap []byte
	err := row.Scan(
		&s.ID, &s.WorkspaceID, &s.SessionID, &s.Status, &s.GenerationMethod, &owner, &lease,
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
	in.EstimatorVersion = strings.TrimSpace(in.EstimatorVersion)
	in.GenerationMethod = strings.ToUpper(strings.TrimSpace(in.GenerationMethod))
	if in.GenerationMethod == "" {
		// Legacy path default; LLM callers must pass GenerationLLM explicitly.
		in.GenerationMethod = GenerationLegacyExtractive
	}
	if len(in.SummarizerSnapshot) == 0 {
		in.SummarizerSnapshot = json.RawMessage(`{}`)
	}
	return in
}

func validateClaim(in ClaimInput) error {
	if !validUUID(in.WorkspaceID) || !validUUID(in.SessionID) || !validUUID(in.CoverageEndMessageID) ||
		!validUUID(in.OwnerToken) || len(in.SourceDigest) != 64 || len(in.PolicyFingerprint) != 64 ||
		len(in.PromptTemplateHash) != 64 {
		return ErrInvalid
	}
	if in.GenerationMethod != GenerationLegacyExtractive && in.GenerationMethod != GenerationLLM {
		return ErrInvalid
	}
	if in.SourceMessageCount < 0 || in.EstimatedInputTokens < 0 || in.EstimatedOutputTokens < 0 {
		return ErrInvalid
	}
	if in.ParentSummaryID != nil && strings.TrimSpace(*in.ParentSummaryID) != "" {
		if !validUUID(strings.TrimSpace(*in.ParentSummaryID)) {
			return ErrInvalid
		}
		if in.ParentSummaryDigest == nil || len(strings.TrimSpace(*in.ParentSummaryDigest)) != 64 {
			return ErrInvalid
		}
	}
	if in.GenerationMethod == GenerationLLM {
		if strings.TrimSpace(in.CoverageStartMessageID) == "" || !validUUID(in.CoverageStartMessageID) {
			return ErrInvalid
		}
		// LLM claims must carry a non-empty canonical summarizer snapshot.
		if err := validateSummarizerSnapshot(in.SummarizerSnapshot); err != nil {
			return err
		}
	} else if len(in.SummarizerSnapshot) > 0 && string(in.SummarizerSnapshot) != "{}" {
		// Legacy may carry a snapshot; if present it must still be a JSON object.
		var obj map[string]any
		if err := json.Unmarshal(in.SummarizerSnapshot, &obj); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

// validateClaimConflict enforces post-unique-key identity for parent + summarizer.
func validateClaimConflict(existing Summary, input ClaimInput) error {
	if !sameOptionalUUID(existing.ParentSummaryID, input.ParentSummaryID) {
		return ErrConflict
	}
	if !sameOptionalDigest(existing.ParentSummaryDigest, input.ParentSummaryDigest) {
		return ErrConflict
	}
	if CanonicalSummarizerSnapshotHash(existing.SummarizerSnapshot) !=
		CanonicalSummarizerSnapshotHash(input.SummarizerSnapshot) {
		return ErrConflict
	}
	if existing.GenerationMethod != "" && input.GenerationMethod != "" &&
		existing.GenerationMethod != input.GenerationMethod {
		return ErrConflict
	}
	return nil
}

func sameOptionalUUID(existing *string, input *string) bool {
	e := ""
	if existing != nil {
		e = strings.TrimSpace(*existing)
	}
	i := ""
	if input != nil {
		i = strings.TrimSpace(*input)
	}
	return e == i
}

func sameOptionalDigest(existing *string, input *string) bool {
	e := ""
	if existing != nil {
		e = strings.ToLower(strings.TrimSpace(*existing))
	}
	i := ""
	if input != nil {
		i = strings.ToLower(strings.TrimSpace(*input))
	}
	return e == i
}

func validateSummarizerSnapshot(raw json.RawMessage) error {
	if len(raw) == 0 {
		return ErrInvalid
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ErrInvalid
	}
	// LLM READY path requires a non-empty object with identity fields
	// (model/template/etc.). Empty {} is not a valid LLM summarizer snapshot.
	if len(obj) == 0 {
		return ErrInvalid
	}
	return nil
}

// CanonicalSummarizerSnapshotHash returns a stable sha256 of canonical JSON object bytes.
// Empty/invalid snapshots hash to a fixed empty-object digest for conflict comparison.
func CanonicalSummarizerSnapshotHash(raw json.RawMessage) string {
	normalized := canonicalizeJSONObject(raw)
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func canonicalizeJSONObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []byte(`{}`)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return []byte(`{}`)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return []byte(`{}`)
	}
	return out
}

// MessageSourceTuple is one eligible chat message identity for source-chain digests.
type MessageSourceTuple struct {
	ID          string
	Role        string
	ContentHash string
}

// SourceChainDigest computes domain-separated cumulative coverage digest.
// parentDigest is empty for root; otherwise it is the parent summary's source_digest.
func SourceChainDigest(parentDigest string, messages []MessageSourceTuple) string {
	parentDigest = strings.ToLower(strings.TrimSpace(parentDigest))
	var b strings.Builder
	b.WriteString(SourceChainDomain)
	b.WriteByte(0)
	b.WriteString(parentDigest)
	b.WriteByte(0)
	for _, m := range messages {
		b.WriteString(strings.TrimSpace(m.ID))
		b.WriteByte('|')
		b.WriteString(strings.ToUpper(strings.TrimSpace(m.Role)))
		b.WriteByte('|')
		b.WriteString(strings.ToLower(strings.TrimSpace(m.ContentHash)))
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// CumulativeSourceMessageCount returns parent count + this pass message count.
func CumulativeSourceMessageCount(parentCount, passCount int) int {
	if parentCount < 0 {
		parentCount = 0
	}
	if passCount < 0 {
		passCount = 0
	}
	return parentCount + passCount
}

// ParentContentDigest returns parent READY content digest or empty for root.
func ParentContentDigest(parent *Summary) (string, error) {
	if parent == nil {
		return "", nil
	}
	if parent.Status != StatusReady || parent.ContentSHA256 == nil ||
		len(strings.TrimSpace(*parent.ContentSHA256)) != 64 {
		return "", ErrInvalid
	}
	return strings.ToLower(strings.TrimSpace(*parent.ContentSHA256)), nil
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
