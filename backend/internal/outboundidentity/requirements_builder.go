package outboundidentity

import (
	"encoding/json"
	"strings"
)

// ConnectionReadiness is the dual-mode readiness signal used by compiler and
// invocation resolvers. It does not contain Secret material.
type ConnectionReadiness struct {
	ConnectionID            string
	ProviderID              string
	Status                  string
	MigrationState          string
	OutboundIdentity        json.RawMessage
	ConnectionPolicyVersion int64
	ProviderPolicyVersion   int64
	ProviderDriverConfig    json.RawMessage
	MachineCredentialActive bool
}

// AssessConnectionReadiness validates that a connection may be used for HTTP
// outbound identity under frozen dual-mode rules.
func AssessConnectionReadiness(readiness ConnectionReadiness) error {
	if strings.TrimSpace(readiness.ConnectionID) == "" || strings.TrimSpace(readiness.ProviderID) == "" {
		return ErrIdentityPolicyInvalid
	}
	if readiness.MigrationState == MigrationStateMigrationRequired {
		return ErrIdentityMigrationRequired
	}
	if readiness.Status != "VERIFIED" || readiness.MigrationState != MigrationStateNone {
		return ErrIdentityConnectionNotReady
	}
	if readiness.ConnectionPolicyVersion <= 0 || readiness.ProviderPolicyVersion <= 0 {
		return ErrIdentityPolicyInvalid
	}
	if len(readiness.OutboundIdentity) == 0 || string(readiness.OutboundIdentity) == "null" {
		// Missing dual-mode contract: treat as migration required (hard cut).
		return ErrIdentityMigrationRequired
	}
	connection, err := ParseConnectionIdentity(readiness.OutboundIdentity)
	if err != nil {
		return err
	}
	// Align column policy version into the identity for builders.
	connection.PolicyVersion = readiness.ConnectionPolicyVersion
	provider, err := parseProviderIdentityFromDriver(readiness.ProviderDriverConfig)
	if err != nil {
		return err
	}
	if err := ValidateConnectionAgainstProvider(connection, provider, readiness.MachineCredentialActive); err != nil {
		return err
	}
	return nil
}

// BuildRequirementsFromConnections builds outbound-requirements.v1 from ready
// dual-mode connections. requiredScopesByConnection is optional.
func BuildRequirementsFromConnections(
	items []ConnectionReadiness,
	requiredScopesByConnection map[string][]string,
) (Requirements, error) {
	if len(items) == 0 {
		return Requirements{SchemaVersion: SchemaRequirements, Connections: nil}, nil
	}
	connections := make([]RequirementConnection, 0, len(items))
	for _, item := range items {
		if err := AssessConnectionReadiness(item); err != nil {
			return Requirements{}, err
		}
		identity, err := ParseConnectionIdentity(item.OutboundIdentity)
		if err != nil {
			return Requirements{}, err
		}
		identity.PolicyVersion = item.ConnectionPolicyVersion
		scopes := requiredScopesByConnection[item.ConnectionID]
		built, err := BuildRequirementConnection(
			item.ConnectionID, item.ProviderID, identity, item.ProviderPolicyVersion, scopes,
		)
		if err != nil {
			return Requirements{}, err
		}
		connections = append(connections, built)
	}
	return NormalizeRequirements(Requirements{
		SchemaVersion: SchemaRequirements,
		Connections:   connections,
	})
}

// DetectPolicyDrift returns ErrIdentityPolicyChanged when live versions diverge
// from a previously published requirements snapshot.
func DetectPolicyDrift(snapshot Requirements, live []ConnectionReadiness) error {
	normalized, err := NormalizeRequirements(snapshot)
	if err != nil {
		return err
	}
	liveByID := make(map[string]ConnectionReadiness, len(live))
	for _, item := range live {
		liveByID[item.ConnectionID] = item
	}
	for _, required := range normalized.Connections {
		current, ok := liveByID[required.ConnectionID]
		if !ok {
			return ErrIdentityPolicyChanged
		}
		if err := AssessConnectionReadiness(current); err != nil {
			return err
		}
		if current.ProviderPolicyVersion != required.ProviderContractVersion ||
			current.ConnectionPolicyVersion != required.ConnectionPolicyVersion {
			return ErrIdentityPolicyChanged
		}
		identity, err := ParseConnectionIdentity(current.OutboundIdentity)
		if err != nil {
			return err
		}
		if identity.Mode != required.Mode {
			return ErrIdentityPolicyChanged
		}
	}
	return nil
}

func parseProviderIdentityFromDriver(driverConfig json.RawMessage) (ProviderIdentity, error) {
	var envelope struct {
		OutboundIdentity json.RawMessage `json:"outboundIdentity"`
		Authentication   json.RawMessage `json:"authentication"`
	}
	if len(strings.TrimSpace(string(driverConfig))) == 0 {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}
	if json.Unmarshal(driverConfig, &envelope) != nil {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}
	if len(envelope.Authentication) > 0 && string(envelope.Authentication) != "null" {
		return ProviderIdentity{}, ErrIdentityModeUnsupported
	}
	if len(envelope.OutboundIdentity) == 0 {
		return ProviderIdentity{}, ErrIdentityMigrationRequired
	}
	return ParseProviderIdentity(envelope.OutboundIdentity)
}

// RequirementsJSON marshals a requirements descriptor for plan/snapshot storage.
func RequirementsJSON(requirements Requirements) (json.RawMessage, error) {
	normalized, err := NormalizeRequirements(requirements)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// ParseRequirementsJSON is a convenience wrapper used by publish/readiness.
func ParseRequirementsJSON(raw json.RawMessage) (Requirements, error) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return Requirements{SchemaVersion: SchemaRequirements}, nil
	}
	return ParseRequirements(raw)
}
