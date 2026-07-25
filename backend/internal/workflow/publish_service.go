package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/domain"
)

var workflowCallableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type PublishAuthorizer interface {
	AuthorizeWorkspace(context.Context, string, string, authz.Action) (authz.WorkspaceContext, error)
}

type WorkflowReleasePublishedEvent struct {
	ID            string
	Type          string
	WorkspaceID   string
	CapabilityID  string
	CompilationID string
	TrialID       string
	RevisionID    string
	RevisionNo    int
	ReleaseID     string
	ReleaseNo     int
	PlanHash      string
	PublishedBy   string
	OccurredAt    time.Time
	SchemaVersion int
}

type PublishEventWriter interface {
	AppendWorkflowReleasePublished(context.Context, *sql.Tx, WorkflowReleasePublishedEvent) error
}

type PublishWorkflowInput struct {
	RevisionID           string
	ReleaseID            string
	EventID              string
	WorkspaceID          string
	CapabilityID         string
	CompilationID        string
	CallableName         string
	CallableDescription  string
	RiskLevel            string
	SideEffectLevel      string
	RequiresConfirmation bool
	PublishNote          string
	PublishedBy          string
}

type PublishWorkflowResult struct {
	Revision Revision
	Release  capability.Release
	Trial    TrialRun
	Event    WorkflowReleasePublishedEvent
}

type PublishService struct {
	repository *Repository
	authorizer PublishAuthorizer
	events     PublishEventWriter
}

func NewPublishService(
	repository *Repository,
	authorizer PublishAuthorizer,
	events PublishEventWriter,
) (*PublishService, error) {
	if repository == nil || authorizer == nil || events == nil {
		return nil, errors.New("workflow publish service dependencies are required")
	}
	return &PublishService{repository: repository, authorizer: authorizer, events: events}, nil
}

func (s *PublishService) Publish(
	ctx context.Context,
	input PublishWorkflowInput,
) (PublishWorkflowResult, error) {
	input = normalizePublishWorkflow(input)
	if !validPublishWorkflow(input) {
		return PublishWorkflowResult{}, ErrInvalid
	}
	if _, err := s.authorizer.AuthorizeWorkspace(
		ctx, input.PublishedBy, input.WorkspaceID, authz.ActionPublish,
	); err != nil {
		return PublishWorkflowResult{}, err
	}
	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("begin workflow publish transaction: %w", err)
	}
	defer tx.Rollback()

	var capabilityKind, capabilityStatus string
	var latestCompilationID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT c.kind,c.status,w.latest_compilation_id
		FROM capabilities c
		JOIN workflows w ON w.workspace_id=c.workspace_id AND w.capability_id=c.id
		WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
		FOR UPDATE OF c,w
	`, input.WorkspaceID, input.CapabilityID).Scan(
		&capabilityKind, &capabilityStatus, &latestCompilationID,
	); errors.Is(err, sql.ErrNoRows) {
		return PublishWorkflowResult{}, ErrNotFound
	} else if err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("lock workflow for publish: %w", err)
	}
	if capabilityKind != "WORKFLOW" || capabilityStatus != "ACTIVE" {
		return PublishWorkflowResult{}, ErrInvalid
	}
	if !latestCompilationID.Valid || latestCompilationID.String != input.CompilationID {
		return PublishWorkflowResult{}, ErrConflict
	}

	draft, err := scanDraft(tx.QueryRowContext(ctx, `
		SELECT `+draftColumns+`
		FROM workflow_drafts d
		JOIN workflows w
		  ON w.workspace_id=d.workspace_id AND w.capability_id=d.capability_id
		 AND w.current_draft_id=d.id
		WHERE d.workspace_id=$1 AND d.capability_id=$2
		FOR UPDATE OF d
	`, input.WorkspaceID, input.CapabilityID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishWorkflowResult{}, ErrNotFound
	}
	if err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("lock workflow draft for publish: %w", err)
	}
	compilation, err := scanCompilation(tx.QueryRowContext(ctx, `
		SELECT `+compilationColumns+`
		FROM workflow_compilations wc
		WHERE wc.workspace_id=$1 AND wc.capability_id=$2 AND wc.id=$3
		 AND wc.draft_id=$4 AND wc.draft_version=$5 AND wc.graph_hash=$6
		 AND wc.status='VALID'
		FOR SHARE
	`, input.WorkspaceID, input.CapabilityID, input.CompilationID,
		draft.ID, draft.DraftVersion, draft.GraphHash))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishWorkflowResult{}, ErrConflict
	}
	if err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("lock workflow compilation for publish: %w", err)
	}
	trial, err := scanTrialRun(tx.QueryRowContext(ctx, `
		SELECT `+trialRunColumns+`
		FROM workflow_trial_runs tr
		WHERE tr.workspace_id=$1 AND tr.capability_id=$2 AND tr.compilation_id=$3
		 AND tr.status='SUCCEEDED'
		ORDER BY tr.started_at DESC,tr.id DESC
		LIMIT 1
		FOR UPDATE
	`, input.WorkspaceID, input.CapabilityID, input.CompilationID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishWorkflowResult{}, ErrNoSuccessfulTrial
	}
	if err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("lock workflow trial for publish: %w", err)
	}
	var alreadyPublished bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		 SELECT 1 FROM workflow_revisions
		 WHERE workspace_id=$1 AND capability_id=$2 AND source_compilation_id=$3
		)
	`, input.WorkspaceID, input.CapabilityID, input.CompilationID).Scan(&alreadyPublished); err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("check workflow compilation publication: %w", err)
	}
	if alreadyPublished {
		return PublishWorkflowResult{}, ErrConflict
	}

	inputSchema, outputSchema, err := workflowReleaseSchemas(compilation.Spec)
	if err != nil {
		return PublishWorkflowResult{}, err
	}
	var revisionNo, releaseNo int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(revision_no),0)+1 FROM workflow_revisions
		WHERE workspace_id=$1 AND capability_id=$2
	`, input.WorkspaceID, input.CapabilityID).Scan(&revisionNo); err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("allocate workflow revision number: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(release_no),0)+1 FROM capability_releases
		WHERE workspace_id=$1 AND capability_id=$2
	`, input.WorkspaceID, input.CapabilityID).Scan(&releaseNo); err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("allocate workflow release number: %w", err)
	}
	revision, err := scanRevision(tx.QueryRowContext(ctx, `
		INSERT INTO workflow_revisions AS wr(
		 id,workspace_id,capability_id,revision_no,source_compilation_id,
		 draft_snapshot,spec_snapshot,plan_snapshot,plan_hash,status,publish_note,
		 created_by,activated_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'PUBLISHED',$10,$11,clock_timestamp())
		RETURNING `+revisionColumns,
		input.RevisionID, input.WorkspaceID, input.CapabilityID, revisionNo,
		compilation.ID, []byte(draft.Graph), []byte(compilation.Spec), []byte(compilation.Plan),
		compilation.PlanHash, input.PublishNote, input.PublishedBy))
	if err != nil {
		return PublishWorkflowResult{}, mapWrite("create workflow revision", err)
	}
	release, err := scanWorkflowRelease(tx.QueryRowContext(ctx, `
		INSERT INTO capability_releases AS r(
		 id,workspace_id,capability_id,release_no,source_type,source_id,
		 callable_name,callable_description,input_schema,output_schema,risk_level,
		 side_effect_level,requires_confirmation,checksum,published_by
		) VALUES($1,$2,$3,$4,'WORKFLOW_REVISION',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING
		 r.id,r.workspace_id,r.capability_id,r.release_no,r.source_type,r.source_id,
		 r.callable_name,r.callable_description,r.input_schema,r.output_schema,r.risk_level,
		 r.side_effect_level,r.requires_confirmation,r.checksum,r.published_by,r.published_at,r.retired_at
	`, input.ReleaseID, input.WorkspaceID, input.CapabilityID, releaseNo, revision.ID,
		input.CallableName, input.CallableDescription, []byte(inputSchema), []byte(outputSchema),
		input.RiskLevel, input.SideEffectLevel, input.RequiresConfirmation,
		compilation.PlanHash, input.PublishedBy))
	if err != nil {
		return PublishWorkflowResult{}, mapWrite("create workflow capability release", err)
	}
	workflowResult, err := tx.ExecContext(ctx, `
		UPDATE workflows SET active_revision_id=$3
		WHERE workspace_id=$1 AND capability_id=$2 AND latest_compilation_id=$4
	`, input.WorkspaceID, input.CapabilityID, revision.ID, compilation.ID)
	if err != nil {
		return PublishWorkflowResult{}, mapWrite("activate workflow revision", err)
	}
	if rows, _ := workflowResult.RowsAffected(); rows != 1 {
		return PublishWorkflowResult{}, ErrConflict
	}
	capabilityResult, err := tx.ExecContext(ctx, `
		UPDATE capabilities
		SET active_release_id=$3,updated_by=$4,updated_at=clock_timestamp(),lock_version=lock_version+1
		WHERE workspace_id=$1 AND id=$2 AND kind='WORKFLOW'
		 AND status='ACTIVE' AND deleted_at IS NULL
	`, input.WorkspaceID, input.CapabilityID, release.ID, input.PublishedBy)
	if err != nil {
		return PublishWorkflowResult{}, mapWrite("activate workflow capability release", err)
	}
	if rows, _ := capabilityResult.RowsAffected(); rows != 1 {
		return PublishWorkflowResult{}, ErrConflict
	}
	event := WorkflowReleasePublishedEvent{
		ID: input.EventID, Type: "workflow.release.published",
		WorkspaceID: input.WorkspaceID, CapabilityID: input.CapabilityID,
		CompilationID: compilation.ID, TrialID: trial.ID,
		RevisionID: revision.ID, RevisionNo: revision.RevisionNo,
		ReleaseID: release.ID, ReleaseNo: release.ReleaseNo, PlanHash: compilation.PlanHash,
		PublishedBy: input.PublishedBy, OccurredAt: release.PublishedAt, SchemaVersion: 1,
	}
	if err := s.events.AppendWorkflowReleasePublished(ctx, tx, event); err != nil {
		return PublishWorkflowResult{}, fmt.Errorf("append workflow release event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PublishWorkflowResult{}, mapWrite("commit workflow publish transaction", err)
	}
	return PublishWorkflowResult{Revision: revision, Release: release, Trial: trial, Event: event}, nil
}

func workflowReleaseSchemas(specPayload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var spec domain.ExecutableWorkflowSpec
	if err := json.Unmarshal(specPayload, &spec); err != nil {
		return nil, nil, fmt.Errorf("decode workflow release spec: %w", err)
	}
	// Default to a concrete OpenAI-safe object schema (never bare {}).
	// Empty {} re-serializes as JSON Schema boolean true in some LLM tool paths.
	inputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	outputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	harvested := map[string]any{}
	for _, node := range spec.Nodes {
		switch node.Type {
		case "Start":
			if value, exists := node.Config["inputSchema"]; exists {
				object, ok := value.(map[string]any)
				if !ok {
					return nil, nil, ErrInvalid
				}
				inputSchema = normalizeReleaseObjectSchema(object)
			}
		case "End":
			if value, exists := node.Config["outputSchema"]; exists {
				object, ok := value.(map[string]any)
				if !ok {
					return nil, nil, ErrInvalid
				}
				outputSchema = normalizeReleaseObjectSchema(object)
			}
		case "Tool":
			// Harvest input.* refs from inputMapping so agent-facing TOOL schema
			// lists real business fields (smart-dag graphs often omit Start.inputSchema).
			collectWorkflowInputFields(node.Config["inputMapping"], harvested)
			collectWorkflowInputFields(node.Config["input"], harvested)
		}
	}
	if props, _ := inputSchema["properties"].(map[string]any); props != nil && len(props) == 0 && len(harvested) > 0 {
		inputSchema["properties"] = harvested
		required := make([]string, 0, len(harvested))
		for name := range harvested {
			required = append(required, name)
		}
		// Stable required order for checksums/tests.
		sort.Strings(required)
		inputSchema["required"] = required
	}
	inputPayload, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, nil, err
	}
	outputPayload, err := json.Marshal(outputSchema)
	if err != nil {
		return nil, nil, err
	}
	canonicalInput, _, err := canonicalJSON(inputPayload, "object")
	if err != nil {
		return nil, nil, err
	}
	canonicalOutput, _, err := canonicalJSON(outputPayload, "object")
	if err != nil {
		return nil, nil, err
	}
	return canonicalInput, canonicalOutput, nil
}

func normalizeReleaseObjectSchema(object map[string]any) map[string]any {
	if object == nil || len(object) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if _, ok := object["type"]; !ok {
		object["type"] = "object"
	}
	if t, _ := object["type"].(string); t == "object" {
		if props, ok := object["properties"]; !ok || props == nil {
			object["properties"] = map[string]any{}
		}
	}
	return object
}

// collectWorkflowInputFields walks tool inputMapping trees and records fields
// referenced as input.<name> so the published WORKFLOW release schema is not empty.
func collectWorkflowInputFields(value any, fields map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if kind, _ := typed["kind"].(string); kind == "ref" {
			path, _ := typed["path"].(string)
			if strings.HasPrefix(path, "input.") {
				name := strings.TrimPrefix(path, "input.")
				if name != "" && !strings.Contains(name, ".") {
					if _, exists := fields[name]; !exists {
						fields[name] = map[string]any{"type": "string"}
					}
				}
			}
			return
		}
		for _, child := range typed {
			collectWorkflowInputFields(child, fields)
		}
	case []any:
		for _, child := range typed {
			collectWorkflowInputFields(child, fields)
		}
	}
}

func scanRevision(row rowScanner) (Revision, error) {
	var value Revision
	var draft, spec, plan []byte
	if err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.RevisionNo,
		&value.SourceCompilationID, &draft, &spec, &plan, &value.PlanHash,
		&value.Status, &value.PublishNote, &value.CreatedBy, &value.CreatedAt,
		&value.ActivatedAt, &value.RetiredAt,
	); err != nil {
		return Revision{}, err
	}
	var err error
	if value.DraftSnapshot, _, err = canonicalJSON(draft, "object"); err != nil {
		return Revision{}, err
	}
	if value.SpecSnapshot, _, err = canonicalJSON(spec, "object"); err != nil {
		return Revision{}, err
	}
	if value.PlanSnapshot, _, err = canonicalJSON(plan, "object"); err != nil {
		return Revision{}, err
	}
	return value, nil
}

func scanWorkflowRelease(row rowScanner) (capability.Release, error) {
	var value capability.Release
	var inputSchema, outputSchema []byte
	if err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.CapabilityID, &value.ReleaseNo,
		&value.SourceType, &value.SourceID, &value.CallableName, &value.CallableDescription,
		&inputSchema, &outputSchema, &value.RiskLevel, &value.SideEffectLevel,
		&value.RequiresConfirmation, &value.Checksum, &value.PublishedBy,
		&value.PublishedAt, &value.RetiredAt,
	); err != nil {
		return capability.Release{}, err
	}
	var err error
	if value.InputSchema, _, err = canonicalJSON(inputSchema, "object"); err != nil {
		return capability.Release{}, err
	}
	if value.OutputSchema, _, err = canonicalJSON(outputSchema, "object"); err != nil {
		return capability.Release{}, err
	}
	return value, nil
}

func normalizePublishWorkflow(input PublishWorkflowInput) PublishWorkflowInput {
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	input.ReleaseID = strings.TrimSpace(input.ReleaseID)
	input.EventID = strings.TrimSpace(input.EventID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CapabilityID = strings.TrimSpace(input.CapabilityID)
	input.CompilationID = strings.TrimSpace(input.CompilationID)
	input.CallableName = strings.TrimSpace(input.CallableName)
	input.CallableDescription = strings.TrimSpace(input.CallableDescription)
	input.RiskLevel = strings.ToUpper(strings.TrimSpace(input.RiskLevel))
	input.SideEffectLevel = strings.ToUpper(strings.TrimSpace(input.SideEffectLevel))
	input.PublishNote = strings.TrimSpace(input.PublishNote)
	input.PublishedBy = strings.TrimSpace(input.PublishedBy)
	return input
}

func validPublishWorkflow(input PublishWorkflowInput) bool {
	return validUUID(input.RevisionID) && validUUID(input.ReleaseID) && validUUID(input.EventID) &&
		validUUID(input.WorkspaceID) && validUUID(input.CapabilityID) &&
		validUUID(input.CompilationID) && validUUID(input.PublishedBy) &&
		workflowCallableNamePattern.MatchString(input.CallableName) &&
		oneOfWorkflow(input.RiskLevel, "LOW", "MEDIUM", "HIGH", "CRITICAL") &&
		oneOfWorkflow(input.SideEffectLevel, "NONE", "READ", "WRITE", "IRREVERSIBLE")
}

func oneOfWorkflow(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
