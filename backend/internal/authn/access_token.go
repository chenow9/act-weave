package authn

import (
	"errors"
	"fmt"
	"time"

	"actweave/backend/internal/identity"
	"github.com/golang-jwt/jwt/v5"
)

const DefaultAccessTokenTTL = 15 * time.Minute

type AccessTokenClaims struct {
	SessionID    string `json:"sid"`
	Username     string `json:"username"`
	PlatformRole string `json:"platformRole"`
	jwt.RegisteredClaims
}

type AccessToken struct {
	Value     string `json:"-"`
	ExpiresAt time.Time
}

type AccessTokenManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewAccessTokenManager(secret string, issuer string, ttl time.Duration) (*AccessTokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("access token HMAC secret must contain at least 32 bytes")
	}
	if issuer == "" {
		return nil, errors.New("access token issuer is required")
	}
	if ttl == 0 {
		ttl = DefaultAccessTokenTTL
	}
	if ttl <= 0 || ttl > time.Hour {
		return nil, errors.New("access token TTL must be positive and at most one hour")
	}
	return &AccessTokenManager{secret: []byte(secret), issuer: issuer, ttl: ttl}, nil
}

func (m *AccessTokenManager) Generate(
	user identity.User,
	sessionID string,
	now time.Time,
) (AccessToken, error) {
	if user.ID == "" || sessionID == "" || now.IsZero() {
		return AccessToken{}, errors.New("access token subject, session, and time are required")
	}
	expiresAt := now.Add(m.ttl)
	claims := AccessTokenClaims{
		SessionID:    sessionID,
		Username:     user.Username,
		PlatformRole: string(user.PlatformRole),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return AccessToken{}, fmt.Errorf("sign access token: %w", err)
	}
	return AccessToken{Value: value, ExpiresAt: expiresAt}, nil
}

func (m *AccessTokenManager) Parse(value string, now time.Time) (AccessTokenClaims, error) {
	claims := AccessTokenClaims{}
	token, err := jwt.ParseWithClaims(
		value,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected access token signing method")
			}
			return m.secret, nil
		},
		jwt.WithIssuer(m.issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return AccessTokenClaims{}, fmt.Errorf("parse access token: %w", err)
	}
	if !token.Valid || claims.Subject == "" || claims.SessionID == "" {
		return AccessTokenClaims{}, errors.New("invalid access token claims")
	}
	return claims, nil
}
