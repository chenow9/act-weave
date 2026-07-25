package aap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
)

func commandRequestHash(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrCommandReceiptInvalid
	}
	digest := sha256.Sum256(encoded)
	return append([]byte(nil), digest[:]...), nil
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
