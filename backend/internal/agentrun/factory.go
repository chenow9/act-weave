package agentrun

import (
	"encoding/json"
	"strings"

	"actweave/backend/internal/config"
	"actweave/backend/internal/metrics"
)

// Factory is the production agent Runtime facade (eino-only).
//
// Enqueue / EnqueueContinue always route to the eino bridge. Agent.Enabled /
// allowlist no longer select an alternate engine; they remain for
// diagnostics/metrics only.
//
// Rollback = previous binary / drain traffic. See
// docs/runbooks/eino-agent-runtime-rollout.md.
//
// Continue path callers must require nested einoChatResume (application
// ContinueDispatcher); chatLoop-only snapshots are invalid.
type Factory struct {
	agent config.RuntimeFeatureRollout
	eino  Runtime // required production engine
}

// NewFactory builds a production Runtime facade. eino is required.
func NewFactory(agent config.RuntimeFeatureRollout, eino Runtime) (*Factory, error) {
	if eino == nil {
		return nil, errFactoryEinoRequired
	}
	return &Factory{
		agent: agent.Normalized(),
		eino:  eino,
	}, nil
}

// Compile-time: Factory is a production Runtime.
var _ Runtime = (*Factory)(nil)

// Enqueue always routes to eino and records engine=eino metrics.
func (f *Factory) Enqueue(job Job) {
	if f == nil || f.eino == nil {
		metrics.Default().ObserveAgentEngineEnqueue("refused")
		return
	}
	metrics.Default().ObserveAgentEngineEnqueue("eino")
	f.eino.Enqueue(job)
}

// CancelRun cancels on the eino runtime.
func (f *Factory) CancelRun(workspaceID, runID string) error {
	if f == nil || f.eino == nil {
		return errFactoryEinoRequired
	}
	return f.eino.CancelRun(workspaceID, runID)
}

// EnqueueContinueWithLifecycle always routes to eino.
// Application ContinueDispatcher must already have selected einoChatResume;
// this method does not branch on snapshot content or allowlist.
func (f *Factory) EnqueueContinueWithLifecycle(
	job Job,
	requestSnapshot, toolResult json.RawMessage,
	life ContinueLifecycle,
) {
	if f == nil || f.eino == nil {
		return
	}
	f.eino.EnqueueContinueWithLifecycle(job, requestSnapshot, toolResult, life)
}

// Eino returns the eino bridge Runtime (never nil after successful NewFactory).
func (f *Factory) Eino() Runtime {
	if f == nil {
		return nil
	}
	return f.eino
}

// AllowsWorkspace reports the configured rollout allowlist. This no longer
// changes Enqueue routing (always eino); retained for ops/diagnostics.
func (f *Factory) AllowsWorkspace(workspaceID string) bool {
	if f == nil {
		return false
	}
	return f.agent.AllowsWorkspace(strings.TrimSpace(workspaceID))
}

// errFactoryEinoRequired is returned when NewFactory is called without eino.
var errFactoryEinoRequired = errString("agentrun factory: eino Runtime is required")

type errString string

func (e errString) Error() string { return string(e) }
