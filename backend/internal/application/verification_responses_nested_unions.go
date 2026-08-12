package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"actweave/backend/internal/einoruntime"
)

// pinnedUnionMemberValidator validates one discriminator-selected nested object
// using declarative field specs + nested union dispatch.
type pinnedUnionMemberValidator func(obj map[string]json.RawMessage) error

// fieldKind is the raw JSON kind enforced for a pinned field.
type fieldKind int

const (
	kindNonemptyString fieldKind = iota // nonempty JSON string
	kindString                          // JSON string (may be empty)
	kindStringOrNull                    // handled via Nullable + kindString
	kindBool
	kindNonNegInt
	kindInt
	kindFiniteNumber
	kindObject       // non-null JSON object
	kindArray        // non-null JSON array (element check separate)
	kindAnyNonNull   // any non-null JSON value (pinned any)
	kindDomainString // nonempty string in Domain
	// kindURI is a nonempty absolute http/https URI string (pinned format:"uri"
	// under verification safety — see validateStrictHTTPURI).
	kindURI
)

// pinnedFieldSpec is the declarative matrix entry for one pinned JSON field.
// Tests must consume this matrix rather than a separate abbreviated required map.
// Independent openai-go reflection parity (test-only) asserts this matrix matches
// pinned concrete structs; it is not the sole source of wire truth.
type pinnedFieldSpec struct {
	Key      string
	Required bool // key must be present
	Nullable bool // if present (or required), JSON null is accepted
	Kind     fieldKind
	Domain   map[string]struct{} // for kindDomainString
	// Format is pinned wire format metadata (e.g. "uri") for parity + validation.
	Format string
	// Optional numeric range (inclusive) for kindFiniteNumber.
	Min *float64
	Max *float64
	// For kindArray: how to validate elements.
	// "string" | "logprob" | "annotation" | "content_part_message" | "summary_text" |
	// "reasoning_text" | "file_search_result" | "ci_output" | "mcp_list_tool" |
	// "function_tool" | "shell_output" | "web_search_source" | "string_queries"
	ArrayElem string
	// For kindObject nested single union/object validators by name.
	// "web_search_action" | "shell_action" | "shell_environment" | "function_parameters"
	Nested string
}

// ---------------------------------------------------------------------------
// Closed member registries: discriminator → validator. No default success.
// Populated in init() to avoid package-init cycles (validators → dispatch → registry).
// ---------------------------------------------------------------------------

var (
	pinnedOutputItemRegistry  map[string]pinnedUnionMemberValidator
	pinnedContentPartRegistry map[string]pinnedUnionMemberValidator
	// pinnedMessageContentPartRegistry is the closed set of content-part types
	// accepted inside ResponseOutputMessage.content[] (message content subset).
	// It is the single production dispatch source for ArrayElem content_part_message.
	pinnedMessageContentPartRegistry    map[string]pinnedUnionMemberValidator
	pinnedAnnotationRegistry            map[string]pinnedUnionMemberValidator
	pinnedWebSearchActionRegistry       map[string]pinnedUnionMemberValidator
	pinnedCodeInterpreterOutputRegistry map[string]pinnedUnionMemberValidator
	pinnedShellEnvironmentRegistry      map[string]pinnedUnionMemberValidator
	pinnedShellOutcomeRegistry          map[string]pinnedUnionMemberValidator
)

// messageContentPartDiscriminators is the exact closed set of message.content[] members.
// Kept as a named production constant so parity can bind to runtime dispatch.
var messageContentPartDiscriminators = []string{"output_text", "refusal"}

func init() {
	// Spec-driven closed registries. Unknown discriminators never succeed.
	// Every member validator re-checks the exact type discriminator so swapping
	// two registry entries remains detectably wrong (local vs timeout, mcp_call
	// vs mcp_approval_request, etc.) even when non-type fields overlap.
	pinnedOutputItemRegistry = make(map[string]pinnedUnionMemberValidator, len(outputItemSpecs))
	for k, specs := range outputItemSpecs {
		pinnedOutputItemRegistry[k] = validateFromSpecWithType(k, specs)
	}
	pinnedContentPartRegistry = make(map[string]pinnedUnionMemberValidator, len(contentPartSpecs))
	for k, specs := range contentPartSpecs {
		pinnedContentPartRegistry[k] = validateFromSpecWithType(k, specs)
	}
	// Message content subset: only output_text + refusal (not reasoning_text).
	pinnedMessageContentPartRegistry = make(map[string]pinnedUnionMemberValidator, len(messageContentPartDiscriminators))
	for _, k := range messageContentPartDiscriminators {
		v, ok := pinnedContentPartRegistry[k]
		if !ok || v == nil {
			panic("message content part registry missing content-part validator: " + k)
		}
		pinnedMessageContentPartRegistry[k] = v
	}
	// Annotations: range-relation members use custom validators after field specs + type.
	pinnedAnnotationRegistry = map[string]pinnedUnionMemberValidator{
		"file_citation":           validateFromSpecWithType("file_citation", annotationSpecs["file_citation"]),
		"url_citation":            validateWithType("url_citation", validateAnnotationURLCitation),
		"container_file_citation": validateWithType("container_file_citation", validateAnnotationContainerFileCitation),
		"file_path":               validateFromSpecWithType("file_path", annotationSpecs["file_path"]),
	}
	pinnedWebSearchActionRegistry = make(map[string]pinnedUnionMemberValidator, len(webSearchActionSpecs))
	for k, specs := range webSearchActionSpecs {
		pinnedWebSearchActionRegistry[k] = validateFromSpecWithType(k, specs)
	}
	pinnedCodeInterpreterOutputRegistry = make(map[string]pinnedUnionMemberValidator, len(ciOutputSpecs))
	for k, specs := range ciOutputSpecs {
		pinnedCodeInterpreterOutputRegistry[k] = validateFromSpecWithType(k, specs)
	}
	pinnedShellEnvironmentRegistry = make(map[string]pinnedUnionMemberValidator, len(shellEnvSpecs))
	for k, specs := range shellEnvSpecs {
		pinnedShellEnvironmentRegistry[k] = validateFromSpecWithType(k, specs)
	}
	pinnedShellOutcomeRegistry = make(map[string]pinnedUnionMemberValidator, len(shellOutcomeSpecs))
	for k, specs := range shellOutcomeSpecs {
		pinnedShellOutcomeRegistry[k] = validateFromSpecWithType(k, specs)
	}
}

// Domains from openai-go/v3@v3.35.0 (explicit closed sets for verification).
var (
	domainMessageStatus = map[string]struct{}{
		"in_progress": {}, "completed": {}, "incomplete": {},
	}
	domainMessageRole = map[string]struct{}{
		"assistant": {},
	}
	domainMessagePhase = map[string]struct{}{
		"commentary": {}, "final_answer": {},
	}
	domainTriState = map[string]struct{}{
		"in_progress": {}, "completed": {}, "incomplete": {},
	}
	domainWebSearchStatus = map[string]struct{}{
		"in_progress": {}, "searching": {}, "completed": {}, "failed": {},
	}
	domainFileSearchStatus = map[string]struct{}{
		"in_progress": {}, "searching": {}, "completed": {}, "incomplete": {}, "failed": {},
	}
	domainCodeInterpreterStatus = map[string]struct{}{
		"in_progress": {}, "completed": {}, "incomplete": {}, "interpreting": {}, "failed": {},
	}
	domainImageGenStatus = map[string]struct{}{
		"in_progress": {}, "completed": {}, "generating": {}, "failed": {},
	}
	domainMCPCallStatus = map[string]struct{}{
		"in_progress": {}, "completed": {}, "incomplete": {}, "calling": {}, "failed": {},
	}
	domainToolSearchExecution = map[string]struct{}{
		"server": {}, "client": {},
	}
)

func f64(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// Declarative field-spec matrices (source of truth for tests + validators)
// ---------------------------------------------------------------------------

var outputItemSpecs = map[string][]pinnedFieldSpec{
	"message": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "content", Required: true, Kind: kindArray, ArrayElem: "content_part_message"},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainMessageStatus},
		// ResponseOutputMessage.Role: always assistant (required on wire for verification).
		{Key: "role", Required: true, Kind: kindDomainString, Domain: domainMessageRole},
		// phase optional nullable closed domain commentary|final_answer
		{Key: "phase", Required: false, Nullable: true, Kind: kindDomainString, Domain: domainMessagePhase},
	},
	"function_call": {
		{Key: "arguments", Required: true, Kind: kindString},
		{Key: "call_id", Required: true, Kind: kindNonemptyString},
		{Key: "name", Required: true, Kind: kindNonemptyString},
		{Key: "id", Required: false, Kind: kindString},
		{Key: "namespace", Required: false, Kind: kindString},
		{Key: "status", Required: false, Kind: kindDomainString, Domain: domainTriState},
	},
	"reasoning": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "summary", Required: true, Kind: kindArray, ArrayElem: "summary_text"},
		{Key: "content", Required: false, Kind: kindArray, ArrayElem: "reasoning_text"},
		// encrypted_content optional nullable string
		{Key: "encrypted_content", Required: false, Nullable: true, Kind: kindString},
		{Key: "status", Required: false, Kind: kindDomainString, Domain: domainTriState},
	},
	"web_search_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "action", Required: true, Kind: kindObject, Nested: "web_search_action"},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainWebSearchStatus},
	},
	"file_search_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "queries", Required: true, Kind: kindArray, ArrayElem: "string_queries"},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainFileSearchStatus},
		// results required-key? no — optional nullable array
		{Key: "results", Required: false, Nullable: true, Kind: kindArray, ArrayElem: "file_search_result"},
	},
	"code_interpreter_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		// required keys; null allowed per API comments ("or null if not available")
		{Key: "code", Required: true, Nullable: true, Kind: kindString},
		{Key: "container_id", Required: true, Kind: kindNonemptyString},
		{Key: "outputs", Required: true, Nullable: true, Kind: kindArray, ArrayElem: "ci_output"},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainCodeInterpreterStatus},
	},
	"image_generation_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "result", Required: true, Kind: kindString},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainImageGenStatus},
	},
	"mcp_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "arguments", Required: true, Kind: kindString},
		{Key: "name", Required: true, Kind: kindNonemptyString},
		{Key: "server_label", Required: true, Kind: kindNonemptyString},
		{Key: "approval_request_id", Required: false, Nullable: true, Kind: kindString},
		{Key: "error", Required: false, Nullable: true, Kind: kindString},
		{Key: "output", Required: false, Nullable: true, Kind: kindString},
		{Key: "status", Required: false, Kind: kindDomainString, Domain: domainMCPCallStatus},
	},
	"mcp_list_tools": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "server_label", Required: true, Kind: kindNonemptyString},
		{Key: "tools", Required: true, Kind: kindArray, ArrayElem: "mcp_list_tool"},
		{Key: "error", Required: false, Nullable: true, Kind: kindString},
	},
	"mcp_approval_request": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "arguments", Required: true, Kind: kindString},
		{Key: "name", Required: true, Kind: kindNonemptyString},
		{Key: "server_label", Required: true, Kind: kindNonemptyString},
	},
	"tool_search_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "arguments", Required: true, Kind: kindAnyNonNull},
		{Key: "call_id", Required: true, Kind: kindNonemptyString},
		{Key: "execution", Required: true, Kind: kindDomainString, Domain: domainToolSearchExecution},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainTriState},
		{Key: "created_by", Required: false, Kind: kindString},
	},
	"tool_search_output": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "call_id", Required: true, Kind: kindNonemptyString},
		{Key: "execution", Required: true, Kind: kindDomainString, Domain: domainToolSearchExecution},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainTriState},
		// Only function tools accepted (client-bounded tool search).
		{Key: "tools", Required: true, Kind: kindArray, ArrayElem: "function_tool"},
		{Key: "created_by", Required: false, Kind: kindString},
	},
	"shell_call": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "call_id", Required: true, Kind: kindNonemptyString},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainTriState},
		{Key: "action", Required: true, Kind: kindObject, Nested: "shell_action"},
		{Key: "environment", Required: true, Kind: kindObject, Nested: "shell_environment"},
		{Key: "created_by", Required: false, Kind: kindString},
	},
	"shell_call_output": {
		{Key: "id", Required: true, Kind: kindNonemptyString},
		{Key: "call_id", Required: true, Kind: kindNonemptyString},
		{Key: "max_output_length", Required: true, Kind: kindNonNegInt},
		{Key: "output", Required: true, Kind: kindArray, ArrayElem: "shell_output"},
		{Key: "status", Required: true, Kind: kindDomainString, Domain: domainTriState},
		{Key: "created_by", Required: false, Kind: kindString},
	},
}

var contentPartSpecs = map[string][]pinnedFieldSpec{
	"output_text": {
		{Key: "text", Required: true, Kind: kindString},
		{Key: "annotations", Required: true, Kind: kindArray, ArrayElem: "annotation"},
		// optional non-nullable logprobs
		{Key: "logprobs", Required: false, Nullable: false, Kind: kindArray, ArrayElem: "logprob"},
	},
	"refusal": {
		{Key: "refusal", Required: true, Kind: kindString},
	},
	"reasoning_text": {
		{Key: "text", Required: true, Kind: kindString},
	},
}

var annotationSpecs = map[string][]pinnedFieldSpec{
	"file_citation": {
		{Key: "file_id", Required: true, Kind: kindNonemptyString},
		{Key: "filename", Required: true, Kind: kindNonemptyString},
		{Key: "index", Required: true, Kind: kindNonNegInt},
	},
	"url_citation": {
		{Key: "url", Required: true, Kind: kindURI, Format: "uri"},
		{Key: "title", Required: true, Kind: kindString},
		{Key: "start_index", Required: true, Kind: kindNonNegInt},
		{Key: "end_index", Required: true, Kind: kindNonNegInt},
	},
	"container_file_citation": {
		{Key: "container_id", Required: true, Kind: kindNonemptyString},
		{Key: "file_id", Required: true, Kind: kindNonemptyString},
		{Key: "filename", Required: true, Kind: kindNonemptyString},
		{Key: "start_index", Required: true, Kind: kindNonNegInt},
		{Key: "end_index", Required: true, Kind: kindNonNegInt},
	},
	"file_path": {
		{Key: "file_id", Required: true, Kind: kindNonemptyString},
		{Key: "index", Required: true, Kind: kindNonNegInt},
	},
}

var webSearchActionSpecs = map[string][]pinnedFieldSpec{
	"search": {
		{Key: "query", Required: true, Kind: kindString},
		{Key: "queries", Required: false, Kind: kindArray, ArrayElem: "string_queries"},
		{Key: "sources", Required: false, Kind: kindArray, ArrayElem: "web_search_source"},
	},
	"open_page": {
		// url optional nullable absolute http(s) URI
		{Key: "url", Required: false, Nullable: true, Kind: kindURI, Format: "uri"},
	},
	"find_in_page": {
		{Key: "pattern", Required: true, Kind: kindString},
		{Key: "url", Required: true, Kind: kindURI, Format: "uri"},
	},
}

var ciOutputSpecs = map[string][]pinnedFieldSpec{
	"logs": {{Key: "logs", Required: true, Kind: kindString}},
	"image": {
		{Key: "url", Required: true, Kind: kindURI, Format: "uri"},
	},
}

var shellEnvSpecs = map[string][]pinnedFieldSpec{
	// Non-nil empty slice: required empty specs must not be nil map values.
	"local": []pinnedFieldSpec{},
	"container_reference": {
		{Key: "container_id", Required: true, Kind: kindNonemptyString},
	},
}

var shellOutcomeSpecs = map[string][]pinnedFieldSpec{
	// Non-nil empty slice: required empty specs must not be nil map values.
	"timeout": []pinnedFieldSpec{},
	"exit": {
		{Key: "exit_code", Required: true, Kind: kindInt},
	},
}

// functionToolSpecs validates tool_search_output tools[] function members.
// parameters uses production catalog schema security (Nested: function_parameters).
var functionToolSpecs = []pinnedFieldSpec{
	{Key: "type", Required: true, Kind: kindDomainString, Domain: map[string]struct{}{"function": {}}},
	{Key: "name", Required: true, Kind: kindNonemptyString},
	{Key: "parameters", Required: true, Kind: kindObject, Nested: "function_parameters"},
	{Key: "strict", Required: true, Kind: kindBool},
	{Key: "description", Required: false, Nullable: true, Kind: kindString},
	{Key: "defer_loading", Required: false, Kind: kindBool},
}

var mcpListToolSpecs = []pinnedFieldSpec{
	{Key: "name", Required: true, Kind: kindNonemptyString},
	// Pinned ResponseOutputItemMcpListToolsTool.InputSchema is `any` + api:"required".
	// Accept any non-null JSON value (scalar/array/object); missing/null fail.
	{Key: "input_schema", Required: true, Kind: kindAnyNonNull},
	{Key: "description", Required: false, Nullable: true, Kind: kindString},
	// annotations pinned `any` nullable — null OK; any non-null JSON value OK.
	{Key: "annotations", Required: false, Nullable: true, Kind: kindAnyNonNull},
}

var fileSearchResultSpecs = []pinnedFieldSpec{
	{Key: "file_id", Required: false, Kind: kindString},
	{Key: "filename", Required: false, Kind: kindString},
	{Key: "text", Required: false, Kind: kindString},
	{Key: "score", Required: false, Kind: kindFiniteNumber, Min: f64(0), Max: f64(1)},
	// attributes nullable map — custom validation below when present
	{Key: "attributes", Required: false, Nullable: true, Kind: kindObject, Nested: "file_search_attributes"},
}

// shellOutputElemSpecs: ResponseFunctionShellToolCallOutputOutput
var shellOutputElemSpecs = []pinnedFieldSpec{
	{Key: "stdout", Required: true, Kind: kindString},
	{Key: "stderr", Required: true, Kind: kindString},
	{Key: "outcome", Required: true, Kind: kindObject, Nested: "shell_outcome"},
	// optional non-nullable string
	{Key: "created_by", Required: false, Kind: kindString},
}

var shellActionSpecs = []pinnedFieldSpec{
	{Key: "commands", Required: true, Kind: kindArray, ArrayElem: "string_queries"},
	{Key: "max_output_length", Required: true, Kind: kindNonNegInt},
	{Key: "timeout_ms", Required: true, Kind: kindNonNegInt},
}

var webSearchSourceSpecs = []pinnedFieldSpec{
	// type defaults to "url"; if present must be "url"
	{Key: "type", Required: false, Kind: kindDomainString, Domain: map[string]struct{}{"url": {}}},
	{Key: "url", Required: true, Kind: kindURI, Format: "uri"},
}

// summaryTextElementSpecs: ResponseReasoningItemSummary
var summaryTextElementSpecs = []pinnedFieldSpec{
	{Key: "type", Required: true, Kind: kindDomainString, Domain: map[string]struct{}{"summary_text": {}}},
	{Key: "text", Required: true, Kind: kindString},
}

// reasoningTextElementSpecs: ResponseReasoningItemContent
var reasoningTextElementSpecs = []pinnedFieldSpec{
	{Key: "type", Required: true, Kind: kindDomainString, Domain: map[string]struct{}{"reasoning_text": {}}},
	{Key: "text", Required: true, Kind: kindString},
}

// logprobElementSpecs: ResponseOutputTextLogprob
var logprobElementSpecs = []pinnedFieldSpec{
	{Key: "token", Required: true, Kind: kindString},
	{Key: "bytes", Required: true, Kind: kindArray, ArrayElem: "byte_int"},
	{Key: "logprob", Required: true, Kind: kindFiniteNumber},
	{Key: "top_logprobs", Required: true, Kind: kindArray, ArrayElem: "top_logprob"},
}

// topLogprobElementSpecs: ResponseOutputTextLogprobTopLogprob
var topLogprobElementSpecs = []pinnedFieldSpec{
	{Key: "token", Required: true, Kind: kindString},
	{Key: "bytes", Required: true, Kind: kindArray, ArrayElem: "byte_int"},
	{Key: "logprob", Required: true, Kind: kindFiniteNumber},
}

// ---------------------------------------------------------------------------
// Spec-driven validator
// ---------------------------------------------------------------------------

func validateFromSpec(specs []pinnedFieldSpec) pinnedUnionMemberValidator {
	// Capture specs by value into closure. Prefer validateFromSpecWithType for
	// registry members so type discriminators distinguish isomorphic shapes.
	s := specs
	return func(obj map[string]json.RawMessage) error {
		return applyFieldSpecs(obj, s)
	}
}

// validateFromSpecWithType requires exact non-null type string before field specs.
// Missing/wrong/null type fails closed. Valid wire objects already carry type.
func validateFromSpecWithType(expectedType string, specs []pinnedFieldSpec) pinnedUnionMemberValidator {
	s := specs
	want := expectedType
	return func(obj map[string]json.RawMessage) error {
		if err := requireExactTypeDiscriminator(obj, want); err != nil {
			return err
		}
		return applyFieldSpecs(obj, s)
	}
}

// validateWithType wraps a custom member validator with exact type checking.
func validateWithType(expectedType string, inner pinnedUnionMemberValidator) pinnedUnionMemberValidator {
	want := expectedType
	return func(obj map[string]json.RawMessage) error {
		if err := requireExactTypeDiscriminator(obj, want); err != nil {
			return err
		}
		return inner(obj)
	}
}

func requireExactTypeDiscriminator(obj map[string]json.RawMessage, expected string) error {
	typeStr, err := requireNonemptyJSONStringField(obj, "type")
	if err != nil {
		return err
	}
	if typeStr != expected {
		return fmt.Errorf("type must be %q, got %q", expected, typeStr)
	}
	return nil
}

func applyFieldSpecs(obj map[string]json.RawMessage, specs []pinnedFieldSpec) error {
	for _, sp := range specs {
		raw, present := obj[sp.Key]
		if !present {
			if sp.Required {
				return fmt.Errorf("missing %s", sp.Key)
			}
			continue
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return fmt.Errorf("%s empty", sp.Key)
		}
		if bytes.Equal(raw, []byte("null")) {
			if sp.Nullable {
				continue
			}
			return fmt.Errorf("%s must not be null", sp.Key)
		}
		if err := validateFieldKind(obj, sp, raw); err != nil {
			return err
		}
	}
	return nil
}

func validateFieldKind(obj map[string]json.RawMessage, sp pinnedFieldSpec, raw json.RawMessage) error {
	switch sp.Kind {
	case kindNonemptyString:
		s, err := requireNonemptyJSONStringField(obj, sp.Key)
		if err != nil {
			return err
		}
		_ = s
		return nil
	case kindString:
		_, err := requireJSONStringField(obj, sp.Key)
		return err
	case kindBool:
		return requireJSONBoolField(obj, sp.Key)
	case kindNonNegInt:
		_, err := requireStrictNonNegJSONIntField(obj, sp.Key)
		return err
	case kindInt:
		_, err := requireStrictJSONIntField(obj, sp.Key)
		return err
	case kindFiniteNumber:
		f, err := requireFiniteJSONNumberField(obj, sp.Key)
		if err != nil {
			return err
		}
		if sp.Min != nil && f < *sp.Min {
			return fmt.Errorf("%s below minimum", sp.Key)
		}
		if sp.Max != nil && f > *sp.Max {
			return fmt.Errorf("%s above maximum", sp.Key)
		}
		return nil
	case kindDomainString:
		s, err := requireNonemptyJSONStringField(obj, sp.Key)
		if err != nil {
			return err
		}
		if _, ok := sp.Domain[s]; !ok {
			return fmt.Errorf("%s %q is not in closed domain", sp.Key, s)
		}
		return nil
	case kindURI:
		return requireStrictHTTPURIField(obj, sp.Key)
	case kindAnyNonNull:
		// already rejected null above
		return nil
	case kindObject:
		// function_parameters: raw-object + production catalog schema security.
		if sp.Nested == "function_parameters" {
			return validateFunctionParametersSchemaField(obj, sp.Key)
		}
		nested, err := requireNonEmptyJSONObject(obj, sp.Key)
		if err != nil {
			// empty object allowed for file_search_attributes
			if sp.Nested == "file_search_attributes" {
				if err2 := requireNonNullJSONObjectField(obj, sp.Key); err2 != nil {
					return err2
				}
				nested = map[string]json.RawMessage{}
				raw := bytes.TrimSpace(obj[sp.Key])
				_ = json.Unmarshal(raw, &nested)
			} else {
				return err
			}
		}
		return validateNested(sp.Nested, nested, sp.Key)
	case kindArray:
		if err := requireJSONArrayField(obj, sp.Key); err != nil {
			return err
		}
		return validateArrayByElem(obj[sp.Key], sp.Key, sp.ArrayElem)
	default:
		return fmt.Errorf("internal: unknown field kind for %s", sp.Key)
	}
}

func validateNested(name string, obj map[string]json.RawMessage, path string) error {
	switch name {
	case "":
		return nil
	case "web_search_action":
		return dispatchUnion(obj, path, pinnedWebSearchActionRegistry)
	case "shell_action":
		return applyFieldSpecs(obj, shellActionSpecs)
	case "shell_environment":
		return dispatchUnion(obj, path, pinnedShellEnvironmentRegistry)
	case "shell_outcome":
		return dispatchUnion(obj, path, pinnedShellOutcomeRegistry)
	case "file_search_attributes":
		return validateFileSearchAttributes(obj)
	default:
		return fmt.Errorf("internal: unknown nested validator %q", name)
	}
}

// validateFunctionParametersSchemaField reuses the exact production OpenAI Agentic
// catalog schema validator. Failures map to stream-invalid without embedding schema body.
func validateFunctionParametersSchemaField(fields map[string]json.RawMessage, key string) error {
	if err := requireNonNullJSONObjectField(fields, key); err != nil {
		return err
	}
	raw := bytes.TrimSpace(fields[key])
	if err := einoruntime.ValidateOpenAIAgenticParametersSchema(raw); err != nil {
		// Do not embed schema body or secret markers; keep catalog keyword-level detail only.
		return fmt.Errorf("%s failed schema security validation", key)
	}
	return nil
}

// requireStrictHTTPURIField requires a nonempty JSON string that is an absolute
// http or https URI suitable for verification safety (pinned format:"uri").
//
// Accepted schemes (documented verification policy): http, https only.
// Rejects: empty, relative, missing host, control characters, userinfo abuse
// patterns, and non-http(s) schemes (file, javascript, data, etc.).
// Not merely url.Parse success.
func requireStrictHTTPURIField(fields map[string]json.RawMessage, key string) error {
	s, err := requireNonemptyJSONStringField(fields, key)
	if err != nil {
		return err
	}
	return validateStrictHTTPURI(s, key)
}

func validateStrictHTTPURI(s, key string) error {
	if s == "" {
		return fmt.Errorf("%s must be a nonempty URI", key)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", key)
		}
	}
	// Fast reject common non-absolute forms before parse.
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.Contains(s, " ") || strings.Contains(s, "\t") {
		return fmt.Errorf("%s must be an absolute http(s) URI", key)
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("%s is not a valid URI", key)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https", key)
	}
	if u.Host == "" {
		return fmt.Errorf("%s must include a host", key)
	}
	// Reject credentials in URL for verification safety (provider may not emit them).
	if u.User != nil {
		return fmt.Errorf("%s must not include userinfo", key)
	}
	// Opaque non-hierarchical forms (e.g. https:foo) without host already rejected.
	if u.Opaque != "" && u.Host == "" {
		return fmt.Errorf("%s must be a hierarchical http(s) URI", key)
	}
	return nil
}

func validateArrayByElem(raw json.RawMessage, path, elem string) error {
	switch elem {
	case "string_queries":
		return validateStringArrayElements(raw, path)
	case "content_part_message":
		// Dispatch through closed message-content registry (parity-introspectable).
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return dispatchUnion(el, path, pinnedMessageContentPartRegistry)
		})
	case "summary_text":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, summaryTextElementSpecs)
		})
	case "reasoning_text":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, reasoningTextElementSpecs)
		})
	case "annotation":
		return validateAnnotationArray(raw)
	case "logprob":
		return validateLogprobsArray(raw)
	case "top_logprob":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, topLogprobElementSpecs)
		})
	case "byte_int":
		return validateByteIntArray(raw, path)
	case "file_search_result":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, fileSearchResultSpecs)
		})
	case "ci_output":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return dispatchUnion(el, path, pinnedCodeInterpreterOutputRegistry)
		})
	case "mcp_list_tool":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, mcpListToolSpecs)
		})
	case "function_tool":
		// Closed: only type=function accepted.
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			typeStr, err := requireNonemptyJSONStringField(el, "type")
			if err != nil {
				return err
			}
			if typeStr != "function" {
				return fmt.Errorf("type %q is not allowed; only function tools accepted in tool_search_output", typeStr)
			}
			return applyFieldSpecs(el, functionToolSpecs)
		})
	case "shell_output":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, shellOutputElemSpecs)
		})
	case "web_search_source":
		return validateArrayElements(raw, path, func(el map[string]json.RawMessage, i int) error {
			return applyFieldSpecs(el, webSearchSourceSpecs)
		})
	default:
		return fmt.Errorf("internal: unknown array element kind %q", elem)
	}
}

// validateFileSearchAttributes enforces max 16 keys, key len <= 64, values string|bool|finite number, string values <= 512.
func validateFileSearchAttributes(obj map[string]json.RawMessage) error {
	if len(obj) > 16 {
		return fmt.Errorf("attributes exceeds 16 keys")
	}
	for k, raw := range obj {
		if len(k) > 64 {
			return fmt.Errorf("attributes key exceeds 64 characters")
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			return fmt.Errorf("attributes[%s] must be string, number, or boolean", k)
		}
		switch raw[0] {
		case '"':
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return fmt.Errorf("attributes[%s] must be string", k)
			}
			if len(s) > 512 {
				return fmt.Errorf("attributes[%s] string exceeds 512 characters", k)
			}
		case 't', 'f':
			if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
				return fmt.Errorf("attributes[%s] must be boolean", k)
			}
		default:
			// number
			var n json.Number
			if err := json.Unmarshal(raw, &n); err != nil {
				return fmt.Errorf("attributes[%s] must be string, number, or boolean", k)
			}
			f, err := n.Float64()
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("attributes[%s] must be finite number", k)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Entry points
// ---------------------------------------------------------------------------

func requirePinnedOutputItemField(fields map[string]json.RawMessage, key string) error {
	obj, err := requireNonEmptyJSONObject(fields, key)
	if err != nil {
		return err
	}
	typeStr, err := requireNonemptyJSONStringField(obj, "type")
	if err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	v, ok := pinnedOutputItemRegistry[typeStr]
	if !ok {
		return fmt.Errorf("%s.type %q is not a supported output item union member", key, typeStr)
	}
	if v == nil {
		return fmt.Errorf("%s.type %q has nil validator", key, typeStr)
	}
	if err := v(obj); err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	return nil
}

func requirePinnedContentPartField(fields map[string]json.RawMessage, key string) error {
	obj, err := requireNonEmptyJSONObject(fields, key)
	if err != nil {
		return err
	}
	typeStr, err := requireNonemptyJSONStringField(obj, "type")
	if err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	v, ok := pinnedContentPartRegistry[typeStr]
	if !ok {
		return fmt.Errorf("%s.type %q is not a valid content part union member", key, typeStr)
	}
	if v == nil {
		return fmt.Errorf("%s.type %q has nil validator", key, typeStr)
	}
	if err := v(obj); err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	return nil
}

func requirePinnedReasoningSummaryPartField(fields map[string]json.RawMessage, key string) error {
	obj, err := requireNonEmptyJSONObject(fields, key)
	if err != nil {
		return err
	}
	if err := applyFieldSpecs(obj, summaryTextElementSpecs); err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	return nil
}

func requirePinnedAnnotationField(fields map[string]json.RawMessage, key string) error {
	obj, err := requireNonEmptyJSONObject(fields, key)
	if err != nil {
		return err
	}
	return validatePinnedAnnotationObject(obj, key)
}

func requirePinnedLogprobsField(fields map[string]json.RawMessage, key string) error {
	// Required non-null array (event-level logprobs on delta/done).
	if err := requireJSONArrayField(fields, key); err != nil {
		return err
	}
	return validateLogprobsArray(fields[key])
}

// ---------------------------------------------------------------------------
// Annotations with cross-field ranges
// ---------------------------------------------------------------------------

func validatePinnedAnnotationObject(obj map[string]json.RawMessage, path string) error {
	typeStr, err := requireNonemptyJSONStringField(obj, "type")
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	v, ok := pinnedAnnotationRegistry[typeStr]
	if !ok {
		return fmt.Errorf("%s.type %q is not a valid annotation union member", path, typeStr)
	}
	if v == nil {
		return fmt.Errorf("%s.type %q has nil validator", path, typeStr)
	}
	if err := v(obj); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

func validateAnnotationArray(raw json.RawMessage) error {
	return validateArrayElements(raw, "annotations", func(el map[string]json.RawMessage, i int) error {
		return validatePinnedAnnotationObject(el, fmt.Sprintf("annotations[%d]", i))
	})
}

func validateAnnotationURLCitation(obj map[string]json.RawMessage) error {
	if err := applyFieldSpecs(obj, annotationSpecs["url_citation"]); err != nil {
		return err
	}
	start, err := requireStrictNonNegJSONIntField(obj, "start_index")
	if err != nil {
		return err
	}
	end, err := requireStrictNonNegJSONIntField(obj, "end_index")
	if err != nil {
		return err
	}
	if start > end {
		return fmt.Errorf("start_index %d > end_index %d", start, end)
	}
	return nil
}

func validateAnnotationContainerFileCitation(obj map[string]json.RawMessage) error {
	if err := applyFieldSpecs(obj, annotationSpecs["container_file_citation"]); err != nil {
		return err
	}
	start, err := requireStrictNonNegJSONIntField(obj, "start_index")
	if err != nil {
		return err
	}
	end, err := requireStrictNonNegJSONIntField(obj, "end_index")
	if err != nil {
		return err
	}
	if start > end {
		return fmt.Errorf("start_index %d > end_index %d", start, end)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Logprobs
// ---------------------------------------------------------------------------

func validateLogprobsArray(raw json.RawMessage) error {
	return validateArrayElements(raw, "logprobs", func(el map[string]json.RawMessage, i int) error {
		return validateLogprobElement(el)
	})
}

func validateLogprobElement(obj map[string]json.RawMessage) error {
	return applyFieldSpecs(obj, logprobElementSpecs)
}

func validateByteIntArray(raw json.RawMessage, path string) error {
	raw = bytes.TrimSpace(raw)
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return fmt.Errorf("%s must be array", path)
	}
	for i, el := range arr {
		el = bytes.TrimSpace(el)
		if len(el) == 0 || el[0] == '"' || el[0] == '{' || el[0] == '[' || el[0] == 't' || el[0] == 'f' || bytes.Equal(el, []byte("null")) {
			return fmt.Errorf("%s[%d] must be a JSON integer", path, i)
		}
		s := string(el)
		if strings.ContainsAny(s, ".eE") {
			return fmt.Errorf("%s[%d] must be a JSON integer", path, i)
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("%s[%d] must be a JSON integer", path, i)
		}
		if n < 0 || n > 255 {
			return fmt.Errorf("%s[%d] out of byte range [0,255]", path, i)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func requireNonEmptyJSONObject(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	if err := requireNonNullJSONObjectField(fields, key); err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace(fields[key])
	if bytes.Equal(raw, []byte("{}")) {
		return nil, fmt.Errorf("%s must be a non-empty object", key)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object", key)
	}
	if len(obj) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty object", key)
	}
	return obj, nil
}

func dispatchUnion(obj map[string]json.RawMessage, path string, reg map[string]pinnedUnionMemberValidator) error {
	typeStr, err := requireNonemptyJSONStringField(obj, "type")
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	v, ok := reg[typeStr]
	if !ok {
		return fmt.Errorf("%s.type %q is not a valid union member", path, typeStr)
	}
	if v == nil {
		return fmt.Errorf("%s.type %q has nil validator", path, typeStr)
	}
	if err := v(obj); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

func validateArrayElements(raw json.RawMessage, path string, fn func(map[string]json.RawMessage, int) error) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return fmt.Errorf("%s must be array", path)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return fmt.Errorf("%s must be array", path)
	}
	for i, el := range arr {
		el = bytes.TrimSpace(el)
		if len(el) == 0 || el[0] != '{' {
			return fmt.Errorf("%s[%d] must be object", path, i)
		}
		if bytes.Equal(el, []byte("{}")) {
			return fmt.Errorf("%s[%d] must be a non-empty object", path, i)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(el, &obj); err != nil {
			return fmt.Errorf("%s[%d] must be object", path, i)
		}
		if err := fn(obj, i); err != nil {
			return fmt.Errorf("%s[%d]: %v", path, i, err)
		}
	}
	return nil
}

func validateStringArrayElements(raw json.RawMessage, path string) error {
	raw = bytes.TrimSpace(raw)
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return fmt.Errorf("%s must be array", path)
	}
	for i, el := range arr {
		el = bytes.TrimSpace(el)
		if len(el) == 0 || el[0] != '"' {
			return fmt.Errorf("%s[%d] must be a JSON string", path, i)
		}
		var s string
		if err := json.Unmarshal(el, &s); err != nil {
			return fmt.Errorf("%s[%d] must be a JSON string", path, i)
		}
	}
	return nil
}

func requireJSONBoolField(fields map[string]json.RawMessage, key string) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
		return fmt.Errorf("%s must be a JSON boolean", key)
	}
	return nil
}

func requireFiniteJSONNumberField(fields map[string]json.RawMessage, key string) (float64, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || raw[0] == '"' || raw[0] == '{' || raw[0] == '[' || raw[0] == 't' || raw[0] == 'f' {
		return 0, fmt.Errorf("%s must be a JSON number", key)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%s must be a JSON number", key)
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s must be a finite JSON number", key)
	}
	return f, nil
}

func requireStrictJSONIntField(fields map[string]json.RawMessage, key string) (int64, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, fmt.Errorf("%s must be a JSON integer", key)
	}
	s := string(raw)
	if strings.ContainsAny(s, ".eE") {
		return 0, fmt.Errorf("%s must be a JSON integer", key)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a JSON integer", key)
	}
	return n, nil
}
