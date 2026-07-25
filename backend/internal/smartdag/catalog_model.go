package smartdag

import (
	"context"
	"fmt"
	"strings"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/tool"
)

// CatalogGraphModel builds graphs from the published tool catalog + user message.
// Offline / test helper only — production wires PlatformChatGraphModel (D2/D3).
// Output still goes through GuardGraph before Draft persist when used in tests.
type CatalogGraphModel struct {
	tools ToolCatalog
}

// NewCatalogGraphModel constructs a catalog-driven graph model.
func NewCatalogGraphModel(tools ToolCatalog) (*CatalogGraphModel, error) {
	if tools == nil {
		return nil, fmt.Errorf("tool catalog is required")
	}
	return &CatalogGraphModel{tools: tools}, nil
}

// GenerateGraph implements GraphModel using catalog tool selection (multi-turn aware).
func (m *CatalogGraphModel) GenerateGraph(ctx context.Context, input GraphModelInput) (domain.WorkflowGraphDraft, error) {
	if m == nil || m.tools == nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("catalog graph model is not configured")
	}
	tools, err := m.tools.List(ctx, input.WorkspaceID)
	if err != nil {
		return domain.WorkflowGraphDraft{}, fmt.Errorf("list tools for catalog model: %w", err)
	}
	available := publishedTools(tools)
	// Prefer catalog IDs supplied by turn service when present.
	if len(input.CatalogToolIDs) > 0 {
		allowed := CatalogToolIDSet(input.CatalogToolIDs)
		filtered := make([]tool.Tool, 0, len(available))
		for _, value := range available {
			if _, ok := allowed[value.CapabilityID]; ok {
				filtered = append(filtered, value)
			}
		}
		available = filtered
	}

	goal := strings.TrimSpace(input.Message)
	if goal == "" {
		goal = "智能编排流程"
	}
	// Incorporate recent history into scoring text (D15 context).
	for _, item := range input.History {
		if strings.EqualFold(item.Role, "user") && strings.TrimSpace(item.Content) != "" {
			goal = goal + " " + item.Content
		}
	}
	// Failure feedback revise context (D14): fold issues into selection text.
	if input.Feedback != nil {
		if ctxText := strings.TrimSpace(input.Feedback.ContextForModel()); ctxText != "" {
			goal = goal + " " + ctxText
		}
	}

	selected := selectTools(goal, available, 3)
	confidence := generationConfidence(len(selected), 0)
	if len(selected) == 0 && len(available) > 0 {
		// Keep graph executable shape with transform only when no match.
		confidence = 82
	}
	graph, _ := buildGraph(input.Message, selected, confidence)

	// Multi-turn revision: if current graph exists and user asks for approval, insert Approval.
	if input.CurrentGraph != nil && wantsApproval(input.Message) {
		graph = insertApprovalNode(*input.CurrentGraph)
	} else if input.CurrentGraph != nil && input.Feedback != nil {
		// Feedback revise (D14): keep prior topology as base; ApplyTurn stamps revisedFrom + draftVersion.
		// Real LLM adapters use Feedback for deeper edits; this catalog model stays conservative.
		base := *input.CurrentGraph
		if base.SchemaVersion == "" {
			base.SchemaVersion = SchemaVersion
		}
		if base.UI == nil {
			base.UI = map[string]any{}
		}
		base.UI["businessGoal"] = input.Message
		return base, nil
	} else if input.CurrentGraph != nil && len(selected) == 0 {
		// No new tool match: start from current graph as base for minor revisions.
		base := *input.CurrentGraph
		if base.SchemaVersion == "" {
			base.SchemaVersion = SchemaVersion
		}
		if base.UI == nil {
			base.UI = map[string]any{}
		}
		base.UI["businessGoal"] = input.Message
		return base, nil
	}

	if graph.UI == nil {
		graph.UI = map[string]any{}
	}
	graph.UI["businessGoal"] = input.Message
	return graph, nil
}

func wantsApproval(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(message, "审批") ||
		strings.Contains(lower, "approval") ||
		strings.Contains(message, "审核")
}

func insertApprovalNode(base domain.WorkflowGraphDraft) domain.WorkflowGraphDraft {
	graph := base
	// Avoid duplicate approval nodes.
	for _, node := range graph.Nodes {
		if strings.EqualFold(node.Type, "Approval") {
			return graph
		}
	}
	// Insert Approval before End if present.
	endIdx := -1
	for i, node := range graph.Nodes {
		if strings.EqualFold(node.Type, "End") {
			endIdx = i
			break
		}
	}
	approval := domain.WorkflowGraphNode{
		ID:    "approval-1",
		Type:  "Approval",
		Label: "人工审批",
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Label: "Input", Direction: "input"},
			{Key: "output", Label: "Output", Direction: "output"},
		},
		Data: map[string]any{},
		UI:   map[string]any{"generated": true, "reason": "根据多轮意图插入审批节点。"},
	}
	if endIdx < 0 {
		graph.Nodes = append(graph.Nodes, approval)
		return graph
	}
	// Find node currently feeding End and rewire via approval.
	endID := graph.Nodes[endIdx].ID
	nodes := make([]domain.WorkflowGraphNode, 0, len(graph.Nodes)+1)
	nodes = append(nodes, graph.Nodes[:endIdx]...)
	nodes = append(nodes, approval)
	nodes = append(nodes, graph.Nodes[endIdx:]...)
	graph.Nodes = nodes

	newEdges := make([]domain.WorkflowGraphEdge, 0, len(graph.Edges)+1)
	rewired := false
	for _, edge := range graph.Edges {
		if edge.TargetNodeID == endID {
			// source -> approval
			newEdges = append(newEdges, domain.WorkflowGraphEdge{
				ID:           edge.ID + "-approval",
				SourceNodeID: edge.SourceNodeID,
				SourcePort:   edge.SourcePort,
				TargetNodeID: approval.ID,
				TargetPort:   "input",
				Data:         map[string]any{},
				UI:           map[string]any{"generated": true},
			})
			rewired = true
			continue
		}
		newEdges = append(newEdges, edge)
	}
	newEdges = append(newEdges, domain.WorkflowGraphEdge{
		ID: "edge-approval-end", SourceNodeID: approval.ID, SourcePort: "output",
		TargetNodeID: endID, TargetPort: "input",
		Data: map[string]any{}, UI: map[string]any{"generated": true},
	})
	if !rewired && len(graph.Nodes) > 1 {
		// Fallback linear edge from previous last non-end node.
	}
	graph.Edges = newEdges
	if graph.UI == nil {
		graph.UI = map[string]any{}
	}
	return graph
}
