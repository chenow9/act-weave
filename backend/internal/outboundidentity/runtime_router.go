package outboundidentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// RuntimeRouter decides local handle vs authenticated forward vs fail-closed.
// It never transports Token / Vault plaintext / credential locators that encode Token state.
type RuntimeRouter struct {
	repo            *RuntimeRepository
	localInstanceID string
	localBootID     string
	staleAfter      time.Duration
	now             func() time.Time

	// nonceReplay is a tiny process-local set to reject internal command replay.
	mu     sync.Mutex
	nonces map[string]time.Time
}

// NewRuntimeRouter builds a router bound to this process boot.
func NewRuntimeRouter(repo *RuntimeRepository, localInstanceID, localBootID string, staleAfter time.Duration) (*RuntimeRouter, error) {
	if repo == nil {
		return nil, errors.New("runtime router repository is required")
	}
	localInstanceID = strings.TrimSpace(localInstanceID)
	localBootID = strings.TrimSpace(localBootID)
	if localInstanceID == "" || localBootID == "" {
		return nil, errors.New("runtime router local instance/boot is required")
	}
	if staleAfter <= 0 {
		staleAfter = DefaultHeartbeatStaleAfter
	}
	return &RuntimeRouter{
		repo:            repo,
		localInstanceID: localInstanceID,
		localBootID:     localBootID,
		staleAfter:      staleAfter,
		now:             func() time.Time { return time.Now().UTC() },
		nonces:          make(map[string]time.Time),
	}, nil
}

// WithClock overrides time for tests.
func (r *RuntimeRouter) WithClock(now func() time.Time) *RuntimeRouter {
	if r != nil && now != nil {
		r.now = now
	}
	return r
}

// LocalIdentity returns this process's instance/boot pair.
func (r *RuntimeRouter) LocalIdentity() (instanceID, bootID string) {
	if r == nil {
		return "", ""
	}
	return r.localInstanceID, r.localBootID
}

// Route resolves where a root continuation should run.
// NoAffinity (RouteNone) means pure Broker / non-passthrough — multi-replica reclaim OK.
func (r *RuntimeRouter) Route(ctx context.Context, workspaceID string, rootType RootScopeType, rootID string) (RouteDecision, error) {
	base := RouteDecision{
		WorkspaceID: workspaceID, RootScopeType: rootType, RootScopeID: rootID,
	}
	if r == nil || r.repo == nil || ctx == nil {
		base.Kind = RouteExpired
		base.ReasonCode = CodeCredentialExpired
		return base, ErrCredentialInvalid
	}
	affinity, err := r.repo.GetAffinity(ctx, workspaceID, rootType, rootID)
	if err != nil {
		if errors.Is(err, ErrAffinityNotFound) {
			base.Kind = RouteNone
			return base, nil
		}
		return base, err
	}
	base.OwnerInstanceID = affinity.OwnerInstanceID
	base.OwnerBootID = affinity.OwnerBootID
	now := r.now().UTC()
	if affinity.Expired(now) {
		base.Kind = RouteExpired
		base.ReasonCode = CodeCredentialExpired
		return base, nil
	}
	if affinity.OwnedBy(r.localInstanceID, r.localBootID) {
		// Confirm local registration is still live.
		inst, instErr := r.repo.GetInstance(ctx, r.localInstanceID, r.localBootID)
		if instErr != nil || !inst.Live(now, r.staleAfter) {
			base.Kind = RouteExpired
			base.ReasonCode = CodeCredentialExpired
			return base, nil
		}
		base.Kind = RouteLocal
		return base, nil
	}
	// Remote owner: only forward if still live.
	inst, instErr := r.repo.GetInstance(ctx, affinity.OwnerInstanceID, affinity.OwnerBootID)
	if instErr != nil || !inst.Live(now, r.staleAfter) {
		base.Kind = RouteExpired
		base.ReasonCode = CodeCredentialExpired
		return base, nil
	}
	base.Kind = RouteForward
	base.InternalAddress = inst.InternalAddress
	return base, nil
}

// ContinuationGate is used by recovery workers before taking side-effect claims.
// Allow=true only when RouteNone (no passthrough) or RouteLocal.
// Skip=true when another live owner holds affinity (do not steal).
// FailClosed=true when affinity exists but owner is lost / expired.
type ContinuationGate struct {
	Allow      bool
	Skip       bool
	FailClosed bool
	Decision   RouteDecision
}

// GateContinuation classifies recovery eligibility for a potential agent run root.
func (r *RuntimeRouter) GateContinuation(ctx context.Context, workspaceID, runID string) (ContinuationGate, error) {
	decision, err := r.Route(ctx, workspaceID, RootScopeAgentRun, runID)
	if err != nil {
		return ContinuationGate{}, err
	}
	switch decision.Kind {
	case RouteNone:
		return ContinuationGate{Allow: true, Decision: decision}, nil
	case RouteLocal:
		return ContinuationGate{Allow: true, Decision: decision}, nil
	case RouteForward:
		return ContinuationGate{Skip: true, Decision: decision}, nil
	default:
		return ContinuationGate{FailClosed: true, Decision: decision}, nil
	}
}

// ValidateInternalCommand checks schema, TTL, size and nonce replay for a forward payload.
// Caller must already have authenticated the peer via workload identity / mTLS.
func (r *RuntimeRouter) ValidateInternalCommand(raw []byte, now time.Time) (InternalRouteCommand, error) {
	var zero InternalRouteCommand
	if r == nil {
		return zero, ErrCredentialInvalid
	}
	if len(raw) == 0 || len(raw) > MaxInternalRouteBodyBytes {
		return zero, ErrCredentialInvalid
	}
	// Strict JSON — reject unknown fields.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var cmd InternalRouteCommand
	if err := dec.Decode(&cmd); err != nil {
		return zero, ErrCredentialInvalid
	}
	// Trailing data rejected by second decode attempt.
	if dec.More() {
		return zero, ErrCredentialInvalid
	}
	if now.IsZero() {
		now = r.now().UTC()
	} else {
		now = now.UTC()
	}
	if !cmd.Valid(now) {
		return zero, ErrCredentialInvalid
	}
	// Nonce replay protection (process-local; multi-boot uses short TTL).
	r.mu.Lock()
	defer r.mu.Unlock()
	r.purgeNoncesLocked(now)
	if _, seen := r.nonces[cmd.Nonce]; seen {
		return zero, ErrCredentialInvalid
	}
	r.nonces[cmd.Nonce] = cmd.ExpiresAt
	return cmd, nil
}

func (r *RuntimeRouter) purgeNoncesLocked(now time.Time) {
	for n, exp := range r.nonces {
		if !exp.After(now.Add(-MaxInternalRouteSkew)) {
			delete(r.nonces, n)
		}
	}
}

// BuildInternalCommand constructs a validated token-free forward command.
func BuildInternalCommand(
	workspaceID string,
	rootType RootScopeType,
	rootID string,
	commandType string,
	opaqueLocator string,
	nonce string,
	now time.Time,
	ttl time.Duration,
) (InternalRouteCommand, error) {
	if ttl <= 0 || ttl > MaxInternalRouteTTL {
		ttl = MaxInternalRouteTTL
	}
	now = now.UTC()
	cmd := InternalRouteCommand{
		SchemaVersion: InternalRouteSchemaVersion,
		WorkspaceID:   strings.TrimSpace(workspaceID),
		RootScopeType: string(rootType),
		RootScopeID:   strings.TrimSpace(rootID),
		CommandType:   commandType,
		OpaqueLocator: opaqueLocator,
		IssuedAt:      now,
		ExpiresAt:     now.Add(ttl),
		Nonce:         strings.TrimSpace(nonce),
	}
	if !cmd.Valid(now) {
		return InternalRouteCommand{}, ErrCredentialInvalid
	}
	return cmd, nil
}

// MarshalInternalCommand encodes without Token fields (none exist on the type).
func MarshalInternalCommand(cmd InternalRouteCommand) ([]byte, error) {
	if !cmd.Valid(time.Now().UTC()) {
		// Allow slightly skewed clock for marshal of already-validated cmds:
		// re-check structure only.
		if cmd.SchemaVersion != InternalRouteSchemaVersion || cmd.Nonce == "" {
			return nil, ErrCredentialInvalid
		}
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		return nil, ErrCredentialInvalid
	}
	if len(raw) > MaxInternalRouteBodyBytes {
		return nil, ErrCredentialInvalid
	}
	return raw, nil
}
