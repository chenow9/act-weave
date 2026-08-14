package metrics

import (
	"strings"
	"sync/atomic"
)

// AAPFileCollector holds low-cardinality AAP file pipeline metrics (design §10.1).
// Labels: stage ∈ {promote,mime_detect,virus_scan,webhook,other},
// result ∈ {succeeded,failed,delivered,timed_out,skipped,error}.
// download purpose ∈ {client_content,tool_invoke,processor_delivery,unknown},
// download result ∈ {ok,not_found,consumed,purpose_denied,error}.
// Never labels file_id / download token / filename.
type AAPFileCollector struct {
	createTotal   atomic.Uint64
	completeTotal atomic.Uint64

	promoteDurationMs atomic.Int64 // last observed promote duration

	promoteSucceeded atomic.Uint64
	promoteFailed    atomic.Uint64
	promoteError     atomic.Uint64

	mimeSucceeded atomic.Uint64
	mimeFailed    atomic.Uint64

	virusSucceeded atomic.Uint64
	virusFailed    atomic.Uint64

	webhookDelivered atomic.Uint64
	webhookFailed    atomic.Uint64
	webhookTimedOut  atomic.Uint64
	webhookSucceeded atomic.Uint64

	downloadOK            atomic.Uint64
	downloadNotFound      atomic.Uint64
	downloadConsumed      atomic.Uint64
	downloadPurposeDenied atomic.Uint64
	downloadError         atomic.Uint64

	otherTotal atomic.Uint64

	pendingUploadGauge atomic.Int64
	stagingOrphanBytes atomic.Int64

	ingestGeneratedOK       atomic.Uint64
	ingestGeneratedDisabled atomic.Uint64
	ingestGeneratedDenied   atomic.Uint64
	ingestGeneratedError    atomic.Uint64
}

var defaultAAPFile = &AAPFileCollector{}

// DefaultAAPFile returns the process-wide AAP file metrics collector.
func DefaultAAPFile() *AAPFileCollector { return defaultAAPFile }

// NewAAPFileCollector returns an isolated collector for tests.
func NewAAPFileCollector() *AAPFileCollector { return &AAPFileCollector{} }

// IncCreate increments aap_file_create_total (no labels).
func (c *AAPFileCollector) IncCreate() {
	if c == nil {
		return
	}
	c.createTotal.Add(1)
}

// IncComplete increments aap_file_complete_total (no labels).
func (c *AAPFileCollector) IncComplete() {
	if c == nil {
		return
	}
	c.completeTotal.Add(1)
}

// ObservePromoteDuration records promote stage latency in milliseconds.
func (c *AAPFileCollector) ObservePromoteDuration(ms int64) {
	if c == nil {
		return
	}
	if ms < 0 {
		ms = 0
	}
	c.promoteDurationMs.Store(ms)
}

// IncProcessing increments processing_total{stage,result} style counters.
func (c *AAPFileCollector) IncProcessing(stage, result string) {
	if c == nil {
		return
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	result = strings.ToLower(strings.TrimSpace(result))
	switch stage {
	case "promote":
		switch result {
		case "succeeded":
			c.promoteSucceeded.Add(1)
		case "failed":
			c.promoteFailed.Add(1)
		default:
			c.promoteError.Add(1)
		}
	case "mime_detect":
		if result == "succeeded" {
			c.mimeSucceeded.Add(1)
		} else {
			c.mimeFailed.Add(1)
		}
	case "virus_scan":
		if result == "succeeded" {
			c.virusSucceeded.Add(1)
		} else {
			c.virusFailed.Add(1)
		}
	case "webhook":
		switch result {
		case "delivered":
			c.webhookDelivered.Add(1)
		case "failed":
			c.webhookFailed.Add(1)
		case "timed_out":
			c.webhookTimedOut.Add(1)
		case "succeeded":
			c.webhookSucceeded.Add(1)
		default:
			c.webhookFailed.Add(1)
		}
	default:
		c.otherTotal.Add(1)
	}
}

// IncDownload increments download_total{purpose,result}-style counters (IC-07).
// purpose/result are low-cardinality only; never pass token ids.
func (c *AAPFileCollector) IncDownload(purpose, result string) {
	if c == nil {
		return
	}
	_ = strings.ToLower(strings.TrimSpace(purpose)) // purpose reserved for future label split
	result = strings.ToLower(strings.TrimSpace(result))
	switch result {
	case "ok", "succeeded", "success":
		c.downloadOK.Add(1)
	case "not_found":
		c.downloadNotFound.Add(1)
	case "consumed":
		c.downloadConsumed.Add(1)
	case "purpose_denied", "purpose_mismatch":
		c.downloadPurposeDenied.Add(1)
	default:
		c.downloadError.Add(1)
	}
}

// IncIngestGenerated increments aap_file_ingest_generated_total{result}.
// result ∈ {ok, disabled, denied, error}; never pass file_id or filename.
func (c *AAPFileCollector) IncIngestGenerated(result string) {
	if c == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "ok", "succeeded", "success":
		c.ingestGeneratedOK.Add(1)
	case "disabled":
		c.ingestGeneratedDisabled.Add(1)
	case "denied":
		c.ingestGeneratedDenied.Add(1)
	default:
		c.ingestGeneratedError.Add(1)
	}
}

// SetPendingUploadGauge sets aap_file_pending_upload_gauge (process-wide count).
func (c *AAPFileCollector) SetPendingUploadGauge(n int64) {
	if c == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	c.pendingUploadGauge.Store(n)
}

// SetStagingOrphanBytes sets aap_file_staging_orphan_bytes (sum of residual staging size_bytes).
func (c *AAPFileCollector) SetStagingOrphanBytes(n int64) {
	if c == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	c.stagingOrphanBytes.Store(n)
}

// Snapshot returns a stable map for /metrics or tests.
// Keys never include file_id or other high-cardinality labels.
func (c *AAPFileCollector) Snapshot() map[string]uint64 {
	if c == nil {
		return map[string]uint64{}
	}
	return map[string]uint64{
		"aap_file_create_total":                 c.createTotal.Load(),
		"aap_file_complete_total":               c.completeTotal.Load(),
		"aap_file_promote_duration_ms":          uint64(c.promoteDurationMs.Load()),
		"aap_file_processing_promote_succeeded": c.promoteSucceeded.Load(),
		"aap_file_processing_promote_failed":    c.promoteFailed.Load(),
		"aap_file_processing_promote_error":     c.promoteError.Load(),
		"aap_file_processing_mime_succeeded":    c.mimeSucceeded.Load(),
		"aap_file_processing_mime_failed":       c.mimeFailed.Load(),
		"aap_file_processing_virus_succeeded":   c.virusSucceeded.Load(),
		"aap_file_processing_virus_failed":      c.virusFailed.Load(),
		"aap_file_processing_webhook_delivered": c.webhookDelivered.Load(),
		"aap_file_processing_webhook_failed":    c.webhookFailed.Load(),
		"aap_file_processing_webhook_timed_out": c.webhookTimedOut.Load(),
		"aap_file_processing_webhook_succeeded": c.webhookSucceeded.Load(),
		"aap_file_download_ok":                  c.downloadOK.Load(),
		"aap_file_download_not_found":           c.downloadNotFound.Load(),
		"aap_file_download_consumed":            c.downloadConsumed.Load(),
		"aap_file_download_purpose_denied":      c.downloadPurposeDenied.Load(),
		"aap_file_download_error":               c.downloadError.Load(),
		"aap_file_processing_other_total":       c.otherTotal.Load(),
		"aap_file_pending_upload_gauge":         uint64(c.pendingUploadGauge.Load()),
		"aap_file_staging_orphan_bytes":         uint64(c.stagingOrphanBytes.Load()),
		"aap_file_ingest_generated_ok":          c.ingestGeneratedOK.Load(),
		"aap_file_ingest_generated_disabled":    c.ingestGeneratedDisabled.Load(),
		"aap_file_ingest_generated_denied":      c.ingestGeneratedDenied.Load(),
		"aap_file_ingest_generated_error":       c.ingestGeneratedError.Load(),
	}
}
