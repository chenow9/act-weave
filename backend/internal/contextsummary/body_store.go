package contextsummary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"actweave/backend/internal/storedobject"
)

// MaxSummaryBodyBytes is the permanent plaintext body ceiling (64 KiB).
const MaxSummaryBodyBytes = 64 << 10

// secureSummaryObjects is the permanent encrypted object surface used by SummaryBodyStore.
// *storedobject.SecureStore satisfies this interface.
type secureSummaryObjects interface {
	Put(context.Context, storedobject.PutInput) (storedobject.StoredObject, error)
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

// SummaryBodyStore persists CHAT_CONTEXT_SUMMARY bodies as permanent, encrypted
// execution-bucket objects with idempotent PutOrVerify semantics.
type SummaryBodyStore struct {
	secure secureSummaryObjects
}

// NewSummaryBodyStore wraps a SecureStore (or test double).
func NewSummaryBodyStore(secure *storedobject.SecureStore) (*SummaryBodyStore, error) {
	if secure == nil {
		return nil, errors.New("summary body secure store is required")
	}
	return &SummaryBodyStore{secure: secure}, nil
}

// newSummaryBodyStoreForTest wires any secureSummaryObjects implementation.
func newSummaryBodyStoreForTest(secure secureSummaryObjects) (*SummaryBodyStore, error) {
	if secure == nil {
		return nil, errors.New("summary body secure store is required")
	}
	return &SummaryBodyStore{secure: secure}, nil
}

// PutInput is a permanent sensitive summary body write.
type PutInput struct {
	// ObjectID is deterministic and must equal the summary metadata ID.
	ObjectID      string
	WorkspaceID   string
	Body          []byte
	CreatedByType string
	CreatedByID   string
	// Actor fields authorize Open on retry verification path.
	ActorType string
	ActorID   string
}

// PutResult is body-free metadata suitable for MarkReady.
type PutResult struct {
	ObjectID    string
	WorkspaceID string
	Kind        string
	SHA256      string
	Length      int64
	Reused      bool
}

// PutOrVerify encrypts and stores body under ObjectID.
// On same-ID retry: decrypt existing and compare plaintext digest/length/kind/scope.
// Identical content is success (Reused=true); mismatch is integrity conflict.
func (s *SummaryBodyStore) PutOrVerify(ctx context.Context, input PutInput) (PutResult, error) {
	if s == nil || s.secure == nil {
		return PutResult{}, ErrInvalid
	}
	input.ObjectID = strings.TrimSpace(input.ObjectID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.CreatedByType = strings.ToUpper(strings.TrimSpace(input.CreatedByType))
	input.CreatedByID = strings.TrimSpace(input.CreatedByID)
	input.ActorType = strings.ToUpper(strings.TrimSpace(input.ActorType))
	input.ActorID = strings.TrimSpace(input.ActorID)
	if input.ActorType == "" {
		input.ActorType = input.CreatedByType
	}
	if input.ActorID == "" {
		input.ActorID = input.CreatedByID
	}
	if !validUUID(input.ObjectID) || !validUUID(input.WorkspaceID) ||
		!validUUID(input.CreatedByID) || !validUUID(input.ActorID) {
		return PutResult{}, ErrInvalid
	}
	if input.CreatedByType == "" || input.ActorType == "" {
		return PutResult{}, ErrInvalid
	}
	if err := validateSummaryPlaintext(input.Body); err != nil {
		return PutResult{}, err
	}
	wantSHA := sha256Hex(input.Body)
	wantLen := int64(len(input.Body))

	created, err := s.secure.Put(ctx, storedobject.PutInput{
		ID:             input.ObjectID,
		WorkspaceID:    input.WorkspaceID,
		Kind:           storedobject.KindChatContextSummary,
		ContentType:    "text/plain; charset=utf-8",
		SizeBytes:      wantLen,
		SHA256:         wantSHA,
		Classification: storedobject.ClassificationSensitive,
		RetentionMode:  storedobject.RetentionPermanent,
		CreatedByType:  input.CreatedByType,
		CreatedByID:    input.CreatedByID,
		Reader:         bytes.NewReader(input.Body),
	})
	if err == nil {
		// SecureStore rewrites SHA256 to ciphertext digest; MarkReady needs plaintext.
		return PutResult{
			ObjectID:    created.ID,
			WorkspaceID: created.WorkspaceID,
			Kind:        created.Kind,
			SHA256:      wantSHA,
			Length:      wantLen,
			Reused:      false,
		}, nil
	}
	if !errors.Is(err, storedobject.ErrConflict) {
		return PutResult{}, err
	}

	// Same deterministic ID already exists: open, decrypt, verify plaintext.
	opened, openErr := s.secure.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: input.WorkspaceID,
		ObjectID:    input.ObjectID,
		ActorType:   input.ActorType,
		ActorID:     input.ActorID,
	})
	if openErr != nil {
		return PutResult{}, openErr
	}
	defer opened.Body.Close()

	if opened.Metadata.Kind != storedobject.KindChatContextSummary ||
		opened.Metadata.WorkspaceID != input.WorkspaceID ||
		opened.Metadata.ID != input.ObjectID ||
		opened.Metadata.RetentionMode != storedobject.RetentionPermanent ||
		(opened.Metadata.Classification != storedobject.ClassificationSensitive &&
			opened.Metadata.Classification != storedobject.ClassificationRestricted) ||
		opened.Metadata.EncryptionKeyID == "" {
		return PutResult{}, storedobject.ErrIntegrity
	}

	existing, readErr := io.ReadAll(io.LimitReader(opened.Body, MaxSummaryBodyBytes+1))
	if readErr != nil {
		return PutResult{}, readErr
	}
	if int64(len(existing)) != wantLen || sha256Hex(existing) != wantSHA {
		return PutResult{}, storedobject.ErrIntegrity
	}
	return PutResult{
		ObjectID:    opened.Metadata.ID,
		WorkspaceID: opened.Metadata.WorkspaceID,
		Kind:        opened.Metadata.Kind,
		SHA256:      wantSHA,
		Length:      wantLen,
		Reused:      true,
	}, nil
}

// OpenPlaintext decrypts a summary body after authorization.
func (s *SummaryBodyStore) OpenPlaintext(ctx context.Context, workspaceID, objectID, actorType, actorID string) ([]byte, error) {
	if s == nil || s.secure == nil {
		return nil, ErrInvalid
	}
	opened, err := s.secure.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ObjectID:    strings.TrimSpace(objectID),
		ActorType:   strings.ToUpper(strings.TrimSpace(actorType)),
		ActorID:     strings.TrimSpace(actorID),
	})
	if err != nil {
		return nil, err
	}
	defer opened.Body.Close()
	if opened.Metadata.Kind != storedobject.KindChatContextSummary {
		return nil, storedobject.ErrIntegrity
	}
	body, err := io.ReadAll(io.LimitReader(opened.Body, MaxSummaryBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxSummaryBodyBytes {
		return nil, ErrInvalid
	}
	return body, nil
}

func validateSummaryPlaintext(body []byte) error {
	if len(body) == 0 || len(body) > MaxSummaryBodyBytes {
		return ErrInvalid
	}
	if !utf8.Valid(body) {
		return ErrInvalid
	}
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
