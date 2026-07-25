package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrStreamRevalidationInvalid = errors.New("AAP stream revalidation input is invalid")
	ErrTokenExpired              = errors.New("AAP access token expired")
	ErrAuthorizationRevoked      = errors.New("AAP stream authorization was revoked")
	ErrSecurityVersionChanged    = errors.New("AAP principal security version changed")
	ErrClientDisabled            = errors.New("AAP client is disabled")
	ErrGrantDisabled             = errors.New("AAP Agent grant is disabled")
	ErrAgentDisabled             = errors.New("AAP Agent is disabled")
	ErrWorkspaceDisabled         = errors.New("AAP Workspace is disabled")
)

type StreamBinding struct {
	WorkspaceID     string
	AgentID         string
	ClientID        string
	GrantID         string
	PrincipalID     string
	SubjectID       string
	SecurityVersion int64
	TokenExpiresAt  time.Time
}

type StreamAuthorizer interface {
	Reauthorize(context.Context, StreamBinding, time.Time) error
}

type RevalidationPolicy struct {
	Interval time.Duration
}

func DefaultRevalidationPolicy() RevalidationPolicy {
	return RevalidationPolicy{Interval: 60 * time.Second}
}

type StreamRevalidator struct {
	authorizer StreamAuthorizer
	changes    SecurityChangeSource
	policy     RevalidationPolicy
	now        func() time.Time
}

func (revalidator *StreamRevalidator) Validate(
	ctx context.Context,
	binding StreamBinding,
) error {
	if revalidator == nil || revalidator.authorizer == nil || ctx == nil {
		return ErrStreamRevalidationInvalid
	}
	binding = normalizeStreamBinding(binding)
	if !validStreamBinding(binding) {
		return ErrStreamRevalidationInvalid
	}
	return revalidator.check(ctx, binding)
}

func NewStreamRevalidator(
	authorizer StreamAuthorizer,
	changes SecurityChangeSource,
	policy RevalidationPolicy,
) (*StreamRevalidator, error) {
	if authorizer == nil || changes == nil || policy.Interval <= 0 || policy.Interval > 60*time.Second {
		return nil, ErrStreamRevalidationInvalid
	}
	return &StreamRevalidator{
		authorizer: authorizer, changes: changes, policy: policy,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Monitor returns a stable revocation reason. It performs an authorization
// check both before and after subscription so a security change in that race
// window cannot survive until the next periodic check.
func (revalidator *StreamRevalidator) Monitor(
	ctx context.Context,
	binding StreamBinding,
) error {
	if revalidator == nil || revalidator.authorizer == nil || revalidator.changes == nil ||
		ctx == nil {
		return ErrStreamRevalidationInvalid
	}
	binding = normalizeStreamBinding(binding)
	if err := revalidator.Validate(ctx, binding); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	subscription, err := revalidator.changes.Subscribe(ctx, binding)
	if err != nil {
		return err
	}
	defer subscription.Close()
	if err := revalidator.check(ctx, binding); err != nil {
		return err
	}

	remaining := binding.TokenExpiresAt.Sub(revalidator.now())
	if remaining <= 0 {
		return ErrTokenExpired
	}
	tokenTimer := time.NewTimer(remaining)
	defer tokenTimer.Stop()
	ticker := time.NewTicker(revalidator.policy.Interval)
	defer ticker.Stop()
	changes := subscription.Changes()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tokenTimer.C:
			return ErrTokenExpired
		case change, open := <-changes:
			if !open {
				changes = nil
				continue
			}
			if change.SecurityVersion != binding.SecurityVersion {
				return ErrSecurityVersionChanged
			}
			if err := revalidator.check(ctx, binding); err != nil {
				return err
			}
		case <-ticker.C:
			if err := revalidator.check(ctx, binding); err != nil {
				return err
			}
		}
	}
}

func (revalidator *StreamRevalidator) check(
	ctx context.Context,
	binding StreamBinding,
) error {
	now := revalidator.now()
	if !now.Before(binding.TokenExpiresAt) {
		return ErrTokenExpired
	}
	err := revalidator.authorizer.Reauthorize(ctx, binding, now)
	if err == nil {
		return nil
	}
	for _, stable := range []error{
		ErrTokenExpired, ErrSecurityVersionChanged, ErrClientDisabled,
		ErrGrantDisabled, ErrAgentDisabled, ErrWorkspaceDisabled,
		ErrAuthorizationRevoked,
	} {
		if errors.Is(err, stable) {
			return stable
		}
	}
	return ErrAuthorizationRevoked
}

func StreamErrorCode(err error) string {
	if errors.Is(err, ErrTokenExpired) {
		return "TOKEN_EXPIRED"
	}
	if IsStreamRevocation(err) {
		return "AUTHORIZATION_REVOKED"
	}
	return "STREAM_REAUTHORIZATION_FAILED"
}

func IsStreamRevocation(err error) bool {
	for _, target := range []error{
		ErrAuthorizationRevoked, ErrSecurityVersionChanged, ErrClientDisabled,
		ErrGrantDisabled, ErrAgentDisabled, ErrWorkspaceDisabled,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func normalizeStreamBinding(binding StreamBinding) StreamBinding {
	binding.WorkspaceID = strings.TrimSpace(binding.WorkspaceID)
	binding.AgentID = strings.TrimSpace(binding.AgentID)
	binding.ClientID = strings.TrimSpace(binding.ClientID)
	binding.GrantID = strings.TrimSpace(binding.GrantID)
	binding.PrincipalID = strings.TrimSpace(binding.PrincipalID)
	binding.SubjectID = strings.TrimSpace(binding.SubjectID)
	binding.TokenExpiresAt = binding.TokenExpiresAt.UTC()
	return binding
}

func validStreamBinding(binding StreamBinding) bool {
	if binding.SecurityVersion < 1 || binding.TokenExpiresAt.IsZero() {
		return false
	}
	for _, value := range []string{
		binding.WorkspaceID, binding.AgentID, binding.ClientID,
		binding.PrincipalID, binding.SubjectID,
	} {
		if !validStableIdentity(value) {
			return false
		}
	}
	return binding.GrantID == "" || validStableIdentity(binding.GrantID)
}

func validStableIdentity(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// ControlledStreamAuthorizer is the M4 boundary implementation. M6 replaces
// its in-memory state with repositories without changing StreamRevalidator.
type ControlledStreamAuthorizer struct {
	mu     sync.Mutex
	states map[string]controlledAuthorization
	checks uint64
}

type controlledAuthorization struct {
	securityVersion int64
	err             error
}

func NewControlledStreamAuthorizer() *ControlledStreamAuthorizer {
	return &ControlledStreamAuthorizer{states: make(map[string]controlledAuthorization)}
}

func (authorizer *ControlledStreamAuthorizer) Set(
	binding StreamBinding,
	securityVersion int64,
	err error,
) error {
	binding = normalizeStreamBinding(binding)
	if authorizer == nil || !validStreamBinding(binding) || securityVersion < 1 {
		return ErrStreamRevalidationInvalid
	}
	authorizer.mu.Lock()
	authorizer.states[streamBindingKey(binding)] = controlledAuthorization{
		securityVersion: securityVersion, err: err,
	}
	authorizer.mu.Unlock()
	return nil
}

func (authorizer *ControlledStreamAuthorizer) Reauthorize(
	_ context.Context,
	binding StreamBinding,
	_ time.Time,
) error {
	if authorizer == nil {
		return ErrAuthorizationRevoked
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.checks++
	state, exists := authorizer.states[streamBindingKey(binding)]
	if !exists {
		return nil
	}
	if state.securityVersion != binding.SecurityVersion {
		return ErrSecurityVersionChanged
	}
	return state.err
}

func (authorizer *ControlledStreamAuthorizer) Checks() uint64 {
	if authorizer == nil {
		return 0
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return authorizer.checks
}

func streamBindingKey(binding StreamBinding) string {
	return binding.WorkspaceID + "\x00" + binding.AgentID + "\x00" + binding.ClientID +
		"\x00" + binding.GrantID + "\x00" + binding.PrincipalID + "\x00" + binding.SubjectID
}
