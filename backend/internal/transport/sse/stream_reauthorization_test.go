package sse_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/transport/sse"
)

func TestStreamReauthorization(t *testing.T) {
	t.Run("expired token becomes a cursorless transport signal", func(t *testing.T) {
		binding := sseRevalidationBinding(time.Now().UTC().Add(10 * time.Millisecond))
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator, err := agentaccessauth.NewStreamRevalidator(
			agentaccessauth.NewControlledStreamAuthorizer(), changes,
			agentaccessauth.RevalidationPolicy{Interval: time.Second},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = revalidator.Monitor(context.Background(), binding)
		if !errors.Is(err, agentaccessauth.ErrTokenExpired) {
			t.Fatalf("expiry result=%v", err)
		}
		signal := sse.NewStreamErrorSignal(
			agentaccessauth.StreamErrorCode(err), "The stream authorization is no longer valid.",
			true, "request-reauth", "trace-reauth", nil, time.Now().UTC(),
		)
		var output bytes.Buffer
		if err := sse.NewEncoder().EncodeStreamError(&output, signal); err != nil {
			t.Fatal(err)
		}
		body := output.String()
		if !strings.Contains(body, "event: stream.error\n") ||
			!strings.Contains(body, `"code":"TOKEN_EXPIRED"`) || strings.Contains(body, "id: ") ||
			strings.Contains(body, `"eventId"`) || strings.Contains(body, `"sequence"`) {
			t.Fatalf("token expiry signal=%s", body)
		}
	})

	t.Run("fresh token resumes from original cursor", func(t *testing.T) {
		binding := sseRevalidationBinding(time.Now().UTC().Add(time.Second))
		changes := agentaccessauth.NewInProcessSecurityChanges()
		revalidator, err := agentaccessauth.NewStreamRevalidator(
			agentaccessauth.NewControlledStreamAuthorizer(), changes,
			agentaccessauth.RevalidationPolicy{Interval: 10 * time.Millisecond},
		)
		if err != nil {
			t.Fatal(err)
		}
		authContext, stopAuth := context.WithCancel(context.Background())
		authResult := make(chan error, 1)
		go func() { authResult <- revalidator.Monitor(authContext, binding) }()
		waitForAAPSecuritySubscription(t, changes)

		reader := newFollowReader(
			followEvent(1, protocolevent.EventRunStarted),
			followEvent(2, protocolevent.EventRunCompleted),
		)
		notifier := newFakeFollowNotifier()
		follow := newTestFollow(t, reader, notifier)
		received := make([]int64, 0, 1)
		if err := follow.Follow(context.Background(), followScope(), 1,
			func(events []protocolevent.ProtocolEvent) error {
				for _, event := range events {
					received = append(received, event.Sequence)
				}
				return nil
			}); err != nil {
			t.Fatal(err)
		}
		if len(received) != 1 || received[0] != 2 {
			t.Fatalf("fresh token replayed wrong cursor: %v", received)
		}
		stopAuth()
		select {
		case err := <-authResult:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("fresh token monitor result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("fresh token monitor leaked")
		}
	})
}

func sseRevalidationBinding(expiresAt time.Time) agentaccessauth.StreamBinding {
	return agentaccessauth.StreamBinding{
		WorkspaceID: followWorkspaceID, AgentID: followAgentID,
		ClientID: "client-reauth", GrantID: "grant-reauth",
		PrincipalID: "principal-reauth", SubjectID: "subject-reauth",
		SecurityVersion: 1, TokenExpiresAt: expiresAt,
	}
}

func waitForAAPSecuritySubscription(
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
	t.Fatal("security revalidation did not subscribe")
}
