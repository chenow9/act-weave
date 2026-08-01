package aapfile_test

import (
	"context"
	"database/sql"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/aapfile"

	"github.com/google/uuid"
)

func TestPipelinePromoteReadyWithoutProcessors(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval: 50 * time.Millisecond,
		ClaimLease:   30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	content := pngBytes
	sum := sha256.Sum256(content)
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), hex.EncodeToString(sum[:])))
	if err != nil {
		t.Fatal(err)
	}
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, content)
	if _, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope: aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID}, FileID: intent.File.ID,
	}); err != nil {
		t.Fatal(err)
	}

	n, err := worker.DrainForTest(ctx, 8)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least promote claim, got %d", n)
	}
	got, err := service.GetFile(ctx, testWorkspaceID, intent.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusReady {
		t.Fatalf("status=%s want READY (jobs should be promote+mime only)", got.Status)
	}
	if got.StoredObjectID == nil {
		t.Fatal("stored_object_id required")
	}
}

func TestPipelineRequiredWebhookFailMarksFileFailed(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	// Partner returns 500 → delivery hard fail → required stage FAILED → file FAILED.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	insertProcessor(t, db, "req-fail", testWebhookPublicURL, "secret-req", true)

	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval:  50 * time.Millisecond,
		ClaimLease:    30 * time.Second,
		PublicBaseURL: "https://aap.test",
		HTTPClient:    testWebhookClient(server),
		Resolver:      publicTestResolver{ip: net.ParseIP("8.8.8.8")},
		Secrets:       aapfile.InlineSecretResolver{},
	})
	if err != nil {
		t.Fatal(err)
	}

	fileID := uploadAndComplete(t, service, staging)
	if _, err := worker.DrainForTest(ctx, 16); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetFile(ctx, testWorkspaceID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusFailed {
		t.Fatalf("status=%s want FAILED after required webhook hard fail", got.Status)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.WebhookStageName("req-fail"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobFailed {
		t.Fatalf("webhook job status=%s want FAILED", job.Status)
	}
}

func TestPipelineOptionalWebhookFailStillReady(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	insertProcessor(t, db, "opt-fail", testWebhookPublicURL, "secret-opt", false)

	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval:  50 * time.Millisecond,
		ClaimLease:    30 * time.Second,
		PublicBaseURL: "https://aap.test",
		HTTPClient:    testWebhookClient(server),
		Resolver:      publicTestResolver{ip: net.ParseIP("8.8.8.8")},
	})
	if err != nil {
		t.Fatal(err)
	}

	fileID := uploadAndComplete(t, service, staging)
	if _, err := worker.DrainForTest(ctx, 16); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetFile(ctx, testWorkspaceID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusReady {
		t.Fatalf("status=%s want READY when optional webhook fails", got.Status)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.WebhookStageName("opt-fail"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobFailed {
		t.Fatalf("optional webhook job status=%s want FAILED", job.Status)
	}
}

func TestPipelineWebhookTimeoutAndLateCallback(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	var deliveryBody []byte
	var deliveryMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		deliveryMu.Lock()
		deliveryBody = raw
		deliveryMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Short timeout so deadline is near-immediate after delivery.
	insertProcessorWithTimeout(t, db, "late-cb", testWebhookPublicURL, "secret-late", true, 1)

	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval:  50 * time.Millisecond,
		ClaimLease:    30 * time.Second,
		PublicBaseURL: "https://aap.test",
		HTTPClient:    testWebhookClient(server),
		Resolver:      publicTestResolver{ip: net.ParseIP("8.8.8.8")},
	})
	if err != nil {
		t.Fatal(err)
	}

	fileID := uploadAndComplete(t, service, staging)
	// Drain promote + webhook delivery (DELIVERED).
	if _, err := worker.DrainForTest(ctx, 8); err != nil {
		t.Fatal(err)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.WebhookStageName("late-cb"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobDelivered {
		t.Fatalf("status=%s want DELIVERED after partner 200", job.Status)
	}
	if job.DeliveryID == nil || job.DeadlineAt == nil {
		t.Fatalf("delivery_id/deadline required: %+v", job)
	}

	// Force deadline into the past for sweeper.
	if _, err := db.Exec(`
		UPDATE aap_file_processing_jobs
		SET deadline_at=clock_timestamp() - interval '1 second'
		WHERE id=$1
	`, job.ID); err != nil {
		t.Fatal(err)
	}

	// Timeout sweep claim.
	if _, err := worker.DrainForTest(ctx, 4); err != nil {
		t.Fatal(err)
	}
	job, err = repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.WebhookStageName("late-cb"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobTimedOut {
		t.Fatalf("status=%s want TIMED_OUT", job.Status)
	}
	file, err := service.GetFile(ctx, testWorkspaceID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if file.Status != aapfile.StatusFailed {
		t.Fatalf("file status=%s want FAILED after required timeout", file.Status)
	}
	failedStatus := file.Status

	// Late callback must not change terminal state.
	cbBody := []byte(`{"processorId":"late-cb","status":"succeeded","artifacts":[]}`)
	_, err = service.HandleProcessorCallback(ctx, aapfile.HandleProcessorCallbackInput{
		DeliveryID: *job.DeliveryID, Body: cbBody,
	})
	if !errors.Is(err, aapfile.ErrCallbackLate) {
		// ApplyCallbackCAS returns ErrCallbackLate for TIMED_OUT.
		if err == nil {
			t.Fatal("expected late callback error")
		}
		if !errors.Is(err, aapfile.ErrCallbackLate) && !strings.Contains(err.Error(), "LATE") {
			// Repository returns ErrCallbackLate
			t.Fatalf("expected ErrCallbackLate, got %v", err)
		}
	}
	file2, err := service.GetFile(ctx, testWorkspaceID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if file2.Status != failedStatus {
		t.Fatalf("late callback changed status %s → %s", failedStatus, file2.Status)
	}

	deliveryMu.Lock()
	if len(deliveryBody) == 0 {
		t.Fatal("partner should have received delivery body")
	}
	var payload map[string]any
	if err := json.Unmarshal(deliveryBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["specVersion"] != aapfile.ProcessorSpecVersion {
		t.Fatalf("specVersion=%v", payload["specVersion"])
	}
	deliveryMu.Unlock()
}

func TestPipelineWebhookSuccessThenReady(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	ctx := context.Background()
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	var gotSig atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig.Store(r.Header.Get(aapfile.SignatureHeaderName))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	secret := "whsec_test_success"
	insertProcessor(t, db, "ok-proc", testWebhookPublicURL, secret, true)

	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval:  50 * time.Millisecond,
		ClaimLease:    30 * time.Second,
		PublicBaseURL: "https://aap.test",
		HTTPClient:    testWebhookClient(server),
		Resolver:      publicTestResolver{ip: net.ParseIP("8.8.8.8")},
	})
	if err != nil {
		t.Fatal(err)
	}

	fileID := uploadAndComplete(t, service, staging)
	if _, err := worker.DrainForTest(ctx, 8); err != nil {
		t.Fatal(err)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.WebhookStageName("ok-proc"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobDelivered || job.DeliveryID == nil {
		t.Fatalf("job=%+v want DELIVERED", job)
	}
	if gotSig.Load() == nil || gotSig.Load().(string) == "" {
		t.Fatal("expected HMAC signature header on delivery")
	}

	// Partner callback success → READY.
	cb := []byte(`{"processorId":"ok-proc","status":"succeeded","artifacts":[],"attributes":{"risk":"low"}}`)
	// Need valid path through HandleProcessorCallback (signature verified at HTTP layer).
	result, err := service.HandleProcessorCallback(ctx, aapfile.HandleProcessorCallbackInput{
		DeliveryID: *job.DeliveryID, Body: cb,
	})
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if result.Job.Status != aapfile.JobSucceeded {
		t.Fatalf("job status=%s", result.Job.Status)
	}
	if result.File.Status != aapfile.StatusReady {
		t.Fatalf("file status=%s want READY", result.File.Status)
	}
}

func TestWebhookHMACReject(t *testing.T) {
	secret := "correct-secret"
	body := []byte(`{"status":"succeeded"}`)
	sig := aapfile.SignPayload(secret, body, time.Now().UTC())
	if err := aapfile.VerifySignature(secret, sig, body, time.Now().UTC(), aapfile.CallbackSignatureSkew); err != nil {
		t.Fatalf("valid sig rejected: %v", err)
	}
	if err := aapfile.VerifySignature("wrong", sig, body, time.Now().UTC(), aapfile.CallbackSignatureSkew); err == nil {
		t.Fatal("wrong secret must fail")
	}
	if err := aapfile.VerifySignature(secret, "t=1,v1=deadbeef", body, time.Now().UTC(), aapfile.CallbackSignatureSkew); err == nil {
		t.Fatal("bad mac must fail")
	}
	// Skew
	old := aapfile.SignPayload(secret, body, time.Now().UTC().Add(-time.Hour))
	if err := aapfile.VerifySignature(secret, old, body, time.Now().UTC(), aapfile.CallbackSignatureSkew); err == nil {
		t.Fatal("expired timestamp must fail")
	}
}

func TestValidateWebhookURLSSRF(t *testing.T) {
	ctx := context.Background()
	if err := aapfile.ValidateWebhookURL(ctx, "http://example.com/hook", nil); err == nil {
		t.Fatal("http must be rejected")
	}
	if err := aapfile.ValidateWebhookURL(ctx, "https://127.0.0.1/hook", nil); err == nil {
		t.Fatal("loopback must be rejected")
	}
	if err := aapfile.ValidateWebhookURL(ctx, "https://10.0.0.5/hook", nil); err == nil {
		t.Fatal("private must be rejected")
	}
	if err := aapfile.ValidateWebhookURL(ctx, "https://partner.example/hook", publicTestResolver{ip: net.ParseIP("8.8.8.8")}); err != nil {
		t.Fatalf("public host: %v", err)
	}
	if err := aapfile.ValidateWebhookURL(ctx, "https://evil.example/hook", publicTestResolver{ip: net.ParseIP("10.1.2.3")}); err == nil {
		t.Fatal("dns to private must be rejected")
	}
}

func TestVirusScanStageOptionalConfig(t *testing.T) {
	service, staging, _, db := newAAPFileService(t)
	// Enable virus scan as required via option on a fresh service.
	repo, err := aapfile.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err = aapfile.NewService(repo, staging, &fakeSecure{}, aapfile.WithVirusScan(aapfile.VirusScanConfig{
		Enabled: true, Required: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := aapfile.NewPipelineWorker(repo, service, aapfile.PipelineWorkerConfig{
		PollInterval: 50 * time.Millisecond,
		ClaimLease:   30 * time.Second,
		VirusScan:    aapfile.VirusScanConfig{Enabled: true, Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fileID := uploadAndComplete(t, service, staging)
	if _, err := worker.DrainForTest(ctx, 8); err != nil {
		t.Fatal(err)
	}
	got, err := service.GetFile(ctx, testWorkspaceID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != aapfile.StatusReady {
		t.Fatalf("status=%s want READY after virus stub", got.Status)
	}
	job, err := repo.GetJob(ctx, testWorkspaceID, fileID, aapfile.StageVirusScan)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != aapfile.JobSucceeded {
		t.Fatalf("virus_scan status=%s", job.Status)
	}
}

// --- helpers ---

type publicTestResolver struct{ ip net.IP }

func (r publicTestResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: r.ip}}, nil
}

func uploadAndComplete(t *testing.T, service *aapfile.Service, staging *fakeStaging) string {
	t.Helper()
	ctx := context.Background()
	content := pngBytes
	sum := sha256.Sum256(content)
	intent, err := service.CreateUploadIntent(ctx, createCCInput(len(content), hex.EncodeToString(sum[:])))
	if err != nil {
		t.Fatal(err)
	}
	staging.put(intent.File.StagingBucket, *intent.File.StagingObjectKey, content)
	if _, err := service.CompleteUpload(ctx, aapfile.CompleteUploadInput{
		Scope: aapfile.Scope{WorkspaceID: testWorkspaceID, AgentID: testAgentID}, FileID: intent.File.ID,
	}); err != nil {
		t.Fatal(err)
	}
	return intent.File.ID
}


func testWebhookClient(server *httptest.Server) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test only
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", server.Listener.Addr().String())
			},
		},
	}
}

const testWebhookPublicURL = "https://webhook.partner.test/hook"

func insertProcessor(t *testing.T, db *sql.DB, processorID, rawURL, secret string, required bool) {
	t.Helper()
	insertProcessorWithTimeout(t, db, processorID, rawURL, secret, required, 10000)
}

func insertProcessorWithTimeout(t *testing.T, db *sql.DB, processorID, rawURL, secret string, required bool, timeoutMs int) {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	_, err := db.Exec(`
		INSERT INTO aap_workspace_file_processors (
			id, workspace_id, processor_id, type, url, secret_ref, timeout_ms, required, enabled, events
		) VALUES ($1,$2,$3,'webhook',$4,$5,$6,$7,true,ARRAY['file.uploaded']::TEXT[])
	`, id, testWorkspaceID, processorID, rawURL, secret, timeoutMs, required)
	if err != nil {
		t.Fatalf("insert processor: %v", err)
	}
}
