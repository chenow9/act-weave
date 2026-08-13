package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/config"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
)

// Fixed non-sensitive probe prompt (no tenant content).
const agenticVerificationPrompt = "ActWeave model verification probe. Reply with a short acknowledgement."

// Inner probe budgets. The outer budget in
// config.DefaultModelVerificationTimeoutSeconds must stay at or above their
// sequential sum plus a connectivity allowance, otherwise
// VerificationService.Verify cancels the whole upstream call before an inner
// deadline can fire.
const (
	agenticProbeResponsesStreamBudget  = 30 * time.Second
	agenticProbeClientToolSearchBudget = 45 * time.Second
	agenticProbeFunctionCallingBudget  = 30 * time.Second
)

// modelVerificationMinClientTimeout is the shortest overall http.Client.Timeout
// that can carry a verification probe without becoming the binding deadline.
// A single probe request never outlives the largest inner budget, so a client at
// or above it lets the inner budgets and the configurable outer budget decide.
//
// The shared application client is 15s (application.Open). Handing it to the
// verifier caps every probe below both inner budgets AND below the outer
// configurable budget, so the upstream is failed at 15s and neither budget can
// ever be reached — the same dead-budget defect the configurable outer timeout
// was introduced to remove, relocated into the HTTP client. Cold-starting
// upstreams then fail verification no matter how the operator configures it.
const modelVerificationMinClientTimeout = agenticProbeClientToolSearchBudget

// modelVerificationTimeout resolves the outer verification budget Open hands to
// modelconfig.NewVerificationService. Zero (config omitted, or a RuntimeConfig
// built without config.Load) becomes the 120s default; every other value is
// passed through unchanged, so a hostile negative reaches
// NewVerificationService's guard and fails Open closed instead of being
// silently repaired here.
func modelVerificationTimeout(runtime config.RuntimeConfig) time.Duration {
	return runtime.ModelVerification.Normalized().Timeout()
}

// Temporary side-effect-free deferred function used only during verification.
const agenticVerificationEchoTool = "actweave_verification_echo"

// verificationEchoTool is a side-effect-free invokable tool used only in the
// model config capability probe. It must never call external systems.
// The required token argument is validated by the probe against a per-run nonce;
// InvokableRun itself is never invoked against external systems.
type verificationEchoTool struct{}

func (verificationEchoTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: agenticVerificationEchoTool,
		Desc: "Echo verification helper. Returns a fixed acknowledgement. No side effects.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"token": {
				Type:     schema.String,
				Desc:     "Opaque verification token (non-sensitive).",
				Required: true,
			},
		}),
	}, nil
}

func (verificationEchoTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	// Never execute external side effects during verification.
	return `{"ok":true,"echo":"actweave_verification"}`, nil
}

// modelConfigVerifier performs ordered, low-token Agentic capability probes
// using the real Task 1 NewOpenAIAgenticModel / pinned agenticopenai Responses
// adapter. Secrets remain byte-scoped via modelapi.SecretOpener (*secret.Service).
type modelConfigVerifier struct {
	client  *http.Client
	secrets modelapi.SecretOpener

	// Built at most once per verifier; see probeHTTPClient.
	fallbackOnce   sync.Once
	fallbackClient *http.Client
}

// probeHTTPClient returns the client used for every verification probe.
// An injected client is honoured only when its overall Timeout cannot cut a
// probe short (0 = context-bound, or at least the largest inner budget);
// otherwise the stream-safe client is used, whose only deadline is the
// transport response-header timeout. Mirrors promptGenerator.llmHTTPClient.
//
// The fallback is built once and reused. Each modelapi.NewStreamingHTTPClient
// owns a fresh http.Transport with its own connection pool, so building one per
// call would stop the probes within a verification from reusing a connection and
// would leak an idle pool per probe (transports are never closed here). The
// production path takes the fallback on every call, which is exactly the path
// that runs several probes back to back against one upstream.
func (verifier *modelConfigVerifier) probeHTTPClient() *http.Client {
	if verifier == nil {
		return modelapi.NewStreamingHTTPClient()
	}
	if verifier.client != nil {
		if verifier.client.Timeout == 0 || verifier.client.Timeout >= modelVerificationMinClientTimeout {
			return verifier.client
		}
	}
	verifier.fallbackOnce.Do(func() {
		verifier.fallbackClient = modelapi.NewStreamingHTTPClient()
	})
	return verifier.fallbackClient
}

// Verify implements modelconfig.Verifier.
func (verifier *modelConfigVerifier) Verify(ctx context.Context, config modelconfig.Config) (modelconfig.AgenticCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return modelconfig.AgenticCapabilities{}, err
	}

	// 1) Lightweight auth/connectivity probe (GET /models). Failure classifies
	// auth/network; success alone is insufficient for VERIFIED.
	if err := verifier.probeAuthConnectivity(ctx, config); err != nil {
		return modelconfig.AgenticCapabilities{}, err
	}

	// 2–4) Responses streaming, then native client tool-search, then (on a
	// capability miss only) ordinary function calling. ToolCalling is the only
	// probe field the service uses; lock/digest/timestamp are stamped there.
	return verifier.probeAgenticCapabilities(ctx, config)
}

func (verifier *modelConfigVerifier) probeAuthConnectivity(ctx context.Context, config modelconfig.Config) error {
	target, err := modelEndpoint(config.APIBase, "models")
	if err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrResponsesUnsupported, err)
	}
	invoke := func(token []byte) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		if len(token) > 0 {
			request.Header.Set("Authorization", "Bearer "+string(token))
		}
		response, err := verifier.probeHTTPClient().Do(request)
		if err != nil {
			return mapNetworkError(err)
		}
		defer response.Body.Close()
		// Classify the status line before touching the body: the status is already
		// available, and a rejected upstream must be reported as a rejection even
		// when its body never completes.
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return modelconfig.ErrUpstreamAuthentication
		case http.StatusNotFound:
			// /models missing is non-fatal for Responses-capable endpoints; continue.
			return nil
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return fmt.Errorf("%w: HTTP_STATUS_%d", modelconfig.ErrVerificationUpstream, response.StatusCode)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("%w: HTTP_STATUS_%d", modelconfig.ErrVerificationUpstream, response.StatusCode)
		}
		// Drain a small prefix only; never retain body for persistence. The read
		// must fail closed: an upstream that returns 200 and then stalls or drops
		// the body has not demonstrated connectivity, and discarding this error
		// reported such an upstream as authenticated. The stall is bounded by ctx,
		// not by the client, so the drain cannot outlive the verification budget.
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10)); err != nil {
			return mapNetworkError(err)
		}
		return nil
	}
	if config.CredentialSecretID == nil || strings.TrimSpace(*config.CredentialSecretID) == "" {
		return invoke(nil)
	}
	if verifier.secrets == nil {
		return errors.New("model verification secrets are required")
	}
	return verifier.secrets.WithActiveSecret(ctx, config.WorkspaceID, *config.CredentialSecretID, invoke)
}

func (verifier *modelConfigVerifier) probeAgenticCapabilities(ctx context.Context, config modelconfig.Config) (modelconfig.AgenticCapabilities, error) {
	client := verifier.probeHTTPClient()
	// Verification-only: wrap with strict raw usage validator around the real
	// pinned adapter HTTP path. Wrong JSON types classify USAGE_INVALID before
	// the typed adapter can lose evidence.
	client = wrapClientWithVerificationUsageValidator(client)

	if verifier.secrets == nil {
		return modelconfig.AgenticCapabilities{}, errors.New("model verification secrets are required")
	}
	am, err := modelapi.NewOpenAIAgenticModel(ctx, client, verifier.secrets, config)
	if err != nil {
		return modelconfig.AgenticCapabilities{}, mapAgenticConstructionError(err)
	}

	// Build a one-tool frozen catalog + bounded client search executor.
	echo := verificationEchoTool{}
	cat, err := einoruntime.BuildToolCatalog(ctx, []einoruntime.ToolCatalogBuildEntry{
		{Tool: echo, Exposure: einoruntime.ToolExposureDeferred, Kind: einoruntime.ToolKindTool},
	})
	if err != nil {
		return modelconfig.AgenticCapabilities{}, fmt.Errorf("%w: catalog: %v", modelconfig.ErrToolSearchUnsupported, err)
	}
	mw, err := einoruntime.NewBoundedClientToolSearchMiddleware(cat)
	if err != nil {
		return modelconfig.AgenticCapabilities{}, fmt.Errorf("%w: search middleware: %v", modelconfig.ErrToolSearchUnsupported, err)
	}
	searchInfo := mw.SearchToolInfo()
	echoInfo, err := echo.Info(ctx)
	if err != nil || echoInfo == nil {
		return modelconfig.AgenticCapabilities{}, fmt.Errorf("%w: echo tool info", modelconfig.ErrToolSearchUnsupported)
	}

	opts := []model.Option{
		model.WithDeferredTools([]*schema.ToolInfo{echoInfo}),
		model.WithToolSearchTool(searchInfo),
	}

	// --- Probe A: Responses streaming with fixed non-sensitive prompt ---
	if err := verifier.probeResponsesStream(ctx, am); err != nil {
		return modelconfig.AgenticCapabilities{}, err
	}

	// --- Probe B: Client tool-search + echo function call (ordered contract) ---
	if err := verifier.probeClientToolSearch(ctx, am, mw, opts); err != nil {
		if !isAgenticToolSearchCapabilityMiss(err) {
			return modelconfig.AgenticCapabilities{}, err
		}
		// Native search is not available; classify ordinary function calling.
		return verifier.probeFunctionCalling(ctx, am, echoInfo)
	}
	return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingNativeClientSearch}, nil
}

func (verifier *modelConfigVerifier) probeResponsesStream(ctx context.Context, am model.AgenticModel) error {
	streamCtx, cancel := context.WithTimeout(ctx, agenticProbeResponsesStreamBudget)
	defer cancel()
	sr, err := am.Stream(streamCtx, []*schema.AgenticMessage{
		agenticmsg.UserText(agenticVerificationPrompt),
	})
	if err != nil {
		return mapAgenticStreamError(err)
	}
	defer sr.Close()

	var chunks []*schema.AgenticMessage
	for {
		chunk, recvErr := sr.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return mapAgenticStreamError(recvErr)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("%w: empty stream", modelconfig.ErrAgenticStreamInvalid)
	}
	msg, err := agenticmsg.ConcatStream(chunks)
	if err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticStreamInvalid, err)
	}
	text, err := agenticmsg.ExtractAssistantText(msg)
	if err != nil || strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: missing assistant text", modelconfig.ErrAgenticStreamInvalid)
	}
	// Every required probe turn must carry valid usage (contract).
	if err := requireProbeUsage(msg); err != nil {
		return err
	}
	return nil
}

func (verifier *modelConfigVerifier) probeClientToolSearch(
	ctx context.Context,
	am model.AgenticModel,
	mw *einoruntime.BoundedClientToolSearchMiddleware,
	opts []model.Option,
) error {
	probeCtx, cancel := context.WithTimeout(ctx, agenticProbeClientToolSearchBudget)
	defer cancel()

	// Unpredictable per-verification nonce; model must echo it exactly.
	nonce, err := newVerificationNonce()
	if err != nil {
		return fmt.Errorf("%w: nonce: %v", modelconfig.ErrToolSearchUnsupported, err)
	}

	// Exact probe contract prompt: model must emit one client tool_search with
	// exact select:echo + max_results:1 (no repair of model args).
	// Keep the plaintext "token <nonce>" phrase so contract tests and humans can
	// locate the per-verification nonce in the request body.
	firstInput := []*schema.AgenticMessage{
		agenticmsg.UserText(fmt.Sprintf(
			"Call tool_search exactly once with JSON arguments "+
				`{"query":"select:%s","max_results":1}`+
				" (exact keys only, no extras). After search completes, call %s "+
				"with token %s exactly as JSON object with only that field "+
				`({"token":"%s"}).`,
			agenticVerificationEchoTool, agenticVerificationEchoTool, nonce, nonce,
		)),
	}
	// Use Generate for the multi-turn tool-search contract so the fake/live
	// Responses body is a complete object (same path as Task 2 wire capture).
	assistant, err := am.Generate(probeCtx, firstInput, opts...)
	if err != nil {
		return mapAgenticToolSearchError(err)
	}
	if assistant == nil {
		return fmt.Errorf("%w: empty tool-search response", modelconfig.ErrAgenticStreamInvalid)
	}
	// Detect hosted/server search path first.
	for _, block := range assistant.ContentBlocks {
		if block != nil && block.ServerToolCall != nil {
			return fmt.Errorf("%w: hosted/server tool search", modelconfig.ErrToolSearchUnsupported)
		}
	}
	// Require exactly one client-executed tool-search call (no text/extra actions).
	var searchCall *schema.FunctionToolCall
	actionCount := 0
	for _, block := range assistant.ContentBlocks {
		if block == nil {
			continue
		}
		if block.AssistantGenText != nil && strings.TrimSpace(block.AssistantGenText.Text) != "" {
			return fmt.Errorf("%w: unexpected text with tool_search", modelconfig.ErrToolSearchUnsupported)
		}
		if block.FunctionToolCall != nil {
			actionCount++
			if agenticopenai.GetToolSearchToolCall(block) {
				if searchCall != nil {
					return fmt.Errorf("%w: multiple tool_search calls", modelconfig.ErrToolSearchUnsupported)
				}
				searchCall = block.FunctionToolCall
				continue
			}
			// Ordinary function without search is insufficient for this probe phase.
			return fmt.Errorf("%w: unexpected non-search function call before search", modelconfig.ErrToolSearchUnsupported)
		}
	}
	if searchCall == nil {
		return fmt.Errorf("%w: no client tool_search call", modelconfig.ErrToolSearchUnsupported)
	}
	if actionCount != 1 {
		return fmt.Errorf("%w: tool-search phase must be single-action", modelconfig.ErrToolSearchUnsupported)
	}
	// Tool-search phase must also carry consistent usage.
	if err := requireProbeUsage(assistant); err != nil {
		return err
	}

	// Validate the model's original native client search args BEFORE local execution.
	// Never rewrite/repair bad query, max_results=999, missing keys, extras, etc.
	// Deviation => MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED (never VERIFIED).
	if err := validateProbeToolSearchArgs(searchCall.Arguments); err != nil {
		return err
	}
	// Execute the original validated args through the bounded executor (probe max=1
	// is part of the validated args; no mutation of the wire arguments).
	exec := mw.Executor()
	toolArg := &schema.ToolArgument{Text: searchCall.Arguments}
	enh, ok := exec.(tool.EnhancedInvokableTool)
	if !ok {
		return fmt.Errorf("%w: search executor missing enhanced interface", modelconfig.ErrToolSearchUnsupported)
	}
	tr, runErr := enh.InvokableRun(probeCtx, toolArg)
	if runErr != nil {
		return fmt.Errorf("%w: search executor: %v", modelconfig.ErrToolSearchUnsupported, runErr)
	}
	if tr == nil || len(tr.Parts) != 1 || tr.Parts[0].ToolSearchResult == nil {
		return fmt.Errorf("%w: invalid search output shape", modelconfig.ErrToolSearchUnsupported)
	}
	searchResult := tr.Parts[0].ToolSearchResult
	if len(searchResult.Tools) == 0 || len(searchResult.Tools) > 1 {
		return fmt.Errorf("%w: search must return exactly 1 tool for verification", modelconfig.ErrToolSearchUnsupported)
	}
	if searchResult.Tools[0] == nil || searchResult.Tools[0].Name != agenticVerificationEchoTool {
		return fmt.Errorf("%w: unexpected search tool", modelconfig.ErrToolSearchUnsupported)
	}

	// Emit one strict standard search output, then require the model's echo function call.
	searchOutputMsg := &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.ToolSearchFunctionToolResult{
				CallID: searchCall.CallID,
				Name:   searchCall.Name,
				Result: searchResult,
			}),
		},
	}
	secondInput := append(append([]*schema.AgenticMessage{}, firstInput...), assistant, searchOutputMsg)

	assistant2, err := am.Generate(probeCtx, secondInput, opts...)
	if err != nil {
		return mapAgenticStreamError(err)
	}
	if assistant2 == nil {
		return fmt.Errorf("%w: empty post-search response", modelconfig.ErrAgenticStreamInvalid)
	}
	if err := requireExactEchoFunctionCall(assistant2, nonce); err != nil {
		return err
	}
	if err := requireProbeUsage(assistant2); err != nil {
		return err
	}
	return nil
}

// probeFunctionCalling is Phase 3: ordinary Responses function tools only.
// Native tool-search options must not be applied. Capability-style misses
// persist as toolCalling=none; infrastructure stays a typed ERROR.
func (verifier *modelConfigVerifier) probeFunctionCalling(
	ctx context.Context,
	am model.AgenticModel,
	echoInfo *schema.ToolInfo,
) (modelconfig.AgenticCapabilities, error) {
	probeCtx, cancel := context.WithTimeout(ctx, agenticProbeFunctionCallingBudget)
	defer cancel()

	nonce, err := newVerificationNonce()
	if err != nil {
		return modelconfig.AgenticCapabilities{}, fmt.Errorf("%w: nonce: %v", modelconfig.ErrVerificationUpstream, err)
	}

	opts := []model.Option{
		model.WithTools([]*schema.ToolInfo{echoInfo}),
	}
	input := []*schema.AgenticMessage{
		agenticmsg.UserText(fmt.Sprintf(
			"Call %s exactly once with token %s exactly as JSON object with only that field "+
				`({"token":"%s"}). Do not emit any other text or tool calls.`,
			agenticVerificationEchoTool, nonce, nonce,
		)),
	}
	assistant, err := am.Generate(probeCtx, input, opts...)
	if err != nil {
		if mapped := mapAgenticFunctionCallingProbeError(err); mapped != nil {
			return modelconfig.AgenticCapabilities{}, mapped
		}
		return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingNone}, nil
	}
	if assistant == nil {
		return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingNone}, nil
	}
	if err := requireExactEchoFunctionCall(assistant, nonce); err != nil {
		return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingNone}, nil
	}
	if err := requireProbeUsage(assistant); err != nil {
		return modelconfig.AgenticCapabilities{}, err
	}
	return modelconfig.AgenticCapabilities{ToolCalling: modelconfig.ToolCallingFunctionCalling}, nil
}

// probeToolSearchExactQuery is the only accepted client search query for the
// verification probe: direct select of the echo tool (no keyword repair).
const probeToolSearchExactQuery = "select:" + agenticVerificationEchoTool

// validateProbeToolSearchArgs enforces the exact probe client-search call contract
// on the model's original native arguments before any local execution:
//
//   - JSON object with exactly keys "query" and "max_results" (no unknown/extra/duplicate)
//   - query exactly "select:actweave_verification_echo"
//   - max_results exactly integer 1 (not 999, not omitted, not float/string/null)
//
// Any deviation returns MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED. Never rewrites args.
func validateProbeToolSearchArgs(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fmt.Errorf("%w: empty tool_search arguments", modelconfig.ErrToolSearchUnsupported)
	}
	if err := rejectDuplicateJSONKeys([]byte(s)); err != nil {
		return fmt.Errorf("%w: tool_search arguments: %v", modelconfig.ErrToolSearchUnsupported, err)
	}
	if s[0] != '{' {
		return fmt.Errorf("%w: tool_search arguments must be a JSON object", modelconfig.ErrToolSearchUnsupported)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return fmt.Errorf("%w: tool_search arguments decode", modelconfig.ErrToolSearchUnsupported)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data in tool_search arguments", modelconfig.ErrToolSearchUnsupported)
	}
	if len(top) != 2 {
		return fmt.Errorf("%w: tool_search arguments must be exactly query+max_results", modelconfig.ErrToolSearchUnsupported)
	}
	queryRaw, hasQuery := top["query"]
	maxRaw, hasMax := top["max_results"]
	if !hasQuery || !hasMax {
		return fmt.Errorf("%w: tool_search arguments missing query or max_results", modelconfig.ErrToolSearchUnsupported)
	}
	var query string
	if err := json.Unmarshal(queryRaw, &query); err != nil {
		return fmt.Errorf("%w: tool_search query must be a string", modelconfig.ErrToolSearchUnsupported)
	}
	if query != probeToolSearchExactQuery {
		return fmt.Errorf("%w: tool_search query must be exact select of verification echo", modelconfig.ErrToolSearchUnsupported)
	}
	// max_results must be JSON integer 1 (reject 1.0 float form, strings, null, bool).
	maxRaw = bytes.TrimSpace(maxRaw)
	if len(maxRaw) == 0 || maxRaw[0] == '"' || maxRaw[0] == '{' || maxRaw[0] == '[' ||
		maxRaw[0] == 't' || maxRaw[0] == 'f' || bytes.Equal(maxRaw, []byte("null")) {
		return fmt.Errorf("%w: tool_search max_results must be integer 1", modelconfig.ErrToolSearchUnsupported)
	}
	if strings.ContainsAny(string(maxRaw), ".eE+") {
		return fmt.Errorf("%w: tool_search max_results must be integer 1", modelconfig.ErrToolSearchUnsupported)
	}
	n, err := strconv.ParseInt(string(maxRaw), 10, 64)
	if err != nil || n != 1 {
		return fmt.Errorf("%w: tool_search max_results must be exactly 1", modelconfig.ErrToolSearchUnsupported)
	}
	return nil
}

// requireExactEchoFunctionCall enforces exactly one function action for
// actweave_verification_echo with exact arguments (only "token": nonce).
// Rejects text, extra/missing/wrong args, search calls, or multiple actions.
// Does not execute the function externally.
func requireExactEchoFunctionCall(msg *schema.AgenticMessage, nonce string) error {
	if msg == nil {
		return fmt.Errorf("%w: nil echo response", modelconfig.ErrToolSearchUnsupported)
	}
	var echoCall *schema.FunctionToolCall
	actionCount := 0
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		if block.AssistantGenText != nil && strings.TrimSpace(block.AssistantGenText.Text) != "" {
			return fmt.Errorf("%w: unexpected text with echo function call", modelconfig.ErrToolSearchUnsupported)
		}
		if block.ServerToolCall != nil {
			return fmt.Errorf("%w: unexpected server tool call after search", modelconfig.ErrToolSearchUnsupported)
		}
		if block.FunctionToolCall == nil {
			continue
		}
		actionCount++
		if agenticopenai.GetToolSearchToolCall(block) {
			return fmt.Errorf("%w: unexpected tool_search after search completion", modelconfig.ErrToolSearchUnsupported)
		}
		if block.FunctionToolCall.Name != agenticVerificationEchoTool {
			return fmt.Errorf("%w: unexpected function %q", modelconfig.ErrToolSearchUnsupported, block.FunctionToolCall.Name)
		}
		if echoCall != nil {
			return fmt.Errorf("%w: multiple echo function calls", modelconfig.ErrToolSearchUnsupported)
		}
		echoCall = block.FunctionToolCall
	}
	if echoCall == nil {
		return fmt.Errorf("%w: missing actweave_verification_echo function call", modelconfig.ErrToolSearchUnsupported)
	}
	if actionCount != 1 {
		return fmt.Errorf("%w: echo phase must be single-action", modelconfig.ErrToolSearchUnsupported)
	}
	// Strict arguments: object with exactly {"token":"<nonce>"}, no extras.
	if err := validateEchoTokenArgs(echoCall.Arguments, nonce); err != nil {
		return err
	}
	return nil
}

func validateEchoTokenArgs(raw string, nonce string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fmt.Errorf("%w: empty echo arguments", modelconfig.ErrToolSearchUnsupported)
	}
	if err := rejectDuplicateJSONKeys([]byte(s)); err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrToolSearchUnsupported, err)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var args struct {
		Token string `json:"token"`
	}
	if err := dec.Decode(&args); err != nil {
		return fmt.Errorf("%w: echo arguments: %v", modelconfig.ErrToolSearchUnsupported, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data in echo arguments", modelconfig.ErrToolSearchUnsupported)
	}
	// Re-decode as map to enforce exactly one field.
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return fmt.Errorf("%w: echo arguments object", modelconfig.ErrToolSearchUnsupported)
	}
	if len(m) != 1 {
		return fmt.Errorf("%w: echo arguments must contain exactly token", modelconfig.ErrToolSearchUnsupported)
	}
	if _, ok := m["token"]; !ok {
		return fmt.Errorf("%w: echo arguments missing token", modelconfig.ErrToolSearchUnsupported)
	}
	if args.Token != nonce {
		return fmt.Errorf("%w: echo token mismatch", modelconfig.ErrToolSearchUnsupported)
	}
	return nil
}

func newVerificationNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "awv-" + hex.EncodeToString(b[:]), nil
}

// requireProbeUsage enforces MODEL_CONFIG_AGENTIC_USAGE_INVALID contract:
// usage object present; non-negative fields; exact totals
// (input/prompt + output/completion == total); cached <= input; reasoning <= output.
// Optional cached/reasoning detail fields may be absent (zero) or present and valid.
func requireProbeUsage(msg *schema.AgenticMessage) error {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.TokenUsage == nil {
		return fmt.Errorf("%w: missing usage", modelconfig.ErrAgenticUsageInvalid)
	}
	usage, err := agenticmsg.Usage(msg)
	if err != nil {
		return fmt.Errorf("%w: %v", modelconfig.ErrAgenticUsageInvalid, err)
	}
	return validateProbeUsageConsistency(usage)
}

func validateProbeUsageConsistency(usage agenticmsg.TokenUsage) error {
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 ||
		usage.CachedTokens < 0 || usage.ReasoningTokens < 0 {
		return fmt.Errorf("%w: negative usage", modelconfig.ErrAgenticUsageInvalid)
	}
	// Zero totals are treated as missing usage for verification probes (adapters may
	// materialize empty TokenUsage structs when the provider omits the usage object).
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return fmt.Errorf("%w: missing or zero usage", modelconfig.ErrAgenticUsageInvalid)
	}
	// Overflow-safe sum of input+output.
	if usage.PromptTokens > math.MaxInt-usage.CompletionTokens {
		return fmt.Errorf("%w: usage overflow", modelconfig.ErrAgenticUsageInvalid)
	}
	if usage.PromptTokens+usage.CompletionTokens != usage.TotalTokens {
		return fmt.Errorf("%w: inconsistent usage totals (input+output != total)", modelconfig.ErrAgenticUsageInvalid)
	}
	if usage.CachedTokens > usage.PromptTokens {
		return fmt.Errorf("%w: cached tokens exceed input", modelconfig.ErrAgenticUsageInvalid)
	}
	if usage.ReasoningTokens > usage.CompletionTokens {
		return fmt.Errorf("%w: reasoning tokens exceed output", modelconfig.ErrAgenticUsageInvalid)
	}
	return nil
}

func mapNetworkError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		// Typed network only — never embed dial/host strings into returned errors.
		return modelconfig.ErrVerificationNetwork
	}
	return modelconfig.ErrVerificationNetwork
}

func mapAgenticConstructionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		return err
	}
	if errors.Is(err, modelconfig.ErrUpstreamAuthentication) {
		return modelconfig.ErrUpstreamAuthentication
	}
	if errors.Is(err, modelconfig.ErrVerificationNetwork) {
		return modelconfig.ErrVerificationNetwork
	}
	if errors.Is(err, modelconfig.ErrVerificationUpstream) {
		return err
	}
	if errors.Is(err, modelapi.ErrAgenticAzureUnsupported) {
		return modelconfig.ErrResponsesUnsupported
	}
	// Construction failures that are not already typed: protocol unsupported.
	// Do not embed provider/construction detail strings (may contain secrets).
	return modelconfig.ErrResponsesUnsupported
}

func mapAgenticStreamError(err error) error {
	if err == nil {
		return nil
	}
	// Preserve already-classified typed errors (usage transport, status, network).
	// Return the typed sentinel alone so provider body text cannot ride along.
	switch {
	case errors.Is(err, modelconfig.ErrAgenticUsageInvalid):
		return modelconfig.ErrAgenticUsageInvalid
	case errors.Is(err, modelconfig.ErrAgenticStreamInvalid):
		return modelconfig.ErrAgenticStreamInvalid
	case errors.Is(err, modelconfig.ErrUpstreamAuthentication):
		return modelconfig.ErrUpstreamAuthentication
	case errors.Is(err, modelconfig.ErrResponsesUnsupported):
		return modelconfig.ErrResponsesUnsupported
	case errors.Is(err, modelconfig.ErrToolSearchUnsupported):
		return modelconfig.ErrToolSearchUnsupported
	case errors.Is(err, modelconfig.ErrVerificationNetwork):
		return modelconfig.ErrVerificationNetwork
	case errors.Is(err, modelconfig.ErrVerificationUpstream):
		return err // may carry HTTP_STATUS_N suffix only (no body)
	case errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, context.Canceled):
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return modelconfig.ErrVerificationNetwork
	}
	// Fallback string classification for adapters that only surface status text.
	// Never re-embed the original error string (may contain provider body/secret).
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") {
		return modelconfig.ErrUpstreamAuthentication
	}
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return modelconfig.ErrResponsesUnsupported
	}
	if strings.Contains(msg, "429") || strings.Contains(msg, "too many") ||
		strings.Contains(msg, "500") || strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return modelconfig.ErrVerificationUpstream
	}
	// Usage defects must classify USAGE_INVALID, not stream/tool-search.
	if strings.Contains(msg, "usage") || strings.Contains(msg, "input_tokens") ||
		strings.Contains(msg, "output_tokens") || strings.Contains(msg, "total_tokens") {
		return modelconfig.ErrAgenticUsageInvalid
	}
	return modelconfig.ErrAgenticStreamInvalid
}

func mapAgenticToolSearchError(err error) error {
	if err == nil {
		return nil
	}
	// Usage invalid must win over tool-search classification.
	if errors.Is(err, modelconfig.ErrAgenticUsageInvalid) {
		return modelconfig.ErrAgenticUsageInvalid
	}
	mapped := mapAgenticStreamError(err)
	// Unparseable wire is infrastructure: do not treat it as a capability miss
	// or the function-calling probe would run against a broken stream.
	if errors.Is(mapped, modelconfig.ErrAgenticStreamInvalid) {
		return modelconfig.ErrAgenticStreamInvalid
	}
	return mapped
}

// isAgenticToolSearchCapabilityMiss reports a Phase 2 contract miss (no search,
// hosted, arg drift, unexpected text/function, bad search output) or an HTTP
// 400/422 reject of the native-search request. Infra failures stay ERROR and
// must not enter Phase 3.
func isAgenticToolSearchCapabilityMiss(err error) bool {
	return errors.Is(err, modelconfig.ErrToolSearchUnsupported) || isPhase2CapabilityHTTPReject(err)
}

// isPhase2CapabilityHTTPReject is only 400/422. Auth, missing route, rate
// limits, and 5xx stay infrastructure.
func isPhase2CapabilityHTTPReject(err error) bool {
	if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
		return false
	}
	status := verificationHTTPStatus(err)
	return status == http.StatusBadRequest || status == http.StatusUnprocessableEntity
}

// mapAgenticFunctionCallingProbeError maps Phase 3 Generate failures.
// Infrastructure stays a typed ERROR. Capability-style rejects (400/422,
// unrecognized 4xx, other non-infra) return nil so the caller persists none.
// Must not call mapAgenticToolSearchError.
func mapAgenticFunctionCallingProbeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, modelconfig.ErrAgenticUsageInvalid):
		return modelconfig.ErrAgenticUsageInvalid
	case errors.Is(err, modelconfig.ErrAgenticStreamInvalid):
		return modelconfig.ErrAgenticStreamInvalid
	case errors.Is(err, modelconfig.ErrUpstreamAuthentication):
		return modelconfig.ErrUpstreamAuthentication
	case errors.Is(err, modelconfig.ErrResponsesUnsupported):
		return modelconfig.ErrResponsesUnsupported
	case errors.Is(err, modelconfig.ErrVerificationNetwork):
		return modelconfig.ErrVerificationNetwork
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, modelconfig.ErrVerificationUpstream):
		if isFunctionCallingCapabilityHTTPReject(err) {
			return nil
		}
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return modelconfig.ErrVerificationNetwork
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") {
		return modelconfig.ErrUpstreamAuthentication
	}
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return modelconfig.ErrResponsesUnsupported
	}
	if strings.Contains(msg, "429") || strings.Contains(msg, "too many") ||
		strings.Contains(msg, "500") || strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return modelconfig.ErrVerificationUpstream
	}
	if strings.Contains(msg, "usage") || strings.Contains(msg, "input_tokens") ||
		strings.Contains(msg, "output_tokens") || strings.Contains(msg, "total_tokens") {
		return modelconfig.ErrAgenticUsageInvalid
	}
	if strings.Contains(msg, "stream") || strings.Contains(msg, "unmarshal") ||
		strings.Contains(msg, "invalid json") {
		return modelconfig.ErrAgenticStreamInvalid
	}
	// Unrecognized 4xx and other capability-style rejects persist as none.
	return nil
}

func isFunctionCallingCapabilityHTTPReject(err error) bool {
	if !errors.Is(err, modelconfig.ErrVerificationUpstream) {
		return false
	}
	status := verificationHTTPStatus(err)
	if status == 0 {
		return false
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return false
	}
	return status >= 400 && status < 500
}

func verificationHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	const prefix = "HTTP_STATUS_"
	msg := err.Error()
	idx := strings.LastIndex(msg, prefix)
	if idx < 0 {
		return 0
	}
	n, convErr := strconv.Atoi(msg[idx+len(prefix):])
	if convErr != nil || n < 100 || n > 599 {
		return 0
	}
	return n
}
