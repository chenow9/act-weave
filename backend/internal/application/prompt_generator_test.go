package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/modelconfig"
)

type stubModelConfigReader struct {
	cfg modelconfig.Config
	err error
}

func (s stubModelConfigReader) Get(context.Context, string, string) (modelconfig.Config, error) {
	return s.cfg, s.err
}

type stubSecretOpener struct{}

func (stubSecretOpener) WithActiveSecret(
	context.Context, string, string, func([]byte) error,
) error {
	return nil
}

func minimalPromptResponsesJSON(text string) string {
	payload := map[string]any{
		"id": "resp_prompt_1", "object": "response", "status": "completed", "model": "prompt-model",
		"output": []map[string]any{{
			"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// Drives promptGenerator.Generate through NewOpenAIAgenticModel (Responses),
// proving auxiliary LLM no longer uses classic Chat Completions (§7.5).
func TestPromptGeneratorGenerateUsesAgenticModel(t *testing.T) {
	t.Parallel()

	var sawPath string
	var sawBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalPromptResponsesJSON("  Improved system prompt.  ")))
	}))
	t.Cleanup(server.Close)

	gen := &promptGenerator{
		models: stubModelConfigReader{cfg: modelconfig.Config{
			WorkspaceID: "ws-prompt",
			APIBase:     server.URL + "/v1",
			ModelName:   "prompt-model",
		}},
		secrets: stubSecretOpener{},
		client:  server.Client(),
	}
	out, err := gen.Generate(context.Background(), agent.PromptGenerationRequest{
		Agent: agent.Agent{WorkspaceID: "ws-prompt", ModelConfigID: "mc-1"},
		Input: "Make this clearer",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != "Improved system prompt." {
		t.Fatalf("output=%q", out)
	}
	if !strings.Contains(sawPath, "responses") {
		t.Fatalf("Agentic path = %q, want .../responses", sawPath)
	}
	if sawBody["model"] != "prompt-model" {
		t.Fatalf("model field = %#v", sawBody["model"])
	}
	if stream, ok := sawBody["stream"].(bool); ok && stream {
		t.Fatal("prompt enhance must use non-stream Generate")
	}
}

func TestPromptGeneratorGenerateRejectsEmptyModelContent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalPromptResponsesJSON("   ")))
	}))
	t.Cleanup(server.Close)

	gen := &promptGenerator{
		models: stubModelConfigReader{cfg: modelconfig.Config{
			WorkspaceID: "ws", APIBase: server.URL + "/v1", ModelName: "m",
		}},
		secrets: stubSecretOpener{},
		client:  server.Client(),
	}
	_, err := gen.Generate(context.Background(), agent.PromptGenerationRequest{
		Agent: agent.Agent{WorkspaceID: "ws", ModelConfigID: "mc"},
	})
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("err=%v, want no content", err)
	}
}

func TestPromptGeneratorLLMHTTPClientRejectsShortSharedTimeout(t *testing.T) {
	t.Parallel()
	gen := &promptGenerator{client: &http.Client{Timeout: 15 * time.Second}}
	got := gen.llmHTTPClient()
	if got == nil {
		t.Fatal("expected fallback client")
	}
	if got.Timeout != 0 {
		t.Fatalf("want stream-safe Timeout=0, got %v", got.Timeout)
	}
	// Explicit long/zero clients are preserved.
	long := &http.Client{Timeout: promptGenerationTimeout}
	gen.client = long
	if gen.llmHTTPClient() != long {
		t.Fatal("expected long-timeout client to be reused")
	}
}
