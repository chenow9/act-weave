package outboundidentity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// MaxDebugAttachmentTTL is the hard upper bound for debug attach locators.
	MaxDebugAttachmentTTL = 60 * time.Second
	// debugLocatorNonceBytes is 256-bit nonce in the signed locator payload.
	debugLocatorNonceBytes = 32
)

// DebugAttachment is a one-shot, process-local credential attachment for the
// 运行调试台 (checklist #11). Tokens live only in the Vault under
// RootScopeDebugAttachment; the locator never enters DB/events/logs.
type DebugAttachment struct {
	WorkspaceID string
	SessionID   string
	ActorID     string // USER only
	OwnerBootID string
	RootScopeID string // random root id for vault entries before move
	ExpiresAt   time.Time
	Consumed    bool
}

// DebugAttachmentStore is process-local, concurrency-safe, single-consume.
type DebugAttachmentStore struct {
	mu      sync.Mutex
	entries map[string]*DebugAttachment // keyed by locator id (random)
	vault   CredentialVault
	// hmacKey signs locator payloads (process secret, not workspace secret).
	hmacKey []byte
	clock   Clock
	// maxTTL caps issued locators.
	maxTTL time.Duration
}

// NewDebugAttachmentStore constructs the store. hmacKey must be >= 32 bytes.
func NewDebugAttachmentStore(vault CredentialVault, hmacKey []byte, clock Clock) (*DebugAttachmentStore, error) {
	if vault == nil || len(hmacKey) < 32 {
		return nil, errors.New("debug attachment store requires vault and hmac key")
	}
	if clock == nil {
		clock = WallClock{}
	}
	return &DebugAttachmentStore{
		entries: make(map[string]*DebugAttachment),
		vault:   vault,
		hmacKey: append([]byte(nil), hmacKey...),
		clock:   clock,
		maxTTL:  MaxDebugAttachmentTTL,
	}, nil
}

// IssueLocator mints a signed locator string after vault bindings are attached
// under RootScopeDebugAttachment + rootScopeID. Does not accept EXTERNAL_SUBJECT.
func (s *DebugAttachmentStore) IssueLocator(
	workspaceID, sessionID, actorID, ownerBootID, rootScopeID string,
	ttl time.Duration,
) (locator string, expiresAt time.Time, err error) {
	if s == nil {
		return "", time.Time{}, ErrCredentialInvalid
	}
	workspaceID, sessionID, actorID = strings.TrimSpace(workspaceID), strings.TrimSpace(sessionID), strings.TrimSpace(actorID)
	ownerBootID, rootScopeID = strings.TrimSpace(ownerBootID), strings.TrimSpace(rootScopeID)
	if workspaceID == "" || sessionID == "" || actorID == "" || ownerBootID == "" || rootScopeID == "" {
		return "", time.Time{}, ErrCredentialInvalid
	}
	if ownerBootID != s.vault.BootID() {
		return "", time.Time{}, ErrCredentialExpired
	}
	if ttl <= 0 || ttl > s.maxTTL {
		ttl = s.maxTTL
	}
	now := s.clock.Now().UTC()
	expiresAt = now.Add(ttl)
	var nonce [debugLocatorNonceBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", time.Time{}, ErrCredentialInvalid
	}
	// Public locator id (128+ bit random) used as map key; not the full signed blob.
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", time.Time{}, ErrCredentialInvalid
	}
	locatorID := base64.RawURLEncoding.EncodeToString(idBytes[:])
	// Signed payload: boot | nonce | exp | workspace | session | actor | root | id
	payload := encodeDebugPayload(ownerBootID, nonce[:], expiresAt.Unix(), workspaceID, sessionID, actorID, rootScopeID, locatorID)
	mac := hmac.New(sha256.New, s.hmacKey)
	_, _ = mac.Write(payload)
	sig := mac.Sum(nil)
	locator = base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[locatorID] = &DebugAttachment{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		OwnerBootID: ownerBootID, RootScopeID: rootScopeID, ExpiresAt: expiresAt,
	}
	return locator, expiresAt, nil
}

// Consume validates the locator (signature, expiry, binding), marks single-use,
// and returns the attachment for MoveRoot to AgentRun. Tamper/replay/cross-user fail.
func (s *DebugAttachmentStore) Consume(
	locator, workspaceID, sessionID, actorID string,
) (DebugAttachment, error) {
	var zero DebugAttachment
	if s == nil || strings.TrimSpace(locator) == "" {
		return zero, ErrCredentialInvalid
	}
	parts := strings.Split(locator, ".")
	if len(parts) != 2 {
		return zero, ErrCredentialInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, ErrCredentialInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, ErrCredentialInvalid
	}
	mac := hmac.New(sha256.New, s.hmacKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return zero, ErrCredentialInvalid
	}
	boot, _, expUnix, ws, sess, actor, rootID, locatorID, err := decodeDebugPayload(payload)
	if err != nil {
		return zero, ErrCredentialInvalid
	}
	now := s.clock.Now().UTC()
	if now.Unix() >= expUnix || boot != s.vault.BootID() {
		return zero, ErrCredentialExpired
	}
	if ws != strings.TrimSpace(workspaceID) || sess != strings.TrimSpace(sessionID) ||
		actor != strings.TrimSpace(actorID) {
		return zero, ErrCredentialTargetMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[locatorID]
	if !ok || entry == nil {
		return zero, ErrCredentialExpired
	}
	if entry.Consumed || !entry.ExpiresAt.After(now) {
		return zero, ErrCredentialExpired
	}
	if entry.WorkspaceID != ws || entry.SessionID != sess || entry.ActorID != actor ||
		entry.RootScopeID != rootID || entry.OwnerBootID != boot {
		return zero, ErrCredentialTargetMismatch
	}
	entry.Consumed = true
	result := *entry
	delete(s.entries, locatorID)
	return result, nil
}

// Destroy discards an unconsumed attachment (cancel / timeout / failed message).
func (s *DebugAttachmentStore) Destroy(locatorID string, subjectType SubjectType, subjectID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	entry, ok := s.entries[locatorID]
	if ok {
		delete(s.entries, locatorID)
	}
	s.mu.Unlock()
	if !ok || entry == nil || s.vault == nil {
		return
	}
	s.vault.CleanupRoot(RootScope{
		BootID: entry.OwnerBootID, WorkspaceID: entry.WorkspaceID,
		SubjectType: subjectType, SubjectID: subjectID,
		RootScopeType: RootScopeDebugAttachment, RootScopeID: entry.RootScopeID,
	})
}

// SweepExpired removes expired unconsumed attachments (idempotent).
func (s *DebugAttachmentStore) SweepExpired(subjectType SubjectType, subjectIDFor func(DebugAttachment) string) int {
	if s == nil {
		return 0
	}
	now := s.clock.Now().UTC()
	s.mu.Lock()
	var expired []DebugAttachment
	for id, entry := range s.entries {
		if entry == nil || !entry.ExpiresAt.After(now) || entry.Consumed {
			if entry != nil {
				expired = append(expired, *entry)
			}
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()
	for _, entry := range expired {
		sid := entry.ActorID
		if subjectIDFor != nil {
			sid = subjectIDFor(entry)
		}
		if s.vault != nil {
			s.vault.CleanupRoot(RootScope{
				BootID: entry.OwnerBootID, WorkspaceID: entry.WorkspaceID,
				SubjectType: subjectType, SubjectID: sid,
				RootScopeType: RootScopeDebugAttachment, RootScopeID: entry.RootScopeID,
			})
		}
	}
	return len(expired)
}

func encodeDebugPayload(boot string, nonce []byte, exp int64, ws, sess, actor, root, id string) []byte {
	// length-prefixed fields for unambiguous parse
	parts := [][]byte{[]byte(boot), nonce, i64bytes(exp), []byte(ws), []byte(sess), []byte(actor), []byte(root), []byte(id)}
	total := 0
	for _, p := range parts {
		total += 2 + len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, byte(len(p)>>8), byte(len(p)))
		out = append(out, p...)
	}
	return out
}

func decodeDebugPayload(payload []byte) (boot string, nonce []byte, exp int64, ws, sess, actor, root, id string, err error) {
	read := func() ([]byte, error) {
		if len(payload) < 2 {
			return nil, ErrCredentialInvalid
		}
		n := int(payload[0])<<8 | int(payload[1])
		payload = payload[2:]
		if n < 0 || len(payload) < n {
			return nil, ErrCredentialInvalid
		}
		b := payload[:n]
		payload = payload[n:]
		return b, nil
	}
	b, err := read()
	if err != nil {
		return
	}
	boot = string(b)
	nonce, err = read()
	if err != nil || len(nonce) != debugLocatorNonceBytes {
		err = ErrCredentialInvalid
		return
	}
	eb, err := read()
	if err != nil || len(eb) != 8 {
		err = ErrCredentialInvalid
		return
	}
	exp = int64(binary.BigEndian.Uint64(eb))
	b, err = read()
	if err != nil {
		return
	}
	ws = string(b)
	b, err = read()
	if err != nil {
		return
	}
	sess = string(b)
	b, err = read()
	if err != nil {
		return
	}
	actor = string(b)
	b, err = read()
	if err != nil {
		return
	}
	root = string(b)
	b, err = read()
	if err != nil {
		return
	}
	id = string(b)
	return
}

func i64bytes(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}
