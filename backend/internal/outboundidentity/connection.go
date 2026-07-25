package outboundidentity

import (
	"encoding/json"
	"strings"
)

// Connection identity TTL bounds from technical design §4.2.
const (
	DefaultMaxTokenTTLSeconds  = 300
	MinMaxTokenTTLSeconds      = 30
	MaxMaxTokenTTLSeconds      = 900
	DefaultMaxResidenceSeconds = 600
	MinMaxResidenceSeconds     = 30
	MaxMaxResidenceSeconds     = 3600
)

// ConnectionIdentity is the normalized Connection contract outbound-connection.v1.
// policyVersion is server-derived and read-only for clients; when present in JSON
// it must be a positive integer and is preserved for snapshots, never client-written
// as a source of truth.
//
// This type never contains Secret IDs, Token values, Vault keys, or locators.
type ConnectionIdentity struct {
	SchemaVersion      string                 `json:"schemaVersion"`
	Mode               Mode                   `json:"mode"`
	PolicyVersion      int64                  `json:"policyVersion,omitempty"`
	BrokerOBO          *ConnectionBrokerOBO   `json:"brokerObo,omitempty"`
	RequestPassthrough *ConnectionPassthrough `json:"requestPassthrough,omitempty"`
}

// ConnectionBrokerOBO is Connection-level Broker selection (no Secret fields).
type ConnectionBrokerOBO struct {
	ClientID           string   `json:"clientId"`
	Scopes             []string `json:"scopes"`
	MaxTokenTTLSeconds int      `json:"maxTokenTtlSeconds"`
}

// ConnectionPassthrough is Connection-level residence policy for request tokens.
type ConnectionPassthrough struct {
	MaxResidenceSeconds int `json:"maxResidenceSeconds"`
}

// ParseConnectionIdentity decodes and validates outbound-connection.v1.
func ParseConnectionIdentity(raw json.RawMessage) (ConnectionIdentity, error) {
	var identity ConnectionIdentity
	if err := decodeStrictJSON(raw, &identity); err != nil {
		return ConnectionIdentity{}, ErrIdentityPolicyInvalid.Wrap(err)
	}
	return NormalizeConnectionIdentity(identity)
}

// NormalizeConnectionIdentity validates and normalizes a Connection contract.
// policyVersion may be zero when validating write input (server assigns it);
// when non-zero it must be > 0.
func NormalizeConnectionIdentity(identity ConnectionIdentity) (ConnectionIdentity, error) {
	if strings.TrimSpace(identity.SchemaVersion) != SchemaConnection {
		return ConnectionIdentity{}, ErrIdentityPolicyInvalid
	}
	mode, ok := normalizeMode(string(identity.Mode))
	if !ok {
		return ConnectionIdentity{}, ErrIdentityModeUnsupported
	}
	if identity.PolicyVersion < 0 {
		return ConnectionIdentity{}, ErrIdentityPolicyInvalid
	}

	var broker *ConnectionBrokerOBO
	var passthrough *ConnectionPassthrough
	switch mode {
	case ModeBrokerOBO:
		if identity.BrokerOBO == nil || identity.RequestPassthrough != nil {
			return ConnectionIdentity{}, ErrIdentityPolicyInvalid
		}
		normalized, err := normalizeConnectionBroker(*identity.BrokerOBO)
		if err != nil {
			return ConnectionIdentity{}, err
		}
		broker = &normalized
	case ModeRequestPassthrough:
		if identity.RequestPassthrough == nil || identity.BrokerOBO != nil {
			return ConnectionIdentity{}, ErrIdentityPolicyInvalid
		}
		normalized, err := normalizeConnectionPassthrough(*identity.RequestPassthrough)
		if err != nil {
			return ConnectionIdentity{}, err
		}
		passthrough = &normalized
	default:
		return ConnectionIdentity{}, ErrIdentityModeUnsupported
	}

	return ConnectionIdentity{
		SchemaVersion:      SchemaConnection,
		Mode:               mode,
		PolicyVersion:      identity.PolicyVersion,
		BrokerOBO:          broker,
		RequestPassthrough: passthrough,
	}, nil
}

// ValidateConnectionAgainstProvider ensures Connection mode and scopes are
// compatible with the Provider contract. machineCredentialConfigured is supplied
// by the repository/DTO layer (never stored inside this JSON contract).
func ValidateConnectionAgainstProvider(
	connection ConnectionIdentity,
	provider ProviderIdentity,
	machineCredentialConfigured bool,
) error {
	normalizedConnection, err := NormalizeConnectionIdentity(connection)
	if err != nil {
		return err
	}
	normalizedProvider, err := NormalizeProviderIdentity(provider)
	if err != nil {
		return err
	}
	if !normalizedProvider.SupportsMode(normalizedConnection.Mode) {
		return ErrIdentityModeUnsupported
	}
	switch normalizedConnection.Mode {
	case ModeBrokerOBO:
		if !machineCredentialConfigured {
			return ErrIdentityConnectionNotReady
		}
		if normalizedProvider.BrokerOBO == nil || normalizedConnection.BrokerOBO == nil {
			return ErrIdentityPolicyInvalid
		}
		allowed := map[string]struct{}{}
		for _, scope := range normalizedProvider.BrokerOBO.AllowedScopes {
			allowed[scope] = struct{}{}
		}
		for _, scope := range normalizedConnection.BrokerOBO.Scopes {
			if _, ok := allowed[scope]; !ok {
				return ErrIdentityScopeNotAllowed
			}
		}
	case ModeRequestPassthrough:
		if machineCredentialConfigured {
			// REQUEST_PASSTHROUGH must not reference business or machine Secrets.
			return ErrIdentityPolicyInvalid
		}
		if normalizedProvider.RequestPassthrough == nil || normalizedConnection.RequestPassthrough == nil {
			return ErrIdentityPolicyInvalid
		}
	}
	return nil
}

// CloneConnectionIdentity returns a deep copy.
func CloneConnectionIdentity(identity ConnectionIdentity) ConnectionIdentity {
	cloned := ConnectionIdentity{
		SchemaVersion: identity.SchemaVersion,
		Mode:          identity.Mode,
		PolicyVersion: identity.PolicyVersion,
	}
	if identity.BrokerOBO != nil {
		broker := *identity.BrokerOBO
		broker.Scopes = append([]string(nil), identity.BrokerOBO.Scopes...)
		cloned.BrokerOBO = &broker
	}
	if identity.RequestPassthrough != nil {
		pt := *identity.RequestPassthrough
		cloned.RequestPassthrough = &pt
	}
	return cloned
}

// EqualConnectionIdentity compares two normalized Connection contracts.
func EqualConnectionIdentity(a, b ConnectionIdentity) bool {
	if a.SchemaVersion != b.SchemaVersion || a.Mode != b.Mode || a.PolicyVersion != b.PolicyVersion {
		return false
	}
	if (a.BrokerOBO == nil) != (b.BrokerOBO == nil) {
		return false
	}
	if a.BrokerOBO != nil {
		if a.BrokerOBO.ClientID != b.BrokerOBO.ClientID ||
			a.BrokerOBO.MaxTokenTTLSeconds != b.BrokerOBO.MaxTokenTTLSeconds ||
			len(a.BrokerOBO.Scopes) != len(b.BrokerOBO.Scopes) {
			return false
		}
		for i := range a.BrokerOBO.Scopes {
			if a.BrokerOBO.Scopes[i] != b.BrokerOBO.Scopes[i] {
				return false
			}
		}
	}
	if (a.RequestPassthrough == nil) != (b.RequestPassthrough == nil) {
		return false
	}
	if a.RequestPassthrough != nil &&
		a.RequestPassthrough.MaxResidenceSeconds != b.RequestPassthrough.MaxResidenceSeconds {
		return false
	}
	return true
}

func normalizeConnectionBroker(cfg ConnectionBrokerOBO) (ConnectionBrokerOBO, error) {
	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" || len(clientID) > 256 || strings.ContainsAny(clientID, "\r\n\x00") {
		return ConnectionBrokerOBO{}, ErrIdentityPolicyInvalid
	}
	scopes, err := normalizeScopes(cfg.Scopes, true)
	if err != nil {
		return ConnectionBrokerOBO{}, err
	}
	ttl := cfg.MaxTokenTTLSeconds
	if ttl == 0 {
		ttl = DefaultMaxTokenTTLSeconds
	}
	if ttl < MinMaxTokenTTLSeconds || ttl > MaxMaxTokenTTLSeconds {
		return ConnectionBrokerOBO{}, ErrIdentityPolicyInvalid
	}
	return ConnectionBrokerOBO{
		ClientID:           clientID,
		Scopes:             scopes,
		MaxTokenTTLSeconds: ttl,
	}, nil
}

func normalizeConnectionPassthrough(cfg ConnectionPassthrough) (ConnectionPassthrough, error) {
	residence := cfg.MaxResidenceSeconds
	if residence == 0 {
		residence = DefaultMaxResidenceSeconds
	}
	if residence < MinMaxResidenceSeconds || residence > MaxMaxResidenceSeconds {
		return ConnectionPassthrough{}, ErrIdentityPolicyInvalid
	}
	return ConnectionPassthrough{MaxResidenceSeconds: residence}, nil
}
