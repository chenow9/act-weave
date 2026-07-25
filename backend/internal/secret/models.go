package secret

import "time"

type ReadDTO struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspaceId"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	Configured      bool      `json:"configured"`
	Fingerprint     string    `json:"fingerprint,omitempty"`
	ActiveVersionNo *int64    `json:"activeVersionNo,omitempty"`
	CreatedBy       string    `json:"createdBy"`
	UpdatedBy       string    `json:"updatedBy"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	LockVersion     int64     `json:"lockVersion"`
}

type CreateInput struct {
	WorkspaceID string
	Name        string
	Kind        string
	Plaintext   string `json:"-"`
	ActorUserID string
}

type RotateInput struct {
	WorkspaceID         string
	SecretID            string
	Plaintext           string `json:"-"`
	ActorUserID         string
	ExpectedLockVersion int64
}

type RevokeInput struct {
	WorkspaceID         string
	SecretID            string
	ActorUserID         string
	ExpectedLockVersion int64
}

type protectedVersion struct {
	ID          string
	WorkspaceID string
	SecretID    string
	Encrypted   EncryptedValue
	Fingerprint string
	CreatedBy   string
}
