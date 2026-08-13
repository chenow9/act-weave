package chatruntimebridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/storedobject"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Real Bridge + Postgres: Eino A→B, B model calls tool; MODEL+TOOL rows written by
// nestedAuditModel + PipelineTool OnToolComplete (no hand INSERT of steps).
func TestBridge_EinoAB_TASK_PersistsChildModelAndTool(t *testing.T) {
	t.Skip("classic driveDelegationAB removed in Task 9; Agentic delegation covered by agentic_delegation_*")
}

func TestBridge_EinoAB_INLINE_PersistsNestedModelAndToolOnParentRun(t *testing.T) {
	t.Skip("classic driveDelegationAB removed in Task 9; Agentic delegation covered by agentic_delegation_*")
}

func TestBridge_NewSession_EmptyGraphFreezeNoRunStateConflict(t *testing.T) {
	harness := dbtest.New(t)
	version := harness.MigrateToLatest(t)
	if !version.Applied || version.Number != 22 || version.Dirty {
		t.Fatalf("migration = %+v", version)
	}
	db := harness.Open(t)
	ctx := context.Background()
	fx := seedBridgeABFixture(t, db, agentdelegation.ModeTask)

	runRepo, err := execution.NewRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	delRepo, err := agentdelegation.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	delSvc, err := agentdelegation.NewService(delRepo)
	if err != nil {
		t.Fatal(err)
	}

	// Production chat path: StartAgentRun with empty graph (no freeze at insert).
	_, err = runRepo.StartAgentRun(ctx, execution.StartAgentRunInput{
		ID: fx.parentRunID, WorkspaceID: fx.workspaceID, SessionID: fx.sessionID,
		AgentID: fx.agentA, TriggerType: "CHAT", TriggeredByType: "USER", TriggeredByID: fx.ownerID,
		TraceID: fx.traceID,
		Snapshots: execution.AgentRunSnapshots{
			SchemaVersion: execution.RunSnapshotSchemaV2,
			Model: mustJSON(map[string]any{
				"id": fx.modelAID, "provider": "openai", "apiBase": "https://example.test",
				"modelName": "agent-a", "lockVersion": 1,
			}),
			Capabilities:  json.RawMessage(`{"schemaVersion":"capability-snapshot.v1","releases":[]}`),
			ContextPolicy: json.RawMessage(`{}`),
			Agent: mustJSON(map[string]any{
				"schemaVersion": "agent-binding.v1", "agentId": fx.agentA, "name": "Agent A",
				"modelConfigId": fx.modelAID, "modelConfigLockVer": 1,
			}),
		},
		AuthorizationSnapshot: json.RawMessage(`{}`),
		InputSummary:          json.RawMessage(`{"source":"chrome.new-session"}`),
		// AgentGraphSnapshot intentionally omitted → DB default {}
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := runRepo.GetAgentRun(ctx, fx.workspaceID, fx.parentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.LockVersion != 1 {
		t.Fatalf("start lock=%d", run.LockVersion)
	}
	if string(run.AgentGraphSnapshot) != "{}" && len(run.AgentGraphSnapshot) != 0 {
		t.Fatalf("want empty graph at start, got %s", run.AgentGraphSnapshot)
	}

	bridge := &Bridge{
		sessions: &noopSessions{}, results: &noopResults{},
		agents: &dbAgentReader{db: db}, models: &dbModelReader{db: db},
		runs: runRepo, events: &noopEvents{}, steps: runRepo,
		logger: slog.Default(), now: time.Now, maxIterations: 4, maxTools: 8,
		activeRuns:      map[string]*activeRunExecution{},
		pendingConfirms: map[string][]einoruntime.PendingConfirmInterrupt{},
		delegation: &DelegationDeps{
			Bindings: delSvc, Audit: delSvc,
			Catalog: &staticCatalog{items: map[string][]capability.Descriptor{
				fx.agentB: {{
					CapabilityID: fx.capID, ReleaseID: fx.relID, Kind: "TOOL",
					CallableName: "lookup_sku", CallableDescription: "lookup",
					InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
					RiskLevel: "LOW", SideEffectLevel: "NONE",
				}},
				fx.agentA: {},
			}},
			ChildRuns: &ChildRunStoreAdapter{
				Runs: runRepo,
				GetParent: func(ctx context.Context, workspaceID, parentRunID string) (execution.AgentRun, error) {
					return runRepo.GetAgentRun(ctx, workspaceID, parentRunID)
				},
			},
		},
	}

	job := agentrun.Job{WorkspaceID: fx.workspaceID, RunID: fx.parentRunID, ActorID: fx.ownerID}
	// Production drive path: live edges → freezeGraphSnapshot → SetAgentGraphSnapshotIfEmpty.
	// (attachDelegationTools also builds child tools; freeze+persist is the failing step.)
	edges, err := delSvc.ListEnabledEdges(ctx, fx.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	edges = agentdelegation.EdgesFromRoot(fx.agentA, edges, agentdelegation.DefaultMaxDepth)
	if len(edges) == 0 {
		t.Fatal("expected TASK binding edge")
	}
	graphSnap, err := bridge.freezeGraphSnapshot(ctx, job, run, edges)
	if err != nil {
		t.Fatalf("freezeGraphSnapshot: %v", err)
	}
	raw, err := graphSnapshotBytes(graphSnap)
	if err != nil {
		t.Fatal(err)
	}
	// Exact production call site (bridge.drive):
	if err := runRepo.SetAgentGraphSnapshotIfEmpty(ctx, job.WorkspaceID, job.RunID, raw); err != nil {
		t.Fatalf("persist agent graph snapshot (new session): %v", err)
	}
	after, err := runRepo.GetAgentRun(ctx, fx.workspaceID, fx.parentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LockVersion != 2 {
		t.Fatalf("lock after freeze=%d want 2", after.LockVersion)
	}
	if !strings.Contains(string(after.AgentGraphSnapshot), "call_b") {
		t.Fatalf("frozen graph missing call_b: %s", after.AgentGraphSnapshot)
	}
	// Second drive attach with frozen graph must not rewrite or conflict.
	if err := runRepo.SetAgentGraphSnapshotIfEmpty(ctx, job.WorkspaceID, job.RunID, raw); err != nil {
		t.Fatalf("idempotent re-persist: %v", err)
	}
}

type bridgeABFixture struct {
	ownerID, workspaceID string
	modelAID, modelBID   string
	agentA, agentB       string
	capID, relID         string
	parentRunID          string
	sessionID, traceID   string
	bindingID            string
}

func seedBridgeABFixture(t *testing.T, db *sql.DB, mode string) bridgeABFixture {
	t.Helper()
	fx := bridgeABFixture{
		ownerID: uuid.Must(uuid.NewV7()).String(), workspaceID: uuid.Must(uuid.NewV7()).String(),
		modelAID: uuid.Must(uuid.NewV7()).String(), modelBID: uuid.Must(uuid.NewV7()).String(),
		agentA: uuid.Must(uuid.NewV7()).String(), agentB: uuid.Must(uuid.NewV7()).String(),
		capID: uuid.Must(uuid.NewV7()).String(), relID: uuid.Must(uuid.NewV7()).String(),
		parentRunID: uuid.Must(uuid.NewV7()).String(), sessionID: uuid.Must(uuid.NewV7()).String(),
		bindingID: uuid.Must(uuid.NewV7()).String(),
	}
	fx.traceID = "trace-" + fx.parentRunID[:8]
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\nSQL: %s", err, q)
		}
	}
	exec(`INSERT INTO users(id,username,display_name) VALUES($1,'bridge.owner','Bridge Owner')`, fx.ownerID)
	exec(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,$2,'Bridge Space','SANDBOX',$3,$3,$3)`, fx.workspaceID, "br-"+fx.workspaceID[:8], fx.ownerID)
	exec(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,status,created_by,updated_by) VALUES
		($1,$3,'ma','openai','https://example.test','agent-a','VERIFIED',$4,$4),
		($2,$3,'mb','openai','https://example.test','agent-b','VERIFIED',$4,$4)`,
		fx.modelAID, fx.modelBID, fx.workspaceID, fx.ownerID)
	exec(`INSERT INTO agents(id,workspace_id,name,model_config_id,status,created_by,updated_by) VALUES
		($1,$3,'Agent A',$4,'ACTIVE',$6,$6),
		($2,$3,'Agent B',$5,'ACTIVE',$6,$6)`,
		fx.agentA, fx.agentB, fx.workspaceID, fx.modelAID, fx.modelBID, fx.ownerID)
	exec(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by)
		VALUES($1,$2,$3,'s',$4)`, fx.sessionID, fx.workspaceID, fx.agentA, fx.ownerID)
	// B's lookup_sku capability release (TOOL step FK + ParseCapabilitySnapshot require releaseId).
	exec(`INSERT INTO capabilities(id,workspace_id,kind,name,slug,created_by,updated_by)
		VALUES($1,$2,'TOOL','Lookup SKU','lookup-sku',$3,$3)`, fx.capID, fx.workspaceID, fx.ownerID)
	relHash := strings.Repeat("c", 64)
	exec(`INSERT INTO capability_releases(
		id,workspace_id,capability_id,release_no,source_type,source_id,callable_name,
		input_schema,output_schema,risk_level,side_effect_level,checksum,published_by
	) VALUES ($1,$2,$3,1,'TOOL_VERSION',$1,'lookup_sku','{}','{}','LOW','NONE',$4,$5)`,
		fx.relID, fx.workspaceID, fx.capID, relHash, fx.ownerID)
	exec(`INSERT INTO agent_delegation_bindings(
		id,workspace_id,caller_agent_id,target_agent_id,callable_name,description,
		mode,context_policy,enabled,version,created_by,updated_by
	) VALUES ($1,$2,$3,$4,'call_b','delegate to B',$5,'TASK_ONLY',true,1,$6,$6)`,
		fx.bindingID, fx.workspaceID, fx.agentA, fx.agentB, mode, fx.ownerID)
	return fx
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type scriptedTurn struct {
	content   string
	toolCalls []schema.ToolCall
}

type scriptedChatModel struct {
	mu    sync.Mutex
	turns []scriptedTurn
	idx   int
}

func (m *scriptedChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx >= len(m.turns) {
		return schema.AssistantMessage("done", nil), nil
	}
	turn := m.turns[m.idx]
	m.idx++
	return schema.AssistantMessage(turn.content, turn.toolCalls), nil
}

func (m *scriptedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type stubToolInvoker struct {
	resolve execution.ResolvedInvocation
	invoke  func(context.Context, execution.InvokeRequest, execution.ResolvedInvocation) (execution.PipelineResult, error)
}

func (s *stubToolInvoker) ResolveInvocation(context.Context, execution.ResolveRequest) (execution.ResolvedInvocation, error) {
	return s.resolve, nil
}

func (s *stubToolInvoker) InvokeResolved(ctx context.Context, req execution.InvokeRequest, res execution.ResolvedInvocation) (execution.PipelineResult, error) {
	return s.invoke(ctx, req, res)
}

type stepModelTurnRecorder struct {
	db    *sql.DB
	steps *execution.RunRepository
}

func (r *stepModelTurnRecorder) Record(ctx context.Context, in chatruntime.ModelTurnRecordInput) (execution.AgentRunStep, error) {
	// Real permanent MODEL_TURN evidence row + step transition (same contract as ModelTurnContentService).
	content := in.Content
	if len(content) == 0 {
		content = []byte(`{"source":"test.model_turn"}`)
	}
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT DO NOTHING
	`, in.StepID, in.WorkspaceID, storedobject.BucketExecutions,
		in.WorkspaceID+"/model-turn/"+in.StepID, storedobject.KindModelTurn,
		"application/json", int64(len(content)), sha,
		"test-key", storedobject.ClassificationSensitive, storedobject.RetentionPermanent,
		firstNonEmpty(in.CreatedByType, "SYSTEM"), firstNonEmpty(in.CreatedByID, "test"))
	if err != nil {
		return execution.AgentRunStep{}, err
	}
	summary, _ := json.Marshal(map[string]any{
		"contentSha256": sha, "contentLength": len(content), "source": "test.model_turn",
	})
	return r.steps.TransitionAgentRunStep(ctx, in.WorkspaceID, in.StepID, execution.StepTransition{
		ExpectedStatus: in.ExpectedStatus, NewStatus: in.NewStatus,
		OutputSummary: summary, ErrorCode: in.ErrorCode,
		RawObjectID: in.StepID, RawSHA256: sha, RawLength: int64(len(content)),
	})
}

type staticCatalog struct {
	items map[string][]capability.Descriptor
}

func (c *staticCatalog) ListForAgent(_ context.Context, _, agentID string) ([]capability.Descriptor, error) {
	return append([]capability.Descriptor(nil), c.items[agentID]...), nil
}

type dbAgentReader struct{ db *sql.DB }

func (r *dbAgentReader) Get(ctx context.Context, workspaceID, agentID string) (agent.Agent, error) {
	var a agent.Agent
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, workspace_id::text, name, COALESCE(role_description,''), model_config_id::text, status
		FROM agents WHERE workspace_id=$1 AND id=$2
	`, workspaceID, agentID).Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.RoleDescription, &a.ModelConfigID, &status)
	if err != nil {
		return agent.Agent{}, err
	}
	a.Status = agent.Status(status)
	return a, nil
}

func (r *dbAgentReader) ListPromptRevisions(context.Context, string, string) ([]agent.PromptRevision, error) {
	return nil, nil
}

type dbModelReader struct{ db *sql.DB }

func (r *dbModelReader) Get(ctx context.Context, workspaceID, modelID string) (modelconfig.Config, error) {
	var c modelconfig.Config
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, workspace_id::text, name, provider, api_base, model_name, status, lock_version
		FROM model_configs WHERE workspace_id=$1 AND id=$2
	`, workspaceID, modelID).Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Provider, &c.APIBase, &c.ModelName, &status, &c.LockVersion)
	if err != nil {
		return modelconfig.Config{}, err
	}
	c.Status = modelconfig.Status(status)
	c.Options = json.RawMessage(`{}`)
	return c, nil
}

type noopSessions struct{}

func (noopSessions) GetSession(context.Context, string, string) (chat.Session, error) {
	return chat.Session{}, nil
}
func (noopSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return nil, nil
}
func (noopSessions) ListMessagesReversePage(context.Context, string, string, int, *chat.MessagePageCursor) (chat.MessagePage, error) {
	return chat.MessagePage{}, nil
}
func (noopSessions) GetMessage(context.Context, string, string) (chat.Message, error) {
	return chat.Message{}, nil
}

type noopResults struct{}

func (noopResults) RecordAssistantResult(context.Context, chat.RecordAssistantResultInput) (chat.RecordAssistantResult, error) {
	return chat.RecordAssistantResult{}, nil
}

type noopEvents struct{}

func (noopEvents) Record(context.Context, chatruntime.ProtocolRecord) error { return nil }
