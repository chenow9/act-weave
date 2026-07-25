package outboundidentity

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
	// OutboundSigningAlgorithm is fixed for Subject Assertions (T1=A). Not chosen
	// from JWT headers at verify time by this package's public JWKS consumers.
	OutboundSigningAlgorithm = "EdDSA"

	// SubjectAssertionType is the JWT typ header for outbound subject assertions.
	// Distinct from AAP access tokens (at+jwt).
	SubjectAssertionType = "actweave-subject-assertion+jwt"

	// DefaultMaxAssertionTTL is the hard upper bound for exp-iat.
	DefaultMaxAssertionTTL = 60 * time.Second

	// DefaultAssertionClockSkew is the nbf / verification skew allowance.
	DefaultAssertionClockSkew = 5 * time.Second

	// DefaultBrokerJWKSCacheWindow is the upper bound assumed for Broker-side JWKS
	// caching; previous public keys stay published at least this long after rotation
	// in addition to assertion TTL + skew.
	DefaultBrokerJWKSCacheWindow = 5 * time.Minute

	maximumOutboundKeyIDLength = 64
)

var ErrOutboundSigningKeyNotFound = errors.New("outbound signing key not found")

// JSONWebKey is the public-only OKP representation for outbound JWKS.
// Intentionally no private-key field (no "d", no seed).
type JSONWebKey struct {
	KeyType   string `json:"kty"`
	Curve     string `json:"crv"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	X         string `json:"x"`
}

// JSONWebKeySet is the outbound JWKS document.
type JSONWebKeySet struct {
	Keys []JSONWebKey `json:"keys"`
}

// SigningKeyProvider is the outbound platform assertion key surface.
// Completely separate from agentAccess.signingKeys trust domain.
type SigningKeyProvider interface {
	ActiveSigningKey(time.Time) (SigningKey, error)
	VerificationKey(string, time.Time) (ed25519.PublicKey, error)
	PublicJWKS(time.Time) (JSONWebKeySet, error)
	MaximumAssertionTTL() time.Duration
}

// SigningKey keeps private material behind Sign methods only.
type SigningKey struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

func (key SigningKey) KeyID() string { return key.keyID }

func (key SigningKey) Algorithm() string { return OutboundSigningAlgorithm }

func (key SigningKey) PublicKey() ed25519.PublicKey {
	if len(key.privateKey) != ed25519.PrivateKeySize {
		return nil
	}
	publicKey, _ := key.privateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), publicKey...)
}

// SignJWT signs claims with fixed EdDSA and typ/kid headers. Claims must already
// be validated by the caller (AssertionIssuer).
func (key SigningKey) SignJWT(typ string, claims jwt.Claims) (string, error) {
	if claims == nil || !validOutboundKeyID(key.keyID) ||
		len(key.privateKey) != ed25519.PrivateKeySize || typ == "" {
		return "", errors.New("outbound signing key is unavailable")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = typ
	token.Header["kid"] = key.keyID
	// Fixed algorithm only — never accept alg from external input.
	token.Header["alg"] = OutboundSigningAlgorithm
	value, err := token.SignedString(key.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign outbound jwt: %w", err)
	}
	return value, nil
}

// PublishedVerificationKey is a retired or pre-published public key.
type PublishedVerificationKey struct {
	KeyID        string
	PublicKey    ed25519.PublicKey
	PublishUntil time.Time
}

type outboundSigningKeyEntry struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

type outboundPublishedKeyEntry struct {
	publicKey    ed25519.PublicKey
	publishUntil time.Time
}

// RotatingSigningKeyProvider is concurrency-safe and keeps retired public keys
// for assertion TTL + skew + Broker JWKS cache window.
type RotatingSigningKeyProvider struct {
	mu              sync.RWMutex
	active          outboundSigningKeyEntry
	published       map[string]outboundPublishedKeyEntry
	retentionWindow time.Duration
	maxAssertionTTL time.Duration
}

// NewRotatingSigningKeyProvider builds an outbound-only key provider.
// maxAssertionTTL must be positive and at most DefaultMaxAssertionTTL (60s).
func NewRotatingSigningKeyProvider(
	activeKeyID string,
	activePrivateKey ed25519.PrivateKey,
	maxAssertionTTL time.Duration,
	published ...PublishedVerificationKey,
) (*RotatingSigningKeyProvider, error) {
	if !validOutboundKeyID(activeKeyID) {
		return nil, errors.New("valid outbound active signing key ID is required")
	}
	if len(activePrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("outbound active signing key must be an Ed25519 private key")
	}
	if maxAssertionTTL <= 0 || maxAssertionTTL > DefaultMaxAssertionTTL {
		return nil, errors.New("outbound maximum assertion TTL must be positive and at most 60 seconds")
	}
	provider := &RotatingSigningKeyProvider{
		active: outboundSigningKeyEntry{
			keyID: activeKeyID, privateKey: append(ed25519.PrivateKey(nil), activePrivateKey...),
		},
		published:       make(map[string]outboundPublishedKeyEntry, len(published)),
		retentionWindow: maxAssertionTTL + DefaultAssertionClockSkew + DefaultBrokerJWKSCacheWindow,
		maxAssertionTTL: maxAssertionTTL,
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
		return SigningKey{}, ErrOutboundSigningKeyNotFound
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.active.keyID == "" || len(provider.active.privateKey) != ed25519.PrivateKeySize {
		return SigningKey{}, ErrOutboundSigningKeyNotFound
	}
	return SigningKey{
		keyID:      provider.active.keyID,
		privateKey: append(ed25519.PrivateKey(nil), provider.active.privateKey...),
	}, nil
}

func (provider *RotatingSigningKeyProvider) MaximumAssertionTTL() time.Duration {
	if provider == nil {
		return 0
	}
	return provider.maxAssertionTTL
}

func (provider *RotatingSigningKeyProvider) VerificationKey(keyID string, now time.Time) (ed25519.PublicKey, error) {
	if provider == nil || now.IsZero() || !validOutboundKeyID(keyID) {
		return nil, ErrOutboundSigningKeyNotFound
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if keyID == provider.active.keyID {
		publicKey, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
		return append(ed25519.PublicKey(nil), publicKey...), nil
	}
	entry, exists := provider.published[keyID]
	if !exists || outboundPublicationExpired(entry.publishUntil, now) {
		return nil, ErrOutboundSigningKeyNotFound
	}
	return append(ed25519.PublicKey(nil), entry.publicKey...), nil
}

func (provider *RotatingSigningKeyProvider) PublicJWKS(now time.Time) (JSONWebKeySet, error) {
	if provider == nil || now.IsZero() {
		return JSONWebKeySet{}, errors.New("outbound JWKS time and provider are required")
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.active.keyID == "" || len(provider.active.privateKey) != ed25519.PrivateKeySize {
		return JSONWebKeySet{}, ErrOutboundSigningKeyNotFound
	}
	publicKeys := make(map[string]ed25519.PublicKey, len(provider.published)+1)
	activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	publicKeys[provider.active.keyID] = activePublic
	for keyID, entry := range provider.published {
		if !outboundPublicationExpired(entry.publishUntil, now) {
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
		result.Keys = append(result.Keys, outboundPublicJWK(keyID, publicKeys[keyID]))
	}
	return result, nil
}

// Rotate switches the active key; previous public key remains published for the
// full retention window (assertion TTL + skew + Broker JWKS cache).
func (provider *RotatingSigningKeyProvider) Rotate(
	newKeyID string,
	newPrivateKey ed25519.PrivateKey,
	now time.Time,
) error {
	if provider == nil || now.IsZero() {
		return errors.New("outbound signing key provider and rotation time are required")
	}
	if !validOutboundKeyID(newKeyID) || len(newPrivateKey) != ed25519.PrivateKeySize {
		return errors.New("valid Ed25519 outbound rotation key and key ID are required")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	newPublic, _ := newPrivateKey.Public().(ed25519.PublicKey)
	if existing, found := provider.published[newKeyID]; found &&
		!existing.publicKey.Equal(newPublic) {
		return errors.New("outbound rotation key ID conflicts with a different published key")
	}
	if newKeyID == provider.active.keyID {
		activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
		if !activePublic.Equal(newPublic) {
			return errors.New("outbound active key ID cannot be reused for different key material")
		}
		return nil
	}
	oldPublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	provider.published[provider.active.keyID] = outboundPublishedKeyEntry{
		publicKey:    append(ed25519.PublicKey(nil), oldPublic...),
		publishUntil: now.Add(provider.retentionWindow),
	}
	delete(provider.published, newKeyID)
	provider.active = outboundSigningKeyEntry{
		keyID: newKeyID, privateKey: append(ed25519.PrivateKey(nil), newPrivateKey...),
	}
	return nil
}

func (provider *RotatingSigningKeyProvider) addPublishedKey(value PublishedVerificationKey) error {
	if !validOutboundKeyID(value.KeyID) || len(value.PublicKey) != ed25519.PublicKeySize {
		return errors.New("valid outbound published Ed25519 key and key ID are required")
	}
	activePublic, _ := provider.active.privateKey.Public().(ed25519.PublicKey)
	if value.KeyID == provider.active.keyID {
		if !activePublic.Equal(value.PublicKey) {
			return errors.New("outbound active key ID conflicts with a published key")
		}
		return nil
	}
	if _, duplicate := provider.published[value.KeyID]; duplicate {
		return errors.New("duplicate outbound published key ID")
	}
	provider.published[value.KeyID] = outboundPublishedKeyEntry{
		publicKey:    append(ed25519.PublicKey(nil), value.PublicKey...),
		publishUntil: value.PublishUntil.UTC(),
	}
	return nil
}

func outboundPublicJWK(keyID string, publicKey ed25519.PublicKey) JSONWebKey {
	return JSONWebKey{
		KeyType: "OKP", Curve: "Ed25519", Use: "sig", Algorithm: OutboundSigningAlgorithm,
		KeyID: keyID, X: base64.RawURLEncoding.EncodeToString(publicKey),
	}
}

func outboundPublicationExpired(until, now time.Time) bool {
	return !until.IsZero() && !now.Before(until)
}

func validOutboundKeyID(value string) bool {
	if value == "" || len(value) > maximumOutboundKeyIDLength {
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
