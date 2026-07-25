package outboundidentity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SubjectAssertionClaims are the approved outbound Subject Assertion claims
// (technical design §7.1). They never include third-party sub, AAP tokens, or
// inbound subject tokens.
type SubjectAssertionClaims struct {
	jwt.RegisteredClaims
	WorkspaceID  string   `json:"actweave_workspace_id"`
	ConnectionID string   `json:"actweave_connection_id"`
	RootScopeID  string   `json:"actweave_root_scope_id"`
	ActorType    string   `json:"actweave_actor_type"`
	ActorID      string   `json:"actweave_actor_id"`
	SubjectType  string   `json:"actweave_subject_type"`
	Scope        []string `json:"scope"`
}

// AssertionIssueRequest is the validated input for minting a Subject Assertion.
// Subject is always an internal ACTWEAVE UUID (USER or EXTERNAL_SUBJECT).
type AssertionIssueRequest struct {
	Issuer       string
	Audience     string
	WorkspaceID  string
	ConnectionID string
	RootScopeID  string
	ActorType    string
	ActorID      string
	SubjectType  SubjectType
	SubjectID    string
	Scopes       []string
	// TTL defaults to provider MaximumAssertionTTL, hard-capped at 60s.
	TTL time.Duration
}

// AssertionIssuer mints short-lived Subject Assertions with the outbound key domain.
type AssertionIssuer struct {
	keys   SigningKeyProvider
	clock  Clock
	issuer string
}

// NewAssertionIssuer constructs an issuer. issuer is the fixed platform iss claim
// (e.g. https://actweave.example/outbound) and must not be the AAP token issuer.
func NewAssertionIssuer(keys SigningKeyProvider, issuer string, clock Clock) (*AssertionIssuer, error) {
	issuer = strings.TrimSpace(issuer)
	if keys == nil || issuer == "" {
		return nil, errors.New("outbound assertion issuer and signing keys are required")
	}
	if clock == nil {
		clock = WallClock{}
	}
	return &AssertionIssuer{keys: keys, clock: clock, issuer: issuer}, nil
}

// Issuer returns the configured fixed iss value.
func (i *AssertionIssuer) Issuer() string {
	if i == nil {
		return ""
	}
	return i.issuer
}

// Issue signs a one-shot Subject Assertion. SYSTEM / nil Subject fail before sign.
// The returned string is ephemeral — never log, audit, cache, or persist it.
func (i *AssertionIssuer) Issue(req AssertionIssueRequest) (string, SubjectAssertionClaims, error) {
	if i == nil || i.keys == nil {
		return "", SubjectAssertionClaims{}, ErrIdentityConnectionNotReady
	}
	if err := validateAssertionRequest(req); err != nil {
		return "", SubjectAssertionClaims{}, err
	}
	// SYSTEM / empty subject never exchange.
	if !req.SubjectType.Valid() || strings.TrimSpace(req.SubjectID) == "" {
		return "", SubjectAssertionClaims{}, ErrSubjectRequired
	}
	if strings.EqualFold(req.ActorType, "SYSTEM") && req.SubjectType == "" {
		return "", SubjectAssertionClaims{}, ErrSubjectRequired
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = i.keys.MaximumAssertionTTL()
	}
	if ttl <= 0 || ttl > DefaultMaxAssertionTTL {
		ttl = DefaultMaxAssertionTTL
	}
	// Hard constraint: exp - iat <= 60s.
	if ttl > DefaultMaxAssertionTTL {
		ttl = DefaultMaxAssertionTTL
	}

	now := i.clock.Now().UTC()
	jti, err := randomJTI()
	if err != nil {
		return "", SubjectAssertionClaims{}, ErrBrokerUnavailable.Wrap(err)
	}
	scopes := append([]string(nil), req.Scopes...)

	claims := SubjectAssertionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   strings.TrimSpace(req.SubjectID),
			Audience:  jwt.ClaimStrings{strings.TrimSpace(req.Audience)},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-DefaultAssertionClockSkew)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        jti,
		},
		WorkspaceID:  strings.TrimSpace(req.WorkspaceID),
		ConnectionID: strings.TrimSpace(req.ConnectionID),
		RootScopeID:  strings.TrimSpace(req.RootScopeID),
		ActorType:    strings.TrimSpace(req.ActorType),
		ActorID:      strings.TrimSpace(req.ActorID),
		SubjectType:  string(req.SubjectType),
		Scope:        scopes,
	}
	if claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > DefaultMaxAssertionTTL {
		return "", SubjectAssertionClaims{}, ErrIdentityPolicyInvalid
	}

	key, err := i.keys.ActiveSigningKey(now)
	if err != nil {
		return "", SubjectAssertionClaims{}, ErrIdentityConnectionNotReady.Wrap(err)
	}
	token, err := key.SignJWT(SubjectAssertionType, claims)
	if err != nil {
		return "", SubjectAssertionClaims{}, ErrBrokerUnavailable.Wrap(err)
	}
	return token, claims, nil
}

func validateAssertionRequest(req AssertionIssueRequest) error {
	if strings.TrimSpace(req.Audience) == "" || !audiencePattern.MatchString(strings.TrimSpace(req.Audience)) {
		return ErrIdentityPolicyInvalid
	}
	if strings.TrimSpace(req.WorkspaceID) == "" ||
		strings.TrimSpace(req.ConnectionID) == "" ||
		strings.TrimSpace(req.RootScopeID) == "" {
		return ErrIdentityPolicyInvalid
	}
	if strings.TrimSpace(req.ActorType) == "" || strings.TrimSpace(req.ActorID) == "" {
		return ErrIdentityPolicyInvalid
	}
	// SYSTEM with no subject is rejected by Issue; still reject empty subject type.
	if req.SubjectType != "" && !req.SubjectType.Valid() {
		return ErrSubjectRequired
	}
	if len(req.Scopes) > 64 {
		return ErrIdentityScopeNotAllowed
	}
	for _, scope := range req.Scopes {
		if strings.TrimSpace(scope) == "" || !scopePattern.MatchString(strings.TrimSpace(scope)) {
			return ErrIdentityScopeNotAllowed
		}
	}
	return nil
}

func randomJTI() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
