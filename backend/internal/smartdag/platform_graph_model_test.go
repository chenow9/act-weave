package smartdag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/tool"
)

type fakeChatModel struct {
	content string
	err     error
	calls   int
	last    []*schema.AgenticMessage
}

func (m *fakeChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.calls++
	m.last = input
	if m.err != nil {
		return nil, m.err
	}
	return agenticmsg.AssistantText(m.content), nil
}

type memoryModelLookup map[string]modelconfig.Config // key workspaceID/configID

func (m memoryModelLookup) Get(_ context.Context, workspaceID, configID string) (modelconfig.Config, error) {
	cfg, ok := m[workspaceID+"/"+configID]
	if !ok {
		return modelconfig.Config{}, modelconfig.ErrNotFound
	}
	return cfg, nil
}

func TestPlatformChatGraphModelGenerateGraphParsesJSON(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeChatModel{content: string(encoded)}
	models := memoryModelLookup{
		testWorkspaceID + "/" + testModelConfigID: {
			ID: testModelConfigID, WorkspaceID: testWorkspaceID,
			APIBase: "http://example.invalid/v1", ModelName: "test-model", Status: modelconfig.StatusVerified,
		},
	}
	m, err := NewPlatformChatGraphModel(PlatformChatGraphModelDeps{
		Models: models,
		Tools:  publishedToolCatalog(),
		Build: func(context.Context, modelconfig.Config) (ChatModel, error) {
			return fake, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := m.GenerateGraph(context.Background(), GraphModelInput{
		SystemPrompt:   DefaultSystemPrompt(),
		AgentID:        testAgentID,
		WorkspaceID:    testWorkspaceID,
		Message:        "查询支付状态",
		CatalogToolIDs: []string{testToolID},
		ModelConfigID:  testModelConfigID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 model call, got %d", fake.calls)
	}
	if len(fake.last) < 3 {
		t.Fatalf("expected system+context+user messages, got %d", len(fake.last))
	}
	if got.SchemaVersion != SchemaVersion || len(got.Nodes) == 0 {
		t.Fatalf("unexpected graph: %+v", got)
	}
	// Guard still required downstream; candidate should pass with catalog.
	report := GuardGraph(got, GuardOptions{CatalogToolIDs: CatalogToolIDSet([]string{testToolID})})
	if !report.OK {
		t.Fatalf("parsed graph should pass guard: %+v", report)
	}
}

func TestPlatformChatGraphModelParsesFencedJSON(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	encoded, _ := json.Marshal(graph)
	content := "Here is the draft:\n```json\n" + string(encoded) + "\n```\n"
	got, err := ParseGraphFromModelContent(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != len(graph.Nodes) {
		t.Fatalf("nodes=%d want %d", len(got.Nodes), len(graph.Nodes))
	}
}

func TestPlatformChatGraphModelRejectsMissingModel(t *testing.T) {
	t.Parallel()
	m, err := NewPlatformChatGraphModel(PlatformChatGraphModelDeps{
		Models: memoryModelLookup{},
		Build: func(context.Context, modelconfig.Config) (ChatModel, error) {
			return &fakeChatModel{content: "{}"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.GenerateGraph(context.Background(), GraphModelInput{
		WorkspaceID:   testWorkspaceID,
		ModelConfigID: testModelConfigID,
		Message:       "hi",
	})
	if !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("want ErrAgentModelRequired, got %v", err)
	}
}

func TestPlatformChatGraphModelUsesRealAgenticHTTP(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	encoded, _ := json.Marshal(graph)
	var sawPath atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "responses") {
			t.Fatalf("path=%s want responses", r.URL.Path)
		}
		sawPath.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalResponsesJSON(string(encoded))))
	}))
	defer server.Close()

	models := memoryModelLookup{
		testWorkspaceID + "/" + testModelConfigID: {
			ID: testModelConfigID, WorkspaceID: testWorkspaceID,
			APIBase: server.URL + "/v1", ModelName: "gpt-test", Status: modelconfig.StatusVerified,
		},
	}
	m, err := NewPlatformChatGraphModel(PlatformChatGraphModelDeps{
		Models: models,
		Tools: &staticToolCatalog{tools: []tool.Tool{{
			CapabilityID: testToolID, WorkspaceID: testWorkspaceID, Name: "Pay", Status: "ACTIVE",
			ActiveReleaseID: strPtr("rel-1"),
		}}},
		Build: func(_ context.Context, cfg modelconfig.Config) (ChatModel, error) {
			return modelapi.NewOpenAIAgenticModelWithEgress(context.Background(), server.Client(), secretOpenerFunc(func(context.Context, string, string, func([]byte) error) error {
				return nil
			}), cfg, modelapi.LoopbackEgressPolicy())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.GenerateGraph(context.Background(), GraphModelInput{
		SystemPrompt:   DefaultSystemPrompt(),
		AgentID:        testAgentID,
		WorkspaceID:    testWorkspaceID,
		Message:        "查支付",
		CatalogToolIDs: []string{testToolID},
		ModelConfigID:  testModelConfigID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPath.Load() {
		t.Fatal("expected Agentic Responses HTTP call")
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("schema=%s", got.SchemaVersion)
	}
}

func minimalResponsesJSON(text string) string {
	payload := map[string]any{
		"id":     "resp_smartdag_1",
		"object": "response",
		"status": "completed",
		"model":  "gpt-test",
		"output": []map[string]any{
			{
				"type":   "message",
				"id":     "msg_1",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": text, "annotations": []any{}},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

type secretOpenerFunc func(context.Context, string, string, func([]byte) error) error

func (fn secretOpenerFunc) WithActiveSecret(ctx context.Context, workspaceID, secretID string, use func([]byte) error) error {
	return fn(ctx, workspaceID, secretID, use)
}

type staticToolCatalog struct {
	tools []tool.Tool
}

func (s *staticToolCatalog) List(context.Context, string) ([]tool.Tool, error) {
	return s.tools, nil
}

func strPtr(v string) *string { return &v }

func TestParseGraphFromModelContentRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := ParseGraphFromModelContent("   "); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlatformChatGraphModelIncludesHistoryAndFeedback(t *testing.T) {
	t.Parallel()
	graph := validD8Graph(testToolID)
	encoded, _ := json.Marshal(graph)
	fake := &fakeChatModel{content: "```\n" + string(encoded) + "\n```"}
	models := memoryModelLookup{
		testWorkspaceID + "/" + testModelConfigID: {
			ID: testModelConfigID, WorkspaceID: testWorkspaceID,
			APIBase: "http://example.invalid/v1", ModelName: "m", Status: modelconfig.StatusVerified,
		},
	}
	m, err := NewPlatformChatGraphModel(PlatformChatGraphModelDeps{
		Models: models,
		Build:  func(context.Context, modelconfig.Config) (ChatModel, error) { return fake, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	current := validD8Graph(testToolID)
	_, err = m.GenerateGraph(context.Background(), GraphModelInput{
		SystemPrompt:  DefaultSystemPrompt(),
		WorkspaceID:   testWorkspaceID,
		ModelConfigID: testModelConfigID,
		Message:       "加上审批",
		History: []TurnHistoryItem{
			{Role: "user", Content: "先查支付"},
			{Role: "assistant", Content: "已生成草稿"},
		},
		CurrentGraph: &current,
		Feedback: &FailureFeedback{
			Source:     "compile",
			WorkflowID: testWorkflowID,
			Issues:     []FailureIssue{{Code: "X", Message: "mapping missing"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// system + structured + 2 history + user
	if len(fake.last) < 5 {
		t.Fatalf("messages=%d", len(fake.last))
	}
	last := fake.last[len(fake.last)-1]
	if last == nil || len(last.ContentBlocks) == 0 || last.ContentBlocks[0].UserInputText == nil {
		t.Fatalf("last message missing user text: %#v", last)
	}
	text := last.ContentBlocks[0].UserInputText.Text
	if !strings.Contains(text, "mapping missing") && !strings.Contains(text, "compile") {
		if !strings.Contains(text, "加上审批") {
			t.Fatalf("user content missing intent: %s", text)
		}
	}
}
