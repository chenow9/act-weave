package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/capability"

	"github.com/gin-gonic/gin"
)

var ErrAAPAgentProfileUnavailable = errors.New("AAP Agent Profile is unavailable")

type AAPAgentProfileStore interface {
	GetSummary(context.Context, string, string) (agent.Summary, error)
}

type AAPAgentProfileCatalog interface {
	ListForAgent(context.Context, string, string) ([]capability.Descriptor, error)
}

type AAPAgentProfileRoutes struct {
	authorizer AAPDataPlaneAuthorizer
	agents     AAPAgentProfileStore
	catalog    AAPAgentProfileCatalog
}

func NewAAPAgentProfileRoutes(
	authorizer AAPDataPlaneAuthorizer,
	agents AAPAgentProfileStore,
	catalog AAPAgentProfileCatalog,
) (*AAPAgentProfileRoutes, error) {
	if authorizer == nil || agents == nil || catalog == nil {
		return nil, errors.New("AAP Agent Profile route dependencies are required")
	}
	return &AAPAgentProfileRoutes{authorizer: authorizer, agents: agents, catalog: catalog}, nil
}

func (routes *AAPAgentProfileRoutes) RegisterAgentAccessV1(v1 AgentAccessV1Routes) {
	v1.Protected.GET("/workspaces/:wid/agents/:aid/profile", routes.getProfile)
}

type aapAgentProfileDTO struct {
	Object                  string                        `json:"object"`
	ID                      string                        `json:"id"`
	Name                    string                        `json:"name"`
	Description             string                        `json:"description"`
	Version                 string                        `json:"version"`
	SupportedContent        []aapSupportedContentDTO      `json:"supportedContent"`
	Capabilities            []aapCapabilitySummaryDTO     `json:"capabilities"`
	InteractionRequirements aapInteractionRequirementsDTO `json:"interactionRequirements"`
}

type aapSupportedContentDTO struct {
	Type  string   `json:"type"`
	Parts []string `json:"parts"`
}

type aapCapabilitySummaryDTO struct {
	Kind                   string `json:"kind"`
	Count                  int    `json:"count"`
	MayRequireConfirmation bool   `json:"mayRequireConfirmation"`
}

type aapInteractionRequirementsDTO struct {
	Approval aapApprovalRequirementDTO `json:"approval"`
}

type aapApprovalRequirementDTO struct {
	Supported     bool     `json:"supported"`
	MayBeRequired bool     `json:"mayBeRequired"`
	Decisions     []string `json:"decisions"`
	RequiredScope string   `json:"requiredScope"`
}

type aapAgentProfileVersionSeed struct {
	Schema                  string                 `json:"schema"`
	AgentID                 string                 `json:"agentId"`
	Name                    string                 `json:"name"`
	Description             string                 `json:"description"`
	AgentLockVersion        int64                  `json:"agentLockVersion"`
	CurrentPromptRevisionID string                 `json:"currentPromptRevisionId"`
	Capabilities            []aapCapabilityVersion `json:"capabilities"`
}

type aapCapabilityVersion struct {
	CapabilityID         string `json:"capabilityId"`
	ReleaseID            string `json:"releaseId"`
	Kind                 string `json:"kind"`
	ConnectionID         string `json:"connectionId,omitempty"`
	RiskLevel            string `json:"riskLevel"`
	SideEffectLevel      string `json:"sideEffectLevel"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
}

func (routes *AAPAgentProfileRoutes) getProfile(c *gin.Context) {
	if _, _, ok := authorizeAAPRequest(c, routes.authorizer,
		agentaccessauth.ActionAgentProfileRead, agentaccessauth.AAPAuthorizationResource{}); !ok {
		return
	}
	value, err := routes.agents.GetSummary(c.Request.Context(), c.Param("wid"), c.Param("aid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	// A public profile exists only for an active Agent with an immutable current
	// prompt revision. The revision is hashed into the version but never exposed.
	if value.Status != agent.StatusActive || value.CurrentPromptRevisionID == nil ||
		strings.TrimSpace(*value.CurrentPromptRevisionID) == "" {
		RespondError(c, agentaccessauth.ErrAAPAuthorizationNotVisible)
		return
	}
	descriptors, err := routes.catalog.ListForAgent(c.Request.Context(), c.Param("wid"), c.Param("aid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	profile, etag, err := projectAAPAgentProfile(value, descriptors)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=60")
	if ifNoneMatch(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, profile)
}

func projectAAPAgentProfile(
	value agent.Summary,
	descriptors []capability.Descriptor,
) (aapAgentProfileDTO, string, error) {
	counts := map[string]*aapCapabilitySummaryDTO{
		"tool":     {Kind: "tool"},
		"workflow": {Kind: "workflow"},
	}
	versions := make([]aapCapabilityVersion, 0, len(descriptors))
	mayRequireApproval := false
	for _, descriptor := range descriptors {
		kind := strings.ToLower(strings.TrimSpace(descriptor.Kind))
		summary, ok := counts[kind]
		if !ok || descriptor.CapabilityID == "" || descriptor.ReleaseID == "" {
			return aapAgentProfileDTO{}, "", ErrAAPAgentProfileUnavailable
		}
		summary.Count++
		summary.MayRequireConfirmation = summary.MayRequireConfirmation || descriptor.RequiresConfirmation
		mayRequireApproval = mayRequireApproval || descriptor.RequiresConfirmation
		versions = append(versions, aapCapabilityVersion{
			CapabilityID: descriptor.CapabilityID, ReleaseID: descriptor.ReleaseID,
			Kind: kind, ConnectionID: descriptor.ConnectionID,
			RiskLevel: descriptor.RiskLevel, SideEffectLevel: descriptor.SideEffectLevel,
			RequiresConfirmation: descriptor.RequiresConfirmation,
		})
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].CapabilityID == versions[j].CapabilityID {
			return versions[i].ReleaseID < versions[j].ReleaseID
		}
		return versions[i].CapabilityID < versions[j].CapabilityID
	})
	capabilities := make([]aapCapabilitySummaryDTO, 0, len(counts))
	for _, kind := range []string{"tool", "workflow"} {
		if counts[kind].Count > 0 {
			capabilities = append(capabilities, *counts[kind])
		}
	}
	seed := aapAgentProfileVersionSeed{
		Schema: "aap.agent-profile.v1", AgentID: value.ID,
		Name: value.Name, Description: value.RoleDescription,
		AgentLockVersion:        value.LockVersion,
		CurrentPromptRevisionID: *value.CurrentPromptRevisionID,
		Capabilities:            versions,
	}
	canonical, err := json.Marshal(seed)
	if err != nil {
		return aapAgentProfileDTO{}, "", ErrAAPAgentProfileUnavailable
	}
	digest := sha256.Sum256(canonical)
	version := "sha256:" + hex.EncodeToString(digest[:])
	profile := aapAgentProfileDTO{
		Object: "agent_profile", ID: value.ID, Name: value.Name,
		Description: value.RoleDescription, Version: version,
		SupportedContent: []aapSupportedContentDTO{{Type: "message", Parts: []string{"text"}}},
		Capabilities:     capabilities,
		InteractionRequirements: aapInteractionRequirementsDTO{
			Approval: aapApprovalRequirementDTO{
				Supported: true, MayBeRequired: mayRequireApproval,
				Decisions: []string{"approve", "reject"}, RequiredScope: "interaction:decide",
			},
		},
	}
	return profile, `"` + version + `"`, nil
}

func ifNoneMatch(value, etag string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag ||
			strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
