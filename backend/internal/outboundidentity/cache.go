package outboundidentity

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// BrokerCacheKey uniquely identifies a cached Broker business Token.
// Must include boot, workspace, subject, root, connection, normalized scopes,
// provider/connection policy versions, and machine secret version (T1/T2).
// Never crosses Runs (root scope is part of the key).
type BrokerCacheKey struct {
	BootID                  string
	WorkspaceID             string
	SubjectType             SubjectType
	SubjectID               string
	RootScopeType           RootScopeType
	RootScopeID             string
	ConnectionID            string
	NormalizedScopes        string // space-joined sorted scopes
	ProviderContractVersion int64
	ConnectionPolicyVersion int64
	MachineSecretVersion    int64
}

// Valid reports whether every required component is present.
func (k BrokerCacheKey) Valid() bool {
	return strings.TrimSpace(k.BootID) != "" &&
		strings.TrimSpace(k.WorkspaceID) != "" &&
		k.SubjectType.Valid() &&
		strings.TrimSpace(k.SubjectID) != "" &&
		k.RootScopeType.Valid() &&
		strings.TrimSpace(k.RootScopeID) != "" &&
		strings.TrimSpace(k.ConnectionID) != "" &&
		k.ProviderContractVersion > 0 &&
		k.ConnectionPolicyVersion > 0 &&
		k.MachineSecretVersion > 0
}

// Equal compares two keys component-wise.
func (k BrokerCacheKey) Equal(other BrokerCacheKey) bool {
	return k.BootID == other.BootID &&
		k.WorkspaceID == other.WorkspaceID &&
		k.SubjectType == other.SubjectType &&
		k.SubjectID == other.SubjectID &&
		k.RootScopeType == other.RootScopeType &&
		k.RootScopeID == other.RootScopeID &&
		k.ConnectionID == other.ConnectionID &&
		k.NormalizedScopes == other.NormalizedScopes &&
		k.ProviderContractVersion == other.ProviderContractVersion &&
		k.ConnectionPolicyVersion == other.ConnectionPolicyVersion &&
		k.MachineSecretVersion == other.MachineSecretVersion
}

// NormalizeBrokerScopes joins sorted unique scopes for cache keys.
func NormalizeBrokerScopes(scopes []string) string {
	if len(scopes) == 0 {
		return ""
	}
	cp := append([]string(nil), scopes...)
	for i := range cp {
		cp[i] = strings.TrimSpace(cp[i])
	}
	sort.Strings(cp)
	// de-dupe
	out := make([]string, 0, len(cp))
	var prev string
	for _, s := range cp {
		if s == "" || s == prev {
			continue
		}
		out = append(out, s)
		prev = s
	}
	return strings.Join(out, " ")
}

// CacheKeyFromExchange builds a BrokerCacheKey from an exchange request.
func CacheKeyFromExchange(req BrokerExchangeRequest) BrokerCacheKey {
	return BrokerCacheKey{
		BootID:                  req.BootID,
		WorkspaceID:             req.WorkspaceID,
		SubjectType:             req.SubjectType,
		SubjectID:               req.SubjectID,
		RootScopeType:           req.RootScopeType,
		RootScopeID:             req.RootScopeID,
		ConnectionID:            req.ConnectionID,
		NormalizedScopes:        NormalizeBrokerScopes(req.Scopes),
		ProviderContractVersion: req.ProviderContractVersion,
		ConnectionPolicyVersion: req.ConnectionPolicyVersion,
		MachineSecretVersion:    req.Machine.Version,
	}
}

type brokerCacheEntry struct {
	token     BrokerToken
	expiresAt time.Time
}

// BrokerTokenCache is a process-local Subject-isolated short-lived Token cache.
// Not durable. No cross-Run reuse. Singleflight per full key.
type BrokerTokenCache struct {
	mu      sync.Mutex
	entries map[string]*brokerCacheEntry
	group   singleflight.Group
	clock   Clock
	skew    time.Duration
	// maxEntries soft cap; excess triggers expiry sweep then reject oldest.
	maxEntries int
}

// NewBrokerTokenCache constructs an in-memory subject cache.
func NewBrokerTokenCache(clock Clock) *BrokerTokenCache {
	if clock == nil {
		clock = WallClock{}
	}
	return &BrokerTokenCache{
		entries:    make(map[string]*brokerCacheEntry),
		clock:      clock,
		skew:       DefaultBrokerSafetySkew,
		maxEntries: 4096,
	}
}

// GetOrExchange returns a cached Token or performs exchange under singleflight.
// Waiters are cancelled by their own context deadline; leader cancel does not
// permanently poison other waiters (singleflight re-runs on next miss).
func (c *BrokerTokenCache) GetOrExchange(
	ctx context.Context,
	client *BrokerClient,
	req BrokerExchangeRequest,
) (BrokerToken, error) {
	if c == nil {
		if client == nil {
			return BrokerToken{}, ErrIdentityConnectionNotReady
		}
		return client.Exchange(ctx, req)
	}
	key := CacheKeyFromExchange(req)
	if !key.Valid() {
		return BrokerToken{}, ErrIdentityPolicyInvalid
	}
	mapKey := FormatBrokerCacheKeyString(key)
	now := c.clock.Now()
	if token, ok := c.lookup(mapKey, now); ok {
		return token, nil
	}
	if client == nil {
		return BrokerToken{}, ErrIdentityConnectionNotReady
	}

	// singleflight shares one exchange per key; result is cloned per waiter.
	v, err, _ := c.group.Do(mapKey, func() (any, error) {
		// Double-check after winning the flight.
		if token, ok := c.lookup(mapKey, c.clock.Now()); ok {
			return token, nil
		}
		token, err := client.Exchange(ctx, req)
		if err != nil {
			return BrokerToken{}, err
		}
		// Compute residence: min(tokenExpiry - skew, root deadline, connection max).
		// parseBrokerTokenResponse already applied connection max and root deadline
		// to ExpiresAt; apply safety skew for cache residence only.
		residence := token.ExpiresAt.Add(-c.skew)
		if !residence.After(c.clock.Now()) {
			// Too short to cache — return once without storing.
			return token, nil
		}
		c.store(mapKey, token, residence)
		return token, nil
	})
	if err != nil {
		return BrokerToken{}, err
	}
	token, _ := v.(BrokerToken)
	// Clone plaintext for the caller so cache entry is independent.
	return BrokerToken{
		AccessToken: append([]byte(nil), token.AccessToken...),
		TokenType:   token.TokenType,
		ExpiresAt:   token.ExpiresAt,
	}, nil
}

func (c *BrokerTokenCache) lookup(mapKey string, now time.Time) (BrokerToken, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[mapKey]
	if !ok || entry == nil || !entry.expiresAt.After(now) {
		if ok {
			entry.token.Zero()
			delete(c.entries, mapKey)
		}
		return BrokerToken{}, false
	}
	return BrokerToken{
		AccessToken: append([]byte(nil), entry.token.AccessToken...),
		TokenType:   entry.token.TokenType,
		ExpiresAt:   entry.token.ExpiresAt,
	}, true
}

func (c *BrokerTokenCache) store(mapKey string, token BrokerToken, residence time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxEntries {
		c.sweepLocked(c.clock.Now())
	}
	if len(c.entries) >= c.maxEntries {
		// Refuse to store rather than evict other Subjects (capacity fail-closed for cache).
		return
	}
	// Replace existing for same key.
	if existing, ok := c.entries[mapKey]; ok && existing != nil {
		existing.token.Zero()
	}
	c.entries[mapKey] = &brokerCacheEntry{
		token: BrokerToken{
			AccessToken: append([]byte(nil), token.AccessToken...),
			TokenType:   token.TokenType,
			ExpiresAt:   token.ExpiresAt,
		},
		expiresAt: residence,
	}
}

func (c *BrokerTokenCache) sweepLocked(now time.Time) {
	for key, entry := range c.entries {
		if entry == nil || !entry.expiresAt.After(now) {
			if entry != nil {
				entry.token.Zero()
			}
			delete(c.entries, key)
		}
	}
}

// InvalidateRoot drops all cache entries for a root execution (terminal / cancel).
func (c *BrokerTokenCache) InvalidateRoot(bootID, workspaceID, rootScopeID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		// Format: boot|ws|subjectType|subjectID|rootType|rootID|...
		parts := strings.Split(key, "|")
		if len(parts) < 6 {
			continue
		}
		if parts[0] == bootID && parts[1] == workspaceID && parts[5] == rootScopeID {
			if entry != nil {
				entry.token.Zero()
			}
			delete(c.entries, key)
		}
	}
}

// InvalidateConnection drops entries for a connection when policy / secret / disable changes.
func (c *BrokerTokenCache) InvalidateConnection(workspaceID, connectionID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		parts := strings.Split(key, "|")
		if len(parts) < 7 {
			continue
		}
		if parts[1] == workspaceID && parts[6] == connectionID {
			if entry != nil {
				entry.token.Zero()
			}
			delete(c.entries, key)
		}
	}
}

// InvalidateKey drops a single cache key (e.g. business 401 for current subject).
func (c *BrokerTokenCache) InvalidateKey(key BrokerCacheKey) {
	if c == nil || !key.Valid() {
		return
	}
	mapKey := FormatBrokerCacheKeyString(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[mapKey]; ok {
		if entry != nil {
			entry.token.Zero()
		}
		delete(c.entries, mapKey)
	}
}

// Stats returns non-sensitive counts for tests / diagnostics.
func (c *BrokerTokenCache) Stats() (entries int) {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close zeros and drops all entries. Idempotent.
func (c *BrokerTokenCache) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if entry != nil {
			entry.token.Zero()
		}
		delete(c.entries, key)
	}
}
