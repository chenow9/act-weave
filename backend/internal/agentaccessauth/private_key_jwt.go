package agentaccessauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	DefaultClientAssertionMaxTTL   = 5 * time.Minute
	maximumClientAssertionBytes    = 16 * 1024
	minimumClientAssertionJTIBytes = 16
	maximumClientAssertionJTIBytes = 256

	FailureMalformedClientAssertion = "MALFORMED_CLIENT_ASSERTION"
	FailureClientAssertionRejected  = "CLIENT_ASSERTION_REJECTED"
	FailureClientAssertionReplay    = "CLIENT_ASSERTION_REPLAY"
	FailureClientJWKSRejected       = "CLIENT_JWKS_REJECTED"
)

var ErrPrivateKeyJWTClientNotFound = errors.New("private_key_jwt Client not found")

type PrivateKeyJWTCredential struct {
	CredentialID  string
	JWKThumbprint []byte
	ValidFrom     time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
}

type PrivateKeyJWTClient struct {
	WorkspaceID              string
	ClientID                 string
	PublicClientID           string
	ServicePrincipalID       string
	ServicePrincipalVersion  int64
	ClientActive             bool
	ServicePrincipalActive   bool
	PrivateKeyAuthentication bool
	JWKSURI                  string
	TokenTTLSeconds          int
	Credentials              []PrivateKeyJWTCredential
}

type PrivateKeyJWTClientStore interface {
	LookupPrivateKeyJWTClient(context.Context, string) (PrivateKeyJWTClient, error)
	MarkPrivateKeyJWTAuthenticated(context.Context, string, string, time.Time) error
}

type ClientAssertionJTIStore interface {
	ClaimClientAssertionJTI(context.Context, string, [sha256.Size]byte, time.Time, time.Time) (bool, error)
}

type PrivateKeyJWTAuthenticationFailure struct {
	WorkspaceID string
	ClientID    string
	ErrorCode   string
	SourceIP    string
	UserAgent   string
}

type PrivateKeyJWTAuthenticationAudit interface {
	RecordPrivateKeyJWTAuthenticationFailure(context.Context, PrivateKeyJWTAuthenticationFailure) error
}

type PrivateKeyJWTAuthenticationRequest struct {
	ClientAssertion string
	SourceIP        string
	UserAgent       string
}

type PrivateKeyJWTAuthenticator struct {
	clients       PrivateKeyJWTClientStore
	keys          *RemoteJWKSCache
	replays       ClientAssertionJTIStore
	tokenEndpoint string
	maxTTL        time.Duration
	now           func() time.Time
	limiter       ClientAuthenticationAttemptLimiter
	audit         PrivateKeyJWTAuthenticationAudit
}

type PrivateKeyJWTAuthenticatorOption func(*PrivateKeyJWTAuthenticator) error

func NewPrivateKeyJWTAuthenticator(
	clients PrivateKeyJWTClientStore,
	keys *RemoteJWKSCache,
	replays ClientAssertionJTIStore,
	tokenEndpoint string,
	options ...PrivateKeyJWTAuthenticatorOption,
) (*PrivateKeyJWTAuthenticator, error) {
	if clients == nil || keys == nil || replays == nil || !validTokenEndpointAudience(tokenEndpoint) {
		return nil, errors.New("private_key_jwt Client store, JWKS, JTI store, and Token Endpoint are required")
	}
	authenticator := &PrivateKeyJWTAuthenticator{
		clients: clients, keys: keys, replays: replays, tokenEndpoint: tokenEndpoint,
		maxTTL: DefaultClientAssertionMaxTTL, now: func() time.Time { return time.Now().UTC() },
		limiter: allowClientAuthenticationLimiter{},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("private_key_jwt authenticator option is required")
		}
		if err := option(authenticator); err != nil {
			return nil, err
		}
	}
	return authenticator, nil
}

func WithPrivateKeyJWTAuthenticationLimiter(
	limiter ClientAuthenticationAttemptLimiter,
) PrivateKeyJWTAuthenticatorOption {
	return func(authenticator *PrivateKeyJWTAuthenticator) error {
		if limiter == nil {
			return errors.New("private_key_jwt authentication limiter is required")
		}
		authenticator.limiter = limiter
		return nil
	}
}

func WithPrivateKeyJWTAuthenticationAudit(
	audit PrivateKeyJWTAuthenticationAudit,
) PrivateKeyJWTAuthenticatorOption {
	return func(authenticator *PrivateKeyJWTAuthenticator) error {
		if audit == nil {
			return errors.New("private_key_jwt authentication audit is required")
		}
		authenticator.audit = audit
		return nil
	}
}

func (authenticator *PrivateKeyJWTAuthenticator) Authenticate(
	ctx context.Context,
	request PrivateKeyJWTAuthenticationRequest,
) (AuthenticatedClient, error) {
	if authenticator == nil || ctx == nil {
		return AuthenticatedClient{}, ErrInvalidClient
	}
	unverifiedClaims, keyID, algorithm, err := parseUnverifiedClientAssertion(request.ClientAssertion)
	if err != nil {
		authenticator.recordMalformedAttempt(ctx, request)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	publicClientID := unverifiedClaims.Issuer
	attempt := ClientAuthenticationAttempt{
		PublicClientID: publicClientID, SourceIP: strings.TrimSpace(request.SourceIP),
	}
	if authenticator.limiter.AllowClientAuthentication(ctx, attempt) != nil {
		return AuthenticatedClient{}, ErrInvalidClient
	}
	client, err := authenticator.clients.LookupPrivateKeyJWTClient(ctx, publicClientID)
	if err != nil {
		if !errors.Is(err, ErrPrivateKeyJWTClientNotFound) {
			return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
		}
		authenticator.reject(ctx, attempt, PrivateKeyJWTClient{}, request, FailureClientAssertionRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	now := authenticator.now()
	if !validPrivateKeyJWTClientState(client, now) || !constantTimeStringEqual(client.PublicClientID, publicClientID) {
		authenticator.reject(ctx, attempt, client, request, FailureClientAssertionRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	verificationKey, err := authenticator.keys.ResolveVerificationKey(ctx, client.JWKSURI, keyID, algorithm)
	if err != nil {
		authenticator.reject(ctx, attempt, client, request, FailureClientJWKSRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	credential, found := matchingPrivateKeyJWTCredential(client.Credentials, verificationKey.Thumbprint(), now)
	if !found {
		authenticator.reject(ctx, attempt, client, request, FailureClientAssertionRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	claims, err := authenticator.verifyClientAssertion(
		request.ClientAssertion, publicClientID, keyID, verificationKey, now,
	)
	if err != nil {
		authenticator.reject(ctx, attempt, client, request, FailureClientAssertionRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	jtiHash := sha256.Sum256([]byte(claims.ID))
	claimUntil := claims.ExpiresAt.Time.UTC().Add(DefaultTokenClockSkew)
	claimed, err := authenticator.replays.ClaimClientAssertionJTI(
		ctx, client.ClientID, jtiHash, claimUntil, now,
	)
	if err != nil {
		return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
	}
	if !claimed {
		authenticator.reject(ctx, attempt, client, request, FailureClientAssertionReplay)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	if err := authenticator.clients.MarkPrivateKeyJWTAuthenticated(
		ctx, credential.CredentialID, client.PublicClientID, now,
	); err != nil {
		if errors.Is(err, ErrPrivateKeyJWTClientNotFound) {
			authenticator.reject(ctx, attempt, client, request, FailureClientAssertionRejected)
			return AuthenticatedClient{}, ErrInvalidClient
		}
		return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
	}
	if authenticator.limiter.RecordClientAuthenticationSuccess(ctx, attempt) != nil {
		return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
	}
	return AuthenticatedClient{
		WorkspaceID: client.WorkspaceID, ClientID: client.ClientID,
		PublicClientID: client.PublicClientID, ServicePrincipalID: client.ServicePrincipalID,
		ServicePrincipalVersion: client.ServicePrincipalVersion,
		CredentialID:            credential.CredentialID, TokenTTLSeconds: client.TokenTTLSeconds,
	}, nil
}

func (authenticator *PrivateKeyJWTAuthenticator) verifyClientAssertion(
	assertion, publicClientID, keyID string,
	verificationKey VerificationJWK,
	now time.Time,
) (jwt.RegisteredClaims, error) {
	claims := jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(
		assertion, &claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != verificationKey.Algorithm() ||
				token.Header["kid"] != keyID || token.Header["alg"] != verificationKey.Algorithm() {
				return nil, ErrInvalidClient
			}
			return verificationKey.PublicKey(), nil
		},
		jwt.WithValidMethods([]string{verificationKey.Algorithm()}),
		jwt.WithIssuer(publicClientID), jwt.WithSubject(publicClientID),
		jwt.WithAudience(authenticator.tokenEndpoint), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(DefaultTokenClockSkew),
		jwt.WithTimeFunc(func() time.Time { return now }), jwt.WithStrictDecoding(),
	)
	if err != nil || token == nil || !token.Valid || claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		len(claims.ID) < minimumClientAssertionJTIBytes || len(claims.ID) > maximumClientAssertionJTIBytes ||
		strings.TrimSpace(claims.ID) != claims.ID {
		return jwt.RegisteredClaims{}, ErrInvalidClient
	}
	audience, err := claims.GetAudience()
	if err != nil || len(audience) != 1 || audience[0] != authenticator.tokenEndpoint {
		return jwt.RegisteredClaims{}, ErrInvalidClient
	}
	issuedAt, expiresAt := claims.IssuedAt.Time.UTC(), claims.ExpiresAt.Time.UTC()
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > authenticator.maxTTL ||
		issuedAt.After(now.Add(DefaultTokenClockSkew)) {
		return jwt.RegisteredClaims{}, ErrInvalidClient
	}
	if claims.NotBefore != nil {
		notBefore := claims.NotBefore.Time.UTC()
		if notBefore.Before(issuedAt.Add(-DefaultTokenClockSkew)) || notBefore.After(expiresAt) {
			return jwt.RegisteredClaims{}, ErrInvalidClient
		}
	}
	return claims, nil
}

func parseUnverifiedClientAssertion(assertion string) (jwt.RegisteredClaims, string, string, error) {
	if assertion == "" || len(assertion) > maximumClientAssertionBytes || strings.Count(assertion, ".") != 2 {
		return jwt.RegisteredClaims{}, "", "", ErrInvalidClient
	}
	claims := jwt.RegisteredClaims{}
	parser := jwt.NewParser(jwt.WithJSONNumber(), jwt.WithStrictDecoding())
	token, _, err := parser.ParseUnverified(assertion, &claims)
	if err != nil || token == nil || !allowedClientAssertionHeader(token.Header) ||
		claims.Issuer == "" || claims.Subject != claims.Issuer || !validPublicClientID(claims.Issuer) {
		return jwt.RegisteredClaims{}, "", "", ErrInvalidClient
	}
	keyID, keyIDOK := token.Header["kid"].(string)
	algorithm, algorithmOK := token.Header["alg"].(string)
	if !keyIDOK || !algorithmOK || !validRemoteJWKKeyID(keyID) || !allowedPrivateKeyJWTAlgorithm(algorithm) {
		return jwt.RegisteredClaims{}, "", "", ErrInvalidClient
	}
	return claims, keyID, algorithm, nil
}

func allowedClientAssertionHeader(header map[string]any) bool {
	for _, forbidden := range []string{"jku", "jwk", "x5u", "x5c", "crit"} {
		if _, exists := header[forbidden]; exists {
			return false
		}
	}
	if value, exists := header["typ"]; exists {
		typeValue, ok := value.(string)
		if !ok || typeValue != "JWT" {
			return false
		}
	}
	return true
}

func validPrivateKeyJWTClientState(client PrivateKeyJWTClient, now time.Time) bool {
	return client.WorkspaceID != "" && client.ClientID != "" && validPublicClientID(client.PublicClientID) &&
		client.ServicePrincipalID != "" && client.ServicePrincipalVersion >= 1 &&
		client.ClientActive && client.ServicePrincipalActive && client.PrivateKeyAuthentication &&
		validRemoteJWKSURI(client.JWKSURI) && client.TokenTTLSeconds > 0 && !now.IsZero() &&
		len(client.Credentials) > 0
}

func matchingPrivateKeyJWTCredential(
	credentials []PrivateKeyJWTCredential,
	thumbprint [sha256.Size]byte,
	now time.Time,
) (PrivateKeyJWTCredential, bool) {
	var result PrivateKeyJWTCredential
	found := false
	for _, credential := range credentials {
		active := credential.CredentialID != "" && len(credential.JWKThumbprint) == sha256.Size &&
			!credential.ValidFrom.IsZero() && !now.Before(credential.ValidFrom) &&
			credential.RevokedAt == nil && (credential.ExpiresAt == nil || now.Before(*credential.ExpiresAt))
		matches := hmac.Equal(credential.JWKThumbprint, thumbprint[:])
		if active && matches {
			if found {
				return PrivateKeyJWTCredential{}, false
			}
			result, found = credential, true
		}
	}
	return result, found
}

func (authenticator *PrivateKeyJWTAuthenticator) recordMalformedAttempt(
	ctx context.Context,
	request PrivateKeyJWTAuthenticationRequest,
) {
	attempt := ClientAuthenticationAttempt{SourceIP: strings.TrimSpace(request.SourceIP)}
	if authenticator.limiter.AllowClientAuthentication(ctx, attempt) != nil {
		return
	}
	_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
	authenticator.recordFailure(ctx, PrivateKeyJWTClient{}, request, FailureMalformedClientAssertion)
}

func (authenticator *PrivateKeyJWTAuthenticator) reject(
	ctx context.Context,
	attempt ClientAuthenticationAttempt,
	client PrivateKeyJWTClient,
	request PrivateKeyJWTAuthenticationRequest,
	code string,
) {
	_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
	authenticator.recordFailure(ctx, client, request, code)
}

func (authenticator *PrivateKeyJWTAuthenticator) recordFailure(
	ctx context.Context,
	client PrivateKeyJWTClient,
	request PrivateKeyJWTAuthenticationRequest,
	code string,
) {
	if authenticator.audit == nil {
		return
	}
	_ = authenticator.audit.RecordPrivateKeyJWTAuthenticationFailure(ctx, PrivateKeyJWTAuthenticationFailure{
		WorkspaceID: client.WorkspaceID, ClientID: client.ClientID, ErrorCode: code,
		SourceIP: strings.TrimSpace(request.SourceIP), UserAgent: strings.TrimSpace(request.UserAgent),
	})
}

func validTokenEndpointAudience(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasSuffix(parsed.Path, "/api/agent-access/v1/oauth/token") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	return parsed.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}
