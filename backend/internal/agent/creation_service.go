package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/storedobject"
	"github.com/google/uuid"
)

const (
	SourceLinkReasonNotEligible = "SOURCE_NOT_ELIGIBLE"
	SourceLinkReasonLinked      = "LINKED"
	ErrorCodePreviewIntegrity   = "PROMPT_PREVIEW_INTEGRITY_ERROR"
)

var (
	ErrPromptPreviewIntegrity = errors.New("prompt preview integrity error")
	ErrPromptOutputMismatch   = errors.New("prompt preview output mismatch")
)

// CreateAgentResult describes the create outcome including optional preview link.
type CreateAgentResult struct {
	Agent                  Agent
	Revision               PromptRevision
	SourcePromptPreviewRun *string
	SourceLinked           bool
	SourceReason           string
}

// PreviewObjectPromoter promotes EXPIRING preview StoredObjects inside a tx.
type PreviewObjectPromoter interface {
	PromotePreviewInTx(ctx context.Context, tx *sql.Tx, workspaceID, objectID string) (storedobject.StoredObject, error)
}

// TxAuditor records audit events inside an open transaction (fail-closed for promotion).
type TxAuditor interface {
	RecordInTransaction(context.Context, *sql.Tx, audit.ManagementEventInput) (audit.Event, error)
}

// CreationService creates Agents with optional atomic CREATE_PREVIEW promotion.
type CreationService struct {
	repository *Repository
	promoter   PreviewObjectPromoter
	auditor    TxAuditor
}

func NewCreationService(repository *Repository, promoter PreviewObjectPromoter) (*CreationService, error) {
	if repository == nil {
		return nil, errors.New("agent creation repository is required")
	}
	return &CreationService{repository: repository, promoter: promoter}, nil
}

// WithAuditor attaches same-transaction promotion audit (required in production).
func (s *CreationService) WithAuditor(auditor TxAuditor) *CreationService {
	if s != nil {
		s.auditor = auditor
	}
	return s
}

// Create builds an Agent. When sourcePromptPreviewRunID is empty, behavior matches
// Repository.Create (MANUAL unless PromptSource set). When set, eligibility is
// evaluated under row lock; eligible sources promote atomically, ineligible
// sources create MANUAL with SOURCE_NOT_ELIGIBLE (unless hash mismatch after
// other checks pass, which is OUTPUT_MISMATCH integrity).
func (s *CreationService) Create(
	ctx context.Context,
	input NewAgent,
	sourcePromptPreviewRunID string,
) (CreateAgentResult, error) {
	input = normalizeNewAgent(input)
	if !validNewAgent(input) {
		return CreateAgentResult{}, ErrInvalid
	}
	sourcePromptPreviewRunID = strings.TrimSpace(sourcePromptPreviewRunID)
	if sourcePromptPreviewRunID == "" {
		agent, revision, err := s.repository.Create(ctx, input)
		if err != nil {
			return CreateAgentResult{}, err
		}
		return CreateAgentResult{Agent: agent, Revision: revision, SourceLinked: false}, nil
	}
	if !validUUID(sourcePromptPreviewRunID) {
		return CreateAgentResult{}, ErrInvalid
	}
	if s.promoter == nil {
		return CreateAgentResult{}, errors.New("preview object promoter is required for source-linked create")
	}

	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CreateAgentResult{}, fmt.Errorf("begin create agent transaction: %w", err)
	}
	defer tx.Rollback()

	if input.IsDefault {
		if err := lockWorkspace(ctx, tx, input.WorkspaceID); err != nil {
			return CreateAgentResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents SET is_default=FALSE, updated_by=$2,
				updated_at=clock_timestamp(), lock_version=lock_version+1
			WHERE workspace_id=$1 AND is_default AND deleted_at IS NULL
		`, input.WorkspaceID, input.CreatedBy); err != nil {
			return CreateAgentResult{}, mapWrite("clear existing default agent", err)
		}
	}

	eligibility, run, err := s.evaluatePreviewSource(ctx, tx, input, sourcePromptPreviewRunID)
	if err != nil {
		return CreateAgentResult{}, err
	}

	promptSource := input.PromptSource
	if promptSource == "" {
		promptSource = PromptSourceManual
	}
	linked := false
	reason := ""
	switch eligibility {
	case sourceEligible:
		promptSource = PromptSourceAIAssisted
		linked = true
		reason = SourceLinkReasonLinked
	case sourceNotEligible:
		promptSource = PromptSourceManual
		linked = false
		reason = SourceLinkReasonNotEligible
	case sourceOutputMismatch:
		return CreateAgentResult{}, ErrPromptOutputMismatch
	case sourceIntegrityError:
		return CreateAgentResult{}, ErrPromptPreviewIntegrity
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents(
			id,workspace_id,name,role_description,model_config_id,is_default,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$7)
	`, input.ID, input.WorkspaceID, input.Name, input.RoleDescription,
		input.ModelConfigID, input.IsDefault, input.CreatedBy); err != nil {
		return CreateAgentResult{}, mapWrite("create agent", err)
	}
	revision, err := insertPromptRevision(ctx, tx, input.InitialRevisionID, input.WorkspaceID,
		input.ID, 1, input.InitialPrompt, promptSource, input.CreatedBy)
	if err != nil {
		return CreateAgentResult{}, err
	}
	value, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents AS a SET current_prompt_revision_id=$3
		WHERE workspace_id=$1 AND id=$2
		RETURNING `+agentColumns,
		input.WorkspaceID, input.ID, revision.ID))
	if err != nil {
		return CreateAgentResult{}, mapWrite("activate initial agent prompt", err)
	}
	if input.IsDefault {
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspaces SET default_agent_id=$2, updated_by=$3,
				updated_at=clock_timestamp(), lock_version=lock_version+1
			WHERE id=$1 AND deleted_at IS NULL
		`, input.WorkspaceID, input.ID, input.CreatedBy); err != nil {
			return CreateAgentResult{}, mapWrite("set workspace default agent", err)
		}
	}

	if linked {
		if run.InputObjectID == "" || run.OutputObjectID == nil {
			return CreateAgentResult{}, ErrPromptPreviewIntegrity
		}
		if _, err := s.promoter.PromotePreviewInTx(ctx, tx, input.WorkspaceID, run.InputObjectID); err != nil {
			return CreateAgentResult{}, fmt.Errorf("%w: promote input: %v", ErrPromptPreviewIntegrity, err)
		}
		if _, err := s.promoter.PromotePreviewInTx(ctx, tx, input.WorkspaceID, *run.OutputObjectID); err != nil {
			return CreateAgentResult{}, fmt.Errorf("%w: promote output: %v", ErrPromptPreviewIntegrity, err)
		}
		promotedRun, err := scanPromptRun(tx.QueryRowContext(ctx, `
			UPDATE prompt_runs
			SET agent_id=$3, accepted_revision_id=$4, promoted_at=clock_timestamp()
			WHERE workspace_id=$1 AND id=$2
			  AND operation_type='CREATE_PREVIEW'
			  AND agent_id IS NULL
			  AND accepted_revision_id IS NULL
			  AND promoted_at IS NULL
			  AND content_purged_at IS NULL
			  AND status='SUCCEEDED'
			  AND expires_at IS NOT NULL
			  AND expires_at > clock_timestamp()
			RETURNING id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
				input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
				status,accepted_revision_id,trace_id,created_by,
				created_at,finished_at,error_code,expires_at,promoted_at,content_purged_at
		`, input.WorkspaceID, run.ID, input.ID, revision.ID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CreateAgentResult{}, ErrPromptPreviewIntegrity
			}
			return CreateAgentResult{}, mapWrite("promote create-preview run", err)
		}
		if s.auditor != nil {
			eventID, err := uuid.NewV7()
			if err != nil {
				return CreateAgentResult{}, err
			}
			meta := map[string]any{
				"agentId":    input.ID,
				"revisionId": revision.ID,
				"runId":      run.ID,
				"source":     PromptSourceAIAssisted,
			}
			if promotedRun.PromotedAt != nil {
				meta["promotedAt"] = promotedRun.PromotedAt.UTC().Format(time.RFC3339Nano)
			}
			if _, err := s.auditor.RecordInTransaction(ctx, tx, audit.ManagementEventInput{
				EventID: eventID.String(), OccurredAt: time.Now().UTC(),
				WorkspaceID: input.WorkspaceID, ActorType: "USER", ActorID: input.CreatedBy,
				ActorDisplay: auditActorDisplay(""),
				Action:       audit.ActionAgentPromptPreviewPromoted, ResourceType: "PROMPT_RUN",
				ResourceID: run.ID, Result: "SUCCESS", Metadata: meta,
			}); err != nil {
				return CreateAgentResult{}, fmt.Errorf("%w: promotion audit: %v", ErrPromptPreviewIntegrity, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return CreateAgentResult{}, mapWrite("commit create agent transaction", err)
	}
	sourceRun := sourcePromptPreviewRunID
	return CreateAgentResult{
		Agent: value, Revision: revision,
		SourcePromptPreviewRun: &sourceRun,
		SourceLinked:           linked,
		SourceReason:           reason,
	}, nil
}

type sourceEligibility int

const (
	sourceEligible sourceEligibility = iota
	sourceNotEligible
	sourceOutputMismatch
	sourceIntegrityError
)

func (s *CreationService) evaluatePreviewSource(
	ctx context.Context,
	tx *sql.Tx,
	input NewAgent,
	runID string,
) (sourceEligibility, PromptRun, error) {
	run, err := scanPromptRun(tx.QueryRowContext(ctx, `
		SELECT id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
			status,accepted_revision_id,trace_id,created_by,
			created_at,finished_at,error_code,expires_at,promoted_at,content_purged_at
		FROM prompt_runs
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, input.WorkspaceID, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return sourceNotEligible, PromptRun{}, nil
	}
	if err != nil {
		return sourceIntegrityError, PromptRun{}, fmt.Errorf("lock prompt preview run: %w", err)
	}

	// Basic eligibility: same workspace (via query), same creator, CREATE_PREVIEW,
	// SUCCEEDED, unpromoted, unpurged, not expired.
	if run.OperationType != PromptOperationCreatePreview ||
		run.Status != "SUCCEEDED" ||
		run.AgentID != nil ||
		run.AcceptedRevisionID != nil ||
		run.PromotedAt != nil ||
		run.ContentPurgedAt != nil ||
		run.CreatedBy != input.CreatedBy ||
		run.ExpiresAt == nil ||
		!run.ExpiresAt.After(time.Now().UTC()) ||
		run.OutputObjectID == nil ||
		run.OutputSHA256 == nil {
		return sourceNotEligible, run, nil
	}

	// Re-check clock from database after lock.
	var stillValid bool
	if err := tx.QueryRowContext(ctx, `
		SELECT expires_at > clock_timestamp()
		FROM prompt_runs WHERE workspace_id=$1 AND id=$2
	`, input.WorkspaceID, runID).Scan(&stillValid); err != nil {
		return sourceIntegrityError, run, err
	}
	if !stillValid {
		return sourceNotEligible, run, nil
	}

	// Hash match uses trimmed system prompt vs stored output hash.
	digest := sha256.Sum256([]byte(strings.TrimSpace(input.InitialPrompt)))
	want := hex.EncodeToString(digest[:])
	if !strings.EqualFold(want, *run.OutputSHA256) {
		// Same-scope qualified context with hash mismatch is integrity/mismatch,
		// not silent MANUAL.
		return sourceOutputMismatch, run, nil
	}

	// Object existence for promotion path is integrity; missing objects after
	// eligibility is integrity error.
	var inputKind, outputKind string
	var inputMode, outputMode string
	var inputPurged, outputPurged sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT kind, retention_mode, body_purged_at FROM stored_objects
		WHERE workspace_id=$1 AND id=$2
	`, input.WorkspaceID, run.InputObjectID).Scan(&inputKind, &inputMode, &inputPurged)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceIntegrityError, run, nil
	}
	if err != nil {
		return sourceIntegrityError, run, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT kind, retention_mode, body_purged_at FROM stored_objects
		WHERE workspace_id=$1 AND id=$2
	`, input.WorkspaceID, *run.OutputObjectID).Scan(&outputKind, &outputMode, &outputPurged)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceIntegrityError, run, nil
	}
	if err != nil {
		return sourceIntegrityError, run, err
	}
	if inputKind != storedobject.KindPromptPreviewInput ||
		outputKind != storedobject.KindPromptPreviewOutput ||
		inputMode != storedobject.RetentionExpiring ||
		outputMode != storedobject.RetentionExpiring ||
		inputPurged.Valid || outputPurged.Valid {
		return sourceIntegrityError, run, nil
	}
	return sourceEligible, run, nil
}

// NewAgentID allocates a v7 UUID for create requests.
func NewAgentID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
