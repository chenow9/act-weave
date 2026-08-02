package a2agateway_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
	"actweave/backend/internal/agentdelegation"
	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/execution"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/google/uuid"
)

func seedListStore(t *testing.T, db *sql.DB) (store *a2agateway.PostgresTaskStore, ws, expID string) {
	t.Helper()
	fx := seedA2AAuditFixture(t, db)
	expID = uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'list-pub','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}
	store, err := a2agateway.NewPostgresTaskStore(db, fx.workspaceID, expID)
	if err != nil {
		t.Fatal(err)
	}
	return store, fx.workspaceID, expID
}

func actorCtx(actorType, actorID string) context.Context {
	return a2agateway.WithTaskActor(context.Background(), actorType, actorID)
}

func saveTask(t *testing.T, store *a2agateway.PostgresTaskStore, ctx context.Context, task *a2a.Task) {
	t.Helper()
	if _, err := store.Save(ctx, task, nil, nil, a2a.TaskVersionMissing); err != nil {
		t.Fatalf("Save %s: %v", task.ID, err)
	}
}

func setUpdatedAt(t *testing.T, db *sql.DB, ws, expID, actorType, actorID, taskID string, ts time.Time) {
	t.Helper()
	if _, err := db.Exec(`
		UPDATE agent_a2a_protocol_tasks SET updated_at=$6
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4 AND task_id=$5
	`, ws, expID, actorType, actorID, taskID, ts.UTC()); err != nil {
		t.Fatal(err)
	}
}

func taskIDs(resp *a2a.ListTasksResponse) []string {
	out := make([]string, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		if t != nil {
			out = append(out, string(t.ID))
		}
	}
	return out
}

// TestPostgresTaskStore_List_MultiPageNoDupNoMiss walks all pages and asserts
// full coverage without duplicates; TotalSize is pre-pagination hit count.
func TestPostgresTaskStore_List_MultiPageNoDupNoMiss(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, ws, expID := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-list-a")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	const n = 5
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("task-page-%02d", i)
		ids[i] = id
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "ctx-shared",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		// Newer updated_at for higher index → DESC order is reverse of creation.
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-list-a", id, base.Add(time.Duration(i)*time.Second))
	}

	var seen []string
	token := ""
	pages := 0
	for {
		resp, err := store.List(ctx, &a2a.ListTasksRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalSize != n {
			t.Fatalf("TotalSize=%d want %d page=%d", resp.TotalSize, n, pages)
		}
		if resp.PageSize != 2 {
			t.Fatalf("PageSize=%d", resp.PageSize)
		}
		for _, id := range taskIDs(resp) {
			seen = append(seen, id)
		}
		pages++
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
		if pages > 10 {
			t.Fatal("pagination loop")
		}
	}
	if pages != 3 {
		t.Fatalf("pages=%d want 3", pages)
	}
	// Expected DESC by updated_at: task-page-04 ... task-page-00
	want := []string{"task-page-04", "task-page-03", "task-page-02", "task-page-01", "task-page-00"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("seen=%v want=%v", seen, want)
	}
	// uniqueness
	set := map[string]struct{}{}
	for _, id := range seen {
		if _, ok := set[id]; ok {
			t.Fatalf("duplicate %s", id)
		}
		set[id] = struct{}{}
	}
	if len(set) != n {
		t.Fatalf("unique=%d want %d", len(set), n)
	}
}

// TestPostgresTaskStore_List_StatusFilterNotBlockedByLeadingNonMatches ensures
// filters run before LIMIT so non-matching leading rows do not hide matches.
func TestPostgresTaskStore_List_StatusFilterNotBlockedByLeadingNonMatches(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, ws, expID := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-filter")
	base := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

	// 4 newest non-matching + 1 older matching completed task.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("nonmatch-%d", i)
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "c",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-filter", id, base.Add(time.Duration(10+i)*time.Second))
	}
	saveTask(t, store, ctx, &a2a.Task{
		ID: "match-completed", ContextID: "c",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	})
	setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-filter", "match-completed", base)

	resp, err := store.List(ctx, &a2a.ListTasksRequest{
		PageSize: 2,
		Status:   a2a.TaskStateCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalSize != 1 {
		t.Fatalf("TotalSize=%d want 1", resp.TotalSize)
	}
	if len(resp.Tasks) != 1 || string(resp.Tasks[0].ID) != "match-completed" {
		t.Fatalf("tasks=%v", taskIDs(resp))
	}
}

func TestPostgresTaskStore_List_LastUpdatedAfter(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, ws, expID := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-lua")
	base := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"old", "mid", "new"} {
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "c",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-lua", id, base.Add(time.Duration(i)*time.Second))
	}
	cutoff := base.Add(1 * time.Second) // includes mid (equal) and new
	resp, err := store.List(ctx, &a2a.ListTasksRequest{LastUpdatedAfter: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalSize != 2 {
		t.Fatalf("TotalSize=%d want 2", resp.TotalSize)
	}
	got := strings.Join(taskIDs(resp), ",")
	if got != "new,mid" {
		t.Fatalf("got=%s", got)
	}
}

func TestPostgresTaskStore_List_IncludeArtifactsDefaultOmit(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, _, _ := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-art")
	saveTask(t, store, ctx, &a2a.Task{
		ID: "with-art", ContextID: "c",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Artifacts: []*a2a.Artifact{{ID: "a1", Name: "secret-payload"}},
	})

	// Default IncludeArtifacts=false must strip artifacts.
	omit, err := store.List(ctx, &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(omit.Tasks) != 1 {
		t.Fatalf("len=%d", len(omit.Tasks))
	}
	if omit.Tasks[0].Artifacts != nil {
		t.Fatalf("artifacts leaked when include=false: %+v", omit.Tasks[0].Artifacts)
	}
	// Explicit include.
	inc, err := store.List(ctx, &a2a.ListTasksRequest{IncludeArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inc.Tasks[0].Artifacts) != 1 || inc.Tasks[0].Artifacts[0].Name != "secret-payload" {
		t.Fatalf("artifacts missing when include=true: %+v", inc.Tasks[0].Artifacts)
	}
}

func TestPostgresTaskStore_List_InvalidPageToken(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, _, _ := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-tok")
	saveTask(t, store, ctx, &a2a.Task{ID: "t1", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}})

	for _, tok := range []string{"not-base64!!!", "dGhpc19pc19ub3RfdmFsaWQ=", "YQ=="} {
		_, err := store.List(ctx, &a2a.ListTasksRequest{PageToken: tok})
		if err == nil || !errors.Is(err, a2a.ErrParseError) {
			t.Fatalf("token=%q want ErrParseError got %v", tok, err)
		}
	}
}

func TestPostgresTaskStore_List_ActorIsolation(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, _, _ := seedListStore(t, db)
	ctxA := actorCtx("SERVICE_PRINCIPAL", "actor-iso-a")
	ctxB := actorCtx("SERVICE_PRINCIPAL", "actor-iso-b")
	saveTask(t, store, ctxA, &a2a.Task{ID: "only-a", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}})
	saveTask(t, store, ctxB, &a2a.Task{ID: "only-b", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}})

	a, err := store.List(ctxA, &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if a.TotalSize != 1 || strings.Join(taskIDs(a), ",") != "only-a" {
		t.Fatalf("actor A list=%v total=%d", taskIDs(a), a.TotalSize)
	}
	b, err := store.List(ctxB, &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalSize != 1 || strings.Join(taskIDs(b), ",") != "only-b" {
		t.Fatalf("actor B list=%v total=%d", taskIDs(b), b.TotalSize)
	}
}

func TestPostgresTaskStore_List_HistoryLength(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, _, _ := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-hist")
	saveTask(t, store, ctx, &a2a.Task{
		ID: "hist", ContextID: "c",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		History: []*a2a.Message{
			{ID: "m1"}, {ID: "m2"}, {ID: "m3"},
		},
	})
	resp, err := store.List(ctx, &a2a.ListTasksRequest{HistoryLength: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tasks) != 1 || len(resp.Tasks[0].History) != 2 {
		t.Fatalf("history len=%v", len(resp.Tasks[0].History))
	}
	if resp.Tasks[0].History[0].ID != "m2" || resp.Tasks[0].History[1].ID != "m3" {
		t.Fatalf("want last 2 messages, got %+v", resp.Tasks[0].History)
	}
	// HistoryLength 0 keeps full history (official mem store).
	full, err := store.List(ctx, &a2a.ListTasksRequest{HistoryLength: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Tasks[0].History) != 3 {
		t.Fatalf("full history=%d", len(full.Tasks[0].History))
	}
}

// TestPostgresTaskStore_List_JSONRPCPath exercises tasks/list through the inbound
// JSON-RPC surface (pagination token + actor isolation).
func TestPostgresTaskStore_List_JSONRPCPath(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	fx := seedA2AAuditFixture(t, db)
	repo, _ := a2agateway.NewRepository(db)
	delRepo, _ := agentdelegation.NewRepository(db)
	audit, _ := agentdelegation.NewService(delRepo)
	runRepo, _ := execution.NewRunRepository(db)

	expID := uuid.Must(uuid.NewV7()).String()
	if _, err := db.Exec(`
		INSERT INTO agent_a2a_exposures(
			id, workspace_id, agent_id, public_name, public_description,
			enabled, auth_mode, version, created_by, updated_by
		) VALUES ($1,$2,$3,'list-rpc','d',true,'AGENT_ACCESS',1,$4,$4)
	`, expID, fx.workspaceID, fx.agentA, fx.ownerID); err != nil {
		t.Fatal(err)
	}

	// Seed protocol tasks for two principals with controlled timestamps.
	store, err := a2agateway.NewPostgresTaskStore(db, fx.workspaceID, expID)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	ctxA := actorCtx("SERVICE_PRINCIPAL", "principal-a")
	ctxB := actorCtx("SERVICE_PRINCIPAL", "principal-b")
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("rpc-a-%d", i)
		saveTask(t, store, ctxA, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "rpc-ctx",
			Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
			Artifacts: []*a2a.Artifact{{ID: a2a.ArtifactID("art"), Name: "hidden"}},
		})
		setUpdatedAt(t, db, fx.workspaceID, expID, "SERVICE_PRINCIPAL", "principal-a", id, base.Add(time.Duration(i)*time.Second))
	}
	saveTask(t, store, ctxB, &a2a.Task{
		ID: "rpc-b-only", ContextID: "rpc-ctx",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	})

	runner := &a2agateway.DurableInboundRunner{
		Runs: runRepo, Freezer: staticFreezer{agentID: fx.agentA, modelID: fx.modelID},
		Execute: func(ctx context.Context, req a2agateway.InboundRunRequest, runID string) (string, error) {
			return "ok", nil
		},
	}
	auth := dualPrincipalAuth{byToken: map[string]string{
		"principal-a": fx.ownerID,
		"principal-b": fx.ownerID, // same user id mapping is fine; actor is token
	}}
	// dualPrincipalAuth returns actor id from token — need principalAuth style.
	// Use principalAuth which maps bearer → SERVICE_PRINCIPAL + token as actor_id.
	_ = auth
	gw, err := a2agateway.NewInboundGateway(repo, audit, runner, "http://test",
		principalAuth{}, a2agateway.WithLeaseTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	invoke := fmt.Sprintf("%s/a2a/workspaces/%s/agents/%s/invoke", srv.URL, fx.workspaceID, fx.agentA)

	// Page 1 for principal-a.
	st, body := postJSONRPC(t, invoke, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "list-1", "method": "tasks/list",
		"params": map[string]any{"page_size": 2},
	})
	if st != http.StatusOK {
		t.Fatalf("status=%d body=%s", st, body)
	}
	var env1 struct {
		Result *a2a.ListTasksResponse `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env1); err != nil {
		t.Fatal(err)
	}
	if env1.Error != nil {
		t.Fatalf("rpc error: %+v body=%s", env1.Error, body)
	}
	if env1.Result == nil || env1.Result.TotalSize != 3 {
		t.Fatalf("page1 total=%v body=%s", env1.Result, body)
	}
	if len(env1.Result.Tasks) != 2 {
		t.Fatalf("page1 tasks=%d", len(env1.Result.Tasks))
	}
	// Default include_artifacts false: no artifact payload.
	for _, tk := range env1.Result.Tasks {
		if len(tk.Artifacts) > 0 {
			t.Fatalf("artifacts present by default: %+v", tk.Artifacts)
		}
	}
	if env1.Result.NextPageToken == "" {
		t.Fatal("expected next_page_token")
	}

	// Page 2.
	st, body = postJSONRPC(t, invoke, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "list-2", "method": "tasks/list",
		"params": map[string]any{
			"page_size":  2,
			"page_token": env1.Result.NextPageToken,
		},
	})
	if st != 200 {
		t.Fatalf("page2 status=%d %s", st, body)
	}
	var env2 struct {
		Result *a2a.ListTasksResponse `json:"result"`
	}
	_ = json.Unmarshal(body, &env2)
	if env2.Result == nil || env2.Result.TotalSize != 3 || len(env2.Result.Tasks) != 1 {
		t.Fatalf("page2=%+v body=%s", env2.Result, body)
	}

	// Actor B must not see A's tasks.
	st, body = postJSONRPC(t, invoke, "principal-b", map[string]any{
		"jsonrpc": "2.0", "id": "list-b", "method": "tasks/list",
		"params": map[string]any{},
	})
	if st != 200 {
		t.Fatalf("b status=%d %s", st, body)
	}
	var envB struct {
		Result *a2a.ListTasksResponse `json:"result"`
	}
	_ = json.Unmarshal(body, &envB)
	if envB.Result == nil || envB.Result.TotalSize != 1 {
		t.Fatalf("actor B total=%v body=%s", envB.Result, body)
	}
	if len(envB.Result.Tasks) != 1 || string(envB.Result.Tasks[0].ID) != "rpc-b-only" {
		t.Fatalf("actor B leak/miss: %v", taskIDs(envB.Result))
	}

	// Illegal page_token → protocol parse error (not silent first page).
	st, body = postJSONRPC(t, invoke, "principal-a", map[string]any{
		"jsonrpc": "2.0", "id": "list-bad", "method": "tasks/list",
		"params": map[string]any{"page_token": "!!!bad!!!"},
	})
	if st != 200 {
		t.Logf("status=%d", st)
	}
	var envBad struct {
		Result any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envBad); err != nil {
		t.Fatal(err)
	}
	if envBad.Error == nil {
		t.Fatalf("want JSON-RPC error for bad token: %s", body)
	}
	// Standard JSON-RPC / A2A parse error code for malformed page token.
	if envBad.Error.Code != -32700 {
		t.Fatalf("want error code -32700 (parse error), got %d msg=%q body=%s",
			envBad.Error.Code, envBad.Error.Message, body)
	}
	if envBad.Result != nil {
		t.Fatalf("bad token must not return result: %s", body)
	}
}

// TestPostgresTaskStore_List_ContextIDFilterBeforePagination: ContextID filter
// is applied before LIMIT so non-matching newer tasks do not hide older matches.
func TestPostgresTaskStore_List_ContextIDFilterBeforePagination(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, ws, expID := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-ctx")
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// 5 newest tasks on other context + 2 older on wanted context.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("other-ctx-%d", i)
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "ctx-noise",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-ctx", id, base.Add(time.Duration(10+i)*time.Second))
	}
	for i, id := range []string{"want-a", "want-b"} {
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "ctx-target",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-ctx", id, base.Add(time.Duration(i)*time.Second))
	}
	resp, err := store.List(ctx, &a2a.ListTasksRequest{
		ContextID: "ctx-target",
		PageSize:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalSize != 2 {
		t.Fatalf("TotalSize=%d want 2 (filter before page)", resp.TotalSize)
	}
	if len(resp.Tasks) != 1 || string(resp.Tasks[0].ID) != "want-b" {
		t.Fatalf("page1=%v", taskIDs(resp))
	}
	if resp.NextPageToken == "" {
		t.Fatal("expected next page for second match")
	}
	page2, err := store.List(ctx, &a2a.ListTasksRequest{
		ContextID: "ctx-target", PageSize: 1, PageToken: resp.NextPageToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Tasks) != 1 || string(page2.Tasks[0].ID) != "want-a" {
		t.Fatalf("page2=%v", taskIDs(page2))
	}
}

// TestPostgresTaskStore_List_SameUpdatedAt_TaskIDDescTiebreak: equal updated_at
// uses task_id DESC for deterministic multi-page traversal without dup/miss.
func TestPostgresTaskStore_List_SameUpdatedAt_TaskIDDescTiebreak(t *testing.T) {
	h := dbtest.New(t)
	h.MigrateToLatest(t)
	db := h.Open(t)
	store, ws, expID := seedListStore(t, db)
	ctx := actorCtx("SERVICE_PRINCIPAL", "actor-tie")
	same := time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC)
	// Lexicographic order: a < b < c < d < e → DESC page order e,d,c,b,a
	ids := []string{"tie-a", "tie-b", "tie-c", "tie-d", "tie-e"}
	for _, id := range ids {
		saveTask(t, store, ctx, &a2a.Task{
			ID: a2a.TaskID(id), ContextID: "ctx-tie",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		})
		setUpdatedAt(t, db, ws, expID, "SERVICE_PRINCIPAL", "actor-tie", id, same)
	}
	var seen []string
	token := ""
	for page := 0; page < 10; page++ {
		resp, err := store.List(ctx, &a2a.ListTasksRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalSize != 5 {
			t.Fatalf("TotalSize=%d", resp.TotalSize)
		}
		seen = append(seen, taskIDs(resp)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	want := []string{"tie-e", "tie-d", "tie-c", "tie-b", "tie-a"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("seen=%v want=%v", seen, want)
	}
	set := map[string]struct{}{}
	for _, id := range seen {
		if _, ok := set[id]; ok {
			t.Fatalf("duplicate %s", id)
		}
		set[id] = struct{}{}
	}
	if len(set) != 5 {
		t.Fatalf("unique=%d", len(set))
	}
}
