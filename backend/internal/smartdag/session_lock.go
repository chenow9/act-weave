package smartdag

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

// SessionLock is a held session-scoped exclusive lock that must be released.
type SessionLock interface {
	// Unlock releases the lock. Safe to call once; subsequent calls are no-ops.
	Unlock(ctx context.Context) error
}

// SessionLocker acquires non-blocking session locks (try-lock semantics).
// Busy must return ErrTurnInProgress immediately (no queue).
type SessionLocker interface {
	TryLock(ctx context.Context, workspaceID, sessionID string) (SessionLock, error)
}

// MemorySessionLocker is an in-process try-lock for unit tests.
type MemorySessionLocker struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// NewMemorySessionLocker constructs an empty memory locker.
func NewMemorySessionLocker() *MemorySessionLocker {
	return &MemorySessionLocker{held: make(map[string]struct{})}
}

func (l *MemorySessionLocker) TryLock(_ context.Context, workspaceID, sessionID string) (SessionLock, error) {
	if l == nil {
		return noopSessionLock{}, nil
	}
	key := sessionKey(workspaceID, sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, busy := l.held[key]; busy {
		return nil, ErrTurnInProgress
	}
	l.held[key] = struct{}{}
	return &memorySessionLock{parent: l, key: key}, nil
}

type memorySessionLock struct {
	parent *MemorySessionLocker
	key    string
	once   sync.Once
}

func (m *memorySessionLock) Unlock(context.Context) error {
	if m == nil || m.parent == nil {
		return nil
	}
	m.once.Do(func() {
		m.parent.mu.Lock()
		delete(m.parent.held, m.key)
		m.parent.mu.Unlock()
	})
	return nil
}

type noopSessionLock struct{}

func (noopSessionLock) Unlock(context.Context) error { return nil }

// SQLSessionLocker holds PostgreSQL session advisory locks on a dedicated Conn.
// Context cancel / connection close releases the lock.
type SQLSessionLocker struct {
	db *sql.DB
}

// NewSQLSessionLocker constructs a SQL-backed session locker.
func NewSQLSessionLocker(db *sql.DB) (*SQLSessionLocker, error) {
	if db == nil {
		return nil, errors.New("session locker database is required")
	}
	return &SQLSessionLocker{db: db}, nil
}

// sessionAdvisoryKey derives a stable 64-bit key from workspace+session.
// Uses FNV-1a 64 over "workspace\x00session" (not cryptographic).
func sessionAdvisoryKey(workspaceID, sessionID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(workspaceID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(sessionID)))
	// Interpret as signed int64 for pg advisory lock bigint.
	sum := h.Sum64()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], sum)
	return int64(binary.BigEndian.Uint64(buf[:]))
}

func (l *SQLSessionLocker) TryLock(ctx context.Context, workspaceID, sessionID string) (SessionLock, error) {
	if l == nil || l.db == nil {
		return nil, errors.New("session locker is not configured")
	}
	// Dedicated connection so the lock is not shared across pool clients.
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := sessionAdvisoryKey(workspaceID, sessionID)
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !ok {
		_ = conn.Close()
		return nil, ErrTurnInProgress
	}
	return &sqlSessionLock{conn: conn, key: key}, nil
}

type sqlSessionLock struct {
	conn *sql.Conn
	key  int64
	once sync.Once
}

func (s *sqlSessionLock) Unlock(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var unlockErr error
	s.once.Do(func() {
		if s.conn == nil {
			return
		}
		// Best-effort unlock; always close the dedicated connection.
		unlockCtx := ctx
		if unlockCtx == nil {
			unlockCtx = context.Background()
		}
		// Bound unlock so a cancelled parent still releases.
		if _, hasDeadline := unlockCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			unlockCtx, cancel = context.WithTimeout(context.WithoutCancel(unlockCtx), 3*time.Second)
			defer cancel()
		}
		var ok bool
		if err := s.conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, s.key).Scan(&ok); err != nil {
			unlockErr = err
		}
		if closeErr := s.conn.Close(); closeErr != nil && unlockErr == nil {
			unlockErr = closeErr
		}
		s.conn = nil
	})
	return unlockErr
}
