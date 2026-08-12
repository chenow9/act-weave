// Package modelapi adapts ACTWEAVE modelconfig + secret credentials to Eino
// AgenticModel (OpenAI Responses).
//
// Production path:
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
//
// Reference / tests only:
//   - PlatformChatModel → hand-rolled Completions client (not wired in production)
//
// Classic NewEinoOpenAIChatModel / eino-ext openai Chat Completions was removed
// in Task 9 (PR-09). Do not reintroduce a production Chat Completions adapter.
//
// Do not hand-roll chat/completions or Responses HTTP clients in business
// packages; extend this package instead (eino-no-reinvent P2).
package modelapi
