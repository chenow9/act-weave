package a2agateway

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/lib/pq"
)

// taskActorContextKey carries authenticated principal into TaskStore Save/Get/List.
type taskActorContextKey struct{}

// TaskActor is the authenticated principal bound to protocol tasks and inbound authority.
type TaskActor struct {
	Type string
	ID   string
}

// WithTaskActor attaches principal identity for a2asrv TaskStore and cancel paths.
func WithTaskActor(ctx context.Context, actorType, actorID string) context.Context {
	return context.WithValue(ctx, taskActorContextKey{}, TaskActor{
		Type: strings.ToUpper(strings.TrimSpace(actorType)),
		ID:   strings.TrimSpace(actorID),
	})
}

// TaskActorFrom returns principal from context when present and non-empty.
func TaskActorFrom(ctx context.Context) (TaskActor, bool) {
	if ctx == nil {
		return TaskActor{}, false
	}
	a, ok := ctx.Value(taskActorContextKey{}).(TaskActor)
	if !ok || strings.TrimSpace(a.Type) == "" || strings.TrimSpace(a.ID) == "" {
		return TaskActor{}, false
	}
	return a, true
}

// PostgresTaskStore is a principal-scoped a2asrv.TaskStore backed by PostgreSQL.
// Rows are isolated by (workspace, exposure, actor_type, actor_id, task_id).
// Save implements optimistic concurrency on version (a2a.ErrConcurrentTaskModification).
// Get/List without a context actor fail closed (ErrTaskNotFound / ErrUnauthenticated).
type PostgresTaskStore struct {
	db          *sql.DB
	workspaceID string
	exposureID  string
}

// NewPostgresTaskStore builds a store scoped to one exposure. actor is read per-call from ctx.
func NewPostgresTaskStore(db *sql.DB, workspaceID, exposureID string) (*PostgresTaskStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: task store db required", ErrInvalid)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	exposureID = strings.TrimSpace(exposureID)
	if workspaceID == "" || exposureID == "" {
		return nil, fmt.Errorf("%w: task store workspace/exposure required", ErrInvalid)
	}
	return &PostgresTaskStore{db: db, workspaceID: workspaceID, exposureID: exposureID}, nil
}

var _ a2asrv.TaskStore = (*PostgresTaskStore)(nil)

func (s *PostgresTaskStore) Save(ctx context.Context, task *a2a.Task, _ a2a.Event, _ *a2a.Task, prevVersion a2a.TaskVersion) (a2a.TaskVersion, error) {
	if s == nil || s.db == nil {
		return a2a.TaskVersionMissing, ErrInvalid
	}
	if task == nil || strings.TrimSpace(string(task.ID)) == "" {
		return a2a.TaskVersionMissing, a2a.ErrInvalidParams
	}
	actor, ok := TaskActorFrom(ctx)
	if !ok {
		return a2a.TaskVersionMissing, a2a.ErrUnauthenticated
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return a2a.TaskVersionMissing, fmt.Errorf("marshal task: %w", err)
	}
	taskID := strings.TrimSpace(string(task.ID))
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return a2a.TaskVersionMissing, err
	}
	defer tx.Rollback()

	var curVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT version FROM agent_a2a_protocol_tasks
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4 AND task_id=$5
		FOR UPDATE
	`, s.workspaceID, s.exposureID, actor.Type, actor.ID, taskID).Scan(&curVersion)

	if errors.Is(err, sql.ErrNoRows) {
		// First insert: prevVersion must be missing (or 0).
		if prevVersion != a2a.TaskVersionMissing && prevVersion != 0 {
			return a2a.TaskVersionMissing, a2a.ErrConcurrentTaskModification
		}
		const first = int64(1)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_a2a_protocol_tasks(
				workspace_id, exposure_id, actor_type, actor_id, task_id,
				version, task_json, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, s.workspaceID, s.exposureID, actor.Type, actor.ID, taskID, first, raw, now)
		if err != nil {
			if isUniqueViolation(err) {
				return a2a.TaskVersionMissing, a2a.ErrConcurrentTaskModification
			}
			return a2a.TaskVersionMissing, mapWrite("protocol task insert", err)
		}
		if err := tx.Commit(); err != nil {
			return a2a.TaskVersionMissing, err
		}
		return a2a.TaskVersion(first), nil
	}
	if err != nil {
		return a2a.TaskVersionMissing, err
	}

	if prevVersion != a2a.TaskVersionMissing && a2a.TaskVersion(curVersion) != prevVersion {
		return a2a.TaskVersionMissing, a2a.ErrConcurrentTaskModification
	}
	next := curVersion + 1
	res, err := tx.ExecContext(ctx, `
		UPDATE agent_a2a_protocol_tasks
		SET version=$6, task_json=$7, updated_at=$8
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4
		  AND task_id=$5 AND version=$9
	`, s.workspaceID, s.exposureID, actor.Type, actor.ID, taskID, next, raw, now, curVersion)
	if err != nil {
		return a2a.TaskVersionMissing, mapWrite("protocol task update", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return a2a.TaskVersionMissing, a2a.ErrConcurrentTaskModification
	}
	if err := tx.Commit(); err != nil {
		return a2a.TaskVersionMissing, err
	}
	return a2a.TaskVersion(next), nil
}

func (s *PostgresTaskStore) Get(ctx context.Context, taskID a2a.TaskID) (*a2a.Task, a2a.TaskVersion, error) {
	if s == nil || s.db == nil {
		return nil, a2a.TaskVersionMissing, ErrInvalid
	}
	actor, ok := TaskActorFrom(ctx)
	if !ok {
		// Fail closed: no existence leak without principal.
		return nil, a2a.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	id := strings.TrimSpace(string(taskID))
	if id == "" {
		return nil, a2a.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	var version int64
	var raw []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT version, task_json FROM agent_a2a_protocol_tasks
		WHERE workspace_id=$1 AND exposure_id=$2 AND actor_type=$3 AND actor_id=$4 AND task_id=$5
	`, s.workspaceID, s.exposureID, actor.Type, actor.ID, id).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, a2a.TaskVersionMissing, a2a.ErrTaskNotFound
	}
	if err != nil {
		return nil, a2a.TaskVersionMissing, err
	}
	var task a2a.Task
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, a2a.TaskVersionMissing, fmt.Errorf("unmarshal protocol task: %w", err)
	}
	return &task, a2a.TaskVersion(version), nil
}

func (s *PostgresTaskStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	if s == nil || s.db == nil {
		return nil, ErrInvalid
	}
	actor, ok := TaskActorFrom(ctx)
	if !ok {
		return nil, a2a.ErrUnauthenticated
	}
	if req == nil {
		req = &a2a.ListTasksRequest{}
	}
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, fmt.Errorf("page size must be between 1 and 100 inclusive, got %d", pageSize)
	}
	if req.HistoryLength < 0 {
		return nil, fmt.Errorf("history length must be non-negative integer, got %d", req.HistoryLength)
	}

	// Principal isolation + optional filters applied before pagination (matches a2a-go mem store).
	where := []string{
		"workspace_id=$1",
		"exposure_id=$2",
		"actor_type=$3",
		"actor_id=$4",
	}
	args := []any{s.workspaceID, s.exposureID, actor.Type, actor.ID}
	argN := 5
	if cid := strings.TrimSpace(req.ContextID); cid != "" {
		where = append(where, fmt.Sprintf("task_json->>'contextId'=$%d", argN))
		args = append(args, cid)
		argN++
	}
	if req.Status != a2a.TaskStateUnspecified {
		where = append(where, fmt.Sprintf("task_json->'status'->>'state'=$%d", argN))
		args = append(args, string(req.Status))
		argN++
	}
	if req.LastUpdatedAfter != nil {
		where = append(where, fmt.Sprintf("updated_at >= $%d", argN))
		args = append(args, req.LastUpdatedAfter.UTC())
		argN++
	}
	whereSQL := strings.Join(where, " AND ")

	var totalSize int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM agent_a2a_protocol_tasks WHERE `+whereSQL, args...).Scan(&totalSize); err != nil {
		return nil, err
	}

	// Keyset cursor: opaque base64 token; invalid token fails closed (never silent first page).
	pageWhere := whereSQL
	pageArgs := append([]any{}, args...)
	pageArgN := argN
	if tok := strings.TrimSpace(req.PageToken); tok != "" {
		cursorTime, cursorTaskID, err := decodeListPageToken(tok)
		if err != nil {
			return nil, err
		}
		// ORDER BY updated_at DESC, task_id DESC → next page is strictly after cursor.
		pageWhere += fmt.Sprintf(
			" AND (updated_at < $%d OR (updated_at = $%d AND task_id < $%d))",
			pageArgN, pageArgN, pageArgN+1,
		)
		pageArgs = append(pageArgs, cursorTime.UTC(), string(cursorTaskID))
		pageArgN += 2
	}
	// Fetch pageSize+1 to detect a following page without relying on TotalSize arithmetic.
	pageArgs = append(pageArgs, pageSize+1)
	limitPh := fmt.Sprintf("$%d", pageArgN)

	rows, err := s.db.QueryContext(ctx, `
		SELECT task_json, updated_at, task_id FROM agent_a2a_protocol_tasks
		WHERE `+pageWhere+`
		ORDER BY updated_at DESC, task_id DESC
		LIMIT `+limitPh, pageArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pageTasks []*a2a.Task
	var pageUpdated []time.Time
	var pageTaskIDs []string
	for rows.Next() {
		var raw []byte
		var updated time.Time
		var taskID string
		if err := rows.Scan(&raw, &updated, &taskID); err != nil {
			return nil, err
		}
		var task a2a.Task
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, fmt.Errorf("unmarshal protocol task: %w", err)
		}
		pageTasks = append(pageTasks, &task)
		pageUpdated = append(pageUpdated, updated.UTC())
		pageTaskIDs = append(pageTaskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var nextPageToken string
	if len(pageTasks) > pageSize {
		pageTasks = pageTasks[:pageSize]
		pageUpdated = pageUpdated[:pageSize]
		pageTaskIDs = pageTaskIDs[:pageSize]
		lastIdx := pageSize - 1
		nextPageToken = encodeListPageToken(pageUpdated[lastIdx], a2a.TaskID(pageTaskIDs[lastIdx]))
	}

	tasks, err := projectListTasks(pageTasks, req)
	if err != nil {
		return nil, err
	}
	return &a2a.ListTasksResponse{
		Tasks:         tasks,
		TotalSize:     totalSize,
		PageSize:      pageSize,
		NextPageToken: nextPageToken,
	}, nil
}

// projectListTasks applies HistoryLength + IncludeArtifacts (official mem-store semantics).
func projectListTasks(tasks []*a2a.Task, req *a2a.ListTasksRequest) ([]*a2a.Task, error) {
	out := make([]*a2a.Task, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		// Copy via JSON so History/Artifacts slices are not shared with store materialization.
		raw, err := json.Marshal(task)
		if err != nil {
			return nil, err
		}
		var copy a2a.Task
		if err := json.Unmarshal(raw, &copy); err != nil {
			return nil, err
		}
		if req.HistoryLength > 0 && len(copy.History) > req.HistoryLength {
			copy.History = copy.History[len(copy.History)-req.HistoryLength:]
		}
		if !req.IncludeArtifacts {
			copy.Artifacts = nil
		}
		out = append(out, &copy)
	}
	return out, nil
}

// encodeListPageToken matches a2a-go internal/taskstore opaque cursor format.
func encodeListPageToken(updatedTime time.Time, taskID a2a.TaskID) string {
	timeStrNano := updatedTime.UTC().Format(time.RFC3339Nano)
	return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("%s_%s", timeStrNano, taskID)))
}

// decodeListPageToken rejects malformed tokens with a2a.ErrParseError (never silent first page).
func decodeListPageToken(pageToken string) (time.Time, a2a.TaskID, error) {
	decoded, err := base64.URLEncoding.DecodeString(pageToken)
	if err != nil {
		return time.Time{}, "", a2a.ErrParseError
	}
	parts := strings.Split(string(decoded), "_")
	if len(parts) != 2 {
		return time.Time{}, "", a2a.ErrParseError
	}
	taskID := a2a.TaskID(parts[1])
	updatedTime, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", a2a.ErrParseError
	}
	return updatedTime, taskID, nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return true
	}
	return false
}
