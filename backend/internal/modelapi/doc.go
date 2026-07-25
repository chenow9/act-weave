// Package modelapi adapts ACTWEAVE modelconfig + secret credentials to Eino
// ChatModel interfaces (Generate / true Stream / WithTools).
//
// Production agent/smartdag LLM traffic prefers NewEinoOpenAIChatModel
// (official eino-ext OpenAI ChatModel). PlatformChatModel remains a
// hand-rolled Completions client used by unit tests and as a fallback reference.
//
// NewEinoOpenAIChatModel is the production out-bound LLM client boundary:
//   - Agent conversation: chatruntimebridge → eino-ext openai ChatModel (true Stream)
//   - Auxiliary features (e.g. prompt enhance): application promptGenerator →
//     PlatformChatModel.Generate
//
// Do not hand-roll chat/completions HTTP clients in business packages; extend
// this package instead (eino-no-reinvent P2).
package modelapi
