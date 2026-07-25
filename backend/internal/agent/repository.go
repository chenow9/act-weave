package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("agent resource not found")
	ErrConflict = errors.New("agent resource conflict")
	ErrInvalid  = errors.New("invalid agent resource")
	ErrInUse    = errors.New("agent resource is in use")
)

const agentColumns = `
	a.id, a.workspace_id, a.name::TEXT, a.role_description,
	a.current_prompt_revision_id, a.model_config_id, a.is_default, a.status,
	a.created_by, a.updated_by, a.created_at, a.updated_at, a.lock_version, a.deleted_at
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("agent repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, input NewAgent) (Agent, PromptRevision, error) {
	input = normalizeNewAgent(input)
	if !validNewAgent(input) {
		return Agent{}, PromptRevision{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, PromptRevision{}, fmt.Errorf("begin create agent transaction: %w", err)
	}
	defer tx.Rollback()
	if input.IsDefault {
		if err := lockWorkspace(ctx, tx, input.WorkspaceID); err != nil {
			return Agent{}, PromptRevision{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents SET is_default=FALSE, updated_by=$2,
				updated_at=clock_timestamp(), lock_version=lock_version+1
			WHERE workspace_id=$1 AND is_default AND deleted_at IS NULL
		`, input.WorkspaceID, input.CreatedBy); err != nil {
			return Agent{}, PromptRevision{}, mapWrite("clear existing default agent", err)
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agents(
			id,workspace_id,name,role_description,model_config_id,is_default,created_by,updated_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$7)
	`, input.ID, input.WorkspaceID, input.Name, input.RoleDescription,
		input.ModelConfigID, input.IsDefault, input.CreatedBy)
	if err != nil {
		return Agent{}, PromptRevision{}, mapWrite("create agent", err)
	}
	revision, err := insertPromptRevision(ctx, tx, input.InitialRevisionID, input.WorkspaceID,
		input.ID, 1, input.InitialPrompt, input.PromptSource, input.CreatedBy)
	if err != nil {
		return Agent{}, PromptRevision{}, err
	}
	value, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents AS a SET current_prompt_revision_id=$3
		WHERE workspace_id=$1 AND id=$2
		RETURNING `+agentColumns,
		input.WorkspaceID, input.ID, revision.ID))
	if err != nil {
		return Agent{}, PromptRevision{}, mapWrite("activate initial agent prompt", err)
	}
	if input.IsDefault {
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspaces SET default_agent_id=$2, updated_by=$3,
				updated_at=clock_timestamp(), lock_version=lock_version+1
			WHERE id=$1 AND deleted_at IS NULL
		`, input.WorkspaceID, input.ID, input.CreatedBy); err != nil {
			return Agent{}, PromptRevision{}, mapWrite("set workspace default agent", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, PromptRevision{}, mapWrite("commit create agent transaction", err)
	}
	return value, revision, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, agentID string) (Agent, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return Agent{}, ErrInvalid
	}
	value, err := scanAgent(r.db.QueryRowContext(ctx, `
		SELECT `+agentColumns+` FROM agents a
		WHERE a.workspace_id=$1 AND a.id=$2 AND a.deleted_at IS NULL
	`, workspaceID, agentID))
	return value, mapRead("get agent", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Agent, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+agentColumns+` FROM agents a
		WHERE a.workspace_id=$1 AND a.deleted_at IS NULL
		ORDER BY a.created_at, a.id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	values := make([]Agent, 0)
	for rows.Next() {
		value, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) GetSummary(ctx context.Context, workspaceID, agentID string) (Summary, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return Summary{}, ErrInvalid
	}
	value, err := scanSummary(r.db.QueryRowContext(ctx, `
		SELECT `+agentColumns+`,
			COUNT(*) FILTER (WHERE b.enabled AND c.kind='TOOL'),
			COUNT(*) FILTER (WHERE b.enabled AND c.kind='WORKFLOW')
		FROM agents a
		LEFT JOIN agent_capability_bindings b
		  ON b.workspace_id=a.workspace_id AND b.agent_id=a.id
		LEFT JOIN capabilities c
		  ON c.workspace_id=b.workspace_id AND c.id=b.capability_id AND c.deleted_at IS NULL
		WHERE a.workspace_id=$1 AND a.id=$2 AND a.deleted_at IS NULL
		GROUP BY a.id
	`, workspaceID, agentID))
	return value, mapSummaryRead("get agent summary", err)
}

func (r *Repository) ListSummaries(ctx context.Context, workspaceID string) ([]Summary, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+agentColumns+`,
			COUNT(*) FILTER (WHERE b.enabled AND c.kind='TOOL'),
			COUNT(*) FILTER (WHERE b.enabled AND c.kind='WORKFLOW')
		FROM agents a
		LEFT JOIN agent_capability_bindings b
		  ON b.workspace_id=a.workspace_id AND b.agent_id=a.id
		LEFT JOIN capabilities c
		  ON c.workspace_id=b.workspace_id AND c.id=b.capability_id AND c.deleted_at IS NULL
		WHERE a.workspace_id=$1 AND a.deleted_at IS NULL
		GROUP BY a.id ORDER BY a.created_at,a.id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list agent summaries: %w", err)
	}
	defer rows.Close()
	values := make([]Summary, 0)
	for rows.Next() {
		value, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent summary: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) Update(ctx context.Context, workspaceID, agentID string, input UpdateAgent) (Agent, error) {
	input = normalizeUpdateAgent(input)
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUpdateAgent(input) {
		return Agent{}, ErrInvalid
	}
	value, err := scanAgent(r.db.QueryRowContext(ctx, `
		UPDATE agents a SET name=$3, role_description=$4, model_config_id=$5,
			status=$6, updated_by=$7, updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND lock_version=$8
		RETURNING `+agentColumns,
		workspaceID, agentID, input.Name, input.RoleDescription, input.ModelConfigID,
		input.Status, input.UpdatedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, r.classifyAgentWrite(ctx, workspaceID, agentID)
	}
	if err != nil {
		return Agent{}, mapWrite("update agent", err)
	}
	return value, nil
}

func (r *Repository) UpdatePrompt(
	ctx context.Context,
	workspaceID, agentID, revisionID, prompt, source, updatedBy string,
	expectedLockVersion int64,
) (Agent, PromptRevision, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUUID(revisionID) ||
		!validUUID(updatedBy) || strings.TrimSpace(prompt) == "" || !validPromptSource(source) || expectedLockVersion < 1 {
		return Agent{}, PromptRevision{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, PromptRevision{}, fmt.Errorf("begin prompt revision transaction: %w", err)
	}
	defer tx.Rollback()
	var lockVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT lock_version FROM agents
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE
	`, workspaceID, agentID).Scan(&lockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, PromptRevision{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, PromptRevision{}, fmt.Errorf("lock agent prompt: %w", err)
	}
	if lockVersion != expectedLockVersion {
		return Agent{}, PromptRevision{}, ErrConflict
	}
	var revisionNo int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(revision_no),0)+1 FROM agent_prompt_revisions
		WHERE workspace_id=$1 AND agent_id=$2
	`, workspaceID, agentID).Scan(&revisionNo); err != nil {
		return Agent{}, PromptRevision{}, fmt.Errorf("allocate prompt revision number: %w", err)
	}
	revision, err := insertPromptRevision(ctx, tx, revisionID, workspaceID, agentID,
		revisionNo, prompt, source, updatedBy)
	if err != nil {
		return Agent{}, PromptRevision{}, err
	}
	value, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents a SET current_prompt_revision_id=$3, updated_by=$4,
			updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$5
		RETURNING `+agentColumns,
		workspaceID, agentID, revisionID, updatedBy, expectedLockVersion))
	if err != nil {
		return Agent{}, PromptRevision{}, mapWrite("activate agent prompt revision", err)
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, PromptRevision{}, mapWrite("commit prompt revision transaction", err)
	}
	return value, revision, nil
}

func (r *Repository) ListPromptRevisions(ctx context.Context, workspaceID, agentID string) ([]PromptRevision, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by,created_at
		FROM agent_prompt_revisions WHERE workspace_id=$1 AND agent_id=$2
		ORDER BY revision_no,id
	`, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list prompt revisions: %w", err)
	}
	defer rows.Close()
	values := make([]PromptRevision, 0)
	for rows.Next() {
		value, err := scanPromptRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan prompt revision: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) SetDefault(ctx context.Context, workspaceID, agentID, updatedBy string, expectedLockVersion int64) (Agent, error) {
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUUID(updatedBy) || expectedLockVersion < 1 {
		return Agent{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, fmt.Errorf("begin default agent transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockWorkspace(ctx, tx, workspaceID); err != nil {
		return Agent{}, err
	}
	var lockVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT lock_version FROM agents
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL AND status='ACTIVE' FOR UPDATE
	`, workspaceID, agentID).Scan(&lockVersion); errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	} else if err != nil {
		return Agent{}, fmt.Errorf("lock default agent: %w", err)
	}
	if lockVersion != expectedLockVersion {
		return Agent{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agents SET is_default=FALSE, updated_by=$3, updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id<>$2 AND is_default AND deleted_at IS NULL
	`, workspaceID, agentID, updatedBy); err != nil {
		return Agent{}, mapWrite("clear previous default agent", err)
	}
	value, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents a SET is_default=TRUE, updated_by=$3, updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$4
		RETURNING `+agentColumns,
		workspaceID, agentID, updatedBy, expectedLockVersion))
	if err != nil {
		return Agent{}, mapWrite("set default agent", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspaces SET default_agent_id=$2, updated_by=$3,
			updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE id=$1 AND deleted_at IS NULL
	`, workspaceID, agentID, updatedBy); err != nil {
		return Agent{}, mapWrite("set workspace default agent", err)
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, mapWrite("commit default agent transaction", err)
	}
	return value, nil
}

func (r *Repository) SoftDelete(ctx context.Context, workspaceID, agentID, deletedBy string, expectedLockVersion int64) error {
	if !validUUID(workspaceID) || !validUUID(agentID) || !validUUID(deletedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE agents SET deleted_at=clock_timestamp(), updated_by=$3,
			updated_at=clock_timestamp(), lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		  AND NOT is_default AND lock_version=$4
	`, workspaceID, agentID, deletedBy, expectedLockVersion)
	if err != nil {
		return mapWrite("soft delete agent", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted agent count: %w", err)
	}
	if rows == 1 {
		return nil
	}
	value, err := r.Get(ctx, workspaceID, agentID)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if value.IsDefault {
		return ErrInUse
	}
	return ErrConflict
}

// IsModelConfigInUse satisfies modelconfig.UsageChecker without introducing a
// dependency from that lower-level package back to Agent.
func (r *Repository) IsModelConfigInUse(ctx context.Context, tx *sql.Tx, workspaceID, configID string) (bool, error) {
	var inUse bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM agents
		WHERE workspace_id=$1 AND model_config_id=$2 AND deleted_at IS NULL)
	`, workspaceID, configID).Scan(&inUse)
	return inUse, err
}

func (r *Repository) StartPromptRun(ctx context.Context, input NewPromptRun) (PromptRun, error) {
	input = normalizePromptRun(input)
	if !validPromptRun(input) {
		return PromptRun{}, ErrInvalid
	}
	value, err := scanPromptRun(r.db.QueryRowContext(ctx, `
		INSERT INTO prompt_runs(
			id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,status,trace_id,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'PENDING',$10,$11)
		RETURNING id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
			status,accepted_revision_id,trace_id,created_by,
			created_at,finished_at,error_code
	`, input.ID, input.WorkspaceID, input.AgentID, input.OperationType, input.ModelConfigID,
		[]byte(input.ModelSnapshot), input.InputObjectID, input.InputSHA256, input.InputLength,
		input.TraceID, input.CreatedBy))
	if err != nil {
		return PromptRun{}, mapWrite("start prompt run", err)
	}
	return value, nil
}

func (r *Repository) CompletePromptRun(ctx context.Context, workspaceID, runID string,
	outputObjectID, outputSHA256 *string, outputLength *int64, errorCode *string,
) (PromptRun, error) {
	outputObjectID = optionalID(outputObjectID)
	outputSHA256 = optionalText(outputSHA256)
	errorCode = optionalText(errorCode)
	status := "SUCCEEDED"
	if errorCode != nil {
		status = "FAILED"
		outputObjectID = nil
		outputSHA256 = nil
		outputLength = nil
	}
	if !validUUID(workspaceID) || !validUUID(runID) ||
		(status == "SUCCEEDED" && (outputObjectID == nil || outputSHA256 == nil ||
			outputLength == nil || *outputLength < 1 || !validSHA256(*outputSHA256))) ||
		(errorCode != nil && !validStableCode(*errorCode)) {
		return PromptRun{}, ErrInvalid
	}
	value, err := scanPromptRun(r.db.QueryRowContext(ctx, `
		UPDATE prompt_runs SET status=$3,output_object_id=$4,output_sha256=$5,
			output_length=$6,error_code=$7,finished_at=clock_timestamp()
		WHERE workspace_id=$1 AND id=$2 AND status IN ('PENDING','RUNNING')
		RETURNING id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
			status,accepted_revision_id,trace_id,created_by,
			created_at,finished_at,error_code
	`, workspaceID, runID, status, outputObjectID, outputSHA256, outputLength, errorCode))
	if errors.Is(err, sql.ErrNoRows) {
		return PromptRun{}, ErrConflict
	}
	if err != nil {
		return PromptRun{}, mapWrite("complete prompt run", err)
	}
	return value, nil
}

func (r *Repository) GetPromptRun(ctx context.Context, workspaceID, runID string) (PromptRun, error) {
	if !validUUID(workspaceID) || !validUUID(runID) {
		return PromptRun{}, ErrInvalid
	}
	value, err := scanPromptRun(r.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
			status,accepted_revision_id,trace_id,created_by,
			created_at,finished_at,error_code
		FROM prompt_runs WHERE workspace_id=$1 AND id=$2
	`, workspaceID, runID))
	return value, mapRead("get prompt run", err)
}

func (r *Repository) AcceptPromptRun(
	ctx context.Context,
	workspaceID, runID, revisionID, prompt, acceptedBy string,
	expectedAgentLockVersion int64,
) (PromptRun, PromptRevision, error) {
	if !validUUID(workspaceID) || !validUUID(runID) || !validUUID(revisionID) ||
		!validUUID(acceptedBy) || strings.TrimSpace(prompt) == "" || expectedAgentLockVersion < 1 {
		return PromptRun{}, PromptRevision{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return PromptRun{}, PromptRevision{}, fmt.Errorf("begin accept prompt run transaction: %w", err)
	}
	defer tx.Rollback()
	var agentID string
	var outputID string
	err = tx.QueryRowContext(ctx, `
		SELECT agent_id,output_object_id FROM prompt_runs
		WHERE workspace_id=$1 AND id=$2 AND status='SUCCEEDED'
		  AND accepted_revision_id IS NULL AND agent_id IS NOT NULL AND output_object_id IS NOT NULL
		FOR UPDATE
	`, workspaceID, runID).Scan(&agentID, &outputID)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptRun{}, PromptRevision{}, ErrConflict
	}
	if err != nil {
		return PromptRun{}, PromptRevision{}, fmt.Errorf("lock prompt run acceptance: %w", err)
	}
	var lockVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT lock_version FROM agents
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE
	`, workspaceID, agentID).Scan(&lockVersion); errors.Is(err, sql.ErrNoRows) {
		return PromptRun{}, PromptRevision{}, ErrNotFound
	} else if err != nil {
		return PromptRun{}, PromptRevision{}, fmt.Errorf("lock agent for prompt acceptance: %w", err)
	}
	if lockVersion != expectedAgentLockVersion {
		return PromptRun{}, PromptRevision{}, ErrConflict
	}
	var revisionNo int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(revision_no),0)+1 FROM agent_prompt_revisions
		WHERE workspace_id=$1 AND agent_id=$2
	`, workspaceID, agentID).Scan(&revisionNo); err != nil {
		return PromptRun{}, PromptRevision{}, fmt.Errorf("allocate accepted prompt revision: %w", err)
	}
	revision, err := insertPromptRevision(ctx, tx, revisionID, workspaceID, agentID,
		revisionNo, prompt, "ENHANCED", acceptedBy)
	if err != nil {
		return PromptRun{}, PromptRevision{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agents SET current_prompt_revision_id=$3,updated_by=$4,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND lock_version=$5
	`, workspaceID, agentID, revisionID, acceptedBy, expectedAgentLockVersion); err != nil {
		return PromptRun{}, PromptRevision{}, mapWrite("activate accepted prompt", err)
	}
	run, err := scanPromptRun(tx.QueryRowContext(ctx, `
		UPDATE prompt_runs SET accepted_revision_id=$3
		WHERE workspace_id=$1 AND id=$2 AND accepted_revision_id IS NULL
		RETURNING id,workspace_id,agent_id,operation_type,model_config_id,model_snapshot,
			input_object_id,input_sha256,input_length,output_object_id,output_sha256,output_length,
			status,accepted_revision_id,trace_id,created_by,
			created_at,finished_at,error_code
	`, workspaceID, runID, revisionID))
	if err != nil {
		return PromptRun{}, PromptRevision{}, mapWrite("record accepted prompt revision", err)
	}
	if err := tx.Commit(); err != nil {
		return PromptRun{}, PromptRevision{}, mapWrite("commit prompt run acceptance", err)
	}
	return run, revision, nil
}

type rowScanner interface{ Scan(...any) error }

func scanAgent(row rowScanner) (Agent, error) {
	var value Agent
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.Name, &value.RoleDescription,
		&value.CurrentPromptRevisionID, &value.ModelConfigID, &value.IsDefault, &value.Status,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.DeletedAt)
	return value, err
}

func scanSummary(row rowScanner) (Summary, error) {
	var value Summary
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.Name, &value.RoleDescription,
		&value.CurrentPromptRevisionID, &value.ModelConfigID, &value.IsDefault, &value.Status,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.DeletedAt, &value.ToolsCount, &value.WorkflowsCount)
	return value, err
}

func mapSummaryRead(operation string, err error) error {
	return mapRead(operation, err)
}

func scanPromptRevision(row rowScanner) (PromptRevision, error) {
	var value PromptRevision
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.AgentID, &value.RevisionNo,
		&value.SystemPrompt, &value.Source, &value.ContentSHA256, &value.CreatedBy, &value.CreatedAt)
	return value, err
}

func scanPromptRun(row rowScanner) (PromptRun, error) {
	var value PromptRun
	var snapshot []byte
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.AgentID, &value.OperationType,
		&value.ModelConfigID, &snapshot, &value.InputObjectID, &value.InputSHA256,
		&value.InputLength, &value.OutputObjectID, &value.OutputSHA256, &value.OutputLength,
		&value.Status, &value.AcceptedRevisionID, &value.TraceID, &value.CreatedBy,
		&value.CreatedAt, &value.FinishedAt, &value.ErrorCode)
	value.ModelSnapshot = append(json.RawMessage(nil), snapshot...)
	return value, err
}

func insertPromptRevision(ctx context.Context, tx *sql.Tx, id, workspaceID, agentID string,
	revisionNo int, prompt, source, createdBy string,
) (PromptRevision, error) {
	digest := sha256.Sum256([]byte(prompt))
	value, err := scanPromptRevision(tx.QueryRowContext(ctx, `
		INSERT INTO agent_prompt_revisions(
			id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by,created_at
	`, id, workspaceID, agentID, revisionNo, prompt, source, hex.EncodeToString(digest[:]), createdBy))
	if err != nil {
		return PromptRevision{}, mapWrite("create prompt revision", err)
	}
	return value, nil
}

func lockWorkspace(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	var id string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM workspaces WHERE id=$1 AND deleted_at IS NULL FOR UPDATE
	`, workspaceID).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	return nil
}

func (r *Repository) classifyAgentWrite(ctx context.Context, workspaceID, agentID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM agents WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL)
	`, workspaceID, agentID).Scan(&exists); err != nil {
		return fmt.Errorf("classify agent write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func normalizeNewAgent(input NewAgent) NewAgent {
	input.ID, input.WorkspaceID, input.ModelConfigID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.ModelConfigID)
	input.Name, input.RoleDescription = strings.TrimSpace(input.Name), strings.TrimSpace(input.RoleDescription)
	input.InitialRevisionID, input.InitialPrompt = strings.TrimSpace(input.InitialRevisionID), strings.TrimSpace(input.InitialPrompt)
	input.PromptSource, input.CreatedBy = strings.TrimSpace(input.PromptSource), strings.TrimSpace(input.CreatedBy)
	if input.PromptSource == "" {
		input.PromptSource = "MANUAL"
	}
	return input
}

func validNewAgent(input NewAgent) bool {
	return validUUID(input.ID) && validUUID(input.WorkspaceID) && validUUID(input.ModelConfigID) &&
		validUUID(input.InitialRevisionID) && validUUID(input.CreatedBy) && input.Name != "" &&
		input.InitialPrompt != "" && validPromptSource(input.PromptSource)
}

func normalizeUpdateAgent(input UpdateAgent) UpdateAgent {
	input.Name, input.RoleDescription = strings.TrimSpace(input.Name), strings.TrimSpace(input.RoleDescription)
	input.ModelConfigID, input.UpdatedBy = strings.TrimSpace(input.ModelConfigID), strings.TrimSpace(input.UpdatedBy)
	return input
}

func validUpdateAgent(input UpdateAgent) bool {
	return input.Name != "" && validUUID(input.ModelConfigID) && validUUID(input.UpdatedBy) &&
		input.ExpectedLockVersion > 0 && (input.Status == StatusActive || input.Status == StatusDisabled || input.Status == StatusError)
}

func normalizePromptRun(input NewPromptRun) NewPromptRun {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.AgentID = optionalID(input.AgentID)
	input.OperationType, input.ModelConfigID = strings.TrimSpace(input.OperationType), strings.TrimSpace(input.ModelConfigID)
	input.InputObjectID, input.TraceID, input.CreatedBy = strings.TrimSpace(input.InputObjectID), strings.TrimSpace(input.TraceID), strings.TrimSpace(input.CreatedBy)
	input.InputSHA256 = strings.ToLower(strings.TrimSpace(input.InputSHA256))
	if len(input.ModelSnapshot) == 0 {
		input.ModelSnapshot = json.RawMessage(`{}`)
	}
	return input
}

func validPromptRun(input NewPromptRun) bool {
	return validUUID(input.ID) && validUUID(input.WorkspaceID) && validOptionalID(input.AgentID) &&
		(input.OperationType == "ENHANCE" || input.OperationType == "GENERATE" || input.OperationType == "PREVIEW") &&
		validUUID(input.ModelConfigID) && validJSONObject(input.ModelSnapshot) && validUUID(input.InputObjectID) &&
		validSHA256(input.InputSHA256) && input.InputLength > 0 && input.TraceID != "" && validUUID(input.CreatedBy)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validPromptSource(value string) bool {
	return value == "MANUAL" || value == "ENHANCED" || value == "GENERATED" || value == "IMPORTED"
}

func validJSONObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func validUUID(value string) bool        { _, err := uuid.Parse(value); return err == nil }
func validOptionalID(value *string) bool { return value == nil || validUUID(*value) }
func optionalID(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
func optionalText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
func validStableCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func mapRead(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func mapWrite(operation string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code.Class() == "23" {
		if pg.Code == "23505" {
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
		return fmt.Errorf("%s: %w", operation, ErrInvalid)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
