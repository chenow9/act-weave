package outboundidentity

import (
	"strings"
	"time"
)

// Heartbeat / affinity operational defaults (T2=A).
const (
	// DefaultHeartbeatInterval is how often a live boot should refresh heartbeat_at.
	DefaultHeartbeatInterval = 10 * time.Second
	// DefaultHeartbeatStaleAfter marks an instance dead for routing / reclaim decisions.
	DefaultHeartbeatStaleAfter = 45 * time.Second
	// DefaultAffinityMaxDeadline bounds root_deadline_at relative to now on claim.
	DefaultAffinityMaxDeadline = 24 * time.Hour
	// MaxInternalRouteBodyBytes caps internal forward payloads (no Token).
	MaxInternalRouteBodyBytes = 64 * 1024
)

// RuntimeInstance is process registration metadata. Never holds Token / Vault keys.
type RuntimeInstance struct {
	InstanceID       string
	BootID           string
	WorkspaceScope   string // typically "cluster"
	InternalAddress  string // deploy-config only; never request-sourced
	RoutingPublicKey []byte // temporary per-boot public key; private key is process-local only
	HeartbeatAt      time.Time
	Draining         bool
	StartedAt        time.Time
	UpdatedAt        time.Time
}

// Valid reports structural validity (no secret material).
func (i RuntimeInstance) Valid() bool {
	return strings.TrimSpace(i.InstanceID) != "" &&
		len(i.InstanceID) <= 128 &&
		strings.TrimSpace(i.BootID) != "" &&
		len(i.BootID) <= 128 &&
		strings.TrimSpace(i.InternalAddress) != "" &&
		len(i.InternalAddress) <= 512 &&
		len(i.RoutingPublicKey) > 0
}

// Live reports whether the instance is eligible as a route target.
func (i RuntimeInstance) Live(now time.Time, staleAfter time.Duration) bool {
	if i.Draining {
		return false
	}
	if staleAfter <= 0 {
		staleAfter = DefaultHeartbeatStaleAfter
	}
	return i.HeartbeatAt.After(now.Add(-staleAfter))
}

// RuntimeAffinity pins a REQUEST_PASSTHROUGH root to one live boot.
// It is routing metadata only — presence must never be used to infer Token validity.
type RuntimeAffinity struct {
	WorkspaceID     string
	RootScopeType   RootScopeType
	RootScopeID     string // UUID string
	OwnerInstanceID string
	OwnerBootID     string
	RootDeadlineAt  time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Valid reports structural validity.
func (a RuntimeAffinity) Valid() bool {
	return strings.TrimSpace(a.WorkspaceID) != "" &&
		a.RootScopeType.Valid() &&
		a.RootScopeType != RootScopeDebugAttachment && // debug uses ephemeral locator, not DB affinity
		strings.TrimSpace(a.RootScopeID) != "" &&
		strings.TrimSpace(a.OwnerInstanceID) != "" &&
		strings.TrimSpace(a.OwnerBootID) != "" &&
		!a.RootDeadlineAt.IsZero()
}

// OwnedBy reports whether this affinity belongs to the given boot.
func (a RuntimeAffinity) OwnedBy(instanceID, bootID string) bool {
	return a.OwnerInstanceID == instanceID && a.OwnerBootID == bootID
}

// Expired reports whether the affinity deadline has passed.
func (a RuntimeAffinity) Expired(now time.Time) bool {
	return !a.RootDeadlineAt.After(now)
}

// RouteKind classifies where a continuation / internal command should go.
type RouteKind string

const (
	// RouteLocal: this process is the live owner; handle here.
	RouteLocal RouteKind = "LOCAL"
	// RouteForward: another live owner holds affinity; forward command without Token.
	RouteForward RouteKind = "FORWARD"
	// RouteExpired: owner lost / boot changed / deadline past — fail closed.
	RouteExpired RouteKind = "EXPIRED"
	// RouteNone: no passthrough affinity; ordinary multi-replica recovery allowed.
	RouteNone RouteKind = "NONE"
)

// RouteDecision is the internal router result. Never carries Token or Vault keys.
type RouteDecision struct {
	Kind            RouteKind
	WorkspaceID     string
	RootScopeType   RootScopeType
	RootScopeID     string
	OwnerInstanceID string
	OwnerBootID     string
	InternalAddress string // set only for FORWARD; deploy-registered address
	// ReasonCode is a stable outbound error code for EXPIRED (safe for logs).
	ReasonCode string
}

// AffinityClaimRequest creates CAS ownership for a passthrough root.
type AffinityClaimRequest struct {
	WorkspaceID     string
	RootScopeType   RootScopeType
	RootScopeID     string
	OwnerInstanceID string
	OwnerBootID     string
	RootDeadlineAt  time.Time
	// RequiresPassthrough must be true. Pure BROKER_OBO roots must not claim affinity.
	RequiresPassthrough bool
}

// InternalRouteCommand is the token-free payload forwarded between instances.
// Schema is fixed; unknown fields must be rejected by the decoder at the wire layer.
type InternalRouteCommand struct {
	SchemaVersion string `json:"schemaVersion"`
	WorkspaceID   string `json:"workspaceId"`
	RootScopeType string `json:"rootScopeType"`
	RootScopeID   string `json:"rootScopeId"`
	CommandType   string `json:"commandType"`
	// OpaqueLocator is an optional short-lived signed debug locator (never a Token).
	OpaqueLocator string `json:"opaqueLocator,omitempty"`
	// IssuedAt / ExpiresAt bound replay windows without encoding credential state.
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Nonce     string    `json:"nonce"`
}

// Allowed internal command types (no credential material).
const (
	InternalRouteSchemaVersion    = "outbound-internal-route.v1"
	InternalCommandContinue       = "CONTINUE"
	InternalCommandDebugDeliver   = "DEBUG_DELIVER"
	InternalCommandHeartbeatProbe = "HEARTBEAT_PROBE"
	MaxInternalRouteSkew          = 2 * time.Minute
	MaxInternalRouteTTL           = 30 * time.Second
)

// Valid performs structural validation (caller still enforces authn + size).
func (c InternalRouteCommand) Valid(now time.Time) bool {
	if c.SchemaVersion != InternalRouteSchemaVersion {
		return false
	}
	if strings.TrimSpace(c.WorkspaceID) == "" ||
		strings.TrimSpace(c.RootScopeID) == "" ||
		strings.TrimSpace(c.Nonce) == "" {
		return false
	}
	switch c.CommandType {
	case InternalCommandContinue, InternalCommandDebugDeliver, InternalCommandHeartbeatProbe:
	default:
		return false
	}
	if !RootScopeType(c.RootScopeType).Valid() {
		return false
	}
	if c.IssuedAt.IsZero() || c.ExpiresAt.IsZero() {
		return false
	}
	if !c.ExpiresAt.After(c.IssuedAt) {
		return false
	}
	if c.ExpiresAt.Sub(c.IssuedAt) > MaxInternalRouteTTL {
		return false
	}
	// Replay / skew window.
	if c.ExpiresAt.Before(now.Add(-MaxInternalRouteSkew)) {
		return false
	}
	if c.IssuedAt.After(now.Add(MaxInternalRouteSkew)) {
		return false
	}
	return true
}
