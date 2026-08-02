package storedobject

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const permanentToolPayloadID = "f18f1f2e-7b5a-7c3d-8e9f-123456789001"

func TestPermanentToolPayloadScrubsSecretsAndEncrypts(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	cipher, _ := NewLocalChunkCipher("tool-payload-key-v1", objectEncryptionTestKey)
	secure, _ := NewSecureStore(objects, cipher)
	writer, err := NewSensitivePayloadWriter(secure, NewJSONSecretScrubber("literal-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := writer.Write(context.Background(), SensitivePayloadInput{
		ObjectID: permanentToolPayloadID, WorkspaceID: minioStoreWorkspaceID,
		Kind:          KindToolInvocationPayload,
		Request:       json.RawMessage(`{"orderId":"A-10293","password":"literal-secret-value","nested":{"value":"literal-secret-value"}}`),
		Response:      json.RawMessage(`{"status":"ok","access_token":"upstream-token"}`),
		CreatedByType: CreatorUser, CreatedByID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectID != permanentToolPayloadID || result.Length < 1 || len(result.SHA256) != 64 {
		t.Fatalf("payload evidence mismatch: %+v", result)
	}
	metadata, err := repository.Get(context.Background(), minioStoreWorkspaceID, permanentToolPayloadID)
	if err != nil || metadata.Kind != KindToolInvocationPayload ||
		metadata.RetentionMode != RetentionPermanent || metadata.EncryptionKeyID != cipher.KeyID() {
		t.Fatalf("payload metadata mismatch: %+v err=%v", metadata, err)
	}
	physical := backend.objectContent(metadata.Bucket, metadata.ObjectKey)
	if bytes.Contains(physical, []byte("A-10293")) || bytes.Contains(physical, []byte("literal-secret-value")) {
		t.Fatal("tool payload backend exposed plaintext")
	}
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: permanentToolPayloadID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil || !bytes.Contains(plaintext, []byte("A-10293")) ||
		strings.Contains(string(plaintext), "literal-secret-value") ||
		strings.Contains(string(plaintext), "upstream-token") ||
		strings.Count(string(plaintext), "[REDACTED]") != 3 {
		t.Fatalf("scrubbed payload mismatch: %s err=%v", plaintext, err)
	}
	retried, err := writer.Write(context.Background(), SensitivePayloadInput{
		ObjectID: permanentToolPayloadID, WorkspaceID: minioStoreWorkspaceID,
		Kind:          KindToolInvocationPayload,
		Request:       json.RawMessage(`{"orderId":"A-10293","password":"literal-secret-value","nested":{"value":"literal-secret-value"}}`),
		Response:      json.RawMessage(`{"status":"ok","access_token":"upstream-token"}`),
		CreatedByType: CreatorUser, CreatedByID: minioStoreOwnerID,
	})
	if err != nil || retried != result {
		t.Fatalf("idempotent payload retry: %+v err=%v", retried, err)
	}
}

// IC-09 / KD-22: KindToolInvocationPayload must never retain wire-only download URLs.
func TestPermanentToolPayloadScrubsFileDownloadURLs(t *testing.T) {
	repository, _ := newMinIOStoreRepository(t)
	backend := newFakeBlobBackend()
	objects, _ := newObjectStore(backend, repository, &recordingReadAuthorizer{}, 1<<20)
	cipher, _ := NewLocalChunkCipher("tool-payload-key-v1", objectEncryptionTestKey)
	secure, _ := NewSecureStore(objects, cipher)
	writer, err := NewSensitivePayloadWriter(secure, NewJSONSecretScrubber())
	if err != nil {
		t.Fatal(err)
	}
	const objectID = "f18f1f2e-7b5a-7c3d-8e9f-1234567890d1"
	request := json.RawMessage(`{
		"document":{
			"fileId":"11111111-1111-7111-8111-111111111111",
			"downloadUrl":"https://aap.example.test/api/agent-access/v1/files/downloads/tok-abc"
		},
		"note":"mirror https://aap.example.test/api/agent-access/v1/files/downloads/tok-xyz"
	}`)
	result, err := writer.Write(context.Background(), SensitivePayloadInput{
		ObjectID: objectID, WorkspaceID: minioStoreWorkspaceID,
		Kind: KindToolInvocationPayload, Request: request,
		Response:      json.RawMessage(`{"ok":true}`),
		CreatedByType: CreatorUser, CreatedByID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := secure.Open(context.Background(), ReadRequest{
		WorkspaceID: minioStoreWorkspaceID, ObjectID: result.ObjectID,
		ActorType: CreatorUser, ActorID: minioStoreOwnerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(plaintext))
	for _, forbidden := range []string{
		"downloadurl", "files/downloads", "tok-abc", "tok-xyz", "https://aap.example.test",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("permanent payload retained %q: %s", forbidden, plaintext)
		}
	}
	if !bytes.Contains(plaintext, []byte("11111111-1111-7111-8111-111111111111")) {
		t.Fatalf("fileId should remain after scrub: %s", plaintext)
	}
}
