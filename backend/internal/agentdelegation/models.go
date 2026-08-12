// Package agentdelegation implements internal Agent→Agent bindings, graph
// construction, root-level budgets, and authoritative delegation audit rows.
package agentdelegation

import (
	"encoding/json"
	"sync"
	"time"
)

// Modes for a delegation binding.
const (
	ModeInline = "INLINE"
	ModeTask   = "TASK"
)

// Context policies for what the parent may pass to the child.
const (
	ContextTaskOnly         = "TASK_ONLY"
	ContextSummary          = "SUMMARY"
	ContextSelectedMessages = "SELECTED_MESSAGES"
)

// Protocol / origin for audit rows.
const (
	ProtocolInternal = "INTERNAL"
	ProtocolA2A      = "A2A"
	OriginInternal   = "INTERNAL"
	OriginExternal   = "EXTERNAL"
)

// Delegation lifecycle statuses.
const (
	StatusPending   = "PENDING"
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
	StatusCancelled = "CANCELLED"
	StatusTimedOut  = "TIMED_OUT"
)

// StepTypeAgentDelegation is the agent_run_steps.step_type for a delegation frame.
const StepTypeAgentDelegation = "AGENT_DELEGATION"

// Graph snapshot schema.
const GraphSnapshotSchemaV1 = "agent_graph_snapshot.v1"

// Root-shared budget defaults (spec §二).
const (
	DefaultMaxDepth            = 2
	DefaultMaxTotalDelegations = 8
	DefaultMaxPerBinding       = 3
)

// Binding is an explicit directed Agent→Agent edge.
type Binding struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspaceId"`
	CallerAgentID string     `json:"callerAgentId"`
	TargetAgentID string     `json:"targetAgentId"`
	CallableName  string     `json:"callableName"`
	Description   string     `json:"description"`
	Mode          string     `json:"mode"`
	ContextPolicy string     `json:"contextPolicy"`
	Enabled       bool       `json:"enabled"`
	Version       int64      `json:"version"`
	CreatedBy     string     `json:"createdBy,omitempty"`
	UpdatedBy     string     `json:"updatedBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
}

// CreateBindingInput creates a new binding (version starts at 1).
type CreateBindingInput struct {
	ID            string
	WorkspaceID   string
	CallerAgentID string
	TargetAgentID string
	CallableName  string
	Description   string
	Mode          string
	ContextPolicy string
	Enabled       bool
	ActorID       string
}

// UpdateBindingInput patches a binding with optimistic concurrency (version).
type UpdateBindingInput struct {
	WorkspaceID     string
	BindingID       string
	ExpectedVersion int64
	TargetAgentID   *string
	CallableName    *string
	Description     *string
	Mode            *string
	ContextPolicy   *string
	Enabled         *bool
	ActorID         string
}

// Delegation is one authoritative agent_run_delegations row.
type Delegation struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspaceId"`
	ParentRunID        string          `json:"parentRunId"`
	ChildRunID         *string         `json:"childRunId,omitempty"`
	ParentDelegationID *string         `json:"parentDelegationId,omitempty"`
	CallerAgentID      string          `json:"callerAgentId"`
	TargetAgentID      *string         `json:"targetAgentId,omitempty"`
	ExternalAgentRef   *string         `json:"externalAgentRef,omitempty"`
	Mode               string          `json:"mode"`
	Protocol           string          `json:"protocol"`
	Origin             string          `json:"origin"`
	Depth              int             `json:"depth"`
	BindingVersion     int64           `json:"bindingVersion"`
	ToolCallID         string          `json:"toolCallId"`
	IdempotencyKey     string          `json:"idempotencyKey"`
	Status             string          `json:"status"`
	InputSummary       json.RawMessage `json:"inputSummary"`
	InputPayload       json.RawMessage `json:"inputPayload"`
	OutputSummary      json.RawMessage `json:"outputSummary"`
	OutputPayload      json.RawMessage `json:"outputPayload"`
	ErrorCode          string          `json:"errorCode,omitempty"`
	ErrorMessage       string          `json:"errorMessage,omitempty"`
	RemoteTaskID       string          `json:"remoteTaskId,omitempty"`
	RemoteContextID    string          `json:"remoteContextId,omitempty"`
	RemoteMessageID    string          `json:"remoteMessageId,omitempty"`
	RemoteEndpointRef  string          `json:"remoteEndpointRef,omitempty"`
	ProtocolStatus     string          `json:"protocolStatus,omitempty"`
	LatencyMs          *int64          `json:"latencyMs,omitempty"`
	// Token usage aggregated from nested MODEL turns under this delegation.
	// Null when unknown (A2A remote without usage) — never invent zeros.
	InputTokens  *int64 `json:"inputTokens,omitempty"`
	OutputTokens *int64 `json:"outputTokens,omitempty"`
	TotalTokens  *int64 `json:"totalTokens,omitempty"`
	TokensKnown  bool   `json:"tokensKnown"`
	// AttemptCount = actual agent dispatch attempts (idempotent replay does not count).
	// RetryCount = max(0, attempt_count-1). Finalize-outbox retries are separate.
	AttemptCount int        `json:"attemptCount"`
	RetryCount   int        `json:"retryCount"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	// StepID is the AGENT_DELEGATION step created with this row (not a DB column
	// on agent_run_delegations; carried for runtime convenience).
	StepID string `json:"stepId,omitempty"`
}

// CreateDelegationInput is fail-closed pre-dispatch write.
type CreateDelegationInput struct {
	ID                 string
	WorkspaceID        string
	ParentRunID        string
	ChildRunID         *string
	ParentDelegationID *string
	CallerAgentID      string
	TargetAgentID      *string
	ExternalAgentRef   *string
	Mode               string
	Protocol           string
	Origin             string
	Depth              int
	BindingVersion     int64
	ToolCallID         string
	IdempotencyKey     string
	InputSummary       json.RawMessage
	InputPayload       json.RawMessage
	// Step fields for the paired AGENT_DELEGATION step (same transaction).
	StepID       string
	AgentID      string // agent that owns the step (caller)
	ParentStepID *string
}

// FinalizeDelegationInput transitions to a terminal status idempotently.
type FinalizeDelegationInput struct {
	WorkspaceID   string
	DelegationID  string
	StepID        string
	Status        string // SUCCEEDED|FAILED|CANCELLED|TIMED_OUT
	OutputSummary json.RawMessage
	OutputPayload json.RawMessage
	ErrorCode     string
	ErrorMessage  string
	ChildRunID    *string
	// Optional A2A remote refs (safe, no secrets).
	RemoteTaskID      string
	RemoteContextID   string
	RemoteMessageID   string
	RemoteEndpointRef string
	ProtocolStatus    string
	// Optional token / attempt overrides written at finalize.
	// When TokensKnown is false, token columns stay NULL.
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
	TokensKnown  bool
	// AttemptCount when >0 sets authoritative dispatch count (else leave DB default).
	AttemptCount int
	RetryCount   int
}

// TokenUsage is one MODEL turn's prompt/completion/total tokens.
// Known=false means provider did not report usage (do not invent 0).
type TokenUsage struct {
	PromptTokens     int  `json:"promptTokens,omitempty"`
	CompletionTokens int  `json:"completionTokens,omitempty"`
	TotalTokens      int  `json:"totalTokens,omitempty"`
	Known            bool `json:"known"`
}

// GraphSnapshotV1 is the immutable agent_graph_snapshot.v1 stored at run start.
// RemotesFrozen=true means FrozenRemotesByCaller is authoritative for all callers
// (empty slices mean "no remotes", never live-fallback). Legacy rows without the
// flag may use controlled live remotes fallback.
type GraphSnapshotV1 struct {
	SchemaVersion string              `json:"schemaVersion"`
	RootAgentID   string              `json:"rootAgentId"`
	MaxDepth      int                 `json:"maxDepth"`
	MaxTotal      int                 `json:"maxTotalDelegations"`
	MaxPerBinding int                 `json:"maxPerBinding"`
	Nodes         []GraphNodeSnapshot `json:"nodes"`
	Edges         []GraphEdgeSnapshot `json:"edges"`
	BuiltAt       time.Time           `json:"builtAt"`
	// FrozenRemotesByCaller maps caller agent id → full remote binding configs.
	// Always emitted by SnapshotJSON (not omitempty) so freeze is explicit-empty safe.
	FrozenRemotesByCaller map[string][]FrozenRemoteBinding `json:"frozenRemotesByCaller"`
	// RemotesFrozen marks that remotes were evaluated at freeze time for every
	// reachable caller (including explicit empty lists).
	RemotesFrozen bool           `json:"remotesFrozen"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// FrozenRemoteBinding is the immutable outbound A2A remote config for one caller.
type FrozenRemoteBinding struct {
	ID            string   `json:"id"`
	CallerAgentID string   `json:"callerAgentId"`
	CallableName  string   `json:"callableName"`
	Description   string   `json:"description,omitempty"`
	EndpointURL   string   `json:"endpointUrl"`
	AgentCardURL  string   `json:"agentCardUrl,omitempty"`
	AllowedHosts  []string `json:"allowedHosts"`
	AuthSecretRef string   `json:"authSecretRef,omitempty"`
	TimeoutMs     int      `json:"timeoutMs"`
	Version       int64    `json:"version"`
}

// GraphNodeSnapshot freezes one agent definition version for rebuild on resume.
type GraphNodeSnapshot struct {
	AgentID            string `json:"agentId"`
	Name               string `json:"name,omitempty"`
	PromptRevisionID   string `json:"promptRevisionId,omitempty"`
	PromptRevisionHash string `json:"promptRevisionHash,omitempty"`
	ModelConfigID      string `json:"modelConfigId,omitempty"`
	// ModelConfigLockVer is always emitted by snapshotAgentNode (lock ≥ 1).
	// Not omitempty: freeze parse requires the field present and equal to nested locks.
	ModelConfigLockVer int64 `json:"modelConfigLockVersion"`
	// ModelSnapshot is the frozen model config document for this node (TASK child runs).
	ModelSnapshot json.RawMessage `json:"modelSnapshot,omitempty"`
	// AgentSnapshot is the frozen agent-binding document for this node.
	AgentSnapshot      json.RawMessage `json:"agentSnapshot,omitempty"`
	CapabilitySnapshot json.RawMessage `json:"capabilitySnapshot,omitempty"`
	Depth              int             `json:"depth"`
}

// GraphEdgeSnapshot freezes one enabled binding version.
type GraphEdgeSnapshot struct {
	BindingID     string `json:"bindingId"`
	CallerAgentID string `json:"callerAgentId"`
	TargetAgentID string `json:"targetAgentId"`
	CallableName  string `json:"callableName"`
	Description   string `json:"description,omitempty"`
	Mode          string `json:"mode"`
	ContextPolicy string `json:"contextPolicy"`
	Version       int64  `json:"version"`
	// Protocol is INTERNAL for agent_delegation_bindings, A2A for remote.
	Protocol string `json:"protocol"`
	// ExternalRef is set for A2A edges (safe endpoint host ref, never secrets).
	ExternalRef string `json:"externalRef,omitempty"`
}

// Budget tracks root-shared delegation limits across the call tree.
// All counters are protected by mu: Eino ToolsNode may invoke multiple AgentTools
// in parallel against the same *Budget (ExecuteSequentially defaults to false).
//
// Lifecycle:
//   - CheckAndReserve: atomically validate depth/total/per-binding and reserve one slot
//   - Release: undo a reservation on any pre-dispatch failure (before actual agent/remote invoke)
//   - After RecordDispatchAttempt succeeds the reservation is consumed permanently (do not Release)
type Budget struct {
	mu sync.Mutex

	MaxDepth      int
	MaxTotal      int
	MaxPerBinding int
	// TotalUsed includes both in-flight reservations and committed dispatches.
	TotalUsed int
	// PerBinding counts per binding id / callable key (reservations + committed).
	PerBinding map[string]int
}

// NewBudget returns defaults.
func NewBudget() *Budget {
	return &Budget{
		MaxDepth:      DefaultMaxDepth,
		MaxTotal:      DefaultMaxTotalDelegations,
		MaxPerBinding: DefaultMaxPerBinding,
		PerBinding:    map[string]int{},
	}
}

// Clone returns a copy with independent counters and mutex (not a live shared budget).
func (b *Budget) Clone() *Budget {
	if b == nil {
		return NewBudget()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := &Budget{
		MaxDepth:      b.MaxDepth,
		MaxTotal:      b.MaxTotal,
		MaxPerBinding: b.MaxPerBinding,
		TotalUsed:     b.TotalUsed,
		PerBinding:    make(map[string]int, len(b.PerBinding)),
	}
	for k, v := range b.PerBinding {
		out.PerBinding[k] = v
	}
	return out
}

// Check allows a new delegation at depth for bindingKey when within limits.
// Prefer CheckAndReserve for production paths that may race under parallel ToolsNode.
func (b *Budget) Check(depth int, bindingKey string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkLocked(depth, bindingKey)
}

func (b *Budget) checkLocked(depth int, bindingKey string) error {
	if depth > b.MaxDepth {
		return ErrDepthExceeded
	}
	if b.TotalUsed >= b.MaxTotal {
		return ErrTotalBudgetExceeded
	}
	if b.PerBinding == nil {
		b.PerBinding = map[string]int{}
	}
	if b.PerBinding[bindingKey] >= b.MaxPerBinding {
		return ErrBindingBudgetExceeded
	}
	return nil
}

// CheckAndReserve atomically validates depth/total/per-binding and reserves one
// dispatch slot. On success the caller MUST either:
//   - Release(bindingKey) if actual agent/remote dispatch never starts, or
//   - keep the reservation after RecordDispatchAttempt succeeds (consumed).
func (b *Budget) CheckAndReserve(depth int, bindingKey string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkLocked(depth, bindingKey); err != nil {
		return err
	}
	if b.PerBinding == nil {
		b.PerBinding = map[string]int{}
	}
	b.TotalUsed++
	b.PerBinding[bindingKey]++
	return nil
}

// Release undoes one CheckAndReserve slot. Safe under concurrent callers.
// Must not be called after RecordDispatchAttempt for that reserved dispatch.
func (b *Budget) Release(bindingKey string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.TotalUsed > 0 {
		b.TotalUsed--
	}
	if b.PerBinding == nil {
		return
	}
	if n := b.PerBinding[bindingKey]; n > 0 {
		if n == 1 {
			delete(b.PerBinding, bindingKey)
		} else {
			b.PerBinding[bindingKey] = n - 1
		}
	}
}

// Consume records one successful dispatch. Prefer CheckAndReserve at the start of
// the call path; Consume remains for callers that reserved via Check+Consume under
// exclusive ownership. When used after CheckAndReserve it would double-count — do not.
func (b *Budget) Consume(bindingKey string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.TotalUsed++
	if b.PerBinding == nil {
		b.PerBinding = map[string]int{}
	}
	b.PerBinding[bindingKey]++
}

// Snapshot returns a race-safe copy of current counters for assertions.
func (b *Budget) Snapshot() (total int, perBinding map[string]int) {
	if b == nil {
		return 0, map[string]int{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	perBinding = make(map[string]int, len(b.PerBinding))
	for k, v := range b.PerBinding {
		perBinding[k] = v
	}
	return b.TotalUsed, perBinding
}
