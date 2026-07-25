package outboundidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Broker exchange network limits (technical design §7.2).
	DefaultBrokerTimeout       = 10 * time.Second
	MaxBrokerResponseBytes     = 64 * 1024
	DefaultClientAssertionTTL  = 60 * time.Second
	DefaultBrokerSafetySkew    = 30 * time.Second
	brokerRetryableMaxAttempts = 2 // initial + one safe retry

	clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
)

// MachineCredential is the decrypted machine private key used only for
// private_key_jwt. Version is part of the cache key; material is never cached
// as a string in logs.
type MachineCredential struct {
	PrivateKey ed25519.PrivateKey
	// Version is the active secret version (or monotonic machine secret version).
	Version int64
}

// BrokerExchangeRequest is the full input for a Broker token exchange.
// All fields are non-secret descriptors except Machine and the transient
// assertion signed inside Exchange.
type BrokerExchangeRequest struct {
	BootID                  string
	WorkspaceID             string
	SubjectType             SubjectType
	SubjectID               string
	RootScopeType           RootScopeType
	RootScopeID             string
	ConnectionID            string
	ProviderContractVersion int64
	ConnectionPolicyVersion int64
	// Normalized required scopes for this invocation (subset of Connection).
	Scopes []string
	// Provider + Connection contracts (already validated).
	Provider   ProviderBrokerOBO
	Connection ConnectionBrokerOBO
	// Principal for Assertion.
	ActorType string
	ActorID   string
	// Machine credential for private_key_jwt (not business API credential).
	Machine MachineCredential
	// RootDeadline bounds token residence (zero = unbounded by root).
	RootDeadline time.Time
}

// BrokerToken is the short-lived business access token result. Plaintext must
// be zeroed by the caller after inject; never serialize this type to JSON/logs.
type BrokerToken struct {
	AccessToken []byte
	TokenType   string
	ExpiresAt   time.Time
}

// Zero overwrites the access token bytes.
func (t *BrokerToken) Zero() {
	if t == nil {
		return
	}
	for i := range t.AccessToken {
		t.AccessToken[i] = 0
	}
	t.AccessToken = nil
}

// BrokerClient performs private_key_jwt + token-exchange against the Broker.
// No client_secret_basic / mTLS fallback. No redirect following.
type BrokerClient struct {
	assertions *AssertionIssuer
	httpClient *http.Client
	clock      Clock
	// allowLoopbackHTTP permits http://127.0.0.1 for unit tests only.
	allowLoopbackHTTP bool
}

// BrokerClientOption configures BrokerClient.
type BrokerClientOption func(*BrokerClient)

// WithBrokerHTTPClient injects a custom base client (tests). Redirects remain blocked.
func WithBrokerHTTPClient(client *http.Client) BrokerClientOption {
	return func(c *BrokerClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithBrokerClock injects a clock.
func WithBrokerClock(clock Clock) BrokerClientOption {
	return func(c *BrokerClient) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// WithBrokerAllowLoopbackHTTP enables http:// loopback for tests.
func WithBrokerAllowLoopbackHTTP(allow bool) BrokerClientOption {
	return func(c *BrokerClient) { c.allowLoopbackHTTP = allow }
}

// NewBrokerClient builds a Broker exchange client.
func NewBrokerClient(assertions *AssertionIssuer, opts ...BrokerClientOption) (*BrokerClient, error) {
	if assertions == nil {
		return nil, errors.New("assertion issuer is required for Broker client")
	}
	client := &BrokerClient{
		assertions: assertions,
		clock:      WallClock{},
		httpClient: &http.Client{
			Timeout: DefaultBrokerTimeout,
			// Never follow redirects (SSRF / token leakage).
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                 nil, // ignore proxy env
				DialContext:           (&net.Dialer{Timeout: DefaultBrokerTimeout}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: DefaultBrokerTimeout,
				ExpectContinueTimeout: 1 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	// Ensure redirect policy even if custom client is injected.
	if client.httpClient.CheckRedirect == nil {
		client.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client, nil
}

// Exchange issues a Subject Assertion, authenticates with private_key_jwt, and
// returns a short-lived business Token. Caller must Zero the token after use.
// Does not consult the subject cache — use BrokerTokenCache.GetOrExchange.
func (c *BrokerClient) Exchange(ctx context.Context, req BrokerExchangeRequest) (BrokerToken, error) {
	if c == nil || c.assertions == nil {
		return BrokerToken{}, ErrIdentityConnectionNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateBrokerExchangeRequest(req); err != nil {
		return BrokerToken{}, err
	}
	endpoint, err := c.parseTokenEndpoint(req.Provider.TokenEndpoint)
	if err != nil {
		return BrokerToken{}, err
	}
	// Network guard: HTTPS, no userinfo, host pin, DNS/CIDR/port fail-closed.
	guard, err := NewBrokerNetworkGuard(endpoint, c.allowLoopbackHTTP, nil)
	if err != nil {
		return BrokerToken{}, err
	}
	if err := guard.ValidateURL(ctx, endpoint); err != nil {
		return BrokerToken{}, err
	}
	protected, err := guard.ProtectClient(c.httpClient)
	if err != nil {
		return BrokerToken{}, err
	}

	assertion, _, err := c.assertions.Issue(AssertionIssueRequest{
		Audience:     req.Provider.Audience,
		WorkspaceID:  req.WorkspaceID,
		ConnectionID: req.ConnectionID,
		RootScopeID:  req.RootScopeID,
		ActorType:    req.ActorType,
		ActorID:      req.ActorID,
		SubjectType:  req.SubjectType,
		SubjectID:    req.SubjectID,
		Scopes:       req.Scopes,
	})
	if err != nil {
		return BrokerToken{}, err
	}
	// assertion is ephemeral; clear reference after building form via defer of zeroing string is not possible —
	// we avoid logging it and drop the stack frame quickly.

	clientAssertion, err := c.signClientAssertion(req.Connection.ClientID, endpoint.String(), req.Machine.PrivateKey)
	if err != nil {
		return BrokerToken{}, err
	}

	form := url.Values{}
	form.Set("grant_type", req.Provider.GrantType)
	form.Set("subject_token_type", req.Provider.SubjectTokenType)
	form.Set("subject_token", assertion)
	form.Set("requested_token_type", req.Provider.RequestedTokenType)
	form.Set("audience", req.Provider.Audience)
	if len(req.Scopes) > 0 {
		form.Set("scope", strings.Join(req.Scopes, " "))
	}
	form.Set("client_id", req.Connection.ClientID)
	form.Set("client_assertion_type", clientAssertionType)
	form.Set("client_assertion", clientAssertion)

	token, err := c.postExchange(ctx, protected, endpoint, form, req)
	// Best-effort drop of assertion / client assertion from form (GC will reclaim).
	form.Del("subject_token")
	form.Del("client_assertion")
	return token, err
}

func (c *BrokerClient) postExchange(
	ctx context.Context,
	httpClient *http.Client,
	endpoint *url.URL,
	form url.Values,
	req BrokerExchangeRequest,
) (BrokerToken, error) {
	var lastErr error
	for attempt := 0; attempt < brokerRetryableMaxAttempts; attempt++ {
		if attempt > 0 {
			// Only retry after network timeout / 429 / 5xx; 401/403 never.
			select {
			case <-ctx.Done():
				return BrokerToken{}, ErrBrokerUnavailable.Wrap(ctx.Err())
			case <-time.After(time.Duration(50+attempt*50) * time.Millisecond):
			}
		}
		token, retryable, err := c.doOnce(ctx, httpClient, endpoint, form, req)
		if err == nil {
			return token, nil
		}
		lastErr = err
		if !retryable {
			return BrokerToken{}, err
		}
	}
	if lastErr == nil {
		lastErr = ErrBrokerUnavailable
	}
	return BrokerToken{}, lastErr
}

func (c *BrokerClient) doOnce(
	ctx context.Context,
	httpClient *http.Client,
	endpoint *url.URL,
	form url.Values,
	req BrokerExchangeRequest,
) (token BrokerToken, retryable bool, err error) {
	body := strings.NewReader(form.Encode())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return BrokerToken{}, true, ErrBrokerUnavailable.Wrap(err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		// Network / timeout → retryable once.
		return BrokerToken{}, true, ErrBrokerUnavailable.Wrap(err)
	}
	defer resp.Body.Close()

	// Redirects must fail closed (CheckRedirect returns last response).
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxBrokerResponseBytes))
		return BrokerToken{}, false, ErrTargetRejected
	}

	limited := io.LimitReader(resp.Body, MaxBrokerResponseBytes+1)
	raw, readErr := io.ReadAll(limited)
	if readErr != nil {
		return BrokerToken{}, true, ErrBrokerUnavailable.Wrap(readErr)
	}
	if len(raw) > MaxBrokerResponseBytes {
		zeroBytes(raw)
		return BrokerToken{}, false, ErrBrokerUnavailable
	}
	defer zeroBytes(raw)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return BrokerToken{}, false, ErrBrokerDenied
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return BrokerToken{}, true, ErrBrokerUnavailable
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return BrokerToken{}, false, ErrBrokerUnavailable
	}

	parsed, err := parseBrokerTokenResponse(raw, req.Provider.Response, req, c.clock.Now())
	if err != nil {
		return BrokerToken{}, false, err
	}
	return parsed, false, nil
}

func parseBrokerTokenResponse(
	raw []byte,
	paths BrokerTokenResponse,
	req BrokerExchangeRequest,
	now time.Time,
) (BrokerToken, error) {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return BrokerToken{}, ErrBrokerUnavailable
	}
	access := stringAtPath(payload, paths.AccessTokenPath)
	if access == "" || !utf8.ValidString(access) || containsControl(access) {
		return BrokerToken{}, ErrBrokerUnavailable
	}
	tokenType := ""
	if paths.TokenTypePath != "" {
		tokenType = stringAtPath(payload, paths.TokenTypePath)
	}
	if tokenType == "" {
		tokenType = paths.ExpectedTokenType
	}
	if paths.ExpectedTokenType != "" &&
		!strings.EqualFold(strings.TrimSpace(tokenType), paths.ExpectedTokenType) {
		return BrokerToken{}, ErrBrokerUnavailable
	}

	var expiresIn int64
	if paths.ExpiresInPath != "" {
		expiresIn = int64AtPath(payload, paths.ExpiresInPath)
	}
	if expiresIn <= 0 {
		// Missing expiry is invalid under T3-style strictness for Broker tokens —
		// use Connection max as upper bound only when expires_in is present and positive.
		return BrokerToken{}, ErrBrokerUnavailable
	}
	maxTTL := time.Duration(req.Connection.MaxTokenTTLSeconds) * time.Second
	if maxTTL <= 0 {
		maxTTL = time.Duration(DefaultMaxTokenTTLSeconds) * time.Second
	}
	ttl := time.Duration(expiresIn) * time.Second
	if ttl > maxTTL {
		ttl = maxTTL
	}
	expiresAt := now.UTC().Add(ttl)
	if !req.RootDeadline.IsZero() && expiresAt.After(req.RootDeadline) {
		expiresAt = req.RootDeadline.UTC()
	}
	if !expiresAt.After(now) {
		return BrokerToken{}, ErrCredentialExpired
	}
	return BrokerToken{
		AccessToken: append([]byte(nil), access...),
		TokenType:   strings.TrimSpace(tokenType),
		ExpiresAt:   expiresAt,
	}, nil
}

func (c *BrokerClient) signClientAssertion(
	clientID, tokenEndpoint string,
	privateKey ed25519.PrivateKey,
) (string, error) {
	if strings.TrimSpace(clientID) == "" || len(privateKey) != ed25519.PrivateKeySize {
		return "", ErrIdentityConnectionNotReady
	}
	now := c.clock.Now().UTC()
	jti, err := randomJTI()
	if err != nil {
		return "", ErrBrokerUnavailable.Wrap(err)
	}
	claims := jwt.RegisteredClaims{
		Issuer:    clientID,
		Subject:   clientID,
		Audience:  jwt.ClaimStrings{tokenEndpoint},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(DefaultClientAssertionTTL)),
		ID:        jti,
	}
	// Fixed EdDSA only — algorithm not chosen from any external header.
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["alg"] = OutboundSigningAlgorithm
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", ErrIdentityConnectionNotReady.Wrap(err)
	}
	return signed, nil
}

func (c *BrokerClient) parseTokenEndpoint(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return nil, ErrIdentityPolicyInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrIdentityPolicyInvalid
	}
	return parsed, nil
}

func validateBrokerExchangeRequest(req BrokerExchangeRequest) error {
	if strings.TrimSpace(req.BootID) == "" ||
		strings.TrimSpace(req.WorkspaceID) == "" ||
		strings.TrimSpace(req.ConnectionID) == "" ||
		strings.TrimSpace(req.RootScopeID) == "" {
		return ErrIdentityPolicyInvalid
	}
	if !req.SubjectType.Valid() || strings.TrimSpace(req.SubjectID) == "" {
		return ErrSubjectRequired
	}
	if strings.EqualFold(req.ActorType, "SYSTEM") && req.SubjectType == "" {
		return ErrSubjectRequired
	}
	if req.ProviderContractVersion <= 0 || req.ConnectionPolicyVersion <= 0 {
		return ErrIdentityPolicyInvalid
	}
	if req.Machine.Version <= 0 || len(req.Machine.PrivateKey) != ed25519.PrivateKeySize {
		return ErrIdentityConnectionNotReady
	}
	if strings.TrimSpace(req.Connection.ClientID) == "" {
		return ErrIdentityPolicyInvalid
	}
	if strings.TrimSpace(req.Provider.TokenEndpoint) == "" ||
		strings.TrimSpace(req.Provider.Audience) == "" {
		return ErrIdentityPolicyInvalid
	}
	if req.Provider.MachineAuthMethod != "" &&
		req.Provider.MachineAuthMethod != MachineAuthPrivateKeyJWT {
		return ErrIdentityModeUnsupported
	}
	if req.Connection.MaxTokenTTLSeconds < 0 {
		return ErrIdentityPolicyInvalid
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func containsControl(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return true
		}
	}
	return false
}

// --- minimal JSON path helpers (allowlisted paths only) ---

func stringAtPath(document any, path string) string {
	value, ok := readJSONPath(document, path)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func int64AtPath(document any, path string) int64 {
	value, ok := readJSONPath(document, path)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func readJSONPath(document any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || document == nil {
		return nil, false
	}
	current := document
	for _, part := range strings.Split(path, ".") {
		if !jsonPathPartPattern.MatchString(part) {
			return nil, false
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := object[part]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}

// FormatBrokerCacheKeyString builds a stable non-secret map key for tests.
// Production cache uses structured BrokerCacheKey equality, not this string, in logs.
func FormatBrokerCacheKeyString(k BrokerCacheKey) string {
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%s|%s|%s|%d|%d|%d",
		k.BootID, k.WorkspaceID, k.SubjectType, k.SubjectID,
		k.RootScopeType, k.RootScopeID, k.ConnectionID, k.NormalizedScopes,
		k.ProviderContractVersion, k.ConnectionPolicyVersion, k.MachineSecretVersion,
	)
}
