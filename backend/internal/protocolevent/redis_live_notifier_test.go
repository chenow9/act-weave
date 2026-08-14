package protocolevent

import (
	"context"
	"testing"
	"time"

	"actweave/backend/internal/redisx"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
)

func TestRedisLiveNotifierCrossInstance(t *testing.T) {
	mini := miniredis.RunT(t)
	a, err := redisx.Open(context.Background(), redisx.Config{Addr: mini.Addr(), KeyPrefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := redisx.Open(context.Background(), redisx.Config{Addr: mini.Addr(), KeyPrefix: "t"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	na, err := NewRedisLiveNotifier(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	nb, err := NewRedisLiveNotifier(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = na.Close(); _ = nb.Close() })

	scope := RunScope{
		WorkspaceID: uuid.Must(uuid.NewV7()).String(), AgentID: uuid.Must(uuid.NewV7()).String(),
		ConversationID: uuid.Must(uuid.NewV7()).String(), RunID: uuid.Must(uuid.NewV7()).String(),
	}
	sub, err := nb.Subscribe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	streamID := uuid.Must(uuid.NewV7()).String()
	if err := na.NotifyCommitted(context.Background(), CommitNotification{
		Events: []CommittedEventRef{{
			EventID: uuid.Must(uuid.NewV7()).String(), EventStreamID: streamID,
			WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
			ConversationID: scope.ConversationID, RunID: scope.RunID,
			Sequence: 1, GlobalPosition: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sub.Notifications():
		if got.HighWatermark != 1 || got.EventStreamID != streamID {
			t.Fatalf("wakeup=%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for redis wakeup")
	}
}
