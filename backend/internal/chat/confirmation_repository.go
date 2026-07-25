package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/principal"
)

const chatConfirmationColumns = `
	cc.id,cc.workspace_id,cc.session_id,cc.run_id,ec.target_item_id,cc.execution_confirmation_id,
	cc.target_type,cc.target_release_id,ec.input_hash,ec.connection_id,ec.plan_hash,
	ec.interaction_binding_hash,cc.risk_level,cc.risk_reasons,
	cc.input_summary,ec.status,ec.confirmed_by,ec.confirmed_at,cc.created_at,
	ec.requested_by,ec.request_principal_snapshot_version,ec.request_actor_type,
	ec.request_actor_id,ec.request_subject_type,ec.request_subject_id,ec.request_client_id,
	ec.request_grant_id,ec.request_grant_version,ec.request_agent_policy_version,
	ec.decision_principal_snapshot_version,ec.decision_actor_type,ec.decision_actor_id,
	ec.decision_subject_type,ec.decision_subject_id,ec.decision_client_id,
	ec.decision_grant_id,ec.decision_grant_version,ec.decision_agent_policy_version,
	ec.lock_version,ec.expires_at
`

func (r *Repository) GetConfirmation(
	ctx context.Context,
	workspaceID, confirmationID string,
) (ChatConfirmation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	confirmationID = strings.TrimSpace(confirmationID)
	if !validUUID(workspaceID) || !validUUID(confirmationID) {
		return ChatConfirmation{}, ErrInvalid
	}
	value, err := scanChatConfirmation(r.db.QueryRowContext(ctx, `
		SELECT `+chatConfirmationColumns+`
		FROM chat_confirmations cc
		JOIN execution_confirmations ec
		  ON ec.workspace_id=cc.workspace_id AND ec.id=cc.execution_confirmation_id
		WHERE cc.workspace_id=$1 AND cc.id=$2
	`, workspaceID, confirmationID))
	return value, mapRead("get chat confirmation", err)
}

func (r *Repository) getConfirmationForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, confirmationID string,
) (ChatConfirmation, error) {
	if tx == nil {
		return ChatConfirmation{}, ErrInvalid
	}
	value, err := scanChatConfirmation(tx.QueryRowContext(ctx, `
		SELECT `+chatConfirmationColumns+`
		FROM chat_confirmations cc
		JOIN execution_confirmations ec
		  ON ec.workspace_id=cc.workspace_id AND ec.id=cc.execution_confirmation_id
		WHERE cc.workspace_id=$1 AND cc.id=$2
		FOR UPDATE OF cc,ec
	`, workspaceID, confirmationID))
	return value, mapRead("lock chat confirmation", err)
}

func (r *Repository) createConfirmationInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	value ChatConfirmation,
) (ChatConfirmation, error) {
	if tx == nil {
		return ChatConfirmation{}, ErrInvalid
	}
	riskReasons, err := json.Marshal(value.RiskReasons)
	if err != nil {
		return ChatConfirmation{}, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_confirmations(
		 id,workspace_id,session_id,run_id,execution_confirmation_id,target_type,
		 target_release_id,risk_level,risk_reasons,input_summary,status,
		 confirmed_by,confirmed_at,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, value.ID, value.WorkspaceID, value.SessionID, value.RunID,
		value.ExecutionConfirmationID, value.TargetType, value.TargetReleaseID,
		value.RiskLevel, riskReasons, []byte(value.InputSummary), value.Status,
		nullableString(value.ConfirmedBy), value.ConfirmedAt, value.CreatedAt); err != nil {
		return ChatConfirmation{}, mapWrite("create chat confirmation", err)
	}
	return r.getConfirmationForUpdate(ctx, tx, value.WorkspaceID, value.ID)
}

func (r *Repository) setPendingConfirmationInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, sessionID, runID, confirmationID string,
	expectedLockVersion int64,
) (Session, error) {
	value, err := scanSession(tx.QueryRowContext(ctx, `
		UPDATE chat_sessions cs
		SET pending_confirmation_id=$5,updated_at=clock_timestamp(),
		    lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND status='ACTIVE'
		  AND latest_run_id=$3 AND lock_version=$4
		  AND pending_confirmation_id IS NULL
		RETURNING `+sessionColumns,
		workspaceID, sessionID, runID, expectedLockVersion, confirmationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrConflict
	}
	if err != nil {
		return Session{}, mapWrite("set chat pending confirmation", err)
	}
	return value, nil
}

func (r *Repository) markMessagePendingConfirmationInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, sessionID, messageID, runID, confirmationID string,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE chat_messages
		SET status='PENDING_CONFIRMATION',confirmation_id=$5
		WHERE workspace_id=$1 AND session_id=$2 AND id=$3 AND run_id=$4
		  AND role='USER' AND status='PROCESSING' AND confirmation_id IS NULL
	`, workspaceID, sessionID, messageID, runID, confirmationID)
	if err != nil {
		return mapWrite("mark chat message pending confirmation", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pending chat message update count: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func scanChatConfirmation(row scanner) (ChatConfirmation, error) {
	var value ChatConfirmation
	var riskReasons, inputSummary []byte
	var targetItemID, connectionID, planHash, bindingHash, requestedBy, confirmedBy sql.NullString
	var confirmedAt sql.NullTime
	var requestVersion, decisionVersion sql.NullString
	var requestActorType, requestActorID, requestSubjectType, requestSubjectID sql.NullString
	var requestClientID, requestGrantID sql.NullString
	var requestGrantVersion, requestPolicyVersion sql.NullInt64
	var decisionActorType, decisionActorID, decisionSubjectType, decisionSubjectID sql.NullString
	var decisionClientID, decisionGrantID sql.NullString
	var decisionGrantVersion, decisionPolicyVersion sql.NullInt64
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.SessionID, &value.RunID, &targetItemID,
		&value.ExecutionConfirmationID, &value.TargetType, &value.TargetReleaseID,
		&value.InputHash, &connectionID, &planHash, &bindingHash,
		&value.RiskLevel, &riskReasons, &inputSummary, &value.Status,
		&confirmedBy, &confirmedAt, &value.CreatedAt, &requestedBy,
		&requestVersion, &requestActorType, &requestActorID, &requestSubjectType,
		&requestSubjectID, &requestClientID, &requestGrantID, &requestGrantVersion,
		&requestPolicyVersion, &decisionVersion, &decisionActorType, &decisionActorID,
		&decisionSubjectType, &decisionSubjectID, &decisionClientID, &decisionGrantID,
		&decisionGrantVersion, &decisionPolicyVersion,
		&value.ExecutionLockVersion, &value.ExpiresAt,
	)
	if err != nil {
		return ChatConfirmation{}, err
	}
	if err := json.Unmarshal(riskReasons, &value.RiskReasons); err != nil {
		return ChatConfirmation{}, fmt.Errorf("decode chat confirmation risk reasons: %w", err)
	}
	value.InputSummary = append(json.RawMessage(nil), inputSummary...)
	value.TargetItemID = targetItemID.String
	value.ConnectionID = connectionID.String
	value.PlanHash = planHash.String
	value.InteractionBindingHash = bindingHash.String
	value.ConfirmedBy = confirmedBy.String
	value.RequestedBy = requestedBy.String
	requestPrincipal, err := scanChatConfirmationPrincipal(
		value.WorkspaceID, requestVersion, requestActorType, requestActorID,
		requestSubjectType, requestSubjectID, requestClientID, requestGrantID,
		requestGrantVersion, requestPolicyVersion,
	)
	if err != nil || requestPrincipal == nil {
		return ChatConfirmation{}, ErrInvalid
	}
	value.RequestPrincipalSnapshotVersion = requestVersion.String
	value.RequestPrincipalSnapshot = *requestPrincipal
	decisionPrincipal, err := scanChatConfirmationPrincipal(
		value.WorkspaceID, decisionVersion, decisionActorType, decisionActorID,
		decisionSubjectType, decisionSubjectID, decisionClientID, decisionGrantID,
		decisionGrantVersion, decisionPolicyVersion,
	)
	if err != nil {
		return ChatConfirmation{}, err
	}
	value.DecisionPrincipalSnapshotVersion = decisionVersion.String
	value.DecisionPrincipalSnapshot = decisionPrincipal
	if confirmedAt.Valid {
		timestamp := confirmedAt.Time.UTC()
		value.ConfirmedAt = &timestamp
	}
	value.CreatedAt = value.CreatedAt.UTC().Truncate(time.Microsecond)
	value.ExpiresAt = value.ExpiresAt.UTC().Truncate(time.Microsecond)
	return value, nil
}

func scanChatConfirmationPrincipal(
	workspaceID string,
	version, actorType, actorID, subjectType, subjectID, clientID, grantID sql.NullString,
	grantVersion, policyVersion sql.NullInt64,
) (*principal.ExecutionSnapshot, error) {
	if !version.Valid && !actorType.Valid && !actorID.Valid {
		return nil, nil
	}
	if !version.Valid || !actorType.Valid || !actorID.Valid {
		return nil, ErrInvalid
	}
	actor, err := principal.RefFromLegacy(workspaceID, actorType.String, actorID.String)
	if err != nil {
		return nil, ErrInvalid
	}
	var subject *principal.Ref
	if subjectType.Valid || subjectID.Valid {
		if !subjectType.Valid || !subjectID.Valid {
			return nil, ErrInvalid
		}
		value, refErr := principal.RefFromLegacy(workspaceID, subjectType.String, subjectID.String)
		if refErr != nil {
			return nil, ErrInvalid
		}
		subject = &value
	}
	identity, err := principal.NewInvocationIdentity(actor, subject)
	if err != nil {
		return nil, ErrInvalid
	}
	value := principal.ExecutionSnapshot{
		Identity: identity, ClientID: clientID.String, GrantID: grantID.String,
		GrantVersion: grantVersion.Int64, AgentPolicyVersion: policyVersion.Int64,
	}
	if version.String == principal.ExecutionAuthorizationSpecV1 && value.Validate() != nil {
		return nil, ErrInvalid
	}
	return &value, nil
}
