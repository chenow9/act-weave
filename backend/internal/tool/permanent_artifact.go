package tool

import (
	"context"
	"errors"
	"strings"

	"actweave/backend/internal/storedobject"
)

type toolPayloadWriter interface {
	Write(context.Context, storedobject.SensitivePayloadInput) (storedobject.SensitivePayloadResult, error)
}

type StoredToolTestArtifacts struct{ writer toolPayloadWriter }

func NewStoredToolTestArtifacts(writer toolPayloadWriter) (*StoredToolTestArtifacts, error) {
	if writer == nil {
		return nil, errors.New("tool test payload writer is required")
	}
	return &StoredToolTestArtifacts{writer: writer}, nil
}

func (store *StoredToolTestArtifacts) WriteToolTestArtifact(
	ctx context.Context,
	artifact ToolTestArtifact,
) (string, error) {
	if artifact.RetentionMode != TestRetentionPermanent {
		return "", ErrInvalid
	}
	result, err := store.writer.Write(ctx, storedobject.SensitivePayloadInput{
		ObjectID: strings.TrimSpace(artifact.TestID), WorkspaceID: strings.TrimSpace(artifact.WorkspaceID),
		Kind: storedobject.KindToolTestPayload, Request: cloneRaw(artifact.Request),
		Response: cloneRaw(artifact.Response), ErrorCode: strings.TrimSpace(artifact.ErrorCode),
		CreatedByType: storedobject.CreatorUser, CreatedByID: strings.TrimSpace(artifact.TestedBy),
	})
	if err != nil {
		return "", err
	}
	return result.ObjectID, nil
}
