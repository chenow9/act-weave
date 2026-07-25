package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrNoPassingTest  = errors.New("tool version has no current passing test")
	stableCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
)

const testRecordColumns = `
	t.id,t.workspace_id,t.tool_version_id,t.status,t.connectivity_passed,
	t.response_schema_passed,t.error_mapping_passed,t.runtime_policy_passed,
	t.request_summary,t.response_summary,t.latency_ms,t.error_code,t.raw_object_id,
	t.tested_by,t.tested_at
`

func (repository *Repository) RecordTest(ctx context.Context, input RecordTestInput) (TestRecord, error) {
	input = normalizeRecordTest(input)
	if !validRecordTest(input) {
		return TestRecord{}, ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return TestRecord{}, fmt.Errorf("begin record tool test transaction: %w", err)
	}
	defer tx.Rollback()
	var checksum, lifecycleStatus string
	var lockVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT checksum::TEXT,lifecycle_status,lock_version
		FROM tool_versions
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, input.WorkspaceID, input.ToolVersionID).Scan(&checksum, &lifecycleStatus, &lockVersion); errors.Is(err, sql.ErrNoRows) {
		return TestRecord{}, ErrNotFound
	} else if err != nil {
		return TestRecord{}, fmt.Errorf("lock tested tool version: %w", err)
	}
	if lifecycleStatus == "PUBLISHED" {
		return TestRecord{}, ErrImmutable
	}
	if checksum != input.VersionChecksum || lockVersion != input.ExpectedVersionLock {
		return TestRecord{}, ErrConflict
	}
	requestSummary, err := summaryWithChecksum(input.RequestSummary, input.VersionChecksum)
	if err != nil {
		return TestRecord{}, ErrInvalid
	}
	record, err := scanTestRecord(tx.QueryRowContext(ctx, `
		INSERT INTO tool_tests AS t(
			id,workspace_id,tool_version_id,status,connectivity_passed,
			response_schema_passed,error_mapping_passed,runtime_policy_passed,
			request_summary,response_summary,latency_ms,error_code,raw_object_id,tested_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING `+testRecordColumns,
		input.ID, input.WorkspaceID, input.ToolVersionID, input.Status,
		input.ConnectivityPassed, input.ResponseSchemaPassed, input.ErrorMappingPassed,
		input.RuntimePolicyPassed, []byte(requestSummary), []byte(input.ResponseSummary),
		input.LatencyMS, input.ErrorCode, input.RawObjectID, input.TestedBy))
	if err != nil {
		return TestRecord{}, mapWrite("record tool test", err)
	}
	if input.Status == "SUCCEEDED" {
		result, err := tx.ExecContext(ctx, `
			UPDATE tool_versions
			SET lifecycle_status='TESTED',updated_by=$3,updated_at=clock_timestamp(),
				lock_version=lock_version+1
			WHERE workspace_id=$1 AND id=$2 AND lock_version=$4 AND checksum=$5
		`, input.WorkspaceID, input.ToolVersionID, input.TestedBy,
			input.ExpectedVersionLock, input.VersionChecksum)
		if err != nil {
			return TestRecord{}, mapWrite("mark tool version tested", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return TestRecord{}, ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return TestRecord{}, mapWrite("commit tool test", err)
	}
	return record, nil
}

// LatestSuccessfulTest only returns a passing latest attempt for the current
// version checksum. A later failure or Draft edit invalidates older success.
func (repository *Repository) LatestSuccessfulTest(ctx context.Context, workspaceID, versionID string) (TestRecord, error) {
	if !validUUID(workspaceID) || !validUUID(versionID) {
		return TestRecord{}, ErrInvalid
	}
	record, err := scanTestRecord(repository.db.QueryRowContext(ctx, `
		SELECT `+testRecordColumns+`
		FROM (
			SELECT * FROM tool_tests
			WHERE workspace_id=$1 AND tool_version_id=$2
			ORDER BY tested_at DESC,id DESC
			LIMIT 1
		) t
		JOIN tool_versions v ON v.workspace_id=t.workspace_id AND v.id=t.tool_version_id
		WHERE t.status='SUCCEEDED' AND t.connectivity_passed AND t.response_schema_passed
		  AND t.error_mapping_passed AND t.runtime_policy_passed
		  AND t.request_summary->>'versionChecksum'=v.checksum::TEXT
	`, workspaceID, versionID))
	if errors.Is(err, sql.ErrNoRows) {
		return TestRecord{}, ErrNoPassingTest
	}
	if err != nil {
		return TestRecord{}, fmt.Errorf("read latest tool test: %w", err)
	}
	return record, nil
}

func scanTestRecord(row rowScanner) (TestRecord, error) {
	var value TestRecord
	var requestSummary, responseSummary []byte
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.ToolVersionID, &value.Status,
		&value.ConnectivityPassed, &value.ResponseSchemaPassed, &value.ErrorMappingPassed,
		&value.RuntimePolicyPassed, &requestSummary, &responseSummary, &value.LatencyMS,
		&value.ErrorCode, &value.RawObjectID, &value.TestedBy, &value.TestedAt)
	value.RequestSummary = append(json.RawMessage(nil), requestSummary...)
	value.ResponseSummary = append(json.RawMessage(nil), responseSummary...)
	return value, err
}

func normalizeRecordTest(input RecordTestInput) RecordTestInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.ToolVersionID, input.VersionChecksum = strings.TrimSpace(input.ToolVersionID), strings.TrimSpace(input.VersionChecksum)
	input.Status, input.TestedBy = strings.TrimSpace(input.Status), strings.TrimSpace(input.TestedBy)
	input.ErrorCode, input.RawObjectID = optionalTextPointer(input.ErrorCode), optionalID(input.RawObjectID)
	input.RequestSummary = normalizeJSONObject(input.RequestSummary)
	input.ResponseSummary = normalizeJSONObject(input.ResponseSummary)
	return input
}

func validRecordTest(input RecordTestInput) bool {
	allPassed := input.ConnectivityPassed && input.ResponseSchemaPassed &&
		input.ErrorMappingPassed && input.RuntimePolicyPassed
	return validUUID(input.ID) && validUUID(input.WorkspaceID) && validUUID(input.ToolVersionID) &&
		validUUID(input.TestedBy) && len(input.VersionChecksum) == 64 &&
		stableLowerHex(input.VersionChecksum) && input.ExpectedVersionLock > 0 &&
		validJSONObject(input.RequestSummary) && validJSONObject(input.ResponseSummary) &&
		(input.LatencyMS == nil || *input.LatencyMS >= 0) && validOptionalID(input.RawObjectID) &&
		((input.Status == "SUCCEEDED" && allPassed && input.ErrorCode == nil) ||
			(input.Status == "FAILED" && input.ErrorCode != nil && stableCodePattern.MatchString(*input.ErrorCode)))
}

func summaryWithChecksum(summary json.RawMessage, checksum string) (json.RawMessage, error) {
	var object map[string]any
	if err := json.Unmarshal(summary, &object); err != nil || object == nil {
		return nil, ErrInvalid
	}
	object["versionChecksum"] = checksum
	return json.Marshal(object)
}

func optionalTextPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func stableLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
