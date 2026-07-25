package agent

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

	"github.com/google/uuid"
)

type promptSecureStore interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

// StoredPromptObjectStore adapts the encrypted object store to PromptService.
// Prompt input and output are always sensitive, permanent objects.
type StoredPromptObjectStore struct{ store promptSecureStore }

func NewStoredPromptObjectStore(store promptSecureStore) (*StoredPromptObjectStore, error) {
	if store == nil {
		return nil, errors.New("prompt secure object store is required")
	}
	return &StoredPromptObjectStore{store: store}, nil
}

func (store *StoredPromptObjectStore) PutPermanent(
	ctx context.Context,
	workspaceID, kind string,
	content []byte,
	createdBy string,
) (string, error) {
	objectKind, err := promptObjectKind(kind)
	if err != nil || len(content) == 0 {
		return "", ErrInvalid
	}
	objectID, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	created, err := store.store.Put(ctx, storedobject.PutInput{
		ID: objectID.String(), WorkspaceID: strings.TrimSpace(workspaceID), Kind: objectKind,
		ContentType: "text/plain; charset=utf-8", SizeBytes: int64(len(content)),
		SHA256: hex.EncodeToString(digest[:]), Classification: storedobject.ClassificationSensitive,
		RetentionMode: storedobject.RetentionPermanent, CreatedByType: storedobject.CreatorUser,
		CreatedByID: strings.TrimSpace(createdBy), Reader: bytes.NewReader(content),
	})
	if err != nil {
		return "", fmt.Errorf("put permanent prompt object: %w", err)
	}
	return created.ID, nil
}

func (store *StoredPromptObjectStore) GetPermanent(
	ctx context.Context,
	workspaceID, objectID, actorID string,
) ([]byte, error) {
	opened, err := store.store.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: strings.TrimSpace(workspaceID), ObjectID: strings.TrimSpace(objectID),
		ActorType: storedobject.CreatorUser, ActorID: strings.TrimSpace(actorID),
	})
	if err != nil {
		return nil, err
	}
	defer opened.Body.Close()
	if opened.Metadata.Kind != storedobject.KindPromptRunOutput {
		return nil, ErrConflict
	}
	content, err := io.ReadAll(opened.Body)
	if err != nil {
		return nil, fmt.Errorf("read permanent prompt object: %w", err)
	}
	return content, nil
}

func promptObjectKind(kind string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "PROMPT_INPUT", storedobject.KindPromptRunInput:
		return storedobject.KindPromptRunInput, nil
	case "PROMPT_OUTPUT", storedobject.KindPromptRunOutput:
		return storedobject.KindPromptRunOutput, nil
	default:
		return "", ErrInvalid
	}
}
