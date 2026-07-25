package agentaccessauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSecurityVersionRevocationRejectsNewRequestImmediately(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	principal := authorizationTestPrincipal(now)
	state := authorizationTestState(principal)
	state.CurrentSecurityVersion++
	audit := &authorizationAuditStub{}
	service, err := NewAAPAuthorizationService(
		&authorizationStateStoreStub{state: state}, FailClosedSubjectOwnershipResolver{},
		WithAAPAuthorizationAudit(audit),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	_, err = service.Authorize(context.Background(), AAPAuthorizationRequest{
		Principal: principal, Action: ActionConversationCreate,
	})
	if !errors.Is(err, ErrAAPAuthorizationNotVisible) || len(audit.denials) != 1 ||
		audit.denials[0].Reason != "SECURITY_VERSION_CHANGED" {
		t.Fatalf("stale Token authorization err=%v audit=%+v", err, audit.denials)
	}
}

func TestSecurityVersionRevocationCacheInvalidationAndBoundedFallback(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	binding := securityVersionTestBinding(now.Add(10 * time.Minute))
	store := &streamAuthorizationStoreStub{state: securityVersionTestState(binding)}
	cache, err := NewSecurityVersionCache(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewCachedStreamAuthorizer(store, cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Reauthorize(context.Background(), binding, now); err != nil {
		t.Fatal(err)
	}
	store.setVersion(binding.SecurityVersion + 1)
	if err := authorizer.Reauthorize(context.Background(), binding, now.Add(time.Second)); err != nil {
		t.Fatalf("unexpired cache should retain its bounded snapshot: %v", err)
	}
	if err := cache.Invalidate(SecurityChange{
		WorkspaceID: binding.WorkspaceID, ClientID: binding.ClientID,
		SecurityVersion: binding.SecurityVersion + 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Reauthorize(context.Background(), binding, now.Add(2*time.Second)); !errors.Is(err, ErrSecurityVersionChanged) {
		t.Fatalf("invalidated cache reauthorization err=%v", err)
	}
	if calls := store.callCount(); calls != 2 {
		t.Fatalf("state store calls=%d want=2", calls)
	}
	stats := cache.Stats()
	if stats.Hits != 1 || stats.Misses != 2 || stats.Invalidations != 1 || stats.Entries != 1 {
		t.Fatalf("cache stats=%+v", stats)
	}

	// Even if a cross-node invalidation is lost, an entry can never survive the
	// 60-second policy window. Advancing the authoritative check time expires it.
	store.setVersion(binding.SecurityVersion + 2)
	if err := authorizer.Reauthorize(context.Background(), binding, now.Add(33*time.Second)); !errors.Is(err, ErrSecurityVersionChanged) {
		t.Fatalf("expired cache did not fall back to authority: %v", err)
	}
	if _, err := NewSecurityVersionCache(MaximumSecurityVersionCacheTTL + time.Nanosecond); !errors.Is(err, ErrStreamRevalidationInvalid) {
		t.Fatalf("oversized cache TTL err=%v", err)
	}
}

func TestSecurityVersionRevocationDisconnectsOnNotificationAndLostNotification(t *testing.T) {
	for name, notify := range map[string]bool{"notification": true, "periodic fallback": false} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			binding := securityVersionTestBinding(now.Add(time.Second))
			store := &streamAuthorizationStoreStub{state: securityVersionTestState(binding)}
			cache, err := NewSecurityVersionCache(3 * time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			authorizer, _ := NewCachedStreamAuthorizer(store, cache)
			changes := NewInProcessSecurityChanges()
			revalidator, err := NewStreamRevalidator(authorizer, changes,
				RevalidationPolicy{Interval: 5 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- revalidator.Monitor(context.Background(), binding) }()
			waitForSecurityVersionSubscription(t, changes)
			store.setVersion(binding.SecurityVersion + 1)
			if notify {
				change := SecurityChange{
					WorkspaceID: binding.WorkspaceID, AgentID: binding.AgentID,
					ClientID: binding.ClientID, GrantID: binding.GrantID,
					SecurityVersion: binding.SecurityVersion + 1,
				}
				if err := cache.Invalidate(change); err != nil {
					t.Fatal(err)
				}
				if err := changes.Publish(change); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrSecurityVersionChanged) {
					t.Fatalf("monitor result=%v", err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("revoked Stream exceeded bounded revalidation window")
			}
			if changes.Stats().ActiveSubscriptions != 0 {
				t.Fatalf("revocation leaked subscription: %+v", changes.Stats())
			}
		})
	}
}

func TestSecurityVersionRevocationBuildsStreamBindingFromSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	principal := authorizationTestPrincipal(now)
	state := authorizationTestState(principal)
	service, _ := NewAAPAuthorizationService(
		&authorizationStateStoreStub{state: state}, FailClosedSubjectOwnershipResolver{},
	)
	service.now = func() time.Time { return now }
	decision, err := service.Authorize(context.Background(), AAPAuthorizationRequest{
		Principal: principal, Action: ActionConversationCreate,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := StreamBindingFromAuthorization(principal, decision.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if binding.WorkspaceID != principal.WorkspaceID || binding.AgentID != principal.AgentID ||
		binding.ClientID != state.ClientID || binding.GrantID != state.GrantID ||
		binding.PrincipalID != principal.ServicePrincipalID ||
		binding.SubjectID != principal.PrincipalID ||
		binding.SecurityVersion != principal.SecurityVersion ||
		!binding.TokenExpiresAt.Equal(principal.ExpiresAt) {
		t.Fatalf("stream binding=%+v", binding)
	}
	decision.Snapshot.SubjectID = "a68f1f2e-7b5a-7c3d-8e9f-123456789099"
	if _, err := StreamBindingFromAuthorization(principal, decision.Snapshot); !errors.Is(err, ErrStreamRevalidationInvalid) {
		t.Fatalf("mismatched Snapshot binding err=%v", err)
	}
}

type streamAuthorizationStoreStub struct {
	mu    sync.Mutex
	state StreamAuthorizationState
	calls int
}

func (store *streamAuthorizationStoreStub) ResolveStreamAuthorizationState(
	context.Context,
	StreamBinding,
	time.Time,
) (StreamAuthorizationState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.calls++
	return store.state, nil
}

func (store *streamAuthorizationStoreStub) setVersion(version int64) {
	store.mu.Lock()
	store.state.SecurityVersion = version
	store.mu.Unlock()
}

func (store *streamAuthorizationStoreStub) callCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.calls
}

func securityVersionTestBinding(expiresAt time.Time) StreamBinding {
	return StreamBinding{
		WorkspaceID:     "b68f1f2e-7b5a-7c3d-8e9f-123456789001",
		AgentID:         "b68f1f2e-7b5a-7c3d-8e9f-123456789002",
		ClientID:        "b68f1f2e-7b5a-7c3d-8e9f-123456789003",
		GrantID:         "b68f1f2e-7b5a-7c3d-8e9f-123456789004",
		PrincipalID:     "b68f1f2e-7b5a-7c3d-8e9f-123456789005",
		SubjectID:       "b68f1f2e-7b5a-7c3d-8e9f-123456789005",
		SecurityVersion: 7, TokenExpiresAt: expiresAt,
	}
}

func securityVersionTestState(binding StreamBinding) StreamAuthorizationState {
	return StreamAuthorizationState{
		WorkspaceID: binding.WorkspaceID, AgentID: binding.AgentID,
		ClientID: binding.ClientID, GrantID: binding.GrantID,
		ServicePrincipalID: binding.PrincipalID, SecurityVersion: binding.SecurityVersion,
	}
}

func waitForSecurityVersionSubscription(t *testing.T, changes *InProcessSecurityChanges) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if changes.Stats().ActiveSubscriptions == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("security change subscription was not registered")
}
