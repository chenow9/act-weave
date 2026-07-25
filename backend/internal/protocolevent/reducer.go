package protocolevent

import (
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrReduceInvalid  = errors.New("protocol event cannot be reduced")
	ErrReduceSequence = errors.New("protocol event sequence is not contiguous")
	ErrReduceScope    = errors.New("protocol event scope changed during replay")
	ErrReduceState    = errors.New("protocol event conflicts with reduced state")
)

// ReducedRunSnapshot is the transport-independent result of replaying one Run
// stream. Item and Interaction order is first-observation order; completed
// snapshots replace earlier states without changing that order.
type ReducedRunSnapshot struct {
	Run          *Run
	Items        []Item
	Interactions []Interaction
	Usage        *Usage
	LastSequence int64
}

// RunReducer is a pure replay state machine. It performs no database, network,
// tool, workflow, or approval actions, so replaying any prefix cannot repeat a
// side effect.
type RunReducer struct {
	workspaceID    string
	agentID        string
	conversationID string
	runID          string
	streamID       string
	traceID        string

	run              *Run
	items            map[string]Item
	itemOrder        []string
	interactions     map[string]Interaction
	interactionOrder []string
	usage            *Usage
	lastSequence     int64
}

func NewRunReducer() *RunReducer {
	return &RunReducer{
		items: make(map[string]Item), interactions: make(map[string]Interaction),
	}
}

func (reducer *RunReducer) Apply(event ProtocolEvent) error {
	if reducer == nil || reducer.items == nil || reducer.interactions == nil ||
		event.Sequence != reducer.lastSequence+1 || event.Sequence < 1 {
		return ErrReduceSequence
	}
	if err := reducer.acceptScope(event); err != nil {
		return err
	}
	data, err := event.DecodeData()
	if err != nil || data == nil || data.validateModel() != nil {
		return ErrReduceInvalid
	}
	if err := reducer.applyData(event, data); err != nil {
		return err
	}
	reducer.lastSequence = event.Sequence
	return nil
}

func (reducer *RunReducer) ApplyAll(events []ProtocolEvent) error {
	if reducer == nil {
		return ErrReduceInvalid
	}
	for _, event := range events {
		if err := reducer.Apply(event); err != nil {
			return err
		}
	}
	return nil
}

func (reducer *RunReducer) Snapshot() (ReducedRunSnapshot, error) {
	if reducer == nil || reducer.items == nil || reducer.interactions == nil {
		return ReducedRunSnapshot{}, ErrReduceInvalid
	}
	snapshot := ReducedRunSnapshot{LastSequence: reducer.lastSequence}
	if reducer.run != nil {
		cloned := *reducer.run
		if reducer.run.CompletedAt != nil {
			completed := *reducer.run.CompletedAt
			cloned.CompletedAt = &completed
		}
		if reducer.run.Error != nil {
			protocolError := *reducer.run.Error
			protocolError.Details = append(json.RawMessage(nil), reducer.run.Error.Details...)
			cloned.Error = &protocolError
		}
		snapshot.Run = &cloned
	}
	for _, id := range reducer.itemOrder {
		item, err := cloneReducedItem(reducer.items[id])
		if err != nil {
			return ReducedRunSnapshot{}, err
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	for _, id := range reducer.interactionOrder {
		interaction, err := cloneReducedInteraction(reducer.interactions[id])
		if err != nil {
			return ReducedRunSnapshot{}, err
		}
		snapshot.Interactions = append(snapshot.Interactions, interaction)
	}
	if reducer.usage != nil {
		usage := *reducer.usage
		snapshot.Usage = &usage
	}
	return snapshot, nil
}

func (reducer *RunReducer) acceptScope(event ProtocolEvent) error {
	values := []string{
		event.WorkspaceID, event.AgentID, event.ConversationID, event.RunID,
		event.EventStreamID, event.TraceID,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return ErrReduceScope
		}
	}
	if reducer.lastSequence == 0 {
		reducer.workspaceID, reducer.agentID = event.WorkspaceID, event.AgentID
		reducer.conversationID, reducer.runID = event.ConversationID, event.RunID
		reducer.streamID, reducer.traceID = event.EventStreamID, event.TraceID
		return nil
	}
	if event.WorkspaceID != reducer.workspaceID || event.AgentID != reducer.agentID ||
		event.ConversationID != reducer.conversationID || event.RunID != reducer.runID ||
		event.EventStreamID != reducer.streamID || event.TraceID != reducer.traceID {
		return ErrReduceScope
	}
	return nil
}

func (reducer *RunReducer) applyData(event ProtocolEvent, data EventData) error {
	switch value := data.(type) {
	case RunSnapshotData:
		return reducer.applyRun(value.Run)
	case RunWaitingData:
		return reducer.applyRun(value.Run)
	case RunResumedData:
		return reducer.applyRun(value.Run)
	case ItemSnapshotData:
		if value.Item == nil || (event.ItemID != "" && event.ItemID != value.Item.ItemID()) {
			return ErrReduceState
		}
		if _, exists := reducer.items[value.Item.ItemID()]; !exists {
			reducer.itemOrder = append(reducer.itemOrder, value.Item.ItemID())
		}
		reducer.items[value.Item.ItemID()] = value.Item
		return nil
	case ItemDeltaData:
		current, exists := reducer.items[value.ItemID]
		if !exists || (event.ItemID != "" && event.ItemID != value.ItemID) {
			return ErrReduceState
		}
		projected, changed, err := applyCurrentItemDelta(current, value.Delta)
		if err != nil {
			return ErrReduceState
		}
		if changed {
			reducer.items[value.ItemID] = projected
		}
		return nil
	case InteractionData:
		if event.InteractionID != "" && event.InteractionID != value.Interaction.ID {
			return ErrReduceState
		}
		if _, exists := reducer.interactions[value.Interaction.ID]; !exists {
			reducer.interactionOrder = append(reducer.interactionOrder, value.Interaction.ID)
		}
		reducer.interactions[value.Interaction.ID] = value.Interaction
		return nil
	case UsageData:
		if reducer.usage != nil && (value.Usage.InputTokens < reducer.usage.InputTokens ||
			value.Usage.OutputTokens < reducer.usage.OutputTokens ||
			value.Usage.TotalTokens < reducer.usage.TotalTokens || value.Usage == *reducer.usage) {
			return ErrReduceState
		}
		usage := value.Usage
		reducer.usage = &usage
		return nil
	case UnknownEventData:
		return nil
	default:
		return ErrReduceInvalid
	}
}

func (reducer *RunReducer) applyRun(run Run) error {
	if run.ID != reducer.runID || run.AgentID != reducer.agentID ||
		run.ConversationID != reducer.conversationID {
		return ErrReduceScope
	}
	if reducer.run != nil && terminalReducedRunStatus(reducer.run.Status) {
		return ErrReduceState
	}
	cloned := run
	reducer.run = &cloned
	return nil
}

func terminalReducedRunStatus(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled
}

func cloneReducedItem(item Item) (Item, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, ErrReduceInvalid
	}
	cloned, err := DecodeItem(raw)
	if err != nil {
		return nil, ErrReduceInvalid
	}
	return cloned, nil
}

func cloneReducedInteraction(interaction Interaction) (Interaction, error) {
	raw, err := json.Marshal(interaction)
	if err != nil {
		return Interaction{}, ErrReduceInvalid
	}
	var cloned Interaction
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Interaction{}, ErrReduceInvalid
	}
	return cloned, nil
}
