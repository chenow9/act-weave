package tooltranslator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// emptyObjectSchema is the default parameter schema when InputSchema is
// missing, null, empty object {}, or a JSON Schema boolean (true/false).
// Include properties so OpenAI-compatible tool parameters always decode as
// an object (never a boolean root).
var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// allowlisted keys that may be read from a broader capability-like map/JSON.
var allowlistedSourceKeys = map[string]struct{}{
	"callablename":        {},
	"callabledescription": {},
	"inputschema":         {},
	// Common aliases used by callers that already hold ToolInfo-shaped data.
	"name":        {},
	"description": {},
	"desc":        {},
}

// sensitiveSourceKeys must never be copied into ToolInfo (Name/Desc/params/Extra).
// Checked case-insensitively against top-level keys of expanded source objects.
var sensitiveSourceKeys = map[string]struct{}{
	"connectionid":         {},
	"connection_id":        {},
	"credentialsecretid":   {},
	"credential_secret_id": {},
	"secretid":             {},
	"secret_id":            {},
	"secret":               {},
	"secrets":              {},
	"rawcredentials":       {},
	"raw_credentials":      {},
	"bearertoken":          {},
	"bearer_token":         {},
	"accesstoken":          {},
	"access_token":         {},
	"apikey":               {},
	"api_key":              {},
	"authorization":        {},
	"egress":               {},
	"egresshosts":          {},
	"egress_hosts":         {},
	"allowedhosts":         {},
	"allowed_hosts":        {},
	"provider":             {},
	"providerconfig":       {},
	"provider_config":      {},
}

// Capability is the pure, LLM-facing capability surface used to build
// schema.ToolInfo. It deliberately excludes connection IDs, secret refs,
// egress hosts, and credentials.
//
// Construct with NewCapability / ExtractCapability rather than copying a
// full release/binding record.
type Capability struct {
	// CallableName is the tool name exposed to the model (unique per run).
	CallableName string
	// CallableDescription tells the model when/why to call the tool.
	CallableDescription string
	// InputSchema is a JSON Schema document for tool parameters.
	// Nil or empty is treated as a no-args object schema.
	InputSchema json.RawMessage
}

// NewCapability builds a Capability from explicit allowlisted fields only.
func NewCapability(callableName, callableDescription string, inputSchema json.RawMessage) Capability {
	return Capability{
		CallableName:        strings.TrimSpace(callableName),
		CallableDescription: strings.TrimSpace(callableDescription),
		InputSchema:         cloneRaw(inputSchema),
	}
}

// ExtractCapability copies only allowlisted LLM-facing fields from a broader
// capability-like value (struct, map, or JSON object). Denylisted keys
// (connection secrets, egress, credentials, provider) are discarded even if
// present on the source.
func ExtractCapability(source any) (Capability, error) {
	if source == nil {
		return Capability{}, fmt.Errorf("capability source is nil")
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return Capability{}, fmt.Errorf("encode capability source: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Capability{}, fmt.Errorf("capability source is empty")
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return Capability{}, fmt.Errorf("capability source must be a JSON object: %w", err)
	}

	var out Capability
	for key, value := range object {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, sensitive := sensitiveSourceKeys[normalized]; sensitive {
			// Explicit drop — never assign into Capability.
			continue
		}
		if _, ok := allowlistedSourceKeys[normalized]; !ok {
			// Unknown keys are ignored (fail-closed allowlist).
			continue
		}
		switch normalized {
		case "callablename", "name":
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return Capability{}, fmt.Errorf("field %q: %w", key, err)
			}
			out.CallableName = strings.TrimSpace(s)
		case "callabledescription", "description", "desc":
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return Capability{}, fmt.Errorf("field %q: %w", key, err)
			}
			out.CallableDescription = strings.TrimSpace(s)
		case "inputschema":
			if len(value) == 0 || string(value) == "null" {
				out.InputSchema = nil
				continue
			}
			// Keep raw schema bytes; validated later in ToToolInfo.
			out.InputSchema = cloneRaw(value)
		}
	}
	return out, nil
}

// ToToolInfo converts one capability into an Eino schema.ToolInfo.
//
// InputSchema is parsed via jsonschema.Schema and attached with
// schema.NewParamsOneOfByJSONSchema. Nil/empty schema yields a no-args
// object schema.
// Invalid JSON Schema documents return a clear error.
//
// ToolInfo never carries Extra maps; sensitive platform fields cannot
// leak through that channel.
func ToToolInfo(cap Capability) (*schema.ToolInfo, error) {
	name := strings.TrimSpace(cap.CallableName)
	if name == "" {
		return nil, fmt.Errorf("callable name is required")
	}

	params, err := paramsOneOfFromSchema(cap.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("tool %q input schema: %w", name, err)
	}

	return &schema.ToolInfo{
		Name:        name,
		Desc:        strings.TrimSpace(cap.CallableDescription),
		ParamsOneOf: params,
		// Extra intentionally left nil — never attach platform metadata.
	}, nil
}

// BuildModelTools converts a run-snapshot capability list into Eino
// schema.ToolInfo values for model.WithTools.
//
// Naming and empty-list behaviour:
// order is preserved, empty input returns nil.
func BuildModelTools(caps []Capability) ([]*schema.ToolInfo, error) {
	if len(caps) == 0 {
		return nil, nil
	}
	tools := make([]*schema.ToolInfo, 0, len(caps))
	for i, cap := range caps {
		tool, err := ToToolInfo(cap)
		if err != nil {
			return nil, fmt.Errorf("capability at index %d: %w", i, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// BuildModelToolsFromSources is a convenience for run snapshots that still
// hold broader capability records. Each source is filtered through
// ExtractCapability before translation.
func BuildModelToolsFromSources(sources []any) ([]*schema.ToolInfo, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	caps := make([]Capability, 0, len(sources))
	for i, source := range sources {
		cap, err := ExtractCapability(source)
		if err != nil {
			return nil, fmt.Errorf("capability at index %d: %w", i, err)
		}
		caps = append(caps, cap)
	}
	return BuildModelTools(caps)
}

func paramsOneOfFromSchema(raw json.RawMessage) (*schema.ParamsOneOf, error) {
	trimmed := normalizeToolParameterSchema(raw)

	// Fast reject of clearly non-JSON before handing to jsonschema.
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("invalid JSON: %s", truncateForErr(trimmed))
	}

	js := &jsonschema.Schema{}
	if err := json.Unmarshal(trimmed, js); err != nil {
		return nil, fmt.Errorf("decode JSON Schema: %w", err)
	}
	// Empty or type-less schemas can re-serialize as JSON Schema boolean true
	// via some library paths; force a concrete object schema for LLM tool use.
	if isVacuousJSONSchema(js) {
		if err := json.Unmarshal(emptyObjectSchema, js); err != nil {
			return nil, fmt.Errorf("decode empty object schema: %w", err)
		}
	}
	return schema.NewParamsOneOfByJSONSchema(js), nil
}

func isVacuousJSONSchema(js *jsonschema.Schema) bool {
	if js == nil {
		return true
	}
	if strings.TrimSpace(js.Type) != "" {
		return false
	}
	if js.Ref != "" {
		return false
	}
	if js.Properties != nil && js.Properties.Len() > 0 {
		return false
	}
	if len(js.AnyOf) > 0 || len(js.OneOf) > 0 || len(js.AllOf) > 0 {
		return false
	}
	return true
}

// normalizeToolParameterSchema coerces missing/empty/boolean JSON Schema roots
// to a standard object schema. WORKFLOW releases may store input_schema as {}.
func normalizeToolParameterSchema(raw json.RawMessage) json.RawMessage {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return emptyObjectSchema
	}
	var probe any
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return trimmed
	}
	switch v := probe.(type) {
	case bool:
		// JSON Schema boolean form (true/false) is not OpenAI tool-parameters.
		return emptyObjectSchema
	case map[string]any:
		if len(v) == 0 {
			return emptyObjectSchema
		}
		// Ensure type=object when properties-only or empty-ish maps.
		if _, hasType := v["type"]; !hasType {
			if _, hasProps := v["properties"]; hasProps || len(v) == 0 {
				v["type"] = "object"
			}
			if props, ok := v["properties"]; !ok || props == nil {
				if t, _ := v["type"].(string); t == "object" || t == "" {
					v["properties"] = map[string]any{}
					if t == "" {
						v["type"] = "object"
					}
				}
			}
			encoded, err := json.Marshal(v)
			if err == nil {
				return encoded
			}
		}
		return trimmed
	default:
		// Non-object roots (string/array/number) are invalid tool parameter schemas.
		return emptyObjectSchema
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func truncateForErr(raw json.RawMessage) string {
	const max = 64
	s := string(raw)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
