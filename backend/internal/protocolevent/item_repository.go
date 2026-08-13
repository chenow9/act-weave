package protocolevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	ErrRunItemInvalid  = errors.New("run item projection is invalid")
	ErrRunItemNotFound = errors.New("run item projection was not found")
	ErrRunItemConflict = errors.New("run item projection state conflicts with the request")
)

const (
	SourceChatMessage           = "CHAT_MESSAGE"
	SourceModelResponse         = "MODEL_RESPONSE"
	SourceToolInvocation        = "TOOL_INVOCATION"
	SourceWorkflowExecution     = "WORKFLOW_EXECUTION"
	SourceWorkflowStep          = "WORKFLOW_STEP"
	SourceExecutionConfirmation = "EXECUTION_CONFIRMATION"
	SourceStoredObject          = "STORED_OBJECT"
	SourceRuntime               = "RUNTIME"
	SourceUnknown               = "UNKNOWN"
)

var allowedRunItemSources = map[string]struct{}{
	SourceChatMessage: {}, SourceModelResponse: {}, SourceToolInvocation: {},
	SourceWorkflowExecution: {}, SourceWorkflowStep: {}, SourceExecutionConfirmation: {},
	SourceStoredObject: {}, SourceRuntime: {}, SourceUnknown: {},
}

type RunItemProjection struct {
	ID          string
	WorkspaceID string
	AgentID     string
	RunID       string
	Ordinal     int
	ItemType    string
	Status      ItemStatus
	SourceType  string
	SourceID    string
	Item        Item
	Snapshot    json.RawMessage
	StartedAt   time.Time
	CompletedAt *time.Time
}

type CreateRunItemInput struct {
	WorkspaceID string
	AgentID     string
	RunID       string
	Ordinal     int
	SourceType  string
	SourceID    string
	Item        Item
	StartedAt   time.Time
}

type ApplyItemDeltaInput struct {
	WorkspaceID string
	AgentID     string
	RunID       string
	ItemID      string
	Delta       Delta
}

type CompleteRunItemInput struct {
	WorkspaceID string
	AgentID     string
	RunID       string
	Item        Item
	CompletedAt time.Time
}

type RunItemRepository struct {
	db        *sql.DB
	validator *PayloadValidator
}

func NewRunItemRepository(db *sql.DB) (*RunItemRepository, error) {
	if db == nil {
		return nil, ErrRunItemInvalid
	}
	return &RunItemRepository{db: db, validator: MustDefaultPayloadValidator()}, nil
}

func (repository *RunItemRepository) Create(
	ctx context.Context,
	input CreateRunItemInput,
) (RunItemProjection, error) {
	if repository == nil || repository.db == nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return RunItemProjection{}, fmt.Errorf("begin run item create: %w", err)
	}
	defer tx.Rollback()
	value, err := repository.CreateInTx(ctx, tx, input)
	if err != nil {
		return RunItemProjection{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunItemProjection{}, mapRunItemWrite("commit run item create", err)
	}
	return value, nil
}

func (repository *RunItemRepository) CreateInTx(
	ctx context.Context,
	tx *sql.Tx,
	input CreateRunItemInput,
) (RunItemProjection, error) {
	input, snapshot, itemType, status, err := repository.validateCreate(input)
	if tx == nil || err != nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	value, err := scanRunItemProjection(tx.QueryRowContext(ctx, `
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		 source_type,source_id,snapshot,started_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		          source_type,source_id,snapshot,started_at,completed_at
	`, input.Item.ItemID(), input.WorkspaceID, input.AgentID, input.RunID,
		input.Ordinal, itemType, status, input.SourceType, nullableAppendID(input.SourceID),
		[]byte(snapshot), input.StartedAt))
	if err != nil {
		return RunItemProjection{}, mapRunItemWrite("insert run item projection", err)
	}
	return value, nil
}

func (repository *RunItemRepository) Get(
	ctx context.Context,
	workspaceID, agentID, runID, itemID string,
) (RunItemProjection, error) {
	workspaceID, agentID, runID, itemID = normalizeProjectionScope(workspaceID, agentID, runID, itemID)
	if repository == nil || repository.db == nil || !modelUUID(workspaceID) ||
		!modelUUID(agentID) || !modelUUID(runID) || !modelUUID(itemID) {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	value, err := scanRunItemProjection(repository.db.QueryRowContext(ctx, `
		SELECT id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		       source_type,source_id,snapshot,started_at,completed_at
		FROM run_items
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4
	`, workspaceID, agentID, runID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return RunItemProjection{}, ErrRunItemNotFound
	}
	return value, err
}

func (repository *RunItemRepository) ListForRun(
	ctx context.Context,
	workspaceID, agentID, runID string,
) ([]RunItemProjection, error) {
	workspaceID, agentID, runID, _ = normalizeProjectionScope(workspaceID, agentID, runID, "")
	if repository == nil || repository.db == nil || !modelUUID(workspaceID) ||
		!modelUUID(agentID) || !modelUUID(runID) {
		return nil, ErrRunItemInvalid
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		       source_type,source_id,snapshot,started_at,completed_at
		FROM run_items
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3
		ORDER BY ordinal,id
	`, workspaceID, agentID, runID)
	if err != nil {
		return nil, fmt.Errorf("list Run Item projections: %w", err)
	}
	defer rows.Close()
	values := make([]RunItemProjection, 0)
	for rows.Next() {
		value, scanErr := scanRunItemProjection(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan Run Item projection: %w", scanErr)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (repository *RunItemRepository) ApplyDelta(
	ctx context.Context,
	input ApplyItemDeltaInput,
) (RunItemProjection, error) {
	if repository == nil || repository.db == nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return RunItemProjection{}, fmt.Errorf("begin run item delta: %w", err)
	}
	defer tx.Rollback()
	value, err := repository.ApplyDeltaInTx(ctx, tx, input)
	if err != nil {
		return RunItemProjection{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunItemProjection{}, mapRunItemWrite("commit run item delta", err)
	}
	return value, nil
}

func (repository *RunItemRepository) ApplyDeltaInTx(
	ctx context.Context,
	tx *sql.Tx,
	input ApplyItemDeltaInput,
) (RunItemProjection, error) {
	input.WorkspaceID, input.AgentID, input.RunID, input.ItemID = normalizeProjectionScope(
		input.WorkspaceID, input.AgentID, input.RunID, input.ItemID,
	)
	if tx == nil || !modelUUID(input.WorkspaceID) || !modelUUID(input.AgentID) ||
		!modelUUID(input.RunID) || !modelUUID(input.ItemID) || ValidateDelta(input.Delta) != nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	deltaData, err := json.Marshal(ItemDeltaData{ItemID: input.ItemID, Delta: input.Delta})
	if err != nil || repository.validator.ValidateEventData(EventItemDelta, deltaData) != nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	current, err := scanRunItemProjection(tx.QueryRowContext(ctx, `
		SELECT id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		       source_type,source_id,snapshot,started_at,completed_at
		FROM run_items
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4
		FOR UPDATE
	`, input.WorkspaceID, input.AgentID, input.RunID, input.ItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return RunItemProjection{}, ErrRunItemNotFound
	}
	if err != nil {
		return RunItemProjection{}, err
	}
	if current.Status != ItemStatusInProgress && current.Status != ItemStatusWaiting {
		return RunItemProjection{}, ErrRunItemConflict
	}
	projected, changed, err := applyCurrentItemDelta(current.Item, input.Delta)
	if err != nil {
		return RunItemProjection{}, err
	}
	if !changed {
		return current, nil
	}
	snapshot, err := marshalProjectionItem(projected, EventItemStarted, repository.validator)
	if err != nil {
		return RunItemProjection{}, err
	}
	value, err := scanRunItemProjection(tx.QueryRowContext(ctx, `
		UPDATE run_items SET snapshot=$5
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4
		  AND status IN ('in_progress','waiting')
		RETURNING id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		          source_type,source_id,snapshot,started_at,completed_at
	`, input.WorkspaceID, input.AgentID, input.RunID, input.ItemID, []byte(snapshot)))
	if err != nil {
		return RunItemProjection{}, mapRunItemWrite("apply run item delta", err)
	}
	return value, nil
}

func (repository *RunItemRepository) Complete(
	ctx context.Context,
	input CompleteRunItemInput,
) (RunItemProjection, error) {
	if repository == nil || repository.db == nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return RunItemProjection{}, fmt.Errorf("begin run item completion: %w", err)
	}
	defer tx.Rollback()
	value, err := repository.CompleteInTx(ctx, tx, input)
	if err != nil {
		return RunItemProjection{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunItemProjection{}, mapRunItemWrite("commit run item completion", err)
	}
	return value, nil
}

func (repository *RunItemRepository) CompleteInTx(
	ctx context.Context,
	tx *sql.Tx,
	input CompleteRunItemInput,
) (RunItemProjection, error) {
	input.WorkspaceID, input.AgentID, input.RunID, _ = normalizeProjectionScope(
		input.WorkspaceID, input.AgentID, input.RunID, "",
	)
	if tx == nil || input.Item == nil || !modelUUID(input.WorkspaceID) ||
		!modelUUID(input.AgentID) || !modelUUID(input.RunID) || !modelUUID(input.Item.ItemID()) ||
		input.CompletedAt.IsZero() || !terminalItemStatus(input.Item.ItemStatusValue()) {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	snapshot, err := marshalProjectionItem(input.Item, EventItemCompleted, repository.validator)
	if err != nil {
		return RunItemProjection{}, err
	}
	itemType := projectionItemType(input.Item)
	status := ParseItemStatus(string(input.Item.ItemStatusValue()))
	value, err := scanRunItemProjection(tx.QueryRowContext(ctx, `
		UPDATE run_items
		SET status=$6,snapshot=$7,completed_at=$8
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4
		  AND item_type=$5 AND status IN ('in_progress','waiting')
		RETURNING id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		          source_type,source_id,snapshot,started_at,completed_at
	`, input.WorkspaceID, input.AgentID, input.RunID, input.Item.ItemID(), itemType,
		status, []byte(snapshot), input.CompletedAt.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM run_items
			 WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3 AND id=$4)
		`, input.WorkspaceID, input.AgentID, input.RunID, input.Item.ItemID()).Scan(&exists); queryErr != nil {
			return RunItemProjection{}, fmt.Errorf("classify run item completion: %w", queryErr)
		}
		if exists {
			return RunItemProjection{}, ErrRunItemConflict
		}
		return RunItemProjection{}, ErrRunItemNotFound
	}
	if err != nil {
		return RunItemProjection{}, mapRunItemWrite("complete run item projection", err)
	}
	return value, nil
}

func (repository *RunItemRepository) validateCreate(
	input CreateRunItemInput,
) (CreateRunItemInput, json.RawMessage, string, ItemStatus, error) {
	input.WorkspaceID, input.AgentID, input.RunID, input.SourceID = normalizeProjectionScope(
		input.WorkspaceID, input.AgentID, input.RunID, input.SourceID,
	)
	input.SourceType = strings.ToUpper(strings.TrimSpace(input.SourceType))
	if input.Item == nil || !modelUUID(input.WorkspaceID) || !modelUUID(input.AgentID) ||
		!modelUUID(input.RunID) || input.Ordinal < 1 || input.StartedAt.IsZero() {
		return CreateRunItemInput{}, nil, "", "", ErrRunItemInvalid
	}
	if _, allowed := allowedRunItemSources[input.SourceType]; !allowed ||
		(input.SourceID != "" && !modelUUID(input.SourceID)) {
		return CreateRunItemInput{}, nil, "", "", ErrRunItemInvalid
	}
	status := ParseItemStatus(string(input.Item.ItemStatusValue()))
	if status != ItemStatusInProgress && status != ItemStatusWaiting && status != ItemStatusUnknown {
		return CreateRunItemInput{}, nil, "", "", ErrRunItemInvalid
	}
	snapshot, err := marshalProjectionItem(input.Item, EventItemStarted, repository.validator)
	if err != nil {
		return CreateRunItemInput{}, nil, "", "", err
	}
	input.StartedAt = input.StartedAt.UTC()
	return input, snapshot, projectionItemType(input.Item), status, nil
}

func marshalProjectionItem(item Item, eventType string, validator *PayloadValidator) (json.RawMessage, error) {
	data, err := ValidateProjectionItemData(item, eventType, validator)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, ErrRunItemInvalid
	}
	return wrapper.Item, nil
}

// ValidateProjectionItem runs the same validation sequence as production
// CompleteRunItem / marshalProjectionItem (KD-11 preflight):
//
//	ValidateItem → json.Marshal(ItemSnapshotData{Item}) → ValidateEventData(eventType, data)
//
// Bridge completeRun must call this before RecordAssistantResult when attaching
// a2ui so preflight pass ⇔ CompleteProjected pass (no false pass / false degrade).
func ValidateProjectionItem(item Item, eventType string, validator *PayloadValidator) error {
	_, err := ValidateProjectionItemData(item, eventType, validator)
	return err
}

// ValidateProjectionItemData is ValidateProjectionItem plus the marshaled
// ItemSnapshotData bytes used for event validation (tests / shared helpers).
func ValidateProjectionItemData(item Item, eventType string, validator *PayloadValidator) (json.RawMessage, error) {
	if validator == nil {
		validator = MustDefaultPayloadValidator()
	}
	if ValidateItem(item) != nil {
		return nil, ErrRunItemInvalid
	}
	data, err := json.Marshal(ItemSnapshotData{Item: item})
	if err != nil || validator.ValidateEventData(eventType, data) != nil {
		return nil, ErrRunItemInvalid
	}
	return data, nil
}

func applyCurrentItemDelta(item Item, delta Delta) (Item, bool, error) {
	switch current := item.(type) {
	case MessageItem:
		text, ok := delta.(TextDelta)
		if !ok {
			return item, false, nil
		}
		if text.Index < 0 || text.Index >= len(current.Content) {
			return nil, false, ErrRunItemInvalid
		}
		part, ok := current.Content[text.Index].(TextContentPart)
		if !ok {
			return item, false, nil
		}
		part.Text += text.Text
		current.Content[text.Index] = part
		return current, true, nil
	case ToolCallItem:
		output, ok := delta.(OutputDelta)
		if !ok {
			return item, false, nil
		}
		currentText := ""
		if len(current.Output) > 0 && string(current.Output) != "null" {
			if err := json.Unmarshal(current.Output, &currentText); err != nil {
				return item, false, nil
			}
		}
		encoded, err := json.Marshal(currentText + output.Text)
		if err != nil {
			return nil, false, ErrRunItemInvalid
		}
		current.Output = encoded
		return current, true, nil
	default:
		return item, false, nil
	}
}

func projectionItemType(item Item) string {
	if unknown, ok := item.(UnknownItem); ok {
		return strings.TrimSpace(unknown.OriginalType())
	}
	return string(item.ItemKind())
}

func terminalItemStatus(status ItemStatus) bool {
	switch ParseItemStatus(string(status)) {
	case ItemStatusCompleted, ItemStatusFailed, ItemStatusDeclined, ItemStatusCancelled:
		return true
	default:
		return false
	}
}

func normalizeProjectionScope(workspaceID, agentID, runID, fourth string) (string, string, string, string) {
	values := [4]string{
		strings.ToLower(strings.TrimSpace(workspaceID)),
		strings.ToLower(strings.TrimSpace(agentID)),
		strings.ToLower(strings.TrimSpace(runID)),
		strings.ToLower(strings.TrimSpace(fourth)),
	}
	return values[0], values[1], values[2], values[3]
}

type runItemScanner interface{ Scan(...any) error }

func scanRunItemProjection(scanner runItemScanner) (RunItemProjection, error) {
	var value RunItemProjection
	var sourceID sql.NullString
	var completedAt sql.NullTime
	var status string
	var snapshot []byte
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.AgentID, &value.RunID,
		&value.Ordinal, &value.ItemType, &status, &value.SourceType,
		&sourceID, &snapshot, &value.StartedAt, &completedAt,
	)
	if err != nil {
		return RunItemProjection{}, mapRunItemRead("scan run item projection", err)
	}
	item, err := DecodeItem(snapshot)
	if err != nil {
		return RunItemProjection{}, ErrRunItemInvalid
	}
	value.Status, value.SourceID = ItemStatus(status), sourceID.String
	value.Snapshot, value.Item = append(json.RawMessage(nil), snapshot...), item
	if completedAt.Valid {
		completed := completedAt.Time
		value.CompletedAt = &completed
	}
	return value, nil
}

func mapRunItemRead(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapRunItemWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001":
			return fmt.Errorf("%s: %w", operation, ErrRunItemConflict)
		case "23502", "23503", "23514", "22P02":
			return fmt.Errorf("%s: %w", operation, ErrRunItemInvalid)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
