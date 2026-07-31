package sse_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
	"actweave/backend/internal/transport/sse"

	"github.com/google/uuid"
)

const (
	recoveryOwnerID        = "708f1f2e-7b5a-7c3d-8e9f-123456789001"
	recoveryWorkspaceID    = "708f1f2e-7b5a-7c3d-8e9f-123456789002"
	recoveryModelID        = "708f1f2e-7b5a-7c3d-8e9f-123456789003"
	recoveryAgentID        = "708f1f2e-7b5a-7c3d-8e9f-123456789004"
	recoveryConversationID = "708f1f2e-7b5a-7c3d-8e9f-123456789005"
	recoveryRunID          = "708f1f2e-7b5a-7c3d-8e9f-123456789006"
	recoveryStreamID       = "708f1f2e-7b5a-7c3d-8e9f-123456789007"
	recoveryItemID         = "708f1f2e-7b5a-7c3d-8e9f-123456789008"
	recoveryTraceID        = "trace-sse-recovery-acceptance"
)

// TestAAPSSERecoveryAcceptance is the executable recovery fault matrix for the
// v1 SSE transport. PostgreSQL is the source of truth; every process-local
// notifier, connection, reducer, and authorization monitor may be discarded.
func TestAAPSSERecoveryAcceptance(t *testing.T) {
	startedAt := time.Now()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 5 || version.Dirty {
		t.Fatalf("expected clean recovery schema version 2, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertRecoveryFixtures(t, db)

	scope := recoveryScope()
	writeNotifier := protocolevent.NewInProcessLiveNotifier()
	events := appendRecoveryTrace(t, db, writeNotifier, scope)
	if err := writeNotifier.Close(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 {
		t.Fatalf("persisted trace length=%d want=8", len(events))
	}

	var runtimeSideEffects atomic.Int64
	runtimeSideEffects.Add(1)
	expected := reduceRecoveryTrace(t, events)
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	policy := sse.DefaultBackpressurePolicy()
	policy.MaxPendingEvents = 2
	policy.WriteTimeout = 20 * time.Millisecond
	limiter, err := sse.NewInMemoryConnectionLimiter(policy)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("forced disconnect after every persisted sequence", func(t *testing.T) {
		for cursor := int64(0); cursor <= int64(len(events)); cursor++ {
			t.Run("after_sequence_"+recoverySequenceLabel(cursor), func(t *testing.T) {
				consumer := resumePersistentTrace(t, reader, scope, cursor, limiter, true)
				assertRecoveredTrace(t, consumer, expected, events)
				if cursor > 0 && consumer.duplicates != 1 {
					t.Fatalf("cursor %d duplicate deliveries=%d want=1", cursor, consumer.duplicates)
				}
				if runtimeSideEffects.Load() != 1 {
					t.Fatalf("replay repeated runtime side effect: %d", runtimeSideEffects.Load())
				}
			})
		}
	})

	t.Run("dropped notification is recovered by authoritative polling", func(t *testing.T) {
		fakeReader := newFollowReader(followEvent(1, protocolevent.EventRunStarted))
		notifier := newFakeFollowNotifier()
		follow := newTestFollow(t, fakeReader, notifier)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		received := make(chan protocolevent.ProtocolEvent, 3)
		result := make(chan error, 1)
		go func() {
			result <- follow.Follow(ctx, followScope(), 0, func(page []protocolevent.ProtocolEvent) error {
				for _, event := range page {
					received <- event
				}
				return nil
			})
		}()
		assertFollowSequences(t, received, 1)
		fakeReader.append(followEvent(2, protocolevent.EventItemStarted))
		assertFollowSequences(t, received, 2)
		fakeReader.append(followEvent(3, protocolevent.EventRunCompleted))
		assertFollowSequences(t, received, 3)
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("poll recovery goroutine did not terminate")
		}
		if notifier.activeCount() != 0 {
			t.Fatalf("dropped notification recovery leaked subscription: %d", notifier.activeCount())
		}
	})

	t.Run("token renewal resumes without advancing cursor", func(t *testing.T) {
		changes := agentaccessauth.NewInProcessSecurityChanges()
		defer changes.Close()
		authorizer := agentaccessauth.NewControlledStreamAuthorizer()
		revalidator, err := agentaccessauth.NewStreamRevalidator(
			authorizer, changes, agentaccessauth.RevalidationPolicy{Interval: time.Second},
		)
		if err != nil {
			t.Fatal(err)
		}
		binding := recoveryBinding(time.Now().UTC().Add(30 * time.Millisecond))
		err = revalidator.Monitor(context.Background(), binding)
		if !errors.Is(err, agentaccessauth.ErrTokenExpired) ||
			agentaccessauth.StreamErrorCode(err) != "TOKEN_EXPIRED" {
			t.Fatalf("expired token result=%v", err)
		}
		var signal bytes.Buffer
		if err := sse.NewEncoder().EncodeStreamError(&signal, sse.NewStreamErrorSignal(
			"TOKEN_EXPIRED", "access token expired", true,
			"request-recovery", recoveryTraceID, nil, time.Now().UTC(),
		)); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(signal.String(), "id: ") {
			t.Fatalf("token transport signal advanced cursor: %s", signal.String())
		}

		fresh := binding
		fresh.TokenExpiresAt = time.Now().UTC().Add(time.Second)
		monitorCtx, cancelMonitor := context.WithCancel(context.Background())
		monitorResult := make(chan error, 1)
		go func() { monitorResult <- revalidator.Monitor(monitorCtx, fresh) }()
		waitForRecoverySecuritySubscription(t, changes)
		consumer := resumePersistentTrace(t, reader, scope, 4, limiter, false)
		assertRecoveredTrace(t, consumer, expected, events)
		cancelMonitor()
		select {
		case err := <-monitorResult:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("fresh token monitor result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("fresh token monitor goroutine did not terminate")
		}
		if stats := changes.Stats(); stats.ActiveSubscriptions != 0 {
			t.Fatalf("token renewal leaked authorization subscription: %+v", stats)
		}
	})

	t.Run("slow consumer disconnects and resumes from last acknowledged cursor", func(t *testing.T) {
		lease, err := limiter.Acquire(context.Background(), recoveryConnection(scope.RunID))
		if err != nil {
			t.Fatal(err)
		}
		server, client := net.Pipe()
		writer, err := sse.NewDeadlineWriter(server, server.SetWriteDeadline, policy.WriteTimeout)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- sse.NewEncoder().Encode(writer, events[4]) }()
		select {
		case err := <-result:
			if !errors.Is(err, sse.ErrSlowConsumer) {
				t.Fatalf("slow consumer result=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("slow consumer writer goroutine did not terminate")
		}
		_ = server.Close()
		_ = client.Close()
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}

		consumer := resumePersistentTrace(t, reader, scope, 4, limiter, false)
		assertRecoveredTrace(t, consumer, expected, events)
	})

	t.Run("service object restart reopens the persisted event store", func(t *testing.T) {
		restartedDB, err := sql.Open("postgres", testDatabase.DSN())
		if err != nil {
			t.Fatal(err)
		}
		defer restartedDB.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := restartedDB.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		restartedReader, err := protocolevent.NewEventReader(restartedDB)
		if err != nil {
			t.Fatal(err)
		}
		consumer := resumePersistentTrace(t, restartedReader, scope, 3, limiter, false)
		assertRecoveredTrace(t, consumer, expected, events)
		if stats := restartedDB.Stats(); stats.InUse != 0 {
			t.Fatalf("restarted database connection remained in use: %+v", stats)
		}
	})

	if runtimeSideEffects.Load() != 1 {
		t.Fatalf("acceptance replay repeated runtime side effect: %d", runtimeSideEffects.Load())
	}
	if stats := limiter.Stats(); stats.Active != 0 || stats.Acquired != stats.Released {
		t.Fatalf("recovery leaked SSE connections: %+v", stats)
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("recovery left database connections in use: %+v", stats)
	}
	t.Logf("AAP SSE recovery baseline: events=%d forced_disconnects=%d duration=%s connections=%d",
		len(events), len(events)+1, time.Since(startedAt), limiter.Stats().Acquired)
}

type recoveryConsumer struct {
	seen       map[string]struct{}
	events     []protocolevent.ProtocolEvent
	frames     bytes.Buffer
	duplicates int
}

func newRecoveryConsumer() *recoveryConsumer {
	return &recoveryConsumer{seen: make(map[string]struct{})}
}

func (consumer *recoveryConsumer) deliver(page []protocolevent.ProtocolEvent) error {
	for _, event := range page {
		if err := sse.NewEncoder().Encode(&consumer.frames, event); err != nil {
			return err
		}
		if _, duplicate := consumer.seen[event.ID]; duplicate {
			consumer.duplicates++
			continue
		}
		consumer.seen[event.ID] = struct{}{}
		consumer.events = append(consumer.events, event)
	}
	return nil
}

func resumePersistentTrace(
	t *testing.T,
	reader sse.FollowEventReader,
	scope protocolevent.RunScope,
	cursor int64,
	limiter *sse.InMemoryConnectionLimiter,
	injectDuplicate bool,
) *recoveryConsumer {
	t.Helper()
	lease, err := limiter.Acquire(context.Background(), recoveryConnection(scope.RunID))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	consumer := newRecoveryConsumer()
	if cursor > 0 {
		prefix, err := reader.ReadRunAfter(context.Background(), scope, 0, int(cursor))
		if err != nil {
			t.Fatal(err)
		}
		if len(prefix) != int(cursor) {
			t.Fatalf("prefix length=%d want=%d", len(prefix), cursor)
		}
		if err := consumer.deliver(prefix); err != nil {
			t.Fatal(err)
		}
		if injectDuplicate {
			if err := consumer.deliver(prefix[len(prefix)-1:]); err != nil {
				t.Fatal(err)
			}
		}
	}

	notifier := protocolevent.NewInProcessLiveNotifier()
	follow, err := sse.NewCatchUpFollow(reader, notifier, sse.FollowPolicy{
		PageSize: 2, PollInterval: 2 * time.Millisecond, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := follow.Follow(ctx, scope, cursor, consumer.deliver); err != nil {
		t.Fatal(err)
	}
	if stats := notifier.Stats(); stats.ActiveSubscriptions != 0 {
		t.Fatalf("follow leaked live subscription: %+v", stats)
	}
	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
	return consumer
}

func assertRecoveredTrace(
	t *testing.T,
	consumer *recoveryConsumer,
	expected protocolevent.ReducedRunSnapshot,
	persisted []protocolevent.ProtocolEvent,
) {
	t.Helper()
	if len(consumer.events) != len(persisted) || len(consumer.seen) != len(persisted) {
		t.Fatalf("unique recovered events=%d seen=%d want=%d",
			len(consumer.events), len(consumer.seen), len(persisted))
	}
	for index, event := range consumer.events {
		wantSequence := int64(index + 1)
		if event.Sequence != wantSequence || event.ID != persisted[index].ID {
			t.Fatalf("recovered event[%d]=%s/%d want=%s/%d",
				index, event.ID, event.Sequence, persisted[index].ID, wantSequence)
		}
	}
	reducer := protocolevent.NewRunReducer()
	if err := reducer.ApplyAll(consumer.events); err != nil {
		t.Fatal(err)
	}
	actual, err := reducer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("recovered terminal snapshot differs\nactual=%+v\nexpected=%+v", actual, expected)
	}
	if strings.Count(consumer.frames.String(), "id: ") != len(persisted)+consumer.duplicates {
		t.Fatalf("unexpected persisted SSE frame count: %s", consumer.frames.String())
	}
}

func reduceRecoveryTrace(t *testing.T, events []protocolevent.ProtocolEvent) protocolevent.ReducedRunSnapshot {
	t.Helper()
	reducer := protocolevent.NewRunReducer()
	if err := reducer.ApplyAll(events); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reducer.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func appendRecoveryTrace(
	t *testing.T,
	db *sql.DB,
	notifier protocolevent.CommitNotifier,
	scope protocolevent.RunScope,
) []protocolevent.ProtocolEvent {
	t.Helper()
	started := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	completed := started.Add(8 * time.Second)
	acceptedRun := protocolevent.Run{
		ID: scope.RunID, ConversationID: scope.ConversationID, AgentID: scope.AgentID,
		Status: protocolevent.RunStatusAccepted, Trigger: protocolevent.RunTriggerAPI,
		StartedAt: started,
	}
	runningRun := acceptedRun
	runningRun.Status = protocolevent.RunStatusRunning
	completedRun := runningRun
	completedRun.Status = protocolevent.RunStatusCompleted
	completedRun.CompletedAt = &completed
	startedItem := protocolevent.MessageItem{
		ID: recoveryItemID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusInProgress, Role: protocolevent.MessageRoleAssistant,
		Content: []protocolevent.ContentPart{protocolevent.TextContentPart{
			Type: protocolevent.ContentPartTypeText, Text: "",
		}},
	}
	completedItem := startedItem
	completedItem.Status = protocolevent.ItemStatusCompleted
	completedItem.Content = []protocolevent.ContentPart{protocolevent.TextContentPart{
		Type: protocolevent.ContentPartTypeText, Text: "你好，ActWeave",
	}}

	inputs := []struct {
		eventType string
		itemID    string
		data      protocolevent.EventData
	}{
		{protocolevent.EventRunAccepted, "", protocolevent.RunSnapshotData{Run: acceptedRun}},
		{protocolevent.EventRunStarted, "", protocolevent.RunSnapshotData{Run: runningRun}},
		{protocolevent.EventItemStarted, recoveryItemID, protocolevent.ItemSnapshotData{Item: startedItem}},
		{protocolevent.EventItemDelta, recoveryItemID, protocolevent.ItemDeltaData{
			ItemID: recoveryItemID, Delta: protocolevent.TextDelta{
				Type: protocolevent.DeltaTypeText, Index: 0, Text: "你好，",
			},
		}},
		{protocolevent.EventItemDelta, recoveryItemID, protocolevent.ItemDeltaData{
			ItemID: recoveryItemID, Delta: protocolevent.TextDelta{
				Type: protocolevent.DeltaTypeText, Index: 0, Text: "ActWeave",
			},
		}},
		{protocolevent.EventItemCompleted, recoveryItemID, protocolevent.ItemSnapshotData{Item: completedItem}},
		{protocolevent.EventUsageUpdated, "", protocolevent.UsageData{Usage: protocolevent.Usage{
			InputTokens: 7, OutputTokens: 5, TotalTokens: 12,
		}}},
		{protocolevent.EventRunCompleted, "", protocolevent.RunSnapshotData{Run: completedRun}},
	}
	toAppend := make([]protocolevent.NewProtocolEvent, 0, len(inputs))
	for index, input := range inputs {
		built, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
			ID: uuid.NewString(), EventStreamID: recoveryStreamID,
			WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
			ConversationID: scope.ConversationID, RunID: scope.RunID,
			Type: input.eventType, SpecVersion: protocolschema.SpecVersion,
			TraceID: recoveryTraceID, ItemID: input.itemID,
			OccurredAt: started.Add(time.Duration(index+1) * time.Second),
		}, input.data)
		if err != nil {
			t.Fatalf("build recovery event %d: %v", index+1, err)
		}
		toAppend = append(toAppend, built)
	}
	unit, err := protocolevent.NewProtocolUnitOfWork(db, notifier)
	if err != nil {
		t.Fatal(err)
	}
	result, err := unit.Execute(context.Background(), func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, recoveryStreamID, scope); err != nil {
			return err
		}
		_, err := transaction.Append(ctx, toAppend)
		return err
	})
	if err != nil || result.NotifyError != nil {
		t.Fatalf("persist recovery trace: err=%v notify=%v", err, result.NotifyError)
	}
	return result.Events
}

func insertRecoveryFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,username,display_name) VALUES($1,'recovery.owner','Recovery Owner')`, []any{recoveryOwnerID}},
		{`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		  VALUES($1,'recovery-space','Recovery Space','PRODUCTION',$2,$2,$2)`, []any{recoveryWorkspaceID, recoveryOwnerID}},
		{`INSERT INTO model_configs(
		    id,workspace_id,name,provider,api_base,model_name,created_by,updated_by
		  ) VALUES($1,$2,'Recovery Model','openai','https://models.example.test','recovery-model',$3,$3)`, []any{recoveryModelID, recoveryWorkspaceID, recoveryOwnerID}},
		{`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		  VALUES($1,$2,'Recovery Agent',$3,$4,$4)`, []any{recoveryAgentID, recoveryWorkspaceID, recoveryModelID, recoveryOwnerID}},
		{`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		  VALUES($1,$2,$3,'Recovery session',$4)`, []any{recoveryConversationID, recoveryWorkspaceID, recoveryAgentID, recoveryOwnerID}},
		{`INSERT INTO agent_runs(
		    id,workspace_id,session_id,agent_id,status,trigger_type,triggered_by_type,
		    triggered_by_id,trace_id,model_snapshot,capability_snapshot
		  ) VALUES($1,$2,$3,$4,'RUNNING','API','USER',$5,$6,'{}','{}')`, []any{
			recoveryRunID, recoveryWorkspaceID, recoveryConversationID, recoveryAgentID,
			recoveryOwnerID, recoveryTraceID,
		}},
	}
	for index, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert recovery fixture %d: %v", index+1, err)
		}
	}
}

func recoveryScope() protocolevent.RunScope {
	return protocolevent.RunScope{
		WorkspaceID: recoveryWorkspaceID, AgentID: recoveryAgentID,
		ConversationID: recoveryConversationID, RunID: recoveryRunID,
	}
}

func recoveryConnection(runID string) sse.ConnectionIdentity {
	return sse.ConnectionIdentity{
		ClientID: "recovery-client", SubjectID: "recovery-subject", RunID: runID,
	}
}

func recoveryBinding(expiresAt time.Time) agentaccessauth.StreamBinding {
	return agentaccessauth.StreamBinding{
		WorkspaceID: recoveryWorkspaceID, AgentID: recoveryAgentID,
		ClientID: "recovery-client", GrantID: "recovery-grant",
		PrincipalID: "recovery-principal", SubjectID: "recovery-subject",
		SecurityVersion: 1, TokenExpiresAt: expiresAt,
	}
}

func waitForRecoverySecuritySubscription(
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
	t.Fatal("authorization monitor did not subscribe")
}

func recoverySequenceLabel(sequence int64) string {
	if sequence == 0 {
		return "zero"
	}
	const digits = "0123456789"
	if sequence < int64(len(digits)) {
		return string(digits[sequence])
	}
	return "many"
}
