package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AAPAccessTokenAudience = "actweave-agent-access"
	MinimumAccessTokenTTL  = 5 * time.Minute
)

var (
	ErrClientCredentialsRequestInvalid = errors.New("client credentials request is invalid")
	ErrClientCredentialsScopeInvalid   = errors.New("invalid_scope")
	ErrClientCredentialsTargetInvalid  = errors.New("invalid_target")
	ErrClientCredentialsGrantNotFound  = errors.New("client credentials Grant was not found")
	ErrTokenServiceUnavailable         = errors.New("token service is unavailable")
)

var canonicalAAPScopes = []string{
	"agent:read",
	"conversation:create",
	"conversation:read",
	"run:create",
	"run:read",
	"run:cancel",
	"event:read",
	"interaction:decide",
	"artifact:read",
	"file:write",
	"file:read",
}

// ClientCredentialsGrant is an authorization snapshot read after Client
// authentication. The store must recheck all mutable Client, Credential,
// Principal, Workspace, Agent, and Grant state in the same query.
type ClientCredentialsGrant struct {
	GrantID                 string
	WorkspaceID             string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	ServicePrincipalVersion int64
	AgentID                 string
	GrantedScopes           []string
	ClientTokenTTL          time.Duration
	GrantExpiresAt          *time.Time
}

type ClientCredentialsGrantStore interface {
	ResolveClientCredentialsGrant(
		context.Context,
		AuthenticatedClient,
		string,
		time.Time,
	) (ClientCredentialsGrant, error)
}

// AAPAccessTokenActorClaim is the RFC 8693 act claim. When present, Token
// Exchange Access Tokens put the External Subject in sub and the Service
// Principal in act.sub. Client Credentials tokens omit act.
type AAPAccessTokenActorClaim struct {
	Subject string `json:"sub"`
}

type AAPAccessTokenClaims struct {
	jwt.RegisteredClaims
	AuthorizedParty string                    `json:"azp"`
	WorkspaceID     string                    `json:"wid"`
	AgentID         string                    `json:"aid"`
	Scope           string                    `json:"scope"`
	SecurityVersion int64                     `json:"ver"`
	Actor           *AAPAccessTokenActorClaim `json:"act,omitempty"`
}

type ClientCredentialsTokenRequest struct {
	Client          AuthenticatedClient
	AgentID         string
	RequestedScopes []string
}

type ClientCredentialsToken struct {
	AccessToken string
	TokenType   string
	ExpiresIn   int64
	Scope       string
	Claims      AAPAccessTokenClaims
}

type ClientCredentialsTokenService struct {
	grants     ClientCredentialsGrantStore
	keys       SigningKeyProvider
	issuer     string
	audience   string
	maximumTTL time.Duration
	now        func() time.Time
	newJTI     func() (string, error)
}

func NewClientCredentialsTokenService(
	grants ClientCredentialsGrantStore,
	keys SigningKeyProvider,
	tokenEndpoint string,
	maximumTTL time.Duration,
) (*ClientCredentialsTokenService, error) {
	if grants == nil || keys == nil || !validTokenEndpointAudience(tokenEndpoint) ||
		maximumTTL < MinimumAccessTokenTTL || maximumTTL > DefaultMaxAccessTokenTTL ||
		maximumTTL%time.Second != 0 || maximumTTL > keys.MaximumTokenTTL() {
		return nil, errors.New("Client Credentials Grant store, signing keys, Token Endpoint, and 5-15 minute maximum TTL are required")
	}
	issuer := strings.TrimSuffix(tokenEndpoint, "/token")
	return &ClientCredentialsTokenService{
		grants: grants, keys: keys, issuer: issuer, audience: AAPAccessTokenAudience,
		maximumTTL: maximumTTL, now: func() time.Time { return time.Now().UTC() },
		newJTI: func() (string, error) {
			value, err := uuid.NewV7()
			return value.String(), err
		},
	}, nil
}

func (service *ClientCredentialsTokenService) IssueClientCredentialsToken(
	ctx context.Context,
	request ClientCredentialsTokenRequest,
) (ClientCredentialsToken, error) {
	if service == nil || service.grants == nil || service.keys == nil || ctx == nil ||
		!validAuthenticatedClient(request.Client) || !validCanonicalUUID(request.AgentID) {
		return ClientCredentialsToken{}, ErrClientCredentialsRequestInvalid
	}
	requestedScopes, scopeValue, err := canonicalizeRequestedScopes(request.RequestedScopes)
	if err != nil {
		return ClientCredentialsToken{}, err
	}
	now := service.now().UTC()
	grant, err := service.grants.ResolveClientCredentialsGrant(ctx, request.Client, request.AgentID, now)
	if err != nil {
		if errors.Is(err, ErrClientCredentialsGrantNotFound) {
			return ClientCredentialsToken{}, ErrClientCredentialsTargetInvalid
		}
		return ClientCredentialsToken{}, ErrTokenServiceUnavailable
	}
	if !validClientCredentialsGrant(grant, request.Client, request.AgentID, now) {
		return ClientCredentialsToken{}, ErrClientCredentialsTargetInvalid
	}
	if !scopeSubset(requestedScopes, grant.GrantedScopes) {
		return ClientCredentialsToken{}, ErrClientCredentialsScopeInvalid
	}
	ttl, ok := service.tokenTTL(grant, now)
	if !ok {
		return ClientCredentialsToken{}, ErrClientCredentialsTargetInvalid
	}
	jti, err := service.newJTI()
	if err != nil || !validCanonicalUUID(jti) {
		return ClientCredentialsToken{}, ErrTokenServiceUnavailable
	}
	claimsNow := now.Truncate(time.Second)
	claims := AAPAccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: service.issuer, Subject: grant.ServicePrincipalID,
			Audience: jwt.ClaimStrings{service.audience}, ID: jti,
			IssuedAt:  jwt.NewNumericDate(claimsNow),
			NotBefore: jwt.NewNumericDate(claimsNow.Add(-DefaultTokenClockSkew)),
			ExpiresAt: jwt.NewNumericDate(claimsNow.Add(ttl)),
		},
		AuthorizedParty: grant.PublicClientID, WorkspaceID: grant.WorkspaceID,
		AgentID: grant.AgentID, Scope: scopeValue,
		SecurityVersion: grant.ServicePrincipalVersion,
	}
	key, err := service.keys.ActiveSigningKey(now)
	if err != nil {
		return ClientCredentialsToken{}, ErrTokenServiceUnavailable
	}
	value, err := key.SignAccessToken(claims)
	if err != nil {
		return ClientCredentialsToken{}, ErrTokenServiceUnavailable
	}
	return ClientCredentialsToken{
		AccessToken: value, TokenType: "Bearer", ExpiresIn: int64(ttl / time.Second),
		Scope: scopeValue, Claims: claims,
	}, nil
}

func (service *ClientCredentialsTokenService) tokenTTL(
	grant ClientCredentialsGrant,
	now time.Time,
) (time.Duration, bool) {
	ttl := grant.ClientTokenTTL
	if ttl > service.maximumTTL {
		ttl = service.maximumTTL
	}
	if grant.GrantExpiresAt != nil {
		remaining := time.Duration(int64(grant.GrantExpiresAt.UTC().Sub(now)/time.Second)) * time.Second
		if remaining < ttl {
			ttl = remaining
		}
	}
	return ttl, ttl >= MinimumAccessTokenTTL && ttl <= DefaultMaxAccessTokenTTL && ttl%time.Second == 0
}

func canonicalizeRequestedScopes(scopes []string) ([]string, string, error) {
	if len(scopes) == 0 || len(scopes) > len(canonicalAAPScopes) {
		return nil, "", ErrClientCredentialsScopeInvalid
	}
	requested := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" || strings.TrimSpace(scope) != scope {
			return nil, "", ErrClientCredentialsScopeInvalid
		}
		if _, duplicate := requested[scope]; duplicate {
			return nil, "", ErrClientCredentialsScopeInvalid
		}
		requested[scope] = struct{}{}
	}
	ordered := make([]string, 0, len(requested))
	for _, scope := range canonicalAAPScopes {
		if _, exists := requested[scope]; exists {
			ordered = append(ordered, scope)
			delete(requested, scope)
		}
	}
	if len(requested) != 0 {
		return nil, "", ErrClientCredentialsScopeInvalid
	}
	return ordered, strings.Join(ordered, " "), nil
}

func scopeSubset(requested, granted []string) bool {
	allowed := make(map[string]struct{}, len(granted))
	for _, scope := range granted {
		allowed[scope] = struct{}{}
	}
	for _, scope := range requested {
		if _, exists := allowed[scope]; !exists {
			return false
		}
	}
	return true
}

func validAuthenticatedClient(client AuthenticatedClient) bool {
	return validCanonicalUUID(client.WorkspaceID) && validCanonicalUUID(client.ClientID) &&
		validPublicClientID(client.PublicClientID) && validCanonicalUUID(client.ServicePrincipalID) &&
		client.ServicePrincipalVersion > 0 && validCanonicalUUID(client.CredentialID) &&
		client.TokenTTLSeconds >= int(MinimumAccessTokenTTL/time.Second) &&
		client.TokenTTLSeconds <= int(DefaultMaxAccessTokenTTL/time.Second)
}

func validClientCredentialsGrant(
	grant ClientCredentialsGrant,
	client AuthenticatedClient,
	agentID string,
	now time.Time,
) bool {
	if !validCanonicalUUID(grant.GrantID) || grant.WorkspaceID != client.WorkspaceID ||
		grant.ClientID != client.ClientID || grant.PublicClientID != client.PublicClientID ||
		grant.ServicePrincipalID != client.ServicePrincipalID ||
		grant.ServicePrincipalVersion != client.ServicePrincipalVersion ||
		grant.AgentID != agentID || grant.ClientTokenTTL != time.Duration(client.TokenTTLSeconds)*time.Second ||
		len(grant.GrantedScopes) == 0 || grant.ClientTokenTTL < MinimumAccessTokenTTL ||
		grant.ClientTokenTTL > DefaultMaxAccessTokenTTL {
		return false
	}
	if grant.GrantExpiresAt != nil && !grant.GrantExpiresAt.After(now) {
		return false
	}
	_, _, err := canonicalizeRequestedScopes(grant.GrantedScopes)
	return err == nil
}

func validCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
