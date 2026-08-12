// Package modelapi adapts ACTWEAVE modelconfig + secret credentials to Eino
// model interfaces (classic ChatModel and AgenticModel).
//
// Classic path (still used by runtime callers until later migration tasks):
//   - NewEinoOpenAIChatModel → eino-ext openai Chat Completions ToolCallingChatModel
//   - PlatformChatModel → hand-rolled Completions client for tests / reference
//
// Agentic foundation (Task 1; callers migrate later):
//   - NewOpenAIAgenticModel → guarded agenticopenai ResponsesModel (model.AgenticModel)
//   - Strict API base validation at construction (absolute http/https,
//     non-empty Hostname, valid port 1..65535 when present; reject relative,
//     userinfo, query, fragment, opaque, empty-host, invalid-port, control
//     characters; errors never echo the raw URL)
//   - Store=false, EnableAutoCache=false, ParallelToolCalls=false (fixed)
//   - Call-time options and ExtraFields cannot raise store/parallel or set
//     previous_response_id; wire-level HTTP rewrite + option append enforce this
//   - Generate/Stream validate conversation protocol surface + tool-result
//     pairing via agenticmsg before delegation
//   - Model-specific HTTP client rejects redirects (no unguarded body follow)
//   - Invalid / non-object Responses JSON bodies fail closed before transport
//   - WithPromptCacheKey for per-request prompt_cache_key (typed option)
//   - WithProtectedPromptCacheKey + transport force-set so ExtraFields cannot
//     override a platform-owned prompt_cache_key on the wire
//   - Azure provider and apiVersion rejected until a complete contract exists
//   - No runtime fallback between Agentic and classic
//
// Do not hand-roll chat/completions or Responses HTTP clients in business
// packages; extend this package instead (eino-no-reinvent P2).
package modelapi
