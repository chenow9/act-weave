package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const clientSecretTestCredentialID = "a18f1f2e-7b5a-7c3d-8e9f-123456789001"

func TestClientSecretAuthenticationAcceptsValidBasicCredentialAndMarksUsage(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	pepper := bytes.Repeat([]byte{0x5a}, 32)
	publicClientID := clientSecretTestPublicID(1)
	secret := clientSecretTestSecret(clientSecretTestCredentialID, 2)
	store := &clientSecretTestStore{record: clientSecretTestRecord(now, publicClientID, secret, pepper)}
	audit := &clientSecretTestAudit{}
	authenticator, err := NewClientSecretAuthenticator(store, pepper, WithClientSecretAuthenticationAudit(audit))
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now }
	result, err := authenticator.AuthenticateBasic(context.Background(), ClientSecretAuthenticationRequest{
		Authorization: clientSecretTestBasic(publicClientID, secret),
		SourceIP:      "203.0.113.8", UserAgent: "partner-app/1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != store.record.WorkspaceID || result.ClientID != store.record.ClientID ||
		result.PublicClientID != publicClientID || result.CredentialID != clientSecretTestCredentialID ||
		result.ServicePrincipalVersion != 7 || result.TokenTTLSeconds != 600 {
		t.Fatalf("unexpected authenticated Client: %+v", result)
	}
	if store.markCalls != 1 || !store.markedAt.Equal(now) || len(audit.failures) != 0 {
		t.Fatalf("usage/audit mismatch marks=%d at=%s failures=%+v", store.markCalls, store.markedAt, audit.failures)
	}
}

func TestClientSecretAuthenticationRejectsAllCredentialStateFailuresAsInvalidClient(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	pepper := bytes.Repeat([]byte{0x6a}, 32)
	publicClientID := clientSecretTestPublicID(3)
	secret := clientSecretTestSecret(clientSecretTestCredentialID, 4)
	base := clientSecretTestRecord(now, publicClientID, secret, pepper)
	expired := now
	revoked := now.Add(-time.Minute)
	tests := map[string]func(*ClientSecretCredential, *string, *string){
		"wrong secret": func(_ *ClientSecretCredential, _ *string, presented *string) {
			*presented = clientSecretTestSecret(clientSecretTestCredentialID, 5)
		},
		"wrong public Client ID": func(_ *ClientSecretCredential, clientID *string, _ *string) {
			*clientID = clientSecretTestPublicID(6)
		},
		"disabled Client":       func(value *ClientSecretCredential, _ *string, _ *string) { value.ClientActive = false },
		"disabled Principal":    func(value *ClientSecretCredential, _ *string, _ *string) { value.ServicePrincipalActive = false },
		"wrong auth method":     func(value *ClientSecretCredential, _ *string, _ *string) { value.SecretAuthentication = false },
		"wrong credential type": func(value *ClientSecretCredential, _ *string, _ *string) { value.CredentialIsClientSecret = false },
		"not yet valid":         func(value *ClientSecretCredential, _ *string, _ *string) { value.ValidFrom = now.Add(time.Second) },
		"expired":               func(value *ClientSecretCredential, _ *string, _ *string) { value.ExpiresAt = &expired },
		"revoked":               func(value *ClientSecretCredential, _ *string, _ *string) { value.RevokedAt = &revoked },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			record := base
			record.SecretHash = append([]byte(nil), base.SecretHash...)
			clientID, presented := publicClientID, secret
			mutate(&record, &clientID, &presented)
			store := &clientSecretTestStore{record: record}
			audit := &clientSecretTestAudit{}
			authenticator, err := NewClientSecretAuthenticator(store, pepper, WithClientSecretAuthenticationAudit(audit))
			if err != nil {
				t.Fatal(err)
			}
			authenticator.now = func() time.Time { return now }
			_, err = authenticator.AuthenticateBasic(context.Background(), ClientSecretAuthenticationRequest{
				Authorization: clientSecretTestBasic(clientID, presented), SourceIP: "203.0.113.9",
			})
			if !errors.Is(err, ErrInvalidClient) || store.markCalls != 0 || len(audit.failures) != 1 ||
				audit.failures[0].ErrorCode != FailureCredentialRejected {
				t.Fatalf("failure err=%v marks=%d audit=%+v", err, store.markCalls, audit.failures)
			}
		})
	}
}

func TestClientSecretAuthenticationRejectsMalformedAndUnknownValuesWithoutLeakingThem(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	pepper := bytes.Repeat([]byte{0x7a}, 32)
	publicClientID := clientSecretTestPublicID(7)
	validSecret := clientSecretTestSecret(clientSecretTestCredentialID, 8)
	unknownSecret := clientSecretTestSecret("a18f1f2e-7b5a-7c3d-8e9f-123456789099", 9)
	tests := []struct {
		name, authorization, code string
		lookupErr                 error
	}{
		{name: "not Basic", authorization: "Bearer " + validSecret, code: FailureMalformedBasic},
		{name: "bad base64", authorization: "Basic !!!", code: FailureMalformedBasic},
		{name: "bad secret format", authorization: clientSecretTestBasic(publicClientID, "not-a-secret"), code: FailureMalformedSecret},
		{name: "unknown credential", authorization: clientSecretTestBasic(publicClientID, unknownSecret),
			code: FailureUnknownCredential, lookupErr: ErrClientSecretCredentialNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &clientSecretTestStore{lookupErr: test.lookupErr}
			audit := &clientSecretTestAudit{}
			authenticator, err := NewClientSecretAuthenticator(store, pepper, WithClientSecretAuthenticationAudit(audit))
			if err != nil {
				t.Fatal(err)
			}
			authenticator.now = func() time.Time { return now }
			_, err = authenticator.AuthenticateBasic(context.Background(), ClientSecretAuthenticationRequest{
				Authorization: test.authorization, SourceIP: "203.0.113.10", UserAgent: "secret-test-agent",
			})
			if !errors.Is(err, ErrInvalidClient) || len(audit.failures) != 1 || audit.failures[0].ErrorCode != test.code {
				t.Fatalf("failure err=%v audit=%+v", err, audit.failures)
			}
			encoded, _ := json.Marshal(audit.failures)
			if bytes.Contains(encoded, []byte(validSecret)) || bytes.Contains(encoded, []byte(unknownSecret)) ||
				bytes.Contains(encoded, []byte(test.authorization)) {
				t.Fatalf("failure audit leaked presented authentication material: %s", encoded)
			}
		})
	}
}

func TestClientSecretAuthenticationRateLimiterBlocksAndRecovers(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	pepper := bytes.Repeat([]byte{0x8a}, 32)
	publicClientID := clientSecretTestPublicID(10)
	secret := clientSecretTestSecret(clientSecretTestCredentialID, 11)
	wrongSecret := clientSecretTestSecret(clientSecretTestCredentialID, 12)
	store := &clientSecretTestStore{record: clientSecretTestRecord(now, publicClientID, secret, pepper)}
	limiter, err := NewInMemoryClientAuthenticationLimiter(2, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	limiter.now = func() time.Time { return now }
	audit := &clientSecretTestAudit{}
	authenticator, err := NewClientSecretAuthenticator(store, pepper,
		WithClientSecretAuthenticationLimiter(limiter), WithClientSecretAuthenticationAudit(audit))
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now }
	request := ClientSecretAuthenticationRequest{
		Authorization: clientSecretTestBasic(publicClientID, wrongSecret), SourceIP: "203.0.113.11",
	}
	for index := 0; index < 2; index++ {
		if _, err := authenticator.AuthenticateBasic(context.Background(), request); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("failed attempt %d err=%v", index+1, err)
		}
	}
	request.Authorization = clientSecretTestBasic(publicClientID, secret)
	if _, err := authenticator.AuthenticateBasic(context.Background(), request); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("rate-limited valid credential err=%v", err)
	}
	if len(audit.failures) != 2 || store.markCalls != 0 {
		t.Fatalf("rate limit audit=%+v marks=%d", audit.failures, store.markCalls)
	}
	now = now.Add(time.Minute)
	if _, err := authenticator.AuthenticateBasic(context.Background(), request); err != nil || store.markCalls != 1 {
		t.Fatalf("credential did not recover after limit window: err=%v marks=%d", err, store.markCalls)
	}
}

func TestClientSecretAuthenticationDistinguishesStoreOutageFromInvalidCredential(t *testing.T) {
	pepper := bytes.Repeat([]byte{0x9a}, 32)
	publicClientID := clientSecretTestPublicID(13)
	secret := clientSecretTestSecret(clientSecretTestCredentialID, 14)
	authenticator, err := NewClientSecretAuthenticator(&clientSecretTestStore{lookupErr: errors.New("database unavailable")}, pepper)
	if err != nil {
		t.Fatal(err)
	}
	_, err = authenticator.AuthenticateBasic(context.Background(), ClientSecretAuthenticationRequest{
		Authorization: clientSecretTestBasic(publicClientID, secret),
	})
	if !errors.Is(err, ErrClientAuthenticationUnavailable) || errors.Is(err, ErrInvalidClient) {
		t.Fatalf("operational outage err=%v", err)
	}
}

func TestClientSecretAuthenticationFormatIsUnambiguous(t *testing.T) {
	valid := clientSecretTestSecret(clientSecretTestCredentialID, 15)
	if credentialID, err := parsePresentedClientSecret(valid); err != nil || credentialID != clientSecretTestCredentialID {
		t.Fatalf("valid Secret parsed id=%q err=%v", credentialID, err)
	}
	randomPart := valid[len(clientSecretPrefix)+37:]
	tests := map[string]string{
		"wrong environment":   "awsk_test_" + clientSecretTestCredentialID + "_" + randomPart,
		"uppercase UUID":      clientSecretPrefix + strings.ToUpper(clientSecretTestCredentialID) + "_" + randomPart,
		"ambiguous delimiter": clientSecretPrefix + clientSecretTestCredentialID + "_" + randomPart[:10] + "_" + randomPart[11:],
		"short entropy":       clientSecretPrefix + clientSecretTestCredentialID + "_" + randomPart[:len(randomPart)-1],
		"trailing data":       valid + "A",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if credentialID, err := parsePresentedClientSecret(value); err == nil || credentialID != "" {
				t.Fatalf("invalid Secret parsed id=%q err=%v", credentialID, err)
			}
		})
	}
}

type clientSecretTestStore struct {
	record    ClientSecretCredential
	lookupErr error
	markErr   error
	markCalls int
	markedAt  time.Time
}

func (store *clientSecretTestStore) LookupClientSecretCredential(
	context.Context,
	string,
) (ClientSecretCredential, error) {
	return store.record, store.lookupErr
}

func (store *clientSecretTestStore) MarkClientSecretAuthenticated(
	_ context.Context,
	_, _ string,
	at time.Time,
) error {
	store.markCalls++
	store.markedAt = at
	return store.markErr
}

type clientSecretTestAudit struct {
	failures []ClientSecretAuthenticationFailure
}

func (audit *clientSecretTestAudit) RecordClientSecretAuthenticationFailure(
	_ context.Context,
	failure ClientSecretAuthenticationFailure,
) error {
	audit.failures = append(audit.failures, failure)
	return nil
}

func clientSecretTestRecord(
	now time.Time,
	publicClientID, secret string,
	pepper []byte,
) ClientSecretCredential {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(secret))
	return ClientSecretCredential{
		WorkspaceID: "a18f1f2e-7b5a-7c3d-8e9f-123456789010",
		ClientID:    "a18f1f2e-7b5a-7c3d-8e9f-123456789011", PublicClientID: publicClientID,
		ServicePrincipalID:      "a18f1f2e-7b5a-7c3d-8e9f-123456789012",
		ServicePrincipalVersion: 7, ClientActive: true, ServicePrincipalActive: true,
		SecretAuthentication: true, TokenTTLSeconds: 600,
		CredentialID: clientSecretTestCredentialID, CredentialIsClientSecret: true,
		SecretHash: mac.Sum(nil), ValidFrom: now.Add(-time.Minute),
	}
}

func clientSecretTestPublicID(fill byte) string {
	return publicClientIDPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func clientSecretTestSecret(credentialID string, fill byte) string {
	randomPart := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
	if strings.Contains(randomPart, "_") {
		panic("test Secret random part must be unambiguous")
	}
	return clientSecretPrefix + credentialID + "_" + randomPart
}

func clientSecretTestBasic(publicClientID, secret string) string {
	value := url.QueryEscape(publicClientID) + ":" + url.QueryEscape(secret)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(value))
}
