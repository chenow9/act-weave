package agentaccessauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

func TestStreamReauthorization(t *testing.T) {
	t.Run("periodic check disconnects disabled resources", func(t *testing.T) {
		for name, revoked := range map[string]error{
			"Client":    agentaccessauth.ErrClientDisabled,
			"Grant":     agentaccessauth.ErrGrantDisabled,
			"Agent":     agentaccessauth.ErrAgentDisabled,
			"Workspace": agentaccessauth.ErrWorkspaceDisabled,
		} {
			t.Run(name, func(t *testing.T) {
				binding := revalidationBinding(time.Now().UTC().Add(time.Second))
				authorizer := agentaccessauth.NewControlledStreamAuthorizer()
				changes := agentaccessauth.NewInProcessSecurityChanges()
				revalidator := newTestRevalidator(t, authorizer, changes, 5*time.Millisecond)
				result := make(chan error, 1)
				go func() { result <- revalidator.Monitor(context.Background(), binding) }()
				waitForSecuritySubscription(t, changes)
				if err := authorizer.Set(binding, binding.SecurityVersion, revoked); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if !errors.Is(err, revoked) {
						t.Fatalf("revocation result=%v", err)
					}
				case <-time.After(200 * time.Millisecond):
					t.Fatal("periodic reauthorization exceeded controlled 60-second equivalent")
				}
				if stats := changes.Stats(); stats.ActiveSubscriptions != 0 {
					t.Fatalf("revocation leaked subscription: %+v", stats)
				}
			})
		}
	})

	t.Run("security version notification disconnects immediately", func(t *testing.T) {
		binding := revalidationBinding(time.Now().UTC().Add(time.Second))
		authorizer := agentaccessauth.NewControlledStreamAuthorizer()
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator := newTestRevalidator(t, authorizer, changes, time.Second)
		result := make(chan error, 1)
		go func() { result <- revalidator.Monitor(context.Background(), binding) }()
		waitForSecuritySubscription(t, changes)
		if err := authorizer.Set(binding, binding.SecurityVersion+1, nil); err != nil {
			t.Fatal(err)
		}
		if err := changes.Publish(agentaccessauth.SecurityChange{
			WorkspaceID: binding.WorkspaceID, AgentID: binding.AgentID,
			ClientID: binding.ClientID, GrantID: binding.GrantID,
			SecurityVersion: binding.SecurityVersion + 1,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, agentaccessauth.ErrSecurityVersionChanged) ||
				agentaccessauth.StreamErrorCode(err) != "AUTHORIZATION_REVOKED" {
				t.Fatalf("security change result=%v code=%s", err, agentaccessauth.StreamErrorCode(err))
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("security change notification was not immediate")
		}
	})

	t.Run("Workspace notification fans out without enumerating Client or Grant", func(t *testing.T) {
		binding := revalidationBinding(time.Now().UTC().Add(time.Second))
		authorizer := agentaccessauth.NewControlledStreamAuthorizer()
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator := newTestRevalidator(t, authorizer, changes, time.Second)
		result := make(chan error, 1)
		go func() { result <- revalidator.Monitor(context.Background(), binding) }()
		waitForSecuritySubscription(t, changes)
		if err := authorizer.Set(binding, binding.SecurityVersion, agentaccessauth.ErrWorkspaceDisabled); err != nil {
			t.Fatal(err)
		}
		if err := changes.Publish(agentaccessauth.SecurityChange{
			WorkspaceID: binding.WorkspaceID, SecurityVersion: binding.SecurityVersion,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, agentaccessauth.ErrWorkspaceDisabled) {
				t.Fatalf("Workspace fanout result=%v", err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Workspace security notification did not fan out")
		}
	})

	t.Run("token expiry has a dedicated transport reason", func(t *testing.T) {
		binding := revalidationBinding(time.Now().UTC().Add(20 * time.Millisecond))
		authorizer := agentaccessauth.NewControlledStreamAuthorizer()
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator := newTestRevalidator(t, authorizer, changes, time.Second)
		startedAt := time.Now()
		err := revalidator.Monitor(context.Background(), binding)
		if !errors.Is(err, agentaccessauth.ErrTokenExpired) ||
			agentaccessauth.StreamErrorCode(err) != "TOKEN_EXPIRED" || time.Since(startedAt) > 200*time.Millisecond {
			t.Fatalf("token expiry result=%v code=%s duration=%s",
				err, agentaccessauth.StreamErrorCode(err), time.Since(startedAt))
		}
		if stats := changes.Stats(); stats.ActiveSubscriptions != 0 {
			t.Fatalf("token expiry leaked subscription: %+v", stats)
		}
	})

	t.Run("new token starts a fresh authorized monitor", func(t *testing.T) {
		oldBinding := revalidationBinding(time.Now().UTC().Add(-time.Millisecond))
		authorizer := agentaccessauth.NewControlledStreamAuthorizer()
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator := newTestRevalidator(t, authorizer, changes, 10*time.Millisecond)
		if err := revalidator.Monitor(context.Background(), oldBinding); !errors.Is(err, agentaccessauth.ErrTokenExpired) {
			t.Fatalf("old token result=%v", err)
		}

		fresh := oldBinding
		fresh.TokenExpiresAt = time.Now().UTC().Add(time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- revalidator.Monitor(ctx, fresh) }()
		waitForSecuritySubscription(t, changes)
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("fresh token result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("fresh token monitor did not cancel")
		}
	})
}

func newTestRevalidator(
	t *testing.T,
	authorizer agentaccessauth.StreamAuthorizer,
	changes agentaccessauth.SecurityChangeSource,
	interval time.Duration,
) *agentaccessauth.StreamRevalidator {
	t.Helper()
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		authorizer, changes, agentaccessauth.RevalidationPolicy{Interval: interval},
	)
	if err != nil {
		t.Fatal(err)
	}
	return revalidator
}

func waitForSecuritySubscription(
	t *testing.T,
	changes *agentaccessauth.InProcessSecurityChanges,
) {
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

func revalidationBinding(expiresAt time.Time) agentaccessauth.StreamBinding {
	return agentaccessauth.StreamBinding{
		WorkspaceID: "11000000-0000-4000-8000-000000000301",
		AgentID:     "22000000-0000-4000-8000-000000000301",
		ClientID:    "client-301", GrantID: "grant-301",
		PrincipalID: "principal-301", SubjectID: "subject-301",
		SecurityVersion: 7, TokenExpiresAt: expiresAt,
	}
}
