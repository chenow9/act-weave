package secret

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var testMasterKey = []byte("0123456789abcdef0123456789abcdef")

func TestEncryptUsesAuthenticatedRandomizedCiphertext(t *testing.T) {
	encryptor, err := NewLocalEncryptor("local-test-v1", testMasterKey)
	if err != nil {
		t.Fatalf("create local encryptor: %v", err)
	}
	ctx := context.Background()
	plaintext := []byte("high-value-api-key")
	aad := []byte("workspace/secret/version")
	first, err := encryptor.Encrypt(ctx, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt first value: %v", err)
	}
	second, err := encryptor.Encrypt(ctx, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt second value: %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatalf("encryption reused nonce/ciphertext: first=%x second=%x", first.Nonce, second.Nonce)
	}
	if bytes.Contains(first.Ciphertext, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	decrypted, err := encryptor.Decrypt(ctx, first, aad)
	if err != nil {
		t.Fatalf("decrypt protected value: %v", err)
	}
	defer wipe(decrypted)
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("unexpected decrypted value %q", decrypted)
	}
	if _, err := encryptor.Decrypt(ctx, first, []byte("different identity")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("expected associated-data authentication failure, got %v", err)
	}
	if encryptor.Fingerprint(plaintext) != encryptor.Fingerprint(plaintext) ||
		strings.Contains(encryptor.Fingerprint(plaintext), string(plaintext)) {
		t.Fatal("fingerprint is unstable or exposes plaintext")
	}
}

func TestEncryptorRejectsMissingOrInvalidMasterKey(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(testMasterKey)
	if _, err := NewLocalEncryptorFromBase64("local-test-v1", encoded); err != nil {
		t.Fatalf("create encryptor from valid base64: %v", err)
	}
	for name, value := range map[string]string{
		"missing":        "",
		"invalid-base64": "not-base64!",
		"wrong-length":   base64.StdEncoding.EncodeToString([]byte("too-short")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLocalEncryptorFromBase64("local-test-v1", value); err == nil {
				t.Fatal("expected master key rejection")
			}
		})
	}
	if _, err := NewLocalEncryptor("", testMasterKey); err == nil {
		t.Fatal("expected missing key id rejection")
	}
}

func TestEncryptedValueAndWriteInputsDoNotSerializeSensitiveFields(t *testing.T) {
	protected := EncryptedValue{
		Ciphertext: []byte("ciphertext"),
		Nonce:      []byte("nonce"),
		KeyID:      "key-id",
	}
	input := RotateInput{Plaintext: "never-serialize-this"}
	for name, value := range map[string]any{
		"encrypted": protected,
		"input":     input,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		for _, forbidden := range []string{"ciphertext", "nonce", "key-id", "never-serialize-this"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s JSON exposed %q: %s", name, forbidden, encoded)
			}
		}
	}
}
