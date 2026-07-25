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

type TraceListItem struct {
	TraceID    string     `json:"traceId"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Status     string     `json:"status"`
	Model      string     `json:"model"`
	UserLabel  string     `json:"userLabel"`
	LatencyMs  *int64     `json:"latencyMs,omitempty"`
	StepCount  int        `json:"stepCount"`
	RunIDs     []string   `json:"runIds"`
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
}

type TraceDetail struct {
	TraceID    string     `json:"traceId"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	LatencyMs  *int64     `json:"latencyMs,omitempty"`
	Status     string     `json:"status"`
	Model      string     `json:"model"`
	UserLabel  string     `json:"userLabel"`
	DebugMode  bool       `json:"debugMode"`
	Steps      []Step     `json:"steps"`
	RunIDs     []string   `json:"runIds"`
	// Step pagination (timeline can be long: many MODEL/TOOL/output cards).
	// StepTotal is the full built timeline length; Steps is one page slice.
	StepTotal  int  `json:"stepTotal"`
	StepOffset int  `json:"stepOffset"`
	StepLimit  int  `json:"stepLimit"`
	HasMore    bool `json:"hasMore"`
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
	ModelSnapshot   json.RawMessage
	StartedAt       time.Time
	FinishedAt      *time.Time
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
}
