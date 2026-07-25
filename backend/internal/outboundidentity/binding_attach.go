package outboundidentity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// BindingAttachContext is the server-derived identity for a top-level envelope.
// Callers must never accept these fields from the client binding body.
type BindingAttachContext struct {
	BootID        string
	WorkspaceID   string
	SubjectType   SubjectType
	SubjectID     string
	RootScopeType RootScopeType
	RootScopeID   string
	// RootDeadline bounds affinity + vault residence (not Token expiry storage).
	RootDeadline time.Time
	// Instance identity for affinity claim (passthrough only).
	OwnerInstanceID string
	OwnerBootID     string
	// Now is validation time (defaults to wall clock).
	Now time.Time
}

// ConnectionPolicyView is the non-secret Connection snapshot used at attach time.
type ConnectionPolicyView struct {
	ConnectionID            string
	ProviderID              string
	Mode                    Mode
	ConnectionPolicyVersion int64
	ProviderContractVersion int64
	// MaxResidenceSeconds from requestPassthrough; 0 means no extra cap.
	MaxResidenceSeconds int
	// Executable is true only for VERIFIED + migration NONE + ready.
	Executable bool
}

// BindingAttachInput is one top-level request's write-only envelope handling.
type BindingAttachInput struct {
	// RawEnvelope is the outboundCredentials object (or full wrapper with that key).
	// Only the dedicated decoder touches plaintext values.
	RawEnvelope json.RawMessage
	// Requirements is the published/compiled allowlist; attach rejects anything outside.
	Requirements Requirements
	// Connections must include every passthrough Connection referenced by requirements
	// or bindings (lookup by ConnectionID).
	Connections []ConnectionPolicyView
	// Context is server-derived principal/root/boot.
	Context BindingAttachContext
	// ExistingVaultAlive is true when an idempotent replay finds the original
	// root still has live vault entries (same owner). When true, plaintext from
	// the replay request is discarded without re-attach.
	ExistingVaultAlive bool
	// ExistingRunID non-empty means this is an idempotent replay of a known run.
	ExistingRunID string
}

// BindingAttachResult is the safe outcome after attach or idempotent discard.
type BindingAttachResult struct {
	// Attached is true when this request performed a new Vault attach.
	Attached bool
	// IdempotentReplay is true when plaintext was discarded and original run reused.
	IdempotentReplay bool
	// AffinityClaimed is true when this request CAS-claimed affinity.
	AffinityClaimed bool
	// CredentialDescriptorHash is the AAP request-hash fragment (no Token material).
	CredentialDescriptorHash string
	// PassthroughConnectionIDs lists connections that received vault bindings.
	PassthroughConnectionIDs []string
	// RequiresPassthrough is true when requirements need at least one passthrough binding.
	RequiresPassthrough bool
}

// BindingAttacher orchestrates parse → allowlist → affinity → vault attach.
// It is the only path that may touch envelope Value after transport decode.
type BindingAttacher struct {
	vault   CredentialVault
	runtime *RuntimeRepository // optional; required when passthrough affinity is needed
}

// NewBindingAttacher constructs the attach orchestrator.
func NewBindingAttacher(vault CredentialVault, runtime *RuntimeRepository) (*BindingAttacher, error) {
	if vault == nil {
		return nil, errors.New("binding attacher vault is required")
	}
	return &BindingAttacher{vault: vault, runtime: runtime}, nil
}

// Attach validates and attaches passthrough credentials for a top-level root.
// On any failure after partial work, it cleans up only this request's attach
// (affinity + vault root) without affecting concurrent winners.
func (a *BindingAttacher) Attach(ctx context.Context, input BindingAttachInput) (BindingAttachResult, error) {
	var zero BindingAttachResult
	if a == nil || a.vault == nil || ctx == nil {
		return zero, ErrCredentialInvalid
	}
	if input.Context.Now.IsZero() {
		input.Context.Now = time.Now().UTC()
	}
	if err := validateAttachContext(input.Context); err != nil {
		return zero, err
	}
	if input.Context.BootID != a.vault.BootID() {
		return zero, ErrCredentialInvalid
	}
	reqNorm, err := NormalizeRequirements(input.Requirements)
	if err != nil {
		return zero, err
	}
	passthroughReqs := passthroughRequirements(reqNorm)
	zero.RequiresPassthrough = len(passthroughReqs) > 0

	// Idempotent replay: never re-bind a new Token to an existing Run.
	if input.ExistingRunID != "" {
		if input.ExistingVaultAlive {
			// Discard plaintext if any (caller should still zero raw body).
			_ = ZeroCredentialsRaw(input.RawEnvelope)
			hash, hashErr := CredentialDescriptorHash(reqNorm, nil)
			if hashErr != nil {
				return zero, hashErr
			}
			return BindingAttachResult{
				IdempotentReplay:         true,
				CredentialDescriptorHash: hash,
				RequiresPassthrough:      zero.RequiresPassthrough,
			}, nil
		}
		// Original vault dead — discard new plaintext and fail closed.
		_ = ZeroCredentialsRaw(input.RawEnvelope)
		return zero, ErrCredentialExpired
	}

	connByID := indexConnections(input.Connections)
	var envelope CredentialsEnvelope
	hasEnvelope := len(input.RawEnvelope) > 0 && string(input.RawEnvelope) != "null"
	if hasEnvelope {
		envelope, err = ParseCredentialsEnvelopeFlexible(input.RawEnvelope)
		if err != nil {
			return zero, err
		}
		// Always zero source envelope values after parse (we hold copies).
		defer zeroCredentialsEnvelope(&envelope)
	}

	if len(passthroughReqs) == 0 {
		// Pure Broker / no outbound: envelope must be absent.
		if hasEnvelope {
			return zero, ErrCredentialInvalid
		}
		hash, hashErr := CredentialDescriptorHash(reqNorm, nil)
		if hashErr != nil {
			return zero, hashErr
		}
		return BindingAttachResult{CredentialDescriptorHash: hash}, nil
	}

	// Passthrough required: envelope must cover every required passthrough connection.
	if !hasEnvelope {
		return zero, ErrCredentialRequired
	}
	if err := ValidateCredentialsEnvelopeExpiry(envelope, input.Context.Now); err != nil {
		return zero, err
	}
	if err := validateBindingsAgainstRequirements(envelope, passthroughReqs, connByID); err != nil {
		return zero, err
	}

	// Claim affinity before vault attach (CAS). Only passthrough roots.
	affinityClaimed := false
	if a.runtime != nil {
		if input.Context.OwnerInstanceID == "" || input.Context.OwnerBootID == "" {
			return zero, ErrCredentialInvalid
		}
		deadline := input.Context.RootDeadline
		if deadline.IsZero() {
			// Bound by min binding expiresAt if root deadline not set.
			deadline = earliestBindingExpiry(envelope)
		}
		_, claimErr := a.runtime.ClaimAffinity(ctx, AffinityClaimRequest{
			WorkspaceID: input.Context.WorkspaceID, RootScopeType: input.Context.RootScopeType,
			RootScopeID:     input.Context.RootScopeID,
			OwnerInstanceID: input.Context.OwnerInstanceID, OwnerBootID: input.Context.OwnerBootID,
			RootDeadlineAt: deadline, RequiresPassthrough: true,
		})
		if claimErr != nil {
			if errors.Is(claimErr, ErrAffinityConflict) {
				// Concurrent winner — do not attach over their vault.
				return zero, ErrAffinityConflict
			}
			return zero, claimErr
		}
		affinityClaimed = true
	}

	bindings := make([]AttachBinding, 0, len(envelope.Bindings))
	for _, b := range envelope.Bindings {
		view := connByID[b.ConnectionID]
		key := VaultKey{
			BootID: input.Context.BootID, WorkspaceID: input.Context.WorkspaceID,
			SubjectType: input.Context.SubjectType, SubjectID: input.Context.SubjectID,
			RootScopeType: input.Context.RootScopeType, RootScopeID: input.Context.RootScopeID,
			ConnectionID: b.ConnectionID, ConnectionPolicyVersion: view.ConnectionPolicyVersion,
		}
		bindings = append(bindings, AttachBinding{
			Key: key, CredentialType: b.CredentialType,
			Value:     append([]byte(nil), b.Value...),
			ExpiresAt: b.ExpiresAt, RootDeadline: input.Context.RootDeadline,
			MaxResidenceSeconds: view.MaxResidenceSeconds,
		})
	}

	if err := a.vault.Attach(bindings); err != nil {
		// Cleanup affinity claimed by this request only.
		if affinityClaimed && a.runtime != nil {
			_ = a.runtime.DeleteAffinity(ctx, input.Context.WorkspaceID, input.Context.RootScopeType, input.Context.RootScopeID)
		}
		// Zero prepared binding copies.
		for i := range bindings {
			zeroBytes(bindings[i].Value)
		}
		return zero, err
	}
	// Zero prepared copies after vault took ownership of its own copies.
	for i := range bindings {
		zeroBytes(bindings[i].Value)
	}

	ids := make([]string, 0, len(envelope.Bindings))
	for _, b := range envelope.Bindings {
		ids = append(ids, b.ConnectionID)
	}
	sort.Strings(ids)
	hash, hashErr := CredentialDescriptorHash(reqNorm, envelope.Bindings)
	if hashErr != nil {
		// Attach succeeded but hash failed — fail closed and cleanup this root.
		a.CleanupRequest(ctx, input.Context, affinityClaimed)
		return zero, hashErr
	}
	return BindingAttachResult{
		Attached: true, AffinityClaimed: affinityClaimed,
		CredentialDescriptorHash: hash,
		PassthroughConnectionIDs: ids,
		RequiresPassthrough:      true,
	}, nil
}

// VaultHasLiveRoot reports whether the process vault still holds live entries
// for the given root (AAP idempotent replay).
func (a *BindingAttacher) VaultHasLiveRoot(root RootScope) bool {
	if a == nil || a.vault == nil {
		return false
	}
	return a.vault.HasLiveRoot(root)
}

// CleanupRequest removes vault entries and optional affinity for this root only.
// Idempotent. Used when DB create fails after attach.
func (a *BindingAttacher) CleanupRequest(ctx context.Context, attachCtx BindingAttachContext, affinityClaimed bool) {
	if a == nil {
		return
	}
	if a.vault != nil {
		a.vault.CleanupRoot(RootScope{
			BootID: attachCtx.BootID, WorkspaceID: attachCtx.WorkspaceID,
			SubjectType: attachCtx.SubjectType, SubjectID: attachCtx.SubjectID,
			RootScopeType: attachCtx.RootScopeType, RootScopeID: attachCtx.RootScopeID,
		})
	}
	if affinityClaimed && a.runtime != nil && ctx != nil {
		_ = a.runtime.DeleteAffinity(ctx, attachCtx.WorkspaceID, attachCtx.RootScopeType, attachCtx.RootScopeID)
	}
}

// CredentialDescriptorHash builds the idempotency fragment for credentials:
// schema version, connection, credential type, provided=true, policy descriptor.
// Explicitly excludes value, hash/fingerprint/claims, expiresAt, locator.
func CredentialDescriptorHash(req Requirements, bindings []CredentialBinding) (string, error) {
	type bindingDesc struct {
		ConnectionID   string `json:"connectionId"`
		CredentialType string `json:"credentialType"`
		Provided       bool   `json:"provided"`
		PolicyVersion  int64  `json:"connectionPolicyVersion,omitempty"`
		Mode           string `json:"mode,omitempty"`
	}
	type payload struct {
		SchemaVersion string        `json:"schemaVersion"`
		Bindings      []bindingDesc `json:"bindings"`
		Requirements  []bindingDesc `json:"requirements"`
	}
	reqNorm, err := NormalizeRequirements(req)
	if err != nil {
		// Empty requirements OK for pure non-outbound.
		reqNorm = Requirements{SchemaVersion: SchemaRequirements}
	}
	p := payload{SchemaVersion: SchemaCredentials}
	for _, c := range reqNorm.Connections {
		p.Requirements = append(p.Requirements, bindingDesc{
			ConnectionID: c.ConnectionID, Mode: string(c.Mode),
			PolicyVersion: c.ConnectionPolicyVersion,
			Provided:      false, // requirements descriptor only
		})
	}
	for _, b := range bindings {
		p.Bindings = append(p.Bindings, bindingDesc{
			ConnectionID: b.ConnectionID, CredentialType: string(b.CredentialType),
			Provided: true,
		})
	}
	sort.Slice(p.Bindings, func(i, j int) bool {
		return p.Bindings[i].ConnectionID < p.Bindings[j].ConnectionID
	})
	sort.Slice(p.Requirements, func(i, j int) bool {
		return p.Requirements[i].ConnectionID < p.Requirements[j].ConnectionID
	})
	raw, err := json.Marshal(p)
	if err != nil {
		return "", ErrCredentialInvalid
	}
	// Safety: hash payload must never contain canary-like secret material keys.
	if strings.Contains(string(raw), `"value"`) || strings.Contains(string(raw), `"expiresAt"`) {
		return "", ErrCredentialInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ParseCredentialsEnvelopeFlexible accepts either the bare envelope object or
// a wrapper `{"outboundCredentials":{...}}`.
func ParseCredentialsEnvelopeFlexible(raw json.RawMessage) (CredentialsEnvelope, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	// Try bare envelope first.
	env, err := ParseCredentialsEnvelope(raw)
	if err == nil {
		return env, nil
	}
	// Wrapper form.
	var wrap struct {
		OutboundCredentials json.RawMessage `json:"outboundCredentials"`
	}
	if wrapErr := decodeStrictJSON(raw, &wrap); wrapErr != nil {
		return CredentialsEnvelope{}, err
	}
	if len(wrap.OutboundCredentials) == 0 {
		return CredentialsEnvelope{}, ErrCredentialInvalid
	}
	return ParseCredentialsEnvelope(wrap.OutboundCredentials)
}

// ExtractOutboundCredentialsRaw pulls the outboundCredentials sub-object from a
// top-level JSON body without decoding other fields into maps that retain Value.
// Returns nil when the field is absent.
func ExtractOutboundCredentialsRaw(body []byte) (json.RawMessage, error) {
	if len(body) == 0 {
		return nil, nil
	}
	// Use decoder to find the raw field without full map materialization of values
	// into interface{} — we re-encode only the credentials sub-tree via json.RawMessage.
	var probe struct {
		OutboundCredentials json.RawMessage `json:"outboundCredentials"`
	}
	// Allow unknown fields at the outer request body (business fields live there).
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, ErrCredentialInvalid
	}
	if len(probe.OutboundCredentials) == 0 {
		return nil, nil
	}
	return probe.OutboundCredentials, nil
}

// ZeroCredentialsRaw best-effort zeros Value strings inside a credentials JSON
// blob after processing (does not guarantee GC erasure).
func ZeroCredentialsRaw(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	// Overwrite the entire slice the caller provided.
	for i := range raw {
		raw[i] = 0
	}
	return nil
}

// StripOutboundCredentialsFromBody returns a copy of body with outboundCredentials
// removed so business handlers never see Token material.
func StripOutboundCredentialsFromBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, ErrCredentialInvalid
	}
	delete(obj, "outboundCredentials")
	return json.Marshal(obj)
}

func validateAttachContext(c BindingAttachContext) error {
	if strings.TrimSpace(c.BootID) == "" || strings.TrimSpace(c.WorkspaceID) == "" ||
		!c.SubjectType.Valid() || strings.TrimSpace(c.SubjectID) == "" ||
		!c.RootScopeType.Valid() || strings.TrimSpace(c.RootScopeID) == "" {
		return ErrCredentialInvalid
	}
	if c.RootScopeType == RootScopeDebugAttachment {
		// Debug attach uses a separate short-lived path (checklist #11).
		return ErrCredentialInvalid
	}
	if c.Now.IsZero() {
		c.Now = time.Now().UTC()
	}
	return nil
}

func passthroughRequirements(req Requirements) []RequirementConnection {
	out := make([]RequirementConnection, 0)
	for _, c := range req.Connections {
		if c.Mode == ModeRequestPassthrough {
			out = append(out, c)
		}
	}
	return out
}

func indexConnections(views []ConnectionPolicyView) map[string]ConnectionPolicyView {
	out := make(map[string]ConnectionPolicyView, len(views))
	for _, v := range views {
		out[strings.TrimSpace(v.ConnectionID)] = v
	}
	return out
}

func validateBindingsAgainstRequirements(
	envelope CredentialsEnvelope,
	passthroughReqs []RequirementConnection,
	connByID map[string]ConnectionPolicyView,
) error {
	// Reuse package-level allowlist check (wrong Connection / Broker binding).
	// Build a Requirements view limited to passthrough entries for the helper.
	reqView := Requirements{SchemaVersion: SchemaRequirements, Connections: passthroughReqs}
	if err := ValidateCredentialsAgainstRequirements(envelope, reqView); err != nil {
		return err
	}
	reqByID := make(map[string]RequirementConnection, len(passthroughReqs))
	for _, r := range passthroughReqs {
		reqByID[r.ConnectionID] = r
	}
	for _, b := range envelope.Bindings {
		req := reqByID[b.ConnectionID]
		view, ok := connByID[b.ConnectionID]
		if !ok {
			return ErrIdentityPolicyInvalid
		}
		if view.Mode != ModeRequestPassthrough {
			return ErrCredentialTargetMismatch
		}
		if !view.Executable {
			return ErrIdentityPolicyInvalid
		}
		if view.ConnectionPolicyVersion != req.ConnectionPolicyVersion {
			return ErrIdentityPolicyChanged
		}
		if view.ProviderContractVersion != req.ProviderContractVersion {
			return ErrIdentityPolicyChanged
		}
	}
	return nil
}

func earliestBindingExpiry(envelope CredentialsEnvelope) time.Time {
	var earliest time.Time
	for _, b := range envelope.Bindings {
		if earliest.IsZero() || b.ExpiresAt.Before(earliest) {
			earliest = b.ExpiresAt
		}
	}
	return earliest
}
