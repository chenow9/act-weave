package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
)

var callableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type PublishAuthorizer interface {
	AuthorizeWorkspace(context.Context, string, string, authz.Action) (authz.WorkspaceContext, error)
}

type ToolReleasePublishedEvent struct {
	ID            string
	Type          string
	WorkspaceID   string
	CapabilityID  string
	ToolVersionID string
	ToolTestID    string
	ReleaseID     string
	ReleaseNo     int
	Checksum      string
	PublishedBy   string
	OccurredAt    time.Time
	SchemaVersion int
}

type PublishEventWriter interface {
	AppendToolReleasePublished(context.Context, *sql.Tx, ToolReleasePublishedEvent) error
}

type PublishToolInput struct {
	ReleaseID           string
	EventID             string
	WorkspaceID         string
	CapabilityID        string
	VersionID           string
	CallableName        string
	CallableDescription string
	PublishedBy         string
	ExpectedVersionLock int64
}

type PublishToolResult struct {
	Release capability.Release
	Version Version
	Test    TestRecord
	Event   ToolReleasePublishedEvent
}

type PublishService struct {
	repository *Repository
	authorizer PublishAuthorizer
	events     PublishEventWriter
}

func NewPublishService(repository *Repository, authorizer PublishAuthorizer, events PublishEventWriter) (*PublishService, error) {
	if repository == nil || authorizer == nil || events == nil {
		return nil, errors.New("tool publish service dependencies are required")
	}
	return &PublishService{repository: repository, authorizer: authorizer, events: events}, nil
}

func (service *PublishService) Publish(ctx context.Context, input PublishToolInput) (PublishToolResult, error) {
	input = normalizePublishTool(input)
	if !validPublishTool(input) {
		return PublishToolResult{}, ErrInvalid
	}
	if _, err := service.authorizer.AuthorizeWorkspace(ctx, input.PublishedBy, input.WorkspaceID, authz.ActionPublish); err != nil {
		return PublishToolResult{}, err
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishToolResult{}, fmt.Errorf("begin publish tool transaction: %w", err)
	}
	defer tx.Rollback()
	var capabilityStatus, capabilityKind string
	if err := tx.QueryRowContext(ctx, `
		SELECT kind,status FROM capabilities
		WHERE workspace_id=$1 AND id=$2 AND deleted_at IS NULL
		FOR UPDATE
	`, input.WorkspaceID, input.CapabilityID).Scan(&capabilityKind, &capabilityStatus); errors.Is(err, sql.ErrNoRows) {
		return PublishToolResult{}, ErrNotFound
	} else if err != nil {
		return PublishToolResult{}, fmt.Errorf("lock tool capability for publish: %w", err)
	}
	if capabilityKind != "TOOL" || capabilityStatus != "ACTIVE" {
		return PublishToolResult{}, ErrInvalid
	}
	version, err := scanVersion(tx.QueryRowContext(ctx, `
		SELECT `+versionColumns+` FROM tool_versions v
		WHERE v.workspace_id=$1 AND v.capability_id=$2 AND v.id=$3
		FOR UPDATE
	`, input.WorkspaceID, input.CapabilityID, input.VersionID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishToolResult{}, ErrNotFound
	}
	if err != nil {
		return PublishToolResult{}, fmt.Errorf("lock tool version for publish: %w", err)
	}
	if version.LifecycleStatus == "PUBLISHED" {
		return PublishToolResult{}, ErrImmutable
	}
	if version.LifecycleStatus != "TESTED" || version.LockVersion != input.ExpectedVersionLock {
		return PublishToolResult{}, ErrConflict
	}
	passingTest, err := latestPassingTestForPublish(ctx, tx, input.WorkspaceID, version.ID, version.Checksum)
	if err != nil {
		return PublishToolResult{}, err
	}
	var releaseNo int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(release_no),0)+1 FROM capability_releases
		WHERE workspace_id=$1 AND capability_id=$2
	`, input.WorkspaceID, input.CapabilityID).Scan(&releaseNo); err != nil {
		return PublishToolResult{}, fmt.Errorf("allocate tool release number: %w", err)
	}
	release, err := scanPublishedRelease(tx.QueryRowContext(ctx, `
		INSERT INTO capability_releases AS r(
			id,workspace_id,capability_id,release_no,source_type,source_id,
			callable_name,callable_description,input_schema,output_schema,risk_level,
			side_effect_level,requires_confirmation,checksum,published_by
		) VALUES($1,$2,$3,$4,'TOOL_VERSION',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING
			r.id,r.workspace_id,r.capability_id,r.release_no,r.source_type,r.source_id,
			r.callable_name,r.callable_description,r.input_schema,r.output_schema,r.risk_level,
			r.side_effect_level,r.requires_confirmation,r.checksum,r.published_by,r.published_at,r.retired_at
	`, input.ReleaseID, input.WorkspaceID, input.CapabilityID, releaseNo, version.ID,
		input.CallableName, input.CallableDescription, []byte(version.InputSchema),
		[]byte(version.OutputSchema), version.RiskLevel, version.SideEffectLevel,
		version.RequiresConfirmation, version.Checksum, input.PublishedBy))
	if err != nil {
		return PublishToolResult{}, mapWrite("create tool capability release", err)
	}
	publishedVersion, err := scanVersion(tx.QueryRowContext(ctx, `
		UPDATE tool_versions v
		SET lifecycle_status='PUBLISHED',published_at=clock_timestamp(),updated_by=$4,
			updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3
		  AND lifecycle_status='TESTED' AND lock_version=$5 AND checksum=$6
		RETURNING `+versionColumns,
		input.WorkspaceID, input.CapabilityID, version.ID, input.PublishedBy,
		input.ExpectedVersionLock, version.Checksum))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishToolResult{}, ErrConflict
	}
	if err != nil {
		return PublishToolResult{}, mapWrite("publish tool version", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE capabilities
		SET active_release_id=$3,updated_by=$4,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='TOOL' AND status='ACTIVE' AND deleted_at IS NULL
	`, input.WorkspaceID, input.CapabilityID, release.ID, input.PublishedBy)
	if err != nil {
		return PublishToolResult{}, mapWrite("activate tool capability release", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return PublishToolResult{}, ErrConflict
	}
	event := ToolReleasePublishedEvent{
		ID: input.EventID, Type: "tool.release.published", WorkspaceID: input.WorkspaceID,
		CapabilityID: input.CapabilityID, ToolVersionID: version.ID, ToolTestID: passingTest.ID,
		ReleaseID: release.ID, ReleaseNo: release.ReleaseNo, Checksum: release.Checksum,
		PublishedBy: input.PublishedBy, OccurredAt: release.PublishedAt, SchemaVersion: 1,
	}
	if err := service.events.AppendToolReleasePublished(ctx, tx, event); err != nil {
		return PublishToolResult{}, fmt.Errorf("append tool release event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PublishToolResult{}, mapWrite("commit tool publish transaction", err)
	}
	return PublishToolResult{Release: release, Version: publishedVersion, Test: passingTest, Event: event}, nil
}

func latestPassingTestForPublish(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, versionID, checksum string,
) (TestRecord, error) {
	record, err := scanTestRecord(tx.QueryRowContext(ctx, `
		SELECT `+testRecordColumns+`
		FROM (
			SELECT * FROM tool_tests
			WHERE workspace_id=$1 AND tool_version_id=$2
			ORDER BY tested_at DESC,id DESC
			LIMIT 1
		) t
		WHERE t.status='SUCCEEDED' AND t.connectivity_passed AND t.response_schema_passed
		  AND t.error_mapping_passed AND t.runtime_policy_passed
		  AND t.request_summary->>'versionChecksum'=$3
	`, workspaceID, versionID, checksum))
	if errors.Is(err, sql.ErrNoRows) {
		return TestRecord{}, ErrNoPassingTest
	}
	if err != nil {
		return TestRecord{}, fmt.Errorf("validate tool publish test: %w", err)
	}
	return record, nil
}

func scanPublishedRelease(row rowScanner) (capability.Release, error) {
	var value capability.Release
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

func normalizePublishTool(input PublishToolInput) PublishToolInput {
	input.ReleaseID, input.EventID = strings.TrimSpace(input.ReleaseID), strings.TrimSpace(input.EventID)
	input.WorkspaceID, input.CapabilityID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.CapabilityID)
	input.VersionID, input.PublishedBy = strings.TrimSpace(input.VersionID), strings.TrimSpace(input.PublishedBy)
	input.CallableName = strings.TrimSpace(input.CallableName)
	input.CallableDescription = strings.TrimSpace(input.CallableDescription)
	return input
}

func validPublishTool(input PublishToolInput) bool {
	return validUUID(input.ReleaseID) && validUUID(input.EventID) && validUUID(input.WorkspaceID) &&
		validUUID(input.CapabilityID) && validUUID(input.VersionID) && validUUID(input.PublishedBy) &&
		callableNamePattern.MatchString(input.CallableName) && input.ExpectedVersionLock > 0
}
