package toolruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// PlatformFileMeta is non-secret metadata for an in-process platform tool open.
// No download URLs are ever generated on this path (design §5.9.2).
type PlatformFileMeta struct {
	FileID          string
	WorkspaceID     string
	AgentID         string
	Status          string
	DeclaredMedia   string
	DetectedMedia   string
	SizeBytes       int64
	StoredObjectID  string
}

// PlatformOpenedFile is a plaintext body stream opened via SecureStore.
// Callers must close Body.
type PlatformOpenedFile struct {
	Meta PlatformFileMeta
	Body io.ReadCloser
}

// PlatformFileOpener opens READY AAP files for same-process platform tools
// without minting download tokens or URLs (KD-9 / KD-22).
type PlatformFileOpener interface {
	OpenReadyFile(ctx context.Context, workspaceID, fileID string) (PlatformOpenedFile, error)
}

// ErrPlatformFileNotReady is returned when the file exists but is not READY.
var ErrPlatformFileNotReady = errors.New("platform file is not ready")

// ErrPlatformFileNotFound is returned when the file is not visible in the workspace.
var ErrPlatformFileNotFound = errors.New("platform file not found")

// PlatformFileAccess is a concrete PlatformFileOpener built from injected deps.
// Used by application wiring and unit tests with fakes.
type PlatformFileAccess struct {
	// GetFile loads file metadata by workspace + file id.
	GetFile func(ctx context.Context, workspaceID, fileID string) (PlatformFileMeta, error)
	// OpenObject opens a permanent stored object body (SecureStore.Open).
	OpenObject func(ctx context.Context, workspaceID, storedObjectID string) (io.ReadCloser, error)
}

// OpenReadyFile validates READY + stored object, then opens via SecureStore.
// It never mints tokens or constructs download URLs.
func (a *PlatformFileAccess) OpenReadyFile(
	ctx context.Context,
	workspaceID, fileID string,
) (PlatformOpenedFile, error) {
	if a == nil || a.GetFile == nil || a.OpenObject == nil {
		return PlatformOpenedFile{}, errors.New("platform file access unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fileID = strings.TrimSpace(fileID)
	if workspaceID == "" || fileID == "" {
		return PlatformOpenedFile{}, ErrPlatformFileNotFound
	}
	meta, err := a.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		if errors.Is(err, ErrPlatformFileNotFound) {
			return PlatformOpenedFile{}, ErrPlatformFileNotFound
		}
		return PlatformOpenedFile{}, err
	}
	if strings.TrimSpace(meta.WorkspaceID) != "" && meta.WorkspaceID != workspaceID {
		return PlatformOpenedFile{}, ErrPlatformFileNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Status), "READY") {
		return PlatformOpenedFile{}, ErrPlatformFileNotReady
	}
	objectID := strings.TrimSpace(meta.StoredObjectID)
	if objectID == "" {
		return PlatformOpenedFile{}, ErrPlatformFileNotReady
	}
	body, err := a.OpenObject(ctx, workspaceID, objectID)
	if err != nil {
		return PlatformOpenedFile{}, fmt.Errorf("open platform file object: %w", err)
	}
	meta.FileID = fileID
	meta.WorkspaceID = workspaceID
	return PlatformOpenedFile{Meta: meta, Body: body}, nil
}
