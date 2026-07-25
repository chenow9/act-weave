package authn

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	manager, err := newPasswordManager(
		DefaultArgon2idParams(),
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
	)
	if err != nil {
		t.Fatalf("create password manager: %v", err)
	}
	encoded, err := manager.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if strings.Contains(encoded, "correct horse") || !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatalf("password hash has unsafe or unexpected encoding: %q", encoded)
	}
	valid, needsRehash, err := manager.Verify("correct horse battery staple", encoded)
	if err != nil || !valid || needsRehash {
		t.Fatalf("verify correct password: valid=%t needsRehash=%t err=%v", valid, needsRehash, err)
	}
	valid, needsRehash, err = manager.Verify("wrong password", encoded)
	if err != nil || valid || needsRehash {
		t.Fatalf("verify wrong password: valid=%t needsRehash=%t err=%v", valid, needsRehash, err)
	}
}

func TestPasswordVerifyRequestsParameterUpgrade(t *testing.T) {
	manager, err := NewPasswordManager(DefaultArgon2idParams())
	if err != nil {
		t.Fatalf("create password manager: %v", err)
	}
	oldParams := Argon2idParams{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}
	encoded := encodePassword("upgrade me", []byte("12345678"), oldParams)
	valid, needsRehash, err := manager.Verify("upgrade me", encoded)
	if err != nil || !valid || !needsRehash {
		t.Fatalf("verify old password parameters: valid=%t needsRehash=%t err=%v", valid, needsRehash, err)
	}
}

func TestPasswordRejectsWeakCurrentConfiguration(t *testing.T) {
	weak := DefaultArgon2idParams()
	weak.MemoryKiB = 8 * 1024
	if _, err := NewPasswordManager(weak); !errors.Is(err, ErrWeakPasswordConfig) {
		t.Fatalf("expected weak configuration error, got %v", err)
	}
}

func TestPasswordRejectsMalformedOrExcessiveHashes(t *testing.T) {
	manager, err := NewPasswordManager(DefaultArgon2idParams())
	if err != nil {
		t.Fatalf("create password manager: %v", err)
	}
	for _, encoded := range []string{
		"",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=999999999,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$***$***",
	} {
		if _, _, err := manager.Verify("password", encoded); !errors.Is(err, ErrInvalidPasswordHash) {
			t.Fatalf("expected invalid hash error for %q, got %v", encoded, err)
		}
	}
}
