package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/domain"
	"actweave/backend/internal/modelconfig"
)

// ChatModel is the minimal no-tools Generate surface used by PlatformChatGraphModel.
// Production wires model.AgenticModel (Responses); test doubles satisfy this interface.
type ChatModel interface {
	Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error)
}

// ChatModelBuilder constructs a ChatModel for an Agent-bound modelConfig (D2/D3).
// Production wires modelapi.NewOpenAIAgenticModel; tests inject fakes.
type ChatModelBuilder func(ctx context.Context, cfg modelconfig.Config) (ChatModel, error)

// PlatformChatGraphModel adapts Agent modelConfig → AgenticModel → workflow.graph.v1.
// It never writes Drafts; TurnService always runs GuardGraph before persist (D3).
// No tools / deferred tools / tool_search are attached (§7.5).
type PlatformChatGraphModel struct {
	models ModelConfigLookup
	tools  ToolCatalog
	build  ChatModelBuilder
}

// PlatformChatGraphModelDeps wires LLM graph generation.
type PlatformChatGraphModelDeps struct {
	Models ModelConfigLookup
	// Tools is optional; when set, published catalog is included in structured context.
	Tools ToolCatalog
	Build ChatModelBuilder
}

// NewPlatformChatGraphModel constructs the production GraphModel (D2/D3).
func NewPlatformChatGraphModel(deps PlatformChatGraphModelDeps) (*PlatformChatGraphModel, error) {
	if deps.Models == nil {
		return nil, errors.New("platform chat graph model requires model config lookup")
	}
	if deps.Build == nil {
		return nil, errors.New("platform chat graph model requires chat model builder")
	}
	return &PlatformChatGraphModel{
		models: deps.Models,
		tools:  deps.Tools,
		build:  deps.Build,
	}, nil
}

// GenerateGraph implements GraphModel via AgenticModel (no rules fallback).
func (m *PlatformChatGraphModel) GenerateGraph(ctx context.Context, input GraphModelInput) (domain.WorkflowGraphDraft, error) {
	if m == nil || m.models == nil || m.build == nil {
		return domain.WorkflowGraphDraft{}, errors.New("platform chat graph model is not configured")
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	modelConfigID := strings.TrimSpace(input.ModelConfigID)
	if workspaceID == "" || modelConfigID == "" {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("%w: modelConfigId missing on generate path", ErrAgentModelRequired)
	}

	cfg, err := m.models.Get(ctx, workspaceID, modelConfigID)
	if err != nil {
		if errors.Is(err, modelconfig.ErrNotFound) {
			return domain.WorkflowGraphDraft{}, ErrAgentModelRequired
		}
		return domain.WorkflowGraphDraft{}, fmt.Errorf("load model config for graph generation: %w", err)
	}
	if !modelConfigUsable(cfg) {
		return domain.WorkflowGraphDraft{}, ErrAgentModelRequired
	}

	chatModel, err := m.build(ctx, cfg)
	if err != nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("build platform chat model: %w", err)
	}
	if chatModel == nil {
		return domain.WorkflowGraphDraft{}, errors.New("chat model builder returned nil")
	}

	messages, err := m.buildMessages(ctx, input)
	if err != nil {
		return domain.WorkflowGraphDraft{}, err
	}

	msg, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("platform chat model generate: %w", err)
	}
	text, err := agenticmsg.ExtractAssistantText(msg)
	if err != nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("platform chat model returned no assistant text: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return domain.WorkflowGraphDraft{}, errors.New("platform chat model returned empty content")
	}

	graph, err := ParseGraphFromModelContent(text)
	if err != nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("parse model graph JSON: %w", err)
	}
	return graph, nil
}

func (m *PlatformChatGraphModel) buildMessages(ctx context.Context, input GraphModelInput) ([]*schema.AgenticMessage, error) {
	systemContent := strings.TrimSpace(input.SystemPrompt.Content)
	if systemContent == "" {
		systemContent = DefaultSystemPrompt().Content
	}

	structured, err := m.structuredContext(ctx, input)
	if err != nil {
		return nil, err
	}

	messages := []*schema.AgenticMessage{
		agenticmsg.System(systemContent),
		agenticmsg.System(structured),
	}

	// Prior turns (oldest first); cap to avoid unbounded context.
	const maxHistory = 12
	history := input.History
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	for _, item := range history {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch role {
		case "assistant":
			messages = append(messages, agenticmsg.AssistantText(content))
		default:
			messages = append(messages, agenticmsg.UserText(content))
		}
	}

	userContent := strings.TrimSpace(input.Message)
	if input.Feedback != nil {
		if fb := strings.TrimSpace(input.Feedback.ContextForModel()); fb != "" {
			userContent = userContent + "\n\n" + fb
		}
	}
	if userContent == "" {
		return nil, ErrInvalid
	}
	messages = append(messages, agenticmsg.UserText(userContent))
	return messages, nil
}

func (m *PlatformChatGraphModel) structuredContext(ctx context.Context, input GraphModelInput) (string, error) {
	type toolSummary struct {
		ID          string `json:"id"`
		Name        string `json:"name,omitempty"`
		Slug        string `json:"slug,omitempty"`
		Description string `json:"description,omitempty"`
	}
	catalog := make([]toolSummary, 0, len(input.CatalogToolIDs))
	if m.tools != nil && strings.TrimSpace(input.WorkspaceID) != "" {
		tools, err := m.tools.List(ctx, input.WorkspaceID)
		if err != nil {
			return "", fmt.Errorf("list tools for structured context: %w", err)
		}
		allowed := CatalogToolIDSet(input.CatalogToolIDs)
		for _, value := range publishedTools(tools) {
			if len(allowed) > 0 {
				if _, ok := allowed[value.CapabilityID]; !ok {
					continue
				}
			}
			catalog = append(catalog, toolSummary{
				ID:          value.CapabilityID,
				Name:        value.Name,
				Slug:        value.Slug,
				Description: truncateRunes(value.Description, 160),
			})
		}
	} else {
		for _, id := range input.CatalogToolIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			catalog = append(catalog, toolSummary{ID: id})
		}
	}

	payload := map[string]any{
		"outputContract": map[string]any{
			"schemaVersion":    SchemaVersion,
			"format":           "json",
			"instruction":      "Respond with a single workflow.graph.v1 JSON object only. Do not wrap in markdown unless necessary.",
			"allowedNodeTypes": []string{"Start", "Tool", "Transform", "Condition", "Approval", "End"},
			"maxNodes":         DefaultMaxNodes,
			"toolIdRule":       "Tool nodes must set data.toolId to a catalog id exactly; never invent ids.",
			"edgeRule":         "Every edge MUST set sourceNodeId and targetNodeId to existing node ids (not empty). Condition needs ≥2 out-edges with data.branch.",
			"dagRule":          "Graph MUST be a directed acyclic graph (DAG). No cycles, no self-loops, no Condition→earlier Tool poll/retry back-edges (e.g. running→get_progress). Async: check status once, completed→next, default/failed→End.",
			"layoutRule":       "Assign distinct position.x/y so nodes do not stack at 0,0; prefer left-to-right columns.",
		},
		"agent": map[string]any{
			"id":            input.AgentID,
			"modelConfigId": input.ModelConfigID,
		},
		"workspaceId":    input.WorkspaceID,
		"publishedTools": catalog,
	}
	if input.CurrentGraph != nil {
		payload["currentGraph"] = input.CurrentGraph
	} else {
		payload["currentGraph"] = nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal structured context: %w", err)
	}
	return "Structured generation context (JSON):\n" + string(encoded), nil
}

// ParseGraphFromModelContent extracts workflow.graph.v1 from model text
// (raw JSON or fenced ```json blocks).
func ParseGraphFromModelContent(content string) (domain.WorkflowGraphDraft, error) {
	raw := strings.TrimSpace(content)
	if raw == "" {
		return domain.WorkflowGraphDraft{}, errors.New("empty model content")
	}
	candidate := extractJSONObject(raw)
	if candidate == "" {
		return domain.WorkflowGraphDraft{}, errors.New("no JSON object found in model content")
	}

	var graph domain.WorkflowGraphDraft
	if err := json.Unmarshal([]byte(candidate), &graph); err != nil {
		// Some models wrap the graph under a "graph" key.
		var wrapper struct {
			Graph         *domain.WorkflowGraphDraft `json:"graph"`
			WorkflowGraph *domain.WorkflowGraphDraft `json:"workflowGraph"`
			SchemaVersion string                     `json:"schemaVersion"`
			Nodes         []domain.WorkflowGraphNode `json:"nodes"`
			Edges         []domain.WorkflowGraphEdge `json:"edges"`
		}
		if wrapErr := json.Unmarshal([]byte(candidate), &wrapper); wrapErr != nil {
			return domain.WorkflowGraphDraft{}, err
		}
		switch {
		case wrapper.Graph != nil:
			graph = *wrapper.Graph
		case wrapper.WorkflowGraph != nil:
			graph = *wrapper.WorkflowGraph
		case len(wrapper.Nodes) > 0:
			graph = domain.WorkflowGraphDraft{
				SchemaVersion: wrapper.SchemaVersion,
				Nodes:         wrapper.Nodes,
				Edges:         wrapper.Edges,
			}
		default:
			return domain.WorkflowGraphDraft{}, err
		}
	}

	// Recover edges that used alternate keys (source/target/from/to) lost by strict struct tags.
	if edges := recoverEdgesFromRawJSON(candidate); len(edges) > 0 {
		// Prefer recovered edges when strict unmarshal produced empty endpoints.
		emptyStrict := true
		for _, e := range graph.Edges {
			if strings.TrimSpace(e.SourceNodeID) != "" && strings.TrimSpace(e.TargetNodeID) != "" {
				emptyStrict = false
				break
			}
		}
		if emptyStrict || len(graph.Edges) == 0 {
			graph.Edges = edges
		}
	}

	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = SchemaVersion
	}
	graph = NormalizeCandidateGraph(graph)
	return graph, nil
}

// recoverEdgesFromRawJSON parses edges as generic maps so LLM aliases are retained.
func recoverEdgesFromRawJSON(candidate string) []domain.WorkflowGraphEdge {
	var root map[string]any
	if err := json.Unmarshal([]byte(candidate), &root); err != nil {
		return nil
	}
	// Unwrap graph / workflowGraph containers.
	for _, key := range []string{"graph", "workflowGraph"} {
		if nested, ok := root[key].(map[string]any); ok {
			root = nested
			break
		}
	}
	rawEdges, ok := root["edges"].([]any)
	if !ok || len(rawEdges) == 0 {
		return nil
	}
	maps := make([]map[string]any, 0, len(rawEdges))
	for _, item := range rawEdges {
		if m, ok := item.(map[string]any); ok {
			maps = append(maps, m)
		}
	}
	if len(maps) == 0 {
		return nil
	}
	return NormalizeEdgesFromRawMaps(maps)
}

func extractJSONObject(content string) string {
	// Prefer fenced code block.
	lower := strings.ToLower(content)
	if idx := strings.Index(lower, "```json"); idx >= 0 {
		rest := content[idx+7:]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	if idx := strings.Index(content, "```"); idx >= 0 {
		rest := content[idx+3:]
		// skip optional language tag
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// First top-level object braces.
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start >= 0 && end > start {
		return strings.TrimSpace(content[start : end+1])
	}
	return ""
}
