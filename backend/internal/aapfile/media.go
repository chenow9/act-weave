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

// AllowedMediaType reports whether mediaType is in the v1 inbound allowlist.
func AllowedMediaType(mediaType string) bool {
	normalized, err := NormalizeMediaType(mediaType)
	if err != nil {
		return false
	}
	_, ok := AllowedMediaTypes[normalized]
	return ok
}

// AllowedOutboundMediaType reports whether mediaType is in the outbound ingest allowlist.
func AllowedOutboundMediaType(mediaType string) bool {
	normalized, err := NormalizeMediaType(mediaType)
	if err != nil {
		return false
	}
	_, ok := AllowedOutboundMediaTypes[normalized]
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
		return MediaTypePNG
	case bytes.HasPrefix(sample, []byte{0xff, 0xd8, 0xff}):
		return MediaTypeJPEG
	case bytes.HasPrefix(sample, []byte("GIF87a")) || bytes.HasPrefix(sample, []byte("GIF89a")):
		return MediaTypeGIF
	case len(sample) >= 12 && bytes.Equal(sample[0:4], []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WEBP")):
		return MediaTypeWEBP
	case bytes.HasPrefix(sample, []byte("%PDF")):
		return MediaTypePDF
	case isZipMagic(sample):
		return MediaTypeZip
	case isOLEMagic(sample):
		return mediaTypeOLE
	}
	return detected
}

func isZipMagic(sample []byte) bool {
	if len(sample) < 4 || sample[0] != 'P' || sample[1] != 'K' {
		return false
	}
	return sample[2] == 0x03 && sample[3] == 0x04 ||
		sample[2] == 0x05 && sample[3] == 0x06 ||
		sample[2] == 0x07 && sample[3] == 0x08
}

func isOLEMagic(sample []byte) bool {
	return bytes.HasPrefix(sample, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
}

func zipFamily(mediaType string) bool {
	switch mediaType {
	case MediaTypeZip, MediaTypeZipAlt, MediaTypeDocx, MediaTypeXlsx:
		return true
	default:
		return false
	}
}

func oleOffice(mediaType string) bool {
	switch mediaType {
	case MediaTypeDoc, MediaTypeXls, mediaTypeOLE:
		return true
	default:
		return false
	}
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
	if declared == MediaTypeJPEG && (detected == "image/jpg" || detected == MediaTypeJPEG) {
		return true
	}
	// docx/xlsx are ZIP containers; sniffing reports application/zip.
	if zipFamily(declared) && zipFamily(detected) {
		return true
	}
	// Legacy .doc/.xls are OLE compound files.
	if oleOffice(declared) && oleOffice(detected) {
		return true
	}
	return false
}

// outboundMediaTypesCompatible reports whether sniffed magic is acceptable for a
// declared outbound type. Text CSV/JSON/Markdown almost always sniff as text/plain.
// Do not use this helper for inbound complete (keep mediaTypesCompatible strict).
func outboundMediaTypesCompatible(declared, detected string) bool {
	declared, err := NormalizeMediaType(declared)
	if err != nil {
		return false
	}
	detected = strings.ToLower(strings.TrimSpace(detected))
	if detected == "" || detected == "application/octet-stream" {
		return true
	}
	if declared == detected {
		return true
	}
	if declared == "image/jpeg" && (detected == "image/jpg" || detected == "image/jpeg") {
		return true
	}
	switch declared {
	case "text/plain", "text/csv", "text/markdown", "application/json":
		return detected == "text/plain"
	default:
		return false
	}
}
