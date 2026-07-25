package smartdag

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemorySessionLocker_TryLockBusy(t *testing.T) {
	t.Parallel()
	locker := NewMemorySessionLocker()
	ctx := context.Background()
	lock1, err := locker.TryLock(ctx, "ws-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = locker.TryLock(ctx, "ws-1", "sess-1")
	if !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("want ErrTurnInProgress, got %v", err)
	}
	// Different session is fine.
	lock2, err := locker.TryLock(ctx, "ws-1", "sess-2")
	if err != nil {
		t.Fatal(err)
	}
	_ = lock1.Unlock(ctx)
	// After unlock, same session can lock again.
	lock3, err := locker.TryLock(ctx, "ws-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = lock2.Unlock(ctx)
	_ = lock3.Unlock(ctx)
}

func TestMemorySessionLocker_ConcurrentTry(t *testing.T) {
	t.Parallel()
	locker := NewMemorySessionLocker()
	ctx := context.Background()
	var wg sync.WaitGroup
	var successes, busy atomicCounter
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := locker.TryLock(ctx, "ws", "sess")
			if errors.Is(err, ErrTurnInProgress) {
				busy.inc()
				return
			}
			if err != nil {
				t.Errorf("unexpected: %v", err)
				return
			}
			successes.inc()
			time.Sleep(20 * time.Millisecond)
			_ = lock.Unlock(ctx)
		}()
	}
	wg.Wait()
	if successes.value() < 1 {
		t.Fatal("expected at least one success")
	}
	if successes.value()+busy.value() != 16 {
		t.Fatalf("success=%d busy=%d", successes.value(), busy.value())
	}
}

func TestSessionAdvisoryKey_StableAndDistinct(t *testing.T) {
	t.Parallel()
	a := sessionAdvisoryKey("ws-1", "sess-1")
	b := sessionAdvisoryKey("ws-1", "sess-1")
	c := sessionAdvisoryKey("ws-1", "sess-2")
	if a != b {
		t.Fatal("key must be stable")
	}
	if a == c {
		t.Fatal("different sessions must differ")
	}
}

func TestClaimAndAdvanceLockVersion(t *testing.T) {
	t.Parallel()
	store := NewMemorySessionStore()
	ctx := context.Background()
	session, err := store.CreateSession(ctx, GenerateSession{
		ID: "55000000-0000-4000-8000-000000000001", WorkspaceID: "11000000-0000-4000-8000-000000000001",
		AgentID: "22000000-0000-4000-8000-000000000001", ModelConfigID: "33000000-0000-4000-8000-000000000001",
		CreatedBy: "44000000-0000-4000-8000-000000000001", LockVersion: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := session.LockVersion
	claimed, err := store.ClaimSessionLockVersion(ctx, session.WorkspaceID, session.ID, &expected)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LockVersion != 2 {
		t.Fatalf("claimed=%d want 2", claimed.LockVersion)
	}
	// Stale expected fails.
	stale := int64(1)
	if _, err := store.ClaimSessionLockVersion(ctx, session.WorkspaceID, session.ID, &stale); !errors.Is(err, ErrSessionVersionConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	advanced, err := store.AdvanceSessionLockVersion(ctx, session.WorkspaceID, session.ID, claimed.LockVersion)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.LockVersion != 3 {
		t.Fatalf("advanced=%d want 3 (N→N+1→N+2)", advanced.LockVersion)
	}
}

type atomicCounter struct {
	mu sync.Mutex
	n  int
}

func (c *atomicCounter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *atomicCounter) value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
