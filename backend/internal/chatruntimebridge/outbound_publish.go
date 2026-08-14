package chatruntimebridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type catalogEntryFlags struct {
	Exposure        string
	PlatformControl bool
}

// outboundFileService is the ingest / list / promote surface used by the runtime.
type outboundFileService interface {
	IngestGenerated(ctx context.Context, in aapfile.IngestGeneratedInput) (aapfile.File, error)
	ListGeneratedForRun(ctx context.Context, workspaceID, agentID, runID string) ([]aapfile.File, error)
	PromoteRetentionOnReference(ctx context.Context, workspaceID, fileID string) error
}

func (b *Bridge) maybeInjectOutboundPublish(
	ctx context.Context,
	job agentrun.Job,
	run execution.AgentRun,
	tools []tool.BaseTool,
	frozenCaps []chatruntime.SnapshotCapability,
	cfg modelconfig.Config,
) ([]tool.BaseTool, map[string]catalogEntryFlags) {
	calling := frozenToolCalling(cfg)
	// Injecting into a previously empty FC catalog makes disclosure platform_bounded
	// and requires runtime.toolDisclosure (fail-closed if that rollout is off).
	if outboundNameTaken(ctx, tools, frozenCaps, aapfile.PublishAttachmentToolName) {
		if b.shouldInjectOutboundPublish(run, calling) {
			metrics.Default().ObserveOutboundPublish("denied")
		}
		return tools, nil
	}
	if !toolCallingSupportsOutbound(calling) {
		if sessioncontext.EnableOutboundAttachmentsFromSnapshot(run.ContextPolicySnapshot) &&
			b.outboundGatesOpen(run) {
			metrics.Default().ObserveOutboundPublish("unsupported")
		}
		return tools, nil
	}
	if !b.shouldInjectOutboundPublish(run, calling) {
		return tools, nil
	}
	collector := aapfile.NewOutboundCollector(b.files, run.WorkspaceID, run.AgentID, run.ID, b.outboundMaxBytes())
	if err := collector.RebuildFromDB(ctx); err != nil {
		logger := slog.Default()
		if b != nil && b.logger != nil {
			logger = b.logger
		}
		logger.Error("outbound collector rebuild failed; skip inject",
			"event", "chatruntimebridge.outbound.collector_rebuild_failed",
			"workspace_id", run.WorkspaceID,
			"run_id", run.ID,
			"error", err.Error(),
		)
		return tools, nil
	}
	principalToken, clientID, policyVer, ok := aapPrincipalFromRun(run)
	if !ok {
		return tools, nil
	}
	inner, err := aapfile.NewPublishAttachmentTool(aapfile.PublishAttachmentConfig{
		Ingest:             b.files,
		Collector:          collector,
		Scope:              aapfile.Scope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID},
		Principal:          principalToken,
		ClientID:           clientID,
		AgentPolicyVersion: policyVer,
		SourceRunID:        run.ID,
	})
	if err != nil {
		if b.logger != nil {
			b.logger.Error("publish attachment tool construct failed",
				"event", "chatruntimebridge.outbound.tool_construct_failed",
				"workspace_id", run.WorkspaceID,
				"run_id", run.ID,
				"error", err.Error(),
			)
		}
		return tools, nil
	}
	wrapped := &publishAttachmentBridgeTool{
		inner:  inner,
		bridge: b,
		job:    job,
		run:    run,
	}
	flags := map[string]catalogEntryFlags{
		aapfile.PublishAttachmentToolName: {
			Exposure:        einoruntime.ToolExposureImmediate,
			PlatformControl: true,
		},
	}
	if b != nil {
		b.rememberOutboundCollector(run.WorkspaceID, run.ID, collector)
	}
	return append(tools, wrapped), flags
}

func outboundNameTaken(
	ctx context.Context,
	tools []tool.BaseTool,
	frozenCaps []chatruntime.SnapshotCapability,
	name string,
) bool {
	for _, cap := range frozenCaps {
		if strings.TrimSpace(cap.CallableName) == name {
			return true
		}
	}
	for _, existing := range tools {
		if existing == nil {
			continue
		}
		info, err := existing.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if strings.TrimSpace(info.Name) == name {
			return true
		}
	}
	return false
}

func shouldAppendOutboundPrompt(flags map[string]catalogEntryFlags) bool {
	return flags[aapfile.PublishAttachmentToolName].PlatformControl
}

func outboundCollectorKey(workspaceID, runID string) string {
	return strings.TrimSpace(workspaceID) + "/" + strings.TrimSpace(runID)
}

func (b *Bridge) rememberOutboundCollector(workspaceID, runID string, collector *aapfile.OutboundCollector) {
	if b == nil || collector == nil {
		return
	}
	b.outboundMu.Lock()
	defer b.outboundMu.Unlock()
	if b.outboundCollectors == nil {
		b.outboundCollectors = make(map[string]*aapfile.OutboundCollector)
	}
	b.outboundCollectors[outboundCollectorKey(workspaceID, runID)] = collector
}

func (b *Bridge) outboundCollector(workspaceID, runID string) *aapfile.OutboundCollector {
	if b == nil {
		return nil
	}
	b.outboundMu.Lock()
	defer b.outboundMu.Unlock()
	return b.outboundCollectors[outboundCollectorKey(workspaceID, runID)]
}

func (b *Bridge) forgetOutboundCollector(workspaceID, runID string) {
	if b == nil {
		return
	}
	b.outboundMu.Lock()
	defer b.outboundMu.Unlock()
	delete(b.outboundCollectors, outboundCollectorKey(workspaceID, runID))
}

func (b *Bridge) shouldInjectOutboundPublish(run execution.AgentRun, toolCalling string) bool {
	if b == nil || b.files == nil || !b.outboundGatesOpen(run) {
		return false
	}
	if !sessioncontext.EnableOutboundAttachmentsFromSnapshot(run.ContextPolicySnapshot) {
		return false
	}
	if !toolCallingSupportsOutbound(toolCalling) {
		return false
	}
	_, _, _, ok := aapPrincipalFromRun(run)
	return ok
}

func (b *Bridge) outboundGatesOpen(run execution.AgentRun) bool {
	if b == nil || b.filesCfg == nil {
		return false
	}
	cfg := *b.filesCfg
	if !cfg.Enabled || !cfg.RuntimeOutboundAttachments {
		return false
	}
	if !cfg.AllowsWorkspace(run.WorkspaceID) {
		return false
	}
	return cfg.AllowsClient(strings.TrimSpace(run.PrincipalSnapshot.ClientID))
}

func (b *Bridge) outboundMaxBytes() int64 {
	if b != nil && b.filesCfg != nil && b.filesCfg.MaxBytes > 0 {
		return b.filesCfg.MaxBytes
	}
	return aapfile.MaxOutboundTurnBytes
}

func frozenToolCalling(cfg modelconfig.Config) string {
	caps, _, err := modelconfig.ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil {
		return ""
	}
	calling := strings.TrimSpace(caps.ToolCalling)
	if calling == "" && caps.SchemaVersion == modelconfig.AgenticCapabilitiesSchemaV1 {
		return modelconfig.ToolCallingNativeClientSearch
	}
	return calling
}

func toolCallingSupportsOutbound(calling string) bool {
	switch calling {
	case modelconfig.ToolCallingFunctionCalling, modelconfig.ToolCallingNativeClientSearch:
		return true
	default:
		return false
	}
}

func aapPrincipalFromRun(run execution.AgentRun) (agentaccessauth.AAPAccessTokenPrincipal, string, int64, bool) {
	snap := run.PrincipalSnapshot
	if snap.Validate() != nil || snap.Identity.Actor.Type != principal.TypeServicePrincipal {
		return agentaccessauth.AAPAccessTokenPrincipal{}, "", 0, false
	}
	principalID := snap.Identity.Actor.ID
	if snap.Identity.Subject != nil && strings.TrimSpace(snap.Identity.Subject.ID) != "" {
		principalID = snap.Identity.Subject.ID
	}
	clientID := strings.TrimSpace(snap.ClientID)
	if clientID == "" {
		return agentaccessauth.AAPAccessTokenPrincipal{}, "", 0, false
	}
	return agentaccessauth.AAPAccessTokenPrincipal{
		PrincipalID:        principalID,
		ServicePrincipalID: snap.Identity.Actor.ID,
		AuthorizedParty:    clientID,
		WorkspaceID:        run.WorkspaceID,
		AgentID:            run.AgentID,
	}, clientID, snap.AgentPolicyVersion, true
}

type publishAttachmentBridgeTool struct {
	inner  *aapfile.PublishAttachmentTool
	bridge *Bridge
	job    agentrun.Job
	run    execution.AgentRun
}

var _ tool.InvokableTool = (*publishAttachmentBridgeTool)(nil)

func (t *publishAttachmentBridgeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if t == nil || t.inner == nil {
		return nil, aapfile.ErrInvalid
	}
	return t.inner.Info(ctx)
}

func (t *publishAttachmentBridgeTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	opts ...tool.Option,
) (string, error) {
	startedAt := time.Now().UTC()
	result, err := t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	finishedAt := time.Now().UTC()
	args := aapfile.AllowlistedPublishArgs(argumentsInJSON)
	ok, errorCode := aapfile.ParsePublishResultStatus(result)
	allowlisted := stripPublishResult(result)
	if ok {
		metrics.Default().ObserveOutboundPublish("ok")
		if n := publishResultSize(allowlisted); n > 0 {
			metrics.Default().ObserveOutboundIngestBytes(n)
		}
	} else if errorCode == aapfile.ErrorCodeFeatureDisabled {
		metrics.Default().ObserveOutboundPublish("disabled")
	} else {
		metrics.Default().ObserveOutboundPublish("error")
	}

	invocationID, idErr := uuid.NewV7()
	if idErr != nil {
		return result, err
	}
	t.projectAndAudit(ctx, invocationID.String(), args, allowlisted, ok, errorCode, startedAt, finishedAt)
	return result, err
}

func (t *publishAttachmentBridgeTool) projectAndAudit(
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
			Run: t.run, Job: t.job, Name: aapfile.PublishAttachmentToolName,
			InvocationID: invocationID, Args: args, Result: result,
			OK: ok, ErrorCode: errorCode, StartedAt: startedAt, FinishedAt: finishedAt,
		}); projErr != nil {
			logger.Error("platform publish tool_call projection failed",
				"event", "chatruntimebridge.outbound.project_failed",
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
		ToolName:     aapfile.PublishAttachmentToolName,
		ArgsJSON:     string(args),
		ResultJSON:   string(result),
		OK:           ok,
		ErrorCode:    errorCode,
	}); recErr != nil {
		logger.Error("platform publish TOOL step failed",
			"event", "chatruntimebridge.outbound.tool_step_failed",
			"workspace_id", t.run.WorkspaceID,
			"run_id", t.run.ID,
			"error", recErr.Error(),
		)
	}
}

func stripPublishResult(raw string) json.RawMessage {
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil || body == nil {
		return json.RawMessage(`{"ok":false}`)
	}
	out := map[string]any{}
	if ok, exists := body["ok"].(bool); exists && ok {
		out["ok"] = true
		for _, key := range []string{"fileId", "filename", "mediaType", "sha256"} {
			if v, has := body[key]; has {
				out[key] = v
			}
		}
		if v, has := body["sizeBytes"]; has {
			out["sizeBytes"] = v
		}
	} else {
		out["ok"] = false
		if v, has := body["errorCode"]; has {
			out["errorCode"] = v
		}
		if v, has := body["message"]; has {
			out["message"] = v
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{"ok":false}`)
	}
	return encoded
}

func publishResultSize(raw json.RawMessage) int64 {
	var body struct {
		SizeBytes int64 `json:"sizeBytes"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return 0
	}
	return body.SizeBytes
}
