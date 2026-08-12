package execution

import (
	"encoding/json"
	"time"

	"actweave/backend/internal/principal"
)

// AgentSnapshot is the run.v2 binding for Agent prompt revision and model config
// references. IC-01 only defines the shape; binding and bridge consumption follow later.
type AgentSnapshot struct {
	SchemaVersion      string `json:"schemaVersion,omitempty"`
	AgentID            string `json:"agentId,omitempty"`
	PromptRevisionID   string `json:"promptRevisionId,omitempty"`
	PromptRevisionHash string `json:"promptRevisionHash,omitempty"`
	ModelConfigID      string `json:"modelConfigId,omitempty"`
	ModelConfigLockVer int64  `json:"modelConfigLockVersion,omitempty"`
}

// ContextAssemblySegment is one projected message identity in an assembly manifest.
// It never carries message body text.
type ContextAssemblySegment struct {
	MessageID       string `json:"messageId,omitempty"`
	Role            string `json:"role,omitempty"`
	ContentHash     string `json:"contentHash,omitempty"`
	EstimatedTokens int64  `json:"estimatedTokens,omitempty"`
}

// ContextAssemblyManifest is the domain shape of agent_run_context_assemblies.
// Persistence and immutability enforcement are schema-backed; writers arrive later.
type ContextAssemblyManifest struct {
	ID                          string
	WorkspaceID                 string
	RunID                       string
	SessionID                   string
	Mode                        string
	PolicySnapshotHash          string
	ModelSnapshotHash           string
	CapabilitySnapshotHash      string
	AgentSnapshotHash           string
	EstimatorProfile            string
	EstimatorVersion            string
	HardInputCeilingTokens      int64
	OutputReserveTokens         int64
	SafetyMarginTokens          int64
	ToolsOverheadTokens         int64
	SystemPromptRevisionID      *string
	SystemPromptHash            string
	IncludedSegments            json.RawMessage
	OmittedPrefixStartMessageID *string
	OmittedPrefixEndMessageID   *string
	OmittedPrefixCount          int
	SummaryID                   *string
	SummaryHash                 *string
	SummaryCoverage             json.RawMessage
	AssemblyDigest              string
	EstimatedTotalTokens        int64
	CreatedAt                   time.Time
}

type AgentRun struct {
	ID                    string
	WorkspaceID           string
	SessionID             string
	AgentID               string
	Status                string
	TriggerType           string
	TriggeredByType       string
	TriggeredByID         string
	TraceID               string
	ModelSnapshot         json.RawMessage
	CapabilitySnapshot    json.RawMessage
	ContextPolicySnapshot json.RawMessage
	// AgentSnapshot is the raw JSON object from agent_runs.agent_snapshot.
	// Empty object "{}" is the expand-only default (legacy / pre-v2).
	AgentSnapshot json.RawMessage
	// AgentGraphSnapshot is agent_graph_snapshot.v1 frozen at run start.
	AgentGraphSnapshot       json.RawMessage
	ParentRunID              string
	ParentDelegationID       string
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
	// Hierarchical attribution (nullable for legacy steps).
	AgentID      string
	DelegationID string
	ParentStepID string
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
	// Agent is the run.v2 binding snapshot (prompt revision + model config refs).
	// Empty object means legacy / gate-off.
	Agent json.RawMessage
	// Graph is the immutable agent_graph_snapshot.v1 frozen at run start. Root
	// chat runs freeze an explicitly empty graph (root node only, zero edges,
	// zero remotes) so the Agentic initial path has a topology authority that is
	// not live delegation config. Empty means legacy / gate-off.
	Graph json.RawMessage
}

// Snapshot schema versions for agent runs.
const (
	RunSnapshotSchemaV1 = "run.v1"
	RunSnapshotSchemaV2 = "run.v2"

	ContextSnapshotSchemaV1 = "session-context.v1"
	AgentSnapshotSchemaV1   = "agent-binding.v1"
)

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
	// Optional parent linkage for TASK-mode child runs.
	ParentRunID        string
	ParentDelegationID string
	// Optional immutable agent_graph_snapshot.v1 at start.
	AgentGraphSnapshot json.RawMessage
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
	// Optional hierarchical attribution for nested agent steps.
	AgentID      string
	DelegationID string
	ParentStepID string
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
