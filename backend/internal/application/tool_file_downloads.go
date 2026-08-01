package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/storedobject"
	"actweave/backend/internal/toolruntime"
)

// Stable SYSTEM actor for platform-tool SecureStore Open (must be a UUID).
// Distinct from multimodal assembly and download-token proxy actors.
const platformToolFileSystemActorID = "019f0000-0000-7000-8000-00000000f109"

// aapToolFileMinter adapts aapfile.Service to toolruntime.ToolFileTokenMinter.
// Mints purpose=tool_invoke single_use tokens for READY files (IC-09 / KD-22).
type aapToolFileMinter struct {
	domain *aapfile.Service
}

func newAAPToolFileMinter(domain *aapfile.Service) *aapToolFileMinter {
	if domain == nil {
		return nil
	}
	return &aapToolFileMinter{domain: domain}
}

// MintToolInvokeToken validates workspace visibility + READY, then mints an
// opaque tool_invoke token. Never returns MinIO/presign URLs.
func (m *aapToolFileMinter) MintToolInvokeToken(
	ctx context.Context,
	workspaceID, fileID, createdBy string,
) (string, error) {
	if m == nil || m.domain == nil {
		return "", errors.New("tool file minter unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	fileID = strings.TrimSpace(fileID)
	createdBy = strings.TrimSpace(createdBy)
	if workspaceID == "" || fileID == "" {
		return "", aapfile.ErrInvalid
	}
	if createdBy == "" {
		createdBy = "system:tool-invoke"
	}
	file, err := m.domain.GetFile(ctx, workspaceID, fileID)
	if err != nil {
		return "", err
	}
	if file.WorkspaceID != workspaceID {
		return "", aapfile.ErrNotFound
	}
	if file.Status != aapfile.StatusReady || file.StoredObjectID == nil ||
		strings.TrimSpace(*file.StoredObjectID) == "" {
		return "", aapfile.ErrNotReady
	}
	minted, err := m.domain.MintDownloadToken(ctx, aapfile.MintDownloadTokenInput{
		Scope: aapfile.Scope{
			WorkspaceID: file.WorkspaceID,
			AgentID:     file.AgentID,
		},
		FileID:    file.ID,
		Purpose:   aapfile.DownloadPurposeToolInvoke,
		SingleUse: true,
		CreatedBy: createdBy,
		TTL:       aapfile.DefaultToolInvokeTokenTTL,
	})
	if err != nil {
		return "", err
	}
	return minted.Token.ID, nil
}

// Ensure compile-time interface satisfaction.
var _ toolruntime.ToolFileTokenMinter = (*aapToolFileMinter)(nil)

// newPlatformToolFileAccess builds SecureStore open for in-process platform tools.
// No download URLs are generated on this path (design §5.9.2).
func newPlatformToolFileAccess(
	domain *aapfile.Service,
	open secureObjectOpener,
) *toolruntime.PlatformFileAccess {
	if domain == nil || open == nil {
		return nil
	}
	return &toolruntime.PlatformFileAccess{
		GetFile: func(ctx context.Context, workspaceID, fileID string) (toolruntime.PlatformFileMeta, error) {
			file, err := domain.GetFile(ctx, workspaceID, fileID)
			if err != nil {
				if errors.Is(err, aapfile.ErrNotFound) {
					return toolruntime.PlatformFileMeta{}, toolruntime.ErrPlatformFileNotFound
				}
				return toolruntime.PlatformFileMeta{}, err
			}
			meta := toolruntime.PlatformFileMeta{
				FileID: file.ID, WorkspaceID: file.WorkspaceID, AgentID: file.AgentID,
				Status: file.Status, DeclaredMedia: file.DeclaredMediaType,
				SizeBytes: file.SizeBytes,
			}
			if file.StoredObjectID != nil {
				meta.StoredObjectID = strings.TrimSpace(*file.StoredObjectID)
			}
			if file.DetectedMediaType != nil {
				meta.DetectedMedia = strings.TrimSpace(*file.DetectedMediaType)
			}
			return meta, nil
		},
		OpenObject: func(ctx context.Context, workspaceID, storedObjectID string) (io.ReadCloser, error) {
			opened, err := open.Open(ctx, storedobject.ReadRequest{
				WorkspaceID: strings.TrimSpace(workspaceID),
				ObjectID:    strings.TrimSpace(storedObjectID),
				ActorType:   storedobject.CreatorSystem,
				ActorID:     platformToolFileSystemActorID,
			})
			if err != nil {
				return nil, fmt.Errorf("secure open aap file: %w", err)
			}
			return opened.Body, nil
		},
	}
}
