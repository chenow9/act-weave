package authn

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRefreshTokenIssueParseAndHash(t *testing.T) {
	manager, err := newRefreshTokenManager(bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)))
	if err != nil {
		t.Fatalf("create refresh token manager: %v", err)
	}
	issued, err := manager.Issue()
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}
	if issued.Plaintext == "" || issued.Hash == "" || strings.Contains(issued.Hash, issued.Plaintext) {
		t.Fatalf("unexpected issued refresh token metadata: %+v", issued)
	}
	sessionID, hash, err := manager.Parse(issued.Plaintext)
	if err != nil {
		t.Fatalf("parse refresh token: %v", err)
	}
	if sessionID != issued.SessionID || hash != issued.Hash || len(hash) != 64 {
		t.Fatalf("refresh token parse mismatch: session=%q hash=%q issued=%+v", sessionID, hash, issued)
	}
}

func TestRefreshTokenRotationKeepsSessionIDAndChangesSecret(t *testing.T) {
	manager := NewRefreshTokenManager()
	initial, err := manager.Issue()
	if err != nil {
		t.Fatalf("issue initial refresh token: %v", err)
	}
	replacement, err := manager.Rotate(initial.SessionID)
	if err != nil {
		t.Fatalf("rotate refresh token: %v", err)
	}
	if replacement.SessionID != initial.SessionID || replacement.Plaintext == initial.Plaintext || replacement.Hash == initial.Hash {
		t.Fatalf("unexpected refresh token rotation: initial=%+v replacement=%+v", initial, replacement)
	}
	parsedSessionID, parsedHash, err := manager.Parse(replacement.Plaintext)
	if err != nil || parsedSessionID != initial.SessionID || parsedHash != replacement.Hash {
		t.Fatalf("parse replacement refresh token: session=%q hash=%q err=%v", parsedSessionID, parsedHash, err)
	}
}

func TestRefreshTokenRejectsMalformedValues(t *testing.T) {
	manager := NewRefreshTokenManager()
	for _, token := range []string{
		"",
		"rt1.invalid.short",
		"rt2.018f1f2e-7b5a-7c3d-8e9f-4234567890ab.c2hvcnQ",
		"rt1.018f1f2e-7b5a-4c3d-8e9f-4234567890ab.c2hvcnQ",
		"rt1.018f1f2e-7b5a-7c3d-8e9f-4234567890ab.***",
	} {
		if _, _, err := manager.Parse(token); !errors.Is(err, ErrInvalidRefreshToken) {
			t.Fatalf("expected invalid refresh token error for %q, got %v", token, err)
		}
	}
}
