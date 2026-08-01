package aap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
)

func commandRequestHash(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrCommandReceiptInvalid
	}
	digest := sha256.Sum256(encoded)
	return append([]byte(nil), digest[:]...), nil
}

// FileCreateRequestHashInput is the stable idempotency material for file.create
// (design §5.6.4). Must not include upload URL or presign headers.
type FileCreateRequestHashInput struct {
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// FileCreateCommandRequestHash hashes create intent fields without upload URL.
func FileCreateCommandRequestHash(input FileCreateRequestHashInput) ([]byte, error) {
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.Filename = strings.TrimSpace(input.Filename)
	return commandRequestHash(input)
}

// FileCompleteRequestHashInput is the stable idempotency material for file.complete.
// Must not include upload URL or staging details.
type FileCompleteRequestHashInput struct {
	FileID string `json:"fileId"`
	SHA256 string `json:"sha256,omitempty"`
}

// FileCompleteCommandRequestHash hashes complete fields without upload URL.
func FileCompleteCommandRequestHash(input FileCompleteRequestHashInput) ([]byte, error) {
	input.FileID = strings.ToLower(strings.TrimSpace(input.FileID))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	return commandRequestHash(input)
}

func observeCommand(
	ctx context.Context,
	ledger CommandReceiptLedger,
	key CommandReceiptKey,
	hash []byte,
) error {
	if ledger == nil {
		return nil
	}
	_, err := ledger.Observe(ctx, ObserveCommandInput{Key: key, RequestHash: hash})
	return err
}

func completeCommand(
	ctx context.Context,
	ledger CommandReceiptLedger,
	key CommandReceiptKey,
	hash []byte,
	resourceType, resourceID string,
	version int64,
) error {
	if ledger == nil {
		return nil
	}
	_, err := ledger.Complete(ctx, CompleteCommandInput{
		Key: key, RequestHash: hash, ResourceType: resourceType,
		ResourceID: resourceID, ResponseVersion: version,
	})
	return err
}
