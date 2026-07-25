package protocolevent_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"

	"github.com/google/uuid"
)

// TestAAPSensitiveDataAcceptance is the M10-T2 gate for Protocol Event / Item
// Snapshot surfaces: golden fixtures, runtime append path, allowlist, and
// irreversible controlled IDs/hashes.
func TestAAPSensitiveDataAcceptance(t *testing.T) {
	t.Run("GoldenTracesAndSnapshotsHaveZeroForbiddenHits", testSensitiveGoldenZeroHits)
	t.Run("ScannerRejectsSecretsWithoutEcho", testSensitiveScannerNoEcho)
	t.Run("FieldAllowlistPermitsUsageTokenMetricsOnly", testSensitiveFieldAllowlist)
	t.Run("ControlledHashAndUUIDAreIrreversiblePublicIDs", testSensitiveControlledIDs)
	t.Run("AppenderRejectsSensitiveBeforePersist", testSensitiveAppenderReject)
}

func testSensitiveGoldenZeroHits(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "protocolschema", "testdata", "aap", "v1")
	jsonl, err := filepath.Glob(filepath.Join(root, "*.jsonl"))
	if err != nil || len(jsonl) != 4 {
		t.Fatalf("golden jsonl paths=%v err=%v", jsonl, err)
	}
	snapshots, err := filepath.Glob(filepath.Join(root, "*.snapshot.json"))
	if err != nil || len(snapshots) != 4 {
		t.Fatalf("golden snapshot paths=%v err=%v", snapshots, err)
	}
	for _, path := range append(append([]string{}, jsonl...), snapshots...) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, ".jsonl") {
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
			line := 0
			for scanner.Scan() {
				line++
				lineRaw := bytes.TrimSpace(scanner.Bytes())
				if len(lineRaw) == 0 {
					continue
				}
				if err := protocolevent.ScanPublicJSON(json.RawMessage(lineRaw)); err != nil {
					t.Fatalf("%s:%d sensitive hit: %v", filepath.Base(path), line, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := protocolevent.ScanPublicJSON(json.RawMessage(raw)); err != nil {
			t.Fatalf("%s sensitive hit: %v", filepath.Base(path), err)
		}
	}
}

func testSensitiveScannerNoEcho(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"authorization":"Bearer super-secret-token-value-xyz"}`,
		`{"nested":{"accessToken":"leak-me-please-12345678"}}`,
		`{"resumeToken":"resume-leak-aaaaaaaa"}`,
		`{"cookie":"session=cookie-leak-bbbbbbbb"}`,
		`{"password":"pw-leak-cccccccc"}`,
		`{"clientSecret":"awsk_live_leak_dddddddd"}`,
		`{"signedUrl":"https://bucket.example/x?X-Amz-Signature=sigleak"}`,
		`{"chainOfThought":"private chain of thought reasoning"}`,
		`{"message":"Bearer abcdefghijklmnop"}`,
		`{"message":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzZWNyZXQifQ.signaturevalue12"}`,
		`{"url":"https://objects.example/a?access_token=query-leak-eeeeeeee"}`,
		`{"pem":"-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBg\n-----END PRIVATE KEY-----"}`,
	}
	for _, payload := range cases {
		err := protocolevent.ScanPublicJSON(json.RawMessage(payload))
		if !errors.Is(err, protocolevent.ErrSensitivePayload) {
			t.Fatalf("expected sensitive reject for %s, got %v", payload, err)
		}
		msg := strings.ToLower(err.Error())
		for _, fragment := range []string{
			"super-secret", "leak-me", "resume-leak", "cookie-leak", "pw-leak",
			"awsk_live_leak", "sigleak", "chain of thought", "query-leak", "miievg",
			"signaturevalue",
		} {
			if strings.Contains(msg, fragment) {
				t.Fatalf("error echoed sensitive fragment %q: %v", fragment, err)
			}
		}
	}
}

func testSensitiveFieldAllowlist(t *testing.T) {
	t.Parallel()
	allow := protocolevent.PublicFieldAllowlist()
	if len(allow) == 0 {
		t.Fatal("empty allowlist")
	}
	for _, name := range allow {
		if !protocolevent.IsControlledPublicTokenField(name) {
			t.Fatalf("allowlist entry not recognized: %s", name)
		}
	}
	// Controlled usage metrics must pass the scanner.
	safe := json.RawMessage(`{"usage":{"inputTokens":12,"outputTokens":8,"totalTokens":20,"tokenCount":20,"maxTokens":4096,"maxOutputTokens":1024},"accessPolicy":"restricted"}`)
	if err := protocolevent.ScanPublicJSON(safe); err != nil {
		t.Fatalf("allowlisted metrics rejected: %v", err)
	}
	// Irreducible secret keys must never be allowlisted.
	policy := protocolevent.DefaultPayloadPolicy()
	for _, bad := range []string{"accessToken", "resumeToken", "authorization", "cookie", "password"} {
		policy.AllowedPropertyNames = append([]string(nil), protocolevent.DefaultPayloadPolicy().AllowedPropertyNames...)
		policy.AllowedPropertyNames = append(policy.AllowedPropertyNames, bad)
		if _, err := protocolevent.NewPayloadValidator(policy); !errors.Is(err, protocolevent.ErrPayloadPolicyInvalid) {
			t.Fatalf("unsafe allowlist entry %q accepted: %v", bad, err)
		}
	}
}

func testSensitiveControlledIDs(t *testing.T) {
	t.Parallel()
	// UUIDs and SHA-256 digests are public identifiers; they must not match secret patterns.
	id := uuid.NewString()
	digest := sha256.Sum256([]byte("not-a-secret-payload"))
	hash := hex.EncodeToString(digest[:])
	payload, _ := json.Marshal(map[string]any{
		"eventId": id, "runId": id, "sha256": hash, "inputHash": hash,
	})
	if err := protocolevent.ScanPublicJSON(payload); err != nil {
		t.Fatalf("public id/hash rejected: %v", err)
	}
	// Hash is one-way: original content must not appear in public payload.
	if strings.Contains(string(payload), "not-a-secret-payload") {
		t.Fatal("raw preimage present in public payload")
	}
}

func testSensitiveAppenderReject(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateTo(t, 40)
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)
	appender := protocolevent.NewEventAppender()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	event := protocolevent.NewProtocolEvent{
		ID: uuid.NewString(), EventStreamID: protocolStreamID,
		WorkspaceID: protocolWorkspaceID, AgentID: protocolAgentID,
		ConversationID: protocolSessionID, RunID: protocolRunID,
		Type: "future.event", SpecVersion: "1.0", TraceID: "trace-sensitive",
		OccurredAt: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
		Data:       json.RawMessage(`{"accessToken":"must-not-persist-secret-value"}`),
	}
	_, err = appender.AppendInTx(context.Background(), tx, []protocolevent.NewProtocolEvent{event})
	if !errors.Is(err, protocolevent.ErrSensitivePayload) {
		_ = tx.Rollback()
		t.Fatalf("append error=%v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "must-not-persist") {
		_ = tx.Rollback()
		t.Fatalf("append error echoed secret: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_events WHERE stream_id=$1`, protocolStreamID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("sensitive event persisted: count=%d", count)
	}
}
