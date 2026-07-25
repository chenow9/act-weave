package outboundidentity

import (
	"encoding/json"
	"sort"
	"strings"
)

// Requirements is the published descriptor outbound-requirements.v1.
// It contains only Connection / Provider IDs, fixed mode, policy versions,
// normalized required scopes, and credentialRequired. It never contains Secret,
// Token, Vault key, attachment ID, or locator fields.
type Requirements struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Connections   []RequirementConnection `json:"connections"`
}

// RequirementConnection is one Connection requirement in a plan/snapshot.
type RequirementConnection struct {
	ConnectionID            string   `json:"connectionId"`
	ProviderID              string   `json:"providerId"`
	Mode                    Mode     `json:"mode"`
	ProviderContractVersion int64    `json:"providerContractVersion"`
	ConnectionPolicyVersion int64    `json:"connectionPolicyVersion"`
	RequiredScopes          []string `json:"requiredScopes"`
	CredentialRequired      bool     `json:"credentialRequired"`
}

// ParseRequirements decodes and validates outbound-requirements.v1.
func ParseRequirements(raw json.RawMessage) (Requirements, error) {
	var requirements Requirements
	if err := decodeStrictJSON(raw, &requirements); err != nil {
		return Requirements{}, ErrIdentityPolicyInvalid.Wrap(err)
	}
	return NormalizeRequirements(requirements)
}

// NormalizeRequirements validates, normalizes scopes, and sorts connections by
// ConnectionID for stable compare/clone semantics.
func NormalizeRequirements(requirements Requirements) (Requirements, error) {
	if strings.TrimSpace(requirements.SchemaVersion) != SchemaRequirements {
		return Requirements{}, ErrIdentityPolicyInvalid
	}
	if len(requirements.Connections) > 64 {
		return Requirements{}, ErrIdentityPolicyInvalid
	}
	// Empty connections is valid (non-HTTP or no outbound identity).
	normalized := make([]RequirementConnection, 0, len(requirements.Connections))
	seen := map[string]struct{}{}
	for _, item := range requirements.Connections {
		connectionID := strings.TrimSpace(item.ConnectionID)
		providerID := strings.TrimSpace(item.ProviderID)
		if connectionID == "" || providerID == "" || len(connectionID) > 64 || len(providerID) > 64 {
			return Requirements{}, ErrIdentityPolicyInvalid
		}
		if _, dup := seen[connectionID]; dup {
			return Requirements{}, ErrIdentityPolicyInvalid
		}
		seen[connectionID] = struct{}{}
		mode, ok := normalizeMode(string(item.Mode))
		if !ok {
			return Requirements{}, ErrIdentityModeUnsupported
		}
		if item.ProviderContractVersion <= 0 || item.ConnectionPolicyVersion <= 0 {
			return Requirements{}, ErrIdentityPolicyInvalid
		}
		scopes, err := normalizeScopes(item.RequiredScopes, true)
		if err != nil {
			return Requirements{}, err
		}
		credentialRequired := item.CredentialRequired
		if mode == ModeRequestPassthrough {
			// Passthrough requirements always require a binding when the connection
			// is in the allowlist for credential-bearing invocations.
			// Spec leaves credentialRequired as explicit; enforce consistency:
			// Broker must never mark credentialRequired (caller token).
		}
		if mode == ModeBrokerOBO && credentialRequired {
			// Broker acquires credentials; callers never supply Token.
			return Requirements{}, ErrIdentityPolicyInvalid
		}
		normalized = append(normalized, RequirementConnection{
			ConnectionID:            connectionID,
			ProviderID:              providerID,
			Mode:                    mode,
			ProviderContractVersion: item.ProviderContractVersion,
			ConnectionPolicyVersion: item.ConnectionPolicyVersion,
			RequiredScopes:          scopes,
			CredentialRequired:      credentialRequired,
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].ConnectionID < normalized[j].ConnectionID
	})
	return Requirements{
		SchemaVersion: SchemaRequirements,
		Connections:   normalized,
	}, nil
}

// CloneRequirements returns a deep copy.
func CloneRequirements(requirements Requirements) Requirements {
	cloned := Requirements{SchemaVersion: requirements.SchemaVersion}
	if len(requirements.Connections) == 0 {
		return cloned
	}
	cloned.Connections = make([]RequirementConnection, len(requirements.Connections))
	for i, item := range requirements.Connections {
		copyItem := item
		copyItem.RequiredScopes = append([]string(nil), item.RequiredScopes...)
		cloned.Connections[i] = copyItem
	}
	return cloned
}

// EqualRequirements compares two normalized requirement descriptors.
func EqualRequirements(a, b Requirements) bool {
	if a.SchemaVersion != b.SchemaVersion || len(a.Connections) != len(b.Connections) {
		return false
	}
	for i := range a.Connections {
		left, right := a.Connections[i], b.Connections[i]
		if left.ConnectionID != right.ConnectionID || left.ProviderID != right.ProviderID ||
			left.Mode != right.Mode || left.ProviderContractVersion != right.ProviderContractVersion ||
			left.ConnectionPolicyVersion != right.ConnectionPolicyVersion ||
			left.CredentialRequired != right.CredentialRequired ||
			len(left.RequiredScopes) != len(right.RequiredScopes) {
			return false
		}
		for j := range left.RequiredScopes {
			if left.RequiredScopes[j] != right.RequiredScopes[j] {
				return false
			}
		}
	}
	return true
}

// ConnectionIDs returns sorted Connection IDs.
func (r Requirements) ConnectionIDs() []string {
	ids := make([]string, 0, len(r.Connections))
	for _, item := range r.Connections {
		ids = append(ids, item.ConnectionID)
	}
	return ids
}

// Lookup returns the requirement for a Connection ID.
func (r Requirements) Lookup(connectionID string) (RequirementConnection, bool) {
	connectionID = strings.TrimSpace(connectionID)
	for _, item := range r.Connections {
		if item.ConnectionID == connectionID {
			return item, true
		}
	}
	return RequirementConnection{}, false
}

// PassthroughRequired reports whether any connection requires a passthrough binding.
func (r Requirements) PassthroughRequired() bool {
	for _, item := range r.Connections {
		if item.Mode == ModeRequestPassthrough && item.CredentialRequired {
			return true
		}
	}
	return false
}

// BuildRequirementConnection constructs a requirement row from validated
// Provider / Connection contracts. It never copies Secret or Token material.
func BuildRequirementConnection(
	connectionID, providerID string,
	connection ConnectionIdentity,
	providerPolicyVersion int64,
	requiredScopes []string,
) (RequirementConnection, error) {
	normalizedConnection, err := NormalizeConnectionIdentity(connection)
	if err != nil {
		return RequirementConnection{}, err
	}
	if strings.TrimSpace(connectionID) == "" || strings.TrimSpace(providerID) == "" {
		return RequirementConnection{}, ErrIdentityPolicyInvalid
	}
	if providerPolicyVersion <= 0 || normalizedConnection.PolicyVersion <= 0 {
		return RequirementConnection{}, ErrIdentityPolicyInvalid
	}
	scopes, err := normalizeScopes(requiredScopes, true)
	if err != nil {
		return RequirementConnection{}, err
	}
	if normalizedConnection.Mode == ModeBrokerOBO && normalizedConnection.BrokerOBO != nil {
		// Runtime required scopes must be subset of Connection scopes when Connection scopes are non-empty.
		if len(normalizedConnection.BrokerOBO.Scopes) > 0 {
			allowed := map[string]struct{}{}
			for _, scope := range normalizedConnection.BrokerOBO.Scopes {
				allowed[scope] = struct{}{}
			}
			for _, scope := range scopes {
				if _, ok := allowed[scope]; !ok {
					return RequirementConnection{}, ErrIdentityScopeNotAllowed
				}
			}
		}
	}
	credentialRequired := normalizedConnection.Mode == ModeRequestPassthrough
	return RequirementConnection{
		ConnectionID:            strings.TrimSpace(connectionID),
		ProviderID:              strings.TrimSpace(providerID),
		Mode:                    normalizedConnection.Mode,
		ProviderContractVersion: providerPolicyVersion,
		ConnectionPolicyVersion: normalizedConnection.PolicyVersion,
		RequiredScopes:          scopes,
		CredentialRequired:      credentialRequired,
	}, nil
}
