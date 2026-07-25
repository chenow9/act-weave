package execution

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/principal"
)

type ToolInvocation struct {
	ID                       string
	WorkspaceID              string
	ToolID                   string
	ToolVersionID            string
	CapabilityReleaseID      string
	ProviderID               string
	ConnectionID             string
	ExecutionLeaseID         string
	ProviderRequestID        string
	AgentRunID               string
	WorkflowExecutionID      string
	ExecutionStepID          string
	ActorType                string
	ActorID                  string
	TraceID                  string
	IdempotencyKey           string
	Status                   string
	InputSummary             json.RawMessage
	OutputSummary            json.RawMessage
	RawObjectID              string
	LatencyMS                *int64
	ErrorCode                string
	StartedAt                time.Time
	FinishedAt               *time.Time
	PrincipalSnapshotVersion string
	PrincipalSnapshot        principal.ExecutionSnapshot
	AuthorizationSnapshot    json.RawMessage
}

type StartToolInvocationInput struct {
	ID                    string
	WorkspaceID           string
	ToolID                string
	ToolVersionID         string
	CapabilityReleaseID   string
	ProviderID            string
	ConnectionID          string
	ExecutionLeaseID      string
	AgentRunID            string
	WorkflowExecutionID   string
	ExecutionStepID       string
	ActorType             string
	ActorID               string
	TraceID               string
	IdempotencyKey        string
	InputSummary          json.RawMessage
	PrincipalSnapshot     *principal.ExecutionSnapshot
	AuthorizationSnapshot json.RawMessage
}

type StartToolInvocationResult struct {
	Invocation ToolInvocation
	Created    bool
}

type CompleteToolInvocationInput struct {
	OutputSummary     json.RawMessage
	RawObjectID       string
	ProviderRequestID string
	FinishedAt        time.Time
}

type FailToolInvocationInput struct {
	OutputSummary     json.RawMessage
	RawObjectID       string
	ProviderRequestID string
	ErrorCode         string
	FinishedAt        time.Time
}
