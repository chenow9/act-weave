package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultInlineDetailBytes = 16 << 10
	maxInlineDetailBytes     = 64 << 10
	redactedValue            = "[REDACTED]"
)

var (
	actionPattern        = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_-]*)+$`)
	resourceTypePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	stableVersionPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	jwtPattern           = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
)

type Builder struct {
	inlineLimit     int
	sensitiveValues []string
}

func NewBuilder(inlineLimit int, sensitiveValues ...string) (*Builder, error) {
	if inlineLimit == 0 {
		inlineLimit = DefaultInlineDetailBytes
	}
	if inlineLimit < 256 || inlineLimit > maxInlineDetailBytes {
		return nil, ErrInvalid
	}
	values := make([]string, 0, len(sensitiveValues))
	for _, value := range sensitiveValues {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return &Builder{inlineLimit: inlineLimit, sensitiveValues: values}, nil
}

func (builder *Builder) Build(input BuildInput) (Event, error) {
	input = normalizeBuildInput(input)
	if !validBuildInput(input) {
		return Event{}, ErrInvalid
	}
	changes := builder.buildDiff(input.Before, input.After)
	metadata := builder.redactMap(input.Metadata)
	if headers := builder.allowedHeaders(input.Headers); len(headers) > 0 {
		metadata["headers"] = headers
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return Event{}, ErrInvalid
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return Event{}, ErrInvalid
	}
	if len(changesJSON)+len(metadataJSON) > builder.inlineLimit {
		if input.PayloadObjectID == "" {
			return Event{}, ErrPayloadRequired
		}
		detail := append(append([]byte(nil), changesJSON...), metadataJSON...)
		digest := sha256.Sum256(detail)
		changesJSON, _ = json.Marshal(map[string]any{
			"overflow": true, "sha256": hex.EncodeToString(digest[:]),
			"sizeBytes": len(detail), "payloadObjectId": input.PayloadObjectID,
		})
		metadataJSON = json.RawMessage(`{"detailStoredExternally":true}`)
	}
	event := Event{
		ID: input.ID, OccurredAt: input.OccurredAt, WorkspaceID: input.WorkspaceID,
		ActorType: input.ActorType, ActorID: input.ActorID, ActorDisplay: builder.redactString(input.ActorDisplay),
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Result: input.Result, RequestID: input.RequestID, TraceID: input.TraceID,
		UserAgent: builder.redactString(input.UserAgent), Changes: changesJSON,
		Metadata: metadataJSON, PayloadObjectID: input.PayloadObjectID, SchemaVersion: SchemaVersionV1,
	}
	if input.SourceIP != "" {
		event.SourceIP, err = netip.ParseAddr(input.SourceIP)
		if err != nil {
			return Event{}, ErrInvalid
		}
	}
	return event, nil
}

func normalizeBuildInput(input BuildInput) BuildInput {
	input.ID, input.WorkspaceID = strings.TrimSpace(input.ID), strings.TrimSpace(input.WorkspaceID)
	input.ActorType, input.ActorID = strings.ToUpper(strings.TrimSpace(input.ActorType)), strings.TrimSpace(input.ActorID)
	input.ActorDisplay = strings.TrimSpace(input.ActorDisplay)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.ResourceType = strings.ToUpper(strings.TrimSpace(input.ResourceType))
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Result = strings.ToUpper(strings.TrimSpace(input.Result))
	input.RequestID, input.TraceID = strings.TrimSpace(input.RequestID), strings.TrimSpace(input.TraceID)
	input.SourceIP, input.UserAgent = strings.TrimSpace(input.SourceIP), strings.TrimSpace(input.UserAgent)
	input.PayloadObjectID = strings.TrimSpace(input.PayloadObjectID)
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	} else {
		input.OccurredAt = input.OccurredAt.UTC()
	}
	return input
}

func validBuildInput(input BuildInput) bool {
	if input.OccurredAt.IsZero() || !validAuditUUID(input.ID) ||
		(input.WorkspaceID != "" && !validAuditUUID(input.WorkspaceID)) ||
		(input.ActorID != "" && !validAuditUUID(input.ActorID)) ||
		(input.ResourceID != "" && !validAuditUUID(input.ResourceID)) ||
		(input.PayloadObjectID != "" && (!validAuditUUID(input.PayloadObjectID) || input.WorkspaceID == "")) ||
		(input.ActorType == "USER" && input.ActorID == "") ||
		(input.ActorType != "USER" && input.ActorType != "SERVICE_PRINCIPAL" && input.ActorType != "SYSTEM") ||
		len(input.ActorDisplay) < 1 || len(input.ActorDisplay) > 255 ||
		!actionPattern.MatchString(input.Action) || !resourceTypePattern.MatchString(input.ResourceType) ||
		(input.Result != "SUCCESS" && input.Result != "FAILURE" && input.Result != "DENIED") ||
		len(input.RequestID) > 255 || len(input.TraceID) > 255 || len(input.UserAgent) > 1024 {
		return false
	}
	return true
}

func (builder *Builder) buildDiff(before, after map[string]any) map[string]any {
	keys := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	diff := make(map[string]any)
	for key := range keys {
		oldValue, oldExists := before[key]
		newValue, newExists := after[key]
		if oldExists == newExists && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		diff[key] = map[string]any{
			"before": builder.redactValue(key, oldValue),
			"after":  builder.redactValue(key, newValue),
		}
	}
	return diff
}

func (builder *Builder) redactMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = builder.redactValue(key, value)
	}
	return output
}

func (builder *Builder) redactValue(key string, value any) any {
	if sensitiveAuditKey(key) || rawContentKey(key) {
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		return builder.redactMap(typed)
	case []any:
		values := make([]any, len(typed))
		for index, child := range typed {
			values[index] = builder.redactValue("", child)
		}
		return values
	case string:
		return builder.redactString(typed)
	default:
		return value
	}
}

func (builder *Builder) redactString(value string) string {
	value = jwtPattern.ReplaceAllString(value, redactedValue)
	for _, sensitive := range builder.sensitiveValues {
		value = strings.ReplaceAll(value, sensitive, redactedValue)
	}
	return value
}

func (builder *Builder) allowedHeaders(headers map[string][]string) map[string][]string {
	allowed := make(map[string][]string)
	for name, values := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		if !allowedAuditHeader(name) {
			continue
		}
		clean := make([]string, 0, len(values))
		for _, value := range values {
			value = builder.redactString(strings.TrimSpace(value))
			if value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\r\n") {
				clean = append(clean, value)
			}
		}
		if len(clean) > 0 {
			allowed[name] = clean
		}
	}
	return allowed
}

func allowedAuditHeader(name string) bool {
	switch name {
	case "accept", "content-type", "x-request-id", "x-correlation-id":
		return true
	default:
		return false
	}
}

func sensitiveAuditKey(key string) bool {
	normalized := normalizeAuditKey(key)
	switch normalized {
	case "password", "passwd", "authorization", "proxyauthorization", "cookie", "setcookie",
		"jwt", "token", "accesstoken", "refreshtoken", "idtoken", "apikey",
		"secret", "secretid", "secretvalue", "clientsecret", "privatekey",
		"resumetoken", "signedurl", "chainofthought", "clientassertion", "subjecttoken",
		"credential", "privatejwk":
		return true
	default:
		// Suffix / contains matches for compound keys (e.g. toolResumeToken).
		if strings.HasSuffix(normalized, "secret") || strings.HasSuffix(normalized, "password") {
			return true
		}
		if strings.HasSuffix(normalized, "token") && !strings.HasSuffix(normalized, "tokens") {
			return true
		}
		return strings.Contains(normalized, "resumetoken") ||
			strings.Contains(normalized, "chainofthought") ||
			strings.Contains(normalized, "privatekey")
	}
}

func rawContentKey(key string) bool {
	normalized := normalizeAuditKey(key)
	switch normalized {
	case "raw", "body", "content", "prompt", "systemprompt", "input", "output",
		"request", "response", "payload", "messagebody", "toolpayload", "toolresult":
		return true
	default:
		// Compound free-text surfaces used by AAP ops/audit callers.
		return strings.HasSuffix(normalized, "payload") ||
			strings.HasSuffix(normalized, "messagebody")
	}
}

func normalizeAuditKey(key string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(key)))
}

func validAuditUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validateEvent(event Event) error {
	if !stableVersionPattern.MatchString(event.SchemaVersion) || !json.Valid(event.Changes) ||
		!json.Valid(event.Metadata) || jwtPattern.MatchString(event.ActorDisplay) ||
		jwtPattern.MatchString(event.UserAgent) {
		return ErrInvalid
	}
	var changes, metadata map[string]any
	if errors.Join(json.Unmarshal(event.Changes, &changes), json.Unmarshal(event.Metadata, &metadata)) != nil ||
		changes == nil || metadata == nil || !safeAuditObject(changes, false) || !safeAuditObject(metadata, true) {
		return ErrInvalid
	}
	return nil
}

func safeAuditObject(object map[string]any, metadata bool) bool {
	for key, value := range object {
		if metadata && normalizeAuditKey(key) == "headers" {
			headers, ok := value.(map[string]any)
			if !ok {
				return false
			}
			for name, values := range headers {
				if !allowedAuditHeader(strings.ToLower(name)) || !safeAuditValue("", values) {
					return false
				}
			}
			continue
		}
		if (sensitiveAuditKey(key) || rawContentKey(key)) && !fullyRedacted(value) {
			return false
		}
		if !safeAuditValue(key, value) {
			return false
		}
	}
	return true
}

func safeAuditValue(key string, value any) bool {
	if (sensitiveAuditKey(key) || rawContentKey(key)) && !fullyRedacted(value) {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		return safeAuditObject(typed, false)
	case []any:
		for _, child := range typed {
			if !safeAuditValue("", child) {
				return false
			}
		}
	case string:
		return !jwtPattern.MatchString(typed)
	}
	return true
}

func fullyRedacted(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == redactedValue
	case map[string]any:
		if len(typed) == 0 {
			return false
		}
		for _, child := range typed {
			if !fullyRedacted(child) {
				return false
			}
		}
		return true
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, child := range typed {
			if !fullyRedacted(child) {
				return false
			}
		}
		return true
	case nil:
		return true
	default:
		return false
	}
}
