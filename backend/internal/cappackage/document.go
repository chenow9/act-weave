package cappackage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	APIVersionV1 = "actweave.package/v1"
	KindPackage  = "Package"

	MaxBytes           = 5 << 20
	MaxTools           = 50
	MaxWorkflows       = 20
	DefaultGraphSchema = "workflow.graph.v1"
	HTTPActionSchema   = "http.v1"
	ActionCreate       = "create"
	ActionUpdateDraft  = "update-draft"
	ActionBlocked      = "blocked"
	ActionRejected     = "rejected"
	KindTool           = "tool"
	KindWorkflow       = "workflow"
	SourceDraft        = "draft"
	SourcePublished    = "published"
	SourceAuto         = ""
)

var (
	ErrInvalid  = errors.New("invalid capability package")
	ErrNotFound = errors.New("capability package resource not found")
	ErrConflict = errors.New("capability package conflict")
	ErrBlocked  = errors.New("capability package import blocked")
	ErrTooLarge = errors.New("capability package too large")
)

var slugPattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// Document is the on-disk YAML package.
type Document struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name       string     `yaml:"name,omitempty" json:"name,omitempty"`
	ExportedAt *time.Time `yaml:"exportedAt,omitempty" json:"exportedAt,omitempty"`
	Source     SourceHint `yaml:"source,omitempty" json:"source,omitempty"`
}

type SourceHint struct {
	WorkspaceHint string `yaml:"workspaceHint,omitempty" json:"workspaceHint,omitempty"`
	ExportedBy    string `yaml:"exportedBy,omitempty" json:"exportedBy,omitempty"`
}

type Spec struct {
	Tools     []ToolSpec     `yaml:"tools,omitempty" json:"tools,omitempty"`
	Workflows []WorkflowSpec `yaml:"workflows,omitempty" json:"workflows,omitempty"`
}

type ToolSpec struct {
	Slug                 string `yaml:"slug" json:"slug"`
	Name                 string `yaml:"name" json:"name"`
	Description          string `yaml:"description,omitempty" json:"description,omitempty"`
	Provider             string `yaml:"provider" json:"provider"`
	Connection           string `yaml:"connection,omitempty" json:"connection,omitempty"`
	RiskLevel            string `yaml:"riskLevel" json:"riskLevel"`
	SideEffectLevel      string `yaml:"sideEffectLevel" json:"sideEffectLevel"`
	RequiresConfirmation bool   `yaml:"requiresConfirmation" json:"requiresConfirmation"`
	Action               Action `yaml:"action" json:"action"`
	InputSchema          Object `yaml:"inputSchema" json:"inputSchema"`
	OutputSchema         Object `yaml:"outputSchema" json:"outputSchema"`
	ErrorMappings        Object `yaml:"errorMappings,omitempty" json:"errorMappings,omitempty"`
	RuntimePolicy        Object `yaml:"runtimePolicy" json:"runtimePolicy"`
}

type Action struct {
	SchemaVersion string `yaml:"schemaVersion" json:"schemaVersion"`
	Config        Object `yaml:"config" json:"config"`
}

type WorkflowSpec struct {
	Slug        string `yaml:"slug" json:"slug"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Graph       Graph  `yaml:"graph" json:"graph"`
}

type Graph struct {
	SchemaVersion string      `yaml:"schemaVersion" json:"schemaVersion"`
	Nodes         []GraphNode `yaml:"nodes" json:"nodes"`
	Edges         []GraphEdge `yaml:"edges" json:"edges"`
	Viewport      Viewport    `yaml:"viewport,omitempty" json:"viewport,omitempty"`
	UI            Object      `yaml:"ui,omitempty" json:"ui,omitempty"`
}

type GraphNode struct {
	ID       string      `yaml:"id" json:"id"`
	Type     string      `yaml:"type" json:"type"`
	Label    string      `yaml:"label,omitempty" json:"label,omitempty"`
	Position Position    `yaml:"position,omitempty" json:"position,omitempty"`
	Ports    []GraphPort `yaml:"ports,omitempty" json:"ports,omitempty"`
	Data     Object      `yaml:"data,omitempty" json:"data,omitempty"`
	UI       Object      `yaml:"ui,omitempty" json:"ui,omitempty"`
}

type GraphEdge struct {
	ID           string `yaml:"id" json:"id"`
	SourceNodeID string `yaml:"sourceNodeId" json:"sourceNodeId"`
	SourcePort   string `yaml:"sourcePort,omitempty" json:"sourcePort,omitempty"`
	TargetNodeID string `yaml:"targetNodeId" json:"targetNodeId"`
	TargetPort   string `yaml:"targetPort,omitempty" json:"targetPort,omitempty"`
	Data         Object `yaml:"data,omitempty" json:"data,omitempty"`
	UI           Object `yaml:"ui,omitempty" json:"ui,omitempty"`
}

type GraphPort struct {
	Key       string `yaml:"key" json:"key"`
	Label     string `yaml:"label,omitempty" json:"label,omitempty"`
	Direction string `yaml:"direction" json:"direction"`
}

type Position struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

type Viewport struct {
	X    float64 `yaml:"x" json:"x"`
	Y    float64 `yaml:"y" json:"y"`
	Zoom float64 `yaml:"zoom,omitempty" json:"zoom,omitempty"`
}

// Object is a JSON object stored as YAML mapping.
type Object map[string]any

func (o Object) JSON() json.RawMessage {
	if o == nil {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(o)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func objectFromJSON(raw json.RawMessage) Object {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return Object{}
	}
	var value Object
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return Object{}
	}
	return value
}

func Parse(content []byte) (Document, error) {
	if int64(len(content)) > MaxBytes {
		return Document{}, ErrTooLarge
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return Document{}, fmt.Errorf("%w: empty document", ErrInvalid)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(trimmed))
	decoder.KnownFields(true)
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return Document{}, fmt.Errorf("%w: document must contain one YAML value", ErrInvalid)
	}
	if err := validateDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func Marshal(document Document) ([]byte, error) {
	if err := validateDocument(document); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("%w: encode yaml: %v", ErrInvalid, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("%w: close yaml encoder: %v", ErrInvalid, err)
	}
	return buffer.Bytes(), nil
}

func validateDocument(document Document) error {
	if strings.TrimSpace(document.APIVersion) != APIVersionV1 {
		return fmt.Errorf("%w: apiVersion must be %s", ErrInvalid, APIVersionV1)
	}
	if strings.TrimSpace(document.Kind) != KindPackage {
		return fmt.Errorf("%w: kind must be %s", ErrInvalid, KindPackage)
	}
	if len(document.Spec.Tools) > MaxTools {
		return fmt.Errorf("%w: at most %d tools", ErrInvalid, MaxTools)
	}
	if len(document.Spec.Workflows) > MaxWorkflows {
		return fmt.Errorf("%w: at most %d workflows", ErrInvalid, MaxWorkflows)
	}
	if len(document.Spec.Tools) == 0 && len(document.Spec.Workflows) == 0 {
		return fmt.Errorf("%w: package has no tools or workflows", ErrInvalid)
	}
	seen := map[string]string{}
	for i, spec := range document.Spec.Tools {
		if err := validateToolSpec(spec); err != nil {
			return fmt.Errorf("%w: tools[%d]: %v", ErrInvalid, i, err)
		}
		key := "tool:" + spec.Slug
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate tool slug %s (also %s)", ErrInvalid, spec.Slug, previous)
		}
		seen[key] = spec.Name
		if containsSensitiveValue(spec.Action.Config) || containsSensitiveValue(spec.RuntimePolicy) ||
			containsSensitiveValue(spec.InputSchema) || containsSensitiveValue(spec.OutputSchema) ||
			containsSensitiveValue(spec.ErrorMappings) {
			return fmt.Errorf("%w: tools[%d] (%s) contains a forbidden secret-like key", ErrInvalid, i, spec.Slug)
		}
	}
	for i, spec := range document.Spec.Workflows {
		if err := validateWorkflowSpec(spec); err != nil {
			return fmt.Errorf("%w: workflows[%d]: %v", ErrInvalid, i, err)
		}
		key := "workflow:" + spec.Slug
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate workflow slug %s (also %s)", ErrInvalid, spec.Slug, previous)
		}
		seen[key] = spec.Name
		if containsSensitiveGraph(spec.Graph) {
			return fmt.Errorf("%w: workflows[%d] (%s) contains a forbidden secret-like key", ErrInvalid, i, spec.Slug)
		}
	}
	return nil
}

func validateToolSpec(spec ToolSpec) error {
	if !validSlug(spec.Slug) {
		return fmt.Errorf("slug %q is invalid", spec.Slug)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(spec.Provider) == "" {
		return errors.New("provider is required")
	}
	if spec.Action.SchemaVersion != HTTPActionSchema {
		return fmt.Errorf("action.schemaVersion must be %s", HTTPActionSchema)
	}
	if spec.Action.Config == nil {
		return errors.New("action.config is required")
	}
	if !oneOf(spec.RiskLevel, "LOW", "MEDIUM", "HIGH", "CRITICAL") {
		return fmt.Errorf("riskLevel %q is invalid", spec.RiskLevel)
	}
	if !oneOf(spec.SideEffectLevel, "NONE", "READ", "WRITE", "IRREVERSIBLE") {
		return fmt.Errorf("sideEffectLevel %q is invalid", spec.SideEffectLevel)
	}
	if spec.InputSchema == nil || spec.OutputSchema == nil || spec.RuntimePolicy == nil {
		return errors.New("inputSchema, outputSchema, and runtimePolicy are required objects")
	}
	return nil
}

func validateWorkflowSpec(spec WorkflowSpec) error {
	if !validSlug(spec.Slug) {
		return fmt.Errorf("slug %q is invalid", spec.Slug)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(spec.Graph.SchemaVersion) == "" {
		return errors.New("graph.schemaVersion is required")
	}
	seen := map[string]struct{}{}
	for _, node := range spec.Graph.Nodes {
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.Type) == "" {
			return errors.New("graph nodes require id and type")
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicate node id %s", node.ID)
		}
		seen[node.ID] = struct{}{}
	}
	for _, edge := range spec.Graph.Edges {
		if strings.TrimSpace(edge.ID) == "" || strings.TrimSpace(edge.SourceNodeID) == "" ||
			strings.TrimSpace(edge.TargetNodeID) == "" {
			return errors.New("graph edges require id, sourceNodeId, and targetNodeId")
		}
	}
	return nil
}

func validSlug(value string) bool {
	return slugPattern.MatchString(strings.TrimSpace(value))
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func FileName(document Document) string {
	name := strings.TrimSpace(document.Metadata.Name)
	if validSlug(name) {
		return name + ".actweave.yaml"
	}
	if len(document.Spec.Tools) == 1 && len(document.Spec.Workflows) == 0 {
		return document.Spec.Tools[0].Slug + ".actweave.yaml"
	}
	if len(document.Spec.Workflows) == 1 && len(document.Spec.Tools) == 0 {
		return document.Spec.Workflows[0].Slug + ".actweave.yaml"
	}
	return "actweave-package.yaml"
}
