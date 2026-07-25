package agentaccess

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type DataPlaneQuotaOperation string

const (
	QuotaConversationCreate DataPlaneQuotaOperation = "conversation.create"
	QuotaRunCreate          DataPlaneQuotaOperation = "run.create"
	QuotaRunCancel          DataPlaneQuotaOperation = "run.cancel"
	QuotaInteractionDecide  DataPlaneQuotaOperation = "interaction.decide"
	// QuotaEventStream bounds SSE open rate (distinct from concurrent stream leases).
	QuotaEventStream DataPlaneQuotaOperation = "event.stream"
)

var (
	ErrDataPlaneQuotaInvalid  = errors.New("AAP data-plane quota request is invalid")
	ErrDataPlaneQuotaExceeded = errors.New("AAP data-plane quota exceeded")
)

type DataPlaneQuotaRequest struct {
	Operation          DataPlaneQuotaOperation
	WorkspaceID        string
	AgentID            string
	ClientID           string
	ServicePrincipalID string
	SubjectID          string
}

type DataPlaneQuotaDecision struct {
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

type DataPlaneQuota interface {
	Allow(context.Context, DataPlaneQuotaRequest) (DataPlaneQuotaDecision, error)
}

type DataPlaneQuotaConfig struct {
	Window     time.Duration
	MaxEntries int
	Limits     map[DataPlaneQuotaOperation]int
}

func DefaultDataPlaneQuotaConfig() DataPlaneQuotaConfig {
	return DataPlaneQuotaConfig{
		Window: time.Minute, MaxEntries: 100_000,
		Limits: map[DataPlaneQuotaOperation]int{
			QuotaConversationCreate: 60,
			QuotaRunCreate:          30,
			QuotaRunCancel:          60,
			QuotaInteractionDecide:  60,
			QuotaEventStream:        120,
		},
	}
}

type dataPlaneQuotaBucket struct {
	count   int
	resetAt time.Time
}

// InMemoryDataPlaneQuota is the single-process adapter. It applies the same
// operation limit independently to Workspace, Client, Agent, and Subject and
// returns the most restrictive remaining budget. The interface can be backed
// by Redis/Gateway without changing HTTP or application services.
type InMemoryDataPlaneQuota struct {
	mu      sync.Mutex
	buckets map[string]dataPlaneQuotaBucket
	config  DataPlaneQuotaConfig
	now     func() time.Time
}

func NewInMemoryDataPlaneQuota(config DataPlaneQuotaConfig) (*InMemoryDataPlaneQuota, error) {
	if config.Window <= 0 || config.MaxEntries < 4 || len(config.Limits) == 0 {
		return nil, ErrDataPlaneQuotaInvalid
	}
	for operation, limit := range config.Limits {
		if !validQuotaOperation(operation) || limit < 1 {
			return nil, ErrDataPlaneQuotaInvalid
		}
	}
	return &InMemoryDataPlaneQuota{
		buckets: make(map[string]dataPlaneQuotaBucket), config: cloneQuotaConfig(config),
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (quota *InMemoryDataPlaneQuota) Allow(
	ctx context.Context,
	request DataPlaneQuotaRequest,
) (DataPlaneQuotaDecision, error) {
	request = normalizeQuotaRequest(request)
	if quota == nil || ctx == nil || ctx.Err() != nil || !validQuotaRequest(request) {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	limit, ok := quota.config.Limits[request.Operation]
	if !ok || limit < 1 {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	now := quota.now().UTC()
	keys := quotaKeys(request)
	quota.mu.Lock()
	defer quota.mu.Unlock()
	quota.pruneExpired(now)
	resetAt := now.Add(quota.config.Window)
	remaining := limit
	for _, key := range keys {
		bucket, exists := quota.buckets[key]
		if exists && now.Before(bucket.resetAt) {
			if bucket.resetAt.Before(resetAt) {
				resetAt = bucket.resetAt
			}
			if left := limit - bucket.count; left < remaining {
				remaining = left
			}
			if bucket.count >= limit {
				retry := bucket.resetAt.Sub(now)
				if retry < time.Second {
					retry = time.Second
				}
				return DataPlaneQuotaDecision{
					Limit: limit, Remaining: 0, ResetAt: bucket.resetAt,
					RetryAfter: retry,
				}, ErrDataPlaneQuotaExceeded
			}
		}
	}
	newBuckets := 0
	for _, key := range keys {
		if _, exists := quota.buckets[key]; !exists {
			newBuckets++
		}
	}
	if len(quota.buckets)+newBuckets > quota.config.MaxEntries {
		return DataPlaneQuotaDecision{
			Limit: limit, Remaining: 0, ResetAt: resetAt,
			RetryAfter: quota.config.Window,
		}, ErrDataPlaneQuotaExceeded
	}
	remaining = limit - 1
	resetAt = now.Add(quota.config.Window)
	for _, key := range keys {
		bucket, exists := quota.buckets[key]
		if !exists || !now.Before(bucket.resetAt) {
			bucket = dataPlaneQuotaBucket{resetAt: now.Add(quota.config.Window)}
		}
		bucket.count++
		quota.buckets[key] = bucket
		if left := limit - bucket.count; left < remaining {
			remaining = left
		}
		if bucket.resetAt.Before(resetAt) {
			resetAt = bucket.resetAt
		}
	}
	return DataPlaneQuotaDecision{
		Limit: limit, Remaining: remaining, ResetAt: resetAt,
	}, nil
}

func (quota *InMemoryDataPlaneQuota) pruneExpired(now time.Time) {
	for key, bucket := range quota.buckets {
		if !now.Before(bucket.resetAt) {
			delete(quota.buckets, key)
		}
	}
}

func quotaKeys(request DataPlaneQuotaRequest) []string {
	prefix := string(request.Operation) + "\x00"
	return []string{
		prefix + "workspace\x00" + request.WorkspaceID,
		prefix + "client\x00" + request.ClientID,
		prefix + "agent\x00" + request.AgentID,
		prefix + "subject\x00" + request.SubjectID,
	}
}

func normalizeQuotaRequest(value DataPlaneQuotaRequest) DataPlaneQuotaRequest {
	value.Operation = DataPlaneQuotaOperation(strings.ToLower(strings.TrimSpace(string(value.Operation))))
	value.WorkspaceID = strings.ToLower(strings.TrimSpace(value.WorkspaceID))
	value.AgentID = strings.ToLower(strings.TrimSpace(value.AgentID))
	value.ClientID = strings.ToLower(strings.TrimSpace(value.ClientID))
	value.ServicePrincipalID = strings.ToLower(strings.TrimSpace(value.ServicePrincipalID))
	value.SubjectID = strings.ToLower(strings.TrimSpace(value.SubjectID))
	return value
}

func validQuotaRequest(value DataPlaneQuotaRequest) bool {
	return validQuotaOperation(value.Operation) && quotaUUID(value.WorkspaceID) &&
		quotaUUID(value.AgentID) && quotaUUID(value.ClientID) &&
		quotaUUID(value.ServicePrincipalID) && quotaUUID(value.SubjectID)
}

func validQuotaOperation(value DataPlaneQuotaOperation) bool {
	switch value {
	case QuotaConversationCreate, QuotaRunCreate, QuotaRunCancel, QuotaInteractionDecide, QuotaEventStream:
		return true
	default:
		return false
	}
}

func quotaUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func cloneQuotaConfig(value DataPlaneQuotaConfig) DataPlaneQuotaConfig {
	copy := DataPlaneQuotaConfig{Window: value.Window, MaxEntries: value.MaxEntries,
		Limits: make(map[DataPlaneQuotaOperation]int, len(value.Limits))}
	for operation, limit := range value.Limits {
		copy.Limits[operation] = limit
	}
	return copy
}

func (decision DataPlaneQuotaDecision) RetryAfterSeconds() string {
	seconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}
