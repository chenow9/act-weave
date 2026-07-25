package agentaccess

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// WorkspaceExternalSubjectRetentionPolicy is the v1 retention contract for
// External Subject identity evidence and presentation fields. Identity is
// permanent for audit continuity; raw external subject values are never stored.
type WorkspaceExternalSubjectRetentionPolicy struct {
	// IdentityRetention is fixed to "permanent" in v1: mappings are never
	// hard-deleted so historical Runs remain auditable by External Subject ID.
	IdentityRetention string `json:"identityRetention"`
	// AllowHardDelete is always false in v1.
	AllowHardDelete bool `json:"allowHardDelete"`
	// AllowReenable permits DISABLED → ACTIVE transitions for the same identity.
	AllowReenable bool `json:"allowReenable"`
	// ClearDisplayOnDisable removes optional presentation when a Subject is
	// disabled so management listings do not keep operator-facing display text
	// after access is revoked. Identity evidence is retained.
	ClearDisplayOnDisable bool `json:"clearDisplayOnDisable"`
}

// DefaultWorkspaceExternalSubjectRetentionPolicy is the fixed v1 policy applied
// to every Workspace. Future Workspace-level overrides require an ADR.
func DefaultWorkspaceExternalSubjectRetentionPolicy() WorkspaceExternalSubjectRetentionPolicy {
	return WorkspaceExternalSubjectRetentionPolicy{
		IdentityRetention:     "permanent",
		AllowHardDelete:       false,
		AllowReenable:         true,
		ClearDisplayOnDisable: true,
	}
}

func (policy WorkspaceExternalSubjectRetentionPolicy) Valid() bool {
	return policy.IdentityRetention == "permanent" && !policy.AllowHardDelete && policy.AllowReenable
}

// ExternalSubjectPublicView is the only External Subject shape allowed in
// management APIs, audits, and ordinary logs. It never includes subject_hash or
// the original external subject value.
type ExternalSubjectPublicView struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	ClientID    string     `json:"clientId"`
	Issuer      string     `json:"issuer"`
	DisplayRef  string     `json:"displayRef,omitempty"`
	Status      Status     `json:"status"`
	FirstSeenAt time.Time  `json:"firstSeenAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	DisabledAt  *time.Time `json:"disabledAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	LockVersion int64      `json:"lockVersion"`
}

func ExternalSubjectToPublicView(value ExternalSubject) ExternalSubjectPublicView {
	return ExternalSubjectPublicView{
		ID: value.ID, WorkspaceID: value.WorkspaceID, ClientID: value.ClientID,
		Issuer: value.Issuer, DisplayRef: value.DisplayRef, Status: value.Status,
		FirstSeenAt: value.FirstSeenAt, LastSeenAt: value.LastSeenAt,
		DisabledAt: value.DisabledAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		LockVersion: value.LockVersion,
	}
}

func (view ExternalSubjectPublicView) AuditState() map[string]any {
	return map[string]any{
		"status": string(view.Status), "displayRef": view.DisplayRef,
		"issuer": view.Issuer, "lockVersion": view.LockVersion,
		"lastSeenAt": view.LastSeenAt, "disabledAt": view.DisabledAt,
	}
}

var externalSubjectDisplayRefPattern = regexp.MustCompile(`^ref_[A-Za-z0-9_-]{1,116}$`)

func ValidExternalSubjectDisplayRef(value string) bool {
	if value == "" {
		return true
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "@ \t\r\n") {
		return false
	}
	return externalSubjectDisplayRefPattern.MatchString(value)
}

// AssertExternalSubjectPrivacyJSON fails if a public payload contains known
// sensitive field names or the provided raw subject token / subject value.
func AssertExternalSubjectPrivacyJSON(payload []byte, forbiddenValues ...string) error {
	if len(payload) == 0 {
		return nil
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		`"subjecthash"`, `"subject_hash"`, `"subjecttoken"`, `"subject_token"`,
		`"rawtoken"`, `"raw_token"`, `"email"`, `"phone"`,
	} {
		if strings.Contains(lower, forbidden) {
			return ErrManagementInvalid
		}
	}
	// subjectHash as JSON key with camelCase from Go would be subjectHash.
	if strings.Contains(string(payload), "SubjectHash") || strings.Contains(string(payload), "subjectHash") {
		return ErrManagementInvalid
	}
	for _, value := range forbiddenValues {
		if value != "" && strings.Contains(string(payload), value) {
			return ErrManagementInvalid
		}
	}
	return nil
}

func marshalExternalSubjectPublic(value ExternalSubject) ([]byte, error) {
	return json.Marshal(ExternalSubjectToPublicView(value))
}
