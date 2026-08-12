package einoruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/modelapi"
)

// ToolSearchMode is the ActWeave run strategy for tool search (D3/D10).
// Provider capability (toolSearchModes:["client"]) is separate and must not be
// mixed into this type.
type ToolSearchMode string

const (
	// ToolSearchModeClientBounded is the only production tool-search mode (D3).
	ToolSearchModeClientBounded ToolSearchMode = "client_bounded"
)

// Agentic agent construction errors.
var (
	// ErrAgenticModelRequired is returned when Model is nil.
	ErrAgenticModelRequired = errors.New("einoruntime agentic builder: Model is required")
	// ErrAgenticClientToolSearchUnverified is returned when tools are present but
	// client-executed tool-search capability was not explicitly verified.
	ErrAgenticClientToolSearchUnverified = errors.New("einoruntime agentic builder: client tool-search capability not verified")
	// ErrAgenticToolSearchMode is returned for empty/unsupported tool-search modes when tools exist.
	ErrAgenticToolSearchMode = errors.New("einoruntime agentic builder: tool search mode must be client_bounded when tools are present")
	// ErrAgenticMaxIterations is returned when MaxIterations is not 0 (normalize to 8) or 8.
	ErrAgenticMaxIterations = errors.New("einoruntime agentic builder: MaxIterations must be 8")
	// ErrAgenticTooManyImmediate is returned when immediate tools exceed the platform ceiling.
	ErrAgenticTooManyImmediate = errors.New("einoruntime agentic builder: too many immediate tools")
	// ErrAgenticPromptCacheKeyRequired is returned when PromptCacheKey is empty.
	ErrAgenticPromptCacheKeyRequired = errors.New("einoruntime agentic builder: PromptCacheKey is required")
	// ErrAgenticBusinessToolImmediate is returned when a non-platform business tool is marked immediate.
	ErrAgenticBusinessToolImmediate = errors.New("einoruntime agentic builder: business tools must be deferred")
	// ErrAgenticCatalogRequired is returned when tools are present but catalog is nil.
	ErrAgenticCatalogRequired = errors.New("einoruntime agentic builder: Catalog is required when tools are present")
	// ErrAgenticNilTool is returned when cfg.Tools contains a nil element.
	// Nil tools are never silently filtered; membership must be fail-closed.
	ErrAgenticNilTool = errors.New("einoruntime agentic builder: Tools must not contain nil")
)

// AgenticAgentBuildConfig constructs a TypedChatModelAgent[*schema.AgenticMessage]
// for the production Agentic path (design §6.2). All new production-target APIs
// are Agentic-only — not a dual-stack abstraction.
//
// Handler / middleware order (documented, preserve when extending):
//
//  1. ToolsNode.ToolCallMiddlewares:
//     a. budget middleware (Invokable + Streamable + EnhancedInvokable +
//     EnhancedStreamable, cap 16; includes search; shared run-local counter)
//     b. ExtraToolMiddlewares (approval / audit / tests), in caller order
//  2. Handlers (interface middleware), registration order = outer→inner for WrapModel;
//     BeforeAgent runs first→last so the LAST handler wins for ToolSearchTool:
//     a. ExtraHandlers (caller-supplied), if any
//     b. promptCacheKeyMiddleware — forces deterministic prompt_cache_key on every model call
//     c. BoundedClientToolSearchMiddleware — FINAL tool-search configuration authority
//
// Sequential tool execution is always enabled. MaxIterations is fixed at 8 for
// the hard 8×5=40 definition proof. No classic model retry/failover.
type AgenticAgentBuildConfig struct {
	// Name is optional for standalone runs; recommended unique across agents.
	Name string
	// Description is optional for standalone runs.
	Description string
	// Instruction is the system prompt.
	Instruction string
	// Model is required (model.AgenticModel — e.g. modelapi.NewOpenAIAgenticModel).
	Model model.AgenticModel
	// Tools are executable InvokableTool / BaseTool instances (e.g. PipelineTool).
	// Must match Catalog one-to-one (name, count, schema). Nil elements are rejected
	// (never silently filtered). Empty/nil Tools is valid only with a nil or truly
	// empty catalog (tool-less path — no native tool_search middleware).
	Tools []tool.BaseTool
	// Catalog is the frozen capability snapshot. Required whenever Tools is non-empty
	// or the catalog itself is non-empty. A truly empty catalog (Len()==0) with no
	// tools is the tool-less path. Non-empty catalogs always fail closed on any
	// tools↔catalog mismatch (nil Tools, empty, missing, extra, duplicate).
	Catalog *ToolCatalogSnapshot

	// MaxIterations caps model rounds. Zero normalizes to DefaultMaxIterations (8).
	// Any value other than 0 or 8 is rejected (no larger hidden bound).
	MaxIterations int
	// MaxToolInvocations hard-caps tool executions across Invokable, Streamable,
	// EnhancedInvokable, and EnhancedStreamable paths.
	// Zero → DefaultMaxToolInvocations (16). Includes the search executor.
	// Values <0 or >16 are rejected with ErrToolBudgetMaxInvalid.
	MaxToolInvocations int

	// ToolSearchMode must be client_bounded when tools are present (D3).
	ToolSearchMode ToolSearchMode
	// ClientToolSearchVerified must be true when tools are present. Fail closed
	// with no runtime fallback when unverified (D2/D3).
	ClientToolSearchVerified bool

	// PromptCacheKey is a non-empty deterministic platform key applied on every
	// model Generate/Stream call. Ordinary caller options cannot override it.
	// The key is also attached as a protected context value so the HTTP transport
	// can force-set prompt_cache_key after ExtraFields JSON-set.
	PromptCacheKey string

	// UnknownToolsHandler handles hallucinated tool names. Optional; defaults
	// to a TOOL_UNKNOWN JSON error result (never panics the run).
	UnknownToolsHandler func(ctx context.Context, name, input string) (string, error)

	// ExtraToolMiddlewares are appended after the budget middleware
	// (approval / audit / tests).
	ExtraToolMiddlewares []compose.ToolMiddleware

	// ExtraHandlers are registered before the platform prompt-cache and
	// bounded tool-search handlers (which remain last for tool-search authority).
	ExtraHandlers []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
}

// BuildAgenticAgent constructs adk.TypedChatModelAgent[*schema.AgenticMessage]
// with D3/D4/D6/D12 assembly. Does not run the agent — callers pass the result
// to AgenticEngine.Run / adk.TypedRunner.
//
// This is the production-target builder. Legacy BuildChatModelAgent remains for
// un-migrated callers until later tasks remove the classic path.
func BuildAgenticAgent(ctx context.Context, cfg AgenticAgentBuildConfig) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	if cfg.Model == nil {
		return nil, ErrAgenticModelRequired
	}

	promptKey := strings.TrimSpace(cfg.PromptCacheKey)
	if promptKey == "" {
		return nil, ErrAgenticPromptCacheKeyRequired
	}

	// Fail closed on nil tool slots — never silently filter.
	for i, t := range cfg.Tools {
		if t == nil {
			return nil, fmt.Errorf("%w: Tools[%d]", ErrAgenticNilTool, i)
		}
	}
	// Defensive copy; membership is authoritative as provided (no nil drop).
	tools := make([]tool.BaseTool, len(cfg.Tools))
	copy(tools, cfg.Tools)
	hasTools := len(tools) > 0
	catalogNonEmpty := cfg.Catalog != nil && cfg.Catalog.Len() > 0

	// MaxIterations: 0 → 8; only 8 accepted otherwise.
	maxIter := cfg.MaxIterations
	if maxIter == 0 {
		maxIter = DefaultMaxIterations
	}
	if maxIter != DefaultMaxIterations {
		return nil, fmt.Errorf("%w: got %d", ErrAgenticMaxIterations, cfg.MaxIterations)
	}

	// Budget middleware validates limit (0→16, 1..16; reject negative/>16).
	budgetMW, err := NewToolBudgetMiddleware(cfg.MaxToolInvocations)
	if err != nil {
		return nil, err
	}

	unknown := cfg.UnknownToolsHandler
	if unknown == nil {
		unknown = defaultUnknownToolsHandler
	}

	var handlers []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
	handlers = append(handlers, cfg.ExtraHandlers...)

	// Prompt-cache key middleware on every model call (before bounded search so
	// search remains the final tool-search handler).
	handlers = append(handlers, newPromptCacheKeyMiddleware(promptKey))

	// Tool-search path when there is any executable tool OR a non-empty catalog.
	// Truly empty catalog (nil or Len()==0) with no tools is the tool-less path:
	// no ClientToolSearchVerified / mode / middleware required.
	// A non-empty catalog with Tools=nil/empty always fails ValidateExecutableTools.
	if hasTools || catalogNonEmpty {
		if !cfg.ClientToolSearchVerified {
			return nil, ErrAgenticClientToolSearchUnverified
		}
		mode := ToolSearchMode(strings.TrimSpace(string(cfg.ToolSearchMode)))
		if mode != ToolSearchModeClientBounded {
			return nil, fmt.Errorf("%w: got %q", ErrAgenticToolSearchMode, cfg.ToolSearchMode)
		}
		if cfg.Catalog == nil {
			return nil, ErrAgenticCatalogRequired
		}
		// One-to-one tools ↔ catalog (name, count, schema digest, overall digest).
		// Rejects missing/extra/duplicate executables and non-empty catalog with
		// empty tools. Empty catalog + empty tools reaches the tool-less path above.
		if err := cfg.Catalog.ValidateExecutableTools(ctx, tools); err != nil {
			return nil, err
		}
		// Immediate tools must be explicit platform-control; business kinds fail.
		// Catalog construction already rejects immediate without PlatformControl;
		// re-check frozen entries for defense in depth and map to builder error.
		for _, e := range cfg.Catalog.entries {
			if e.Exposure == ToolExposureImmediate && !e.PlatformControl {
				return nil, fmt.Errorf("%w: %q kind %q", ErrAgenticBusinessToolImmediate, e.Name, e.Kind)
			}
		}
		if n := cfg.Catalog.ImmediateCount(); n > MaxImmediatePlatformTools {
			return nil, fmt.Errorf("%w: %d > %d", ErrAgenticTooManyImmediate, n, MaxImmediatePlatformTools)
		}
		// Collision with search tool name is already rejected in BuildToolCatalog;
		// re-check for defense in depth.
		if cfg.Catalog.hasName(ClientToolSearchToolName) {
			return nil, fmt.Errorf("%w: %q", ErrToolCatalogSearchNameCollision, ClientToolSearchToolName)
		}

		bounded, err := NewBoundedClientToolSearchMiddleware(cfg.Catalog)
		if err != nil {
			return nil, err
		}
		// Final tool-search configuration handler — later handlers must not overwrite.
		handlers = append(handlers, bounded)
	}

	middlewares := make([]compose.ToolMiddleware, 0, 1+len(cfg.ExtraToolMiddlewares))
	middlewares = append(middlewares, budgetMW)
	middlewares = append(middlewares, cfg.ExtraToolMiddlewares...)

	// Production structural invariant: at most one executable action per model
	// turn (function call and/or native client tool-search). ParallelToolCalls=false
	// and ExecuteSequentially=true remain defense-in-depth; this guard fails closed
	// before ToolsNode even when a non-platform AgenticModel ignores them.
	guardedModel := wrapSingleActionAgenticModel(cfg.Model)

	return adk.NewTypedChatModelAgent(ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:          strings.TrimSpace(cfg.Name),
		Description:   strings.TrimSpace(cfg.Description),
		Instruction:   cfg.Instruction,
		Model:         guardedModel,
		MaxIterations: maxIter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ExecuteSequentially: true, // D11 v1 — sequential tool execution
				UnknownToolsHandler: unknown,
				ToolCallMiddlewares: middlewares,
			},
		},
		Handlers: handlers,
		// Explicitly nil: no model retry / failover to classic or full-tools mode.
		ModelRetryConfig:    nil,
		ModelFailoverConfig: nil,
	})
}

// promptCacheKeyMiddleware forces a non-empty platform prompt_cache_key on every
// model Generate/Stream via WrapModel. Options are appended after caller options
// so GetImplSpecificOptions leaves the platform key as the effective value, and
// the protected context value is force-set so the HTTP transport can overwrite
// ExtraFields JSON-set of prompt_cache_key.
type promptCacheKeyMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	key string
}

func newPromptCacheKeyMiddleware(key string) *promptCacheKeyMiddleware {
	return &promptCacheKeyMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		key:                               key,
	}
}

func (m *promptCacheKeyMiddleware) WrapModel(
	_ context.Context,
	mdl model.BaseModel[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (model.BaseModel[*schema.AgenticMessage], error) {
	return &forcedPromptCacheModel{inner: mdl, key: m.key}, nil
}

// forcedPromptCacheModel appends modelapi.WithPromptCacheKey after all caller
// options on every Generate/Stream and overwrites the protected context value
// with the platform key. It does not use previous_response_id, Store, or auto cache.
type forcedPromptCacheModel struct {
	inner model.BaseModel[*schema.AgenticMessage]
	key   string
}

func (m *forcedPromptCacheModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	ctx = modelapi.WithProtectedPromptCacheKey(ctx, m.key)
	return m.inner.Generate(ctx, input, appendPlatformPromptCacheKey(opts, m.key)...)
}

func (m *forcedPromptCacheModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	ctx = modelapi.WithProtectedPromptCacheKey(ctx, m.key)
	return m.inner.Stream(ctx, input, appendPlatformPromptCacheKey(opts, m.key)...)
}

func appendPlatformPromptCacheKey(opts []model.Option, key string) []model.Option {
	out := make([]model.Option, 0, len(opts)+1)
	out = append(out, opts...)
	// Last wins for impl-specific promptCacheKey pointer field.
	out = append(out, modelapi.WithPromptCacheKey(key))
	return out
}
