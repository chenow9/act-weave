package chatruntimebridge

import (
	"context"
	"log/slog"
	"strings"

	"actweave/backend/internal/a2ui"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/protocolevent"
)

func (b *Bridge) snapshotOutboundFiles(ctx context.Context, workspaceID, agentID, runID string) []protocolevent.OutputFileContentPart {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if collector := b.outboundCollector(workspaceID, runID); collector != nil {
		files, err := collector.Snapshot(ctx)
		if err != nil {
			b.observeOutboundSnapshotFail(workspaceID, runID, err)
			return nil
		}
		return filesToOutputParts(files)
	}
	if b.files == nil {
		return nil
	}
	files, err := b.files.ListGeneratedForRun(ctx, workspaceID, agentID, runID)
	if err != nil {
		b.observeOutboundSnapshotFail(workspaceID, runID, err)
		return nil
	}
	return filesToOutputParts(files)
}

func (b *Bridge) observeOutboundSnapshotFail(workspaceID, runID string, err error) {
	metrics.Default().ObserveOutboundSnapshotFail()
	logger := slog.Default()
	if b != nil && b.logger != nil {
		logger = b.logger
	}
	logger.Error("outbound file snapshot failed",
		"event", "chatruntimebridge.outbound.snapshot_failed",
		"workspace_id", workspaceID,
		"run_id", runID,
		"error", err.Error(),
	)
}

func filesToOutputParts(files []aapfile.File) []protocolevent.OutputFileContentPart {
	out := make([]protocolevent.OutputFileContentPart, 0, len(files))
	for _, file := range files {
		filename := ""
		if file.Filename != nil {
			filename = *file.Filename
		}
		cleaned, err := protocolevent.AllowlistedOutputFile(protocolevent.OutputFileContentPart{
			Type:      protocolevent.ContentPartTypeOutputFile,
			FileID:    file.ID,
			MediaType: file.DeclaredMediaType,
			Filename:  filename,
			SizeBytes: file.SizeBytes,
		})
		if err != nil {
			continue
		}
		out = append(out, cleaned)
	}
	return out
}

// attachOutboundFiles merges allowlisted output_file parts into terminal content.
// Preflight failure degrades to the incoming text/A2UI body (no files).
func attachOutboundFiles(content string, files []protocolevent.OutputFileContentPart) (string, []protocolevent.OutputFileContentPart) {
	if len(files) == 0 {
		return content, nil
	}
	text, payload := splitAssistantTextAndA2UI(content)
	next, err := chat.SerializeAssistantDurableV2(text, files, payload)
	if err != nil || strings.TrimSpace(next) == "" {
		return content, nil
	}
	if err := preflightAssistantItem("", next); err != nil {
		metrics.Default().ObserveOutboundAttachPreflightFail()
		return content, nil
	}
	return next, files
}

func preflightAssistantItem(messageID, durable string) error {
	return preflightAssistantA2UIItem(messageID, durable)
}

func splitAssistantTextAndA2UI(content string) (string, *a2ui.Payload) {
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	parts, err := chat.ParseMessageContentParts(content)
	if err != nil {
		return chat.JoinTextPartsFromDurable(content), nil
	}
	// Legacy/plain: a single text part whose text is the whole body.
	if !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return content, nil
	}
	text := chat.JoinTextPartsFromDurable(content)
	var payload *a2ui.Payload
	for _, part := range parts {
		a2uiPart, ok := part.(protocolevent.A2UIContentPart)
		if !ok || len(a2uiPart.Surface) == 0 {
			continue
		}
		payload = &a2ui.Payload{
			Version:   a2uiPart.Version,
			CatalogID: a2uiPart.CatalogID,
			Surface:   append([]byte(nil), a2uiPart.Surface...),
		}
		break
	}
	return text, payload
}

func (b *Bridge) promoteOutboundFiles(ctx context.Context, workspaceID string, files []protocolevent.OutputFileContentPart) {
	if b == nil || b.files == nil || len(files) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, file := range files {
		_ = b.files.PromoteRetentionOnReference(ctx, workspaceID, file.FileID)
	}
}
