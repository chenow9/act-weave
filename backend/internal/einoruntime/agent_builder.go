package einoruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// AgentBuildConfig constructs a ChatModelAgent for one agent run (design §3.4).
//
// Locked assembly (PR6):
//   - MaxIterations = 8 (D12)
//   - tool invocation hard-cap = 16 via ToolCallMiddlewares (D12)
//   - ExecuteSequentially = true (D11)
//   - Model is BaseChatModel (true Stream from PR1 / modelapi)
type AgentBuildConfig struct {
	// Name is optional for standalone runs; recommended unique across agents.
	Name string
	// Description is optional for standalone runs.
	Description string
	// Instruction is the system prompt.
	Instruction string
	// Model is required (eino-ext openai ChatModel or test fake).
	Model model.BaseChatModel
	// Tools are InvokableTool instances (e.g. PipelineTool from PR5).
	Tools []tool.BaseTool

	// MaxIterations caps model rounds. Zero → DefaultMaxIterations (8).
	MaxIterations int
	// MaxToolInvocations hard-caps tool InvokableRun calls.
	// Zero → DefaultMaxToolInvocations (16). Values <0 or >16 are rejected.
	MaxToolInvocations int

	// UnknownToolsHandler handles hallucinated tool names. Optional; defaults
	// to a TOOL_UNKNOWN JSON error result (never panics the run).
	UnknownToolsHandler func(ctx context.Context, name, input string) (string, error)

	// ExtraToolMiddlewares are appended after the budget middleware.
	// Used by tests or future audit middleware (design §3.4 auditMiddleware).
	ExtraToolMiddlewares []compose.ToolMiddleware
}

// BuildChatModelAgent constructs adk.ChatModelAgent with D11/D12 assembly.
// Does not run the agent — callers pass the result to Engine.Run / adk.Runner.
func BuildChatModelAgent(ctx context.Context, cfg AgentBuildConfig) (*adk.ChatModelAgent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("einoruntime agent builder: Model is required")
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}
	// Same limit contract as agentic path: 0→16, 1..16; reject negative/>16.
	budgetMW, err := NewToolBudgetMiddleware(cfg.MaxToolInvocations)
	if err != nil {
		return nil, err
	}

	unknown := cfg.UnknownToolsHandler
	if unknown == nil {
		unknown = defaultUnknownToolsHandler
	}

	tools := make([]tool.BaseTool, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if t != nil {
			tools = append(tools, t)
		}
	}

	middlewares := make([]compose.ToolMiddleware, 0, 1+len(cfg.ExtraToolMiddlewares))
	middlewares = append(middlewares, budgetMW)
	middlewares = append(middlewares, cfg.ExtraToolMiddlewares...)

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          strings.TrimSpace(cfg.Name),
		Description:   strings.TrimSpace(cfg.Description),
		Instruction:   cfg.Instruction,
		Model:         cfg.Model,
		MaxIterations: maxIter,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ExecuteSequentially: true, // D11 v1
				UnknownToolsHandler: unknown,
				ToolCallMiddlewares: middlewares,
			},
		},
	})
}

func defaultUnknownToolsHandler(_ context.Context, name, _ string) (string, error) {
	return formatToolErrorResult("UNKNOWN_TOOL", fmt.Sprintf("unknown tool %q", name)), nil
}
