package debugtests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

/*
Live debug usage:

	cd backend
	ACTWEAVE_DEBUG_RUN=1 \
	ACTWEAVE_DEBUG_AGENT_ID=327743349161201665 \
	go test ./debugtests -run TestDebugEnhanceSystemPromptLive -v

Optional variables:
	ACTWEAVE_DEBUG_BASE_URL=http://127.0.0.1:8082/api
	ACTWEAVE_DEBUG_USERNAME=chen.ops
	ACTWEAVE_DEBUG_PASSWORD=actweave-demo
	ACTWEAVE_DEBUG_TIMEOUT_SECONDS=190

To debug the upstream model API directly:
	ACTWEAVE_DEBUG_RUN=1 \
	ACTWEAVE_DEBUG_AGENT_ID=327743349161201665 \
	go test ./debugtests -run TestDebugDirectModelChatCompletion -v

If the stored model config field is masked, override it manually:
	ACTWEAVE_DEBUG_MODEL_API_KEY=sk-xxx
	ACTWEAVE_DEBUG_MODEL_API_BASE=http://192.168.20.4:7080/v1
	ACTWEAVE_DEBUG_MODEL_NAME=gpt-5.4
*/

type debugLoginResponse struct {
	Token string `json:"token"`
}

type debugAgent struct {
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspaceId"`
	Name            string `json:"name"`
	RoleDescription string `json:"roleDescription"`
	ModelConfigID   string `json:"modelConfigId"`
	SystemPrompt    string `json:"systemPrompt"`
	IsDefault       bool   `json:"isDefault"`
	Status          string `json:"status"`
	StatusSource    string `json:"statusSource"`
	ToolsCount      int    `json:"toolsCount"`
	WorkflowsCount  int    `json:"workflowsCount"`
}

type debugAgentResponse struct {
	Agent debugAgent `json:"agent"`
}

type debugModelConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	APIKeyMasked string `json:"apiKeyMasked"`
	APIBase      string `json:"apiBase"`
	ModelName    string `json:"modelName"`
	Status       string `json:"status"`
}

type debugModelConfigResponse struct {
	ModelConfig debugModelConfig `json:"modelConfig"`
}

func TestDebugEnhanceSystemPromptLive(t *testing.T) {
	baseURL, timeout := requireDebugConfig(t)
	token := debugLogin(t, baseURL, timeout)
	agentID := os.Getenv("ACTWEAVE_DEBUG_AGENT_ID")
	agent := debugFetchAgent(t, baseURL, token, agentID, timeout)

	statusCode, body, elapsed := debugDoJSONRequest(
		t,
		http.MethodPost,
		baseURL+"/agents/"+agentID+"/enhance-system-prompt",
		token,
		agent,
		timeout,
	)

	t.Logf("agent_id=%s workspace_id=%s model_config_id=%s", agent.ID, agent.WorkspaceID, agent.ModelConfigID)
	t.Logf("status=%d elapsed=%s", statusCode, elapsed)
	t.Logf("body=%s", truncateForLog(body, 4000))

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		t.Fatalf("enhance-system-prompt returned status %d", statusCode)
	}
}

func TestDebugDirectModelChatCompletion(t *testing.T) {
	baseURL, timeout := requireDebugConfig(t)
	token := debugLogin(t, baseURL, timeout)
	agentID := os.Getenv("ACTWEAVE_DEBUG_AGENT_ID")
	agent := debugFetchAgent(t, baseURL, token, agentID, timeout)
	modelConfig := debugFetchModelConfig(t, baseURL, token, agent.ModelConfigID, timeout)

	apiKey := firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_MODEL_API_KEY"), modelConfig.APIKeyMasked)
	apiBase := firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_MODEL_API_BASE"), modelConfig.APIBase)
	modelName := firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_MODEL_NAME"), modelConfig.ModelName)
	if strings.TrimSpace(apiKey) == "" || strings.Contains(apiKey, "****") {
		t.Skip("model api key is empty or masked; set ACTWEAVE_DEBUG_MODEL_API_KEY to run this test")
	}

	endpoint, err := debugChatCompletionsEndpoint(apiBase)
	if err != nil {
		t.Fatalf("build chat completions endpoint: %v", err)
	}

	payload := map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a debugging assistant.",
			},
			{
				"role":    "user",
				"content": "Reply with one short line confirming the upstream model is reachable.",
			},
		},
		"max_tokens": 64,
	}

	statusCode, body, elapsed := debugDoJSONRequestWithHeaders(
		t,
		http.MethodPost,
		endpoint,
		map[string]string{
			"Authorization": "Bearer " + apiKey,
			"Content-Type":  "application/json",
		},
		payload,
		timeout,
	)

	t.Logf("provider=%s model_config_id=%s model_name=%s endpoint=%s", modelConfig.Provider, modelConfig.ID, modelName, endpoint)
	t.Logf("status=%d elapsed=%s", statusCode, elapsed)
	t.Logf("body=%s", truncateForLog(body, 4000))

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		t.Fatalf("direct chat completion returned status %d", statusCode)
	}
}

func requireDebugConfig(t *testing.T) (string, time.Duration) {
	t.Helper()

	if os.Getenv("ACTWEAVE_DEBUG_RUN") != "1" {
		t.Skip("set ACTWEAVE_DEBUG_RUN=1 to enable live debug tests")
	}

	if strings.TrimSpace(os.Getenv("ACTWEAVE_DEBUG_AGENT_ID")) == "" {
		t.Skip("set ACTWEAVE_DEBUG_AGENT_ID to the target agent id")
	}

	baseURL := strings.TrimRight(firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_BASE_URL"), "http://127.0.0.1:8082/api"), "/")
	timeoutSeconds := 190
	if raw := strings.TrimSpace(os.Getenv("ACTWEAVE_DEBUG_TIMEOUT_SECONDS")); raw != "" {
		if parsed, err := time.ParseDuration(raw + "s"); err == nil {
			return baseURL, parsed
		}
		t.Fatalf("invalid ACTWEAVE_DEBUG_TIMEOUT_SECONDS=%q", raw)
	}
	return baseURL, time.Duration(timeoutSeconds) * time.Second
}

func debugLogin(t *testing.T, baseURL string, timeout time.Duration) string {
	t.Helper()

	statusCode, body, elapsed := debugDoJSONRequest(
		t,
		http.MethodPost,
		baseURL+"/auth/login",
		"",
		map[string]string{
			"username": firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_USERNAME"), "chen.ops"),
			"password": firstNonEmpty(os.Getenv("ACTWEAVE_DEBUG_PASSWORD"), "actweave-demo"),
		},
		timeout,
	)
	t.Logf("login status=%d elapsed=%s body=%s", statusCode, elapsed, truncateForLog(body, 1000))
	if statusCode != http.StatusOK {
		t.Fatalf("login returned status %d", statusCode)
	}

	var payload debugLoginResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		t.Fatal("login response token is empty")
	}
	return payload.Token
}

func debugFetchAgent(t *testing.T, baseURL string, token string, agentID string, timeout time.Duration) debugAgent {
	t.Helper()

	statusCode, body, elapsed := debugDoJSONRequest(t, http.MethodGet, baseURL+"/agents/"+agentID, token, nil, timeout)
	t.Logf("get-agent status=%d elapsed=%s body=%s", statusCode, elapsed, truncateForLog(body, 2000))
	if statusCode != http.StatusOK {
		t.Fatalf("get agent returned status %d", statusCode)
	}

	var payload debugAgentResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode agent response: %v", err)
	}
	if payload.Agent.ID == "" {
		t.Fatal("agent response did not include agent.id")
	}
	return payload.Agent
}

func debugFetchModelConfig(t *testing.T, baseURL string, token string, modelConfigID string, timeout time.Duration) debugModelConfig {
	t.Helper()

	statusCode, body, elapsed := debugDoJSONRequest(t, http.MethodGet, baseURL+"/model-api-configs/"+modelConfigID, token, nil, timeout)
	t.Logf("get-model-config status=%d elapsed=%s body=%s", statusCode, elapsed, truncateForLog(body, 2000))
	if statusCode != http.StatusOK {
		t.Fatalf("get model config returned status %d", statusCode)
	}

	var payload debugModelConfigResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode model config response: %v", err)
	}
	if payload.ModelConfig.ID == "" {
		t.Fatal("model config response did not include modelConfig.id")
	}
	return payload.ModelConfig
}

func debugDoJSONRequest(t *testing.T, method string, requestURL string, token string, payload any, timeout time.Duration) (int, string, time.Duration) {
	t.Helper()

	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if payload != nil {
		headers["Content-Type"] = "application/json"
	}
	return debugDoJSONRequestWithHeaders(t, method, requestURL, headers, payload, timeout)
}

func debugDoJSONRequestWithHeaders(t *testing.T, method string, requestURL string, headers map[string]string, payload any, timeout time.Duration) (int, string, time.Duration) {
	t.Helper()

	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal request payload: %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	request, err := http.NewRequest(method, requestURL, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	client := &http.Client{Timeout: timeout}
	started := time.Now()
	response, err := client.Do(request)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("request failed after %s: %v", elapsed, err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return response.StatusCode, string(rawBody), elapsed
}

func debugChatCompletionsEndpoint(apiBase string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid api base %q", apiBase)
	}
	parsed.Path = path.Join(parsed.Path, "chat/completions")
	if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateForLog(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "...(truncated)"
}
