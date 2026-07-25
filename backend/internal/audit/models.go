package audit

import (
	"encoding/json"
	"net/netip"
	"time"
)

const SchemaVersionV1 = "audit.v1"

type Event struct {
	ID              string
	OccurredAt      time.Time
	WorkspaceID     string
	ActorType       string
	ActorID         string
	ActorDisplay    string
	Action          string
	ResourceType    string
	ResourceID      string
	Result          string
	RequestID       string
	TraceID         string
	SourceIP        netip.Addr
	UserAgent       string
	Changes         json.RawMessage
	Metadata        json.RawMessage
	PayloadObjectID string
	SchemaVersion   string
}

type BuildInput struct {
	ID              string
	OccurredAt      time.Time
	WorkspaceID     string
	ActorType       string
	ActorID         string
	ActorDisplay    string
	Action          string
	ResourceType    string
	ResourceID      string
	Result          string
	RequestID       string
	TraceID         string
	SourceIP        string
	UserAgent       string
	Headers         map[string][]string
	Before          map[string]any
	After           map[string]any
	Metadata        map[string]any
	PayloadObjectID string
}
