package chatruntimebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentrun"
	"actweave/backend/internal/chat"
	"actweave/backend/internal/chatruntime"
	"actweave/backend/internal/config"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/modelconfig"
	"actweave/backend/internal/principal"
	"actweave/backend/internal/sessioncontext"
	"actweave/backend/internal/toolruntime"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
)

const inboundTestFileID = "d41f1f2e-7b5a-7c3d-8e9f-1234567890f1"

type fakeReadOpener struct {
	meta toolruntime.PlatformFileMeta
	body []byte
}

func (f *fakeReadOpener) OpenReadyFile(_ context.Context, workspaceID, fileID string) (toolruntime.PlatformOpenedFile, error) {
	if workspaceID != f.meta.WorkspaceID || fileID != f.meta.FileID {
		return toolruntime.PlatformOpenedFile{}, toolruntime.ErrPlatformFileNotFound
	}
	return toolruntime.PlatformOpenedFile{
		Meta: f.meta,
		Body: io.NopCloser(bytes.NewReader(f.body)),
	}, nil
}

func testInboundRun(t *testing.T) execution.AgentRun {
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
			"aap":{"enableInboundRead":true}
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
		TriggeredByID: sp, TraceID: "trace-inbound",
		ContextPolicySnapshot: raw, PrincipalSnapshot: snap,
	}
}

func openInboundFilesCfg() *config.AgentAccessFilesConfig {
	return &config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
		RuntimeInboundRead: true,
	}
}

func inboundUserEnvelope() string {
	raw, _ := json.Marshal(map[string]any{
		"schemaVersion": "aap.message-content.v1",
		"parts": []map[string]string{
			{"type": "text", "text": "summarize"},
			{"type": "input_file", "fileId": inboundTestFileID, "mediaType": "application/pdf"},
		},
	})
	return string(raw)
}

func TestReadBridgeProjectsTextOnToolCall(t *testing.T) {
	run := testInboundRun(t)
	pdf := aapfile.BuildTextPDF([]string{"Hello inbound PDF"})
	opener := &fakeReadOpener{
		meta: toolruntime.PlatformFileMeta{
			FileID: inboundTestFileID, WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
			Status: aapfile.StatusReady, Filename: "contract.pdf", DeclaredMedia: aapfile.MediaTypePDF,
			SizeBytes: int64(len(pdf)),
		},
		body: pdf,
	}
	inner, err := aapfile.NewReadAttachmentTool(aapfile.ReadAttachmentConfig{
		Opener: opener, ReadableFileIDs: map[string]struct{}{inboundTestFileID: {}},
		WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	proj := &capturePlatformCalls{}
	steps := &captureStepStoreWithTransitions{}
	tool := &readAttachmentBridgeTool{
		inner:  inner,
		bridge: &Bridge{platformCalls: proj, steps: steps, filesCfg: openInboundFilesCfg()},
		job:    agentrun.Job{WorkspaceID: run.WorkspaceID, RunID: run.ID, ActorID: run.TriggeredByID},
		run:    run,
	}
	out, err := tool.InvokableRun(context.Background(), `{"fileId":"`+inboundTestFileID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := aapfile.ParseReadResultStatus(out)
	if !ok || !strings.Contains(out, "Hello inbound PDF") {
		t.Fatalf("result=%s", out)
	}
	if len(proj.calls) != 1 {
		t.Fatalf("projections=%d", len(proj.calls))
	}
	call := proj.calls[0]
	if !call.OK || call.Name != aapfile.ReadAttachmentToolName {
		t.Fatalf("call=%+v", call)
	}
	if !strings.Contains(string(call.Result), "Hello inbound PDF") {
		t.Fatalf("projected result missing text: %s", call.Result)
	}
	if strings.Contains(string(call.Result), "downloadUrl") || strings.Contains(string(call.Result), "https://") {
		t.Fatalf("url leaked: %s", call.Result)
	}
	if len(steps.appended) != 1 || steps.appended[0].StepType != "TOOL" {
		t.Fatalf("steps=%+v", steps.appended)
	}
}

func TestInjectInboundReadSideBandFlags(t *testing.T) {
	run := testInboundRun(t)
	pdf := aapfile.BuildTextPDF([]string{"x"})
	b := &Bridge{
		fileOpener: &fakeReadOpener{
			meta: toolruntime.PlatformFileMeta{
				FileID: inboundTestFileID, WorkspaceID: run.WorkspaceID, AgentID: run.AgentID,
				Status: aapfile.StatusReady, DeclaredMedia: aapfile.MediaTypePDF, SizeBytes: int64(len(pdf)),
			},
			body: pdf,
		},
		filesCfg: openInboundFilesCfg(),
		sessions: &inboundSessions{messages: []chat.Message{{
			ID: uuid.NewString(), WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
			Role: "USER", Content: inboundUserEnvelope(),
		}}},
	}
	cfg := testFCCaps(t)
	tools, flags := b.maybeInjectInboundRead(context.Background(), agentrun.Job{
		WorkspaceID: run.WorkspaceID, SessionID: run.SessionID, RunID: run.ID,
	}, run, nil, nil, cfg)
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	flag, ok := flags[aapfile.ReadAttachmentToolName]
	if !ok || !flag.PlatformControl || flag.Exposure != einoruntime.ToolExposureImmediate {
		t.Fatalf("flags=%+v", flags)
	}
	catalog, err := buildFrozenToolCatalogStrict(context.Background(), tools, nil, flags)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := catalog.Entry(aapfile.ReadAttachmentToolName)
	if !ok || !entry.PlatformControl || entry.Exposure != einoruntime.ToolExposureImmediate {
		t.Fatalf("entry=%+v", entry)
	}
	if !shouldAppendInboundReadPrompt(flags) {
		t.Fatal("injected tool must append prompt")
	}
}

func TestInjectInboundReadSkipsWhenNone(t *testing.T) {
	run := testInboundRun(t)
	b := &Bridge{
		fileOpener: &fakeReadOpener{meta: toolruntime.PlatformFileMeta{FileID: inboundTestFileID, WorkspaceID: run.WorkspaceID}},
		filesCfg:   openInboundFilesCfg(),
		sessions:   &inboundSessions{},
	}
	doc, err := modelconfig.CanonicalAgenticCapabilitiesV2(
		modelconfig.ToolCallingNone,
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		1,
		strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	cfg := modelconfig.Config{AgenticCapabilities: raw}
	got, flags := b.maybeInjectInboundRead(context.Background(), agentrun.Job{}, run, nil, nil, cfg)
	if len(got) != 0 || flags != nil {
		t.Fatalf("none must skip inject tools=%d flags=%v", len(got), flags)
	}
}

func TestInjectInboundReadSkipsWhenRuntimeOff(t *testing.T) {
	run := testInboundRun(t)
	cfgFlag := openInboundFilesCfg()
	cfgFlag.RuntimeInboundRead = false
	b := &Bridge{
		fileOpener: &fakeReadOpener{meta: toolruntime.PlatformFileMeta{FileID: inboundTestFileID, WorkspaceID: run.WorkspaceID}},
		filesCfg:   cfgFlag,
		sessions:   &inboundSessions{},
	}
	got, flags := b.maybeInjectInboundRead(context.Background(), agentrun.Job{}, run, nil, nil, testFCCaps(t))
	if len(got) != 0 || flags != nil {
		t.Fatalf("runtime off must skip tools=%d flags=%v", len(got), flags)
	}
}

func TestInjectInboundReadSkipsNameCollision(t *testing.T) {
	run := testInboundRun(t)
	b := &Bridge{fileOpener: &fakeReadOpener{}, filesCfg: openInboundFilesCfg()}
	caps := []chatruntime.SnapshotCapability{{
		CallableName: aapfile.ReadAttachmentToolName, CapabilityID: uuid.NewString(),
		ReleaseID: uuid.NewString(), Kind: "TOOL",
	}}
	tools := []tool.BaseTool{&stubTool{name: aapfile.ReadAttachmentToolName, desc: "biz"}}
	got, flags := b.maybeInjectInboundRead(context.Background(), agentrun.Job{}, run, tools, caps, testFCCaps(t))
	if len(got) != 1 || flags != nil {
		t.Fatalf("collision must skip inject tools=%d flags=%v", len(got), flags)
	}
}

func TestConversationInboundFileIDsCrossTurn(t *testing.T) {
	run := testInboundRun(t)
	b := &Bridge{
		sessions: &inboundSessions{messages: []chat.Message{
			{
				ID: uuid.NewString(), WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
				Role: "USER", Content: inboundUserEnvelope(),
			},
			{
				ID: uuid.NewString(), WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
				Role: "ASSISTANT", Content: "ok",
			},
		}},
	}
	ids, err := b.conversationInboundFileIDs(context.Background(), agentrun.Job{
		WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
	}, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids[inboundTestFileID]; !ok {
		t.Fatalf("ids=%v", ids)
	}
}

type inboundSessions struct {
	messages []chat.Message
}

func (s *inboundSessions) GetSession(_ context.Context, workspaceID, sessionID string) (chat.Session, error) {
	return chat.Session{ID: sessionID, WorkspaceID: workspaceID, LockVersion: 1}, nil
}

func (s *inboundSessions) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return s.messages, nil
}

func (s *inboundSessions) ListMessagesReversePage(
	context.Context, string, string, int, *chat.MessagePageCursor,
) (chat.MessagePage, error) {
	return chat.MessagePage{Messages: s.messages}, nil
}

func (s *inboundSessions) GetMessage(_ context.Context, _, messageID string) (chat.Message, error) {
	for _, m := range s.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return chat.Message{}, chat.ErrNotFound
}
