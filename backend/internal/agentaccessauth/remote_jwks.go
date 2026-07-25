package agentaccessauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	PrivateKeyJWTAlgorithmEdDSA   = "EdDSA"
	PrivateKeyJWTAlgorithmPS256   = "PS256"
	DefaultRemoteJWKSMaxBytes     = 256 * 1024
	DefaultRemoteJWKSMaxKeys      = 32
	DefaultRemoteJWKSCacheTTL     = 5 * time.Minute
	MaximumRemoteJWKSCacheTTL     = 15 * time.Minute
	DefaultRemoteJWKSCacheEntries = 10_000
)

var ErrRemoteJWKSRejected = errors.New("remote JWKS was rejected")

type RemoteJWKSFetchResult struct {
	Body        []byte
	CacheTTL    time.Duration
	CacheTTLSet bool
}

type RemoteJWKSFetcher interface {
	FetchRemoteJWKS(context.Context, string, int64) (RemoteJWKSFetchResult, error)
}

type VerificationJWK struct {
	keyID      string
	algorithm  string
	publicKey  any
	thumbprint [sha256.Size]byte
}

func (key VerificationJWK) KeyID() string { return key.keyID }

func (key VerificationJWK) Algorithm() string { return key.algorithm }

func (key VerificationJWK) PublicKey() any { return key.publicKey }

func (key VerificationJWK) Thumbprint() [sha256.Size]byte { return key.thumbprint }

type remoteJWKSCacheEntry struct {
	keys      []VerificationJWK
	expiresAt time.Time
}

type RemoteJWKSCache struct {
	mu         sync.Mutex
	entries    map[string]remoteJWKSCacheEntry
	fetcher    RemoteJWKSFetcher
	maxBytes   int64
	maxKeys    int
	maxEntries int
	now        func() time.Time
}

func NewRemoteJWKSCache(
	fetcher RemoteJWKSFetcher,
	maxBytes int64,
	maxKeys, maxEntries int,
) (*RemoteJWKSCache, error) {
	if fetcher == nil || maxBytes < 1 || maxBytes > 4*DefaultRemoteJWKSMaxBytes ||
		maxKeys < 1 || maxKeys > 128 || maxEntries < 1 {
		return nil, errors.New("remote JWKS cache bounds and fetcher are required")
	}
	return &RemoteJWKSCache{
		entries: make(map[string]remoteJWKSCacheEntry), fetcher: fetcher,
		maxBytes: maxBytes, maxKeys: maxKeys, maxEntries: maxEntries,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (cache *RemoteJWKSCache) ResolveVerificationKey(
	ctx context.Context,
	jwksURI, keyID, algorithm string,
) (VerificationJWK, error) {
	return cache.ResolveVerificationKeyWithAlgorithms(
		ctx, jwksURI, keyID, algorithm, PrivateKeyJWTAlgorithms(),
	)
}

// ResolveVerificationKeyWithAlgorithms resolves a public key for the given kid
// and algorithm, restricting JWKS parsing and selection to allowedAlgorithms.
// Callers that only need private_key_jwt should use ResolveVerificationKey.
func (cache *RemoteJWKSCache) ResolveVerificationKeyWithAlgorithms(
	ctx context.Context,
	jwksURI, keyID, algorithm string,
	allowedAlgorithms []string,
) (VerificationJWK, error) {
	allowed := allowedAlgorithmSet(allowedAlgorithms)
	if cache == nil || ctx == nil || !validRemoteJWKSURI(jwksURI) ||
		!validRemoteJWKKeyID(keyID) || allowed == nil {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	if _, ok := allowed[algorithm]; !ok {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	now := cache.now()
	if keys, found := cache.cachedKeys(jwksURI, now); found {
		if key, found := findVerificationJWK(keys, keyID, algorithm); found {
			return key, nil
		}
	}
	// Unknown kid deliberately bypasses a still-fresh cache once so Client key
	// rotation becomes visible without waiting for max-age.
	keys, err := cache.refresh(ctx, jwksURI, allowed, now)
	if err != nil {
		return VerificationJWK{}, err
	}
	key, found := findVerificationJWK(keys, keyID, algorithm)
	if !found {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	return key, nil
}

func (cache *RemoteJWKSCache) cachedKeys(uri string, now time.Time) ([]VerificationJWK, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, exists := cache.entries[uri]
	if !exists || !now.Before(entry.expiresAt) {
		if exists {
			delete(cache.entries, uri)
		}
		return nil, false
	}
	return append([]VerificationJWK(nil), entry.keys...), true
}

func (cache *RemoteJWKSCache) refresh(
	ctx context.Context,
	uri string,
	allowed map[string]struct{},
	now time.Time,
) ([]VerificationJWK, error) {
	result, err := cache.fetcher.FetchRemoteJWKS(ctx, uri, cache.maxBytes)
	if err != nil || len(result.Body) == 0 || int64(len(result.Body)) > cache.maxBytes {
		return nil, ErrRemoteJWKSRejected
	}
	keys, err := parseRemoteJWKS(result.Body, cache.maxKeys, allowed)
	if err != nil {
		return nil, err
	}
	ttl := result.CacheTTL
	if !result.CacheTTLSet {
		ttl = DefaultRemoteJWKSCacheTTL
	}
	if ttl < 0 {
		ttl = 0
	}
	if ttl > MaximumRemoteJWKSCacheTTL {
		ttl = MaximumRemoteJWKSCacheTTL
	}
	entry := remoteJWKSCacheEntry{keys: append([]VerificationJWK(nil), keys...), expiresAt: now.Add(ttl)}
	cache.mu.Lock()
	cache.pruneExpiredLocked(now)
	if _, exists := cache.entries[uri]; !exists && len(cache.entries) >= cache.maxEntries {
		cache.mu.Unlock()
		return nil, ErrRemoteJWKSRejected
	}
	cache.entries[uri] = entry
	cache.mu.Unlock()
	return keys, nil
}

func (cache *RemoteJWKSCache) pruneExpiredLocked(now time.Time) {
	for uri, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			delete(cache.entries, uri)
		}
	}
}

func findVerificationJWK(keys []VerificationJWK, keyID, algorithm string) (VerificationJWK, bool) {
	var result VerificationJWK
	found := false
	for _, key := range keys {
		if key.keyID != keyID || key.algorithm != algorithm {
			continue
		}
		if found {
			return VerificationJWK{}, false
		}
		result, found = key, true
	}
	return result, found
}

// ParseInlineJWKS parses a fixed inline JWKS document. It does not perform any
// network I/O and is intended for Trusted Subject Issuer configuration.
func ParseInlineJWKS(payload []byte, maxKeys int, allowedAlgorithms []string) ([]VerificationJWK, error) {
	return parseRemoteJWKS(payload, maxKeys, allowedAlgorithmSet(allowedAlgorithms))
}

func parseRemoteJWKS(payload []byte, maxKeys int, allowed map[string]struct{}) ([]VerificationJWK, error) {
	if len(payload) == 0 || maxKeys < 1 || len(allowed) == 0 {
		return nil, ErrRemoteJWKSRejected
	}
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&document); err != nil {
		return nil, ErrRemoteJWKSRejected
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		len(document.Keys) == 0 || len(document.Keys) > maxKeys {
		return nil, ErrRemoteJWKSRejected
	}
	keys := make([]VerificationJWK, 0, len(document.Keys))
	seen := make(map[string]struct{}, len(document.Keys))
	for _, raw := range document.Keys {
		key, err := parseRemoteJWK(raw, allowed)
		if err != nil {
			return nil, err
		}
		identity := key.keyID
		if _, duplicate := seen[identity]; duplicate {
			return nil, ErrRemoteJWKSRejected
		}
		seen[identity] = struct{}{}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].keyID == keys[right].keyID {
			return keys[left].algorithm < keys[right].algorithm
		}
		return keys[left].keyID < keys[right].keyID
	})
	return keys, nil
}

func parseRemoteJWK(raw json.RawMessage, allowed map[string]struct{}) (VerificationJWK, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	for _, privateField := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		if _, exists := fields[privateField]; exists {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
	}
	var value struct {
		KeyType   string `json:"kty"`
		Use       string `json:"use"`
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Curve     string `json:"crv"`
		X         string `json:"x"`
		N         string `json:"n"`
		E         string `json:"e"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Use != "sig" ||
		!validRemoteJWKKeyID(value.KeyID) {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	if _, ok := allowed[value.Algorithm]; !ok {
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
	switch {
	case value.KeyType == "OKP" && value.Curve == "Ed25519" && value.Algorithm == PrivateKeyJWTAlgorithmEdDSA:
		x, err := base64.RawURLEncoding.DecodeString(value.X)
		if err != nil || len(x) != ed25519.PublicKeySize || value.N != "" || value.E != "" {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
		canonical, _ := json.Marshal(struct {
			Curve   string `json:"crv"`
			KeyType string `json:"kty"`
			X       string `json:"x"`
		}{Curve: "Ed25519", KeyType: "OKP", X: value.X})
		return VerificationJWK{
			keyID: value.KeyID, algorithm: value.Algorithm,
			publicKey: ed25519.PublicKey(append([]byte(nil), x...)), thumbprint: sha256.Sum256(canonical),
		}, nil
	case value.KeyType == "RSA" && value.Algorithm == PrivateKeyJWTAlgorithmPS256:
		modulusBytes, err := base64.RawURLEncoding.DecodeString(value.N)
		if err != nil || len(modulusBytes) < 256 || len(modulusBytes) > 512 || len(modulusBytes) == 0 ||
			modulusBytes[0] == 0 || value.Curve != "" || value.X != "" {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(value.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
		exponent := 0
		for _, current := range exponentBytes {
			exponent = exponent<<8 | int(current)
		}
		if exponent != 65537 {
			return VerificationJWK{}, ErrRemoteJWKSRejected
		}
		canonical, _ := json.Marshal(struct {
			E       string `json:"e"`
			KeyType string `json:"kty"`
			N       string `json:"n"`
		}{E: value.E, KeyType: "RSA", N: value.N})
		return VerificationJWK{
			keyID: value.KeyID, algorithm: value.Algorithm,
			publicKey:  &rsa.PublicKey{N: new(big.Int).SetBytes(modulusBytes), E: exponent},
			thumbprint: sha256.Sum256(canonical),
		}, nil
	default:
		return VerificationJWK{}, ErrRemoteJWKSRejected
	}
}

// PrivateKeyJWTAlgorithms is the fixed algorithm whitelist for Client
// Assertion authentication. Subject Token verification uses Client-configured
// algorithms that must still be a subset of this supported set.
func PrivateKeyJWTAlgorithms() []string {
	return []string{PrivateKeyJWTAlgorithmEdDSA, PrivateKeyJWTAlgorithmPS256}
}

func allowedAlgorithmSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !supportedVerificationAlgorithm(value) {
			return nil
		}
		if _, exists := result[value]; exists {
			return nil
		}
		result[value] = struct{}{}
	}
	return result
}

func supportedVerificationAlgorithm(value string) bool {
	return value == PrivateKeyJWTAlgorithmEdDSA || value == PrivateKeyJWTAlgorithmPS256
}

func validRemoteJWKSURI(value string) bool {
	if value == "" || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.Fragment == ""
}

func validRemoteJWKKeyID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func allowedPrivateKeyJWTAlgorithm(value string) bool {
	return supportedVerificationAlgorithm(value)
}
