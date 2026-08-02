// Package a2agateway implements external A2A inbound/outbound adapters using
// the official github.com/a2aproject/a2a-go SDK (stable v0.3.x).
// Protocol details stay out of einoruntime.
package a2agateway

import (
	"context"
	"encoding/json"
	"time"
)

// Exposure allowlists an internal agent for inbound A2A.
type Exposure struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspaceId"`
	AgentID           string          `json:"agentId"`
	PublicName        string          `json:"publicName"`
	PublicDescription string          `json:"publicDescription"`
	Enabled           bool            `json:"enabled"`
	CardOverrides     json.RawMessage `json:"cardOverrides"`
	AuthMode          string          `json:"authMode"`
	Version           int64           `json:"version"`
	CreatedBy         string          `json:"createdBy,omitempty"`
	UpdatedBy         string          `json:"updatedBy,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
	DeletedAt         *time.Time      `json:"deletedAt,omitempty"`
}

// RemoteBinding lets an internal agent call a remote A2A agent.
type RemoteBinding struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspaceId"`
	CallerAgentID string     `json:"callerAgentId"`
	CallableName  string     `json:"callableName"`
	Description   string     `json:"description"`
	EndpointURL   string     `json:"endpointUrl"`
	AgentCardURL  string     `json:"agentCardUrl,omitempty"`
	AllowedHosts  []string   `json:"allowedHosts"`
	AuthSecretRef string     `json:"authSecretRef,omitempty"` // secret reference only
	TimeoutMs     int        `json:"timeoutMs"`
	Enabled       bool       `json:"enabled"`
	Version       int64      `json:"version"`
	CreatedBy     string     `json:"createdBy,omitempty"`
	UpdatedBy     string     `json:"updatedBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	DeletedAt     *time.Time `json:"deletedAt,omitempty"`
}

// SafeExternalRef is a non-secret host/path reference for audit.
func SafeExternalRef(endpointURL string) string {
	// Host only; never credentials.
	return sanitizeEndpointRef(endpointURL)
}

// RemoteLister lists enabled outbound A2A bindings for a caller agent.
type RemoteLister interface {
	ListEnabledRemotesForCaller(ctx context.Context, workspaceID, callerAgentID string) ([]RemoteBinding, error)
}

const (
	AuthModeAgentAccess = "AGENT_ACCESS"
	AuthModeNone        = "NONE"
)
