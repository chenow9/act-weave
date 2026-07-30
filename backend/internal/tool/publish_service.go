package tool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
)

var (
	callableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// ErrForcePublishDisabled is returned when tools.allowForcePublish is off.
	ErrForcePublishDisabled = errors.New("tool force publish is disabled")
	// ErrForceReasonRequired is returned when force-publish reason is missing/too short.
	ErrForceReasonRequired = errors.New("tool force publish requires a reason of at least 8 characters")
)

const (
	forcePublishReasonMinRunes = 8
	forcePublishReasonMaxRunes = 500
)

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
	// Force is true when publish skipped live tool tests (platform-admin escape hatch).
	Force bool
	// ForceReason is the operator-supplied justification; only set when Force is true.
	ForceReason string
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

// ForcePublishToolInput publishes without a live invoke test. Caller (HTTP) must
// gate PLATFORM_ADMIN; service gates config + workspace PUBLISH + reason.
type ForcePublishToolInput struct {
	PublishToolInput
	// TestID is the synthetic SUCCEEDED tool_tests row id (attest record).
	TestID string
	// Reason is a mandatory operator justification (min 8 runes).
	Reason string
}

type PublishToolResult struct {
	Release capability.Release
	Version Version
	Test    TestRecord
	Event   ToolReleasePublishedEvent
}

type PublishService struct {
	repository        *Repository
	authorizer        PublishAuthorizer
	events            PublishEventWriter
	allowForcePublish bool
}

func NewPublishService(repository *Repository, authorizer PublishAuthorizer, events PublishEventWriter) (*PublishService, error) {
	if repository == nil || authorizer == nil || events == nil {
		return nil, errors.New("tool publish service dependencies are required")
	}
	return &PublishService{repository: repository, authorizer: authorizer, events: events}, nil
}

// AllowForcePublish enables the platform-admin force-publish escape hatch.
// Default is false (safe for production). Restart-bound via application wiring.
func (service *PublishService) AllowForcePublish(enabled bool) *PublishService {
	if service != nil {
		service.allowForcePublish = enabled
	}
	return service
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

// ForcePublish creates a synthetic SUCCEEDED test attestation (no live invoke),
// advances the version to TESTED when needed, then freezes a capability release.
// It still requires workspace PUBLISH; PLATFORM_ADMIN is enforced at the HTTP edge.
func (service *PublishService) ForcePublish(ctx context.Context, input ForcePublishToolInput) (PublishToolResult, error) {
	if service == nil || !service.allowForcePublish {
		return PublishToolResult{}, ErrForcePublishDisabled
	}
	input.PublishToolInput = normalizePublishTool(input.PublishToolInput)
	input.TestID = strings.TrimSpace(input.TestID)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validPublishTool(input.PublishToolInput) || !validUUID(input.TestID) {
		return PublishToolResult{}, ErrInvalid
	}
	reasonRunes := utf8.RuneCountInString(input.Reason)
	if reasonRunes < forcePublishReasonMinRunes || reasonRunes > forcePublishReasonMaxRunes {
		return PublishToolResult{}, ErrForceReasonRequired
	}
	if _, err := service.authorizer.AuthorizeWorkspace(ctx, input.PublishedBy, input.WorkspaceID, authz.ActionPublish); err != nil {
		return PublishToolResult{}, err
	}
	tx, err := service.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishToolResult{}, fmt.Errorf("begin force publish tool transaction: %w", err)
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
		return PublishToolResult{}, fmt.Errorf("lock tool capability for force publish: %w", err)
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
		return PublishToolResult{}, fmt.Errorf("lock tool version for force publish: %w", err)
	}
	if version.LifecycleStatus == "PUBLISHED" {
		return PublishToolResult{}, ErrImmutable
	}
	if version.LockVersion != input.ExpectedVersionLock {
		return PublishToolResult{}, ErrConflict
	}
	// Synthetic attest test: marks all publish gates true without invoking upstream.
	// tool_tests.raw_object_id is NOT NULL and must reference permanent TOOL_TEST_PAYLOAD.
	requestSummary, err := summaryWithChecksum(mustJSON(map[string]any{
		"forcePublish": true,
		"reason":       input.Reason,
		"mode":         "FORCE_ATTEST",
	}), version.Checksum)
	if err != nil {
		return PublishToolResult{}, ErrInvalid
	}
	responseSummary := mustJSON(map[string]any{
		"forcePublish": true,
		"attested":     true,
	})
	// Reuse TestID as payload object id (same pattern as live tool test artifacts).
	attestDigest := sha256Hex(append(append([]byte(nil), requestSummary...), responseSummary...))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stored_objects(
			id,workspace_id,bucket,object_key,kind,content_type,size_bytes,sha256,
			encryption_key_id,classification,retention_mode,created_by_type,created_by_id
		) VALUES (
			$1,$2,'tool-test-payloads',$3,'TOOL_TEST_PAYLOAD','application/json',$4,$5,
			'force-publish-attest','SENSITIVE','PERMANENT','USER',$6
		)
	`, input.TestID, input.WorkspaceID, "force-attest/"+input.TestID,
		len(requestSummary)+len(responseSummary), attestDigest, input.PublishedBy); err != nil {
		return PublishToolResult{}, mapWrite("create force publish attest payload", err)
	}
	attestTest, err := scanTestRecord(tx.QueryRowContext(ctx, `
		INSERT INTO tool_tests AS t(
			id,workspace_id,tool_version_id,status,connectivity_passed,
			response_schema_passed,error_mapping_passed,runtime_policy_passed,
			request_summary,response_summary,latency_ms,error_code,raw_object_id,tested_by
		) VALUES($1,$2,$3,'SUCCEEDED',TRUE,TRUE,TRUE,TRUE,$4::jsonb,$5::jsonb,0,NULL,$1,$6)
		RETURNING `+testRecordColumns,
		input.TestID, input.WorkspaceID, version.ID,
		string(requestSummary), string(responseSummary), input.PublishedBy))
	if err != nil {
		return PublishToolResult{}, mapWrite("record force publish attest test", err)
	}
	// SUCCEEDED test always advances lifecycle to TESTED and bumps lock (same as live test).
	testedVersion, err := scanVersion(tx.QueryRowContext(ctx, `
		UPDATE tool_versions v
		SET lifecycle_status='TESTED',updated_by=$4,updated_at=clock_timestamp(),
			lock_version=lock_version+1
		WHERE workspace_id=$1 AND capability_id=$2 AND id=$3
		  AND lifecycle_status<>'PUBLISHED' AND lock_version=$5 AND checksum=$6
		RETURNING `+versionColumns,
		input.WorkspaceID, input.CapabilityID, version.ID, input.PublishedBy,
		input.ExpectedVersionLock, version.Checksum))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishToolResult{}, ErrConflict
	}
	if err != nil {
		return PublishToolResult{}, mapWrite("mark tool version tested for force publish", err)
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
	`, input.ReleaseID, input.WorkspaceID, input.CapabilityID, releaseNo, testedVersion.ID,
		input.CallableName, input.CallableDescription, []byte(testedVersion.InputSchema),
		[]byte(testedVersion.OutputSchema), testedVersion.RiskLevel, testedVersion.SideEffectLevel,
		testedVersion.RequiresConfirmation, testedVersion.Checksum, input.PublishedBy))
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
		input.WorkspaceID, input.CapabilityID, testedVersion.ID, input.PublishedBy,
		testedVersion.LockVersion, testedVersion.Checksum))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishToolResult{}, ErrConflict
	}
	if err != nil {
		return PublishToolResult{}, mapWrite("force publish tool version", err)
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
		CapabilityID: input.CapabilityID, ToolVersionID: testedVersion.ID, ToolTestID: attestTest.ID,
		ReleaseID: release.ID, ReleaseNo: release.ReleaseNo, Checksum: release.Checksum,
		PublishedBy: input.PublishedBy, OccurredAt: release.PublishedAt, SchemaVersion: 1,
		Force: true, ForceReason: input.Reason,
	}
	if err := service.events.AppendToolReleasePublished(ctx, tx, event); err != nil {
		return PublishToolResult{}, fmt.Errorf("append tool force publish event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PublishToolResult{}, mapWrite("commit tool force publish transaction", err)
	}
	return PublishToolResult{Release: release, Version: publishedVersion, Test: attestTest, Event: event}, nil
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

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
