package agentaccessauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	clientSecretPrefix            = "awsk_live_"
	publicClientIDPrefix          = "awcl_"
	clientSecretRandomBytes       = 32
	clientSecretRandomEncodedSize = 43
	maximumBasicAuthorizationSize = 2048

	FailureMalformedBasic     = "MALFORMED_BASIC_AUTHENTICATION"
	FailureMalformedSecret    = "MALFORMED_CLIENT_SECRET"
	FailureUnknownCredential  = "UNKNOWN_CREDENTIAL"
	FailureCredentialRejected = "CREDENTIAL_REJECTED"
)

var (
	ErrInvalidClient                   = errors.New("invalid_client")
	ErrClientAuthenticationUnavailable = errors.New("client authentication unavailable")
	ErrClientSecretCredentialNotFound  = errors.New("Client Secret credential not found")
	ErrClientAuthenticationLimited     = errors.New("Client authentication attempt is rate limited")
)

type ClientSecretCredential struct {
	WorkspaceID              string
	ClientID                 string
	PublicClientID           string
	ServicePrincipalID       string
	ServicePrincipalVersion  int64
	ClientActive             bool
	ServicePrincipalActive   bool
	SecretAuthentication     bool
	TokenTTLSeconds          int
	CredentialID             string
	CredentialIsClientSecret bool
	SecretHash               []byte
	ValidFrom                time.Time
	ExpiresAt                *time.Time
	RevokedAt                *time.Time
}

type ClientSecretCredentialStore interface {
	LookupClientSecretCredential(context.Context, string) (ClientSecretCredential, error)
	MarkClientSecretAuthenticated(context.Context, string, string, time.Time) error
}

type ClientAuthenticationAttempt struct {
	PublicClientID string
	SourceIP       string
}

type ClientAuthenticationAttemptLimiter interface {
	AllowClientAuthentication(context.Context, ClientAuthenticationAttempt) error
	RecordClientAuthenticationFailure(context.Context, ClientAuthenticationAttempt) error
	RecordClientAuthenticationSuccess(context.Context, ClientAuthenticationAttempt) error
}

type ClientSecretAuthenticationFailure struct {
	WorkspaceID string
	ClientID    string
	ErrorCode   string
	SourceIP    string
	UserAgent   string
}

type ClientSecretAuthenticationAudit interface {
	RecordClientSecretAuthenticationFailure(context.Context, ClientSecretAuthenticationFailure) error
}

type AuthenticatedClient struct {
	WorkspaceID             string
	ClientID                string
	PublicClientID          string
	ServicePrincipalID      string
	ServicePrincipalVersion int64
	CredentialID            string
	TokenTTLSeconds         int
}

type ClientSecretAuthenticationRequest struct {
	Authorization string
	SourceIP      string
	UserAgent     string
}

type ClientSecretAuthenticator struct {
	store   ClientSecretCredentialStore
	pepper  []byte
	now     func() time.Time
	limiter ClientAuthenticationAttemptLimiter
	audit   ClientSecretAuthenticationAudit
}

type ClientSecretAuthenticatorOption func(*ClientSecretAuthenticator) error

func NewClientSecretAuthenticator(
	store ClientSecretCredentialStore,
	pepper []byte,
	options ...ClientSecretAuthenticatorOption,
) (*ClientSecretAuthenticator, error) {
	if store == nil || len(pepper) < 32 {
		return nil, errors.New("Client Secret credential store and a 32-byte pepper are required")
	}
	authenticator := &ClientSecretAuthenticator{
		store: store, pepper: append([]byte(nil), pepper...),
		now:     func() time.Time { return time.Now().UTC() },
		limiter: allowClientAuthenticationLimiter{},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("Client Secret authenticator option is required")
		}
		if err := option(authenticator); err != nil {
			return nil, err
		}
	}
	return authenticator, nil
}

func WithClientSecretAuthenticationLimiter(
	limiter ClientAuthenticationAttemptLimiter,
) ClientSecretAuthenticatorOption {
	return func(authenticator *ClientSecretAuthenticator) error {
		if limiter == nil {
			return errors.New("Client Secret authentication limiter is required")
		}
		authenticator.limiter = limiter
		return nil
	}
}

func WithClientSecretAuthenticationAudit(
	audit ClientSecretAuthenticationAudit,
) ClientSecretAuthenticatorOption {
	return func(authenticator *ClientSecretAuthenticator) error {
		if audit == nil {
			return errors.New("Client Secret authentication audit is required")
		}
		authenticator.audit = audit
		return nil
	}
}

// AuthenticateBasic is intentionally an operation for the future Token
// Endpoint, not an HTTP middleware. Client Secrets are never accepted as AAP
// data-plane bearer credentials.
func (authenticator *ClientSecretAuthenticator) AuthenticateBasic(
	ctx context.Context,
	request ClientSecretAuthenticationRequest,
) (AuthenticatedClient, error) {
	if authenticator == nil || authenticator.store == nil || ctx == nil {
		return AuthenticatedClient{}, ErrInvalidClient
	}
	publicClientID, presentedSecret, err := parseClientSecretBasic(request.Authorization)
	if err != nil {
		attempt := ClientAuthenticationAttempt{SourceIP: strings.TrimSpace(request.SourceIP)}
		if authenticator.limiter.AllowClientAuthentication(ctx, attempt) == nil {
			_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
			authenticator.recordFailure(ctx, ClientSecretCredential{}, request, FailureMalformedBasic)
		}
		return AuthenticatedClient{}, ErrInvalidClient
	}
	attempt := ClientAuthenticationAttempt{
		PublicClientID: publicClientID, SourceIP: strings.TrimSpace(request.SourceIP),
	}
	if err := authenticator.limiter.AllowClientAuthentication(ctx, attempt); err != nil {
		// The limiter already accounted for the preceding failures. Suppress an
		// unbounded audit write for every blocked retry.
		return AuthenticatedClient{}, ErrInvalidClient
	}
	credentialID, err := parsePresentedClientSecret(presentedSecret)
	if err != nil {
		_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
		authenticator.recordFailure(ctx, ClientSecretCredential{}, request, FailureMalformedSecret)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	record, lookupErr := authenticator.store.LookupClientSecretCredential(ctx, credentialID)
	presentedHash := authenticator.hashSecret(presentedSecret)
	if lookupErr != nil {
		_ = hmac.Equal(presentedHash, make([]byte, sha256.Size))
		if !errors.Is(lookupErr, ErrClientSecretCredentialNotFound) {
			return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
		}
		_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
		authenticator.recordFailure(ctx, ClientSecretCredential{}, request, FailureUnknownCredential)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	now := authenticator.now()
	secretMatches := hmac.Equal(presentedHash, record.SecretHash)
	clientMatches := constantTimeStringEqual(publicClientID, record.PublicClientID)
	validState := record.ClientActive && record.ServicePrincipalActive && record.SecretAuthentication &&
		record.CredentialIsClientSecret && record.ServicePrincipalVersion >= 1 && record.TokenTTLSeconds > 0 &&
		len(record.SecretHash) == sha256.Size && !record.ValidFrom.IsZero() && !now.Before(record.ValidFrom) &&
		record.RevokedAt == nil && (record.ExpiresAt == nil || now.Before(*record.ExpiresAt))
	if !secretMatches || !clientMatches || !validState {
		_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
		authenticator.recordFailure(ctx, record, request, FailureCredentialRejected)
		return AuthenticatedClient{}, ErrInvalidClient
	}
	if err := authenticator.store.MarkClientSecretAuthenticated(
		ctx, record.CredentialID, record.PublicClientID, now,
	); err != nil {
		if errors.Is(err, ErrClientSecretCredentialNotFound) {
			_ = authenticator.limiter.RecordClientAuthenticationFailure(ctx, attempt)
			authenticator.recordFailure(ctx, record, request, FailureCredentialRejected)
			return AuthenticatedClient{}, ErrInvalidClient
		}
		return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
	}
	if err := authenticator.limiter.RecordClientAuthenticationSuccess(ctx, attempt); err != nil {
		return AuthenticatedClient{}, ErrClientAuthenticationUnavailable
	}
	return AuthenticatedClient{
		WorkspaceID: record.WorkspaceID, ClientID: record.ClientID,
		PublicClientID: record.PublicClientID, ServicePrincipalID: record.ServicePrincipalID,
		ServicePrincipalVersion: record.ServicePrincipalVersion,
		CredentialID:            record.CredentialID, TokenTTLSeconds: record.TokenTTLSeconds,
	}, nil
}

func (authenticator *ClientSecretAuthenticator) hashSecret(value string) []byte {
	mac := hmac.New(sha256.New, authenticator.pepper)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (authenticator *ClientSecretAuthenticator) recordFailure(
	ctx context.Context,
	record ClientSecretCredential,
	request ClientSecretAuthenticationRequest,
	code string,
) {
	if authenticator.audit == nil {
		return
	}
	_ = authenticator.audit.RecordClientSecretAuthenticationFailure(ctx, ClientSecretAuthenticationFailure{
		WorkspaceID: record.WorkspaceID, ClientID: record.ClientID, ErrorCode: code,
		SourceIP: strings.TrimSpace(request.SourceIP), UserAgent: strings.TrimSpace(request.UserAgent),
	})
}

func parseClientSecretBasic(value string) (string, string, error) {
	if value == "" || len(value) > maximumBasicAuthorizationSize {
		return "", "", ErrInvalidClient
	}
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", "", ErrInvalidClient
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(decoded) == 0 || len(decoded) > maximumBasicAuthorizationSize {
		return "", "", ErrInvalidClient
	}
	separator := strings.IndexByte(string(decoded), ':')
	if separator <= 0 || separator == len(decoded)-1 {
		return "", "", ErrInvalidClient
	}
	publicClientID, err := url.QueryUnescape(string(decoded[:separator]))
	if err != nil || !validPublicClientID(publicClientID) {
		return "", "", ErrInvalidClient
	}
	secret, err := url.QueryUnescape(string(decoded[separator+1:]))
	if err != nil || secret == "" {
		return "", "", ErrInvalidClient
	}
	return publicClientID, secret, nil
}

func parsePresentedClientSecret(value string) (string, error) {
	wantLength := len(clientSecretPrefix) + 36 + 1 + clientSecretRandomEncodedSize
	if len(value) != wantLength || !strings.HasPrefix(value, clientSecretPrefix) {
		return "", ErrInvalidClient
	}
	credentialID := value[len(clientSecretPrefix) : len(clientSecretPrefix)+36]
	if value[len(clientSecretPrefix)+36] != '_' {
		return "", ErrInvalidClient
	}
	parsed, err := uuid.Parse(credentialID)
	if err != nil || parsed.String() != credentialID {
		return "", ErrInvalidClient
	}
	randomPart := value[len(clientSecretPrefix)+37:]
	if strings.Contains(randomPart, "_") {
		return "", ErrInvalidClient
	}
	raw, err := base64.RawURLEncoding.DecodeString(randomPart)
	if err != nil || len(raw) != clientSecretRandomBytes {
		return "", ErrInvalidClient
	}
	return credentialID, nil
}

func validPublicClientID(value string) bool {
	if !strings.HasPrefix(value, publicClientIDPrefix) || len(value) != len(publicClientIDPrefix)+43 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[len(publicClientIDPrefix):])
	return err == nil && len(raw) == 32
}

func constantTimeStringEqual(left, right string) bool {
	leftHash, rightHash := sha256.Sum256([]byte(left)), sha256.Sum256([]byte(right))
	return hmac.Equal(leftHash[:], rightHash[:])
}

type allowClientAuthenticationLimiter struct{}

func (allowClientAuthenticationLimiter) AllowClientAuthentication(context.Context, ClientAuthenticationAttempt) error {
	return nil
}

func (allowClientAuthenticationLimiter) RecordClientAuthenticationFailure(context.Context, ClientAuthenticationAttempt) error {
	return nil
}

func (allowClientAuthenticationLimiter) RecordClientAuthenticationSuccess(context.Context, ClientAuthenticationAttempt) error {
	return nil
}
