package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/responses"

	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/modelconfig"
)

// Stable non-secret construction errors for the Agentic OpenAI adapter.
var (
	// ErrAgenticAzureUnsupported is returned when provider is Azure. The
	// agenticopenai Responses adapter does not yet implement a complete Azure
	// endpoint/api-version contract; fail closed until it does.
	ErrAgenticAzureUnsupported = errors.New("modelapi agentic azure provider is not supported")
	// ErrAgenticParallelToolCallsFixed is returned when options explicitly set
	// parallelToolCalls/parallel_tool_calls to true. Production is fixed false.
	ErrAgenticParallelToolCallsFixed = errors.New("modelapi agentic parallelToolCalls is fixed false")
	// ErrAgenticAPIVersionUnsupported is returned when apiVersion/api_version
	// is supplied. Azure is unsupported, so the field is always inapplicable.
	ErrAgenticAPIVersionUnsupported = errors.New("modelapi agentic apiVersion is not supported")
	// ErrAgenticOptionAliasConflict is returned when both camelCase and
	// snake_case aliases of the same setting are supplied.
	ErrAgenticOptionAliasConflict = errors.New("modelapi agentic options: conflicting camelCase/snake_case aliases")
	// ErrAgenticHTTPRedirect is returned when the model-specific HTTP client
	// would follow a redirect. Redirects are rejected so credentials and
	// request bodies never leave the original endpoint unguarded.
	ErrAgenticHTTPRedirect = errors.New("modelapi agentic HTTP redirects are not allowed")
	// ErrAgenticInvalidResponsesBody is returned when a POST to /responses has
	// a body that is not a JSON object. Fail closed without calling the base
	// transport so unguarded or non-rewritable payloads never leave.
	ErrAgenticInvalidResponsesBody = errors.New("modelapi agentic responses request body must be a JSON object")
	// ErrAgenticInvalidAPIBase is returned when APIBase fails strict construction
	// validation. openai-go applies WithBaseURL lazily, so malformed, relative,
	// userinfo-bearing, query/fragment, or non-HTTP(S) bases must be rejected
	// here before the adapter is built. Errors never echo credentials/userinfo.
	ErrAgenticInvalidAPIBase = errors.New("modelapi agentic API base is invalid")
)

// NewOpenAIAgenticModel builds a production model.AgenticModel using the
// official eino-ext agenticopenai Responses adapter (Responses API), wrapped
// in a guarding boundary that enforces platform request invariants.
//
// This is the sole production OpenAI model path after Task 9. There is no
// runtime fallback to classic Chat Completions.
//
// Security and request semantics:
//   - Credentials are resolved once via SecretOpener; secret material is never
//     placed in returned errors.
//   - Store is forced false and EnableAutoCache is forced false so the adapter
//     never enables server-side response storage / previous_response_id
//     continuation as a correctness dependency.
//   - ParallelToolCalls is fixed false (config and call-time).
//   - A guarding AgenticModel + outbound HTTP rewrite prevent call-time
//     options (including WithExtraFields) and input message metadata from
//     overriding store, parallel_tool_calls, or previous_response_id.
//   - Common Eino options (tools, deferred tools, tool-search tool, prompt
//     cache key, temperature, …) remain usable.
//   - HTTPClient should be stream-safe (NewStreamingHTTPClient).
//
// Azure is rejected entirely until a correct Responses Azure contract exists.
func NewOpenAIAgenticModel(
	ctx context.Context,
	client *http.Client,
	secrets SecretOpener,
	config modelconfig.Config,
) (model.AgenticModel, error) {
	if secrets == nil {
		return nil, errors.New("modelapi secrets are required")
	}
	// Strict API base validation runs before secret resolution and adapter
	// construction. openai-go/v3 applies WithBaseURL lazily, so invalid bases
	// would otherwise survive construction and only fail (or mis-route) later.
	apiBase, err := validateAgenticAPIBase(config.APIBase)
	if err != nil {
		return nil, err
	}
	modelName := strings.TrimSpace(config.ModelName)
	if modelName == "" {
		return nil, errors.New("modelapi model name is required")
	}
	if isAzureProvider(config.Provider) {
		return nil, ErrAgenticAzureUnsupported
	}
	if client == nil {
		client = NewStreamingHTTPClient()
	}
	// Enforce wire-level invariants after agenticopenai applies options/extra
	// fields (ExtraFields use JSON-set and would otherwise overwrite store).
	client = wrapClientWithResponsesGuards(client)

	apiKey, err := resolveAPIKey(ctx, secrets, config)
	if err != nil {
		// resolveAPIKey / secret openers must not embed plaintext secrets.
		return nil, err
	}

	mapped, err := mapAgenticOptions(config.Options)
	if err != nil {
		return nil, err
	}

	storeFalse := false
	parallelFalse := false
	// Zero retries: transport-level verification rejects must not be retried into
	// later fake/live turns (would reclassify USAGE_INVALID as tool-search errors).
	// Production Agentic also uses store=false; retries belong above this layer.
	maxRetries := 0
	cfg := &agenticopenai.ResponsesConfig{
		APIKey:            apiKey,
		BaseURL:           apiBase,
		Model:             modelName,
		HTTPClient:        client,
		Store:             &storeFalse,
		EnableAutoCache:   false,
		ParallelToolCalls: &parallelFalse,
		ByAzure:           false,
		MaxRetries:        &maxRetries,
	}
	if mapped.Temperature != nil {
		cfg.Temperature = mapped.Temperature
	}
	if mapped.TopP != nil {
		cfg.TopP = mapped.TopP
	}
	if mapped.MaxTokens != nil {
		cfg.MaxTokens = mapped.MaxTokens
	}
	if mapped.Reasoning != nil {
		cfg.Reasoning = mapped.Reasoning
	}

	inner, err := agenticopenai.NewResponsesModel(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &guardedAgenticModel{inner: inner}, nil
}

// WithPromptCacheKey returns a per-request model option that sets the OpenAI
// Responses prompt_cache_key. Callers should pass a deterministic key derived
// from the run snapshot. This does not use previous_response_id.
//
// Note: agenticopenai applies ExtraFields via JSON-set after structured fields,
// so a caller WithExtraFields({"prompt_cache_key": ...}) can still overwrite
// this option on the wire. Production platform keys must also use
// WithProtectedPromptCacheKey so the HTTP transport force-sets the key after
// ExtraFields.
func WithPromptCacheKey(key string) model.Option {
	return agenticopenai.WithResponsesPromptCacheKey(strings.TrimSpace(key))
}

// protectedPromptCacheKey is an unexported context key type so callers cannot
// forge the platform-owned prompt cache key via a plain string context key.
type protectedPromptCacheKey struct{}

// WithProtectedPromptCacheKey attaches a platform-owned prompt_cache_key to ctx.
// The Task 2 model wrapper overwrites any preexisting caller value with its
// configured key. responsesGuardTransport force-sets prompt_cache_key in the
// final JSON map after ExtraFields when this value is present.
// Empty keys clear the protected value (Task 1 path with no platform key).
func WithProtectedPromptCacheKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		// Explicit empty: remove by storing empty string so RoundTrip does not force.
		return context.WithValue(ctx, protectedPromptCacheKey{}, "")
	}
	return context.WithValue(ctx, protectedPromptCacheKey{}, key)
}

// ProtectedPromptCacheKeyFromContext returns the platform-owned key if present
// and non-empty. Used by the Responses HTTP transport guard.
func ProtectedPromptCacheKeyFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v := ctx.Value(protectedPromptCacheKey{})
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

// ValidateAgenticAPIBase is the exported form of the construction-time API base
// validator (used by frozen graph node model snapshots and model builders).
func ValidateAgenticAPIBase(raw string) (string, error) {
	return validateAgenticAPIBase(raw)
}

// validateAgenticAPIBase parses and validates a model API base before adapter
// construction.
//
// Accepts only absolute http/https URLs with a non-empty hostname (DNS, IPv4,
// or bracketed IPv6). Optional valid ports (1..65535), path prefixes, and
// trailing slashes are preserved (e.g. https://host/v1/, https://[::1]:8443/v1).
//
// Rejects relative URLs, unsupported schemes, malformed URLs, userinfo,
// query strings, fragments, opaque forms, empty hostnames (including
// hostless forms like https://:443/v1), invalid/out-of-range ports, and
// control characters in the host.
//
// Returned errors wrap ErrAgenticInvalidAPIBase (except empty base) and never
// echo the raw URL, userinfo, or other secret-bearing components.
func validateAgenticAPIBase(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("modelapi API base is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: malformed URL", ErrAgenticInvalidAPIBase)
	}
	if !u.IsAbs() {
		return "", fmt.Errorf("%w: must be an absolute http or https URL", ErrAgenticInvalidAPIBase)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: scheme must be http or https", ErrAgenticInvalidAPIBase)
	}
	// Opaque non-hierarchical forms (rare) are ambiguous for HTTP clients.
	if u.Opaque != "" {
		return "", fmt.Errorf("%w: opaque URL form is not allowed", ErrAgenticInvalidAPIBase)
	}
	if u.User != nil {
		// Do not echo userinfo (may contain credentials).
		return "", fmt.Errorf("%w: userinfo is not allowed", ErrAgenticInvalidAPIBase)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("%w: query string is not allowed on API base", ErrAgenticInvalidAPIBase)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("%w: fragment is not allowed", ErrAgenticInvalidAPIBase)
	}
	// Require a real hostname. Prefer Hostname() over Host so forms like
	// "https://:443/v1" (empty host, port-only) are rejected even though Host
	// is non-empty.
	host := u.Hostname()
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("%w: host is required", ErrAgenticInvalidAPIBase)
	}
	if hostHasInvalidRunes(host) {
		return "", fmt.Errorf("%w: host contains invalid characters", ErrAgenticInvalidAPIBase)
	}
	// When a port is explicitly present, it must be a valid TCP port 1..65535.
	if portStr := u.Port(); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("%w: invalid port", ErrAgenticInvalidAPIBase)
		}
	}
	// Ambiguous Host forms: reject if Host was non-empty solely as ":port"
	// without a hostname (already covered by Hostname empty check).
	return s, nil
}

// hostHasInvalidRunes rejects control characters and other non-printable
// runes in hostnames. Legitimate DNS labels, IPv4, and IPv6 (without brackets,
// as returned by url.Hostname) are accepted.
func hostHasInvalidRunes(host string) bool {
	for _, r := range host {
		if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

// guardedAgenticModel is the platform-owned model.AgenticModel boundary around
// agenticopenai.ResponsesModel. It preserves common Eino options while
// neutralizing call-time overrides of store / parallel_tool_calls /
// previous_response_id and scrubbing previous-response metadata from input.
type guardedAgenticModel struct {
	inner model.AgenticModel
}

func (g *guardedAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	// Fail closed on unsupported/malformed protocol surface and unpaired
	// function/tool-search results before any network call.
	if err := agenticmsg.ValidateConversation(input); err != nil {
		return nil, err
	}
	// Preserve context cancellation/deadlines; protected cache key (if any) is
	// already on ctx from the platform WrapModel wrapper, or absent for Task 1
	// direct callers.
	return g.inner.Generate(ctx, scrubAgenticInput(input), appendGuardOptions(opts)...)
}

func (g *guardedAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if err := agenticmsg.ValidateConversation(input); err != nil {
		return nil, err
	}
	return g.inner.Stream(ctx, scrubAgenticInput(input), appendGuardOptions(opts)...)
}

// appendGuardOptions appends force-options AFTER caller options so GetImplSpecificOptions
// precedence leaves store=false, parallel=false, and no previous_response_id head.
// WithExtraFields can still overwrite structured fields via JSON-set; the HTTP
// transport guard rewrites the outbound body for those cases.
func appendGuardOptions(opts []model.Option) []model.Option {
	out := make([]model.Option, 0, len(opts)+3)
	out = append(out, opts...)
	out = append(out,
		agenticopenai.WithResponsesStore(false),
		agenticopenai.WithResponsesParallelToolCalls(false),
		// Empty ID: populateCache only sets PreviousResponseID when non-empty.
		agenticopenai.WithHeadPreviousResponseID(""),
	)
	return out
}

// scrubAgenticInput returns a shallow-copied message list with previous-response
// continuation metadata removed so auto-cache discovery cannot reintroduce
// previous_response_id even if EnableAutoCache were ever flipped on.
func scrubAgenticInput(input []*schema.AgenticMessage) []*schema.AgenticMessage {
	if len(input) == 0 {
		return input
	}
	out := make([]*schema.AgenticMessage, len(input))
	for i, msg := range input {
		if msg == nil {
			continue
		}
		cp := *msg
		if msg.Extra != nil {
			extra := make(map[string]any, len(msg.Extra))
			for k, v := range msg.Extra {
				// Drop agenticopenai auto-cache marker if present.
				if k == "_eino_ext_agenticopenai_auto_cached" {
					continue
				}
				extra[k] = v
			}
			if len(extra) == 0 {
				cp.Extra = nil
			} else {
				cp.Extra = extra
			}
		}
		if msg.ResponseMeta != nil {
			meta := *msg.ResponseMeta
			if msg.ResponseMeta.OpenAIExtension != nil {
				ext := *msg.ResponseMeta.OpenAIExtension
				ext.ID = ""
				ext.PreviousResponseID = ""
				meta.OpenAIExtension = &ext
			}
			cp.ResponseMeta = &meta
		}
		out[i] = &cp
	}
	return out
}

// responsesGuardTransport rewrites outbound Responses API JSON bodies so that
// store=false, parallel_tool_calls=false, and previous_response_id is absent
// even when call-time WithExtraFields (JSON-set, last-write-wins) tries to
// override structured request fields. Invalid / non-object JSON fails closed
// without invoking the base transport.
type responsesGuardTransport struct {
	base http.RoundTripper
}

// rejectAgenticRedirect is the CheckRedirect hook for model-specific clients.
// Returning a non-ErrUseLastResponse error stops http.Client from following
// redirects with the original unguarded GetBody (307/308 preserve method+body).
func rejectAgenticRedirect(_ *http.Request, _ []*http.Request) error {
	return ErrAgenticHTTPRedirect
}

func wrapClientWithResponsesGuards(client *http.Client) *http.Client {
	if client == nil {
		client = NewStreamingHTTPClient()
	}
	// Shallow-copy so shared callers keep their original client intact.
	c := *client
	// Security contract: never follow redirects on the model-specific client.
	// http.Client can rebuild redirected requests from the original GetBody
	// before our transport rewrites them; rejecting redirects closes that gap.
	c.CheckRedirect = rejectAgenticRedirect
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*responsesGuardTransport); ok {
		return &c
	}
	c.Transport = &responsesGuardTransport{base: base}
	return &c
}

func (t *responsesGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req != nil && req.Method == http.MethodPost && req.Body != nil && isResponsesPath(req.URL.Path) {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		// Platform-owned cache key (if present) is force-set after ExtraFields.
		var platformCacheKey string
		if req.Context() != nil {
			if k, ok := ProtectedPromptCacheKeyFromContext(req.Context()); ok {
				platformCacheKey = k
			}
		}
		rewritten, err := enforceResponsesRequestInvariants(body, platformCacheKey)
		if err != nil {
			// Fail closed: do not call the underlying transport with unguarded
			// or non-rewritable bodies (invalid JSON, non-object JSON).
			return nil, err
		}
		// Rewrite the request that is actually sent. Method, URL, headers, and
		// context (including cancellation/deadlines) are preserved; Body /
		// ContentLength / GetBody are replaced with the guarded payload so safe
		// retries also re-send invariants.
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(rewritten))
		req.ContentLength = int64(len(rewritten))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(rewritten)), nil
		}
	}
	return base.RoundTrip(req)
}

func isResponsesPath(path string) bool {
	// openai-go posts to "{base}/responses"; tolerate trailing slash variants.
	p := strings.TrimSuffix(path, "/")
	return strings.HasSuffix(p, "/responses") || p == "responses"
}

// enforceResponsesRequestInvariants rewrites a Responses request JSON body.
// Invalid JSON and non-object JSON fail closed with ErrAgenticInvalidResponsesBody.
//
// When platformCacheKey is non-empty it is force-set as prompt_cache_key after
// any ExtraFields JSON-set, so callers cannot override the platform key via
// WithExtraFields, option order, or a caller context value.
// When platformCacheKey is empty (Task 1 / no protected key), prompt_cache_key
// is left as the adapter already set it (including From WithPromptCacheKey).
func enforceResponsesRequestInvariants(body []byte, platformCacheKey string) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: not a JSON object", ErrAgenticInvalidResponsesBody)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgenticInvalidResponsesBody, err)
	}
	if m == nil {
		// json.Unmarshal into map yields nil for JSON null.
		return nil, fmt.Errorf("%w: JSON null", ErrAgenticInvalidResponsesBody)
	}
	m["store"] = false
	m["parallel_tool_calls"] = false
	delete(m, "previous_response_id")
	if platformCacheKey != "" {
		// Force after ExtraFields — last write on the final map wins on the wire.
		m["prompt_cache_key"] = platformCacheKey
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("%w: re-marshal: %v", ErrAgenticInvalidResponsesBody, err)
	}
	return out, nil
}

// agenticMappedOptions holds validated Responses fields derived from
// modelconfig.Options JSON.
type agenticMappedOptions struct {
	Temperature *float32
	TopP        *float32
	MaxTokens   *int
	Reasoning   *responses.ReasoningParam
}

// knownAgenticOptionKeys are the only modelconfig.Options keys accepted by the
// Agentic adapter. Unknown keys fail closed (no silent passthrough).
//
// apiVersion/api_version are recognized only so they can be rejected as
// inapplicable (Azure is unsupported on this adapter).
var knownAgenticOptionKeys = map[string]struct{}{
	"reasoningEffort":     {},
	"temperature":         {},
	"topP":                {},
	"top_p":               {},
	"maxTokens":           {},
	"max_tokens":          {},
	"parallelToolCalls":   {},
	"parallel_tool_calls": {},
	"apiVersion":          {},
	"api_version":         {},
}

// aliasPairs lists camelCase/snake_case pairs that must not both be present.
var agenticOptionAliasPairs = [][2]string{
	{"topP", "top_p"},
	{"maxTokens", "max_tokens"},
	{"parallelToolCalls", "parallel_tool_calls"},
	{"apiVersion", "api_version"},
}

func mapAgenticOptions(raw json.RawMessage) (agenticMappedOptions, error) {
	var out agenticMappedOptions
	if len(raw) == 0 || string(raw) == "null" {
		// Omitted options: default high reasoning effort so gpt-5-class
		// gateways that only surface reasoning text at high still work.
		// Invalid explicit values fail closed below.
		out.Reasoning = &responses.ReasoningParam{Effort: responses.ReasoningEffortHigh}
		return out, nil
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return out, fmt.Errorf("modelapi agentic options: invalid JSON: %w", err)
	}
	for key := range asMap {
		if _, ok := knownAgenticOptionKeys[key]; !ok {
			return out, fmt.Errorf("modelapi agentic options: unknown option %q", key)
		}
	}
	for _, pair := range agenticOptionAliasPairs {
		_, a := asMap[pair[0]]
		_, b := asMap[pair[1]]
		if a && b {
			return out, fmt.Errorf("%w: %s and %s", ErrAgenticOptionAliasConflict, pair[0], pair[1])
		}
	}

	var opts struct {
		ReasoningEffort   *string  `json:"reasoningEffort"`
		Temperature       *float64 `json:"temperature"`
		TopP              *float64 `json:"topP"`
		TopPSnake         *float64 `json:"top_p"`
		MaxTokens         *int     `json:"maxTokens"`
		MaxTokensSnake    *int     `json:"max_tokens"`
		ParallelToolCalls *bool    `json:"parallelToolCalls"`
		ParallelSnake     *bool    `json:"parallel_tool_calls"`
		APIVersion        *string  `json:"apiVersion"`
		APIVersionSnake   *string  `json:"api_version"`
	}
	if err := json.Unmarshal(raw, &opts); err != nil {
		return out, fmt.Errorf("modelapi agentic options: invalid types: %w", err)
	}

	// apiVersion is always inapplicable here (Azure unsupported).
	if opts.APIVersion != nil || opts.APIVersionSnake != nil {
		return out, ErrAgenticAPIVersionUnsupported
	}

	effort, apply, err := agenticReasoningEffort(opts.ReasoningEffort)
	if err != nil {
		return out, err
	}
	if apply {
		out.Reasoning = &responses.ReasoningParam{Effort: effort}
	}

	if opts.Temperature != nil {
		t := float32(*opts.Temperature)
		if t < 0 || t > 2 {
			return out, fmt.Errorf("modelapi agentic options: temperature out of range [0,2]")
		}
		out.Temperature = &t
	}
	topP := opts.TopP
	if topP == nil {
		topP = opts.TopPSnake
	}
	if topP != nil {
		v := float32(*topP)
		if v < 0 || v > 1 {
			return out, fmt.Errorf("modelapi agentic options: topP out of range [0,1]")
		}
		out.TopP = &v
	}
	maxTok := opts.MaxTokens
	if maxTok == nil {
		maxTok = opts.MaxTokensSnake
	}
	if maxTok != nil {
		if *maxTok <= 0 {
			return out, fmt.Errorf("modelapi agentic options: maxTokens must be > 0")
		}
		out.MaxTokens = maxTok
	}
	parallel := opts.ParallelToolCalls
	if parallel == nil {
		parallel = opts.ParallelSnake
	}
	if parallel != nil && *parallel {
		return out, ErrAgenticParallelToolCallsFixed
	}
	// Explicit false is accepted; value is always forced false on the config.
	return out, nil
}

// agenticReasoningEffort maps modelconfig reasoningEffort onto Responses
// ReasoningEffort. Returns (effort, apply, err).
//
//   - key omitted / null options → default high (apply=true)
//   - "none"/"off"/"disabled"/"false" → skip Reasoning param (apply=false)
//   - known levels → apply
//   - unknown value → fail closed (unlike classic adapter silent default)
func agenticReasoningEffort(raw *string) (responses.ReasoningEffort, bool, error) {
	if raw == nil {
		return responses.ReasoningEffortHigh, true, nil
	}
	switch strings.ToLower(strings.TrimSpace(*raw)) {
	case "low":
		return responses.ReasoningEffortLow, true, nil
	case "medium":
		return responses.ReasoningEffortMedium, true, nil
	case "high":
		return responses.ReasoningEffortHigh, true, nil
	case "minimal":
		return responses.ReasoningEffortMinimal, true, nil
	case "xhigh":
		return responses.ReasoningEffortXhigh, true, nil
	case "none", "off", "disabled", "false":
		return "", false, nil
	default:
		return "", false, fmt.Errorf("modelapi agentic options: invalid reasoningEffort %q", strings.TrimSpace(*raw))
	}
}
