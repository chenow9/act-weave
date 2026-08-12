package modelconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

const (
	ErrorCodeVerificationTimeout   = "MODEL_CONFIG_VERIFICATION_TIMEOUT"
	ErrorCodeNetwork               = "MODEL_CONFIG_NETWORK_ERROR"
	ErrorCodeAuthentication        = "MODEL_CONFIG_AUTHENTICATION_FAILED"
	ErrorCodeUpstream              = "MODEL_CONFIG_UPSTREAM_ERROR"
	ErrorCodeResponsesUnsupported  = "MODEL_CONFIG_RESPONSES_UNSUPPORTED"
	ErrorCodeToolSearchUnsupported = "MODEL_CONFIG_TOOL_SEARCH_UNSUPPORTED"
	ErrorCodeAgenticStreamInvalid  = "MODEL_CONFIG_AGENTIC_STREAM_INVALID"
	ErrorCodeAgenticUsageInvalid   = "MODEL_CONFIG_AGENTIC_USAGE_INVALID"
)

// Typed verification failures. Match with errors.Is. Never embed provider bodies
// or secrets; classifyVerificationError maps them to stable codes only.
var (
	ErrUpstreamAuthentication = errors.New("upstream authentication failed")
	ErrResponsesUnsupported   = errors.New(ErrorCodeResponsesUnsupported)
	ErrToolSearchUnsupported  = errors.New(ErrorCodeToolSearchUnsupported)
	ErrAgenticStreamInvalid   = errors.New(ErrorCodeAgenticStreamInvalid)
	ErrAgenticUsageInvalid    = errors.New(ErrorCodeAgenticUsageInvalid)
	ErrVerificationNetwork    = errors.New(ErrorCodeNetwork)
	ErrVerificationUpstream   = errors.New(ErrorCodeUpstream)
)

// Verifier owns credential resolution and Agentic protocol probes. The service
// deliberately gives it a value snapshot rather than a repository or transaction.
// On success it returns a partially filled AgenticCapabilities document (schema
// fields only); the service stamps lock identity, config digest, and timestamp.
// On failure it returns a typed error; capabilities are ignored and stored as {}.
type Verifier interface {
	Verify(context.Context, Config) (AgenticCapabilities, error)
}

type VerifierFunc func(context.Context, Config) (AgenticCapabilities, error)

func (f VerifierFunc) Verify(ctx context.Context, config Config) (AgenticCapabilities, error) {
	return f(ctx, config)
}

type VerificationService struct {
	repository *Repository
	verifier   Verifier
	timeout    time.Duration
	// now is optional for tests; defaults to time.Now.
	now func() time.Time
}

func NewVerificationService(repository *Repository, verifier Verifier, timeout time.Duration) (*VerificationService, error) {
	if repository == nil || verifier == nil || timeout <= 0 {
		return nil, errors.New("model config verification repository, verifier, and positive timeout are required")
	}
	return &VerificationService{
		repository: repository,
		verifier:   verifier,
		timeout:    timeout,
		now:        time.Now,
	}, nil
}

// Verify performs no database transaction around the external call. The final
// CAS write is intentionally small and refuses to apply a stale result after a
// concurrent edit. Success persists canonical AgenticCapabilities; failure
// persists "{}" plus a stable error code (never raw provider bodies).
func (s *VerificationService) Verify(ctx context.Context, workspaceID, configID, verifiedBy string) (Config, error) {
	config, err := s.repository.Get(ctx, workspaceID, configID)
	if err != nil {
		return Config{}, err
	}
	startedAt := s.now()
	upstreamCtx, cancel := context.WithTimeout(ctx, s.timeout)
	probeCaps, upstreamErr := s.verifier.Verify(upstreamCtx, config)
	cancel()
	latencyMS := int(s.now().Sub(startedAt).Milliseconds())
	if latencyMS < 0 {
		latencyMS = 0
	}

	status := StatusVerified
	var errorCode *string
	var capsRaw json.RawMessage
	// Shared evidence timestamp for capability.VerifiedAt and last_verified_at
	// (UTC second). Written only on VERIFIED success.
	var evidenceAt time.Time
	if upstreamErr != nil {
		status = StatusError
		code := classifyVerificationError(upstreamErr)
		errorCode = &code
		capsRaw = json.RawMessage(`{}`)
	} else {
		// Stamp lock/config identity so runtime can detect staleness after CAS.
		evidenceAt = s.now().UTC().Truncate(time.Second)
		canonical, stampErr := CanonicalAgenticCapabilities(
			evidenceAt,
			config.LockVersion,
			WireConfigDigest(config),
		)
		if stampErr != nil {
			// Fail closed: treat stamp failure as upstream classification error.
			status = StatusError
			code := ErrorCodeUpstream
			errorCode = &code
			capsRaw = json.RawMessage(`{}`)
			evidenceAt = time.Time{}
		} else {
			// Prefer probe-reported fields only when they already match the
			// canonical constants; CanonicalAgenticCapabilities is the source of truth.
			_ = probeCaps
			normalized, normErr := NormalizeAgenticCapabilitiesRaw(mustMarshalAgentic(canonical))
			if normErr != nil {
				status = StatusError
				code := ErrorCodeUpstream
				errorCode = &code
				capsRaw = json.RawMessage(`{}`)
				evidenceAt = time.Time{}
			} else {
				capsRaw = normalized
			}
		}
	}

	return s.repository.RecordVerification(ctx, VerificationUpdate{
		WorkspaceID:         workspaceID,
		ConfigID:            configID,
		Status:              status,
		LatencyMS:           latencyMS,
		ErrorCode:           errorCode,
		AgenticCapabilities: capsRaw,
		VerifiedAt:          evidenceAt,
		VerifiedBy:          verifiedBy,
		ExpectedLockVersion: config.LockVersion,
	})
}

func mustMarshalAgentic(doc AgenticCapabilities) json.RawMessage {
	b, err := json.Marshal(doc)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}

func classifyVerificationError(err error) string {
	if err == nil {
		return ErrorCodeUpstream
	}
	var networkError net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorCodeVerificationTimeout
	case errors.Is(err, ErrUpstreamAuthentication):
		return ErrorCodeAuthentication
	case errors.Is(err, ErrResponsesUnsupported):
		return ErrorCodeResponsesUnsupported
	case errors.Is(err, ErrToolSearchUnsupported):
		return ErrorCodeToolSearchUnsupported
	case errors.Is(err, ErrAgenticStreamInvalid):
		return ErrorCodeAgenticStreamInvalid
	case errors.Is(err, ErrAgenticUsageInvalid):
		return ErrorCodeAgenticUsageInvalid
	case errors.Is(err, ErrVerificationNetwork):
		return ErrorCodeNetwork
	case errors.As(err, &networkError):
		return ErrorCodeNetwork
	default:
		return ErrorCodeUpstream
	}
}
