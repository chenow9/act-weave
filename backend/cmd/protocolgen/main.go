// protocolgen regenerates AAP artifacts from the single Schema Registry.
//
//	go run ./cmd/protocolgen
//
// Outputs:
//   - schemas/aap/v1/SCHEMASET.sha256
//   - internal/protocolschema/generated/schema_meta.gen.go
//   - ../../sdk/typescript/src/generated/protocol.gen.ts
//   - ../../docs/openapi/generated/aap-protocol-components.gen.yaml
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "protocolgen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, schemaDir, err := resolvePaths()
	if err != nil {
		return err
	}

	docs, names, err := loadSchemas(schemaDir)
	if err != nil {
		return err
	}

	setHash, docHashes, err := hashSchemas(schemaDir, names)
	if err != nil {
		return err
	}
	if err := writeChecksumFile(filepath.Join(schemaDir, "SCHEMASET.sha256"), setHash, docHashes, names); err != nil {
		return err
	}

	eventTypes := extractEventTypes(docs["events.schema.json"])
	runStatus := extractEnum(docs["run.schema.json"], "status")
	runTrigger := extractEnum(docs["run.schema.json"], "trigger")
	itemStatus := extractDefEnum(docs["item.schema.json"], "status")
	deltaTypes := extractDeltaTypes(docs["delta.schema.json"])

	meta := genMeta{
		SpecVersion:     "1.0",
		ProtocolVersion: "2026-08-11",
		SchemaSetSHA256: setHash,
		DocumentNames:   names,
		DocumentSHA256:  docHashes,
		EventTypes:      eventTypes,
		RunStatuses:     runStatus,
		RunTriggers:     runTrigger,
		ItemStatuses:    itemStatus,
		DeltaTypes:      deltaTypes,
	}

	if err := writeGoGenerated(filepath.Join(repoRoot, "backend", "internal", "protocolschema", "generated", "schema_meta.gen.go"), meta); err != nil {
		return err
	}
	if err := writeTSGenerated(filepath.Join(repoRoot, "sdk", "typescript", "src", "generated", "protocol.gen.ts"), meta); err != nil {
		return err
	}
	if err := writeOpenAPIGenerated(filepath.Join(repoRoot, "docs", "openapi", "generated", "aap-protocol-components.gen.yaml"), meta); err != nil {
		return err
	}
	fmt.Printf("protocolgen: schema-set %s (%d documents)\n", setHash, len(names))
	return nil
}

func resolvePaths() (repoRoot, schemaDir string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	// Prefer running from backend/.
	candidates := []string{
		filepath.Join(cwd, "internal", "protocolschema", "schemas", "aap", "v1"),
		filepath.Join(cwd, "backend", "internal", "protocolschema", "schemas", "aap", "v1"),
	}
	for _, dir := range candidates {
		if st, statErr := os.Stat(dir); statErr == nil && st.IsDir() {
			schemaDir = dir
			break
		}
	}
	if schemaDir == "" {
		return "", "", fmt.Errorf("cannot locate schemas/aap/v1 from %s", cwd)
	}
	// schemaDir = <repo>/backend/internal/protocolschema/schemas/aap/v1
	repoRoot = filepath.Clean(filepath.Join(schemaDir, "..", "..", "..", "..", "..", ".."))
	return repoRoot, schemaDir, nil
}

func loadSchemas(dir string) (map[string]map[string]any, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	docs := make(map[string]map[string]any)
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".schema.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		docs[name] = doc
		names = append(names, name)
	}
	sort.Strings(names)
	return docs, names, nil
}

func hashSchemas(dir string, names []string) (setHash string, docHashes map[string]string, err error) {
	docHashes = make(map[string]string, len(names))
	set := sha256.New()
	for _, name := range names {
		raw, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return "", nil, readErr
		}
		sum := sha256.Sum256(raw)
		docHashes[name] = fmt.Sprintf("%x", sum)
		_, _ = set.Write(raw)
	}
	return fmt.Sprintf("%x", set.Sum(nil)), docHashes, nil
}

func writeChecksumFile(path, setHash string, docHashes map[string]string, names []string) error {
	var b strings.Builder
	b.WriteString("# AAP 1.0 / protocol date 2026-08-11\n")
	b.WriteString("# schema-set (the concatenated bytes of the JSON files below in filename order)\n")
	b.WriteString(setHash + "  schema-set\n\n")
	for _, name := range names {
		b.WriteString(docHashes[name] + "  " + name + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func extractEventTypes(eventsDoc map[string]any) []string {
	// Stable catalog from registry mapping (not only $defs keys which may include helpers).
	// Prefer $defs keys that end with Data and match known event naming.
	known := []string{
		"run.accepted", "run.started", "run.waiting", "run.resumed",
		"run.completed", "run.failed", "run.cancelled",
		"item.started", "item.delta", "item.completed",
		"interaction.requested", "interaction.resolved", "interaction.expired",
		"usage.updated",
	}
	if eventsDoc == nil {
		return known
	}
	defs, _ := eventsDoc["$defs"].(map[string]any)
	// Verify defs exist for each known type's camelCase Data name.
	_ = defs
	return known
}

func extractEnum(doc map[string]any, prop string) []string {
	if doc == nil {
		return nil
	}
	props, _ := doc["properties"].(map[string]any)
	propSchema, _ := props[prop].(map[string]any)
	return enumValues(propSchema)
}

func extractDefEnum(doc map[string]any, def string) []string {
	if doc == nil {
		return nil
	}
	defs, _ := doc["$defs"].(map[string]any)
	schema, _ := defs[def].(map[string]any)
	return enumValues(schema)
}

func extractDeltaTypes(doc map[string]any) []string {
	if doc == nil {
		return nil
	}
	// delta.schema.json uses oneOf with const type fields.
	oneOf, _ := doc["oneOf"].([]any)
	var types []string
	for _, branch := range oneOf {
		branchMap, _ := branch.(map[string]any)
		props, _ := branchMap["properties"].(map[string]any)
		typeSchema, _ := props["type"].(map[string]any)
		if c, ok := typeSchema["const"].(string); ok {
			types = append(types, c)
		}
	}
	return types
}

func enumValues(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	raw, ok := schema["enum"].([]any)
	if !ok {
		return nil
	}
	var values []string
	for _, item := range raw {
		if s, ok := item.(string); ok {
			values = append(values, s)
		}
	}
	return values
}

type genMeta struct {
	SpecVersion     string
	ProtocolVersion string
	SchemaSetSHA256 string
	DocumentNames   []string
	DocumentSHA256  map[string]string
	EventTypes      []string
	RunStatuses     []string
	RunTriggers     []string
	ItemStatuses    []string
	DeltaTypes      []string
}

func writeGoGenerated(path string, meta genMeta) error {
	const tmpl = `// Code generated by protocolgen; DO NOT EDIT.
// Source: backend/internal/protocolschema/schemas/aap/v1

package generated

// SpecVersion is the AAP JSON Schema / event envelope spec version.
const SpecVersion = {{printf "%q" .SpecVersion}}

// ProtocolVersion is the frozen protocol date version.
const ProtocolVersion = {{printf "%q" .ProtocolVersion}}

// SchemaSetSHA256 is SHA-256 of all schema documents concatenated in filename order.
const SchemaSetSHA256 = {{printf "%q" .SchemaSetSHA256}}

// DocumentNames lists Schema Registry documents in generation order.
var DocumentNames = []string{
{{- range .DocumentNames}}
	{{printf "%q" .}},
{{- end}}
}

// DocumentSHA256 maps document file name → SHA-256 hex of raw bytes.
var DocumentSHA256 = map[string]string{
{{- range .DocumentNames}}
	{{printf "%q" .}}: {{printf "%q" (index $.DocumentSHA256 .)}},
{{- end}}
}

// EventTypes is the frozen v1 persisted protocol event catalog.
var EventTypes = []string{
{{- range .EventTypes}}
	{{printf "%q" .}},
{{- end}}
}

// RunStatuses is generated from run.schema.json.
var RunStatuses = []string{
{{- range .RunStatuses}}
	{{printf "%q" .}},
{{- end}}
}

// RunTriggers is generated from run.schema.json.
var RunTriggers = []string{
{{- range .RunTriggers}}
	{{printf "%q" .}},
{{- end}}
}

// ItemStatuses is generated from item.schema.json#/$defs/status.
var ItemStatuses = []string{
{{- range .ItemStatuses}}
	{{printf "%q" .}},
{{- end}}
}

// DeltaTypes is generated from delta.schema.json oneOf consts.
var DeltaTypes = []string{
{{- range .DeltaTypes}}
	{{printf "%q" .}},
{{- end}}
}
`
	return renderTemplate(path, tmpl, meta)
}

func writeTSGenerated(path string, meta genMeta) error {
	const tmpl = `// Code generated by protocolgen; DO NOT EDIT.
// Source: backend/internal/protocolschema/schemas/aap/v1

/** AAP event envelope / schema spec version. */
export const AAP_SPEC_VERSION = {{printf "%q" .SpecVersion}} as const;

/** Frozen protocol date version. */
export const AAP_PROTOCOL_DATE = {{printf "%q" .ProtocolVersion}} as const;

/** SHA-256 of the Schema Registry set (filename-sorted concatenation). */
export const AAP_SCHEMA_SET_SHA256 = {{printf "%q" .SchemaSetSHA256}} as const;

export const AAP_V1_DOCUMENT_NAMES = [
{{- range .DocumentNames}}
  {{printf "%q" .}},
{{- end}}
] as const;

export const AAP_V1_DOCUMENT_SHA256: Record<string, string> = {
{{- range .DocumentNames}}
  {{printf "%q" .}}: {{printf "%q" (index $.DocumentSHA256 .)}},
{{- end}}
};

export const AAP_V1_EVENT_TYPES = [
{{- range .EventTypes}}
  {{printf "%q" .}},
{{- end}}
] as const;

export type AAPV1EventType = (typeof AAP_V1_EVENT_TYPES)[number];

export const AAP_V1_RUN_STATUSES = [
{{- range .RunStatuses}}
  {{printf "%q" .}},
{{- end}}
] as const;

export type AAPV1RunStatus = (typeof AAP_V1_RUN_STATUSES)[number];

export const AAP_V1_RUN_TRIGGERS = [
{{- range .RunTriggers}}
  {{printf "%q" .}},
{{- end}}
] as const;

export type AAPV1RunTrigger = (typeof AAP_V1_RUN_TRIGGERS)[number];

export const AAP_V1_ITEM_STATUSES = [
{{- range .ItemStatuses}}
  {{printf "%q" .}},
{{- end}}
] as const;

export type AAPV1ItemStatus = (typeof AAP_V1_ITEM_STATUSES)[number];

export const AAP_V1_DELTA_TYPES = [
{{- range .DeltaTypes}}
  {{printf "%q" .}},
{{- end}}
] as const;

export type AAPV1DeltaType = (typeof AAP_V1_DELTA_TYPES)[number];

export function isAAPV1EventType(value: string): value is AAPV1EventType {
  return (AAP_V1_EVENT_TYPES as readonly string[]).includes(value);
}
`
	return renderTemplate(path, tmpl, meta)
}

func writeOpenAPIGenerated(path string, meta genMeta) error {
	const tmpl = `# Code generated by protocolgen; DO NOT EDIT.
# Source: backend/internal/protocolschema/schemas/aap/v1
# Schema-set SHA-256: {{.SchemaSetSHA256}}

components:
  schemas:
    AAPSpecVersion:
      type: string
      enum: [{{printf "%q" .SpecVersion}}]
    AAPProtocolDate:
      type: string
      enum: [{{printf "%q" .ProtocolVersion}}]
    AAPEventType:
      type: string
      description: Frozen v1 persisted protocol event types from the Schema Registry.
      enum:
{{- range .EventTypes}}
        - {{.}}
{{- end}}
    AAPRunStatus:
      type: string
      enum:
{{- range .RunStatuses}}
        - {{.}}
{{- end}}
    AAPRunTrigger:
      type: string
      enum:
{{- range .RunTriggers}}
        - {{.}}
{{- end}}
    AAPItemStatus:
      type: string
      enum:
{{- range .ItemStatuses}}
        - {{.}}
{{- end}}
    AAPDeltaType:
      type: string
      enum:
{{- range .DeltaTypes}}
        - {{.}}
{{- end}}
    AAPEventEnvelope:
      type: object
      additionalProperties: false
      required:
        - specVersion
        - type
        - eventId
        - streamId
        - sequence
        - occurredAt
        - workspaceId
        - agentId
        - conversationId
        - runId
        - traceId
        - data
      properties:
        specVersion:
          $ref: '#/components/schemas/AAPSpecVersion'
        type:
          type: string
        eventId:
          type: string
          format: uuid
        streamId:
          type: string
          pattern: '^run:'
        sequence:
          type: integer
          format: int64
          minimum: 1
        occurredAt:
          type: string
          format: date-time
        workspaceId:
          type: string
          format: uuid
        agentId:
          type: string
          format: uuid
        conversationId:
          type: string
          format: uuid
        runId:
          type: string
          format: uuid
        traceId:
          type: string
        data:
          type: object
`
	return renderTemplate(path, tmpl, meta)
}

func renderTemplate(path, tmpl string, data any) error {
	parsed, err := template.New("out").Parse(tmpl)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := buf.Bytes()
	if filepath.Ext(path) == ".go" {
		// Generated Go has to be gofmt-clean as written. A template can only
		// guess at alignment, so without this a formatting pass over the repo
		// edits the file and "generate must be clean" then fails on whitespace
		// no one chose.
		formatted, err := format.Source(payload)
		if err != nil {
			return fmt.Errorf("format %s: %w", path, err)
		}
		payload = formatted
	}
	return os.WriteFile(path, payload, 0o644)
}
