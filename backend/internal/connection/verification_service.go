package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodeVerificationTimeout = "CONNECTION_VERIFICATION_TIMEOUT"
	ErrorCodeNetwork             = "CONNECTION_NETWORK_ERROR"
	ErrorCodeAuthentication      = "CONNECTION_AUTHENTICATION_FAILED"
	ErrorCodeUpstream            = "CONNECTION_UPSTREAM_ERROR"
)

var (
	ErrUpstreamAuthentication = errors.New("upstream authentication failed")
	// safeDetailPattern allows only low-sensitivity diagnostic tokens (no secrets).
	safeDetailPattern = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_ .:/=-]{0,120}$`)
	httpStatusDetail  = regexp.MustCompile(`(?i)\bHTTP_STATUS_(\d{3})\b`)
)

type Verifier interface {
	Verify(context.Context, Connection) error
}

type VerifierFunc func(context.Context, Connection) error

func (f VerifierFunc) Verify(ctx context.Context, connection Connection) error {
	return f(ctx, connection)
}

type VerificationService struct {
	repository *Repository
	verifier   Verifier
	timeout    time.Duration
	logger     *slog.Logger
}

func NewVerificationService(repository *Repository, verifier Verifier, timeout time.Duration) (*VerificationService, error) {
	if repository == nil || verifier == nil || timeout <= 0 {
		return nil, errors.New("connection verification repository, verifier, and positive timeout are required")
	}
	return &VerificationService{
		repository: repository,
		verifier:   verifier,
		timeout:    timeout,
		logger:     slog.Default(),
	}, nil
}

// Verify calls the upstream without an open database transaction, then stores
// only a stable code/category (+ safe detail) diagnostic in one short transaction.
func (s *VerificationService) Verify(ctx context.Context, workspaceID, connectionID, testedBy string) (Verification, error) {
	value, err := s.repository.Get(ctx, workspaceID, connectionID)
	if err != nil {
		return Verification{}, err
	}
	startedAt := time.Now()
	upstreamCtx, cancel := context.WithTimeout(ctx, s.timeout)
	upstreamErr := s.verifier.Verify(upstreamCtx, value)
	cancel()
	latencyMS := int(time.Since(startedAt).Milliseconds())
	status := "SUCCEEDED"
	category, code, detail := "OK", "CONNECTION_VERIFIED", ""
	var errorCode *string
	if upstreamErr != nil {
		status = "FAILED"
		category, code, detail = classifyVerificationError(upstreamErr)
		errorCode = &code
		logger := s.logger
		if logger == nil {
			logger = slog.Default()
		}
		// Intentionally log only redacted fields so credentials never enter ops logs.
		logger.Warn("connection verification failed",
			"workspace_id", workspaceID,
			"connection_id", connectionID,
			"provider_id", value.ProviderID,
			"error_code", code,
			"category", category,
			"detail", detail,
			"latency_ms", latencyMS,
			"err_type", fmt.Sprintf("%T", upstreamErr),
		)
	}
	payload := map[string]string{"category": category, "code": code}
	if detail != "" {
		payload["detail"] = detail
	}
	diagnostics, err := json.Marshal(payload)
	if err != nil {
		return Verification{}, err
	}
	verificationID, err := uuid.NewV7()
	if err != nil {
		return Verification{}, err
	}
	return s.repository.RecordVerification(ctx, NewVerification{
		ID: verificationID.String(), WorkspaceID: workspaceID, ConnectionID: connectionID,
		Status: status, Diagnostics: diagnostics, LatencyMS: &latencyMS, TestedBy: testedBy,
		ErrorCode: errorCode, ExpectedLockVersion: value.LockVersion,
	})
}

func classifyVerificationError(err error) (category, code, detail string) {
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT", ErrorCodeVerificationTimeout, "deadline_exceeded"
	case errors.Is(err, ErrUpstreamAuthentication):
		return "AUTHENTICATION", ErrorCodeAuthentication, "http_unauthorized_or_forbidden"
	case errors.Is(err, ErrConflict):
		return "UPSTREAM", ErrorCodeUpstream, "provider_not_active_or_conflict"
	case errors.Is(err, ErrInvalid):
		return "UPSTREAM", ErrorCodeUpstream, "invalid_connection_or_provider_config"
	case errors.As(err, &networkError):
		detail = "network_error"
		if networkError.Timeout() {
			detail = "network_timeout"
		}
		return "NETWORK", ErrorCodeNetwork, detail
	default:
		return "UPSTREAM", ErrorCodeUpstream, safeVerificationDetail(err)
	}
}

// safeVerificationDetail extracts a stable, non-sensitive operator hint.
// Prefer structured tokens (HTTP_STATUS_502); never forward raw error text that
// may embed credentials, tokens, or full request URLs with secrets.
func safeVerificationDetail(err error) string {
	if err == nil {
		return "upstream_error"
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "upstream_error"
	}
	if m := httpStatusDetail.FindStringSubmatch(msg); len(m) == 2 {
		return "HTTP_STATUS_" + m[1]
	}
	// Known safe prefixes produced by application verifiers / guards.
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "invalid connection verification url"):
		return "invalid_verification_url"
	case strings.Contains(lower, "provider service base url is required"):
		return "missing_service_base_url"
	case strings.Contains(lower, "egress"):
		return "egress_policy_rejected"
	case strings.Contains(lower, "dns") || strings.Contains(lower, "no such host"):
		return "dns_lookup_failed"
	case strings.Contains(lower, "connection refused"):
		return "connection_refused"
	case strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate"):
		return "tls_handshake_failed"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "timeout"
	case strings.Contains(lower, "i/o timeout"):
		return "io_timeout"
	}
	// Allow only already-safe, short, token-like messages.
	if safeDetailPattern.MatchString(msg) && !looksSensitiveVerificationDetail(msg) {
		return msg
	}
	return "upstream_error"
}

func looksSensitiveVerificationDetail(msg string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(msg))
	for _, token := range []string{
		"password", "secret", "token", "apikey", "authorization", "bearer", "privatekey", "credential",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
