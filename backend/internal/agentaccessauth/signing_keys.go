package agentaccessauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AAPAccessTokenType        = "at+jwt"
	AAPSigningAlgorithm       = "EdDSA"
	DefaultMaxAccessTokenTTL  = 15 * time.Minute
	DefaultTokenClockSkew     = 5 * time.Second
	maximumSigningKeyIDLength = 64
)

var ErrSigningKeyNotFound = errors.New("AAP signing key not found")

// JSONWebKey is the public-only OKP representation used by the AAP JWKS.
// It intentionally has no private-key field.
type JSONWebKey struct {
	KeyType   string `json:"kty"`
	Curve     string `json:"crv"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	X         string `json:"x"`
}

type JSONWebKeySet struct {
	Keys []JSONWebKey `json:"keys"`
}

type SigningKeyProvider interface {
	ActiveSigningKey(time.Time) (SigningKey, error)
	VerificationKey(string, time.Time) (ed25519.PublicKey, error)
	PublicJWKS(time.Time) (JSONWebKeySet, error)
	MaximumTokenTTL() time.Duration
}

// SigningKey keeps private material behind behavior instead of exposing it as a
// serializable field. AAP access tokens can therefore only be signed with the
// fixed algorithm and protected header policy below.
type SigningKey struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

func (key SigningKey) KeyID() string { return key.keyID }

func (key SigningKey) Algorithm() string { return AAPSigningAlgorithm }

func (key SigningKey) PublicKey() ed25519.PublicKey {
	if len(key.privateKey) != ed25519.PrivateKeySize {
		return nil
	}
	publicKey, _ := key.privateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), publicKey...)
}

func (key SigningKey) SignAccessToken(claims jwt.Claims) (string, error) {
	if claims == nil || !validSigningKeyID(key.keyID) || len(key.privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("AAP access token signing key is unavailable")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = AAPAccessTokenType
	token.Header["kid"] = key.keyID
	value, err := token.SignedString(key.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign AAP access token: %w", err)
	}
	return value, nil
}

type PublishedVerificationKey struct {
	KeyID        string
	PublicKey    ed25519.PublicKey
	PublishUntil time.Time
}

type signingKeyEntry struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

type publishedKeyEntry struct {
	publicKey    ed25519.PublicKey
	publishUntil time.Time
}

// RotatingSigningKeyProvider is concurrency-safe and keeps retired public keys
// for the complete maximum token lifetime plus verifier clock skew.
type RotatingSigningKeyProvider struct {
	mu              sync.RWMutex
	active          signingKeyEntry
	published       map[string]publishedKeyEntry
	retentionWindow time.Duration
}

func NewRotatingSigningKeyProvider(
	activeKeyID string,
	activePrivateKey ed25519.PrivateKey,
	maxTokenTTL time.Duration,
	published ...PublishedVerificationKey,
) (*RotatingSigningKeyProvider, error) {
	if !validSigningKeyID(activeKeyID) {
		return nil, errors.New("valid AAP active signing key ID is required")
	}
	if len(activePrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("AAP active signing key must be an Ed25519 private key")
	}
	if maxTokenTTL <= 0 || maxTokenTTL > DefaultMaxAccessTokenTTL {
		return nil, errors.New("AAP maximum access token TTL must be positive and at most 15 minutes")
	}
	provider := &RotatingSigningKeyProvider{
		active: signingKeyEntry{
			keyID: activeKeyID, privateKey: append(ed25519.PrivateKey(nil), activePrivateKey...),
		},
		published:       make(map[string]publishedKeyEntry, len(published)),
		retentionWindow: maxTokenTTL + DefaultTokenClockSkew,
	}
	for _, value := range published {
		if err := provider.addPublishedKey(value); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

func (provider *RotatingSigningKeyProvider) ActiveSigningKey(_ time.Time) (SigningKey, error) {
	if provider == nil {
		return SigningKey{}, ErrSigningKeyNotFound
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.active.keyID == "" || len(provider.active.privateKey) != ed25519.PrivateKeySize {
		return SigningKey{}, ErrSigningKeyNotFound
	}
	return SigningKey{
		keyID:      provider.active.keyID,
		privateKey: append(ed25519.PrivateKey(nil), provider.active.privateKey...),
	}, nil
}

func (provider *RotatingSigningKeyProvider) MaximumTokenTTL() time.Duration {
	if provider == nil || provider.retentionWindow <= DefaultTokenClockSkew {
		return 0
	}
	return provider.retentionWindow - DefaultTokenClockSkew
}

func (provider *RotatingSigningKeyProvider) VerificationKey(keyID string, now time.Time) (ed25519.PublicKey, error) {
	if provider == nil || now.IsZero() || !validSigningKeyID(keyID) {
		return nil, ErrSigningKeyNotFound
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if keyID == provider.active.keyID {
		publicKey, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
		return append(ed25519.PublicKey(nil), publicKey...), nil
	}
	entry, exists := provider.published[keyID]
	if !exists || publicationExpired(entry.publishUntil, now) {
		return nil, ErrSigningKeyNotFound
	}
	return append(ed25519.PublicKey(nil), entry.publicKey...), nil
}

func (provider *RotatingSigningKeyProvider) PublicJWKS(now time.Time) (JSONWebKeySet, error) {
	if provider == nil || now.IsZero() {
		return JSONWebKeySet{}, errors.New("AAP JWKS time and provider are required")
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.active.keyID == "" || len(provider.active.privateKey) != ed25519.PrivateKeySize {
		return JSONWebKeySet{}, ErrSigningKeyNotFound
	}
	publicKeys := make(map[string]ed25519.PublicKey, len(provider.published)+1)
	activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	publicKeys[provider.active.keyID] = activePublic
	for keyID, entry := range provider.published {
		if !publicationExpired(entry.publishUntil, now) {
			publicKeys[keyID] = entry.publicKey
		}
	}
	keyIDs := make([]string, 0, len(publicKeys))
	for keyID := range publicKeys {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	result := JSONWebKeySet{Keys: make([]JSONWebKey, 0, len(keyIDs))}
	for _, keyID := range keyIDs {
		result.Keys = append(result.Keys, publicJWK(keyID, publicKeys[keyID]))
	}
	return result, nil
}

// Rotate switches the signing key atomically. The previous active public key
// remains published until every token it may have signed is invalid, including
// the fixed clock-skew allowance.
func (provider *RotatingSigningKeyProvider) Rotate(
	newKeyID string,
	newPrivateKey ed25519.PrivateKey,
	now time.Time,
) error {
	if provider == nil || now.IsZero() {
		return errors.New("AAP signing key provider and rotation time are required")
	}
	if !validSigningKeyID(newKeyID) || len(newPrivateKey) != ed25519.PrivateKeySize {
		return errors.New("valid Ed25519 AAP rotation key and key ID are required")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	newPublic, _ := newPrivateKey.Public().(ed25519.PublicKey)
	if existing, found := provider.published[newKeyID]; found &&
		!existing.publicKey.Equal(newPublic) {
		return errors.New("AAP rotation key ID conflicts with a different published key")
	}
	if newKeyID == provider.active.keyID {
		activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
		if !activePublic.Equal(newPublic) {
			return errors.New("AAP active key ID cannot be reused for different key material")
		}
		return nil
	}
	oldPublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	provider.published[provider.active.keyID] = publishedKeyEntry{
		publicKey:    append(ed25519.PublicKey(nil), oldPublic...),
		publishUntil: now.Add(provider.retentionWindow),
	}
	delete(provider.published, newKeyID)
	provider.active = signingKeyEntry{
		keyID: newKeyID, privateKey: append(ed25519.PrivateKey(nil), newPrivateKey...),
	}
	return nil
}

func (provider *RotatingSigningKeyProvider) addPublishedKey(value PublishedVerificationKey) error {
	if !validSigningKeyID(value.KeyID) || len(value.PublicKey) != ed25519.PublicKeySize {
		return errors.New("valid AAP published Ed25519 key and key ID are required")
	}
	activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	if value.KeyID == provider.active.keyID {
		if !activePublic.Equal(value.PublicKey) {
			return errors.New("AAP active key ID conflicts with a published key")
		}
		return nil
	}
	if _, duplicate := provider.published[value.KeyID]; duplicate {
		return errors.New("duplicate AAP published key ID")
	}
	provider.published[value.KeyID] = publishedKeyEntry{
		publicKey:    append(ed25519.PublicKey(nil), value.PublicKey...),
		publishUntil: value.PublishUntil.UTC(),
	}
	return nil
}

func publicJWK(keyID string, publicKey ed25519.PublicKey) JSONWebKey {
	return JSONWebKey{
		KeyType: "OKP", Curve: "Ed25519", Use: "sig", Algorithm: AAPSigningAlgorithm,
		KeyID: keyID, X: base64.RawURLEncoding.EncodeToString(publicKey),
	}
}

func publicationExpired(until, now time.Time) bool {
	return !until.IsZero() && !now.Before(until)
}

func validSigningKeyID(value string) bool {
	if value == "" || len(value) > maximumSigningKeyIDLength {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' &&
			character != '.' {
			return false
		}
	}
	return true
}
