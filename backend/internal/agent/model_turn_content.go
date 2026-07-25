package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/storedobject"
)

type modelTurnSecureStore interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

type ModelTurnContentService struct {
	store modelTurnSecureStore
	runs  *execution.RunRepository
}

type RecordModelTurnInput struct {
	WorkspaceID    string
	StepID         string
	Content        []byte
	CreatedByType  string
	CreatedByID    string
	ExpectedStatus string
	NewStatus      string
	ErrorCode      string
	// Reasoning is optional debug-audit text merged into OutputSummary when non-empty.
	Reasoning string
}

func NewModelTurnContentService(
	store modelTurnSecureStore,
	runs *execution.RunRepository,
) (*ModelTurnContentService, error) {
	if store == nil || runs == nil {
		return nil, errors.New("model turn object store and run repository are required")
	}
	return &ModelTurnContentService{store: store, runs: runs}, nil
}

// Record saves the full model turn before recording only its audit evidence on
// the normalized AgentRunStep. StepID is reused as ObjectID, making a retry
// after a database failure converge on the same permanent object.
func (service *ModelTurnContentService) Record(
	ctx context.Context,
	input RecordModelTurnInput,
) (execution.AgentRunStep, error) {
	input.WorkspaceID, input.StepID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.StepID)
	input.CreatedByType = strings.ToUpper(strings.TrimSpace(input.CreatedByType))
	input.CreatedByID = strings.TrimSpace(input.CreatedByID)
	if len(input.Content) == 0 {
		return execution.AgentRunStep{}, ErrInvalid
	}
	digest := sha256.Sum256(input.Content)
	hash := hex.EncodeToString(digest[:])
	put := storedobject.PutInput{
		ID: input.StepID, WorkspaceID: input.WorkspaceID, Kind: storedobject.KindModelTurn,
		ContentType: "application/json", SizeBytes: int64(len(input.Content)), SHA256: hash,
		Classification: storedobject.ClassificationSensitive,
		RetentionMode:  storedobject.RetentionPermanent, CreatedByType: input.CreatedByType,
		CreatedByID: input.CreatedByID, Reader: bytes.NewReader(input.Content),
	}
	if _, err := service.store.Put(ctx, put); err != nil {
		if !errors.Is(err, storedobject.ErrConflict) {
			return execution.AgentRunStep{}, fmt.Errorf("put permanent model turn: %w", err)
		}
		opened, openErr := service.store.Open(ctx, storedobject.ReadRequest{
			WorkspaceID: input.WorkspaceID, ObjectID: input.StepID,
			ActorType: input.CreatedByType, ActorID: input.CreatedByID,
		})
		if openErr != nil {
			return execution.AgentRunStep{}, errors.Join(err, openErr)
		}
		existing, readErr := io.ReadAll(opened.Body)
		_ = opened.Body.Close()
		if readErr != nil || opened.Metadata.Kind != storedobject.KindModelTurn ||
			!bytes.Equal(existing, input.Content) {
			return execution.AgentRunStep{}, ErrConflict
		}
	}
	summaryMap := map[string]any{
		"contentSha256": hash, "contentLength": len(input.Content),
	}
	if text := strings.TrimSpace(input.Reasoning); text != "" {
		summaryMap["reasoning"] = text
	}
	summary, err := json.Marshal(summaryMap)
	if err != nil {
		return execution.AgentRunStep{}, err
	}
	return service.runs.TransitionAgentRunStep(ctx, input.WorkspaceID, input.StepID,
		execution.StepTransition{
			ExpectedStatus: input.ExpectedStatus, NewStatus: input.NewStatus,
			OutputSummary: summary, RawObjectID: input.StepID,
			RawSHA256: hash, RawLength: int64(len(input.Content)), ErrorCode: input.ErrorCode,
		})
}
