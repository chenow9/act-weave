package aapfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"actweave/backend/internal/metrics"

	"github.com/google/uuid"
)

// PipelineWorkerConfig bounds the promote/pipeline claim loop.
type PipelineWorkerConfig struct {
	PollInterval  time.Duration
	ClaimLease    time.Duration
	PublicBaseURL string // e.g. https://aap.example.com — used for download/callback URLs
	VirusScan     VirusScanConfig
	// HTTPClient is used for webhook delivery; defaults to SSRF-safe client.
	HTTPClient *http.Client
	Resolver   HostResolver
	Secrets    SecretResolver
	Metrics    *metrics.AAPFileCollector
	Logger     *slog.Logger
}

// DefaultPipelineWorkerConfig is the production baseline (idle-safe when no jobs).
func DefaultPipelineWorkerConfig() PipelineWorkerConfig {
	return PipelineWorkerConfig{
		PollInterval: 500 * time.Millisecond,
		ClaimLease:   2 * time.Minute,
	}
}

// PipelineWorker claims aap_file_processing_jobs and runs promote / stages / webhooks.
type PipelineWorker struct {
	repo    *Repository
	service *Service
	config  PipelineWorkerConfig
	client  *http.Client
	secrets SecretResolver
	metrics *metrics.AAPFileCollector
	logger  *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewPipelineWorker constructs a worker. Safe to Start even when files are disabled
// (loop idles when the jobs table is empty).
func NewPipelineWorker(
	repo *Repository,
	service *Service,
	config PipelineWorkerConfig,
) (*PipelineWorker, error) {
	if repo == nil || service == nil {
		return nil, errors.New("aapfile pipeline worker repository and service are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if config.ClaimLease <= 0 {
		config.ClaimLease = 2 * time.Minute
	}
	if config.PollInterval < 50*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("aapfile pipeline poll interval out of range")
	}
	if config.ClaimLease < time.Second || config.ClaimLease > 15*time.Minute {
		return nil, errors.New("aapfile pipeline claim lease out of range")
	}
	secrets := config.Secrets
	if secrets == nil {
		secrets = InlineSecretResolver{}
	}
	client := config.HTTPClient
	if client == nil {
		client = SafeHTTPClient(DefaultWebhookTimeout, config.Resolver)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	collector := config.Metrics
	if collector == nil {
		collector = metrics.DefaultAAPFile()
	}
	// Apply virus scan option onto service if not already set via WithVirusScan.
	if config.VirusScan.Enabled {
		service.virusScan = config.VirusScan
	}
	return &PipelineWorker{
		repo: repo, service: service, config: config,
		client: client, secrets: secrets, metrics: collector, logger: logger,
	}, nil
}

// Start begins the background claim loop.
func (w *PipelineWorker) Start(parent context.Context) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.loop(ctx)
}

// Stop cancels the loop and waits for exit.
func (w *PipelineWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	w.cancel = nil
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (w *PipelineWorker) loop(ctx context.Context) {
	defer close(w.done)
	for {
		processed, err := w.ProcessOne(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return
			}
			w.logger.Warn("aapfile pipeline process error", "err", err.Error())
		}
		if processed {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// ProcessOne claims and handles a single job. Returns true when work was found.
func (w *PipelineWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w == nil || w.repo == nil {
		return false, ErrInvalid
	}
	claimID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	job, found, err := w.repo.ClaimNextJob(ctx, claimID.String(), w.config.ClaimLease)
	if err != nil || !found {
		return false, err
	}
	start := time.Now().UTC()
	if err := w.handleClaimed(ctx, job, claimID.String()); err != nil {
		w.metrics.IncProcessing(normalizeStageLabel(job.Stage), "error")
		// Best-effort release for retryable failures.
		next := time.Now().UTC().Add(5 * time.Second)
		_ = w.repo.ReleaseJobClaim(ctx, job.ID, claimID.String(), &next, ErrorCodeProcessingFailed, JobPending)
		return true, err
	}
	if job.Stage == StagePromote {
		w.metrics.ObservePromoteDuration(time.Since(start).Milliseconds())
	}
	return true, nil
}

func (w *PipelineWorker) handleClaimed(ctx context.Context, job ProcessingJob, claimToken string) error {
	// Timeout sweep for overdue DELIVERED webhooks.
	if job.Status == JobDelivered {
		return w.timeoutDelivered(ctx, job, claimToken)
	}
	switch {
	case job.Stage == StagePromote:
		return w.runPromote(ctx, job, claimToken)
	case job.Stage == StageMIMEDetect:
		return w.runMIMEDetect(ctx, job, claimToken)
	case job.Stage == StageVirusScan:
		return w.runVirusScan(ctx, job, claimToken)
	case strings.HasPrefix(job.Stage, StageWebhookPrefix):
		return w.runWebhookDelivery(ctx, job, claimToken)
	default:
		_, err := w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobSkipped, "", MarshalJobMeta(false, map[string]any{
			"reason": "unknown_stage",
		}))
		if err != nil {
			return err
		}
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing(normalizeStageLabel(job.Stage), "skipped")
		return nil
	}
}

func (w *PipelineWorker) timeoutDelivered(ctx context.Context, job ProcessingJob, claimToken string) error {
	// Re-claim of DELIVERED keeps status DELIVERED (see ClaimNextJob).
	_, err := w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobTimedOut, ErrorCodeProcessingFailed, MarshalJobMeta(
		JobResultRequired(job.Result, job.Stage),
		map[string]any{"reason": "callback_timeout"},
	))
	if err != nil {
		return err
	}
	_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
	w.metrics.IncProcessing(normalizeStageLabel(job.Stage), "timed_out")
	return nil
}

func (w *PipelineWorker) runPromote(ctx context.Context, job ProcessingJob, claimToken string) error {
	// Service.Promote marks promote SUCCEEDED via ApplyPromoteSuccess (not claim-aware).
	// Complete the claim after so the row is not left RUNNING with a claim.
	file, err := w.service.Promote(ctx, job.WorkspaceID, job.FileID)
	if err != nil {
		// Promote may already have marked job FAILED; clear claim if still held.
		if errors.Is(err, ErrFailed) {
			_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeProcessingFailed, nil)
			w.metrics.IncProcessing(StagePromote, "failed")
			return nil
		}
		return err
	}
	// ApplyPromoteSuccess already set promote SUCCEEDED; clear claim if still ours.
	// If claim no longer matches (status SUCCEEDED without claim), ignore conflict.
	_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobSucceeded, "", MarshalJobMeta(true, map[string]any{
		"fileStatus": file.Status,
	}))
	// Re-evaluate in case follow-on stages already terminal (none).
	_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
	w.metrics.IncProcessing(StagePromote, "succeeded")
	return nil
}

func (w *PipelineWorker) runMIMEDetect(ctx context.Context, job ProcessingJob, claimToken string) error {
	// Prefer declared/detected already on the file (promote path records it).
	file, err := w.service.GetFile(ctx, job.WorkspaceID, job.FileID)
	if err != nil {
		return err
	}
	media := file.DeclaredMediaType
	if file.DetectedMediaType != nil && strings.TrimSpace(*file.DetectedMediaType) != "" {
		media = *file.DetectedMediaType
	}
	_, err = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobSucceeded, "", MarshalJobMeta(true, map[string]any{
		"mediaType": media,
	}))
	if err != nil {
		return err
	}
	_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
	w.metrics.IncProcessing(StageMIMEDetect, "succeeded")
	return nil
}

func (w *PipelineWorker) runVirusScan(ctx context.Context, job ProcessingJob, claimToken string) error {
	// v1 stub: always clean.
	required := JobResultRequired(job.Result, job.Stage)
	_, err := w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobSucceeded, "", MarshalJobMeta(required, map[string]any{
		"result": "clean",
		"stub":   true,
	}))
	if err != nil {
		return err
	}
	_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
	w.metrics.IncProcessing(StageVirusScan, "succeeded")
	return nil
}

func (w *PipelineWorker) runWebhookDelivery(ctx context.Context, job ProcessingJob, claimToken string) error {
	processorID := ProcessorIDFromStage(job.Stage)
	if processorID == "" {
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeWebhookDelivery, nil)
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}
	proc, err := w.repo.GetProcessor(ctx, job.WorkspaceID, processorID)
	if err != nil {
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeWebhookDelivery, nil)
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}
	if err := ValidateWebhookURL(ctx, proc.URL, w.config.Resolver); err != nil {
		status := JobFailed
		if !proc.Required {
			// Optional processor with bad URL: mark failed but do not block READY.
			status = JobFailed
		}
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, status, ErrorCodeWebhookSSRF, MarshalJobMeta(proc.Required, map[string]any{
			"reason": "ssrf_denied",
		}))
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}

	file, err := w.service.GetFile(ctx, job.WorkspaceID, job.FileID)
	if err != nil {
		return err
	}
	// File must be post-promote (permanent object) before processor download works.
	if file.StoredObjectID == nil || strings.TrimSpace(*file.StoredObjectID) == "" {
		return fmt.Errorf("webhook delivery before promote complete")
	}

	deliveryID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	// Mint processor_delivery download token (READY not required for processor path?
	// Design says download is for READY; but webhook runs while PROCESSING.
	// Use a direct token insert for processor_delivery on PROCESSING files.
	token, err := w.mintProcessorToken(ctx, file)
	if err != nil {
		return err
	}

	base := strings.TrimRight(strings.TrimSpace(w.config.PublicBaseURL), "/")
	if base == "" {
		base = "https://localhost"
	}
	downloadURL := base + "/api/agent-access/v1/files/downloads/" + token.ID
	callbackURL := base + "/api/agent-access/v1/internal/file-processor/callbacks/" + deliveryID.String()
	now := time.Now().UTC()
	timeout := time.Duration(proc.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultWebhookTimeout
	}
	// Callback deadline = max(processor timeout, default callback TTL).
	deadline := now.Add(timeout)
	if min := now.Add(DefaultWebhookCallbackTTL); deadline.Before(min) {
		deadline = min
	}

	_, body, err := BuildDeliveryPayload(file, deliveryID.String(), downloadURL, callbackURL, token.ExpiresAt, deadline)
	if err != nil {
		return err
	}
	secret, err := w.secrets.Resolve(ctx, proc.WorkspaceID, proc.SecretRef)
	if err != nil {
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeWebhookDelivery, nil)
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}
	sig := SignPayload(secret, body, now)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proc.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeaderName, sig)
	req.Header.Set("User-Agent", "ActWeave-FileProcessor/1.0")

	client := w.client
	if client.Timeout == 0 || client.Timeout > timeout+5*time.Second {
		// Clone with tighter timeout for this delivery.
		cloned := *client
		cloned.Timeout = timeout + 5*time.Second
		client = &cloned
	}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeWebhookDelivery, MarshalJobMeta(proc.Required, map[string]any{
			"reason": "http_error",
		}))
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = w.repo.CompleteClaimedJob(ctx, job.ID, claimToken, JobFailed, ErrorCodeWebhookDelivery, MarshalJobMeta(proc.Required, map[string]any{
			"reason":     "http_status",
			"statusCode": resp.StatusCode,
		}))
		_, _ = w.service.EvaluateReady(ctx, job.WorkspaceID, job.FileID)
		w.metrics.IncProcessing("webhook", "failed")
		return nil
	}

	meta := MarshalJobMeta(proc.Required, map[string]any{
		"processorId": processorID,
		"deliveryId":  deliveryID.String(),
	})
	_, err = w.repo.MarkJobDelivered(ctx, job.ID, claimToken, deliveryID.String(), deadline, meta)
	if err != nil {
		return err
	}
	w.metrics.IncProcessing("webhook", "delivered")
	return nil
}

// mintProcessorToken inserts a processor_delivery token for PROCESSING files
// (MintDownloadToken requires READY; processors run earlier).
func (w *PipelineWorker) mintProcessorToken(ctx context.Context, file File) (DownloadToken, error) {
	tokenID, err := uuid.NewV7()
	if err != nil {
		return DownloadToken{}, err
	}
	jti, err := uuid.NewV7()
	if err != nil {
		return DownloadToken{}, err
	}
	now := time.Now().UTC()
	maxBytes := file.SizeBytes
	token := DownloadToken{
		ID: tokenID.String(), WorkspaceID: file.WorkspaceID, FileID: file.ID,
		Purpose: DownloadPurposeProcessorDelivery, JTI: jti.String(),
		SingleUse: true, MaxBytes: &maxBytes,
		ExpiresAt: now.Add(DefaultProcessorDeliveryTokenTTL),
		CreatedBy: "system:file-pipeline",
	}
	return w.repo.InsertDownloadToken(ctx, token)
}

func normalizeStageLabel(stage string) string {
	stage = strings.TrimSpace(stage)
	if strings.HasPrefix(stage, StageWebhookPrefix) {
		return "webhook"
	}
	switch stage {
	case StagePromote, StageMIMEDetect, StageVirusScan:
		return stage
	default:
		return "other"
	}
}

// DrainForTest processes up to maxJobs claims (tests only).
func (w *PipelineWorker) DrainForTest(ctx context.Context, maxJobs int) (int, error) {
	if maxJobs < 1 {
		maxJobs = 32
	}
	n := 0
	for i := 0; i < maxJobs; i++ {
		ok, err := w.ProcessOne(ctx)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, nil
		}
		n++
	}
	return n, nil
}
