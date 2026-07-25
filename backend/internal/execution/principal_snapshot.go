package execution

import (
	"database/sql"
	"encoding/json"
	"strings"

	"actweave/backend/internal/principal"
)

func prepareExecutionPrincipalSnapshot(
	workspaceID, actorType, actorID string,
	provided *principal.ExecutionSnapshot,
	evidence json.RawMessage,
) (principal.ExecutionSnapshot, json.RawMessage, error) {
	workspaceID, actorType, actorID = strings.TrimSpace(workspaceID), strings.TrimSpace(actorType), strings.TrimSpace(actorID)
	var value principal.ExecutionSnapshot
	var err error
	if provided == nil {
		value, err = principal.NewInternalExecutionSnapshot(
			workspaceID, principal.Type(actorType), actorID,
		)
	} else {
		value = cloneExecutionSnapshot(*provided)
		err = value.Validate()
	}
	if err != nil || value.Identity.Actor.WorkspaceID != workspaceID ||
		string(value.Identity.Actor.Type) != actorType || value.Identity.Actor.ID != actorID {
		return principal.ExecutionSnapshot{}, nil, ErrRunInvalid
	}
	envelope, err := value.Envelope(evidence)
	if err != nil {
		return principal.ExecutionSnapshot{}, nil, ErrRunInvalid
	}
	return value, envelope, nil
}

func cloneExecutionSnapshot(value principal.ExecutionSnapshot) principal.ExecutionSnapshot {
	result := value
	if value.Identity.Subject != nil {
		subject := *value.Identity.Subject
		result.Identity.Subject = &subject
	}
	return result
}

func executionSnapshotArguments(value principal.ExecutionSnapshot) []any {
	var subjectType, subjectID any
	if value.Identity.Subject != nil {
		subjectType, subjectID = value.Identity.Subject.Type, value.Identity.Subject.ID
	}
	return []any{
		principal.ExecutionAuthorizationSpecV1, subjectType, subjectID,
		runNullableString(value.ClientID), runNullableString(value.GrantID),
		runNullableInt64(value.GrantVersion), runNullableInt64(value.AgentPolicyVersion),
	}
}

func scannedExecutionSnapshot(
	version, workspaceID, actorType, actorID string,
	subjectType, subjectID, clientID, grantID sql.NullString,
	grantVersion, policyVersion sql.NullInt64,
) (principal.ExecutionSnapshot, error) {
	actor, err := principal.RefFromLegacy(workspaceID, actorType, actorID)
	if err != nil {
		return principal.ExecutionSnapshot{}, err
	}
	var subject *principal.Ref
	if subjectType.Valid || subjectID.Valid {
		if !subjectType.Valid || !subjectID.Valid {
			return principal.ExecutionSnapshot{}, ErrRunInvalid
		}
		entry, refErr := principal.RefFromLegacy(workspaceID, subjectType.String, subjectID.String)
		if refErr != nil {
			return principal.ExecutionSnapshot{}, refErr
		}
		subject = &entry
	}
	identity, err := principal.NewInvocationIdentity(actor, subject)
	if err != nil {
		return principal.ExecutionSnapshot{}, err
	}
	value := principal.ExecutionSnapshot{
		Identity: identity, ClientID: clientID.String, GrantID: grantID.String,
		GrantVersion: grantVersion.Int64, AgentPolicyVersion: policyVersion.Int64,
	}
	if version == principal.ExecutionAuthorizationSpecV1 && value.Validate() != nil {
		return principal.ExecutionSnapshot{}, ErrRunInvalid
	}
	return value, nil
}
