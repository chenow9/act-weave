package agentaccessauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

const (
	SubjectClaimSub                 = "sub"
	DefaultSubjectClaimMaxBytes     = 256
	DefaultSubjectTokenMaxTTLSeconds = 3600
	minimumSubjectTokenMaxTTLSeconds = 60
	maximumSubjectTokenMaxTTLSeconds = 86400
	maximumSubjectTokenBytes        = 16 * 1024
	maximumSubjectIssuerBytes       = 2048
	maximumSubjectAudienceBytes     = 2048
)

var (
	ErrTrustedSubjectIssuerInvalid = errors.New("trusted subject issuer configuration is invalid")
	ErrSubjectTokenRejected        = errors.New("subject token was rejected")
)

// SubjectClaimPolicy controls which Subject Token claims are accepted for a
// Client. v1 only supports the standard "sub" claim and never stores or returns
// raw external subject values through management APIs.
type SubjectClaimPolicy struct {
	SubjectClaim       string `json:"subjectClaim"`
	RequireJTI         bool   `json:"requireJti"`
	MaxSubjectBytes    int    `json:"maxSubjectBytes"`
	MaxTokenTTLSeconds int    `json:"maxTokenTTLSeconds"`
}

// TrustedSubjectIssuerConfig is the Client-scoped trust material used to
// validate Subject Tokens. Inline JWKS and JWKS URI are mutually exclusive.
// OIDC Discovery is intentionally unsupported.
type TrustedSubjectIssuerConfig struct {
	Issuer      string
	Audience    string
	JWKSURI     string
	InlineJWKS  json.RawMessage
	Algorithms  []string
	ClaimPolicy SubjectClaimPolicy
}

// VerifiedSubjectToken is the internal result of Subject Token verification.
// Subject holds the claim value only for the caller's keyed-hash boundary;
// callers must not log, audit, or return the raw Subject Token or Subject.
type VerifiedSubjectToken struct {
	Issuer    string
	Audience  string
	Subject   string `json:"-"`
	TokenID   string
	Algorithm string
	KeyID     string
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time
}

type TrustedSubjectTokenVerifier struct {
	keys *RemoteJWKSCache
	now  func() time.Time
}

func NewTrustedSubjectTokenVerifier(keys *RemoteJWKSCache) (*TrustedSubjectTokenVerifier, error) {
	if keys == nil {
		return nil, ErrTrustedSubjectIssuerInvalid
	}
	return &TrustedSubjectTokenVerifier{
		keys: keys,
		now:  func() time.Time { return time.Now().UTC() },
	}, nil
}

func DefaultSubjectClaimPolicy() SubjectClaimPolicy {
	return SubjectClaimPolicy{
		SubjectClaim:       SubjectClaimSub,
		RequireJTI:         true,
		MaxSubjectBytes:    DefaultSubjectClaimMaxBytes,
		MaxTokenTTLSeconds: DefaultSubjectTokenMaxTTLSeconds,
	}
}

func ValidTrustedSubjectIssuerConfig(config TrustedSubjectIssuerConfig) bool {
	if !validTrustedSubjectIssuer(config.Issuer) || !validTrustedSubjectAudience(config.Audience) ||
		!validSubjectClaimPolicy(config.ClaimPolicy) {
		return false
	}
	algorithms := allowedAlgorithmSet(config.Algorithms)
	if len(algorithms) == 0 {
		return false
	}
	hasURI := strings.TrimSpace(config.JWKSURI) != ""
	hasInline := len(bytesTrimSpace(config.InlineJWKS)) > 0
	if hasURI == hasInline {
		return false
	}
	if hasURI && !validRemoteJWKSURI(config.JWKSURI) {
		return false
	}
	if hasInline {
		if _, err := ParseInlineJWKS(config.InlineJWKS, DefaultRemoteJWKSMaxKeys, config.Algorithms); err != nil {
			return false
		}
	}
	return true
}

func (verifier *TrustedSubjectTokenVerifier) VerifySubjectToken(
	ctx context.Context,
	config TrustedSubjectIssuerConfig,
	rawToken string,
) (VerifiedSubjectToken, error) {
	if verifier == nil || verifier.keys == nil || ctx == nil ||
		!ValidTrustedSubjectIssuerConfig(config) ||
		rawToken == "" || strings.TrimSpace(rawToken) != rawToken ||
		len(rawToken) > maximumSubjectTokenBytes || strings.Count(rawToken, ".") != 2 {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	now := verifier.now().UTC()
	unverified, keyID, algorithm, err := parseUnverifiedSubjectToken(rawToken, config)
	if err != nil {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	verificationKey, err := verifier.resolveKey(ctx, config, keyID, algorithm)
	if err != nil {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken, &claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != verificationKey.Algorithm() ||
				token.Header["kid"] != keyID || token.Header["alg"] != verificationKey.Algorithm() {
				return nil, ErrSubjectTokenRejected
			}
			return verificationKey.PublicKey(), nil
		},
		jwt.WithValidMethods([]string{verificationKey.Algorithm()}),
		jwt.WithIssuer(config.Issuer), jwt.WithAudience(config.Audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(),
		jwt.WithLeeway(DefaultTokenClockSkew),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithStrictDecoding(),
	)
	if err != nil || token == nil || !token.Valid {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	return validateVerifiedSubjectClaims(claims, config, algorithm, keyID, now, unverified)
}

func (verifier *TrustedSubjectTokenVerifier) resolveKey(
	ctx context.Context,
	config TrustedSubjectIssuerConfig,
	keyID, algorithm string,
) (VerificationJWK, error) {
	if len(config.InlineJWKS) > 0 {
		keys, err := ParseInlineJWKS(config.InlineJWKS, DefaultRemoteJWKSMaxKeys, config.Algorithms)
		if err != nil {
			return VerificationJWK{}, err
		}
		key, found := findVerificationJWK(keys, keyID, algorithm)
		if !found {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
		return key, nil
	}
	return verifier.keys.ResolveVerificationKeyWithAlgorithms(
		ctx, config.JWKSURI, keyID, algorithm, config.Algorithms,
	)
}

func parseUnverifiedSubjectToken(
	rawToken string,
	config TrustedSubjectIssuerConfig,
) (jwt.MapClaims, string, string, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithJSONNumber(), jwt.WithStrictDecoding())
	token, _, err := parser.ParseUnverified(rawToken, &claims)
	if err != nil || token == nil || !allowedSubjectTokenHeader(token.Header) {
		return nil, "", "", ErrSubjectTokenRejected
	}
	keyID, keyIDOK := token.Header["kid"].(string)
	algorithm, algorithmOK := token.Header["alg"].(string)
	allowed := allowedAlgorithmSet(config.Algorithms)
	if !keyIDOK || !algorithmOK || !validRemoteJWKKeyID(keyID) || allowed == nil {
		return nil, "", "", ErrSubjectTokenRejected
	}
	if _, ok := allowed[algorithm]; !ok {
		return nil, "", "", ErrSubjectTokenRejected
	}
	return claims, keyID, algorithm, nil
}

func allowedSubjectTokenHeader(header map[string]any) bool {
	// Reject header-based key discovery. Subject trust uses only the configured
	// Inline JWKS or fixed JWKS URI — never jku/jwk/x5u or OIDC Discovery.
	for _, forbidden := range []string{"jku", "jwk", "x5u", "x5c", "crit"} {
		if _, exists := header[forbidden]; exists {
			return false
		}
	}
	if value, exists := header["typ"]; exists {
		typeValue, ok := value.(string)
		if !ok || (typeValue != "JWT" && typeValue != "jwt") {
			return false
		}
	}
	return true
}

func validateVerifiedSubjectClaims(
	claims jwt.MapClaims,
	config TrustedSubjectIssuerConfig,
	algorithm, keyID string,
	now time.Time,
	_ jwt.MapClaims,
) (VerifiedSubjectToken, error) {
	issuer, err := claims.GetIssuer()
	if err != nil || issuer != config.Issuer {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	audience, err := claims.GetAudience()
	if err != nil || len(audience) != 1 || audience[0] != config.Audience {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	subject, ok := claims[config.ClaimPolicy.SubjectClaim].(string)
	if !ok || !validSubjectClaimValue(subject, config.ClaimPolicy.MaxSubjectBytes) {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	issuedAt, err := claims.GetIssuedAt()
	if err != nil || issuedAt == nil {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	expiresAt, err := claims.GetExpirationTime()
	if err != nil || expiresAt == nil {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	issued := issuedAt.Time.UTC()
	expires := expiresAt.Time.UTC()
	if issued.IsZero() || expires.IsZero() || !expires.After(issued) ||
		issued.After(now.Add(DefaultTokenClockSkew)) ||
		expires.Sub(issued) > time.Duration(config.ClaimPolicy.MaxTokenTTLSeconds)*time.Second ||
		!now.Before(expires.Add(DefaultTokenClockSkew)) {
		return VerifiedSubjectToken{}, ErrSubjectTokenRejected
	}
	notBefore := issued
	if rawNotBefore, err := claims.GetNotBefore(); err == nil && rawNotBefore != nil {
		notBefore = rawNotBefore.Time.UTC()
		if notBefore.IsZero() ||
			notBefore.Before(issued.Add(-DefaultTokenClockSkew)) ||
			notBefore.After(expires) ||
			notBefore.After(now.Add(DefaultTokenClockSkew)) {
			return VerifiedSubjectToken{}, ErrSubjectTokenRejected
		}
	}
	tokenID := ""
	if config.ClaimPolicy.RequireJTI {
		rawJTI, ok := claims["jti"].(string)
		if !ok || !validSubjectTokenID(rawJTI) {
			return VerifiedSubjectToken{}, ErrSubjectTokenRejected
		}
		tokenID = rawJTI
	} else if rawJTI, ok := claims["jti"].(string); ok && rawJTI != "" {
		if !validSubjectTokenID(rawJTI) {
			return VerifiedSubjectToken{}, ErrSubjectTokenRejected
		}
		tokenID = rawJTI
	}
	return VerifiedSubjectToken{
		Issuer: issuer, Audience: audience[0], Subject: subject, TokenID: tokenID,
		Algorithm: algorithm, KeyID: keyID, IssuedAt: issued, NotBefore: notBefore, ExpiresAt: expires,
	}, nil
}

func validTrustedSubjectIssuer(value string) bool {
	if value == "" || len(value) > maximumSubjectIssuerBytes || strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == ""
}

func validTrustedSubjectAudience(value string) bool {
	if value == "" || len(value) > maximumSubjectAudienceBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character == '\n' || character == '\r' || character == '\t' {
			return false
		}
	}
	return true
}

func validSubjectClaimPolicy(policy SubjectClaimPolicy) bool {
	return policy.SubjectClaim == SubjectClaimSub &&
		policy.MaxSubjectBytes >= 1 && policy.MaxSubjectBytes <= DefaultSubjectClaimMaxBytes &&
		policy.MaxTokenTTLSeconds >= minimumSubjectTokenMaxTTLSeconds &&
		policy.MaxTokenTTLSeconds <= maximumSubjectTokenMaxTTLSeconds
}

func validSubjectClaimValue(value string, maxBytes int) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) ||
		len(value) > maxBytes {
		return false
	}
	for _, character := range value {
		if character == 0 || character == '\n' || character == '\r' {
			return false
		}
	}
	return true
}

func validSubjectTokenID(value string) bool {
	if value == "" || len(value) < minimumClientAssertionJTIBytes ||
		len(value) > maximumClientAssertionJTIBytes || strings.TrimSpace(value) != value {
		return false
	}
	return true
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func MarshalSubjectClaimPolicy(policy SubjectClaimPolicy) ([]byte, error) {
	if !validSubjectClaimPolicy(policy) {
		return nil, ErrTrustedSubjectIssuerInvalid
	}
	return json.Marshal(SubjectClaimPolicy{
		SubjectClaim:       policy.SubjectClaim,
		RequireJTI:         policy.RequireJTI,
		MaxSubjectBytes:    policy.MaxSubjectBytes,
		MaxTokenTTLSeconds: policy.MaxTokenTTLSeconds,
	})
}

func ParseSubjectClaimPolicy(raw []byte) (SubjectClaimPolicy, error) {
	if len(raw) == 0 {
		return SubjectClaimPolicy{}, ErrTrustedSubjectIssuerInvalid
	}
	var policy SubjectClaimPolicy
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil || !validSubjectClaimPolicy(policy) {
		return SubjectClaimPolicy{}, ErrTrustedSubjectIssuerInvalid
	}
	return policy, nil
}

func CanonicalSubjectAlgorithms(values []string) ([]string, error) {
	allowed := allowedAlgorithmSet(values)
	if len(allowed) == 0 {
		return nil, ErrTrustedSubjectIssuerInvalid
	}
	// Stable order for persistence and exact comparisons.
	canonical := make([]string, 0, len(allowed))
	for _, algorithm := range PrivateKeyJWTAlgorithms() {
		if _, ok := allowed[algorithm]; ok {
			canonical = append(canonical, algorithm)
		}
	}
	if len(canonical) != len(allowed) {
		return nil, ErrTrustedSubjectIssuerInvalid
	}
	return canonical, nil
}
