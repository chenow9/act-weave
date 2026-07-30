package contextsummary

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"actweave/backend/internal/storedobject"
)

const (
	bodyOwnerID     = "e18f1f2e-7b5a-7c3d-8e9f-123456789001"
	bodyWorkspaceID = "e18f1f2e-7b5a-7c3d-8e9f-123456789002"
	bodyObjectID    = "e18f1f2e-7b5a-7c3d-8e9f-1234567890f1"
)

type memorySecureSummary struct {
	objects map[string]storedobject.StoredObject
	bodies  map[string][]byte
}

func newMemorySecureSummary() *memorySecureSummary {
	return &memorySecureSummary{
		objects: make(map[string]storedobject.StoredObject),
		bodies:  make(map[string][]byte),
	}
}

func (m *memorySecureSummary) key(workspaceID, id string) string {
	return workspaceID + "/" + id
}

func (m *memorySecureSummary) Put(_ context.Context, input storedobject.PutInput) (storedobject.StoredObject, error) {
	key := m.key(input.WorkspaceID, input.ID)
	if _, exists := m.objects[key]; exists {
		return storedobject.StoredObject{}, storedobject.ErrConflict
	}
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return storedobject.StoredObject{}, err
	}
	meta := storedobject.StoredObject{
		ID: input.ID, WorkspaceID: input.WorkspaceID, Kind: input.Kind,
		ContentType: input.ContentType, SizeBytes: int64(len(body)),
		SHA256: "cipher-" + sha256Hex(body), EncryptionKeyID: "test-key",
		Classification: input.Classification, RetentionMode: input.RetentionMode,
		CreatedByType: input.CreatedByType, CreatedByID: input.CreatedByID,
		Bucket: "actweave-executions",
	}
	m.objects[key] = meta
	m.bodies[key] = append([]byte(nil), body...)
	return meta, nil
}

func (m *memorySecureSummary) Open(_ context.Context, req storedobject.ReadRequest) (storedobject.OpenedObject, error) {
	key := m.key(req.WorkspaceID, req.ObjectID)
	meta, ok := m.objects[key]
	if !ok {
		return storedobject.OpenedObject{}, storedobject.ErrNotFound
	}
	body := append([]byte(nil), m.bodies[key]...)
	return storedobject.OpenedObject{
		Metadata: meta,
		Body:     io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func TestNewSummaryBodyStoreNil(t *testing.T) {
	if _, err := NewSummaryBodyStore(nil); err == nil {
		t.Fatal("expected nil secure store error")
	}
}

func TestSummaryBodyStorePutOrVerifyIdempotentAndConflict(t *testing.T) {
	mem := newMemorySecureSummary()
	store, err := newSummaryBodyStoreForTest(mem)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("stable facts:\n- order A-1 is ready\n")
	input := PutInput{
		ObjectID: bodyObjectID, WorkspaceID: bodyWorkspaceID, Body: body,
		CreatedByType: storedobject.CreatorUser, CreatedByID: bodyOwnerID,
	}
	first, err := store.PutOrVerify(context.Background(), input)
	if err != nil || first.Reused || first.SHA256 != sha256Hex(body) || first.Length != int64(len(body)) {
		t.Fatalf("first: %+v err=%v", first, err)
	}
	if first.Kind != storedobject.KindChatContextSummary {
		t.Fatalf("kind=%s", first.Kind)
	}

	second, err := store.PutOrVerify(context.Background(), input)
	if err != nil || !second.Reused || second.SHA256 != first.SHA256 {
		t.Fatalf("retry: %+v err=%v", second, err)
	}

	conflict := input
	conflict.Body = []byte("completely different summary body")
	if _, err := store.PutOrVerify(context.Background(), conflict); !errors.Is(err, storedobject.ErrIntegrity) {
		t.Fatalf("conflict err=%v", err)
	}
	got, err := store.OpenPlaintext(context.Background(), bodyWorkspaceID, bodyObjectID,
		storedobject.CreatorUser, bodyOwnerID)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("original after conflict: %q err=%v", got, err)
	}
}

func TestSummaryBodyStoreRejectsInvalidBodies(t *testing.T) {
	mem := newMemorySecureSummary()
	store, _ := newSummaryBodyStoreForTest(mem)
	if _, err := store.PutOrVerify(context.Background(), PutInput{
		ObjectID: bodyObjectID, WorkspaceID: bodyWorkspaceID, Body: nil,
		CreatedByType: storedobject.CreatorUser, CreatedByID: bodyOwnerID,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	big := bytes.Repeat([]byte("x"), MaxSummaryBodyBytes+1)
	if _, err := store.PutOrVerify(context.Background(), PutInput{
		ObjectID: bodyObjectID, WorkspaceID: bodyWorkspaceID, Body: big,
		CreatedByType: storedobject.CreatorUser, CreatedByID: bodyOwnerID,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := store.PutOrVerify(context.Background(), PutInput{
		ObjectID: bodyObjectID, WorkspaceID: bodyWorkspaceID, Body: []byte{0xff, 0xfe},
		CreatedByType: storedobject.CreatorUser, CreatedByID: bodyOwnerID,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("utf8: %v", err)
	}
}

func TestSummaryBodyMaxBytesConstant(t *testing.T) {
	if MaxSummaryBodyBytes != 64<<10 {
		t.Fatalf("MaxSummaryBodyBytes=%d", MaxSummaryBodyBytes)
	}
}
