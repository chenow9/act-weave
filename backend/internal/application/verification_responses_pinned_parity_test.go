package application

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

// Cycle 16 cumulative parity: cycle-14 full field contracts + cycle-15 immutable
// anchors/semantic identity. checkPinnedParity is pure over a clonable view;
// immutable manifests are external anchors.

const (
	idRegOutput         = "registry:output"
	idRegContent        = "registry:content"
	idRegContentMessage = "registry:content_message"
	idRegAnnotation     = "registry:annotation"
	idRegWebAction      = "registry:web_action"
	idRegCIOutput       = "registry:ci_output"
	idRegShellEnv       = "registry:shell_env"
	idRegShellOutcome   = "registry:shell_outcome"
)

// ---------------------------------------------------------------------------
// Exact token → identity tables (immutable)
// ---------------------------------------------------------------------------

var exactArrayElemIdentity = map[string]string{
	"content_part_message": idRegContentMessage,
	"annotation":           idRegAnnotation,
	"ci_output":            idRegCIOutput,
	"summary_text":         "element:nested.summary_text",
	"reasoning_text":       "element:nested.reasoning_text",
	"logprob":              "element:nested.logprob",
	"top_logprob":          "element:nested.top_logprob",
	"file_search_result":   "element:nested.file_search_result",
	"mcp_list_tool":        "element:nested.mcp_list_tool",
	"function_tool":        "element:nested.function_tool",
	"shell_output":         "element:nested.shell_output",
	"web_search_source":    "element:web_action.search_source",
	"string_queries":       "primitive:string",
	"byte_int":             "primitive:byte_int",
}

var exactNestedIdentity = map[string]string{
	"web_search_action":      idRegWebAction,
	"shell_action":           "element:nested.shell_action",
	"shell_environment":      idRegShellEnv,
	"shell_outcome":          idRegShellOutcome,
	"function_parameters":    "primitive:function_parameters",
	"file_search_attributes": "primitive:file_search_attributes",
}

// ---------------------------------------------------------------------------
// Field contracts (independent of production specs)
// ---------------------------------------------------------------------------

type fieldContract struct {
	Key               string
	Required          bool
	Nullable          bool
	KindClass         string // string|bool|int|number|object|array|any|domain|uri
	Format            string
	Domain            []string
	ArrayElemIdentity string
	NestedIdentity    string
}

type fieldSupp struct {
	Required          *bool
	Nullable          *bool
	Domain            []string
	KindClass         string
	Format            string
	ArrayElemIdentity string
	NestedIdentity    string
	// Omit from contract (type via registry exception only).
	Omit bool
}

func bptr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// Immutable member/registry anchors
// ---------------------------------------------------------------------------

type immutableMemberAnchor struct {
	Key        string
	MemberPath string
	Sample     any
	RegistryID string
	// TypeInSpecs when type is part of field specs (function_tool, summary_text, …).
	TypeInSpecs bool
	// FieldSupplements for domains/identities/required overrides on top of reflection.
	FieldSupplements        map[string]fieldSupp
	PositiveProbe           map[string]any
	NegativeProbe           map[string]any
	RequireNonNilEmptySpecs bool
}

type immutableRegistryAnchor struct {
	ID      string
	Members []immutableMemberAnchor
}

func immutableRegistryManifest() []immutableRegistryAnchor {
	return []immutableRegistryAnchor{
		{ID: idRegOutput, Members: outputMembers()},
		{ID: idRegContent, Members: contentMembers()},
		{ID: idRegContentMessage, Members: contentMessageMembers()},
		{ID: idRegAnnotation, Members: annotationMembers()},
		{ID: idRegWebAction, Members: webActionMembers()},
		{ID: idRegCIOutput, Members: ciOutputMembers()},
		{ID: idRegShellEnv, Members: shellEnvMembers()},
		{ID: idRegShellOutcome, Members: shellOutcomeMembers()},
	}
}

// Nested non-registry elements still need full field contracts.
func immutableElementMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "", MemberPath: "web_action.search_source", Sample: responses.ResponseFunctionWebSearchActionSearchSource{}, TypeInSpecs: true,
			FieldSupplements: map[string]fieldSupp{
				"type": {Domain: []string{"url"}, KindClass: "domain", Required: bptr(false)},
				"url":  {KindClass: "uri", Format: "uri", Required: bptr(true)},
			},
			PositiveProbe: map[string]any{"type": "url", "url": "https://example.com"},
			NegativeProbe: map[string]any{"url": "/rel"}},
		{Key: "", MemberPath: "nested.shell_output", Sample: responses.ResponseFunctionShellToolCallOutputOutput{},
			FieldSupplements: map[string]fieldSupp{
				"type":       {Omit: true},
				"outcome":    {NestedIdentity: idRegShellOutcome, Required: bptr(true), KindClass: "object"},
				"stdout":     {Required: bptr(true), KindClass: "string"},
				"stderr":     {Required: bptr(true), KindClass: "string"},
				"created_by": {Required: bptr(false), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"stdout": "", "stderr": "", "outcome": map[string]any{"type": "exit", "exit_code": 0}},
			NegativeProbe: map[string]any{"stdout": ""}},
		{Key: "", MemberPath: "nested.shell_action", Sample: responses.ResponseFunctionShellToolCallAction{},
			FieldSupplements: map[string]fieldSupp{
				"type":              {Omit: true},
				"commands":          {ArrayElemIdentity: "primitive:string", Required: bptr(true), KindClass: "array"},
				"max_output_length": {Required: bptr(true), KindClass: "int"},
				"timeout_ms":        {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"commands": []any{"x"}, "max_output_length": 1, "timeout_ms": 1},
			NegativeProbe: map[string]any{"commands": []any{"x"}}},
		{Key: "", MemberPath: "nested.file_search_result", Sample: responses.ResponseFileSearchToolCallResult{},
			FieldSupplements: map[string]fieldSupp{
				"type":       {Omit: true},
				"file_id":    {Required: bptr(false), KindClass: "string"},
				"filename":   {Required: bptr(false), KindClass: "string"},
				"text":       {Required: bptr(false), KindClass: "string"},
				"score":      {Required: bptr(false), KindClass: "number"},
				"attributes": {Required: bptr(false), Nullable: bptr(true), KindClass: "object", NestedIdentity: "primitive:file_search_attributes"},
			},
			PositiveProbe: map[string]any{"file_id": "f1"},
			NegativeProbe: map[string]any{"score": "x"}},
		{Key: "", MemberPath: "nested.mcp_list_tool", Sample: responses.ResponseOutputItemMcpListToolsTool{},
			FieldSupplements: map[string]fieldSupp{
				"type":         {Omit: true},
				"name":         {Required: bptr(true), KindClass: "string"},
				"input_schema": {Required: bptr(true), KindClass: "any"},
				"description":  {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"annotations":  {Required: bptr(false), Nullable: bptr(true), KindClass: "any"},
			},
			PositiveProbe: map[string]any{"name": "t", "input_schema": map[string]any{}},
			NegativeProbe: map[string]any{"name": "t"}},
		{Key: "function", MemberPath: "nested.function_tool", Sample: responses.FunctionTool{}, TypeInSpecs: true,
			FieldSupplements: map[string]fieldSupp{
				"type":          {Required: bptr(true), Domain: []string{"function"}, KindClass: "domain"},
				"name":          {Required: bptr(true), KindClass: "string"},
				"parameters":    {Required: bptr(true), KindClass: "object", NestedIdentity: "primitive:function_parameters"},
				"strict":        {Required: bptr(true), KindClass: "bool"},
				"description":   {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"defer_loading": {Required: bptr(false), KindClass: "bool"},
			},
			PositiveProbe: map[string]any{"type": "function", "name": "echo", "strict": true, "parameters": map[string]any{}},
			NegativeProbe: map[string]any{"type": "function", "name": "echo", "strict": true}},
		{Key: "summary_text", MemberPath: "nested.summary_text", Sample: responses.ResponseReasoningItemSummary{}, TypeInSpecs: true,
			FieldSupplements: map[string]fieldSupp{
				"type": {Required: bptr(true), Domain: []string{"summary_text"}, KindClass: "domain"},
				"text": {Required: bptr(true), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "summary_text", "text": "s"},
			NegativeProbe: map[string]any{"type": "summary_text"}},
		{Key: "reasoning_text", MemberPath: "nested.reasoning_text", Sample: responses.ResponseReasoningItemContent{}, TypeInSpecs: true,
			FieldSupplements: map[string]fieldSupp{
				"type": {Required: bptr(true), Domain: []string{"reasoning_text"}, KindClass: "domain"},
				"text": {Required: bptr(true), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "reasoning_text", "text": "t"},
			NegativeProbe: map[string]any{"type": "reasoning_text"}},
		{Key: "", MemberPath: "nested.logprob", Sample: responses.ResponseOutputTextLogprob{},
			FieldSupplements: map[string]fieldSupp{
				"type":         {Omit: true},
				"token":        {Required: bptr(true), KindClass: "string"},
				"bytes":        {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "primitive:byte_int"},
				"logprob":      {Required: bptr(true), KindClass: "number"},
				"top_logprobs": {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.top_logprob"},
			},
			PositiveProbe: map[string]any{"token": "a", "bytes": []any{1}, "logprob": 0.0, "top_logprobs": []any{}},
			NegativeProbe: map[string]any{"token": "a", "bytes": []any{1}, "logprob": 0.0}},
		{Key: "", MemberPath: "nested.top_logprob", Sample: responses.ResponseOutputTextLogprobTopLogprob{},
			FieldSupplements: map[string]fieldSupp{
				"type":    {Omit: true},
				"token":   {Required: bptr(true), KindClass: "string"},
				"bytes":   {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "primitive:byte_int"},
				"logprob": {Required: bptr(true), KindClass: "number"},
			},
			PositiveProbe: map[string]any{"token": "a", "bytes": []any{1}, "logprob": 0.0},
			NegativeProbe: map[string]any{"token": "a", "bytes": []any{1}}},
	}
}

func outputMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "message", MemberPath: "output.message", Sample: responses.ResponseOutputMessage{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type":    {Omit: true},
				"id":      {Required: bptr(true), KindClass: "string"},
				"content": {Required: bptr(true), KindClass: "array", ArrayElemIdentity: idRegContentMessage},
				"status":  {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
				"role":    {Required: bptr(true), KindClass: "domain", Domain: []string{"assistant"}},
				"phase":   {Required: bptr(false), Nullable: bptr(true), KindClass: "domain", Domain: []string{"commentary", "final_answer"}},
			},
			PositiveProbe: map[string]any{"type": "message", "id": "m1", "status": "completed", "role": "assistant", "content": []any{}},
			NegativeProbe: map[string]any{"type": "message", "id": "m1"}},
		{Key: "function_call", MemberPath: "output.function_call", Sample: responses.ResponseFunctionToolCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "arguments": {Required: bptr(true), KindClass: "string"},
				"call_id": {Required: bptr(true), KindClass: "string"}, "name": {Required: bptr(true), KindClass: "string"},
				"id": {Required: bptr(false), KindClass: "string"}, "namespace": {Required: bptr(false), KindClass: "string"},
				"status": {Required: bptr(false), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
			},
			PositiveProbe: map[string]any{"type": "function_call", "call_id": "c1", "name": "echo", "arguments": "{}"},
			NegativeProbe: map[string]any{"type": "function_call", "call_id": "c1"}},
		{Key: "reasoning", MemberPath: "output.reasoning", Sample: responses.ResponseReasoningItem{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"summary":           {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.summary_text"},
				"content":           {Required: bptr(false), KindClass: "array", ArrayElemIdentity: "element:nested.reasoning_text"},
				"encrypted_content": {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"status":            {Required: bptr(false), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
			},
			PositiveProbe: map[string]any{"type": "reasoning", "id": "r1", "summary": []any{}},
			NegativeProbe: map[string]any{"type": "reasoning", "id": "r1"}},
		{Key: "web_search_call", MemberPath: "output.web_search_call", Sample: responses.ResponseFunctionWebSearch{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"action": {Required: bptr(true), KindClass: "object", NestedIdentity: idRegWebAction},
				"status": {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "searching", "completed", "failed"}},
			},
			PositiveProbe: map[string]any{"type": "web_search_call", "id": "w1", "status": "completed", "action": map[string]any{"type": "search", "query": "q"}},
			NegativeProbe: map[string]any{"type": "web_search_call", "id": "w1", "status": "completed"}},
		{Key: "file_search_call", MemberPath: "output.file_search_call", Sample: responses.ResponseFileSearchToolCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"queries": {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "primitive:string"},
				"status":  {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "searching", "completed", "incomplete", "failed"}},
				"results": {Required: bptr(false), Nullable: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.file_search_result"},
			},
			PositiveProbe: map[string]any{"type": "file_search_call", "id": "f1", "status": "completed", "queries": []any{}},
			NegativeProbe: map[string]any{"type": "file_search_call", "id": "f1", "status": "completed"}},
		{Key: "code_interpreter_call", MemberPath: "output.code_interpreter_call", Sample: responses.ResponseCodeInterpreterToolCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"code":         {Required: bptr(true), Nullable: bptr(true), KindClass: "string"},
				"container_id": {Required: bptr(true), KindClass: "string"},
				"outputs":      {Required: bptr(true), Nullable: bptr(true), KindClass: "array", ArrayElemIdentity: idRegCIOutput},
				"status":       {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete", "interpreting", "failed"}},
			},
			PositiveProbe: map[string]any{"type": "code_interpreter_call", "id": "c1", "code": nil, "container_id": "ctr", "outputs": nil, "status": "completed"},
			NegativeProbe: map[string]any{"type": "code_interpreter_call", "id": "c1", "container_id": "ctr", "status": "completed"}},
		{Key: "image_generation_call", MemberPath: "output.image_generation_call", Sample: responses.ResponseInputItemImageGenerationCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"result": {Required: bptr(true), KindClass: "string"},
				"status": {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "generating", "failed"}},
			},
			PositiveProbe: map[string]any{"type": "image_generation_call", "id": "i1", "result": "", "status": "completed"},
			NegativeProbe: map[string]any{"type": "image_generation_call", "id": "i1", "status": "completed"}},
		{Key: "mcp_call", MemberPath: "output.mcp_call", Sample: responses.ResponseOutputItemMcpCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"arguments": {Required: bptr(true), KindClass: "string"}, "name": {Required: bptr(true), KindClass: "string"},
				"server_label":        {Required: bptr(true), KindClass: "string"},
				"approval_request_id": {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"error":               {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"output":              {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
				"status":              {Required: bptr(false), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete", "calling", "failed"}},
			},
			PositiveProbe: map[string]any{"type": "mcp_call", "id": "m1", "name": "t", "server_label": "s", "arguments": "{}"},
			NegativeProbe: map[string]any{"type": "mcp_call", "id": "m1", "name": "t"}},
		{Key: "mcp_list_tools", MemberPath: "output.mcp_list_tools", Sample: responses.ResponseOutputItemMcpListTools{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"server_label": {Required: bptr(true), KindClass: "string"},
				"tools":        {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.mcp_list_tool"},
				"error":        {Required: bptr(false), Nullable: bptr(true), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "mcp_list_tools", "id": "m1", "server_label": "s", "tools": []any{}},
			NegativeProbe: map[string]any{"type": "mcp_list_tools", "id": "m1", "server_label": "s"}},
		{Key: "mcp_approval_request", MemberPath: "output.mcp_approval_request", Sample: responses.ResponseOutputItemMcpApprovalRequest{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"arguments": {Required: bptr(true), KindClass: "string"}, "name": {Required: bptr(true), KindClass: "string"},
				"server_label": {Required: bptr(true), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "mcp_approval_request", "id": "m1", "name": "t", "server_label": "s", "arguments": "{}"},
			NegativeProbe: map[string]any{"type": "mcp_approval_request", "id": "m1"}},
		{Key: "tool_search_call", MemberPath: "output.tool_search_call", Sample: responses.ResponseToolSearchCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"arguments": {Required: bptr(true), KindClass: "any"}, "call_id": {Required: bptr(true), KindClass: "string"},
				"execution":  {Required: bptr(true), KindClass: "domain", Domain: []string{"server", "client"}},
				"status":     {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
				"created_by": {Required: bptr(false), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "tool_search_call", "id": "t1", "call_id": "c1", "arguments": map[string]any{}, "execution": "server", "status": "completed"},
			NegativeProbe: map[string]any{"type": "tool_search_call", "id": "t1", "call_id": "c1"}},
		{Key: "tool_search_output", MemberPath: "output.tool_search_output", Sample: responses.ResponseToolSearchOutputItem{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"call_id":    {Required: bptr(true), KindClass: "string"},
				"execution":  {Required: bptr(true), KindClass: "domain", Domain: []string{"server", "client"}},
				"status":     {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
				"tools":      {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.function_tool"},
				"created_by": {Required: bptr(false), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "tool_search_output", "id": "t1", "call_id": "c1", "execution": "client", "status": "completed", "tools": []any{}},
			NegativeProbe: map[string]any{"type": "tool_search_output", "id": "t1", "call_id": "c1", "execution": "client", "status": "completed"}},
		{Key: "shell_call", MemberPath: "output.shell_call", Sample: responses.ResponseFunctionShellToolCall{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"call_id":     {Required: bptr(true), KindClass: "string"},
				"status":      {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
				"action":      {Required: bptr(true), KindClass: "object", NestedIdentity: "element:nested.shell_action"},
				"environment": {Required: bptr(true), KindClass: "object", NestedIdentity: idRegShellEnv},
				"created_by":  {Required: bptr(false), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "shell_call", "id": "s1", "call_id": "c1", "status": "completed",
				"action":      map[string]any{"commands": []any{"x"}, "max_output_length": 1, "timeout_ms": 1},
				"environment": map[string]any{"type": "local"}},
			NegativeProbe: map[string]any{"type": "shell_call", "id": "s1", "call_id": "c1", "status": "completed"}},
		{Key: "shell_call_output", MemberPath: "output.shell_call_output", Sample: responses.ResponseFunctionShellToolCallOutput{}, RegistryID: idRegOutput,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "id": {Required: bptr(true), KindClass: "string"},
				"call_id":           {Required: bptr(true), KindClass: "string"},
				"max_output_length": {Required: bptr(true), KindClass: "int"},
				"output":            {Required: bptr(true), KindClass: "array", ArrayElemIdentity: "element:nested.shell_output"},
				"status":            {Required: bptr(true), KindClass: "domain", Domain: []string{"in_progress", "completed", "incomplete"}},
				"created_by":        {Required: bptr(false), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "shell_call_output", "id": "s1", "call_id": "c1", "max_output_length": 1, "status": "completed", "output": []any{}},
			NegativeProbe: map[string]any{"type": "shell_call_output", "id": "s1", "call_id": "c1", "status": "completed"}},
	}
}

func contentMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "output_text", MemberPath: "content.output_text", Sample: responses.ResponseOutputText{}, RegistryID: idRegContent,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "text": {Required: bptr(true), KindClass: "string"},
				"annotations": {Required: bptr(true), KindClass: "array", ArrayElemIdentity: idRegAnnotation},
				"logprobs":    {Required: bptr(false), Nullable: bptr(false), KindClass: "array", ArrayElemIdentity: "element:nested.logprob"},
			},
			PositiveProbe: map[string]any{"type": "output_text", "text": "x", "annotations": []any{}},
			NegativeProbe: map[string]any{"type": "output_text", "text": "x"}},
		{Key: "refusal", MemberPath: "content.refusal", Sample: responses.ResponseOutputRefusal{}, RegistryID: idRegContent,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}, "refusal": {Required: bptr(true), KindClass: "string"}},
			PositiveProbe:    map[string]any{"type": "refusal", "refusal": "no"}, NegativeProbe: map[string]any{"type": "refusal"}},
		{Key: "reasoning_text", MemberPath: "content.reasoning_text", Sample: responses.ResponseReasoningItemContent{}, RegistryID: idRegContent,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}, "text": {Required: bptr(true), KindClass: "string"}},
			PositiveProbe:    map[string]any{"type": "reasoning_text", "text": "why"}, NegativeProbe: map[string]any{"type": "reasoning_text"}},
	}
}

func contentMessageMembers() []immutableMemberAnchor {
	// Subset of content — same contracts for the two accepted members.
	all := contentMembers()
	out := make([]immutableMemberAnchor, 0, 2)
	for _, m := range all {
		if m.Key == "output_text" || m.Key == "refusal" {
			m.RegistryID = idRegContentMessage
			out = append(out, m)
		}
	}
	return out
}

func annotationMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "file_citation", MemberPath: "annotation.file_citation", Sample: responses.ResponseOutputTextAnnotationFileCitation{}, RegistryID: idRegAnnotation,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "file_id": {Required: bptr(true), KindClass: "string"},
				"filename": {Required: bptr(true), KindClass: "string"}, "index": {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"type": "file_citation", "file_id": "f1", "filename": "a.txt", "index": 0},
			NegativeProbe: map[string]any{"type": "file_citation", "file_id": "f1", "index": 0}},
		{Key: "url_citation", MemberPath: "annotation.url_citation", Sample: responses.ResponseOutputTextAnnotationURLCitation{}, RegistryID: idRegAnnotation,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "url": {Required: bptr(true), KindClass: "uri", Format: "uri"},
				"title":       {Required: bptr(true), KindClass: "string"},
				"start_index": {Required: bptr(true), KindClass: "int"}, "end_index": {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"type": "url_citation", "url": "https://example.com", "title": "t", "start_index": 0, "end_index": 1},
			NegativeProbe: map[string]any{"type": "url_citation", "url": "https://example.com", "title": "t", "start_index": 5, "end_index": 1}},
		{Key: "container_file_citation", MemberPath: "annotation.container_file_citation", Sample: responses.ResponseOutputTextAnnotationContainerFileCitation{}, RegistryID: idRegAnnotation,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "container_id": {Required: bptr(true), KindClass: "string"},
				"file_id": {Required: bptr(true), KindClass: "string"}, "filename": {Required: bptr(true), KindClass: "string"},
				"start_index": {Required: bptr(true), KindClass: "int"}, "end_index": {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"type": "container_file_citation", "container_id": "c1", "file_id": "f1", "filename": "a.txt", "start_index": 0, "end_index": 1},
			NegativeProbe: map[string]any{"type": "container_file_citation", "file_id": "f1", "filename": "a.txt", "start_index": 0, "end_index": 1}},
		{Key: "file_path", MemberPath: "annotation.file_path", Sample: responses.ResponseOutputTextAnnotationFilePath{}, RegistryID: idRegAnnotation,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "file_id": {Required: bptr(true), KindClass: "string"}, "index": {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"type": "file_path", "file_id": "f1", "index": 0},
			NegativeProbe: map[string]any{"type": "file_path", "index": 0}},
	}
}

func webActionMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "search", MemberPath: "web_action.search", Sample: responses.ResponseFunctionWebSearchActionSearch{}, RegistryID: idRegWebAction,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "query": {Required: bptr(true), KindClass: "string"},
				"queries": {Required: bptr(false), KindClass: "array", ArrayElemIdentity: "primitive:string"},
				"sources": {Required: bptr(false), KindClass: "array", ArrayElemIdentity: "element:web_action.search_source"},
			},
			PositiveProbe: map[string]any{"type": "search", "query": "q"}, NegativeProbe: map[string]any{"type": "search"}},
		{Key: "open_page", MemberPath: "web_action.open_page", Sample: responses.ResponseFunctionWebSearchActionOpenPage{}, RegistryID: idRegWebAction,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "url": {Required: bptr(false), Nullable: bptr(true), KindClass: "uri", Format: "uri"},
			},
			PositiveProbe: map[string]any{"type": "open_page"}, NegativeProbe: map[string]any{"type": "open_page", "url": 123}},
		{Key: "find_in_page", MemberPath: "web_action.find_in_page", Sample: responses.ResponseFunctionWebSearchActionFind{}, RegistryID: idRegWebAction,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "pattern": {Required: bptr(true), KindClass: "string"},
				"url": {Required: bptr(true), KindClass: "uri", Format: "uri"},
			},
			PositiveProbe: map[string]any{"type": "find_in_page", "pattern": "x", "url": "https://example.com"},
			NegativeProbe: map[string]any{"type": "find_in_page", "pattern": "x"}},
	}
}

func ciOutputMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "logs", MemberPath: "ci_output.logs", Sample: responses.ResponseCodeInterpreterToolCallOutputLogs{}, RegistryID: idRegCIOutput,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}, "logs": {Required: bptr(true), KindClass: "string"}},
			PositiveProbe:    map[string]any{"type": "logs", "logs": "out"}, NegativeProbe: map[string]any{"type": "logs"}},
		{Key: "image", MemberPath: "ci_output.image", Sample: responses.ResponseCodeInterpreterToolCallOutputImage{}, RegistryID: idRegCIOutput,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}, "url": {Required: bptr(true), KindClass: "uri", Format: "uri"}},
			PositiveProbe:    map[string]any{"type": "image", "url": "https://cdn.example.com/i.png"},
			NegativeProbe:    map[string]any{"type": "image", "url": "/rel"}},
	}
}

func shellEnvMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "local", MemberPath: "shell_env.local", Sample: responses.ResponseLocalEnvironment{}, RegistryID: idRegShellEnv,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}},
			PositiveProbe:    map[string]any{"type": "local"},
			// wrong type makes validators distinguishable after type-check
			NegativeProbe: map[string]any{"type": "timeout"}, RequireNonNilEmptySpecs: true},
		{Key: "container_reference", MemberPath: "shell_env.container_reference", Sample: responses.ResponseContainerReference{}, RegistryID: idRegShellEnv,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "container_id": {Required: bptr(true), KindClass: "string"},
			},
			PositiveProbe: map[string]any{"type": "container_reference", "container_id": "ctr"},
			NegativeProbe: map[string]any{"type": "container_reference"}},
	}
}

func shellOutcomeMembers() []immutableMemberAnchor {
	return []immutableMemberAnchor{
		{Key: "timeout", MemberPath: "shell_outcome.timeout", Sample: responses.ResponseFunctionShellToolCallOutputOutputOutcomeTimeout{}, RegistryID: idRegShellOutcome,
			FieldSupplements: map[string]fieldSupp{"type": {Omit: true}},
			PositiveProbe:    map[string]any{"type": "timeout"},
			NegativeProbe:    map[string]any{"type": "local"}, RequireNonNilEmptySpecs: true},
		{Key: "exit", MemberPath: "shell_outcome.exit", Sample: responses.ResponseFunctionShellToolCallOutputOutputOutcomeExit{}, RegistryID: idRegShellOutcome,
			FieldSupplements: map[string]fieldSupp{
				"type": {Omit: true}, "exit_code": {Required: bptr(true), KindClass: "int"},
			},
			PositiveProbe: map[string]any{"type": "exit", "exit_code": 0},
			NegativeProbe: map[string]any{"type": "exit"}},
	}
}

// ---------------------------------------------------------------------------
// Exception manifest
// ---------------------------------------------------------------------------

type immutableExceptionAnchor struct {
	MemberPath            string
	Field                 string
	Reason                string
	ExpectedDiscriminator string
	RegistryIdentity      string
	Sample                any
}

func immutableExceptionManifest() []immutableExceptionAnchor {
	const reason = "discriminator handled by closed registry dispatch"
	var out []immutableExceptionAnchor
	for _, reg := range immutableRegistryManifest() {
		for _, m := range reg.Members {
			if m.TypeInSpecs {
				continue
			}
			// type omitted from specs for registry members
			out = append(out, immutableExceptionAnchor{
				MemberPath: m.MemberPath, Field: "type", Reason: reason,
				ExpectedDiscriminator: m.Key, RegistryIdentity: reg.ID, Sample: m.Sample,
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Independent field extraction (not from production specs)
// ---------------------------------------------------------------------------

func independentFieldContracts(m immutableMemberAnchor) ([]fieldContract, error) {
	t := reflect.TypeOf(m.Sample)
	if t == nil {
		return nil, fmt.Errorf("%s nil sample", m.MemberPath)
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	supps := m.FieldSupplements
	if supps == nil {
		supps = map[string]fieldSupp{}
	}
	var out []fieldContract
	seen := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Name == "JSON" {
			continue
		}
		jt := f.Tag.Get("json")
		if jt == "" || jt == "-" {
			continue
		}
		name := strings.Split(jt, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		supp, hasSupp := supps[name]
		if hasSupp && supp.Omit {
			continue
		}
		if name == "type" && !m.TypeInSpecs {
			// Must be covered by exception, not field contract.
			continue
		}
		api := f.Tag.Get("api")
		required, nullable := false, false
		for _, p := range strings.Split(api, ",") {
			switch strings.TrimSpace(p) {
			case "required":
				required = true
			case "nullable":
				nullable = true
			}
		}
		format := f.Tag.Get("format")
		kind := classifyPinnedGoType(f.Type, format)
		fc := fieldContract{Key: name, Required: required, Nullable: nullable, KindClass: kind, Format: format}
		if hasSupp {
			if supp.Required != nil {
				fc.Required = *supp.Required
			}
			if supp.Nullable != nil {
				fc.Nullable = *supp.Nullable
			}
			if len(supp.Domain) > 0 {
				fc.Domain = append([]string(nil), supp.Domain...)
				fc.KindClass = "domain"
			}
			if supp.KindClass != "" {
				fc.KindClass = supp.KindClass
			}
			if supp.Format != "" {
				fc.Format = supp.Format
			}
			fc.ArrayElemIdentity = supp.ArrayElemIdentity
			fc.NestedIdentity = supp.NestedIdentity
		}
		if format == "uri" && fc.KindClass != "uri" {
			fc.KindClass = "uri"
		}
		if strings.HasPrefix(fc.KindClass, "UNCLASSIFIED:") {
			return nil, fmt.Errorf("%s.%s unclassified kind %s", m.MemberPath, name, fc.KindClass)
		}
		out = append(out, fc)
		seen[name] = true
	}
	// Supplements may declare fields not on struct? disallow — only allow overrides.
	for name, supp := range supps {
		if supp.Omit {
			continue
		}
		if !seen[name] && name != "type" {
			// type may be TypeInSpecs-only via supplement without being "seen" if omit false
			if m.TypeInSpecs && name == "type" {
				fc := fieldContract{Key: "type", Required: true, KindClass: "domain"}
				if supp.Required != nil {
					fc.Required = *supp.Required
				}
				if len(supp.Domain) > 0 {
					fc.Domain = append([]string(nil), supp.Domain...)
				}
				if supp.KindClass != "" {
					fc.KindClass = supp.KindClass
				}
				out = append(out, fc)
				continue
			}
			return nil, fmt.Errorf("%s: supplement for unknown field %q", m.MemberPath, name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func classifyPinnedGoType(t reflect.Type, format string) string {
	if format == "uri" {
		return "uri"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Interface:
		return "any"
	default:
		return "UNCLASSIFIED:" + t.String()
	}
}

func productionKindClass(sp pinnedFieldSpec) string {
	switch sp.Kind {
	case kindNonemptyString, kindString:
		return "string"
	case kindBool:
		return "bool"
	case kindNonNegInt, kindInt:
		return "int"
	case kindFiniteNumber:
		return "number"
	case kindObject:
		return "object"
	case kindArray:
		return "array"
	case kindAnyNonNull:
		return "any"
	case kindDomainString:
		return "domain"
	case kindURI:
		return "uri"
	default:
		return "UNCLASSIFIED"
	}
}

func kindsCompatible(indep, prod string) bool {
	if indep == prod {
		return true
	}
	// nonempty string still string class
	if indep == "string" && (prod == "domain" || prod == "uri") {
		return true
	}
	return false
}

func structTypeDefault(sample any) (bool, string) {
	t := reflect.TypeOf(sample)
	if t == nil {
		return false, ""
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if strings.Split(f.Tag.Get("json"), ",")[0] == "type" {
			return true, f.Tag.Get("default")
		}
	}
	return false, ""
}

func concreteTypeName(sample any) string {
	if sample == nil {
		return "<nil>"
	}
	return reflect.TypeOf(sample).String()
}

// ---------------------------------------------------------------------------
// Mutable view
// ---------------------------------------------------------------------------

type parityView struct {
	OutputRegistry         map[string]pinnedUnionMemberValidator
	ContentRegistry        map[string]pinnedUnionMemberValidator
	MessageContentRegistry map[string]pinnedUnionMemberValidator
	AnnotationRegistry     map[string]pinnedUnionMemberValidator
	WebActionRegistry      map[string]pinnedUnionMemberValidator
	CIOutputRegistry       map[string]pinnedUnionMemberValidator
	ShellEnvRegistry       map[string]pinnedUnionMemberValidator
	ShellOutcomeRegistry   map[string]pinnedUnionMemberValidator

	OutputItemSpecs       map[string][]pinnedFieldSpec
	ContentPartSpecs      map[string][]pinnedFieldSpec
	AnnotationSpecs       map[string][]pinnedFieldSpec
	WebSearchActionSpecs  map[string][]pinnedFieldSpec
	CIOutputSpecs         map[string][]pinnedFieldSpec
	ShellEnvSpecs         map[string][]pinnedFieldSpec
	ShellOutcomeSpecs     map[string][]pinnedFieldSpec
	WebSearchSourceSpecs  []pinnedFieldSpec
	ShellOutputElemSpecs  []pinnedFieldSpec
	ShellActionSpecs      []pinnedFieldSpec
	FileSearchResultSpecs []pinnedFieldSpec
	MCPListToolSpecs      []pinnedFieldSpec
	FunctionToolSpecs     []pinnedFieldSpec
	SummaryTextSpecs      []pinnedFieldSpec
	ReasoningTextSpecs    []pinnedFieldSpec
	LogprobSpecs          []pinnedFieldSpec
	TopLogprobSpecs       []pinnedFieldSpec

	OutputFixtures       map[string]func() map[string]any
	TypeOmitExceptions   []immutableExceptionAnchor
	SimulatedTypeDefault map[string]*string
	// Probe overrides for invalid-probe mutation tests
	ProbeOverrides map[string]struct{ Pos, Neg map[string]any }
}

func liveParityView() parityView {
	return parityView{
		OutputRegistry:         pinnedOutputItemRegistry,
		ContentRegistry:        pinnedContentPartRegistry,
		MessageContentRegistry: pinnedMessageContentPartRegistry,
		AnnotationRegistry:     pinnedAnnotationRegistry,
		WebActionRegistry:      pinnedWebSearchActionRegistry,
		CIOutputRegistry:       pinnedCodeInterpreterOutputRegistry,
		ShellEnvRegistry:       pinnedShellEnvironmentRegistry,
		ShellOutcomeRegistry:   pinnedShellOutcomeRegistry,
		OutputItemSpecs:        outputItemSpecs,
		ContentPartSpecs:       contentPartSpecs,
		AnnotationSpecs:        annotationSpecs,
		WebSearchActionSpecs:   webSearchActionSpecs,
		CIOutputSpecs:          ciOutputSpecs,
		ShellEnvSpecs:          shellEnvSpecs,
		ShellOutcomeSpecs:      shellOutcomeSpecs,
		WebSearchSourceSpecs:   webSearchSourceSpecs,
		ShellOutputElemSpecs:   shellOutputElemSpecs,
		ShellActionSpecs:       shellActionSpecs,
		FileSearchResultSpecs:  fileSearchResultSpecs,
		MCPListToolSpecs:       mcpListToolSpecs,
		FunctionToolSpecs:      functionToolSpecs,
		SummaryTextSpecs:       summaryTextElementSpecs,
		ReasoningTextSpecs:     reasoningTextElementSpecs,
		LogprobSpecs:           logprobElementSpecs,
		TopLogprobSpecs:        topLogprobElementSpecs,
		OutputFixtures:         outputItemPositiveFixtures,
		TypeOmitExceptions:     immutableExceptionManifest(),
		SimulatedTypeDefault:   map[string]*string{},
		ProbeOverrides:         map[string]struct{ Pos, Neg map[string]any }{},
	}
}

func cloneValMap(in map[string]pinnedUnionMemberValidator) map[string]pinnedUnionMemberValidator {
	out := make(map[string]pinnedUnionMemberValidator, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneSpecsMap(in map[string][]pinnedFieldSpec) map[string][]pinnedFieldSpec {
	out := make(map[string][]pinnedFieldSpec, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		cp := make([]pinnedFieldSpec, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
func cloneSpecsSlice(in []pinnedFieldSpec) []pinnedFieldSpec {
	if in == nil {
		return nil
	}
	cp := make([]pinnedFieldSpec, len(in))
	copy(cp, in)
	return cp
}

func cloneLiveParityView() parityView {
	v := liveParityView()
	v.OutputRegistry = cloneValMap(v.OutputRegistry)
	v.ContentRegistry = cloneValMap(v.ContentRegistry)
	v.MessageContentRegistry = cloneValMap(v.MessageContentRegistry)
	v.AnnotationRegistry = cloneValMap(v.AnnotationRegistry)
	v.WebActionRegistry = cloneValMap(v.WebActionRegistry)
	v.CIOutputRegistry = cloneValMap(v.CIOutputRegistry)
	v.ShellEnvRegistry = cloneValMap(v.ShellEnvRegistry)
	v.ShellOutcomeRegistry = cloneValMap(v.ShellOutcomeRegistry)
	v.OutputItemSpecs = cloneSpecsMap(v.OutputItemSpecs)
	v.ContentPartSpecs = cloneSpecsMap(v.ContentPartSpecs)
	v.AnnotationSpecs = cloneSpecsMap(v.AnnotationSpecs)
	v.WebSearchActionSpecs = cloneSpecsMap(v.WebSearchActionSpecs)
	v.CIOutputSpecs = cloneSpecsMap(v.CIOutputSpecs)
	v.ShellEnvSpecs = cloneSpecsMap(v.ShellEnvSpecs)
	v.ShellOutcomeSpecs = cloneSpecsMap(v.ShellOutcomeSpecs)
	v.WebSearchSourceSpecs = cloneSpecsSlice(v.WebSearchSourceSpecs)
	v.ShellOutputElemSpecs = cloneSpecsSlice(v.ShellOutputElemSpecs)
	v.ShellActionSpecs = cloneSpecsSlice(v.ShellActionSpecs)
	v.FileSearchResultSpecs = cloneSpecsSlice(v.FileSearchResultSpecs)
	v.MCPListToolSpecs = cloneSpecsSlice(v.MCPListToolSpecs)
	v.FunctionToolSpecs = cloneSpecsSlice(v.FunctionToolSpecs)
	v.SummaryTextSpecs = cloneSpecsSlice(v.SummaryTextSpecs)
	v.ReasoningTextSpecs = cloneSpecsSlice(v.ReasoningTextSpecs)
	v.LogprobSpecs = cloneSpecsSlice(v.LogprobSpecs)
	v.TopLogprobSpecs = cloneSpecsSlice(v.TopLogprobSpecs)
	fix := make(map[string]func() map[string]any, len(v.OutputFixtures))
	for k, fn := range v.OutputFixtures {
		fix[k] = fn
	}
	v.OutputFixtures = fix
	v.TypeOmitExceptions = append([]immutableExceptionAnchor(nil), v.TypeOmitExceptions...)
	sim := make(map[string]*string)
	for k, p := range v.SimulatedTypeDefault {
		if p == nil {
			sim[k] = nil
			continue
		}
		s := *p
		sim[k] = &s
	}
	v.SimulatedTypeDefault = sim
	v.ProbeOverrides = map[string]struct{ Pos, Neg map[string]any }{}
	return v
}

func (v parityView) productionRegistry(id string) map[string]pinnedUnionMemberValidator {
	switch id {
	case idRegOutput:
		return v.OutputRegistry
	case idRegContent:
		return v.ContentRegistry
	case idRegContentMessage:
		return v.MessageContentRegistry
	case idRegAnnotation:
		return v.AnnotationRegistry
	case idRegWebAction:
		return v.WebActionRegistry
	case idRegCIOutput:
		return v.CIOutputRegistry
	case idRegShellEnv:
		return v.ShellEnvRegistry
	case idRegShellOutcome:
		return v.ShellOutcomeRegistry
	default:
		return nil
	}
}

func (v parityView) specsForPath(path string) []pinnedFieldSpec {
	switch path {
	case "output.message":
		return v.OutputItemSpecs["message"]
	case "output.function_call":
		return v.OutputItemSpecs["function_call"]
	case "output.reasoning":
		return v.OutputItemSpecs["reasoning"]
	case "output.web_search_call":
		return v.OutputItemSpecs["web_search_call"]
	case "output.file_search_call":
		return v.OutputItemSpecs["file_search_call"]
	case "output.code_interpreter_call":
		return v.OutputItemSpecs["code_interpreter_call"]
	case "output.image_generation_call":
		return v.OutputItemSpecs["image_generation_call"]
	case "output.mcp_call":
		return v.OutputItemSpecs["mcp_call"]
	case "output.mcp_list_tools":
		return v.OutputItemSpecs["mcp_list_tools"]
	case "output.mcp_approval_request":
		return v.OutputItemSpecs["mcp_approval_request"]
	case "output.tool_search_call":
		return v.OutputItemSpecs["tool_search_call"]
	case "output.tool_search_output":
		return v.OutputItemSpecs["tool_search_output"]
	case "output.shell_call":
		return v.OutputItemSpecs["shell_call"]
	case "output.shell_call_output":
		return v.OutputItemSpecs["shell_call_output"]
	case "content.output_text":
		return v.ContentPartSpecs["output_text"]
	case "content.refusal":
		return v.ContentPartSpecs["refusal"]
	case "content.reasoning_text":
		return v.ContentPartSpecs["reasoning_text"]
	case "annotation.file_citation":
		return v.AnnotationSpecs["file_citation"]
	case "annotation.url_citation":
		return v.AnnotationSpecs["url_citation"]
	case "annotation.container_file_citation":
		return v.AnnotationSpecs["container_file_citation"]
	case "annotation.file_path":
		return v.AnnotationSpecs["file_path"]
	case "web_action.search":
		return v.WebSearchActionSpecs["search"]
	case "web_action.open_page":
		return v.WebSearchActionSpecs["open_page"]
	case "web_action.find_in_page":
		return v.WebSearchActionSpecs["find_in_page"]
	case "web_action.search_source":
		return v.WebSearchSourceSpecs
	case "ci_output.logs":
		return v.CIOutputSpecs["logs"]
	case "ci_output.image":
		return v.CIOutputSpecs["image"]
	case "shell_env.local":
		return v.ShellEnvSpecs["local"]
	case "shell_env.container_reference":
		return v.ShellEnvSpecs["container_reference"]
	case "shell_outcome.timeout":
		return v.ShellOutcomeSpecs["timeout"]
	case "shell_outcome.exit":
		return v.ShellOutcomeSpecs["exit"]
	case "nested.shell_output":
		return v.ShellOutputElemSpecs
	case "nested.shell_action":
		return v.ShellActionSpecs
	case "nested.file_search_result":
		return v.FileSearchResultSpecs
	case "nested.mcp_list_tool":
		return v.MCPListToolSpecs
	case "nested.function_tool":
		return v.FunctionToolSpecs
	case "nested.summary_text":
		return v.SummaryTextSpecs
	case "nested.reasoning_text":
		return v.ReasoningTextSpecs
	case "nested.logprob":
		return v.LogprobSpecs
	case "nested.top_logprob":
		return v.TopLogprobSpecs
	default:
		return nil
	}
}

func asRawObj(m map[string]any) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func sortedDomain(d map[string]struct{}) []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Core checker
// ---------------------------------------------------------------------------

func checkPinnedParity(v parityView) error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	manifest := immutableRegistryManifest()
	elements := immutableElementMembers()
	immEx := immutableExceptionManifest()

	// Exception bidirectional equality
	exKey := func(e immutableExceptionAnchor) string {
		return strings.Join([]string{e.MemberPath, e.Field, e.Reason, e.ExpectedDiscriminator, e.RegistryIdentity, concreteTypeName(e.Sample)}, "\x00")
	}
	immExSet := map[string]immutableExceptionAnchor{}
	for _, e := range immEx {
		immExSet[exKey(e)] = e
	}
	viewExSet := map[string]immutableExceptionAnchor{}
	for _, e := range v.TypeOmitExceptions {
		viewExSet[exKey(e)] = e
	}
	for k := range immExSet {
		if _, ok := viewExSet[k]; !ok {
			add("exception missing from view: %s", strings.ReplaceAll(k, "\x00", " | "))
		}
	}
	for k := range viewExSet {
		if _, ok := immExSet[k]; !ok {
			add("exception extra in view: %s", strings.ReplaceAll(k, "\x00", " | "))
		}
	}

	// Collect all members for field parity (registry + nested elements)
	allMembers := make([]immutableMemberAnchor, 0, 64)
	for _, reg := range manifest {
		allMembers = append(allMembers, reg.Members...)
	}
	allMembers = append(allMembers, elements...)

	// Validate probes are well-formed: positive != negative when both set; positive has type when registry member.
	for _, m := range allMembers {
		pos, neg := m.PositiveProbe, m.NegativeProbe
		if ov, ok := v.ProbeOverrides[m.MemberPath]; ok {
			if ov.Pos != nil {
				pos = ov.Pos
			}
			if ov.Neg != nil {
				neg = ov.Neg
			}
		}
		if pos == nil {
			add("%s: missing positive probe definition", m.MemberPath)
			continue
		}
		if neg == nil {
			add("%s: missing negative probe definition", m.MemberPath)
			continue
		}
		// Positive and negative must not be identical definitions.
		pb, _ := json.Marshal(pos)
		nb, _ := json.Marshal(neg)
		if string(pb) == string(nb) {
			add("%s: positive and negative probes are identical (invalid definition)", m.MemberPath)
		}
		if m.Key != "" {
			if t, _ := pos["type"].(string); t != m.Key {
				add("%s: positive probe type %q != key %q", m.MemberPath, t, m.Key)
			}
		}
	}

	// Registry membership + semantic identity (immutable → production)
	for _, reg := range manifest {
		prod := v.productionRegistry(reg.ID)
		if prod == nil {
			add("immutable registry %s missing production map", reg.ID)
			continue
		}
		immKeys := map[string]immutableMemberAnchor{}
		for _, m := range reg.Members {
			immKeys[m.Key] = m
		}
		for key, m := range immKeys {
			val, ok := prod[key]
			if !ok {
				add("%s: production missing %q", reg.ID, key)
				continue
			}
			if val == nil {
				add("%s: nil validator %q", reg.ID, key)
				continue
			}
			// Discriminator default
			present, def := structTypeDefault(m.Sample)
			if !present {
				add("%s/%s missing type field on struct", reg.ID, key)
			}
			if sim, has := v.SimulatedTypeDefault[m.MemberPath]; has {
				if sim == nil {
					def = ""
				} else {
					def = *sim
				}
			}
			if def == "" {
				add("%s/%s: type default/constant absent", reg.ID, key)
			} else if def != key {
				add("%s/%s: type default %q != key %q", reg.ID, key, def, key)
			}
			// Empty specs non-nil
			if m.RequireNonNilEmptySpecs {
				var specs []pinnedFieldSpec
				var has bool
				switch reg.ID {
				case idRegShellEnv:
					specs, has = v.ShellEnvSpecs[key]
				case idRegShellOutcome:
					specs, has = v.ShellOutcomeSpecs[key]
				}
				if !has {
					add("%s/%s: specs key missing", reg.ID, key)
				} else if specs == nil {
					add("%s/%s: required non-nil empty specs is nil", reg.ID, key)
				}
			}
			// Semantic probes
			pos := m.PositiveProbe
			neg := m.NegativeProbe
			if ov, ok := v.ProbeOverrides[m.MemberPath]; ok {
				if ov.Pos != nil {
					pos = ov.Pos
				}
				if ov.Neg != nil {
					neg = ov.Neg
				}
			}
			posRaw, err := asRawObj(pos)
			if err != nil {
				add("%s/%s positive marshal: %v", reg.ID, key, err)
			} else if err := val(posRaw); err != nil {
				add("%s/%s positive rejected: %v", reg.ID, key, err)
			}
			negRaw, err := asRawObj(neg)
			if err != nil {
				add("%s/%s negative marshal: %v", reg.ID, key, err)
			} else if err := val(negRaw); err == nil {
				add("%s/%s negative incorrectly accepted", reg.ID, key)
			}
			// Cross: positive of this key must fail at least one other validator (type check ensures)
			// After type-bound validators, any other key's validator rejects wrong type.
			if len(immKeys) > 1 {
				distinguished := false
				for otherKey, otherVal := range prod {
					if otherKey == key || otherVal == nil {
						continue
					}
					if otherVal(posRaw) != nil {
						distinguished = true
						break
					}
				}
				if !distinguished {
					add("%s/%s not distinguished from other members", reg.ID, key)
				}
			}
		}
		for key, val := range prod {
			if _, ok := immKeys[key]; !ok {
				add("%s: production extra key %q", reg.ID, key)
			}
			if val == nil {
				add("%s: nil validator %q", reg.ID, key)
			}
		}
		if reg.ID == idRegContentMessage {
			want := append([]string(nil), messageContentPartDiscriminators...)
			sort.Strings(want)
			got := sortedKeys(prod)
			if !reflect.DeepEqual(got, want) {
				add("content_message keys %v != messageContentPartDiscriminators %v", got, want)
			}
			if _, ok := prod["reasoning_text"]; ok {
				add("content_message must not include reasoning_text")
			}
		}
	}

	// Full field-level parity for every member
	for _, m := range allMembers {
		indep, err := independentFieldContracts(m)
		if err != nil {
			add("%v", err)
			continue
		}
		prod := v.specsForPath(m.MemberPath)
		if prod == nil && len(indep) > 0 && !m.RequireNonNilEmptySpecs {
			// empty members still have path with empty slice
			if !(m.RequireNonNilEmptySpecs) {
				// check empty
			}
		}
		if prod == nil && len(indep) > 0 {
			// shell local has empty non-nil
			if specs := v.specsForPath(m.MemberPath); specs == nil && len(indep) > 0 {
				// specsForPath returns nil only if key missing from map; empty slice is non-nil
			}
		}
		// Detect missing path: for empty-spec members, map must have key
		if m.RequireNonNilEmptySpecs {
			// already checked
		} else if v.specsForPath(m.MemberPath) == nil && len(indep) > 0 {
			add("%s: production specs missing", m.MemberPath)
			continue
		}
		prodSpecs := v.specsForPath(m.MemberPath)
		if prodSpecs == nil {
			prodSpecs = []pinnedFieldSpec{}
		}
		prodBy := map[string]pinnedFieldSpec{}
		for _, sp := range prodSpecs {
			prodBy[sp.Key] = sp
		}
		indepBy := map[string]fieldContract{}
		for _, fc := range indep {
			indepBy[fc.Key] = fc
		}
		for _, fc := range indep {
			sp, ok := prodBy[fc.Key]
			if !ok {
				add("%s: field %q missing from production specs", m.MemberPath, fc.Key)
				continue
			}
			if sp.Required != fc.Required {
				add("%s.%s required prod=%v indep=%v", m.MemberPath, fc.Key, sp.Required, fc.Required)
			}
			if sp.Nullable != fc.Nullable {
				add("%s.%s nullable prod=%v indep=%v", m.MemberPath, fc.Key, sp.Nullable, fc.Nullable)
			}
			pk := productionKindClass(sp)
			if !kindsCompatible(fc.KindClass, pk) {
				add("%s.%s kind indep=%q prod=%q", m.MemberPath, fc.Key, fc.KindClass, pk)
			}
			if fc.Format != "" && sp.Format != fc.Format {
				add("%s.%s format prod=%q indep=%q", m.MemberPath, fc.Key, sp.Format, fc.Format)
			}
			if fc.KindClass == "domain" || pk == "domain" {
				got := sortedDomain(sp.Domain)
				want := append([]string(nil), fc.Domain...)
				sort.Strings(want)
				if len(want) > 0 && !reflect.DeepEqual(got, want) {
					add("%s.%s domain prod=%v indep=%v", m.MemberPath, fc.Key, got, want)
				}
			}
			// ArrayElem exact identity
			if fc.ArrayElemIdentity != "" {
				if sp.Kind != kindArray {
					add("%s.%s expected array", m.MemberPath, fc.Key)
				} else {
					mapped, ok := exactArrayElemIdentity[sp.ArrayElem]
					if !ok {
						add("%s.%s ArrayElem token %q unknown", m.MemberPath, fc.Key, sp.ArrayElem)
					} else if mapped != fc.ArrayElemIdentity {
						add("%s.%s ArrayElem identity prod=%q want=%q (token %q)", m.MemberPath, fc.Key, mapped, fc.ArrayElemIdentity, sp.ArrayElem)
					}
					// If registry identity, prove target registry exists and is closed.
					if strings.HasPrefix(mapped, "registry:") {
						if v.productionRegistry(mapped) == nil {
							add("%s.%s ArrayElem target registry %s missing", m.MemberPath, fc.Key, mapped)
						}
					}
				}
			} else if sp.Kind == kindArray && sp.ArrayElem != "" {
				// production has ArrayElem but independent didn't expect — still must resolve
				if _, ok := exactArrayElemIdentity[sp.ArrayElem]; !ok {
					add("%s.%s production ArrayElem %q not in identity table", m.MemberPath, fc.Key, sp.ArrayElem)
				}
			}
			// Nested exact identity
			if fc.NestedIdentity != "" {
				if sp.Kind != kindObject {
					add("%s.%s expected object for Nested", m.MemberPath, fc.Key)
				} else {
					mapped, ok := exactNestedIdentity[sp.Nested]
					if !ok {
						add("%s.%s Nested token %q unknown", m.MemberPath, fc.Key, sp.Nested)
					} else if mapped != fc.NestedIdentity {
						add("%s.%s Nested identity prod=%q want=%q", m.MemberPath, fc.Key, mapped, fc.NestedIdentity)
					}
					if strings.HasPrefix(mapped, "registry:") && v.productionRegistry(mapped) == nil {
						add("%s.%s Nested target registry %s missing", m.MemberPath, fc.Key, mapped)
					}
				}
			} else if sp.Kind == kindObject && sp.Nested != "" {
				if _, ok := exactNestedIdentity[sp.Nested]; !ok {
					add("%s.%s production Nested %q not in identity table", m.MemberPath, fc.Key, sp.Nested)
				}
			}
		}
		for _, sp := range prodSpecs {
			if _, ok := indepBy[sp.Key]; !ok {
				add("%s: production extra field %q", m.MemberPath, sp.Key)
			}
		}
	}

	// Every production ArrayElem/Nested token on any known path must resolve (global scan of view maps)
	scanSpecs := func(label string, specs []pinnedFieldSpec) {
		for _, sp := range specs {
			if sp.Kind == kindArray && sp.ArrayElem != "" {
				if _, ok := exactArrayElemIdentity[sp.ArrayElem]; !ok {
					add("%s: ArrayElem token %q unresolved", label, sp.ArrayElem)
				}
			}
			if sp.Kind == kindObject && sp.Nested != "" {
				if _, ok := exactNestedIdentity[sp.Nested]; !ok {
					add("%s: Nested token %q unresolved", label, sp.Nested)
				}
			}
		}
	}
	for k, specs := range v.OutputItemSpecs {
		scanSpecs("output."+k, specs)
	}
	for k, specs := range v.ContentPartSpecs {
		scanSpecs("content."+k, specs)
	}
	for k, specs := range v.AnnotationSpecs {
		scanSpecs("annotation."+k, specs)
	}
	for k, specs := range v.WebSearchActionSpecs {
		scanSpecs("web_action."+k, specs)
	}
	scanSpecs("nested.shell_output", v.ShellOutputElemSpecs)
	scanSpecs("nested.function_tool", v.FunctionToolSpecs)
	scanSpecs("nested.logprob", v.LogprobSpecs)

	// Output fixtures
	for _, m := range manifest[0].Members {
		fn := v.OutputFixtures[m.Key]
		if fn == nil {
			add("fixture missing %q", m.Key)
			continue
		}
		fix := fn()
		if fix == nil {
			add("fixture %q nil", m.Key)
			continue
		}
		if t, _ := fix["type"].(string); t != m.Key {
			add("fixture %q type %q", m.Key, t)
		}
		raw, err := asRawObj(fix)
		if err != nil {
			add("fixture %q marshal: %v", m.Key, err)
			continue
		}
		if val := v.OutputRegistry[m.Key]; val != nil {
			if err := val(raw); err != nil {
				add("fixture %q rejected: %v", m.Key, err)
			}
		}
	}

	// Identity table targets must be required registries
	immIDs := map[string]struct{}{}
	for _, reg := range manifest {
		immIDs[reg.ID] = struct{}{}
	}
	for tok, id := range exactArrayElemIdentity {
		if strings.HasPrefix(id, "registry:") {
			if _, ok := immIDs[id]; !ok {
				add("ArrayElem %q → unknown registry %s", tok, id)
			}
		}
	}
	for tok, id := range exactNestedIdentity {
		if strings.HasPrefix(id, "registry:") {
			if _, ok := immIDs[id]; !ok {
				add("Nested %q → unknown registry %s", tok, id)
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("pinned parity failures (%d):\n  - %s", len(errs), strings.Join(errs, "\n  - "))
}

// ---------------------------------------------------------------------------
// Tests + cumulative mutation ledger
// ---------------------------------------------------------------------------

func TestIndependentPinnedParity_Closed(t *testing.T) {
	if err := checkPinnedParity(liveParityView()); err != nil {
		t.Fatal(err)
	}
}

type parityMutation struct {
	name   string
	mutate func(v *parityView) (changed bool)
}

func TestPinnedParity_CumulativeMutationLedger(t *testing.T) {
	ledger := []parityMutation{
		// Field regressions
		{"delete_message_phase", func(v *parityView) bool {
			specs := v.OutputItemSpecs["message"]
			out := specs[:0:0]
			for _, sp := range specs {
				if sp.Key == "phase" {
					continue
				}
				out = append(out, sp)
			}
			if len(out) == len(specs) {
				return false
			}
			v.OutputItemSpecs["message"] = out
			return true
		}},
		{"message_content_arrayelem_unknown", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "content" {
					specs[i].ArrayElem = "unknown"
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"message_content_arrayelem_annotation", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "content" {
					specs[i].ArrayElem = "annotation"
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"web_action_nested_unknown", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["web_search_call"]...)
			for i := range specs {
				if specs[i].Key == "action" {
					specs[i].Nested = "unknown"
					v.OutputItemSpecs["web_search_call"] = specs
					return true
				}
			}
			return false
		}},
		{"web_action_nested_shell_environment", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["web_search_call"]...)
			for i := range specs {
				if specs[i].Key == "action" {
					specs[i].Nested = "shell_environment"
					v.OutputItemSpecs["web_search_call"] = specs
					return true
				}
			}
			return false
		}},
		{"message_status_required_false", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "status" {
					specs[i].Required = false
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"message_phase_nullable_false", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "phase" {
					specs[i].Nullable = false
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"message_id_kind_bool", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "id" {
					specs[i].Kind = kindBool
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"message_role_domain_wrong", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			for i := range specs {
				if specs[i].Key == "role" {
					specs[i].Domain = map[string]struct{}{"user": {}}
					v.OutputItemSpecs["message"] = specs
					return true
				}
			}
			return false
		}},
		{"url_citation_format_cleared", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.AnnotationSpecs["url_citation"]...)
			for i := range specs {
				if specs[i].Key == "url" {
					specs[i].Format = ""
					specs[i].Kind = kindNonemptyString
					v.AnnotationSpecs["url_citation"] = specs
					return true
				}
			}
			return false
		}},
		{"message_extra_field", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["message"]...)
			specs = append(specs, pinnedFieldSpec{Key: "totally_extra", Kind: kindString})
			v.OutputItemSpecs["message"] = specs
			return true
		}},
		{"function_call_delete_namespace", func(v *parityView) bool {
			specs := v.OutputItemSpecs["function_call"]
			out := make([]pinnedFieldSpec, 0, len(specs))
			for _, sp := range specs {
				if sp.Key == "namespace" {
					continue
				}
				out = append(out, sp)
			}
			if len(out) == len(specs) {
				return false
			}
			v.OutputItemSpecs["function_call"] = out
			return true
		}},
		{"logprob_arrayelem_wrong", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.LogprobSpecs...)
			for i := range specs {
				if specs[i].Key == "top_logprobs" {
					specs[i].ArrayElem = "annotation"
					v.LogprobSpecs = specs
					return true
				}
			}
			return false
		}},
		{"shell_call_env_nested_wrong", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.OutputItemSpecs["shell_call"]...)
			for i := range specs {
				if specs[i].Key == "environment" {
					specs[i].Nested = "web_search_action"
					v.OutputItemSpecs["shell_call"] = specs
					return true
				}
			}
			return false
		}},
		// Semantic / closure
		{"exception_reason_only", func(v *parityView) bool {
			if len(v.TypeOmitExceptions) == 0 {
				return false
			}
			v.TypeOmitExceptions[0].Reason = "MUTATED_REASON_ONLY"
			return true
		}},
		{"exception_field_only", func(v *parityView) bool {
			if len(v.TypeOmitExceptions) == 0 {
				return false
			}
			v.TypeOmitExceptions[0].Field = "not_type"
			return true
		}},
		{"exception_registry_only", func(v *parityView) bool {
			if len(v.TypeOmitExceptions) == 0 {
				return false
			}
			v.TypeOmitExceptions[0].RegistryIdentity = "registry:wrong"
			return true
		}},
		{"exception_discriminator_only", func(v *parityView) bool {
			if len(v.TypeOmitExceptions) == 0 {
				return false
			}
			v.TypeOmitExceptions[0].ExpectedDiscriminator = "not_the_key"
			return true
		}},
		{"exception_path_only", func(v *parityView) bool {
			if len(v.TypeOmitExceptions) == 0 {
				return false
			}
			v.TypeOmitExceptions[0].MemberPath = "output.not_a_path"
			return true
		}},
		{"exception_concrete_type_swap", func(v *parityView) bool {
			var i, j int = -1, -1
			for idx, e := range v.TypeOmitExceptions {
				if e.MemberPath == "annotation.file_citation" {
					i = idx
				}
				if e.MemberPath == "annotation.file_path" {
					j = idx
				}
			}
			if i < 0 || j < 0 {
				return false
			}
			v.TypeOmitExceptions[i].Sample, v.TypeOmitExceptions[j].Sample =
				v.TypeOmitExceptions[j].Sample, v.TypeOmitExceptions[i].Sample
			return true
		}},
		{"discriminator_default_removed", func(v *parityView) bool {
			empty := ""
			v.SimulatedTypeDefault["annotation.file_path"] = &empty
			return true
		}},
		{"annotation_validators_swapped", func(v *parityView) bool {
			a, b := v.AnnotationRegistry["file_citation"], v.AnnotationRegistry["file_path"]
			if a == nil || b == nil {
				return false
			}
			v.AnnotationRegistry["file_citation"], v.AnnotationRegistry["file_path"] = b, a
			return true
		}},
		{"ci_validators_swapped", func(v *parityView) bool {
			a, b := v.CIOutputRegistry["logs"], v.CIOutputRegistry["image"]
			if a == nil || b == nil {
				return false
			}
			v.CIOutputRegistry["logs"], v.CIOutputRegistry["image"] = b, a
			return true
		}},
		{"local_timeout_validators_swapped", func(v *parityView) bool {
			a, b := v.ShellEnvRegistry["local"], v.ShellOutcomeRegistry["timeout"]
			if a == nil || b == nil {
				return false
			}
			// cross-registry swap into shell env map keys
			v.ShellEnvRegistry["local"] = b
			v.ShellOutcomeRegistry["timeout"] = a
			return true
		}},
		{"mcp_call_approval_validators_swapped", func(v *parityView) bool {
			a, b := v.OutputRegistry["mcp_call"], v.OutputRegistry["mcp_approval_request"]
			if a == nil || b == nil {
				return false
			}
			v.OutputRegistry["mcp_call"], v.OutputRegistry["mcp_approval_request"] = b, a
			return true
		}},
		{"output_fixtures_swapped", func(v *parityView) bool {
			if v.OutputFixtures["message"] == nil || v.OutputFixtures["function_call"] == nil {
				return false
			}
			v.OutputFixtures["message"], v.OutputFixtures["function_call"] =
				v.OutputFixtures["function_call"], v.OutputFixtures["message"]
			return true
		}},
		{"empty_spec_nil", func(v *parityView) bool {
			v.ShellEnvSpecs["local"] = nil
			return true
		}},
		{"ci_lockstep_delete", func(v *parityView) bool {
			v.CIOutputRegistry = map[string]pinnedUnionMemberValidator{}
			v.CIOutputSpecs = map[string][]pinnedFieldSpec{}
			return true
		}},
		{"annotation_registry_removed", func(v *parityView) bool {
			v.AnnotationRegistry = map[string]pinnedUnionMemberValidator{}
			return true
		}},
		{"content_message_add_reasoning", func(v *parityView) bool {
			if v.ContentRegistry["reasoning_text"] == nil {
				return false
			}
			v.MessageContentRegistry["reasoning_text"] = v.ContentRegistry["reasoning_text"]
			return true
		}},
		{"content_message_remove_refusal", func(v *parityView) bool {
			if _, ok := v.MessageContentRegistry["refusal"]; !ok {
				return false
			}
			delete(v.MessageContentRegistry, "refusal")
			return true
		}},
		{"annotations_arrayelem_ci_output", func(v *parityView) bool {
			specs := append([]pinnedFieldSpec(nil), v.ContentPartSpecs["output_text"]...)
			for i := range specs {
				if specs[i].Key == "annotations" {
					specs[i].ArrayElem = "ci_output"
					v.ContentPartSpecs["output_text"] = specs
					return true
				}
			}
			return false
		}},
		{"identical_probes_invalid", func(v *parityView) bool {
			// Force identical pos/neg for message via override
			pos := map[string]any{"type": "message", "id": "m1", "status": "completed", "role": "assistant", "content": []any{}}
			v.ProbeOverrides["output.message"] = struct{ Pos, Neg map[string]any }{Pos: pos, Neg: pos}
			return true
		}},
	}

	for _, mut := range ledger {
		mut := mut
		t.Run(mut.name, func(t *testing.T) {
			v := cloneLiveParityView()
			if !mut.mutate(&v) {
				t.Fatalf("mutation %s did not change intended state (vacuous)", mut.name)
			}
			if err := checkPinnedParity(v); err == nil {
				t.Fatalf("mutation %s expected parity failure, got nil", mut.name)
			}
		})
	}
}

// Keep named semantic tests for clarity / count20 targeting.
func TestParityMutation_LocalTimeoutSwap(t *testing.T) {
	v := cloneLiveParityView()
	v.ShellEnvRegistry["local"], v.ShellOutcomeRegistry["timeout"] =
		v.ShellOutcomeRegistry["timeout"], v.ShellEnvRegistry["local"]
	if err := checkPinnedParity(v); err == nil {
		t.Fatal("local/timeout swap must fail")
	}
}

func TestParityMutation_MCPCallApprovalSwap(t *testing.T) {
	v := cloneLiveParityView()
	v.OutputRegistry["mcp_call"], v.OutputRegistry["mcp_approval_request"] =
		v.OutputRegistry["mcp_approval_request"], v.OutputRegistry["mcp_call"]
	if err := checkPinnedParity(v); err == nil {
		t.Fatal("mcp_call/approval swap must fail")
	}
}
