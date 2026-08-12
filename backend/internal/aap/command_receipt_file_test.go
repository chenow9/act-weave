package aap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFileCommandReceiptOpsAndResource(t *testing.T) {
	for _, op := range []string{CommandFileCreate, CommandFileComplete} {
		key := CommandReceiptKey{
			WorkspaceID:        "a68f1f2e-7b5a-7c3d-8e9f-123456789001",
			AgentID:            "a68f1f2e-7b5a-7c3d-8e9f-123456789002",
			ClientID:           "a68f1f2e-7b5a-7c3d-8e9f-123456789003",
			ServicePrincipalID: "a68f1f2e-7b5a-7c3d-8e9f-123456789004",
			SubjectID:          "a68f1f2e-7b5a-7c3d-8e9f-123456789005",
			Operation:          op,
			IdempotencyKey:     "a68f1f2e-7b5a-7c3d-8e9f-123456789006",
		}
		if !validCommandReceiptKey(key) {
			t.Fatalf("operation %q must be a valid command receipt key", op)
		}
	}
	if !validCommandResource("FILE", "a68f1f2e-7b5a-7c3d-8e9f-123456789007") {
		t.Fatal("FILE resource type must be valid")
	}
	if validCommandResource("FILE_BLOB", "a68f1f2e-7b5a-7c3d-8e9f-123456789007") {
		t.Fatal("unknown resource type must be invalid")
	}
}

func TestFileCommandRequestHashExcludesUploadURL(t *testing.T) {
	createHash, err := FileCreateCommandRequestHash(FileCreateRequestHashInput{
		MediaType: "image/png",
		SizeBytes: 42,
		SHA256:    "ABCDEF",
		Purpose:   "GENERAL",
		Filename:  "a.png",
	})
	if err != nil || len(createHash) != 32 {
		t.Fatalf("create hash err=%v len=%d", err, len(createHash))
	}
	// Ensure marshaled create material has only allowlisted fields.
	raw, err := json.Marshal(FileCreateRequestHashInput{
		MediaType: "image/png", SizeBytes: 42, SHA256: "abcdef",
		Purpose: "GENERAL", Filename: "a.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{"upload", "url", "presign", "headers", "Authorization"} {
		if strings.Contains(strings.ToLower(serialized), strings.ToLower(forbidden)) {
			t.Fatalf("create hash input leaked %q: %s", forbidden, serialized)
		}
	}

	completeHash, err := FileCompleteCommandRequestHash(FileCompleteRequestHashInput{
		// non-canonical UUID fixture: casing must be canonicalized by the hash.
		FileID: "A68F1F2E-7B5A-7C3D-8E9F-1234567890F1",
		SHA256: "DEADBEEF",
	})
	if err != nil || len(completeHash) != 32 {
		t.Fatalf("complete hash err=%v len=%d", err, len(completeHash))
	}
	// Same fileId with different casing must canonicalize.
	again, err := FileCompleteCommandRequestHash(FileCompleteRequestHashInput{
		FileID: "a68f1f2e-7b5a-7c3d-8e9f-1234567890f1",
		SHA256: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(completeHash) != string(again) {
		t.Fatal("file complete hash must canonicalize fileId/sha256")
	}
}
