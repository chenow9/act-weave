package outboundidentity

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestDebugAttachmentIssueConsumeReplay(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault("boot-debug", clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	store, err := NewDebugAttachmentStore(vault, key, clock)
	if err != nil {
		t.Fatal(err)
	}
	locator, exp, err := store.IssueLocator(
		"ws-1", "sess-1", "user-1", "boot-debug", "root-1", 30*time.Second,
	)
	if err != nil || locator == "" || !exp.After(clock.Now()) {
		t.Fatalf("issue: %v locator=%q", err, locator)
	}
	if strings.Contains(locator, "token") {
		t.Fatal("locator must not contain token material")
	}
	att, err := store.Consume(locator, "ws-1", "sess-1", "user-1")
	if err != nil || att.SessionID != "sess-1" {
		t.Fatalf("consume: %v %+v", err, att)
	}
	// Replay fails.
	if _, err := store.Consume(locator, "ws-1", "sess-1", "user-1"); err == nil {
		t.Fatal("expected replay rejection")
	}
}

func TestDebugAttachmentTamperAndCrossUser(t *testing.T) {
	clock := NewFakeClock(time.Now().UTC())
	vault, _ := NewRuntimeCredentialVault("boot-debug", clock, VaultConfig{})
	defer vault.Close()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	store, _ := NewDebugAttachmentStore(vault, key, clock)
	locator, _, err := store.IssueLocator("ws-1", "sess-1", "user-1", "boot-debug", "root-1", MaxDebugAttachmentTTL)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper
	tampered := locator[:len(locator)-2] + "xx"
	if _, err := store.Consume(tampered, "ws-1", "sess-1", "user-1"); err == nil {
		t.Fatal("expected tamper rejection")
	}
	// Cross-user
	if _, err := store.Consume(locator, "ws-1", "sess-1", "user-2"); err == nil {
		t.Fatal("expected cross-user rejection")
	}
	// Cross-session
	if _, err := store.Consume(locator, "ws-1", "sess-2", "user-1"); err == nil {
		t.Fatal("expected cross-session rejection")
	}
}

func TestDebugAttachmentExpiry(t *testing.T) {
	clock := NewFakeClock(time.Now().UTC())
	vault, _ := NewRuntimeCredentialVault("boot-debug", clock, VaultConfig{})
	defer vault.Close()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	store, _ := NewDebugAttachmentStore(vault, key, clock)
	locator, _, _ := store.IssueLocator("ws-1", "sess-1", "user-1", "boot-debug", "root-1", MaxDebugAttachmentTTL)
	clock.Advance(MaxDebugAttachmentTTL + time.Second)
	if _, err := store.Consume(locator, "ws-1", "sess-1", "user-1"); err == nil {
		t.Fatal("expected expiry rejection")
	}
}
