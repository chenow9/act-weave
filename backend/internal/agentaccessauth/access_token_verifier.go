package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const maximumAAPAccessTokenBytes = 16 * 1024

var ErrInvalidAAPAccessToken = errors.New("AAP access token is invalid")

type AAPAccessTokenPrincipal struct {
	PrincipalID        string
	ServicePrincipalID string
	AuthorizedParty    string
	WorkspaceID        string
	AgentID            string
	Scopes             []string
	SecurityVersion    int64
	TokenID            string
	IssuedAt           time.Time
	NotBefore          time.Time
	ExpiresAt          time.Time
}

func (principal AAPAccessTokenPrincipal) HasScope(scope string) bool {
	for _, granted := range principal.Scopes {
		if granted == scope {
			return true
		}
	}
	return false
}

type AAPAccessTokenVerifier struct {
	keys       SigningKeyProvider
	issuer     string
	audience   string
	maximumTTL time.Duration
	now        func() time.Time
}

func NewAAPAccessTokenVerifier(
	keys SigningKeyProvider,
	tokenEndpoint string,
	maximumTTL time.Duration,
) (*AAPAccessTokenVerifier, error) {
	if keys == nil || !validTokenEndpointAudience(tokenEndpoint) ||
		maximumTTL < MinimumAccessTokenTTL || maximumTTL > DefaultMaxAccessTokenTTL ||
		maximumTTL%time.Second != 0 || maximumTTL > keys.MaximumTokenTTL() {
		return nil, errors.New("AAP signing keys with a 5-15 minute lifetime and Token Endpoint are required")
	}
	return &AAPAccessTokenVerifier{
		keys: keys, issuer: strings.TrimSuffix(tokenEndpoint, "/token"),
		audience: AAPAccessTokenAudience, maximumTTL: maximumTTL,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (verifier *AAPAccessTokenVerifier) VerifyAccessToken(
	ctx context.Context,
	value string,
) (AAPAccessTokenPrincipal, error) {
	if verifier == nil || verifier.keys == nil || ctx == nil || value == "" ||
		strings.TrimSpace(value) != value || len(value) > maximumAAPAccessTokenBytes ||
		strings.Count(value, ".") != 2 {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	now := verifier.now().UTC()
	claims := AAPAccessTokenClaims{}
	token, err := jwt.ParseWithClaims(
		value, &claims,
		func(token *jwt.Token) (any, error) {
			if !validAAPAccessTokenHeader(token) {
				return nil, ErrInvalidAAPAccessToken
			}
			keyID := token.Header["kid"].(string)
			key, err := verifier.keys.VerificationKey(keyID, now)
			if err != nil {
				return nil, ErrInvalidAAPAccessToken
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{AAPSigningAlgorithm}),
		jwt.WithStrictDecoding(), jwt.WithoutClaimsValidation(),
	)
	if err != nil || token == nil || !token.Valid || !validAAPAccessTokenHeader(token) {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	principal, err := verifier.validateClaims(claims, now)
	if err != nil {
		return AAPAccessTokenPrincipal{}, err
	}
	return principal, nil
}

func (verifier *AAPAccessTokenVerifier) validateClaims(
	claims AAPAccessTokenClaims,
	now time.Time,
) (AAPAccessTokenPrincipal, error) {
	if claims.Issuer != verifier.issuer || claims.Subject == "" ||
		!validCanonicalUUID(claims.Subject) || !validPublicClientID(claims.AuthorizedParty) ||
		!validCanonicalUUID(claims.WorkspaceID) || !validCanonicalUUID(claims.AgentID) ||
		claims.SecurityVersion < 1 || !validCanonicalUUID(claims.ID) ||
		claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt == nil {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	servicePrincipalID := claims.Subject
	if claims.Actor != nil {
		if claims.Actor.Subject == "" || !validCanonicalUUID(claims.Actor.Subject) ||
			claims.Actor.Subject == claims.Subject {
			return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
		}
		servicePrincipalID = claims.Actor.Subject
	}
	audience, err := claims.GetAudience()
	if err != nil || len(audience) != 1 || audience[0] != verifier.audience {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	scopes, canonicalScope, err := canonicalizeRequestedScopes(strings.Split(claims.Scope, " "))
	if err != nil || claims.Scope != canonicalScope {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	issuedAt := claims.IssuedAt.Time.UTC()
	notBefore := claims.NotBefore.Time.UTC()
	expiresAt := claims.ExpiresAt.Time.UTC()
	if issuedAt.IsZero() || notBefore.IsZero() || expiresAt.IsZero() ||
		issuedAt.After(now.Add(DefaultTokenClockSkew)) ||
		notBefore.After(now.Add(DefaultTokenClockSkew)) ||
		notBefore.Before(issuedAt.Add(-DefaultTokenClockSkew)) || notBefore.After(issuedAt) ||
		!expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) < MinimumAccessTokenTTL ||
		expiresAt.Sub(issuedAt) > verifier.maximumTTL {
		return AAPAccessTokenPrincipal{}, ErrInvalidAAPAccessToken
	}
	if !now.Before(expiresAt) {
		return AAPAccessTokenPrincipal{}, ErrTokenExpired
	}
	return AAPAccessTokenPrincipal{
		PrincipalID: claims.Subject, ServicePrincipalID: servicePrincipalID,
		AuthorizedParty: claims.AuthorizedParty, WorkspaceID: claims.WorkspaceID,
		AgentID: claims.AgentID, Scopes: append([]string(nil), scopes...),
		SecurityVersion: claims.SecurityVersion, TokenID: claims.ID,
		IssuedAt: issuedAt, NotBefore: notBefore, ExpiresAt: expiresAt,
	}, nil
}

func validAAPAccessTokenHeader(token *jwt.Token) bool {
	if token == nil || len(token.Header) != 3 || token.Method != jwt.SigningMethodEdDSA {
		return false
	}
	algorithm, algorithmOK := token.Header["alg"].(string)
	typeValue, typeOK := token.Header["typ"].(string)
	keyID, keyIDOK := token.Header["kid"].(string)
	return algorithmOK && typeOK && keyIDOK && algorithm == AAPSigningAlgorithm &&
		typeValue == AAPAccessTokenType && validSigningKeyID(keyID)
}
