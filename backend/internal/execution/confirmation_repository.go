package execution

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	ErrConfirmationNotFound           = errors.New("execution confirmation not found")
	ErrConfirmationConflict           = errors.New("execution confirmation state conflict")
	ErrConfirmationExpired            = errors.New("execution confirmation expired")
	ErrConfirmationRequesterMismatch  = errors.New("execution confirmation requester mismatch")
	ErrConfirmationDecisionNotAllowed = errors.New("execution confirmation decision is not allowed")
	ErrConfirmationTokenInvalid       = errors.New("execution confirmation resume token invalid")
	ErrConfirmationBindingChanged     = errors.New("execution confirmation binding changed")
	ErrConfirmationInvalid            = errors.New("invalid execution confirmation")
)

const executionConfirmationColumns = `
	ec.id,ec.workspace_id,ec.execution_id,ec.run_id,ec.target_item_id,ec.node_id,ec.status,
	ec.reason,ec.risk_reasons,ec.scope_snapshot,ec.release_id,ec.input_hash,
	ec.connection_id,ec.plan_hash,ec.interaction_binding_hash,ec.resume_token_hash,ec.requested_by,
	ec.request_principal_snapshot_version,ec.request_actor_type,ec.request_actor_id,
	ec.request_subject_type,ec.request_subject_id,ec.request_client_id,ec.request_grant_id,
	ec.request_grant_version,ec.request_agent_policy_version,ec.confirmed_by,
	ec.decision_principal_snapshot_version,ec.decision_actor_type,ec.decision_actor_id,
	ec.decision_subject_type,ec.decision_subject_id,ec.decision_client_id,ec.decision_grant_id,
	ec.decision_grant_version,ec.decision_agent_policy_version,ec.decision_policy_snapshot,
	ec.created_at,ec.expires_at,ec.confirmed_at,
	ec.cancelled_at,ec.lock_version
`

type confirmationRepositoryRecord struct {
	ExecutionConfirmation
	ResumeTokenHash string
}

type ConfirmationRepository struct {
	db *sql.DB
}

func NewConfirmationRepository(db *sql.DB) (*ConfirmationRepository, error) {
	if db == nil {
		return nil, errors.New("confirmation repository database is required")
	}
	return &ConfirmationRepository{db: db}, nil
}

func (repository *ConfirmationRepository) create(
	ctx context.Context,
	input newExecutionConfirmation,
) (ExecutionConfirmation, error) {
	return repository.createWith(ctx, repository.db, input)
}

type confirmationQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (repository *ConfirmationRepository) createWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	input newExecutionConfirmation,
) (ExecutionConfirmation, error) {
	riskReasons, err := json.Marshal(input.RiskReasons)
	if err != nil {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		INSERT INTO execution_confirmations AS ec(
			id,workspace_id,execution_id,run_id,target_item_id,node_id,status,reason,risk_reasons,
			scope_snapshot,release_id,input_hash,connection_id,plan_hash,
			interaction_binding_hash,resume_token_hash,request_principal_snapshot_version,request_actor_type,
			request_actor_id,request_subject_type,request_subject_id,request_client_id,
			request_grant_id,request_grant_version,request_agent_policy_version,
			requested_by,decision_policy_snapshot,created_at,expires_at,lock_version
		) VALUES($1,$2,$3,$4,$5,$6,'PENDING',$7,$8,$9,$10,$11,$12,$13,$14,
			$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,'{}',$26,$27,1)
		RETURNING `+executionConfirmationColumns,
		input.ID, input.WorkspaceID, nullableConfirmationString(input.ExecutionID),
		nullableConfirmationString(input.RunID), input.TargetItemID, input.NodeID, input.Reason,
		riskReasons, []byte(input.ScopeSnapshot), input.ReleaseID, input.InputHash,
		nullableConfirmationString(input.ConnectionID), nullableConfirmationString(input.PlanHash),
		input.InteractionBindingHash, input.ResumeTokenHash,
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[0],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[1],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[2],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[3],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[4],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[5],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[6],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[7],
		confirmationSnapshotArguments(input.RequestPrincipalSnapshot)[8],
		confirmationUserProjection(input.RequestPrincipalSnapshot), input.CreatedAt, input.ExpiresAt,
	))
	if err != nil {
		return ExecutionConfirmation{}, mapConfirmationWrite("request execution confirmation", err)
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) Get(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ExecutionConfirmation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	if !invocationValidUUID(workspaceID) || !invocationValidUUID(confirmationID) {
		return ExecutionConfirmation{}, ErrConfirmationInvalid
	}
	value, err := repository.getRecord(ctx, workspaceID, confirmationID)
	if err != nil {
		return ExecutionConfirmation{}, err
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) confirm(
	ctx context.Context,
	binding confirmationMutationBinding,
) (ExecutionConfirmation, error) {
	value, err := repository.confirmWith(ctx, repository.db, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionConfirmation{}, repository.classifyMutation(ctx, binding, true)
	}
	return value, err
}

func (repository *ConfirmationRepository) confirmWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	binding confirmationMutationBinding,
) (ExecutionConfirmation, error) {
	principalArguments := confirmationSnapshotArguments(binding.PrincipalSnapshot)
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		UPDATE execution_confirmations ec
		SET status='CONFIRMED',
		 decision_principal_snapshot_version=$3,decision_actor_type=$4,decision_actor_id=$5,
		 decision_subject_type=$6,decision_subject_id=$7,decision_client_id=$8,
		 decision_grant_id=$9,decision_grant_version=$10,decision_agent_policy_version=$11,
		 confirmed_by=$12,decision_policy_snapshot=$13,confirmed_at=$22,
		 lock_version=lock_version+1
		WHERE ec.workspace_id=$1 AND ec.id=$2 AND ec.status='PENDING'
		  AND ec.request_actor_type=$4 AND ec.request_actor_id=$5
		  AND ec.request_subject_type IS NOT DISTINCT FROM $6::text
		  AND ec.request_subject_id IS NOT DISTINCT FROM $7::uuid
		  AND ec.request_client_id IS NOT DISTINCT FROM $8::uuid
		  AND ec.request_grant_id IS NOT DISTINCT FROM $9::uuid
		  AND ec.resume_token_hash=$14 AND ec.run_id=$15
		  AND ec.release_id=$16 AND ec.input_hash=$17
		  AND ec.connection_id IS NOT DISTINCT FROM $18::uuid
		  AND ec.plan_hash IS NOT DISTINCT FROM $19::text
		  AND ec.target_item_id=$20 AND ec.lock_version=$21 AND ec.expires_at>$22
		RETURNING `+executionConfirmationColumns,
		binding.WorkspaceID, binding.ConfirmationID,
		principalArguments[0], principalArguments[1], principalArguments[2],
		principalArguments[3], principalArguments[4], principalArguments[5],
		principalArguments[6], principalArguments[7], principalArguments[8],
		confirmationUserProjection(binding.PrincipalSnapshot), []byte(binding.DecisionPolicySnapshot),
		binding.ResumeTokenHash, binding.RunID, binding.ReleaseID, binding.InputHash,
		nullableConfirmationString(binding.ConnectionID), nullableConfirmationString(binding.PlanHash),
		binding.TargetItemID, binding.ExpectedLockVersion, binding.Now,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionConfirmation{}, err
		}
		return ExecutionConfirmation{}, mapConfirmationWrite("confirm execution confirmation", err)
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) confirmPreparedWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	binding confirmationMutationBinding,
) (ExecutionConfirmation, error) {
	principalArguments := confirmationSnapshotArguments(binding.PrincipalSnapshot)
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		UPDATE execution_confirmations ec
		SET status='CONFIRMED',
		 decision_principal_snapshot_version=$3,decision_actor_type=$4,decision_actor_id=$5,
		 decision_subject_type=$6,decision_subject_id=$7,decision_client_id=$8,
		 decision_grant_id=$9,decision_grant_version=$10,decision_agent_policy_version=$11,
		 confirmed_by=$12,decision_policy_snapshot=$13,confirmed_at=$24,
		 lock_version=lock_version+1
		WHERE ec.workspace_id=$1 AND ec.id=$2 AND ec.status='PENDING'
		  AND ec.request_actor_type=$4 AND ec.request_actor_id=$5
		  AND ec.request_subject_type IS NOT DISTINCT FROM $6::text
		  AND ec.request_subject_id IS NOT DISTINCT FROM $7::uuid
		  AND ec.request_client_id IS NOT DISTINCT FROM $8::uuid
		  AND ec.request_grant_id IS NOT DISTINCT FROM $9::uuid
		  AND ec.resume_token_hash=$14
		  AND ec.run_id IS NOT DISTINCT FROM $15::uuid
		  AND ec.target_item_id=$16
		  AND ec.release_id=$17 AND ec.input_hash=$18
		  AND ec.connection_id IS NOT DISTINCT FROM $19::uuid
		  AND ec.plan_hash IS NOT DISTINCT FROM $20::text
		  AND ec.interaction_binding_hash=$21 AND ec.expires_at=$22
		  AND ec.lock_version=$23 AND ec.expires_at>$24
		RETURNING `+executionConfirmationColumns,
		binding.WorkspaceID, binding.ConfirmationID,
		principalArguments[0], principalArguments[1], principalArguments[2],
		principalArguments[3], principalArguments[4], principalArguments[5],
		principalArguments[6], principalArguments[7], principalArguments[8],
		confirmationUserProjection(binding.PrincipalSnapshot), []byte(binding.DecisionPolicySnapshot),
		binding.ResumeTokenHash, nullableConfirmationString(binding.RunID), binding.TargetItemID,
		binding.ReleaseID, binding.InputHash, nullableConfirmationString(binding.ConnectionID),
		nullableConfirmationString(binding.PlanHash), binding.InteractionBindingHash,
		binding.ExpiresAt.UTC(), binding.ExpectedLockVersion, binding.Now,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionConfirmation{}, err
		}
		return ExecutionConfirmation{}, mapConfirmationWrite("confirm prepared execution", err)
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) cancel(
	ctx context.Context,
	binding confirmationMutationBinding,
) (ExecutionConfirmation, error) {
	value, err := repository.cancelWith(ctx, repository.db, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionConfirmation{}, repository.classifyMutation(ctx, binding, false)
	}
	return value, err
}

func (repository *ConfirmationRepository) cancelWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	binding confirmationMutationBinding,
) (ExecutionConfirmation, error) {
	principalArguments := confirmationSnapshotArguments(binding.PrincipalSnapshot)
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		UPDATE execution_confirmations ec
		SET status='CANCELLED',
		 decision_principal_snapshot_version=$3,decision_actor_type=$4,decision_actor_id=$5,
		 decision_subject_type=$6,decision_subject_id=$7,decision_client_id=$8,
		 decision_grant_id=$9,decision_grant_version=$10,decision_agent_policy_version=$11,
		 decision_policy_snapshot=$12,cancelled_at=$15,lock_version=lock_version+1
		WHERE ec.workspace_id=$1 AND ec.id=$2 AND ec.status='PENDING'
		  AND ec.request_actor_type=$4 AND ec.request_actor_id=$5
		  AND ec.request_subject_type IS NOT DISTINCT FROM $6::text
		  AND ec.request_subject_id IS NOT DISTINCT FROM $7::uuid
		  AND ec.request_client_id IS NOT DISTINCT FROM $8::uuid
		  AND ec.request_grant_id IS NOT DISTINCT FROM $9::uuid
		  AND ec.lock_version=$13 AND ec.expires_at>$14
		RETURNING `+executionConfirmationColumns,
		binding.WorkspaceID, binding.ConfirmationID,
		principalArguments[0], principalArguments[1], principalArguments[2],
		principalArguments[3], principalArguments[4], principalArguments[5],
		principalArguments[6], principalArguments[7], principalArguments[8],
		[]byte(binding.DecisionPolicySnapshot), binding.ExpectedLockVersion, binding.Now, binding.Now,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionConfirmation{}, err
		}
		return ExecutionConfirmation{}, mapConfirmationWrite("cancel execution confirmation", err)
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) ExpireDue(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]ExecutionConfirmation, error) {
	if now.IsZero() || limit <= 0 || limit > 1000 {
		return nil, ErrConfirmationInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		WITH due AS (
			SELECT ec.id FROM execution_confirmations ec
			WHERE ec.status='PENDING' AND ec.expires_at<=$1
			  AND NOT EXISTS (
			    SELECT 1 FROM confirmation_resume_checkpoints crc
			    WHERE crc.workspace_id=ec.workspace_id AND crc.confirmation_id=ec.id
			  )
			ORDER BY ec.expires_at,ec.id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE execution_confirmations ec
		SET status='EXPIRED',lock_version=lock_version+1
		FROM due WHERE ec.id=due.id
		RETURNING `+executionConfirmationColumns,
		now.UTC(), limit)
	if err != nil {
		return nil, mapConfirmationWrite("expire execution confirmations", err)
	}
	defer rows.Close()
	confirmations := make([]ExecutionConfirmation, 0)
	for rows.Next() {
		value, scanErr := scanConfirmationRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		confirmations = append(confirmations, value.ExecutionConfirmation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan expired execution confirmations: %w", err)
	}
	return confirmations, nil
}

func (repository *ConfirmationRepository) expireWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	workspaceID, confirmationID string,
	now time.Time,
) (ExecutionConfirmation, error) {
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		UPDATE execution_confirmations ec
		SET status='EXPIRED',lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='PENDING' AND expires_at<=$3
		RETURNING `+executionConfirmationColumns,
		workspaceID, confirmationID, now.UTC()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionConfirmation{}, err
		}
		return ExecutionConfirmation{}, mapConfirmationWrite("expire execution confirmation", err)
	}
	return value.ExecutionConfirmation, nil
}

func (repository *ConfirmationRepository) VerifyConfirmed(
	ctx context.Context,
	check ConfirmationCheck,
	now time.Time,
) error {
	check.WorkspaceID = strings.TrimSpace(check.WorkspaceID)
	check.ConfirmationID = strings.TrimSpace(check.ConfirmationID)
	check.RunID = strings.TrimSpace(check.RunID)
	check.TargetItemID = strings.TrimSpace(check.TargetItemID)
	check.ReleaseID = strings.TrimSpace(check.ReleaseID)
	check.ConnectionID = strings.TrimSpace(check.ConnectionID)
	check.PlanHash = strings.ToLower(strings.TrimSpace(check.PlanHash))
	check.InputHash = strings.ToLower(strings.TrimSpace(check.InputHash))
	check.InteractionBindingHash = strings.ToLower(strings.TrimSpace(check.InteractionBindingHash))
	check.ActorID = strings.TrimSpace(check.ActorID)
	decisionPrincipal, err := prepareConfirmationDecisionPrincipal(
		check.WorkspaceID, check.ActorID, check.PrincipalSnapshot,
	)
	if err != nil {
		return err
	}
	check.PrincipalSnapshot = &decisionPrincipal
	if !validConfirmationCheck(check) || now.IsZero() {
		return ErrConfirmationInvalid
	}
	principalArguments := confirmationSnapshotArguments(decisionPrincipal)
	var matched bool
	err = repository.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM execution_confirmations ec
			WHERE ec.workspace_id=$1 AND ec.id=$2 AND ec.status='CONFIRMED'
			  AND ec.request_actor_type=$3 AND ec.request_actor_id=$4
			  AND ec.request_subject_type IS NOT DISTINCT FROM $5::text
			  AND ec.request_subject_id IS NOT DISTINCT FROM $6::uuid
			  AND ec.request_client_id IS NOT DISTINCT FROM $7::uuid
			  AND ec.request_grant_id IS NOT DISTINCT FROM $8::uuid
			  AND ec.decision_actor_type=$3 AND ec.decision_actor_id=$4
			  AND ec.decision_subject_type IS NOT DISTINCT FROM $5::text
			  AND ec.decision_subject_id IS NOT DISTINCT FROM $6::uuid
			  AND ec.decision_client_id IS NOT DISTINCT FROM $7::uuid
			  AND ec.decision_grant_id IS NOT DISTINCT FROM $8::uuid
			  AND ($9::uuid IS NULL OR ec.run_id=$9)
			  AND ($10::uuid IS NULL OR ec.target_item_id=$10)
			  AND ec.release_id=$11 AND ec.input_hash=$12
			  AND ec.connection_id IS NOT DISTINCT FROM $13::uuid
			  AND ec.plan_hash IS NOT DISTINCT FROM $14::text
			  AND ($15::text IS NULL OR ec.interaction_binding_hash=$15) AND ec.expires_at>$16
		)
	`, check.WorkspaceID, check.ConfirmationID,
		principalArguments[1], principalArguments[2], principalArguments[3],
		principalArguments[4], principalArguments[5], principalArguments[6],
		nullableConfirmationString(check.RunID), nullableConfirmationString(check.TargetItemID),
		check.ReleaseID, check.InputHash,
		nullableConfirmationString(check.ConnectionID), nullableConfirmationString(check.PlanHash),
		nullableConfirmationString(check.InteractionBindingHash), now.UTC()).Scan(&matched)
	if err != nil {
		return fmt.Errorf("verify execution confirmation: %w", err)
	}
	if matched {
		return nil
	}
	record, err := repository.getRecord(ctx, check.WorkspaceID, check.ConfirmationID)
	if err != nil {
		return err
	}
	if now.UTC().After(record.ExpiresAt) || now.UTC().Equal(record.ExpiresAt) {
		return ErrConfirmationExpired
	}
	if record.Status != ConfirmationStatusConfirmed {
		return ErrConfirmationConflict
	}
	if record.DecisionPrincipalSnapshot == nil ||
		!record.RequestPrincipalSnapshot.SameDecisionPrincipal(decisionPrincipal) ||
		!record.DecisionPrincipalSnapshot.SameDecisionPrincipal(decisionPrincipal) {
		return ErrConfirmationRequesterMismatch
	}
	if (check.RunID != "" && record.RunID != check.RunID) ||
		(check.TargetItemID != "" && record.TargetItemID != check.TargetItemID) ||
		(check.InteractionBindingHash != "" && record.InteractionBindingHash != check.InteractionBindingHash) ||
		record.ReleaseID != check.ReleaseID || record.InputHash != check.InputHash ||
		record.ConnectionID != check.ConnectionID || record.PlanHash != check.PlanHash {
		return ErrConfirmationBindingChanged
	}
	return ErrConfirmationConflict
}

func (repository *ConfirmationRepository) classifyMutation(
	ctx context.Context,
	binding confirmationMutationBinding,
	checkTokenAndBinding bool,
) error {
	record, err := repository.getRecord(ctx, binding.WorkspaceID, binding.ConfirmationID)
	if err != nil {
		return err
	}
	if !record.ExpiresAt.After(binding.Now) {
		_, _ = repository.db.ExecContext(ctx, `
			UPDATE execution_confirmations SET status='EXPIRED',lock_version=lock_version+1
			WHERE workspace_id=$1 AND id=$2 AND status='PENDING' AND expires_at<=$3
		`, binding.WorkspaceID, binding.ConfirmationID, binding.Now)
		return ErrConfirmationExpired
	}
	if record.Status != ConfirmationStatusPending {
		return ErrConfirmationConflict
	}
	if !record.RequestPrincipalSnapshot.SameDecisionPrincipal(binding.PrincipalSnapshot) {
		return ErrConfirmationRequesterMismatch
	}
	if record.LockVersion != binding.ExpectedLockVersion {
		return ErrConfirmationConflict
	}
	if checkTokenAndBinding {
		if subtle.ConstantTimeCompare([]byte(record.ResumeTokenHash), []byte(binding.ResumeTokenHash)) != 1 {
			return ErrConfirmationTokenInvalid
		}
		if record.RunID != binding.RunID || record.TargetItemID != binding.TargetItemID ||
			record.ReleaseID != binding.ReleaseID || record.InputHash != binding.InputHash ||
			record.ConnectionID != binding.ConnectionID || record.PlanHash != binding.PlanHash {
			return ErrConfirmationBindingChanged
		}
	}
	return ErrConfirmationConflict
}

func (repository *ConfirmationRepository) getRecord(
	ctx context.Context,
	workspaceID, confirmationID string,
) (confirmationRepositoryRecord, error) {
	return repository.getRecordWith(ctx, repository.db, workspaceID, confirmationID, false)
}

func (repository *ConfirmationRepository) getRecordForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID string,
) (confirmationRepositoryRecord, error) {
	if tx == nil {
		return confirmationRepositoryRecord{}, ErrConfirmationInvalid
	}
	return repository.getRecordWith(ctx, tx, workspaceID, confirmationID, true)
}

func (repository *ConfirmationRepository) getRecordWith(
	ctx context.Context,
	queryer confirmationQueryRower,
	workspaceID, confirmationID string,
	forUpdate bool,
) (confirmationRepositoryRecord, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE OF ec"
	}
	value, err := scanConfirmationRecord(queryer.QueryRowContext(ctx, `
		SELECT `+executionConfirmationColumns+`
		FROM execution_confirmations ec
		WHERE ec.workspace_id=$1 AND ec.id=$2
	`+suffix, workspaceID, confirmationID))
	if errors.Is(err, sql.ErrNoRows) {
		return confirmationRepositoryRecord{}, ErrConfirmationNotFound
	}
	if err != nil {
		return confirmationRepositoryRecord{}, fmt.Errorf("get execution confirmation: %w", err)
	}
	return value, nil
}

type confirmationScanner interface {
	Scan(...any) error
}

func scanConfirmationRecord(scanner confirmationScanner) (confirmationRepositoryRecord, error) {
	var value confirmationRepositoryRecord
	var executionID, runID, targetItemID, connectionID, planHash, bindingHash, requestedBy, confirmedBy sql.NullString
	var requestVersion, decisionVersion sql.NullString
	var requestActorType, requestActorID, requestSubjectType, requestSubjectID sql.NullString
	var requestClientID, requestGrantID sql.NullString
	var requestGrantVersion, requestPolicyVersion sql.NullInt64
	var decisionActorType, decisionActorID, decisionSubjectType, decisionSubjectID sql.NullString
	var decisionClientID, decisionGrantID sql.NullString
	var decisionGrantVersion, decisionPolicyVersion sql.NullInt64
	var confirmedAt, cancelledAt sql.NullTime
	var riskReasons, scopeSnapshot, decisionPolicySnapshot []byte
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &executionID, &runID, &targetItemID, &value.NodeID, &value.Status,
		&value.Reason, &riskReasons, &scopeSnapshot, &value.ReleaseID, &value.InputHash,
		&connectionID, &planHash, &bindingHash, &value.ResumeTokenHash, &requestedBy,
		&requestVersion, &requestActorType, &requestActorID, &requestSubjectType,
		&requestSubjectID, &requestClientID, &requestGrantID, &requestGrantVersion,
		&requestPolicyVersion, &confirmedBy, &decisionVersion, &decisionActorType,
		&decisionActorID, &decisionSubjectType, &decisionSubjectID, &decisionClientID,
		&decisionGrantID, &decisionGrantVersion, &decisionPolicyVersion,
		&decisionPolicySnapshot, &value.CreatedAt, &value.ExpiresAt, &confirmedAt,
		&cancelledAt, &value.LockVersion,
	)
	if err != nil {
		return value, err
	}
	value.ExecutionID = nullableConfirmationValue(executionID)
	value.RunID = nullableConfirmationValue(runID)
	value.TargetItemID = nullableConfirmationValue(targetItemID)
	value.ConnectionID = nullableConfirmationValue(connectionID)
	value.PlanHash = nullableConfirmationValue(planHash)
	value.InteractionBindingHash = nullableConfirmationValue(bindingHash)
	value.RequestedBy = nullableConfirmationValue(requestedBy)
	value.ConfirmedBy = nullableConfirmationValue(confirmedBy)
	requestSnapshot, err := scanConfirmationSnapshot(
		requestVersion.String, value.WorkspaceID, requestActorType, requestActorID,
		requestSubjectType, requestSubjectID, requestClientID, requestGrantID,
		requestGrantVersion, requestPolicyVersion,
	)
	if err != nil || requestSnapshot == nil {
		return value, ErrConfirmationInvalid
	}
	value.RequestPrincipalSnapshotVersion = requestVersion.String
	value.RequestPrincipalSnapshot = *requestSnapshot
	decisionSnapshot, err := scanConfirmationSnapshot(
		decisionVersion.String, value.WorkspaceID, decisionActorType, decisionActorID,
		decisionSubjectType, decisionSubjectID, decisionClientID, decisionGrantID,
		decisionGrantVersion, decisionPolicyVersion,
	)
	if err != nil {
		return value, err
	}
	value.DecisionPrincipalSnapshotVersion = decisionVersion.String
	value.DecisionPrincipalSnapshot = decisionSnapshot
	value.DecisionPolicySnapshot = append(json.RawMessage(nil), decisionPolicySnapshot...)
	value.ScopeSnapshot = append(json.RawMessage(nil), scopeSnapshot...)
	if err := json.Unmarshal(riskReasons, &value.RiskReasons); err != nil {
		return value, fmt.Errorf("decode execution confirmation risk reasons: %w", err)
	}
	if confirmedAt.Valid {
		confirmed := confirmedAt.Time.UTC()
		value.ConfirmedAt = &confirmed
	}
	if cancelledAt.Valid {
		cancelled := cancelledAt.Time.UTC()
		value.CancelledAt = &cancelled
	}
	value.CreatedAt = value.CreatedAt.UTC().Truncate(time.Microsecond)
	value.ExpiresAt = value.ExpiresAt.UTC().Truncate(time.Microsecond)
	return value, nil
}

func mapConfirmationWrite(operation string, err error) error {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		if databaseError.Constraint == "execution_confirmation_service_decision_check" {
			return ErrConfirmationDecisionNotAllowed
		}
		switch databaseError.Code {
		case "23503":
			return ErrConfirmationNotFound
		case "23505":
			return ErrConfirmationConflict
		case "23514", "22P02", "22023":
			return ErrConfirmationInvalid
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func nullableConfirmationString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func nullableConfirmationValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func validConfirmationCheck(check ConfirmationCheck) bool {
	return invocationValidUUID(check.WorkspaceID) && invocationValidUUID(check.ConfirmationID) &&
		(check.RunID == "" || invocationValidUUID(check.RunID)) &&
		(check.TargetItemID == "" || invocationValidUUID(check.TargetItemID)) &&
		invocationValidUUID(check.ReleaseID) && check.PrincipalSnapshot != nil &&
		check.PrincipalSnapshot.Validate() == nil &&
		(check.ConnectionID == "" || invocationValidUUID(check.ConnectionID)) &&
		(check.PlanHash == "" || validConfirmationHash(check.PlanHash)) &&
		validConfirmationHash(check.InputHash) &&
		(check.InteractionBindingHash == "" || validConfirmationHash(check.InteractionBindingHash))
}
