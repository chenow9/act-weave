package outboundidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const maximumOutboundSigningKeyFileBytes = 16 * 1024

// PublicKeyFile describes a pre-published verification public key on disk.
type PublicKeyFile struct {
	KeyID         string
	PublicKeyFile string
}

// RetiredPublicKeyFile describes a retired key still within the publish window.
type RetiredPublicKeyFile struct {
	KeyID         string
	PublicKeyFile string
	RetiredAt     time.Time
	PublishUntil  time.Time
}

// FileSigningKeyConfig loads the outbound (non-AAP) signing domain from files.
type FileSigningKeyConfig struct {
	Algorithm              string
	ActiveKeyID            string
	PrivateKeyFile         string
	GenerateIfMissing      bool
	MaxAssertionTTL        time.Duration
	PrepublishedPublicKeys []PublicKeyFile
	RetiredPublicKeys      []RetiredPublicKeyFile
}

// LoadFileSigningKeyProvider loads or optionally generates the outbound EdDSA key.
// Keys never enter Workspace Secret, DB, or API payloads.
func LoadFileSigningKeyProvider(config FileSigningKeyConfig, now time.Time) (*RotatingSigningKeyProvider, error) {
	if config.Algorithm != OutboundSigningAlgorithm {
		return nil, errors.New("outbound signing algorithm must be EdDSA")
	}
	if now.IsZero() || !validOutboundKeyID(config.ActiveKeyID) || config.PrivateKeyFile == "" {
		return nil, errors.New("outbound active signing key ID, private key file, and load time are required")
	}
	maxTTL := config.MaxAssertionTTL
	if maxTTL == 0 {
		maxTTL = DefaultMaxAssertionTTL
	}
	privateKey, err := loadOrCreateOutboundEd25519PrivateKey(config.PrivateKeyFile, config.GenerateIfMissing)
	if err != nil {
		return nil, err
	}
	published := make([]PublishedVerificationKey, 0,
		len(config.PrepublishedPublicKeys)+len(config.RetiredPublicKeys))
	for _, value := range config.PrepublishedPublicKeys {
		publicKey, err := loadOutboundEd25519PublicKey(value.PublicKeyFile)
		if err != nil {
			return nil, err
		}
		published = append(published, PublishedVerificationKey{
			KeyID: value.KeyID, PublicKey: publicKey,
		})
	}
	retentionWindow := maxTTL + DefaultAssertionClockSkew + DefaultBrokerJWKSCacheWindow
	for _, value := range config.RetiredPublicKeys {
		if value.RetiredAt.IsZero() || value.PublishUntil.IsZero() ||
			value.PublishUntil.Before(value.RetiredAt.Add(retentionWindow)) {
			return nil, errors.New("outbound retired public key retention must cover assertion TTL, skew, and Broker JWKS cache")
		}
		if outboundPublicationExpired(value.PublishUntil, now) {
			continue
		}
		publicKey, err := loadOutboundEd25519PublicKey(value.PublicKeyFile)
		if err != nil {
			return nil, err
		}
		published = append(published, PublishedVerificationKey{
			KeyID: value.KeyID, PublicKey: publicKey, PublishUntil: value.PublishUntil,
		})
	}
	provider, err := NewRotatingSigningKeyProvider(
		config.ActiveKeyID, privateKey, maxTTL, published...,
	)
	if err != nil {
		return nil, fmt.Errorf("configure outbound signing keys: %w", err)
	}
	return provider, nil
}

func loadOrCreateOutboundEd25519PrivateKey(path string, generateIfMissing bool) (ed25519.PrivateKey, error) {
	value, err := readBoundedOutboundKeyFile(path)
	if err == nil {
		return parseOutboundEd25519PrivateKey(value)
	}
	if !generateIfMissing || !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load outbound private signing key file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("create outbound private signing key directory")
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errors.New("generate outbound private signing key")
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, errors.New("encode outbound private signing key")
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			value, readErr := readBoundedOutboundKeyFile(path)
			if readErr != nil {
				return nil, errors.New("load concurrently created outbound private signing key")
			}
			return parseOutboundEd25519PrivateKey(value)
		}
		return nil, errors.New("create outbound private signing key file")
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, errors.New("write outbound private signing key file")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, errors.New("sync outbound private signing key file")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("close outbound private signing key file")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func loadOutboundEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	value, err := readBoundedOutboundKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("load outbound public signing key file: %w", err)
	}
	block, trailing := pem.Decode(value)
	if block == nil || len(trailing) != 0 || block.Type != "PUBLIC KEY" {
		return nil, errors.New("outbound public signing key must contain one PEM public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse outbound public signing key")
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("outbound public signing key must be Ed25519")
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func parseOutboundEd25519PrivateKey(value []byte) (ed25519.PrivateKey, error) {
	block, trailing := pem.Decode(value)
	if block == nil || len(trailing) != 0 || block.Type != "PRIVATE KEY" {
		return nil, errors.New("outbound private signing key must contain one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse outbound private signing key")
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("outbound private signing key must be Ed25519")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

// ParseMachinePrivateKey decodes a Connection machine credential Secret value
// (PKCS#8 PEM Ed25519). Used only for private_key_jwt client assertions.
func ParseMachinePrivateKey(value []byte) (ed25519.PrivateKey, error) {
	if len(value) == 0 || len(value) > maximumOutboundSigningKeyFileBytes {
		return nil, ErrIdentityConnectionNotReady
	}
	key, err := parseOutboundEd25519PrivateKey(value)
	if err != nil {
		return nil, ErrIdentityConnectionNotReady.Wrap(err)
	}
	return key, nil
}

func readBoundedOutboundKeyFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximumOutboundSigningKeyFileBytes+1))
	if err != nil {
		return nil, errors.New("read outbound signing key file")
	}
	if len(value) > maximumOutboundSigningKeyFileBytes {
		return nil, errors.New("outbound signing key file exceeds size limit")
	}
	return value, nil
}
