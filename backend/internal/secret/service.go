package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	repository *Repository
	encryptor  EnvelopeEncryptor
}

func NewService(repository *Repository, encryptor EnvelopeEncryptor) (*Service, error) {
	if repository == nil {
		return nil, errors.New("secret repository is required")
	}
	if encryptor == nil {
		return nil, errors.New("secret envelope encryptor is required")
	}
	return &Service{repository: repository, encryptor: encryptor}, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (ReadDTO, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	if !validUUID(input.WorkspaceID) || input.Name == "" || input.Kind == "" ||
		!validUUID(input.ActorUserID) || invalidPlaintext(input.Plaintext) {
		return ReadDTO{}, ErrInvalid
	}
	secretID, err := newUUIDv7()
	if err != nil {
		return ReadDTO{}, err
	}
	versionID, err := newUUIDv7()
	if err != nil {
		return ReadDTO{}, err
	}
	version, err := s.protect(ctx, input.WorkspaceID, secretID, versionID, input.Plaintext, input.ActorUserID)
	if err != nil {
		return ReadDTO{}, err
	}
	return s.repository.create(ctx, input, secretID, version)
}

func (s *Service) Rotate(ctx context.Context, input RotateInput) (ReadDTO, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SecretID = strings.TrimSpace(input.SecretID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	if !validUUID(input.WorkspaceID) || !validUUID(input.SecretID) ||
		!validUUID(input.ActorUserID) || input.ExpectedLockVersion < 1 ||
		invalidPlaintext(input.Plaintext) {
		return ReadDTO{}, ErrInvalid
	}
	versionID, err := newUUIDv7()
	if err != nil {
		return ReadDTO{}, err
	}
	version, err := s.protect(
		ctx,
		input.WorkspaceID,
		input.SecretID,
		versionID,
		input.Plaintext,
		input.ActorUserID,
	)
	if err != nil {
		return ReadDTO{}, err
	}
	return s.repository.rotate(ctx, input, version)
}

func (s *Service) Revoke(ctx context.Context, input RevokeInput) (ReadDTO, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.SecretID = strings.TrimSpace(input.SecretID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	if !validUUID(input.WorkspaceID) || !validUUID(input.SecretID) ||
		!validUUID(input.ActorUserID) || input.ExpectedLockVersion < 1 {
		return ReadDTO{}, ErrInvalid
	}
	return s.repository.Revoke(ctx, input)
}

func (s *Service) Get(ctx context.Context, workspaceID string, secretID string) (ReadDTO, error) {
	workspaceID, secretID = strings.TrimSpace(workspaceID), strings.TrimSpace(secretID)
	if !validUUID(workspaceID) || !validUUID(secretID) {
		return ReadDTO{}, ErrInvalid
	}
	return s.repository.Get(ctx, workspaceID, secretID)
}

// WithActiveSecret decrypts the current version only for the duration of the
// callback. The plaintext buffer is wiped before this method returns and is
// never exposed through a DTO.
func (s *Service) WithActiveSecret(
	ctx context.Context,
	workspaceID string,
	secretID string,
	use func([]byte) error,
) error {
	workspaceID, secretID = strings.TrimSpace(workspaceID), strings.TrimSpace(secretID)
	if !validUUID(workspaceID) || !validUUID(secretID) || use == nil {
		return ErrInvalid
	}
	version, err := s.repository.activeVersion(ctx, workspaceID, secretID)
	if err != nil {
		return err
	}
	plaintext, err := s.encryptor.Decrypt(ctx, version.Encrypted, associatedData(
		version.WorkspaceID, version.SecretID, version.ID,
	))
	if err != nil {
		return fmt.Errorf("decrypt active secret: %w", err)
	}
	defer wipe(plaintext)
	return use(plaintext)
}

func (s *Service) protect(
	ctx context.Context,
	workspaceID string,
	secretID string,
	versionID string,
	plaintext string,
	actorUserID string,
) (protectedVersion, error) {
	value := []byte(plaintext)
	defer wipe(value)
	protected, err := s.encryptor.Encrypt(ctx, value, associatedData(workspaceID, secretID, versionID))
	if err != nil {
		return protectedVersion{}, fmt.Errorf("encrypt secret value: %w", err)
	}
	return protectedVersion{
		ID:          versionID,
		WorkspaceID: workspaceID,
		SecretID:    secretID,
		Encrypted:   protected,
		Fingerprint: s.encryptor.Fingerprint(value),
		CreatedBy:   actorUserID,
	}, nil
}

func associatedData(workspaceID string, secretID string, versionID string) []byte {
	return []byte("actweave.secret.v1\x00" + workspaceID + "\x00" + secretID + "\x00" + versionID)
}

func invalidPlaintext(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	switch trimmed {
	case "****", "********", "••••", "••••••••":
		return true
	default:
		return false
	}
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func newUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}
	return id.String(), nil
}
