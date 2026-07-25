package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const localMasterKeyBytes = 32

var ErrDecrypt = errors.New("secret ciphertext authentication failed")

// EncryptedValue contains only protected material and key metadata. The JSON
// exclusions prevent accidental transport serialization.
type EncryptedValue struct {
	Ciphertext []byte `json:"-"`
	Nonce      []byte `json:"-"`
	KeyID      string `json:"-"`
}

// EnvelopeEncryptor is the boundary for local or future KMS-backed
// implementations. Associated data binds ciphertext to its database identity.
type EnvelopeEncryptor interface {
	Encrypt(context.Context, []byte, []byte) (EncryptedValue, error)
	Decrypt(context.Context, EncryptedValue, []byte) ([]byte, error)
	Fingerprint([]byte) string
}

type LocalEncryptor struct {
	keyID string
	key   [localMasterKeyBytes]byte
	rand  io.Reader
}

func NewLocalEncryptor(keyID string, masterKey []byte) (*LocalEncryptor, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, errors.New("secret encryption key id is required")
	}
	if len(masterKey) != localMasterKeyBytes {
		return nil, fmt.Errorf("secret master key must contain %d bytes", localMasterKeyBytes)
	}
	encryptor := &LocalEncryptor{keyID: keyID, rand: rand.Reader}
	copy(encryptor.key[:], masterKey)
	return encryptor, nil
}

func NewLocalEncryptorFromBase64(keyID string, encodedMasterKey string) (*LocalEncryptor, error) {
	if strings.TrimSpace(encodedMasterKey) == "" {
		return nil, errors.New("secret master key is required")
	}
	masterKey, err := base64.StdEncoding.DecodeString(encodedMasterKey)
	if err != nil {
		return nil, errors.New("secret master key must be valid base64")
	}
	defer wipe(masterKey)
	return NewLocalEncryptor(keyID, masterKey)
}

func (e *LocalEncryptor) Encrypt(
	ctx context.Context,
	plaintext []byte,
	associatedData []byte,
) (EncryptedValue, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedValue{}, err
	}
	if len(plaintext) == 0 {
		return EncryptedValue{}, ErrInvalid
	}
	aead, err := e.aead()
	if err != nil {
		return EncryptedValue{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(e.rand, nonce); err != nil {
		return EncryptedValue{}, fmt.Errorf("generate secret encryption nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, associatedData)
	return EncryptedValue{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyID:      e.keyID,
	}, nil
}

func (e *LocalEncryptor) Decrypt(
	ctx context.Context,
	protected EncryptedValue,
	associatedData []byte,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if protected.KeyID != e.keyID || len(protected.Ciphertext) == 0 || len(protected.Nonce) == 0 {
		return nil, ErrDecrypt
	}
	aead, err := e.aead()
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, protected.Nonce, protected.Ciphertext, associatedData)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

func (e *LocalEncryptor) Fingerprint(plaintext []byte) string {
	mac := hmac.New(sha256.New, e.key[:])
	_, _ = mac.Write(plaintext)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func (e *LocalEncryptor) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(e.key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret AEAD: %w", err)
	}
	return aead, nil
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
