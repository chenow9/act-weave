package storedobject

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

const encryptedObjectContentType = "application/vnd.actweave.encrypted+octet-stream"

type SecureStore struct {
	objects *ObjectStore
	active  StreamCipher
	ciphers map[string]StreamCipher
}

func NewSecureStore(
	objects *ObjectStore,
	active StreamCipher,
	decryptors ...StreamCipher,
) (*SecureStore, error) {
	if objects == nil || active == nil || strings.TrimSpace(active.KeyID()) == "" {
		return nil, errors.New("secure object store and active cipher are required")
	}
	ciphers := map[string]StreamCipher{active.KeyID(): active}
	for _, decryptor := range decryptors {
		if decryptor == nil || strings.TrimSpace(decryptor.KeyID()) == "" {
			return nil, ErrInvalid
		}
		if _, exists := ciphers[decryptor.KeyID()]; exists {
			return nil, ErrConflict
		}
		ciphers[decryptor.KeyID()] = decryptor
	}
	return &SecureStore{objects: objects, active: active, ciphers: ciphers}, nil
}

func (store *SecureStore) Put(ctx context.Context, input PutInput) (StoredObject, error) {
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	input.Classification = strings.ToUpper(strings.TrimSpace(input.Classification))
	input.RetentionMode = strings.ToUpper(strings.TrimSpace(input.RetentionMode))
	if err := validateRetentionPolicy(input); err != nil {
		return StoredObject{}, err
	}
	if input.Classification != ClassificationSensitive &&
		input.Classification != ClassificationRestricted {
		if strings.TrimSpace(input.EncryptionKeyID) != "" {
			return StoredObject{}, ErrInvalid
		}
		return store.objects.Put(ctx, input)
	}
	if strings.TrimSpace(input.EncryptionKeyID) != "" || !validHash(strings.ToLower(strings.TrimSpace(input.SHA256))) {
		return StoredObject{}, ErrInvalid
	}
	binding := CipherBinding{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ObjectID:    strings.TrimSpace(input.ID), Kind: input.Kind,
	}
	encrypted, err := store.active.Encrypt(
		ctx, binding, input.Reader, input.SizeBytes, input.SHA256,
	)
	if err != nil {
		return StoredObject{}, err
	}
	defer encrypted.Reader.Close()
	input.Reader = encrypted.Reader
	input.SizeBytes = encrypted.Size
	input.SHA256 = ""
	input.EncryptionKeyID = encrypted.KeyID
	input.StorageContentType = encryptedObjectContentType
	return store.objects.Put(ctx, input)
}

func (store *SecureStore) Open(
	ctx context.Context,
	request ReadRequest,
) (OpenedObject, error) {
	opened, err := store.objects.Open(ctx, request)
	if err != nil {
		return OpenedObject{}, err
	}
	if opened.Metadata.EncryptionKeyID == "" {
		return opened, nil
	}
	decryptor, exists := store.ciphers[opened.Metadata.EncryptionKeyID]
	if !exists {
		_ = opened.Body.Close()
		return OpenedObject{}, ErrDecrypt
	}
	decrypted, err := decryptor.Decrypt(ctx, CipherBinding{
		WorkspaceID: opened.Metadata.WorkspaceID,
		ObjectID:    opened.Metadata.ID, Kind: opened.Metadata.Kind,
	}, opened.Body)
	if err != nil {
		_ = opened.Body.Close()
		return OpenedObject{}, err
	}
	opened.Body = decrypted
	return opened, nil
}

// PresignDownload is intentionally limited to unencrypted objects. Sensitive
// ciphertext must be streamed through Open so authorization and decryption
// remain server-side; a raw MinIO URL would expose only protected bytes.
func (store *SecureStore) PresignDownload(
	ctx context.Context,
	request ReadRequest,
	ttl time.Duration,
) (*url.URL, error) {
	metadata, err := store.objects.authorizedMetadata(ctx, request)
	if err != nil {
		return nil, err
	}
	if metadata.EncryptionKeyID != "" {
		return nil, ErrInvalid
	}
	return store.objects.PresignDownload(ctx, request, ttl)
}

func validateRetentionPolicy(input PutInput) error {
	if !validKind(input.Kind) || !validClassification(input.Classification) ||
		!validRetentionMode(input.RetentionMode) {
		return ErrInvalid
	}
	if requiresPermanentSensitiveContent(input.Kind) {
		if input.RetentionMode != RetentionPermanent || input.RetentionUntil != nil ||
			(input.Classification != ClassificationSensitive &&
				input.Classification != ClassificationRestricted) {
			return ErrInvalid
		}
	}
	if input.Kind == KindOpenAPISource &&
		(input.RetentionMode != RetentionPermanent || input.RetentionUntil != nil ||
			input.Classification == ClassificationPublic) {
		return ErrInvalid
	}
	if input.Kind == KindAuditExport &&
		(input.RetentionMode != RetentionExpiring || input.RetentionUntil == nil) {
		return ErrInvalid
	}
	return nil
}

func requiresPermanentSensitiveContent(kind string) bool {
	switch kind {
	case KindPromptRunInput, KindPromptRunOutput, KindModelTurn, KindChatMessage,
		KindToolTestPayload, KindToolInvocationPayload, KindExecutionCheckpoint:
		return true
	default:
		return false
	}
}
