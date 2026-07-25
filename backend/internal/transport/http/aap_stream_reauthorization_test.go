package httptransport

import (
	"context"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/protocolevent"
)

func TestAAPStreamReauthorization(t *testing.T) {
	reader := newCatchUpReader(t, 0)
	handler, err := NewAAPEventCatchUp(reader, blockingAAPFollower{})
	if err != nil {
		t.Fatal(err)
	}
	changes := agentaccessauth.NewInProcessSecurityChanges()
	revalidator, err := agentaccessauth.NewStreamRevalidator(
		agentaccessauth.NewControlledStreamAuthorizer(), changes,
		agentaccessauth.RevalidationPolicy{Interval: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureRevalidator(revalidator); err != nil {
		t.Fatal(err)
	}
	binding := agentaccessauth.StreamBinding{
		WorkspaceID: catchUpWorkspaceID, AgentID: catchUpAgentID,
		ClientID: "client-http-reauth", GrantID: "grant-http-reauth",
		PrincipalID: "principal-http-reauth", SubjectID: "subject-http-reauth",
		SecurityVersion: 1, TokenExpiresAt: time.Now().UTC().Add(15 * time.Millisecond),
	}
	response := requestCatchUpHandler(t, handler, catchUpScope(), "0", "100", AAPStreamSession{
		Authorization: &binding,
	})
	body := response.Body.String()
	if !strings.Contains(body, "event: stream.error\n") ||
		!strings.Contains(body, `"code":"TOKEN_EXPIRED"`) || strings.Contains(body, "id: ") ||
		strings.Contains(body, `"eventId"`) || strings.Contains(body, `"sequence"`) {
		t.Fatalf("HTTP reauthorization signal=%s", body)
	}
	if changes.Stats().ActiveSubscriptions != 0 {
		t.Fatalf("HTTP reauthorization leaked subscription: %+v", changes.Stats())
	}
}

type blockingAAPFollower struct{}

func (blockingAAPFollower) Follow(
	ctx context.Context,
	_ protocolevent.RunScope,
	_ int64,
	_ func([]protocolevent.ProtocolEvent) error,
) error {
	<-ctx.Done()
	return ctx.Err()
}
