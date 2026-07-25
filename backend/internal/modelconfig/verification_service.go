package modelconfig

import (
	"context"
	"errors"
	"net"
	"time"
)

const (
	ErrorCodeVerificationTimeout = "MODEL_CONFIG_VERIFICATION_TIMEOUT"
	ErrorCodeNetwork             = "MODEL_CONFIG_NETWORK_ERROR"
	ErrorCodeAuthentication      = "MODEL_CONFIG_AUTHENTICATION_FAILED"
	ErrorCodeUpstream            = "MODEL_CONFIG_UPSTREAM_ERROR"
)

var ErrUpstreamAuthentication = errors.New("upstream authentication failed")

// Verifier owns any credential resolution and upstream protocol details. The
// service deliberately gives it a value snapshot rather than a repository or
// transaction.
type Verifier interface {
	Verify(context.Context, Config) error
}

type VerifierFunc func(context.Context, Config) error

func (f VerifierFunc) Verify(ctx context.Context, config Config) error { return f(ctx, config) }

type VerificationService struct {
	repository *Repository
	verifier   Verifier
	timeout    time.Duration
}

func NewVerificationService(repository *Repository, verifier Verifier, timeout time.Duration) (*VerificationService, error) {
	if repository == nil || verifier == nil || timeout <= 0 {
		return nil, errors.New("model config verification repository, verifier, and positive timeout are required")
	}
	return &VerificationService{repository: repository, verifier: verifier, timeout: timeout}, nil
}

// Verify performs no database transaction around the external call. The final
// CAS write is intentionally small and refuses to apply a stale result after a
// concurrent edit.
func (s *VerificationService) Verify(ctx context.Context, workspaceID, configID, verifiedBy string) (Config, error) {
	config, err := s.repository.Get(ctx, workspaceID, configID)
	if err != nil {
		return Config{}, err
	}
	startedAt := time.Now()
	upstreamCtx, cancel := context.WithTimeout(ctx, s.timeout)
	upstreamErr := s.verifier.Verify(upstreamCtx, config)
	cancel()
	latencyMS := int(time.Since(startedAt).Milliseconds())
	status := StatusVerified
	var errorCode *string
	if upstreamErr != nil {
		status = StatusError
		code := classifyVerificationError(upstreamErr)
		errorCode = &code
	}
	return s.repository.RecordVerification(ctx, VerificationUpdate{
		WorkspaceID: workspaceID, ConfigID: configID, Status: status,
		LatencyMS: latencyMS, ErrorCode: errorCode, VerifiedBy: verifiedBy,
		ExpectedLockVersion: config.LockVersion,
	})
}

func classifyVerificationError(err error) string {
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorCodeVerificationTimeout
	case errors.Is(err, ErrUpstreamAuthentication):
		return ErrorCodeAuthentication
	case errors.As(err, &networkError):
		return ErrorCodeNetwork
	default:
		return ErrorCodeUpstream
	}
}
