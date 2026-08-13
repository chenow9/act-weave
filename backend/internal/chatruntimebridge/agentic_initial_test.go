package chatruntimebridge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agenticmsg"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/config"
	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelapi"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// --- fakes ---

type scriptedAgenticModel struct {
	mu        sync.Mutex
	responses []*schema.AgenticMessage
	calls     atomic.Int64
	lastInput []*schema.AgenticMessage
	lastOpts  []model.Option
	// trustedWS records the trusted workspace visible in the call context, one
	// entry per call, empty when the bridge did not bind one.
	trustedWS []string
	onCall    func(call int, input []*schema.AgenticMessage, opts []model.Option)
}

func (m *scriptedAgenticModel) next(ctx context.Context, input []*schema.AgenticMessage, opts []model.Option) (*schema.AgenticMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := int(m.calls.Add(1))
	m.lastInput = append([]*schema.AgenticMessage(nil), input...)
	m.lastOpts = append([]model.Option(nil), opts...)
	seen, _ := einoruntime.TrustedWorkspaceID(ctx)
	m.trustedWS = append(m.trustedWS, seen)
	if m.onCall != nil {
		m.onCall(n, input, opts)
	}
	if len(m.responses) == 0 {
		return nil, errors.New("scriptedAgenticModel: no more responses")
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

func (m *scriptedAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return m.next(ctx, input, opts)
}

func (m *scriptedAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	msg, err := m.next(ctx, input, opts)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{msg}), nil
}

// inputSnapshot returns the last input under the lock next() writes it with.
func (m *scriptedAgenticModel) inputSnapshot() []*schema.AgenticMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*schema.AgenticMessage(nil), m.lastInput...)
}

// trustedWorkspaces returns the trusted workspace seen on each model call.
func (m *scriptedAgenticModel) trustedWorkspaces() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.trustedWS...)
}

var _ model.AgenticModel = (*scriptedAgenticModel)(nil)

type classicBuilderSpy struct {
	calls  atomic.Int64
	panic  bool
	errMsg string
}

func (s *classicBuilderSpy) Build(ctx context.Context, cfg modelconfig.Config) (model.BaseChatModel, error) {
	s.calls.Add(1)
	if s.panic {
		panic("classic BuildChatModel must not be called on agentic initial path")
	}
	if s.errMsg == "" {
		s.errMsg = "classic builder must not be called on initial Chat path"
	}
	return nil, errors.New(s.errMsg)
}

type agenticCallCounter struct {
	calls atomic.Int64
	model model.AgenticModel
}

func (c *agenticCallCounter) Build(ctx context.Context, cfg modelconfig.Config) (model.AgenticModel, error) {
	c.calls.Add(1)
	return c.model, nil
}

// sinkCounter retains every sink it hands out along with the identity it was
// opened under, so a test can assert what the client actually received and under
// which assistant message item.
type sinkCounter struct {
	opens atomic.Int64
	mu    sync.Mutex
	args  []chatruntimebridge.TextSinkArgs
	sinks []*chatruntimebridge.RecordingTextDeltaSink
}

func (s *sinkCounter) Factory(
	_ context.Context, args chatruntimebridge.TextSinkArgs,
) (chatruntime.TextDeltaSink, error) {
	s.opens.Add(1)
	sink := &chatruntimebridge.RecordingTextDeltaSink{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.args = append(s.args, args)
	s.sinks = append(s.sinks, sink)
	return sink, nil
}

// last returns the most recently opened sink and the identity it carries.
func (s *sinkCounter) last() (*chatruntimebridge.RecordingTextDeltaSink, chatruntimebridge.TextSinkArgs, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sinks) == 0 {
		return nil, chatruntimebridge.TextSinkArgs{}, false
	}
	return s.sinks[len(s.sinks)-1], s.args[len(s.args)-1], true
}

type eventCounter struct {
	calls atomic.Int64
}

func (e *eventCounter) Record(context.Context, chatruntime.ProtocolRecord) error {
	e.calls.Add(1)
	return nil
}

type memAssemblies struct {
	mu      sync.Mutex
	byRun   map[string]execution.ContextAssemblyRecord
	inserts atomic.Int64
}

func newMemAssemblies() *memAssemblies {
	return &memAssemblies{byRun: make(map[string]execution.ContextAssemblyRecord)}
}

func (m *memAssemblies) InsertImmutable(_ context.Context, rec execution.ContextAssemblyRecord) (execution.ContextAssemblyRecord, error) {
	m.inserts.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	key := rec.WorkspaceID + "/" + rec.RunID
	if existing, ok := m.byRun[key]; ok {
		if existing.AssemblyDigest == rec.AssemblyDigest {
			return existing, nil
		}
		return execution.ContextAssemblyRecord{}, execution.ErrContextAssemblyConflict
	}
	if rec.ID == "" {
		rec.ID = "asm-" + rec.RunID
	}
	m.byRun[key] = rec
	return rec, nil
}

func (m *memAssemblies) Get(workspaceID, runID string) (execution.ContextAssemblyRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byRun[workspaceID+"/"+runID]
	return rec, ok
}

const (
	testModelUUID = "c08f1f2e-7b5a-7c3d-8e9f-1234567890a1"
	testWSUUID    = "a11ce000-0000-4000-8000-000000000001"
	testRunUUID   = "b22ce000-0000-4000-8000-000000000002"
	testSessUUID  = "c33ce000-0000-4000-8000-000000000003"
	testAgentUUID = "d44ce000-0000-4000-8000-000000000004"
	testMsgUUID   = "e55ce000-0000-4000-8000-000000000005"
	testPromptRev = "f66ce000-0000-4000-8000-000000000006"
	testCapUUID   = "a77ce000-0000-4000-8000-000000000007"
	testRelUUID   = "b88ce000-0000-4000-8000-000000000008"
	testConnUUID  = "c99ce000-0000-4000-8000-000000000009"
)

// testModelLockVersion is the frozen model lock every fixture agrees on:
// run.ModelSnapshot.lockVersion, run.AgentSnapshot.modelConfigLockVersion and
// the graph root node's lock triple. It is 2 because a VERIFIED model config
// cannot exist below 2 (create writes 1, verification CAS bumps to 2 while
// stamping verifiedLockVersion=1).
const testModelLockVersion int64 = 2

// testFrozenPrompt is the body of the frozen prompt revision the fakes serve.
// It is deliberately identical to the bridge's built-in default instruction so
// prompt-dependent assertions (cache keys, assembly hashes) stay stable.
const testFrozenPrompt = "You are a helpful workspace agent. Answer clearly and concisely."

func testFrozenPromptHash() string { return sha256Hex(testFrozenPrompt) }

// testRunAgentSnapshot mirrors application.agentRunSnapshots.SnapshotAgentRun
// (run.v2 branch) exactly: six keys, canonical UUIDs, lowercase 64-hex hash.
func testRunAgentSnapshot() json.RawMessage {
	return testRunAgentSnapshotWithLock(testModelLockVersion)
}

func testRunAgentSnapshotWithLock(lock int64) json.RawMessage {
	raw, err := json.Marshal(map[string]any{
		"schemaVersion":          execution.AgentSnapshotSchemaV1,
		"agentId":                testAgentUUID,
		"promptRevisionId":       testPromptRev,
		"promptRevisionHash":     testFrozenPromptHash(),
		"modelConfigId":          testModelUUID,
		"modelConfigLockVersion": lock,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

// mustAgenticCaps mirrors what modelconfig verification actually persists:
// the CAS stamps verifiedLockVersion = pre-CAS lock and then bumps lock_version,
// so a readable VERIFIED row always has verifiedLockVersion == LockVersion-1.
// Fixtures therefore start at LockVersion 2 (see testModelCfg).
func mustAgenticCaps(t *testing.T, cfg modelconfig.Config) json.RawMessage {
	t.Helper()
	digest := modelconfig.WireConfigDigest(cfg)
	doc, err := modelconfig.CanonicalAgenticCapabilities(
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), cfg.LockVersion-1, digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustAgenticCapsV2(t *testing.T, cfg modelconfig.Config, toolCalling string) json.RawMessage {
	t.Helper()
	doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(
		toolCalling,
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		cfg.LockVersion-1,
		modelconfig.WireConfigDigest(cfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testModelCfg(t *testing.T) modelconfig.Config {
	t.Helper()
	cfg := modelconfig.Config{
		ID:                  testModelUUID,
		WorkspaceID:         testWSUUID,
		Provider:            "openai",
		APIBase:             "https://api.example.com/v1",
		ModelName:           "gpt-test",
		Options:             json.RawMessage(`{}`),
		RuntimeCapabilities: json.RawMessage(`{}`),
		// 2 is the lowest lock a VERIFIED row can carry (create=1, verify CAS=+1).
		LockVersion: testModelLockVersion,
		Status:      modelconfig.StatusVerified,
	}
	cfg.AgenticCapabilities = mustAgenticCaps(t, cfg)
	return cfg
}

func marshalTestModelSnapshot(t *testing.T, cfg modelconfig.Config) json.RawMessage {
	t.Helper()
	m := map[string]any{
		"id": cfg.ID, "provider": cfg.Provider, "apiBase": cfg.APIBase,
		"modelName": cfg.ModelName, "options": cfg.Options,
		"status": cfg.Status, "lockVersion": cfg.LockVersion,
		"agenticCapabilities": cfg.AgenticCapabilities,
		"runtimeCapabilities": cfg.RuntimeCapabilities,
	}
	if cfg.CredentialSecretID != nil {
		m["credentialSecretId"] = *cfg.CredentialSecretID
	}
	if len(cfg.ToolDisclosurePolicy) > 0 {
		m["toolDisclosurePolicy"] = cfg.ToolDisclosurePolicy
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testContextPolicyV1(t *testing.T, window, maxIn, reserve, safety int64) json.RawMessage {
	t.Helper()
	doc, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		WorkspacePolicy: json.RawMessage(`{
			"schemaVersion":"session-context-policy.v1",
			"mode":"token_window",
			"maxInputTokens":` + itoa(maxIn) + `,
			"outputReserveTokens":` + itoa(reserve) + `,
			"safetyMarginTokens":` + itoa(safety) + `
		}`),
		ContextWindowTokens:        window,
		DefaultOutputReserveTokens: reserve,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		RolloutVersion:             "agentic-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Mode != sessioncontext.ModeTokenWindow {
		t.Fatalf("mode=%s", doc.Mode)
	}
	return raw
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

type agenticSessions struct {
	messages []chat.Message
}

func (s *agenticSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}
func (s *agenticSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return s.messages, nil
}
func (s *agenticSessions) ListMessagesReversePage(
	_ context.Context, _, _ string, limit int, cursor *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	msgs := append([]chat.Message(nil), s.messages...)
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
func (s *agenticSessions) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	for _, m := range s.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}

type agenticResults struct {
	mu        sync.Mutex
	content   string
	messageID string
}

func (r *agenticResults) RecordAssistantResult(_ context.Context, in chat.RecordAssistantResultInput) (chat.RecordAssistantResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.content = in.Content
	r.messageID = in.AssistantMessageID
	return chat.RecordAssistantResult{Message: chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "COMPLETED", RunID: in.RunID,
		CreatedAt: time.Now().UTC(),
	}}, nil
}

type agenticAgents struct {
	modelConfigID string
	revisions     []agent.PromptRevision
	revisionsErr  error
}

func (a agenticAgents) Get(_ context.Context, workspaceID, agentID string) (agent.Agent, error) {
	mid := a.modelConfigID
	if mid == "" {
		mid = testModelUUID
	}
	return agent.Agent{
		ID: agentID, WorkspaceID: workspaceID, ModelConfigID: mid,
		Status: agent.StatusActive,
	}, nil
}

// ListPromptRevisions serves the frozen revision referenced by
// testRunAgentSnapshot. revisions/hash may be overridden per test to exercise
// the MAJOR-4 drift and missing-revision paths.
func (a agenticAgents) ListPromptRevisions(context.Context, string, string) ([]agent.PromptRevision, error) {
	if a.revisions != nil {
		return a.revisions, nil
	}
	if a.revisionsErr != nil {
		return nil, a.revisionsErr
	}
	return []agent.PromptRevision{{
		ID: testPromptRev, WorkspaceID: testWSUUID, AgentID: testAgentUUID,
		RevisionNo: 1, SystemPrompt: testFrozenPrompt, Source: agent.PromptSourceManual,
		ContentSHA256: testFrozenPromptHash(),
	}}, nil
}

type agenticModels struct {
	cfg modelconfig.Config
}

func (m agenticModels) Get(_ context.Context, workspaceID, id string) (modelconfig.Config, error) {
	out := m.cfg
	if out.ID == "" {
		out.ID = id
	}
	out.WorkspaceID = workspaceID
	return out, nil
}

type agenticRuns struct {
	run execution.AgentRun
}

func (r *agenticRuns) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	out := r.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

func emptyCapSnap() json.RawMessage {
	return json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`)
}

// producerNodeModelSnap is the node-local model shape from snapshotAgentNode
// (not root run.ModelSnapshot / marshalModelSnapshot).
func producerNodeModelSnap(modelID string) json.RawMessage {
	return json.RawMessage(`{` +
		`"id":"` + modelID + `",` +
		`"provider":"openai",` +
		`"apiBase":"https://api.example.com/v1",` +
		`"modelName":"gpt-test",` +
		`"options":{},` +
		`"credentialSecretId":null,` +
		`"lockVersion":` + itoa(testModelLockVersion) + `,` +
		`"status":"VERIFIED",` +
		`"agenticCapabilities":{},` +
		`"runtimeCapabilities":{},` +
		`"toolDisclosurePolicy":{}` +
		`}`)
}

func producerNodeAgentSnap(agentID, modelID string) json.RawMessage {
	return json.RawMessage(`{` +
		`"schemaVersion":"agent-binding.v1",` +
		`"agentId":"` + agentID + `",` +
		`"name":"Agent",` +
		`"roleDescription":"",` +
		`"promptRevisionId":"",` +
		`"promptRevisionHash":"",` +
		`"modelConfigId":"` + modelID + `",` +
		`"modelConfigLockVer":` + itoa(testModelLockVersion) +
		`}`)
}

// explicitEmptyAgentGraph builds a valid agent_graph_snapshot.v1 with zero edges
// and RemotesFrozen=true (explicit empty). Not "{}" — that is invalid for Task4A.
// Nested node snapshots use the node producer schema (not root model shape).
// The ignored rootModelSnap parameter is retained for call-site compatibility.
func explicitEmptyAgentGraph(t *testing.T, agentID, modelID string, _ json.RawMessage) json.RawMessage {
	t.Helper()
	nodeModel := producerNodeModelSnap(modelID)
	agentSnap := producerNodeAgentSnap(agentID, modelID)
	capSnap := emptyCapSnap()
	snap := agentdelegation.GraphSnapshotV1{
		SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
		RootAgentID:   agentID,
		MaxDepth:      agentdelegation.DefaultMaxDepth,
		MaxTotal:      agentdelegation.DefaultMaxTotalDelegations,
		MaxPerBinding: agentdelegation.DefaultMaxPerBinding,
		Nodes: []agentdelegation.GraphNodeSnapshot{{
			AgentID:            agentID,
			ModelConfigID:      modelID,
			ModelConfigLockVer: testModelLockVersion,
			ModelSnapshot:      nodeModel,
			AgentSnapshot:      agentSnap,
			CapabilitySnapshot: capSnap,
			Depth:              0,
		}},
		Edges:                 nil,
		FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{agentID: {}},
		RemotesFrozen:         true,
		BuiltAt:               time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	raw, err := agentdelegation.SnapshotJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip integrity: production ParseSnapshot must accept this.
	if parsed, err := agentdelegation.ParseSnapshot(testWSUUID, raw); err != nil || parsed == nil {
		t.Fatalf("empty graph fixture invalid: %v", err)
	}
	return raw
}

// toolCapSnap mirrors the SnapshotAgentRun release shape byte-for-byte:
// all ten always-present keys plus connectionId (canonical UUIDs, closed enums).
func toolCapSnap(name, desc string, schemaObj string) json.RawMessage {
	if schemaObj == "" {
		schemaObj = `{"type":"object","properties":{"q":{"type":"string"}}}`
	}
	return json.RawMessage(`{
		"schemaVersion":"capability-snapshot.v1",
		"releases":[{
			"capabilityId":"` + testCapUUID + `","releaseId":"` + testRelUUID + `","kind":"TOOL",
			"callableName":"` + name + `","callableDescription":"` + desc + `",
			"inputSchema":` + schemaObj + `,"outputSchema":{},
			"riskLevel":"LOW","sideEffectLevel":"NONE",
			"requiresConfirmation":false,"connectionId":"` + testConnUUID + `"
		}]
	}`)
}

type secretOpenerFunc func(ctx context.Context, workspaceID, secretID string, use func([]byte) error) error

func (f secretOpenerFunc) WithActiveSecret(ctx context.Context, workspaceID, secretID string, use func([]byte) error) error {
	return f(ctx, workspaceID, secretID, use)
}

var _ modelapi.SecretOpener = secretOpenerFunc(nil)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// agenticFixture holds counters + deps for Bridge.Execute initial path tests.
type agenticFixture struct {
	cfg            modelconfig.Config
	mdl            *scriptedAgenticModel
	classic        *classicBuilderSpy
	agentic        *agenticCallCounter
	sinks          *sinkCounter
	events         *eventCounter
	assemblies     *memAssemblies
	results        *agenticResults
	store          *memStore
	run            execution.AgentRun
	messages       []chat.Message
	delegation     *chatruntimebridge.DelegationDeps
	agents         agenticAgents
	invoker        *bridgeToolInvoker
	toolDisclosure config.RuntimeFeatureRollout
	// steps and modelTurns are opt-in: MODEL audit evidence is only persisted
	// when both are wired, so tests that do not assert on it leave them nil.
	steps      *agenticSteps
	modelTurns *agenticModelTurns
}

func newAgenticFixture(t *testing.T, mutate func(*agenticFixture)) *agenticFixture {
	t.Helper()
	cfg := testModelCfg(t)
	modelSnap := marshalTestModelSnapshot(t, cfg)
	f := &agenticFixture{
		cfg:        cfg,
		mdl:        &scriptedAgenticModel{responses: []*schema.AgenticMessage{agenticmsg.AssistantText("agentic-ok")}},
		classic:    &classicBuilderSpy{},
		sinks:      &sinkCounter{},
		events:     &eventCounter{},
		assemblies: newMemAssemblies(),
		results:    &agenticResults{},
		store:      newMemStore(),
		messages: []chat.Message{{
			ID: testMsgUUID, SessionID: testSessUUID, Role: "USER", Content: "hello agentic",
			Status: "COMPLETED", CreatedAt: time.Now().UTC(),
		}},
		run: execution.AgentRun{
			ID: testRunUUID, WorkspaceID: testWSUUID, SessionID: testSessUUID,
			AgentID: testAgentUUID, Status: "RUNNING",
			SnapshotSchemaVersion: execution.RunSnapshotSchemaV2,
			CapabilitySnapshot:    emptyCapSnap(),
			ModelSnapshot:         modelSnap,
			ContextPolicySnapshot: testContextPolicyV1(t, 128000, 100000, 4096, 2048),
			AgentSnapshot:         testRunAgentSnapshot(),
			AgentGraphSnapshot:    explicitEmptyAgentGraph(t, testAgentUUID, testModelUUID, modelSnap),
			TriggeredByType:       "USER", TriggeredByID: "user-1", TraceID: "trace-1",
			LockVersion: 1,
		},
	}
	f.agentic = &agenticCallCounter{model: f.mdl}
	f.agents = agenticAgents{modelConfigID: testModelUUID}
	if mutate != nil {
		mutate(f)
	}
	return f
}

func (f *agenticFixture) bridge(t *testing.T) *chatruntimebridge.Bridge {
	t.Helper()
	deps := chatruntimebridge.Dependencies{
		Sessions:          &agenticSessions{messages: f.messages},
		Results:           f.results,
		Agents:            f.agents,
		Models:            agenticModels{cfg: f.cfg},
		Runs:              &agenticRuns{run: f.run},
		Events:            f.events,
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
		BuildAgenticModel: f.agentic.Build,
		TextSinkFactory:   f.sinks.Factory,
		Assemblies:        f.assemblies,
		Delegation:        f.delegation,
		ToolDisclosure:    f.toolDisclosure,
	}
	if f.steps != nil {
		deps.Steps = f.steps
	}
	if f.modelTurns != nil {
		deps.ModelTurns = f.modelTurns
	}
	if f.invoker != nil {
		deps.ToolInvoker = f.invoker
	}
	b, err := chatruntimebridge.NewBridge(deps)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *agenticFixture) job() agentrun.Job {
	return agentrun.Job{
		WorkspaceID: testWSUUID, SessionID: testSessUUID, RunID: testRunUUID,
		UserMessageID: testMsgUUID, ActorID: "actor-1", InitialEventsCommitted: true,
	}
}

func (f *agenticFixture) assertNoSideEffects(t *testing.T) {
	t.Helper()
	if f.agentic.calls.Load() != 0 {
		t.Fatalf("agentic builder calls=%d want 0", f.agentic.calls.Load())
	}
	if f.classic.calls.Load() != 0 {
		t.Fatalf("classic builder calls=%d want 0", f.classic.calls.Load())
	}
	if f.mdl.calls.Load() != 0 {
		t.Fatalf("model calls=%d want 0", f.mdl.calls.Load())
	}
	if f.sinks.opens.Load() != 0 {
		t.Fatalf("sink opens=%d want 0", f.sinks.opens.Load())
	}
	// Protocol events may record run.failed on failRun — allow 0 only for pure pre-failRun paths.
	// Failures after Execute's failRun may emit; we check builder/model/sink only.
}

// --- cumulative first-cycle + repair-cycle ledger ---

func TestAgenticInitial_ToollessFunctionCallingAndNoneCanPlan(t *testing.T) {
	for _, calling := range []string{
		modelconfig.ToolCallingFunctionCalling,
		modelconfig.ToolCallingNone,
	} {
		t.Run(calling, func(t *testing.T) {
			f := newAgenticFixture(t, func(f *agenticFixture) {
				f.cfg.AgenticCapabilities = mustAgenticCapsV2(t, f.cfg, calling)
				f.run.ModelSnapshot = marshalTestModelSnapshot(t, f.cfg)
			})
			if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
				t.Fatalf("tool-less %s must plan and run: %v", calling, err)
			}
			if f.agentic.calls.Load() != 1 {
				t.Fatalf("agentic builder=%d", f.agentic.calls.Load())
			}
		})
	}
}

func TestAgenticInitial_ToolBearingNonNativeFailClosed(t *testing.T) {
	cases := []struct {
		calling string
		want    error
	}{
		{modelconfig.ToolCallingFunctionCalling, modelconfig.ErrToolDisclosureNotRolledOut},
		{modelconfig.ToolCallingNone, modelconfig.ErrAgentModelToolsUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.calling, func(t *testing.T) {
			f := newAgenticFixture(t, func(f *agenticFixture) {
				f.cfg.AgenticCapabilities = mustAgenticCapsV2(t, f.cfg, tc.calling)
				f.run.ModelSnapshot = marshalTestModelSnapshot(t, f.cfg)
				f.run.CapabilitySnapshot = toolCapSnap("lookup_order", "find order",
					`{"type":"object","properties":{"id":{"type":"string"}}}`)
				f.invoker = &bridgeToolInvoker{spy: &spyInvoker{}, free: true}
			})
			err := f.bridge(t).Execute(context.Background(), f.job())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if f.mdl.calls.Load() != 0 {
				t.Fatal("model must not be invoked for fail-closed tool-bearing non-native")
			}
		})
	}
}

func TestAgenticInitial_FunctionCallingPlatformWritesV2Assembly(t *testing.T) {
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.cfg.AgenticCapabilities = mustAgenticCapsV2(t, f.cfg, modelconfig.ToolCallingFunctionCalling)
		policy, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModePlatformOnDemand)
		if err != nil {
			t.Fatal(err)
		}
		f.cfg.ToolDisclosurePolicy = policy
		f.run.ModelSnapshot = marshalTestModelSnapshot(t, f.cfg)
		f.run.CapabilitySnapshot = toolCapSnap("lookup_order", "find order",
			`{"type":"object","properties":{"id":{"type":"string"}}}`)
		f.invoker = &bridgeToolInvoker{spy: &spyInvoker{}, free: true}
		f.toolDisclosure = config.RuntimeFeatureRollout{Enabled: true, AllowAllWorkspaces: true}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("rolled-out FC: %v", err)
	}
	rec, ok := f.assemblies.Get(testWSUUID, testRunUUID)
	if !ok {
		t.Fatal("missing assembly")
	}
	if rec.ToolSearchMode != execution.AssemblyToolSearchModePlatformBounded {
		t.Fatalf("mode=%s", rec.ToolSearchMode)
	}
	if rec.EstimatorVersion != contextwindow.EstimatorVersionAgenticOpenAIResponsesV2 {
		t.Fatalf("estimator=%s", rec.EstimatorVersion)
	}
}

func TestAgenticInitial_FrozenCarryAllIgnoresLiveSetDisclosure(t *testing.T) {
	carry, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModeCarryAll)
	if err != nil {
		t.Fatal(err)
	}
	onDemand, err := modelconfig.CanonicalToolDisclosurePolicy(modelconfig.DisclosureModePlatformOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.cfg.AgenticCapabilities = mustAgenticCapsV2(t, f.cfg, modelconfig.ToolCallingFunctionCalling)
		f.cfg.ToolDisclosurePolicy = carry
		f.run.ModelSnapshot = marshalTestModelSnapshot(t, f.cfg)
		f.run.CapabilitySnapshot = toolCapSnap("lookup_order", "find order",
			`{"type":"object","properties":{"id":{"type":"string"}}}`)
		f.invoker = &bridgeToolInvoker{spy: &spyInvoker{}, free: true}
		f.toolDisclosure = config.RuntimeFeatureRollout{Enabled: true, AllowAllWorkspaces: true}
		// Live config after set-disclosure no longer matches the freeze.
		f.cfg.ToolDisclosurePolicy = onDemand
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("in-flight carry_all: %v", err)
	}
	rec, ok := f.assemblies.Get(testWSUUID, testRunUUID)
	if !ok {
		t.Fatal("missing assembly")
	}
	if rec.ToolSearchMode != execution.AssemblyToolSearchModeCarryAll {
		t.Fatalf("in-flight must keep frozen carry_all, got %s", rec.ToolSearchMode)
	}
}

func TestAgenticInitial_NeverCallsClassicBuilder(t *testing.T) {
	f := newAgenticFixture(t, nil)
	b := f.bridge(t)
	if err := b.Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.classic.calls.Load() != 0 {
		t.Fatalf("classic called %d", f.classic.calls.Load())
	}
	if f.agentic.calls.Load() != 1 {
		t.Fatalf("agentic builder=%d", f.agentic.calls.Load())
	}
	if f.mdl.calls.Load() < 1 {
		t.Fatal("expected model call")
	}
	if f.assemblies.inserts.Load() != 1 {
		t.Fatalf("assembly inserts=%d want 1", f.assemblies.inserts.Load())
	}
	rec, ok := f.assemblies.Get(testWSUUID, testRunUUID)
	if !ok || rec.ToolSearchMode != execution.AssemblyToolSearchModeClientBounded {
		t.Fatalf("manifest: ok=%v mode=%q", ok, rec.ToolSearchMode)
	}
	if rec.EstimatorVersion != contextwindow.EstimatorVersionAgenticOpenAIResponsesV1 {
		t.Fatalf("estimator version=%q", rec.EstimatorVersion)
	}
}

func TestAgenticInitial_WireCapture_ClientToolSearch(t *testing.T) {
	ctx := context.Background()
	cfg := testModelCfg(t)
	var bodies []map[string]any
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad", 400)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		delta, _ := json.Marshal(map[string]any{
			"type": "response.output_text.delta", "content_index": 0, "delta": "wire-ok",
			"item_id": "msg_1", "output_index": 0, "sequence_number": 1,
		})
		completed, _ := json.Marshal(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_test_stream", "object": "response", "status": "completed",
				"model": "gpt-test",
				"output": []map[string]any{{
					"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
					"content": []map[string]any{{
						"type": "output_text", "text": "wire-ok", "annotations": []any{},
					}},
				}},
				"usage": map[string]any{
					"input_tokens": 3, "output_tokens": 2, "total_tokens": 5,
					"input_tokens_details":  map[string]any{"cached_tokens": 0},
					"output_tokens_details": map[string]any{"reasoning_tokens": 0},
				},
			},
			"sequence_number": 2,
		})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: response.output_text.delta\ndata: " + string(delta) + "\n\n" +
				"event: response.completed\ndata: " + string(completed) + "\n\n",
		))
	}))
	t.Cleanup(server.Close)

	cfg.APIBase = server.URL + "/v1"
	secretID := "sec-wire-1"
	cfg.CredentialSecretID = &secretID
	cfg.AgenticCapabilities = mustAgenticCaps(t, cfg)

	opener := secretOpenerFunc(func(_ context.Context, _, _ string, use func([]byte) error) error {
		return use([]byte("sk-wire-test"))
	})
	classic := &classicBuilderSpy{}
	agenticBuilds := atomic.Int64{}
	assemblies := newMemAssemblies()
	store := newMemStore()
	capSnap := toolCapSnap("lookup_order", "find order", `{"type":"object","properties":{"id":{"type":"string"}}}`)
	invoker := &bridgeToolInvoker{spy: &spyInvoker{}}
	modelSnap := marshalTestModelSnapshot(t, cfg)

	bridge, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: &agenticSessions{messages: []chat.Message{{
			ID: testMsgUUID, SessionID: testSessUUID, Role: "USER", Content: "lookup order 1",
			Status: "COMPLETED", CreatedAt: time.Now().UTC(),
		}}},
		Results: &agenticResults{},
		Agents:  agenticAgents{modelConfigID: testModelUUID},
		Models:  agenticModels{cfg: cfg},
		Runs: &agenticRuns{run: execution.AgentRun{
			ID: testRunUUID, WorkspaceID: testWSUUID, SessionID: testSessUUID,
			AgentID: testAgentUUID, Status: "RUNNING",
			SnapshotSchemaVersion: execution.RunSnapshotSchemaV2,
			CapabilitySnapshot:    capSnap,
			ModelSnapshot:         modelSnap,
			ContextPolicySnapshot: testContextPolicyV1(t, 128000, 100000, 4096, 2048),
			AgentSnapshot:         testRunAgentSnapshot(),
			AgentGraphSnapshot:    explicitEmptyAgentGraph(t, testAgentUUID, testModelUUID, modelSnap),
			TriggeredByType:       "USER", TriggeredByID: "user-1", TraceID: "trace-1",
			LockVersion: 1,
		}},
		Events:        &eventCounter{},
		ToolInvoker:   invoker,
		AgenticEngine: einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: store}),
		BuildAgenticModel: func(ctx context.Context, c modelconfig.Config) (model.AgenticModel, error) {
			agenticBuilds.Add(1)
			return modelapi.NewOpenAIAgenticModel(ctx, modelapi.NewStreamingHTTPClient(), opener, c)
		},
		Assemblies:      assemblies,
		TextSinkFactory: (&sinkCounter{}).Factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	execErr := bridge.Execute(ctx, agentrun.Job{
		WorkspaceID: testWSUUID, SessionID: testSessUUID, RunID: testRunUUID,
		UserMessageID: testMsgUUID, ActorID: "actor-1", InitialEventsCommitted: true,
	})
	if classic.calls.Load() != 0 {
		t.Fatal("classic builder called")
	}
	mu.Lock()
	n := len(bodies)
	mu.Unlock()
	if n == 0 {
		t.Fatalf("expected Responses body; Execute err=%v agenticBuilds=%d assemblies=%d",
			execErr, agenticBuilds.Load(), assemblies.inserts.Load())
	}
	body := bodies[0]
	if store, ok := body["store"].(bool); !ok || store {
		t.Fatalf("store=%v", body["store"])
	}
	if p, ok := body["parallel_tool_calls"].(bool); ok && p {
		t.Fatalf("parallel_tool_calls=%v", p)
	}
	cacheKey, _ := body["prompt_cache_key"].(string)
	if !strings.HasPrefix(cacheKey, "aw:agentic:v1:") {
		t.Fatalf("cache key=%q", cacheKey)
	}
	for _, s := range []string{testWSUUID, testRunUUID, "lookup order 1", "sk-wire"} {
		if strings.Contains(cacheKey, s) {
			t.Fatalf("key leaked %q", s)
		}
	}
	rawTools, _ := json.Marshal(body["tools"])
	if !strings.Contains(string(rawTools), "tool_search") {
		t.Fatalf("missing tool_search: %s", rawTools)
	}
	if !strings.Contains(string(rawTools), "defer_loading") {
		t.Fatalf("missing defer_loading: %s", rawTools)
	}
}

func TestAgenticInitial_CacheKeyStabilityAcrossIdentityChanges(t *testing.T) {
	cfg := testModelCfg(t)
	empty, err := einoruntime.BuildToolCatalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	system := "You are helpful."
	in := contextwindow.PromptCacheKeyInput{
		ProviderProtocol:   contextwindow.PromptCacheProviderProtocolOpenAIResponsesV1,
		ModelConfigID:      cfg.ID,
		ModelLockVersion:   cfg.LockVersion,
		PromptRevisionHash: sha256Hex(system),
		CatalogDigest:      empty.CatalogDigest(),
		AdapterVersion:     contextwindow.PromptCacheAdapterAgenticOpenAIV022,
	}
	k1, err := contextwindow.BuildAgenticPromptCacheKey(in)
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := contextwindow.BuildAgenticPromptCacheKey(in)
	if k1 != k2 {
		t.Fatal("unstable")
	}
	in.ModelLockVersion++
	k3, _ := contextwindow.BuildAgenticPromptCacheKey(in)
	if k3 == k1 {
		t.Fatal("lock should change key")
	}
}

func TestAgenticInitial_MessageMappingPreservesRoles(t *testing.T) {
	var saw []*schema.AgenticMessage
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.messages = []chat.Message{
			{ID: "m0", SessionID: testSessUUID, Role: "ASSISTANT", Content: "prior answer", Status: "COMPLETED", CreatedAt: time.Now().UTC().Add(-time.Minute)},
			{ID: testMsgUUID, SessionID: testSessUUID, Role: "USER", Content: "follow up", Status: "COMPLETED", CreatedAt: time.Now().UTC()},
		}
		f.mdl.onCall = func(_ int, input []*schema.AgenticMessage, _ []model.Option) {
			saw = append([]*schema.AgenticMessage(nil), input...)
		}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatal(err)
	}
	if len(saw) < 2 {
		t.Fatalf("messages=%d", len(saw))
	}
	last := saw[len(saw)-1]
	if last.Role != schema.AgenticRoleTypeUser {
		t.Fatalf("role=%s", last.Role)
	}
	for _, m := range saw {
		for _, b := range m.ContentBlocks {
			if b != nil && (b.FunctionToolCall != nil || b.Reasoning != nil) {
				t.Fatalf("leaked tool/reasoning: %+v", b)
			}
		}
	}
}

// TestAgenticInitial_Ledger_RepairCycle2 is the cumulative real-entry matrix for
// freeze-only model, no delegation attach, and mandatory preflight/manifest.
func TestAgenticInitial_Ledger_RepairCycle2(t *testing.T) {
	type tc struct {
		name       string
		mutate     func(*agenticFixture)
		wantSubstr string // error must contain
		wantCode   string // optional stable code via executionErrorCode path
	}
	cases := []tc{
		{
			name: "absent_model_snapshot_no_live_fallback",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = nil
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "empty_model_snapshot_object",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(`{}`)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "null_model_snapshot",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(`null`)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "malformed_model_snapshot_json",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(`{not-json`)
			},
			wantSubstr: "AGENTIC_MODEL_SNAPSHOT_REQUIRED",
		},
		{
			name: "unverified_agentic_capabilities",
			mutate: func(f *agenticFixture) {
				f.run.ModelSnapshot = json.RawMessage(`{
					"id":"` + testModelUUID + `","provider":"openai","apiBase":"https://api.example.com/v1",
					"modelName":"gpt-test","options":{},"status":"VERIFIED","lockVersion":1,
					"agenticCapabilities":{},"runtimeCapabilities":{}
				}`)
			},
			wantSubstr: "CONTEXT_ASSEMBLY_FAILED",
		},
		{
			name: "stale_lock_version",
			mutate: func(f *agenticFixture) {
				// caps verified at lock 1 (CAS pair is lock 2); snapshot claims lock 3,
				// i.e. the config moved on after verification.
				raw := map[string]any{
					"id": f.cfg.ID, "provider": f.cfg.Provider, "apiBase": f.cfg.APIBase,
					"modelName": f.cfg.ModelName, "options": json.RawMessage(`{}`),
					"status": "VERIFIED", "lockVersion": 3,
					"agenticCapabilities": f.cfg.AgenticCapabilities,
					"runtimeCapabilities": json.RawMessage(`{}`),
				}
				b, _ := json.Marshal(raw)
				f.run.ModelSnapshot = b
			},
			wantSubstr: "CONTEXT_ASSEMBLY_FAILED",
		},
		{
			name: "cross_config_agent_binding_mismatch",
			mutate: func(f *agenticFixture) {
				// Keep snapshot id = testModelUUID; agent returns different model id via Models only —
				// Agents.Get ModelConfigID must differ.
			},
			// Use custom agents in mutate via bridge rebuild — handled below with special case
			wantSubstr: "", // special
		},
		{
			name: "legacy_context_policy_no_preflight_bypass",
			mutate: func(f *agenticFixture) {
				f.run.ContextPolicySnapshot = json.RawMessage(`{}`)
			},
			wantSubstr: "CONTEXT_MODEL_LIMIT_UNKNOWN",
		},
		{
			name: "missing_snapshot_schema_version",
			mutate: func(f *agenticFixture) {
				f.run.SnapshotSchemaVersion = ""
			},
			wantSubstr: "CONTEXT_SNAPSHOT_UNSUPPORTED",
		},
		{
			name: "run_schema_v1_rejected",
			mutate: func(f *agenticFixture) {
				f.run.SnapshotSchemaVersion = execution.RunSnapshotSchemaV1
			},
			wantSubstr: "CONTEXT_SNAPSHOT_UNSUPPORTED",
		},
		{
			name: "nil_assemblies_fail_closed",
			mutate: func(f *agenticFixture) {
				f.assemblies = nil
			},
			wantSubstr: "CONTEXT_ASSEMBLY_FAILED",
		},
		{
			name: "mandatory_too_large_preflight",
			mutate: func(f *agenticFixture) {
				// Valid policy with small ceiling; huge user input → preflight reject.
				// reserve+safety must leave positive maxInput under window.
				f.run.ContextPolicySnapshot = testContextPolicyV1(t, 2000, 400, 200, 100)
				f.messages = []chat.Message{{
					ID: testMsgUUID, SessionID: testSessUUID, Role: "USER",
					Content: strings.Repeat("word ", 8000),
					Status:  "COMPLETED", CreatedAt: time.Now().UTC(),
				}}
			},
			wantSubstr: "CONTEXT_REQUIRED_INPUT_TOO_LARGE",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "cross_config_agent_binding_mismatch" {
				f := newAgenticFixture(t, nil)
				// Rebuild bridge with agent bound to different model id.
				classic := f.classic
				agentic := f.agentic
				b, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
					Sessions:          &agenticSessions{messages: f.messages},
					Results:           f.results,
					Agents:            agenticAgents{modelConfigID: "f66ce000-0000-4000-8000-000000000099"},
					Models:            agenticModels{cfg: f.cfg},
					Runs:              &agenticRuns{run: f.run},
					Events:            f.events,
					AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
					BuildAgenticModel: agentic.Build,
					Assemblies:        f.assemblies,
					TextSinkFactory:   f.sinks.Factory,
				})
				if err != nil {
					t.Fatal(err)
				}
				err = b.Execute(context.Background(), f.job())
				if err == nil || !strings.Contains(err.Error(), "AGENTIC_MODEL_SNAPSHOT_REQUIRED") {
					t.Fatalf("err=%v", err)
				}
				if agentic.calls.Load() != 0 || classic.calls.Load() != 0 || f.mdl.calls.Load() != 0 || f.sinks.opens.Load() != 0 {
					t.Fatalf("side effects: agentic=%d classic=%d model=%d sink=%d",
						agentic.calls.Load(), classic.calls.Load(), f.mdl.calls.Load(), f.sinks.opens.Load())
				}
				return
			}

			f := newAgenticFixture(t, tc.mutate)
			// nil assemblies: bridge deps need explicit nil
			var b *chatruntimebridge.Bridge
			var err error
			if f.assemblies == nil {
				b, err = chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
					Sessions:          &agenticSessions{messages: f.messages},
					Results:           f.results,
					Agents:            agenticAgents{modelConfigID: testModelUUID},
					Models:            agenticModels{cfg: f.cfg},
					Runs:              &agenticRuns{run: f.run},
					Events:            f.events,
					AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
					BuildAgenticModel: f.agentic.Build,
					Assemblies:        nil,
					TextSinkFactory:   f.sinks.Factory,
				})
			} else {
				b = f.bridge(t)
			}
			if err != nil {
				t.Fatal(err)
			}
			execErr := b.Execute(context.Background(), f.job())
			if execErr == nil {
				t.Fatal("expected fail closed")
			}
			if tc.wantSubstr != "" && !strings.Contains(execErr.Error(), tc.wantSubstr) {
				t.Fatalf("err=%v want substr %q", execErr, tc.wantSubstr)
			}
			if f.agentic.calls.Load() != 0 {
				t.Fatalf("agentic builder=%d", f.agentic.calls.Load())
			}
			if f.classic.calls.Load() != 0 {
				t.Fatalf("classic builder=%d", f.classic.calls.Load())
			}
			if f.mdl.calls.Load() != 0 {
				t.Fatalf("model calls=%d", f.mdl.calls.Load())
			}
			if f.sinks.opens.Load() != 0 {
				t.Fatalf("sink opens=%d", f.sinks.opens.Load())
			}
			if f.assemblies != nil && f.assemblies.inserts.Load() != 0 && tc.name != "mandatory_too_large_preflight" {
				// preflight failures must not persist assembly
				if f.assemblies.inserts.Load() != 0 {
					t.Fatalf("assembly inserts=%d want 0", f.assemblies.inserts.Load())
				}
			}
			if tc.name == "mandatory_too_large_preflight" && f.assemblies.inserts.Load() != 0 {
				t.Fatalf("preflight fail must not insert assembly, inserts=%d", f.assemblies.inserts.Load())
			}
		})
	}
}

// TestAgenticInitial_FrozenEmptyGraph_IgnoresPostFreezeLiveEdges: valid
// agent_graph_snapshot.v1 with explicit empty edges proceeds even when
// Delegation.Bindings would return a live edge (must never ListEnabledEdges for decision).
func TestAgenticInitial_FrozenEmptyGraph_IgnoresPostFreezeLiveEdges(t *testing.T) {
	listCalls := atomic.Int64{}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.classic.panic = true
		// Graph already explicit-empty in fixture; add live edge lister that must not be consulted.
		f.delegation = &chatruntimebridge.DelegationDeps{
			Bindings: countingEdgeLister{
				calls: &listCalls,
				edges: []agentdelegation.GraphEdgeSnapshot{{
					BindingID: "bind-1", CallerAgentID: testAgentUUID,
					TargetAgentID: "f77ce000-0000-4000-8000-000000000077",
					CallableName:  "delegate_child", Mode: "TASK",
					ContextPolicy: "NONE", Protocol: "INTERNAL", Version: 1,
				}},
			},
		}
	})
	if err := f.bridge(t).Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("ListEnabledEdges called %d times on initial path (must be 0)", listCalls.Load())
	}
	if f.agentic.calls.Load() != 1 {
		t.Fatalf("agentic builder=%d", f.agentic.calls.Load())
	}
	if f.classic.calls.Load() != 0 {
		t.Fatal("classic builder called")
	}
}

// TestAgenticInitial_FrozenNonemptyGraph_RequiresDelegationAudit: a valid freeze
// with edges is accepted as topology (Task 5) but still fails closed before any
// model construction when DelegationDeps.Audit is missing — and never consults
// live ListEnabledEdges.
func TestAgenticInitial_FrozenNonemptyGraph_RequiresDelegationAudit(t *testing.T) {
	listCalls := atomic.Int64{}
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.classic.panic = true
		childID := "f77ce000-0000-4000-8000-000000000077"
		nodeModel := producerNodeModelSnap(testModelUUID)
		capSnap := emptyCapSnap()
		snap := agentdelegation.GraphSnapshotV1{
			SchemaVersion: agentdelegation.GraphSnapshotSchemaV1,
			RootAgentID:   testAgentUUID,
			MaxDepth:      2, MaxTotal: 8, MaxPerBinding: 3,
			Nodes: []agentdelegation.GraphNodeSnapshot{
				{AgentID: testAgentUUID, ModelConfigID: testModelUUID, ModelConfigLockVer: testModelLockVersion,
					ModelSnapshot: nodeModel, AgentSnapshot: producerNodeAgentSnap(testAgentUUID, testModelUUID),
					CapabilitySnapshot: capSnap, Depth: 0},
				{AgentID: childID, ModelConfigID: testModelUUID, ModelConfigLockVer: testModelLockVersion,
					ModelSnapshot: nodeModel, AgentSnapshot: producerNodeAgentSnap(childID, testModelUUID),
					CapabilitySnapshot: capSnap, Depth: 1},
			},
			Edges: []agentdelegation.GraphEdgeSnapshot{{
				BindingID: "a11ce000-0000-4000-8000-0000000000b1", CallerAgentID: testAgentUUID, TargetAgentID: childID,
				CallableName: "child", Mode: "TASK", ContextPolicy: agentdelegation.ContextTaskOnly,
				Protocol: "INTERNAL", Version: 1,
			}},
			FrozenRemotesByCaller: map[string][]agentdelegation.FrozenRemoteBinding{
				testAgentUUID: {}, childID: {},
			},
			RemotesFrozen: true,
			BuiltAt:       time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		}
		raw, err := agentdelegation.SnapshotJSON(snap)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agentdelegation.ParseSnapshot(testWSUUID, raw); err != nil {
			t.Fatalf("fixture graph: %v", err)
		}
		f.run.AgentGraphSnapshot = raw
		f.delegation = &chatruntimebridge.DelegationDeps{
			Bindings: countingEdgeLister{calls: &listCalls, edges: nil},
		}
	})
	err := f.bridge(t).Execute(context.Background(), f.job())
	if err == nil || !strings.Contains(err.Error(), "Audit required") {
		t.Fatalf("err=%v want Audit required", err)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("must not list live edges, calls=%d", listCalls.Load())
	}
	if f.agentic.calls.Load() != 0 || f.mdl.calls.Load() != 0 || f.sinks.opens.Load() != 0 {
		t.Fatal("side effects non-zero")
	}
}

// TestAgenticInitial_MissingGraph_FailClosed without live list.
func TestAgenticInitial_MissingGraph_FailClosed(t *testing.T) {
	for _, name := range []string{"nil", "empty_obj", "null", "malformed"} {
		name := name
		t.Run(name, func(t *testing.T) {
			listCalls := atomic.Int64{}
			f := newAgenticFixture(t, func(f *agenticFixture) {
				switch name {
				case "nil":
					f.run.AgentGraphSnapshot = nil
				case "empty_obj":
					f.run.AgentGraphSnapshot = json.RawMessage(`{}`)
				case "null":
					f.run.AgentGraphSnapshot = json.RawMessage(`null`)
				case "malformed":
					f.run.AgentGraphSnapshot = json.RawMessage(`{not-json`)
				}
				f.delegation = &chatruntimebridge.DelegationDeps{
					Bindings: countingEdgeLister{calls: &listCalls},
				}
			})
			err := f.bridge(t).Execute(context.Background(), f.job())
			if err == nil || !strings.Contains(err.Error(), "AGENTIC_GRAPH_SNAPSHOT_REQUIRED") {
				t.Fatalf("err=%v", err)
			}
			if listCalls.Load() != 0 {
				t.Fatalf("ListEnabledEdges=%d", listCalls.Load())
			}
			if f.agentic.calls.Load() != 0 || f.mdl.calls.Load() != 0 {
				t.Fatal("builders must not run")
			}
		})
	}
}

// TestAgenticInitial_ProviderTuple_AnthropicForgedOpenAICaps fails pre-builder.
func TestAgenticInitial_ProviderTuple_AnthropicForgedOpenAICaps(t *testing.T) {
	f := newAgenticFixture(t, func(f *agenticFixture) {
		// Forge openai agentic caps under anthropic provider (digest computed on anthropic cfg).
		cfg := f.cfg
		cfg.Provider = "anthropic"
		cfg.AgenticCapabilities = mustAgenticCaps(t, cfg)
		f.cfg = cfg
		modelSnap := marshalTestModelSnapshot(t, cfg)
		f.run.ModelSnapshot = modelSnap
		f.run.AgentGraphSnapshot = explicitEmptyAgentGraph(t, testAgentUUID, testModelUUID, modelSnap)
	})
	err := f.bridge(t).Execute(context.Background(), f.job())
	if err == nil || !strings.Contains(err.Error(), "AGENTIC_PROVIDER_TUPLE_UNSUPPORTED") {
		t.Fatalf("err=%v", err)
	}
	if f.agentic.calls.Load() != 0 || f.mdl.calls.Load() != 0 || f.sinks.opens.Load() != 0 {
		t.Fatal("side effects")
	}
}

// TestAgenticInitial_ProviderTuple_NoncanonicalCases table-driven.
func TestAgenticInitial_ProviderTuple_NoncanonicalCases(t *testing.T) {
	cases := []struct {
		name     string
		provider string
	}{
		{"OpenAI_case", "OpenAI"},
		{"padded", " openai"},
		{"azure", "azure"},
		{"azure_openai", "azure_openai"},
		{"empty", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newAgenticFixture(t, func(f *agenticFixture) {
				cfg := f.cfg
				cfg.Provider = tc.provider
				// Caps digest uses provider string as stored on cfg.
				cfg.AgenticCapabilities = mustAgenticCaps(t, cfg)
				modelSnap := marshalTestModelSnapshot(t, cfg)
				f.cfg = cfg
				f.run.ModelSnapshot = modelSnap
				f.run.AgentGraphSnapshot = explicitEmptyAgentGraph(t, testAgentUUID, testModelUUID, modelSnap)
			})
			err := f.bridge(t).Execute(context.Background(), f.job())
			if err == nil {
				t.Fatal("expected fail")
			}
			// empty provider may hit snapshot required first; others provider tuple
			if f.agentic.calls.Load() != 0 || f.mdl.calls.Load() != 0 {
				t.Fatal("builders must not run")
			}
		})
	}
}

// TestAgenticInitial_LiveGetErrorDoesNotBlockValidFreeze.
func TestAgenticInitial_LiveGetErrorDoesNotBlockValidFreeze(t *testing.T) {
	f := newAgenticFixture(t, nil)
	// Models that always error on Get after freeze is valid.
	b, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions:          &agenticSessions{messages: f.messages},
		Results:           f.results,
		Agents:            agenticAgents{modelConfigID: testModelUUID},
		Models:            errModels{},
		Runs:              &agenticRuns{run: f.run},
		Events:            f.events,
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
		BuildAgenticModel: f.agentic.Build,
		Assemblies:        f.assemblies,
		TextSinkFactory:   f.sinks.Factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Execute(context.Background(), f.job()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.agentic.calls.Load() != 1 {
		t.Fatalf("agentic=%d", f.agentic.calls.Load())
	}
}

// TestAgenticInitial_LiveDisabledKillSwitch after freeze validation.
func TestAgenticInitial_LiveDisabledKillSwitch(t *testing.T) {
	f := newAgenticFixture(t, nil)
	disabled := f.cfg
	disabled.Status = modelconfig.StatusDisabled
	b, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions:          &agenticSessions{messages: f.messages},
		Results:           f.results,
		Agents:            agenticAgents{modelConfigID: testModelUUID},
		Models:            agenticModels{cfg: disabled},
		Runs:              &agenticRuns{run: f.run},
		Events:            f.events,
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
		BuildAgenticModel: f.agentic.Build,
		Assemblies:        f.assemblies,
		TextSinkFactory:   f.sinks.Factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = b.Execute(context.Background(), f.job())
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err=%v", err)
	}
	if f.agentic.calls.Load() != 0 {
		t.Fatal("builder must not run when disabled")
	}
}

type countingEdgeLister struct {
	calls *atomic.Int64
	edges []agentdelegation.GraphEdgeSnapshot
}

func (e countingEdgeLister) ListEnabledEdges(context.Context, string) ([]agentdelegation.GraphEdgeSnapshot, error) {
	if e.calls != nil {
		e.calls.Add(1)
	}
	return e.edges, nil
}

type errModels struct{}

func (errModels) Get(context.Context, string, string) (modelconfig.Config, error) {
	return modelconfig.Config{}, errors.New("live model store unavailable")
}

// TestAgenticInitial_ManifestPersistedNoLeak verifies assembly row fields.
func TestAgenticInitial_ManifestPersistedNoLeak(t *testing.T) {
	f := newAgenticFixture(t, func(f *agenticFixture) {
		f.run.CapabilitySnapshot = toolCapSnap("lookup_order", "find order",
			`{"type":"object","properties":{"id":{"type":"string"}}}`)
	})
	// Need tool invoker for pipeline tools
	b, err := chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions:          &agenticSessions{messages: f.messages},
		Results:           f.results,
		Agents:            agenticAgents{modelConfigID: testModelUUID},
		Models:            agenticModels{cfg: f.cfg},
		Runs:              &agenticRuns{run: f.run},
		Events:            f.events,
		ToolInvoker:       &bridgeToolInvoker{spy: &spyInvoker{}},
		AgenticEngine:     einoruntime.NewAgenticEngine(einoruntime.AgenticEngineConfig{Store: f.store}),
		BuildAgenticModel: f.agentic.Build,
		Assemblies:        f.assemblies,
		TextSinkFactory:   f.sinks.Factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Re-use fixture run (already has empty graph) with tool invoker.
	if err := b.Execute(context.Background(), f.job()); err != nil {
		t.Fatal(err)
	}
	rec, ok := f.assemblies.Get(testWSUUID, testRunUUID)
	if !ok {
		t.Fatal("missing assembly")
	}
	if rec.ToolSearchMode != execution.AssemblyToolSearchModeClientBounded {
		t.Fatalf("mode=%s", rec.ToolSearchMode)
	}
	if rec.DeferredToolCount != 1 || rec.MaxLoadedToolCount != 1 {
		t.Fatalf("counts deferred=%d max=%d", rec.DeferredToolCount, rec.MaxLoadedToolCount)
	}
	if rec.ToolsOverheadTokens != rec.ImmediateToolsTokens+rec.DeferredMetadataTokens+rec.DynamicToolLoadReserveTokens {
		t.Fatalf("tools overhead mismatch")
	}
	blob, _ := json.Marshal(rec)
	for _, s := range []string{"lookup order", "hello agentic", "sk-", `"id":`} {
		// schema bodies must not appear; "id" alone is too broad — check query content
		if s == "hello agentic" && strings.Contains(string(blob), s) {
			t.Fatalf("manifest leaked user text")
		}
		if s == "lookup order" && strings.Contains(string(blob), s) {
			t.Fatalf("manifest leaked query-like content")
		}
	}
	if strings.Contains(string(blob), "properties") {
		t.Fatalf("manifest leaked schema body: %s", blob)
	}
}
