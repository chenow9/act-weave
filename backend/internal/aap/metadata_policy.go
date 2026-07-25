package aap

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxRunMetadataEntries    = 32
	MaxRunMetadataKeyBytes   = 64
	MaxRunMetadataValueBytes = 512
	MaxRunMetadataTotalBytes = 8 << 10
)

var (
	metadataKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	jwtValuePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}$`)
)

// ValidateRunMetadata is shared by Transport and application services so an
// alternate adapter cannot use Metadata as an authorization or secret side
// channel. Metadata remains opaque business correlation data only.
func ValidateRunMetadata(metadata map[string]string) error {
	if len(metadata) > MaxRunMetadataEntries {
		return ErrRunInvalid
	}
	total := 0
	seen := make(map[string]struct{}, len(metadata))
	for key, value := range metadata {
		trimmedKey := strings.TrimSpace(key)
		lowerKey := strings.ToLower(trimmedKey)
		if key != trimmedKey || !metadataKeyPattern.MatchString(key) ||
			len([]byte(key)) > MaxRunMetadataKeyBytes || !utf8.ValidString(value) ||
			len([]byte(value)) > MaxRunMetadataValueBytes ||
			strings.ContainsAny(value, "\r\n\x00") || sensitiveMetadataKey(lowerKey) ||
			sensitiveMetadataValue(value) {
			return ErrRunInvalid
		}
		if _, duplicate := seen[lowerKey]; duplicate {
			return ErrRunInvalid
		}
		seen[lowerKey] = struct{}{}
		total += len([]byte(key)) + len([]byte(value))
		if total > MaxRunMetadataTotalBytes {
			return ErrRunInvalid
		}
	}
	return nil
}

func sensitiveMetadataKey(value string) bool {
	for _, forbidden := range []string{
		"authorization", "credential", "password", "passwd", "secret",
		"token", "cookie", "session", "privatekey", "private_key", "apikey", "api_key",
	} {
		if strings.Contains(value, forbidden) {
			return true
		}
	}
	return false
}

func sensitiveMetadataValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "bearer ") ||
		strings.Contains(lower, "-----begin private key-----") ||
		strings.Contains(lower, "-----begin rsa private key-----") ||
		strings.Contains(lower, "-----begin ec private key-----") ||
		strings.HasPrefix(lower, "sk-") || jwtValuePattern.MatchString(trimmed)
}
