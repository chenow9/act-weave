package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOAuthTokenExchangeIssuesDelegatedTokenWithActClaim(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	fixture := newTokenExchangeTestFixture(t, now)
	subject := "partner-user-42"
	subjectToken := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, subject, "subject-jti-exchange-0001",
		now, now.Add(30*time.Minute))

	issued, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID,
		RequestedScopes:  []string{"run:create", "event:read"},
		SubjectToken:     subjectToken,
		SubjectTokenType: SubjectTokenTypeJWT,
	})
	if err != nil || issued.AccessToken == "" || issued.TokenType != "Bearer" ||
		issued.IssuedTokenType != IssuedTokenTypeAccessToken || issued.ExpiresIn != 600 ||
		issued.Scope != "run:create event:read" {
		t.Fatalf("issued=%+v err=%v", issued, err)
	}
	if issued.Claims.Subject != fixture.subjects.lastSubjectID ||
		issued.Claims.Actor == nil ||
		issued.Claims.Actor.Subject != fixture.client.ServicePrincipalID ||
		issued.Claims.AuthorizedParty != fixture.client.PublicClientID ||
		issued.Claims.WorkspaceID != fixture.client.WorkspaceID ||
		issued.Claims.AgentID != fixture.agentID ||
		issued.Claims.SecurityVersion != fixture.client.ServicePrincipalVersion {
		t.Fatalf("claims=%+v subjects=%+v", issued.Claims, fixture.subjects)
	}
	// Privacy: response claims must not embed raw external subject or subject token.
	encoded, _ := json.Marshal(issued.Claims)
	if bytes.Contains(encoded, []byte(subject)) || bytes.Contains(encoded, []byte(subjectToken)) {
		t.Fatalf("claims leaked raw subject or subject token: %s", encoded)
	}
	// Verifier accepts delegated token with act.sub.
	principal, err := fixture.accessVerifier.VerifyAccessToken(context.Background(), issued.AccessToken)
	if err != nil || principal.PrincipalID != fixture.subjects.lastSubjectID ||
		principal.ServicePrincipalID != fixture.client.ServicePrincipalID {
		t.Fatalf("verified principal=%+v err=%v", principal, err)
	}
	// TTL capped by subject token remaining life.
	shortToken := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, "short-user", "subject-jti-exchange-0002",
		now, now.Add(8*time.Minute))
	shortIssued, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID,
		RequestedScopes: []string{"run:create"}, SubjectToken: shortToken,
		SubjectTokenType: SubjectTokenTypeJWT,
	})
	if err != nil || shortIssued.ExpiresIn != 8*60 {
		t.Fatalf("subject-capped TTL issued=%+v err=%v", shortIssued, err)
	}
}

func TestOAuthTokenExchangeRejectsForgeryReplayAndMissingTrust(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	fixture := newTokenExchangeTestFixture(t, now)
	validToken := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, "user-1", "subject-jti-exchange-0003",
		now, now.Add(20*time.Minute))

	// Replay.
	req := TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID,
		RequestedScopes: []string{"run:create"}, SubjectToken: validToken,
		SubjectTokenType: SubjectTokenTypeJWT,
	}
	if _, err := fixture.service.IssueTokenExchange(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.IssueTokenExchange(context.Background(), req); !errors.Is(err, ErrTokenExchangeReplay) {
		t.Fatalf("replay err=%v", err)
	}

	// Wrong issuer.
	wrongIssuer := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		"https://evil.example.test", fixture.audience, "user-2", "subject-jti-exchange-0004",
		now, now.Add(20*time.Minute))
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: wrongIssuer, SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeSubjectInvalid) {
		t.Fatalf("wrong issuer err=%v", err)
	}

	// Wrong audience.
	wrongAudience := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, "other-aud", "user-3", "subject-jti-exchange-0005",
		now, now.Add(20*time.Minute))
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: wrongAudience, SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeSubjectInvalid) {
		t.Fatalf("wrong audience err=%v", err)
	}

	// Expired subject token.
	expired := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, "user-4", "subject-jti-exchange-0006",
		now.Add(-2*time.Hour), now.Add(-time.Hour))
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: expired, SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeSubjectInvalid) {
		t.Fatalf("expired err=%v", err)
	}

	// Subject remaining life below minimum Access Token TTL.
	tooShort := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, "user-5", "subject-jti-exchange-0007",
		now, now.Add(2*time.Minute))
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: tooShort, SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeSubjectInvalid) {
		t.Fatalf("too-short remaining life err=%v", err)
	}

	// Disabled external subject.
	fixture.subjects.disabled = true
	disabledToken := trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
		fixture.issuer, fixture.audience, "user-disabled", "subject-jti-exchange-0008",
		now, now.Add(20*time.Minute))
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: disabledToken, SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeSubjectDenied) {
		t.Fatalf("disabled subject err=%v", err)
	}
	fixture.subjects.disabled = false

	// Missing trust config.
	fixture.trust.missing = true
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
			fixture.issuer, fixture.audience, "user-6", "subject-jti-exchange-0009",
			now, now.Add(20*time.Minute)),
		SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeTrustMissing) {
		t.Fatalf("missing trust err=%v", err)
	}

	// Invalid scope and target.
	fixture.trust.missing = false
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID,
		RequestedScopes: []string{"workspace:manage"}, SubjectToken: trustedSubjectTestToken(t,
			fixture.subjectKey, "subject-ed-1", fixture.issuer, fixture.audience, "user-7",
			"subject-jti-exchange-0010", now, now.Add(20*time.Minute)),
		SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeScopeInvalid) {
		t.Fatalf("invalid scope err=%v", err)
	}
	fixture.grants.missing = true
	if _, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: trustedSubjectTestToken(t, fixture.subjectKey, "subject-ed-1",
			fixture.issuer, fixture.audience, "user-8", "subject-jti-exchange-0011",
			now, now.Add(20*time.Minute)),
		SubjectTokenType: SubjectTokenTypeJWT,
	}); !errors.Is(err, ErrTokenExchangeTargetInvalid) {
		t.Fatalf("missing grant err=%v", err)
	}

	// Hash is keyed and stable.
	left := HashExternalSubject(bytes.Repeat([]byte{0x11}, 32), fixture.issuer, "same")
	right := HashExternalSubject(bytes.Repeat([]byte{0x11}, 32), fixture.issuer, "same")
	other := HashExternalSubject(bytes.Repeat([]byte{0x12}, 32), fixture.issuer, "same")
	if left != right || left == other {
		t.Fatalf("hash stability left=%x right=%x other=%x", left, right, other)
	}
}

type tokenExchangeTestFixture struct {
	service        *TokenExchangeService
	accessVerifier *AAPAccessTokenVerifier
	client         AuthenticatedClient
	agentID        string
	issuer         string
	audience       string
	subjectKey     ed25519.PrivateKey
	subjects       *tokenExchangeSubjectStore
	trust          *tokenExchangeTrustStore
	grants         *tokenExchangeGrantStore
}

func newTokenExchangeTestFixture(t *testing.T, now time.Time) *tokenExchangeTestFixture {
	t.Helper()
	subjectKey := signingTestPrivateKey(101)
	jwks, _ := privateKeyJWTTestEdJWKS(t, "subject-ed-1", subjectKey.Public().(ed25519.PublicKey))
	issuer := "https://idp.partner.example.test"
	audience := "actweave-partner-subject"
	config := TrustedSubjectIssuerConfig{
		Issuer: issuer, Audience: audience, InlineJWKS: jwks,
		Algorithms: []string{PrivateKeyJWTAlgorithmEdDSA}, ClaimPolicy: DefaultSubjectClaimPolicy(),
	}
	client := AuthenticatedClient{
		WorkspaceID: "f48f1f2e-7b5a-7c3d-8e9f-123456789001",
		ClientID:    "f48f1f2e-7b5a-7c3d-8e9f-123456789002",
		PublicClientID: clientSecretTestPublicID(41),
		ServicePrincipalID: "f48f1f2e-7b5a-7c3d-8e9f-123456789003",
		ServicePrincipalVersion: 7, CredentialID: "f48f1f2e-7b5a-7c3d-8e9f-123456789004",
		TokenTTLSeconds: 600,
	}
	agentID := "f48f1f2e-7b5a-7c3d-8e9f-123456789005"
	cache := privateKeyJWTTestCache(t, &privateKeyJWTTestFetcher{
		results: []RemoteJWKSFetchResult{{Body: jwks}},
	}, now)
	verifier, err := NewTrustedSubjectTokenVerifier(cache)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	accessKey := signingTestPrivateKey(102)
	keys, err := NewRotatingSigningKeyProvider("exchange-signing-key", accessKey, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	accessVerifier, err := NewAAPAccessTokenVerifier(keys,
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	accessVerifier.now = func() time.Time { return now }
	replays, err := NewInMemorySubjectTokenJTIStore(100)
	if err != nil {
		t.Fatal(err)
	}
	trust := &tokenExchangeTrustStore{config: config}
	subjects := &tokenExchangeSubjectStore{}
	grants := &tokenExchangeGrantStore{client: client, agentID: agentID}
	service, err := NewTokenExchangeService(
		trust, subjects, verifier, replays, grants, keys,
		bytes.Repeat([]byte{0x44}, 32),
		"https://api.example.test/api/agent-access/v1/oauth/token", 15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return &tokenExchangeTestFixture{
		service: service, accessVerifier: accessVerifier, client: client, agentID: agentID,
		issuer: issuer, audience: audience, subjectKey: subjectKey,
		subjects: subjects, trust: trust, grants: grants,
	}
}

type tokenExchangeTrustStore struct {
	config  TrustedSubjectIssuerConfig
	missing bool
}

func (store *tokenExchangeTrustStore) LookupTokenExchangeTrust(
	context.Context, AuthenticatedClient,
) (TokenExchangeTrust, error) {
	if store.missing {
		return TokenExchangeTrust{}, ErrTokenExchangeTrustMissing
	}
	return TokenExchangeTrust{Config: store.config}, nil
}

type tokenExchangeSubjectStore struct {
	lastSubjectID string
	disabled      bool
	calls         int
}

func (store *tokenExchangeSubjectStore) ResolveActiveExternalSubject(
	_ context.Context,
	_, _, _ string,
	_ [sha256.Size]byte,
	_ time.Time,
) (ExternalSubjectBinding, error) {
	store.calls++
	if store.disabled {
		return ExternalSubjectBinding{}, ErrTokenExchangeSubjectDenied
	}
	store.lastSubjectID = uuid.NewString()
	return ExternalSubjectBinding{SubjectID: store.lastSubjectID, Active: true}, nil
}

type tokenExchangeGrantStore struct {
	client  AuthenticatedClient
	agentID string
	missing bool
}

func (store *tokenExchangeGrantStore) ResolveClientCredentialsGrant(
	_ context.Context,
	client AuthenticatedClient,
	agentID string,
	_ time.Time,
) (ClientCredentialsGrant, error) {
	if store.missing || client.ClientID != store.client.ClientID || agentID != store.agentID {
		return ClientCredentialsGrant{}, ErrClientCredentialsGrantNotFound
	}
	return ClientCredentialsGrant{
		GrantID: "f48f1f2e-7b5a-7c3d-8e9f-123456789006",
		WorkspaceID: client.WorkspaceID, ClientID: client.ClientID,
		PublicClientID: client.PublicClientID, ServicePrincipalID: client.ServicePrincipalID,
		ServicePrincipalVersion: client.ServicePrincipalVersion, AgentID: agentID,
		GrantedScopes:  []string{"run:create", "event:read", "agent:read"},
		ClientTokenTTL: time.Duration(client.TokenTTLSeconds) * time.Second,
	}, nil
}

func TestOAuthTokenExchangeDoesNotLeakSubjectInErrors(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	fixture := newTokenExchangeTestFixture(t, now)
	raw := "leaked-subject-token-value"
	_, err := fixture.service.IssueTokenExchange(context.Background(), TokenExchangeRequest{
		Client: fixture.client, AgentID: fixture.agentID, RequestedScopes: []string{"run:create"},
		SubjectToken: raw, SubjectTokenType: SubjectTokenTypeJWT,
	})
	if err == nil {
		t.Fatal("expected invalid subject token")
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatalf("error leaked subject token: %v", err)
	}
}
