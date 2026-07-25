package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/workspace"
)

const maxAuditExportLifetime = 24 * time.Hour

type Export struct {
	ID             string
	WorkspaceID    string
	FilterSnapshot json.RawMessage
	Status         string
	ObjectID       string
	RequestedBy    string
	RequestedAt    time.Time
	CompletedAt    *time.Time
	ExpiresAt      time.Time
	ErrorCode      string
}

type CreateExportInput struct {
	ID          string
	WorkspaceID string
	RequestedBy string
	Role        workspace.Role
	Filter      QueryInput
	ExpiresAt   time.Time
}

type AuditExportObjectStore interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
	PresignDownload(context.Context, storedobject.ReadRequest, time.Duration) (*url.URL, error)
}

type ExportService struct {
	db       *sql.DB
	queries  *QueryService
	recorder *Recorder
	objects  AuditExportObjectStore
	now      func() time.Time
}

func NewExportService(
	db *sql.DB,
	queries *QueryService,
	recorder *Recorder,
	objects AuditExportObjectStore,
) (*ExportService, error) {
	if db == nil || queries == nil || recorder == nil || objects == nil {
		return nil, errors.New("audit export dependencies are required")
	}
	return &ExportService{
		db: db, queries: queries, recorder: recorder, objects: objects,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (service *ExportService) Create(
	ctx context.Context,
	input CreateExportInput,
) (Export, error) {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	now := service.now().UTC()
	if !canManageAudit(input.Role) {
		return Export{}, authz.ErrDenied
	}
	if !validAuditUUID(input.ID) || !validAuditUUID(input.WorkspaceID) ||
		!validAuditUUID(input.RequestedBy) || !input.ExpiresAt.After(now) ||
		input.ExpiresAt.After(now.Add(maxAuditExportLifetime)) {
		return Export{}, ErrInvalid
	}
	input.Filter.WorkspaceID = input.WorkspaceID
	input.Filter.IncludeSensitive = true
	input.Filter.BeforeOccurredAt, input.Filter.BeforeID = time.Time{}, ""
	input.Filter.Limit = 500
	filter, err := normalizeQueryInput(input.Filter)
	if err != nil {
		return Export{}, err
	}
	snapshot, err := json.Marshal(filter)
	if err != nil {
		return Export{}, ErrInvalid
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Export{}, err
	}
	defer tx.Rollback()
	value, err := scanExport(tx.QueryRowContext(ctx, `
		INSERT INTO audit_exports(id,workspace_id,filter_snapshot,requested_by,expires_at)
		VALUES($1,$2,$3,$4,$5)
		RETURNING id,workspace_id,filter_snapshot,status,object_id,requested_by,
			requested_at,completed_at,expires_at,error_code
	`, input.ID, input.WorkspaceID, snapshot, input.RequestedBy, input.ExpiresAt.UTC()))
	if err != nil {
		return Export{}, mapExportWrite("create audit export", err)
	}
	eventID, err := newAuditEventID()
	if err != nil {
		return Export{}, err
	}
	if _, err := service.recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: eventID, WorkspaceID: input.WorkspaceID,
		ActorType: "USER", ActorID: input.RequestedBy, ActorDisplay: "Audit administrator",
		Action: ActionAuditExportRequested, ResourceType: "AUDIT_EXPORT", ResourceID: input.ID,
		Result: "SUCCESS", Metadata: map[string]any{"expiresAt": input.ExpiresAt.UTC()},
	}); err != nil {
		return Export{}, err
	}
	if err := tx.Commit(); err != nil {
		return Export{}, mapExportWrite("commit audit export request", err)
	}
	return value, nil
}

func (service *ExportService) Get(
	ctx context.Context,
	workspaceID, exportID string,
	role workspace.Role,
) (Export, error) {
	if !canManageAudit(role) {
		return Export{}, authz.ErrDenied
	}
	if !validAuditUUID(strings.TrimSpace(workspaceID)) || !validAuditUUID(strings.TrimSpace(exportID)) {
		return Export{}, ErrInvalid
	}
	value, err := scanExport(service.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,filter_snapshot,status,object_id,requested_by,
			requested_at,completed_at,expires_at,error_code
		FROM audit_exports WHERE workspace_id=$1 AND id=$2
	`, workspaceID, exportID))
	if errors.Is(err, sql.ErrNoRows) {
		return Export{}, ErrNotFound
	}
	if err != nil {
		return Export{}, fmt.Errorf("get audit export: %w", err)
	}
	return value, nil
}

// ProcessNext claims a pending export in a short SKIP LOCKED transaction,
// performs query and object I/O without holding database locks, then records
// completion in another short transaction.
func (service *ExportService) ProcessNext(ctx context.Context) (bool, error) {
	value, claimed, err := service.claimNext(ctx)
	if err != nil || !claimed {
		return claimed, err
	}
	var filter QueryInput
	if err := json.Unmarshal(value.FilterSnapshot, &filter); err != nil {
		return true, service.fail(ctx, value, "AUDIT_EXPORT_FILTER_INVALID")
	}
	events, err := service.queryAll(ctx, filter)
	if err != nil {
		return true, errors.Join(err, service.fail(ctx, value, "AUDIT_EXPORT_QUERY_FAILED"))
	}
	content, err := encodeAuditExport(events)
	if err != nil {
		return true, errors.Join(err, service.fail(ctx, value, "AUDIT_EXPORT_ENCODING_FAILED"))
	}
	digest := sha256.Sum256(content)
	created, err := service.objects.Put(ctx, storedobject.PutInput{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Kind: storedobject.KindAuditExport,
		ContentType: "application/x-ndjson", SizeBytes: int64(len(content)),
		SHA256: hex.EncodeToString(digest[:]), Classification: storedobject.ClassificationInternal,
		RetentionMode: storedobject.RetentionExpiring, RetentionUntil: &value.ExpiresAt,
		CreatedByType: storedobject.CreatorUser, CreatedByID: value.RequestedBy,
		Reader: bytes.NewReader(content),
	})
	if err != nil {
		return true, errors.Join(err, service.fail(ctx, value, "AUDIT_EXPORT_STORAGE_FAILED"))
	}
	if created.ID != value.ID {
		return true, service.fail(ctx, value, "AUDIT_EXPORT_STORAGE_CONFLICT")
	}
	return true, service.complete(ctx, value, len(events))
}

func (service *ExportService) DownloadURL(
	ctx context.Context,
	workspaceID, exportID, actorID string,
	role workspace.Role,
	ttl time.Duration,
) (*url.URL, error) {
	value, err := service.Get(ctx, workspaceID, exportID, role)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	if value.Status != "SUCCEEDED" || value.ObjectID == "" || !value.ExpiresAt.After(now) ||
		!validAuditUUID(actorID) || ttl < time.Second || ttl > 15*time.Minute {
		return nil, ErrInvalid
	}
	if remaining := value.ExpiresAt.Sub(now); ttl > remaining {
		ttl = remaining
	}
	if ttl < time.Second {
		return nil, ErrInvalid
	}
	return service.objects.PresignDownload(ctx, storedobject.ReadRequest{
		WorkspaceID: value.WorkspaceID, ObjectID: value.ObjectID,
		ActorType: storedobject.CreatorUser, ActorID: actorID,
	}, ttl)
}

func (service *ExportService) claimNext(ctx context.Context) (Export, bool, error) {
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Export{}, false, err
	}
	defer tx.Rollback()
	value, err := scanExport(tx.QueryRowContext(ctx, `
		SELECT id,workspace_id,filter_snapshot,status,object_id,requested_by,
			requested_at,completed_at,expires_at,error_code
		FROM audit_exports WHERE status='PENDING' AND expires_at > clock_timestamp()
		ORDER BY requested_at,id FOR UPDATE SKIP LOCKED LIMIT 1
	`))
	if errors.Is(err, sql.ErrNoRows) {
		return Export{}, false, nil
	}
	if err != nil {
		return Export{}, false, fmt.Errorf("claim audit export: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE audit_exports SET status='RUNNING' WHERE id=$1`, value.ID); err != nil {
		return Export{}, false, mapExportWrite("mark audit export running", err)
	}
	if err := tx.Commit(); err != nil {
		return Export{}, false, mapExportWrite("commit audit export claim", err)
	}
	value.Status = "RUNNING"
	return value, true, nil
}

func (service *ExportService) queryAll(ctx context.Context, filter QueryInput) ([]Event, error) {
	filter.IncludeSensitive, filter.Limit = true, 500
	filter.BeforeOccurredAt, filter.BeforeID = time.Time{}, ""
	values := make([]Event, 0)
	for {
		page, err := service.queries.Query(ctx, workspace.RoleOwner, filter)
		if err != nil {
			return nil, err
		}
		values = append(values, page...)
		if len(page) < filter.Limit {
			return values, nil
		}
		last := page[len(page)-1]
		filter.BeforeOccurredAt, filter.BeforeID = last.OccurredAt, last.ID
	}
}

func encodeAuditExport(events []Event) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func (service *ExportService) complete(ctx context.Context, value Export, count int) error {
	return service.finish(ctx, value, "SUCCEEDED", "", ActionAuditExportCompleted, "SUCCESS", map[string]any{
		"eventCount": count, "objectId": value.ID,
	})
}

func (service *ExportService) fail(ctx context.Context, value Export, code string) error {
	return service.finish(ctx, value, "FAILED", code, ActionAuditExportFailed, "FAILURE", map[string]any{
		"errorCode": code,
	})
}

func (service *ExportService) finish(
	ctx context.Context,
	value Export,
	status, errorCode, action, result string,
	metadata map[string]any,
) error {
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var objectID any
	if status == "SUCCEEDED" {
		objectID = value.ID
	}
	resultSQL, err := tx.ExecContext(ctx, `UPDATE audit_exports
		SET status=$3,object_id=$4,completed_at=clock_timestamp(),error_code=$5
		WHERE workspace_id=$1 AND id=$2 AND status='RUNNING'`,
		value.WorkspaceID, value.ID, status, objectID, nullableAuditString(errorCode))
	if err != nil {
		return mapExportWrite("finish audit export", err)
	}
	if rows, _ := resultSQL.RowsAffected(); rows != 1 {
		return ErrConflict
	}
	eventID, err := newAuditEventID()
	if err != nil {
		return err
	}
	if _, err := service.recorder.RecordInTransaction(ctx, tx, ManagementEventInput{
		EventID: eventID, WorkspaceID: value.WorkspaceID,
		ActorType: "SYSTEM", ActorDisplay: "Audit export worker",
		Action: action, ResourceType: "AUDIT_EXPORT", ResourceID: value.ID,
		Result: result, Metadata: metadata,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

type exportScanner interface{ Scan(...any) error }

func scanExport(scanner exportScanner) (Export, error) {
	var value Export
	var objectID, errorCode sql.NullString
	var completedAt sql.NullTime
	var snapshot []byte
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &snapshot, &value.Status, &objectID,
		&value.RequestedBy, &value.RequestedAt, &completedAt, &value.ExpiresAt, &errorCode,
	)
	if err != nil {
		return Export{}, err
	}
	value.FilterSnapshot = append([]byte(nil), snapshot...)
	value.ObjectID, value.ErrorCode = objectID.String, errorCode.String
	value.RequestedAt, value.ExpiresAt = value.RequestedAt.UTC(), value.ExpiresAt.UTC()
	if completedAt.Valid {
		completed := completedAt.Time.UTC()
		value.CompletedAt = &completed
	}
	return value, nil
}

func mapExportWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	return mapAuditWrite(operation, err)
}
