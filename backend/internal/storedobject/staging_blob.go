package storedobject

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// BlobStat is a minimal raw-object stat (staging buckets; not stored_objects).
type BlobStat struct {
	Size   int64
	SHA256 string
}

// StatRawObject stats a raw bucket object without stored_objects authorization.
// Used for AAP staging complete (plaintext short-lived objects).
func (store *ObjectStore) StatRawObject(
	ctx context.Context,
	bucket, objectKey string,
) (BlobStat, error) {
	if store == nil || store.backend == nil || ctx == nil {
		return BlobStat{}, ErrInvalid
	}
	bucket = strings.TrimSpace(bucket)
	objectKey = strings.TrimSpace(objectKey)
	if bucket == "" || objectKey == "" {
		return BlobStat{}, ErrInvalid
	}
	info, err := store.backend.Stat(ctx, bucket, objectKey)
	if err != nil {
		return BlobStat{}, fmt.Errorf("stat raw object: %w: %v", ErrObjectStorage, err)
	}
	return BlobStat{Size: info.Size, SHA256: info.SHA256}, nil
}

// OpenRawObject opens a raw bucket object body without stored_objects authorization.
func (store *ObjectStore) OpenRawObject(
	ctx context.Context,
	bucket, objectKey string,
) (io.ReadCloser, BlobStat, error) {
	if store == nil || store.backend == nil || ctx == nil {
		return nil, BlobStat{}, ErrInvalid
	}
	bucket = strings.TrimSpace(bucket)
	objectKey = strings.TrimSpace(objectKey)
	if bucket == "" || objectKey == "" {
		return nil, BlobStat{}, ErrInvalid
	}
	body, info, err := store.backend.Open(ctx, bucket, objectKey)
	if err != nil {
		return nil, BlobStat{}, fmt.Errorf("open raw object: %w: %v", ErrObjectStorage, err)
	}
	return body, BlobStat{Size: info.Size, SHA256: info.SHA256}, nil
}

// DeleteRawObject deletes a raw bucket object (staging GC / promote cleanup).
func (store *ObjectStore) DeleteRawObject(
	ctx context.Context,
	bucket, objectKey string,
) error {
	if store == nil || store.backend == nil || ctx == nil {
		return ErrInvalid
	}
	bucket = strings.TrimSpace(bucket)
	objectKey = strings.TrimSpace(objectKey)
	if bucket == "" || objectKey == "" {
		return ErrInvalid
	}
	if err := store.backend.Delete(ctx, bucket, objectKey); err != nil {
		return fmt.Errorf("delete raw object: %w: %v", ErrObjectStorage, err)
	}
	return nil
}
