package agentaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("agent audit trace not found")
	ErrInvalid  = errors.New("agent audit request is invalid")
)

// Service loads workspace-scoped agent traces for platform-admin audit UI.
type Service struct {
	db        *sql.DB
	debugMode bool
}

func NewService(db *sql.DB, debugMode bool) (*Service, error) {
	if db == nil {
		return nil, errors.New("agent audit database is required")
	}
	return &Service{db: db, debugMode: debugMode}, nil
}

func (s *Service) DebugMode() bool {
	if s == nil {
		return false
	}
	return s.debugMode
}

type ListFilter struct {
	Query  string
	Limit  int
	Offset int
}

func (s *Service) ListTraces(ctx context.Context, workspaceID string, filter ListFilter) (ListResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return ListResult{}, ErrInvalid
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := strings.TrimSpace(filter.Query)

	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ar.trace_id)::int
		FROM agent_runs ar
		WHERE ar.workspace_id = $1
		  AND ($2 = '' OR ar.trace_id ILIKE '%' || $2 || '%' OR CAST(ar.triggered_by_id AS text) ILIKE '%' || $2 || '%')
	`, workspaceID, query).Scan(&total); err != nil {
		return ListResult{}, fmt.Errorf("count agent traces: %w", err)
	}

	traceRows, err := s.db.QueryContext(ctx, `
		SELECT ar.trace_id
		FROM agent_runs ar
		WHERE ar.workspace_id = $1
		  AND ($2 = '' OR ar.trace_id ILIKE '%' || $2 || '%' OR CAST(ar.triggered_by_id AS text) ILIKE '%' || $2 || '%')
		GROUP BY ar.trace_id
		ORDER BY MAX(ar.started_at) DESC, ar.trace_id DESC
		LIMIT $3 OFFSET $4
	`, workspaceID, query, limit, offset)
	if err != nil {
		return ListResult{}, fmt.Errorf("list agent traces: %w", err)
	}
	defer traceRows.Close()

	order := []string{}
	for traceRows.Next() {
		var traceID string
		if err := traceRows.Scan(&traceID); err != nil {
			return ListResult{}, err
		}
		order = append(order, traceID)
	}
	if err := traceRows.Err(); err != nil {
		return ListResult{}, err
	}

	byTrace := map[string][]RunFact{}
	if len(order) > 0 {
		rows, err := s.db.QueryContext(ctx, `
			SELECT ar.id, ar.trace_id, ar.status, ar.triggered_by_type, ar.triggered_by_id,
			       ar.model_snapshot, ar.started_at, ar.finished_at
			FROM agent_runs ar
			WHERE ar.workspace_id = $1
			  AND ar.trace_id = ANY($2::text[])
			ORDER BY ar.started_at DESC, ar.id DESC
		`, workspaceID, pq.Array(order))
		if err != nil {
			return ListResult{}, fmt.Errorf("list agent runs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var run RunFact
			var finished sql.NullTime
			var model []byte
			if err := rows.Scan(
				&run.ID, &run.TraceID, &run.Status, &run.TriggeredByType, &run.TriggeredByID,
				&model, &run.StartedAt, &finished,
			); err != nil {
				return ListResult{}, err
			}
			run.ModelSnapshot = append(json.RawMessage(nil), model...)
			if finished.Valid {
				t := finished.Time.UTC()
				run.FinishedAt = &t
			}
			run.StartedAt = run.StartedAt.UTC()
			byTrace[run.TraceID] = append(byTrace[run.TraceID], run)
		}
		if err := rows.Err(); err != nil {
			return ListResult{}, err
		}
	}

	items := make([]TraceListItem, 0, len(order))
	for _, traceID := range order {
		runs := byTrace[traceID]
		if len(runs) == 0 {
			continue
		}
		stepCount, _ := s.countSteps(ctx, workspaceID, runIDs(runs))
		items = append(items, AggregateListItem(runs, stepCount))
	}

	stats, err := s.computeStats(ctx, workspaceID)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Stats: stats, DebugMode: s.debugMode, Total: total}, nil
}

func (s *Service) countSteps(ctx context.Context, workspaceID string, runIDs []string) (int, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM agent_run_steps
		WHERE workspace_id = $1 AND run_id = ANY($2::uuid[])
	`, workspaceID, pq.Array(runIDs)).Scan(&count)
	return count, err
}

func (s *Service) computeStats(ctx context.Context, workspaceID string) (Stats, error) {
	var total, succeeded, failed int64
	var avgMs sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint,
			AVG(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
				FILTER (WHERE finished_at IS NOT NULL)
		FROM agent_runs
		WHERE workspace_id = $1
	`, workspaceID).Scan(&total, &succeeded, &failed, &avgMs)
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{TotalRuns: total}
	if total > 0 {
		stats.SuccessRate = float64(succeeded) * 100 / float64(total)
		stats.FailureRate = float64(failed) * 100 / float64(total)
	}
	if avgMs.Valid {
		stats.AvgLatencyMs = avgMs.Float64
	}
	return stats, nil
}

func (s *Service) GetTrace(ctx context.Context, workspaceID, traceID string, filter DetailFilter) (TraceDetail, error) {
	workspaceID, traceID = strings.TrimSpace(workspaceID), strings.TrimSpace(traceID)
	if workspaceID == "" || traceID == "" {
		return TraceDetail{}, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trace_id, status, triggered_by_type, triggered_by_id, model_snapshot, started_at, finished_at
		FROM agent_runs
		WHERE workspace_id = $1 AND trace_id = $2
		ORDER BY started_at ASC, id ASC
	`, workspaceID, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	defer rows.Close()
	var runs []RunFact
	for rows.Next() {
		var run RunFact
		var finished sql.NullTime
		var model []byte
		if err := rows.Scan(
			&run.ID, &run.TraceID, &run.Status, &run.TriggeredByType, &run.TriggeredByID,
			&model, &run.StartedAt, &finished,
		); err != nil {
			return TraceDetail{}, err
		}
		run.ModelSnapshot = append(json.RawMessage(nil), model...)
		run.StartedAt = run.StartedAt.UTC()
		if finished.Valid {
			t := finished.Time.UTC()
			run.FinishedAt = &t
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return TraceDetail{}, err
	}
	if len(runs) == 0 {
		return TraceDetail{}, ErrNotFound
	}

	ids := runIDs(runs)
	messages, err := s.loadMessages(ctx, workspaceID, ids)
	if err != nil {
		return TraceDetail{}, err
	}
	steps, err := s.loadSteps(ctx, workspaceID, ids)
	if err != nil {
		return TraceDetail{}, err
	}
	// Build full ordered timeline, then page the presentation slice so the
	// audit UI can infinite-scroll without shipping every tool body at once.
	return PageTimelineSteps(BuildTimeline(runs, messages, steps, s.debugMode), filter), nil
}

func (s *Service) loadMessages(ctx context.Context, workspaceID string, runIDs []string) ([]MessageFact, error) {
	if len(runIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(run_id::text,''), role, COALESCE(content,''), created_at
		FROM chat_messages
		WHERE workspace_id = $1 AND run_id = ANY($2::uuid[])
		ORDER BY created_at ASC, id ASC
	`, workspaceID, pq.Array(runIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageFact
	for rows.Next() {
		var msg MessageFact
		if err := rows.Scan(&msg.ID, &msg.RunID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, err
		}
		msg.CreatedAt = msg.CreatedAt.UTC()
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Service) loadSteps(ctx context.Context, workspaceID string, runIDs []string) ([]StepFact, error) {
	if len(runIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, sequence_no, step_type, status,
		       COALESCE(input_summary, '{}'::jsonb), COALESCE(output_summary, '{}'::jsonb),
		       COALESCE(raw_object_id::text,''), started_at, finished_at
		FROM agent_run_steps
		WHERE workspace_id = $1 AND run_id = ANY($2::uuid[])
		ORDER BY started_at ASC, sequence_no ASC, id ASC
	`, workspaceID, pq.Array(runIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StepFact
	for rows.Next() {
		var step StepFact
		var finished sql.NullTime
		var input, output []byte
		if err := rows.Scan(
			&step.ID, &step.RunID, &step.SequenceNo, &step.StepType, &step.Status,
			&input, &output, &step.RawObjectID, &step.StartedAt, &finished,
		); err != nil {
			return nil, err
		}
		step.InputSummary = append(json.RawMessage(nil), input...)
		step.OutputSummary = append(json.RawMessage(nil), output...)
		step.StartedAt = step.StartedAt.UTC()
		if finished.Valid {
			t := finished.Time.UTC()
			step.FinishedAt = &t
		}
		if strings.EqualFold(step.StepType, "MODEL") {
			var summary map[string]any
			if json.Unmarshal(step.OutputSummary, &summary) == nil {
				step.ModelTurn = summary
			}
		}
		if strings.EqualFold(step.StepType, "TOOL") || strings.EqualFold(step.StepType, "WORKFLOW") {
			step.ToolName = toolNameFromSummary(step.InputSummary)
			var parsed map[string]any
			if json.Unmarshal(step.InputSummary, &parsed) == nil {
				if args, ok := parsed["arguments"]; ok {
					if raw, err := json.Marshal(args); err == nil {
						step.ToolParams = raw
						step.ToolPayloadAvailable = true
					}
				}
				if inv, ok := parsed["toolCallId"].(string); ok {
					step.InvocationID = inv
				}
			}
			if len(step.OutputSummary) > 0 {
				step.ToolResult = step.OutputSummary
				step.ToolPayloadAvailable = true
			}
		}
		out = append(out, step)
	}
	return out, rows.Err()
}
