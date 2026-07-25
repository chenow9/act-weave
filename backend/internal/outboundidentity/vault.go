package outboundidentity

import (
	"sync"
	"time"
)

// Capacity defaults (process-level). Checklist #5: over-limit rejects without
// evicting other Subjects' active Tokens.
const (
	DefaultMaxProcessEntries   = 10_000
	DefaultMaxProcessBytes     = 64 * 1024 * 1024 // 64 MiB
	DefaultMaxWorkspaceEntries = 2_000
	DefaultMaxWorkspaceBytes   = 16 * 1024 * 1024 // 16 MiB
	DefaultMaxRootScopeEntries = MaxBindingsPerEnvelope
)

// VaultConfig controls capacity. Zero fields use defaults.
type VaultConfig struct {
	MaxProcessEntries   int
	MaxProcessBytes     int
	MaxWorkspaceEntries int
	MaxWorkspaceBytes   int
	BootID              string
}

func (c VaultConfig) withDefaults(bootID string) VaultConfig {
	if c.MaxProcessEntries <= 0 {
		c.MaxProcessEntries = DefaultMaxProcessEntries
	}
	if c.MaxProcessBytes <= 0 {
		c.MaxProcessBytes = DefaultMaxProcessBytes
	}
	if c.MaxWorkspaceEntries <= 0 {
		c.MaxWorkspaceEntries = DefaultMaxWorkspaceEntries
	}
	if c.MaxWorkspaceBytes <= 0 {
		c.MaxWorkspaceBytes = DefaultMaxWorkspaceBytes
	}
	if c.BootID == "" {
		c.BootID = bootID
	}
	return c
}

// AttachBinding is one fully-validated passthrough binding ready for Vault attach.
// Callers must not retain Value after a successful Attach (the vault takes ownership
// of a copy; the caller should Zero the source).
type AttachBinding struct {
	Key                 VaultKey
	CredentialType      CredentialType
	Value               []byte
	ExpiresAt           time.Time
	RootDeadline        time.Time // optional zero
	MaxResidenceSeconds int       // from Connection; 0 uses no connection-side cap beyond expiresAt
}

// BorrowedCredential is a short-lived view of vault plaintext. Callers MUST
// call Release when the HTTP callback finishes. Release is idempotent.
//
// Security: Bytes is a copy of the vault slice for the borrow duration only.
// Do not put Bytes into maps, logs, traces, or persistent structures. Zero
// local copies after use.
type BorrowedCredential struct {
	Key            VaultKey
	CredentialType CredentialType
	Bytes          []byte
	Deadline       time.Time
	release        func()
	released       bool
}

// Release returns the borrow and zeros the local copy.
func (b *BorrowedCredential) Release() {
	if b == nil || b.released {
		return
	}
	b.released = true
	zeroBytes(b.Bytes)
	b.Bytes = nil
	if b.release != nil {
		b.release()
		b.release = nil
	}
}

// vaultEntry is the internal RuntimeSecret. It is never exported and has no
// JSON tags so accidental encoding cannot leak plaintext.
type vaultEntry struct {
	key            VaultKey
	credentialType CredentialType
	plaintext      []byte // mutable; zeroed on destroy
	deadline       time.Time
	inUse          int
	closing        bool // set when cleanup requested while in use
}

// RuntimeCredentialVault is the process-local, non-persistent store for
// REQUEST_PASSTHROUGH tokens (T2=A).
//
// Invariants:
//   - Keys always include boot, workspace, subject, root scope, connection, policy version.
//   - No plaintext list/read management API.
//   - Attach is all-or-nothing.
//   - Capacity exceeded never evicts other Subjects' entries.
//   - Cleanup / Sweep / Close are idempotent.
//
// GC limitation: overwriting plaintext reduces residual exposure but Go cannot
// guarantee absolute erasure of all copies. Operators must keep short residence
// deadlines and not enable heap profiling while entries are active.
type RuntimeCredentialVault struct {
	mu      sync.Mutex
	clock   Clock
	config  VaultConfig
	bootID  string
	entries map[vaultMapKey]*vaultEntry
	// indexes for bulk cleanup by root scope
	byRoot map[rootMapKey]map[vaultMapKey]struct{}
	// capacity accounting
	processBytes   int
	workspaceBytes map[string]int
	workspaceCount map[string]int
	closed         bool
}

type vaultMapKey struct {
	boot, workspace, subjectType, subjectID, rootType, rootID, connection string
	policy                                                                int64
}

type rootMapKey struct {
	boot, workspace, subjectType, subjectID, rootType, rootID string
}

func toVaultMapKey(k VaultKey) vaultMapKey {
	return vaultMapKey{
		boot: k.BootID, workspace: k.WorkspaceID,
		subjectType: string(k.SubjectType), subjectID: k.SubjectID,
		rootType: string(k.RootScopeType), rootID: k.RootScopeID,
		connection: k.ConnectionID, policy: k.ConnectionPolicyVersion,
	}
}

func toRootMapKey(r RootScope) rootMapKey {
	return rootMapKey{
		boot: r.BootID, workspace: r.WorkspaceID,
		subjectType: string(r.SubjectType), subjectID: r.SubjectID,
		rootType: string(r.RootScopeType), rootID: r.RootScopeID,
	}
}

func rootKeyFromVault(k VaultKey) rootMapKey {
	return rootMapKey{
		boot: k.BootID, workspace: k.WorkspaceID,
		subjectType: string(k.SubjectType), subjectID: k.SubjectID,
		rootType: string(k.RootScopeType), rootID: k.RootScopeID,
	}
}

// NewRuntimeCredentialVault constructs an empty vault for this process boot.
func NewRuntimeCredentialVault(bootID string, clock Clock, config VaultConfig) (*RuntimeCredentialVault, error) {
	if bootID == "" {
		return nil, ErrCredentialInvalid
	}
	if clock == nil {
		clock = WallClock{}
	}
	config = config.withDefaults(bootID)
	return &RuntimeCredentialVault{
		clock:          clock,
		config:         config,
		bootID:         bootID,
		entries:        make(map[vaultMapKey]*vaultEntry),
		byRoot:         make(map[rootMapKey]map[vaultMapKey]struct{}),
		workspaceBytes: make(map[string]int),
		workspaceCount: make(map[string]int),
	}, nil
}

// BootID returns the vault's process boot identifier.
func (v *RuntimeCredentialVault) BootID() string {
	if v == nil {
		return ""
	}
	return v.bootID
}

// Attach inserts all bindings atomically. On any validation or capacity failure,
// no entries from this call remain. Source Value slices are not retained by the
// vault after Attach returns (the vault stores independent copies).
func (v *RuntimeCredentialVault) Attach(bindings []AttachBinding) error {
	if v == nil {
		return ErrCredentialInvalid
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrCredentialExpired
	}
	if len(bindings) == 0 {
		return ErrCredentialInvalid
	}
	if len(bindings) > MaxBindingsPerEnvelope {
		return ErrCredentialInvalid
	}

	now := v.clock.Now()
	// Phase 1: validate all candidates without mutation.
	prepared := make([]*vaultEntry, 0, len(bindings))
	seen := make(map[vaultMapKey]struct{}, len(bindings))
	addBytes := 0
	wsAddBytes := map[string]int{}
	wsAddCount := map[string]int{}

	for _, binding := range bindings {
		if err := v.validateAttachLocked(binding, now); err != nil {
			return err
		}
		mk := toVaultMapKey(binding.Key)
		if _, dup := seen[mk]; dup {
			return ErrCredentialInvalid
		}
		if _, exists := v.entries[mk]; exists {
			// Re-attach same key is rejected (no silent overwrite of live tokens).
			return ErrCredentialInvalid
		}
		seen[mk] = struct{}{}
		deadline := computeResidenceDeadline(binding, now)
		if !deadline.After(now) {
			return ErrCredentialInvalid
		}
		if len(binding.Value) == 0 || len(binding.Value) > MaxTokenBytes {
			return ErrCredentialInvalid
		}
		addBytes += len(binding.Value)
		wsAddBytes[binding.Key.WorkspaceID] += len(binding.Value)
		wsAddCount[binding.Key.WorkspaceID]++
		cp := append([]byte(nil), binding.Value...)
		prepared = append(prepared, &vaultEntry{
			key: binding.Key, credentialType: binding.CredentialType,
			plaintext: cp, deadline: deadline,
		})
	}
	if addBytes > MaxEnvelopeSecretBytes {
		for _, e := range prepared {
			zeroBytes(e.plaintext)
		}
		return ErrCredentialCapacityExceeded
	}
	// Capacity checks: never evict others.
	if len(v.entries)+len(prepared) > v.config.MaxProcessEntries ||
		v.processBytes+addBytes > v.config.MaxProcessBytes {
		for _, e := range prepared {
			zeroBytes(e.plaintext)
		}
		return ErrCredentialCapacityExceeded
	}
	for ws, n := range wsAddCount {
		if v.workspaceCount[ws]+n > v.config.MaxWorkspaceEntries ||
			v.workspaceBytes[ws]+wsAddBytes[ws] > v.config.MaxWorkspaceBytes {
			for _, e := range prepared {
				zeroBytes(e.plaintext)
			}
			return ErrCredentialCapacityExceeded
		}
	}

	// Phase 2: commit all.
	for _, entry := range prepared {
		mk := toVaultMapKey(entry.key)
		v.entries[mk] = entry
		rk := rootKeyFromVault(entry.key)
		if v.byRoot[rk] == nil {
			v.byRoot[rk] = make(map[vaultMapKey]struct{})
		}
		v.byRoot[rk][mk] = struct{}{}
		v.processBytes += len(entry.plaintext)
		v.workspaceBytes[entry.key.WorkspaceID] += len(entry.plaintext)
		v.workspaceCount[entry.key.WorkspaceID]++
	}
	return nil
}

func (v *RuntimeCredentialVault) validateAttachLocked(binding AttachBinding, now time.Time) error {
	if !binding.Key.Valid() {
		return ErrCredentialInvalid
	}
	if binding.Key.BootID != v.bootID {
		return ErrCredentialInvalid
	}
	if !binding.CredentialType.Valid() {
		return ErrCredentialInvalid
	}
	if binding.ExpiresAt.IsZero() {
		// T3=A: expiresAt required.
		return ErrCredentialInvalid
	}
	if containsControlBytes(binding.Value) {
		return ErrCredentialInvalid
	}
	return nil
}

func computeResidenceDeadline(binding AttachBinding, now time.Time) time.Time {
	deadline := binding.ExpiresAt.UTC()
	if !binding.RootDeadline.IsZero() && binding.RootDeadline.Before(deadline) {
		deadline = binding.RootDeadline.UTC()
	}
	if binding.MaxResidenceSeconds > 0 {
		capAt := now.Add(time.Duration(binding.MaxResidenceSeconds) * time.Second)
		if capAt.Before(deadline) {
			deadline = capAt
		}
	}
	return deadline
}

// Borrow returns a copy of plaintext for one injection callback.
// Only exact VaultKey matches succeed — partial keys are rejected.
func (v *RuntimeCredentialVault) Borrow(key VaultKey) (*BorrowedCredential, error) {
	if v == nil || !key.Valid() {
		return nil, ErrCredentialInvalid
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil, ErrCredentialExpired
	}
	mk := toVaultMapKey(key)
	entry, ok := v.entries[mk]
	if !ok || entry == nil {
		return nil, ErrCredentialExpired
	}
	if entry.closing {
		return nil, ErrCredentialExpired
	}
	now := v.clock.Now()
	if !entry.deadline.After(now) {
		// Expired: destroy if not in use, else mark closing.
		if entry.inUse == 0 {
			v.destroyEntryLocked(mk, entry)
		} else {
			entry.closing = true
		}
		return nil, ErrCredentialExpired
	}
	entry.inUse++
	cp := append([]byte(nil), entry.plaintext...)
	borrowed := &BorrowedCredential{
		Key: key, CredentialType: entry.credentialType,
		Bytes: cp, Deadline: entry.deadline,
	}
	borrowed.release = func() {
		v.returnBorrow(key)
	}
	return borrowed, nil
}

func (v *RuntimeCredentialVault) returnBorrow(key VaultKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	mk := toVaultMapKey(key)
	entry, ok := v.entries[mk]
	if !ok || entry == nil {
		return
	}
	if entry.inUse > 0 {
		entry.inUse--
	}
	if entry.closing && entry.inUse == 0 {
		v.destroyEntryLocked(mk, entry)
		return
	}
	now := v.clock.Now()
	if !entry.deadline.After(now) && entry.inUse == 0 {
		v.destroyEntryLocked(mk, entry)
	}
}

// CleanupRoot removes all entries for a root scope. In-use entries are marked
// closing and destroyed when references drop to zero. Idempotent.
func (v *RuntimeCredentialVault) CleanupRoot(root RootScope) {
	if v == nil || !root.Valid() {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cleanupRootLocked(root)
}

func (v *RuntimeCredentialVault) cleanupRootLocked(root RootScope) {
	rk := toRootMapKey(root)
	keys, ok := v.byRoot[rk]
	if !ok {
		return
	}
	// Copy key set because destroy mutates maps.
	list := make([]vaultMapKey, 0, len(keys))
	for mk := range keys {
		list = append(list, mk)
	}
	for _, mk := range list {
		entry := v.entries[mk]
		if entry == nil {
			continue
		}
		if entry.inUse > 0 {
			entry.closing = true
			continue
		}
		v.destroyEntryLocked(mk, entry)
	}
}

// MoveRoot transfers all entries from one root scope to another (debug attach →
// AgentRun). Source root must not be empty after move of overlapping keys is
// not allowed. All-or-nothing: if any target key collides, no move occurs.
func (v *RuntimeCredentialVault) MoveRoot(from, to RootScope) error {
	if v == nil || !from.Valid() || !to.Valid() {
		return ErrCredentialInvalid
	}
	if from.BootID != v.bootID || to.BootID != v.bootID {
		return ErrCredentialInvalid
	}
	if from.WorkspaceID != to.WorkspaceID || from.SubjectType != to.SubjectType || from.SubjectID != to.SubjectID {
		// Attachment cannot change subject/workspace.
		return ErrCredentialInvalid
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrCredentialExpired
	}
	srcKeys, ok := v.byRoot[toRootMapKey(from)]
	if !ok || len(srcKeys) == 0 {
		return ErrCredentialExpired
	}
	// Build new keys and check collisions.
	type moveItem struct {
		oldMK  vaultMapKey
		entry  *vaultEntry
		newKey VaultKey
	}
	moves := make([]moveItem, 0, len(srcKeys))
	for mk := range srcKeys {
		entry := v.entries[mk]
		if entry == nil {
			continue
		}
		if entry.inUse > 0 || entry.closing {
			return ErrCredentialInvalid
		}
		newKey := entry.key
		newKey.RootScopeType = to.RootScopeType
		newKey.RootScopeID = to.RootScopeID
		newMK := toVaultMapKey(newKey)
		if _, exists := v.entries[newMK]; exists {
			return ErrCredentialInvalid
		}
		moves = append(moves, moveItem{oldMK: mk, entry: entry, newKey: newKey})
	}
	if len(moves) == 0 {
		return ErrCredentialExpired
	}
	// Commit moves.
	for _, m := range moves {
		delete(v.entries, m.oldMK)
		m.entry.key = m.newKey
		newMK := toVaultMapKey(m.newKey)
		v.entries[newMK] = m.entry
		// Update root indexes.
		oldRK := rootKeyFromVault(VaultKey{
			BootID: m.oldMK.boot, WorkspaceID: m.oldMK.workspace,
			SubjectType: SubjectType(m.oldMK.subjectType), SubjectID: m.oldMK.subjectID,
			RootScopeType: RootScopeType(m.oldMK.rootType), RootScopeID: m.oldMK.rootID,
		})
		if set := v.byRoot[oldRK]; set != nil {
			delete(set, m.oldMK)
			if len(set) == 0 {
				delete(v.byRoot, oldRK)
			}
		}
		newRK := rootKeyFromVault(m.newKey)
		if v.byRoot[newRK] == nil {
			v.byRoot[newRK] = make(map[vaultMapKey]struct{})
		}
		v.byRoot[newRK][newMK] = struct{}{}
	}
	return nil
}

// SweepExpired destroys entries past deadline with zero in-use refs. Idempotent.
func (v *RuntimeCredentialVault) SweepExpired() int {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return 0
	}
	now := v.clock.Now()
	removed := 0
	for mk, entry := range v.entries {
		if entry == nil {
			continue
		}
		if entry.deadline.After(now) {
			continue
		}
		if entry.inUse > 0 {
			entry.closing = true
			continue
		}
		v.destroyEntryLocked(mk, entry)
		removed++
	}
	return removed
}

// Close rejects new attaches/borrows and destroys idle entries. In-use entries
// are marked closing. Idempotent.
func (v *RuntimeCredentialVault) Close() {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	v.closed = true
	for mk, entry := range v.entries {
		if entry == nil {
			continue
		}
		if entry.inUse > 0 {
			entry.closing = true
			continue
		}
		v.destroyEntryLocked(mk, entry)
	}
}

// HasActiveEntries reports whether any non-destroyed entries remain (for dump gating).
func (v *RuntimeCredentialVault) HasActiveEntries() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.entries) > 0
}

// HasLiveRoot reports whether any non-expired, non-closing entry exists under root.
// Used for AAP createRun idempotent replay (alive → discard plaintext; dead → EXPIRED).
func (v *RuntimeCredentialVault) HasLiveRoot(root RootScope) bool {
	if v == nil || !root.Valid() {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return false
	}
	keys, ok := v.byRoot[toRootMapKey(root)]
	if !ok || len(keys) == 0 {
		return false
	}
	now := v.clock.Now()
	for mk := range keys {
		entry := v.entries[mk]
		if entry == nil || entry.closing {
			continue
		}
		if entry.deadline.After(now) {
			return true
		}
	}
	return false
}

// Stats returns non-sensitive capacity counters for ops metrics (no keys/tokens).
func (v *RuntimeCredentialVault) Stats() (entries int, bytes int) {
	if v == nil {
		return 0, 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.entries), v.processBytes
}

func (v *RuntimeCredentialVault) destroyEntryLocked(mk vaultMapKey, entry *vaultEntry) {
	if entry == nil {
		return
	}
	n := len(entry.plaintext)
	zeroBytes(entry.plaintext)
	entry.plaintext = nil
	delete(v.entries, mk)
	rk := rootKeyFromVault(entry.key)
	if set := v.byRoot[rk]; set != nil {
		delete(set, mk)
		if len(set) == 0 {
			delete(v.byRoot, rk)
		}
	}
	v.processBytes -= n
	if v.processBytes < 0 {
		v.processBytes = 0
	}
	ws := entry.key.WorkspaceID
	v.workspaceBytes[ws] -= n
	if v.workspaceBytes[ws] <= 0 {
		delete(v.workspaceBytes, ws)
	}
	v.workspaceCount[ws]--
	if v.workspaceCount[ws] <= 0 {
		delete(v.workspaceCount, ws)
	}
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ---------------------------------------------------------------------------
// Application-facing interfaces (checklist #5 DI surface; wiring later).
// ---------------------------------------------------------------------------

// CredentialVault is the DI interface for application assembly. It deliberately
// omits any List/ReadPlaintext management methods.
type CredentialVault interface {
	BootID() string
	Attach([]AttachBinding) error
	Borrow(VaultKey) (*BorrowedCredential, error)
	CleanupRoot(RootScope)
	MoveRoot(from, to RootScope) error
	SweepExpired() int
	Close()
	HasActiveEntries() bool
	HasLiveRoot(RootScope) bool
}

// RootLifecycleCleaner is the hook root execution terminal paths will call
// (wired in checklist items 7/10/11).
type RootLifecycleCleaner interface {
	CleanupRoot(RootScope)
}

// Ensure RuntimeCredentialVault implements the DI interfaces.
var (
	_ CredentialVault      = (*RuntimeCredentialVault)(nil)
	_ RootLifecycleCleaner = (*RuntimeCredentialVault)(nil)
)
