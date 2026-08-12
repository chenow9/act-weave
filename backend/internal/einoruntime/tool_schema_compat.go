package einoruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Platform-frozen hard limits for OpenAI Agentic tool catalogs (not user options).
// Conservative, benchmark-informed defaults; exact N accepted / N+1 rejected is tested.
const (
	// MaxToolDescriptionBytes is the max UTF-8 byte length of a single tool description.
	MaxToolDescriptionBytes = 4096
	// MaxToolSchemaBytes is the max canonical JSON byte length of a single parameters schema.
	// Also enforced as a raw-byte cap *before* JSON parse / annotation stripping.
	MaxToolSchemaBytes = 32 * 1024
	// MaxSchemaNestingDepth is the max object/array nesting depth in a parameters schema.
	MaxSchemaNestingDepth = 12
	// MaxSchemaPropertyCount is the max total property count across the schema tree.
	MaxSchemaPropertyCount = 256
	// MaxCatalogDeferredMetadataBytes is the max sum of deferred name+description bytes.
	MaxCatalogDeferredMetadataBytes = 512 * 1024
	// MaxCatalogDeferredToolCount is the max number of deferred tools in one catalog.
	MaxCatalogDeferredToolCount = 2000
)

// MODEL_TOOL_CATALOG_INVALID family errors (typed, fail-closed).
const (
	ModelToolCatalogInvalidCode = "MODEL_TOOL_CATALOG_INVALID"
)

var (
	// ErrModelToolCatalogInvalid is the stable MODEL_TOOL_CATALOG_INVALID family root.
	ErrModelToolCatalogInvalid = errors.New(ModelToolCatalogInvalidCode)

	// ErrToolSchemaInvalidRoot is returned when parameters is not a JSON object.
	ErrToolSchemaInvalidRoot = fmt.Errorf("%w: parameters schema root must be object", ErrModelToolCatalogInvalid)
	// ErrToolSchemaDuplicateKey is returned when parameters JSON has duplicate keys.
	ErrToolSchemaDuplicateKey = fmt.Errorf("%w: duplicate JSON key in parameters schema", ErrModelToolCatalogInvalid)
	// ErrToolSchemaNonFinite is returned for NaN/Inf numeric values.
	ErrToolSchemaNonFinite = fmt.Errorf("%w: non-finite number in parameters schema", ErrModelToolCatalogInvalid)
	// ErrToolSchemaExternalRef is returned for remote/external $ref values.
	ErrToolSchemaExternalRef = fmt.Errorf("%w: external or remote $ref is not allowed", ErrModelToolCatalogInvalid)
	// ErrToolSchemaUnsafeRef is returned for unsupported recursive/local $ref shapes.
	ErrToolSchemaUnsafeRef = fmt.Errorf("%w: unsupported $ref shape", ErrModelToolCatalogInvalid)
	// ErrToolSchemaUnsupportedKeyword is returned for keywords outside the allowlist
	// when the policy is reject (validation-semantic keywords are never silently dropped).
	ErrToolSchemaUnsupportedKeyword = fmt.Errorf("%w: unsupported schema keyword", ErrModelToolCatalogInvalid)
	// ErrToolSchemaInvalidValue is returned for wrong types/domains on allowed keywords.
	ErrToolSchemaInvalidValue = fmt.Errorf("%w: invalid schema keyword value", ErrModelToolCatalogInvalid)
	// ErrToolDescriptionTooLarge is returned when description exceeds MaxToolDescriptionBytes.
	ErrToolDescriptionTooLarge = fmt.Errorf("%w: description exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolSchemaTooLarge is returned when raw or canonical schema exceeds MaxToolSchemaBytes.
	ErrToolSchemaTooLarge = fmt.Errorf("%w: schema exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolSchemaTooDeep is returned when nesting exceeds MaxSchemaNestingDepth.
	ErrToolSchemaTooDeep = fmt.Errorf("%w: schema nesting exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolSchemaTooManyProperties is returned when total properties exceed MaxSchemaPropertyCount.
	ErrToolSchemaTooManyProperties = fmt.Errorf("%w: schema property count exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolCatalogDeferredTooLarge is returned when deferred metadata budget is exceeded.
	ErrToolCatalogDeferredTooLarge = fmt.Errorf("%w: deferred catalog metadata exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolCatalogDeferredTooMany is returned when deferred tool count exceeds the limit.
	ErrToolCatalogDeferredTooMany = fmt.Errorf("%w: deferred tool count exceeds platform limit", ErrModelToolCatalogInvalid)
	// ErrToolSchemaInvalidUTF8 is returned for non-UTF-8 schema/description bytes.
	ErrToolSchemaInvalidUTF8 = fmt.Errorf("%w: schema must be valid UTF-8", ErrModelToolCatalogInvalid)
	// ErrToolSchemaAdapterNumericUnrepresentable is returned when a JSON number in the
	// schema cannot survive the pinned agenticopenai adapter wire path
	// (sonic.Marshal → sonic.Unmarshal into map[string]any without UseNumber →
	// float64) without changing mathematical value. Catalog freeze fails closed so
	// digest/search/estimator never describe a different schema than the model receives.
	ErrToolSchemaAdapterNumericUnrepresentable = fmt.Errorf("%w: schema number not exactly representable on OpenAI adapter wire", ErrModelToolCatalogInvalid)
)

// openaiSchemaKeywordPolicy documents the canonical allowlist for OpenAI Agentic
// tool parameter schemas.
//
// Policy (stricter fail-closed):
//   - Supported keywords are retained and re-emitted in canonical form.
//   - Annotation/extension keywords that never affect validation (title/description
//     retained when allowed; examples, $comment, readOnly, writeOnly, deprecated,
//     markdownDescription, x-*) are stripped during canonicalization so digests stay
//     stable. Examples must never carry secrets into manifests.
//   - JSON Schema `default` is NOT stripped: it is rejected as unsupported. Defaults
//     can embed secrets and must never be accepted into catalog freeze or verification.
//   - Validation-semantic keywords outside the allowlist are rejected (never silently
//     dropped), because removing them would change validation semantics.
//   - $ref is rejected entirely in v1 (external, remote, and local/recursive) unless
//     a future pinned OpenAI subset is explicitly and safely supported.
//   - Value types/domains for every allowed keyword are validated at every depth.
//
// Supported keywords:
//
//	type, properties, required, items, additionalProperties, enum, const,
//	minimum, maximum, exclusiveMinimum, exclusiveMaximum, multipleOf,
//	minLength, maxLength, pattern, minItems, maxItems, uniqueItems,
//	minProperties, maxProperties, description, title
//
// Stripped annotation keywords (no validation semantics change):
//
//	examples, example, $comment, readOnly, writeOnly, deprecated,
//	markdownDescription, x-* vendor extensions
//
// Explicitly rejected (not stripped):
//
//	default — secret-bearing risk; fail closed with ErrToolSchemaUnsupportedKeyword
var (
	openaiSchemaAllowedKeywords = map[string]struct{}{
		"type":                 {},
		"properties":           {},
		"required":             {},
		"items":                {},
		"additionalProperties": {},
		"enum":                 {},
		"const":                {},
		"minimum":              {},
		"maximum":              {},
		"exclusiveMinimum":     {},
		"exclusiveMaximum":     {},
		"multipleOf":           {},
		"minLength":            {},
		"maxLength":            {},
		"pattern":              {},
		"minItems":             {},
		"maxItems":             {},
		"uniqueItems":          {},
		"minProperties":        {},
		"maxProperties":        {},
		"description":          {},
		"title":                {},
	}
	openaiSchemaStripKeywords = map[string]struct{}{
		// "default" intentionally NOT stripped — see reject below / unsupported path.
		"examples":            {},
		"example":             {},
		"$comment":            {},
		"readOnly":            {},
		"writeOnly":           {},
		"deprecated":          {},
		"markdownDescription": {},
	}
)

// openaiSchemaAllowedTypes are the exact case-sensitive JSON Schema type strings
// accepted at every depth. Uppercase/nonsense (e.g. "Object", "STRING") reject.
var openaiSchemaAllowedTypes = map[string]struct{}{
	"object":  {},
	"array":   {},
	"string":  {},
	"number":  {},
	"integer": {},
	"boolean": {},
	"null":    {},
}

// canonicalizeAndValidateParametersSchema validates raw parameters JSON for OpenAI
// Agentic use and returns a deterministic canonical UTF-8 JSON object.
//
// Rejects: JSON null, non-object root, invalid/non-finite JSON, duplicate keys at
// every depth, external/$ref, unsupported validation keywords, wrong value types
// for allowed keywords, size/depth/property limits, invalid UTF-8.
// Raw byte cap is enforced on original bytes *before* TrimSpace / JSON parse /
// annotation stripping (whitespace padding counts toward the cap).
// Does not log or return schema body in errors beyond keyword names.
func canonicalizeAndValidateParametersSchema(raw json.RawMessage) (json.RawMessage, error) {
	// Raw byte cap on ORIGINAL bytes before TrimSpace (whitespace padding counts).
	if len(raw) > MaxToolSchemaBytes {
		return nil, ErrToolSchemaTooLarge
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	// JSON null is not an empty object — reject.
	if bytes.Equal(raw, []byte("null")) {
		return nil, ErrToolSchemaInvalidRoot
	}
	if !utf8.Valid(raw) {
		return nil, ErrToolSchemaInvalidUTF8
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid JSON", ErrModelToolCatalogInvalid)
	}
	if raw[0] != '{' {
		return nil, ErrToolSchemaInvalidRoot
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrToolSchemaDuplicateKey, err)
	}

	var root any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrModelToolCatalogInvalid, err)
	}
	obj, ok := root.(map[string]any)
	if !ok || obj == nil {
		return nil, ErrToolSchemaInvalidRoot
	}

	// Root type must be object or omitted (treated as object).
	if t, has := obj["type"]; has {
		if err := validateSchemaTypeKeyword(t, true); err != nil {
			return nil, err
		}
	}

	canonical, propCount, err := canonicalizeSchemaNode(obj, 1, 0)
	if err != nil {
		return nil, err
	}
	if propCount > MaxSchemaPropertyCount {
		return nil, ErrToolSchemaTooManyProperties
	}
	// Cross-field membership: required ⊆ properties keys when properties present.
	if err := validateRequiredMembership(canonical); err != nil {
		return nil, err
	}
	// Pinned adapter float64 projection: every JSON number (enum/const/keywords)
	// must survive sonic map[string]any round-trip without semantic change.
	// Fail closed before catalog digest/wire can diverge.
	if err := validatePinnedAdapterNumericProjection(canonical); err != nil {
		return nil, err
	}
	// Bound conflicts at this node and descendants are checked during canonicalize.
	out, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrModelToolCatalogInvalid, err)
	}
	if len(out) > MaxToolSchemaBytes {
		return nil, ErrToolSchemaTooLarge
	}
	if !utf8.Valid(out) {
		return nil, ErrToolSchemaInvalidUTF8
	}
	return json.RawMessage(out), nil
}

// ValidateOpenAIAgenticParametersSchema validates raw parameters JSON using the
// exact production OpenAI Agentic catalog-freeze policy (canonicalizeAndValidateParametersSchema).
//
// Callers that need only a pass/fail security check (e.g. Responses verification of
// tool_search_output function tools) should use this export rather than re-implementing
// catalog limits. Returns nil when the schema is accepted; on failure returns a
// MODEL_TOOL_CATALOG_INVALID-family error without embedding schema bodies or secrets.
// Callers map the error to their public class (e.g. ErrAgenticStreamInvalid).
// Canonical bytes are intentionally not returned to avoid accidental wire divergence
// from verification payload semantics.
func ValidateOpenAIAgenticParametersSchema(raw json.RawMessage) error {
	_, err := canonicalizeAndValidateParametersSchema(raw)
	return err
}

// validatePinnedAdapterNumericProjection walks the UseNumber-decoded schema tree
// and rejects any JSON number whose mathematical value changes under the pinned
// agenticopenai@v0.2.2 toFunctionTool path:
//
//	sonic.Marshal(schema) → sonic.Unmarshal into map[string]any (float64) → HTTP JSON
//
// Semantically equivalent lexical forms (1e2 vs 100) are allowed when the emitted
// float64 value equals the original. Overflow / NaN / Inf and precision loss
// (e.g. 9007199254740993 → 9007199254740992) fail closed.
// Does not embed schema body in errors.
func validatePinnedAdapterNumericProjection(v any) error {
	switch typed := v.(type) {
	case json.Number:
		return assertJSONNumberSurvivesFloat64Projection(typed)
	case map[string]any:
		for _, child := range typed {
			if err := validatePinnedAdapterNumericProjection(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validatePinnedAdapterNumericProjection(child); err != nil {
				return err
			}
		}
	case nil, string, bool:
		// non-numeric leaves
	default:
		// int/int64 should not appear on UseNumber path; reject unexpected float64.
		if _, ok := typed.(float64); ok {
			return fmt.Errorf("%w: float64 number path is forbidden", ErrToolSchemaNonFinite)
		}
	}
	return nil
}

// assertJSONNumberSurvivesFloat64Projection compares exact mathematical values
// before and after float64 projection using big.Rat (no silent float equality).
func assertJSONNumberSurvivesFloat64Projection(n json.Number) error {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return fmt.Errorf("%w: empty number", ErrToolSchemaInvalidValue)
	}
	orig, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("%w: invalid number token", ErrToolSchemaInvalidValue)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%w: number not finite for adapter wire", ErrToolSchemaNonFinite)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%w: non-finite adapter projection", ErrToolSchemaNonFinite)
	}
	// Re-encode as JSON number the same way map[string]any re-marshals float64.
	projectedBytes, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("%w: adapter projection marshal failed", ErrToolSchemaInvalidValue)
	}
	proj, ok := new(big.Rat).SetString(string(projectedBytes))
	if !ok {
		return fmt.Errorf("%w: adapter projection not a number", ErrToolSchemaInvalidValue)
	}
	if orig.Cmp(proj) != 0 {
		return ErrToolSchemaAdapterNumericUnrepresentable
	}
	return nil
}

// canonicalizeSchemaNode returns a canonical map and cumulative property count.
func canonicalizeSchemaNode(node map[string]any, depth, propCount int) (map[string]any, int, error) {
	if depth > MaxSchemaNestingDepth {
		return nil, propCount, ErrToolSchemaTooDeep
	}
	out := make(map[string]any, len(node))
	for key, val := range node {
		k := key
		if strings.HasPrefix(k, "x-") || strings.HasPrefix(k, "X-") {
			// Strip vendor extensions (annotation policy).
			continue
		}
		if k == "default" {
			// Never strip secret-bearing defaults into a valid schema; fail closed.
			// Error names the keyword only — never the default value/body.
			return nil, propCount, fmt.Errorf("%w: default", ErrToolSchemaUnsupportedKeyword)
		}
		if _, strip := openaiSchemaStripKeywords[k]; strip {
			continue
		}
		if k == "$ref" {
			return nil, propCount, rejectRef(val)
		}
		if k == "$defs" || k == "definitions" || k == "$schema" || k == "$id" || k == "$anchor" || k == "$dynamicRef" {
			return nil, propCount, fmt.Errorf("%w: %s", ErrToolSchemaUnsupportedKeyword, k)
		}
		if _, ok := openaiSchemaAllowedKeywords[k]; !ok {
			// Validation-semantic or unknown: reject (never silently drop).
			return nil, propCount, fmt.Errorf("%w: %s", ErrToolSchemaUnsupportedKeyword, k)
		}
		switch k {
		case "type":
			if err := validateSchemaTypeKeyword(val, false); err != nil {
				return nil, propCount, err
			}
			out[k] = val
		case "properties":
			props, ok := val.(map[string]any)
			if !ok {
				return nil, propCount, fmt.Errorf("%w: properties must be object", ErrToolSchemaInvalidValue)
			}
			canonProps := make(map[string]any, len(props))
			for pk, pv := range props {
				propCount++
				if propCount > MaxSchemaPropertyCount {
					return nil, propCount, ErrToolSchemaTooManyProperties
				}
				child, ok := pv.(map[string]any)
				if !ok {
					return nil, propCount, fmt.Errorf("%w: property schema must be object", ErrToolSchemaInvalidValue)
				}
				c, pc, err := canonicalizeSchemaNode(child, depth+1, propCount)
				if err != nil {
					return nil, propCount, err
				}
				propCount = pc
				// Nested required membership for this child.
				if err := validateRequiredMembership(c); err != nil {
					return nil, propCount, err
				}
				canonProps[pk] = c
			}
			out[k] = canonProps
		case "items":
			switch typed := val.(type) {
			case map[string]any:
				c, pc, err := canonicalizeSchemaNode(typed, depth+1, propCount)
				if err != nil {
					return nil, propCount, err
				}
				propCount = pc
				if err := validateRequiredMembership(c); err != nil {
					return nil, propCount, err
				}
				out[k] = c
			default:
				return nil, propCount, fmt.Errorf("%w: items must be object schema", ErrToolSchemaInvalidValue)
			}
		case "additionalProperties":
			switch typed := val.(type) {
			case bool:
				out[k] = typed
			case map[string]any:
				c, pc, err := canonicalizeSchemaNode(typed, depth+1, propCount)
				if err != nil {
					return nil, propCount, err
				}
				propCount = pc
				if err := validateRequiredMembership(c); err != nil {
					return nil, propCount, err
				}
				out[k] = c
			default:
				return nil, propCount, fmt.Errorf("%w: additionalProperties must be bool or object", ErrToolSchemaInvalidValue)
			}
		case "required":
			req, err := parseRequiredArray(val)
			if err != nil {
				return nil, propCount, err
			}
			out[k] = req
		case "enum":
			arr, ok := val.([]any)
			if !ok {
				return nil, propCount, fmt.Errorf("%w: enum must be array", ErrToolSchemaInvalidValue)
			}
			if len(arr) == 0 {
				return nil, propCount, fmt.Errorf("%w: enum must be nonempty", ErrToolSchemaInvalidValue)
			}
			// Each enum value is a JSON tree at the current schema depth (same limit
			// counter as nested property schemas). Nested objects/arrays inside a
			// value increment depth; deep value trees cannot bypass DoS limits.
			canonArr := make([]any, len(arr))
			for i, item := range arr {
				c, pc, err := canonicalizeJSONTreeNumbersAtDepth(item, depth, propCount)
				if err != nil {
					return nil, propCount, err
				}
				propCount = pc
				canonArr[i] = c
			}
			seenEnum := make(map[string]struct{}, len(canonArr))
			for _, item := range canonArr {
				key, err := enumValueCanonicalKey(item)
				if err != nil {
					return nil, propCount, fmt.Errorf("%w: enum value not serializable", ErrToolSchemaInvalidValue)
				}
				if _, dup := seenEnum[key]; dup {
					return nil, propCount, fmt.Errorf("%w: enum values must be unique", ErrToolSchemaInvalidValue)
				}
				seenEnum[key] = struct{}{}
			}
			out[k] = canonArr
		case "const":
			// const value tree shares the schema node's depth budget.
			canon, pc, err := canonicalizeJSONTreeNumbersAtDepth(val, depth, propCount)
			if err != nil {
				return nil, propCount, err
			}
			propCount = pc
			out[k] = canon
		case "description", "title":
			s, ok := val.(string)
			if !ok {
				return nil, propCount, fmt.Errorf("%w: %s must be string", ErrToolSchemaInvalidValue, k)
			}
			out[k] = s
		case "pattern":
			s, ok := val.(string)
			if !ok {
				return nil, propCount, fmt.Errorf("%w: pattern must be string", ErrToolSchemaInvalidValue)
			}
			// Compile/validate regex; reject invalid patterns fail-closed.
			if _, err := regexp.Compile(s); err != nil {
				return nil, propCount, fmt.Errorf("%w: pattern is not a valid regexp", ErrToolSchemaInvalidValue)
			}
			out[k] = s
		case "uniqueItems":
			b, ok := val.(bool)
			if !ok {
				return nil, propCount, fmt.Errorf("%w: uniqueItems must be boolean", ErrToolSchemaInvalidValue)
			}
			out[k] = b
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			n, err := parseNonNegativeInteger(val, k)
			if err != nil {
				return nil, propCount, err
			}
			// Emit as json.Number for exact integer spelling (no float64).
			out[k] = json.Number(fmt.Sprintf("%d", n))
		case "minimum", "maximum":
			num, err := parseFiniteJSONNumber(val, k)
			if err != nil {
				return nil, propCount, err
			}
			out[k] = num
		case "exclusiveMinimum", "exclusiveMaximum":
			// Accept boolean (draft-04) or number (draft-06+).
			switch typed := val.(type) {
			case bool:
				out[k] = typed
			default:
				num, err := parseFiniteJSONNumber(val, k)
				if err != nil {
					return nil, propCount, fmt.Errorf("%w: %s must be boolean or number", ErrToolSchemaInvalidValue, k)
				}
				out[k] = num
			}
		case "multipleOf":
			num, err := parseFiniteJSONNumber(val, k)
			if err != nil {
				return nil, propCount, err
			}
			r, err := jsonNumberToRat(num)
			if err != nil {
				return nil, propCount, fmt.Errorf("%w: multipleOf must be finite number", ErrToolSchemaInvalidValue)
			}
			if r.Sign() <= 0 {
				return nil, propCount, fmt.Errorf("%w: multipleOf must be > 0", ErrToolSchemaInvalidValue)
			}
			out[k] = num
		default:
			canon, pc, err := canonicalizeJSONTreeNumbersAtDepth(val, depth, propCount)
			if err != nil {
				return nil, propCount, err
			}
			propCount = pc
			out[k] = canon
		}
	}
	if err := validateBoundConflicts(out); err != nil {
		return nil, propCount, err
	}
	return out, propCount, nil
}

func validateSchemaTypeKeyword(val any, rootObjectOnly bool) error {
	switch typed := val.(type) {
	case string:
		// Exact case-sensitive match; no trim/normalize of type strings.
		if typed == "" {
			return fmt.Errorf("%w: type must be nonempty string", ErrToolSchemaInvalidValue)
		}
		if rootObjectOnly {
			// Root must be exact "object" (uppercase/nonsense fail as invalid root).
			if typed != "object" {
				return fmt.Errorf("%w: root type must be object", ErrToolSchemaInvalidRoot)
			}
			return nil
		}
		if _, ok := openaiSchemaAllowedTypes[typed]; !ok {
			return fmt.Errorf("%w: unsupported type %q", ErrToolSchemaInvalidValue, typed)
		}
		return nil
	case []any:
		if rootObjectOnly {
			return fmt.Errorf("%w: root type must be object string", ErrToolSchemaInvalidRoot)
		}
		if len(typed) == 0 {
			return fmt.Errorf("%w: type array must be nonempty", ErrToolSchemaInvalidValue)
		}
		seen := make(map[string]struct{}, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok || s == "" {
				return fmt.Errorf("%w: type array items must be nonempty strings", ErrToolSchemaInvalidValue)
			}
			if _, ok := openaiSchemaAllowedTypes[s]; !ok {
				return fmt.Errorf("%w: unsupported type %q", ErrToolSchemaInvalidValue, s)
			}
			if _, dup := seen[s]; dup {
				return fmt.Errorf("%w: duplicate type value", ErrToolSchemaInvalidValue)
			}
			seen[s] = struct{}{}
		}
		return nil
	default:
		// Reject type:7, type:true, type:null, etc.
		return fmt.Errorf("%w: type must be string or string array", ErrToolSchemaInvalidValue)
	}
}

func parseRequiredArray(val any) ([]string, error) {
	arr, ok := val.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: required must be array", ErrToolSchemaInvalidValue)
	}
	req := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: required items must be strings", ErrToolSchemaInvalidValue)
		}
		if _, dup := seen[s]; dup {
			return nil, fmt.Errorf("%w: required items must be unique", ErrToolSchemaInvalidValue)
		}
		seen[s] = struct{}{}
		req = append(req, s)
	}
	return req, nil
}

func validateRequiredMembership(node map[string]any) error {
	reqRaw, hasReq := node["required"]
	if !hasReq {
		return nil
	}
	req, ok := reqRaw.([]string)
	if !ok {
		// Should already be []string from canonicalize; defensive.
		return fmt.Errorf("%w: required must be string array", ErrToolSchemaInvalidValue)
	}
	propsRaw, hasProps := node["properties"]
	if !hasProps {
		// required without properties: membership cannot be checked against keys;
		// allow empty required only, or reject non-empty (fail closed).
		if len(req) > 0 {
			return fmt.Errorf("%w: required entries without properties", ErrToolSchemaInvalidValue)
		}
		return nil
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: properties must be object", ErrToolSchemaInvalidValue)
	}
	for _, name := range req {
		if _, ok := props[name]; !ok {
			return fmt.Errorf("%w: required %q not in properties", ErrToolSchemaInvalidValue, name)
		}
	}
	return nil
}

// Platform caps for JSON number decode/canonicalization (CPU safety).
// Digits beyond this (excluding sign/dot/exponent marker) and absolute exponents
// beyond the cap are rejected fail-closed — never rounded via float64.
const (
	maxJSONNumberSignificantDigits = 100
	maxJSONNumberAbsExponent       = 10000
)

func parseNonNegativeInteger(val any, key string) (int64, error) {
	switch typed := val.(type) {
	case json.Number:
		// Mathematical integer via big.Rat (accepts 1, 1.0, 1e0); reject fractional.
		r, err := parseJSONNumberToRat(typed.String())
		if err != nil {
			return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
		}
		if !r.IsInt() || r.Sign() < 0 {
			return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
		}
		// Fit in int64 for length/count keywords.
		if !r.Num().IsInt64() {
			return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
		}
		return r.Num().Int64(), nil
	case int:
		if typed < 0 {
			return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
		}
		return int64(typed), nil
	case int64:
		if typed < 0 {
			return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
		}
		return typed, nil
	default:
		// Reject float64, string "1", bool, object — decode uses UseNumber only.
		return 0, fmt.Errorf("%w: %s must be nonnegative integer", ErrToolSchemaInvalidValue, key)
	}
}

// parseFiniteJSONNumber validates and returns a deterministic exact JSON number
// without float64 conversion. Equivalent forms (1 / 1.0 / 1e0) canonicalize to
// one exact representation; 9007199254740993 stays exact.
func parseFiniteJSONNumber(val any, key string) (json.Number, error) {
	switch typed := val.(type) {
	case json.Number:
		canon, _, err := canonicalizeJSONNumberToken(typed.String())
		if err != nil {
			return "", fmt.Errorf("%w: %s must be finite number", ErrToolSchemaInvalidValue, key)
		}
		return canon, nil
	case int:
		return json.Number(fmt.Sprintf("%d", typed)), nil
	case int64:
		return json.Number(fmt.Sprintf("%d", typed)), nil
	default:
		// No float64 path — UseNumber decode only.
		return "", fmt.Errorf("%w: %s must be number", ErrToolSchemaInvalidValue, key)
	}
}

// canonicalizeJSONNumberToken parses s with big.Rat under digit/exponent caps and
// emits a deterministic exact JSON number spelling. Negative zero → "0".
// Never rounds; never uses float64.
func canonicalizeJSONNumberToken(s string) (json.Number, *big.Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil, fmt.Errorf("empty number")
	}
	if err := validateJSONNumberCPUCaps(s); err != nil {
		return "", nil, err
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", nil, fmt.Errorf("invalid number")
	}
	// Negative zero and plain zero both emit "0".
	if r.Sign() == 0 {
		r.SetInt64(0)
		return json.Number("0"), r, nil
	}
	if r.IsInt() {
		// Exact integer form (no decimal/exponent); preserves values beyond float64.
		return json.Number(r.Num().String()), r, nil
	}
	// Non-integer: emit shortest exact decimal when denominator is a power of 10
	// after reduction; otherwise use FloatString with enough digits from the
	// original significant length (never float64 rounding of the value).
	canon, err := ratToExactJSONNumber(r)
	if err != nil {
		return "", nil, err
	}
	return canon, r, nil
}

// validateJSONNumberCPUCaps rejects absurd digit/exponent lengths before Rat work.
func validateJSONNumberCPUCaps(s string) error {
	// Count significant digits (0-9) and parse exponent magnitude if present.
	digits := 0
	expAbs := 0
	sawExp := false
	expSign := 1
	expDigits := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if sawExp {
				expDigits = expDigits*10 + int(c-'0')
				if expDigits > maxJSONNumberAbsExponent {
					return fmt.Errorf("exponent exceeds cap")
				}
			} else {
				digits++
				if digits > maxJSONNumberSignificantDigits {
					return fmt.Errorf("digit count exceeds cap")
				}
			}
		case 'e', 'E':
			if sawExp {
				return fmt.Errorf("invalid number")
			}
			sawExp = true
			// Look ahead for optional sign.
			if i+1 < len(s) && (s[i+1] == '+' || s[i+1] == '-') {
				if s[i+1] == '-' {
					expSign = -1
				}
				i++
			}
		case '+', '-', '.':
			// Allowed in number syntax; Rat.SetString validates structure.
		default:
			return fmt.Errorf("invalid number character")
		}
	}
	if sawExp {
		expAbs = expDigits
		if expSign < 0 {
			// magnitude already absolute
		}
		if expAbs > maxJSONNumberAbsExponent {
			return fmt.Errorf("exponent exceeds cap")
		}
	}
	if digits == 0 {
		return fmt.Errorf("no digits")
	}
	return nil
}

// ratToExactJSONNumber emits an exact decimal JSON number for a non-integer Rat.
// Uses big.Float with high precision derived from numerator/denominator bit lengths
// so mathematical value is preserved without float64.
func ratToExactJSONNumber(r *big.Rat) (json.Number, error) {
	if r == nil {
		return "", fmt.Errorf("nil rat")
	}
	// Prefer exact decimal when the reduced denominator divides a power of 10.
	// Otherwise fall back to FloatString with digit budget from bit length.
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())
	// Factor den into 2^a * 5^b * other.
	two, five := big.NewInt(2), big.NewInt(5)
	a, b := 0, 0
	tmp := new(big.Int)
	for {
		tmp.Rem(den, two)
		if tmp.Sign() != 0 {
			break
		}
		den.Quo(den, two)
		a++
	}
	for {
		tmp.Rem(den, five)
		if tmp.Sign() != 0 {
			break
		}
		den.Quo(den, five)
		b++
	}
	if den.Cmp(big.NewInt(1)) == 0 {
		// Exact terminating decimal. Scale to 10^max(a,b).
		scale := a
		if b > a {
			scale = b
		}
		// num * 2^(scale-a) * 5^(scale-b) / 10^scale
		scaled := new(big.Int).Set(num)
		for i := 0; i < scale-a; i++ {
			scaled.Mul(scaled, two)
		}
		for i := 0; i < scale-b; i++ {
			scaled.Mul(scaled, five)
		}
		neg := scaled.Sign() < 0
		if neg {
			scaled.Abs(scaled)
		}
		digits := scaled.String()
		if scale == 0 {
			if neg {
				return json.Number("-" + digits), nil
			}
			return json.Number(digits), nil
		}
		// Pad left with zeros if needed.
		for len(digits) <= scale {
			digits = "0" + digits
		}
		intPart := digits[:len(digits)-scale]
		fracPart := digits[len(digits)-scale:]
		// Trim trailing zeros in fraction (deterministic shortest).
		fracPart = strings.TrimRight(fracPart, "0")
		var out string
		if fracPart == "" {
			out = intPart
		} else {
			out = intPart + "." + fracPart
		}
		if neg {
			out = "-" + out
		}
		return json.Number(out), nil
	}
	// Non-terminating in base 10: emit with enough precision that round-trip via
	// Rat preserves value for schema keywords (bounded by digit cap).
	prec := maxJSONNumberSignificantDigits
	s := r.FloatString(prec)
	// Trim trailing zeros after decimal for determinism.
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-" {
		s = "0"
	}
	// Verify round-trip under Rat (fail closed if precision insufficient).
	back, ok := new(big.Rat).SetString(s)
	if !ok || back.Cmp(r) != 0 {
		// Keep full FloatString without trim as last resort exact-enough form.
		s = r.FloatString(prec)
	}
	return json.Number(s), nil
}

func parseJSONNumberToRat(s string) (*big.Rat, error) {
	_, r, err := canonicalizeJSONNumberToken(s)
	return r, err
}

func jsonNumberToRat(n json.Number) (*big.Rat, error) {
	return parseJSONNumberToRat(n.String())
}

// canonicalizeJSONTreeNumbers walks arrays/objects and rewrites every JSON number
// to a deterministic exact json.Number (no float64). Used for enum/const trees
// without depth tracking (tests/helpers). Production schema path uses
// canonicalizeJSONTreeNumbersAtDepth.
func canonicalizeJSONTreeNumbers(v any) (any, error) {
	out, _, err := canonicalizeJSONTreeNumbersAtDepth(v, 1, 0)
	return out, err
}

// canonicalizeJSONTreeNumbersAtDepth walks enum/const value trees applying the
// same MaxSchemaNestingDepth / MaxSchemaPropertyCount DoS limits as schema nodes.
// depth is the nesting depth of this value relative to the catalog root (same
// counter as canonicalizeSchemaNode). Depth is checked only on object/array
// containers (scalars at the boundary are fine, matching property-schema depth).
// Nested objects/arrays increment depth; each object key counts toward propCount.
func canonicalizeJSONTreeNumbersAtDepth(v any, depth, propCount int) (any, int, error) {
	switch t := v.(type) {
	case nil:
		return nil, propCount, nil
	case bool, string:
		return t, propCount, nil
	case json.Number:
		canon, _, err := canonicalizeJSONNumberToken(t.String())
		if err != nil {
			return nil, propCount, ErrToolSchemaNonFinite
		}
		return canon, propCount, nil
	case int:
		return json.Number(fmt.Sprintf("%d", t)), propCount, nil
	case int64:
		return json.Number(fmt.Sprintf("%d", t)), propCount, nil
	case map[string]any:
		if depth > MaxSchemaNestingDepth {
			return nil, propCount, ErrToolSchemaTooDeep
		}
		out := make(map[string]any, len(t))
		for k, child := range t {
			propCount++
			if propCount > MaxSchemaPropertyCount {
				return nil, propCount, ErrToolSchemaTooManyProperties
			}
			c, pc, err := canonicalizeJSONTreeNumbersAtDepth(child, depth+1, propCount)
			if err != nil {
				return nil, propCount, err
			}
			propCount = pc
			out[k] = c
		}
		return out, propCount, nil
	case []any:
		if depth > MaxSchemaNestingDepth {
			return nil, propCount, ErrToolSchemaTooDeep
		}
		out := make([]any, len(t))
		for i, child := range t {
			c, pc, err := canonicalizeJSONTreeNumbersAtDepth(child, depth+1, propCount)
			if err != nil {
				return nil, propCount, err
			}
			propCount = pc
			out[i] = c
		}
		return out, propCount, nil
	default:
		// Reject float64 and other unexpected types (UseNumber decode path).
		if _, ok := v.(float64); ok {
			return nil, propCount, fmt.Errorf("%w: float64 number path is forbidden", ErrToolSchemaNonFinite)
		}
		return nil, propCount, fmt.Errorf("%w: unsupported JSON value type %T", ErrToolSchemaInvalidValue, v)
	}
}

func validateBoundConflicts(node map[string]any) error {
	// Integer bound pairs (stored as json.Number after canonicalize).
	pairs := [][2]string{
		{"minLength", "maxLength"},
		{"minItems", "maxItems"},
		{"minProperties", "maxProperties"},
	}
	for _, p := range pairs {
		minV, hasMin := node[p[0]]
		maxV, hasMax := node[p[1]]
		if !hasMin || !hasMax {
			continue
		}
		minN, ok1 := asInt64(minV)
		maxN, ok2 := asInt64(maxV)
		if ok1 && ok2 && minN > maxN {
			return fmt.Errorf("%w: %s > %s", ErrToolSchemaInvalidValue, p[0], p[1])
		}
	}
	// Numeric minimum/maximum via exact big.Rat comparison (no float64).
	if minV, hasMin := node["minimum"]; hasMin {
		if maxV, hasMax := node["maximum"]; hasMax {
			minR, ok1 := asRat(minV)
			maxR, ok2 := asRat(maxV)
			if ok1 && ok2 && minR.Cmp(maxR) > 0 {
				return fmt.Errorf("%w: minimum > maximum", ErrToolSchemaInvalidValue)
			}
		}
	}
	// Coherent const/enum vs type when both present and type is a single string.
	if err := validateConstEnumTypeCoherence(node); err != nil {
		return err
	}
	return nil
}

// validateConstEnumTypeCoherence rejects type mismatches for const/enum when
// type is a single string or a union type array. Every enum/const value must
// conform to at least one allowed type (e.g. boolean rejects for [string,integer]).
func validateConstEnumTypeCoherence(node map[string]any) error {
	typeRaw, hasType := node["type"]
	if !hasType {
		return nil
	}
	allowed, err := schemaTypeList(typeRaw)
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		return nil
	}
	check := func(val any, label string) error {
		for _, typ := range allowed {
			if jsonValueMatchesSchemaType(val, typ) {
				return nil
			}
		}
		return fmt.Errorf("%w: %s does not match any allowed type", ErrToolSchemaInvalidValue, label)
	}
	if c, has := node["const"]; has {
		if err := check(c, "const"); err != nil {
			return err
		}
	}
	if e, has := node["enum"]; has {
		arr, ok := e.([]any)
		if !ok {
			return fmt.Errorf("%w: enum must be array", ErrToolSchemaInvalidValue)
		}
		for _, item := range arr {
			if err := check(item, "enum value"); err != nil {
				return err
			}
		}
	}
	return nil
}

// schemaTypeList extracts allowed type strings from a type keyword (string or array).
func schemaTypeList(typeRaw any) ([]string, error) {
	switch t := typeRaw.(type) {
	case string:
		if t == "" {
			return nil, fmt.Errorf("%w: type must be nonempty", ErrToolSchemaInvalidValue)
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("%w: type array items must be strings", ErrToolSchemaInvalidValue)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: type must be string or array", ErrToolSchemaInvalidValue)
	}
}

// enumValueCanonicalKey returns a deterministic uniqueness key for an enum value.
// Numeric values use lossless big.Rat comparison so JSON 1, 1.0, and 1e0 collide;
// -0 and 0 are the same. Nested arrays/objects recursively canonicalize mathematical
// JSON numbers while preserving object key ordering and array order semantics
// (no float precision loss).
func enumValueCanonicalKey(item any) (string, error) {
	return enumValueCanonicalKeyRec(item)
}

func enumValueCanonicalKeyRec(item any) (string, error) {
	switch v := item.(type) {
	case nil:
		return "null", nil
	case bool:
		if v {
			return "b:true", nil
		}
		return "b:false", nil
	case string:
		return "s:" + v, nil
	case json.Number:
		return numericEnumKey(v.String())
	case int:
		return "n:" + new(big.Rat).SetInt64(int64(v)).RatString(), nil
	case int64:
		return "n:" + new(big.Rat).SetInt64(v).RatString(), nil
	case map[string]any:
		// Preserve object key ordering by sorting keys for uniqueness only;
		// nested number values are recursively Rat-canonicalized.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		// Stable uniqueness key uses sorted keys; object order in the original
		// enum value is preserved in the schema emission (out[k]=arr as-is).
		sortStrings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return "", err
			}
			vk, err := enumValueCanonicalKeyRec(v[k])
			if err != nil {
				return "", err
			}
			b.Write(kb)
			b.WriteByte(':')
			b.WriteString(vk)
		}
		b.WriteString("}")
		return "o:" + b.String(), nil
	case []any:
		// Array order is significant for uniqueness.
		var b strings.Builder
		b.WriteString("[")
		for i, el := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			ek, err := enumValueCanonicalKeyRec(el)
			if err != nil {
				return "", err
			}
			b.WriteString(ek)
		}
		b.WriteString("]")
		return "a:" + b.String(), nil
	default:
		// Fallback: canonical JSON.
		cb, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return "j:" + string(cb), nil
	}
}

// sortStrings is a tiny local sort to avoid importing sort solely for enum keys
// when sort is already used elsewhere in the package... (keep explicit).
func sortStrings(keys []string) {
	// Insertion sort is fine for small enum object key sets.
	for i := 1; i < len(keys); i++ {
		j := i
		for j > 0 && keys[j-1] > keys[j] {
			keys[j-1], keys[j] = keys[j], keys[j-1]
			j--
		}
	}
}

func numericEnumKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty number")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", fmt.Errorf("invalid number")
	}
	// -0 and 0 are the same numeric value under uniqueness policy.
	if r.Sign() == 0 {
		r.SetInt64(0)
	}
	return "n:" + r.RatString(), nil
}

// jsonValueIsMathematicalInteger reports whether val is a mathematical integer
// (1, 1.0, 1e0, -0.0) via big.Rat.IsInt. Fractional numbers reject. No float64.
func jsonValueIsMathematicalInteger(val any) bool {
	switch t := val.(type) {
	case json.Number:
		r, err := parseJSONNumberToRat(t.String())
		return err == nil && r.IsInt()
	case int, int64:
		return true
	default:
		return false
	}
}

func jsonValueMatchesSchemaType(val any, typeStr string) bool {
	switch typeStr {
	case "null":
		return val == nil
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "string":
		_, ok := val.(string)
		return ok
	case "integer":
		// Accept any mathematical integer representation (1, 1.0, 1e0, -0.0)
		// via big.Rat.IsInt; reject fractional numbers.
		return jsonValueIsMathematicalInteger(val)
	case "number":
		switch t := val.(type) {
		case json.Number:
			_, err := parseJSONNumberToRat(t.String())
			return err == nil
		case int, int64:
			return true
		default:
			return false
		}
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "array":
		_, ok := val.([]any)
		return ok
	default:
		return false
	}
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		r, err := parseJSONNumberToRat(t.String())
		if err != nil || !r.IsInt() || !r.Num().IsInt64() {
			return 0, false
		}
		return r.Num().Int64(), true
	}
	return 0, false
}

// asRat converts a stored schema numeric (json.Number / int) to *big.Rat.
// No float64 conversion.
func asRat(v any) (*big.Rat, bool) {
	switch t := v.(type) {
	case json.Number:
		r, err := parseJSONNumberToRat(t.String())
		return r, err == nil
	case int:
		return new(big.Rat).SetInt64(int64(t)), true
	case int64:
		return new(big.Rat).SetInt64(t), true
	}
	return nil, false
}

func rejectRef(val any) error {
	s, ok := val.(string)
	if !ok {
		return ErrToolSchemaUnsafeRef
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ErrToolSchemaUnsafeRef
	}
	// Any $ref is rejected in v1 (including #/ local refs).
	if strings.Contains(s, "://") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "http") {
		return ErrToolSchemaExternalRef
	}
	return ErrToolSchemaUnsafeRef
}

func validateFiniteJSONValues(values []any) error {
	for _, v := range values {
		if err := validateFiniteJSONValue(v); err != nil {
			return err
		}
	}
	return nil
}

func validateFiniteJSONValue(v any) error {
	switch typed := v.(type) {
	case json.Number:
		if _, err := parseJSONNumberToRat(typed.String()); err != nil {
			return ErrToolSchemaNonFinite
		}
	case float64:
		// Forbidden path: UseNumber decode only.
		return ErrToolSchemaNonFinite
	case map[string]any:
		for _, child := range typed {
			if err := validateFiniteJSONValue(child); err != nil {
				return err
			}
		}
	case []any:
		return validateFiniteJSONValues(typed)
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return rejectDupValue(dec)
}

func rejectDupValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("object key must be a string")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDupValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != '}' {
			return errors.New("expected end of object")
		}
		return nil
	case '[':
		for dec.More() {
			if err := rejectDupValue(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != ']' {
			return errors.New("expected end of array")
		}
		return nil
	default:
		return fmt.Errorf("unexpected delimiter %v", delim)
	}
}

// validateToolDescriptionLimits enforces description byte limit (UTF-8).
func validateToolDescriptionLimits(desc string) error {
	if !utf8.ValidString(desc) {
		return ErrToolSchemaInvalidUTF8
	}
	if len(desc) > MaxToolDescriptionBytes {
		return ErrToolDescriptionTooLarge
	}
	return nil
}

// validateDeferredCatalogBudgets enforces deferred metadata total bytes and count.
// Never logs schema/description content.
func validateDeferredCatalogBudgets(entries []ToolCatalogEntry) error {
	var deferredCount int
	var metaBytes int
	for _, e := range entries {
		if e.Exposure != ToolExposureDeferred {
			continue
		}
		deferredCount++
		if deferredCount > MaxCatalogDeferredToolCount {
			return ErrToolCatalogDeferredTooMany
		}
		// name + description only (no parameters) for initial visible metadata budget.
		metaBytes += len(e.Name) + len(e.Description)
		if metaBytes > MaxCatalogDeferredMetadataBytes {
			return ErrToolCatalogDeferredTooLarge
		}
	}
	return nil
}
