package agentaccessauth

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

const maximumSigningKeyFileBytes = 16 * 1024

type PublicKeyFile struct {
	KeyID         string
	PublicKeyFile string
}

type RetiredPublicKeyFile struct {
	KeyID         string
	PublicKeyFile string
	RetiredAt     time.Time
	PublishUntil  time.Time
}

type FileSigningKeyConfig struct {
	Algorithm              string
	ActiveKeyID            string
	PrivateKeyFile         string
	GenerateIfMissing      bool
	MaxTokenTTL            time.Duration
	PrepublishedPublicKeys []PublicKeyFile
	RetiredPublicKeys      []RetiredPublicKeyFile
}

func LoadFileSigningKeyProvider(config FileSigningKeyConfig, now time.Time) (*RotatingSigningKeyProvider, error) {
	if config.Algorithm != AAPSigningAlgorithm {
		return nil, errors.New("AAP signing algorithm must be EdDSA")
	}
	if now.IsZero() || !validSigningKeyID(config.ActiveKeyID) || config.PrivateKeyFile == "" {
		return nil, errors.New("AAP active signing key ID, private key file, and load time are required")
	}
	privateKey, err := loadOrCreateEd25519PrivateKey(config.PrivateKeyFile, config.GenerateIfMissing)
	if err != nil {
		return nil, err
	}
	published := make([]PublishedVerificationKey, 0,
		len(config.PrepublishedPublicKeys)+len(config.RetiredPublicKeys))
	for _, value := range config.PrepublishedPublicKeys {
		publicKey, err := loadEd25519PublicKey(value.PublicKeyFile)
		if err != nil {
			return nil, err
		}
		published = append(published, PublishedVerificationKey{
			KeyID: value.KeyID, PublicKey: publicKey,
		})
	}
	retentionWindow := config.MaxTokenTTL + DefaultTokenClockSkew
	for _, value := range config.RetiredPublicKeys {
		if value.RetiredAt.IsZero() || value.PublishUntil.IsZero() ||
			value.PublishUntil.Before(value.RetiredAt.Add(retentionWindow)) {
			return nil, errors.New("AAP retired public key retention must cover maximum token TTL and clock skew")
		}
		if publicationExpired(value.PublishUntil, now) {
			continue
		}
		publicKey, err := loadEd25519PublicKey(value.PublicKeyFile)
		if err != nil {
			return nil, err
		}
		published = append(published, PublishedVerificationKey{
			KeyID: value.KeyID, PublicKey: publicKey, PublishUntil: value.PublishUntil,
		})
	}
	provider, err := NewRotatingSigningKeyProvider(
		config.ActiveKeyID, privateKey, config.MaxTokenTTL, published...,
	)
	if err != nil {
		return nil, fmt.Errorf("configure AAP signing keys: %w", err)
	}
	return provider, nil
}

func loadOrCreateEd25519PrivateKey(path string, generateIfMissing bool) (ed25519.PrivateKey, error) {
	value, err := readBoundedKeyFile(path)
	if err == nil {
		return parseEd25519PrivateKey(value)
	}
	if !generateIfMissing || !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load AAP private signing key file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("create AAP private signing key directory")
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errors.New("generate AAP private signing key")
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, errors.New("encode AAP private signing key")
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			value, readErr := readBoundedKeyFile(path)
			if readErr != nil {
				return nil, errors.New("load concurrently created AAP private signing key")
			}
			return parseEd25519PrivateKey(value)
		}
		return nil, errors.New("create AAP private signing key file")
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, errors.New("write AAP private signing key file")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, errors.New("sync AAP private signing key file")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("close AAP private signing key file")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func loadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	value, err := readBoundedKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("load AAP public signing key file: %w", err)
	}
	block, trailing := pem.Decode(value)
	if block == nil || len(trailing) != 0 || block.Type != "PUBLIC KEY" {
		return nil, errors.New("AAP public signing key must contain one PEM public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse AAP public signing key")
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("AAP public signing key must be Ed25519")
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func parseEd25519PrivateKey(value []byte) (ed25519.PrivateKey, error) {
	block, trailing := pem.Decode(value)
	if block == nil || len(trailing) != 0 || block.Type != "PRIVATE KEY" {
		return nil, errors.New("AAP private signing key must contain one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse AAP private signing key")
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("AAP private signing key must be Ed25519")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func readBoundedKeyFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximumSigningKeyFileBytes+1))
	if err != nil {
		return nil, errors.New("read signing key file")
	}
	if len(value) > maximumSigningKeyFileBytes {
		return nil, errors.New("signing key file exceeds size limit")
	}
	return value, nil
}
