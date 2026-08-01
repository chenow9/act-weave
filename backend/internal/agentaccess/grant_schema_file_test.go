package agentaccess_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"actweave/backend/internal/agentaccess"
)

func TestGrantSchemaIncludesFileScopesAndSharingResource(t *testing.T) {
	scopes := agentaccess.KnownAgentScopes()
	if len(scopes) != 11 {
		t.Fatalf("KnownAgentScopes len=%d want 11", len(scopes))
	}
	if !slices.Contains(scopes, agentaccess.ScopeFileWrite) ||
		!slices.Contains(scopes, agentaccess.ScopeFileRead) {
		t.Fatalf("file scopes missing: %v", scopes)
	}
	resources := agentaccess.KnownSubjectSharingResources()
	if len(resources) != 6 || !slices.Contains(resources, agentaccess.SubjectSharingFile) {
		t.Fatalf("subjectSharing resources=%v want file among 6", resources)
	}

	raw, err := agentaccess.GrantConfigurationSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Scopes struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Enum []agentaccess.AgentScope `json:"enum"`
				} `json:"items"`
			} `json:"scopes"`
			Policy struct {
				Properties struct {
					SubjectSharing struct {
						OneOf []struct {
							Properties struct {
								Resources struct {
									MaxItems int `json:"maxItems"`
									Items    struct {
										Enum []agentaccess.SubjectSharingResource `json:"enum"`
									} `json:"items"`
								} `json:"resources"`
							} `json:"properties"`
						} `json:"oneOf"`
					} `json:"subjectSharing"`
				} `json:"properties"`
			} `json:"policy"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties.Scopes.MaxItems != 11 {
		t.Fatalf("scopes maxItems=%d want 11", schema.Properties.Scopes.MaxItems)
	}
	if !slices.Equal(schema.Properties.Scopes.Items.Enum, agentaccess.KnownAgentScopes()) {
		t.Fatalf("schema scopes=%v go=%v", schema.Properties.Scopes.Items.Enum, agentaccess.KnownAgentScopes())
	}
	sharing := schema.Properties.Policy.Properties.SubjectSharing.OneOf
	if len(sharing) != 2 || sharing[1].Properties.Resources.MaxItems != 6 {
		t.Fatalf("subjectSharing oneOf=%+v", sharing)
	}
	if !slices.Equal(
		sharing[1].Properties.Resources.Items.Enum,
		agentaccess.KnownSubjectSharingResources(),
	) {
		t.Fatalf("schema resources=%v go=%v",
			sharing[1].Properties.Resources.Items.Enum, agentaccess.KnownSubjectSharingResources())
	}

	// Old grant without file scopes remains valid.
	legacy := json.RawMessage(`{"scopes":["agent:read","run:create"],"policy":{}}`)
	if _, err := agentaccess.ValidateGrantConfiguration(legacy); err != nil {
		t.Fatalf("legacy grant without file scopes must remain valid: %v", err)
	}

	// New grant with file scopes is accepted.
	withFile := json.RawMessage(`{
		"scopes":["file:write","file:read","agent:read"],
		"policy":{"subjectSharing":{"enabled":true,"resources":["file","conversation"]}}
	}`)
	configuration, err := agentaccess.ValidateGrantConfiguration(withFile)
	if err != nil {
		t.Fatalf("file grant: %v", err)
	}
	if !slices.Contains(configuration.Scopes, agentaccess.ScopeFileWrite) ||
		!slices.Contains(configuration.Policy.SubjectSharing.Resources, agentaccess.SubjectSharingFile) {
		t.Fatalf("parsed file grant=%+v", configuration)
	}

	// Unknown file-adjacent resource rejected.
	invalid := json.RawMessage(`{
		"scopes":["file:read"],
		"policy":{"subjectSharing":{"enabled":true,"resources":["files"]}}
	}`)
	if _, err := agentaccess.ValidateGrantConfiguration(invalid); !errors.Is(err, agentaccess.ErrGrantConfigurationInvalid) {
		t.Fatalf("invalid sharing resource accepted: %v", err)
	}
}
