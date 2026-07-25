package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"

	"github.com/lib/pq"
)

const auditEventColumns = `
	id,occurred_at,workspace_id,actor_type,actor_id,actor_display,action,
	resource_type,resource_id,result,request_id,trace_id,source_ip,user_agent,
	changes,metadata,payload_object_id,schema_version
`

type QueryInput struct {
	WorkspaceID      string
	ActorType        string
	ActorID          string
	ResourceType     string
	ResourceID       string
	Action           string
	Results          []string
	RequestID        string
	TraceID          string
	OccurredFrom     time.Time
	OccurredUntil    time.Time
	BeforeOccurredAt time.Time
	BeforeID         string
	Limit            int
	IncludeSensitive bool
}

type QueryService struct {
	db      *sql.DB
	payload AuditPayloadReader
}

type AuditPayloadReader interface {
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

func NewQueryService(db *sql.DB, payload AuditPayloadReader) (*QueryService, error) {
	if db == nil {
		return nil, errors.New("audit query database is required")
	}
	return &QueryService{db: db, payload: payload}, nil
}

func (service *QueryService) Query(
	ctx context.Context,
	role workspace.Role,
	input QueryInput,
) ([]Event, error) {
	input, err := normalizeQueryInput(input)
	if err != nil {
		return nil, err
	}
	if input.IncludeSensitive && !canManageAudit(role) {
		return nil, authz.ErrDenied
	}
	query := `SELECT ` + auditEventColumns + ` FROM audit_events WHERE workspace_id=$1`
	arguments := []any{input.WorkspaceID}
	add := func(clause string, argument any) {
		arguments = append(arguments, argument)
		query += fmt.Sprintf(clause, len(arguments))
	}
	if input.ActorType != "" {
		add(` AND actor_type=$%d`, input.ActorType)
	}
	if input.ActorID != "" {
		add(` AND actor_id=$%d`, input.ActorID)
	}
	if input.ResourceType != "" {
		add(` AND resource_type=$%d`, input.ResourceType)
	}
	if input.ResourceID != "" {
		add(` AND resource_id=$%d`, input.ResourceID)
	}
	if input.Action != "" {
		add(` AND action=$%d`, input.Action)
	}
	if len(input.Results) > 0 {
		add(` AND result=ANY($%d)`, pq.Array(input.Results))
	}
	if input.RequestID != "" {
		add(` AND request_id=$%d`, input.RequestID)
	}
	if input.TraceID != "" {
		add(` AND trace_id=$%d`, input.TraceID)
	}
	if !input.OccurredFrom.IsZero() {
		add(` AND occurred_at >= $%d`, input.OccurredFrom)
	}
	if !input.OccurredUntil.IsZero() {
		add(` AND occurred_at < $%d`, input.OccurredUntil)
	}
	if !input.BeforeOccurredAt.IsZero() {
		arguments = append(arguments, input.BeforeOccurredAt, input.BeforeID)
		query += fmt.Sprintf(` AND (occurred_at,id) < ($%d,$%d)`, len(arguments)-1, len(arguments))
	}
	arguments = append(arguments, input.Limit)
	query += fmt.Sprintf(` ORDER BY occurred_at DESC,id DESC LIMIT $%d`, len(arguments))
	rows, err := service.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()
	values := make([]Event, 0)
	for rows.Next() {
		value, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan audit query: %w", err)
		}
		if !input.IncludeSensitive {
			value.SourceIP = netip.Addr{}
			value.UserAgent, value.PayloadObjectID = "", ""
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (service *QueryService) Get(
	ctx context.Context,
	role workspace.Role,
	workspaceID, eventID string,
	includeSensitive bool,
) (Event, error) {
	workspaceID, eventID = strings.TrimSpace(workspaceID), strings.TrimSpace(eventID)
	if !validAuditUUID(workspaceID) || !validAuditUUID(eventID) {
		return Event{}, ErrInvalid
	}
	if includeSensitive && !canManageAudit(role) {
		return Event{}, authz.ErrDenied
	}
	value, err := scanEvent(service.db.QueryRowContext(ctx, `
		SELECT `+auditEventColumns+` FROM audit_events
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("get audit event: %w", err)
	}
	if !includeSensitive {
		value.SourceIP = netip.Addr{}
		value.UserAgent, value.PayloadObjectID = "", ""
	}
	return value, nil
}

func (service *QueryService) OpenPayload(
	ctx context.Context,
	workspaceID, eventID, actorID string,
	role workspace.Role,
) (storedobject.OpenedObject, error) {
	if !canManageAudit(role) {
		return storedobject.OpenedObject{}, authz.ErrDenied
	}
	if service.payload == nil || !validAuditUUID(workspaceID) || !validAuditUUID(eventID) || !validAuditUUID(actorID) {
		return storedobject.OpenedObject{}, ErrInvalid
	}
	var payloadID sql.NullString
	err := service.db.QueryRowContext(ctx, `SELECT payload_object_id FROM audit_events
		WHERE workspace_id=$1 AND id=$2 ORDER BY occurred_at DESC LIMIT 1`, workspaceID, eventID).Scan(&payloadID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !payloadID.Valid) {
		return storedobject.OpenedObject{}, ErrNotFound
	}
	if err != nil {
		return storedobject.OpenedObject{}, fmt.Errorf("get audit payload reference: %w", err)
	}
	return service.payload.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: workspaceID, ObjectID: payloadID.String,
		ActorType: storedobject.CreatorUser, ActorID: actorID,
	})
}

func normalizeQueryInput(input QueryInput) (QueryInput, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ActorType = strings.ToUpper(strings.TrimSpace(input.ActorType))
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ResourceType = strings.ToUpper(strings.TrimSpace(input.ResourceType))
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.RequestID, input.TraceID = strings.TrimSpace(input.RequestID), strings.TrimSpace(input.TraceID)
	input.BeforeID = strings.TrimSpace(input.BeforeID)
	if input.Limit == 0 {
		input.Limit = 100
	}
	if !validAuditUUID(input.WorkspaceID) || input.Limit < 1 || input.Limit > 500 ||
		(input.ActorType != "" && input.ActorType != "USER" && input.ActorType != "SERVICE_PRINCIPAL" && input.ActorType != "SYSTEM") ||
		(input.ActorID != "" && !validAuditUUID(input.ActorID)) ||
		(input.ResourceType != "" && !resourceTypePattern.MatchString(input.ResourceType)) ||
		(input.ResourceID != "" && !validAuditUUID(input.ResourceID)) ||
		(input.Action != "" && !actionPattern.MatchString(input.Action)) ||
		len(input.RequestID) > 255 || len(input.TraceID) > 255 ||
		(!input.OccurredFrom.IsZero() && !input.OccurredUntil.IsZero() && !input.OccurredFrom.Before(input.OccurredUntil)) ||
		(input.BeforeOccurredAt.IsZero() != (input.BeforeID == "")) ||
		(input.BeforeID != "" && !validAuditUUID(input.BeforeID)) {
		return QueryInput{}, ErrInvalid
	}
	seen := make(map[string]struct{}, len(input.Results))
	results := make([]string, 0, len(input.Results))
	for _, result := range input.Results {
		result = strings.ToUpper(strings.TrimSpace(result))
		if result != "SUCCESS" && result != "FAILURE" && result != "DENIED" {
			return QueryInput{}, ErrInvalid
		}
		if _, exists := seen[result]; exists {
			continue
		}
		seen[result] = struct{}{}
		results = append(results, result)
	}
	input.Results = results
	input.OccurredFrom = input.OccurredFrom.UTC()
	input.OccurredUntil = input.OccurredUntil.UTC()
	input.BeforeOccurredAt = input.BeforeOccurredAt.UTC()
	return input, nil
}

func canManageAudit(role workspace.Role) bool {
	return role == workspace.RoleOwner || role == workspace.RoleAdmin
}
