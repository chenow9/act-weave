package agentaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"actweave/backend/internal/contextsummary"
	"actweave/backend/internal/storedobject"
)

// SummaryReadPurpose distinguishes runtime inject vs admin audit opens (IC-11).
type SummaryReadPurpose string

const (
	// PurposeMainAssembly is reserved for runtime compact inject (not used by audit service).
	PurposeMainAssembly SummaryReadPurpose = "MAIN_ASSEMBLY"
	// PurposeAdminAudit is the only purpose agent audit may open summary bodies.
	PurposeAdminAudit SummaryReadPurpose = "ADMIN_AUDIT"
)

// SummaryBodyReadRequest identifies a READY summary for authorized open.
// Body must never be sourced from protocol JSONB (T4-B bypass guard).
type SummaryBodyReadRequest struct {
	WorkspaceID    string
	RunID          string
	StepID         string
	SummaryID      string
	// ExpectedDigest is optional plaintext sha256 from step output_summary; mismatch → cipher.
	ExpectedDigest string
}

// SummaryBodyReadResult is body + display state (no protocol payload fields).
type SummaryBodyReadResult struct {
	Body  string
	State ContentState
}

// SummaryBodyReader opens encrypted CHAT_CONTEXT_SUMMARY objects for compact audit.
// Implementations must not read AAP protocol_events / run_items snapshots for body.
type SummaryBodyReader interface {
	Open(ctx context.Context, purpose SummaryReadPurpose, req SummaryBodyReadRequest) (SummaryBodyReadResult, error)
}

// SecureSummaryOpener is the permanent encrypted object surface used by audit reader.
type SecureSummaryOpener interface {
	Open(context.Context, storedobject.ReadRequest) (storedobject.OpenedObject, error)
}

// SummaryMetadataLookup loads READY summary metadata (object id / digest).
type SummaryMetadataLookup interface {
	Get(ctx context.Context, workspaceID, summaryID string) (contextsummary.Summary, error)
}

// EncryptedSummaryBodyReader implements SummaryBodyReader via metadata + SecureStore.
// ADMIN_AUDIT uses SYSTEM principal only for CHAT_CONTEXT_SUMMARY (runtime same path).
type EncryptedSummaryBodyReader struct {
	Summaries SummaryMetadataLookup
	Objects   SecureSummaryOpener
	// OpenCalls is incremented on every Open attempt (tests assert debug=false → 0).
	OpenCalls int
}

// NewEncryptedSummaryBodyReader wires repository + secure store.
func NewEncryptedSummaryBodyReader(summaries *contextsummary.Repository, objects *storedobject.SecureStore) (*EncryptedSummaryBodyReader, error) {
	if summaries == nil || objects == nil {
		return nil, errors.New("summary body reader requires summaries and secure store")
	}
	return &EncryptedSummaryBodyReader{Summaries: summaries, Objects: objects}, nil
}

// Open loads and decrypts summary body. Never accepts protocol JSONB.
func (r *EncryptedSummaryBodyReader) Open(
	ctx context.Context,
	purpose SummaryReadPurpose,
	req SummaryBodyReadRequest,
) (SummaryBodyReadResult, error) {
	if r == nil || r.Summaries == nil || r.Objects == nil {
		return SummaryBodyReadResult{State: ContentMissing}, errors.New("summary body reader not configured")
	}
	r.OpenCalls++
	if purpose != PurposeAdminAudit && purpose != PurposeMainAssembly {
		return SummaryBodyReadResult{State: ContentMissing}, ErrInvalid
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.SummaryID = strings.TrimSpace(req.SummaryID)
	req.ExpectedDigest = strings.ToLower(strings.TrimSpace(req.ExpectedDigest))
	if req.WorkspaceID == "" || req.SummaryID == "" {
		return SummaryBodyReadResult{State: ContentMissing}, ErrInvalid
	}

	sum, err := r.Summaries.Get(ctx, req.WorkspaceID, req.SummaryID)
	if err != nil {
		if errors.Is(err, contextsummary.ErrNotFound) {
			return SummaryBodyReadResult{State: ContentMissing}, nil
		}
		return SummaryBodyReadResult{State: ContentMissing}, err
	}
	if !strings.EqualFold(sum.Status, contextsummary.StatusReady) ||
		sum.ContentObjectID == nil || strings.TrimSpace(*sum.ContentObjectID) == "" {
		return SummaryBodyReadResult{State: ContentMissing}, nil
	}
	if req.ExpectedDigest != "" && sum.ContentSHA256 != nil {
		got := strings.ToLower(strings.TrimSpace(*sum.ContentSHA256))
		if got != "" && got != req.ExpectedDigest {
			return SummaryBodyReadResult{State: ContentCipher}, nil
		}
	}

	objectID := strings.TrimSpace(*sum.ContentObjectID)
	// SYSTEM + objectID is authorized only for CHAT_CONTEXT_SUMMARY (access_policy).
	opened, err := r.Objects.Open(ctx, storedobject.ReadRequest{
		WorkspaceID: req.WorkspaceID,
		ObjectID:    objectID,
		ActorType:   storedobject.CreatorSystem,
		ActorID:     objectID,
	})
	if err != nil {
		// Decrypt/auth/not-found → cipher|missing without partial plaintext.
		if errors.Is(err, storedobject.ErrDecrypt) || errors.Is(err, storedobject.ErrIntegrity) {
			return SummaryBodyReadResult{State: ContentCipher}, nil
		}
		if errors.Is(err, storedobject.ErrNotFound) {
			return SummaryBodyReadResult{State: ContentMissing}, nil
		}
		return SummaryBodyReadResult{State: ContentCipher}, nil
	}
	defer opened.Body.Close()
	if opened.Metadata.Kind != storedobject.KindChatContextSummary {
		return SummaryBodyReadResult{State: ContentCipher}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(opened.Body, contextsummary.MaxSummaryBodyBytes+1))
	if err != nil {
		return SummaryBodyReadResult{State: ContentCipher}, nil
	}
	if len(raw) == 0 || len(raw) > contextsummary.MaxSummaryBodyBytes {
		return SummaryBodyReadResult{State: ContentMissing}, nil
	}
	if req.ExpectedDigest != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != req.ExpectedDigest {
			return SummaryBodyReadResult{State: ContentCipher}, nil
		}
	}
	return SummaryBodyReadResult{Body: string(raw), State: ContentPlain}, nil
}
