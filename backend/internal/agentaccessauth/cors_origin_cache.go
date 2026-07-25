package agentaccessauth

import (
	"context"
	"strings"
	"sync"
	"time"
)

// CORSOriginBinding ties one exact HTTPS Origin to a single ACTIVE Client
// (public client_id + workspace). Production CORS must not union origins
// across clients when reflecting Access-Control-Allow-Origin.
type CORSOriginBinding struct {
	Origin           string
	WorkspaceID      string
	PublicClientID   string
	InternalClientID string
}

// CORSOriginLister loads Client-scoped exact Origin bindings. Production
// wiring sources ACTIVE Agent Access Client allowedCorsOrigins from PostgreSQL.
type CORSOriginLister interface {
	ListExactCORSOriginBindings(context.Context) ([]CORSOriginBinding, error)
}

// CachedExactOriginMatcher is a process-local exact-origin matcher with TTL
// refresh and Client/Workspace isolation. Load failures keep the last good
// set (retry soon); unknown origins are never echoed.
type CachedExactOriginMatcher struct {
	lister CORSOriginLister
	ttl    time.Duration
	now    func() time.Time

	mu sync.RWMutex
	// origin -> bindings that registered it
	byOrigin map[string][]CORSOriginBinding
	// publicClientID|origin
	byClientOrigin map[string]struct{}
	// workspaceID|origin
	byWorkspaceOrigin map[string]struct{}
	refreshed         time.Time
}

// NewCachedExactOriginMatcher builds a matcher. ttl must be positive.
func NewCachedExactOriginMatcher(
	lister CORSOriginLister,
	ttl time.Duration,
) (*CachedExactOriginMatcher, error) {
	if lister == nil || ttl <= 0 {
		return nil, ErrCORSPolicyInvalid
	}
	return &CachedExactOriginMatcher{
		lister:            lister,
		ttl:               ttl,
		now:               func() time.Time { return time.Now().UTC() },
		byOrigin:          make(map[string][]CORSOriginBinding),
		byClientOrigin:    make(map[string]struct{}),
		byWorkspaceOrigin: make(map[string]struct{}),
	}, nil
}

// AllowsExactOrigin reports whether any ACTIVE client registered origin.
// Prefer AllowsOriginForClient / AllowsOriginForWorkspace for isolation.
func (matcher *CachedExactOriginMatcher) AllowsExactOrigin(origin string) bool {
	if matcher == nil {
		return false
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	matcher.refreshIfStale()
	matcher.mu.RLock()
	defer matcher.mu.RUnlock()
	_, ok := matcher.byOrigin[origin]
	return ok
}

// AllowsOriginForClient reports whether the given public client_id (azp)
// registered origin. Empty clientID denies.
func (matcher *CachedExactOriginMatcher) AllowsOriginForClient(origin, publicClientID string) bool {
	if matcher == nil {
		return false
	}
	origin = strings.TrimSpace(origin)
	publicClientID = strings.TrimSpace(publicClientID)
	if origin == "" || publicClientID == "" {
		return false
	}
	matcher.refreshIfStale()
	matcher.mu.RLock()
	defer matcher.mu.RUnlock()
	_, ok := matcher.byClientOrigin[clientOriginKey(publicClientID, origin)]
	return ok
}

// AllowsOriginForWorkspace reports whether any ACTIVE client in workspace
// registered origin.
func (matcher *CachedExactOriginMatcher) AllowsOriginForWorkspace(origin, workspaceID string) bool {
	if matcher == nil {
		return false
	}
	origin = strings.TrimSpace(origin)
	workspaceID = strings.TrimSpace(workspaceID)
	if origin == "" || workspaceID == "" {
		return false
	}
	matcher.refreshIfStale()
	matcher.mu.RLock()
	defer matcher.mu.RUnlock()
	_, ok := matcher.byWorkspaceOrigin[workspaceOriginKey(workspaceID, origin)]
	return ok
}

// Snapshot returns a defensive copy of currently cached origins (for tests/ops).
func (matcher *CachedExactOriginMatcher) Snapshot() []string {
	if matcher == nil {
		return nil
	}
	matcher.refreshIfStale()
	matcher.mu.RLock()
	defer matcher.mu.RUnlock()
	out := make([]string, 0, len(matcher.byOrigin))
	for origin := range matcher.byOrigin {
		out = append(out, origin)
	}
	return out
}

func (matcher *CachedExactOriginMatcher) refreshIfStale() {
	now := matcher.now()
	matcher.mu.RLock()
	fresh := !matcher.refreshed.IsZero() && now.Sub(matcher.refreshed) < matcher.ttl
	matcher.mu.RUnlock()
	if fresh {
		return
	}
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	if !matcher.refreshed.IsZero() && now.Sub(matcher.refreshed) < matcher.ttl {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := matcher.lister.ListExactCORSOriginBindings(ctx)
	if err != nil {
		// Keep last good set; mark partially stale so we retry soon.
		matcher.refreshed = now.Add(-matcher.ttl / 2)
		return
	}
	byOrigin := make(map[string][]CORSOriginBinding)
	byClient := make(map[string]struct{})
	byWorkspace := make(map[string]struct{})
	for _, raw := range list {
		origin := strings.TrimSpace(raw.Origin)
		workspaceID := strings.TrimSpace(raw.WorkspaceID)
		publicClientID := strings.TrimSpace(raw.PublicClientID)
		if origin == "" || workspaceID == "" || publicClientID == "" {
			continue
		}
		binding := CORSOriginBinding{
			Origin: origin, WorkspaceID: workspaceID, PublicClientID: publicClientID,
			InternalClientID: strings.TrimSpace(raw.InternalClientID),
		}
		byOrigin[origin] = append(byOrigin[origin], binding)
		byClient[clientOriginKey(publicClientID, origin)] = struct{}{}
		byWorkspace[workspaceOriginKey(workspaceID, origin)] = struct{}{}
	}
	matcher.byOrigin = byOrigin
	matcher.byClientOrigin = byClient
	matcher.byWorkspaceOrigin = byWorkspace
	matcher.refreshed = now
}

func clientOriginKey(publicClientID, origin string) string {
	return publicClientID + "\x00" + origin
}

func workspaceOriginKey(workspaceID, origin string) string {
	return workspaceID + "\x00" + origin
}
