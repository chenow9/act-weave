package storedobject

import "time"

const (
	KindOpenAPISource         = "OPENAPI_SOURCE"
	KindPromptRunInput        = "PROMPT_RUN_INPUT"
	KindPromptRunOutput       = "PROMPT_RUN_OUTPUT"
	KindModelTurn             = "MODEL_TURN"
	KindChatMessage           = "CHAT_MESSAGE"
	KindToolTestPayload       = "TOOL_TEST_PAYLOAD"
	KindToolInvocationPayload = "TOOL_INVOCATION_PAYLOAD"
	KindExecutionCheckpoint   = "EXECUTION_CHECKPOINT"
	KindAuditEventPayload     = "AUDIT_EVENT_PAYLOAD"
	KindAuditExport           = "AUDIT_EXPORT"
	ClassificationPublic      = "PUBLIC"
	ClassificationInternal    = "INTERNAL"
	ClassificationSensitive   = "SENSITIVE"
	ClassificationRestricted  = "RESTRICTED"
	RetentionPermanent        = "PERMANENT"
	RetentionExpiring         = "EXPIRING"
	CreatorUser               = "USER"
	CreatorServicePrincipal   = "SERVICE_PRINCIPAL"
	CreatorSystem             = "SYSTEM"
)

type StoredObject struct {
	ID              string
	WorkspaceID     string
	Bucket          string
	ObjectKey       string
	Kind            string
	ContentType     string
	SizeBytes       int64
	SHA256          string
	EncryptionKeyID string
	Classification  string
	RetentionMode   string
	RetentionUntil  *time.Time
	CreatedByType   string
	CreatedByID     string
	CreatedAt       time.Time
}

type CreateInput struct {
	ID              string
	WorkspaceID     string
	Bucket          string
	ObjectKey       string
	Kind            string
	ContentType     string
	SizeBytes       int64
	SHA256          string
	EncryptionKeyID string
	Classification  string
	RetentionMode   string
	RetentionUntil  *time.Time
	CreatedByType   string
	CreatedByID     string
}

type ListInput struct {
	WorkspaceID    string
	Kind           string
	Classification string
	RetentionMode  string
	Limit          int
}
