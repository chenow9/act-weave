package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"actweave/backend/internal/listpage"
)

// Tool list sort allowlist — values are SQL expressions only (never raw client input).
var toolListSortColumns = map[string]string{
	"name":      "c.name",
	"protocol":  "head.executor_type",
	"status":    "head.lifecycle_status",
	"createdAt": "c.created_at",
	"updatedAt": "c.updated_at",
	"updatedBy": "c.updated_by",
}

// ListQuery drives GET /workspaces/:wid/tools server-side listing.
type ListQuery struct {
	listpage.Params
	// Status filters by head version lifecycle (DRAFT/REVIEW/TESTED/PUBLISHED)
	// or capability DISABLED. Empty = all.
	Status string
	// Type filters by executor type: HTTP | WORKFLOW (case-insensitive).
	Type string
}

// HeadVersionSummary is a lightweight version snapshot for list rows (no schemas).
type HeadVersionSummary struct {
	ID                  string
	VersionNo           int
	LifecycleStatus     string
	ExecutorType        string
	DefaultConnectionID *string
	// ActionConfig may include method/path for list display; schemas are omitted.
	ActionConfig        json.RawMessage
	ActionSchemaVersion string
	// LockVersion is the tool_versions optimistic lock; required for publish/test CAS
	// from list rows (must not be hard-coded client-side).
	LockVersion int64
}

// ListItem is a tool row with head version summary for management tables.
type ListItem struct {
	Tool
	Head HeadVersionSummary
}

// ListSummary counts the workspace tool set for KPI cards (not the current page).
type ListSummary struct {
	Total     int `json:"total"`
	Published int `json:"published"`
	Tested    int `json:"tested"`
	Draft     int `json:"draft"`
	Review    int `json:"review"`
	Disabled  int `json:"disabled"`
}

// ListPage is a paged tool list result.
type ListPage struct {
	Items    []ListItem
	Page     int
	PageSize int
	Total    int
	Summary  ListSummary
}

// Validate normalizes and validates ListQuery. Returns ErrInvalid on bad input.
func (q *ListQuery) Validate() error {
	if q.Page < 1 || q.Page > listpage.MaxPage || !listpage.IsAllowedPageSize(q.PageSize) {
		return ErrInvalid
	}
	if len(q.Query) > listpage.MaxQueryLen {
		return ErrInvalid
	}
	q.Status = strings.ToUpper(strings.TrimSpace(q.Status))
	switch q.Status {
	case "", "DRAFT", "REVIEW", "TESTED", "PUBLISHED", "DISABLED":
	default:
		return ErrInvalid
	}
	q.Type = strings.ToUpper(strings.TrimSpace(q.Type))
	// FE sends "HTTP Tool" / "Workflow Tool" — accept both forms.
	switch q.Type {
	case "", "HTTP", "WORKFLOW", "HTTP TOOL", "WORKFLOW TOOL":
	default:
		return ErrInvalid
	}
	if strings.HasPrefix(q.Type, "HTTP") {
		q.Type = "HTTP"
	} else if strings.HasPrefix(q.Type, "WORKFLOW") {
		q.Type = "WORKFLOW"
	}
	if q.SortBy != "" {
		if _, ok := toolListSortColumns[q.SortBy]; !ok {
			return ErrInvalid
		}
		if q.SortOrder != "asc" && q.SortOrder != "desc" {
			return ErrInvalid
		}
	}
	return nil
}

// ListPage returns a filtered, sorted, paged tool list with head-version summary.
// It does not load full version schemas — callers load versions on demand for detail/edit.
func (r *Repository) ListPage(ctx context.Context, workspaceID string, query ListQuery) (ListPage, error) {
	if !validUUID(workspaceID) {
		return ListPage{}, ErrInvalid
	}
	if query.Page == 0 && query.PageSize == 0 {
		query.Page = listpage.DefaultPage
		query.PageSize = listpage.DefaultPageSize
	}
	if err := query.Validate(); err != nil {
		return ListPage{}, err
	}

	summary, err := r.listSummary(ctx, workspaceID)
	if err != nil {
		return ListPage{}, err
	}

	args := []any{workspaceID}
	var filters strings.Builder
	filters.WriteString(`
		FROM tools t
		JOIN capabilities c
		  ON c.workspace_id = t.workspace_id AND c.id = t.capability_id
		LEFT JOIN LATERAL (
			SELECT v.id, v.version_no, v.lifecycle_status, v.executor_type,
				v.default_connection_id, v.action_config, v.action_schema_version,
				v.lock_version
			FROM tool_versions v
			WHERE v.workspace_id = t.workspace_id AND v.capability_id = t.capability_id
			ORDER BY v.version_no DESC, v.id DESC
			LIMIT 1
		) head ON TRUE
		WHERE t.workspace_id = $1 AND c.deleted_at IS NULL
	`)

	if query.Status == "DISABLED" {
		filters.WriteString(` AND c.status = 'DISABLED'`)
	} else if query.Status != "" {
		args = append(args, query.Status)
		filters.WriteString(fmt.Sprintf(` AND c.status <> 'DISABLED' AND head.lifecycle_status = $%d`, len(args)))
	}

	if query.Type != "" {
		args = append(args, query.Type)
		filters.WriteString(fmt.Sprintf(` AND UPPER(COALESCE(head.executor_type, 'HTTP')) = $%d`, len(args)))
	}

	if q := strings.TrimSpace(query.Query); q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		n := len(args)
		filters.WriteString(fmt.Sprintf(` AND (
			LOWER(c.name::TEXT) LIKE $%d OR
			LOWER(c.slug::TEXT) LIKE $%d OR
			LOWER(COALESCE(c.description, '')) LIKE $%d OR
			LOWER(COALESCE(head.action_config->>'path', '')) LIKE $%d OR
			LOWER(COALESCE(head.action_config->>'method', '')) LIKE $%d OR
			LOWER(COALESCE(head.executor_type, '')) LIKE $%d
		)`, n, n, n, n, n, n))
	}

	fromWhere := filters.String()

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) `+fromWhere, args...).Scan(&total); err != nil {
		return ListPage{}, fmt.Errorf("count tools page: %w", err)
	}

	orderBy := listpage.SortSQL(query.Params, toolListSortColumns, "c.updated_at DESC, c.id ASC")
	if !strings.Contains(orderBy, "c.id") {
		orderBy = orderBy + ", c.id ASC"
	}

	args = append(args, query.PageSize, query.Offset())
	limitArg := len(args) - 1
	offsetArg := len(args)

	rows, err := r.db.QueryContext(ctx, `
		SELECT `+toolColumns+`,
			head.id, head.version_no, head.lifecycle_status, head.executor_type,
			head.default_connection_id, head.action_config, head.action_schema_version,
			head.lock_version
		`+fromWhere+`
		ORDER BY `+orderBy+`
		LIMIT $`+strconv.Itoa(limitArg)+` OFFSET $`+strconv.Itoa(offsetArg), args...)
	if err != nil {
		return ListPage{}, fmt.Errorf("list tools page: %w", err)
	}
	defer rows.Close()

	items := make([]ListItem, 0, query.PageSize)
	for rows.Next() {
		item, err := scanToolListItem(rows)
		if err != nil {
			return ListPage{}, fmt.Errorf("scan tool list item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, err
	}

	return ListPage{
		Items:    items,
		Page:     query.Page,
		PageSize: query.PageSize,
		Total:    total,
		Summary:  summary,
	}, nil
}

func (r *Repository) listSummary(ctx context.Context, workspaceID string) (ListSummary, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE c.status = 'DISABLED')::int AS disabled,
			COUNT(*) FILTER (
				WHERE c.status <> 'DISABLED' AND head.lifecycle_status = 'PUBLISHED'
			)::int AS published,
			COUNT(*) FILTER (
				WHERE c.status <> 'DISABLED' AND head.lifecycle_status = 'TESTED'
			)::int AS tested,
			COUNT(*) FILTER (
				WHERE c.status <> 'DISABLED' AND head.lifecycle_status = 'DRAFT'
			)::int AS draft,
			COUNT(*) FILTER (
				WHERE c.status <> 'DISABLED' AND head.lifecycle_status = 'REVIEW'
			)::int AS review
		FROM tools t
		JOIN capabilities c
		  ON c.workspace_id = t.workspace_id AND c.id = t.capability_id
		LEFT JOIN LATERAL (
			SELECT v.lifecycle_status
			FROM tool_versions v
			WHERE v.workspace_id = t.workspace_id AND v.capability_id = t.capability_id
			ORDER BY v.version_no DESC, v.id DESC
			LIMIT 1
		) head ON TRUE
		WHERE t.workspace_id = $1 AND c.deleted_at IS NULL
	`, workspaceID)
	var s ListSummary
	if err := row.Scan(&s.Total, &s.Disabled, &s.Published, &s.Tested, &s.Draft, &s.Review); err != nil {
		return ListSummary{}, fmt.Errorf("tool list summary: %w", err)
	}
	return s, nil
}

func scanToolListItem(row rowScanner) (ListItem, error) {
	var value Tool
	var headID, lifecycle, executor, schemaVersion sql.NullString
	var versionNo, headLock sql.NullInt64
	var defaultConn sql.NullString
	var actionConfig []byte
	err := row.Scan(
		&value.CapabilityID, &value.WorkspaceID, &value.ProviderID,
		&value.SourceAssetID, &value.DefaultConnectionID, &value.SourceEndpointID,
		&value.Name, &value.Slug, &value.Description, &value.Status, &value.ActiveReleaseID,
		&value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.LockVersion, &value.DeletedAt,
		&headID, &versionNo, &lifecycle, &executor,
		&defaultConn, &actionConfig, &schemaVersion, &headLock,
	)
	if err != nil {
		return ListItem{}, err
	}
	item := ListItem{Tool: value}
	if headID.Valid {
		item.Head = HeadVersionSummary{
			ID:                  headID.String,
			VersionNo:           int(versionNo.Int64),
			LifecycleStatus:     lifecycle.String,
			ExecutorType:        executor.String,
			ActionSchemaVersion: schemaVersion.String,
			ActionConfig:        append(json.RawMessage(nil), actionConfig...),
			LockVersion:         headLock.Int64,
		}
		if defaultConn.Valid {
			conn := defaultConn.String
			item.Head.DefaultConnectionID = &conn
		}
	}
	return item, nil
}
