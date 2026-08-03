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
	// summaryBodies opens encrypted compact summary bodies when debugMode=true.
	// Nil or debug=false → zero opens (AC-08 / IC-11).
	summaryBodies SummaryBodyReader
}

// ServiceOption configures optional audit dependencies.
type ServiceOption func(*Service)

// WithSummaryBodyReader enables ADMIN_AUDIT hydration from encrypted summary objects.
func WithSummaryBodyReader(reader SummaryBodyReader) ServiceOption {
	return func(s *Service) {
		if s != nil {
			s.summaryBodies = reader
		}
	}
}

func NewService(db *sql.DB, debugMode bool, opts ...ServiceOption) (*Service, error) {
	if db == nil {
		return nil, errors.New("agent audit database is required")
	}
	s := &Service{db: db, debugMode: debugMode}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
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
		runs, err := s.loadRunsWithActors(ctx, workspaceID, order, true)
		if err != nil {
			return ListResult{}, err
		}
		for _, run := range runs {
			byTrace[run.TraceID] = append(byTrace[run.TraceID], run)
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
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED','TIMED_OUT'))::bigint,
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
	runs, err := s.loadRunsWithActors(ctx, workspaceID, []string{traceID}, false)
	if err != nil {
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
	detail := PageTimelineSteps(BuildTimeline(runs, messages, steps, s.debugMode), filter)
	if err := s.enrichStepAgentNames(ctx, workspaceID, detail.Steps); err != nil {
		return TraceDetail{}, err
	}
	// AC-08 / IC-11: only when server debug is on, hydrate completed compact
	// bodies from encrypted objects — never from protocol JSONB (T4-B).
	// debug=false must perform zero SummaryBodyReader opens.
	if s.debugMode {
		s.hydrateCompactSummaryBodies(ctx, workspaceID, detail.Steps)
	}
	return detail, nil
}

// loadRunsWithActors loads agent_runs and joins USER / SERVICE_PRINCIPAL display names.
// desc=true orders started_at DESC (list page); false orders ASC (detail page).
func (s *Service) loadRunsWithActors(ctx context.Context, workspaceID string, traceIDs []string, desc bool) ([]RunFact, error) {
	if len(traceIDs) == 0 {
		return nil, nil
	}
	order := "ASC"
	if desc {
		order = "DESC"
	}
	// Join actors in SQL so list/detail always get human names without a second round-trip.
	// Compare ids as text to avoid cast failures on legacy rows.
	q := `
		SELECT ar.id,
		       ar.trace_id,
		       ar.status,
		       ar.triggered_by_type,
		       BTRIM(ar.triggered_by_id::text),
		       ar.model_snapshot,
		       ar.started_at,
		       ar.finished_at,
		       COALESCE(u.username, ''),
		       COALESCE(u.display_name, ''),
		       COALESCE(sp.name, ''),
		       COALESCE(c.client_id, ''),
		       COALESCE(c.name, '')
		FROM agent_runs ar
		LEFT JOIN users u
		  ON UPPER(BTRIM(ar.triggered_by_type::text)) = 'USER'
		 AND u.id::text = BTRIM(ar.triggered_by_id::text)
		LEFT JOIN service_principals sp
		  ON sp.workspace_id = ar.workspace_id
		 AND UPPER(BTRIM(ar.triggered_by_type::text)) IN ('SERVICE_PRINCIPAL', 'SERVICE')
		 AND sp.id::text = BTRIM(ar.triggered_by_id::text)
		LEFT JOIN LATERAL (
			SELECT client_id, name
			FROM agent_access_clients
			WHERE workspace_id = ar.workspace_id
			  AND service_principal_id = sp.id
			  AND status IS DISTINCT FROM 'DISABLED'
			ORDER BY created_at DESC NULLS LAST, id DESC
			LIMIT 1
		) c ON TRUE
		WHERE ar.workspace_id = $1
		  AND ar.trace_id = ANY($2::text[])
		ORDER BY ar.started_at ` + order + `, ar.id ` + order + `
	`
	rows, err := s.db.QueryContext(ctx, q, workspaceID, pq.Array(traceIDs))
	if err != nil {
		return nil, fmt.Errorf("list agent runs with actors: %w", err)
	}
	defer rows.Close()

	out := make([]RunFact, 0)
	for rows.Next() {
		var run RunFact
		var finished sql.NullTime
		var model []byte
		var username, displayName, spName, clientID, clientName string
		if err := rows.Scan(
			&run.ID, &run.TraceID, &run.Status, &run.TriggeredByType, &run.TriggeredByID,
			&model, &run.StartedAt, &finished,
			&username, &displayName, &spName, &clientID, &clientName,
		); err != nil {
			return nil, err
		}
		run.ModelSnapshot = append(json.RawMessage(nil), model...)
		run.StartedAt = run.StartedAt.UTC()
		if finished.Valid {
			t := finished.Time.UTC()
			run.FinishedAt = &t
		}
		typ := strings.ToUpper(strings.TrimSpace(run.TriggeredByType))
		switch typ {
		case "USER":
			run.TriggeredUsername = strings.TrimSpace(username)
			run.TriggeredDisplayName = strings.TrimSpace(displayName)
		case "SERVICE_PRINCIPAL", "SERVICE":
			// Prefer client display name, then service principal name.
			run.TriggeredClientID = strings.TrimSpace(clientID)
			run.TriggeredClientName = strings.TrimSpace(clientName)
			run.TriggeredDisplayName = firstNonEmpty(clientName, spName)
			run.TriggeredUsername = firstNonEmpty(clientID, spName)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func (s *Service) enrichStepAgentNames(ctx context.Context, workspaceID string, steps []Step) error {
	ids := collectAgentIDsFromSteps(steps)
	if len(ids) == 0 {
		return nil
	}
	names, err := s.loadAgentNames(ctx, workspaceID, ids)
	if err != nil {
		return err
	}
	applyAgentNamesToSteps(steps, names)
	return nil
}

func collectAgentIDsFromSteps(steps []Step) []string {
	seen := map[string]struct{}{}
	var out []string
	var walk func([]Step)
	walk = func(list []Step) {
		for _, st := range list {
			for _, id := range []string{st.CallerAgentID, st.TargetAgentID, st.AgentID} {
				id = strings.TrimSpace(id)
				if id == "" {
					continue
				}
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				out = append(out, id)
			}
			if len(st.Children) > 0 {
				walk(st.Children)
			}
		}
	}
	walk(steps)
	return out
}

func (s *Service) loadAgentNames(ctx context.Context, workspaceID string, agentIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(name, '')
		FROM agents
		WHERE workspace_id = $1 AND id = ANY($2::uuid[])
	`, workspaceID, pq.Array(agentIDs))
	if err != nil {
		return nil, fmt.Errorf("load agent names for audit: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) != "" {
			out[id] = name
		}
	}
	return out, rows.Err()
}

func applyAgentNamesToSteps(steps []Step, names map[string]string) {
	for i := range steps {
		if n := names[strings.TrimSpace(steps[i].CallerAgentID)]; n != "" {
			steps[i].CallerAgentName = n
		}
		if n := names[strings.TrimSpace(steps[i].TargetAgentID)]; n != "" {
			steps[i].TargetAgentName = n
		}
		if len(steps[i].Children) > 0 {
			applyAgentNamesToSteps(steps[i].Children, names)
		}
	}
}

// hydrateCompactSummaryBodies fills context_compaction Content from encrypted objects.
// Caller must ensure platform-admin route gate and debugMode=true.
func (s *Service) hydrateCompactSummaryBodies(ctx context.Context, workspaceID string, steps []Step) {
	if s == nil || s.summaryBodies == nil || !s.debugMode {
		return
	}
	for i := range steps {
		if steps[i].Type != "context_compaction" {
			continue
		}
		summaryID, digest := compactSummaryRefFromParams(steps[i].Params)
		if summaryID == "" {
			// Completed without summary id → leave redacted/empty (no protocol fallback).
			if steps[i].ContentState == "" {
				steps[i].ContentState = ContentRedacted
			}
			continue
		}
		// Only hydrate successful completed compact (fallback/failed stay metadata-only).
		if !compactParamsCompleted(steps[i].Params) {
			continue
		}
		got, err := s.summaryBodies.Open(ctx, PurposeAdminAudit, SummaryBodyReadRequest{
			WorkspaceID:    workspaceID,
			RunID:          steps[i].RunID,
			StepID:         steps[i].StepID,
			SummaryID:      summaryID,
			ExpectedDigest: digest,
		})
		if err != nil {
			steps[i].Content = ""
			steps[i].ContentState = ContentCipher
			continue
		}
		steps[i].Content = got.Body
		if got.State == "" {
			steps[i].ContentState = ContentRedacted
		} else {
			steps[i].ContentState = got.State
		}
	}
}

func compactSummaryRefFromParams(params json.RawMessage) (summaryID, digest string) {
	if len(params) == 0 {
		return "", ""
	}
	var meta map[string]any
	if json.Unmarshal(params, &meta) != nil {
		return "", ""
	}
	if v, ok := meta["summaryId"].(string); ok {
		summaryID = strings.TrimSpace(v)
	}
	if v, ok := meta["summaryDigest"].(string); ok {
		digest = strings.TrimSpace(v)
	}
	return summaryID, digest
}

func compactParamsCompleted(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var meta map[string]any
	if json.Unmarshal(params, &meta) != nil {
		return false
	}
	res, _ := meta["result"].(string)
	return strings.EqualFold(strings.TrimSpace(res), "completed")
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
	// LEFT JOIN agent_run_delegations so AGENT_DELEGATION frames carry authoritative
	// caller/target/child_run/remote IDs/status/latency — not only step input_summary.
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.run_id, s.sequence_no, s.step_type, s.status,
		       COALESCE(s.input_summary, '{}'::jsonb), COALESCE(s.output_summary, '{}'::jsonb),
		       COALESCE(s.raw_object_id::text,''), s.started_at, s.finished_at,
		       COALESCE(s.agent_id::text,''), COALESCE(s.delegation_id::text,''),
		       COALESCE(s.parent_step_id::text,''),
		       COALESCE(d.child_run_id::text,''),
		       COALESCE(d.parent_delegation_id::text,''),
		       COALESCE(d.caller_agent_id::text,''),
		       COALESCE(d.target_agent_id::text,''),
		       COALESCE(d.external_agent_ref,''),
		       COALESCE(d.mode,''), COALESCE(d.protocol,''), COALESCE(d.origin,''),
		       COALESCE(d.depth, 0),
		       COALESCE(d.remote_task_id,''), COALESCE(d.remote_context_id,''),
		       COALESCE(d.remote_message_id,''), COALESCE(d.remote_endpoint_ref,''),
		       COALESCE(d.protocol_status,''),
		       COALESCE(d.error_code,''), COALESCE(d.error_message,''),
		       d.latency_ms, COALESCE(d.status,''),
		       d.input_tokens, d.output_tokens, d.total_tokens, COALESCE(d.tokens_known,false),
		       COALESCE(d.attempt_count,0), COALESCE(d.retry_count,0)
		FROM agent_run_steps s
		LEFT JOIN agent_run_delegations d
		  ON d.workspace_id = s.workspace_id
		 AND d.id = s.delegation_id
		WHERE s.workspace_id = $1 AND s.run_id = ANY($2::uuid[])
		ORDER BY s.started_at ASC, s.sequence_no ASC, s.id ASC
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
		var latency, inTok, outTok, totTok sql.NullInt64
		var tokensKnown bool
		var attempt, retry int
		if err := rows.Scan(
			&step.ID, &step.RunID, &step.SequenceNo, &step.StepType, &step.Status,
			&input, &output, &step.RawObjectID, &step.StartedAt, &finished,
			&step.AgentID, &step.DelegationID, &step.ParentStepID,
			&step.ChildRunID, &step.ParentDelegationID, &step.CallerAgentID, &step.TargetAgentID,
			&step.ExternalAgentRef, &step.Mode, &step.Protocol, &step.Origin,
			&step.Depth, &step.RemoteTaskID, &step.RemoteContextID,
			&step.RemoteMessageID, &step.RemoteEndpointRef, &step.ProtocolStatus,
			&step.DelegationErrorCode, &step.DelegationErrorMsg,
			&latency, &step.DelegationStatus,
			&inTok, &outTok, &totTok, &tokensKnown, &attempt, &retry,
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
		if latency.Valid {
			v := latency.Int64
			step.DelegationLatencyMs = &v
		}
		step.TokensKnown = tokensKnown
		if tokensKnown {
			if inTok.Valid {
				v := inTok.Int64
				step.InputTokens = &v
			}
			if outTok.Valid {
				v := outTok.Int64
				step.OutputTokens = &v
			}
			if totTok.Valid {
				v := totTok.Int64
				step.TotalTokens = &v
			}
		}
		step.AttemptCount, step.RetryCount = attempt, retry
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
