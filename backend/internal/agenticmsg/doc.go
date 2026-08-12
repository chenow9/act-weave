// Package agenticmsg is the single shared construction and extraction layer
// for Eino schema.AgenticMessage content (design D8).
//
// All bridge, SmartDAG, prompt enhancement, compaction, and audit wrappers
// must use this package for Agentic content access. Callers must not
// re-implement ContentBlock switches elsewhere.
//
// Supported protocol surface (deliberately narrow; production mode is
// client-executed function tools + client-executed native tool search):
//
//	system:    user_input_text, user_input_image
//	user:      user_input_text|image|file, function_tool_result (text-only nested),
//	           tool_search_result
//	assistant: assistant_gen_text, function_tool_call, reasoning
//	           (complete reasoning always requires ResponseMeta.OpenAIExtension
//	           != nil, matching agenticopenai isSelfGeneratedMessage)
//
// Rejected with typed errors even if generic Eino or the upstream adapter can
// represent them: server-tool call/result, all MCP types, approval request/
// response, user audio/video, assistant-generated image/audio/video,
// nested function-tool-result image/file/audio/video, and
// AssistantGenText.OpenAIExtension.Refusal (adapter can emit it; protocol does
// not project refusal semantics and would otherwise drop it on extract/replay).
//
// Fail-closed rules:
//   - Validate means complete, safe-to-send/persist message (strict OpenAI
//     self-generated reasoning metadata, valid JSON object Arguments, exclusive
//     annotation unions, non-nil reasoning content entries, non-empty tool text).
//     Stream-assembly-only extension indexes (TextAnnotation.Index != 0,
//     ReasoningContent.Index != nil) are rejected with ErrStreamOnlyField
//     because the pinned adapter drops them on replay conversion.
//   - ValidateStreamChunk is for raw stream fragments only; it may permit
//     temporarily incomplete response metadata, partial function-call fields,
//     and stream-assembly indexes the pinned stream converter emits, but only
//     when those blocks carry ContentBlock.StreamingMeta (explicit stream
//     provenance). Without the marker, incomplete/stream-only fields fail
//     typed under complete rules.
//   - ValidateConversation uses Validate plus a typed, consuming CallID ledger
//     that classifies ordinary vs tool-search calls via
//     agenticopenai.GetToolSearchToolCall and rejects cross-kind pairing,
//     duplicates, repeated results, blank result names, and result Name
//     mismatches against the originating call (exact trimmed match).
//   - ContentBlock union exclusivity: exactly one payload matching Type.
//   - Nested FunctionToolResult content is non-empty, non-whitespace text-only;
//     tool-search Result must be non-nil; tool slices reject nil entries.
//   - Image/file URL-vs-base64/MIME preconditions match the pinned converter.
//   - Extraction helpers never silently ignore role-incompatible blocks;
//     valid companions may be ignored only after Validate succeeds.
//   - ConcatStream: ValidateStreamChunk each input → upstream concat →
//     normalize retained stream-assembly indexes (without mutating inputs) →
//     Validate complete output; typed errors per phase; ErrConcat wraps
//     upstream concat failures.
package agenticmsg
