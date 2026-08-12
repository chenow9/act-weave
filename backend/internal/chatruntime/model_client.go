package chatruntime

import "encoding/json"

// ChatCompletionMessage is one OpenAI-compatible chat message used by the
// optional text-stream adapter surface (tests / streaming helpers).
type ChatCompletionMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is one model-requested function invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the callable name and JSON arguments string.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition is an OpenAI-compatible tools[] entry.
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

// ToolFunctionSchema describes one callable exposed to the model.
type ToolFunctionSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// CompletionRequest is one chat completion / text-stream invocation payload.
// Production agent traffic uses modelapi.NewOpenAIAgenticModel via chatruntimebridge;
// this type remains for the provider-neutral text stream adapter interface.
type CompletionRequest struct {
	Messages   []ChatCompletionMessage
	Tools      []ToolDefinition
	ToolChoice string // "auto" (default when tools present), "none", or empty
}
