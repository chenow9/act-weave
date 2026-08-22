package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/storedobject"
)

// Stable SYSTEM actor for model-assembly SecureStore Open (must be a UUID).
// Distinct from the download-token proxy actor; still uses the trusted SYSTEM
// path on AAPFileObjectAuthorizer (in-process after createRun file.read).
const multimodalAssemblySystemActorID = "019f0000-0000-7000-8000-00000000f108"

// multimodalFileSource adapts aapfile.Service + SecureStore to chatruntime.
type multimodalFileSource struct {
	domain *aapfile.Service
	open   secureObjectOpener
}

type secureObjectOpener interface {
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

func newMultimodalFileSource(domain *aapfile.Service, open secureObjectOpener) *multimodalFileSource {
	if domain == nil || open == nil {
		return nil
	}
	return &multimodalFileSource{domain: domain, open: open}
}

func (s *multimodalFileSource) GetFile(
	ctx context.Context,
	workspaceID, fileID string,
) (chatruntime.MultimodalFileMeta, error) {
	if s == nil || s.domain == nil {
		return chatruntime.MultimodalFileMeta{}, errors.New("multimodal file source unavailable")
	}
	file, err := s.domain.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return chatruntime.MultimodalFileMeta{}, err
	}
	meta := chatruntime.MultimodalFileMeta{
		ID: file.ID, WorkspaceID: file.WorkspaceID, AgentID: file.AgentID,
		Status: file.Status, DeclaredMediaType: file.DeclaredMediaType,
		SizeBytes: file.SizeBytes,
	}
	if file.Filename != nil {
		meta.Filename = strings.TrimSpace(*file.Filename)
	}
	if file.StoredObjectID != nil {
		meta.StoredObjectID = strings.TrimSpace(*file.StoredObjectID)
	}
	if file.DetectedMediaType != nil {
		meta.DetectedMediaType = strings.TrimSpace(*file.DetectedMediaType)
	}
	return meta, nil
}

func (s *multimodalFileSource) OpenFileBytes(
	ctx context.Context,
	workspaceID, storedObjectID string,
) ([]byte, error) {
	if s == nil || s.open == nil {
		return nil, errors.New("multimodal file opener unavailable")
	}
	opened, err := s.open.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ObjectID:    strings.TrimSpace(storedObjectID),
		ActorType:   storedobject.CreatorSystem,
		ActorID:     multimodalAssemblySystemActorID,
	})
	if err != nil {
		return nil, fmt.Errorf("open aap file object: %w", err)
	}
	defer opened.Body.Close()
	// Cap at default max + 1 so the assembler can reject oversized bodies.
	limited := io.LimitReader(opened.Body, aapfile.DefaultMaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Ensure compile-time interface satisfaction.
var _ chatruntime.MultimodalFileSource = (*multimodalFileSource)(nil)
