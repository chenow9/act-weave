package storedobject

import "time"

const (
	KindOpenAPISource         = "OPENAPI_SOURCE"
	KindPromptRunInput        = "PROMPT_RUN_INPUT"
	KindPromptRunOutput       = "PROMPT_RUN_OUTPUT"
	KindPromptPreviewInput    = "PROMPT_PREVIEW_INPUT"
	KindPromptPreviewOutput   = "PROMPT_PREVIEW_OUTPUT"
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
	ID                  string
	WorkspaceID         string
	Bucket              string
	ObjectKey           string
	Kind                string
	ContentType         string
	SizeBytes           int64
	SHA256              string
	EncryptionKeyID     string
	Classification      string
	RetentionMode       string
	RetentionUntil      *time.Time
	CreatedByType       string
	CreatedByID         string
	CreatedAt           time.Time
	BodyPurgedAt        *time.Time
	PurgeClaimToken     *string
	PurgeClaimExpiresAt *time.Time
	PurgeAttempts       int
	PurgeNextAttemptAt  *time.Time
	PurgeLastErrorCode  *string
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

// IsPromptPreview reports whether kind is a create-preview body object.
func IsPromptPreview(kind string) bool {
	switch kind {
	case KindPromptPreviewInput, KindPromptPreviewOutput:
		return true
	default:
		return false
	}
}

// BodyUnavailable reports whether metadata forbids returning ciphertext body.
// Expired EXPIRING objects and body-purged tombs are unreadable regardless of
// whether the worker has finished deleting the blob.
func (object StoredObject) BodyUnavailable(now time.Time) bool {
	if object.BodyPurgedAt != nil {
		return true
	}
	if object.RetentionMode == RetentionExpiring && object.RetentionUntil != nil &&
		!object.RetentionUntil.After(now.UTC()) {
		return true
	}
	return false
}

type ListInput struct {
	WorkspaceID    string
	Kind           string
	Classification string
	RetentionMode  string
	Limit          int
}
