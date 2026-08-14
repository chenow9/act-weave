package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// AgenticModelBuilder constructs a production model.AgenticModel for one frozen
// model config. Production uses modelapi.NewOpenAIAgenticModel (store=false,
// ParallelToolCalls=false, no auto-cache). No classic Chat Completions fallback.
type AgenticModelBuilder func(ctx context.Context, cfg modelconfig.Config) (model.AgenticModel, error)

// ErrAgenticModelSnapshotRequired is returned when run.ModelSnapshot is missing
// or cannot be used as the sole model identity for Agentic initial construction.
var ErrAgenticModelSnapshotRequired = errors.New("AGENTIC_MODEL_SNAPSHOT_REQUIRED")

// ErrAgenticGraphSnapshotRequired is returned when agent_graph_snapshot.v1 is
// missing, empty, unversioned, or fails integrity validation. Live topology is
// never consulted to recover.
var ErrAgenticGraphSnapshotRequired = errors.New("AGENTIC_GRAPH_SNAPSHOT_REQUIRED")

// ErrAgenticProviderTupleUnsupported is returned when frozen provider is not in
// the exact OpenAI Responses Agentic implementation set, or is inconsistent with
// the verified capability protocol/adapter tuple.
var ErrAgenticProviderTupleUnsupported = errors.New("AGENTIC_PROVIDER_TUPLE_UNSUPPORTED")

// ErrAgenticAgentSnapshotRequired is returned when run.AgentSnapshot (the
// agent-binding.v1 freeze) is missing, malformed, or disagrees with the run
// agent / run.ModelSnapshot identity. Live agent binding is never consulted.
var ErrAgenticAgentSnapshotRequired = errors.New("AGENTIC_AGENT_SNAPSHOT_REQUIRED")

// ErrAgenticCapabilitySnapshotRequired is returned when run.CapabilitySnapshot
// fails the Agentic initial strict contract. Malformed entries fail the run
// instead of being silently dropped into a different executable tool set.
var ErrAgenticCapabilitySnapshotRequired = errors.New("AGENTIC_CAPABILITY_SNAPSHOT_REQUIRED")

// ErrAgenticPromptRevisionMismatch is returned when the frozen prompt revision
// cannot be loaded or its live content hash drifted from the freeze.
var ErrAgenticPromptRevisionMismatch = errors.New("AGENTIC_PROMPT_REVISION_MISMATCH")

// ContextAssemblyInserter persists immutable context assembly manifests.
// *execution.ContextAssemblyRepository implements this in production.
type ContextAssemblyInserter interface {
	InsertImmutable(ctx context.Context, rec execution.ContextAssemblyRecord) (execution.ContextAssemblyRecord, error)
}

const fallbackInstruction = "You are a helpful workspace agent. Answer clearly and concisely."

// Supported frozen Provider values for the OpenAI Responses Agentic path (exact).
// Casing/aliases are not normalized: "OpenAI", " openai ", "azure" fail closed.
// "openai-compatible" is the platform configuration enum used in production HTTP.
// Azure variants are rejected (modelapi.ErrAgenticAzureUnsupported).
var agenticSupportedProviders = map[string]struct{}{
	"openai":            {},
	"openai-compatible": {},
}

// requireFrozenModelConfig selects model identity only from run.ModelSnapshot via
// the strict raw JSON boundary (parseModelSnapshotStrict). No TrimSpace success
// path, no live/snapRT fallback.
//
// parseModelSnapshotStrict also applies modelapi.ValidateAgenticAPIBase to the
// root apiBase, so a hostile base (file://, ftp://, userinfo, query) fails here
// — before assembly manifest / model / agent / sink construction.
func requireFrozenModelConfig(modelSnapshot json.RawMessage, workspaceID string) (modelconfig.Config, error) {
	if len(modelSnapshot) == 0 || string(modelSnapshot) == "null" || string(modelSnapshot) == "{}" {
		return modelconfig.Config{}, fmt.Errorf("%w: model snapshot missing", ErrAgenticModelSnapshotRequired)
	}
	return parseModelSnapshotStrict(modelSnapshot, workspaceID)
}

// requireVerifiedAgenticSnapshot fails closed unless the frozen model snapshot
// carries a well-formed VERIFIED capability document bound to the supported
// OpenAI Responses provider tuple. Catalog contents and disclosure mode are
// not consulted here. Never falls back to live capabilities.
func requireVerifiedAgenticSnapshot(modelSnapshot json.RawMessage, cfg modelconfig.Config) error {
	if len(modelSnapshot) == 0 || string(modelSnapshot) == "null" || string(modelSnapshot) == "{}" {
		return fmt.Errorf("%w: model snapshot missing", ErrAgenticModelSnapshotRequired)
	}
	// Provider must be an exact supported enum before accepting capability claims.
	if err := requireSupportedAgenticProvider(cfg.Provider); err != nil {
		return err
	}
	// Prefer capabilities already bound on cfg from strict parse; fall back to
	// raw field only if present (still re-validated by ParseAgenticCapabilities).
	raw := cfg.AgenticCapabilities
	if modelconfig.IsUnverifiedAgenticCapabilities(raw) {
		raw = extractAgenticCapabilitiesFromModelSnapshot(modelSnapshot)
	}
	if modelconfig.IsUnverifiedAgenticCapabilities(raw) {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	doc, _, err := modelconfig.ParseAgenticCapabilities(raw)
	if err != nil {
		// Structural capability errors are snapshot-required for initial Agentic.
		return fmt.Errorf("%w: agenticCapabilities", ErrAgenticModelSnapshotRequired)
	}
	switch doc.SchemaVersion {
	case modelconfig.AgenticCapabilitiesSchemaV1, modelconfig.AgenticCapabilitiesSchemaV2:
	default:
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if doc.Protocol != modelconfig.AgenticProtocolOpenAIResponsesV1 {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if doc.VerifiedAdapter != modelconfig.VerifiedAdapterAgenticOpenAIV022 {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if !doc.Streaming || !doc.Usage {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	// Lock identity uses the persisted CAS relation (verifiedLockVersion ==
	// lockVersion-1), shared with modelconfig.Repository read validation. Plain
	// equality here accepted only synthetic fixtures and rejected every row the
	// real verification flow can produce.
	if !modelconfig.AgenticCapabilityLockMatches(doc, cfg.LockVersion) {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if doc.VerifiedConfigDigest == "" {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	want := modelconfig.WireConfigDigest(cfg)
	if doc.VerifiedConfigDigest != want {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	return nil
}

// frozenCapsAreNative reports whether the frozen capability document is native
// client-search (v1, or v2 toolCalling=native_client_search). Parse failure is
// not native.
func frozenCapsAreNative(cfg modelconfig.Config) bool {
	doc, _, err := modelconfig.ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil {
		return false
	}
	return doc.ToolCalling == modelconfig.ToolCallingNativeClientSearch
}

// requireSupportedAgenticProvider enforces the exact frozen Provider values that
// bind to openai-responses-v1 + agenticopenai/v0.2.2. No case fold / alias map.
func requireSupportedAgenticProvider(provider string) error {
	if provider == "" || provider != strings.TrimSpace(provider) {
		return fmt.Errorf("%w: provider must be exact canonical value", ErrAgenticProviderTupleUnsupported)
	}
	// Azure is never part of the OpenAI Responses agentic tuple.
	switch strings.ToLower(provider) {
	case "azure", "azure_openai", "azure-openai":
		return fmt.Errorf("%w: azure provider is not supported", ErrAgenticProviderTupleUnsupported)
	}
	if _, ok := agenticSupportedProviders[provider]; !ok {
		return fmt.Errorf("%w: provider %q is not in the OpenAI Responses agentic set", ErrAgenticProviderTupleUnsupported, provider)
	}
	return nil
}

func isCanonicalModelConfigUUID(v string) bool {
	id, err := uuid.Parse(v)
	if err != nil {
		return false
	}
	return id.String() == v
}

// rawJSONStringField returns a top-level string field without whitespace normalize.
func rawJSONStringField(raw json.RawMessage, field string) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	v, ok := doc[field]
	if !ok {
		return "", fmt.Errorf("missing field %s", field)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	return s, nil
}

func extractAgenticCapabilitiesFromModelSnapshot(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return json.RawMessage(`{}`)
	}
	var doc struct {
		AgenticCapabilities json.RawMessage `json:"agenticCapabilities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return json.RawMessage(`{}`)
	}
	if len(doc.AgenticCapabilities) == 0 {
		return json.RawMessage(`{}`)
	}
	return doc.AgenticCapabilities
}

// requireFrozenAgentGraph validates run.AgentGraphSnapshot as the sole topology
// authority for Agentic turns (Task 4A/5).
//
// Contract (agent_graph_snapshot.v1 via agentdelegation.ParseSnapshot):
//   - Missing / null / {} / unversioned / integrity failure → ErrAgenticGraphSnapshotRequired
//     (never ListEnabledEdges / live topology to recover).
//   - Valid freeze (empty or with edges/remotes) → returned for attachAgenticDelegationTools.
//
// Never calls classic live-topology attach or classic model builders.
//
// cfg is the already-strict-parsed run.ModelSnapshot. The graph is not an
// independent island: its root node must describe exactly the model identity
// that actually drives execution.
func requireFrozenAgentGraph(
	workspaceID string,
	run execution.AgentRun,
	cfg modelconfig.Config,
) (*agentdelegation.GraphSnapshotV1, error) {
	// No TrimSpace of the freeze blob — outer whitespace fails closed in strict parse.
	rawSnap := run.AgentGraphSnapshot
	if len(rawSnap) == 0 || string(rawSnap) == "null" || string(rawSnap) == "{}" {
		return nil, fmt.Errorf("%w: agent_graph_snapshot.v1 missing (explicit empty freeze required)", ErrAgenticGraphSnapshotRequired)
	}
	parsed, perr := agentdelegation.ParseSnapshot(workspaceID, rawSnap)
	if perr != nil {
		// Never embed raw snapshot / secrets in the public error surface.
		return nil, fmt.Errorf("%w: agent_graph_snapshot.v1 invalid", ErrAgenticGraphSnapshotRequired)
	}
	if parsed == nil {
		return nil, fmt.Errorf("%w: agent_graph_snapshot.v1 empty after parse", ErrAgenticGraphSnapshotRequired)
	}
	// Root must match the run agent (cross-identity fail closed).
	runAgentID := strings.TrimSpace(run.AgentID)
	root := strings.TrimSpace(parsed.RootAgentID)
	if root == "" || runAgentID == "" || root != runAgentID {
		return nil, fmt.Errorf("%w: graph rootAgentId does not match run agent", ErrAgenticGraphSnapshotRequired)
	}
	if err := requireGraphRootMatchesFrozenModel(parsed, runAgentID, cfg); err != nil {
		return nil, err
	}
	// Post-freeze live topology is ignored; edges/remotes in the freeze are authoritative.
	return parsed, nil
}

// requireGraphRootMatchesFrozenModel binds the graph freeze to the model freeze.
// Both documents are attacker-reachable independently; agreement between them is
// what makes the three-layer lock matrix meaningful for the executed run.
func requireGraphRootMatchesFrozenModel(
	parsed *agentdelegation.GraphSnapshotV1,
	runAgentID string,
	cfg modelconfig.Config,
) error {
	var rootNode *agentdelegation.GraphNodeSnapshot
	for i := range parsed.Nodes {
		if strings.TrimSpace(parsed.Nodes[i].AgentID) == runAgentID {
			rootNode = &parsed.Nodes[i]
			break
		}
	}
	if rootNode == nil {
		return fmt.Errorf("%w: graph root node missing for run agent", ErrAgenticGraphSnapshotRequired)
	}
	if strings.TrimSpace(rootNode.ModelConfigID) != strings.TrimSpace(cfg.ID) {
		return fmt.Errorf(
			"%w: graph root modelConfigId does not match run.ModelSnapshot id", ErrAgenticGraphSnapshotRequired)
	}
	if rootNode.ModelConfigLockVer != cfg.LockVersion {
		return fmt.Errorf(
			"%w: graph root modelConfigLockVersion does not match run.ModelSnapshot lockVersion",
			ErrAgenticGraphSnapshotRequired)
	}
	// The nested node modelSnapshot is what child paths would execute; it must
	// name the same config + lock as the root run. parseSnapshotStrict already
	// tied nested lock == node lock, so id equality closes the remaining gap.
	nestedID, err := rawJSONStringField(rootNode.ModelSnapshot, "id")
	if err != nil || strings.TrimSpace(nestedID) != strings.TrimSpace(cfg.ID) {
		return fmt.Errorf(
			"%w: graph root modelSnapshot id does not match run.ModelSnapshot id", ErrAgenticGraphSnapshotRequired)
	}
	nestedAgentID, err := rawJSONStringField(rootNode.AgentSnapshot, "agentId")
	if err != nil || strings.TrimSpace(nestedAgentID) != runAgentID {
		return fmt.Errorf(
			"%w: graph root agentSnapshot agentId does not match run agent", ErrAgenticGraphSnapshotRequired)
	}
	return nil
}

// requireFrozenInstruction resolves the system prompt from the frozen prompt
// revision and verifies the live revision content hash still equals the freeze.
//
// Same comparison semantics as the child delegation path
// (delegation.go loadChildAgentParts): revision must exist, body must be
// non-empty, and hash comparison is case-insensitive hex. Unlike the child path
// the freeze hash is mandatory here, because run.AgentSnapshot is produced by
// SnapshotAgentRun from agent_prompt_revisions.content_sha256 (NOT NULL).
func (b *Bridge) requireFrozenInstruction(
	ctx context.Context,
	workspaceID, agentID string,
	binding runAgentBinding,
) (string, error) {
	if b == nil || b.agents == nil {
		return "", fmt.Errorf("%w: prompt revision reader unavailable", ErrAgenticPromptRevisionMismatch)
	}
	revisions, err := b.agents.ListPromptRevisions(ctx, workspaceID, agentID)
	if err != nil {
		return "", fmt.Errorf("%w: list prompt revisions: %v", ErrAgenticPromptRevisionMismatch, err)
	}
	for _, rev := range revisions {
		if rev.ID != binding.PromptRevisionID {
			continue
		}
		prompt := strings.TrimSpace(rev.SystemPrompt)
		if prompt == "" {
			return "", fmt.Errorf("%w: frozen prompt revision is empty", ErrAgenticPromptRevisionMismatch)
		}
		liveHash := strings.TrimSpace(rev.ContentSHA256)
		if liveHash == "" {
			return "", fmt.Errorf("%w: live prompt revision has no content hash", ErrAgenticPromptRevisionMismatch)
		}
		if !strings.EqualFold(liveHash, binding.PromptRevisionHash) {
			return "", fmt.Errorf("%w: frozen prompt revision hash drift", ErrAgenticPromptRevisionMismatch)
		}
		return prompt, nil
	}
	return "", fmt.Errorf("%w: frozen prompt revision not found", ErrAgenticPromptRevisionMismatch)
}

// checkLiveModelDisabledKillSwitch reads live model status for the same canonical
// config ID only. It never supplies provider/options/capabilities/catalog identity.
// Get errors are ignored (cannot block a valid freeze). Only StatusDisabled fails.
func (b *Bridge) checkLiveModelDisabledKillSwitch(ctx context.Context, workspaceID, modelConfigID string) error {
	if b == nil || b.models == nil {
		return nil
	}
	id := strings.TrimSpace(modelConfigID)
	if id == "" || !isCanonicalModelConfigUUID(id) {
		return nil
	}
	live, err := b.models.Get(ctx, workspaceID, id)
	if err != nil {
		// Live read failure must not alter a valid frozen initial run.
		return nil
	}
	if live.Status == modelconfig.StatusDisabled {
		return errors.New("model config is disabled")
	}
	return nil
}

// requireAgenticContextPolicy rejects legacy/missing context policy for new
// Agentic initial runs. Returns a validated non-legacy resolved snapshot with
// known model limits and tokenizer profile.
func requireAgenticContextPolicy(run execution.AgentRun) (sessioncontext.ResolvedSnapshot, error) {
	if run.SnapshotSchemaVersion != execution.RunSnapshotSchemaV2 {
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextSnapshotUnsupported)
	}
	if sessioncontext.IsLegacySnapshot(run.ContextPolicySnapshot) {
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	resolved, err := sessioncontext.ParseResolvedSnapshot(run.ContextPolicySnapshot)
	if err != nil {
		if errors.Is(err, sessioncontext.ErrUnsupportedSnapshot) {
			return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextSnapshotUnsupported)
		}
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if resolved.Mode == sessioncontext.ModeLegacy || resolved.Mode == "" {
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	if resolved.ModelContextWindowTokens <= 0 || strings.TrimSpace(resolved.TokenizerProfile) == "" {
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	if resolved.OutputReserveTokens < 0 || resolved.SafetyMarginTokens < 0 {
		return sessioncontext.ResolvedSnapshot{}, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	return resolved, nil
}

// agenticFrozenPlan is everything the Agentic runtime derives from a run's
// frozen documents, before any turn-specific work.
//
// It exists so the initial turn and a resume produce the identical agent by
// construction. A resume restores adk state that the paused agent wrote, so a
// rebuild that differs — a different catalog, a different prompt cache key, an
// Instruction on one side only — changes the wire and the cache identity of a
// conversation that is already half-executed. Two hand-maintained copies of this
// sequence would drift on the first edit; one function cannot.
type agenticFrozenPlan struct {
	configuredAgent   agent.Agent
	cfg               modelconfig.Config
	binding           runAgentBinding
	frozenCaps        []chatruntime.SnapshotCapability
	policy            sessioncontext.ResolvedSnapshot
	graph             *agentdelegation.GraphSnapshotV1
	tools             []tool.BaseTool
	catalog           *einoruntime.ToolCatalogSnapshot
	instruction       string
	promptCacheKey    string
	hasToolsOrCatalog bool
	delBudget         *agentdelegation.Budget
	toolSearchMode    einoruntime.ToolSearchMode
	toolCalling       string
}

// planAgenticRun performs the frozen-identity validation shared by every Agentic
// turn, in a fixed order, and derives the executable tools/catalog/instruction.
//
// Side-effect order (fail closed) — frozen identity before any live model read:
//  1. load agent (status/binding id only)
//  2. frozen model snapshot + provider↔protocol↔adapter tuple + agentic capability
//  3. frozen agent_graph_snapshot.v1 (authoritative empty/nonempty; no live edges)
//  4. optional same-id DISABLED kill-switch via models.Get (never supplies identity)
//  5. frozen context policy + pipeline tools/catalog from capability snapshot
//
// Nothing here builds a model, an agent, a sink or a manifest: callers decide
// what to do between validation and construction (the initial turn must persist
// its assembly manifest first; a resume has no assembly).
func (b *Bridge) planAgenticRun(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
) (*agenticFrozenPlan, error) {
	// Agent status + binding id only (not model config identity).
	configuredAgent, err := b.agents.Get(ctx, job.WorkspaceID, run.AgentID)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	if configuredAgent.Status != agent.StatusActive {
		return nil, errors.New("agent is not active")
	}

	// 1–2) Frozen model snapshot + full Agentic provider tuple (no live models.Get yet).
	cfg, err := requireFrozenModelConfig(run.ModelSnapshot, job.WorkspaceID)
	if err != nil {
		return nil, err
	}
	// Cross-config: snapshot model id must match the agent-bound model when both present.
	if mid := strings.TrimSpace(configuredAgent.ModelConfigID); mid != "" && mid != cfg.ID {
		return nil, fmt.Errorf("%w: model snapshot id does not match agent binding", ErrAgenticModelSnapshotRequired)
	}
	if err := requireVerifiedAgenticSnapshot(run.ModelSnapshot, cfg); err != nil {
		return nil, err
	}

	// 2b) Frozen agent binding (run.AgentSnapshot) cross-bound to run agent and to
	// the frozen model snapshot. Nothing downstream may read this document loosely.
	binding, err := requireFrozenAgentBinding(run, cfg)
	if err != nil {
		return nil, err
	}

	// 3) Frozen agent graph is the only topology authority (no ListEnabledEdges),
	// and its root node must agree with the frozen model identity.
	graph, err := requireFrozenAgentGraph(job.WorkspaceID, run, cfg)
	if err != nil {
		return nil, err
	}

	// 3b) Frozen capability snapshot must satisfy the producer contract exactly
	// before any PipelineTool / catalog digest is derived from it.
	frozenCaps, err := parseRunCapabilitySnapshotStrict(run.CapabilitySnapshot)
	if err != nil {
		return nil, err
	}

	// 4) Same-identity DISABLED kill-switch only. Get errors / other statuses never
	// alter frozen identity or invent capabilities.
	if err := b.checkLiveModelDisabledKillSwitch(ctx, job.WorkspaceID, cfg.ID); err != nil {
		return nil, err
	}

	// Context policy required for hard preflight (no legacy bypass). A resume has
	// no assembly to preflight, but the document is still validated here so both
	// turns agree on which frozen document is authoritative and so the order of
	// rejections cannot depend on which turn is running.
	policy, err := requireAgenticContextPolicy(run)
	if err != nil {
		return nil, err
	}

	// 5) Capability tools from the immutable snapshot, then Typed AgentTool / A2A
	// from the frozen graph (never classic live-topology attach).
	pendingKey := pendingConfirmKey(job.WorkspaceID, job.RunID)
	b.clearPending(pendingKey)
	tools, err := b.buildPipelineToolsFrom(ctx, job, run, pendingKey, frozenCaps)
	if err != nil {
		return nil, err
	}
	tools, delBudget, err := b.attachAgenticDelegationTools(ctx, job, run, tools, pendingKey, graph)
	if err != nil {
		return nil, err
	}

	tools, catalogFlags := b.maybeInjectOutboundPublish(ctx, job, run, tools, frozenCaps, cfg)

	catalog, err := buildFrozenToolCatalogStrict(ctx, tools, frozenCaps, catalogFlags)
	if err != nil {
		return nil, err
	}

	// Instruction comes from the frozen prompt revision only (never live
	// CurrentPromptRevisionID), and only after its content hash matches the freeze.
	instruction, err := b.requireFrozenInstruction(ctx, job.WorkspaceID, run.AgentID, binding)
	if err != nil {
		return nil, err
	}
	// KD-17: A2UI rules go on the frozen system prompt once, before the cache
	// key and assembly. Resume rebuilds the same plan; AppendPromptRules is
	// idempotent. BuildAgenticAgent.Instruction stays empty so adk does not
	// prepend a second copy on the wire.
	if sessioncontext.EnableA2UIFromSnapshot(run.ContextPolicySnapshot) {
		instruction = a2ui.AppendPromptRules(instruction)
	}
	if shouldAppendOutboundPrompt(catalogFlags) {
		instruction = aapfile.AppendOutboundPromptRules(instruction)
	}

	mode, calling, err := b.resolveFrozenDisclosure(job.WorkspaceID, cfg, catalog)
	if err != nil {
		return nil, err
	}
	promptCacheKey, err := buildRunPromptCacheKey(cfg, instruction, catalog, mode)
	if err != nil {
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	return &agenticFrozenPlan{
		configuredAgent:   configuredAgent,
		cfg:               cfg,
		binding:           binding,
		frozenCaps:        frozenCaps,
		policy:            policy,
		graph:             graph,
		tools:             tools,
		catalog:           catalog,
		instruction:       instruction,
		promptCacheKey:    promptCacheKey,
		hasToolsOrCatalog: len(tools) > 0 || (catalog != nil && catalog.Len() > 0),
		delBudget:         delBudget,
		toolSearchMode:    mode,
		toolCalling:       calling,
	}, nil
}

// buildAgenticAgentFromPlan builds the model and the typed agent from a plan.
//
// Both turns go through here so the agent a resume restores into is the agent
// that paused. Instruction is deliberately empty: the frozen system prompt is
// already the leading message of the assembled list, which is the audited
// description of the wire (SystemPromptHash plus a single out-of-band system term
// in EstimateAgenticRequest). adk prepends a non-empty Instruction on top of the
// input messages, so setting it here would put the system prompt on the wire
// twice while the manifest and the preflight estimate still count one.
func (b *Bridge) buildAgenticAgentFromPlan(
	ctx context.Context,
	run execution.AgentRun,
	plan *agenticFrozenPlan,
) (adk.TypedAgent[*schema.AgenticMessage], error) {
	agenticModel, err := b.buildAgenticModel(ctx, plan.cfg)
	if err != nil {
		return nil, fmt.Errorf("build agentic model: %w", err)
	}
	name := "agent-" + strings.TrimSpace(run.AgentID)
	desc := "workspace agent"
	if n := strings.TrimSpace(plan.configuredAgent.Name); n != "" {
		name = n
	}
	if d := strings.TrimSpace(plan.configuredAgent.RoleDescription); d != "" {
		desc = d
	}
	clientVerified, fcVerified := disclosureVerifiedFlags(plan.toolSearchMode, plan.hasToolsOrCatalog)
	built, err := einoruntime.BuildAgenticAgent(ctx, einoruntime.AgenticAgentBuildConfig{
		Name:                     name,
		Description:              desc,
		Model:                    agenticModel,
		Tools:                    plan.tools,
		Catalog:                  plan.catalog,
		MaxIterations:            einoruntime.DefaultMaxIterations,
		MaxToolInvocations:       b.maxTools,
		ToolSearchMode:           plan.toolSearchMode,
		ClientToolSearchVerified: clientVerified,
		FunctionCallingVerified:  fcVerified,
		PromptCacheKey:           plan.promptCacheKey,
		// Instruction deliberately empty: frozen system prompt is the leading
		// assembled message. Children under AgentTool set Instruction themselves.
	})
	if err != nil {
		return nil, fmt.Errorf("build agentic agent: %w", err)
	}
	return built, nil
}

// driveAgenticInitial is the Task 4A production initial Chat path: AgenticModel +
// Typed Agent + frozen catalog + deferred-aware assembly + deterministic cache key.
//
// Validation and derivation happen in planAgenticRun; this function adds the
// initial-turn work: assemble + estimate + hard preflight + persist the manifest,
// all of it before the model, the agent, the sink or the provider exist.
func (b *Bridge) driveAgenticInitial(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
) (text string, streamMessageID string, err error) {
	if b.buildAgenticModel == nil {
		return "", "", errors.New("chatruntimebridge: agentic model builder is not configured")
	}
	if b.agenticEngine == nil {
		return "", "", errors.New("chatruntimebridge: agentic engine is not configured")
	}
	if b.assemblies == nil {
		// Manifest persistence is mandatory for every successful Agentic initial.
		return "", "", execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	ctx = withDisclosureAssemblyObserve(ctx)
	plan, err := b.planAgenticRun(ctx, job, run)
	if err != nil {
		return "", "", err
	}

	// 6) Assemble + estimate + hard preflight + persist agentic manifest BEFORE model/sink/provider.
	ctx = withFrozenDisclosure(ctx, frozenDisclosure{
		Mode: plan.toolSearchMode, ToolCalling: plan.toolCalling,
	})
	messages, msgErr := b.buildInitialAgenticMessages(
		ctx, job, run, plan.configuredAgent, plan.instruction, plan.catalog, plan.policy, plan.toolSearchMode)
	if msgErr != nil {
		return "", "", msgErr
	}

	// 7) Model + agent + sink only after preflight/manifest succeeded.
	built, err := b.buildAgenticAgentFromPlan(ctx, run, plan)
	if err != nil {
		return "", "", err
	}
	observeDisclosureAssembly(ctx, plan.toolSearchMode, plan.toolCalling)

	ctx = withDelegationRunContext(ctx, job, run, plan.delBudget)
	return b.runAgenticTurn(ctx, job, run, built, func(
		turnCtx context.Context,
		agent adk.TypedAgent[*schema.AgenticMessage],
		projector einoruntime.ProtocolProjector,
	) (*einoruntime.AgenticRunResult, error) {
		return b.agenticEngine.Run(turnCtx, agent, einoruntime.AgenticRunInput{
			WorkspaceID: job.WorkspaceID,
			RunID:       job.RunID,
			Messages:    messages,
			Projector:   projector,
		})
	})
}

// buildFrozenToolCatalog freezes an immutable ToolCatalog from executable tools
// and capability snapshot metadata. All business tools are deferred.
func buildFrozenToolCatalog(
	ctx context.Context,
	tools []tool.BaseTool,
	capabilitySnapshot json.RawMessage,
) (*einoruntime.ToolCatalogSnapshot, error) {
	caps, err := chatruntime.ParseCapabilitySnapshot(capabilitySnapshot)
	if err != nil {
		caps = nil
	}
	return buildFrozenToolCatalogStrict(ctx, tools, caps, nil)
}

// buildFrozenToolCatalogStrict is the Agentic initial form: the capability list
// has already passed parseRunCapabilitySnapshotStrict, so catalog kind/ids come
// from a freeze that was validated, not from a best-effort re-parse.
func buildFrozenToolCatalogStrict(
	ctx context.Context,
	tools []tool.BaseTool,
	capabilities []chatruntime.SnapshotCapability,
	flags map[string]catalogEntryFlags,
) (*einoruntime.ToolCatalogSnapshot, error) {
	if len(tools) == 0 {
		return einoruntime.BuildToolCatalog(ctx, nil)
	}
	capByName := map[string]chatruntime.SnapshotCapability{}
	for _, c := range capabilities {
		name := strings.TrimSpace(c.CallableName)
		if name != "" {
			capByName[name] = c
		}
	}
	inputs := make([]einoruntime.ToolCatalogBuildEntry, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			return nil, fmt.Errorf("%w: nil tool", einoruntime.ErrToolCatalogInvalid)
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			return nil, fmt.Errorf("%w: tool Info: %v", einoruntime.ErrToolCatalogInvalid, err)
		}
		name := strings.TrimSpace(info.Name)
		entry := einoruntime.ToolCatalogBuildEntry{
			Tool:     t,
			Exposure: einoruntime.ToolExposureDeferred,
			Kind:     einoruntime.ToolKindTool,
		}
		if cap, ok := capByName[name]; ok {
			entry.CapabilityID = strings.TrimSpace(cap.CapabilityID)
			entry.RevisionID = strings.TrimSpace(cap.ReleaseID)
			kind := strings.ToUpper(strings.TrimSpace(cap.Kind))
			switch kind {
			case "WORKFLOW":
				entry.Kind = einoruntime.ToolKindWorkflow
			case "AGENT":
				entry.Kind = einoruntime.ToolKindAgent
			case "A2A":
				entry.Kind = einoruntime.ToolKindA2A
			default:
				entry.Kind = einoruntime.ToolKindTool
			}
		} else if kind := catalogKindForTool(t); kind != "" {
			// Graph-edge AgentTool / A2A outbound are not capability releases.
			entry.Kind = kind
		}
		if flag, ok := flags[name]; ok {
			if strings.TrimSpace(flag.Exposure) != "" {
				entry.Exposure = flag.Exposure
			}
			entry.PlatformControl = flag.PlatformControl
		}
		inputs = append(inputs, entry)
	}
	return einoruntime.BuildToolCatalog(ctx, inputs)
}

func toolExposureFromCatalog(cat *einoruntime.ToolCatalogSnapshot) contextwindow.ToolExposureEstimate {
	if cat == nil || cat.Len() == 0 {
		return contextwindow.ToolExposureEstimate{}
	}
	entries := cat.Entries()
	out := contextwindow.ToolExposureEstimate{}
	for _, e := range entries {
		params := e.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		params = append(json.RawMessage(nil), params...)
		switch e.Exposure {
		case einoruntime.ToolExposureImmediate:
			out.Immediate = append(out.Immediate, contextwindow.ToolSchema{
				Name: e.Name, Description: e.Description, Parameters: params,
			})
		default:
			out.DeferredMetadata = append(out.DeferredMetadata, contextwindow.ToolMetadata{
				Name: e.Name, Description: e.Description,
			})
			out.LoadCandidates = append(out.LoadCandidates, contextwindow.ToolSchema{
				Name: e.Name, Description: e.Description, Parameters: params,
			})
		}
	}
	return out
}

func buildRunPromptCacheKey(
	cfg modelconfig.Config,
	systemPrompt string,
	catalog *einoruntime.ToolCatalogSnapshot,
	mode einoruntime.ToolSearchMode,
) (string, error) {
	digest := ""
	if catalog != nil {
		digest = catalog.CatalogDigest()
	}
	if digest == "" {
		empty, err := einoruntime.BuildToolCatalog(context.Background(), nil)
		if err != nil {
			return "", err
		}
		digest = empty.CatalogDigest()
	}
	promptHash := sha256Hex(strings.TrimSpace(systemPrompt))
	return contextwindow.BuildAgenticPromptCacheKey(contextwindow.PromptCacheKeyInput{
		ProviderProtocol:   contextwindow.PromptCacheProviderProtocolOpenAIResponsesV1,
		ModelConfigID:      strings.TrimSpace(cfg.ID),
		ModelLockVersion:   cfg.LockVersion,
		PromptRevisionHash: promptHash,
		CatalogDigest:      digest,
		AdapterVersion:     contextwindow.PromptCacheAdapterAgenticOpenAIV022,
		DisclosureMode:     promptCacheDisclosureMode(mode),
	})
}

// buildInitialAgenticMessages assembles validated AgenticMessages with deferred-aware
// estimation, hard preflight, and immutable agentic assembly manifest. policy is
// required (non-legacy) — no meta=nil skip path.
func (b *Bridge) buildInitialAgenticMessages(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	configuredAgent agent.Agent,
	instruction string,
	catalog *einoruntime.ToolCatalogSnapshot,
	policy sessioncontext.ResolvedSnapshot,
	mode einoruntime.ToolSearchMode,
) ([]*schema.AgenticMessage, error) {
	if policy.ModelContextWindowTokens <= 0 || strings.TrimSpace(policy.TokenizerProfile) == "" {
		return nil, execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	exposure := toolExposureForDisclosure(catalog, mode)
	catalogDigest := ""
	if mode != einoruntime.ToolSearchModeNone && catalog != nil {
		catalogDigest = catalog.CatalogDigest()
	}
	if mode != einoruntime.ToolSearchModeNone && catalogDigest == "" {
		empty, err := einoruntime.BuildToolCatalog(ctx, nil)
		if err != nil {
			return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
		}
		catalogDigest = empty.CatalogDigest()
	}

	// Token-window assembly path (required for Agentic initial).
	return b.buildAgenticMessagesTokenWindow(ctx, job, run, instruction, policy, exposure, catalogDigest, mode)
}

func (b *Bridge) buildAgenticMessagesTokenWindow(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	instruction string,
	policy sessioncontext.ResolvedSnapshot,
	exposure contextwindow.ToolExposureEstimate,
	catalogDigest string,
	mode einoruntime.ToolSearchMode,
) ([]*schema.AgenticMessage, error) {
	// instruction is already the hash-verified frozen revision (see
	// requireFrozenInstruction); re-reading run.AgentSnapshot here would reopen
	// the unvalidated path that the strict binding parser closed.
	system := instruction

	current, priorTurns, err := b.loadBoundedHistoryForAssembly(ctx, job, system, policy, nil)
	if err != nil {
		if errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
			return nil, execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		// Current user missing etc. — fail closed as assembly failure.
		if errors.Is(err, contextwindow.ErrCurrentUserMissing) {
			return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
		}
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	compact := b.maybeCompactForInitialRun(ctx, agentrunJob{
		WorkspaceID: job.WorkspaceID, SessionID: job.SessionID, RunID: job.RunID,
		UserMessageID: job.UserMessageID, ActorID: job.ActorID,
	}, run, policy, system, nil, current, priorTurns)
	if compact.HardFail != nil {
		return nil, compact.HardFail
	}
	plan, err := contextwindow.AssembleTokenWindow(contextwindow.AssemblerInput{
		PolicyMode:               policy.Mode,
		ModelContextWindowTokens: policy.ModelContextWindowTokens,
		OutputReserveTokens:      policy.OutputReserveTokens,
		SafetyMarginTokens:       policy.SafetyMarginTokens,
		MaxInputTokens:           policy.EffectiveMaxInputTokens,
		MaxRecentTurns:           policy.MaxRecentTurns,
		TokenizerProfile:         policy.TokenizerProfile,
		SystemPrompt:             system,
		Tools:                    nil,
		PriorTurns:               priorTurns,
		CurrentUser:              current,
		OptionalSummary:          compact.OptionalSummary,
	})
	if err != nil {
		if errors.Is(err, contextwindow.ErrRequiredInputTooLarge) {
			return nil, execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	estMsgs := make([]contextwindow.Message, 0, len(plan.PromptMessages))
	out := make([]*schema.AgenticMessage, 0, len(plan.PromptMessages))
	for _, m := range plan.PromptMessages {
		switch m.Role {
		case contextwindow.RoleSystem:
			out = append(out, agenticmsg.System(m.Content))
		case contextwindow.RoleUser:
			userMsg, userErr := b.assembleUserAgenticMessage(ctx, job.WorkspaceID, run.AgentID, m.Content)
			if userErr != nil {
				return nil, userErr
			}
			out = append(out, userMsg)
			estMsgs = append(estMsgs, contextwindow.Message{
				Role: contextwindow.RoleUser, Content: plainTextForEstimate(m.Content),
			})
		case contextwindow.RoleAssistant:
			// KD-10: join text parts only; never feed raw surface JSON to the model.
			assistantText := strings.TrimSpace(assistantModelHistoryText(m.Content))
			if assistantText == "" {
				continue
			}
			out = append(out, agenticmsg.AssistantText(assistantText))
			estMsgs = append(estMsgs, contextwindow.Message{
				Role: contextwindow.RoleAssistant, Content: assistantText,
			})
		}
	}
	if len(out) < 2 {
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if err := agenticmsg.ValidateConversation(out); err != nil {
		return nil, execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	segments := make([]map[string]any, 0, len(plan.IncludedMessages))
	for _, m := range plan.IncludedMessages {
		segments = append(segments, map[string]any{
			"messageId": m.ID, "role": m.Role, "contentHash": m.ContentHash,
		})
	}
	segJSON, _ := json.Marshal(segments)

	meta := &agenticAssemblyPlanMeta{
		Mode:                   plan.Mode,
		HardInputCeilingTokens: plan.HardInputCeilingTokens,
		OutputReserveTokens:    plan.OutputReserveTokens,
		SafetyMarginTokens:     plan.SafetyMarginTokens,
		SystemPromptHash:       sha256Hex(system),
		IncludedSegments:       segJSON,
		OmittedPrefixCount:     plan.OmittedTurnCount,
		Policy:                 policy,
	}
	if err := b.estimateAndPreflightAgentic(ctx, job, run, system, exposure, catalogDigest, estMsgs, meta, mode); err != nil {
		return nil, err
	}
	return out, nil
}

type agenticAssemblyPlanMeta struct {
	Mode                   string
	HardInputCeilingTokens int64
	OutputReserveTokens    int64
	SafetyMarginTokens     int64
	SystemPromptHash       string
	IncludedSegments       json.RawMessage
	OmittedPrefixCount     int
	Policy                 sessioncontext.ResolvedSnapshot
}

// estimateAndPreflightAgentic always runs EstimateAgenticRequestV2 + PreflightAgenticMandatory
// and persists the agentic ContextAssemblyRecord. Native still uses the v1 formula
// via V2's client_bounded delegate. meta and assemblies are required; there is
// no nil-meta skip path.
func (b *Bridge) estimateAndPreflightAgentic(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	system string,
	exposure contextwindow.ToolExposureEstimate,
	catalogDigest string,
	estMsgs []contextwindow.Message,
	meta *agenticAssemblyPlanMeta,
	mode einoruntime.ToolSearchMode,
) error {
	if meta == nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	if b.assemblies == nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	profile := strings.TrimSpace(meta.Policy.TokenizerProfile)
	if profile == "" {
		return execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}
	modelCtx := meta.Policy.ModelContextWindowTokens
	outReserve := meta.Policy.OutputReserveTokens
	safety := meta.Policy.SafetyMarginTokens
	if modelCtx <= 0 {
		return execution.NewContextError(execution.ErrCodeContextModelLimitUnknown)
	}

	est, err := contextwindow.NewEstimator(profile)
	if err != nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	got, err := est.EstimateAgenticRequestV2(system, exposure, estMsgs)
	if err != nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	// Hard preflight always — fail before model/sink/provider.
	_, preErr := contextwindow.PreflightAgenticMandatory(contextwindow.AgenticPreflightInput{
		ModelContextWindowTokens: modelCtx,
		OutputReserveTokens:      outReserve,
		SafetyMarginTokens:       safety,
		DynamicReserveTokens:     got.DynamicToolLoadReserveTokens,
		MandatoryTokens:          got.InitialVisibleTokens,
		MaxLoadedToolCount:       got.MaxLoadedToolCount,
		ActualLoadedToolCount:    0,
	})
	if preErr != nil {
		if errors.Is(preErr, contextwindow.ErrMandatoryInputTooLarge) ||
			errors.Is(preErr, contextwindow.ErrDynamicToolReserveExceeded) {
			return execution.NewContextError(execution.ErrCodeContextRequiredInputTooLarge)
		}
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}

	hardCeiling := meta.HardInputCeilingTokens
	if hardCeiling <= 0 {
		ceil := modelCtx - outReserve - safety - got.DynamicToolLoadReserveTokens
		if ceil > 0 {
			hardCeiling = ceil
		}
	}
	// Prefer policy values when meta tokens are zero (assembler may set them).
	outRes := meta.OutputReserveTokens
	if outRes == 0 {
		outRes = outReserve
	}
	safe := meta.SafetyMarginTokens
	if safe == 0 {
		safe = safety
	}
	policyMode := meta.Mode
	if policyMode == "" {
		policyMode = meta.Policy.Mode
	}
	searchMode, estimatorVersion := assemblyFieldsForDisclosure(mode)
	if searchMode != execution.AssemblyToolSearchModeNone {
		if strings.TrimSpace(catalogDigest) == "" || len(catalogDigest) != 64 {
			return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
		}
	} else {
		catalogDigest = ""
	}

	rec := execution.ContextAssemblyRecord{
		WorkspaceID:                  job.WorkspaceID,
		RunID:                        job.RunID,
		SessionID:                    job.SessionID,
		Mode:                         policyMode,
		PolicySnapshotHash:           execution.HashJSONObject(run.ContextPolicySnapshot),
		ModelSnapshotHash:            execution.HashJSONObject(run.ModelSnapshot),
		CapabilitySnapshotHash:       execution.HashJSONObject(run.CapabilitySnapshot),
		AgentSnapshotHash:            execution.HashJSONObject(run.AgentSnapshot),
		EstimatorProfile:             got.Profile,
		EstimatorVersion:             estimatorVersion,
		HardInputCeilingTokens:       hardCeiling,
		OutputReserveTokens:          outRes,
		SafetyMarginTokens:           safe,
		ToolsOverheadTokens:          got.ToolsTokens,
		SystemPromptHash:             meta.SystemPromptHash,
		IncludedSegments:             meta.IncludedSegments,
		OmittedPrefixCount:           meta.OmittedPrefixCount,
		EstimatedTotalTokens:         got.TotalTokens,
		ToolSearchMode:               searchMode,
		ToolCatalogDigest:            catalogDigest,
		ImmediateToolCount:           got.ImmediateToolCount,
		DeferredToolCount:            got.DeferredToolCount,
		MaxLoadedToolCount:           got.MaxLoadedToolCount,
		ImmediateToolsTokens:         got.ImmediateToolsTokens,
		DeferredMetadataTokens:       got.DeferredMetadataTokens,
		DynamicToolLoadReserveTokens: got.DynamicToolLoadReserveTokens,
	}
	// Fail closed if any sensitive marker could appear in persisted fields.
	if assemblyRecordLeaksSensitive(rec, job) {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	rec.AssemblyDigest = execution.ComputeAssemblyDigest(rec)
	if _, err := b.assemblies.InsertImmutable(ctx, rec); err != nil {
		return execution.NewContextError(execution.ErrCodeContextAssemblyFailed)
	}
	return nil
}

func assemblyRecordLeaksSensitive(rec execution.ContextAssemblyRecord, job agentrun.Job) bool {
	// Digests/hashes only — reject if query/body-like fields snuck into mode/version.
	blob := rec.Mode + rec.EstimatorVersion + rec.ToolSearchMode + rec.ToolCatalogDigest
	for _, s := range []string{
		strings.TrimSpace(job.UserMessageID),
		// never store message body; check catalog digest is hex only
	} {
		if s != "" && strings.Contains(blob, s) && len(s) > 8 {
			// user message id in mode would be odd; ignore short collisions
		}
	}
	// none assemblies persist a NULL digest; every other mode is 64-hex only.
	if rec.ToolSearchMode == execution.AssemblyToolSearchModeNone {
		return rec.ToolCatalogDigest != ""
	}
	if len(rec.ToolCatalogDigest) != 64 {
		return true
	}
	for i := 0; i < 64; i++ {
		c := rec.ToolCatalogDigest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return true
		}
	}
	return false
}

func (b *Bridge) assembleUserAgenticMessage(
	ctx context.Context,
	workspaceID, agentID, content string,
) (*schema.AgenticMessage, error) {
	assembler := b.multimodal
	if assembler == nil {
		assembler = &chatruntime.MultimodalAssembler{RuntimeMultimodal: false}
	}
	msg, err := assembler.AssembleUserAgenticMessage(ctx, workspaceID, agentID, content)
	if err != nil {
		return nil, err
	}
	if err := agenticmsg.Validate(msg); err != nil {
		return nil, fmt.Errorf("%w: %v", chatruntime.ErrModelContentUnsupported, err)
	}
	return msg, nil
}

func plainTextForEstimate(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if text, ok := chatruntime.TextForTokenEstimate(content); ok {
		return text
	}
	return content
}
