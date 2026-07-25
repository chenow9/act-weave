package agentaccessauth

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrStreamAuthorizationStateNotFound = errors.New("AAP stream authorization state was not found")

const MaximumSecurityVersionCacheTTL = 60 * time.Second

type StreamAuthorizationState struct {
	WorkspaceID, AgentID, ClientID, GrantID, ServicePrincipalID string
	SecurityVersion                                             int64
}

type StreamAuthorizationStateStore interface {
	ResolveStreamAuthorizationState(
		context.Context,
		StreamBinding,
		time.Time,
	) (StreamAuthorizationState, error)
}

type SecurityVersionCacheStats struct {
	Hits, Misses, Invalidations uint64
	Entries                     int
}

type securityVersionCacheEntry struct {
	state     StreamAuthorizationState
	expiresAt time.Time
}

// SecurityVersionCache is only a Stream optimization. Ordinary data-plane
// authorization deliberately bypasses it, so a committed revocation rejects
// new requests immediately on every node. Its maximum TTL is the Stream
// revalidation window, making a lost invalidation a bounded delay.
type SecurityVersionCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]securityVersionCacheEntry
	stats   SecurityVersionCacheStats
}

func NewSecurityVersionCache(ttl time.Duration) (*SecurityVersionCache, error) {
	if ttl <= 0 || ttl > MaximumSecurityVersionCacheTTL {
		return nil, ErrStreamRevalidationInvalid
	}
	return &SecurityVersionCache{
		ttl: ttl, entries: make(map[string]securityVersionCacheEntry),
	}, nil
}

func (cache *SecurityVersionCache) resolve(
	ctx context.Context,
	store StreamAuthorizationStateStore,
	binding StreamBinding,
	at time.Time,
) (StreamAuthorizationState, error) {
	if cache == nil || store == nil || ctx == nil || !validStreamBinding(binding) || at.IsZero() {
		return StreamAuthorizationState{}, ErrStreamRevalidationInvalid
	}
	key := streamBindingKey(binding)
	cache.mu.Lock()
	if entry, exists := cache.entries[key]; exists && at.Before(entry.expiresAt) {
		cache.stats.Hits++
		state := entry.state
		cache.mu.Unlock()
		return state, nil
	}
	delete(cache.entries, key)
	cache.stats.Misses++
	cache.mu.Unlock()

	state, err := store.ResolveStreamAuthorizationState(ctx, binding, at)
	if err != nil {
		return StreamAuthorizationState{}, err
	}
	if !validStreamAuthorizationState(state, binding) {
		return StreamAuthorizationState{}, ErrStreamAuthorizationStateNotFound
	}
	cache.mu.Lock()
	cache.entries[key] = securityVersionCacheEntry{state: state, expiresAt: at.Add(cache.ttl)}
	cache.mu.Unlock()
	return state, nil
}

// Invalidate removes all matching Workspace/Agent/Client/Grant entries. The
// mutation path calls this after commit and before publishing the wakeup hint.
func (cache *SecurityVersionCache) Invalidate(change SecurityChange) error {
	change.WorkspaceID = normalizeIdentity(change.WorkspaceID)
	change.AgentID = normalizeIdentity(change.AgentID)
	change.ClientID = normalizeIdentity(change.ClientID)
	change.GrantID = normalizeIdentity(change.GrantID)
	if cache == nil || !validSecurityChange(change) {
		return ErrStreamRevalidationInvalid
	}
	cache.mu.Lock()
	for key, entry := range cache.entries {
		if securityChangeMatchesState(change, entry.state) {
			delete(cache.entries, key)
			cache.stats.Invalidations++
		}
	}
	cache.mu.Unlock()
	return nil
}

func securityChangeMatchesState(change SecurityChange, state StreamAuthorizationState) bool {
	return change.WorkspaceID == state.WorkspaceID &&
		(change.AgentID == "" || change.AgentID == state.AgentID) &&
		(change.ClientID == "" || change.ClientID == state.ClientID) &&
		(change.GrantID == "" || change.GrantID == state.GrantID)
}

func (cache *SecurityVersionCache) Stats() SecurityVersionCacheStats {
	if cache == nil {
		return SecurityVersionCacheStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	result := cache.stats
	result.Entries = len(cache.entries)
	return result
}

type CachedStreamAuthorizer struct {
	store StreamAuthorizationStateStore
	cache *SecurityVersionCache
}

func NewCachedStreamAuthorizer(
	store StreamAuthorizationStateStore,
	cache *SecurityVersionCache,
) (*CachedStreamAuthorizer, error) {
	if store == nil || cache == nil {
		return nil, ErrStreamRevalidationInvalid
	}
	return &CachedStreamAuthorizer{store: store, cache: cache}, nil
}

func (authorizer *CachedStreamAuthorizer) Reauthorize(
	ctx context.Context,
	binding StreamBinding,
	at time.Time,
) error {
	binding = normalizeStreamBinding(binding)
	if authorizer == nil || authorizer.store == nil || authorizer.cache == nil || ctx == nil ||
		!validStreamBinding(binding) || at.IsZero() {
		return ErrAuthorizationRevoked
	}
	state, err := authorizer.cache.resolve(ctx, authorizer.store, binding, at.UTC())
	if err != nil {
		return ErrAuthorizationRevoked
	}
	if state.SecurityVersion != binding.SecurityVersion {
		return ErrSecurityVersionChanged
	}
	return nil
}

func validStreamAuthorizationState(state StreamAuthorizationState, binding StreamBinding) bool {
	return state.WorkspaceID == binding.WorkspaceID && state.AgentID == binding.AgentID &&
		state.ClientID == binding.ClientID && state.GrantID == binding.GrantID &&
		state.ServicePrincipalID == binding.PrincipalID && state.SecurityVersion > 0
}

// StreamBindingFromAuthorization constructs the immutable binding used by SSE
// from the verified Token and successful Authorization Snapshot.
func StreamBindingFromAuthorization(
	principal AAPAccessTokenPrincipal,
	snapshot AAPAuthorizationSnapshot,
) (StreamBinding, error) {
	binding := StreamBinding{
		WorkspaceID: snapshot.WorkspaceID, AgentID: snapshot.AgentID,
		ClientID: snapshot.ClientID, GrantID: snapshot.GrantID,
		PrincipalID: snapshot.ServicePrincipalID, SubjectID: snapshot.SubjectID,
		SecurityVersion: principal.SecurityVersion, TokenExpiresAt: principal.ExpiresAt,
	}
	binding = normalizeStreamBinding(binding)
	if !validAuthorizationPrincipal(principal) || !validStreamBinding(binding) ||
		snapshot.WorkspaceID != principal.WorkspaceID || snapshot.AgentID != principal.AgentID ||
		snapshot.AuthorizedParty != principal.AuthorizedParty ||
		snapshot.ServicePrincipalID != principal.ServicePrincipalID ||
		snapshot.SubjectID != principal.PrincipalID ||
		snapshot.TokenSecurityVersion != principal.SecurityVersion {
		return StreamBinding{}, ErrStreamRevalidationInvalid
	}
	return binding, nil
}
