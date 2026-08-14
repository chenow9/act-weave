package aapfile_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/storedobject"

	"github.com/google/uuid"
)

const (
	testRunID      = "a18f1f2e-7b5a-7c3d-8e9f-123456789010"
	testRunIDAlt   = "a18f1f2e-7b5a-7c3d-8e9f-123456789011"
	testAgentIDAlt = "a18f1f2e-7b5a-7c3d-8e9f-123456789012"
)

func TestIngestGeneratedRoundTripAndListIsolation(t *testing.T) {
	collector := metrics.NewAAPFileCollector()
	service, _, secure, db := newAAPFileService(t,
		aapfile.WithFilesFeatureGate(openOutboundGate()),
		aapfile.WithMetrics(collector),
	)
	ctx := context.Background()

	csvBody := []byte("month,total\n2026-08,12.4\n")
	got, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, csvBody))
	if err != nil {
		t.Fatalf("IngestGenerated: %v", err)
	}
	if got.Status != aapfile.StatusReady || got.Purpose != aapfile.PurposeAgentOutput {
		t.Fatalf("status/purpose=%s/%s", got.Status, got.Purpose)
	}
	if got.SourceRunID != testRunID {
		t.Fatalf("SourceRunID=%q want %s", got.SourceRunID, testRunID)
	}
	if got.StoredObjectID == nil || strings.TrimSpace(*got.StoredObjectID) == "" {
		t.Fatal("stored_object_id required")
	}
	if got.StagingObjectKey != nil {
		t.Fatalf("staging key must be nil, got %v", got.StagingObjectKey)
	}
	if got.StagingDeletedAt == nil || got.ReadyAt == nil {
		t.Fatal("staging_deleted_at and ready_at required")
	}
	if got.StagingBucket != storedobject.BucketAAPStaging {
		t.Fatalf("staging bucket=%s", got.StagingBucket)
	}
	if secure.putCalls != 1 {
		t.Fatalf("Put calls=%d want 1", secure.putCalls)
	}
	if secure.lastKind != storedobject.KindAAPFile ||
		secure.lastClass != storedobject.ClassificationSensitive ||
		secure.lastRetention != storedobject.RetentionExpiring {
		t.Fatalf("put metadata kind=%s class=%s retention=%s",
			secure.lastKind, secure.lastClass, secure.lastRetention)
	}
	if secure.lastCreatedBy != storedobject.CreatorServicePrincipal || secure.lastCreatedByID != testServiceID {
		t.Fatalf("created_by=%s/%s", secure.lastCreatedBy, secure.lastCreatedByID)
	}

	loaded, err := service.GetFile(ctx, testWorkspaceID, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SourceRunID != testRunID {
		t.Fatalf("GetFile SourceRunID=%q", loaded.SourceRunID)
	}

	// Same run, second file — list preserves ingest order.
	secondBody := []byte("{\"ok\":true}\n")
	second, err := service.IngestGenerated(ctx, ingestJSONInput(testRunID, secondBody))
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	listed, err := service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentID, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != got.ID || listed[1].ID != second.ID {
		t.Fatalf("list same run=%v", idsOf(listed))
	}

	// Different run.
	if _, err := service.IngestGenerated(ctx, ingestCSVInput(testRunIDAlt, csvBody)); err != nil {
		t.Fatal(err)
	}
	otherRun, err := service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentID, testRunIDAlt)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherRun) != 1 || otherRun[0].SourceRunID != testRunIDAlt {
		t.Fatalf("other run leaked: %+v", otherRun)
	}
	sameRun, err := service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentID, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sameRun) != 2 {
		t.Fatalf("run isolation broken: %d", len(sameRun))
	}

	// Different purpose must not appear.
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'agent-alt',$3,$4,$4)
	`, testAgentIDAlt, testWorkspaceID, testModelID, testOwnerID); err != nil {
		t.Fatal(err)
	}
	inbound, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := got.CreatedAt
	general := got
	general.ID = uuid.Must(uuid.NewV7()).String()
	general.Purpose = aapfile.PurposeGeneral
	general.SourceRunID = testRunID
	general.CreatedAt = now
	if _, err := inbound.InsertFile(ctx, general); err != nil {
		t.Fatalf("insert GENERAL with source_run_id: %v", err)
	}
	listed, err = service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentID, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("purpose leak: got %d files %v", len(listed), idsOf(listed))
	}

	// Different agent.
	otherAgent, err := service.IngestGenerated(ctx, ingestCSVInputForAgent(testRunID, testAgentIDAlt, csvBody))
	if err != nil {
		t.Fatal(err)
	}
	listedAgent, err := service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentIDAlt, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAgent) != 1 || listedAgent[0].ID != otherAgent.ID {
		t.Fatalf("agent isolation: %+v", listedAgent)
	}
	listedOrig, err := service.ListGeneratedForRun(ctx, testWorkspaceID, testAgentID, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedOrig) != 2 {
		t.Fatalf("agent leak into original list: %d", len(listedOrig))
	}

	snap := collector.Snapshot()
	if snap["aap_file_ingest_generated_ok"] < 3 {
		t.Fatalf("ingest ok metric=%d", snap["aap_file_ingest_generated_ok"])
	}
}

func TestIngestGeneratedMIMEWeakSniff(t *testing.T) {
	service, _, _, _ := newAAPFileService(t, aapfile.WithFilesFeatureGate(openOutboundGate()))
	ctx := context.Background()

	cases := []struct {
		name      string
		mediaType string
		filename  string
		body      []byte
	}{
		{name: "csv", mediaType: "text/csv", filename: "a.csv", body: []byte("a,b\n1,2\n")},
		{name: "json", mediaType: "application/json", filename: "a.json", body: []byte(`{"a":1}`)},
		{name: "markdown", mediaType: "text/markdown", filename: "a.md", body: []byte("# hi\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.IngestGenerated(ctx, ingestInput(testRunID, testAgentID, tc.filename, tc.mediaType, tc.body))
			if err != nil {
				t.Fatalf("weak sniff rejected: %v", err)
			}
			if got.DeclaredMediaType != tc.mediaType {
				t.Fatalf("declared=%s", got.DeclaredMediaType)
			}
		})
	}

	_, err := service.IngestGenerated(ctx, ingestInput(testRunID, testAgentID, "x.csv", "text/csv", pngBytes))
	if err == nil || !errors.Is(err, aapfile.ErrFailed) || !strings.Contains(err.Error(), aapfile.ErrorCodeMediaTypeMismatch) {
		t.Fatalf("csv+png want FILE_MEDIA_TYPE_MISMATCH, got %v", err)
	}
}

func TestIngestGeneratedGateAndQuota(t *testing.T) {
	ctx := context.Background()
	body := []byte("a,b\n1,2\n")

	t.Run("gate closed is FILE_FEATURE_DISABLED", func(t *testing.T) {
		service, _, secure, _ := newAAPFileService(t)
		_, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err == nil || !errors.Is(err, aapfile.ErrFeatureDisabled) {
			t.Fatalf("err=%v want ErrFeatureDisabled", err)
		}
		if !strings.Contains(err.Error(), aapfile.ErrorCodeFeatureDisabled) {
			t.Fatalf("err=%v", err)
		}
		if secure.putCalls != 0 {
			t.Fatal("must not put when gate closed")
		}
	})

	t.Run("allowlist miss", func(t *testing.T) {
		service, _, _, _ := newAAPFileService(t, aapfile.WithFilesFeatureGate(aapfile.FilesFeatureGate{
			Enabled: true, RuntimeOutboundAttachments: true,
			AllowsWorkspace: func(string) bool { return true },
			AllowsClient:    func(string) bool { return false },
		}))
		_, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err == nil || !errors.Is(err, aapfile.ErrFeatureDisabled) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("runtime outbound off", func(t *testing.T) {
		service, _, _, _ := newAAPFileService(t, aapfile.WithFilesFeatureGate(aapfile.FilesFeatureGate{
			Enabled: true, RuntimeOutboundAttachments: false,
			AllowsWorkspace: func(string) bool { return true },
			AllowsClient:    func(string) bool { return true },
		}))
		_, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err == nil || !errors.Is(err, aapfile.ErrFeatureDisabled) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("does not consume pending slots", func(t *testing.T) {
		service, staging, _, _ := newAAPFileService(t,
			aapfile.WithFilesFeatureGate(openOutboundGate()),
			aapfile.WithMaxPendingPerWorkspace(1),
		)
		intent, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), ""))
		if err != nil {
			t.Fatal(err)
		}
		staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, pngBytes)
		if _, err := service.CreateUploadIntent(ctx, createCCInput(len(pngBytes), "")); !errors.Is(err, aapfile.ErrPendingLimit) {
			t.Fatalf("second create err=%v want ErrPendingLimit", err)
		}
		got, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err != nil {
			t.Fatalf("ingest must not use pending quota: %v", err)
		}
		if got.Status != aapfile.StatusReady {
			t.Fatalf("status=%s", got.Status)
		}
	})

	t.Run("ready quota is FILE_SIZE_EXCEEDED not pending", func(t *testing.T) {
		service, _, _, _ := newAAPFileService(t,
			aapfile.WithFilesFeatureGate(openOutboundGate()),
			aapfile.WithMaxReadyBytesPerWorkspace(int64(len(body))),
			aapfile.WithMaxPendingPerWorkspace(1),
		)
		first, err := service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err != nil {
			t.Fatalf("first ingest: %v", err)
		}
		if first.Status != aapfile.StatusReady {
			t.Fatalf("status=%s", first.Status)
		}
		_, err = service.IngestGenerated(ctx, ingestCSVInput(testRunID, body))
		if err == nil || !errors.Is(err, aapfile.ErrFailed) || !strings.Contains(err.Error(), aapfile.ErrorCodeSizeExceeded) {
			t.Fatalf("quota overflow err=%v want FILE_SIZE_EXCEEDED", err)
		}
		if strings.Contains(err.Error(), aapfile.ErrorCodePendingLimit) || errors.Is(err, aapfile.ErrPendingLimit) {
			t.Fatalf("must not use FILE_PENDING_LIMIT: %v", err)
		}
	})

	t.Run("integrity mismatch", func(t *testing.T) {
		service, _, _, _ := newAAPFileService(t, aapfile.WithFilesFeatureGate(openOutboundGate()))
		in := ingestCSVInput(testRunID, body)
		in.SHA256 = strings.Repeat("ab", 32)
		_, err := service.IngestGenerated(ctx, in)
		if err == nil || !strings.Contains(err.Error(), aapfile.ErrorCodeIntegrityMismatch) {
			t.Fatalf("err=%v", err)
		}
	})
}

func openOutboundGate() aapfile.FilesFeatureGate {
	return aapfile.FilesFeatureGate{
		Enabled:                    true,
		RuntimeOutboundAttachments: true,
		AllowsWorkspace:            func(string) bool { return true },
		AllowsClient:               func(string) bool { return true },
	}
}

func ingestCSVInput(runID string, body []byte) aapfile.IngestGeneratedInput {
	return ingestInput(runID, testAgentID, "invoice.csv", "text/csv", body)
}

func ingestJSONInput(runID string, body []byte) aapfile.IngestGeneratedInput {
	return ingestInput(runID, testAgentID, "payload.json", "application/json", body)
}

func ingestCSVInputForAgent(runID, agentID string, body []byte) aapfile.IngestGeneratedInput {
	return ingestInput(runID, agentID, "invoice.csv", "text/csv", body)
}

func ingestInput(runID, agentID, filename, mediaType string, body []byte) aapfile.IngestGeneratedInput {
	sum := sha256.Sum256(body)
	return aapfile.IngestGeneratedInput{
		Scope: aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: agentID},
		Principal: agentaccessauth.AAPAccessTokenPrincipal{
			PrincipalID:        testServiceID,
			ServicePrincipalID: testServiceID,
			WorkspaceID:        testWorkspaceID,
			AgentID:            agentID,
			AuthorizedParty:    testClientID,
		},
		ClientID:           testClientID,
		AgentPolicyVersion: 7,
		Filename:           filename,
		MediaType:          mediaType,
		SizeBytes:          int64(len(body)),
		SHA256:             hex.EncodeToString(sum[:]),
		Body:               bytes.NewReader(body),
		SourceRunID:        runID,
	}
}

func idsOf(files []aapfile.File) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.ID)
	}
	return out
}
