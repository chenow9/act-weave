package audit_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/audit"
	"actweave/backend/internal/database/dbtest"

	"github.com/google/uuid"
)

func TestOutboxWorkerClaimsPublishesRetriesAndStops(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 20 || version.Dirty {
		t.Fatalf("outbox worker migration = %+v", version)
	}
	db := testDatabase.Open(t)
	db.SetMaxOpenConns(24)
	insertAuditMigrationFixtures(t, db)
	repository, _ := audit.NewOutboxRepository(db)
	ctx := context.Background()
	publisher := newWorkerPublisher()
	config := audit.OutboxWorkerConfig{
		PollInterval: time.Millisecond, ClaimLease: time.Second,
		Backoff: fixedBackoff(time.Hour),
	}

	const eventCount = 16
	for index := 0; index < eventCount; index++ {
		input := workerOutboxInput(index)
		if result, err := repository.Append(ctx, input); err != nil || !result.Created {
			t.Fatalf("append worker event %d: %+v err=%v", index, result, err)
		}
	}
	const workerCount = 4
	workers := make([]*audit.OutboxWorker, workerCount)
	for index := range workers {
		workers[index], _ = audit.NewOutboxWorker(repository, publisher, config)
	}
	var wait sync.WaitGroup
	for _, worker := range workers {
		wait.Add(1)
		go func(worker *audit.OutboxWorker) {
			defer wait.Done()
			for {
				processed, err := worker.ProcessOne(ctx)
				if err != nil {
					t.Errorf("process outbox event: %v", err)
					return
				}
				if !processed {
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	if publisher.concurrentDuplicate || publisher.totalCalls() != eventCount {
		t.Fatalf("outbox publish calls=%d concurrentDuplicate=%v counts=%+v",
			publisher.totalCalls(), publisher.concurrentDuplicate, publisher.counts)
	}
	var publishedCount, wrongAttempts, claimedCount int
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL`).Scan(&publishedCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events WHERE attempts <> 1`).Scan(&wrongAttempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox_events WHERE claim_token IS NOT NULL`).Scan(&claimedCount); err != nil {
		t.Fatal(err)
	}
	if publishedCount != eventCount || wrongAttempts != 0 || claimedCount != 0 {
		t.Fatalf("published/wrongAttempts/claimed=%d/%d/%d", publishedCount, wrongAttempts, claimedCount)
	}
	if processed, err := workers[0].ProcessOne(ctx); err != nil || processed {
		t.Fatalf("published event was reclaimed: processed=%v err=%v", processed, err)
	}

	failureInput := workerOutboxInput(100)
	failureInput.ID = "a68f1f2e-7b5a-7c3d-8e9f-123456789001"
	failureInput.IdempotencyKey = "worker-failure"
	if _, err := repository.Append(ctx, failureInput); err != nil {
		t.Fatal(err)
	}
	publisher.failOnce[failureInput.ID] = true
	if processed, err := workers[0].ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("process failing event: processed=%v err=%v", processed, err)
	}
	var attempts int
	var lastError string
	var published bool
	if err := db.QueryRow(`SELECT attempts,last_error,published_at IS NOT NULL
		FROM outbox_events WHERE id=$1`, failureInput.ID).Scan(&attempts, &lastError, &published); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError != "OUTBOX_PUBLISH_FAILED" || published {
		t.Fatalf("failed event attempts/error/published=%d/%q/%v", attempts, lastError, published)
	}
	if processed, err := workers[0].ProcessOne(ctx); err != nil || processed {
		t.Fatalf("backoff event was reclaimed early: processed=%v err=%v", processed, err)
	}
	if _, err := db.Exec(`UPDATE outbox_events SET available_at=clock_timestamp() WHERE id=$1`, failureInput.ID); err != nil {
		t.Fatal(err)
	}
	if processed, err := workers[0].ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("retry due event: processed=%v err=%v", processed, err)
	}
	if err := db.QueryRow(`SELECT attempts,last_error IS NULL,published_at IS NOT NULL
		FROM outbox_events WHERE id=$1`, failureInput.ID).Scan(&attempts, new(bool), &published); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !published || publisher.callCount(failureInput.ID) != 2 {
		t.Fatalf("retried event attempts/published/calls=%d/%v/%d",
			attempts, published, publisher.callCount(failureInput.ID))
	}

	leaseInput := workerOutboxInput(101)
	leaseInput.ID = "a68f1f2e-7b5a-7c3d-8e9f-123456789002"
	leaseInput.IdempotencyKey = "worker-expired-lease"
	if _, err := repository.Append(ctx, leaseInput); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE outbox_events SET claim_token=$2,claimed_at=clock_timestamp(),
		claim_expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1`,
		leaseInput.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if processed, err := workers[0].ProcessOne(ctx); err != nil || processed {
		t.Fatalf("live lease reclaimed: processed=%v err=%v", processed, err)
	}
	if _, err := db.Exec(`UPDATE outbox_events
		SET claimed_at=clock_timestamp()-interval '2 seconds',
		 claim_expires_at=clock_timestamp()-interval '1 second'
		WHERE id=$1`, leaseInput.ID); err != nil {
		t.Fatal(err)
	}
	if processed, err := workers[0].ProcessOne(ctx); err != nil || !processed {
		t.Fatalf("expired lease recovery: processed=%v err=%v", processed, err)
	}

	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- workers[0].Run(runContext) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful worker stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("outbox worker did not stop after cancellation")
	}

}

type fixedBackoff time.Duration

func (backoff fixedBackoff) NextDelay(int) time.Duration { return time.Duration(backoff) }

type workerPublisher struct {
	mu                  sync.Mutex
	counts              map[string]int
	active              map[string]bool
	failOnce            map[string]bool
	concurrentDuplicate bool
}

func newWorkerPublisher() *workerPublisher {
	return &workerPublisher{
		counts: map[string]int{}, active: map[string]bool{}, failOnce: map[string]bool{},
	}
}

func (publisher *workerPublisher) PublishOutbox(_ context.Context, event audit.OutboxEvent) error {
	publisher.mu.Lock()
	if publisher.active[event.ID] {
		publisher.concurrentDuplicate = true
	}
	publisher.active[event.ID] = true
	publisher.counts[event.ID]++
	shouldFail := publisher.failOnce[event.ID]
	delete(publisher.failOnce, event.ID)
	publisher.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	publisher.mu.Lock()
	delete(publisher.active, event.ID)
	publisher.mu.Unlock()
	if shouldFail {
		return errors.New("publisher response included private upstream detail")
	}
	return nil
}

func (publisher *workerPublisher) totalCalls() int {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	total := 0
	for _, count := range publisher.counts {
		total += count
	}
	return total
}

func (publisher *workerPublisher) callCount(eventID string) int {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return publisher.counts[eventID]
}

func workerOutboxInput(index int) audit.AppendOutboxInput {
	eventID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("worker-event-%d", index))).String()
	return audit.AppendOutboxInput{
		ID: eventID, WorkspaceID: auditMigrationWorkspaceID,
		AggregateType: "TOOL", AggregateID: outboxAggregateID,
		EventType:     "tool.release.published",
		Payload:       []byte(`{"schemaVersion":"tool.release.v1","releaseNo":1}`),
		SchemaVersion: "tool.release.v1", IdempotencyKey: "worker:" + eventID,
	}
}
