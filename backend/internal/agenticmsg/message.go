package agenticmsg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/schema/openai"
)

// Stable error values for callers that need typed matching via errors.Is.
var (
	// ErrNilMessage is returned when a required AgenticMessage is nil.
	ErrNilMessage = errors.New("agenticmsg: message is nil")
	// ErrNilChunk is returned when ConcatStream receives a nil chunk.
	ErrNilChunk = errors.New("agenticmsg: nil stream chunk")
	// ErrNilBlock is returned when a content block pointer is nil.
	ErrNilBlock = errors.New("agenticmsg: nil content block")
	// ErrWrongRole is returned when a message role is incompatible with the
	// requested extraction.
	ErrWrongRole = errors.New("agenticmsg: incompatible message role")
	// ErrInvalidRole is returned when a message role is empty or not a known
	// Agentic role.
	ErrInvalidRole = errors.New("agenticmsg: invalid message role")
	// ErrMalformedBlock is returned when a known content block type has a
	// missing expected payload, extra incompatible payload, or otherwise
	// violates ContentBlock union exclusivity or nested preconditions.
	ErrMalformedBlock = errors.New("agenticmsg: malformed content block")
	// ErrIncompatibleBlock is returned when a content block type is not
	// allowed for the message role (role/block compatibility matrix).
	ErrIncompatibleBlock = errors.New("agenticmsg: content block incompatible with message role")
	// ErrNoAssistantText is returned when assistant text is required but no
	// AssistantGenText blocks with non-empty text are present.
	ErrNoAssistantText = errors.New("agenticmsg: no assistant text content")
	// ErrUnsupportedBlock is returned when a content block type is unknown or
	// outside the deliberately narrow supported protocol surface.
	ErrUnsupportedBlock = errors.New("agenticmsg: unsupported content block")
	// ErrEmptyConcat is returned when ConcatStream is given an empty slice.
	ErrEmptyConcat = errors.New("agenticmsg: empty stream chunks")
	// ErrConcat is returned when schema.ConcatAgenticMessages fails for a
	// reason other than the pre-concat sentinels (empty/nil/validation).
	// Callers may use errors.Is(err, ErrConcat) for role mismatch and other
	// upstream concat failures.
	ErrConcat = errors.New("agenticmsg: concat failed")
	// ErrUnpairedToolResult is returned by ValidateConversation when a
	// function_tool_result or tool_search_result has no prior matching
	// call CallID (of any kind) in the conversation.
	ErrUnpairedToolResult = errors.New("agenticmsg: tool result without matching prior call")
	// ErrDuplicateCallID is returned when two assistant calls share a CallID.
	ErrDuplicateCallID = errors.New("agenticmsg: duplicate tool call ID")
	// ErrRepeatedToolResult is returned when a CallID is consumed by more than
	// one result block.
	ErrRepeatedToolResult = errors.New("agenticmsg: repeated tool result for call ID")
	// ErrWrongKindToolResult is returned when a result kind does not match the
	// prior call kind (ordinary function vs client tool-search).
	ErrWrongKindToolResult = errors.New("agenticmsg: tool result kind does not match call")
	// ErrToolResultNameMismatch is returned when a function or tool-search
	// result Name does not exactly match (after trim) the Name of its
	// originating assistant call. Names are not case-folded.
	ErrToolResultNameMismatch = errors.New("agenticmsg: tool result name mismatch")
	// ErrToolSearchNameMismatch is retained as an alias of ErrToolResultNameMismatch
	// so existing errors.Is checks for tool-search pairing continue to work.
	ErrToolSearchNameMismatch = ErrToolResultNameMismatch
	// ErrInvalidToolArguments is returned when FunctionToolCall.Arguments is
	// not a single JSON object in the form the adapter expects (including when
	// objects contain duplicate keys at any nesting level).
	ErrInvalidToolArguments = errors.New("agenticmsg: invalid tool call arguments")
	// ErrStreamOnlyField is returned when a complete/persisted message carries
	// stream-assembly-only OpenAI extension state that the pinned adapter drops
	// during replay conversion (TextAnnotation.Index, ReasoningContent.Index).
	// These fields are permitted only on authentic stream fragments whose
	// enclosing ContentBlock.StreamingMeta != nil.
	ErrStreamOnlyField = errors.New("agenticmsg: stream-only field not allowed on complete message")
)

// validationKind selects complete-message vs stream-fragment rules.
type validationKind int

const (
	// validateComplete requires a safe-to-send/persist message (strict).
	validateComplete validationKind = iota
	// validateStreamFragment permits temporarily incomplete response metadata
	// and partial function-call fields that the pinned stream converter emits.
	validateStreamFragment
)

// callKind distinguishes ordinary function calls from client tool-search calls.
type callKind int

const (
	callKindFunction callKind = iota
	callKindToolSearch
)

// callLedgerEntry tracks one assistant call for conversation pairing.
type callLedgerEntry struct {
	kind     callKind
	name     string
	consumed bool
}

// TokenUsage is a stable token-usage projection including cached and reasoning
// token breakdowns from Agentic ResponseMeta.
//
// Named TokenUsage (not Usage) so the package can export func Usage without a
// Go identifier clash while matching design D8's Usage() accessor.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
	ReasoningTokens  int
}

// System builds a system-role AgenticMessage with a single text input block.
func System(text string) *schema.AgenticMessage {
	return schema.SystemAgenticMessage(text)
}

// UserText builds a user-role AgenticMessage with a single text input block.
func UserText(text string) *schema.AgenticMessage {
	return schema.UserAgenticMessage(text)
}

// AssistantText builds an assistant-role AgenticMessage with a single
// AssistantGenText content block.
func AssistantText(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.AssistantGenText{Text: text}),
		},
	}
}

// isOpenAISelfGenerated mirrors agenticopenai v0.2.2 isSelfGeneratedMessage:
// ResponseMeta.OpenAIExtension != nil marks OpenAI adapter-produced messages.
func isOpenAISelfGenerated(msg *schema.AgenticMessage) bool {
	return msg != nil && msg.ResponseMeta != nil && msg.ResponseMeta.OpenAIExtension != nil
}

// Validate performs centralized strict validation of a complete AgenticMessage
// safe to send or persist. Nil messages, invalid roles, nil blocks, known block
// types with missing/extra payloads, role-incompatible blocks, and nested
// converter preconditions fail closed with typed errors.
//
// Supported surface (deliberately narrow for client-executed function tools +
// client-executed native tool search):
//
//	system:    user_input_text, user_input_image
//	user:      user_input_text|image|file, function_tool_result, tool_search_result
//	assistant: assistant_gen_text, function_tool_call, reasoning
//
// Complete reasoning always requires OpenAI self-generated metadata
// (ResponseMeta.OpenAIExtension != nil), regardless of StreamingMeta. Partial
// stream fragments must use ValidateStreamChunk instead.
//
// Message-local only: call/result pairing across messages is ValidateConversation.
func Validate(msg *schema.AgenticMessage) error {
	return validateMessage(msg, validateComplete)
}

// ValidateStreamChunk validates a raw stream fragment. It keeps nil/union/type
// safety checks. Completeness relaxation (partial function-tool-call fields,
// reasoning without OpenAI ResponseMeta) is granted only when the incomplete
// block carries the pinned Eino stream marker (ContentBlock.StreamingMeta != nil).
// Malformed complete-looking messages without StreamingMeta are validated under
// strict complete rules even through this API. ConcatStream validates chunks
// with this, then re-validates the concatenated output with Validate.
func ValidateStreamChunk(msg *schema.AgenticMessage) error {
	return validateMessage(msg, validateStreamFragment)
}

func validateMessage(msg *schema.AgenticMessage, kind validationKind) error {
	if msg == nil {
		return ErrNilMessage
	}
	if err := validateRole(msg.Role); err != nil {
		return err
	}
	for i, block := range msg.ContentBlocks {
		if err := validateBlock(msg, i, block, kind); err != nil {
			return err
		}
	}
	return nil
}

// ValidateConversation validates each message with Validate (complete-message
// semantics), then enforces a typed, consuming call ledger for the supported
// function/tool-search subset:
//
//   - Each assistant FunctionToolCall is classified via
//     agenticopenai.GetToolSearchToolCall as ordinary function vs client tool-search.
//   - CallIDs must be unique; empty IDs are rejected by Validate.
//   - FunctionToolResult may consume only an ordinary function call.
//   - ToolSearchFunctionToolResult may consume only a marked tool-search call.
//   - Results before calls, repeated results, and wrong-kind pairing fail with
//     typed errors. Unconsumed trailing calls are valid.
//   - Both ordinary function results and tool-search results require a non-empty
//     Name (via Validate) that exactly matches the originating call Name after
//     trim (no case-folding). Mismatches return ErrToolResultNameMismatch
//     (alias ErrToolSearchNameMismatch).
func ValidateConversation(msgs []*schema.AgenticMessage) error {
	ledger := make(map[string]*callLedgerEntry)
	for i, msg := range msgs {
		if msg == nil {
			return fmt.Errorf("%w: conversation index %d", ErrNilMessage, i)
		}
		if err := Validate(msg); err != nil {
			return fmt.Errorf("agenticmsg: conversation message %d: %w", i, err)
		}
		for _, block := range msg.ContentBlocks {
			if block == nil {
				continue
			}
			switch block.Type {
			case schema.ContentBlockTypeFunctionToolCall:
				id := strings.TrimSpace(block.FunctionToolCall.CallID)
				if id == "" {
					// Validate already requires CallID; defensive.
					return fmt.Errorf("%w: empty function_tool_call CallID at conversation index %d", ErrMalformedBlock, i)
				}
				if _, exists := ledger[id]; exists {
					return fmt.Errorf("%w: CallID %q at conversation index %d", ErrDuplicateCallID, id, i)
				}
				kind := callKindFunction
				if agenticopenai.GetToolSearchToolCall(block) {
					kind = callKindToolSearch
				}
				ledger[id] = &callLedgerEntry{
					kind: kind,
					name: strings.TrimSpace(block.FunctionToolCall.Name),
				}
			case schema.ContentBlockTypeFunctionToolResult:
				id := strings.TrimSpace(block.FunctionToolResult.CallID)
				if id == "" {
					return fmt.Errorf("%w: empty function_tool_result CallID at conversation index %d", ErrUnpairedToolResult, i)
				}
				entry, ok := ledger[id]
				if !ok {
					return fmt.Errorf("%w: function_tool_result CallID %q at conversation index %d", ErrUnpairedToolResult, id, i)
				}
				if entry.kind != callKindFunction {
					return fmt.Errorf("%w: function_tool_result for tool-search CallID %q at conversation index %d", ErrWrongKindToolResult, id, i)
				}
				if entry.consumed {
					return fmt.Errorf("%w: function_tool_result CallID %q at conversation index %d", ErrRepeatedToolResult, id, i)
				}
				resultName := strings.TrimSpace(block.FunctionToolResult.Name)
				if resultName != entry.name {
					return fmt.Errorf("%w: function_tool_result Name %q vs call Name %q for CallID %q at conversation index %d",
						ErrToolResultNameMismatch, resultName, entry.name, id, i)
				}
				entry.consumed = true
			case schema.ContentBlockTypeToolSearchResult:
				id := strings.TrimSpace(block.ToolSearchFunctionToolResult.CallID)
				if id == "" {
					return fmt.Errorf("%w: empty tool_search_result CallID at conversation index %d", ErrUnpairedToolResult, i)
				}
				entry, ok := ledger[id]
				if !ok {
					return fmt.Errorf("%w: tool_search_result CallID %q at conversation index %d", ErrUnpairedToolResult, id, i)
				}
				if entry.kind != callKindToolSearch {
					return fmt.Errorf("%w: tool_search_result for ordinary function CallID %q at conversation index %d", ErrWrongKindToolResult, id, i)
				}
				if entry.consumed {
					return fmt.Errorf("%w: tool_search_result CallID %q at conversation index %d", ErrRepeatedToolResult, id, i)
				}
				resultName := strings.TrimSpace(block.ToolSearchFunctionToolResult.Name)
				if resultName != entry.name {
					return fmt.Errorf("%w: tool_search_result Name %q vs call Name %q for CallID %q at conversation index %d",
						ErrToolResultNameMismatch, resultName, entry.name, id, i)
				}
				entry.consumed = true
			}
		}
	}
	return nil
}

func validateRole(role schema.AgenticRoleType) error {
	switch role {
	case schema.AgenticRoleTypeSystem, schema.AgenticRoleTypeUser, schema.AgenticRoleTypeAssistant:
		return nil
	case "":
		return fmt.Errorf("%w: empty", ErrInvalidRole)
	default:
		return fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
}

// roleAllowsBlock reports whether blockType is permitted on role for the
// deliberately narrow production protocol surface.
func roleAllowsBlock(role schema.AgenticRoleType, blockType schema.ContentBlockType) bool {
	switch role {
	case schema.AgenticRoleTypeSystem:
		switch blockType {
		case schema.ContentBlockTypeUserInputText,
			schema.ContentBlockTypeUserInputImage:
			return true
		default:
			return false
		}
	case schema.AgenticRoleTypeUser:
		switch blockType {
		case schema.ContentBlockTypeUserInputText,
			schema.ContentBlockTypeUserInputImage,
			schema.ContentBlockTypeUserInputFile,
			schema.ContentBlockTypeFunctionToolResult,
			schema.ContentBlockTypeToolSearchResult:
			return true
		default:
			return false
		}
	case schema.AgenticRoleTypeAssistant:
		switch blockType {
		case schema.ContentBlockTypeReasoning,
			schema.ContentBlockTypeAssistantGenText,
			schema.ContentBlockTypeFunctionToolCall:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// isProviderSupportedBlockType reports whether blockType appears in the
// narrow supported surface (regardless of role).
func isProviderSupportedBlockType(blockType schema.ContentBlockType) bool {
	return roleAllowsBlock(schema.AgenticRoleTypeSystem, blockType) ||
		roleAllowsBlock(schema.AgenticRoleTypeUser, blockType) ||
		roleAllowsBlock(schema.AgenticRoleTypeAssistant, blockType)
}

// contentPayloadSet reports which typed content payloads are non-nil.
// StreamingMeta and Extra are metadata, not content-union members.
type contentPayloadSet struct {
	reasoning               bool
	userInputText           bool
	userInputImage          bool
	userInputAudio          bool
	userInputVideo          bool
	userInputFile           bool
	assistantGenText        bool
	assistantGenImage       bool
	assistantGenAudio       bool
	assistantGenVideo       bool
	functionToolCall        bool
	functionToolResult      bool
	toolSearchResult        bool
	serverToolCall          bool
	serverToolResult        bool
	mcpToolCall             bool
	mcpToolResult           bool
	mcpListToolsResult      bool
	mcpToolApprovalRequest  bool
	mcpToolApprovalResponse bool
}

func collectPayloads(block *schema.ContentBlock) contentPayloadSet {
	return contentPayloadSet{
		reasoning:               block.Reasoning != nil,
		userInputText:           block.UserInputText != nil,
		userInputImage:          block.UserInputImage != nil,
		userInputAudio:          block.UserInputAudio != nil,
		userInputVideo:          block.UserInputVideo != nil,
		userInputFile:           block.UserInputFile != nil,
		assistantGenText:        block.AssistantGenText != nil,
		assistantGenImage:       block.AssistantGenImage != nil,
		assistantGenAudio:       block.AssistantGenAudio != nil,
		assistantGenVideo:       block.AssistantGenVideo != nil,
		functionToolCall:        block.FunctionToolCall != nil,
		functionToolResult:      block.FunctionToolResult != nil,
		toolSearchResult:        block.ToolSearchFunctionToolResult != nil,
		serverToolCall:          block.ServerToolCall != nil,
		serverToolResult:        block.ServerToolResult != nil,
		mcpToolCall:             block.MCPToolCall != nil,
		mcpToolResult:           block.MCPToolResult != nil,
		mcpListToolsResult:      block.MCPListToolsResult != nil,
		mcpToolApprovalRequest:  block.MCPToolApprovalRequest != nil,
		mcpToolApprovalResponse: block.MCPToolApprovalResponse != nil,
	}
}

func (p contentPayloadSet) count() int {
	n := 0
	if p.reasoning {
		n++
	}
	if p.userInputText {
		n++
	}
	if p.userInputImage {
		n++
	}
	if p.userInputAudio {
		n++
	}
	if p.userInputVideo {
		n++
	}
	if p.userInputFile {
		n++
	}
	if p.assistantGenText {
		n++
	}
	if p.assistantGenImage {
		n++
	}
	if p.assistantGenAudio {
		n++
	}
	if p.assistantGenVideo {
		n++
	}
	if p.functionToolCall {
		n++
	}
	if p.functionToolResult {
		n++
	}
	if p.toolSearchResult {
		n++
	}
	if p.serverToolCall {
		n++
	}
	if p.serverToolResult {
		n++
	}
	if p.mcpToolCall {
		n++
	}
	if p.mcpToolResult {
		n++
	}
	if p.mcpListToolsResult {
		n++
	}
	if p.mcpToolApprovalRequest {
		n++
	}
	if p.mcpToolApprovalResponse {
		n++
	}
	return n
}

// expectedPayloadSet is true when the payload matching block.Type is present.
func (p contentPayloadSet) hasExpected(blockType schema.ContentBlockType) bool {
	switch blockType {
	case schema.ContentBlockTypeReasoning:
		return p.reasoning
	case schema.ContentBlockTypeUserInputText:
		return p.userInputText
	case schema.ContentBlockTypeUserInputImage:
		return p.userInputImage
	case schema.ContentBlockTypeUserInputAudio:
		return p.userInputAudio
	case schema.ContentBlockTypeUserInputVideo:
		return p.userInputVideo
	case schema.ContentBlockTypeUserInputFile:
		return p.userInputFile
	case schema.ContentBlockTypeToolSearchResult:
		return p.toolSearchResult
	case schema.ContentBlockTypeAssistantGenText:
		return p.assistantGenText
	case schema.ContentBlockTypeAssistantGenImage:
		return p.assistantGenImage
	case schema.ContentBlockTypeAssistantGenAudio:
		return p.assistantGenAudio
	case schema.ContentBlockTypeAssistantGenVideo:
		return p.assistantGenVideo
	case schema.ContentBlockTypeFunctionToolCall:
		return p.functionToolCall
	case schema.ContentBlockTypeFunctionToolResult:
		return p.functionToolResult
	case schema.ContentBlockTypeServerToolCall:
		return p.serverToolCall
	case schema.ContentBlockTypeServerToolResult:
		return p.serverToolResult
	case schema.ContentBlockTypeMCPToolCall:
		return p.mcpToolCall
	case schema.ContentBlockTypeMCPToolResult:
		return p.mcpToolResult
	case schema.ContentBlockTypeMCPListToolsResult:
		return p.mcpListToolsResult
	case schema.ContentBlockTypeMCPToolApprovalRequest:
		return p.mcpToolApprovalRequest
	case schema.ContentBlockTypeMCPToolApprovalResponse:
		return p.mcpToolApprovalResponse
	default:
		return false
	}
}

// knownEinoBlockTypes are Eino-defined types recognized for missing-payload
// diagnostics before unsupported-surface rejection.
func isKnownEinoBlockType(blockType schema.ContentBlockType) bool {
	switch blockType {
	case schema.ContentBlockTypeReasoning,
		schema.ContentBlockTypeUserInputText,
		schema.ContentBlockTypeUserInputImage,
		schema.ContentBlockTypeUserInputAudio,
		schema.ContentBlockTypeUserInputVideo,
		schema.ContentBlockTypeUserInputFile,
		schema.ContentBlockTypeToolSearchResult,
		schema.ContentBlockTypeAssistantGenText,
		schema.ContentBlockTypeAssistantGenImage,
		schema.ContentBlockTypeAssistantGenAudio,
		schema.ContentBlockTypeAssistantGenVideo,
		schema.ContentBlockTypeFunctionToolCall,
		schema.ContentBlockTypeFunctionToolResult,
		schema.ContentBlockTypeServerToolCall,
		schema.ContentBlockTypeServerToolResult,
		schema.ContentBlockTypeMCPToolCall,
		schema.ContentBlockTypeMCPToolResult,
		schema.ContentBlockTypeMCPListToolsResult,
		schema.ContentBlockTypeMCPToolApprovalRequest,
		schema.ContentBlockTypeMCPToolApprovalResponse:
		return true
	default:
		return false
	}
}

func validateBlock(msg *schema.AgenticMessage, index int, block *schema.ContentBlock, kind validationKind) error {
	if block == nil {
		return fmt.Errorf("%w: at index %d", ErrNilBlock, index)
	}
	if block.Type == "" {
		return fmt.Errorf("%w: empty type at index %d", ErrUnsupportedBlock, index)
	}

	payloads := collectPayloads(block)
	if !payloads.hasExpected(block.Type) {
		if isKnownEinoBlockType(block.Type) {
			return fmt.Errorf("%w: %s payload missing at index %d", ErrMalformedBlock, block.Type, index)
		}
		return fmt.Errorf("%w: unknown type %q at index %d", ErrUnsupportedBlock, block.Type, index)
	}

	// Union exclusivity: exactly one content payload, matching Type.
	if payloads.count() != 1 {
		return fmt.Errorf("%w: type %q has extra incompatible payload at index %d", ErrMalformedBlock, block.Type, index)
	}

	if !roleAllowsBlock(msg.Role, block.Type) {
		// Supported type on the wrong role → incompatible.
		// Outside the narrow surface → unsupported.
		if isProviderSupportedBlockType(block.Type) {
			return fmt.Errorf("%w: type %q not allowed on role %q at index %d", ErrIncompatibleBlock, block.Type, msg.Role, index)
		}
		return fmt.Errorf("%w: type %q not in supported protocol surface at index %d", ErrUnsupportedBlock, block.Type, index)
	}

	// Nested / type-specific preconditions matching the pinned converter.
	switch block.Type {
	case schema.ContentBlockTypeUserInputImage:
		return validateUserInputImage(index, block.UserInputImage)
	case schema.ContentBlockTypeUserInputFile:
		return validateUserInputFile(index, block.UserInputFile)
	case schema.ContentBlockTypeFunctionToolResult:
		return validateFunctionToolResult(index, block.FunctionToolResult)
	case schema.ContentBlockTypeToolSearchResult:
		return validateToolSearchResult(index, block.ToolSearchFunctionToolResult)
	case schema.ContentBlockTypeFunctionToolCall:
		return validateFunctionToolCall(index, block.FunctionToolCall, kind, block.StreamingMeta != nil)
	case schema.ContentBlockTypeAssistantGenText:
		return validateAssistantGenText(index, block.AssistantGenText, kind, block.StreamingMeta != nil)
	case schema.ContentBlockTypeReasoning:
		return validateReasoning(msg, index, block, kind)
	default:
		return nil
	}
}

// allowStreamOnlyIndexes reports whether stream-assembly-only extension indexes
// (TextAnnotation.Index, ReasoningContent.Index) may be present. Relaxation is
// granted only for authentic raw stream fragments: ValidateStreamChunk mode and
// a non-nil ContentBlock.StreamingMeta provenance marker.
func allowStreamOnlyIndexes(kind validationKind, hasStreamProvenance bool) bool {
	return kind == validateStreamFragment && hasStreamProvenance
}

// validateMediaSource mirrors agenticopenai resolveURL preconditions:
// non-empty URL, or raw base64 + MIME (base64 must not already be a data: URL).
func validateMediaSource(kind string, index int, url, base64Data, mimeType string) error {
	if strings.TrimSpace(url) != "" {
		return nil
	}
	if strings.TrimSpace(base64Data) == "" {
		return fmt.Errorf("%w: %s requires URL or base64 data at index %d", ErrMalformedBlock, kind, index)
	}
	if strings.HasPrefix(base64Data, "data:") {
		return fmt.Errorf("%w: %s base64Data must be raw base64 without data: prefix at index %d", ErrMalformedBlock, kind, index)
	}
	if strings.TrimSpace(mimeType) == "" {
		return fmt.Errorf("%w: %s mimeType is required when using base64Data at index %d", ErrMalformedBlock, kind, index)
	}
	return nil
}

func validateUserInputImage(index int, img *schema.UserInputImage) error {
	if img == nil {
		return fmt.Errorf("%w: user_input_image payload missing at index %d", ErrMalformedBlock, index)
	}
	if err := validateMediaSource("user_input_image", index, img.URL, img.Base64Data, img.MIMEType); err != nil {
		return err
	}
	switch img.Detail {
	case "", schema.ImageURLDetailHigh, schema.ImageURLDetailLow, schema.ImageURLDetailAuto:
		return nil
	default:
		return fmt.Errorf("%w: invalid image detail %q at index %d", ErrMalformedBlock, img.Detail, index)
	}
}

func validateUserInputFile(index int, file *schema.UserInputFile) error {
	if file == nil {
		return fmt.Errorf("%w: user_input_file payload missing at index %d", ErrMalformedBlock, index)
	}
	return validateMediaSource("user_input_file", index, file.URL, file.Base64Data, file.MIMEType)
}

func validateFunctionToolCall(index int, call *schema.FunctionToolCall, kind validationKind, hasStreamProvenance bool) error {
	if call == nil {
		return fmt.Errorf("%w: function_tool_call payload missing at index %d", ErrMalformedBlock, index)
	}
	// Completeness relaxation (partial CallID/Name/Arguments) is granted only
	// for authentic stream fragments that carry StreamingMeta. Messages that
	// merely call ValidateStreamChunk without the marker are held to complete rules.
	if kind == validateStreamFragment && hasStreamProvenance {
		return nil
	}
	if strings.TrimSpace(call.CallID) == "" {
		return fmt.Errorf("%w: function_tool_call CallID is required at index %d", ErrMalformedBlock, index)
	}
	if strings.TrimSpace(call.Name) == "" {
		return fmt.Errorf("%w: function_tool_call Name is required at index %d", ErrMalformedBlock, index)
	}
	if err := validateJSONObjectArguments(index, call.Arguments); err != nil {
		return err
	}
	return nil
}

// validateJSONObjectArguments requires Arguments to be exactly one JSON object
// (the form Responses/function and client tool-search calls expect). Rejects
// empty, null, arrays, scalars, trailing data, and duplicate object keys at
// any nesting level (including objects inside arrays and escaped-equivalent
// keys such as "a" vs "\u0061"). Errors never embed the raw argument string.
func validateJSONObjectArguments(index int, args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return fmt.Errorf("%w: empty at index %d", ErrInvalidToolArguments, index)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	if err := decodeStrictJSONObject(dec); err != nil {
		switch {
		case errors.Is(err, errJSONMustBeObject):
			return fmt.Errorf("%w: must be a JSON object at index %d", ErrInvalidToolArguments, index)
		case errors.Is(err, errJSONDuplicateKey):
			return fmt.Errorf("%w: duplicate object key at index %d", ErrInvalidToolArguments, index)
		default:
			// Do not include raw args (may contain secrets); keep a short cause.
			return fmt.Errorf("%w: not valid JSON at index %d: %v", ErrInvalidToolArguments, index, err)
		}
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after JSON value at index %d", ErrInvalidToolArguments, index)
	}
	// Reject leftover tokens/garbage after the single object (More only sees
	// additional JSON values; Token catches other trailing content).
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data after JSON value at index %d", ErrInvalidToolArguments, index)
	}
	return nil
}

// Internal sentinels for strict JSON object decoding (not exported).
var (
	errJSONMustBeObject  = errors.New("json must be object")
	errJSONDuplicateKey  = errors.New("json duplicate key")
	errJSONUnexpectedEnd = errors.New("json unexpected end")
)

// decodeStrictJSONObject reads exactly one JSON object from dec, recursively
// rejecting duplicate keys in every nested object.
func decodeStrictJSONObject(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return errJSONMustBeObject
	}
	return decodeStrictObjectBody(dec)
}

func decodeStrictObjectBody(dec *json.Decoder) error {
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("object key is not a string")
		}
		// json.Decoder unescapes keys, so "a" and "\u0061" collide here.
		if _, exists := seen[key]; exists {
			return errJSONDuplicateKey
		}
		seen[key] = struct{}{}
		if err := decodeStrictJSONValue(dec); err != nil {
			return err
		}
	}
	// Consume the closing '}'.
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '}' {
		return errJSONUnexpectedEnd
	}
	return nil
}

func decodeStrictArrayBody(dec *json.Decoder) error {
	for dec.More() {
		if err := decodeStrictJSONValue(dec); err != nil {
			return err
		}
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != ']' {
		return errJSONUnexpectedEnd
	}
	return nil
}

func decodeStrictJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); ok {
		switch delim {
		case '{':
			return decodeStrictObjectBody(dec)
		case '[':
			return decodeStrictArrayBody(dec)
		default:
			return fmt.Errorf("unexpected delimiter %q", delim)
		}
	}
	// Primitive (string, Number, bool, nil) already consumed as the token.
	return nil
}

func validateAssistantGenText(index int, text *schema.AssistantGenText, kind validationKind, hasStreamProvenance bool) error {
	if text == nil {
		return fmt.Errorf("%w: assistant_gen_text payload missing at index %d", ErrMalformedBlock, index)
	}
	// Claude/other extensions are not needed on the OpenAI production path;
	// reject so they cannot be silently discarded by the converter.
	if text.ClaudeExtension != nil {
		return fmt.Errorf("%w: assistant_gen_text ClaudeExtension not supported at index %d", ErrUnsupportedBlock, index)
	}
	if text.Extension != nil {
		return fmt.Errorf("%w: assistant_gen_text generic Extension not supported at index %d", ErrUnsupportedBlock, index)
	}
	if text.OpenAIExtension == nil {
		return nil
	}
	return validateAssistantGenTextOpenAIExtension(index, text.OpenAIExtension, kind, hasStreamProvenance)
}

func validateAssistantGenTextOpenAIExtension(index int, ext *openai.AssistantGenTextExtension, kind validationKind, hasStreamProvenance bool) error {
	// Refusal is a distinct OpenAI output content kind the pinned adapter can
	// emit, but the deliberate narrow production protocol does not project
	// refusal semantics (replay only maps Text + Annotations; ExtractAssistantText
	// would silently drop Refusal). Fail closed on any non-nil Refusal pointer,
	// including empty-reason sentinels — do not merge into assistant text.
	if ext.Refusal != nil {
		return fmt.Errorf("%w: assistant_gen_text OpenAIExtension.Refusal not supported at index %d", ErrUnsupportedBlock, index)
	}
	// Annotations are losslessly supported by the pinned converter for semantic
	// citation payloads; TextAnnotation.Index is stream-assembly-only and is
	// dropped by ConcatAssistantGenTextExtensions / ignored on replay input.
	for i, anno := range ext.Annotations {
		if anno == nil {
			return fmt.Errorf("%w: nil text annotation at index %d annotation %d", ErrNilBlock, index, i)
		}
		if err := validateTextAnnotation(index, i, anno, kind, hasStreamProvenance); err != nil {
			return err
		}
	}
	return nil
}

func validateTextAnnotation(parentIndex, annoIndex int, annotation *openai.TextAnnotation, kind validationKind, hasStreamProvenance bool) error {
	// Stream-assembly index: pinned schema marks "only available in streaming
	// response"; adapter replay ignores it. Reject non-default on complete
	// paths; permit only with StreamingMeta provenance under stream validation.
	if annotation.Index != 0 && !allowStreamOnlyIndexes(kind, hasStreamProvenance) {
		return fmt.Errorf("%w: TextAnnotation.Index is stream-assembly-only at index %d annotation %d",
			ErrStreamOnlyField, parentIndex, annoIndex)
	}

	// Exclusive nested union: exactly one payload pointer matching Type.
	// Nested payload indexes (FileCitation.Index, URL Start/EndIndex, FilePath.Index)
	// are semantic citation positions, losslessly replayed — not stream-assembly.
	n := 0
	if annotation.FileCitation != nil {
		n++
	}
	if annotation.URLCitation != nil {
		n++
	}
	if annotation.ContainerFileCitation != nil {
		n++
	}
	if annotation.FilePath != nil {
		n++
	}

	switch annotation.Type {
	case openai.TextAnnotationTypeFileCitation:
		if annotation.FileCitation == nil {
			return fmt.Errorf("%w: file citation is nil at index %d annotation %d", ErrMalformedBlock, parentIndex, annoIndex)
		}
	case openai.TextAnnotationTypeURLCitation:
		if annotation.URLCitation == nil {
			return fmt.Errorf("%w: url citation is nil at index %d annotation %d", ErrMalformedBlock, parentIndex, annoIndex)
		}
	case openai.TextAnnotationTypeContainerFileCitation:
		if annotation.ContainerFileCitation == nil {
			return fmt.Errorf("%w: container file citation is nil at index %d annotation %d", ErrMalformedBlock, parentIndex, annoIndex)
		}
	case openai.TextAnnotationTypeFilePath:
		if annotation.FilePath == nil {
			return fmt.Errorf("%w: file path is nil at index %d annotation %d", ErrMalformedBlock, parentIndex, annoIndex)
		}
	case "":
		return fmt.Errorf("%w: empty text annotation type at index %d annotation %d", ErrMalformedBlock, parentIndex, annoIndex)
	default:
		return fmt.Errorf("%w: invalid text annotation type %q at index %d annotation %d", ErrUnsupportedBlock, annotation.Type, parentIndex, annoIndex)
	}
	if n != 1 {
		return fmt.Errorf("%w: text annotation type %q must have exactly one payload at index %d annotation %d",
			ErrMalformedBlock, annotation.Type, parentIndex, annoIndex)
	}
	return nil
}

func validateReasoning(msg *schema.AgenticMessage, index int, block *schema.ContentBlock, kind validationKind) error {
	if block.Reasoning == nil {
		return fmt.Errorf("%w: reasoning payload missing at index %d", ErrMalformedBlock, index)
	}
	// Complete messages always require OpenAI self-generated metadata
	// (mirrors isSelfGeneratedMessage). Stream fragments may omit it only when
	// the block carries the pinned StreamingMeta marker; otherwise fail typed
	// even under ValidateStreamChunk so complete-looking messages cannot bypass
	// complete validation by calling the stream API.
	if !isOpenAISelfGenerated(msg) {
		if kind == validateStreamFragment && block.StreamingMeta != nil {
			// Authentic stream fragment: ResponseMeta may arrive on a later chunk.
		} else {
			return fmt.Errorf("%w: reasoning requires OpenAI self-generated ResponseMeta.OpenAIExtension at index %d", ErrMalformedBlock, index)
		}
	}
	// Nested OpenAI reasoning content: converter silently skips nil entries;
	// fail closed so complete/persist paths never accept data that would be dropped.
	// ReasoningContent.Index is stream-assembly-only (adapter drops on replay;
	// ConcatReasoningExtensions clears it when multi-chunk concat runs).
	if ext := block.Reasoning.OpenAIExtension; ext != nil {
		for i, c := range ext.Content {
			if c == nil {
				return fmt.Errorf("%w: nil OpenAI reasoning content at index %d content %d", ErrNilBlock, index, i)
			}
			if c.Index != nil && !allowStreamOnlyIndexes(kind, block.StreamingMeta != nil) {
				return fmt.Errorf("%w: ReasoningContent.Index is stream-assembly-only at index %d content %d",
					ErrStreamOnlyField, index, i)
			}
		}
	}
	return nil
}

// validateFunctionToolResult enforces pinned converter preconditions:
// non-empty CallID and Name; non-empty content; text-only nested blocks
// (project tool outputs are textual). Conversation pairing additionally
// requires Name to match the originating call (see ValidateConversation).
func validateFunctionToolResult(parentIndex int, result *schema.FunctionToolResult) error {
	if result == nil {
		return fmt.Errorf("%w: function_tool_result payload missing at index %d", ErrMalformedBlock, parentIndex)
	}
	if strings.TrimSpace(result.CallID) == "" {
		return fmt.Errorf("%w: function_tool_result CallID is required at index %d", ErrMalformedBlock, parentIndex)
	}
	if strings.TrimSpace(result.Name) == "" {
		return fmt.Errorf("%w: function_tool_result Name is required at index %d", ErrMalformedBlock, parentIndex)
	}
	if len(result.Content) == 0 {
		return fmt.Errorf("%w: function_tool_result content is empty at index %d", ErrMalformedBlock, parentIndex)
	}
	for i, nested := range result.Content {
		if err := validateFunctionToolResultContentBlock(parentIndex, i, nested); err != nil {
			return err
		}
	}
	return nil
}

func validateFunctionToolResultContentBlock(parentIndex, index int, block *schema.FunctionToolResultContentBlock) error {
	if block == nil {
		return fmt.Errorf("%w: function_tool_result content at parent %d nested index %d", ErrNilBlock, parentIndex, index)
	}
	if block.Type == "" {
		return fmt.Errorf("%w: empty nested function_tool_result type at parent %d index %d", ErrUnsupportedBlock, parentIndex, index)
	}

	n := 0
	if block.Text != nil {
		n++
	}
	if block.Image != nil {
		n++
	}
	if block.Audio != nil {
		n++
	}
	if block.Video != nil {
		n++
	}
	if block.File != nil {
		n++
	}

	switch block.Type {
	case schema.FunctionToolResultContentBlockTypeText:
		if block.Text == nil {
			return fmt.Errorf("%w: nested text payload missing at parent %d index %d", ErrMalformedBlock, parentIndex, index)
		}
		if strings.TrimSpace(block.Text.Text) == "" {
			return fmt.Errorf("%w: nested text is empty or whitespace-only at parent %d index %d", ErrMalformedBlock, parentIndex, index)
		}
		// Text-only foundation: extra media pointers fail exclusivity below.
	case schema.FunctionToolResultContentBlockTypeImage,
		schema.FunctionToolResultContentBlockTypeFile,
		schema.FunctionToolResultContentBlockTypeAudio,
		schema.FunctionToolResultContentBlockTypeVideo:
		// Nested multimodal tool results are out of the production surface;
		// project tool outputs are textual JSON/text only.
		return fmt.Errorf("%w: nested function_tool_result type %q not supported (text only) at parent %d index %d",
			ErrUnsupportedBlock, block.Type, parentIndex, index)
	default:
		return fmt.Errorf("%w: unknown nested function_tool_result type %q at parent %d index %d",
			ErrUnsupportedBlock, block.Type, parentIndex, index)
	}

	if n != 1 {
		return fmt.Errorf("%w: nested type %q has extra incompatible payload at parent %d index %d",
			ErrMalformedBlock, block.Type, parentIndex, index)
	}
	return nil
}

// validateToolSearchResult enforces Result non-nil, non-empty Name, and safe
// tool slice preconditions for agenticopenai toolSearchToolResultBlockToInputItem /
// toDeferredFunctionTools (nil *ToolInfo panics upstream). Conversation pairing
// additionally requires Name to match the originating marked call.
func validateToolSearchResult(parentIndex int, result *schema.ToolSearchFunctionToolResult) error {
	if result == nil {
		return fmt.Errorf("%w: tool_search_result payload missing at index %d", ErrMalformedBlock, parentIndex)
	}
	if strings.TrimSpace(result.CallID) == "" {
		return fmt.Errorf("%w: tool_search_result CallID is required at index %d", ErrMalformedBlock, parentIndex)
	}
	if strings.TrimSpace(result.Name) == "" {
		return fmt.Errorf("%w: tool_search_result Name is required at index %d", ErrMalformedBlock, parentIndex)
	}
	if result.Result == nil {
		return fmt.Errorf("%w: tool_search_result Result must be non-nil at index %d", ErrMalformedBlock, parentIndex)
	}
	// Empty non-nil Tools is valid (zero search results).
	for i, ti := range result.Result.Tools {
		if err := validateToolInfo(parentIndex, i, ti); err != nil {
			return err
		}
	}
	return nil
}

func validateToolInfo(parentIndex, index int, ti *schema.ToolInfo) error {
	if ti == nil {
		return fmt.Errorf("%w: nil ToolInfo at tool_search parent %d tool index %d", ErrNilBlock, parentIndex, index)
	}
	if strings.TrimSpace(ti.Name) == "" {
		return fmt.Errorf("%w: empty tool name at tool_search parent %d tool index %d", ErrMalformedBlock, parentIndex, index)
	}
	// Probe public schema conversion; recover from panics on nil ParameterInfo
	// entries inside ParamsOneOf (unexported fields; ToJSONSchema can panic).
	if err := probeToolParamsSchema(ti); err != nil {
		return fmt.Errorf("%w: tool %q schema at tool_search parent %d tool index %d: %v",
			ErrMalformedBlock, ti.Name, parentIndex, index, err)
	}
	return nil
}

func probeToolParamsSchema(ti *schema.ToolInfo) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("schema conversion panic: %v", r)
		}
	}()
	// ToolInfo embeds *ParamsOneOf; nil ParamsOneOf is valid (no parameters).
	if _, e := ti.ToJSONSchema(); e != nil {
		return e
	}
	return nil
}

// ExtractAssistantText concatenates all AssistantGenText blocks from an
// assistant message. Fails closed on nil messages, non-assistant roles,
// malformed known blocks, role-incompatible blocks, and unknown types.
//
// Allowed non-text companion blocks (assistant-legal only; ignored after
// centralized validation): Reasoning, FunctionToolCall.
func ExtractAssistantText(msg *schema.AgenticMessage) (string, error) {
	if err := Validate(msg); err != nil {
		return "", err
	}
	if msg.Role != schema.AgenticRoleTypeAssistant {
		return "", fmt.Errorf("%w: got %q want assistant", ErrWrongRole, msg.Role)
	}
	text, err := assistantPublicText(msg)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrNoAssistantText
	}
	return text, nil
}

// ExtractAssistantChunkText is ExtractAssistantText for one streaming chunk.
//
// Chunks are validated as chunks (stream provenance, partial reasoning), which
// the complete-message rules of ExtractAssistantText reject. Unlike a complete
// message, a chunk carrying no public text is ordinary rather than an error: a
// turn streams reasoning and tool-call chunks alongside text ones. Callers that
// project deltas must skip the empty result, not treat it as a failure.
//
// It shares assistantPublicText with ExtractAssistantText so there is exactly
// one definition of which blocks are public: a second walker would be free to
// drift and start projecting reasoning as user-visible text.
func ExtractAssistantChunkText(chunk *schema.AgenticMessage) (string, error) {
	if err := ValidateStreamChunk(chunk); err != nil {
		return "", err
	}
	if chunk.Role != schema.AgenticRoleTypeAssistant {
		return "", fmt.Errorf("%w: got %q want assistant", ErrWrongRole, chunk.Role)
	}
	return assistantPublicText(chunk)
}

// assistantPublicText concatenates the blocks of an assistant message that are
// user-visible text. Reasoning and tool calls are valid companions and are
// deliberately not public.
func assistantPublicText(msg *schema.AgenticMessage) (string, error) {
	var parts []string
	for i, block := range msg.ContentBlocks {
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			parts = append(parts, block.AssistantGenText.Text)
		case schema.ContentBlockTypeReasoning,
			schema.ContentBlockTypeFunctionToolCall:
			continue
		default:
			// Defensive: validation should already reject anything outside the
			// narrow assistant matrix.
			return "", fmt.Errorf("%w: type %q at index %d", ErrUnsupportedBlock, block.Type, i)
		}
	}
	return strings.Join(parts, ""), nil
}

// ExtractReasoningText concatenates Reasoning block text from an assistant
// message. Nil / wrong role / malformed / role-incompatible data fail closed.
func ExtractReasoningText(msg *schema.AgenticMessage) (string, error) {
	if err := Validate(msg); err != nil {
		return "", err
	}
	if msg.Role != schema.AgenticRoleTypeAssistant {
		return "", fmt.Errorf("%w: got %q want assistant", ErrWrongRole, msg.Role)
	}
	var parts []string
	for _, block := range msg.ContentBlocks {
		if block.Type != schema.ContentBlockTypeReasoning {
			continue
		}
		if t := block.Reasoning.Text; t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, ""), nil
}

// FunctionCalls returns function tool call blocks from an assistant message.
// Nil / wrong role / malformed / role-incompatible data fail closed.
func FunctionCalls(msg *schema.AgenticMessage) ([]schema.FunctionToolCall, error) {
	if err := Validate(msg); err != nil {
		return nil, err
	}
	if msg.Role != schema.AgenticRoleTypeAssistant {
		return nil, fmt.Errorf("%w: got %q want assistant", ErrWrongRole, msg.Role)
	}
	var out []schema.FunctionToolCall
	for _, block := range msg.ContentBlocks {
		if block.Type != schema.ContentBlockTypeFunctionToolCall {
			continue
		}
		out = append(out, *block.FunctionToolCall)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Usage projects token usage including cached prompt tokens and reasoning
// completion tokens. It always runs centralized Validate first.
//
// Nil, invalid-role, malformed, or role-incompatible messages return a typed
// error. Absence of optional usage metadata on an otherwise valid message
// returns zero TokenUsage with a nil error.
func Usage(msg *schema.AgenticMessage) (TokenUsage, error) {
	if err := Validate(msg); err != nil {
		return TokenUsage{}, err
	}
	if msg.ResponseMeta == nil || msg.ResponseMeta.TokenUsage == nil {
		return TokenUsage{}, nil
	}
	u := msg.ResponseMeta.TokenUsage
	return TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.PromptTokenDetails.CachedTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
	}, nil
}

// ConcatStream concatenates streaming AgenticMessage chunks using Eino's
// schema.ConcatAgenticMessages.
//
// Phases (typed errors preserved per phase):
//  1. Pre-concat: empty input / nil chunks / ValidateStreamChunk on each chunk
//  2. Upstream concat: failures wrap ErrConcat (role mismatch, mixed streaming, …)
//  3. Normalize: clear stream-assembly-only indexes if upstream retained them
//     (without mutating caller-owned input chunks)
//  4. Post-concat: Validate the complete output (strict complete-message semantics)
//
// Incomplete reasoning stream fragments are therefore accepted as inputs only
// when they later concatenate into a self-generated complete message.
func ConcatStream(chunks []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
	if len(chunks) == 0 {
		return nil, ErrEmptyConcat
	}
	for i, c := range chunks {
		if c == nil {
			return nil, fmt.Errorf("%w: at index %d", ErrNilChunk, i)
		}
		if err := ValidateStreamChunk(c); err != nil {
			return nil, fmt.Errorf("agenticmsg: concat chunk %d: %w", i, err)
		}
	}
	out, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		// Preserve any of our sentinels if they ever surface from upstream;
		// wrap everything else so callers can match ErrConcat.
		if errors.Is(err, ErrEmptyConcat) || errors.Is(err, ErrNilChunk) ||
			errors.Is(err, ErrNilMessage) || errors.Is(err, ErrNilBlock) ||
			errors.Is(err, ErrMalformedBlock) || errors.Is(err, ErrIncompatibleBlock) ||
			errors.Is(err, ErrUnsupportedBlock) || errors.Is(err, ErrInvalidRole) ||
			errors.Is(err, ErrUnpairedToolResult) ||
			errors.Is(err, ErrInvalidToolArguments) ||
			errors.Is(err, ErrDuplicateCallID) ||
			errors.Is(err, ErrRepeatedToolResult) ||
			errors.Is(err, ErrWrongKindToolResult) ||
			errors.Is(err, ErrToolResultNameMismatch) ||
			errors.Is(err, ErrStreamOnlyField) ||
			errors.Is(err, ErrConcat) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrConcat, err)
	}
	// Upstream multi-chunk concat clears TextAnnotation.Index / ReasoningContent.Index
	// via openai.Concat*Extensions, but single-chunk early returns (and some
	// single-payload multi-message paths) retain the original pointers. Clear
	// proven stream-assembly indexes only after cloning so inputs stay intact.
	out = normalizeConcatOutputStreamOnlyIndexes(out)
	if err := Validate(out); err != nil {
		return nil, fmt.Errorf("agenticmsg: concat output: %w", err)
	}
	return out, nil
}

// messageHasStreamOnlyIndexes reports whether msg still carries stream-assembly
// indexes that complete Validate would reject.
func messageHasStreamOnlyIndexes(msg *schema.AgenticMessage) bool {
	if msg == nil {
		return false
	}
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		if text := block.AssistantGenText; text != nil && text.OpenAIExtension != nil {
			for _, anno := range text.OpenAIExtension.Annotations {
				if anno != nil && anno.Index != 0 {
					return true
				}
			}
		}
		if reasoning := block.Reasoning; reasoning != nil && reasoning.OpenAIExtension != nil {
			for _, c := range reasoning.OpenAIExtension.Content {
				if c != nil && c.Index != nil {
					return true
				}
			}
		}
	}
	return false
}

// normalizeConcatOutputStreamOnlyIndexes returns a message safe for complete
// Validate with respect to stream-assembly indexes. When no such indexes are
// present, msg is returned unchanged. Otherwise a shallow structural copy is
// produced and only those indexes are cleared — never mutating the input graph
// that may still be referenced by caller-owned chunks.
func normalizeConcatOutputStreamOnlyIndexes(msg *schema.AgenticMessage) *schema.AgenticMessage {
	if !messageHasStreamOnlyIndexes(msg) {
		return msg
	}
	out := &schema.AgenticMessage{
		Role:         msg.Role,
		ResponseMeta: msg.ResponseMeta,
		Extra:        msg.Extra,
	}
	if len(msg.ContentBlocks) == 0 {
		return out
	}
	out.ContentBlocks = make([]*schema.ContentBlock, len(msg.ContentBlocks))
	for i, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		// Copy the block shell so we can replace nested payloads we normalize.
		nb := *block
		if text := block.AssistantGenText; text != nil && text.OpenAIExtension != nil &&
			annotationSliceHasNonDefaultIndex(text.OpenAIExtension.Annotations) {
			tCopy := *text
			extCopy := *text.OpenAIExtension
			extCopy.Annotations = cloneAnnotationsClearingStreamIndex(text.OpenAIExtension.Annotations)
			tCopy.OpenAIExtension = &extCopy
			nb.AssistantGenText = &tCopy
		}
		if reasoning := block.Reasoning; reasoning != nil && reasoning.OpenAIExtension != nil &&
			reasoningContentSliceHasIndex(reasoning.OpenAIExtension.Content) {
			rCopy := *reasoning
			extCopy := *reasoning.OpenAIExtension
			extCopy.Content = cloneReasoningContentClearingStreamIndex(reasoning.OpenAIExtension.Content)
			rCopy.OpenAIExtension = &extCopy
			nb.Reasoning = &rCopy
		}
		out.ContentBlocks[i] = &nb
	}
	return out
}

func annotationSliceHasNonDefaultIndex(annos []*openai.TextAnnotation) bool {
	for _, a := range annos {
		if a != nil && a.Index != 0 {
			return true
		}
	}
	return false
}

func reasoningContentSliceHasIndex(content []*openai.ReasoningContent) bool {
	for _, c := range content {
		if c != nil && c.Index != nil {
			return true
		}
	}
	return false
}

func cloneAnnotationsClearingStreamIndex(annos []*openai.TextAnnotation) []*openai.TextAnnotation {
	if annos == nil {
		return nil
	}
	out := make([]*openai.TextAnnotation, len(annos))
	for i, a := range annos {
		if a == nil {
			continue
		}
		ac := *a
		ac.Index = 0
		out[i] = &ac
	}
	return out
}

func cloneReasoningContentClearingStreamIndex(content []*openai.ReasoningContent) []*openai.ReasoningContent {
	if content == nil {
		return nil
	}
	out := make([]*openai.ReasoningContent, len(content))
	for i, c := range content {
		if c == nil {
			continue
		}
		cc := *c
		cc.Index = nil
		out[i] = &cc
	}
	return out
}
