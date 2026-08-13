package protocolschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrSchemaViolation = errors.New("AAP_SCHEMA_VALIDATION_FAILED")

type validationRegistry struct {
	documents map[string]map[string]any
	err       error
}

var cachedValidationRegistry = sync.OnceValue(func() validationRegistry {
	registry := validationRegistry{documents: make(map[string]map[string]any, len(schemaNames))}
	if err := ValidateRegistry(); err != nil {
		registry.err = err
		return registry
	}
	for _, name := range schemaNames {
		raw, err := Document(name)
		if err != nil {
			registry.err = err
			return registry
		}
		var document map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err != nil {
			registry.err = err
			return registry
		}
		registry.documents[document["$id"].(string)] = document
	}
	return registry
})

// ValidateEventData validates an event Data object against the stable Schema
// Registry. Unknown additive events are accepted only when Data is an object.
// The returned error never contains payload values.
func ValidateEventData(eventType string, raw json.RawMessage) error {
	value, err := decodeValidationJSON(raw)
	if err != nil {
		return ErrSchemaViolation
	}
	ref, known := EventDataSchema(strings.TrimSpace(eventType))
	if !known {
		if _, object := value.(map[string]any); !object {
			return ErrSchemaViolation
		}
		return nil
	}
	registry := cachedValidationRegistry()
	if registry.err != nil {
		return ErrSchemaViolation
	}
	schema, documentID, ok := resolveValidationRef(registry, ref.DocumentID, ref.Fragment)
	if !ok || validateSchemaValue(registry, documentID, schema, value) != nil {
		return ErrSchemaViolation
	}
	return nil
}

// ValidateDocument validates a complete public value against one stable
// registry document. It is used for non-Event protocol objects such as the
// transport-only stream.error signal.
func ValidateDocument(name string, raw json.RawMessage) error {
	value, err := decodeValidationJSON(raw)
	if err != nil {
		return ErrSchemaViolation
	}
	name = strings.TrimSpace(name)
	if !contains(schemaNames, name) {
		return ErrSchemaViolation
	}
	registry := cachedValidationRegistry()
	if registry.err != nil {
		return ErrSchemaViolation
	}
	documentID := "https://schemas.actweave.dev/aap/v1/" + name
	schema, exists := registry.documents[documentID]
	if !exists || validateSchemaValue(registry, documentID, schema, value) != nil {
		return ErrSchemaViolation
	}
	return nil
}

func decodeValidationJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrSchemaViolation
	}
	return value, nil
}

func validateSchemaValue(
	registry validationRegistry,
	documentID string,
	schema map[string]any,
	value any,
) error {
	if ref, ok := schema["$ref"].(string); ok {
		resolved, resolvedDocument, exists := resolveValidationRef(registry, documentID, ref)
		if !exists {
			return ErrSchemaViolation
		}
		return validateSchemaValue(registry, resolvedDocument, resolved, value)
	}
	if branches, ok := schema["allOf"].([]any); ok {
		for _, branch := range branches {
			if validateSchemaValue(registry, documentID, schemaObject(branch), value) != nil {
				return ErrSchemaViolation
			}
		}
	}
	if branches, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, branch := range branches {
			if validateSchemaValue(registry, documentID, schemaObject(branch), value) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return ErrSchemaViolation
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, branch := range branches {
			if validateSchemaValue(registry, documentID, schemaObject(branch), value) == nil {
				matches++
			}
		}
		if matches != 1 {
			return ErrSchemaViolation
		}
	}
	if negated, ok := schema["not"].(map[string]any); ok &&
		validateSchemaValue(registry, documentID, negated, value) == nil {
		return ErrSchemaViolation
	}
	if expected, exists := schema["const"]; exists && !reflect.DeepEqual(expected, value) {
		return ErrSchemaViolation
	}
	if values, ok := schema["enum"].([]any); ok {
		matched := false
		for _, expected := range values {
			if reflect.DeepEqual(expected, value) {
				matched = true
				break
			}
		}
		if !matched {
			return ErrSchemaViolation
		}
	}
	if expected, exists := schema["type"]; exists && !validationTypeMatches(expected, value) {
		return ErrSchemaViolation
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := validateObjectKeywords(registry, documentID, schema, typed); err != nil {
			return err
		}
	case []any:
		if minimum, ok := schemaNumber(schema["minItems"]); ok && float64(len(typed)) < minimum {
			return ErrSchemaViolation
		}
		if maximum, ok := schemaNumber(schema["maxItems"]); ok && float64(len(typed)) > maximum {
			return ErrSchemaViolation
		}
		if unique, _ := schema["uniqueItems"].(bool); unique && !validationItemsUnique(typed) {
			return ErrSchemaViolation
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range typed {
				if validateSchemaValue(registry, documentID, itemSchema, item) != nil {
					return ErrSchemaViolation
				}
			}
		}
	case string:
		length := float64(utf8.RuneCountInString(typed))
		if minimum, ok := schemaNumber(schema["minLength"]); ok && length < minimum {
			return ErrSchemaViolation
		}
		if maximum, ok := schemaNumber(schema["maxLength"]); ok && length > maximum {
			return ErrSchemaViolation
		}
		if pattern, ok := schema["pattern"].(string); ok {
			compiled := compiledPattern(pattern)
			if compiled == nil || !compiled.MatchString(typed) {
				return ErrSchemaViolation
			}
		}
		if format, _ := schema["format"].(string); !validationFormatMatches(format, typed) {
			return ErrSchemaViolation
		}
	case json.Number:
		number, err := typed.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return ErrSchemaViolation
		}
		if minimum, ok := schemaNumber(schema["minimum"]); ok && number < minimum {
			return ErrSchemaViolation
		}
		if maximum, ok := schemaNumber(schema["maximum"]); ok && number > maximum {
			return ErrSchemaViolation
		}
	}
	return nil
}

func validateObjectKeywords(
	registry validationRegistry,
	documentID string,
	schema map[string]any,
	value map[string]any,
) error {
	if required, ok := schema["required"].([]any); ok {
		for _, field := range required {
			name, _ := field.(string)
			if _, exists := value[name]; !exists {
				return ErrSchemaViolation
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, propertySchema := range properties {
		field, exists := value[name]
		if !exists {
			continue
		}
		if validateSchemaValue(registry, documentID, schemaObject(propertySchema), field) != nil {
			return ErrSchemaViolation
		}
	}
	if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
		for name := range value {
			if _, allowed := properties[name]; !allowed {
				return ErrSchemaViolation
			}
		}
	}
	return nil
}

func validationTypeMatches(expected any, value any) bool {
	if values, ok := expected.([]any); ok {
		for _, candidate := range values {
			if validationTypeMatches(candidate, value) {
				return true
			}
		}
		return false
	}
	name, _ := expected.(string)
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		if _, err := number.Int64(); err == nil {
			return true
		}
		parsed, err := number.Float64()
		return err == nil && !math.IsInf(parsed, 0) && math.Trunc(parsed) == parsed
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

// patternCache memoizes compiled pattern keywords. Schemas are fixed documents
// with a small set of patterns, but validation runs per request and per string
// member, so recompiling dominated both time and allocations. A nil entry means
// the pattern does not compile, which fails validation the same way it always did.
var patternCache sync.Map

func compiledPattern(pattern string) *regexp.Regexp {
	if cached, ok := patternCache.Load(pattern); ok {
		compiled, _ := cached.(*regexp.Regexp)
		return compiled
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		compiled = nil
	}
	patternCache.Store(pattern, compiled)
	return compiled
}

func validationFormatMatches(format, value string) bool {
	switch format {
	case "":
		return true
	case "uuid":
		_, err := uuid.Parse(value)
		return err == nil
	case "date-time":
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	default:
		return true
	}
}

func validationItemsUnique(values []any) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return false
		}
		key := string(raw)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func resolveValidationRef(
	registry validationRegistry,
	currentDocumentID string,
	ref string,
) (map[string]any, string, bool) {
	documentID, fragment := currentDocumentID, ""
	if strings.HasPrefix(ref, "#") {
		fragment = ref
	} else {
		parts := strings.SplitN(ref, "#", 2)
		documentRef := parts[0]
		if strings.HasPrefix(documentRef, "https://") || strings.HasPrefix(documentRef, "http://") {
			documentID = documentRef
		} else {
			separator := strings.LastIndex(currentDocumentID, "/")
			documentID = currentDocumentID[:separator+1] + documentRef
		}
		if len(parts) == 2 {
			fragment = "#" + parts[1]
		}
	}
	document, exists := registry.documents[documentID]
	if !exists {
		return nil, "", false
	}
	if fragment == "" || fragment == "#" {
		return document, documentID, true
	}
	var node any = document
	for _, segment := range strings.Split(strings.TrimPrefix(fragment, "#/"), "/") {
		object, ok := node.(map[string]any)
		if !ok {
			return nil, "", false
		}
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		node, exists = object[segment]
		if !exists {
			return nil, "", false
		}
	}
	resolved, ok := node.(map[string]any)
	return resolved, documentID, ok
}

func schemaObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func schemaNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		return parsed, err == nil
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
