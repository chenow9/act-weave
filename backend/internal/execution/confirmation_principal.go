package execution

import (
	"database/sql"
	"encoding/json"
	"strings"

	"actweave/backend/internal/principal"
)

const (
	confirmationDecisionModeUser             = "actweave_user"
	confirmationDecisionModeExternalSubject  = "external_subject"
	confirmationDecisionModeServicePrincipal = "service_principal"
)

func prepareConfirmationRequestPrincipal(
	workspaceID, requestedBy string,
	provided *principal.ExecutionSnapshot,
) (principal.ExecutionSnapshot, error) {
	workspaceID, requestedBy = strings.TrimSpace(workspaceID), strings.TrimSpace(requestedBy)
	if provided == nil {
		value, err := principal.NewInternalExecutionSnapshot(
			workspaceID, principal.TypeUser, requestedBy,
		)
		if err != nil {
			return principal.ExecutionSnapshot{}, ErrConfirmationInvalid
		}
		return value, nil
	}
	value := cloneExecutionSnapshot(*provided)
	if value.Validate() != nil || value.Identity.Actor.WorkspaceID != workspaceID ||
		(requestedBy != "" && requestedBy != value.Identity.Actor.ID) {
		return principal.ExecutionSnapshot{}, ErrConfirmationInvalid
	}
	return value, nil
}

func prepareConfirmationDecisionPrincipal(
	workspaceID, actorID string,
	provided *principal.ExecutionSnapshot,
) (principal.ExecutionSnapshot, error) {
	workspaceID, actorID = strings.TrimSpace(workspaceID), strings.TrimSpace(actorID)
	if provided == nil {
		value, err := principal.NewInternalExecutionSnapshot(
			workspaceID, principal.TypeUser, actorID,
		)
		if err != nil {
			return principal.ExecutionSnapshot{}, ErrConfirmationInvalid
		}
		return value, nil
	}
	value := cloneExecutionSnapshot(*provided)
	if value.Validate() != nil || value.Identity.Actor.WorkspaceID != workspaceID ||
		(actorID != "" && actorID != value.Identity.Actor.ID) {
		return principal.ExecutionSnapshot{}, ErrConfirmationInvalid
	}
	return value, nil
}

func buildConfirmationDecisionPolicySnapshot(
	value principal.ExecutionSnapshot,
	policy *ServicePrincipalDecisionPolicy,
) (json.RawMessage, error) {
	mode := confirmationDecisionModeUser
	document := map[string]any{}
	switch value.Identity.Actor.Type {
	case principal.TypeUser:
		if value.Identity.Subject == nil || value.Identity.Subject.Type != principal.TypeUser || policy != nil {
			return nil, ErrConfirmationInvalid
		}
	case principal.TypeServicePrincipal:
		if value.Identity.Subject != nil {
			if value.Identity.Subject.Type != principal.TypeExternalSubject || policy != nil {
				return nil, ErrConfirmationInvalid
			}
			mode = confirmationDecisionModeExternalSubject
			break
		}
		if policy == nil || !policy.Enabled {
			return nil, ErrConfirmationRequesterMismatch
		}
		maxRisk := strings.ToLower(strings.TrimSpace(policy.MaxRisk))
		if maxRisk != "low" && maxRisk != "medium" {
			return nil, ErrConfirmationInvalid
		}
		mode = confirmationDecisionModeServicePrincipal
		document["enabled"] = true
		document["maxRisk"] = maxRisk
	default:
		return nil, ErrConfirmationInvalid
	}
	document["mode"] = mode
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrConfirmationInvalid
	}
	return encoded, nil
}

func confirmationUserProjection(value principal.ExecutionSnapshot) any {
	if value.Identity.Actor.Type == principal.TypeUser {
		return value.Identity.Actor.ID
	}
	return nil
}

func confirmationSnapshotArguments(value principal.ExecutionSnapshot) []any {
	var subjectType, subjectID any
	if value.Identity.Subject != nil {
		subjectType, subjectID = value.Identity.Subject.Type, value.Identity.Subject.ID
	}
	return []any{
		principal.ExecutionAuthorizationSpecV1,
		value.Identity.Actor.Type, value.Identity.Actor.ID,
		subjectType, subjectID,
		nullableConfirmationString(value.ClientID),
		nullableConfirmationString(value.GrantID),
		nullableConfirmationInt64(value.GrantVersion),
		nullableConfirmationInt64(value.AgentPolicyVersion),
	}
}

func nullableConfirmationInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func scanConfirmationSnapshot(
	version, workspaceID string,
	actorType, actorID, subjectType, subjectID, clientID, grantID sql.NullString,
	grantVersion, policyVersion sql.NullInt64,
) (*principal.ExecutionSnapshot, error) {
	if !actorType.Valid && !actorID.Valid && strings.TrimSpace(version) == "" {
		return nil, nil
	}
	if !actorType.Valid || !actorID.Valid || strings.TrimSpace(version) == "" {
		return nil, ErrConfirmationInvalid
	}
	value, err := scannedExecutionSnapshot(
		version, workspaceID, actorType.String, actorID.String,
		subjectType, subjectID, clientID, grantID, grantVersion, policyVersion,
	)
	if err != nil {
		return nil, ErrConfirmationInvalid
	}
	return &value, nil
}
