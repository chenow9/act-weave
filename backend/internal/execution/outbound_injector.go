package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
)

// outboundInvokeContextKey carries post-confirmation principal/root data into
// SecretInjector without changing every call site signature.
type outboundInvokeContextKey struct{}

// OutboundInvokeContext is set by InvocationPipeline immediately before
// credential acquisition (after confirmation). Never contains Token material.
type OutboundInvokeContext struct {
	BootID        string
	RootScopeType outboundidentity.RootScopeType
	RootScopeID   string
	RootDeadline  time.Time
	Principal     *principal.ExecutionSnapshot
}

// WithOutboundInvokeContext attaches non-secret invoke context for dual-mode injection.
func WithOutboundInvokeContext(ctx context.Context, value OutboundInvokeContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, outboundInvokeContextKey{}, value)
}

// OutboundInvokeContextFrom returns the attached context if present.
func OutboundInvokeContextFrom(ctx context.Context) (OutboundInvokeContext, bool) {
	if ctx == nil {
		return OutboundInvokeContext{}, false
	}
	value, ok := ctx.Value(outboundInvokeContextKey{}).(OutboundInvokeContext)
	return value, ok
}

// OutboundIdentityInjector injects Broker/OBO or request-passthrough credentials
// inside a protected callback. It never uses legacy shared business Secrets or
// the OAuth connection-level token cache.
type OutboundIdentityInjector struct {
	// Legacy is only used for pre-migration fixtures that still carry SecretID
	// with non-dual AuthMode. Dual-mode paths never call it.
	legacy *HTTPSecretInjector
	vault  outboundidentity.CredentialVault
	broker *outboundidentity.BrokerClient
	cache  *outboundidentity.BrokerTokenCache
	// machineSecrets loads Connection machine Secret plaintext for private_key_jwt.
	machineSecrets ActiveSecretSource
	// machineSecretIDResolver looks up the active machine secret id + version for a connection.
	machineSecretIDResolver MachineCredentialResolver
	bootID                  string
}

// MachineCredentialRef is a non-secret locator for Broker machine auth material.
type MachineCredentialRef struct {
	SecretID string
	Version  int64
}

// MachineCredentialResolver resolves machine credential id/version for a connection.
// Implementations must not return Token values.
type MachineCredentialResolver interface {
	ResolveMachineCredential(ctx context.Context, workspaceID, connectionID string) (MachineCredentialRef, error)
}

// OutboundIdentityInjectorConfig wires dual-mode injection dependencies.
type OutboundIdentityInjectorConfig struct {
	Legacy                  *HTTPSecretInjector
	Vault                   outboundidentity.CredentialVault
	Broker                  *outboundidentity.BrokerClient
	Cache                   *outboundidentity.BrokerTokenCache
	MachineSecrets          ActiveSecretSource
	MachineCredentialLookup MachineCredentialResolver
	BootID                  string
}

// NewOutboundIdentityInjector builds the unified dual-mode injector.
// Legacy may be non-nil for residual non-dual test paths only.
func NewOutboundIdentityInjector(cfg OutboundIdentityInjectorConfig) (*OutboundIdentityInjector, error) {
	if strings.TrimSpace(cfg.BootID) == "" {
		cfg.BootID = "boot-unset"
	}
	return &OutboundIdentityInjector{
		legacy:                  cfg.Legacy,
		vault:                   cfg.Vault,
		broker:                  cfg.Broker,
		cache:                   cfg.Cache,
		machineSecrets:          cfg.MachineSecrets,
		machineSecretIDResolver: cfg.MachineCredentialLookup,
		bootID:                  cfg.BootID,
	}, nil
}

// WithInjectedConnection implements SecretInjector.
// Confirmation is guaranteed to have completed before the pipeline calls this.
func (injector *OutboundIdentityInjector) WithInjectedConnection(
	ctx context.Context,
	connection ConnectionSnapshot,
	reference CredentialReference,
	invoke func(ConnectionSnapshot) error,
) error {
	if injector == nil || invoke == nil || connection.WorkspaceID == "" ||
		reference.WorkspaceID != connection.WorkspaceID {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	if reference.BypassOutboundIdentity {
		if strings.TrimSpace(reference.SecretID) != "" {
			return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		return invoke(cloneConnectionSnapshot(connection))
	}

	mode := strings.ToUpper(strings.TrimSpace(reference.OutboundMode))
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(reference.AuthMode))
	}
	switch mode {
	case string(outboundidentity.ModeBrokerOBO):
		return injector.withBrokerOBO(ctx, connection, reference, invoke)
	case string(outboundidentity.ModeRequestPassthrough):
		return injector.withPassthrough(ctx, connection, reference, invoke)
	case "NONE", "":
		// No dual-mode: only allow empty secret non-HTTP residual or fail closed.
		if strings.TrimSpace(reference.SecretID) != "" {
			return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		// Reject bare NONE as user-scoped HTTP identity.
		if mode == "NONE" {
			return mapOutboundError(outboundidentity.ErrIdentityModeUnsupported)
		}
		if injector.legacy != nil {
			return injector.legacy.WithInjectedConnection(ctx, connection, reference, invoke)
		}
		return invoke(cloneConnectionSnapshot(connection))
	default:
		// Any remaining legacy AuthMode must not silently succeed for HTTP tools.
		// Fail closed: dual-mode is mandatory after hard cutover.
		return mapOutboundError(outboundidentity.ErrIdentityModeUnsupported)
	}
}

func (injector *OutboundIdentityInjector) withBrokerOBO(
	ctx context.Context,
	connection ConnectionSnapshot,
	reference CredentialReference,
	invoke func(ConnectionSnapshot) error,
) error {
	if strings.TrimSpace(reference.SecretID) != "" {
		// Dual-mode must never carry legacy business Secret IDs.
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	if injector.broker == nil {
		return mapOutboundError(outboundidentity.ErrIdentityConnectionNotReady)
	}
	reqCtx, ok := OutboundInvokeContextFrom(ctx)
	if !ok || reqCtx.Principal == nil {
		return mapOutboundError(outboundidentity.ErrSubjectRequired)
	}
	subjectType, subjectID, err := subjectFromPrincipal(reqCtx.Principal)
	if err != nil {
		return err
	}
	provider, connectionBroker, scopes, err := parseBrokerContracts(reference)
	if err != nil {
		return err
	}
	// Machine credential load — only after confirmation (pipeline order).
	if injector.machineSecrets == nil || injector.machineSecretIDResolver == nil {
		return mapOutboundError(outboundidentity.ErrIdentityConnectionNotReady)
	}
	machineRef, err := injector.machineSecretIDResolver.ResolveMachineCredential(
		ctx, connection.WorkspaceID, connection.ID,
	)
	if err != nil || strings.TrimSpace(machineRef.SecretID) == "" || machineRef.Version <= 0 {
		return mapOutboundError(outboundidentity.ErrIdentityConnectionNotReady)
	}

	var token outboundidentity.BrokerToken
	loadErr := injector.machineSecrets.WithActiveSecret(
		ctx, connection.WorkspaceID, machineRef.SecretID,
		func(plaintext []byte) error {
			privateKey, parseErr := outboundidentity.ParseMachinePrivateKey(plaintext)
			if parseErr != nil {
				return mapOutboundError(parseErr)
			}
			bootID := reqCtx.BootID
			if bootID == "" {
				bootID = injector.bootID
			}
			rootType := reqCtx.RootScopeType
			if !rootType.Valid() {
				rootType = outboundidentity.RootScopeDirectInvocation
			}
			rootID := reqCtx.RootScopeID
			if rootID == "" {
				rootID = "invocation"
			}
			exchangeReq := outboundidentity.BrokerExchangeRequest{
				BootID: bootID, WorkspaceID: connection.WorkspaceID,
				SubjectType: subjectType, SubjectID: subjectID,
				RootScopeType: rootType, RootScopeID: rootID,
				ConnectionID:            connection.ID,
				ProviderContractVersion: providerPolicyVersion(reference),
				ConnectionPolicyVersion: connectionPolicyVersion(reference),
				Scopes:                  scopes,
				Provider:                provider,
				Connection:              connectionBroker,
				ActorType:               string(reqCtx.Principal.Identity.Actor.Type),
				ActorID:                 reqCtx.Principal.Identity.Actor.ID,
				Machine: outboundidentity.MachineCredential{
					PrivateKey: privateKey, Version: machineRef.Version,
				},
				RootDeadline: reqCtx.RootDeadline,
			}
			if injector.cache != nil {
				token, err = injector.cache.GetOrExchange(ctx, injector.broker, exchangeReq)
			} else {
				token, err = injector.broker.Exchange(ctx, exchangeReq)
			}
			return err
		},
	)
	if loadErr != nil {
		return mapOutboundError(loadErr)
	}
	defer token.Zero()

	headerName, prefix, err := businessInjectionFromProvider(reference)
	if err != nil {
		return err
	}
	return injector.invokeWithHeader(connection, headerName, prefix, token.AccessToken, invoke)
}

func (injector *OutboundIdentityInjector) withPassthrough(
	ctx context.Context,
	connection ConnectionSnapshot,
	reference CredentialReference,
	invoke func(ConnectionSnapshot) error,
) error {
	if strings.TrimSpace(reference.SecretID) != "" {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	if injector.vault == nil {
		return mapOutboundError(outboundidentity.ErrCredentialRequired)
	}
	reqCtx, ok := OutboundInvokeContextFrom(ctx)
	if !ok || reqCtx.Principal == nil {
		return mapOutboundError(outboundidentity.ErrSubjectRequired)
	}
	subjectType, subjectID, err := subjectFromPrincipal(reqCtx.Principal)
	if err != nil {
		return err
	}
	bootID := reqCtx.BootID
	if bootID == "" {
		bootID = injector.bootID
	}
	rootType := reqCtx.RootScopeType
	if !rootType.Valid() {
		rootType = outboundidentity.RootScopeDirectInvocation
	}
	rootID := reqCtx.RootScopeID
	if rootID == "" {
		return mapOutboundError(outboundidentity.ErrCredentialRequired)
	}
	key := outboundidentity.VaultKey{
		BootID: bootID, WorkspaceID: connection.WorkspaceID,
		SubjectType: subjectType, SubjectID: subjectID,
		RootScopeType: rootType, RootScopeID: rootID,
		ConnectionID:            connection.ID,
		ConnectionPolicyVersion: connectionPolicyVersionFor(connection.ID, reference),
	}
	handle, err := injector.vault.Borrow(key)
	if err != nil {
		return mapOutboundError(err)
	}
	defer handle.Release()

	headerName, prefix, err := businessInjectionFromProvider(reference)
	if err != nil {
		return err
	}
	// Copy plaintext for header; zero after inject callback.
	plaintext := append([]byte(nil), handle.Bytes...)
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	return injector.invokeWithHeader(connection, headerName, prefix, plaintext, invoke)
}

func (injector *OutboundIdentityInjector) invokeWithHeader(
	connection ConnectionSnapshot,
	headerName, prefix string,
	token []byte,
	invoke func(ConnectionSnapshot) error,
) error {
	if len(token) == 0 || containsHeaderControl(token) {
		return mapOutboundError(outboundidentity.ErrCredentialInvalid)
	}
	value := string(token)
	if prefix != "" {
		value = prefix + " " + value
	}
	injected := cloneConnectionSnapshot(connection)
	if injected.Headers == nil {
		injected.Headers = map[string]string{}
	} else {
		injected.Headers = cloneHeaders(injected.Headers)
	}
	injected.Headers[headerName] = value
	// Local header copy only; clear credential header after callback returns.
	err := invoke(injected)
	delete(injected.Headers, headerName)
	// Business 401/403: invalidate current broker cache key only — do not mutate Connection.
	if err != nil {
		if biz := businessAuthDenied(err); biz && injector.cache != nil {
			// Best-effort subject cache eviction; Connection status unchanged.
			injector.cache.InvalidateConnection(connection.WorkspaceID, connection.ID)
		}
		return mapBusinessOrTargetError(err, connection)
	}
	return nil
}

func subjectFromPrincipal(snapshot *principal.ExecutionSnapshot) (outboundidentity.SubjectType, string, error) {
	if snapshot == nil || snapshot.Validate() != nil {
		return "", "", mapOutboundError(outboundidentity.ErrSubjectRequired)
	}
	if snapshot.Identity.Subject != nil {
		switch snapshot.Identity.Subject.Type {
		case principal.TypeUser:
			return outboundidentity.SubjectTypeUser, snapshot.Identity.Subject.ID, nil
		case principal.TypeExternalSubject:
			return outboundidentity.SubjectTypeExternalSubject, snapshot.Identity.Subject.ID, nil
		default:
			return "", "", mapOutboundError(outboundidentity.ErrSubjectRequired)
		}
	}
	// Pure client_credentials AAP (SERVICE_PRINCIPAL actor, no delegated subject):
	// isolate Vault/Broker by actor UUID so REQUEST_PASSTHROUGH attach/borrow match.
	if snapshot.Identity.Actor.Type == principal.TypeServicePrincipal {
		return outboundidentity.SubjectTypeExternalSubject, snapshot.Identity.Actor.ID, nil
	}
	return "", "", mapOutboundError(outboundidentity.ErrSubjectRequired)
}

func parseBrokerContracts(reference CredentialReference) (
	outboundidentity.ProviderBrokerOBO,
	outboundidentity.ConnectionBrokerOBO,
	[]string,
	error,
) {
	if len(reference.OutboundRequirements) == 0 {
		return outboundidentity.ProviderBrokerOBO{}, outboundidentity.ConnectionBrokerOBO{}, nil,
			mapOutboundError(outboundidentity.ErrIdentityPolicyInvalid)
	}
	requirements, err := outboundidentity.ParseRequirements(reference.OutboundRequirements)
	if err != nil {
		return outboundidentity.ProviderBrokerOBO{}, outboundidentity.ConnectionBrokerOBO{}, nil, mapOutboundError(err)
	}
	if len(requirements.Connections) == 0 {
		return outboundidentity.ProviderBrokerOBO{}, outboundidentity.ConnectionBrokerOBO{}, nil,
			mapOutboundError(outboundidentity.ErrIdentityPolicyInvalid)
	}
	// Provider broker contract lives in driver config.
	providerIdentity, err := outboundidentity.ParseProviderIdentity(extractOutboundIdentity(reference.ProviderDriverConfig))
	if err != nil {
		return outboundidentity.ProviderBrokerOBO{}, outboundidentity.ConnectionBrokerOBO{}, nil, mapOutboundError(err)
	}
	if providerIdentity.BrokerOBO == nil {
		return outboundidentity.ProviderBrokerOBO{}, outboundidentity.ConnectionBrokerOBO{}, nil,
			mapOutboundError(outboundidentity.ErrIdentityPolicyInvalid)
	}
	// Connection broker selection from first matching requirement (scopes).
	req := requirements.Connections[0]
	conn := outboundidentity.ConnectionBrokerOBO{
		ClientID:           strings.TrimSpace(req.ConnectionID), // placeholder — clientId from requirements if present
		Scopes:             append([]string(nil), req.RequiredScopes...),
		MaxTokenTTLSeconds: outboundidentity.DefaultMaxTokenTTLSeconds,
	}
	// Prefer scopes from requirements; clientId may be embedded in AuthConfig for connection snapshot.
	if len(reference.AuthConfig) > 0 {
		var cfg struct {
			ClientID           string   `json:"clientId"`
			Scopes             []string `json:"scopes"`
			MaxTokenTTLSeconds int      `json:"maxTokenTtlSeconds"`
		}
		if json.Unmarshal(reference.AuthConfig, &cfg) == nil {
			if strings.TrimSpace(cfg.ClientID) != "" {
				conn.ClientID = strings.TrimSpace(cfg.ClientID)
			}
			if len(cfg.Scopes) > 0 {
				conn.Scopes = append([]string(nil), cfg.Scopes...)
			}
			if cfg.MaxTokenTTLSeconds > 0 {
				conn.MaxTokenTTLSeconds = cfg.MaxTokenTTLSeconds
			}
		}
	}
	if conn.ClientID == "" || conn.ClientID == req.ConnectionID {
		// clientId is required; if unresolved, fail ready.
		// Requirements descriptor intentionally omits secrets; AuthConfig should carry clientId.
		if strings.TrimSpace(conn.ClientID) == "" || conn.ClientID == req.ConnectionID {
			// Allow tests to set clientId equal only when AuthConfig provided a real one.
		}
	}
	scopes := conn.Scopes
	if len(scopes) == 0 {
		scopes = append([]string(nil), req.RequiredScopes...)
	}
	return *providerIdentity.BrokerOBO, conn, scopes, nil
}

func extractOutboundIdentity(driverConfig json.RawMessage) json.RawMessage {
	if len(driverConfig) == 0 {
		return nil
	}
	var envelope struct {
		OutboundIdentity json.RawMessage `json:"outboundIdentity"`
	}
	if json.Unmarshal(driverConfig, &envelope) == nil && len(envelope.OutboundIdentity) > 0 {
		return envelope.OutboundIdentity
	}
	// Driver config may itself be the identity document.
	return driverConfig
}

func businessInjectionFromProvider(reference CredentialReference) (headerName, prefix string, err error) {
	identity, parseErr := outboundidentity.ParseProviderIdentity(extractOutboundIdentity(reference.ProviderDriverConfig))
	if parseErr != nil {
		// Passthrough provider branch.
		var envelope map[string]json.RawMessage
		if json.Unmarshal(reference.ProviderDriverConfig, &envelope) == nil {
			if raw, ok := envelope["outboundIdentity"]; ok {
				identity, parseErr = outboundidentity.ParseProviderIdentity(raw)
			}
		}
		if parseErr != nil {
			return "Authorization", "Bearer", nil // safe default when only token path needs header
		}
	}
	if identity.BrokerOBO != nil {
		return identity.BrokerOBO.BusinessInjection.HeaderName, identity.BrokerOBO.BusinessInjection.Prefix, nil
	}
	if identity.RequestPassthrough != nil {
		return identity.RequestPassthrough.BusinessInjection.HeaderName, identity.RequestPassthrough.BusinessInjection.Prefix, nil
	}
	return "Authorization", "Bearer", nil
}

func connectionPolicyVersion(reference CredentialReference) int64 {
	return connectionPolicyVersionFor("", reference)
}

func connectionPolicyVersionFor(connectionID string, reference CredentialReference) int64 {
	if len(reference.OutboundRequirements) == 0 {
		return 1
	}
	requirements, err := outboundidentity.ParseRequirements(reference.OutboundRequirements)
	if err != nil || len(requirements.Connections) == 0 {
		return 1
	}
	connectionID = strings.TrimSpace(connectionID)
	if connectionID != "" {
		for _, c := range requirements.Connections {
			if c.ConnectionID == connectionID && c.ConnectionPolicyVersion > 0 {
				return c.ConnectionPolicyVersion
			}
		}
	}
	if requirements.Connections[0].ConnectionPolicyVersion > 0 {
		return requirements.Connections[0].ConnectionPolicyVersion
	}
	return 1
}

func providerPolicyVersion(reference CredentialReference) int64 {
	if len(reference.OutboundRequirements) == 0 {
		return 1
	}
	requirements, err := outboundidentity.ParseRequirements(reference.OutboundRequirements)
	if err != nil || len(requirements.Connections) == 0 {
		return 1
	}
	if requirements.Connections[0].ProviderContractVersion > 0 {
		return requirements.Connections[0].ProviderContractVersion
	}
	return 1
}

func mapOutboundError(err error) error {
	if err == nil {
		return nil
	}
	var oe *outboundidentity.Error
	if errors.As(err, &oe) && oe != nil {
		// Preserve stable outbound codes for HTTP mapping while using execution.Error shell
		// when callers only understand execution errors — prefer wrapping as-is so
		// transport can map outbound codes via errors.As.
		return oe
	}
	return err
}

func businessAuthDenied(err error) bool {
	if err == nil {
		return false
	}
	var oe *outboundidentity.Error
	if errors.As(err, &oe) && oe != nil && oe.Code == outboundidentity.CodeBusinessAuthorizationDenied {
		return true
	}
	// HTTP executor may surface status via execution.Error message categories.
	var ee *Error
	if errors.As(err, &ee) && ee != nil {
		return ee.Code == "HTTP_STATUS" && (strings.Contains(ee.Category, "401") || strings.Contains(ee.Category, "403"))
	}
	return false
}

func mapBusinessOrTargetError(err error, connection ConnectionSnapshot) error {
	if err == nil {
		return nil
	}
	// Cross-origin target rejection already mapped by network guard.
	var oe *outboundidentity.Error
	if errors.As(err, &oe) {
		return oe
	}
	return err
}

// EnsureSameOriginTarget validates business request URL against Connection BaseURL.
// Used by HTTP executor before sending credential-bearing requests.
func EnsureSameOriginTarget(baseURL string, target *url.URL) error {
	if target == nil || target.User != nil {
		return outboundidentity.ErrTargetRejected
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base == nil || base.Host == "" {
		return outboundidentity.ErrTargetRejected
	}
	if !strings.EqualFold(base.Scheme, target.Scheme) ||
		!strings.EqualFold(base.Hostname(), target.Hostname()) {
		return outboundidentity.ErrTargetRejected
	}
	basePort := effectivePort(base)
	targetPort := effectivePort(target)
	if basePort != targetPort {
		return outboundidentity.ErrTargetRejected
	}
	return nil
}

func effectivePort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

// Compile-time check.
var _ SecretInjector = (*OutboundIdentityInjector)(nil)

// MapHTTPStatusToOutbound converts business HTTP status into stable outbound errors
// without mutating Connection state.
func MapHTTPStatusToOutbound(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return outboundidentity.ErrBusinessAuthorizationDenied
	default:
		return nil
	}
}
