package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

const (
	refreshTokenPrefix = "rt1"
	refreshSecretBytes = 32
)

var ErrInvalidRefreshToken = errors.New("invalid refresh token")

// IssuedRefreshToken separates the value sent to the client from the hash
// persisted in auth_sessions. Plaintext must never be logged or stored.
type IssuedRefreshToken struct {
	SessionID string
	Plaintext string `json:"-"`
	Hash      string `json:"-"`
}

type RefreshTokenManager struct {
	random io.Reader
}

func NewRefreshTokenManager() *RefreshTokenManager {
	return &RefreshTokenManager{random: rand.Reader}
}

func newRefreshTokenManager(random io.Reader) (*RefreshTokenManager, error) {
	if random == nil {
		return nil, errors.New("refresh token random source is required")
	}
	return &RefreshTokenManager{random: random}, nil
}

// Issue creates an application-side UUIDv7 session ID and a 256-bit opaque
// secret. Only Hash is suitable for persistence.
func (m *RefreshTokenManager) Issue() (IssuedRefreshToken, error) {
	sessionID, err := uuid.NewV7()
	if err != nil {
		return IssuedRefreshToken{}, fmt.Errorf("generate UUIDv7 session id: %w", err)
	}
	return m.issueForSession(sessionID)
}

// Rotate issues a new secret for an existing UUIDv7 session. The session ID is
// stable across rotation so the token continues to address the same row.
func (m *RefreshTokenManager) Rotate(sessionID string) (IssuedRefreshToken, error) {
	parsed, err := uuid.Parse(sessionID)
	if err != nil || parsed.Version() != 7 {
		return IssuedRefreshToken{}, ErrInvalidRefreshToken
	}
	return m.issueForSession(parsed)
}

func (m *RefreshTokenManager) issueForSession(sessionID uuid.UUID) (IssuedRefreshToken, error) {
	secret := make([]byte, refreshSecretBytes)
	if _, err := io.ReadFull(m.random, secret); err != nil {
		return IssuedRefreshToken{}, fmt.Errorf("generate refresh token secret: %w", err)
	}
	plaintext := strings.Join([]string{
		refreshTokenPrefix,
		sessionID.String(),
		base64.RawURLEncoding.EncodeToString(secret),
	}, ".")
	return IssuedRefreshToken{
		SessionID: sessionID.String(),
		Plaintext: plaintext,
		Hash:      HashRefreshToken(plaintext),
	}, nil
}

// Parse validates the opaque token structure and returns the public session ID
// plus a one-way hash suitable for repository validation.
func (m *RefreshTokenManager) Parse(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != refreshTokenPrefix {
		return "", "", ErrInvalidRefreshToken
	}
	sessionID, err := uuid.Parse(parts[1])
	if err != nil || sessionID.Version() != 7 {
		return "", "", ErrInvalidRefreshToken
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(secret) != refreshSecretBytes {
		return "", "", ErrInvalidRefreshToken
	}
	return sessionID.String(), HashRefreshToken(token), nil
}

func HashRefreshToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
