package execution

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

// Stable context-window error codes (safe for Console/AAP projection).
const (
	ErrCodeContextSnapshotUnsupported   = "CONTEXT_SNAPSHOT_UNSUPPORTED"
	ErrCodeContextModelLimitUnknown     = "CONTEXT_MODEL_LIMIT_UNKNOWN"
	ErrCodeContextRequiredInputTooLarge = "CONTEXT_REQUIRED_INPUT_TOO_LARGE"
	ErrCodeContextAssemblyFailed        = "CONTEXT_ASSEMBLY_FAILED"
	ErrCodeContextWindowExceededUpstream = "CONTEXT_WINDOW_EXCEEDED_UPSTREAM"
)

// Typed context errors for runtime mapping.
var (
	ErrContextSnapshotUnsupported   = errors.New(ErrCodeContextSnapshotUnsupported)
	ErrContextModelLimitUnknown     = errors.New(ErrCodeContextModelLimitUnknown)
	ErrContextRequiredInputTooLarge = errors.New(ErrCodeContextRequiredInputTooLarge)
	ErrContextAssemblyFailed        = errors.New(ErrCodeContextAssemblyFailed)
	ErrContextWindowExceededUpstream = errors.New(ErrCodeContextWindowExceededUpstream)
	ErrContextAssemblyConflict      = errors.New("context assembly digest conflict")
)

// ContextError is a stable, non-sensitive runtime error for context assembly.
type ContextError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *ContextError) Error() string {
	if e == nil {
		return ErrCodeContextAssemblyFailed
	}
	return e.Code + ": " + e.Message
}

// NewContextError builds a user-safe context error without cause/provider body.
func NewContextError(code string) *ContextError {
	switch code {
	case ErrCodeContextSnapshotUnsupported:
		return &ContextError{Code: code, Message: "运行上下文版本不受支持，请联系管理员", Retryable: false}
	case ErrCodeContextModelLimitUnknown:
		return &ContextError{Code: code, Message: "模型未配置上下文容量，请联系管理员", Retryable: false}
	case ErrCodeContextRequiredInputTooLarge:
		return &ContextError{Code: code, Message: "当前输入过长；请缩短输入、减少附件/工具或新建会话", Retryable: false}
	case ErrCodeContextAssemblyFailed:
		return &ContextError{Code: code, Message: "无法准备本次上下文，请稍后重试", Retryable: true}
	case ErrCodeContextWindowExceededUpstream:
		return &ContextError{Code: code, Message: "模型上下文容量校验失败，请联系管理员", Retryable: false}
	default:
		return &ContextError{Code: ErrCodeContextAssemblyFailed, Message: "无法准备本次上下文，请稍后重试", Retryable: true}
	}
}

// ContextAssemblyRecord is the immutable assembly manifest row (no body text).
type ContextAssemblyRecord struct {
	ID                          string
	WorkspaceID                 string
	RunID                       string
	SessionID                   string
	Mode                        string
	PolicySnapshotHash          string
	ModelSnapshotHash           string
	CapabilitySnapshotHash      string
	AgentSnapshotHash           string
	EstimatorProfile            string
	EstimatorVersion            string
	HardInputCeilingTokens      int64
	OutputReserveTokens         int64
	SafetyMarginTokens          int64
	ToolsOverheadTokens         int64
	SystemPromptRevisionID      *string
	SystemPromptHash            string
	IncludedSegments            json.RawMessage
	OmittedPrefixStartMessageID *string
	OmittedPrefixEndMessageID   *string
	OmittedPrefixCount          int
	SummaryID                   *string
	SummaryHash                 *string
	SummaryCoverage             json.RawMessage
	AssemblyDigest              string
	EstimatedTotalTokens        int64
	CreatedAt                   time.Time
}

// ContextAssemblyRepository persists agent_run_context_assemblies.
type ContextAssemblyRepository struct {
	db *sql.DB
}

func NewContextAssemblyRepository(db *sql.DB) (*ContextAssemblyRepository, error) {
	if db == nil {
		return nil, errors.New("context assembly repository database is required")
	}
	return &ContextAssemblyRepository{db: db}, nil
}

// InsertImmutable writes a manifest for a run. Same digest is idempotent reuse;
// different digest on same run returns ErrContextAssemblyConflict.
func (r *ContextAssemblyRepository) InsertImmutable(ctx context.Context, rec ContextAssemblyRecord) (ContextAssemblyRecord, error) {
	if r == nil {
		return ContextAssemblyRecord{}, ErrRunInvalid
	}
	rec = normalizeAssemblyRecord(rec)
	if err := validateAssemblyRecord(rec); err != nil {
		return ContextAssemblyRecord{}, err
	}
	if rec.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return ContextAssemblyRecord{}, err
		}
		rec.ID = id.String()
	}
	// Existing?
	existing, err := r.GetByRun(ctx, rec.WorkspaceID, rec.RunID)
	if err == nil {
		if existing.AssemblyDigest == rec.AssemblyDigest {
			return existing, nil
		}
		return ContextAssemblyRecord{}, ErrContextAssemblyConflict
	}
	if !errors.Is(err, ErrRunNotFound) {
		return ContextAssemblyRecord{}, err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_revision_id,system_prompt_hash,included_segments,
			omitted_prefix_start_message_id,omitted_prefix_end_message_id,omitted_prefix_count,
			summary_id,summary_hash,summary_coverage,assembly_digest,estimated_total_tokens
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26
		)
	`,
		rec.ID, rec.WorkspaceID, rec.RunID, nullableStr(rec.SessionID), rec.Mode,
		rec.PolicySnapshotHash, rec.ModelSnapshotHash, rec.CapabilitySnapshotHash, rec.AgentSnapshotHash,
		rec.EstimatorProfile, rec.EstimatorVersion,
		rec.HardInputCeilingTokens, rec.OutputReserveTokens, rec.SafetyMarginTokens, rec.ToolsOverheadTokens,
		nullableStrPtr(rec.SystemPromptRevisionID), rec.SystemPromptHash, []byte(rec.IncludedSegments),
		nullableStrPtr(rec.OmittedPrefixStartMessageID), nullableStrPtr(rec.OmittedPrefixEndMessageID), rec.OmittedPrefixCount,
		nullableStrPtr(rec.SummaryID), nullableStrPtr(rec.SummaryHash), nullableJSON(rec.SummaryCoverage),
		rec.AssemblyDigest, rec.EstimatedTotalTokens,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			// Race: re-read winner
			existing, getErr := r.GetByRun(ctx, rec.WorkspaceID, rec.RunID)
			if getErr == nil {
				if existing.AssemblyDigest == rec.AssemblyDigest {
					return existing, nil
				}
				return ContextAssemblyRecord{}, ErrContextAssemblyConflict
			}
		}
		return ContextAssemblyRecord{}, fmt.Errorf("insert context assembly: %w", err)
	}
	return r.GetByRun(ctx, rec.WorkspaceID, rec.RunID)
}

// GetByRun loads the assembly for a workspace-scoped run.
func (r *ContextAssemblyRepository) GetByRun(ctx context.Context, workspaceID, runID string) (ContextAssemblyRecord, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) {
		return ContextAssemblyRecord{}, ErrRunInvalid
	}
	var rec ContextAssemblyRecord
	var sessionID, sysRev, omitStart, omitEnd, summaryID, summaryHash sql.NullString
	var segments, coverage []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_revision_id,system_prompt_hash,included_segments,
			omitted_prefix_start_message_id,omitted_prefix_end_message_id,omitted_prefix_count,
			summary_id,summary_hash,summary_coverage,assembly_digest,estimated_total_tokens,created_at
		FROM agent_run_context_assemblies
		WHERE workspace_id=$1 AND run_id=$2
	`, workspaceID, runID).Scan(
		&rec.ID, &rec.WorkspaceID, &rec.RunID, &sessionID, &rec.Mode,
		&rec.PolicySnapshotHash, &rec.ModelSnapshotHash, &rec.CapabilitySnapshotHash, &rec.AgentSnapshotHash,
		&rec.EstimatorProfile, &rec.EstimatorVersion,
		&rec.HardInputCeilingTokens, &rec.OutputReserveTokens, &rec.SafetyMarginTokens, &rec.ToolsOverheadTokens,
		&sysRev, &rec.SystemPromptHash, &segments,
		&omitStart, &omitEnd, &rec.OmittedPrefixCount,
		&summaryID, &summaryHash, &coverage, &rec.AssemblyDigest, &rec.EstimatedTotalTokens, &rec.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextAssemblyRecord{}, ErrRunNotFound
	}
	if err != nil {
		return ContextAssemblyRecord{}, fmt.Errorf("get context assembly: %w", err)
	}
	rec.SessionID = sessionID.String
	rec.IncludedSegments = append(json.RawMessage(nil), segments...)
	if sysRev.Valid {
		rec.SystemPromptRevisionID = &sysRev.String
	}
	if omitStart.Valid {
		rec.OmittedPrefixStartMessageID = &omitStart.String
	}
	if omitEnd.Valid {
		rec.OmittedPrefixEndMessageID = &omitEnd.String
	}
	if summaryID.Valid {
		rec.SummaryID = &summaryID.String
	}
	if summaryHash.Valid {
		rec.SummaryHash = &summaryHash.String
	}
	if len(coverage) > 0 {
		rec.SummaryCoverage = append(json.RawMessage(nil), coverage...)
	}
	return rec, nil
}

// ComputeAssemblyDigest is a canonical SHA-256 over body-free identity fields.
func ComputeAssemblyDigest(rec ContextAssemblyRecord) string {
	// Intentionally exclude ID/created_at so digest is content-addressed.
	payload := strings.Join([]string{
		rec.WorkspaceID, rec.RunID, rec.SessionID, rec.Mode,
		rec.PolicySnapshotHash, rec.ModelSnapshotHash, rec.CapabilitySnapshotHash, rec.AgentSnapshotHash,
		rec.EstimatorProfile, rec.EstimatorVersion,
		fmt.Sprintf("%d", rec.HardInputCeilingTokens),
		fmt.Sprintf("%d", rec.OutputReserveTokens),
		fmt.Sprintf("%d", rec.SafetyMarginTokens),
		fmt.Sprintf("%d", rec.ToolsOverheadTokens),
		ptrStr(rec.SystemPromptRevisionID), rec.SystemPromptHash,
		string(rec.IncludedSegments),
		ptrStr(rec.OmittedPrefixStartMessageID), ptrStr(rec.OmittedPrefixEndMessageID),
		fmt.Sprintf("%d", rec.OmittedPrefixCount),
		ptrStr(rec.SummaryID), ptrStr(rec.SummaryHash), string(rec.SummaryCoverage),
		fmt.Sprintf("%d", rec.EstimatedTotalTokens),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// HashJSONObject returns sha256 hex of canonical JSON object bytes (or empty object).
func HashJSONObject(raw json.RawMessage) string {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeAssemblyRecord(rec ContextAssemblyRecord) ContextAssemblyRecord {
	rec.WorkspaceID = strings.TrimSpace(rec.WorkspaceID)
	rec.RunID = strings.TrimSpace(rec.RunID)
	rec.SessionID = strings.TrimSpace(rec.SessionID)
	rec.Mode = strings.TrimSpace(rec.Mode)
	rec.EstimatorProfile = strings.TrimSpace(rec.EstimatorProfile)
	rec.EstimatorVersion = strings.TrimSpace(rec.EstimatorVersion)
	if len(rec.IncludedSegments) == 0 {
		rec.IncludedSegments = json.RawMessage(`[]`)
	}
	if rec.AssemblyDigest == "" {
		rec.AssemblyDigest = ComputeAssemblyDigest(rec)
	}
	return rec
}

func validateAssemblyRecord(rec ContextAssemblyRecord) error {
	if !invocationValidUUID(rec.WorkspaceID) || !invocationValidUUID(rec.RunID) {
		return ErrRunInvalid
	}
	if rec.Mode == "" || rec.EstimatorProfile == "" || rec.EstimatorVersion == "" {
		return ErrRunInvalid
	}
	if !isHex64(rec.PolicySnapshotHash) || !isHex64(rec.ModelSnapshotHash) ||
		!isHex64(rec.CapabilitySnapshotHash) || !isHex64(rec.AgentSnapshotHash) ||
		!isHex64(rec.SystemPromptHash) || !isHex64(rec.AssemblyDigest) {
		return ErrRunInvalid
	}
	return nil
}

func isHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

func nullableStr(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullableStrPtr(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}

func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return []byte(v)
}

func ptrStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
