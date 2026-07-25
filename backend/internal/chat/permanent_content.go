package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"actweave/backend/internal/storedobject"
)

type chatSecureStore interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

type StoredMessageContent struct{ store chatSecureStore }

func NewStoredMessageContent(store chatSecureStore) (*StoredMessageContent, error) {
	if store == nil {
		return nil, errors.New("chat secure object store is required")
	}
	return &StoredMessageContent{store: store}, nil
}

func (store *StoredMessageContent) PutPermanentChat(
	ctx context.Context,
	input PermanentContentInput,
) (string, error) {
	digest := sha256.Sum256(input.Content)
	put := storedobject.PutInput{
		ID: strings.TrimSpace(input.ObjectID), WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		Kind: storedobject.KindChatMessage, ContentType: "text/plain; charset=utf-8",
		SizeBytes: int64(len(input.Content)), SHA256: hex.EncodeToString(digest[:]),
		Classification: storedobject.ClassificationSensitive,
		RetentionMode:  storedobject.RetentionPermanent,
		CreatedByType:  strings.ToUpper(strings.TrimSpace(input.CreatedByType)),
		CreatedByID:    strings.TrimSpace(input.CreatedByID), Reader: bytes.NewReader(input.Content),
	}
	created, err := store.store.Put(ctx, put)
	if err == nil {
		return created.ID, nil
	}
	if !errors.Is(err, storedobject.ErrConflict) {
		return "", fmt.Errorf("put permanent chat content: %w", err)
	}
	// The object write precedes the short message transaction. A retry after a
	// database failure is safe only when the existing permanent object is byte
	// identical to the requested message.
	opened, openErr := store.store.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: put.WorkspaceID, ObjectID: put.ID,
		ActorType: put.CreatedByType, ActorID: put.CreatedByID,
	})
	if openErr != nil {
		return "", errors.Join(err, openErr)
	}
	defer opened.Body.Close()
	existing, readErr := io.ReadAll(opened.Body)
	if readErr != nil {
		return "", readErr
	}
	if opened.Metadata.Kind != storedobject.KindChatMessage || !bytes.Equal(existing, input.Content) {
		return "", ErrConflict
	}
	return put.ID, nil
}

func (store *StoredMessageContent) OpenPermanentChat(
	ctx context.Context,
	workspaceID, objectID, actorType, actorID string,
) (storedobject.OpenedObject, error) {
	return store.store.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: strings.TrimSpace(workspaceID), ObjectID: strings.TrimSpace(objectID),
		ActorType: strings.ToUpper(strings.TrimSpace(actorType)), ActorID: strings.TrimSpace(actorID),
	})
}

func (store *StoredMessageContent) ReadPermanentChat(
	ctx context.Context,
	workspaceID, objectID, actorID string,
) (string, error) {
	opened, err := store.OpenPermanentChat(ctx, workspaceID, objectID, "USER", actorID)
	if err != nil {
		return "", err
	}
	defer opened.Body.Close()
	content, err := io.ReadAll(opened.Body)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
