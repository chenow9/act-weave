package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/config"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/sessioncontext"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
)

type capturePlatformCalls struct {
	calls []chatruntime.ProjectPlatformToolCallInput
	err   error
}

func (c *capturePlatformCalls) ProjectPlatformToolCall(_ context.Context, in chatruntime.ProjectPlatformToolCallInput) error {
	c.calls = append(c.calls, in)
	return c.err
}

type fakeOutboundFiles struct {
	ingest    aapfile.File
	ingestErr error
	listed    []aapfile.File
	promoted  []string
}

func (f *fakeOutboundFiles) IngestGenerated(_ context.Context, in aapfile.IngestGeneratedInput) (aapfile.File, error) {
	if f.ingestErr != nil {
		return aapfile.File{}, f.ingestErr
	}
	out := f.ingest
	if out.ID == "" {
		out.ID = uuid.NewString()
	}
	out.SourceRunID = in.SourceRunID
	out.DeclaredMediaType = in.MediaType
	out.SizeBytes = in.SizeBytes
	name := in.Filename
	out.Filename = &name
	sha := in.SHA256
	out.SHA256 = &sha
	f.listed = append(f.listed, out)
	return out, nil
}

func (f *fakeOutboundFiles) ListGeneratedForRun(context.Context, string, string, string) ([]aapfile.File, error) {
	return append([]aapfile.File(nil), f.listed...), nil
}

func (f *fakeOutboundFiles) PromoteRetentionOnReference(_ context.Context, _, fileID string) error {
	f.promoted = append(f.promoted, fileID)
	return nil
}

func testAAPRun(t *testing.T) execution.AgentRun {
	t.Helper()
	ws := "a18f1f2e-7b5a-7c3d-8e9f-123456789002"
	agentID := "a18f1f2e-7b5a-7c3d-8e9f-123456789004"
	client := "a18f1f2e-7b5a-7c3d-8e9f-123456789005"
	sp := "a18f1f2e-7b5a-7c3d-8e9f-123456789006"
	ident, err := principal.NewInvocationIdentity(
		principal.Ref{WorkspaceID: ws, Type: principal.TypeServicePrincipal, ID: sp},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := principal.NewExecutionSnapshot(ident, client, uuid.NewString(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := sessioncontext.Resolve(sessioncontext.ResolveInput{
		AgentPolicy: json.RawMessage(`{
			"schemaVersion":"session-context-policy.v2",
			"mode":"token_window",
			"aap":{"enableOutboundAttachments":true}
		}`),
		ContextWindowTokens:        128000,
		DefaultOutputReserveTokens: 4096,
		OutputTokenLimitMode:       "max_tokens",
		TokenizerProfile:           "o200k_base",
		TokenizerVersion:           "2026-01",
		GateEnabled:                true,
		CompactionGateEnabled:      false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return execution.AgentRun{
		ID: uuid.NewString(), WorkspaceID: ws, SessionID: uuid.NewString(),
		AgentID: agentID, Status: "RUNNING", TriggeredByType: "SERVICE_PRINCIPAL",
		TriggeredByID: sp, TraceID: "trace-outbound",
		ContextPolicySnapshot: raw, PrincipalSnapshot: snap,
	}
}

func openFilesCfg() *config.AgentAccessFilesConfig {
	return &config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
		RuntimeOutboundAttachments: true,
	}
}

func TestPublishBridgeProjectsSuccessAndRecordsToolStep(t *testing.T) {
	run := testAAPRun(t)
	files := &fakeOutboundFiles{}
	proj := &capturePlatformCalls{}
	steps := &captureStepStoreWithTransitions{}
	bridge := &Bridge{
		platformCalls: proj, steps: steps, files: files, filesCfg: openFilesCfg(),
	}
	collector := aapfile.NewOutboundCollector(files, run.WorkspaceID, run.AgentID, run.ID, aapfile.MaxOutboundTurnBytes)
	inner, err := aapfile.NewPublishAttachmentTool(aapfile.PublishAttachmentConfig{
		Ingest: files, Collector: collector,
		Scope: aapfile.Scope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID:        run.PrincipalSnapshot.Identity.Actor.ID,
			ServicePrincipalID: run.PrincipalSnapshot.Identity.Actor.ID,
			AuthorizedParty:    run.PrincipalSnapshot.ClientID,
			WorkspaceID:        run.WorkspaceID, AgentID: run.AgentID,
		},
		ClientID: run.PrincipalSnapshot.ClientID, AgentPolicyVersion: 1, SourceRunID: run.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := &publishAttachmentBridgeTool{
		inner: inner, bridge: bridge,
		job: agentrun.Job{WorkspaceID: run.WorkspaceID, RunID: run.ID, ActorID: run.TriggeredByID},
		run: run,
	}
	out, err := tool.InvokableRun(context.Background(), `{"filename":"invoice.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := aapfile.ParsePublishResultStatus(out)
	if !ok {
		t.Fatalf("result=%s", out)
	}
	if len(proj.calls) != 1 {
		t.Fatalf("projections=%d", len(proj.calls))
	}
	call := proj.calls[0]
	if !call.OK || call.Name != aapfile.PublishAttachmentToolName {
		t.Fatalf("call=%+v", call)
	}
	if strings.Contains(string(call.Args), "url") || strings.Contains(string(call.Result), "downloadUrl") {
		t.Fatalf("url leaked args=%s result=%s", call.Args, call.Result)
	}
	if len(steps.appended) != 1 || steps.appended[0].StepType != "TOOL" {
		t.Fatalf("steps=%+v", steps.appended)
	}
	if steps.appended[0].CapabilityReleaseID != "" {
		t.Fatalf("audit must not write sentinel release: %q", steps.appended[0].CapabilityReleaseID)
	}
	if len(steps.transitions) != 1 || steps.transitions[0].NewStatus != "SUCCEEDED" {
		t.Fatalf("transitions=%+v", steps.transitions)
	}
}

func TestPublishBridgeFailedIngestStillProjects(t *testing.T) {
	run := testAAPRun(t)
	files := &fakeOutboundFiles{ingestErr: aapfile.ErrFeatureDisabled}
	proj := &capturePlatformCalls{}
	steps := &captureStepStoreWithTransitions{}
	bridge := &Bridge{platformCalls: proj, steps: steps, files: files, filesCfg: openFilesCfg()}
	collector := aapfile.NewOutboundCollector(files, run.WorkspaceID, run.AgentID, run.ID, aapfile.MaxOutboundTurnBytes)
	inner, err := aapfile.NewPublishAttachmentTool(aapfile.PublishAttachmentConfig{
		Ingest: files, Collector: collector,
		Scope: aapfile.Scope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID:        run.PrincipalSnapshot.Identity.Actor.ID,
			ServicePrincipalID: run.PrincipalSnapshot.Identity.Actor.ID,
			AuthorizedParty:    run.PrincipalSnapshot.ClientID,
			WorkspaceID:        run.WorkspaceID, AgentID: run.AgentID,
		},
		ClientID: run.PrincipalSnapshot.ClientID, AgentPolicyVersion: 1, SourceRunID: run.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := &publishAttachmentBridgeTool{
		inner: inner, bridge: bridge,
		job: agentrun.Job{WorkspaceID: run.WorkspaceID, RunID: run.ID, ActorID: run.TriggeredByID},
		run: run,
	}
	out, err := tool.InvokableRun(context.Background(), `{"filename":"invoice.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, code := aapfile.ParsePublishResultStatus(out)
	if ok || code != aapfile.ErrorCodeFeatureDisabled {
		t.Fatalf("result=%s", out)
	}
	if len(proj.calls) != 1 || proj.calls[0].OK || proj.calls[0].ErrorCode != aapfile.ErrorCodeFeatureDisabled {
		t.Fatalf("call=%+v", proj.calls)
	}
	if len(steps.transitions) != 1 || steps.transitions[0].NewStatus != "FAILED" {
		t.Fatalf("transitions=%+v", steps.transitions)
	}
	listed, _ := files.ListGeneratedForRun(context.Background(), run.WorkspaceID, run.AgentID, run.ID)
	if len(listed) != 0 {
		t.Fatalf("failed ingest must not list files: %v", listed)
	}
}

func TestPublishBridgeProjectionFailureStillReturnsIngest(t *testing.T) {
	run := testAAPRun(t)
	files := &fakeOutboundFiles{}
	proj := &capturePlatformCalls{err: errors.New("project boom")}
	bridge := &Bridge{platformCalls: proj, files: files, filesCfg: openFilesCfg()}
	collector := aapfile.NewOutboundCollector(files, run.WorkspaceID, run.AgentID, run.ID, aapfile.MaxOutboundTurnBytes)
	inner, err := aapfile.NewPublishAttachmentTool(aapfile.PublishAttachmentConfig{
		Ingest: files, Collector: collector,
		Scope: aapfile.Scope{WorkspaceID: run.WorkspaceID, AgentID: run.AgentID},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID:        run.PrincipalSnapshot.Identity.Actor.ID,
			ServicePrincipalID: run.PrincipalSnapshot.Identity.Actor.ID,
			AuthorizedParty:    run.PrincipalSnapshot.ClientID,
			WorkspaceID:        run.WorkspaceID, AgentID: run.AgentID,
		},
		ClientID: run.PrincipalSnapshot.ClientID, AgentPolicyVersion: 1, SourceRunID: run.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := &publishAttachmentBridgeTool{inner: inner, bridge: bridge, run: run}
	out, err := tool.InvokableRun(context.Background(), `{"filename":"invoice.csv","mediaType":"text/csv","text":"a,b"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := aapfile.ParsePublishResultStatus(out)
	if !ok {
		t.Fatalf("model must still receive ingest result: %s", out)
	}
}

func TestShouldInjectOutboundPublishGates(t *testing.T) {
	run := testAAPRun(t)
	b := &Bridge{files: &fakeOutboundFiles{}, filesCfg: openFilesCfg()}
	if !b.shouldInjectOutboundPublish(run, modelconfig.ToolCallingFunctionCalling) {
		t.Fatal("expected inject")
	}
	if b.shouldInjectOutboundPublish(run, modelconfig.ToolCallingNone) {
		t.Fatal("none must not inject")
	}
	closed := openFilesCfg()
	closed.RuntimeOutboundAttachments = false
	b.filesCfg = closed
	if b.shouldInjectOutboundPublish(run, modelconfig.ToolCallingFunctionCalling) {
		t.Fatal("runtime flag off")
	}
}

func testFCCaps(t *testing.T) modelconfig.Config {
	t.Helper()
	doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(
		modelconfig.ToolCallingFunctionCalling,
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		1,
		strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return modelconfig.Config{AgenticCapabilities: raw}
}

func TestInjectOutboundPublishSideBandFlags(t *testing.T) {
	run := testAAPRun(t)
	b := &Bridge{files: &fakeOutboundFiles{}, filesCfg: openFilesCfg()}
	cfg := testFCCaps(t)
	tools, flags := b.maybeInjectOutboundPublish(context.Background(), agentrun.Job{
		WorkspaceID: run.WorkspaceID, RunID: run.ID,
	}, run, nil, nil, cfg)
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	flag, ok := flags[aapfile.PublishAttachmentToolName]
	if !ok || !flag.PlatformControl || flag.Exposure != einoruntime.ToolExposureImmediate {
		t.Fatalf("flags=%+v", flags)
	}
	catalog, err := buildFrozenToolCatalogStrict(context.Background(), tools, nil, flags)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := catalog.Entry(aapfile.PublishAttachmentToolName)
	if !ok || !entry.PlatformControl || entry.Exposure != einoruntime.ToolExposureImmediate {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestInjectSkipsNameCollision(t *testing.T) {
	run := testAAPRun(t)
	b := &Bridge{files: &fakeOutboundFiles{}, filesCfg: openFilesCfg()}
	cfg := testFCCaps(t)
	caps := []chatruntime.SnapshotCapability{{
		CallableName: aapfile.PublishAttachmentToolName, CapabilityID: uuid.NewString(),
		ReleaseID: uuid.NewString(), Kind: "TOOL",
	}}
	tools := []tool.BaseTool{&stubTool{name: aapfile.PublishAttachmentToolName, desc: "biz"}}
	got, flags := b.maybeInjectOutboundPublish(context.Background(), agentrun.Job{}, run, tools, caps, cfg)
	if len(got) != 1 || flags != nil {
		t.Fatalf("collision must skip inject tools=%d flags=%v", len(got), flags)
	}
}

func TestCompleteRunPromotesAfterRecord(t *testing.T) {
	run := testAAPRun(t)
	filename := "invoice.csv"
	fileID := "019f0000-0000-7000-8000-00000000f001"
	files := &fakeOutboundFiles{listed: []aapfile.File{{
		ID: fileID, Filename: &filename, DeclaredMediaType: "text/csv", SizeBytes: 4,
	}}}
	results := &captureAssistantResults{}
	runs := &staticRunReader{run: run}
	b := &Bridge{files: files, filesCfg: openFilesCfg(), results: results, runs: runs, now: func() time.Time { return time.Now().UTC() }}
	err := b.completeRun(context.Background(), agentrun.Job{
		WorkspaceID: run.WorkspaceID, SessionID: run.SessionID, RunID: run.ID,
		UserMessageID: uuid.NewString(), ActorID: run.TriggeredByID,
	}, run, "", uuid.NewString(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(results.content) == "" || !strings.Contains(results.content, "output_file") {
		t.Fatalf("content=%s", results.content)
	}
	if len(files.promoted) != 1 || files.promoted[0] != fileID {
		t.Fatalf("promoted=%v", files.promoted)
	}
}

type captureAssistantResults struct {
	content string
}

func (r *captureAssistantResults) RecordAssistantResult(_ context.Context, in chat.RecordAssistantResultInput) (chat.RecordAssistantResult, error) {
	r.content = in.Content
	return chat.RecordAssistantResult{Message: chat.Message{
		ID: in.AssistantMessageID, WorkspaceID: in.WorkspaceID, SessionID: in.SessionID,
		Role: "ASSISTANT", Content: in.Content, Status: "COMPLETED", RunID: in.RunID,
		CreatedAt: time.Now().UTC(),
	}}, nil
}

type staticRunReader struct {
	run execution.AgentRun
}

func (s *staticRunReader) GetAgentRun(context.Context, string, string) (execution.AgentRun, error) {
	return s.run, nil
}
