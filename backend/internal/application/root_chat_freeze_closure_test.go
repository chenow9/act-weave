package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/config"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"
	"actweave/backend/internal/workspace"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Producer→consumer closure for the root chat run freeze.
//
// Every earlier Task 4A test drove Bridge.Execute from synthetic fixtures, so a
// producer that emits a document its own consumer rejects stayed invisible.
// This test runs the real chain end to end:
//
//	agentRunSnapshots.SnapshotAgentRun  (real DB rows)
//	  → execution.RunService.PrepareAgentRun / StartAgentRun  (real persistence)
//	    → agentdelegation.ParseSnapshot  (frozen graph contract)
//	      → chatruntimebridge.Bridge.Execute(targets=nil)  (Agentic initial)
//
// Only chat message I/O and the LLM are faked; every snapshot under test is the
// production producer's own bytes.
const (
	closureOwnerID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789001"
	closureWorkspaceID = "b18f1f2e-7b5a-7c3d-8e9f-123456789002"
	closureModelID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789003"
	closureAgentID     = "b18f1f2e-7b5a-7c3d-8e9f-123456789004"
	closureRunID       = "b18f1f2e-7b5a-7c3d-8e9f-123456789005"
	closureRevisionID  = "b18f1f2e-7b5a-7c3d-8e9f-123456789006"
	closureSessionID   = "b18f1f2e-7b5a-7c3d-8e9f-123456789007"
	closureMessageID   = "b18f1f2e-7b5a-7c3d-8e9f-123456789008"
	closureCapID       = "b18f1f2e-7b5a-7c3d-8e9f-123456789009"
	closureReleaseID   = "b18f1f2e-7b5a-7c3d-8e9f-12345678900a"
	closureSourceID    = "b18f1f2e-7b5a-7c3d-8e9f-12345678900b"
	closurePrompt      = "You are the closure test agent."
	closureChecksum    = "5994471abb01112afcc18159f6cc74b4f511b99806da59b3caf5a9c173cacfc5"
)

func TestRootChatFreeze_ProducerToBridgeExecuteClosure(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	ctx := context.Background()

	source, repos := seedClosureFixture(t, db)

	// --- 1) Real producer ----------------------------------------------------
	snapshots, err := source.SnapshotAgentRun(ctx, closureWorkspaceID, closureAgentID)
	if err != nil {
		t.Fatalf("SnapshotAgentRun: %v", err)
	}
	if snapshots.SchemaVersion != execution.RunSnapshotSchemaV2 {
		t.Fatalf("expected run.v2 from producer, got %q", snapshots.SchemaVersion)
	}
	if len(snapshots.Graph) == 0 || string(snapshots.Graph) == "{}" {
		t.Fatalf("root chat run producer emitted no agent_graph_snapshot: %s", snapshots.Graph)
	}

	// --- 1b) Digest stability across the freeze boundary --------------------
	// The verification evidence digest is computed over the live row; the bridge
	// recomputes it over the frozen bytes. Any re-serialization difference
	// (jsonb spacing vs encoding/json compaction) silently breaks every run.
	liveCfg, err := repos.models.Get(ctx, closureWorkspaceID, closureModelID)
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		Options             json.RawMessage `json:"options"`
		RuntimeCapabilities json.RawMessage `json:"runtimeCapabilities"`
		AgenticCapabilities json.RawMessage `json:"agenticCapabilities"`
		Provider            string          `json:"provider"`
		APIBase             string          `json:"apiBase"`
		ModelName           string          `json:"modelName"`
	}
	if err := json.Unmarshal(snapshots.Model, &frozen); err != nil {
		t.Fatal(err)
	}
	frozenDigest := modelconfig.WireConfigDigest(modelconfig.Config{
		Provider: frozen.Provider, APIBase: frozen.APIBase, ModelName: frozen.ModelName,
		Options: frozen.Options, RuntimeCapabilities: frozen.RuntimeCapabilities,
	})
	if frozenDigest != modelconfig.WireConfigDigest(liveCfg) {
		t.Fatalf("freeze changed the wire config digest:\n live=%s\nfrozen=%s\n live runtime=%s\nfrozen runtime=%s",
			modelconfig.WireConfigDigest(liveCfg), frozenDigest,
			liveCfg.RuntimeCapabilities, frozen.RuntimeCapabilities)
	}

	// --- 2) Frozen graph contract -------------------------------------------
	parsed, err := agentdelegation.ParseSnapshot(closureWorkspaceID, snapshots.Graph)
	if err != nil {
		t.Fatalf("ParseSnapshot rejected the real producer output: %v", err)
	}
	if parsed == nil {
		t.Fatal("ParseSnapshot treated the real producer output as absent")
	}
	if parsed.RootAgentID != closureAgentID {
		t.Fatalf("rootAgentId=%q want %q", parsed.RootAgentID, closureAgentID)
	}
	if len(parsed.Edges) != 0 {
		t.Fatalf("root chat freeze must be an explicitly empty graph, edges=%d", len(parsed.Edges))
	}
	if list, ok := parsed.FrozenRemotesByCaller[closureAgentID]; !ok || len(list) != 0 {
		t.Fatalf("frozenRemotesByCaller must carry an explicit empty list for the root agent: %v", parsed.FrozenRemotesByCaller)
	}
	// Cross-snapshot identity the Agentic consumer asserts (BLOCKER-2).
	if len(parsed.Nodes) != 1 {
		t.Fatalf("nodes=%d want 1", len(parsed.Nodes))
	}
	var rootModel struct {
		ID          string `json:"id"`
		LockVersion int64  `json:"lockVersion"`
	}
	if err := json.Unmarshal(snapshots.Model, &rootModel); err != nil {
		t.Fatal(err)
	}
	if parsed.Nodes[0].ModelConfigID != rootModel.ID {
		t.Fatalf("graph root modelConfigId=%q run.ModelSnapshot.id=%q", parsed.Nodes[0].ModelConfigID, rootModel.ID)
	}
	if parsed.Nodes[0].ModelConfigLockVer != rootModel.LockVersion {
		t.Fatalf("graph root lock=%d run.ModelSnapshot lock=%d",
			parsed.Nodes[0].ModelConfigLockVer, rootModel.LockVersion)
	}

	// --- 3) Real persistence via RunService ---------------------------------
	runService, err := execution.NewRunService(repos.runs, source, closureAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	input, err := runService.PrepareAgentRun(ctx, execution.StartAgentRunRequest{
		ID: closureRunID, WorkspaceID: closureWorkspaceID, SessionID: closureSessionID,
		AgentID: closureAgentID, TriggerType: "CHAT", TriggeredByType: "USER",
		TriggeredByID: closureOwnerID, TraceID: "trace-closure",
		InputSummary: json.RawMessage(`{"messageId":"` + closureMessageID + `"}`),
	})
	if err != nil {
		t.Fatalf("PrepareAgentRun: %v", err)
	}
	if len(input.AgentGraphSnapshot) == 0 || string(input.AgentGraphSnapshot) == "{}" {
		t.Fatalf("RunService dropped the frozen graph: %s", input.AgentGraphSnapshot)
	}
	run, err := repos.runs.StartAgentRun(ctx, input)
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	stored, err := repos.runs.GetAgentRun(ctx, closureWorkspaceID, closureRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.AgentGraphSnapshot) == 0 || string(stored.AgentGraphSnapshot) == "{}" {
		t.Fatalf("agent_runs.agent_graph_snapshot persisted empty: %s", stored.AgentGraphSnapshot)
	}
	if run.SnapshotSchemaVersion != execution.RunSnapshotSchemaV2 {
		t.Fatalf("stored schema=%q", run.SnapshotSchemaVersion)
	}

	// The frozen context policy must survive the jsonb round trip: the Agentic
	// preflight has no legacy bypass, so an unparseable policy is fatal.
	if _, err := sessioncontext.ParseResolvedSnapshot(stored.ContextPolicySnapshot); err != nil {
		t.Fatalf("stored context policy is not parseable by its consumer: %v", err)
	}

	// --- 4) Real Bridge.Execute(targets=nil) --------------------------------
	fake := &closureRuntime{
		run: stored,
		messages: []chat.Message{{
			ID: closureMessageID, SessionID: closureSessionID, Role: "USER",
			Content: "closure hello", Status: "COMPLETED", CreatedAt: time.Now().UTC(),
		}},
		reply: "closure-ok",
	}
	assemblyRepo, err := execution.NewContextAssemblyRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	assemblies := &countingAssemblies{inner: assemblyRepo, t: t}
	store := newClosureCheckpointStore()
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: fake, Results: fake, Agents: repos.agents, Models: repos.models,
		Runs: fake, Events: fake,
		Engine:            einoruntime.NewEngine(einoruntime.EngineConfig{Store: store}),
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: store}),
		BuildAgenticModel: fake.buildAgenticModel,
		BuildChatModel:    fake.buildClassicModel,
		Assemblies:        assemblies,
		ToolInvoker:       nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Execute(ctx, agentrun.Job{
		WorkspaceID: closureWorkspaceID, SessionID: closureSessionID, RunID: closureRunID,
		UserMessageID: closureMessageID, ActorID: closureOwnerID, InitialEventsCommitted: true,
	}); err != nil {
		t.Fatalf("Bridge.Execute on real producer freeze: %v\nmodel=%s\ncontext=%s\ncaps=%s\nagent=%s",
			err, stored.ModelSnapshot, stored.ContextPolicySnapshot,
			stored.CapabilitySnapshot, stored.AgentSnapshot)
	}
	if fake.classicBuilds != 0 {
		t.Fatalf("classic builder called %d times on the initial path", fake.classicBuilds)
	}
	if assemblies.writes != 1 {
		t.Fatalf("assembly manifest writes=%d want exactly 1", assemblies.writes)
	}
	if _, err := assemblyRepo.GetByRun(ctx, closureWorkspaceID, closureRunID); err != nil {
		t.Fatalf("persisted assembly manifest failed its own read-back validation: %v", err)
	}
	if fake.agenticBuilds != 1 {
		t.Fatalf("agentic builder calls=%d want 1", fake.agenticBuilds)
	}
	if got := fake.recordedContent(); got != "closure-ok" {
		t.Fatalf("assistant content=%q", got)
	}
	// The frozen prompt revision (not the built-in default) must drive the run.
	if !strings.Contains(fake.systemPrompt(), closurePrompt) {
		t.Fatalf("system prompt did not come from the frozen revision: %q", fake.systemPrompt())
	}
}

// TestRootChatFreeze_ProducerCapabilityReleasesPassStrictParse feeds the real
// producer's capability freeze through Bridge.Execute with a published+bound
// TOOL release. It is the MAJOR-5 producer-closure guard: the Agentic strict
// capability validator must accept exactly what SnapshotAgentRun writes,
// including the connectionId key that is emitted only when a binding pins one.
func TestRootChatFreeze_ProducerCapabilityReleasesPassStrictParse(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	ctx := context.Background()

	source, repos := seedClosureFixture(t, db)
	publishAndBindClosureTool(t, db)

	snapshots, err := source.SnapshotAgentRun(ctx, closureWorkspaceID, closureAgentID)
	if err != nil {
		t.Fatalf("SnapshotAgentRun: %v", err)
	}
	var envelope struct {
		SchemaVersion string           `json:"schemaVersion"`
		Releases      []map[string]any `json:"releases"`
	}
	if err := json.Unmarshal(snapshots.Capabilities, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Releases) != 1 {
		t.Fatalf("expected the bound TOOL in the freeze, got %s", snapshots.Capabilities)
	}

	runService, err := execution.NewRunService(repos.runs, source, closureAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	input, err := runService.PrepareAgentRun(ctx, execution.StartAgentRunRequest{
		ID: closureRunID, WorkspaceID: closureWorkspaceID, SessionID: closureSessionID,
		AgentID: closureAgentID, TriggerType: "CHAT", TriggeredByType: "USER",
		TriggeredByID: closureOwnerID, TraceID: "trace-closure-caps",
		InputSummary: json.RawMessage(`{"messageId":"` + closureMessageID + `"}`),
	})
	if err != nil {
		t.Fatalf("PrepareAgentRun: %v", err)
	}
	if _, err := repos.runs.StartAgentRun(ctx, input); err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	stored, err := repos.runs.GetAgentRun(ctx, closureWorkspaceID, closureRunID)
	if err != nil {
		t.Fatal(err)
	}

	fake := &closureRuntime{
		run: stored,
		messages: []chat.Message{{
			ID: closureMessageID, SessionID: closureSessionID, Role: "USER",
			Content: "closure with tools", Status: "COMPLETED", CreatedAt: time.Now().UTC(),
		}},
		reply: "closure-tools-ok",
	}
	assemblyRepo, err := execution.NewContextAssemblyRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	assemblies := &countingAssemblies{inner: assemblyRepo, t: t}
	store := newClosureCheckpointStore()
	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: fake, Results: fake, Agents: repos.agents, Models: repos.models,
		Runs: fake, Events: fake,
		Engine:            einoruntime.NewEngine(einoruntime.EngineConfig{Store: store}),
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: store}),
		BuildAgenticModel: fake.buildAgenticModel,
		BuildChatModel:    fake.buildClassicModel,
		Assemblies:        assemblies,
		ToolInvoker:       closureToolInvoker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Execute(ctx, agentrun.Job{
		WorkspaceID: closureWorkspaceID, SessionID: closureSessionID, RunID: closureRunID,
		UserMessageID: closureMessageID, ActorID: closureOwnerID, InitialEventsCommitted: true,
	}); err != nil {
		t.Fatalf("Bridge.Execute with producer capability freeze: %v", err)
	}
	if got := fake.recordedContent(); got != "closure-tools-ok" {
		t.Fatalf("assistant content=%q", got)
	}
}

// --- fixture ---------------------------------------------------------------

type closureRepos struct {
	agents *agent.Repository
	models *modelconfig.Repository
	runs   *execution.RunRepository
}

func seedClosureFixture(t *testing.T, db *sql.DB) (*agentRunSnapshots, closureRepos) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("seed %q: %v", strings.TrimSpace(strings.SplitN(query, "\n", 3)[1]), err)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'closure.owner','Closure Owner')`, closureOwnerID)
	exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'closure-space','Closure Space','SANDBOX',$2,$2,$2)
	`, closureWorkspaceID, closureOwnerID)
	exec(`
		INSERT INTO model_configs(
		 id,workspace_id,name,provider,api_base,model_name,options,created_by,updated_by
		) VALUES($1,$2,'Closure Model','openai','https://models.example.test/v1','closure-model','{}',$3,$3)
	`, closureModelID, closureWorkspaceID, closureOwnerID)
	exec(`
		UPDATE model_configs SET runtime_capabilities=$2::jsonb WHERE id=$1
	`, closureModelID, `{
		"schemaVersion":"model-runtime.v1",
		"contextWindowTokens":128000,
		"defaultOutputReserveTokens":4096,
		"outputTokenLimitMode":"max_tokens",
		"tokenizerProfile":"o200k_base",
		"tokenizerVersion":"2026-01"
	}`)
	exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'Closure Agent',$3,$4,$4)
	`, closureAgentID, closureWorkspaceID, closureModelID, closureOwnerID)
	exec(`
		INSERT INTO agent_prompt_revisions(
			id,workspace_id,agent_id,revision_no,system_prompt,source,content_sha256,created_by
		) VALUES($1,$2,$3,1,$4,'MANUAL',$5,$6)
	`, closureRevisionID, closureWorkspaceID, closureAgentID, closurePrompt,
		sha256HexString(closurePrompt), closureOwnerID)
	exec(`UPDATE agents SET current_prompt_revision_id=$2 WHERE id=$1`, closureAgentID, closureRevisionID)
	exec(`
		INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'Closure session',$4)
	`, closureSessionID, closureWorkspaceID, closureAgentID, closureOwnerID)

	agentRepository, err := agent.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	modelRepository, err := modelconfig.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capability.NewCatalog(capabilityRepository, capabilityRepository)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRepository, err := workspace.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	runRepository, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// Reproduce exactly what the verification CAS persists: evidence stamped at
	// the pre-CAS lock (1) and the row landing one higher (2). Any other pairing
	// is unreadable through modelconfig.Repository.Get, so the closure test must
	// not invent a friendlier shape.
	cfg, err := modelRepository.Get(context.Background(), closureWorkspaceID, closureModelID)
	if err != nil {
		t.Fatal(err)
	}
	// Must be a whole UTC second at or after created_at: the read invariant
	// compares agenticCapabilities.verifiedAt with last_verified_at exactly.
	verifiedAt := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)
	doc, err := modelconfig.CanonicalAgenticCapabilities(
		verifiedAt, cfg.LockVersion, modelconfig.WireConfigDigest(cfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	agenticRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	exec(`
		UPDATE model_configs
		SET agentic_capabilities=$2::jsonb, status='VERIFIED',
			last_verified_at=$3::timestamptz, last_latency_ms=12,
			last_error_code=NULL, lock_version=lock_version+1
		WHERE id=$1
	`, closureModelID, string(agenticRaw), verifiedAt)
	if _, err := modelRepository.Get(context.Background(), closureWorkspaceID, closureModelID); err != nil {
		t.Fatalf("seeded VERIFIED row is not readable through the repository: %v", err)
	}

	source := &agentRunSnapshots{
		agents: agentRepository, models: modelRepository, catalog: catalog,
		workspaces: workspaceRepository,
		sessionContext: config.SessionContextRollout{
			Enabled: true, AllowAllWorkspaces: true, Mode: "enforced",
			RolloutVersion: "closure-rollout",
		},
	}
	return source, closureRepos{agents: agentRepository, models: modelRepository, runs: runRepository}
}

func publishAndBindClosureTool(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'TOOL','Closure Tool','closure-tool',$3,$3)
	`, closureCapID, closureWorkspaceID, closureOwnerID); err != nil {
		t.Fatal(err)
	}
	capabilityRepository, err := capability.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := capabilityRepository.Publish(context.Background(), capability.PublishRelease{
		ID: closureReleaseID, WorkspaceID: closureWorkspaceID, CapabilityID: closureCapID,
		SourceType: "TOOL_VERSION", SourceID: closureSourceID,
		CallableName: "closure_lookup", CallableDescription: "Closure lookup",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel:    "LOW", SideEffectLevel: "READ",
		Checksum: closureChecksum, PublishedBy: closureOwnerID,
	}); err != nil {
		t.Fatalf("publish closure tool: %v", err)
	}
	if _, err := capabilityRepository.Bind(context.Background(), capability.BindInput{
		WorkspaceID: closureWorkspaceID, AgentID: closureAgentID, CapabilityID: closureCapID,
		VersionPolicy: "FOLLOW_ACTIVE", Enabled: true, BoundBy: closureOwnerID,
	}); err != nil {
		t.Fatalf("bind closure tool: %v", err)
	}
}

// --- minimal chat/LLM doubles ----------------------------------------------

type closureAuthorizer struct{}

func (closureAuthorizer) AuthorizeRun(
	_ context.Context, _, _, _, _, _ string,
) (json.RawMessage, error) {
	return json.RawMessage(`{"decision":"ALLOW"}`), nil
}

type closureRuntime struct {
	mu            sync.Mutex
	run           execution.AgentRun
	messages      []chat.Message
	reply         string
	content       string
	prompt        string
	agenticBuilds int
	classicBuilds int
}

func (c *closureRuntime) recordedContent() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.content
}

func (c *closureRuntime) systemPrompt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prompt
}

func (c *closureRuntime) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}

func (c *closureRuntime) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return c.messages, nil
}

func (c *closureRuntime) ListMessagesReversePage(
	_ context.Context, _, _ string, limit int, cursor *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	msgs := append([]chat.Message(nil), c.messages...)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	start := 0
	if cursor != nil {
		for i, m := range msgs {
			if m.ID == cursor.ID {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	hasMore := end < len(msgs)
	if end > len(msgs) {
		end = len(msgs)
	}
	page := chat.MessagePage{Messages: msgs[start:end], HasMore: hasMore}
	if hasMore && len(page.Messages) > 0 {
		last := page.Messages[len(page.Messages)-1]
		page.NextCursor = &chat.MessagePageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (c *closureRuntime) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	for _, m := range c.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}

func (c *closureRuntime) RecordAssistantResult(
	_ context.Context, in chat.RecordAssistantResultInput,
) (chat.RecordAssistantResult, error) {
	c.mu.Lock()
	c.content = in.Content
	c.mu.Unlock()
	return chat.RecordAssistantResult{Message: chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "COMPLETED", RunID: in.RunID,
		CreatedAt: time.Now().UTC(),
	}}, nil
}

func (c *closureRuntime) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	out := c.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

func (c *closureRuntime) Record(context.Context, chatruntime.ProtocolRecord) error { return nil }

func (c *closureRuntime) buildAgenticModel(context.Context, modelconfig.Config) (model.AgenticModel, error) {
	c.mu.Lock()
	c.agenticBuilds++
	c.mu.Unlock()
	return &closureAgenticModel{owner: c}, nil
}

func (c *closureRuntime) buildClassicModel(context.Context, modelconfig.Config) (model.BaseChatModel, error) {
	c.mu.Lock()
	c.classicBuilds++
	c.mu.Unlock()
	return nil, errClosureClassicForbidden
}

var errClosureClassicForbidden = errClosure("classic builder must not run on the Agentic initial path")

type errClosure string

func (e errClosure) Error() string { return string(e) }

type closureAgenticModel struct{ owner *closureRuntime }

// record keeps the serialized model input so the test can prove the frozen
// prompt revision (not the bridge default) reached the provider boundary.
func (m *closureAgenticModel) record(input []*schema.AgenticMessage) {
	raw, err := json.Marshal(input)
	if err != nil {
		return
	}
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	m.owner.prompt = string(raw)
}

func (m *closureAgenticModel) Generate(
	_ context.Context, input []*schema.AgenticMessage, _ ...model.Option,
) (*schema.AgenticMessage, error) {
	m.record(input)
	return agenticmsg.AssistantText(m.owner.reply), nil
}

func (m *closureAgenticModel) Stream(
	_ context.Context, input []*schema.AgenticMessage, _ ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.record(input)
	return schema.StreamReaderFromArray(
		[]*schema.AgenticMessage{agenticmsg.AssistantText(m.owner.reply)}), nil
}

// closureToolInvoker satisfies the invoker contract so PipelineTools can be
// built from the frozen capability release. The scripted model never emits a
// tool call, so neither method runs.
type closureToolInvoker struct{}

func (closureToolInvoker) ResolveInvocation(
	_ context.Context, req execution.ResolveRequest,
) (execution.ResolvedInvocation, error) {
	return execution.ResolvedInvocation{
		Snapshot: execution.ReleaseSnapshot{
			WorkspaceID: req.WorkspaceID, CapabilityID: req.CapabilityID, ReleaseID: req.ReleaseID,
		},
		Connection: execution.ConnectionSnapshot{
			ID: "conn-closure", WorkspaceID: req.WorkspaceID, Environment: "DEV",
		},
		RiskLevel: "LOW", SideEffectLevel: "READ",
	}, nil
}

func (closureToolInvoker) InvokeResolved(
	context.Context, execution.InvokeRequest, execution.ResolvedInvocation,
) (execution.PipelineResult, error) {
	return execution.PipelineResult{}, errClosure("tool invoke must not run in this closure test")
}

func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// countingAssemblies wraps the real repository so the closure tests can assert
// exactly one manifest write and surface the underlying persistence error
// instead of the flattened CONTEXT_ASSEMBLY_FAILED code.
type countingAssemblies struct {
	inner  *execution.ContextAssemblyRepository
	t      *testing.T
	writes int
}

func (a *countingAssemblies) InsertImmutable(
	ctx context.Context, rec execution.ContextAssemblyRecord,
) (execution.ContextAssemblyRecord, error) {
	a.writes++
	out, err := a.inner.InsertImmutable(ctx, rec)
	if err != nil {
		a.t.Logf("context assembly insert failed: %v (digest self-consistent=%v, segments=%s)",
			err, rec.AssemblyDigest == execution.ComputeAssemblyDigest(rec), rec.IncludedSegments)
	}
	return out, err
}

type closureCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newClosureCheckpointStore() *closureCheckpointStore {
	return &closureCheckpointStore{data: make(map[string][]byte)}
}

func (s *closureCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[id]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), b...), true, nil
}

func (s *closureCheckpointStore) Set(_ context.Context, id string, checkPoint []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = append([]byte(nil), checkPoint...)
	return nil
}
