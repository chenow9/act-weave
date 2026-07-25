package agent

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
	StatusError    Status = "ERROR"
)

type Agent struct {
	ID                      string
	WorkspaceID             string
	Name                    string
	RoleDescription         string
	CurrentPromptRevisionID *string
	ModelConfigID           string
	IsDefault               bool
	Status                  Status
	CreatedBy               string
	UpdatedBy               string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	LockVersion             int64
	DeletedAt               *time.Time
}

type Summary struct {
	Agent
	ToolsCount     int
	WorkflowsCount int
}

type PromptRevision struct {
	ID            string
	WorkspaceID   string
	AgentID       string
	RevisionNo    int
	SystemPrompt  string
	Source        string
	ContentSHA256 string
	CreatedBy     string
	CreatedAt     time.Time
}

type PromptRun struct {
	ID                 string
	WorkspaceID        string
	AgentID            *string
	OperationType      string
	ModelConfigID      string
	ModelSnapshot      json.RawMessage
	InputObjectID      string
	InputSHA256        string
	InputLength        int64
	OutputObjectID     *string
	OutputSHA256       *string
	OutputLength       *int64
	Status             string
	AcceptedRevisionID *string
	TraceID            string
	CreatedBy          string
	CreatedAt          time.Time
	FinishedAt         *time.Time
	ErrorCode          *string
}

type NewAgent struct {
	ID                string
	WorkspaceID       string
	Name              string
	RoleDescription   string
	ModelConfigID     string
	IsDefault         bool
	InitialRevisionID string
	InitialPrompt     string
	PromptSource      string
	CreatedBy         string
}

type UpdateAgent struct {
	Name                string
	RoleDescription     string
	ModelConfigID       string
	Status              Status
	UpdatedBy           string
	ExpectedLockVersion int64
}

type NewPromptRun struct {
	ID            string
	WorkspaceID   string
	AgentID       *string
	OperationType string
	ModelConfigID string
	ModelSnapshot json.RawMessage
	InputObjectID string
	InputSHA256   string
	InputLength   int64
	TraceID       string
	CreatedBy     string
}
