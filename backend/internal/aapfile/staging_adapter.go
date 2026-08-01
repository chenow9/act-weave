package aapfile

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"actweave/backend/internal/storedobject"
)

// ObjectStagingStore adapts storedobject.ObjectStore to StagingStore.
type ObjectStagingStore struct {
	Store *storedobject.ObjectStore
}

// Stat implements StagingStore.
func (s ObjectStagingStore) Stat(ctx context.Context, bucket, key string) (BlobInfo, error) {
	if s.Store == nil {
		return BlobInfo{}, ErrInvalid
	}
	info, err := s.Store.StatRawObject(ctx, bucket, key)
	if err != nil {
		return BlobInfo{}, err
	}
	return BlobInfo{Size: info.Size, SHA256: info.SHA256}, nil
}

// Open implements StagingStore.
func (s ObjectStagingStore) Open(
	ctx context.Context,
	bucket, key string,
) (io.ReadCloser, BlobInfo, error) {
	if s.Store == nil {
		return nil, BlobInfo{}, ErrInvalid
	}
	body, info, err := s.Store.OpenRawObject(ctx, bucket, key)
	if err != nil {
		return nil, BlobInfo{}, err
	}
	return body, BlobInfo{Size: info.Size, SHA256: info.SHA256}, nil
}

// Delete implements StagingStore.
func (s ObjectStagingStore) Delete(ctx context.Context, bucket, key string) error {
	if s.Store == nil {
		return ErrInvalid
	}
	return s.Store.DeleteRawObject(ctx, bucket, key)
}

// PresignPutWithHeaders implements StagingStore.
func (s ObjectStagingStore) PresignPutWithHeaders(
	ctx context.Context,
	bucket, key string,
	ttl time.Duration,
	headers http.Header,
) (*url.URL, error) {
	if s.Store == nil {
		return nil, ErrInvalid
	}
	return s.Store.PresignPutWithHeaders(ctx, bucket, key, ttl, headers)
}
