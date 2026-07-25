package protocolevent

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"actweave/backend/internal/protocolschema"
)

const (
	DefaultMaxEventDataBytes = 2 * 1024 * 1024
	DefaultMaxEnvelopeBytes  = DefaultMaxEventDataBytes + 64*1024
)

var (
	ErrSchemaValidationFailed = errors.New("AAP_SCHEMA_VALIDATION_FAILED")
	ErrSensitivePayload       = errors.New("AAP_SENSITIVE_PAYLOAD_REJECTED")
	ErrPayloadTooLarge        = errors.New("AAP_PAYLOAD_TOO_LARGE")
	ErrPayloadPolicyInvalid   = errors.New("AAP_PAYLOAD_POLICY_INVALID")
)

type PayloadPolicy struct {
	MaxDataBytes         int
	MaxEnvelopeBytes     int
	AllowedPropertyNames []string
}

func DefaultPayloadPolicy() PayloadPolicy {
	return PayloadPolicy{
		MaxDataBytes: DefaultMaxEventDataBytes, MaxEnvelopeBytes: DefaultMaxEnvelopeBytes,
		AllowedPropertyNames: []string{
			"inputTokens", "outputTokens", "totalTokens", "tokenCount",
			"maxTokens", "maxOutputTokens", "accessPolicy",
		},
	}
}

type PayloadValidator struct {
	maxDataBytes     int
	maxEnvelopeBytes int
	allowedKeys      map[string]struct{}
}

func NewPayloadValidator(policy PayloadPolicy) (*PayloadValidator, error) {
	if policy.MaxDataBytes <= 0 || policy.MaxEnvelopeBytes <= 0 ||
		policy.MaxEnvelopeBytes < policy.MaxDataBytes {
		return nil, ErrPayloadPolicyInvalid
	}
	validator := &PayloadValidator{
		maxDataBytes: policy.MaxDataBytes, maxEnvelopeBytes: policy.MaxEnvelopeBytes,
		allowedKeys: make(map[string]struct{}, len(policy.AllowedPropertyNames)),
	}
	for _, key := range policy.AllowedPropertyNames {
		normalized := normalizeSensitiveKey(key)
		if normalized == "" || sensitiveKey(normalized) {
			return nil, ErrPayloadPolicyInvalid
		}
		validator.allowedKeys[normalized] = struct{}{}
	}
	return validator, nil
}

func MustDefaultPayloadValidator() *PayloadValidator {
	validator, err := NewPayloadValidator(DefaultPayloadPolicy())
	if err != nil {
		panic(err)
	}
	return validator
}

func (validator *PayloadValidator) ValidateEventData(eventType string, data json.RawMessage) error {
	if validator == nil || validator.maxDataBytes <= 0 {
		return ErrPayloadPolicyInvalid
	}
	if len(data) > validator.maxDataBytes {
		return ErrPayloadTooLarge
	}
	if err := protocolschema.ValidateEventData(eventType, data); err != nil {
		return ErrSchemaValidationFailed
	}
	value, err := decodePolicyJSON(data)
	if err != nil {
		return ErrSchemaValidationFailed
	}
	if scanErr := validator.scan(value, 0); errors.Is(scanErr, ErrPayloadTooLarge) {
		return ErrPayloadTooLarge
	} else if scanErr != nil {
		return ErrSensitivePayload
	}
	return nil
}

// ScanPublicJSON recursively rejects forbidden property names and value patterns
// in public protocol payloads (events, item snapshots, transport data). Errors never
// include the sensitive value itself — only a stable sentinel.
func ScanPublicJSON(raw json.RawMessage) error {
	return MustDefaultPayloadValidator().ScanValueJSON(raw)
}

// ScanValueJSON is the validator-bound recursive public-payload scanner.
func (validator *PayloadValidator) ScanValueJSON(raw json.RawMessage) error {
	if validator == nil {
		return ErrPayloadPolicyInvalid
	}
	if len(raw) == 0 {
		return nil
	}
	value, err := decodePolicyJSON(raw)
	if err != nil {
		return ErrSchemaValidationFailed
	}
	if scanErr := validator.scan(value, 0); errors.Is(scanErr, ErrPayloadTooLarge) {
		return ErrPayloadTooLarge
	} else if scanErr != nil {
		return ErrSensitivePayload
	}
	return nil
}

// PublicFieldAllowlist returns the controlled property names that may contain
// the substring "token" (or similar) without being treated as secrets.
func PublicFieldAllowlist() []string {
	return append([]string(nil), DefaultPayloadPolicy().AllowedPropertyNames...)
}

// IsControlledPublicTokenField reports whether a JSON property name is on the
// irreversible/controlled usage-metrics allowlist (e.g. inputTokens).
func IsControlledPublicTokenField(name string) bool {
	normalized := normalizeSensitiveKey(name)
	for _, allowed := range DefaultPayloadPolicy().AllowedPropertyNames {
		if normalizeSensitiveKey(allowed) == normalized {
			return true
		}
	}
	return false
}

func (validator *PayloadValidator) ValidateEnvelopeSize(payload json.RawMessage) error {
	if validator == nil || validator.maxEnvelopeBytes <= 0 {
		return ErrPayloadPolicyInvalid
	}
	if len(payload) > validator.maxEnvelopeBytes {
		return ErrPayloadTooLarge
	}
	return nil
}

func (validator *PayloadValidator) scan(value any, depth int) error {
	if depth > 64 {
		return ErrPayloadTooLarge
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := normalizeSensitiveKey(key)
			if _, allowed := validator.allowedKeys[normalized]; !allowed && sensitiveKey(normalized) {
				return ErrSensitivePayload
			}
			if err := validator.scan(nested, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := validator.scan(nested, depth+1); err != nil {
				return err
			}
		}
	case string:
		if sensitiveString(typed) {
			return ErrSensitivePayload
		}
	}
	return nil
}

func decodePolicyJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func normalizeSensitiveKey(value string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(
		strings.ToLower(strings.TrimSpace(value)),
	)
}

func irreduciblySensitiveKey(value string) bool {
	return value == "authorization" || value == "cookie" || value == "password" ||
		value == "secret" || value == "token" || value == "resumetoken" ||
		value == "signedurl" || value == "chainofthought" || value == "apikey" ||
		value == "privatekey" || value == "credential"
}

func sensitiveKey(value string) bool {
	if irreduciblySensitiveKey(value) || strings.Contains(value, "authorization") ||
		strings.Contains(value, "cookie") || strings.Contains(value, "password") ||
		strings.Contains(value, "resumetoken") || strings.Contains(value, "signedurl") ||
		strings.Contains(value, "chainofthought") || strings.Contains(value, "privatekey") ||
		strings.Contains(value, "apikey") || strings.Contains(value, "credential") {
		return true
	}
	if strings.HasSuffix(value, "secret") {
		return true
	}
	return strings.HasSuffix(value, "token") && !strings.HasSuffix(value, "tokens")
}

var (
	bearerOrBasicPattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{8,}`)
	jwtPattern           = regexp.MustCompile(`^[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}$`)
	privateKeyPattern    = regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----`)
	actWeaveKeyPattern   = regexp.MustCompile(`(?i)\bawsk_[a-z0-9_-]{8,}`)
)

func sensitiveString(value string) bool {
	trimmed := strings.TrimSpace(value)
	if bearerOrBasicPattern.MatchString(trimmed) || jwtPattern.MatchString(trimmed) ||
		privateKeyPattern.MatchString(trimmed) || actWeaveKeyPattern.MatchString(trimmed) {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	for key := range parsed.Query() {
		normalized := normalizeSensitiveKey(key)
		if normalized == "sig" || normalized == "signature" ||
			strings.Contains(normalized, "signature") || normalized == "accesstoken" ||
			normalized == "resumetoken" {
			return true
		}
	}
	return false
}
