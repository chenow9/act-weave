package agentaccessauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

const DefaultClientAssertionJTIEntries = 100_000

type InMemoryClientAssertionJTIStore struct {
	mu         sync.Mutex
	entries    map[string]time.Time
	maxEntries int
}

func NewInMemoryClientAssertionJTIStore(maxEntries int) (*InMemoryClientAssertionJTIStore, error) {
	if maxEntries < 1 {
		return nil, errors.New("Client Assertion JTI store capacity must be positive")
	}
	return &InMemoryClientAssertionJTIStore{
		entries: make(map[string]time.Time), maxEntries: maxEntries,
	}, nil
}

func (store *InMemoryClientAssertionJTIStore) ClaimClientAssertionJTI(
	ctx context.Context,
	clientID string,
	jtiHash [sha256.Size]byte,
	expiresAt, now time.Time,
) (bool, error) {
	if store == nil || ctx == nil || ctx.Err() != nil || clientID == "" || now.IsZero() || !expiresAt.After(now) {
		return false, errors.New("invalid Client Assertion JTI claim")
	}
	key := clientID + "\x00" + string(jtiHash[:])
	store.mu.Lock()
	defer store.mu.Unlock()
	for existing, expiry := range store.entries {
		if !now.Before(expiry) {
			delete(store.entries, existing)
		}
	}
	if expiry, exists := store.entries[key]; exists && now.Before(expiry) {
		return false, nil
	}
	if len(store.entries) >= store.maxEntries {
		return false, errors.New("Client Assertion JTI store capacity exceeded")
	}
	store.entries[key] = expiresAt
	return true, nil
}
