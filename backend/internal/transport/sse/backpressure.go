package sse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var (
	ErrBackpressureInvalid     = errors.New("AAP SSE backpressure input is invalid")
	ErrSlowConsumer            = errors.New("AAP SSE consumer write timed out")
	ErrConnectionLimitExceeded = errors.New("AAP SSE connection limit exceeded")
)

type BackpressurePolicy struct {
	MaxPendingEvents         int
	MaxCatchUpBatches        int
	WriteTimeout             time.Duration
	MaxConnectionsPerClient  int
	MaxConnectionsPerSubject int
	MaxConnectionsPerRun     int
}

func DefaultBackpressurePolicy() BackpressurePolicy {
	return BackpressurePolicy{
		MaxPendingEvents: 100, MaxCatchUpBatches: 1000,
		WriteTimeout:            5 * time.Second,
		MaxConnectionsPerClient: 16, MaxConnectionsPerSubject: 8,
		MaxConnectionsPerRun: 4,
	}
}

func (policy BackpressurePolicy) Validate() error {
	if policy.MaxPendingEvents < 1 || policy.MaxPendingEvents > 500 ||
		policy.MaxCatchUpBatches < 1 || policy.WriteTimeout <= 0 ||
		policy.MaxConnectionsPerClient < 1 || policy.MaxConnectionsPerSubject < 1 ||
		policy.MaxConnectionsPerRun < 1 {
		return ErrBackpressureInvalid
	}
	return nil
}

type DeadlineWriter struct {
	writer      io.Writer
	setDeadline func(time.Time) error
	timeout     time.Duration
	now         func() time.Time
}

func NewDeadlineWriter(
	writer io.Writer,
	setDeadline func(time.Time) error,
	timeout time.Duration,
) (*DeadlineWriter, error) {
	if writer == nil || setDeadline == nil || timeout <= 0 {
		return nil, ErrBackpressureInvalid
	}
	return &DeadlineWriter{
		writer: writer, setDeadline: setDeadline, timeout: timeout,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (writer *DeadlineWriter) Write(payload []byte) (int, error) {
	if writer == nil || writer.writer == nil || writer.setDeadline == nil || writer.timeout <= 0 {
		return 0, ErrBackpressureInvalid
	}
	if err := writer.setDeadline(writer.now().Add(writer.timeout)); err != nil {
		return 0, fmt.Errorf("set AAP SSE write deadline: %w", err)
	}
	written, err := writer.writer.Write(payload)
	if err != nil {
		var networkError interface{ Timeout() bool }
		if errors.As(err, &networkError) && networkError.Timeout() {
			return written, fmt.Errorf("%w: %w", ErrSlowConsumer, err)
		}
		return written, err
	}
	if written != len(payload) {
		return written, io.ErrShortWrite
	}
	return written, nil
}

type ConnectionIdentity struct {
	ClientID  string
	SubjectID string
	RunID     string
}

type ConnectionLease interface {
	Close() error
}

type ConnectionLimiter interface {
	Acquire(context.Context, ConnectionIdentity) (ConnectionLease, error)
	Stats() ConnectionLimiterStats
}

type ConnectionLimiterStats struct {
	Active   int
	Acquired uint64
	Released uint64
	Rejected uint64
}

type InMemoryConnectionLimiter struct {
	mu       sync.Mutex
	policy   BackpressurePolicy
	clients  map[string]int
	subjects map[string]int
	runs     map[string]int
	stats    ConnectionLimiterStats
}

func NewInMemoryConnectionLimiter(
	policy BackpressurePolicy,
) (*InMemoryConnectionLimiter, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &InMemoryConnectionLimiter{
		policy: policy, clients: make(map[string]int),
		subjects: make(map[string]int), runs: make(map[string]int),
	}, nil
}

func (limiter *InMemoryConnectionLimiter) Acquire(
	ctx context.Context,
	identity ConnectionIdentity,
) (ConnectionLease, error) {
	if limiter == nil || ctx == nil {
		return nil, ErrBackpressureInvalid
	}
	identity = normalizeConnectionIdentity(identity)
	if !validConnectionIdentity(identity) {
		return nil, ErrBackpressureInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.clients[identity.ClientID] >= limiter.policy.MaxConnectionsPerClient ||
		limiter.subjects[identity.SubjectID] >= limiter.policy.MaxConnectionsPerSubject ||
		limiter.runs[identity.RunID] >= limiter.policy.MaxConnectionsPerRun {
		limiter.stats.Rejected++
		return nil, ErrConnectionLimitExceeded
	}
	limiter.clients[identity.ClientID]++
	limiter.subjects[identity.SubjectID]++
	limiter.runs[identity.RunID]++
	limiter.stats.Active++
	limiter.stats.Acquired++
	return &inMemoryConnectionLease{limiter: limiter, identity: identity}, nil
}

func (limiter *InMemoryConnectionLimiter) Stats() ConnectionLimiterStats {
	if limiter == nil {
		return ConnectionLimiterStats{}
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.stats
}

type inMemoryConnectionLease struct {
	limiter  *InMemoryConnectionLimiter
	identity ConnectionIdentity
	once     sync.Once
}

func (lease *inMemoryConnectionLease) Close() error {
	if lease == nil || lease.limiter == nil {
		return ErrBackpressureInvalid
	}
	lease.once.Do(func() {
		limiter := lease.limiter
		limiter.mu.Lock()
		decrementConnectionCount(limiter.clients, lease.identity.ClientID)
		decrementConnectionCount(limiter.subjects, lease.identity.SubjectID)
		decrementConnectionCount(limiter.runs, lease.identity.RunID)
		limiter.stats.Active--
		limiter.stats.Released++
		limiter.mu.Unlock()
	})
	return nil
}

func decrementConnectionCount(counts map[string]int, key string) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}

func normalizeConnectionIdentity(identity ConnectionIdentity) ConnectionIdentity {
	identity.ClientID = strings.TrimSpace(identity.ClientID)
	identity.SubjectID = strings.TrimSpace(identity.SubjectID)
	identity.RunID = strings.TrimSpace(identity.RunID)
	return identity
}

func validConnectionIdentity(identity ConnectionIdentity) bool {
	for _, value := range []string{identity.ClientID, identity.SubjectID, identity.RunID} {
		if value == "" || len(value) > 128 {
			return false
		}
		for _, character := range value {
			if character < 0x20 || character == 0x7f {
				return false
			}
		}
	}
	return true
}
