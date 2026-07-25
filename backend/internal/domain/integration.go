package domain

import (
	"fmt"
	"strings"
)

type ToolParameterValueSource string

const (
	ToolParameterValueSourceUserInput     ToolParameterValueSource = "UserInput"
	ToolParameterValueSourceSystemDefault ToolParameterValueSource = "SystemDefault"
)

type ToolParameter struct {
	Location     string                   `json:"location"`
	Name         string                   `json:"name"`
	Type         string                   `json:"type"`
	Required     bool                     `json:"required"`
	Description  string                   `json:"description"`
	ValueSource  ToolParameterValueSource `json:"valueSource,omitempty"`
	DefaultValue any                      `json:"defaultValue,omitempty"`
	Children     []ToolParameter          `json:"children,omitempty"`
	Item         *ToolParameter           `json:"item,omitempty"`
}

func (p ToolParameter) UsesSystemDefault() bool {
	_, ok := p.SystemDefaultValue()
	return ok
}

func (p ToolParameter) SystemDefaultValue() (any, bool) {
	if p.ValueSource == ToolParameterValueSourceUserInput {
		return nil, false
	}
	if p.DefaultValue != nil {
		return p.DefaultValue, true
	}
	return inferredTechnicalParameterDefault(p)
}

func inferredTechnicalParameterDefault(param ToolParameter) (any, bool) {
	location := strings.ToLower(strings.TrimSpace(param.Location))
	if location != "query" && location != "body" {
		return nil, false
	}
	name := normalizeTechnicalParameterName(param.Name)
	switch name {
	case "pagenum", "page_num", "page_no", "pageno", "page_number", "pagenumber":
		return typedTechnicalDefaultValue(param.Type, 1), true
	case "pagesize", "page_size":
		return typedTechnicalDefaultValue(param.Type, 20), true
	default:
		return nil, false
	}
}

func typedTechnicalDefaultValue(paramType string, value int) any {
	switch strings.ToLower(strings.TrimSpace(paramType)) {
	case "string":
		return fmt.Sprint(value)
	case "number":
		return float64(value)
	default:
		return value
	}
}

func normalizeTechnicalParameterName(name string) string {
	var normalized strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'A' && r <= 'Z':
			normalized.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			normalized.WriteRune(r)
		case r == '_':
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

type ToolResponseField struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description"`
	Children    []ToolResponseField `json:"children,omitempty"`
	Item        *ToolResponseField  `json:"item,omitempty"`
}

type OpenAPIEndpoint struct {
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	OperationID     string              `json:"operationId"`
	Summary         string              `json:"summary"`
	ToolIDCandidate string              `json:"toolIdCandidate"`
	RequestParams   []ToolParameter     `json:"requestParams"`
	ResponseFields  []ToolResponseField `json:"responseFields"`
	Issues          []string            `json:"issues"`
	Ready           bool                `json:"ready"`
}
