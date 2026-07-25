package connection

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodeVerificationTimeout = "CONNECTION_VERIFICATION_TIMEOUT"
	ErrorCodeNetwork             = "CONNECTION_NETWORK_ERROR"
	ErrorCodeAuthentication      = "CONNECTION_AUTHENTICATION_FAILED"
	ErrorCodeUpstream            = "CONNECTION_UPSTREAM_ERROR"
)

var ErrUpstreamAuthentication = errors.New("upstream authentication failed")

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
}

func NewVerificationService(repository *Repository, verifier Verifier, timeout time.Duration) (*VerificationService, error) {
	if repository == nil || verifier == nil || timeout <= 0 {
		return nil, errors.New("connection verification repository, verifier, and positive timeout are required")
	}
	return &VerificationService{repository: repository, verifier: verifier, timeout: timeout}, nil
}

// Verify calls the upstream without an open database transaction, then stores
// only a stable code/category diagnostic in one short transaction.
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
	category, code := "OK", "CONNECTION_VERIFIED"
	var errorCode *string
	if upstreamErr != nil {
		status = "FAILED"
		category, code = classifyVerificationError(upstreamErr)
		errorCode = &code
	}
	diagnostics, err := json.Marshal(map[string]string{"category": category, "code": code})
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

func classifyVerificationError(err error) (string, string) {
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT", ErrorCodeVerificationTimeout
	case errors.Is(err, ErrUpstreamAuthentication):
		return "AUTHENTICATION", ErrorCodeAuthentication
	case errors.As(err, &networkError):
		return "NETWORK", ErrorCodeNetwork
	default:
		return "UPSTREAM", ErrorCodeUpstream
	}
}
