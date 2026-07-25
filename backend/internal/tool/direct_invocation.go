package tool

import (
	"context"
	"errors"
	"strings"
	"time"

	"actweave/backend/internal/execution"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/principal"
)

type DirectInvocationService struct {
	resolver *InvocationResolver
	pipeline *execution.InvocationPipeline
	// attacher is optional; required when REQUEST_PASSTHROUGH envelope is present
	// on a top-level direct invoke (no AgentRun/Workflow root).
	attacher *outboundidentity.BindingAttacher
	bootID   string
}

func NewDirectInvocationService(resolver *InvocationResolver, pipeline *execution.InvocationPipeline) (*DirectInvocationService, error) {
	if resolver == nil || pipeline == nil {
		return nil, errors.New("tool direct invocation resolver and pipeline are required")
	}
	return &DirectInvocationService{resolver: resolver, pipeline: pipeline}, nil
}

// WithBindingAttacher enables Vault attach for top-level REQUEST_PASSTHROUGH invokes.
// bootID must match the RuntimeCredentialVault process boot used by the dual-mode injector.
func (s *DirectInvocationService) WithBindingAttacher(attacher *outboundidentity.BindingAttacher, bootID string) *DirectInvocationService {
	if s != nil {
		s.attacher = attacher
		s.bootID = strings.TrimSpace(bootID)
	}
	return s
}

// Invoke resolves an optional release selection first, then executes exactly
// that immutable release through the normal authorization/idempotency pipeline.
func (s *DirectInvocationService) Invoke(ctx context.Context, request execution.InvokeRequest) (execution.PipelineResult, error) {
	resolved, err := s.resolver.ResolveInvocation(ctx, execution.ResolveRequest{
		WorkspaceID: request.WorkspaceID, CapabilityID: request.CapabilityID,
		ReleaseID: strings.TrimSpace(request.ReleaseID), ExplicitConnectionID: request.ExplicitConnectionID,
		PlanConnectionID: request.PlanConnectionID, BindingConnectionID: request.BindingConnectionID,
	})
	if err != nil {
		return execution.PipelineResult{}, err
	}
	request.ReleaseID = resolved.Snapshot.ReleaseID

	// Ensure dual-mode injector has a principal for top-level USER invokes.
	if request.PrincipalSnapshot == nil && strings.EqualFold(request.ActorType, "USER") {
		snapshot, snapErr := principal.NewInternalExecutionSnapshot(
			request.WorkspaceID, principal.TypeUser, request.ActorID,
		)
		if snapErr == nil {
			request.PrincipalSnapshot = &snapshot
		}
	}

	cleanup, attachErr := s.attachDirectOutbound(ctx, request, resolved)
	if attachErr != nil {
		_ = outboundidentity.ZeroCredentialsRaw(request.OutboundCredentialsRaw)
		return execution.PipelineResult{}, attachErr
	}
	defer cleanup()
	_ = outboundidentity.ZeroCredentialsRaw(request.OutboundCredentialsRaw)
	request.OutboundCredentialsRaw = nil

	return s.pipeline.InvokeResolved(ctx, request, resolved)
}

// attachDirectOutbound attaches REQUEST_PASSTHROUGH credentials for top-level
// DIRECT_INVOCATION only. Nested AgentRun / Workflow roots inherit vault entries
// from their parent attach and must not re-bind from the invoke body.
func (s *DirectInvocationService) attachDirectOutbound(
	ctx context.Context,
	request execution.InvokeRequest,
	resolved execution.ResolvedInvocation,
) (func(), error) {
	noop := func() {}
	// Nested roots: parent attach owns vault entries.
	if strings.TrimSpace(request.AgentRunID) != "" || strings.TrimSpace(request.WorkflowExecutionID) != "" {
		if len(request.OutboundCredentialsRaw) > 0 {
			return noop, outboundidentity.ErrCredentialInvalid
		}
		return noop, nil
	}

	mode := strings.ToUpper(strings.TrimSpace(resolved.Credential.OutboundMode))
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(resolved.Credential.AuthMode))
	}
	needsPassthrough := mode == string(outboundidentity.ModeRequestPassthrough)
	hasEnvelope := len(request.OutboundCredentialsRaw) > 0 && string(request.OutboundCredentialsRaw) != "null"

	if !needsPassthrough {
		if hasEnvelope {
			return noop, outboundidentity.ErrCredentialInvalid
		}
		return noop, nil
	}
	if !hasEnvelope {
		return noop, outboundidentity.ErrCredentialRequired
	}
	if s == nil || s.attacher == nil {
		return noop, outboundidentity.ErrCredentialInvalid
	}

	requirements, err := parseToolTestRequirements(resolved.Credential)
	if err != nil {
		return noop, err
	}
	views, err := connectionViewsFromCredential(resolved.Connection, resolved.Credential, requirements)
	if err != nil {
		return noop, err
	}

	bootID := strings.TrimSpace(s.bootID)
	if bootID == "" {
		bootID = "direct-invoke-boot"
	}
	subjectType := outboundidentity.SubjectTypeUser
	subjectID := strings.TrimSpace(request.ActorID)
	if request.PrincipalSnapshot != nil && request.PrincipalSnapshot.Identity.Subject != nil {
		switch request.PrincipalSnapshot.Identity.Subject.Type {
		case principal.TypeExternalSubject:
			subjectType = outboundidentity.SubjectTypeExternalSubject
		case principal.TypeUser:
			subjectType = outboundidentity.SubjectTypeUser
		}
		subjectID = request.PrincipalSnapshot.Identity.Subject.ID
	}
	rootDeadline := time.Now().UTC().Add(15 * time.Minute)
	attachResult, attachErr := s.attacher.Attach(ctx, outboundidentity.BindingAttachInput{
		RawEnvelope:  request.OutboundCredentialsRaw,
		Requirements: requirements,
		Connections:  views,
		Context: outboundidentity.BindingAttachContext{
			BootID: bootID, WorkspaceID: request.WorkspaceID,
			SubjectType: subjectType, SubjectID: subjectID,
			RootScopeType: outboundidentity.RootScopeDirectInvocation,
			RootScopeID:   request.InvocationID,
			RootDeadline:  rootDeadline,
		},
	})
	if attachErr != nil {
		return noop, attachErr
	}
	affinityClaimed := attachResult.AffinityClaimed
	return func() {
		s.attacher.CleanupRequest(context.WithoutCancel(ctx), outboundidentity.BindingAttachContext{
			BootID: bootID, WorkspaceID: request.WorkspaceID,
			SubjectType: subjectType, SubjectID: subjectID,
			RootScopeType: outboundidentity.RootScopeDirectInvocation,
			RootScopeID:   request.InvocationID,
		}, affinityClaimed)
	}, nil
}
