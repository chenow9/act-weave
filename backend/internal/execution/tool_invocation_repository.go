package execution

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const toolInvocationColumns = `
	ti.id,ti.workspace_id,ti.tool_id,ti.tool_version_id,ti.capability_release_id,
	ti.provider_id,ti.connection_id,ti.execution_lease_id,ti.provider_request_id,
	ti.agent_run_id,ti.workflow_execution_id,ti.execution_step_id,ti.actor_type,
	ti.actor_id,ti.trace_id,ti.idempotency_key,ti.status,ti.input_summary,
	ti.output_summary,ti.raw_object_id,ti.latency_ms,ti.error_code,
	ti.started_at,ti.finished_at,ti.principal_snapshot_version,ti.subject_type,
	ti.subject_id,ti.client_id,ti.grant_id,ti.grant_version,ti.agent_policy_version,
	ti.authorization_snapshot
`

type ToolInvocationRepository struct{ db *sql.DB }

func NewToolInvocationRepository(db *sql.DB) (*ToolInvocationRepository, error) {
	if db == nil {
		return nil, errors.New("tool invocation repository database is required")
	}
	return &ToolInvocationRepository{db: db}, nil
}

func (r *ToolInvocationRepository) Start(
	ctx context.Context,
	input StartToolInvocationInput,
) (StartToolInvocationResult, error) {
	input = normalizeStartToolInvocation(input)
	canonicalInput, err := canonicalInvocationObject(input.InputSummary)
	if err != nil || !validStartToolInvocation(input) {
		return StartToolInvocationResult{}, ErrToolInvocationInvalid
	}
	input.InputSummary = canonicalInput
	principalSnapshot, authorizationEnvelope, snapshotErr := prepareExecutionPrincipalSnapshot(
		input.WorkspaceID, input.ActorType, input.ActorID,
		input.PrincipalSnapshot, input.AuthorizationSnapshot,
	)
	if snapshotErr != nil {
		return StartToolInvocationResult{}, ErrToolInvocationInvalid
	}
	input.PrincipalSnapshot = &principalSnapshot
	input.AuthorizationSnapshot = authorizationEnvelope

	query := `
		INSERT INTO tool_invocations AS ti(
		 id,workspace_id,tool_id,tool_version_id,capability_release_id,provider_id,
		 connection_id,execution_lease_id,agent_run_id,workflow_execution_id,
		 execution_step_id,actor_type,actor_id,trace_id,idempotency_key,status,
		 input_summary,principal_snapshot_version,subject_type,subject_id,client_id,
		 grant_id,grant_version,agent_policy_version,authorization_snapshot
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'RUNNING',$16,
		 $17,$18,$19,$20,$21,$22,$23,$24)
		RETURNING ` + toolInvocationColumns
	arguments := startToolInvocationArguments(input, canonicalInput)
	if input.IdempotencyKey != "" {
		query = strings.Replace(query, "RETURNING ", `
		ON CONFLICT (workspace_id,tool_version_id,idempotency_key)
		WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING `, 1)
	}
	value, err := scanToolInvocation(r.db.QueryRowContext(ctx, query, arguments...))
	if err == nil {
		return StartToolInvocationResult{Invocation: value, Created: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return StartToolInvocationResult{}, mapToolInvocationWrite("start tool invocation", err)
	}
	if input.IdempotencyKey == "" {
		return StartToolInvocationResult{}, ErrToolInvocationConflict
	}
	existing, err := r.getByIdempotencyKey(ctx, input.WorkspaceID, input.ToolVersionID, input.IdempotencyKey)
	if err != nil {
		return StartToolInvocationResult{}, err
	}
	if !sameToolInvocationRequest(existing, input) {
		return StartToolInvocationResult{}, ErrToolInvocationIdempotencyConflict
	}
	return StartToolInvocationResult{Invocation: existing, Created: false}, nil
}

func (r *ToolInvocationRepository) Get(
	ctx context.Context,
	workspaceID, invocationID string,
) (ToolInvocation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	invocationID = strings.TrimSpace(invocationID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(invocationID) {
		return ToolInvocation{}, ErrToolInvocationInvalid
	}
	value, err := scanToolInvocation(r.db.QueryRowContext(ctx, `
		SELECT `+toolInvocationColumns+`
		FROM tool_invocations ti
		WHERE ti.workspace_id=$1 AND ti.id=$2
	`, workspaceID, invocationID))
	return value, mapToolInvocationRead("get tool invocation", err)
}

// ListForExecutionStep returns the ToolInvocation facts represented by child
// tool_call Items of one public workflow_step Item. The immutable invocation
// start order is used only for the relation list; protocol event ordering still
// comes exclusively from the Run stream sequence.
func (r *ToolInvocationRepository) ListForExecutionStep(
	ctx context.Context,
	workspaceID, executionID, stepID string,
) ([]ToolInvocation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	executionID = strings.TrimSpace(executionID)
	stepID = strings.TrimSpace(stepID)
	if r == nil || r.db == nil || !invocationValidUUID(workspaceID) ||
		!invocationValidUUID(executionID) || !invocationValidUUID(stepID) {
		return nil, ErrToolInvocationInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+toolInvocationColumns+`
		FROM tool_invocations ti
		WHERE ti.workspace_id=$1 AND ti.workflow_execution_id=$2
		  AND ti.execution_step_id=$3
		ORDER BY ti.started_at,ti.id
	`, workspaceID, executionID, stepID)
	if err != nil {
		return nil, fmt.Errorf("list tool invocations for execution step: %w", err)
	}
	defer rows.Close()
	values := make([]ToolInvocation, 0)
	for rows.Next() {
		value, scanErr := scanToolInvocation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan tool invocation for execution step: %w", scanErr)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool invocations for execution step: %w", err)
	}
	return values, nil
}

func (r *ToolInvocationRepository) Complete(
	ctx context.Context,
	workspaceID, invocationID string,
	input CompleteToolInvocationInput,
) (ToolInvocation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	invocationID = strings.TrimSpace(invocationID)
	input.RawObjectID = strings.TrimSpace(input.RawObjectID)
	input.ProviderRequestID = strings.TrimSpace(input.ProviderRequestID)
	output, err := canonicalInvocationObject(input.OutputSummary)
	if err != nil || !validInvocationCompletion(workspaceID, invocationID,
		input.RawObjectID, input.ProviderRequestID) {
		return ToolInvocation{}, ErrToolInvocationInvalid
	}
	var finishedAt any
	if !input.FinishedAt.IsZero() {
		finishedAt = input.FinishedAt.UTC()
	}
	value, err := scanToolInvocation(r.db.QueryRowContext(ctx, `
		UPDATE tool_invocations ti SET status='SUCCEEDED',output_summary=$3,
		 raw_object_id=$4,provider_request_id=$5,
		 finished_at=GREATEST(COALESCE($6::timestamptz,clock_timestamp()),started_at),
		 latency_ms=GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (
		  GREATEST(COALESCE($6::timestamptz,clock_timestamp()),started_at)-started_at
		 ))*1000)::bigint)
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		RETURNING `+toolInvocationColumns,
		workspaceID, invocationID, []byte(output), nullableInvocationString(input.RawObjectID),
		nullableInvocationString(input.ProviderRequestID), finishedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolInvocation{}, r.classifyInvocationState(ctx, workspaceID, invocationID)
	}
	if err != nil {
		return ToolInvocation{}, mapToolInvocationWrite("complete tool invocation", err)
	}
	return value, nil
}

func (r *ToolInvocationRepository) Fail(
	ctx context.Context,
	workspaceID, invocationID string,
	input FailToolInvocationInput,
) (ToolInvocation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	invocationID = strings.TrimSpace(invocationID)
	input.RawObjectID = strings.TrimSpace(input.RawObjectID)
	input.ProviderRequestID = strings.TrimSpace(input.ProviderRequestID)
	input.ErrorCode = strings.TrimSpace(input.ErrorCode)
	output, err := canonicalInvocationObject(input.OutputSummary)
	if err != nil || input.ErrorCode == "" || !validInvocationCompletion(
		workspaceID, invocationID, input.RawObjectID, input.ProviderRequestID,
	) {
		return ToolInvocation{}, ErrToolInvocationInvalid
	}
	var finishedAt any
	if !input.FinishedAt.IsZero() {
		finishedAt = input.FinishedAt.UTC()
	}
	value, err := scanToolInvocation(r.db.QueryRowContext(ctx, `
		UPDATE tool_invocations ti SET status='FAILED',output_summary=$3,
		 raw_object_id=$4,provider_request_id=$5,error_code=$6,
		 finished_at=GREATEST(COALESCE($7::timestamptz,clock_timestamp()),started_at),
		 latency_ms=GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (
		  GREATEST(COALESCE($7::timestamptz,clock_timestamp()),started_at)-started_at
		 ))*1000)::bigint)
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'
		RETURNING `+toolInvocationColumns,
		workspaceID, invocationID, []byte(output), nullableInvocationString(input.RawObjectID),
		nullableInvocationString(input.ProviderRequestID), input.ErrorCode, finishedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return ToolInvocation{}, r.classifyInvocationState(ctx, workspaceID, invocationID)
	}
	if err != nil {
		return ToolInvocation{}, mapToolInvocationWrite("fail tool invocation", err)
	}
	return value, nil
}

func (r *ToolInvocationRepository) getByIdempotencyKey(
	ctx context.Context,
	workspaceID, toolVersionID, idempotencyKey string,
) (ToolInvocation, error) {
	value, err := scanToolInvocation(r.db.QueryRowContext(ctx, `
		SELECT `+toolInvocationColumns+`
		FROM tool_invocations ti
		WHERE ti.workspace_id=$1 AND ti.tool_version_id=$2 AND ti.idempotency_key=$3
	`, workspaceID, toolVersionID, idempotencyKey))
	return value, mapToolInvocationRead("get idempotent tool invocation", err)
}

func (r *ToolInvocationRepository) classifyInvocationState(
	ctx context.Context,
	workspaceID, invocationID string,
) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM tool_invocations WHERE workspace_id=$1 AND id=$2)
	`, workspaceID, invocationID).Scan(&exists); err != nil {
		return fmt.Errorf("classify tool invocation state: %w", err)
	}
	if !exists {
		return ErrToolInvocationNotFound
	}
	return ErrToolInvocationConflict
}

func normalizeStartToolInvocation(input StartToolInvocationInput) StartToolInvocationInput {
	input.ID = strings.TrimSpace(input.ID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ToolID = strings.TrimSpace(input.ToolID)
	input.ToolVersionID = strings.TrimSpace(input.ToolVersionID)
	input.CapabilityReleaseID = strings.TrimSpace(input.CapabilityReleaseID)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.ExecutionLeaseID = strings.TrimSpace(input.ExecutionLeaseID)
	input.AgentRunID = strings.TrimSpace(input.AgentRunID)
	input.WorkflowExecutionID = strings.TrimSpace(input.WorkflowExecutionID)
	input.ExecutionStepID = strings.TrimSpace(input.ExecutionStepID)
	input.ActorType = strings.TrimSpace(input.ActorType)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.TraceID = strings.TrimSpace(input.TraceID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

func validStartToolInvocation(input StartToolInvocationInput) bool {
	for _, value := range []string{
		input.ID, input.WorkspaceID, input.ToolID, input.ToolVersionID,
		input.CapabilityReleaseID, input.ProviderID, input.ActorID,
	} {
		if !invocationValidUUID(value) {
			return false
		}
	}
	for _, value := range []string{
		input.ConnectionID, input.ExecutionLeaseID, input.AgentRunID,
		input.WorkflowExecutionID, input.ExecutionStepID,
	} {
		if value != "" && !invocationValidUUID(value) {
			return false
		}
	}
	if input.ExecutionStepID != "" && input.WorkflowExecutionID == "" {
		return false
	}
	if input.ActorType != "USER" && input.ActorType != "SERVICE_PRINCIPAL" && input.ActorType != "SYSTEM" {
		return false
	}
	return input.TraceID != "" && len(input.IdempotencyKey) <= 255
}

func validInvocationCompletion(
	workspaceID, invocationID, rawObjectID, providerRequestID string,
) bool {
	return invocationValidUUID(workspaceID) && invocationValidUUID(invocationID) &&
		(rawObjectID == "" || invocationValidUUID(rawObjectID)) &&
		(providerRequestID == "" || strings.TrimSpace(providerRequestID) != "")
}

func canonicalInvocationObject(value json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, ErrToolInvocationInvalid
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func sameToolInvocationRequest(value ToolInvocation, input StartToolInvocationInput) bool {
	existingInput, err := canonicalInvocationObject(value.InputSummary)
	if err != nil {
		return false
	}
	existingAuthorization, err := canonicalInvocationObject(value.AuthorizationSnapshot)
	if err != nil {
		return false
	}
	requestedAuthorization, err := canonicalInvocationObject(input.AuthorizationSnapshot)
	if err != nil {
		return false
	}
	return value.WorkspaceID == input.WorkspaceID && value.ToolID == input.ToolID &&
		value.ToolVersionID == input.ToolVersionID &&
		value.CapabilityReleaseID == input.CapabilityReleaseID &&
		value.ProviderID == input.ProviderID && value.ConnectionID == input.ConnectionID &&
		value.ExecutionLeaseID == input.ExecutionLeaseID && value.AgentRunID == input.AgentRunID &&
		value.WorkflowExecutionID == input.WorkflowExecutionID &&
		value.ExecutionStepID == input.ExecutionStepID && value.ActorType == input.ActorType &&
		value.ActorID == input.ActorID && bytes.Equal(existingInput, input.InputSummary) &&
		input.PrincipalSnapshot != nil && value.PrincipalSnapshot.SameBinding(*input.PrincipalSnapshot) &&
		bytes.Equal(existingAuthorization, requestedAuthorization)
}

func startToolInvocationArguments(input StartToolInvocationInput, canonicalInput json.RawMessage) []any {
	arguments := []any{
		input.ID, input.WorkspaceID, input.ToolID, input.ToolVersionID,
		input.CapabilityReleaseID, input.ProviderID,
		nullableInvocationString(input.ConnectionID),
		nullableInvocationString(input.ExecutionLeaseID),
		nullableInvocationString(input.AgentRunID),
		nullableInvocationString(input.WorkflowExecutionID),
		nullableInvocationString(input.ExecutionStepID),
		input.ActorType, input.ActorID, input.TraceID,
		nullableInvocationString(input.IdempotencyKey), []byte(canonicalInput),
	}
	arguments = append(arguments, executionSnapshotArguments(*input.PrincipalSnapshot)...)
	arguments = append(arguments, []byte(input.AuthorizationSnapshot))
	return arguments
}

func nullableInvocationString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type toolInvocationScanner interface{ Scan(...any) error }

func scanToolInvocation(scanner toolInvocationScanner) (ToolInvocation, error) {
	var value ToolInvocation
	var connectionID, executionLeaseID, providerRequestID sql.NullString
	var agentRunID, workflowExecutionID, executionStepID sql.NullString
	var idempotencyKey, rawObjectID, errorCode sql.NullString
	var latencyMS sql.NullInt64
	var grantVersion, policyVersion sql.NullInt64
	var subjectType, subjectID, clientID, grantID sql.NullString
	var finishedAt sql.NullTime
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.ToolID, &value.ToolVersionID,
		&value.CapabilityReleaseID, &value.ProviderID, &connectionID,
		&executionLeaseID, &providerRequestID, &agentRunID, &workflowExecutionID,
		&executionStepID, &value.ActorType, &value.ActorID, &value.TraceID,
		&idempotencyKey, &value.Status, &value.InputSummary, &value.OutputSummary,
		&rawObjectID, &latencyMS, &errorCode, &value.StartedAt, &finishedAt,
		&value.PrincipalSnapshotVersion, &subjectType, &subjectID, &clientID, &grantID,
		&grantVersion, &policyVersion, &value.AuthorizationSnapshot,
	)
	if err != nil {
		return ToolInvocation{}, err
	}
	value.ConnectionID = connectionID.String
	value.ExecutionLeaseID = executionLeaseID.String
	value.ProviderRequestID = providerRequestID.String
	value.AgentRunID = agentRunID.String
	value.WorkflowExecutionID = workflowExecutionID.String
	value.ExecutionStepID = executionStepID.String
	value.IdempotencyKey = idempotencyKey.String
	value.RawObjectID = rawObjectID.String
	value.ErrorCode = errorCode.String
	value.PrincipalSnapshot, err = scannedExecutionSnapshot(
		value.PrincipalSnapshotVersion, value.WorkspaceID, value.ActorType, value.ActorID,
		subjectType, subjectID, clientID, grantID, grantVersion, policyVersion,
	)
	if err != nil {
		return ToolInvocation{}, err
	}
	if latencyMS.Valid {
		latency := latencyMS.Int64
		value.LatencyMS = &latency
	}
	if finishedAt.Valid {
		finished := finishedAt.Time
		value.FinishedAt = &finished
	}
	return value, nil
}

func invocationValidUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func mapToolInvocationRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrToolInvocationNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapToolInvocationWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505", "40001", "55000":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrToolInvocationConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s (%s): %w", operation, pqError.Constraint, ErrToolInvocationInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
