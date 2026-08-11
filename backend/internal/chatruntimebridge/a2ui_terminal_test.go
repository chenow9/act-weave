package chatruntimebridge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/chatruntimebridge"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// password surface golden: preflight path identical to marshalProjectionItem must pass.
func TestGoldenA2UI_PasswordSurfacePreflightPass(t *testing.T) {
	t.Parallel()
	surface := map[string]any{
		"password":     "label-for-password-field",
		"accessToken":  "binding.path.token",
		"apiKey":       "binding.path.apiKey",
		"bearerToken":  "binding.path.bearer",
		"clientSecret": "form-field-name-not-secret",
	}
	surfaceRaw, err := json.Marshal(surface)
	if err != nil {
		t.Fatal(err)
	}
	text := "Please complete the form."
	durable, err := a2ui.SerializeAssistantDurable(text, &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV0, CatalogID: "standard", Surface: surfaceRaw,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Shipped extract/serialize functions produce this durable shape.
	parts, err := chat.ParseMessageContentParts(durable)
	if err != nil {
		t.Fatalf("ParseMessageContentParts: %v", err)
	}
	messageID := uuid.NewString()
	item := protocolevent.MessageItem{
		ID: messageID, Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("preflight ValidateProjectionItem: %v", err)
	}

	// MapCompleted path (CompleteProjected rehydrate) must accept the same durable body.
	msg := chat.Message{
		ID: messageID, WorkspaceID: uuid.NewString(), SessionID: uuid.NewString(),
		RunID: uuid.NewString(), Role: "ASSISTANT", Content: durable,
		ContentSHA256: contentSHA256Hex(durable), ContentLength: int64(len([]byte(durable))),
		Status: "EXECUTED", CreatedAt: time.Now().UTC(),
	}
	mapped, err := chat.NewProtocolMessageMapper(nil).MapCompleted(context.Background(), msg, "")
	if err != nil {
		t.Fatalf("MapCompleted: %v", err)
	}
	if len(mapped.Content) != 2 {
		t.Fatalf("parts=%d want 2", len(mapped.Content))
	}
	if err := protocolevent.ValidateProjectionItem(
		mapped, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("MapCompleted item failed shared preflight: %v", err)
	}
}

func TestGoldenA2UI_EmptyTextValidSurface(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + `{"version":"a2ui-surface.v0","surface":{"root":"card"}}` + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI: true, ProjectionEnabled: true,
	})
	if !prepared.AttachedA2UI || prepared.Result != a2ui.EmitOKEmptyText {
		t.Fatalf("prepared=%+v", prepared)
	}
	parts, err := chat.ParseMessageContentParts(prepared.Content)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%v err=%v", parts, err)
	}
	textPart, ok := parts[0].(protocolevent.TextContentPart)
	if !ok || textPart.Text != "" {
		t.Fatalf("text part=%+v", parts[0])
	}
	if _, ok := parts[1].(protocolevent.A2UIContentPart); !ok {
		t.Fatalf("a2ui part=%T", parts[1])
	}
	item := protocolevent.MessageItem{
		ID: uuid.NewString(), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("preflight: %v", err)
	}
}

func TestGoldenA2UI_InvalidEmptyFallbackKeepsRaw(t *testing.T) {
	t.Parallel()
	full := a2ui.FenceStart + `{not-json` + a2ui.FenceEnd
	prepared := a2ui.PrepareAssistantContent(full, a2ui.PrepareOptions{
		EnableA2UI: true, ProjectionEnabled: true,
	})
	if prepared.AttachedA2UI || prepared.Result != a2ui.EmitInvalidJSON {
		t.Fatalf("prepared=%+v", prepared)
	}
	if prepared.Content != full {
		t.Fatalf("fallback content=%q want raw full", prepared.Content)
	}
	if strings.TrimSpace(prepared.Content) == "" {
		t.Fatal("RecordAssistantResult requires non-empty Content")
	}
}

func TestGoldenA2UI_NextTurnHistoryOmitsSurface(t *testing.T) {
	t.Parallel()
	surface := `{"root":"form","password":{"label":"Secret"},"schemaVersion":"must-not-leak"}`
	durable, err := a2ui.SerializeAssistantDurable("Prior reply", &a2ui.Payload{
		Version: a2ui.EnvelopeVersionV0, Surface: json.RawMessage(surface),
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := chat.JoinTextPartsFromDurable(durable)
	if joined != "Prior reply" {
		t.Fatalf("joined=%q", joined)
	}
	if strings.Contains(joined, "password") || strings.Contains(joined, "schemaVersion") ||
		strings.Contains(joined, "surface") || strings.Contains(joined, "a2ui") {
		t.Fatalf("surface leaked into model history text: %q", joined)
	}

	emptyDurable, err := a2ui.SerializeAssistantDurable("", &a2ui.Payload{
		Surface: json.RawMessage(`{"root":"only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := chat.JoinTextPartsFromDurable(emptyDurable); got != "" {
		t.Fatalf("empty join=%q", got)
	}
}

func TestCompleteRun_MaterializesA2UIEnvelope(t *testing.T) {
	const (
		ws  = "a1000000-0000-4000-8000-0000000000a1"
		run = "a2000000-0000-4000-8000-0000000000a2"
		sid = "a3000000-0000-4000-8000-0000000000a3"
		uid = "a4000000-0000-4000-8000-0000000000a4"
		aid = "a5000000-0000-4000-8000-0000000000a5"
		mid = "a6000000-0000-4000-8000-0000000000a6"
	)

	surfaceBody := `{"version":"a2ui-surface.v0","catalogId":"standard","surface":{"password":{"label":"Pw"},"accessToken":{"label":"Tok"}}}`
	modelOut := "Please fill:\n" + a2ui.FenceStart + surfaceBody + a2ui.FenceEnd

	results := &a2uiResults{}
	bridge, err := newA2UITestBridge(t, a2uiBridgeOpts{
		ws: ws, runID: run, sid: sid, uid: uid, aid: aid, mid: mid,
		enableA2UI: true, modelOut: modelOut, results: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := agentrun.Job{
		WorkspaceID: ws, RunID: run, SessionID: sid, UserMessageID: uid,
		ActorID: "system", InitialEventsCommitted: true,
	}
	if err := bridge.Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := results.lastContent()
	if !strings.Contains(content, a2ui.MessageContentSchemaVersion) {
		t.Fatalf("want v1 envelope, got %s", content)
	}
	if strings.Contains(content, a2ui.FenceStart) {
		t.Fatalf("fence must not remain in durable content: %s", content)
	}
	parts, err := chat.ParseMessageContentParts(content)
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts=%v err=%v content=%s", parts, err, content)
	}
	a2uiPart, ok := parts[1].(protocolevent.A2UIContentPart)
	if !ok || !strings.Contains(string(a2uiPart.Surface), "password") {
		t.Fatalf("a2ui part=%+v", parts[1])
	}
	item := protocolevent.MessageItem{
		ID: results.lastID(), Type: protocolevent.ItemTypeMessage,
		Status: protocolevent.ItemStatusCompleted, Role: protocolevent.MessageRoleAssistant,
		Content: parts,
	}
	if err := protocolevent.ValidateProjectionItem(
		item, protocolevent.EventItemCompleted, protocolevent.MustDefaultPayloadValidator(),
	); err != nil {
		t.Fatalf("recorded durable preflight: %v", err)
	}
}

func TestCompleteRun_CapabilityOffNoExtractAttach(t *testing.T) {
	const (
		ws  = "b1000000-0000-4000-8000-0000000000b1"
		run = "b2000000-0000-4000-8000-0000000000b2"
		sid = "b3000000-0000-4000-8000-0000000000b3"
		uid = "b4000000-0000-4000-8000-0000000000b4"
		aid = "b5000000-0000-4000-8000-0000000000b5"
		mid = "b6000000-0000-4000-8000-0000000000b6"
	)
	modelOut := "Just text, no UI."
	results := &a2uiResults{}
	bridge, err := newA2UITestBridge(t, a2uiBridgeOpts{
		ws: ws, runID: run, sid: sid, uid: uid, aid: aid, mid: mid,
		enableA2UI: false, modelOut: modelOut, results: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := agentrun.Job{
		WorkspaceID: ws, RunID: run, SessionID: sid, UserMessageID: uid,
		ActorID: "system", InitialEventsCommitted: true,
	}
	if err := bridge.Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := results.lastContent(); got != modelOut {
		t.Fatalf("want plain %q, got %q", modelOut, got)
	}
}

func TestCompleteRun_ProjectionEnvOffStripsOnly(t *testing.T) {
	prev := os.Getenv(a2ui.EnvProjection)
	t.Setenv(a2ui.EnvProjection, "off")
	t.Cleanup(func() { _ = os.Setenv(a2ui.EnvProjection, prev) })

	const (
		ws  = "c1000000-0000-4000-8000-0000000000c1"
		run = "c2000000-0000-4000-8000-0000000000c2"
		sid = "c3000000-0000-4000-8000-0000000000c3"
		uid = "c4000000-0000-4000-8000-0000000000c4"
		aid = "c5000000-0000-4000-8000-0000000000c5"
		mid = "c6000000-0000-4000-8000-0000000000c6"
	)
	modelOut := "Hi\n" + a2ui.FenceStart + `{"surface":{"root":"x"}}` + a2ui.FenceEnd
	results := &a2uiResults{}
	bridge, err := newA2UITestBridge(t, a2uiBridgeOpts{
		ws: ws, runID: run, sid: sid, uid: uid, aid: aid, mid: mid,
		enableA2UI: true, modelOut: modelOut, results: results,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := agentrun.Job{
		WorkspaceID: ws, RunID: run, SessionID: sid, UserMessageID: uid,
		ActorID: "system", InitialEventsCommitted: true,
	}
	if err := bridge.Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := results.lastContent(); got != "Hi" {
		t.Fatalf("want stripped plain Hi, got %q", got)
	}
	if strings.Contains(results.lastContent(), "schemaVersion") {
		t.Fatal("projection off must not persist v1 envelope")
	}
}

func TestDrive_InjectsA2UIPromptWhenEnabled(t *testing.T) {
	const (
		ws  = "d1000000-0000-4000-8000-0000000000d1"
		run = "d2000000-0000-4000-8000-0000000000d2"
		sid = "d3000000-0000-4000-8000-0000000000d3"
		uid = "d4000000-0000-4000-8000-0000000000d4"
		aid = "d5000000-0000-4000-8000-0000000000d5"
		mid = "d6000000-0000-4000-8000-0000000000d6"
	)
	capturing := &a2uiCapturingModel{}
	results := &a2uiResults{}
	bridge, err := newA2UITestBridge(t, a2uiBridgeOpts{
		ws: ws, runID: run, sid: sid, uid: uid, aid: aid, mid: mid,
		enableA2UI: true, modelOut: "ok", results: results, model: capturing,
		systemPrompt: "Base agent prompt.",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := agentrun.Job{
		WorkspaceID: ws, RunID: run, SessionID: sid, UserMessageID: uid,
		ActorID: "system", InitialEventsCommitted: true,
	}
	if err := bridge.Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if capturing.system == "" {
		t.Fatal("model received no system instruction")
	}
	if !strings.Contains(capturing.system, a2ui.PromptTemplateV1) {
		t.Fatalf("A2UI rules not injected into instruction: %q", capturing.system)
	}
	if !strings.Contains(capturing.system, "Base agent prompt.") {
		t.Fatalf("base prompt lost: %q", capturing.system)
	}
	if strings.Count(capturing.system, a2ui.FenceStart) != 1 {
		t.Fatalf("expected single fence mention in prompt, got %q", capturing.system)
	}
}

// --- helpers / fakes ---

type a2uiBridgeOpts struct {
	ws, runID, sid, uid, aid, mid string
	enableA2UI                    bool
	modelOut                      string
	results                       *a2uiResults
	model                         model.BaseChatModel
	systemPrompt                  string
}

func newA2UITestBridge(t *testing.T, opts a2uiBridgeOpts) (*chatruntimebridge.Bridge, error) {
	t.Helper()
	fake := opts.model
	if fake == nil {
		fake = &a2uiFixedModel{content: opts.modelOut}
	}
	promptRev := "e7000000-0000-4000-8000-0000000000e7"
	var revID *string
	if opts.systemPrompt != "" {
		revID = &promptRev
	}
	agents := &a2uiAgents{
		agent: agent.Agent{
			ID: opts.aid, WorkspaceID: opts.ws, ModelConfigID: opts.mid,
			Status: agent.StatusActive, CurrentPromptRevisionID: revID,
		},
		revisions: nil,
	}
	if opts.systemPrompt != "" {
		agents.revisions = []agent.PromptRevision{{
			ID: promptRev, WorkspaceID: opts.ws, AgentID: opts.aid,
			SystemPrompt: opts.systemPrompt,
		}}
	}
	// Legacy history assembly: empty SnapshotSchemaVersion (no token-window path).
	// ContextPolicySnapshot still freezes enableA2UI for inject + extract.
	var snap json.RawMessage
	if opts.enableA2UI {
		snap = mustA2UISnapshot(t, true)
	} else {
		snap = json.RawMessage(`{}`)
	}
	return chatruntimebridge.NewBridge(chatruntimebridge.Dependencies{
		Sessions: &a2uiSessions{messages: []chat.Message{{
			ID: opts.uid, WorkspaceID: opts.ws, SessionID: opts.sid,
			Role: "USER", Content: "hi", Status: "RECEIVED",
		}}},
		Results: opts.results,
		Agents:  agents,
		Models: &a2uiModels{cfg: modelconfig.Config{
			ID: opts.mid, WorkspaceID: opts.ws, Status: modelconfig.StatusVerified,
			Provider: "openai", ModelName: "fake",
		}},
		Runs: &a2uiRuns{run: execution.AgentRun{
			ID: opts.runID, WorkspaceID: opts.ws, AgentID: opts.aid, SessionID: opts.sid,
			Status: "RUNNING", LockVersion: 1,
			ContextPolicySnapshot: snap,
			// Empty schema version → legacy buildMessages (instruction as system message).
			SnapshotSchemaVersion: "",
			TriggeredByType:       "USER", TriggeredByID: "user-1", TraceID: "trace-a2ui",
		}},
		Events: a2uiEvents{},
		Engine: einoruntime.NewEngine(einoruntime.EngineConfig{}),
		BuildChatModel: func(context.Context, modelconfig.Config) (model.BaseChatModel, error) {
			return fake, nil
		},
		Now: time.Now,
	})
}

func mustA2UISnapshot(t *testing.T, enable bool) json.RawMessage {
	t.Helper()
	hash := sessioncontext.DefaultCompactionTemplateHash()
	doc := sessioncontext.ResolvedSnapshot{
		SchemaVersion:            sessioncontext.SnapshotSchemaV2,
		Mode:                     sessioncontext.ModeTokenWindow,
		ModelContextWindowTokens: 128000,
		EffectiveMaxInputTokens:  100000,
		OutputReserveTokens:      4096,
		SafetyMarginTokens:       2048,
		MaxRecentTurns:           0,
		TokenizerProfile:         "tiktoken-cl100k_base",
		TokenizerVersion:         "1",
		OutputTokenLimitMode:     "max_tokens",
		Summary:                  json.RawMessage(`null`),
		Compaction: &sessioncontext.CompactionSnapshot{
			TriggerBps: sessioncontext.TriggerBps, TargetBps: sessioncontext.TargetBps,
			MaxSummaryTokens: 2048, MinEvictedTurns: 4, MaxGenerationPasses: 2,
			TemplateVersion:  sessioncontext.DefaultCompactionTemplateVersion,
			TemplateHash:     hash,
			TotalTimeoutMs:   sessioncontext.DefaultTotalTimeoutMs,
			PerPassTimeoutMs: sessioncontext.DefaultPerPassTimeoutMs,
			ClaimWaitMs:      sessioncontext.DefaultClaimWaitMs,
		},
		AAP: &sessioncontext.AAPSnapshot{EnableA2UI: enable},
		Sources: sessioncontext.SnapshotSources{
			GateEnabled: true, CompactionGateEnabled: false,
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if sessioncontext.EnableA2UIFromSnapshot(raw) != enable {
		t.Fatalf("EnableA2UIFromSnapshot mismatch")
	}
	return raw
}

func contentSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type a2uiResults struct {
	mu       sync.Mutex
	contents []string
	ids      []string
}

func (r *a2uiResults) RecordAssistantResult(
	_ context.Context, in chat.RecordAssistantResultInput,
) (chat.RecordAssistantResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contents = append(r.contents, in.Content)
	r.ids = append(r.ids, in.AssistantMessageID)
	return chat.RecordAssistantResult{Message: chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "EXECUTED", RunID: in.RunID,
		ContentSHA256: contentSHA256Hex(in.Content), ContentLength: int64(len([]byte(in.Content))),
		CreatedAt: time.Now().UTC(),
	}}, nil
}

func (r *a2uiResults) lastContent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.contents) == 0 {
		return ""
	}
	return r.contents[len(r.contents)-1]
}

func (r *a2uiResults) lastID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ids) == 0 {
		return uuid.NewString()
	}
	return r.ids[len(r.ids)-1]
}

type a2uiRuns struct {
	run execution.AgentRun
}

func (r *a2uiRuns) GetAgentRun(_ context.Context, workspaceID, runID string) (execution.AgentRun, error) {
	out := r.run
	out.WorkspaceID = workspaceID
	out.ID = runID
	return out, nil
}

type a2uiSessions struct {
	messages []chat.Message
}

func (s *a2uiSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}
func (s *a2uiSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return s.messages, nil
}
func (s *a2uiSessions) ListMessagesReversePage(
	context.Context, string, string, int, *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	return chat.MessagePage{Messages: s.messages}, nil
}
func (s *a2uiSessions) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	for _, m := range s.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}

type a2uiAgents struct {
	agent     agent.Agent
	revisions []agent.PromptRevision
}

func (a *a2uiAgents) Get(context.Context, string, string) (agent.Agent, error) {
	return a.agent, nil
}
func (a *a2uiAgents) ListPromptRevisions(context.Context, string, string) ([]agent.PromptRevision, error) {
	return a.revisions, nil
}

type a2uiModels struct {
	cfg modelconfig.Config
}

func (m *a2uiModels) Get(context.Context, string, string) (modelconfig.Config, error) {
	return m.cfg, nil
}

type a2uiEvents struct{}

func (a2uiEvents) Record(context.Context, chatruntime.ProtocolRecord) error { return nil }

type a2uiFixedModel struct {
	content string
}

func (m *a2uiFixedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.content, nil), nil
}
func (m *a2uiFixedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(schema.AssistantMessage(m.content, nil), nil)
	}()
	return sr, nil
}

type a2uiCapturingModel struct {
	system string
}

func (m *a2uiCapturingModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range msgs {
		if msg != nil && msg.Role == schema.System {
			m.system = msg.Content
		}
	}
	// Also capture from first message if ADK folds instruction differently.
	if m.system == "" {
		for _, msg := range msgs {
			if msg != nil && strings.Contains(msg.Content, a2ui.PromptTemplateV1) {
				m.system = msg.Content
				break
			}
		}
	}
	return schema.AssistantMessage("ok", nil), nil
}
func (m *a2uiCapturingModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()
		_ = sw.Send(msg, nil)
	}()
	return sr, nil
}
