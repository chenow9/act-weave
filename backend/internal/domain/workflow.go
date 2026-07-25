package domain

import "time"

type WorkflowIssueStage string

const (
	WorkflowIssueStageGraph    WorkflowIssueStage = "graph"
	WorkflowIssueStageSemantic WorkflowIssueStage = "semantic"
	WorkflowIssueStageSpec     WorkflowIssueStage = "spec"
	WorkflowIssueStagePlan     WorkflowIssueStage = "plan"
	WorkflowIssueStageRuntime  WorkflowIssueStage = "runtime"
)

type WorkflowCompilationStatus string

const (
	WorkflowCompilationPending WorkflowCompilationStatus = "Pending"
	WorkflowCompilationValid   WorkflowCompilationStatus = "Valid"
	WorkflowCompilationInvalid WorkflowCompilationStatus = "Invalid"
)

type WorkflowPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type CanvasViewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

type WorkflowGraphPort struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
}

type WorkflowGraphNode struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Label    string              `json:"label"`
	Position WorkflowPosition    `json:"position"`
	Ports    []WorkflowGraphPort `json:"ports"`
	Data     map[string]any      `json:"data"`
	UI       map[string]any      `json:"ui"`
}

type WorkflowGraphEdge struct {
	ID           string         `json:"id"`
	SourceNodeID string         `json:"sourceNodeId"`
	SourcePort   string         `json:"sourcePort"`
	TargetNodeID string         `json:"targetNodeId"`
	TargetPort   string         `json:"targetPort"`
	Data         map[string]any `json:"data"`
	UI           map[string]any `json:"ui"`
}

type WorkflowGraphDraft struct {
	SchemaVersion string              `json:"schemaVersion"`
	Nodes         []WorkflowGraphNode `json:"nodes"`
	Edges         []WorkflowGraphEdge `json:"edges"`
	Viewport      CanvasViewport      `json:"viewport"`
	UI            map[string]any      `json:"ui"`
}

type WorkflowCompilationIssue struct {
	Code        string             `json:"code"`
	Message     string             `json:"message"`
	Severity    string             `json:"severity"`
	SourceStage WorkflowIssueStage `json:"sourceStage"`
	NodeID      string             `json:"nodeId,omitempty"`
	EdgeID      string             `json:"edgeId,omitempty"`
	PortKey     string             `json:"portKey,omitempty"`
	FieldPath   string             `json:"fieldPath,omitempty"`
	Suggestion  string             `json:"suggestion,omitempty"`
}

type WorkflowCompilation struct {
	WorkflowID   string                     `json:"workflowId"`
	DraftVersion string                     `json:"draftVersion"`
	Status       WorkflowCompilationStatus  `json:"status"`
	Spec         *ExecutableWorkflowSpec    `json:"spec,omitempty"`
	Plan         *CompiledExecutionPlan     `json:"plan,omitempty"`
	Issues       []WorkflowCompilationIssue `json:"issues"`
	CompiledAt   time.Time                  `json:"compiledAt"`
}

type WorkflowRevision struct {
	WorkflowID  string                 `json:"workflowId"`
	RevisionID  string                 `json:"revisionId"`
	Status      string                 `json:"status"`
	Draft       WorkflowGraphDraft     `json:"draft"`
	Spec        ExecutableWorkflowSpec `json:"spec"`
	Plan        CompiledExecutionPlan  `json:"plan"`
	CreatedAt   time.Time              `json:"createdAt"`
	CreatedBy   string                 `json:"createdBy,omitempty"`
	PublishNote string                 `json:"publishNote,omitempty"`
	PlanHash    string                 `json:"planHash,omitempty"`
	ActivatedAt *time.Time             `json:"activatedAt,omitempty"`
	Metadata    map[string]any         `json:"metadata,omitempty"`
}

type ExecutableNodeSpec struct {
	NodeID string         `json:"nodeId"`
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

type ExecutableWorkflowSpec struct {
	WorkflowID string               `json:"workflowId"`
	Nodes      []ExecutableNodeSpec `json:"nodes"`
}

type ExecutionPlanNode struct {
	NodeID         string         `json:"nodeId"`
	Type           string         `json:"type"`
	Dependencies   []string       `json:"dependencies,omitempty"`
	IncomingBranch string         `json:"incomingBranch,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
}

type CompiledExecutionPlan struct {
	WorkflowID string              `json:"workflowId"`
	Nodes      []ExecutionPlanNode `json:"nodes"`
	// OutboundRequirements is outbound-requirements.v1 descriptor JSON when the
	// plan references dual-mode HTTP Tool connections. It never contains Token,
	// Secret, Vault key, or attachment locator fields.
	OutboundRequirements any `json:"outboundRequirements,omitempty"`
}

type ExecutionStatus string

const (
	ExecutionRunning  ExecutionStatus = "Running"
	ExecutionApproval ExecutionStatus = "Approval"
	ExecutionSuccess  ExecutionStatus = "Success"
	ExecutionFailed   ExecutionStatus = "Failed"
)

type ExecutionStepStatus string

const (
	ExecutionStepQueued          ExecutionStepStatus = "Queued"
	ExecutionStepRunning         ExecutionStepStatus = "Running"
	ExecutionStepPassed          ExecutionStepStatus = "Passed"
	ExecutionStepSkipped         ExecutionStepStatus = "Skipped"
	ExecutionStepWaitingApproval ExecutionStepStatus = "WaitingApproval"
	ExecutionStepFailed          ExecutionStepStatus = "Failed"
	ExecutionStepCancelled       ExecutionStepStatus = "Cancelled"
)

type Execution struct {
	ID                      string                `json:"id"`
	WorkflowID              string                `json:"workflowId"`
	WorkflowVersion         string                `json:"workflowVersion"`
	WorkspaceID             string                `json:"workspaceId"`
	Trigger                 string                `json:"trigger"`
	UserID                  string                `json:"userId"`
	TraceID                 string                `json:"traceId"`
	Status                  ExecutionStatus       `json:"status"`
	DurationMS              int                   `json:"durationMs"`
	InputSummary            string                `json:"inputSummary"`
	OutputSummary           string                `json:"outputSummary"`
	ErrorMessage            string                `json:"errorMessage,omitempty"`
	RawPayloadObjectAddress string                `json:"rawPayloadObjectAddress"`
	Steps                   []ExecutionStepRecord `json:"steps"`
	StartedAt               time.Time             `json:"startedAt"`
	FinishedAt              time.Time             `json:"finishedAt"`
	CreatedAt               time.Time             `json:"createdAt"`
}

type ExecutionStepRecord struct {
	ID                      string              `json:"id"`
	ExecutionID             string              `json:"executionId"`
	Name                    string              `json:"name"`
	NodeID                  string              `json:"nodeId,omitempty"`
	NodeType                string              `json:"nodeType,omitempty"`
	Status                  ExecutionStepStatus `json:"status"`
	InputSummary            string              `json:"inputSummary"`
	OutputSummary           string              `json:"outputSummary"`
	ErrorMessage            string              `json:"errorMessage,omitempty"`
	DurationMS              int                 `json:"durationMs"`
	RawPayloadObjectAddress string              `json:"rawPayloadObjectAddress,omitempty"`
	StartedAt               time.Time           `json:"startedAt"`
	FinishedAt              time.Time           `json:"finishedAt"`
}
