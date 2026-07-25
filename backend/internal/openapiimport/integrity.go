package openapiimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Integrity status values (computed, never persisted).
const (
	IntegrityComplete   = "COMPLETE"
	IntegrityIncomplete = "INCOMPLETE"
)

// ErrImportIncomplete means the Import cannot generate tools safely.
// Transport maps to HTTP 409 OPENAPI_IMPORT_INCOMPLETE, retryable=false.
var ErrImportIncomplete = errors.New("openapi import is incomplete")

// IntegrityIssue is a safe, non-sensitive description of one integrity problem.
type IntegrityIssue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	EndpointID string `json:"endpointId,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
}

// IntegrityReport is the real-time projection for GET detail and Generate gate.
type IntegrityReport struct {
	Status                 string          `json:"status"`
	ExpectedTotalEndpoints int             `json:"expectedTotalEndpoints"`
	ActualTotalEndpoints   int             `json:"actualTotalEndpoints"`
	ExpectedReadyEndpoints int             `json:"expectedReadyEndpoints"`
	ActualReadyEndpoints   int             `json:"actualReadyEndpoints"`
	Issues                 []IntegrityIssue `json:"issues"`
}

// EvaluateIntegrity computes integrity from the current Import summary row and
// endpoint rows. It never writes, re-parses, or guesses historical data.
func EvaluateIntegrity(imp Import, endpoints []Endpoint) IntegrityReport {
	report := IntegrityReport{
		ExpectedTotalEndpoints: imp.TotalEndpoints,
		ActualTotalEndpoints:   len(endpoints),
		ExpectedReadyEndpoints: imp.ReadyEndpoints,
		Issues:                 make([]IntegrityIssue, 0),
	}
	actualReady := 0
	for _, ep := range endpoints {
		issues := evaluateEndpoint(ep)
		report.Issues = append(report.Issues, issues...)
		if ep.Ready && len(issues) == 0 {
			// Ready flag only counts when structural/schema gates also pass.
			if endpointEligibleForGeneration(ep) {
				actualReady++
			} else {
				// Flagged ready but fails generation preconditions.
				report.Issues = append(report.Issues, IntegrityIssue{
					Code:       "ENDPOINT_READY_PRECONDITION_FAILED",
					Message:    "Endpoint is marked ready but fails generation schema/action preconditions.",
					EndpointID: ep.ID, Method: ep.Method, Path: ep.Path,
				})
			}
		} else if ep.Ready && len(issues) > 0 {
			report.Issues = append(report.Issues, IntegrityIssue{
				Code:       "ENDPOINT_READY_WITH_ISSUES",
				Message:    "Endpoint is marked ready but has structural or schema issues.",
				EndpointID: ep.ID, Method: ep.Method, Path: ep.Path,
			})
		}
	}
	report.ActualReadyEndpoints = actualReady

	// Summary vs actual list drift (e.g. 8 expected, 0 rows).
	if report.ExpectedTotalEndpoints != report.ActualTotalEndpoints {
		report.Issues = append(report.Issues, IntegrityIssue{
			Code:    "ENDPOINT_COUNT_MISMATCH",
			Message: fmt.Sprintf("Import summary expects %d endpoints but %d rows are present.", report.ExpectedTotalEndpoints, report.ActualTotalEndpoints),
		})
	}
	if report.ExpectedReadyEndpoints != report.ActualReadyEndpoints {
		report.Issues = append(report.Issues, IntegrityIssue{
			Code:    "READY_COUNT_MISMATCH",
			Message: fmt.Sprintf("Import summary expects %d ready endpoints but %d are actually generation-ready.", report.ExpectedReadyEndpoints, report.ActualReadyEndpoints),
		})
	}

	if len(report.Issues) == 0 {
		report.Status = IntegrityComplete
	} else {
		report.Status = IntegrityIncomplete
	}
	return report
}

func evaluateEndpoint(ep Endpoint) []IntegrityIssue {
	var issues []IntegrityIssue
	if strings.TrimSpace(ep.ID) == "" {
		issues = append(issues, IntegrityIssue{
			Code: "ENDPOINT_ID_MISSING", Message: "Endpoint is missing an id.",
			Method: ep.Method, Path: ep.Path,
		})
	}
	if strings.TrimSpace(ep.Method) == "" {
		issues = append(issues, IntegrityIssue{
			Code: "ENDPOINT_METHOD_MISSING", Message: "Endpoint is missing an HTTP method.",
			EndpointID: ep.ID, Path: ep.Path,
		})
	}
	if strings.TrimSpace(ep.Path) == "" {
		issues = append(issues, IntegrityIssue{
			Code: "ENDPOINT_PATH_MISSING", Message: "Endpoint is missing a path.",
			EndpointID: ep.ID, Method: ep.Method,
		})
	}
	if !isValidJSONSchemaObject(ep.InputSchema) {
		issues = append(issues, IntegrityIssue{
			Code:       "INPUT_SCHEMA_INVALID",
			Message:    "Endpoint input schema is missing or is not a valid JSON schema object.",
			EndpointID: ep.ID, Method: ep.Method, Path: ep.Path,
		})
	}
	if len(ep.OutputSchema) > 0 && string(ep.OutputSchema) != "null" && !isValidJSONSchemaObject(ep.OutputSchema) {
		issues = append(issues, IntegrityIssue{
			Code:       "OUTPUT_SCHEMA_INVALID",
			Message:    "Endpoint output schema is not a valid JSON schema object.",
			EndpointID: ep.ID, Method: ep.Method, Path: ep.Path,
		})
	}
	return issues
}

// isValidJSONSchemaObject accepts a JSON object suitable as a tool schema.
// Legal empty object schemas like {"type":"object","properties":{}} are valid.
// Missing/null/array/string/non-object fail.
func isValidJSONSchemaObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return false
	}
	// If type is present it must be object (or include object).
	if typeVal, hasType := object["type"]; hasType {
		switch t := typeVal.(type) {
		case string:
			if t != "object" {
				return false
			}
		case []any:
			found := false
			for _, item := range t {
				if s, ok := item.(string); ok && s == "object" {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		default:
			return false
		}
	}
	// properties when present must be an object (may be empty).
	if props, hasProps := object["properties"]; hasProps {
		if _, ok := props.(map[string]any); !ok && props != nil {
			return false
		}
	}
	return true
}

// endpointEligibleForGeneration mirrors generation actionConfig preconditions
// without creating tools: input schema must support parameter projection.
func endpointEligibleForGeneration(ep Endpoint) bool {
	if !ep.Ready {
		return false
	}
	if strings.TrimSpace(ep.Method) == "" || strings.TrimSpace(ep.Path) == "" {
		return false
	}
	if !isValidJSONSchemaObject(ep.InputSchema) {
		return false
	}
	// Same structural requirement as actionConfigForEndpoint: properties map present
	// (may be empty for no-parameter endpoints).
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if json.Unmarshal(ep.InputSchema, &schema) != nil || schema.Properties == nil {
		return false
	}
	// Each property used for HTTP mapping must have location when properties exist.
	for name, property := range schema.Properties {
		if property == nil {
			return false
		}
		location, _ := property["x-actweave-location"].(string)
		parameterName, _ := property["x-actweave-parameter-name"].(string)
		location = strings.ToLower(strings.TrimSpace(location))
		parameterName = strings.TrimSpace(parameterName)
		if parameterName == "" {
			parameterName = name
		}
		if location != "path" && location != "query" && location != "header" && location != "body" {
			// Empty properties map is OK; non-empty without location is not generation-ready.
			if len(schema.Properties) > 0 && strings.TrimSpace(location) == "" {
				return false
			}
			if location != "" && location != "path" && location != "query" && location != "header" && location != "body" {
				return false
			}
		}
	}
	return true
}

// AssertImportComplete fails closed when integrity is incomplete.
func AssertImportComplete(imp Import, endpoints []Endpoint) error {
	report := EvaluateIntegrity(imp, endpoints)
	if report.Status != IntegrityComplete {
		return ErrImportIncomplete
	}
	return nil
}
