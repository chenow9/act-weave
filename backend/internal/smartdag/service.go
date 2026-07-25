package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid smart dag generation request")

const SchemaVersion = "workflow.graph.v1"

type ToolCatalog interface {
	List(context.Context, string) ([]tool.Tool, error)
}

type WorkflowCreator interface {
	Create(context.Context, workflow.CreateInput) (workflow.Workflow, workflow.Draft, error)
}

type IDGenerator func() (string, error)

type Service struct {
	tools     ToolCatalog
	workflows WorkflowCreator
	nextID    IDGenerator
}

type GenerateRequest struct {
	WorkspaceID string
	Goal        string
	CreatedBy   string
}

type ReasoningStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type MissingCapability struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Reason            string `json:"reason"`
	SuggestedProtocol string `json:"suggestedProtocol"`
}

type NodeExplanation struct {
	NodeID string `json:"nodeId"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type GenerateResult struct {
	Workflow            workflow.Workflow
	Draft               workflow.Draft
	ReasoningSteps      []ReasoningStep
	MissingCapabilities []MissingCapability
	NodeExplanations    []NodeExplanation
	AvailableToolIDs    []string
	SelectedToolIDs     []string
	Reasoning           string
	Confidence          int
}

func NewService(tools ToolCatalog, workflows WorkflowCreator, nextID IDGenerator) (*Service, error) {
	if tools == nil || workflows == nil || nextID == nil {
		return nil, errors.New("smart dag dependencies are required")
	}
	return &Service{tools: tools, workflows: workflows, nextID: nextID}, nil
}

func UUIDv7Generator() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func (s *Service) Generate(ctx context.Context, request GenerateRequest) (GenerateResult, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.Goal = strings.TrimSpace(request.Goal)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	if !validUUID(request.WorkspaceID) || !validUUID(request.CreatedBy) || request.Goal == "" || utf8.RuneCountInString(request.Goal) > 2000 {
		return GenerateResult{}, ErrInvalid
	}

	tools, err := s.tools.List(ctx, request.WorkspaceID)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("list smart dag tools: %w", err)
	}
	available := publishedTools(tools)
	selected := selectTools(request.Goal, available, 3)
	missing := missingCapabilities(request.Goal, available, selected)
	confidence := generationConfidence(len(selected), len(missing))
	capabilityID, err := s.nextID()
	if err != nil {
		return GenerateResult{}, fmt.Errorf("generate smart dag workflow id: %w", err)
	}
	draftID, err := s.nextID()
	if err != nil {
		return GenerateResult{}, fmt.Errorf("generate smart dag draft id: %w", err)
	}
	if !validUUID(capabilityID) || !validUUID(draftID) {
		return GenerateResult{}, errors.New("smart dag id generator returned an invalid UUID")
	}

	graph, explanations := buildGraph(request.Goal, selected, confidence)
	encodedGraph, err := json.Marshal(graph)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("marshal smart dag workflow graph: %w", err)
	}
	createdWorkflow, createdDraft, err := s.workflows.Create(ctx, workflow.CreateInput{
		CapabilityID:  capabilityID,
		DraftID:       draftID,
		WorkspaceID:   request.WorkspaceID,
		Name:          generatedName(request.Goal),
		Slug:          "ai-workflow-" + strings.ReplaceAll(capabilityID[:8], "-", ""),
		Description:   "由智能编排根据业务目标生成：" + request.Goal,
		SchemaVersion: SchemaVersion,
		Graph:         encodedGraph,
		CreatedBy:     request.CreatedBy,
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create smart dag workflow draft: %w", err)
	}

	availableIDs := toolIDs(available)
	selectedIDs := toolIDs(selected)
	return GenerateResult{
		Workflow: createdWorkflow,
		Draft:    createdDraft,
		ReasoningSteps: []ReasoningStep{
			{ID: "goal", Label: "解析业务目标和风险门槛", Status: "COMPLETED", Detail: summarizeGoal(request.Goal)},
			{ID: "catalog", Label: "匹配当前空间可调用能力", Status: "COMPLETED", Detail: fmt.Sprintf("发现 %d 个已发布 Tool，匹配 %d 个", len(available), len(selected))},
			{ID: "structure", Label: "推断可执行节点结构", Status: "COMPLETED", Detail: "生成 Start、能力调用、结果整理与 End 节点"},
			{ID: "graph", Label: "生成画布节点布局与连线", Status: "COMPLETED", Detail: fmt.Sprintf("生成 %d 个节点和 %d 条连线", len(graph.Nodes), len(graph.Edges))},
			{ID: "handoff", Label: "写入正式 Workflow Draft", Status: "COMPLETED", Detail: "草稿已进入 Workflow v1 编译、试运行和发布生命周期"},
		},
		MissingCapabilities: missing,
		NodeExplanations:    explanations,
		AvailableToolIDs:    availableIDs,
		SelectedToolIDs:     selectedIDs,
		Reasoning:           "生成器只使用当前 Workspace 中已有 active release 的 Tool；未匹配能力以缺口返回，不虚构可执行 Tool。",
		Confidence:          confidence,
	}, nil
}

func buildGraph(goal string, selected []tool.Tool, confidence int) (domain.WorkflowGraphDraft, []NodeExplanation) {
	nodes := []domain.WorkflowGraphNode{
		{
			ID: "start", Type: "Start", Label: "接收业务请求", Position: domain.WorkflowPosition{X: 120, Y: 260},
			Ports: []domain.WorkflowGraphPort{{Key: "output", Label: "Output", Direction: "output"}},
			Data: map[string]any{
				"inputSchema":  map[string]any{"type": "object", "properties": map[string]any{}},
				"workflowVars": map[string]any{"businessGoal": goal},
			},
			UI: map[string]any{"generated": true, "reason": "统一定义入口和业务目标上下文。"},
		},
	}
	explanations := []NodeExplanation{{NodeID: "start", Title: "接收业务请求", Reason: "统一定义入口和业务目标上下文。"}}
	for index, value := range selected {
		nodeID := fmt.Sprintf("tool-%d", index+1)
		nodes = append(nodes, domain.WorkflowGraphNode{
			ID: nodeID, Type: "Tool", Label: value.Name, Position: domain.WorkflowPosition{X: 420 + float64(index)*300, Y: 180 + float64(index%2)*160},
			Ports: []domain.WorkflowGraphPort{
				{Key: "input", Label: "Input", Direction: "input"},
				{Key: "output", Label: "Output", Direction: "output"},
			},
			Data: map[string]any{
				"toolId":       value.CapabilityID,
				"inputMapping": map[string]any{},
			},
			UI: map[string]any{"generated": true, "reason": "名称、标识或描述与业务目标匹配，复用已发布能力。"},
		})
		explanations = append(explanations, NodeExplanation{NodeID: nodeID, Title: value.Name, Reason: "匹配当前 Workspace 已发布 Tool，未创建不存在的能力。"})
	}

	transformX := 420 + float64(len(selected))*300
	nodes = append(nodes, domain.WorkflowGraphNode{
		ID: "result", Type: "Transform", Label: "整理执行结果", Position: domain.WorkflowPosition{X: transformX, Y: 260},
		Ports: []domain.WorkflowGraphPort{
			{Key: "input", Label: "Input", Direction: "input"},
			{Key: "output", Label: "Output", Direction: "output"},
		},
		Data: map[string]any{"template": "智能编排已完成：{{workflowVars.businessGoal}}"},
		UI:   map[string]any{"generated": true, "reason": "把执行链路结果收敛为稳定输出。"},
	})
	nodes = append(nodes, domain.WorkflowGraphNode{
		ID: "end", Type: "End", Label: "返回流程结果", Position: domain.WorkflowPosition{X: transformX + 300, Y: 260},
		Ports: []domain.WorkflowGraphPort{{Key: "input", Label: "Input", Direction: "input"}},
		Data:  map[string]any{"output": map[string]any{"kind": "ref", "path": "nodeOutputs.result.result"}},
		UI:    map[string]any{"generated": true, "reason": "形成可审计的流程终点。"},
	})
	explanations = append(explanations,
		NodeExplanation{NodeID: "result", Title: "整理执行结果", Reason: "把执行链路结果收敛为稳定输出。"},
		NodeExplanation{NodeID: "end", Title: "返回流程结果", Reason: "形成可审计的流程终点。"},
	)

	edges := make([]domain.WorkflowGraphEdge, 0, len(nodes)-1)
	previousID := nodes[0].ID
	for index := 1; index < len(nodes); index++ {
		target := nodes[index]
		edges = append(edges, domain.WorkflowGraphEdge{
			ID: fmt.Sprintf("edge-%s-%s", previousID, target.ID), SourceNodeID: previousID, SourcePort: "output",
			TargetNodeID: target.ID, TargetPort: "input", Data: map[string]any{}, UI: map[string]any{"generated": true},
		})
		previousID = target.ID
	}
	return domain.WorkflowGraphDraft{
		SchemaVersion: SchemaVersion, Nodes: nodes, Edges: edges,
		Viewport: domain.CanvasViewport{X: 0, Y: 0, Zoom: 1},
		UI:       map[string]any{"generatedBy": "smart-dag.v1", "businessGoal": goal, "confidence": confidence},
	}, explanations
}

func publishedTools(values []tool.Tool) []tool.Tool {
	available := make([]tool.Tool, 0, len(values))
	for _, value := range values {
		if strings.EqualFold(value.Status, "ACTIVE") && value.ActiveReleaseID != nil {
			available = append(available, value)
		}
	}
	sort.Slice(available, func(i, j int) bool {
		return strings.ToLower(available[i].Name) < strings.ToLower(available[j].Name)
	})
	return available
}

func selectTools(goal string, values []tool.Tool, limit int) []tool.Tool {
	type scoredTool struct {
		value tool.Tool
		score int
	}
	goal = strings.ToLower(goal)
	goalTokens := tokens(goal)
	scored := make([]scoredTool, 0, len(values))
	for _, value := range values {
		text := strings.ToLower(strings.Join([]string{value.Name, value.Slug, value.Description}, " "))
		score := 0
		for _, token := range goalTokens {
			if utf8.RuneCountInString(token) >= 2 && strings.Contains(text, token) {
				score += utf8.RuneCountInString(token)
			}
		}
		for _, candidate := range []string{strings.ToLower(value.Name), strings.ToLower(value.Slug)} {
			if utf8.RuneCountInString(candidate) >= 2 && strings.Contains(goal, candidate) {
				score += 10
			}
		}
		if score > 0 {
			scored = append(scored, scoredTool{value: value, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].value.CapabilityID < scored[j].value.CapabilityID
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	selected := make([]tool.Tool, len(scored))
	for index := range scored {
		selected[index] = scored[index].value
	}
	return selected
}

func tokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
}

func missingCapabilities(goal string, available, selected []tool.Tool) []MissingCapability {
	if len(selected) > 0 {
		return nil
	}
	reason := "当前 Workspace 没有已发布 Tool，生成的 Draft 只包含可试运行的输入、结果整理与输出节点。"
	if len(available) > 0 {
		reason = "当前已发布 Tool 与业务目标没有明确匹配，生成器没有猜测或绑定无关能力。"
	}
	return []MissingCapability{{
		ID: "unmatched-business-capability", Name: summarizeGoal(goal), Reason: reason, SuggestedProtocol: "HTTP_OPENAPI",
	}}
}

func toolIDs(values []tool.Tool) []string {
	ids := make([]string, len(values))
	for index := range values {
		ids[index] = values[index].CapabilityID
	}
	return ids
}

func generatedName(goal string) string {
	return "AI · " + truncateRunes(strings.Join(strings.Fields(goal), " "), 48)
}

func summarizeGoal(goal string) string {
	return truncateRunes(strings.Join(strings.Fields(goal), " "), 80)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func generationConfidence(selected, missing int) int {
	if selected > 0 && missing == 0 {
		return min(98, 88+selected*3)
	}
	return 82
}

func validUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
