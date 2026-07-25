package execution

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/principal"
)

type AgentRun struct {
	ID                       string
	WorkspaceID              string
	SessionID                string
	AgentID                  string
	Status                   string
	TriggerType              string
	TriggeredByType          string
	TriggeredByID            string
	TraceID                  string
	ModelSnapshot            json.RawMessage
	CapabilitySnapshot       json.RawMessage
	ContextPolicySnapshot    json.RawMessage
	SnapshotSchemaVersion    string
	AuthorizationSnapshot    json.RawMessage
	InputSummary             json.RawMessage
	OutputSummary            json.RawMessage
	ErrorCode                string
	StartedAt                time.Time
	FinishedAt               *time.Time
	LockVersion              int64
	PrincipalSnapshotVersion string
	PrincipalSnapshot        principal.ExecutionSnapshot
}

type AgentRunStep struct {
	ID                  string
	WorkspaceID         string
	RunID               string
	SequenceNo          int
	StepType            string
	Status              string
	CapabilityReleaseID string
	InputSummary        json.RawMessage
	OutputSummary       json.RawMessage
	RawObjectID         string
	RawSHA256           string
	RawLength           int64
	StartedAt           time.Time
	FinishedAt          *time.Time
	ErrorCode           string
}

type WorkflowExecution struct {
	ID                       string
	WorkspaceID              string
	WorkflowID               string
	RevisionID               string
	AgentRunID               string
	TriggerType              string
	TriggeredByType          string
	TriggeredByID            string
	TraceID                  string
	Status                   string
	SnapshotSchemaVersion    string
	AuthorizationSnapshot    json.RawMessage
	InputSummary             json.RawMessage
	OutputSummary            json.RawMessage
	ErrorCode                string
	StartedAt                time.Time
	FinishedAt               *time.Time
	LockVersion              int64
	PrincipalSnapshotVersion string
	PrincipalSnapshot        principal.ExecutionSnapshot
}

type ExecutionStep struct {
	ID            string
	WorkspaceID   string
	ExecutionID   string
	NodeID        string
	NodeType      string
	SequenceNo    int
	Status        string
	InputSummary  json.RawMessage
	OutputSummary json.RawMessage
	RawObjectID   string
	StartedAt     time.Time
	FinishedAt    *time.Time
	ErrorCode     string
}

type AgentRunSnapshots struct {
	SchemaVersion string
	Model         json.RawMessage
	Capabilities  json.RawMessage
	ContextPolicy json.RawMessage
}

type StartAgentRunInput struct {
	ID                    string
	WorkspaceID           string
	SessionID             string
	AgentID               string
	TriggerType           string
	TriggeredByType       string
	TriggeredByID         string
	TraceID               string
	Snapshots             AgentRunSnapshots
	AuthorizationSnapshot json.RawMessage
	InputSummary          json.RawMessage
	PrincipalSnapshot     *principal.ExecutionSnapshot
}

type StartWorkflowExecutionInput struct {
	ID                    string
	WorkspaceID           string
	WorkflowID            string
	RevisionID            string
	AgentRunID            string
	TriggerType           string
	TriggeredByType       string
	TriggeredByID         string
	TraceID               string
	SnapshotSchemaVersion string
	AuthorizationSnapshot json.RawMessage
	InputSummary          json.RawMessage
	PrincipalSnapshot     *principal.ExecutionSnapshot
}

type RunTransition struct {
	ExpectedStatus      string
	ExpectedLockVersion int64
	NewStatus           string
	OutputSummary       json.RawMessage
	ErrorCode           string
}

type StepTransition struct {
	ExpectedStatus string
	NewStatus      string
	OutputSummary  json.RawMessage
	RawObjectID    string
	RawSHA256      string
	RawLength      int64
	ErrorCode      string
}

type AppendAgentRunStepInput struct {
	ID                  string
	WorkspaceID         string
	RunID               string
	StepType            string
	CapabilityReleaseID string
	InputSummary        json.RawMessage
}

type AppendExecutionStepInput struct {
	ID           string
	WorkspaceID  string
	ExecutionID  string
	NodeID       string
	NodeType     string
	InputSummary json.RawMessage
}

type WorkflowExecutionFilter struct {
	Status        string
	TraceID       string
	WorkflowID    string
	StartedAfter  *time.Time
	StartedBefore *time.Time
	Limit         int
}
