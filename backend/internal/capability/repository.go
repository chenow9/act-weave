package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrNotFound         = errors.New("capability not found")
	ErrConflict         = errors.New("capability conflict")
	ErrInvalid          = errors.New("invalid capability")
	ErrUnavailable      = errors.New("capability unavailable")
	ErrCallableConflict = errors.New("capability callable name conflict")
)

const capabilityColumns = `
	c.id,c.workspace_id,c.kind,c.name::TEXT,c.slug::TEXT,c.description,c.status,
	c.active_release_id,c.created_by,c.updated_by,c.created_at,c.updated_at,c.lock_version,c.deleted_at
`

const releaseColumns = `
	r.id,r.workspace_id,r.capability_id,r.release_no,r.source_type,r.source_id,
	r.callable_name,r.callable_description,r.input_schema,r.output_schema,r.risk_level,
	r.side_effect_level,r.requires_confirmation,r.checksum,r.published_by,r.published_at,r.retired_at
`

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("capability repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, input NewCapability) (Capability, error) {
	input = normalizeCapability(input)
	if !validNewCapability(input) {
		return Capability{}, ErrInvalid
	}
	value, err := scanCapability(r.db.QueryRowContext(ctx, `
		INSERT INTO capabilities AS c(id,workspace_id,kind,name,slug,description,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$7)
		RETURNING `+capabilityColumns,
		input.ID, input.WorkspaceID, input.Kind, input.Name, input.Slug, input.Description, input.CreatedBy))
	if err != nil {
		return Capability{}, mapWrite("create capability", err)
	}
	return value, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID, capabilityID string) (Capability, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) {
		return Capability{}, ErrInvalid
	}
	value, err := scanCapability(r.db.QueryRowContext(ctx, `
		SELECT `+capabilityColumns+` FROM capabilities c
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
	`, workspaceID, capabilityID))
	return value, mapRead("get capability", err)
}

func (r *Repository) ListCatalog(ctx context.Context, workspaceID string) ([]CatalogItem, error) {
	if !validUUID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+capabilityColumns+`,
			COUNT(DISTINCT b.agent_id) FILTER (WHERE b.enabled),
			r.id,r.callable_name,r.callable_description,r.input_schema,r.output_schema,
			r.risk_level,r.side_effect_level,r.requires_confirmation
		FROM capabilities c
		LEFT JOIN agent_capability_bindings b
		  ON b.workspace_id=c.workspace_id AND b.capability_id=c.id
		LEFT JOIN capability_releases r
		  ON r.workspace_id=c.workspace_id AND r.capability_id=c.id AND r.id=c.active_release_id
		WHERE c.workspace_id=$1 AND c.deleted_at IS NULL
		GROUP BY c.id,r.id ORDER BY c.name,c.id
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list capability catalog: %w", err)
	}
	defer rows.Close()
	items := make([]CatalogItem, 0)
	for rows.Next() {
		item, err := scanCatalogItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan capability catalog: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Publish(ctx context.Context, input PublishRelease) (Capability, Release, error) {
	input = normalizeRelease(input)
	if !validPublishRelease(input) {
		return Capability{}, Release{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Capability{}, Release{}, fmt.Errorf("begin publish capability transaction: %w", err)
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM capabilities
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE
	`, input.WorkspaceID, input.CapabilityID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Capability{}, Release{}, ErrNotFound
	}
	if err != nil {
		return Capability{}, Release{}, fmt.Errorf("lock capability for publish: %w", err)
	}
	if status != "ACTIVE" {
		return Capability{}, Release{}, ErrUnavailable
	}
	var releaseNo int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(release_no),0)+1 FROM capability_releases
		WHERE workspace_id=$1 AND capability_id=$2
	`, input.WorkspaceID, input.CapabilityID).Scan(&releaseNo); err != nil {
		return Capability{}, Release{}, fmt.Errorf("allocate capability release number: %w", err)
	}
	release, err := scanRelease(tx.QueryRowContext(ctx, `
		INSERT INTO capability_releases AS r(
			id,workspace_id,capability_id,release_no,source_type,source_id,
			callable_name,callable_description,input_schema,output_schema,risk_level,
			side_effect_level,requires_confirmation,checksum,published_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING `+releaseColumns,
		input.ID, input.WorkspaceID, input.CapabilityID, releaseNo, input.SourceType,
		input.SourceID, input.CallableName, input.CallableDescription, []byte(input.InputSchema),
		[]byte(input.OutputSchema), input.RiskLevel, input.SideEffectLevel,
		input.RequiresConfirmation, input.Checksum, input.PublishedBy))
	if err != nil {
		return Capability{}, Release{}, mapWrite("publish capability release", err)
	}
	value, err := scanCapability(tx.QueryRowContext(ctx, `
		UPDATE capabilities c SET active_release_id=$3,updated_by=$4,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		RETURNING `+capabilityColumns,
		input.WorkspaceID, input.CapabilityID, input.ID, input.PublishedBy))
	if err != nil {
		return Capability{}, Release{}, mapWrite("activate capability release", err)
	}
	if err := tx.Commit(); err != nil {
		return Capability{}, Release{}, mapWrite("commit publish capability transaction", err)
	}
	return value, release, nil
}

// Resolve returns an immutable release snapshot. An empty releaseID selects
// the current active release; a non-empty value implements a pinned selection.
func (r *Repository) Resolve(ctx context.Context, workspaceID, capabilityID, releaseID string) (ResolvedCapability, error) {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || (releaseID != "" && !validUUID(releaseID)) {
		return ResolvedCapability{}, ErrInvalid
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT c.kind,c.status,c.deleted_at,`+releaseColumns+`
		FROM capabilities c
		JOIN capability_releases r
		  ON r.workspace_id=c.workspace_id AND r.capability_id=c.id
		 AND r.id=COALESCE(NULLIF($3::TEXT,'')::UUID,c.active_release_id)
		WHERE c.workspace_id=$1 AND c.id=$2
	`, workspaceID, capabilityID, releaseID)
	var kind, capabilityStatus string
	var deletedAt sql.NullTime
	release, err := scanReleaseWithPrefix(row, &kind, &capabilityStatus, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedCapability{}, ErrNotFound
	}
	if err != nil {
		return ResolvedCapability{}, fmt.Errorf("resolve capability release: %w", err)
	}
	if capabilityStatus != "ACTIVE" || deletedAt.Valid || release.RetiredAt != nil {
		return ResolvedCapability{}, ErrUnavailable
	}
	return resolvedFrom(kind, release), nil
}

func (r *Repository) Retire(ctx context.Context, workspaceID, capabilityID, releaseID string) error {
	if !validUUID(workspaceID) || !validUUID(capabilityID) || !validUUID(releaseID) {
		return ErrInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE capability_releases SET retired_at=clock_timestamp()
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3 AND retired_at IS NULL
	`, workspaceID, capabilityID, releaseID)
	if err != nil {
		return mapWrite("retire capability release", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read retired release count: %w", err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanCapability(row rowScanner) (Capability, error) {
	var value Capability
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.Kind, &value.Name, &value.Slug,
		&value.Description, &value.Status, &value.ActiveReleaseID, &value.CreatedBy,
		&value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt, &value.LockVersion, &value.DeletedAt)
	return value, err
}

func scanCatalogItem(row rowScanner) (CatalogItem, error) {
	var item CatalogItem
	var releaseID, callableName, callableDescription, riskLevel, sideEffectLevel sql.NullString
	var inputSchema, outputSchema []byte
	var requiresConfirmation sql.NullBool
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.Kind, &item.Name, &item.Slug,
		&item.Description, &item.Status, &item.ActiveReleaseID, &item.CreatedBy,
		&item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.LockVersion, &item.DeletedAt,
		&item.BoundAgentCount, &releaseID, &callableName, &callableDescription,
		&inputSchema, &outputSchema, &riskLevel, &sideEffectLevel, &requiresConfirmation)
	if err != nil {
		return CatalogItem{}, err
	}
	if releaseID.Valid {
		item.ActiveRelease = &Descriptor{CapabilityID: item.ID, ReleaseID: releaseID.String,
			Kind: item.Kind, CallableName: callableName.String, CallableDescription: callableDescription.String,
			InputSchema: append(json.RawMessage(nil), inputSchema...), OutputSchema: append(json.RawMessage(nil), outputSchema...),
			RiskLevel: riskLevel.String, SideEffectLevel: sideEffectLevel.String,
			RequiresConfirmation: requiresConfirmation.Bool}
	}
	return item, nil
}

func scanRelease(row rowScanner) (Release, error) {
	var value Release
	var inputSchema, outputSchema []byte
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.ReleaseNo,
		&value.SourceType, &value.SourceID, &value.CallableName, &value.CallableDescription,
		&inputSchema, &outputSchema, &value.RiskLevel, &value.SideEffectLevel,
		&value.RequiresConfirmation, &value.Checksum, &value.PublishedBy,
		&value.PublishedAt, &value.RetiredAt)
	value.InputSchema = append(json.RawMessage(nil), inputSchema...)
	value.OutputSchema = append(json.RawMessage(nil), outputSchema...)
	return value, err
}

func scanReleaseWithPrefix(row rowScanner, prefix ...any) (Release, error) {
	var value Release
	var inputSchema, outputSchema []byte
	destinations := append(prefix,
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.ReleaseNo,
		&value.SourceType, &value.SourceID, &value.CallableName, &value.CallableDescription,
		&inputSchema, &outputSchema, &value.RiskLevel, &value.SideEffectLevel,
		&value.RequiresConfirmation, &value.Checksum, &value.PublishedBy,
		&value.PublishedAt, &value.RetiredAt,
	)
	err := row.Scan(destinations...)
	value.InputSchema = append(json.RawMessage(nil), inputSchema...)
	value.OutputSchema = append(json.RawMessage(nil), outputSchema...)
	return value, err
}

func resolvedFrom(kind string, release Release) ResolvedCapability {
	return ResolvedCapability{
		Descriptor: Descriptor{
			CapabilityID: release.CapabilityID, ReleaseID: release.ID, Kind: kind,
			CallableName: release.CallableName, CallableDescription: release.CallableDescription,
			InputSchema:  append(json.RawMessage(nil), release.InputSchema...),
			OutputSchema: append(json.RawMessage(nil), release.OutputSchema...),
			RiskLevel:    release.RiskLevel, SideEffectLevel: release.SideEffectLevel,
			RequiresConfirmation: release.RequiresConfirmation,
		},
		WorkspaceID: release.WorkspaceID, SourceType: release.SourceType, SourceID: release.SourceID,
		Checksum: release.Checksum, ReleaseNo: release.ReleaseNo, PublishedAt: release.PublishedAt,
	}
}

func normalizeCapability(input NewCapability) NewCapability {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.Kind, input.Name, input.Slug = strings.TrimSpace(input.Kind), strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description, input.CreatedBy = strings.TrimSpace(input.Description), strings.TrimSpace(input.CreatedBy)
	return input
}

func validNewCapability(input NewCapability) bool {
	return validUUID(input.ID) && validUUID(input.WorkspaceID) && validUUID(input.CreatedBy) &&
		(input.Kind == "TOOL" || input.Kind == "WORKFLOW") && input.Name != "" && input.Slug != ""
}

func normalizeRelease(input PublishRelease) PublishRelease {
	input.ID, input.WorkspaceID, input.CapabilityID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.CapabilityID)
	input.SourceType, input.SourceID = strings.TrimSpace(input.SourceType), strings.TrimSpace(input.SourceID)
	input.CallableName, input.CallableDescription = strings.TrimSpace(input.CallableName), strings.TrimSpace(input.CallableDescription)
	input.RiskLevel, input.SideEffectLevel = strings.TrimSpace(input.RiskLevel), strings.TrimSpace(input.SideEffectLevel)
	input.Checksum, input.PublishedBy = strings.TrimSpace(input.Checksum), strings.TrimSpace(input.PublishedBy)
	return input
}

func validPublishRelease(input PublishRelease) bool {
	return validUUID(input.ID) && validUUID(input.WorkspaceID) && validUUID(input.CapabilityID) &&
		validUUID(input.SourceID) && validUUID(input.PublishedBy) &&
		(input.SourceType == "TOOL_VERSION" || input.SourceType == "WORKFLOW_REVISION") &&
		input.CallableName != "" && validJSONObject(input.InputSchema) && validJSONObject(input.OutputSchema) &&
		oneOf(input.RiskLevel, "LOW", "MEDIUM", "HIGH", "CRITICAL") &&
		oneOf(input.SideEffectLevel, "NONE", "READ", "WRITE", "IRREVERSIBLE") &&
		validChecksum(input.Checksum)
}

func validUUID(value string) bool { _, err := uuid.Parse(value); return err == nil }
func validJSONObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}
func validChecksum(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func mapRead(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func mapWrite(operation string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			if strings.Contains(strings.ToLower(pg.Message), "callable") {
				return fmt.Errorf("%s: %w", operation, ErrCallableConflict)
			}
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		}
		if pg.Code.Class() == "23" {
			return fmt.Errorf("%s: %w", operation, ErrInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
