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
	KindChatContextSummary    = "CHAT_CONTEXT_SUMMARY"
	// KindAAPFile is a permanent (post-promote) AAP uploaded file body.
	// Default classification is SENSITIVE with EXPIRING retention (v1).
	// Must NOT be forced into requiresPermanentSensitiveContent.
	// Permanent ciphertext is written only via SecureStore after promote;
	// clients never PUT directly into the permanent bucket.
	KindAAPFile = "AAP_FILE"
	// KindAAPFileDerived is a pipeline-derived artifact body (e.g. thumbnail).
	// Same retention/classification defaults as KindAAPFile; also not forced PERMANENT.
	KindAAPFileDerived = "AAP_FILE_DERIVED"
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

// AAPStagingObjectKey returns the short-lived plaintext staging object key for
// an AAP upload intent: {workspaceId}/aap-staging/{fileId}.
// Staging is plaintext for a short TTL and is never a stored_objects row until
// promote copies the body through SecureStore into the permanent bucket.
func AAPStagingObjectKey(workspaceID, fileID string) string {
	return workspaceID + "/aap-staging/" + fileID
}

// AAPPermanentObjectKey returns the permanent object key produced by preparePut
// for KindAAPFile: {workspaceId}/aap-file/{objectId}.
// Prefer preparePut / SecureStore.Put for real writes; this helper is for
// tests and key-shape assertions.
func AAPPermanentObjectKey(workspaceID, objectID string) string {
	return workspaceID + "/aap-file/" + objectID
}

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

// IsAAPFileKind reports whether kind is an AAP permanent file body (IC-11 purge).
func IsAAPFileKind(kind string) bool {
	switch kind {
	case KindAAPFile, KindAAPFileDerived:
		return true
	default:
		return false
	}
}

// IsExpiringBodyPurgeable reports kinds whose ciphertext may be purged after
// retention_until (prompt preview + AAP file EXPIRING paths).
func IsExpiringBodyPurgeable(kind string) bool {
	return IsPromptPreview(kind) || IsAAPFileKind(kind)
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
