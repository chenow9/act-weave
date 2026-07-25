package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound          = errors.New("workflow not found")
	ErrConflict          = errors.New("workflow conflict")
	ErrInvalid           = errors.New("invalid workflow")
	ErrTrialFailed       = errors.New("workflow trial failed")
	ErrNoSuccessfulTrial = errors.New("workflow has no successful trial")
)

const workflowColumns = `
	w.capability_id,w.workspace_id,w.current_draft_id,w.active_revision_id,
	w.latest_compilation_id,c.name::TEXT,c.slug::TEXT,c.description,c.status,
	c.created_by,c.updated_by,c.created_at,c.updated_at,c.lock_version,
	COALESCE((SELECT jsonb_array_length(d.graph->'nodes') FROM workflow_drafts d
	 WHERE d.workspace_id=w.workspace_id AND d.id=w.current_draft_id),0),
	COALESCE((SELECT jsonb_array_length(d.graph->'edges') FROM workflow_drafts d
	 WHERE d.workspace_id=w.workspace_id AND d.id=w.current_draft_id),0),
	c.deleted_at
`

const draftColumns = `
	d.id,d.workspace_id,d.capability_id,d.draft_version,d.schema_version,
	d.graph,d.graph_hash,d.updated_by,d.updated_at,d.lock_version
`

const compilationColumns = `
	wc.id,wc.workspace_id,wc.capability_id,wc.draft_id,wc.draft_version,
	wc.graph_hash,wc.compiler_version,wc.status,wc.spec,wc.plan,wc.issues,
	wc.plan_hash,wc.compiled_by,wc.compiled_at
`

const trialRunColumns = `
	tr.id,tr.workspace_id,tr.capability_id,tr.compilation_id,tr.execution_id,
	tr.status,tr.input_hash,tr.started_by,tr.started_at,tr.finished_at
`

const revisionColumns = `
	wr.id,wr.workspace_id,wr.capability_id,wr.revision_no,
	wr.source_compilation_id,wr.draft_snapshot,wr.spec_snapshot,wr.plan_snapshot,
	wr.plan_hash,wr.status,wr.publish_note,wr.created_by,wr.created_at,
	wr.activated_at,wr.retired_at
`

type Repository struct {
	db                      *sql.DB
	supportsTrialExecutions bool
}

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("workflow repository database is required")
	}
	var supportsTrialExecutions bool
	if err := db.QueryRow(`
		SELECT EXISTS(
		 SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='workflow_executions'
		  AND column_name='compilation_id'
		)
	`).Scan(&supportsTrialExecutions); err != nil {
		return nil, fmt.Errorf("inspect workflow trial execution schema: %w", err)
	}
	return &Repository{db: db, supportsTrialExecutions: supportsTrialExecutions}, nil
}

func (r *Repository) Create(ctx context.Context, input CreateInput) (Workflow, Draft, error) {
	input = normalizeCreate(input)
	canonicalGraph, graphHash, err := canonicalGraph(input.Graph)
	if !validCreate(input) || err != nil {
		return Workflow{}, Draft{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Workflow{}, Draft{}, fmt.Errorf("begin create workflow transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO capabilities(
		 id,workspace_id,kind,name,slug,description,created_by,updated_by
		) VALUES($1,$2,'WORKFLOW',$3,$4,$5,$6,$6)
	`, input.CapabilityID, input.WorkspaceID, input.Name, input.Slug,
		input.Description, input.CreatedBy); err != nil {
		return Workflow{}, Draft{}, mapWrite("create workflow capability", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflows(capability_id,workspace_id,current_draft_id)
		VALUES($1,$2,$3)
	`, input.CapabilityID, input.WorkspaceID, input.DraftID); err != nil {
		return Workflow{}, Draft{}, mapWrite("create workflow specialization", err)
	}
	draft, err := scanDraft(tx.QueryRowContext(ctx, `
		INSERT INTO workflow_drafts AS d(
		 id,workspace_id,capability_id,draft_version,schema_version,graph,
		 graph_hash,updated_by,lock_version
		) VALUES($1,$2,$3,1,$4,$5,$6,$7,1)
		RETURNING `+draftColumns,
		input.DraftID, input.WorkspaceID, input.CapabilityID, input.SchemaVersion,
		[]byte(canonicalGraph), graphHash, input.CreatedBy))
	if err != nil {
		return Workflow{}, Draft{}, mapWrite("create workflow draft", err)
	}
	value, err := scanWorkflow(tx.QueryRowContext(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows w JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE w.workspace_id=$1 AND w.capability_id=$2
	`, input.WorkspaceID, input.CapabilityID))
	if err != nil {
		return Workflow{}, Draft{}, fmt.Errorf("read created workflow: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Workflow{}, Draft{}, mapWrite("commit create workflow", err)
	}
	return value, draft, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, capabilityID string) (Workflow, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return Workflow{}, ErrInvalid
	}
	value, err := scanWorkflow(r.db.QueryRowContext(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows w JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE w.workspace_id=$1 AND w.capability_id=$2 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID))
	return value, mapRead("get workflow", err)
}

func (r *Repository) List(ctx context.Context, workspaceID string) ([]Workflow, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows w JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE w.workspace_id=$1 AND c.deleted_at IS NULL
		ORDER BY c.updated_at DESC,c.id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()
	values := make([]Workflow, 0)
	for rows.Next() {
		value, err := scanWorkflow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) UpdateMetadata(
	ctx context.Context,
	workspaceID, capabilityID string,
	input MetadataUpdate,
) (Workflow, error) {
	input = normalizeMetadata(input)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validMetadata(input) {
		return Workflow{}, ErrInvalid
	}
	value, err := scanWorkflow(r.db.QueryRowContext(ctx, `
		WITH updated AS (
		 UPDATE capabilities SET name=$3,slug=$4,description=$5,status=$6,
		  updated_by=$7,updated_at=clock_timestamp(),lock_version=lock_version+1
		 WHERE workspace_id=$1 AND id=$2 AND kind='WORKFLOW'
		  AND deleted_at IS NULL AND lock_version=$8
		 RETURNING *
		)
		SELECT `+workflowColumns+`
		FROM workflows w JOIN updated c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
	`, workspaceID, capabilityID, input.Name, input.Slug, input.Description,
		input.Status, input.UpdatedBy, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, r.classifyWorkflowWrite(ctx, workspaceID, capabilityID)
	}
	if err != nil {
		return Workflow{}, mapWrite("update workflow metadata", err)
	}
	return value, nil
}

func (r *Repository) SoftDelete(
	ctx context.Context,
	workspaceID, capabilityID, updatedBy string,
	expectedLockVersion int64,
) error {
	workspaceID, capabilityID, updatedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(capabilityID), strings.TrimSpace(updatedBy)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(updatedBy) || expectedLockVersion < 1 {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE capabilities SET status='DISABLED',deleted_at=clock_timestamp(),
		 updated_by=$3,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='WORKFLOW'
		 AND deleted_at IS NULL AND lock_version=$4
	`, workspaceID, capabilityID, updatedBy, expectedLockVersion)
	if err != nil {
		return mapWrite("soft delete workflow", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted workflow count: %w", err)
	}
	if rows != 1 {
		return r.classifyWorkflowWrite(ctx, workspaceID, capabilityID)
	}
	return nil
}

func (r *Repository) GetDraft(ctx context.Context, workspaceID, capabilityID string) (Draft, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return Draft{}, ErrInvalid
	}
	value, err := scanDraft(r.db.QueryRowContext(ctx, `
		SELECT `+draftColumns+`
		FROM workflow_drafts d
		JOIN capabilities c ON c.workspace_id=d.workspace_id AND c.id=d.capability_id
		WHERE d.workspace_id=$1 AND d.capability_id=$2 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID))
	return value, mapRead("get workflow draft", err)
}

func (r *Repository) UpdateDraft(
	ctx context.Context,
	workspaceID, capabilityID string,
	input DraftUpdate,
) (Draft, error) {
	input = normalizeDraftUpdate(input)
	canonical, graphHash, err := canonicalGraph(input.Graph)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validDraftUpdate(input) || err != nil {
		return Draft{}, ErrInvalid
	}
	value, err := scanDraft(r.db.QueryRowContext(ctx, `
		UPDATE workflow_drafts d SET draft_version=d.draft_version+1,
			 schema_version=$3,graph=$4,graph_hash=$5,updated_by=$6,
			 updated_at=clock_timestamp(),lock_version=d.lock_version+1
		FROM capabilities c
		WHERE d.workspace_id=$1 AND d.capability_id=$2
		 AND c.workspace_id=d.workspace_id AND c.id=d.capability_id AND c.deleted_at IS NULL
		 AND d.draft_version=$7 AND d.lock_version=$8
		RETURNING `+draftColumns,
		workspaceID, capabilityID, input.SchemaVersion, []byte(canonical), graphHash,
		input.UpdatedBy, input.ExpectedDraftVersion, input.ExpectedLockVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, r.classifyDraftWrite(ctx, workspaceID, capabilityID)
	}
	if err != nil {
		return Draft{}, mapWrite("update workflow draft", err)
	}
	return value, nil
}

func (r *Repository) CreateCompilation(
	ctx context.Context,
	draft Draft,
	input CompilationCreate,
) (Compilation, error) {
	input = normalizeCompilationCreate(input)
	if !validCompilationCreate(draft, input) {
		return Compilation{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Compilation{}, fmt.Errorf("begin workflow compilation transaction: %w", err)
	}
	defer tx.Rollback()

	var current bool
	if err := tx.QueryRowContext(ctx, `
		SELECT true
		FROM workflows w
		JOIN workflow_drafts d
		  ON d.workspace_id=w.workspace_id AND d.capability_id=w.capability_id
		 AND d.id=w.current_draft_id
		JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE w.workspace_id=$1 AND w.capability_id=$2 AND d.id=$3
		 AND d.draft_version=$4 AND d.graph_hash=$5 AND c.deleted_at IS NULL
		FOR UPDATE OF w,d
	`, draft.WorkspaceID, draft.CapabilityID, draft.ID, draft.DraftVersion, draft.GraphHash).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Compilation{}, r.classifyDraftWrite(ctx, draft.WorkspaceID, draft.CapabilityID)
		}
		return Compilation{}, fmt.Errorf("lock current workflow draft for compilation: %w", err)
	}

	value, err := scanCompilation(tx.QueryRowContext(ctx, `
		INSERT INTO workflow_compilations AS wc(
		 id,workspace_id,capability_id,draft_id,draft_version,graph_hash,
		 compiler_version,status,spec,plan,issues,plan_hash,compiled_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING `+compilationColumns,
		input.ID, draft.WorkspaceID, draft.CapabilityID, draft.ID,
		draft.DraftVersion, draft.GraphHash, input.CompilerVersion, input.Status,
		[]byte(input.Spec), []byte(input.Plan), []byte(input.Issues), input.PlanHash,
		input.CompiledBy))
	if err != nil {
		return Compilation{}, mapWrite("create workflow compilation", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workflows SET latest_compilation_id=$3
		WHERE workspace_id=$1 AND capability_id=$2
	`, draft.WorkspaceID, draft.CapabilityID, input.ID); err != nil {
		return Compilation{}, mapWrite("set latest workflow compilation", err)
	}
	if err := tx.Commit(); err != nil {
		return Compilation{}, mapWrite("commit workflow compilation", err)
	}
	return value, nil
}

func (r *Repository) GetCompilation(
	ctx context.Context,
	workspaceID, capabilityID, compilationID string,
) (Compilation, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(compilationID) {
		return Compilation{}, ErrInvalid
	}
	value, err := scanCompilation(r.db.QueryRowContext(ctx, `
		SELECT `+compilationColumns+`
		FROM workflow_compilations wc
		JOIN capabilities c
		  ON c.workspace_id=wc.workspace_id AND c.id=wc.capability_id
		WHERE wc.workspace_id=$1 AND wc.capability_id=$2 AND wc.id=$3
		 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID, compilationID))
	return value, mapRead("get workflow compilation", err)
}

func (r *Repository) GetCurrentValidCompilation(
	ctx context.Context,
	workspaceID, capabilityID, compilationID string,
) (Compilation, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(compilationID) {
		return Compilation{}, ErrInvalid
	}
	value, err := scanCompilation(r.db.QueryRowContext(ctx, `
		SELECT `+compilationColumns+`
		FROM workflow_compilations wc
		JOIN workflows w
		  ON w.workspace_id=wc.workspace_id AND w.capability_id=wc.capability_id
		JOIN workflow_drafts d
		  ON d.workspace_id=w.workspace_id AND d.capability_id=w.capability_id
		 AND d.id=w.current_draft_id
		JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE wc.workspace_id=$1 AND wc.capability_id=$2 AND wc.id=$3
		 AND wc.status='VALID' AND wc.draft_id=d.id
		 AND wc.draft_version=d.draft_version AND wc.graph_hash=d.graph_hash
		 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID, compilationID))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := r.GetCompilation(ctx, workspaceID, capabilityID, compilationID); getErr == nil {
			return Compilation{}, ErrConflict
		} else if !errors.Is(getErr, ErrNotFound) {
			return Compilation{}, getErr
		}
		return Compilation{}, ErrNotFound
	}
	if err != nil {
		return Compilation{}, fmt.Errorf("get current valid workflow compilation: %w", err)
	}
	return value, nil
}

func (r *Repository) CreateTrialRun(
	ctx context.Context,
	workspaceID, capabilityID, compilationID string,
	input TrialRunCreate,
) (TrialRun, error) {
	input = normalizeTrialRunCreate(input)
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(compilationID) ||
		!validTrialRunCreate(input) {
		return TrialRun{}, ErrInvalid
	}
	if r.supportsTrialExecutions {
		return r.createTrialRunWithExecution(ctx, workspaceID, capabilityID, compilationID, input)
	}
	value, err := scanTrialRun(r.db.QueryRowContext(ctx, `
		INSERT INTO workflow_trial_runs AS tr(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,
		 input_hash,started_by
		)
		SELECT $4,wc.workspace_id,wc.capability_id,wc.id,$5,'RUNNING',$6,$7
		FROM workflow_compilations wc
		JOIN workflows w
		  ON w.workspace_id=wc.workspace_id AND w.capability_id=wc.capability_id
		JOIN workflow_drafts d
		  ON d.workspace_id=w.workspace_id AND d.capability_id=w.capability_id
		 AND d.id=w.current_draft_id
		JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE wc.workspace_id=$1 AND wc.capability_id=$2 AND wc.id=$3
		 AND wc.status='VALID' AND wc.draft_id=d.id
		 AND wc.draft_version=d.draft_version AND wc.graph_hash=d.graph_hash
		 AND c.deleted_at IS NULL
		RETURNING `+trialRunColumns,
		workspaceID, capabilityID, compilationID, input.ID, input.ExecutionID,
		input.InputHash, input.StartedBy))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := r.GetCompilation(ctx, workspaceID, capabilityID, compilationID); getErr == nil {
			return TrialRun{}, ErrConflict
		} else if !errors.Is(getErr, ErrNotFound) {
			return TrialRun{}, getErr
		}
		return TrialRun{}, ErrNotFound
	}
	if err != nil {
		return TrialRun{}, mapWrite("create workflow trial run", err)
	}
	return value, nil
}

func (r *Repository) createTrialRunWithExecution(
	ctx context.Context,
	workspaceID, capabilityID, compilationID string,
	input TrialRunCreate,
) (TrialRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return TrialRun{}, fmt.Errorf("begin workflow trial transaction: %w", err)
	}
	defer tx.Rollback()
	var current bool
	if err := tx.QueryRowContext(ctx, `
		SELECT true
		FROM workflow_compilations wc
		JOIN workflows w
		  ON w.workspace_id=wc.workspace_id AND w.capability_id=wc.capability_id
		JOIN workflow_drafts d
		  ON d.workspace_id=w.workspace_id AND d.capability_id=w.capability_id
		 AND d.id=w.current_draft_id
		JOIN capabilities c
		  ON c.workspace_id=w.workspace_id AND c.id=w.capability_id
		WHERE wc.workspace_id=$1 AND wc.capability_id=$2 AND wc.id=$3
		 AND wc.status='VALID' AND wc.draft_id=d.id
		 AND wc.draft_version=d.draft_version AND wc.graph_hash=d.graph_hash
		 AND c.deleted_at IS NULL
		FOR SHARE OF wc,w,d,c
	`, workspaceID, capabilityID, compilationID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		if _, getErr := r.GetCompilation(ctx, workspaceID, capabilityID, compilationID); getErr == nil {
			return TrialRun{}, ErrConflict
		} else if !errors.Is(getErr, ErrNotFound) {
			return TrialRun{}, getErr
		}
		return TrialRun{}, ErrNotFound
	} else if err != nil {
		return TrialRun{}, fmt.Errorf("lock workflow compilation for trial: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_executions(
		 id,workspace_id,workflow_id,revision_id,compilation_id,agent_run_id,
		 trigger_type,triggered_by_type,triggered_by_id,trace_id,status,
		 snapshot_schema_version,authorization_snapshot,input_summary
		) VALUES($1,$2,$3,NULL,$4,NULL,'TRIAL','USER',$5,$6,'RUNNING',
		 'workflow-trial.v1','{}',$7)
	`, input.ExecutionID, workspaceID, capabilityID, compilationID, input.StartedBy,
		"workflow-trial/"+input.ExecutionID, []byte(`{"inputHash":"`+input.InputHash+`"}`)); err != nil {
		return TrialRun{}, mapWrite("create workflow trial execution", err)
	}
	value, err := scanTrialRun(tx.QueryRowContext(ctx, `
		INSERT INTO workflow_trial_runs AS tr(
		 id,workspace_id,capability_id,compilation_id,execution_id,status,
		 input_hash,started_by
		) VALUES($1,$2,$3,$4,$5,'RUNNING',$6,$7)
		RETURNING `+trialRunColumns,
		input.ID, workspaceID, capabilityID, compilationID, input.ExecutionID,
		input.InputHash, input.StartedBy))
	if err != nil {
		return TrialRun{}, mapWrite("create workflow trial run", err)
	}
	if err := tx.Commit(); err != nil {
		return TrialRun{}, mapWrite("commit workflow trial start", err)
	}
	return value, nil
}

func (r *Repository) CompleteTrialRun(
	ctx context.Context,
	workspaceID, capabilityID, trialID, status string,
) (TrialRun, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	capabilityID = strings.TrimSpace(capabilityID)
	trialID = strings.TrimSpace(trialID)
	status = strings.ToUpper(strings.TrimSpace(status))
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(trialID) ||
		(status != "SUCCEEDED" && status != "FAILED" && status != "CANCELLED") {
		return TrialRun{}, ErrInvalid
	}
	if r.supportsTrialExecutions {
		return r.completeTrialRunWithExecution(ctx, workspaceID, capabilityID, trialID, status)
	}
	value, err := scanTrialRun(r.db.QueryRowContext(ctx, `
		UPDATE workflow_trial_runs AS tr
		SET status=$4,finished_at=clock_timestamp()
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND status='RUNNING'
		RETURNING `+trialRunColumns,
		workspaceID, capabilityID, trialID, status))
	if errors.Is(err, sql.ErrNoRows) {
		return TrialRun{}, r.classifyTrialWrite(ctx, workspaceID, capabilityID, trialID)
	}
	if err != nil {
		return TrialRun{}, mapWrite("complete workflow trial run", err)
	}
	return value, nil
}

func (r *Repository) completeTrialRunWithExecution(
	ctx context.Context,
	workspaceID, capabilityID, trialID, status string,
) (TrialRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return TrialRun{}, fmt.Errorf("begin workflow trial completion: %w", err)
	}
	defer tx.Rollback()
	var executionID string
	if err := tx.QueryRowContext(ctx, `
		SELECT execution_id FROM workflow_trial_runs
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND status='RUNNING'
		FOR UPDATE
	`, workspaceID, capabilityID, trialID).Scan(&executionID); errors.Is(err, sql.ErrNoRows) {
		return TrialRun{}, r.classifyTrialWrite(ctx, workspaceID, capabilityID, trialID)
	} else if err != nil {
		return TrialRun{}, fmt.Errorf("lock workflow trial completion: %w", err)
	}
	var errorCode any
	if status == "FAILED" {
		errorCode = "WORKFLOW_TRIAL_FAILED"
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE workflow_executions
		SET status=$3,output_summary='{}',error_code=$4,
		 finished_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
	`, workspaceID, executionID, status, errorCode)
	if err != nil {
		return TrialRun{}, mapWrite("complete workflow trial execution", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return TrialRun{}, ErrConflict
	}
	value, err := scanTrialRun(tx.QueryRowContext(ctx, `
		UPDATE workflow_trial_runs AS tr
		SET status=$4,finished_at=clock_timestamp()
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND status='RUNNING'
		RETURNING `+trialRunColumns,
		workspaceID, capabilityID, trialID, status))
	if err != nil {
		return TrialRun{}, mapWrite("complete workflow trial run", err)
	}
	if err := tx.Commit(); err != nil {
		return TrialRun{}, mapWrite("commit workflow trial completion", err)
	}
	return value, nil
}

func (r *Repository) GetTrialRun(
	ctx context.Context,
	workspaceID, capabilityID, trialID string,
) (TrialRun, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(trialID) {
		return TrialRun{}, ErrInvalid
	}
	value, err := scanTrialRun(r.db.QueryRowContext(ctx, `
		SELECT `+trialRunColumns+`
		FROM workflow_trial_runs tr
		JOIN capabilities c
		  ON c.workspace_id=tr.workspace_id AND c.id=tr.capability_id
		WHERE tr.workspace_id=$1 AND tr.capability_id=$2 AND tr.id=$3
		 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID, trialID))
	return value, mapRead("get workflow trial run", err)
}

func (r *Repository) GetLatestSuccessfulTrialRun(
	ctx context.Context,
	workspaceID, capabilityID, compilationID string,
) (TrialRun, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(compilationID) {
		return TrialRun{}, ErrInvalid
	}
	value, err := scanTrialRun(r.db.QueryRowContext(ctx, `
		SELECT `+trialRunColumns+`
		FROM workflow_trial_runs tr
		JOIN capabilities c
		  ON c.workspace_id=tr.workspace_id AND c.id=tr.capability_id
		WHERE tr.workspace_id=$1 AND tr.capability_id=$2 AND tr.compilation_id=$3
		 AND tr.status='SUCCEEDED' AND c.deleted_at IS NULL
		ORDER BY tr.started_at DESC,tr.id DESC
		LIMIT 1
	`, workspaceID, capabilityID, compilationID))
	return value, mapRead("get latest successful workflow trial run", err)
}

func (r *Repository) GetActiveRevisionSourceCompilation(
	ctx context.Context,
	workspaceID, capabilityID, revisionID string,
) (string, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(revisionID) {
		return "", ErrInvalid
	}
	var compilationID string
	err := r.db.QueryRowContext(ctx, `
		SELECT wr.source_compilation_id
		FROM workflow_revisions wr
		JOIN capabilities c
		  ON c.workspace_id=wr.workspace_id AND c.id=wr.capability_id
		WHERE wr.workspace_id=$1 AND wr.capability_id=$2 AND wr.id=$3
		 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID, revisionID).Scan(&compilationID)
	return compilationID, mapRead("get active workflow revision source", err)
}

// GetReleaseIDForRevision resolves the capability_releases row published for a
// workflow revision (source_type=WORKFLOW_REVISION). Used by production Approval HITL.
func (r *Repository) GetReleaseIDForRevision(
	ctx context.Context,
	workspaceID, capabilityID, revisionID string,
) (string, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(revisionID) {
		return "", ErrInvalid
	}
	var releaseID string
	err := r.db.QueryRowContext(ctx, `
		SELECT cr.id
		FROM capability_releases cr
		JOIN capabilities c
		  ON c.workspace_id=cr.workspace_id AND c.id=cr.capability_id
		WHERE cr.workspace_id=$1 AND cr.capability_id=$2
		  AND cr.source_type='WORKFLOW_REVISION' AND cr.source_id=$3
		  AND cr.retired_at IS NULL AND c.deleted_at IS NULL
		ORDER BY cr.release_no DESC, cr.id DESC
		LIMIT 1
	`, workspaceID, capabilityID, revisionID).Scan(&releaseID)
	return releaseID, mapRead("get capability release for workflow revision", err)
}

func (r *Repository) classifyWorkflowWrite(ctx context.Context, workspaceID, capabilityID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM capabilities
		 WHERE workspace_id=$1 AND id=$2 AND kind='WORKFLOW' AND deleted_at IS NULL)
	`, workspaceID, capabilityID).Scan(&exists); err != nil {
		return fmt.Errorf("classify workflow write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func (r *Repository) classifyDraftWrite(ctx context.Context, workspaceID, capabilityID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
		 SELECT 1 FROM workflow_drafts d JOIN capabilities c
		  ON c.workspace_id=d.workspace_id AND c.id=d.capability_id
		 WHERE d.workspace_id=$1 AND d.capability_id=$2 AND c.deleted_at IS NULL
		)
	`, workspaceID, capabilityID).Scan(&exists); err != nil {
		return fmt.Errorf("classify workflow draft write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func (r *Repository) classifyTrialWrite(
	ctx context.Context,
	workspaceID, capabilityID, trialID string,
) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
		 SELECT 1 FROM workflow_trial_runs tr JOIN capabilities c
		  ON c.workspace_id=tr.workspace_id AND c.id=tr.capability_id
		 WHERE tr.workspace_id=$1 AND tr.capability_id=$2 AND tr.id=$3
		  AND c.deleted_at IS NULL
		)
	`, workspaceID, capabilityID, trialID).Scan(&exists); err != nil {
		return fmt.Errorf("classify workflow trial write: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

type rowScanner interface{ Scan(...any) error }

func scanWorkflow(row rowScanner) (Workflow, error) {
	var value Workflow
	err := row.Scan(
		&value.CapabilityID, &value.WorkspaceID, &value.CurrentDraftID,
		&value.ActiveRevisionID, &value.LatestCompilationID,
		&value.Name, &value.Slug, &value.Description, &value.Status,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.NodeCount, &value.EdgeCount, &value.DeletedAt,
	)
	return value, err
}

func scanDraft(row rowScanner) (Draft, error) {
	var value Draft
	var graph []byte
	if err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.DraftVersion,
		&value.SchemaVersion, &graph, &value.GraphHash, &value.UpdatedBy,
		&value.UpdatedAt, &value.LockVersion,
	); err != nil {
		return Draft{}, err
	}
	canonical, _, err := canonicalGraph(graph)
	if err != nil {
		return Draft{}, fmt.Errorf("canonicalize stored workflow graph: %w", err)
	}
	value.Graph = canonical
	return value, nil
}

func scanCompilation(row rowScanner) (Compilation, error) {
	var value Compilation
	var spec, plan, issues []byte
	if err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.DraftID,
		&value.DraftVersion, &value.GraphHash, &value.CompilerVersion, &value.Status,
		&spec, &plan, &issues, &value.PlanHash, &value.CompiledBy, &value.CompiledAt,
	); err != nil {
		return Compilation{}, err
	}
	var err error
	if value.Spec, _, err = canonicalJSON(spec, "object"); err != nil {
		return Compilation{}, fmt.Errorf("canonicalize stored workflow spec: %w", err)
	}
	if value.Plan, _, err = canonicalJSON(plan, "object"); err != nil {
		return Compilation{}, fmt.Errorf("canonicalize stored workflow plan: %w", err)
	}
	if value.Issues, _, err = canonicalJSON(issues, "array"); err != nil {
		return Compilation{}, fmt.Errorf("canonicalize stored workflow issues: %w", err)
	}
	return value, nil
}

func scanTrialRun(row rowScanner) (TrialRun, error) {
	var value TrialRun
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.CompilationID,
		&value.ExecutionID, &value.Status, &value.InputHash, &value.StartedBy,
		&value.StartedAt, &value.FinishedAt,
	)
	return value, err
}

func normalizeCreate(input CreateInput) CreateInput {
	input.CapabilityID = strings.TrimSpace(input.CapabilityID)
	input.DraftID = strings.TrimSpace(input.DraftID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description = strings.TrimSpace(input.Description)
	input.SchemaVersion = strings.TrimSpace(input.SchemaVersion)
	input.Graph = append(json.RawMessage(nil), input.Graph...)
	input.CreatedBy = strings.TrimSpace(input.CreatedBy)
	return input
}

func validCreate(input CreateInput) bool {
	return validUUID(input.CapabilityID) && validUUID(input.DraftID) &&
		validUUID(input.WorkspaceID) && validUUID(input.CreatedBy) &&
		input.Name != "" && input.Slug != "" && input.SchemaVersion != ""
}

func normalizeMetadata(input MetadataUpdate) MetadataUpdate {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	return input
}

func validMetadata(input MetadataUpdate) bool {
	return input.Name != "" && input.Slug != "" && validUUID(input.UpdatedBy) &&
		(input.Status == "ACTIVE" || input.Status == "DISABLED") && input.ExpectedLockVersion > 0
}

func normalizeDraftUpdate(input DraftUpdate) DraftUpdate {
	input.SchemaVersion = strings.TrimSpace(input.SchemaVersion)
	input.Graph = append(json.RawMessage(nil), input.Graph...)
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	return input
}

func validDraftUpdate(input DraftUpdate) bool {
	return input.SchemaVersion != "" && validUUID(input.UpdatedBy) &&
		input.ExpectedDraftVersion > 0 && input.ExpectedLockVersion > 0
}

func normalizeCompilationCreate(input CompilationCreate) CompilationCreate {
	input.ID = strings.TrimSpace(input.ID)
	input.CompilerVersion = strings.TrimSpace(input.CompilerVersion)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	input.Spec = append(json.RawMessage(nil), input.Spec...)
	input.Plan = append(json.RawMessage(nil), input.Plan...)
	input.Issues = append(json.RawMessage(nil), input.Issues...)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.CompiledBy = strings.TrimSpace(input.CompiledBy)
	return input
}

func validCompilationCreate(draft Draft, input CompilationCreate) bool {
	return validUUID(draft.ID) && validUUID(draft.WorkspaceID) && validUUID(draft.CapabilityID) &&
		draft.DraftVersion > 0 && len(draft.GraphHash) == 64 &&
		validUUID(input.ID) && validUUID(input.CompiledBy) && input.CompilerVersion != "" &&
		(input.Status == "VALID" || input.Status == "INVALID" || input.Status == "FAILED") &&
		jsonKind(input.Spec, "object") && jsonKind(input.Plan, "object") &&
		jsonKind(input.Issues, "array") && len(input.PlanHash) == 64
}

func normalizeTrialRunCreate(input TrialRunCreate) TrialRunCreate {
	input.ID = strings.TrimSpace(input.ID)
	input.ExecutionID = strings.TrimSpace(input.ExecutionID)
	input.InputHash = strings.ToLower(strings.TrimSpace(input.InputHash))
	input.StartedBy = strings.TrimSpace(input.StartedBy)
	return input
}

func validTrialRunCreate(input TrialRunCreate) bool {
	return validUUID(input.ID) && validUUID(input.ExecutionID) &&
		len(input.InputHash) == 64 && validUUID(input.StartedBy)
}

func jsonKind(value json.RawMessage, expected string) bool {
	_, _, err := canonicalJSON(value, expected)
	return err == nil
}

func canonicalGraph(value json.RawMessage) (json.RawMessage, string, error) {
	return canonicalJSON(value, "object")
}

func canonicalJSON(value json.RawMessage, expected string) (json.RawMessage, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil || decoded == nil {
		return nil, "", ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, "", ErrInvalid
	}
	switch expected {
	case "object":
		if _, ok := decoded.(map[string]any); !ok {
			return nil, "", ErrInvalid
		}
	case "array":
		if _, ok := decoded.([]any); !ok {
			return nil, "", ErrInvalid
		}
	default:
		return nil, "", ErrInvalid
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

func validUUID(value string) bool { _, err := uuid.Parse(strings.TrimSpace(value)); return err == nil }

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
	if err == nil {
		return nil
	}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		if databaseError.Code == "23505" {
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
		if databaseError.Code.Class() == "23" {
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
