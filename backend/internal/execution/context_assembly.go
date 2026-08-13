package execution

import (
	"bytes"
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
	ErrCodeContextSnapshotUnsupported    = "CONTEXT_SNAPSHOT_UNSUPPORTED"
	ErrCodeContextModelLimitUnknown      = "CONTEXT_MODEL_LIMIT_UNKNOWN"
	ErrCodeContextRequiredInputTooLarge  = "CONTEXT_REQUIRED_INPUT_TOO_LARGE"
	ErrCodeContextAssemblyFailed         = "CONTEXT_ASSEMBLY_FAILED"
	ErrCodeContextWindowExceededUpstream = "CONTEXT_WINDOW_EXCEEDED_UPSTREAM"
)

// Typed context errors for runtime mapping.
var (
	ErrContextSnapshotUnsupported    = errors.New(ErrCodeContextSnapshotUnsupported)
	ErrContextModelLimitUnknown      = errors.New(ErrCodeContextModelLimitUnknown)
	ErrContextRequiredInputTooLarge  = errors.New(ErrCodeContextRequiredInputTooLarge)
	ErrContextAssemblyFailed         = errors.New(ErrCodeContextAssemblyFailed)
	ErrContextWindowExceededUpstream = errors.New(ErrCodeContextWindowExceededUpstream)
	ErrContextAssemblyConflict       = errors.New("context assembly digest conflict")
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

// Tool search modes for assembly manifests (design §9.3).
const (
	// AssemblyToolSearchModeNone is the default for classic/old assemblies.
	AssemblyToolSearchModeNone = "none"
	// AssemblyToolSearchModeClientBounded is required for new Agentic assemblies.
	AssemblyToolSearchModeClientBounded   = "client_bounded"
	AssemblyToolSearchModePlatformBounded = "platform_bounded"
	AssemblyToolSearchModeCarryAll        = "carry_all"

	assemblyEstimatorAgenticOpenAIV1 = "contextwindow-estimator.agentic-openai-responses.v1"
	assemblyEstimatorAgenticOpenAIV2 = "contextwindow-estimator.agentic-openai-responses.v2"
)

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
	// Agentic expand-only fields (defaults for classic/old rows).
	ToolSearchMode               string
	ToolCatalogDigest            string // 64 hex; required for client_bounded
	ImmediateToolCount           int
	DeferredToolCount            int
	MaxLoadedToolCount           int
	ImmediateToolsTokens         int64
	DeferredMetadataTokens       int64
	DynamicToolLoadReserveTokens int64
	CreatedAt                    time.Time
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
			summary_id,summary_hash,summary_coverage,assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,
			$27,$28,$29,$30,$31,$32,$33,$34
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
		rec.ToolSearchMode, nullableStr(rec.ToolCatalogDigest),
		rec.ImmediateToolCount, rec.DeferredToolCount, rec.MaxLoadedToolCount,
		rec.ImmediateToolsTokens, rec.DeferredMetadataTokens, rec.DynamicToolLoadReserveTokens,
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
//
// Read integrity: after scan, only allowed body-free defaults are normalized
// (empty tool_search_mode → none, empty included_segments → []), then
// validateAssemblyRecord runs including AssemblyDigest recompute/equality,
// estimator/mode/token/count fields. Corrupt or cross-state DB rows fail closed.
// Insert race/idempotent winner path relies on this validated GetByRun and never
// trusts a stored digest alone without structural validation.
func (r *ContextAssemblyRepository) GetByRun(ctx context.Context, workspaceID, runID string) (ContextAssemblyRecord, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) {
		return ContextAssemblyRecord{}, ErrRunInvalid
	}
	var rec ContextAssemblyRecord
	var sessionID, sysRev, omitStart, omitEnd, summaryID, summaryHash, catalogDigest sql.NullString
	var segments, coverage []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,run_id,session_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_revision_id,system_prompt_hash,included_segments,
			omitted_prefix_start_message_id,omitted_prefix_end_message_id,omitted_prefix_count,
			summary_id,summary_hash,summary_coverage,assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens,
			created_at
		FROM agent_run_context_assemblies
		WHERE workspace_id=$1 AND run_id=$2
	`, workspaceID, runID).Scan(
		&rec.ID, &rec.WorkspaceID, &rec.RunID, &sessionID, &rec.Mode,
		&rec.PolicySnapshotHash, &rec.ModelSnapshotHash, &rec.CapabilitySnapshotHash, &rec.AgentSnapshotHash,
		&rec.EstimatorProfile, &rec.EstimatorVersion,
		&rec.HardInputCeilingTokens, &rec.OutputReserveTokens, &rec.SafetyMarginTokens, &rec.ToolsOverheadTokens,
		&sysRev, &rec.SystemPromptHash, &segments,
		&omitStart, &omitEnd, &rec.OmittedPrefixCount,
		&summaryID, &summaryHash, &coverage, &rec.AssemblyDigest, &rec.EstimatedTotalTokens,
		&rec.ToolSearchMode, &catalogDigest,
		&rec.ImmediateToolCount, &rec.DeferredToolCount, &rec.MaxLoadedToolCount,
		&rec.ImmediateToolsTokens, &rec.DeferredMetadataTokens, &rec.DynamicToolLoadReserveTokens,
		&rec.CreatedAt,
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
	if catalogDigest.Valid {
		rec.ToolCatalogDigest = catalogDigest.String
	}
	if len(coverage) > 0 {
		rec.SummaryCoverage = append(json.RawMessage(nil), coverage...)
	}
	// Normalize only allowed body-free defaults, then strict validate (digest etc.).
	rec = normalizeAssemblyRecordForRead(rec)
	if err := validateAssemblyRecord(rec); err != nil {
		return ContextAssemblyRecord{}, err
	}
	return rec, nil
}

// ComputeAssemblyDigest is a canonical SHA-256 over body-free identity fields.
// Current (agentic-aware) form always includes Agentic expand-only fields so new
// classic and client_bounded rows share one content-addressed payload.
//
// Legacy classic-v1 digests (rows written before Agentic fields entered the
// payload) are accepted on read via assemblyDigestMatches / computeClassicV1AssemblyDigest.
// Do not rewrite old rows.
func ComputeAssemblyDigest(rec ContextAssemblyRecord) string {
	// Intentionally exclude ID/created_at so digest is content-addressed.
	// Never includes schema bodies, tool names list text, or query content.
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
		rec.ToolSearchMode, rec.ToolCatalogDigest,
		fmt.Sprintf("%d", rec.ImmediateToolCount),
		fmt.Sprintf("%d", rec.DeferredToolCount),
		fmt.Sprintf("%d", rec.MaxLoadedToolCount),
		fmt.Sprintf("%d", rec.ImmediateToolsTokens),
		fmt.Sprintf("%d", rec.DeferredMetadataTokens),
		fmt.Sprintf("%d", rec.DynamicToolLoadReserveTokens),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// computeClassicV1AssemblyDigest is the original pre-Agentic digest payload
// (no tool_search_mode / catalog / count / token Agentic fields). Used only to
// accept legacy classic rows on read; new inserts always use ComputeAssemblyDigest.
func computeClassicV1AssemblyDigest(rec ContextAssemblyRecord) string {
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

// assemblyDigestMatches reports whether the stored digest is valid for rec.
// Accepts current ComputeAssemblyDigest always; for classic mode (none) also
// accepts exact classic-v1 digests so legacy rows remain readable without rewrite.
func assemblyDigestMatches(rec ContextAssemblyRecord) bool {
	if rec.AssemblyDigest == ComputeAssemblyDigest(rec) {
		return true
	}
	mode := rec.ToolSearchMode
	if mode == "" {
		mode = AssemblyToolSearchModeNone
	}
	if mode == AssemblyToolSearchModeNone {
		return rec.AssemblyDigest == computeClassicV1AssemblyDigest(rec)
	}
	return false
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
	rec = normalizeAssemblyRecordForRead(rec)
	// Empty AssemblyDigest is computed canonically after other fields normalize.
	// New inserts always use the current (agentic-aware) digest form.
	if rec.AssemblyDigest == "" {
		rec.AssemblyDigest = ComputeAssemblyDigest(rec)
	}
	return rec
}

// normalizeAssemblyRecordForRead applies only allowed body-free defaults.
// Never rewrites digests, catalog digests, or token/count fields.
func normalizeAssemblyRecordForRead(rec ContextAssemblyRecord) ContextAssemblyRecord {
	rec.WorkspaceID = strings.TrimSpace(rec.WorkspaceID)
	rec.RunID = strings.TrimSpace(rec.RunID)
	rec.SessionID = strings.TrimSpace(rec.SessionID)
	rec.Mode = strings.TrimSpace(rec.Mode)
	rec.EstimatorProfile = strings.TrimSpace(rec.EstimatorProfile)
	rec.EstimatorVersion = strings.TrimSpace(rec.EstimatorVersion)
	rec.ToolSearchMode = strings.TrimSpace(rec.ToolSearchMode)
	if rec.ToolSearchMode == "" {
		rec.ToolSearchMode = AssemblyToolSearchModeNone
	}
	// Never lowercase/trim catalog or assembly digests into validity — reject
	// noncanonical forms in validateAssemblyRecord instead.
	if len(rec.IncludedSegments) == 0 {
		rec.IncludedSegments = json.RawMessage(`[]`)
	}
	// The digest hashes these two members as text, and jsonb does not round-trip
	// text: it re-spaces and reorders object keys. Without a canonical form the
	// digest recomputed in GetByRun never matches the digest written moments
	// earlier, so every assembly carrying a non-empty segment list failed its own
	// read-back validation. Canonicalizing (sorted keys, compact, numeric
	// literals preserved) makes writer and reader agree without weakening the
	// digest: different content still yields different bytes.
	rec.IncludedSegments = canonicalDigestJSON(rec.IncludedSegments)
	if len(rec.SummaryCoverage) > 0 {
		rec.SummaryCoverage = canonicalDigestJSON(rec.SummaryCoverage)
	}
	return rec
}

// canonicalDigestJSON re-encodes raw with object keys sorted and whitespace
// removed. Malformed or trailing-garbage input is returned trimmed but
// otherwise untouched so it cannot be normalized into a colliding shape.
func canonicalDigestJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return trimmed
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return trimmed
	}
	if decoder.More() {
		return trimmed
	}
	out, err := json.Marshal(value)
	if err != nil {
		return trimmed
	}
	return json.RawMessage(out)
}

func validateAssemblyRecord(rec ContextAssemblyRecord) error {
	if !invocationValidUUID(rec.WorkspaceID) || !invocationValidUUID(rec.RunID) {
		return ErrRunInvalid
	}
	if rec.Mode == "" || rec.EstimatorProfile == "" || rec.EstimatorVersion == "" {
		return ErrRunInvalid
	}
	// Snapshot hashes and digests must be exact lowercase 64-hex (no case/whitespace normalize).
	if !isHex64(rec.PolicySnapshotHash) || !isHex64(rec.ModelSnapshotHash) ||
		!isHex64(rec.CapabilitySnapshotHash) || !isHex64(rec.AgentSnapshotHash) ||
		!isHex64(rec.SystemPromptHash) || !isHex64(rec.AssemblyDigest) {
		return ErrRunInvalid
	}
	// AssemblyDigest must match current content digest, or classic-v1 for legacy
	// classic rows (exact original semantics; do not rewrite old rows).
	if !assemblyDigestMatches(rec) {
		return ErrRunInvalid
	}
	switch rec.ToolSearchMode {
	case AssemblyToolSearchModeNone, AssemblyToolSearchModeClientBounded,
		AssemblyToolSearchModePlatformBounded, AssemblyToolSearchModeCarryAll:
	default:
		return ErrRunInvalid
	}
	if rec.ImmediateToolCount < 0 || rec.DeferredToolCount < 0 || rec.MaxLoadedToolCount < 0 ||
		rec.ImmediateToolsTokens < 0 || rec.DeferredMetadataTokens < 0 || rec.DynamicToolLoadReserveTokens < 0 ||
		rec.ToolsOverheadTokens < 0 {
		return ErrRunInvalid
	}

	// Classic / non-agentic: all Agentic fields must be at defaults.
	if rec.ToolSearchMode == AssemblyToolSearchModeNone {
		if rec.ToolCatalogDigest != "" ||
			rec.ImmediateToolCount != 0 || rec.DeferredToolCount != 0 || rec.MaxLoadedToolCount != 0 ||
			rec.ImmediateToolsTokens != 0 || rec.DeferredMetadataTokens != 0 || rec.DynamicToolLoadReserveTokens != 0 {
			return ErrRunInvalid
		}
		// Classic estimator version must not claim agentic.
		if rec.EstimatorVersion == assemblyEstimatorAgenticOpenAIV1 ||
			rec.EstimatorVersion == assemblyEstimatorAgenticOpenAIV2 {
			return ErrRunInvalid
		}
		return nil
	}

	if !isHex64(rec.ToolCatalogDigest) {
		return ErrRunInvalid
	}

	switch rec.ToolSearchMode {
	case AssemblyToolSearchModeClientBounded:
		if rec.EstimatorVersion != assemblyEstimatorAgenticOpenAIV1 {
			return ErrRunInvalid
		}
		wantMax := rec.DeferredToolCount
		if wantMax > 40 {
			wantMax = 40
		}
		if rec.MaxLoadedToolCount != wantMax || rec.MaxLoadedToolCount > 40 {
			return ErrRunInvalid
		}
		sum, err := addAssemblyTokens(
			rec.ImmediateToolsTokens,
			rec.DeferredMetadataTokens,
			rec.DynamicToolLoadReserveTokens,
		)
		if err != nil || rec.ToolsOverheadTokens != sum {
			return ErrRunInvalid
		}
		return nil
	case AssemblyToolSearchModePlatformBounded:
		if rec.EstimatorVersion != assemblyEstimatorAgenticOpenAIV2 {
			return ErrRunInvalid
		}
		if rec.DeferredMetadataTokens != 0 {
			return ErrRunInvalid
		}
		wantMax := rec.DeferredToolCount
		if wantMax > 5 {
			wantMax = 5
		}
		if rec.MaxLoadedToolCount != wantMax {
			return ErrRunInvalid
		}
		sum, err := addAssemblyTokens(
			rec.ImmediateToolsTokens,
			rec.DeferredMetadataTokens,
			rec.DynamicToolLoadReserveTokens,
		)
		if err != nil || rec.ToolsOverheadTokens != sum {
			return ErrRunInvalid
		}
		return nil
	case AssemblyToolSearchModeCarryAll:
		if rec.EstimatorVersion != assemblyEstimatorAgenticOpenAIV2 {
			return ErrRunInvalid
		}
		if rec.DeferredToolCount != 0 || rec.DeferredMetadataTokens != 0 ||
			rec.MaxLoadedToolCount != 0 || rec.DynamicToolLoadReserveTokens != 0 {
			return ErrRunInvalid
		}
		if rec.ToolsOverheadTokens != rec.ImmediateToolsTokens {
			return ErrRunInvalid
		}
		return nil
	default:
		return ErrRunInvalid
	}
}

// addAssemblyTokens is overflow-safe sum of nonnegative int64 token components.
func addAssemblyTokens(parts ...int64) (int64, error) {
	var sum int64
	for _, p := range parts {
		if p < 0 {
			return 0, ErrRunInvalid
		}
		if p > 0 && sum > (1<<63-1)-p {
			return 0, ErrRunInvalid
		}
		sum += p
	}
	return sum, nil
}

// isHex64 requires exact lowercase 64-hex. Uppercase and whitespace are rejected
// (never normalized into validity).
func isHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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
