package chatruntimebridge

import (
	"encoding/json"

	"actweave/backend/internal/modelconfig"
)

// MarshalNodeModelSnapshot is the closed producer for graph node model blobs.
// credentialSecretId is always present; unbound is JSON null.
func MarshalNodeModelSnapshot(config modelconfig.Config) (json.RawMessage, error) {
	agentic := json.RawMessage(config.AgenticCapabilities)
	if len(agentic) == 0 {
		agentic = json.RawMessage(`{}`)
	}
	runtime := json.RawMessage(config.RuntimeCapabilities)
	if len(runtime) == 0 {
		runtime = json.RawMessage(`{}`)
	}
	options := json.RawMessage(config.Options)
	if len(options) == 0 || string(options) == "null" {
		options = json.RawMessage(`{}`)
	}
	policy := json.RawMessage(config.ToolDisclosurePolicy)
	if modelconfig.IsUnsetToolDisclosurePolicy(policy) {
		policy = json.RawMessage(`{}`)
	}
	var cred any
	if config.CredentialSecretID != nil {
		cred = *config.CredentialSecretID
	}
	return json.Marshal(map[string]any{
		"id": config.ID, "provider": config.Provider, "apiBase": config.APIBase,
		"modelName": config.ModelName, "options": options,
		"credentialSecretId": cred, "lockVersion": config.LockVersion,
		"status": config.Status, "agenticCapabilities": agentic,
		"runtimeCapabilities": runtime, "toolDisclosurePolicy": policy,
	})
}
