package execution

import (
	"context"
	"encoding/json"
	"time"
)

const (
	ExecutorTypeHTTP = "HTTP"
	// ExecutorTypeWORKFLOW runs a published workflow revision as an agent tool
	// (bound WORKFLOW capability). Side effects go through workflowruntime, not
	// the HTTP tool_invocations table.
	ExecutorTypeWORKFLOW = "WORKFLOW"
)

type ExecutorFeatures struct {
	Streaming bool
	Cancel    bool
	Session   bool
	Sandbox   bool
}

// ReleaseSnapshot is a fully resolved, immutable execution contract. Executors
// must not query mutable Tool or Connection state while an invocation is active.
type ReleaseSnapshot struct {
	ReleaseID           string
	WorkspaceID         string
	CapabilityID        string
	ToolVersionID       string
	ExecutorType        string
	ProviderID          string
	ActionSchemaVersion string
	ActionConfig        json.RawMessage
	InputSchema         json.RawMessage
	OutputSchema        json.RawMessage
	ErrorMappings       json.RawMessage
	RuntimePolicy       json.RawMessage
	Checksum            string
}

// ConnectionSnapshot contains only the connection data already authorized and
// resolved by the invocation orchestrator. Headers are never copied to results.
type ConnectionSnapshot struct {
	ID                   string
	WorkspaceID          string
	ProviderID           string
	Environment          string
	BaseURL              string
	Headers              map[string]string `json:"-"`
	SensitiveHeaderNames []string          `json:"-"`
	EgressPolicy         EgressPolicy
}

type EgressPolicy struct {
	AllowedHosts []string
	AllowedPorts []int
	AllowedCIDRs []string
	MaxRedirects int
}

type InvocationRequest struct {
	InvocationID string
	TraceID      string
	Snapshot     ReleaseSnapshot
	Connection   ConnectionSnapshot
	Input        json.RawMessage
	// Actor fields are set by the invocation pipeline for WORKFLOW executors
	// (PublishedRevisionRunner identity). HTTP executors ignore them.
	ActorType  string
	ActorID    string
	AgentRunID string
}

type InvocationRef struct {
	InvocationID string
}

type InvocationResult struct {
	InvocationID string
	TraceID      string
	Output       json.RawMessage
	HTTPStatus   int
	ContentType  string
	StartedAt    time.Time
	FinishedAt   time.Time
	Latency      time.Duration
}

type InvocationEvent struct {
	InvocationID string
	Type         string
	ErrorCode    string
	OccurredAt   time.Time
}

const (
	EventStarted   = "STARTED"
	EventCompleted = "COMPLETED"
	EventFailed    = "FAILED"
)

type InvocationEventSink interface {
	Emit(context.Context, InvocationEvent) error
}

type InvocationEventSinkFunc func(context.Context, InvocationEvent) error

func (function InvocationEventSinkFunc) Emit(ctx context.Context, event InvocationEvent) error {
	return function(ctx, event)
}

type CapabilityExecutor interface {
	Kind() string
	Capabilities() ExecutorFeatures
	Invoke(context.Context, InvocationRequest, InvocationEventSink) (InvocationResult, error)
	Cancel(context.Context, InvocationRef) error
}
