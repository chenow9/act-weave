package protocolevent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"
)

func TestLiveNotifier(t *testing.T) {
	ctx := context.Background()
	testDatabase := dbtest.New(t)
	testDatabase.MigrateTo(t, 40)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)

	notifier := protocolevent.NewInProcessLiveNotifier()
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	scope := protocolRunScope()

	subscriptionContext, cancelSubscription := context.WithCancel(ctx)
	subscription, err := notifier.Subscribe(subscriptionContext, scope)
	if err != nil {
		t.Fatal(err)
	}
	otherSubscription, err := notifier.Subscribe(ctx, protocolevent.RunScope{
		WorkspaceID: protocolWorkspaceID, AgentID: protocolOtherAgentID,
		ConversationID: protocolOtherSession, RunID: protocolOtherRunID,
	})
	if err != nil {
		t.Fatal(err)
	}

	firstItem, firstEvent := commitLiveNotifierEvent(t, unit, 1)
	secondItem, secondEvent := commitLiveNotifierEvent(t, unit, 2)
	wakeup := receiveLiveWakeup(t, subscription.Notifications())
	if wakeup.Scope != scope || wakeup.EventStreamID != protocolStreamID || wakeup.HighWatermark != 2 {
		t.Fatalf("coalesced wakeup=%+v", wakeup)
	}
	select {
	case unexpected := <-otherSubscription.Notifications():
		t.Fatalf("cross-scope notification=%+v", unexpected)
	default:
	}
	stats := notifier.Stats()
	if stats.Published != 2 || stats.Delivered != 1 || stats.Coalesced != 1 ||
		stats.ActiveSubscriptions != 2 {
		t.Fatalf("live notifier stats after coalescing=%+v", stats)
	}

	events, err := reader.ReadRunAfter(ctx, scope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != firstEvent.ID || events[1].ID != secondEvent.ID ||
		events[0].ItemID != firstItem.ID || events[1].ItemID != secondItem.ID {
		t.Fatalf("reader did not recover committed events: %+v", events)
	}

	cancelSubscription()
	assertLiveSubscriptionClosed(t, subscription.Notifications())
	if err := otherSubscription.Close(); err != nil {
		t.Fatal(err)
	}
	assertLiveSubscriptionClosed(t, otherSubscription.Notifications())
	if active := notifier.Stats().ActiveSubscriptions; active != 0 {
		t.Fatalf("active subscriptions after cancellation=%d", active)
	}

	_, thirdEvent := commitLiveNotifierEvent(t, unit, 3)
	events, err = reader.ReadRunAfter(ctx, scope, 2, 100)
	if err != nil || len(events) != 1 || events[0].ID != thirdEvent.ID || events[0].Sequence != 3 {
		t.Fatalf("lost wakeup recovery events=%+v err=%v", events, err)
	}

	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
	_, fourthEvent, result := commitLiveNotifierEventResult(t, unit, 4)
	if !errors.Is(result.NotifyError, protocolevent.ErrLiveNotifierClosed) {
		t.Fatalf("closed notifier error=%v", result.NotifyError)
	}
	events, err = reader.ReadRunAfter(ctx, scope, 3, 100)
	if err != nil || len(events) != 1 || events[0].ID != fourthEvent.ID || events[0].Sequence != 4 {
		t.Fatalf("notifier failure lost fact events=%+v err=%v", events, err)
	}
	if notifier.Stats().Rejected != 1 {
		t.Fatalf("notifier failure was not observable: %+v", notifier.Stats())
	}
	if _, err := notifier.Subscribe(ctx, scope); !errors.Is(err, protocolevent.ErrLiveNotifierClosed) {
		t.Fatalf("subscribe after close error=%v", err)
	}

	assertMassLiveSubscriptionCancellation(t, scope)
}

func commitLiveNotifierEvent(
	t *testing.T,
	unit *protocolevent.ProtocolUnitOfWork,
	ordinal int,
) (protocolevent.NoticeItem, protocolevent.NewProtocolEvent) {
	item, event, result := commitLiveNotifierEventResult(t, unit, ordinal)
	if result.NotifyError != nil {
		t.Fatalf("notify committed event %d: %v", ordinal, result.NotifyError)
	}
	return item, event
}

func commitLiveNotifierEventResult(
	t *testing.T,
	unit *protocolevent.ProtocolUnitOfWork,
	ordinal int,
) (protocolevent.NoticeItem, protocolevent.NewProtocolEvent, protocolevent.UnitOfWorkResult) {
	t.Helper()
	item := unitOfWorkNotice("LIVE_NOTIFY")
	event := unitOfWorkStartedEvent(t, item)
	result, err := unit.Execute(context.Background(), func(
		ctx context.Context,
		transaction *protocolevent.ProtocolTransaction,
	) error {
		if _, err := transaction.CreateRunItem(ctx, unitOfWorkCreateInput(item, ordinal)); err != nil {
			return err
		}
		_, err := transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		t.Fatalf("commit live notifier event %d: %v", ordinal, err)
	}
	return item, event, result
}

func receiveLiveWakeup(t *testing.T, notifications <-chan protocolevent.LiveNotification) protocolevent.LiveNotification {
	t.Helper()
	select {
	case notification, open := <-notifications:
		if !open {
			t.Fatal("subscription closed before wakeup")
		}
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live notification")
		return protocolevent.LiveNotification{}
	}
}

func assertLiveSubscriptionClosed(t *testing.T, notifications <-chan protocolevent.LiveNotification) {
	t.Helper()
	select {
	case _, open := <-notifications:
		if open {
			t.Fatal("expected closed live subscription")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription cancellation")
	}
}

func assertMassLiveSubscriptionCancellation(t *testing.T, scope protocolevent.RunScope) {
	t.Helper()
	notifier := protocolevent.NewInProcessLiveNotifier()
	for index := 0; index < 256; index++ {
		ctx, cancel := context.WithCancel(context.Background())
		subscription, err := notifier.Subscribe(ctx, scope)
		if err != nil {
			t.Fatalf("subscribe %d: %v", index, err)
		}
		cancel()
		assertLiveSubscriptionClosed(t, subscription.Notifications())
	}
	if stats := notifier.Stats(); stats.ActiveSubscriptions != 0 {
		t.Fatalf("mass cancellation left subscriptions: %+v", stats)
	}
	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
}
