package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/config"

	"github.com/google/uuid"
)

func TestProcessorCallbackHMACAndLate(t *testing.T) {
	filesOn := config.AgentAccessFilesConfig{
		Enabled: true, AllowAllWorkspaces: true, AllowAllClients: true,
	}
	domain := &callbackTestDomain{
		secret: "cb-secret",
		job: aapfile.ProcessingJob{
			ID: uuid.NewString(), WorkspaceID: aapFileWorkspaceID, FileID: aapFileAgentID,
			Stage: aapfile.WebhookStageName("partner"), Status: aapfile.JobDelivered,
			DeliveryID: strPtr(uuid.NewString()),
			DeadlineAt: timePtr(time.Now().UTC().Add(5 * time.Minute)),
			Result:     aapfile.MarshalJobMeta(true, map[string]any{"processorId": "partner"}),
		},
		proc: aapfile.WorkspaceFileProcessor{
			WorkspaceID: aapFileWorkspaceID, ProcessorID: "partner",
			SecretRef: "cb-secret", URL: "https://partner.test/hook",
		},
		file: aapfile.File{
			ID: aapFileAgentID, WorkspaceID: aapFileWorkspaceID, Status: aapfile.StatusProcessing,
		},
	}
	// Use stable delivery id
	deliveryID := uuid.NewString()
	domain.job.DeliveryID = &deliveryID

	store := newMemoryFileStore()
	staging := newMemoryStaging()
	secure := &memorySecurePutter{}
	fileDomain, err := aapfile.NewService(store, staging, secure)
	if err != nil {
		t.Fatal(err)
	}
	app, err := aap.NewFileService(fileDomain)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewAAPFileRoutes(&aapFileAuthorizer{}, app, nil, &filesOn)
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.ConfigureProcessorCallback(domain, aapfile.InlineSecretResolver{}); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{
		AgentAccessAuthenticator: aapFileTokenAuthenticator{},
		AgentAccessRegistrars:    []AgentAccessV1RouteRegistrar{routes},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"processorId":"partner","status":"succeeded","artifacts":[]}`)
	path := "/api/agent-access/v1/internal/file-processor/callbacks/" + deliveryID

	t.Run("reject missing signature", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "FILE_PROCESSOR_CALLBACK_UNAUTHORIZED") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("reject bad signature", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(aapfile.SignatureHeaderName, aapfile.SignPayload("wrong", body, time.Now().UTC()))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("accept valid signature without bearer", func(t *testing.T) {
		domain.handleOK = true
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(aapfile.SignatureHeaderName, aapfile.SignPayload("cb-secret", body, time.Now().UTC()))
		// Explicitly no Authorization header.
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp["deliveryId"] != deliveryID {
			t.Fatalf("resp=%v", resp)
		}
	})

	t.Run("late callback 409", func(t *testing.T) {
		domain.job.Status = aapfile.JobTimedOut
		domain.handleOK = false
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(aapfile.SignatureHeaderName, aapfile.SignPayload("cb-secret", body, time.Now().UTC()))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "FILE_PROCESSOR_CALLBACK_LATE") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("oversized artifact 422", func(t *testing.T) {
		domain.job.Status = aapfile.JobDelivered
		domain.handleErr = aapfile.ErrArtifactTooLarge
		domain.handleOK = false
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(aapfile.SignatureHeaderName, aapfile.SignPayload("cb-secret", body, time.Now().UTC()))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "FILE_PROCESSOR_ARTIFACT_TOO_LARGE") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})
}

type callbackTestDomain struct {
	secret    string
	job       aapfile.ProcessingJob
	proc      aapfile.WorkspaceFileProcessor
	file      aapfile.File
	handleOK  bool
	handleErr error
}

func (d *callbackTestDomain) LookupDeliveryForCallback(
	_ context.Context, deliveryID string,
) (aapfile.ProcessingJob, aapfile.WorkspaceFileProcessor, aapfile.File, error) {
	if d.job.DeliveryID == nil || *d.job.DeliveryID != deliveryID {
		return aapfile.ProcessingJob{}, aapfile.WorkspaceFileProcessor{}, aapfile.File{}, aapfile.ErrNotFound
	}
	return d.job, d.proc, d.file, nil
}

func (d *callbackTestDomain) HandleProcessorCallback(
	_ context.Context, input aapfile.HandleProcessorCallbackInput,
) (aapfile.HandleProcessorCallbackResult, error) {
	if d.handleErr != nil {
		return aapfile.HandleProcessorCallbackResult{}, d.handleErr
	}
	if !d.handleOK {
		return aapfile.HandleProcessorCallbackResult{}, aapfile.ErrInvalid
	}
	job := d.job
	job.Status = aapfile.JobSucceeded
	file := d.file
	file.Status = aapfile.StatusReady
	return aapfile.HandleProcessorCallbackResult{Job: job, File: file}, nil
}

func strPtr(v string) *string { return &v }
func timePtr(v time.Time) *time.Time { return &v }
