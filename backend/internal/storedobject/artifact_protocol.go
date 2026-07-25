package storedobject

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"strings"
	"time"

	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

var (
	ErrArtifactProtocolInvalid = errors.New("stored object artifact protocol item is invalid")
	ErrArtifactKindUnsupported = errors.New("stored object kind is not a public artifact")
)

var publicArtifactKinds = map[string]struct{}{
	KindPromptRunOutput: {},
	KindAuditExport:     {},
}

var publicArtifactMediaTypes = map[string]struct{}{
	"application/json": {}, "application/pdf": {}, "application/zip": {},
	"text/plain": {}, "text/csv": {},
	"image/png": {}, "image/jpeg": {}, "image/webp": {}, "image/gif": {},
}

type ArtifactProtocolMapper struct {
	authorizer ReadAuthorizer
	validator  *protocolevent.PayloadValidator
	now        func() time.Time
}

func NewArtifactProtocolMapper(authorizer ReadAuthorizer) (*ArtifactProtocolMapper, error) {
	if authorizer == nil {
		return nil, ErrArtifactProtocolInvalid
	}
	return &ArtifactProtocolMapper{
		authorizer: authorizer, validator: protocolevent.MustDefaultPayloadValidator(),
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// MapCompleted exposes only an independently authorized object reference and
// media type. Bucket/key, encryption metadata, classification, and signed URLs
// remain behind the StoredObject resource authorization boundary.
func (mapper *ArtifactProtocolMapper) MapCompleted(
	ctx context.Context,
	object StoredObject,
	actorType, actorID string,
) (protocolevent.ArtifactItem, error) {
	if mapper == nil || mapper.authorizer == nil || mapper.validator == nil ||
		!validArtifactFact(object, mapper.now().UTC()) {
		return protocolevent.ArtifactItem{}, ErrArtifactProtocolInvalid
	}
	if _, allowed := publicArtifactKinds[object.Kind]; !allowed {
		return protocolevent.ArtifactItem{}, ErrArtifactKindUnsupported
	}
	mediaType, _, err := mime.ParseMediaType(object.ContentType)
	if err != nil {
		return protocolevent.ArtifactItem{}, ErrArtifactProtocolInvalid
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if _, allowed := publicArtifactMediaTypes[mediaType]; !allowed {
		return protocolevent.ArtifactItem{}, ErrArtifactKindUnsupported
	}
	if err := mapper.authorizer.AuthorizeStoredObjectRead(ctx, ReadAuthorization{
		WorkspaceID: object.WorkspaceID, ObjectID: object.ID,
		ActorType: strings.ToUpper(strings.TrimSpace(actorType)), ActorID: strings.TrimSpace(actorID),
		Classification: object.Classification, Kind: object.Kind,
	}); err != nil {
		return protocolevent.ArtifactItem{}, err
	}
	item := protocolevent.ArtifactItem{
		ID: object.ID, Type: protocolevent.ItemTypeArtifact,
		Status: protocolevent.ItemStatusCompleted, ArtifactID: object.ID, MediaType: mediaType,
	}
	data, err := json.Marshal(protocolevent.ItemSnapshotData{Item: item})
	if err != nil || mapper.validator.ValidateEventData(protocolevent.EventItemCompleted, data) != nil {
		return protocolevent.ArtifactItem{}, ErrArtifactProtocolInvalid
	}
	return item, nil
}

type ArtifactProtocolContext struct {
	Scope         protocolevent.RunScope
	EventStreamID string
	TraceID       string
}

type ProjectArtifactInput struct {
	Context   ArtifactProtocolContext
	Object    StoredObject
	ActorType string
	ActorID   string
	Ordinal   int
}

type ArtifactProjectionResult struct {
	Projection  protocolevent.RunItemProjection
	Events      []protocolevent.ProtocolEvent
	NotifyError error
}

type ArtifactProtocolProjector struct {
	unit   *protocolevent.ProtocolUnitOfWork
	mapper *ArtifactProtocolMapper
}

func NewArtifactProtocolProjector(
	unit *protocolevent.ProtocolUnitOfWork,
	mapper *ArtifactProtocolMapper,
) (*ArtifactProtocolProjector, error) {
	if unit == nil || mapper == nil {
		return nil, ErrArtifactProtocolInvalid
	}
	return &ArtifactProtocolProjector{unit: unit, mapper: mapper}, nil
}

func (projector *ArtifactProtocolProjector) ProjectCompleted(
	ctx context.Context,
	input ProjectArtifactInput,
) (ArtifactProjectionResult, error) {
	if projector == nil || projector.unit == nil || projector.mapper == nil || input.Ordinal < 1 ||
		validateArtifactContext(input.Context, input.Object) != nil {
		return ArtifactProjectionResult{}, ErrArtifactProtocolInvalid
	}
	item, err := projector.mapper.MapCompleted(ctx, input.Object, input.ActorType, input.ActorID)
	if err != nil {
		return ArtifactProjectionResult{}, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return ArtifactProjectionResult{}, err
	}
	event, err := protocolevent.BuildProtocolEvent(protocolevent.NewProtocolEvent{
		ID: eventID.String(), EventStreamID: input.Context.EventStreamID,
		WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
		ConversationID: input.Context.Scope.ConversationID, RunID: input.Context.Scope.RunID,
		Type: protocolevent.EventItemCompleted, SpecVersion: "1.0", TraceID: input.Context.TraceID,
		ItemID: item.ID, OccurredAt: input.Object.CreatedAt,
	}, protocolevent.ItemSnapshotData{Item: item})
	if err != nil {
		return ArtifactProjectionResult{}, err
	}
	started := item
	started.Status = protocolevent.ItemStatusInProgress
	var projection protocolevent.RunItemProjection
	result, err := projector.unit.Execute(ctx, func(ctx context.Context, transaction *protocolevent.ProtocolTransaction) error {
		if _, err := transaction.EnsureRunEventStream(ctx, input.Context.EventStreamID, input.Context.Scope); err != nil {
			return err
		}
		if _, err := transaction.CreateRunItem(ctx, protocolevent.CreateRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Ordinal: input.Ordinal,
			SourceType: protocolevent.SourceStoredObject, SourceID: input.Object.ID,
			Item: started, StartedAt: input.Object.CreatedAt,
		}); err != nil {
			return err
		}
		projection, err = transaction.CompleteRunItem(ctx, protocolevent.CompleteRunItemInput{
			WorkspaceID: input.Context.Scope.WorkspaceID, AgentID: input.Context.Scope.AgentID,
			RunID: input.Context.Scope.RunID, Item: item, CompletedAt: input.Object.CreatedAt,
		})
		if err != nil {
			return err
		}
		_, err = transaction.Append(ctx, []protocolevent.NewProtocolEvent{event})
		return err
	})
	if err != nil {
		return ArtifactProjectionResult{}, err
	}
	return ArtifactProjectionResult{
		Projection: projection, Events: result.Events, NotifyError: result.NotifyError,
	}, nil
}

func validArtifactFact(object StoredObject, now time.Time) bool {
	if !validUUID(object.ID) || !validUUID(object.WorkspaceID) || !validUUID(object.CreatedByID) ||
		!validKind(object.Kind) || !validClassification(object.Classification) ||
		!validRetentionMode(object.RetentionMode) || !validHash(object.SHA256) ||
		strings.TrimSpace(object.ContentType) == "" || object.SizeBytes < 0 || object.CreatedAt.IsZero() {
		return false
	}
	if object.RetentionMode == RetentionExpiring {
		return object.RetentionUntil != nil && object.RetentionUntil.After(now)
	}
	return object.RetentionUntil == nil
}

func validateArtifactContext(protocolContext ArtifactProtocolContext, object StoredObject) error {
	for _, value := range []string{
		protocolContext.Scope.WorkspaceID, protocolContext.Scope.AgentID,
		protocolContext.Scope.ConversationID, protocolContext.Scope.RunID,
		protocolContext.EventStreamID,
	} {
		if !validUUID(strings.TrimSpace(value)) {
			return ErrArtifactProtocolInvalid
		}
	}
	if strings.TrimSpace(protocolContext.TraceID) == "" || object.WorkspaceID != protocolContext.Scope.WorkspaceID {
		return ErrArtifactProtocolInvalid
	}
	return nil
}
