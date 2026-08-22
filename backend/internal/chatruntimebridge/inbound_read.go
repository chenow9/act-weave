package chatruntimebridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func (b *Bridge) maybeInjectInboundRead(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	tools []tool.BaseTool,
	frozenCaps []chatruntime.SnapshotCapability,
	cfg modelconfig.Config,
) ([]tool.BaseTool, map[string]catalogEntryFlags) {
	calling := frozenToolCalling(cfg)
	if outboundNameTaken(ctx, tools, frozenCaps, aapfile.ReadAttachmentToolName) {
		if b.shouldInjectInboundRead(run, calling) {
			metrics.Default().ObserveInboundRead("denied")
		}
		return tools, nil
	}
	if !toolCallingSupportsOutbound(calling) {
		if sessioncontext.EnableInboundReadFromSnapshot(run.ContextPolicySnapshot) &&
			b.inboundGatesOpen(run) {
			metrics.Default().ObserveInboundRead("unsupported")
		}
		return tools, nil
	}
	if !b.shouldInjectInboundRead(run, calling) {
		return tools, nil
	}
	if b.fileOpener == nil {
		return tools, nil
	}
	readable, err := b.conversationInboundFileIDs(ctx, job, run)
	if err != nil {
		logger := slog.Default()
		if b.logger != nil {
			logger = b.logger
		}
		logger.Error("inbound readable set failed; skip inject",
			"event", "chatruntimebridge.inbound.readable_failed",
			"workspace_id", run.WorkspaceID,
			"run_id", run.ID,
			"error", err.Error(),
		)
		return tools, nil
	}
	inner, err := aapfile.NewReadAttachmentTool(aapfile.ReadAttachmentConfig{
		Opener:          b.fileOpener,
		ReadableFileIDs: readable,
		WorkspaceID:     run.WorkspaceID,
		AgentID:         run.AgentID,
		MaxBytes:        b.outboundMaxBytes(),
	})
	if err != nil {
		if b.logger != nil {
			b.logger.Error("read attachment tool construct failed",
				"event", "chatruntimebridge.inbound.tool_construct_failed",
				"workspace_id", run.WorkspaceID,
				"run_id", run.ID,
				"error", err.Error(),
			)
		}
		return tools, nil
	}
	wrapped := &readAttachmentBridgeTool{
		inner:  inner,
		bridge: b,
		job:    job,
		run:    run,
	}
	flags := map[string]catalogEntryFlags{
		aapfile.ReadAttachmentToolName: {
			Exposure:        einoruntime.ToolExposureImmediate,
			PlatformControl: true,
		},
	}
	return append(tools, wrapped), flags
}

func (b *Bridge) shouldInjectInboundRead(run execution.AgentRun, toolCalling string) bool {
	if b == nil || b.fileOpener == nil || !b.inboundGatesOpen(run) {
		return false
	}
	if !sessioncontext.EnableInboundReadFromSnapshot(run.ContextPolicySnapshot) {
		return false
	}
	if !toolCallingSupportsOutbound(toolCalling) {
		return false
	}
	_, _, _, ok := aapPrincipalFromRun(run)
	return ok
}

func (b *Bridge) inboundGatesOpen(run execution.AgentRun) bool {
	if b == nil || b.filesCfg == nil {
		return false
	}
	cfg := *b.filesCfg
	if !cfg.Enabled || !cfg.RuntimeInboundRead {
		return false
	}
	if !cfg.AllowsWorkspace(run.WorkspaceID) {
		return false
	}
	return cfg.AllowsClient(strings.TrimSpace(run.PrincipalSnapshot.ClientID))
}

func shouldAppendInboundReadPrompt(flags map[string]catalogEntryFlags) bool {
	return flags[aapfile.ReadAttachmentToolName].PlatformControl
}

func mergeCatalogFlags(dst, src map[string]catalogEntryFlags) map[string]catalogEntryFlags {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		out := make(map[string]catalogEntryFlags, len(src))
		for k, v := range src {
			out[k] = v
		}
		return out
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (b *Bridge) conversationInboundFileIDs(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	addFromContent := func(content string) {
		for _, att := range chat.FileAttachmentsFromDurable(content) {
			if att.Type != string(protocolevent.ContentPartTypeInputFile) {
				continue
			}
			id := strings.ToLower(strings.TrimSpace(att.FileID))
			if id != "" {
				ids[id] = struct{}{}
			}
		}
	}
	if b != nil && b.sessions != nil {
		msgs, err := b.sessions.ListMessages(ctx, run.WorkspaceID, run.SessionID)
		if err != nil {
			return nil, err
		}
		for _, msg := range msgs {
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "USER") {
				continue
			}
			content, err := b.resolveMessageContent(ctx, job, msg)
			if err != nil {
				return nil, err
			}
			addFromContent(content)
		}
	}
	return ids, nil
}

type readAttachmentBridgeTool struct {
	inner  *aapfile.ReadAttachmentTool
	bridge *Bridge
	job    agentrun.Job
	run    execution.AgentRun
}

var _ tool.InvokableTool = (*readAttachmentBridgeTool)(nil)

func (t *readAttachmentBridgeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.inner == nil {
		return nil, aapfile.ErrInvalid
	}
	return t.inner.Info(ctx)
}

func (t *readAttachmentBridgeTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	opts ...tool.Option,
) (string, error) {
	startedAt := time.Now().UTC()
	result, err := t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	finishedAt := time.Now().UTC()
	args := aapfile.AllowlistedReadArgs(argumentsInJSON)
	ok, errorCode := aapfile.ParseReadResultStatus(result)
	allowlisted := stripReadResult(result)
	if ok {
		if inboundResultWarning(allowlisted) == "NO_TEXT_LAYER" {
			metrics.Default().ObserveInboundRead("no_text")
		} else {
			metrics.Default().ObserveInboundRead("ok")
		}
	} else if errorCode == aapfile.ErrorCodeFeatureDisabled {
		metrics.Default().ObserveInboundRead("disabled")
	} else if errorCode == aapfile.ErrorCodeMediaTypeDenied {
		metrics.Default().ObserveInboundRead("denied")
	} else {
		metrics.Default().ObserveInboundRead("error")
	}

	invocationID, idErr := uuid.NewV7()
	if idErr != nil {
		return result, err
	}
	t.projectAndAudit(ctx, invocationID.String(), args, allowlisted, ok, errorCode, startedAt, finishedAt)
	return result, err
}

func (t *readAttachmentBridgeTool) projectAndAudit(
	ctx context.Context,
	invocationID string,
	args, result json.RawMessage,
	ok bool,
	errorCode string,
	startedAt, finishedAt time.Time,
) {
	if t == nil || t.bridge == nil {
		return
	}
	logger := t.bridge.logger
	if logger == nil {
		logger = slog.Default()
	}
	if t.bridge.platformCalls != nil {
		if projErr := t.bridge.platformCalls.ProjectPlatformToolCall(ctx, chatruntime.ProjectPlatformToolCallInput{
			Run: t.run, Job: t.job, Name: aapfile.ReadAttachmentToolName,
			InvocationID: invocationID, Args: args, Result: result,
			OK: ok, ErrorCode: errorCode, StartedAt: startedAt, FinishedAt: finishedAt,
		}); projErr != nil {
			logger.Error("platform read tool_call projection failed",
				"event", "chatruntimebridge.inbound.project_failed",
				"workspace_id", t.run.WorkspaceID,
				"run_id", t.run.ID,
				"error", projErr.Error(),
			)
		}
	}
	if recErr := t.bridge.recordToolStep(ctx, einoruntime.ToolCompleteEvent{
		WorkspaceID:  t.run.WorkspaceID,
		AgentRunID:   t.run.ID,
		InvocationID: invocationID,
		ToolName:     aapfile.ReadAttachmentToolName,
		ArgsJSON:     string(args),
		ResultJSON:   string(result),
		OK:           ok,
		ErrorCode:    errorCode,
	}); recErr != nil {
		logger.Error("platform read TOOL step failed",
			"event", "chatruntimebridge.inbound.tool_step_failed",
			"workspace_id", t.run.WorkspaceID,
			"run_id", t.run.ID,
			"error", recErr.Error(),
		)
	}
}

func stripReadResult(raw string) json.RawMessage {
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil || body == nil {
		return json.RawMessage(`{"ok":false}`)
	}
	out := aapfile.AllowlistedReadResult(body)
	encoded, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{"ok":false}`)
	}
	return encoded
}

func inboundResultWarning(raw json.RawMessage) string {
	var body struct {
		Warning string `json:"warning"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return strings.TrimSpace(body.Warning)
}
