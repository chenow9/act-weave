package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

const agentRunColumns = `
	ar.id,ar.workspace_id,ar.session_id,ar.agent_id,ar.status,ar.trigger_type,
	ar.triggered_by_type,ar.triggered_by_id,ar.trace_id,ar.model_snapshot,
	ar.capability_snapshot,ar.context_policy_snapshot,ar.snapshot_schema_version,
	ar.authorization_snapshot,ar.input_summary,ar.output_summary,ar.error_code,
	ar.started_at,ar.finished_at,ar.lock_version,ar.principal_snapshot_version,
	ar.subject_type,ar.subject_id,ar.client_id,ar.grant_id,ar.grant_version,
	ar.agent_policy_version
`

const agentRunStepColumns = `
	ars.id,ars.workspace_id,ars.run_id,ars.sequence_no,ars.step_type,ars.status,
	ars.capability_release_id,ars.input_summary,ars.output_summary,ars.raw_object_id,
	ars.raw_sha256,ars.raw_length,ars.started_at,ars.finished_at,ars.error_code
`

const workflowExecutionColumns = `
	we.id,we.workspace_id,we.workflow_id,we.revision_id,we.agent_run_id,
	we.trigger_type,we.triggered_by_type,we.triggered_by_id,we.trace_id,we.status,
	we.snapshot_schema_version,we.authorization_snapshot,we.input_summary,
	we.output_summary,we.error_code,we.started_at,we.finished_at,we.lock_version,
	we.principal_snapshot_version,we.subject_type,we.subject_id,we.client_id,
	we.grant_id,we.grant_version,we.agent_policy_version
`

const executionStepColumns = `
	es.id,es.workspace_id,es.execution_id,es.node_id,es.node_type,es.sequence_no,
	es.status,es.input_summary,es.output_summary,es.raw_object_id,es.started_at,
	es.finished_at,es.error_code
`

type RunRepository struct{ db *sql.DB }

func NewRunRepository(db *sql.DB) (*RunRepository, error) {
	if db == nil {
		return nil, errors.New("run repository database is required")
	}
	return &RunRepository{db: db}, nil
}

func (r *RunRepository) StartAgentRun(
	ctx context.Context,
	input StartAgentRunInput,
) (AgentRun, error) {
	return startAgentRun(ctx, r.db, input)
}

func (r *RunRepository) StartAgentRunInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	input StartAgentRunInput,
) (AgentRun, error) {
	if tx == nil {
		return AgentRun{}, ErrRunInvalid
	}
	return startAgentRun(ctx, tx, input)
}

type runQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func startAgentRun(
	ctx context.Context,
	queryer runQueryRower,
	input StartAgentRunInput,
) (AgentRun, error) {
	input = normalizeStartAgentRun(input)
	model, modelErr := canonicalRunObject(input.Snapshots.Model)
	capabilities, capabilityErr := canonicalRunObject(input.Snapshots.Capabilities)
	contextPolicy, contextErr := canonicalRunObject(input.Snapshots.ContextPolicy)
	authorization, authorizationErr := canonicalRunObject(input.AuthorizationSnapshot)
	inputSummary, inputErr := canonicalRunObject(input.InputSummary)
	if !validStartAgentRun(input) || errors.Join(
		modelErr, capabilityErr, contextErr, authorizationErr, inputErr,
	) != nil {
		return AgentRun{}, ErrRunInvalid
	}
	principalSnapshot, authorizationEnvelope, snapshotErr := prepareExecutionPrincipalSnapshot(
		input.WorkspaceID, input.TriggeredByType, input.TriggeredByID,
		input.PrincipalSnapshot, authorization,
	)
	if snapshotErr != nil {
		return AgentRun{}, ErrRunInvalid
	}
	snapshotArguments := executionSnapshotArguments(principalSnapshot)
	value, err := scanAgentRun(queryer.QueryRowContext(ctx, `
		INSERT INTO agent_runs AS ar(
		 id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		 triggered_by_id,trace_id,model_snapshot,capability_snapshot,
		 context_policy_snapshot,snapshot_schema_version,authorization_snapshot,
		 input_summary,principal_snapshot_version,subject_type,subject_id,client_id,
		 grant_id,grant_version,agent_policy_version
		) VALUES($1,$2,$3,$4,'RUNNING',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
		 $15,$16,$17,$18,$19,$20,$21)
		RETURNING `+agentRunColumns,
		input.ID, input.WorkspaceID, runNullableString(input.SessionID), input.AgentID,
		input.TriggerType, input.TriggeredByType, input.TriggeredByID, input.TraceID,
		[]byte(model), []byte(capabilities), []byte(contextPolicy),
		input.Snapshots.SchemaVersion, []byte(authorizationEnvelope), []byte(inputSummary),
		snapshotArguments[0], snapshotArguments[1], snapshotArguments[2], snapshotArguments[3],
		snapshotArguments[4], snapshotArguments[5], snapshotArguments[6]))
	if err != nil {
		return AgentRun{}, mapRunWrite("start agent run", err)
	}
	return value, nil
}

func (r *RunRepository) GetAgentRun(
	ctx context.Context,
	workspaceID, runID string,
) (AgentRun, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) {
		return AgentRun{}, ErrRunInvalid
	}
	value, err := scanAgentRun(r.db.QueryRowContext(ctx, `
		SELECT `+agentRunColumns+` FROM agent_runs ar
		WHERE ar.workspace_id=$1 AND ar.id=$2
	`, workspaceID, runID))
	return value, mapRunRead("get agent run", err)
}

// GetAgentRunInTransaction reads a Run through a caller-owned transaction.
// Protocol application services use this after mutating a Run so the emitted
// snapshot is guaranteed to describe the state committed with the event.
func (r *RunRepository) GetAgentRunInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, runID string,
) (AgentRun, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if r == nil || tx == nil || !invocationValidUUID(workspaceID) ||
		!invocationValidUUID(runID) {
		return AgentRun{}, ErrRunInvalid
	}
	value, err := scanAgentRun(tx.QueryRowContext(ctx, `
		SELECT `+agentRunColumns+` FROM agent_runs ar
		WHERE ar.workspace_id=$1 AND ar.id=$2
	`, workspaceID, runID))
	return value, mapRunRead("get Agent Run in transaction", err)
}

// ListAgentRunsForConversation is the scoped read used by the public AAP
// Conversation projection. All three parent dimensions are predicates so an
// accidental Conversation/Agent mismatch cannot silently return another
// Agent's execution history.
func (r *RunRepository) ListAgentRunsForConversation(
	ctx context.Context,
	workspaceID, agentID, conversationID string,
	limit int,
) ([]AgentRun, error) {
	workspaceID, agentID = strings.TrimSpace(workspaceID), strings.TrimSpace(agentID)
	conversationID = strings.TrimSpace(conversationID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(agentID) ||
		!invocationValidUUID(conversationID) || limit < 1 || limit > 100 {
		return nil, ErrRunInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+agentRunColumns+` FROM agent_runs ar
		WHERE ar.workspace_id=$1 AND ar.agent_id=$2 AND ar.session_id=$3
		ORDER BY ar.started_at DESC,ar.id DESC
		LIMIT $4
	`, workspaceID, agentID, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list Agent Runs for Conversation: %w", err)
	}
	defer rows.Close()
	values := make([]AgentRun, 0)
	for rows.Next() {
		value, scanErr := scanAgentRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan Agent Run for Conversation: %w", scanErr)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *RunRepository) ListAgentRunSteps(
	ctx context.Context,
	workspaceID, runID string,
) ([]AgentRunStep, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(runID) {
		return nil, ErrRunInvalid
	}
	if _, err := r.GetAgentRun(ctx, workspaceID, runID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+agentRunStepColumns+` FROM agent_run_steps ars
		WHERE ars.workspace_id=$1 AND ars.run_id=$2
		ORDER BY ars.sequence_no,ars.id
	`, workspaceID, runID)
	if err != nil {
		return nil, fmt.Errorf("list agent run steps: %w", err)
	}
	defer rows.Close()
	values := make([]AgentRunStep, 0)
	for rows.Next() {
		value, err := scanAgentRunStep(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent run step: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *RunRepository) TransitionAgentRun(
	ctx context.Context,
	workspaceID, runID string,
	transition RunTransition,
) (AgentRun, error) {
	return r.transitionAgentRun(ctx, r.db, workspaceID, runID, transition)
}

func (r *RunRepository) TransitionAgentRunInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, runID string,
	transition RunTransition,
) (AgentRun, error) {
	if tx == nil {
		return AgentRun{}, ErrRunInvalid
	}
	return r.transitionAgentRun(ctx, tx, workspaceID, runID, transition)
}

func (r *RunRepository) transitionAgentRun(
	ctx context.Context,
	queryer runQueryRower,
	workspaceID, runID string,
	transition RunTransition,
) (AgentRun, error) {
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	transition = normalizeRunTransition(transition)
	output, err := canonicalRunObject(transition.OutputSummary)
	if err != nil || !validRunTransition(workspaceID, runID, transition) {
		return AgentRun{}, ErrRunInvalid
	}
	value, err := scanAgentRun(queryer.QueryRowContext(ctx, `
		UPDATE agent_runs ar SET status=$5,output_summary=$6,error_code=$7,
		 finished_at=$8,lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status=$3 AND lock_version=$4
		RETURNING `+agentRunColumns,
		workspaceID, runID, transition.ExpectedStatus, transition.ExpectedLockVersion,
		transition.NewStatus, []byte(output), runNullableString(transition.ErrorCode),
		runFinishedAt(transition.NewStatus)))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRun{}, r.classifyAgentRunWith(ctx, queryer, workspaceID, runID)
	}
	if err != nil {
		return AgentRun{}, mapRunWrite("transition agent run", err)
	}
	return value, nil
}

func (r *RunRepository) AppendAgentRunStep(
	ctx context.Context,
	input AppendAgentRunStepInput,
) (AgentRunStep, error) {
	input = normalizeAgentRunStep(input)
	summary, err := canonicalRunObject(input.InputSummary)
	if err != nil || !validAgentRunStep(input) {
		return AgentRunStep{}, ErrRunInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRunStep{}, fmt.Errorf("begin append agent run step: %w", err)
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM agent_runs WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.RunID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return AgentRunStep{}, ErrRunNotFound
	} else if err != nil {
		return AgentRunStep{}, fmt.Errorf("lock agent run for step append: %w", err)
	}
	if status != "RUNNING" {
		return AgentRunStep{}, ErrRunConflict
	}
	var sequence int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence_no),0)+1 FROM agent_run_steps
		WHERE workspace_id=$1 AND run_id=$2
	`, input.WorkspaceID, input.RunID).Scan(&sequence); err != nil {
		return AgentRunStep{}, fmt.Errorf("allocate agent run step sequence: %w", err)
	}
	value, err := scanAgentRunStep(tx.QueryRowContext(ctx, `
		INSERT INTO agent_run_steps AS ars(
		 id,workspace_id,run_id,sequence_no,step_type,status,
		 capability_release_id,input_summary
		) VALUES($1,$2,$3,$4,$5,'RUNNING',$6,$7)
		RETURNING `+agentRunStepColumns,
		input.ID, input.WorkspaceID, input.RunID, sequence, input.StepType,
		runNullableString(input.CapabilityReleaseID), []byte(summary)))
	if err != nil {
		return AgentRunStep{}, mapRunWrite("append agent run step", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentRunStep{}, mapRunWrite("commit agent run step", err)
	}
	return value, nil
}

func (r *RunRepository) TransitionAgentRunStep(
	ctx context.Context,
	workspaceID, stepID string,
	transition StepTransition,
) (AgentRunStep, error) {
	return r.transitionAgentRunStep(ctx, r.db, workspaceID, stepID, transition)
}

func (r *RunRepository) TransitionAgentRunStepInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, stepID string,
	transition StepTransition,
) (AgentRunStep, error) {
	if tx == nil {
		return AgentRunStep{}, ErrRunInvalid
	}
	return r.transitionAgentRunStep(ctx, tx, workspaceID, stepID, transition)
}

func (r *RunRepository) transitionAgentRunStep(
	ctx context.Context,
	queryer runQueryRower,
	workspaceID, stepID string,
	transition StepTransition,
) (AgentRunStep, error) {
	workspaceID, stepID = strings.TrimSpace(workspaceID), strings.TrimSpace(stepID)
	transition = normalizeStepTransition(transition)
	output, err := canonicalRunObject(transition.OutputSummary)
	if err != nil || !validStepTransition(workspaceID, stepID, transition) {
		return AgentRunStep{}, ErrRunInvalid
	}
	// finished_at uses GREATEST(started_at, now()) so sub-ms clock skew between
	// app and DB cannot violate agent_run_steps_finished_at_check (finished >= started).
	value, err := scanAgentRunStep(queryer.QueryRowContext(ctx, `
		UPDATE agent_run_steps ars SET status=$4,output_summary=$5,raw_object_id=$6,
		 raw_sha256=$7,raw_length=$8,error_code=$9,
		 finished_at=CASE WHEN $4 IN ('SUCCEEDED','FAILED','SKIPPED','CANCELLED')
		   THEN GREATEST(ars.started_at, NOW()) ELSE NULL END
		WHERE workspace_id=$1 AND id=$2 AND status=$3
		RETURNING `+agentRunStepColumns,
		workspaceID, stepID, transition.ExpectedStatus, transition.NewStatus,
		[]byte(output), runNullableString(transition.RawObjectID),
		runNullableString(transition.RawSHA256), runNullableInt64(transition.RawLength),
		runNullableString(transition.ErrorCode)))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRunStep{}, r.classifyAgentRunStep(ctx, workspaceID, stepID)
	}
	if err != nil {
		return AgentRunStep{}, mapRunWrite("transition agent run step", err)
	}
	return value, nil
}

func (r *RunRepository) StartWorkflowExecution(
	ctx context.Context,
	input StartWorkflowExecutionInput,
) (WorkflowExecution, error) {
	input = normalizeStartWorkflowExecution(input)
	authorization, authorizationErr := canonicalRunObject(input.AuthorizationSnapshot)
	inputSummary, inputErr := canonicalRunObject(input.InputSummary)
	if !validStartWorkflowExecution(input) || errors.Join(authorizationErr, inputErr) != nil {
		return WorkflowExecution{}, ErrRunInvalid
	}
	principalSnapshot, authorizationEnvelope, snapshotErr := prepareExecutionPrincipalSnapshot(
		input.WorkspaceID, input.TriggeredByType, input.TriggeredByID,
		input.PrincipalSnapshot, authorization,
	)
	if snapshotErr != nil {
		return WorkflowExecution{}, ErrRunInvalid
	}
	snapshotArguments := executionSnapshotArguments(principalSnapshot)
	value, err := scanWorkflowExecution(r.db.QueryRowContext(ctx, `
		INSERT INTO workflow_executions AS we(
		 id,workspace_id,workflow_id,revision_id,agent_run_id,trigger_type,
		 triggered_by_type,triggered_by_id,trace_id,status,snapshot_schema_version,
		 authorization_snapshot,input_summary,principal_snapshot_version,
		 subject_type,subject_id,client_id,grant_id,grant_version,agent_policy_version
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'RUNNING',$10,$11,$12,
		 $13,$14,$15,$16,$17,$18,$19)
		RETURNING `+workflowExecutionColumns,
		input.ID, input.WorkspaceID, input.WorkflowID, input.RevisionID,
		runNullableString(input.AgentRunID), input.TriggerType, input.TriggeredByType,
		input.TriggeredByID, input.TraceID, input.SnapshotSchemaVersion,
		[]byte(authorizationEnvelope), []byte(inputSummary),
		snapshotArguments[0], snapshotArguments[1], snapshotArguments[2], snapshotArguments[3],
		snapshotArguments[4], snapshotArguments[5], snapshotArguments[6]))
	if err != nil {
		return WorkflowExecution{}, mapRunWrite("start workflow execution", err)
	}
	return value, nil
}

func (r *RunRepository) GetWorkflowExecution(
	ctx context.Context,
	workspaceID, executionID string,
) (WorkflowExecution, error) {
	workspaceID, executionID = strings.TrimSpace(workspaceID), strings.TrimSpace(executionID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(executionID) {
		return WorkflowExecution{}, ErrRunInvalid
	}
	value, err := scanWorkflowExecution(r.db.QueryRowContext(ctx, `
		SELECT `+workflowExecutionColumns+` FROM workflow_executions we
		WHERE we.workspace_id=$1 AND we.id=$2
	`, workspaceID, executionID))
	return value, mapRunRead("get workflow execution", err)
}

func (r *RunRepository) ListWorkflowExecutions(
	ctx context.Context,
	workspaceID string,
	filter WorkflowExecutionFilter,
) ([]WorkflowExecution, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	filter.Status = strings.ToUpper(strings.TrimSpace(filter.Status))
	filter.TraceID = strings.TrimSpace(filter.TraceID)
	filter.WorkflowID = strings.TrimSpace(filter.WorkflowID)
	if !invocationValidUUID(workspaceID) ||
		(filter.WorkflowID != "" && !invocationValidUUID(filter.WorkflowID)) ||
		(filter.Status != "" && !validRunStatus(filter.Status)) ||
		(filter.StartedAfter != nil && filter.StartedAfter.IsZero()) ||
		(filter.StartedBefore != nil && filter.StartedBefore.IsZero()) ||
		(filter.StartedAfter != nil && filter.StartedBefore != nil && !filter.StartedAfter.Before(*filter.StartedBefore)) {
		return nil, ErrRunInvalid
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+workflowExecutionColumns+` FROM workflow_executions we
		WHERE we.workspace_id=$1
		 AND ($2='' OR we.status=$2)
		 AND ($3='' OR we.trace_id=$3)
		 AND ($4='' OR we.workflow_id=NULLIF($4,'')::UUID)
		 AND ($5::TIMESTAMPTZ IS NULL OR we.started_at >= $5)
		 AND ($6::TIMESTAMPTZ IS NULL OR we.started_at < $6)
		ORDER BY we.started_at DESC,we.id DESC
		LIMIT $7
	`, workspaceID, filter.Status, filter.TraceID, filter.WorkflowID,
		nullableRunTime(filter.StartedAfter), nullableRunTime(filter.StartedBefore), filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list workflow executions: %w", err)
	}
	defer rows.Close()
	values := make([]WorkflowExecution, 0)
	for rows.Next() {
		value, err := scanWorkflowExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow execution: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *RunRepository) ListExecutionSteps(
	ctx context.Context,
	workspaceID, executionID string,
) ([]ExecutionStep, error) {
	workspaceID, executionID = strings.TrimSpace(workspaceID), strings.TrimSpace(executionID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(executionID) {
		return nil, ErrRunInvalid
	}
	if _, err := r.GetWorkflowExecution(ctx, workspaceID, executionID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+executionStepColumns+` FROM execution_steps es
		WHERE es.workspace_id=$1 AND es.execution_id=$2
		ORDER BY es.sequence_no,es.id
	`, workspaceID, executionID)
	if err != nil {
		return nil, fmt.Errorf("list workflow execution steps: %w", err)
	}
	defer rows.Close()
	values := make([]ExecutionStep, 0)
	for rows.Next() {
		value, err := scanExecutionStep(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow execution step: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *RunRepository) TransitionWorkflowExecution(
	ctx context.Context,
	workspaceID, executionID string,
	transition RunTransition,
) (WorkflowExecution, error) {
	return r.transitionWorkflowExecution(ctx, r.db, workspaceID, executionID, transition)
}

func (r *RunRepository) TransitionWorkflowExecutionInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, executionID string,
	transition RunTransition,
) (WorkflowExecution, error) {
	if tx == nil {
		return WorkflowExecution{}, ErrRunInvalid
	}
	return r.transitionWorkflowExecution(ctx, tx, workspaceID, executionID, transition)
}

func (r *RunRepository) transitionWorkflowExecution(
	ctx context.Context,
	queryer runQueryRower,
	workspaceID, executionID string,
	transition RunTransition,
) (WorkflowExecution, error) {
	workspaceID, executionID = strings.TrimSpace(workspaceID), strings.TrimSpace(executionID)
	transition = normalizeRunTransition(transition)
	output, err := canonicalRunObject(transition.OutputSummary)
	if err != nil || !validRunTransition(workspaceID, executionID, transition) {
		return WorkflowExecution{}, ErrRunInvalid
	}
	value, err := scanWorkflowExecution(queryer.QueryRowContext(ctx, `
		UPDATE workflow_executions we SET status=$5,output_summary=$6,error_code=$7,
		 finished_at=$8,lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status=$3 AND lock_version=$4
		RETURNING `+workflowExecutionColumns,
		workspaceID, executionID, transition.ExpectedStatus,
		transition.ExpectedLockVersion, transition.NewStatus, []byte(output),
		runNullableString(transition.ErrorCode), runFinishedAt(transition.NewStatus)))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowExecution{}, r.classifyWorkflowExecution(ctx, workspaceID, executionID)
	}
	if err != nil {
		return WorkflowExecution{}, mapRunWrite("transition workflow execution", err)
	}
	return value, nil
}

func (r *RunRepository) AppendExecutionStep(
	ctx context.Context,
	input AppendExecutionStepInput,
) (ExecutionStep, error) {
	input = normalizeExecutionStep(input)
	summary, err := canonicalRunObject(input.InputSummary)
	if err != nil || !validExecutionStep(input) {
		return ExecutionStep{}, ErrRunInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionStep{}, fmt.Errorf("begin append workflow execution step: %w", err)
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM workflow_executions
		WHERE workspace_id=$1 AND id=$2 FOR UPDATE
	`, input.WorkspaceID, input.ExecutionID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return ExecutionStep{}, ErrRunNotFound
	} else if err != nil {
		return ExecutionStep{}, fmt.Errorf("lock workflow execution for step append: %w", err)
	}
	if status != "RUNNING" {
		return ExecutionStep{}, ErrRunConflict
	}
	var sequence int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence_no),0)+1 FROM execution_steps
		WHERE workspace_id=$1 AND execution_id=$2
	`, input.WorkspaceID, input.ExecutionID).Scan(&sequence); err != nil {
		return ExecutionStep{}, fmt.Errorf("allocate workflow execution step sequence: %w", err)
	}
	value, err := scanExecutionStep(tx.QueryRowContext(ctx, `
		INSERT INTO execution_steps AS es(
		 id,workspace_id,execution_id,node_id,node_type,sequence_no,status,input_summary
		) VALUES($1,$2,$3,$4,$5,$6,'RUNNING',$7)
		RETURNING `+executionStepColumns,
		input.ID, input.WorkspaceID, input.ExecutionID, input.NodeID, input.NodeType,
		sequence, []byte(summary)))
	if err != nil {
		return ExecutionStep{}, mapRunWrite("append workflow execution step", err)
	}
	if err := tx.Commit(); err != nil {
		return ExecutionStep{}, mapRunWrite("commit workflow execution step", err)
	}
	return value, nil
}

func (r *RunRepository) TransitionExecutionStep(
	ctx context.Context,
	workspaceID, stepID string,
	transition StepTransition,
) (ExecutionStep, error) {
	return r.transitionExecutionStep(ctx, r.db, workspaceID, stepID, transition)
}

func (r *RunRepository) TransitionExecutionStepInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, stepID string,
	transition StepTransition,
) (ExecutionStep, error) {
	if tx == nil {
		return ExecutionStep{}, ErrRunInvalid
	}
	return r.transitionExecutionStep(ctx, tx, workspaceID, stepID, transition)
}

func (r *RunRepository) transitionExecutionStep(
	ctx context.Context,
	queryer runQueryRower,
	workspaceID, stepID string,
	transition StepTransition,
) (ExecutionStep, error) {
	workspaceID, stepID = strings.TrimSpace(workspaceID), strings.TrimSpace(stepID)
	transition = normalizeStepTransition(transition)
	output, err := canonicalRunObject(transition.OutputSummary)
	if err != nil || !validStepTransition(workspaceID, stepID, transition) {
		return ExecutionStep{}, ErrRunInvalid
	}
	value, err := scanExecutionStep(queryer.QueryRowContext(ctx, `
		UPDATE execution_steps es SET status=$4,output_summary=$5,raw_object_id=$6,
		 error_code=$7,finished_at=$8
		WHERE workspace_id=$1 AND id=$2 AND status=$3
		RETURNING `+executionStepColumns,
		workspaceID, stepID, transition.ExpectedStatus, transition.NewStatus,
		[]byte(output), runNullableString(transition.RawObjectID),
		runNullableString(transition.ErrorCode), runFinishedAt(transition.NewStatus)))
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionStep{}, r.classifyExecutionStep(ctx, workspaceID, stepID)
	}
	if err != nil {
		return ExecutionStep{}, mapRunWrite("transition workflow execution step", err)
	}
	return value, nil
}

func (r *RunRepository) classifyAgentRun(ctx context.Context, workspaceID, id string) error {
	return r.classifyRunRecord(ctx, "agent_runs", workspaceID, id)
}

func (r *RunRepository) classifyAgentRunWith(
	ctx context.Context,
	queryer runQueryRower,
	workspaceID, id string,
) error {
	var exists bool
	if err := queryer.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM agent_runs WHERE workspace_id=$1 AND id=$2)
	`, workspaceID, id).Scan(&exists); err != nil {
		return fmt.Errorf("classify agent run state: %w", err)
	}
	if !exists {
		return ErrRunNotFound
	}
	return ErrRunConflict
}

func (r *RunRepository) classifyAgentRunStep(ctx context.Context, workspaceID, id string) error {
	return r.classifyRunRecord(ctx, "agent_run_steps", workspaceID, id)
}

func (r *RunRepository) classifyWorkflowExecution(ctx context.Context, workspaceID, id string) error {
	return r.classifyRunRecord(ctx, "workflow_executions", workspaceID, id)
}

func (r *RunRepository) classifyExecutionStep(ctx context.Context, workspaceID, id string) error {
	return r.classifyRunRecord(ctx, "execution_steps", workspaceID, id)
}

func (r *RunRepository) classifyRunRecord(
	ctx context.Context,
	table, workspaceID, id string,
) error {
	var exists bool
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE workspace_id=$1 AND id=$2)", table)
	if err := r.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&exists); err != nil {
		return fmt.Errorf("classify run state: %w", err)
	}
	if !exists {
		return ErrRunNotFound
	}
	return ErrRunConflict
}

func normalizeStartAgentRun(input StartAgentRunInput) StartAgentRunInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.SessionID, input.AgentID = strings.TrimSpace(input.SessionID), strings.TrimSpace(input.AgentID)
	input.TriggerType = strings.TrimSpace(input.TriggerType)
	input.TriggeredByType = strings.TrimSpace(input.TriggeredByType)
	input.TriggeredByID, input.TraceID = strings.TrimSpace(input.TriggeredByID), strings.TrimSpace(input.TraceID)
	input.Snapshots.SchemaVersion = strings.TrimSpace(input.Snapshots.SchemaVersion)
	if input.Snapshots.SchemaVersion == "" {
		input.Snapshots.SchemaVersion = "run.v1"
	}
	return input
}

func validStartAgentRun(input StartAgentRunInput) bool {
	return invocationValidUUID(input.ID) && invocationValidUUID(input.WorkspaceID) &&
		(input.SessionID == "" || invocationValidUUID(input.SessionID)) &&
		invocationValidUUID(input.AgentID) && invocationValidUUID(input.TriggeredByID) &&
		validActorType(input.TriggeredByType) && input.TriggerType != "" &&
		input.TraceID != "" && input.Snapshots.SchemaVersion != ""
}

func normalizeStartWorkflowExecution(input StartWorkflowExecutionInput) StartWorkflowExecutionInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.WorkflowID, input.RevisionID = strings.TrimSpace(input.WorkflowID), strings.TrimSpace(input.RevisionID)
	input.AgentRunID = strings.TrimSpace(input.AgentRunID)
	input.TriggerType, input.TriggeredByType = strings.TrimSpace(input.TriggerType), strings.TrimSpace(input.TriggeredByType)
	input.TriggeredByID, input.TraceID = strings.TrimSpace(input.TriggeredByID), strings.TrimSpace(input.TraceID)
	input.SnapshotSchemaVersion = strings.TrimSpace(input.SnapshotSchemaVersion)
	if input.SnapshotSchemaVersion == "" {
		input.SnapshotSchemaVersion = "run.v1"
	}
	return input
}

func validStartWorkflowExecution(input StartWorkflowExecutionInput) bool {
	return invocationValidUUID(input.ID) && invocationValidUUID(input.WorkspaceID) &&
		invocationValidUUID(input.WorkflowID) && invocationValidUUID(input.RevisionID) &&
		(input.AgentRunID == "" || invocationValidUUID(input.AgentRunID)) &&
		invocationValidUUID(input.TriggeredByID) && validActorType(input.TriggeredByType) &&
		input.TriggerType != "" && input.TraceID != "" && input.SnapshotSchemaVersion != ""
}

func normalizeRunTransition(value RunTransition) RunTransition {
	value.ExpectedStatus, value.NewStatus = strings.TrimSpace(value.ExpectedStatus), strings.TrimSpace(value.NewStatus)
	value.ErrorCode = strings.TrimSpace(value.ErrorCode)
	return value
}

func validRunTransition(workspaceID, id string, value RunTransition) bool {
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(id) ||
		value.ExpectedLockVersion <= 0 || !validRunStatus(value.ExpectedStatus) ||
		!validRunStatus(value.NewStatus) {
		return false
	}
	return (value.NewStatus == "FAILED" && value.ErrorCode != "") ||
		(value.NewStatus != "FAILED" && value.ErrorCode == "")
}

func normalizeAgentRunStep(input AppendAgentRunStepInput) AppendAgentRunStepInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.RunID, input.StepType = strings.TrimSpace(input.RunID), strings.TrimSpace(input.StepType)
	input.CapabilityReleaseID = strings.TrimSpace(input.CapabilityReleaseID)
	return input
}

func validAgentRunStep(input AppendAgentRunStepInput) bool {
	return invocationValidUUID(input.ID) && invocationValidUUID(input.WorkspaceID) &&
		invocationValidUUID(input.RunID) && input.StepType != "" &&
		(input.CapabilityReleaseID == "" || invocationValidUUID(input.CapabilityReleaseID))
}

func normalizeExecutionStep(input AppendExecutionStepInput) AppendExecutionStepInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.ExecutionID = strings.TrimSpace(input.ExecutionID)
	input.NodeID, input.NodeType = strings.TrimSpace(input.NodeID), strings.TrimSpace(input.NodeType)
	return input
}

func validExecutionStep(input AppendExecutionStepInput) bool {
	return invocationValidUUID(input.ID) && invocationValidUUID(input.WorkspaceID) &&
		invocationValidUUID(input.ExecutionID)
}

func normalizeStepTransition(value StepTransition) StepTransition {
	value.ExpectedStatus, value.NewStatus = strings.TrimSpace(value.ExpectedStatus), strings.TrimSpace(value.NewStatus)
	value.RawObjectID, value.ErrorCode = strings.TrimSpace(value.RawObjectID), strings.TrimSpace(value.ErrorCode)
	value.RawSHA256 = strings.ToLower(strings.TrimSpace(value.RawSHA256))
	return value
}

func validStepTransition(workspaceID, id string, value StepTransition) bool {
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(id) ||
		!validStepStatus(value.ExpectedStatus) || !validStepStatus(value.NewStatus) ||
		(value.RawObjectID != "" && (!invocationValidUUID(value.RawObjectID) ||
			!validRunSHA256(value.RawSHA256) || value.RawLength < 1)) ||
		(value.RawObjectID == "" && (value.RawSHA256 != "" || value.RawLength != 0)) {
		return false
	}
	return (value.NewStatus == "FAILED" && value.ErrorCode != "") ||
		(value.NewStatus != "FAILED" && value.ErrorCode == "")
}

func canonicalRunObject(value json.RawMessage) (json.RawMessage, error) {
	canonical, err := canonicalInvocationObject(value)
	if err != nil {
		return nil, ErrRunInvalid
	}
	return canonical, nil
}

func validActorType(value string) bool {
	return value == "USER" || value == "SERVICE_PRINCIPAL" || value == "SYSTEM"
}

func validRunStatus(value string) bool {
	switch value {
	case "PENDING", "RUNNING", "WAITING_CONFIRMATION", "SUCCEEDED", "FAILED", "CANCELLED":
		return true
	default:
		return false
	}
}

func validStepStatus(value string) bool {
	switch value {
	case "QUEUED", "RUNNING", "WAITING_CONFIRMATION", "SUCCEEDED", "FAILED", "SKIPPED", "CANCELLED":
		return true
	default:
		return false
	}
}

func runFinishedAt(status string) any {
	switch status {
	case "SUCCEEDED", "FAILED", "SKIPPED", "CANCELLED":
		return time.Now().UTC()
	default:
		return nil
	}
}

func runNullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func runNullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableRunTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func validRunSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

type runScanner interface{ Scan(...any) error }

func scanAgentRun(scanner runScanner) (AgentRun, error) {
	var value AgentRun
	var sessionID, errorCode sql.NullString
	var subjectType, subjectID, clientID, grantID sql.NullString
	var grantVersion, policyVersion sql.NullInt64
	var finishedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &sessionID, &value.AgentID, &value.Status,
		&value.TriggerType, &value.TriggeredByType, &value.TriggeredByID, &value.TraceID,
		&value.ModelSnapshot, &value.CapabilitySnapshot, &value.ContextPolicySnapshot,
		&value.SnapshotSchemaVersion, &value.AuthorizationSnapshot, &value.InputSummary,
		&value.OutputSummary, &errorCode, &value.StartedAt, &finishedAt, &value.LockVersion,
		&value.PrincipalSnapshotVersion, &subjectType, &subjectID, &clientID, &grantID,
		&grantVersion, &policyVersion,
	)
	if err != nil {
		return AgentRun{}, err
	}
	value.SessionID, value.ErrorCode = sessionID.String, errorCode.String
	value.PrincipalSnapshot, err = scannedExecutionSnapshot(
		value.PrincipalSnapshotVersion, value.WorkspaceID, value.TriggeredByType,
		value.TriggeredByID, subjectType, subjectID, clientID, grantID,
		grantVersion, policyVersion,
	)
	if err != nil {
		return AgentRun{}, err
	}
	if finishedAt.Valid {
		finished := finishedAt.Time
		value.FinishedAt = &finished
	}
	return value, nil
}

func scanAgentRunStep(scanner runScanner) (AgentRunStep, error) {
	var value AgentRunStep
	var releaseID, rawObjectID, rawSHA256, errorCode sql.NullString
	var rawLength sql.NullInt64
	var finishedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.RunID, &value.SequenceNo,
		&value.StepType, &value.Status, &releaseID, &value.InputSummary,
		&value.OutputSummary, &rawObjectID, &rawSHA256, &rawLength,
		&value.StartedAt, &finishedAt, &errorCode,
	)
	if err != nil {
		return AgentRunStep{}, err
	}
	value.CapabilityReleaseID, value.RawObjectID = releaseID.String, rawObjectID.String
	value.RawSHA256, value.RawLength = rawSHA256.String, rawLength.Int64
	value.ErrorCode = errorCode.String
	if finishedAt.Valid {
		finished := finishedAt.Time
		value.FinishedAt = &finished
	}
	return value, nil
}

func scanWorkflowExecution(scanner runScanner) (WorkflowExecution, error) {
	var value WorkflowExecution
	var revisionID, agentRunID, errorCode sql.NullString
	var subjectType, subjectID, clientID, grantID sql.NullString
	var grantVersion, policyVersion sql.NullInt64
	var finishedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.WorkflowID, &revisionID,
		&agentRunID, &value.TriggerType, &value.TriggeredByType, &value.TriggeredByID,
		&value.TraceID, &value.Status, &value.SnapshotSchemaVersion,
		&value.AuthorizationSnapshot, &value.InputSummary, &value.OutputSummary,
		&errorCode, &value.StartedAt, &finishedAt, &value.LockVersion,
		&value.PrincipalSnapshotVersion, &subjectType, &subjectID, &clientID, &grantID,
		&grantVersion, &policyVersion,
	)
	if err != nil {
		return WorkflowExecution{}, err
	}
	value.RevisionID = revisionID.String
	value.AgentRunID, value.ErrorCode = agentRunID.String, errorCode.String
	value.PrincipalSnapshot, err = scannedExecutionSnapshot(
		value.PrincipalSnapshotVersion, value.WorkspaceID, value.TriggeredByType,
		value.TriggeredByID, subjectType, subjectID, clientID, grantID,
		grantVersion, policyVersion,
	)
	if err != nil {
		return WorkflowExecution{}, err
	}
	if finishedAt.Valid {
		finished := finishedAt.Time
		value.FinishedAt = &finished
	}
	return value, nil
}

func scanExecutionStep(scanner runScanner) (ExecutionStep, error) {
	var value ExecutionStep
	var rawObjectID, errorCode sql.NullString
	var finishedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ExecutionID, &value.NodeID,
		&value.NodeType, &value.SequenceNo, &value.Status, &value.InputSummary,
		&value.OutputSummary, &rawObjectID, &value.StartedAt, &finishedAt, &errorCode,
	)
	if err != nil {
		return ExecutionStep{}, err
	}
	value.RawObjectID, value.ErrorCode = rawObjectID.String, errorCode.String
	if finishedAt.Valid {
		finished := finishedAt.Time
		value.FinishedAt = &finished
	}
	return value, nil
}

func mapRunRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRunNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapRunWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505", "40001", "55000":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrRunConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrRunInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
