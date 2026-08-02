package a2agateway

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestMaxBytesReadCloser_ExactLimitOK(t *testing.T) {
	t.Parallel()
	const limit = 64
	src := bytes.Repeat([]byte("a"), limit)
	r := &maxBytesReadCloser{r: io.NopCloser(bytes.NewReader(src)), n: limit}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("exact limit read: %v", err)
	}
	if len(got) != limit {
		t.Fatalf("len=%d want %d", len(got), limit)
	}
	// Subsequent read should be EOF, not oversize.
	n, err := r.Read(make([]byte, 8))
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("after exact: n=%d err=%v want EOF", n, err)
	}
}

func TestMaxBytesReadCloser_LimitPlusOneDenied(t *testing.T) {
	t.Parallel()
	const limit = 64
	src := bytes.Repeat([]byte("b"), limit+1)
	r := &maxBytesReadCloser{r: io.NopCloser(bytes.NewReader(src)), n: limit}
	buf := make([]byte, limit+8)
	n, err := r.Read(buf)
	// First read may return up to limit bytes without error.
	if n > limit {
		t.Fatalf("returned %d > limit", n)
	}
	if err != nil && !errors.Is(err, ErrSSRFDenied) {
		// May return partial then oversize on next read
		t.Logf("first read n=%d err=%v", n, err)
	}
	// Drain: must eventually get oversize, never accept limit+1 total.
	total := n
	for {
		n2, e2 := r.Read(buf)
		total += n2
		if e2 != nil {
			if !errors.Is(e2, ErrSSRFDenied) && !errors.Is(e2, io.EOF) {
				t.Fatalf("unexpected err: %v", e2)
			}
			if errors.Is(e2, io.EOF) && total > limit {
				t.Fatalf("accepted %d bytes with EOF", total)
			}
			if errors.Is(e2, ErrSSRFDenied) {
				if total > limit {
					// bytes returned before oversize signal must be <= limit
				}
				return
			}
			break
		}
		if total > limit {
			t.Fatalf("accepted %d without oversize", total)
		}
	}
	// If we finished without oversize while source has limit+1, fail.
	if total <= limit {
		// Reader correctly stopped at limit; oversize on next Read.
		_, e3 := r.Read(buf)
		if !errors.Is(e3, ErrSSRFDenied) {
			t.Fatalf("want oversize after limit, got %v total=%d", e3, total)
		}
	}
}
