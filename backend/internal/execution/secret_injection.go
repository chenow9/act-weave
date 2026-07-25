package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/serviceauth"
)

type ActiveSecretSource interface {
	WithActiveSecret(context.Context, string, string, func([]byte) error) error
}

type CredentialReference struct {
	WorkspaceID              string
	SecretID                 string
	SecretFingerprint        string
	AuthMode                 string
	AuthConfig               json.RawMessage
	ProviderDriverConfig     json.RawMessage
	AllowedCredentialHeaders []string
	// BypassOutboundIdentity is set for non-HTTP executors (e.g. WORKFLOW) that
	// must not synthesize AuthMode=NONE as an HTTP identity scheme.
	BypassOutboundIdentity bool
	// OutboundMode is BROKER_OBO or REQUEST_PASSTHROUGH for dual-mode HTTP tools.
	OutboundMode string
	// OutboundRequirements is the versioned requirements descriptor for this
	// connection (outbound-requirements.v1). Never contains Token material.
	OutboundRequirements json.RawMessage
}

type HTTPSecretInjector struct {
	source ActiveSecretSource
	client *http.Client
	mu     sync.Mutex
	tokens map[string]oauthToken
}

type oauthToken struct {
	value        string
	renewalToken string
	tokenType    string
	expiresAt    time.Time
}

func NewHTTPSecretInjector(source ActiveSecretSource) (*HTTPSecretInjector, error) {
	if source == nil {
		return nil, errors.New("active secret source is required")
	}
	return &HTTPSecretInjector{source: source, client: &http.Client{Timeout: 15 * time.Second}, tokens: make(map[string]oauthToken)}, nil
}

// WithInjectedConnection limits plaintext lifetime to the callback and passes
// the executor only a cloned ConnectionSnapshot with the required auth header.
func (injector *HTTPSecretInjector) WithInjectedConnection(
	ctx context.Context,
	connection ConnectionSnapshot,
	reference CredentialReference,
	invoke func(ConnectionSnapshot) error,
) error {
	if injector == nil || invoke == nil || connection.WorkspaceID == "" ||
		reference.WorkspaceID != connection.WorkspaceID {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	// Non-HTTP executors skip legacy HTTP secret injection entirely.
	if reference.BypassOutboundIdentity {
		if strings.TrimSpace(reference.SecretID) != "" {
			return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		return invoke(cloneConnectionSnapshot(connection))
	}
	authMode := strings.ToUpper(strings.TrimSpace(reference.AuthMode))
	// Dual-mode outbound identity must use OutboundIdentityInjector. The legacy
	// HTTPSecretInjector never acquires Broker/Vault credentials and fails closed
	// so production wiring cannot silently skip user-scoped auth.
	if authMode == "BROKER_OBO" || authMode == "REQUEST_PASSTHROUGH" ||
		strings.EqualFold(reference.OutboundMode, "BROKER_OBO") ||
		strings.EqualFold(reference.OutboundMode, "REQUEST_PASSTHROUGH") {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	// Legacy AuthMode=NONE is no longer a valid user-scoped HTTP identity path.
	// Only empty synthetic references for pre-existing non-HTTP tests may still
	// pass when no Secret is configured; production HTTP tools must use dual-mode.
	if authMode == "NONE" {
		if strings.TrimSpace(reference.SecretID) != "" {
			return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		return invoke(cloneConnectionSnapshot(connection))
	}
	if strings.TrimSpace(reference.SecretID) == "" {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	if authMode == serviceauth.SchemeOAuth2Client {
		return injector.withOAuthClientCredentials(ctx, connection, reference, invoke)
	}
	config, err := parseCredentialHeaderConfig(authMode, reference)
	if err != nil {
		return err
	}
	callbackInvoked := false
	err = injector.source.WithActiveSecret(ctx, connection.WorkspaceID, strings.TrimSpace(reference.SecretID), func(plaintext []byte) error {
		callbackInvoked = true
		if len(plaintext) == 0 || containsHeaderControl(plaintext) {
			return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		value := string(plaintext)
		if config.Prefix != "" {
			value = config.Prefix + " " + value
		}
		injected := cloneConnectionSnapshot(connection)
		injected.Headers[config.HeaderName] = value
		injected.SensitiveHeaderNames = appendUniqueFold(injected.SensitiveHeaderNames, config.HeaderName)
		invokeError := invoke(injected)
		delete(injected.Headers, config.HeaderName)
		value = ""
		return invokeError
	})
	if err != nil && !callbackInvoked {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, err)
	}
	return err
}

func (injector *HTTPSecretInjector) withOAuthClientCredentials(
	ctx context.Context, connection ConnectionSnapshot, reference CredentialReference,
	invoke func(ConnectionSnapshot) error,
) error {
	resolved, err := serviceauth.Resolve(
		reference.ProviderDriverConfig, reference.AuthConfig, reference.AuthMode,
		strings.TrimSpace(reference.SecretID) != "",
	)
	if err != nil || resolved.OAuth2 == nil {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	config := *resolved.OAuth2
	tokenURL, err := url.Parse(strings.TrimSpace(config.TokenURL))
	if err != nil || tokenURL == nil || tokenURL.User != nil || tokenURL.Host == "" || (tokenURL.Scheme != "https" && tokenURL.Scheme != "http") {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	cacheKey := oauthCacheKey(connection, reference, config)
	injector.mu.Lock()
	cached, ok := injector.tokens[cacheKey]
	injector.mu.Unlock()
	if !ok || oauthTokenNeedsRenewal(cached) {
		var fetchErr error
		fetchErr = injector.source.WithActiveSecret(ctx, connection.WorkspaceID, strings.TrimSpace(reference.SecretID), func(plaintext []byte) error {
			if len(plaintext) == 0 || containsHeaderControl(plaintext) {
				return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
			}
			if ok && config.RefreshStrategy == serviceauth.RefreshToken && strings.TrimSpace(cached.renewalToken) != "" {
				cached, fetchErr = injector.exchangeOAuthToken(ctx, tokenURL, config, string(plaintext), "refresh_token", cached.renewalToken, connection.EgressPolicy)
				if fetchErr != nil {
					// A stale/revoked renewal token must not strand a valid Client
					// Credentials connection. Fall back to the base grant once.
					cached, fetchErr = injector.exchangeOAuthToken(ctx, tokenURL, config, string(plaintext), "client_credentials", "", connection.EgressPolicy)
				}
			} else {
				cached, fetchErr = injector.exchangeOAuthToken(ctx, tokenURL, config, string(plaintext), "client_credentials", "", connection.EgressPolicy)
			}
			return fetchErr
		})
		if fetchErr != nil {
			return fetchErr
		}
		injector.storeOAuthToken(cacheKey, cached)
	}
	headerName := config.Injection.HeaderName
	// The OAuth injection header belongs to the validated Provider contract.
	// Connection policy may add headers for legacy modes, but should not need to
	// repeat Provider-owned metadata before that contract can execute.
	providerAllowedHeaders := append(append([]string(nil), reference.AllowedCredentialHeaders...), headerName)
	if !safeCredentialHeader(headerName, providerAllowedHeaders) {
		return NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	prefix := strings.TrimSpace(config.Injection.Prefix)
	value := cached.value
	if prefix != "" {
		value = prefix + " " + value
	} else if strings.TrimSpace(cached.tokenType) != "" {
		value = strings.TrimSpace(cached.tokenType) + " " + value
	}
	injected := cloneConnectionSnapshot(connection)
	injected.Headers[headerName] = value
	injected.SensitiveHeaderNames = appendUniqueFold(injected.SensitiveHeaderNames, headerName)
	err = invoke(injected)
	delete(injected.Headers, headerName)
	value = ""
	return err
}

func (injector *HTTPSecretInjector) exchangeOAuthToken(ctx context.Context, target *url.URL, config serviceauth.ResolvedOAuth2, clientSecret string, grantType string, renewalToken string, policy EgressPolicy) (oauthToken, error) {
	port := 443
	if target.Scheme == "http" {
		port = 80
	}
	if target.Port() != "" {
		var parsed int
		if _, err := fmt.Sscanf(target.Port(), "%d", &parsed); err != nil || parsed < 1 || parsed > 65535 {
			return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		} else {
			port = parsed
		}
	}
	policy.AllowedHosts = append(policy.AllowedHosts, target.Hostname())
	policy.AllowedPorts = append(policy.AllowedPorts, port)
	guard, err := NewHTTPNetworkGuard(policy, nil)
	if err != nil || guard.ValidateURL(ctx, target) != nil {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	form := url.Values{"grant_type": {grantType}}
	if grantType == "client_credentials" && strings.TrimSpace(config.Scope) != "" {
		form.Set("scope", strings.TrimSpace(config.Scope))
	}
	for name, value := range config.TokenParameters {
		form.Set(name, value)
	}
	if grantType == "refresh_token" {
		form.Set("refresh_token", renewalToken)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if config.ClientAuthMethod == serviceauth.ClientSecretPost {
		form.Set("client_id", strings.TrimSpace(config.ClientID))
		form.Set("client_secret", clientSecret)
		request, _ = http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		request.SetBasicAuth(url.QueryEscape(strings.TrimSpace(config.ClientID)), url.QueryEscape(clientSecret))
	}
	baseClient := *injector.client
	baseClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client, err := guard.ProtectClient(&baseClient, []string{"Authorization"})
	if err != nil {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	response, err := client.Do(request)
	if err != nil {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	accessToken := serviceauth.StringAt(payload, config.Response.AccessTokenPath)
	if accessToken == "" || containsHeaderControl([]byte(accessToken)) {
		return oauthToken{}, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	ttl := time.Duration(serviceauth.Int64At(payload, config.Response.ExpiresInPath)) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	resolvedRenewalToken := serviceauth.StringAt(payload, config.Response.RenewalTokenPath)
	if resolvedRenewalToken == "" && grantType == "refresh_token" {
		resolvedRenewalToken = renewalToken
	}
	return oauthToken{
		value: accessToken, renewalToken: resolvedRenewalToken,
		tokenType: serviceauth.StringAt(payload, config.Response.TokenTypePath), expiresAt: time.Now().Add(ttl),
	}, nil
}

func oauthCacheKey(connection ConnectionSnapshot, reference CredentialReference, config serviceauth.ResolvedOAuth2) string {
	digest := sha256.New()
	_, _ = digest.Write(reference.ProviderDriverConfig)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(reference.AuthConfig)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(reference.SecretFingerprint))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(config.ClientAuthMethod))
	return connection.WorkspaceID + "\x00" + connection.ID + "\x00" + reference.SecretID + "\x00" + fmt.Sprintf("%x", digest.Sum(nil))
}

func oauthTokenNeedsRenewal(token oauthToken) bool {
	remaining := time.Until(token.expiresAt)
	if remaining <= 0 {
		return true
	}
	window := 30 * time.Second
	if remaining < time.Minute {
		window = 5 * time.Second
	}
	return remaining <= window
}

func (injector *HTTPSecretInjector) storeOAuthToken(key string, token oauthToken) {
	injector.mu.Lock()
	defer injector.mu.Unlock()
	if len(injector.tokens) >= 512 {
		now := time.Now()
		for existingKey, existing := range injector.tokens {
			if !existing.expiresAt.After(now) {
				delete(injector.tokens, existingKey)
			}
		}
		if len(injector.tokens) >= 512 {
			for existingKey := range injector.tokens {
				delete(injector.tokens, existingKey)
				break
			}
		}
	}
	injector.tokens[key] = token
}

type credentialHeaderConfig struct {
	HeaderName string `json:"headerName"`
	Prefix     string `json:"prefix,omitempty"`
	Placement  string `json:"placement,omitempty"`
}

func parseCredentialHeaderConfig(authMode string, reference CredentialReference) (credentialHeaderConfig, error) {
	var config credentialHeaderConfig
	if len(reference.AuthConfig) > 0 && json.Unmarshal(reference.AuthConfig, &config) != nil {
		return config, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	config.HeaderName, config.Prefix = strings.TrimSpace(config.HeaderName), strings.TrimSpace(config.Prefix)
	if placement := strings.ToUpper(strings.TrimSpace(config.Placement)); placement != "" && placement != "HEADER" {
		return config, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	switch authMode {
	case "API_KEY", "API_KEY_HEADER":
		if config.HeaderName == "" {
			config.HeaderName = "X-API-Key"
		}
	case "BEARER", "BEARER_TOKEN", "FIXED_TOKEN":
		if config.HeaderName == "" {
			config.HeaderName = "Authorization"
		}
		if !strings.EqualFold(config.HeaderName, "Authorization") {
			return config, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
		}
		if config.Prefix == "" {
			config.Prefix = "Bearer"
		}
	default:
		return config, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	if !safeCredentialHeader(config.HeaderName, reference.AllowedCredentialHeaders) ||
		containsHeaderControl([]byte(config.Prefix)) || len(config.Prefix) > 64 {
		return config, NewError(ErrorCodeCredential, "CREDENTIAL", false, 0, nil)
	}
	return config, nil
}

func safeCredentialHeader(name string, additional []string) bool {
	if !validHeaderToken(name) {
		return false
	}
	for _, forbidden := range []string{
		"Host", "Cookie", "Set-Cookie", "Content-Length", "Transfer-Encoding",
		"Connection", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP",
	} {
		if strings.EqualFold(name, forbidden) {
			return false
		}
	}
	for _, allowed := range append([]string{"Authorization", "X-API-Key", "X-Auth-Token"}, additional...) {
		if strings.EqualFold(name, strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func validHeaderToken(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		value := name[index]
		if value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value)) {
			continue
		}
		return false
	}
	return true
}

func containsHeaderControl(value []byte) bool {
	for _, character := range value {
		if character == '\r' || character == '\n' || character == 0 {
			return true
		}
	}
	return false
}

func cloneConnectionSnapshot(value ConnectionSnapshot) ConnectionSnapshot {
	cloned := value
	cloned.Headers = make(map[string]string, len(value.Headers)+1)
	for name, headerValue := range value.Headers {
		cloned.Headers[name] = headerValue
	}
	cloned.SensitiveHeaderNames = append([]string(nil), value.SensitiveHeaderNames...)
	cloned.EgressPolicy.AllowedHosts = append([]string(nil), value.EgressPolicy.AllowedHosts...)
	cloned.EgressPolicy.AllowedPorts = append([]int(nil), value.EgressPolicy.AllowedPorts...)
	cloned.EgressPolicy.AllowedCIDRs = append([]string(nil), value.EgressPolicy.AllowedCIDRs...)
	return cloned
}

func appendUniqueFold(values []string, candidate string) []string {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return values
		}
	}
	return append(values, candidate)
}
