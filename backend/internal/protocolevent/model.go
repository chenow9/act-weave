package protocolevent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrModelInvalid      = errors.New("protocol model is invalid")
	ErrModelTypeMismatch = errors.New("protocol model type does not match event type")
)

const (
	EventRunAccepted          = "run.accepted"
	EventRunStarted           = "run.started"
	EventRunWaiting           = "run.waiting"
	EventRunResumed           = "run.resumed"
	EventRunCompleted         = "run.completed"
	EventRunFailed            = "run.failed"
	EventRunCancelled         = "run.cancelled"
	EventItemStarted          = "item.started"
	EventItemDelta            = "item.delta"
	EventItemCompleted        = "item.completed"
	EventInteractionRequested = "interaction.requested"
	EventInteractionResolved  = "interaction.resolved"
	EventInteractionExpired   = "interaction.expired"
	EventUsageUpdated         = "usage.updated"
)

var knownEventTypes = []string{
	EventInteractionExpired, EventInteractionRequested, EventInteractionResolved,
	EventItemCompleted, EventItemDelta, EventItemStarted,
	EventRunAccepted, EventRunCancelled, EventRunCompleted, EventRunFailed,
	EventRunResumed, EventRunStarted, EventRunWaiting, EventUsageUpdated,
}

type EventType string

const EventTypeUnknown EventType = "unknown"

func ParseEventType(value string) EventType {
	value = strings.TrimSpace(value)
	if IsKnownEventType(value) {
		return EventType(value)
	}
	return EventTypeUnknown
}

func KnownEventTypes() []string { return append([]string(nil), knownEventTypes...) }

func IsKnownEventType(value string) bool {
	for _, known := range knownEventTypes {
		if value == known {
			return true
		}
	}
	return false
}

type RunStatus string

const (
	RunStatusAccepted           RunStatus = "accepted"
	RunStatusRunning            RunStatus = "running"
	RunStatusWaitingInteraction RunStatus = "waiting_interaction"
	RunStatusCompleted          RunStatus = "completed"
	RunStatusFailed             RunStatus = "failed"
	RunStatusCancelled          RunStatus = "cancelled"
	RunStatusUnknown            RunStatus = "unknown"
)

func ParseRunStatus(value string) RunStatus {
	parsed := RunStatus(strings.TrimSpace(value))
	switch parsed {
	case RunStatusAccepted, RunStatusRunning, RunStatusWaitingInteraction,
		RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return parsed
	default:
		return RunStatusUnknown
	}
}

type RunTrigger string

const (
	RunTriggerMessage  RunTrigger = "message"
	RunTriggerAPI      RunTrigger = "api"
	RunTriggerWorkflow RunTrigger = "workflow"
	RunTriggerSystem   RunTrigger = "system"
	RunTriggerUnknown  RunTrigger = "unknown"
)

func ParseRunTrigger(value string) RunTrigger {
	parsed := RunTrigger(strings.TrimSpace(value))
	switch parsed {
	case RunTriggerMessage, RunTriggerAPI, RunTriggerWorkflow, RunTriggerSystem:
		return parsed
	default:
		return RunTriggerUnknown
	}
}

type Run struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversationId"`
	AgentID        string         `json:"agentId"`
	Status         RunStatus      `json:"status"`
	Trigger        RunTrigger     `json:"trigger"`
	StartedAt      time.Time      `json:"startedAt"`
	CompletedAt    *time.Time     `json:"completedAt,omitempty"`
	Error          *ProtocolError `json:"error,omitempty"`
}

type ProtocolError struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

type ItemType string

const (
	ItemTypeMessage          ItemType = "message"
	ItemTypeToolCall         ItemType = "tool_call"
	ItemTypeWorkflowStep     ItemType = "workflow_step"
	ItemTypeInteraction      ItemType = "interaction"
	ItemTypeArtifact         ItemType = "artifact"
	ItemTypeReasoningSummary ItemType = "reasoning_summary"
	ItemTypeNotice           ItemType = "notice"
	ItemTypeUnknown          ItemType = "unknown"
)

var knownItemTypes = []ItemType{
	ItemTypeMessage, ItemTypeToolCall, ItemTypeWorkflowStep, ItemTypeInteraction,
	ItemTypeArtifact, ItemTypeReasoningSummary, ItemTypeNotice,
}

func KnownItemTypes() []ItemType { return append([]ItemType(nil), knownItemTypes...) }

func ParseItemType(value string) ItemType {
	parsed := ItemType(strings.TrimSpace(value))
	for _, known := range knownItemTypes {
		if parsed == known {
			return parsed
		}
	}
	return ItemTypeUnknown
}

type ItemStatus string

const (
	ItemStatusInProgress ItemStatus = "in_progress"
	ItemStatusWaiting    ItemStatus = "waiting"
	ItemStatusCompleted  ItemStatus = "completed"
	ItemStatusFailed     ItemStatus = "failed"
	ItemStatusDeclined   ItemStatus = "declined"
	ItemStatusCancelled  ItemStatus = "cancelled"
	ItemStatusUnknown    ItemStatus = "unknown"
)

func ParseItemStatus(value string) ItemStatus {
	parsed := ItemStatus(strings.TrimSpace(value))
	switch parsed {
	case ItemStatusInProgress, ItemStatusWaiting, ItemStatusCompleted,
		ItemStatusFailed, ItemStatusDeclined, ItemStatusCancelled:
		return parsed
	default:
		return ItemStatusUnknown
	}
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUnknown   MessageRole = "unknown"
)

func ParseMessageRole(value string) MessageRole {
	parsed := MessageRole(strings.TrimSpace(value))
	switch parsed {
	case MessageRoleUser, MessageRoleAssistant, MessageRoleTool, MessageRoleSystem:
		return parsed
	default:
		return MessageRoleUnknown
	}
}

type ContentPartType string

const (
	ContentPartTypeText    ContentPartType = "text"
	ContentPartTypeUnknown ContentPartType = "unknown"
)

func ParseContentPartType(value string) ContentPartType {
	if strings.TrimSpace(value) == string(ContentPartTypeText) {
		return ContentPartTypeText
	}
	return ContentPartTypeUnknown
}

type ContentPart interface {
	ContentKind() ContentPartType
}

type TextContentPart struct {
	Type ContentPartType `json:"type"`
	Text string          `json:"text"`
}

func (TextContentPart) ContentKind() ContentPartType { return ContentPartTypeText }

type UnknownContentPart struct {
	Type string
	raw  json.RawMessage
}

func (part UnknownContentPart) ContentKind() ContentPartType { return ContentPartTypeUnknown }
func (part UnknownContentPart) OriginalType() string         { return part.Type }
func (part UnknownContentPart) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), part.raw...), nil
}

type Item interface {
	ItemKind() ItemType
	ItemID() string
	ItemStatusValue() ItemStatus
}

type MessageItem struct {
	ID      string        `json:"id"`
	Type    ItemType      `json:"type"`
	Status  ItemStatus    `json:"status"`
	Role    MessageRole   `json:"role"`
	Content []ContentPart `json:"content"`
}

func (item MessageItem) ItemKind() ItemType          { return ItemTypeMessage }
func (item MessageItem) ItemID() string              { return item.ID }
func (item MessageItem) ItemStatusValue() ItemStatus { return item.Status }

type ToolCallItem struct {
	ID        string          `json:"id"`
	Type      ItemType        `json:"type"`
	Status    ItemStatus      `json:"status"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

func (item ToolCallItem) ItemKind() ItemType          { return ItemTypeToolCall }
func (item ToolCallItem) ItemID() string              { return item.ID }
func (item ToolCallItem) ItemStatusValue() ItemStatus { return item.Status }

type WorkflowStepItem struct {
	ID                  string          `json:"id"`
	Type                ItemType        `json:"type"`
	Status              ItemStatus      `json:"status"`
	NodeID              string          `json:"nodeId"`
	NodeType            string          `json:"nodeType"`
	WorkflowExecutionID string          `json:"workflowExecutionId,omitempty"`
	StepSequence        int             `json:"stepSequence,omitempty"`
	ToolCallItemIDs     []string        `json:"toolCallItemIds,omitempty"`
	Result              json.RawMessage `json:"result,omitempty"`
}

func (item WorkflowStepItem) ItemKind() ItemType          { return ItemTypeWorkflowStep }
func (item WorkflowStepItem) ItemID() string              { return item.ID }
func (item WorkflowStepItem) ItemStatusValue() ItemStatus { return item.Status }

type InteractionItem struct {
	ID          string      `json:"id"`
	Type        ItemType    `json:"type"`
	Status      ItemStatus  `json:"status"`
	Interaction Interaction `json:"interaction"`
}

func (item InteractionItem) ItemKind() ItemType          { return ItemTypeInteraction }
func (item InteractionItem) ItemID() string              { return item.ID }
func (item InteractionItem) ItemStatusValue() ItemStatus { return item.Status }

type ArtifactItem struct {
	ID         string     `json:"id"`
	Type       ItemType   `json:"type"`
	Status     ItemStatus `json:"status"`
	ArtifactID string     `json:"artifactId"`
	MediaType  string     `json:"mediaType"`
}

func (item ArtifactItem) ItemKind() ItemType          { return ItemTypeArtifact }
func (item ArtifactItem) ItemID() string              { return item.ID }
func (item ArtifactItem) ItemStatusValue() ItemStatus { return item.Status }

type ReasoningSummaryItem struct {
	ID     string     `json:"id"`
	Type   ItemType   `json:"type"`
	Status ItemStatus `json:"status"`
	Text   string     `json:"text"`
}

func (item ReasoningSummaryItem) ItemKind() ItemType          { return ItemTypeReasoningSummary }
func (item ReasoningSummaryItem) ItemID() string              { return item.ID }
func (item ReasoningSummaryItem) ItemStatusValue() ItemStatus { return item.Status }

type NoticeItem struct {
	ID      string     `json:"id"`
	Type    ItemType   `json:"type"`
	Status  ItemStatus `json:"status"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
}

func (item NoticeItem) ItemKind() ItemType          { return ItemTypeNotice }
func (item NoticeItem) ItemID() string              { return item.ID }
func (item NoticeItem) ItemStatusValue() ItemStatus { return item.Status }

type UnknownItem struct {
	ID     string
	Type   string
	Status string
	raw    json.RawMessage
}

func (item UnknownItem) ItemKind() ItemType          { return ItemTypeUnknown }
func (item UnknownItem) ItemID() string              { return item.ID }
func (item UnknownItem) ItemStatusValue() ItemStatus { return ItemStatusUnknown }
func (item UnknownItem) OriginalType() string        { return item.Type }
func (item UnknownItem) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), item.raw...), nil
}

type DeltaType string

const (
	DeltaTypeText          DeltaType = "text_delta"
	DeltaTypeArgumentsJSON DeltaType = "arguments_json_delta"
	DeltaTypeOutput        DeltaType = "output_delta"
	DeltaTypeProgress      DeltaType = "progress"
	DeltaTypeUnknown       DeltaType = "unknown"
)

var knownDeltaTypes = []DeltaType{
	DeltaTypeText, DeltaTypeArgumentsJSON, DeltaTypeOutput, DeltaTypeProgress,
}

func KnownDeltaTypes() []DeltaType { return append([]DeltaType(nil), knownDeltaTypes...) }

func ParseDeltaType(value string) DeltaType {
	parsed := DeltaType(strings.TrimSpace(value))
	for _, known := range knownDeltaTypes {
		if parsed == known {
			return parsed
		}
	}
	return DeltaTypeUnknown
}

type Delta interface{ DeltaKind() DeltaType }

type TextDelta struct {
	Type  DeltaType `json:"type"`
	Index int       `json:"index"`
	Text  string    `json:"text"`
}

func (TextDelta) DeltaKind() DeltaType { return DeltaTypeText }

type ArgumentsJSONDelta struct {
	Type        DeltaType `json:"type"`
	PartialJSON string    `json:"partialJson"`
}

func (ArgumentsJSONDelta) DeltaKind() DeltaType { return DeltaTypeArgumentsJSON }

type OutputDelta struct {
	Type DeltaType `json:"type"`
	Text string    `json:"text"`
}

func (OutputDelta) DeltaKind() DeltaType { return DeltaTypeOutput }

type ProgressDelta struct {
	Type    DeltaType `json:"type"`
	Current float64   `json:"current"`
	Total   *float64  `json:"total,omitempty"`
	Unit    string    `json:"unit"`
	Message string    `json:"message,omitempty"`
}

func (ProgressDelta) DeltaKind() DeltaType { return DeltaTypeProgress }

type UnknownDelta struct {
	Type string
	raw  json.RawMessage
}

func (UnknownDelta) DeltaKind() DeltaType       { return DeltaTypeUnknown }
func (delta UnknownDelta) OriginalType() string { return delta.Type }
func (delta UnknownDelta) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), delta.raw...), nil
}

type InteractionKind string

const (
	InteractionKindApproval InteractionKind = "approval"
	InteractionKindUnknown  InteractionKind = "unknown"
)

func ParseInteractionKind(value string) InteractionKind {
	if strings.TrimSpace(value) == string(InteractionKindApproval) {
		return InteractionKindApproval
	}
	return InteractionKindUnknown
}

type InteractionStatus string

const (
	InteractionStatusPending   InteractionStatus = "pending"
	InteractionStatusApproved  InteractionStatus = "approved"
	InteractionStatusDeclined  InteractionStatus = "declined"
	InteractionStatusCancelled InteractionStatus = "cancelled"
	InteractionStatusExpired   InteractionStatus = "expired"
	InteractionStatusUnknown   InteractionStatus = "unknown"
)

func ParseInteractionStatus(value string) InteractionStatus {
	parsed := InteractionStatus(strings.TrimSpace(value))
	switch parsed {
	case InteractionStatusPending, InteractionStatusApproved, InteractionStatusDeclined,
		InteractionStatusCancelled, InteractionStatusExpired:
		return parsed
	default:
		return InteractionStatusUnknown
	}
}

type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
	RiskLevelUnknown  RiskLevel = "unknown"
)

func ParseRiskLevel(value string) RiskLevel {
	parsed := RiskLevel(strings.TrimSpace(value))
	switch parsed {
	case RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return parsed
	default:
		return RiskLevelUnknown
	}
}

type InteractionDecision string

const (
	InteractionDecisionApprove InteractionDecision = "approve"
	InteractionDecisionDecline InteractionDecision = "decline"
	InteractionDecisionCancel  InteractionDecision = "cancel"
	InteractionDecisionUnknown InteractionDecision = "unknown"
)

func ParseInteractionDecision(value string) InteractionDecision {
	parsed := InteractionDecision(strings.TrimSpace(value))
	switch parsed {
	case InteractionDecisionApprove, InteractionDecisionDecline, InteractionDecisionCancel:
		return parsed
	default:
		return InteractionDecisionUnknown
	}
}

type RequiredDecider string

const (
	RequiredDeciderSameExternalSubject RequiredDecider = "same_external_subject"
	RequiredDeciderActWeaveUser        RequiredDecider = "actweave_user"
	RequiredDeciderServicePrincipal    RequiredDecider = "service_principal"
	RequiredDeciderUnknown             RequiredDecider = "unknown"
)

func ParseRequiredDecider(value string) RequiredDecider {
	parsed := RequiredDecider(strings.TrimSpace(value))
	switch parsed {
	case RequiredDeciderSameExternalSubject, RequiredDeciderActWeaveUser,
		RequiredDeciderServicePrincipal:
		return parsed
	default:
		return RequiredDeciderUnknown
	}
}

type Interaction struct {
	ID               string                `json:"id"`
	Kind             InteractionKind       `json:"kind"`
	Status           InteractionStatus     `json:"status"`
	TargetItemID     string                `json:"targetItemId"`
	RunID            string                `json:"runId,omitempty"`
	ReleaseID        string                `json:"releaseId,omitempty"`
	InputHash        string                `json:"inputHash,omitempty"`
	ConnectionID     string                `json:"connectionId,omitempty"`
	PlanHash         string                `json:"planHash,omitempty"`
	Title            string                `json:"title"`
	Reason           string                `json:"reason"`
	Risk             InteractionRisk       `json:"risk"`
	InputSummary     json.RawMessage       `json:"inputSummary,omitempty"`
	AllowedDecisions []InteractionDecision `json:"allowedDecisions"`
	RequiredDecider  RequiredDecider       `json:"requiredDecider,omitempty"`
	Version          int64                 `json:"version"`
	ExpiresAt        time.Time             `json:"expiresAt"`
}

type InteractionRisk struct {
	Level   RiskLevel `json:"level"`
	Reasons []string  `json:"reasons"`
}

type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
}

type EventData interface {
	acceptsEventType(string) bool
	validateModel() error
}

type RunSnapshotData struct {
	Run Run `json:"run"`
}

func (RunSnapshotData) acceptsEventType(value string) bool {
	switch value {
	case EventRunAccepted, EventRunStarted, EventRunCompleted, EventRunFailed, EventRunCancelled:
		return true
	default:
		return false
	}
}
func (data RunSnapshotData) validateModel() error { return validateRun(data.Run) }

type RunWaitingData struct {
	Run            Run      `json:"run"`
	InteractionIDs []string `json:"interactionIds"`
}

func (RunWaitingData) acceptsEventType(value string) bool { return value == EventRunWaiting }
func (data RunWaitingData) validateModel() error {
	if validateRun(data.Run) != nil || len(data.InteractionIDs) == 0 {
		return ErrModelInvalid
	}
	for _, id := range data.InteractionIDs {
		if !modelUUID(id) {
			return ErrModelInvalid
		}
	}
	return nil
}

type RunResumedData struct {
	Run           Run    `json:"run"`
	InteractionID string `json:"interactionId"`
}

func (RunResumedData) acceptsEventType(value string) bool { return value == EventRunResumed }
func (data RunResumedData) validateModel() error {
	if validateRun(data.Run) != nil || !modelUUID(data.InteractionID) {
		return ErrModelInvalid
	}
	return nil
}

type ItemSnapshotData struct {
	Item Item `json:"item"`
}

func (ItemSnapshotData) acceptsEventType(value string) bool {
	return value == EventItemStarted || value == EventItemCompleted
}
func (data ItemSnapshotData) validateModel() error { return ValidateItem(data.Item) }
func (data ItemSnapshotData) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Item Item `json:"item"`
	}{Item: data.Item})
}

type ItemDeltaData struct {
	ItemID string `json:"itemId"`
	Delta  Delta  `json:"delta"`
}

func (ItemDeltaData) acceptsEventType(value string) bool { return value == EventItemDelta }
func (data ItemDeltaData) validateModel() error {
	if !modelUUID(data.ItemID) {
		return ErrModelInvalid
	}
	return ValidateDelta(data.Delta)
}
func (data ItemDeltaData) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ItemID string `json:"itemId"`
		Delta  Delta  `json:"delta"`
	}{ItemID: data.ItemID, Delta: data.Delta})
}

type InteractionData struct {
	Interaction Interaction `json:"interaction"`
}

func (InteractionData) acceptsEventType(value string) bool {
	return value == EventInteractionRequested || value == EventInteractionResolved ||
		value == EventInteractionExpired
}
func (data InteractionData) validateModel() error { return validateInteraction(data.Interaction) }

type UsageData struct {
	Usage Usage `json:"usage"`
}

func (UsageData) acceptsEventType(value string) bool { return value == EventUsageUpdated }
func (data UsageData) validateModel() error {
	if data.Usage.InputTokens < 0 || data.Usage.OutputTokens < 0 ||
		data.Usage.TotalTokens < 0 {
		return ErrModelInvalid
	}
	return nil
}

type UnknownEventData struct{ raw json.RawMessage }

func (UnknownEventData) acceptsEventType(value string) bool { return !IsKnownEventType(value) }
func (data UnknownEventData) validateModel() error {
	_, err := canonicalAppendObject(data.raw)
	return err
}
func (data UnknownEventData) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), data.raw...), nil
}

func NewUnknownEventData(raw json.RawMessage) (UnknownEventData, error) {
	canonical, err := canonicalAppendObject(raw)
	if err != nil {
		return UnknownEventData{}, ErrModelInvalid
	}
	return UnknownEventData{raw: canonical}, nil
}

func BuildProtocolEvent(input NewProtocolEvent, data EventData) (NewProtocolEvent, error) {
	if data == nil || !data.acceptsEventType(strings.TrimSpace(input.Type)) {
		return NewProtocolEvent{}, ErrModelTypeMismatch
	}
	if err := data.validateModel(); err != nil {
		return NewProtocolEvent{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return NewProtocolEvent{}, fmt.Errorf("marshal protocol event data: %w", err)
	}
	input.Data = raw
	value, err := normalizeAppendEvent(input)
	if err != nil {
		return NewProtocolEvent{}, ErrModelInvalid
	}
	return value, nil
}

func DecodeEventData(eventType string, raw json.RawMessage) (EventData, error) {
	canonical, err := canonicalAppendObject(raw)
	if err != nil {
		return nil, ErrModelInvalid
	}
	switch eventType {
	case EventRunAccepted, EventRunStarted, EventRunCompleted, EventRunFailed, EventRunCancelled:
		var value RunSnapshotData
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case EventRunWaiting:
		var value RunWaitingData
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case EventRunResumed:
		var value RunResumedData
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case EventItemStarted, EventItemCompleted:
		var wire struct {
			Item json.RawMessage `json:"item"`
		}
		if err = json.Unmarshal(canonical, &wire); err != nil {
			return nil, ErrModelInvalid
		}
		item, decodeErr := DecodeItem(wire.Item)
		return ItemSnapshotData{Item: item}, decodeErr
	case EventItemDelta:
		var wire struct {
			ItemID string          `json:"itemId"`
			Delta  json.RawMessage `json:"delta"`
		}
		if err = json.Unmarshal(canonical, &wire); err != nil {
			return nil, ErrModelInvalid
		}
		delta, decodeErr := DecodeDelta(wire.Delta)
		return ItemDeltaData{ItemID: wire.ItemID, Delta: delta}, decodeErr
	case EventInteractionRequested, EventInteractionResolved, EventInteractionExpired:
		var value InteractionData
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case EventUsageUpdated:
		var value UsageData
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	default:
		return UnknownEventData{raw: canonical}, nil
	}
}

func (event ProtocolEvent) DecodeData() (EventData, error) {
	return DecodeEventData(event.Type, event.Data)
}

func DecodeItem(raw json.RawMessage) (Item, error) {
	canonical, err := canonicalAppendObject(raw)
	if err != nil {
		return nil, ErrModelInvalid
	}
	var discriminator struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(canonical, &discriminator); err != nil {
		return nil, ErrModelInvalid
	}
	switch ParseItemType(discriminator.Type) {
	case ItemTypeMessage:
		var wire struct {
			ID      string            `json:"id"`
			Type    ItemType          `json:"type"`
			Status  ItemStatus        `json:"status"`
			Role    MessageRole       `json:"role"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(canonical, &wire); err != nil {
			return nil, ErrModelInvalid
		}
		item := MessageItem{ID: wire.ID, Type: wire.Type, Status: wire.Status, Role: wire.Role}
		for _, partRaw := range wire.Content {
			part, err := DecodeContentPart(partRaw)
			if err != nil {
				return nil, err
			}
			item.Content = append(item.Content, part)
		}
		return item, nil
	case ItemTypeToolCall:
		var value ToolCallItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case ItemTypeWorkflowStep:
		var value WorkflowStepItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case ItemTypeInteraction:
		var value InteractionItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case ItemTypeArtifact:
		var value ArtifactItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case ItemTypeReasoningSummary:
		var value ReasoningSummaryItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case ItemTypeNotice:
		var value NoticeItem
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	default:
		if !modelUUID(discriminator.ID) || strings.TrimSpace(discriminator.Type) == "" ||
			strings.TrimSpace(discriminator.Status) == "" {
			return nil, ErrModelInvalid
		}
		return UnknownItem{
			ID: discriminator.ID, Type: discriminator.Type,
			Status: discriminator.Status, raw: canonical,
		}, nil
	}
}

func DecodeContentPart(raw json.RawMessage) (ContentPart, error) {
	canonical, err := canonicalAppendObject(raw)
	if err != nil {
		return nil, ErrModelInvalid
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(canonical, &discriminator); err != nil || discriminator.Type == "" {
		return nil, ErrModelInvalid
	}
	if discriminator.Type == "text" {
		var value TextContentPart
		if err := json.Unmarshal(canonical, &value); err != nil {
			return nil, ErrModelInvalid
		}
		return value, nil
	}
	return UnknownContentPart{Type: discriminator.Type, raw: canonical}, nil
}

func DecodeDelta(raw json.RawMessage) (Delta, error) {
	canonical, err := canonicalAppendObject(raw)
	if err != nil {
		return nil, ErrModelInvalid
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(canonical, &discriminator); err != nil || discriminator.Type == "" {
		return nil, ErrModelInvalid
	}
	switch ParseDeltaType(discriminator.Type) {
	case DeltaTypeText:
		var value TextDelta
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case DeltaTypeArgumentsJSON:
		var value ArgumentsJSONDelta
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case DeltaTypeOutput:
		var value OutputDelta
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	case DeltaTypeProgress:
		var value ProgressDelta
		err = json.Unmarshal(canonical, &value)
		return value, modelDecodeError(err)
	default:
		return UnknownDelta{Type: discriminator.Type, raw: canonical}, nil
	}
}

func ValidateItem(item Item) error {
	if item == nil || !modelUUID(item.ItemID()) || item.ItemStatusValue() == "" {
		return ErrModelInvalid
	}
	switch value := item.(type) {
	case MessageItem:
		if value.Type != ItemTypeMessage || value.Role == "" || value.Content == nil {
			return ErrModelInvalid
		}
		for _, part := range value.Content {
			if part == nil || strings.TrimSpace(string(part.ContentKind())) == "" {
				return ErrModelInvalid
			}
			if text, ok := part.(TextContentPart); ok && text.Type != ContentPartTypeText {
				return ErrModelInvalid
			}
		}
	case ToolCallItem:
		if value.Type != ItemTypeToolCall || strings.TrimSpace(value.Name) == "" {
			return ErrModelInvalid
		}
	case WorkflowStepItem:
		if value.Type != ItemTypeWorkflowStep || strings.TrimSpace(value.NodeID) == "" ||
			strings.TrimSpace(value.NodeType) == "" {
			return ErrModelInvalid
		}
		if (value.WorkflowExecutionID != "" && !modelUUID(value.WorkflowExecutionID)) ||
			value.StepSequence < 0 {
			return ErrModelInvalid
		}
		seenToolItems := make(map[string]struct{}, len(value.ToolCallItemIDs))
		for _, itemID := range value.ToolCallItemIDs {
			if !modelUUID(itemID) {
				return ErrModelInvalid
			}
			if _, duplicate := seenToolItems[itemID]; duplicate {
				return ErrModelInvalid
			}
			seenToolItems[itemID] = struct{}{}
		}
	case InteractionItem:
		if value.Type != ItemTypeInteraction || validateInteraction(value.Interaction) != nil {
			return ErrModelInvalid
		}
	case ArtifactItem:
		if value.Type != ItemTypeArtifact || !modelUUID(value.ArtifactID) ||
			strings.TrimSpace(value.MediaType) == "" {
			return ErrModelInvalid
		}
	case ReasoningSummaryItem:
		if value.Type != ItemTypeReasoningSummary {
			return ErrModelInvalid
		}
	case NoticeItem:
		if value.Type != ItemTypeNotice || strings.TrimSpace(value.Code) == "" ||
			strings.TrimSpace(value.Message) == "" {
			return ErrModelInvalid
		}
	case UnknownItem:
		if ParseItemType(value.Type) != ItemTypeUnknown || len(value.raw) == 0 {
			return ErrModelInvalid
		}
	default:
		return ErrModelInvalid
	}
	return nil
}

func ValidateDelta(delta Delta) error {
	if delta == nil {
		return ErrModelInvalid
	}
	switch value := delta.(type) {
	case TextDelta:
		if value.Type != DeltaTypeText || value.Index < 0 {
			return ErrModelInvalid
		}
	case ArgumentsJSONDelta:
		if value.Type != DeltaTypeArgumentsJSON {
			return ErrModelInvalid
		}
	case OutputDelta:
		if value.Type != DeltaTypeOutput {
			return ErrModelInvalid
		}
	case ProgressDelta:
		if value.Type != DeltaTypeProgress || value.Current < 0 ||
			(value.Total != nil && *value.Total < 0) || strings.TrimSpace(value.Unit) == "" {
			return ErrModelInvalid
		}
	case UnknownDelta:
		if ParseDeltaType(value.Type) != DeltaTypeUnknown || len(value.raw) == 0 {
			return ErrModelInvalid
		}
	default:
		return ErrModelInvalid
	}
	return nil
}

func validateRun(run Run) error {
	if !modelUUID(run.ID) || !modelUUID(run.ConversationID) || !modelUUID(run.AgentID) ||
		run.Status == "" || run.Trigger == "" || run.StartedAt.IsZero() {
		return ErrModelInvalid
	}
	if run.CompletedAt != nil && run.CompletedAt.Before(run.StartedAt) {
		return ErrModelInvalid
	}
	return nil
}

func validateInteraction(value Interaction) error {
	if !modelUUID(value.ID) || value.Kind == "" || value.Status == "" ||
		!modelUUID(value.TargetItemID) || strings.TrimSpace(value.Title) == "" ||
		strings.TrimSpace(value.Reason) == "" || value.Risk.Level == "" ||
		len(value.AllowedDecisions) == 0 || value.Version < 1 || value.ExpiresAt.IsZero() {
		return ErrModelInvalid
	}
	if (value.RunID != "" && !modelUUID(value.RunID)) ||
		(value.ReleaseID != "" && !modelUUID(value.ReleaseID)) ||
		(value.ConnectionID != "" && !modelUUID(value.ConnectionID)) ||
		(value.InputHash != "" && !modelSHA256(value.InputHash)) ||
		(value.PlanHash != "" && !modelSHA256(value.PlanHash)) {
		return ErrModelInvalid
	}
	return nil
}

func modelSHA256(value string) bool {
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

func modelUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func modelDecodeError(err error) error {
	if err != nil {
		return ErrModelInvalid
	}
	return nil
}
