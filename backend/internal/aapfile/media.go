package aapfile

import (
	"bytes"
	"mime"
	"net/http"
	"strings"
)

// NormalizeMediaType returns a lower-cased type/subtype without parameters.
func NormalizeMediaType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrInvalid
	}
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", ErrInvalid
	}
	parsed = strings.ToLower(strings.TrimSpace(parsed))
	if parsed == "" {
		return "", ErrInvalid
	}
	return parsed, nil
}

// AllowedMediaType reports whether mediaType is in the v1 allowlist.
func AllowedMediaType(mediaType string) bool {
	normalized, err := NormalizeMediaType(mediaType)
	if err != nil {
		return false
	}
	_, ok := AllowedMediaTypes[normalized]
	return ok
}

// DetectMediaTypeFromSample sniffs up to the first 512 bytes (http.DetectContentType).
// For empty samples it returns application/octet-stream.
func DetectMediaTypeFromSample(sample []byte) string {
	if len(sample) == 0 {
		return "application/octet-stream"
	}
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(sample)))
	// http.DetectContentType may append "; charset=utf-8" for text; strip params.
	if idx := strings.IndexByte(detected, ';'); idx >= 0 {
		detected = strings.TrimSpace(detected[:idx])
	}
	// Map jpeg sniff to image/jpeg (DetectContentType returns image/jpeg already).
	// Prefer exact allowlist forms for known magic:
	switch {
	case bytes.HasPrefix(sample, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case bytes.HasPrefix(sample, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(sample, []byte("GIF87a")) || bytes.HasPrefix(sample, []byte("GIF89a")):
		return "image/gif"
	case len(sample) >= 12 && bytes.Equal(sample[0:4], []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(sample, []byte("%PDF")):
		return "application/pdf"
	}
	return detected
}

// mediaTypesCompatible reports whether detected magic is acceptable for declared.
// When sample is empty (optional magic path), only declared allowlist is enforced
// elsewhere; this returns true so complete can skip magic mismatch.
func mediaTypesCompatible(declared, detected string) bool {
	declared, err := NormalizeMediaType(declared)
	if err != nil {
		return false
	}
	detected = strings.ToLower(strings.TrimSpace(detected))
	if detected == "" || detected == "application/octet-stream" {
		// Weak sniff: do not fail hard when magic is inconclusive.
		return true
	}
	if declared == detected {
		return true
	}
	// JPEG aliases.
	if declared == "image/jpeg" && (detected == "image/jpg" || detected == "image/jpeg") {
		return true
	}
	return false
}
