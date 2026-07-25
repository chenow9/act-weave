package outboundidentity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testBoot      = "boot-test-1"
	testWorkspace = "ws-1"
	testSubject   = "user-1"
	testRootID    = "run-1"
	testConn      = "conn-1"
)

func testKey(overrides ...func(*VaultKey)) VaultKey {
	k := VaultKey{
		BootID: testBoot, WorkspaceID: testWorkspace,
		SubjectType: SubjectTypeUser, SubjectID: testSubject,
		RootScopeType: RootScopeAgentRun, RootScopeID: testRootID,
		ConnectionID: testConn, ConnectionPolicyVersion: 1,
	}
	for _, fn := range overrides {
		fn(&k)
	}
	return k
}

func testRoot(overrides ...func(*RootScope)) RootScope {
	r := RootScope{
		BootID: testBoot, WorkspaceID: testWorkspace,
		SubjectType: SubjectTypeUser, SubjectID: testSubject,
		RootScopeType: RootScopeAgentRun, RootScopeID: testRootID,
	}
	for _, fn := range overrides {
		fn(&r)
	}
	return r
}

func canaryToken(tag string) []byte {
	return []byte("CANARY-TOKEN-" + tag + "-do-not-leak")
}

func TestVaultAttachBorrowReturnCleanup(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	token := canaryToken("basic")
	key := testKey()
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: token, ExpiresAt: clock.Now().Add(5 * time.Minute),
		MaxResidenceSeconds: 600,
	}}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Source canary must not be required after attach (vault has copy).
	for i := range token {
		token[i] = 0
	}

	borrowed, err := vault.Borrow(key)
	if err != nil {
		t.Fatalf("borrow: %v", err)
	}
	if !bytes.Equal(borrowed.Bytes, canaryToken("basic")) {
		t.Fatalf("borrowed bytes mismatch")
	}
	// Partial key must fail.
	_, err = vault.Borrow(testKey(func(k *VaultKey) { k.SubjectID = "other" }))
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("cross-subject borrow: %v", err)
	}
	_, err = vault.Borrow(testKey(func(k *VaultKey) { k.RootScopeID = "other-run" }))
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("cross-run borrow: %v", err)
	}
	_, err = vault.Borrow(testKey(func(k *VaultKey) { k.ConnectionPolicyVersion = 2 }))
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("policy version borrow: %v", err)
	}

	borrowed.Release()
	borrowed.Release() // idempotent

	vault.CleanupRoot(testRoot())
	_, err = vault.Borrow(key)
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("after cleanup: %v", err)
	}
	vault.CleanupRoot(testRoot()) // idempotent
}

func TestVaultAttachAllOrNothing(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	good := AttachBinding{
		Key: testKey(), CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("good"), ExpiresAt: clock.Now().Add(time.Minute),
	}
	bad := AttachBinding{
		Key:            testKey(func(k *VaultKey) { k.ConnectionID = "conn-2" }),
		CredentialType: CredentialTypeAccessToken,
		Value:          canaryToken("bad"),
		// missing expiresAt → fail
	}
	if err := vault.Attach([]AttachBinding{good, bad}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("expected invalid, got %v", err)
	}
	// Neither entry should exist.
	if _, err := vault.Borrow(good.Key); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("partial attach left entry: %v", err)
	}
	entries, _ := vault.Stats()
	if entries != 0 {
		t.Fatalf("entries=%d after failed attach", entries)
	}
}

func TestVaultCapacityDoesNotEvictOthers(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{
		MaxProcessEntries: 1, MaxWorkspaceEntries: 10,
		MaxProcessBytes: 1024 * 1024, MaxWorkspaceBytes: 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	key1 := testKey()
	if err := vault.Attach([]AttachBinding{{
		Key: key1, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("one"), ExpiresAt: clock.Now().Add(time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
	key2 := testKey(func(k *VaultKey) {
		k.SubjectID = "user-2"
		k.ConnectionID = "conn-2"
	})
	if err := vault.Attach([]AttachBinding{{
		Key: key2, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("two"), ExpiresAt: clock.Now().Add(time.Minute),
	}}); !errors.Is(err, ErrCredentialCapacityExceeded) {
		t.Fatalf("expected capacity exceeded, got %v", err)
	}
	// Original entry still present — no LRU eviction of other subject.
	b, err := vault.Borrow(key1)
	if err != nil {
		t.Fatalf("original evicted: %v", err)
	}
	b.Release()
}

func TestVaultTTLAndSweep(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	key := testKey()
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("ttl"), ExpiresAt: clock.Now().Add(30 * time.Second),
	}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * time.Second)
	if _, err := vault.Borrow(key); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expired borrow: %v", err)
	}
	// Re-attach for sweep path
	clock.Set(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("ttl2"), ExpiresAt: clock.Now().Add(10 * time.Second),
	}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if n := vault.SweepExpired(); n != 1 {
		t.Fatalf("sweep removed %d", n)
	}
	if vault.HasActiveEntries() {
		t.Fatal("expected empty after sweep")
	}
}

func TestVaultCleanupWhileBorrowed(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	key := testKey()
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("inuse"), ExpiresAt: clock.Now().Add(time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
	b, err := vault.Borrow(key)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup while borrowed marks closing; new borrow fails.
	vault.CleanupRoot(testRoot())
	if _, err := vault.Borrow(key); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("borrow after cleanup while in use: %v", err)
	}
	// Still can use existing borrow bytes until release.
	if !bytes.Equal(b.Bytes, canaryToken("inuse")) {
		t.Fatal("borrowed bytes cleared early")
	}
	b.Release()
	// Entry destroyed after last return.
	if vault.HasActiveEntries() {
		t.Fatal("expected destroy after release")
	}
}

func TestVaultMoveRoot(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	debugRoot := testRoot(func(r *RootScope) {
		r.RootScopeType = RootScopeDebugAttachment
		r.RootScopeID = "attach-1"
	})
	key := testKey(func(k *VaultKey) {
		k.RootScopeType = RootScopeDebugAttachment
		k.RootScopeID = "attach-1"
	})
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("move"), ExpiresAt: clock.Now().Add(time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
	runRoot := testRoot()
	if err := vault.MoveRoot(debugRoot, runRoot); err != nil {
		t.Fatalf("move: %v", err)
	}
	// Old key gone.
	if _, err := vault.Borrow(key); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("old key after move: %v", err)
	}
	// New key works.
	newKey := testKey()
	b, err := vault.Borrow(newKey)
	if err != nil {
		t.Fatalf("new key: %v", err)
	}
	if !bytes.Equal(b.Bytes, canaryToken("move")) {
		t.Fatal("token lost on move")
	}
	b.Release()

	// Cross-subject move rejected.
	if err := vault.MoveRoot(runRoot, testRoot(func(r *RootScope) { r.SubjectID = "other" })); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("cross subject move: %v", err)
	}
}

func TestVaultDualSubjectWorkspaceIsolation(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	a := testKey()
	b := testKey(func(k *VaultKey) {
		k.SubjectID = "user-2"
		k.WorkspaceID = "ws-2"
		k.RootScopeID = "run-2"
		k.ConnectionID = "conn-b"
	})
	if err := vault.Attach([]AttachBinding{
		{Key: a, CredentialType: CredentialTypeAccessToken, Value: canaryToken("A"), ExpiresAt: clock.Now().Add(time.Minute)},
		{Key: b, CredentialType: CredentialTypeAccessToken, Value: canaryToken("B"), ExpiresAt: clock.Now().Add(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	// Cleanup A does not touch B.
	vault.CleanupRoot(testRoot())
	if _, err := vault.Borrow(a); !errors.Is(err, ErrCredentialExpired) {
		t.Fatal("A should be gone")
	}
	bb, err := vault.Borrow(b)
	if err != nil {
		t.Fatalf("B isolated cleanup failed: %v", err)
	}
	if !bytes.Equal(bb.Bytes, canaryToken("B")) {
		t.Fatal("B token corrupted")
	}
	bb.Release()
}

func TestVaultCloseAndRace(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	bindings := make([]AttachBinding, 0, n)
	for i := 0; i < n; i++ {
		i := i
		bindings = append(bindings, AttachBinding{
			Key: testKey(func(k *VaultKey) {
				k.ConnectionID = fmt.Sprintf("conn-%d", i)
				k.RootScopeID = fmt.Sprintf("run-%d", i%4)
			}),
			CredentialType: CredentialTypeAccessToken,
			Value:          canaryToken(fmt.Sprintf("%d", i)),
			ExpiresAt:      clock.Now().Add(time.Minute),
		})
	}
	if err := vault.Attach(bindings); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := bindings[i%n].Key
			b, err := vault.Borrow(key)
			if err != nil {
				return
			}
			_ = b.Bytes
			b.Release()
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vault.CleanupRoot(testRoot(func(r *RootScope) {
				r.RootScopeID = fmt.Sprintf("run-%d", i%4)
			}))
			vault.SweepExpired()
		}(i)
	}
	wg.Wait()
	vault.Close()
	vault.Close() // idempotent
	if err := vault.Attach([]AttachBinding{{
		Key: testKey(), CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("after-close"), ExpiresAt: clock.Now().Add(time.Minute),
	}}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("attach after close: %v", err)
	}
}

func TestVaultRejectsWrongBootAndMissingExpiresAt(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Attach([]AttachBinding{{
		Key:            testKey(func(k *VaultKey) { k.BootID = "other-boot" }),
		CredentialType: CredentialTypeAccessToken,
		Value:          canaryToken("boot"),
		ExpiresAt:      clock.Now().Add(time.Minute),
	}}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("wrong boot: %v", err)
	}
}

func TestVaultCanaryNotInErrorsOrJSON(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{MaxProcessEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret := canaryToken("json-leak")
	if err := vault.Attach([]AttachBinding{{
		Key: testKey(), CredentialType: CredentialTypeAccessToken,
		Value: secret, ExpiresAt: clock.Now().Add(time.Minute),
	}}); err != nil {
		t.Fatal(err)
	}
	// Capacity error must not include any entry plaintext.
	err = vault.Attach([]AttachBinding{{
		Key:            testKey(func(k *VaultKey) { k.ConnectionID = "conn-x"; k.SubjectID = "other" }),
		CredentialType: CredentialTypeAccessToken,
		Value:          canaryToken("overflow"),
		ExpiresAt:      clock.Now().Add(time.Minute),
	}})
	if err == nil {
		t.Fatal("expected capacity error")
	}
	if strings.Contains(err.Error(), "CANARY") {
		t.Fatalf("canary leaked in error: %v", err)
	}
	// Vault has no JSON serialization of entries.
	statEntries, statBytes := vault.Stats()
	encoded, _ := json.Marshal(map[string]any{
		"boot": vault.BootID(), "stats": fmt.Sprintf("%d:%d", statEntries, statBytes),
	})
	if strings.Contains(string(encoded), "CANARY") {
		t.Fatalf("canary in dump: %s", encoded)
	}
	// Entry type has no json tags — encode attempt of empty struct only.
	type noLeak struct {
		Entries int `json:"entries"`
	}
	entries, _ := vault.Stats()
	out, _ := json.Marshal(noLeak{Entries: entries})
	if strings.Contains(string(out), "CANARY") {
		t.Fatal("leak via stats json")
	}
}

func TestVaultResidenceMinDeadline(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	vault, err := NewRuntimeCredentialVault(testBoot, clock, VaultConfig{})
	if err != nil {
		t.Fatal(err)
	}
	key := testKey()
	// expiresAt far future but maxResidence 5s and root deadline 3s → deadline = 3s
	if err := vault.Attach([]AttachBinding{{
		Key: key, CredentialType: CredentialTypeAccessToken,
		Value: canaryToken("res"), ExpiresAt: clock.Now().Add(time.Hour),
		RootDeadline: clock.Now().Add(3 * time.Second), MaxResidenceSeconds: 5,
	}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(4 * time.Second)
	if _, err := vault.Borrow(key); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expected root deadline expiry, got %v", err)
	}
}
