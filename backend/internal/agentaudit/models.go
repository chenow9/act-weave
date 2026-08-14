// Package agentaudit provides PLATFORM_ADMIN read models for agent full-trace debug audit.
package agentaudit

import (
	"encoding/json"
	"time"
)

const MissingReasoningText = "无推理数据"

// ContentState describes how a field should be displayed.
type ContentState string

const (
	ContentPlain    ContentState = "plain"
	ContentRedacted ContentState = "redacted"
	ContentCipher   ContentState = "cipher"
	ContentMissing  ContentState = "missing"
)

// ActorSummary is the human-facing initiator (USER/SYSTEM/service principal).
type ActorSummary struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	// Agent Access client (when actor is a service principal with a client).
	ClientID   string `json:"clientId,omitempty"`
	ClientName string `json:"clientName,omitempty"`
}

type TraceListItem struct {
	TraceID    string     `json:"traceId"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Status     string     `json:"status"`
	Model      string     `json:"model"`
	// UserLabel is the primary display string (prefer displayName/username over raw UUID).
	UserLabel string `json:"userLabel"`
	// User carries structured initiator details for list/detail hover cards.
	User      *ActorSummary `json:"user,omitempty"`
	LatencyMs *int64        `json:"latencyMs,omitempty"`
	StepCount int           `json:"stepCount"`
	RunIDs    []string      `json:"runIds"`
}

type Stats struct {
	TotalRuns    int64   `json:"totalRuns"`
	SuccessRate  float64 `json:"successRate"`
	FailureRate  float64 `json:"failureRate"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
}

type ListResult struct {
	Items     []TraceListItem `json:"items"`
	Stats     Stats           `json:"stats"`
	DebugMode bool            `json:"debugMode"`
	// Total is the number of distinct traces matching the list filter (for pagination).
	Total int `json:"total"`
}

type Step struct {
	Type         string          `json:"type"`
	Title        string          `json:"title"`
	TimeOffsetMs int64           `json:"timeOffsetMs"`
	LatencyMs    *int64          `json:"latencyMs,omitempty"`
	Content      string          `json:"content,omitempty"`
	ContentState ContentState    `json:"contentState,omitempty"`
	Params       json.RawMessage `json:"params,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	ParamsState  ContentState    `json:"paramsState,omitempty"`
	ResultState  ContentState    `json:"resultState,omitempty"`
	RunID        string          `json:"runId,omitempty"`
	StepID       string          `json:"stepId,omitempty"`
	InvocationID string          `json:"invocationId,omitempty"`
	// Nested agent attribution (AGENT_DELEGATION hierarchy).
	AgentID            string `json:"agentId,omitempty"`
	DelegationID       string `json:"delegationId,omitempty"`
	ParentDelegationID string `json:"parentDelegationId,omitempty"`
	ParentStepID       string `json:"parentStepId,omitempty"`
	ChildRunID         string `json:"childRunId,omitempty"`
	CallerAgentID      string `json:"callerAgentId,omitempty"`
	TargetAgentID      string `json:"targetAgentId,omitempty"`
	// Human names when resolvable (audit UI path labels).
	CallerAgentName string `json:"callerAgentName,omitempty"`
	TargetAgentName string `json:"targetAgentName,omitempty"`
	ExternalRef     string `json:"externalAgentRef,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	Origin          string `json:"origin,omitempty"`
	// Depth must always serialize (including 0 for EXTERNAL root inbound).
	// omitempty would drop depth=0 and break API/UI contracts.
	Depth        int    `json:"depth"`
	Status       string `json:"status,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	// Remote A2A linkage (authoritative from agent_run_delegations).
	RemoteTaskID      string `json:"remoteTaskId,omitempty"`
	RemoteContextID   string `json:"remoteContextId,omitempty"`
	RemoteMessageID   string `json:"remoteMessageId,omitempty"`
	RemoteEndpointRef string `json:"remoteEndpointRef,omitempty"`
	ProtocolStatus    string `json:"protocolStatus,omitempty"`
	// Token usage (null when unknown — never invent 0 for A2A without usage).
	InputTokens  *int64 `json:"inputTokens,omitempty"`
	OutputTokens *int64 `json:"outputTokens,omitempty"`
	TotalTokens  *int64 `json:"totalTokens,omitempty"`
	TokensKnown  bool   `json:"tokensKnown,omitempty"`
	// Dispatch attempt/retry (execution only; not finalize-outbox).
	// Pointer + omitempty: nil omits (non-delegation steps); non-nil including 0
	// serializes so pre-dispatch failures stay visible as attemptCount:0 / retryCount:0.
	AttemptCount *int `json:"attemptCount,omitempty"`
	RetryCount   *int `json:"retryCount,omitempty"`
	// Children holds nested timeline steps under an agent_delegation frame.
	Children []Step `json:"children,omitempty"`
	// Collapsed hints UI default expand state (false = expanded when depth small).
	Collapsed bool `json:"collapsed,omitempty"`
}

type TraceDetail struct {
	TraceID    string               `json:"traceId"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt *time.Time           `json:"finishedAt,omitempty"`
	LatencyMs  *int64               `json:"latencyMs,omitempty"`
	Status     string               `json:"status"`
	Model      string               `json:"model"`
	UserLabel  string               `json:"userLabel"`
	User       *ActorSummary        `json:"user,omitempty"`
	DebugMode  bool                 `json:"debugMode"`
	Steps      []Step               `json:"steps"`
	RunIDs     []string             `json:"runIds"`
	Failure    *TraceFailureSummary `json:"failure,omitempty"`
	// Step pagination (timeline can be long: many MODEL/TOOL/output cards).
	// StepTotal is the full built timeline length; Steps is one page slice.
	StepTotal  int  `json:"stepTotal"`
	StepOffset int  `json:"stepOffset"`
	StepLimit  int  `json:"stepLimit"`
	HasMore    bool `json:"hasMore"`
}

// TraceFailureSummary keeps the first-screen diagnosis independent from timeline pagination.
type TraceFailureSummary struct {
	Stage        string `json:"stage"`
	StepTitle    string `json:"stepTitle"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	TimeOffsetMs int64  `json:"timeOffsetMs"`
	RunID        string `json:"runId,omitempty"`
	StepID       string `json:"stepId,omitempty"`
}

// DetailFilter pages the built timeline steps (not raw agent_run_steps rows).
type DetailFilter struct {
	Limit  int
	Offset int
}

const (
	DefaultDetailStepLimit = 30
	MaxDetailStepLimit     = 100
)

// RunFact is a minimal domain slice used for aggregation tests and service assembly.
type RunFact struct {
	ID              string
	TraceID         string
	Status          string
	TriggeredByType string
	TriggeredByID   string
	// Optional profile when actor resolves (USER / SERVICE_PRINCIPAL).
	TriggeredUsername    string
	TriggeredDisplayName string
	TriggeredClientID    string
	TriggeredClientName  string
	ModelSnapshot        json.RawMessage
	StartedAt            time.Time
	FinishedAt           *time.Time
}

type MessageFact struct {
	ID        string
	RunID     string
	Role      string
	Content   string
	CreatedAt time.Time
}

type StepFact struct {
	ID            string
	RunID         string
	SequenceNo    int
	StepType      string
	Status        string
	InputSummary  json.RawMessage
	OutputSummary json.RawMessage
	RawObjectID   string
	StartedAt     time.Time
	FinishedAt    *time.Time
	// ModelTurn is optional parsed MODEL_TURN object body when available.
	ModelTurn map[string]any
	// Tool params/result when resolved for the step.
	ToolParams           json.RawMessage
	ToolResult           json.RawMessage
	ToolName             string
	InvocationID         string
	ToolPayloadAvailable bool
	// Nested attribution.
	AgentID            string
	DelegationID       string
	ParentDelegationID string
	ParentStepID       string
	// Authoritative fields from agent_run_delegations (joined at load).
	ChildRunID          string
	CallerAgentID       string
	TargetAgentID       string
	ExternalAgentRef    string
	Mode                string
	Protocol            string
	Origin              string
	Depth               int
	RemoteTaskID        string
	RemoteContextID     string
	RemoteMessageID     string
	RemoteEndpointRef   string
	ProtocolStatus      string
	DelegationErrorCode string
	DelegationErrorMsg  string
	DelegationLatencyMs *int64
	// DelegationStatus is the authoritative row status (may differ from step).
	DelegationStatus string
	// Token + attempt fields from agent_run_delegations.
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
	TokensKnown  bool
	AttemptCount int
	RetryCount   int
}
