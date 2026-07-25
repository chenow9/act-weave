package agentaccessauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	TokenExchangeGrantType       = "urn:ietf:params:oauth:grant-type:token-exchange"
	SubjectTokenTypeJWT          = "urn:ietf:params:oauth:token-type:jwt"
	IssuedTokenTypeAccessToken   = "urn:ietf:params:oauth:token-type:access_token"
	maximumSubjectTokenFormBytes = maximumSubjectTokenBytes
)

var (
	ErrTokenExchangeRequestInvalid = errors.New("token exchange request is invalid")
	ErrTokenExchangeSubjectInvalid = errors.New("invalid_subject_token")
	ErrTokenExchangeTargetInvalid  = errors.New("invalid_target")
	ErrTokenExchangeScopeInvalid   = errors.New("invalid_scope")
	ErrTokenExchangeTrustMissing   = errors.New("trusted subject issuer is not configured")
	ErrTokenExchangeSubjectDenied  = errors.New("external subject is not allowed")
	ErrTokenExchangeReplay         = errors.New("subject token was replayed")
)

// TokenExchangeTrust is the Client-scoped trust material required for Subject
// Token validation. Callers must load it from current Client configuration.
type TokenExchangeTrust struct {
	Config TrustedSubjectIssuerConfig
}

type TokenExchangeTrustStore interface {
	LookupTokenExchangeTrust(context.Context, AuthenticatedClient) (TokenExchangeTrust, error)
}

// ExternalSubjectBinding is the durable External Subject mapping used as the
// Access Token sub. Raw subject values never leave the hashing boundary.
type ExternalSubjectBinding struct {
	SubjectID string
	Active    bool
}

type ExternalSubjectMapper interface {
	ResolveActiveExternalSubject(
		context.Context,
		string, // workspaceID
		string, // clientID
		string, // issuer
		[sha256.Size]byte,
		time.Time,
	) (ExternalSubjectBinding, error)
}

type SubjectTokenJTIStore interface {
	ClaimSubjectTokenJTI(context.Context, string, [sha256.Size]byte, time.Time, time.Time) (bool, error)
}

type TokenExchangeRequest struct {
	Client             AuthenticatedClient
	AgentID            string
	RequestedScopes    []string
	SubjectToken       string `json:"-"`
	SubjectTokenType   string
	RequestedTokenType string
}

type TokenExchangeToken struct {
	AccessToken     string
	IssuedTokenType string
	TokenType       string
	ExpiresIn       int64
	Scope           string
	Claims          AAPAccessTokenClaims
}

type TokenExchangeService struct {
	trust     TokenExchangeTrustStore
	subjects  ExternalSubjectMapper
	verifier  *TrustedSubjectTokenVerifier
	replays   SubjectTokenJTIStore
	grants    ClientCredentialsGrantStore
	keys      SigningKeyProvider
	pepper    []byte
	issuer    string
	audience  string
	maximumTTL time.Duration
	now       func() time.Time
	newJTI    func() (string, error)
}

func NewTokenExchangeService(
	trust TokenExchangeTrustStore,
	subjects ExternalSubjectMapper,
	verifier *TrustedSubjectTokenVerifier,
	replays SubjectTokenJTIStore,
	grants ClientCredentialsGrantStore,
	keys SigningKeyProvider,
	pepper []byte,
	tokenEndpoint string,
	maximumTTL time.Duration,
) (*TokenExchangeService, error) {
	if trust == nil || subjects == nil || verifier == nil || replays == nil || grants == nil ||
		keys == nil || len(pepper) < 32 || !validTokenEndpointAudience(tokenEndpoint) ||
		maximumTTL < MinimumAccessTokenTTL || maximumTTL > DefaultMaxAccessTokenTTL ||
		maximumTTL%time.Second != 0 || maximumTTL > keys.MaximumTokenTTL() {
		return nil, errors.New("Token Exchange dependencies, pepper, Token Endpoint, and 5-15 minute maximum TTL are required")
	}
	return &TokenExchangeService{
		trust: trust, subjects: subjects, verifier: verifier, replays: replays,
		grants: grants, keys: keys, pepper: append([]byte(nil), pepper...),
		issuer: strings.TrimSuffix(tokenEndpoint, "/token"), audience: AAPAccessTokenAudience,
		maximumTTL: maximumTTL, now: func() time.Time { return time.Now().UTC() },
		newJTI: func() (string, error) {
			value, err := uuid.NewV7()
			return value.String(), err
		},
	}, nil
}

func (service *TokenExchangeService) IssueTokenExchange(
	ctx context.Context,
	request TokenExchangeRequest,
) (TokenExchangeToken, error) {
	if service == nil || ctx == nil || !validAuthenticatedClient(request.Client) ||
		!validCanonicalUUID(request.AgentID) ||
		request.SubjectTokenType != SubjectTokenTypeJWT ||
		request.SubjectToken == "" || strings.TrimSpace(request.SubjectToken) != request.SubjectToken ||
		len(request.SubjectToken) > maximumSubjectTokenFormBytes ||
		(request.RequestedTokenType != "" && request.RequestedTokenType != IssuedTokenTypeAccessToken) {
		return TokenExchangeToken{}, ErrTokenExchangeRequestInvalid
	}
	requestedScopes, scopeValue, err := canonicalizeRequestedScopes(request.RequestedScopes)
	if err != nil {
		return TokenExchangeToken{}, ErrTokenExchangeScopeInvalid
	}
	trust, err := service.trust.LookupTokenExchangeTrust(ctx, request.Client)
	if err != nil {
		if errors.Is(err, ErrTokenExchangeTrustMissing) {
			return TokenExchangeToken{}, ErrTokenExchangeTrustMissing
		}
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	if !ValidTrustedSubjectIssuerConfig(trust.Config) {
		return TokenExchangeToken{}, ErrTokenExchangeTrustMissing
	}
	now := service.now().UTC()
	verified, err := service.verifier.VerifySubjectToken(ctx, trust.Config, request.SubjectToken)
	if err != nil {
		return TokenExchangeToken{}, ErrTokenExchangeSubjectInvalid
	}
	if trust.Config.ClaimPolicy.RequireJTI {
		if verified.TokenID == "" {
			return TokenExchangeToken{}, ErrTokenExchangeSubjectInvalid
		}
		jtiHash := sha256.Sum256([]byte(verified.TokenID))
		claimUntil := verified.ExpiresAt.Add(DefaultTokenClockSkew)
		claimed, claimErr := service.replays.ClaimSubjectTokenJTI(
			ctx, request.Client.ClientID, jtiHash, claimUntil, now,
		)
		if claimErr != nil {
			return TokenExchangeToken{}, ErrTokenServiceUnavailable
		}
		if !claimed {
			return TokenExchangeToken{}, ErrTokenExchangeReplay
		}
	}
	subjectHash := HashExternalSubject(service.pepper, verified.Issuer, verified.Subject)
	binding, err := service.subjects.ResolveActiveExternalSubject(
		ctx, request.Client.WorkspaceID, request.Client.ClientID, verified.Issuer, subjectHash, now,
	)
	if err != nil {
		if errors.Is(err, ErrTokenExchangeSubjectDenied) {
			return TokenExchangeToken{}, ErrTokenExchangeSubjectDenied
		}
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	if !binding.Active || !validCanonicalUUID(binding.SubjectID) {
		return TokenExchangeToken{}, ErrTokenExchangeSubjectDenied
	}
	grant, err := service.grants.ResolveClientCredentialsGrant(ctx, request.Client, request.AgentID, now)
	if err != nil {
		if errors.Is(err, ErrClientCredentialsGrantNotFound) {
			return TokenExchangeToken{}, ErrTokenExchangeTargetInvalid
		}
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	if !validClientCredentialsGrant(grant, request.Client, request.AgentID, now) {
		return TokenExchangeToken{}, ErrTokenExchangeTargetInvalid
	}
	if !scopeSubset(requestedScopes, grant.GrantedScopes) {
		return TokenExchangeToken{}, ErrTokenExchangeScopeInvalid
	}
	ttl, ok := service.tokenTTL(grant, verified, now)
	if !ok {
		return TokenExchangeToken{}, ErrTokenExchangeSubjectInvalid
	}
	jti, err := service.newJTI()
	if err != nil || !validCanonicalUUID(jti) {
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	claimsNow := now.Truncate(time.Second)
	claims := AAPAccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: service.issuer, Subject: binding.SubjectID,
			Audience: jwt.ClaimStrings{service.audience}, ID: jti,
			IssuedAt:  jwt.NewNumericDate(claimsNow),
			NotBefore: jwt.NewNumericDate(claimsNow.Add(-DefaultTokenClockSkew)),
			ExpiresAt: jwt.NewNumericDate(claimsNow.Add(ttl)),
		},
		AuthorizedParty: grant.PublicClientID, WorkspaceID: grant.WorkspaceID,
		AgentID: grant.AgentID, Scope: scopeValue,
		SecurityVersion: grant.ServicePrincipalVersion,
		Actor:           &AAPAccessTokenActorClaim{Subject: grant.ServicePrincipalID},
	}
	key, err := service.keys.ActiveSigningKey(now)
	if err != nil {
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	value, err := key.SignAccessToken(claims)
	if err != nil {
		return TokenExchangeToken{}, ErrTokenServiceUnavailable
	}
	return TokenExchangeToken{
		AccessToken: value, IssuedTokenType: IssuedTokenTypeAccessToken,
		TokenType: "Bearer", ExpiresIn: int64(ttl / time.Second),
		Scope: scopeValue, Claims: claims,
	}, nil
}

func (service *TokenExchangeService) tokenTTL(
	grant ClientCredentialsGrant,
	verified VerifiedSubjectToken,
	now time.Time,
) (time.Duration, bool) {
	ttl := grant.ClientTokenTTL
	if ttl > service.maximumTTL {
		ttl = service.maximumTTL
	}
	// Output lifetime must not exceed remaining Subject Token lifetime.
	subjectRemaining := time.Duration(int64(verified.ExpiresAt.UTC().Sub(now)/time.Second)) * time.Second
	if subjectRemaining < ttl {
		ttl = subjectRemaining
	}
	if grant.GrantExpiresAt != nil {
		remaining := time.Duration(int64(grant.GrantExpiresAt.UTC().Sub(now)/time.Second)) * time.Second
		if remaining < ttl {
			ttl = remaining
		}
	}
	return ttl, ttl >= MinimumAccessTokenTTL && ttl <= DefaultMaxAccessTokenTTL && ttl%time.Second == 0
}

// HashExternalSubject produces the durable keyed mapping used by external_subjects.
// The raw subject value is never persisted.
func HashExternalSubject(pepper []byte, issuer, subject string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(issuer))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(subject))
	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

// InMemorySubjectTokenJTIStore is a process-local replay guard for tests and
// single-instance deployments. Production uses the PostgreSQL-backed store.
type InMemorySubjectTokenJTIStore struct {
	inner *InMemoryClientAssertionJTIStore
}

func NewInMemorySubjectTokenJTIStore(maxEntries int) (*InMemorySubjectTokenJTIStore, error) {
	inner, err := NewInMemoryClientAssertionJTIStore(maxEntries)
	if err != nil {
		return nil, err
	}
	return &InMemorySubjectTokenJTIStore{inner: inner}, nil
}

func (store *InMemorySubjectTokenJTIStore) ClaimSubjectTokenJTI(
	ctx context.Context,
	clientID string,
	jtiHash [sha256.Size]byte,
	expiresAt, now time.Time,
) (bool, error) {
	if store == nil || store.inner == nil {
		return false, errors.New("subject token JTI store is required")
	}
	return store.inner.ClaimClientAssertionJTI(ctx, clientID, jtiHash, expiresAt, now)
}
