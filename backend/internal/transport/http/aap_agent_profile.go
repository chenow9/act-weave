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

	"actweave/backend/internal/aapfile"
	"actweave/backend/internal/agent"
	"actweave/backend/internal/agentaccessauth"
	"actweave/backend/internal/capability"
	"actweave/backend/internal/config"
	"actweave/backend/internal/sessioncontext"

	"github.com/gin-gonic/gin"
)

var ErrAAPAgentProfileUnavailable = errors.New("AAP Agent Profile is unavailable")

// MVP A2UI profile advertisement constants (KD-15 / Q10 / Q11).
// Centralized a2ui package lands in PR-4; keep literals here until then.
const (
	aapA2UIMaxSurfaceBytes = 64 << 10 // 65536
	aapA2UIDelivery        = "item_completed"
	aapA2UISpecHint        = "a2ui-surface.v0"
)

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
	// filesGate optionally extends supportedContent with input_file (KD-14).
	filesGate *config.AgentAccessFilesConfig
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

// ConfigureFiles enables additive profile parts for input_file when files are enabled
// for the workspace (KD-14). When nil/off, profile remains parts:["text"] only.
func (routes *AAPAgentProfileRoutes) ConfigureFiles(gate *config.AgentAccessFilesConfig) {
	if routes != nil {
		routes.filesGate = gate
	}
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
	// A2UI is present only when agent context_policy.aap.enableA2UI is true (omit when disabled).
	A2UI *aapA2UIDTO `json:"a2ui,omitempty"`
}

// aapA2UIDTO advertises assistant outbound A2UI capability (not inbound createRun).
type aapA2UIDTO struct {
	Enabled         bool   `json:"enabled"`
	Delivery        string `json:"delivery"`
	Streaming       bool   `json:"streaming"`
	Actions         bool   `json:"actions"`
	MaxSurfaceBytes int64  `json:"maxSurfaceBytes"`
	SpecHint        string `json:"specHint"`
}

type aapSupportedContentDTO struct {
	Type       string   `json:"type"`
	Parts      []string `json:"parts,omitempty"`
	MediaTypes []string `json:"mediaTypes,omitempty"`
	MaxBytes   int64    `json:"maxBytes,omitempty"`
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
	Schema                  string                   `json:"schema"`
	AgentID                 string                   `json:"agentId"`
	Name                    string                   `json:"name"`
	Description             string                   `json:"description"`
	AgentLockVersion        int64                    `json:"agentLockVersion"`
	CurrentPromptRevisionID string                   `json:"currentPromptRevisionId"`
	Capabilities            []aapCapabilityVersion   `json:"capabilities"`
	SupportedContent        []aapSupportedContentDTO `json:"supportedContent,omitempty"`
	// A2UI stable metadata subset must flip ETag when capability knobs change (KD-15).
	A2UI *aapA2UIDTO `json:"a2ui,omitempty"`
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
	filesEnabled := routes.filesGate != nil && routes.filesGate.AllowsWorkspace(c.Param("wid"))
	profile, etag, err := projectAAPAgentProfile(value, descriptors, filesEnabled, routes.filesGate)
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
	filesEnabled bool,
	filesGate *config.AgentAccessFilesConfig,
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
	// Read enableA2UI from agent ContextPolicy (no new store). Parse errors fail closed.
	enableA2UI := aapEnableA2UIFromPolicy(value.ContextPolicy)
	supportedContent := aapSupportedContentForAgent(filesEnabled, filesGate, enableA2UI)
	// When disabled: omit top-level a2ui (prefer omit over enabled:false; KD-15).
	var a2ui *aapA2UIDTO
	if enableA2UI {
		a2ui = aapA2UIAdvertisement()
	}
	seed := aapAgentProfileVersionSeed{
		Schema: "aap.agent-profile.v1", AgentID: value.ID,
		Name: value.Name, Description: value.RoleDescription,
		AgentLockVersion:        value.LockVersion,
		CurrentPromptRevisionID: *value.CurrentPromptRevisionID,
		Capabilities:            versions,
		// Content support + a2ui metadata changes must flip ETag (KD-14 / KD-15).
		SupportedContent: supportedContent,
		A2UI:             a2ui,
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
		SupportedContent: supportedContent,
		Capabilities:     capabilities,
		A2UI:             a2ui,
		InteractionRequirements: aapInteractionRequirementsDTO{
			Approval: aapApprovalRequirementDTO{
				Supported: true, MayBeRequired: mayRequireApproval,
				Decisions: []string{"approve", "reject"}, RequiredScope: "interaction:decide",
			},
		},
	}
	return profile, `"` + version + `"`, nil
}

// aapEnableA2UIFromPolicy reads context_policy.aap.enableA2UI (default false; fail-closed).
func aapEnableA2UIFromPolicy(raw json.RawMessage) bool {
	doc, _, err := sessioncontext.ParsePolicy(raw)
	if err != nil {
		return false
	}
	return doc.EnableA2UI()
}

// aapA2UIAdvertisement is the MVP top-level a2ui object when enableA2UI is true.
func aapA2UIAdvertisement() *aapA2UIDTO {
	return &aapA2UIDTO{
		Enabled:         true,
		Delivery:        aapA2UIDelivery,
		Streaming:       false,
		Actions:         false,
		MaxSurfaceBytes: aapA2UIMaxSurfaceBytes,
		SpecHint:        aapA2UISpecHint,
	}
}

// aapSupportedContentForAgent composes message parts: text → input_file? → a2ui? (KD-15).
func aapSupportedContentForAgent(
	filesEnabled bool,
	filesGate *config.AgentAccessFilesConfig,
	enableA2UI bool,
) []aapSupportedContentDTO {
	content := aapSupportedContentForFiles(filesEnabled, filesGate)
	if !enableA2UI {
		return content
	}
	for i := range content {
		if content[i].Type != "message" {
			continue
		}
		already := false
		for _, part := range content[i].Parts {
			if part == "a2ui" {
				already = true
				break
			}
		}
		if !already {
			content[i].Parts = append(append([]string(nil), content[i].Parts...), "a2ui")
		}
		break
	}
	return content
}

func aapSupportedContentForFiles(
	filesEnabled bool,
	filesGate *config.AgentAccessFilesConfig,
) []aapSupportedContentDTO {
	// When files gate is closed for the workspace, advertise text only (KD-14).
	if !filesEnabled {
		return []aapSupportedContentDTO{{Type: "message", Parts: []string{"text"}}}
	}
	maxBytes := aapfile.DefaultMaxBytes
	mediaTypes := []string{
		"image/png", "image/jpeg", "image/webp", "image/gif", "application/pdf",
	}
	if filesGate != nil {
		if filesGate.MaxBytes > 0 {
			maxBytes = filesGate.MaxBytes
		}
		if len(filesGate.AllowedMediaTypes) > 0 {
			mediaTypes = append([]string(nil), filesGate.AllowedMediaTypes...)
		}
	}
	return []aapSupportedContentDTO{
		{Type: "message", Parts: []string{"text", "input_file"}},
		{
			Type: "input_file_constraints", MediaTypes: mediaTypes, MaxBytes: maxBytes,
		},
	}
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
