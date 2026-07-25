// Package workspaceoverview aggregates production-facing health metrics
// across one or more workspaces for the platform Overview console.
package workspaceoverview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

const (
	defaultWindowDays = 14
	minWindowDays     = 1
	maxWindowDays     = 366
)

// Range is an inclusive calendar-day window [From, To] in UTC (date-only semantics).
type Range struct {
	From time.Time // start of first day UTC (inclusive)
	To   time.Time // start of last day UTC (inclusive)
}

// Metrics is the API payload for GET /overview/metrics (all accessible workspaces).
type Metrics struct {
	WindowDays     int        `json:"windowDays"`
	From           time.Time  `json:"from"`
	To             time.Time  `json:"to"` // exclusive end (start of day after last inclusive day)
	FromDate       string     `json:"fromDate"` // YYYY-MM-DD inclusive
	ToDate         string     `json:"toDate"`   // YYYY-MM-DD inclusive
	WorkspaceCount int        `json:"workspaceCount"`
	WorkspaceIDs   []string   `json:"workspaceIds,omitempty"`
	KPIs           KPIs       `json:"kpis"`
	Series         []DayPoint `json:"series"`
	Inventory      Inventory  `json:"inventory"`
	// Breakdowns for detail tables.
	TopTools       []EntityStat `json:"topTools"`
	TopWorkspaces  []EntityStat `json:"topWorkspaces"`
	FailingTools   []EntityStat `json:"failingTools"`
}

type KPIs struct {
	ToolCallSuccessRate float64 `json:"toolCallSuccessRate"`
	ToolCallsTotal      int64   `json:"toolCallsTotal"`
	ToolCallsSucceeded  int64   `json:"toolCallsSucceeded"`
	ToolCallsFailed     int64   `json:"toolCallsFailed"`
	AvgToolLatencyMs    float64 `json:"avgToolLatencyMs"`

	RunSuccessRate  float64 `json:"runSuccessRate"`
	RunsTotal       int64   `json:"runsTotal"`
	RunsSucceeded   int64   `json:"runsSucceeded"`
	RunsFailed      int64   `json:"runsFailed"`
	AvgRunLatencyMs float64 `json:"avgRunLatencyMs"`

	// Workflow production executions in the window.
	WorkflowSuccessRate  float64 `json:"workflowSuccessRate"`
	WorkflowTotal        int64   `json:"workflowTotal"`
	WorkflowSucceeded    int64   `json:"workflowSucceeded"`
	WorkflowFailed       int64   `json:"workflowFailed"`
	AvgWorkflowLatencyMs float64 `json:"avgWorkflowLatencyMs"`

	SessionCountToday  int64   `json:"sessionCountToday"`
	SessionCountPeriod int64   `json:"sessionCountPeriod"`
	AvgSessionsPerDay  float64 `json:"avgSessionsPerDay"`
}

type DayPoint struct {
	Date               string `json:"date"`
	Sessions           int64  `json:"sessions"`
	RunsTotal          int64  `json:"runsTotal"`
	RunsSucceeded      int64  `json:"runsSucceeded"`
	RunsFailed         int64  `json:"runsFailed"`
	ToolCallsTotal     int64  `json:"toolCallsTotal"`
	ToolCallsSucceeded int64  `json:"toolCallsSucceeded"`
	ToolCallsFailed    int64  `json:"toolCallsFailed"`
	WorkflowTotal      int64  `json:"workflowTotal"`
	WorkflowSucceeded  int64  `json:"workflowSucceeded"`
	WorkflowFailed     int64  `json:"workflowFailed"`
}

type Inventory struct {
	WorkspaceCount      int  `json:"workspaceCount"`
	AgentCount          int  `json:"agentCount"`
	ToolCount           int  `json:"toolCount"`
	WorkflowCount       int  `json:"workflowCount"`
	ConnectionTotal     int  `json:"connectionTotal"`
	ConnectionVerified  int  `json:"connectionVerified"`
	ModelConfigTotal    int  `json:"modelConfigTotal"`
	ModelConfigVerified int  `json:"modelConfigVerified"`
	HasVerifiedModel    bool `json:"hasVerifiedModel"`
}

// EntityStat is a ranked breakdown row (tool / workspace).
type EntityStat struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Total           int64   `json:"total"`
	Succeeded       int64   `json:"succeeded"`
	Failed          int64   `json:"failed"`
	SuccessRate     float64 `json:"successRate"`
	AvgLatencyMs    float64 `json:"avgLatencyMs,omitempty"`
	Sessions        int64   `json:"sessions,omitempty"`
	Runs            int64   `json:"runs,omitempty"`
	ToolCalls       int64   `json:"toolCalls,omitempty"`
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, errors.New("workspace overview database is required")
	}
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// NormalizeRange validates/clamps an inclusive date range. Zero times fall back to last 14 days.
func NormalizeRange(now time.Time, from, toInclusive time.Time) (Range, int, error) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var start, endDay time.Time
	if from.IsZero() && toInclusive.IsZero() {
		endDay = today
		start = today.AddDate(0, 0, -(defaultWindowDays - 1))
	} else if from.IsZero() {
		endDay = startOfUTCDay(toInclusive)
		start = endDay.AddDate(0, 0, -(defaultWindowDays - 1))
	} else if toInclusive.IsZero() {
		start = startOfUTCDay(from)
		endDay = today
	} else {
		start = startOfUTCDay(from)
		endDay = startOfUTCDay(toInclusive)
	}

	if endDay.Before(start) {
		return Range{}, 0, errors.New("to must be on or after from")
	}
	// Cap open-ended future: end cannot be after today for "today" KPIs consistency.
	if endDay.After(today) {
		endDay = today
	}
	days := int(endDay.Sub(start).Hours()/24) + 1
	if days < minWindowDays {
		days = minWindowDays
	}
	if days > maxWindowDays {
		// Keep the end day; truncate the start so the window is at most maxWindowDays.
		start = endDay.AddDate(0, 0, -(maxWindowDays - 1))
		days = maxWindowDays
	}
	return Range{From: start, To: endDay}, days, nil
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Metrics aggregates KPIs across the given workspace IDs for an inclusive calendar range.
// Pass zero from/to for the default last-14-days window.
func (s *Service) Metrics(ctx context.Context, workspaceIDs []string, from, toInclusive time.Time) (Metrics, error) {
	ids := sanitizeWorkspaceIDs(workspaceIDs)
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	rng, days, err := NormalizeRange(now, from, toInclusive)
	if err != nil {
		return Metrics{}, err
	}
	// exclusive end for SQL: day after last inclusive day
	toExclusive := rng.To.AddDate(0, 0, 1)

	out := Metrics{
		WindowDays:     days,
		From:           rng.From,
		To:             toExclusive,
		FromDate:       rng.From.Format("2006-01-02"),
		ToDate:         rng.To.Format("2006-01-02"),
		WorkspaceCount: len(ids),
		WorkspaceIDs:   ids,
		Series:         emptySeries(rng.From, days),
		TopTools:       []EntityStat{},
		TopWorkspaces:  []EntityStat{},
		FailingTools:   []EntityStat{},
	}
	out.Inventory.WorkspaceCount = len(ids)

	if len(ids) == 0 {
		return out, nil
	}

	if err := s.fillInventory(ctx, ids, &out.Inventory); err != nil {
		return Metrics{}, err
	}
	if err := s.fillSessionKPIs(ctx, ids, rng.From, toExclusive, today, &out); err != nil {
		return Metrics{}, err
	}
	if err := s.fillRunKPIs(ctx, ids, rng.From, toExclusive, &out); err != nil {
		return Metrics{}, err
	}
	if err := s.fillToolKPIs(ctx, ids, rng.From, toExclusive, &out); err != nil {
		return Metrics{}, err
	}
	if err := s.fillWorkflowKPIs(ctx, ids, rng.From, toExclusive, &out); err != nil {
		return Metrics{}, err
	}
	if err := s.fillSessionSeries(ctx, ids, rng.From, toExclusive, out.Series); err != nil {
		return Metrics{}, err
	}
	if err := s.fillRunSeries(ctx, ids, rng.From, toExclusive, out.Series); err != nil {
		return Metrics{}, err
	}
	if err := s.fillToolSeries(ctx, ids, rng.From, toExclusive, out.Series); err != nil {
		return Metrics{}, err
	}
	if err := s.fillWorkflowSeries(ctx, ids, rng.From, toExclusive, out.Series); err != nil {
		return Metrics{}, err
	}
	if tools, err := s.queryTopTools(ctx, ids, rng.From, toExclusive, 10, false); err != nil {
		return Metrics{}, err
	} else {
		out.TopTools = tools
	}
	if failing, err := s.queryTopTools(ctx, ids, rng.From, toExclusive, 10, true); err != nil {
		return Metrics{}, err
	} else {
		out.FailingTools = failing
	}
	if spaces, err := s.queryTopWorkspaces(ctx, ids, rng.From, toExclusive, 10); err != nil {
		return Metrics{}, err
	} else {
		out.TopWorkspaces = spaces
	}
	return out, nil
}

// ListAccessibleWorkspaceIDs returns active workspaces the user can access.
// Platform admins see every non-deleted workspace.
func (s *Service) ListAccessibleWorkspaceIDs(ctx context.Context, userID string, platformAdmin bool) ([]string, error) {
	uid := trimUUID(userID)
	if uid == "" {
		return nil, errors.New("user id is required")
	}
	var rows *sql.Rows
	var err error
	if platformAdmin {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id::text FROM workspaces
			WHERE deleted_at IS NULL
			ORDER BY updated_at DESC, id
			LIMIT 500
		`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT w.id::text
			FROM workspaces w
			JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $1 AND m.disabled_at IS NULL
			WHERE w.deleted_at IS NULL
			ORDER BY w.updated_at DESC, w.id
			LIMIT 500
		`, uid)
	}
	if err != nil {
		return nil, fmt.Errorf("list overview workspaces: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func sanitizeWorkspaceIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = trimUUID(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func trimUUID(s string) string {
	// Minimal trim; SQL uuid cast will reject junk.
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func emptySeries(from time.Time, days int) []DayPoint {
	series := make([]DayPoint, 0, days)
	for i := 0; i < days; i++ {
		day := from.AddDate(0, 0, i)
		series = append(series, DayPoint{Date: day.Format("2006-01-02")})
	}
	return series
}

func indexSeries(series []DayPoint) map[string]int {
	out := make(map[string]int, len(series))
	for i, p := range series {
		out[p.Date] = i
	}
	return out
}

func (s *Service) fillInventory(ctx context.Context, ids []string, inv *Inventory) error {
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*)::int FROM agents a
			  WHERE a.workspace_id = ANY($1::uuid[]) AND a.deleted_at IS NULL),
			(SELECT COUNT(*)::int FROM tools t
			  JOIN capabilities c ON c.workspace_id = t.workspace_id AND c.id = t.capability_id
			  WHERE t.workspace_id = ANY($1::uuid[]) AND c.deleted_at IS NULL AND c.kind = 'TOOL'),
			(SELECT COUNT(*)::int FROM workflows w
			  JOIN capabilities c ON c.workspace_id = w.workspace_id AND c.id = w.capability_id
			  WHERE w.workspace_id = ANY($1::uuid[]) AND c.deleted_at IS NULL AND c.kind = 'WORKFLOW'),
			(SELECT COUNT(*)::int FROM service_connections c
			  WHERE c.workspace_id = ANY($1::uuid[]) AND c.deleted_at IS NULL),
			(SELECT COUNT(*)::int FROM service_connections c
			  WHERE c.workspace_id = ANY($1::uuid[]) AND c.deleted_at IS NULL AND c.status = 'VERIFIED'),
			(SELECT COUNT(*)::int FROM model_configs m
			  WHERE m.workspace_id = ANY($1::uuid[]) AND m.deleted_at IS NULL),
			(SELECT COUNT(*)::int FROM model_configs m
			  WHERE m.workspace_id = ANY($1::uuid[]) AND m.deleted_at IS NULL AND m.status = 'VERIFIED')
	`, pq.Array(ids)).Scan(
		&inv.AgentCount, &inv.ToolCount, &inv.WorkflowCount,
		&inv.ConnectionTotal, &inv.ConnectionVerified,
		&inv.ModelConfigTotal, &inv.ModelConfigVerified,
	)
	if err != nil {
		return fmt.Errorf("overview inventory: %w", err)
	}
	inv.WorkspaceCount = len(ids)
	inv.HasVerifiedModel = inv.ModelConfigVerified > 0
	return nil
}

func (s *Service) fillSessionKPIs(ctx context.Context, ids []string, from, to, today time.Time, out *Metrics) error {
	var period, todayCount int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $2 AND created_at < $3)::bigint,
			COUNT(*) FILTER (WHERE created_at >= $4 AND created_at < $3)::bigint
		FROM chat_sessions
		WHERE workspace_id = ANY($1::uuid[])
	`, pq.Array(ids), from, to, today).Scan(&period, &todayCount); err != nil {
		return fmt.Errorf("overview session kpis: %w", err)
	}
	out.KPIs.SessionCountPeriod = period
	out.KPIs.SessionCountToday = todayCount
	if out.WindowDays > 0 {
		out.KPIs.AvgSessionsPerDay = float64(period) / float64(out.WindowDays)
	}
	return nil
}

func (s *Service) fillRunKPIs(ctx context.Context, ids []string, from, to time.Time, out *Metrics) error {
	var total, succeeded, failed int64
	var avgMs sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint,
			AVG(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
				FILTER (WHERE finished_at IS NOT NULL AND status IN ('SUCCEEDED','FAILED','CANCELLED'))
		FROM agent_runs
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
	`, pq.Array(ids), from, to).Scan(&total, &succeeded, &failed, &avgMs); err != nil {
		return fmt.Errorf("overview run kpis: %w", err)
	}
	out.KPIs.RunsTotal = total
	out.KPIs.RunsSucceeded = succeeded
	out.KPIs.RunsFailed = failed
	if total > 0 {
		out.KPIs.RunSuccessRate = float64(succeeded) * 100 / float64(total)
	}
	if avgMs.Valid {
		out.KPIs.AvgRunLatencyMs = avgMs.Float64
	}
	return nil
}

func (s *Service) fillToolKPIs(ctx context.Context, ids []string, from, to time.Time, out *Metrics) error {
	var total, succeeded, failed int64
	var avgMs sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint,
			AVG(latency_ms)
				FILTER (WHERE latency_ms IS NOT NULL AND status IN ('SUCCEEDED','FAILED','CANCELLED'))
		FROM tool_invocations
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
	`, pq.Array(ids), from, to).Scan(&total, &succeeded, &failed, &avgMs); err != nil {
		return fmt.Errorf("overview tool kpis: %w", err)
	}
	out.KPIs.ToolCallsTotal = total
	out.KPIs.ToolCallsSucceeded = succeeded
	out.KPIs.ToolCallsFailed = failed
	if total > 0 {
		out.KPIs.ToolCallSuccessRate = float64(succeeded) * 100 / float64(total)
	}
	if avgMs.Valid {
		out.KPIs.AvgToolLatencyMs = avgMs.Float64
	}
	return nil
}

func (s *Service) fillSessionSeries(ctx context.Context, ids []string, from, to time.Time, series []DayPoint) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (created_at AT TIME ZONE 'UTC')::date AS day, COUNT(*)::bigint
		FROM chat_sessions
		WHERE workspace_id = ANY($1::uuid[])
		  AND created_at >= $2 AND created_at < $3
		GROUP BY day
		ORDER BY day
	`, pq.Array(ids), from, to)
	if err != nil {
		return fmt.Errorf("overview session series: %w", err)
	}
	defer rows.Close()
	idx := indexSeries(series)
	for rows.Next() {
		var day time.Time
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			return err
		}
		key := day.UTC().Format("2006-01-02")
		if i, ok := idx[key]; ok {
			series[i].Sessions = count
		}
	}
	return rows.Err()
}

func (s *Service) fillRunSeries(ctx context.Context, ids []string, from, to time.Time, series []DayPoint) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at AT TIME ZONE 'UTC')::date AS day,
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint
		FROM agent_runs
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
		GROUP BY day
		ORDER BY day
	`, pq.Array(ids), from, to)
	if err != nil {
		return fmt.Errorf("overview run series: %w", err)
	}
	defer rows.Close()
	idx := indexSeries(series)
	for rows.Next() {
		var day time.Time
		var total, ok, fail int64
		if err := rows.Scan(&day, &total, &ok, &fail); err != nil {
			return err
		}
		key := day.UTC().Format("2006-01-02")
		if i, found := idx[key]; found {
			series[i].RunsTotal = total
			series[i].RunsSucceeded = ok
			series[i].RunsFailed = fail
		}
	}
	return rows.Err()
}

func (s *Service) fillToolSeries(ctx context.Context, ids []string, from, to time.Time, series []DayPoint) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at AT TIME ZONE 'UTC')::date AS day,
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint
		FROM tool_invocations
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
		GROUP BY day
		ORDER BY day
	`, pq.Array(ids), from, to)
	if err != nil {
		return fmt.Errorf("overview tool series: %w", err)
	}
	defer rows.Close()
	idx := indexSeries(series)
	for rows.Next() {
		var day time.Time
		var total, ok, fail int64
		if err := rows.Scan(&day, &total, &ok, &fail); err != nil {
			return err
		}
		key := day.UTC().Format("2006-01-02")
		if i, found := idx[key]; found {
			series[i].ToolCallsTotal = total
			series[i].ToolCallsSucceeded = ok
			series[i].ToolCallsFailed = fail
		}
	}
	return rows.Err()
}

func (s *Service) fillWorkflowKPIs(ctx context.Context, ids []string, from, to time.Time, out *Metrics) error {
	var total, succeeded, failed int64
	var avgMs sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint,
			AVG(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
				FILTER (WHERE finished_at IS NOT NULL AND status IN ('SUCCEEDED','FAILED','CANCELLED'))
		FROM workflow_executions
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
	`, pq.Array(ids), from, to).Scan(&total, &succeeded, &failed, &avgMs); err != nil {
		return fmt.Errorf("overview workflow kpis: %w", err)
	}
	out.KPIs.WorkflowTotal = total
	out.KPIs.WorkflowSucceeded = succeeded
	out.KPIs.WorkflowFailed = failed
	if total > 0 {
		out.KPIs.WorkflowSuccessRate = float64(succeeded) * 100 / float64(total)
	}
	if avgMs.Valid {
		out.KPIs.AvgWorkflowLatencyMs = avgMs.Float64
	}
	return nil
}

func (s *Service) fillWorkflowSeries(ctx context.Context, ids []string, from, to time.Time, series []DayPoint) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at AT TIME ZONE 'UTC')::date AS day,
			COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint,
			COUNT(*) FILTER (WHERE status = 'SUCCEEDED')::bigint,
			COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED'))::bigint
		FROM workflow_executions
		WHERE workspace_id = ANY($1::uuid[])
		  AND started_at >= $2 AND started_at < $3
		GROUP BY day
		ORDER BY day
	`, pq.Array(ids), from, to)
	if err != nil {
		return fmt.Errorf("overview workflow series: %w", err)
	}
	defer rows.Close()
	idx := indexSeries(series)
	for rows.Next() {
		var day time.Time
		var total, ok, fail int64
		if err := rows.Scan(&day, &total, &ok, &fail); err != nil {
			return err
		}
		key := day.UTC().Format("2006-01-02")
		if i, found := idx[key]; found {
			series[i].WorkflowTotal = total
			series[i].WorkflowSucceeded = ok
			series[i].WorkflowFailed = fail
		}
	}
	return rows.Err()
}

func (s *Service) queryTopTools(ctx context.Context, ids []string, from, to time.Time, limit int, orderByFailures bool) ([]EntityStat, error) {
	order := `total DESC, failed DESC`
	if orderByFailures {
		order = `failed DESC, total DESC`
	}
	// #nosec G201 -- order is a fixed internal constant, not user input.
	q := `
		SELECT ti.tool_id::text,
			COALESCE(c.name::text, ti.tool_id::text) AS name,
			COUNT(*) FILTER (WHERE ti.status IN ('SUCCEEDED','FAILED','CANCELLED'))::bigint AS total,
			COUNT(*) FILTER (WHERE ti.status = 'SUCCEEDED')::bigint AS succeeded,
			COUNT(*) FILTER (WHERE ti.status IN ('FAILED','CANCELLED'))::bigint AS failed,
			COALESCE(AVG(ti.latency_ms) FILTER (
				WHERE ti.latency_ms IS NOT NULL AND ti.status IN ('SUCCEEDED','FAILED','CANCELLED')
			), 0)
		FROM tool_invocations ti
		LEFT JOIN capabilities c
			ON c.workspace_id = ti.workspace_id AND c.id = ti.tool_id AND c.deleted_at IS NULL
		WHERE ti.workspace_id = ANY($1::uuid[])
		  AND ti.started_at >= $2 AND ti.started_at < $3
		GROUP BY ti.tool_id, c.name
		HAVING COUNT(*) FILTER (WHERE ti.status IN ('SUCCEEDED','FAILED','CANCELLED')) > 0
		ORDER BY ` + order + `
		LIMIT $4
	`
	rows, err := s.db.QueryContext(ctx, q, pq.Array(ids), from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("overview top tools: %w", err)
	}
	defer rows.Close()
	out := make([]EntityStat, 0, limit)
	for rows.Next() {
		var st EntityStat
		var avg float64
		if err := rows.Scan(&st.ID, &st.Name, &st.Total, &st.Succeeded, &st.Failed, &avg); err != nil {
			return nil, err
		}
		st.AvgLatencyMs = avg
		st.ToolCalls = st.Total
		if st.Total > 0 {
			st.SuccessRate = float64(st.Succeeded) * 100 / float64(st.Total)
		}
		if orderByFailures && st.Failed == 0 {
			continue
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Service) queryTopWorkspaces(ctx context.Context, ids []string, from, to time.Time, limit int) ([]EntityStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id::text,
			COALESCE(NULLIF(w.display_name, ''), w.slug::text, w.id::text) AS name,
			COALESCE(r.runs, 0)::bigint,
			COALESCE(r.runs_ok, 0)::bigint,
			COALESCE(r.runs_fail, 0)::bigint,
			COALESCE(t.tools, 0)::bigint,
			COALESCE(s.sessions, 0)::bigint
		FROM workspaces w
		LEFT JOIN (
			SELECT workspace_id,
				COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED')) AS runs,
				COUNT(*) FILTER (WHERE status = 'SUCCEEDED') AS runs_ok,
				COUNT(*) FILTER (WHERE status IN ('FAILED','CANCELLED')) AS runs_fail
			FROM agent_runs
			WHERE workspace_id = ANY($1::uuid[])
			  AND started_at >= $2 AND started_at < $3
			GROUP BY workspace_id
		) r ON r.workspace_id = w.id
		LEFT JOIN (
			SELECT workspace_id,
				COUNT(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','CANCELLED')) AS tools
			FROM tool_invocations
			WHERE workspace_id = ANY($1::uuid[])
			  AND started_at >= $2 AND started_at < $3
			GROUP BY workspace_id
		) t ON t.workspace_id = w.id
		LEFT JOIN (
			SELECT workspace_id, COUNT(*) AS sessions
			FROM chat_sessions
			WHERE workspace_id = ANY($1::uuid[])
			  AND created_at >= $2 AND created_at < $3
			GROUP BY workspace_id
		) s ON s.workspace_id = w.id
		WHERE w.id = ANY($1::uuid[]) AND w.deleted_at IS NULL
		ORDER BY (COALESCE(r.runs,0) + COALESCE(t.tools,0) + COALESCE(s.sessions,0)) DESC, w.display_name
		LIMIT $4
	`, pq.Array(ids), from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("overview top workspaces: %w", err)
	}
	defer rows.Close()
	out := make([]EntityStat, 0, limit)
	for rows.Next() {
		var st EntityStat
		if err := rows.Scan(&st.ID, &st.Name, &st.Runs, &st.Succeeded, &st.Failed, &st.ToolCalls, &st.Sessions); err != nil {
			return nil, err
		}
		st.Total = st.Runs + st.ToolCalls + st.Sessions
		if st.Runs > 0 {
			st.SuccessRate = float64(st.Succeeded) * 100 / float64(st.Runs)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
