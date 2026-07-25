package einoruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/domain"
)

// Scope resolution helpers aligned with workflowruntime/value_resolver.go
// (design §4.2 — one expression semantics; no second dialect).

var (
	graphTemplatePattern     = regexp.MustCompile(`\{\{\s*([^{}]+)\s*\}\}`)
	graphFullTemplatePattern = regexp.MustCompile(`^\s*\{\{\s*([^{}]+)\s*\}\}\s*$`)
)

func resolveMapValues(values map[string]any, scope GraphScope) (map[string]any, error) {
	resolved := make(map[string]any, len(values))
	for key, value := range values {
		next, err := resolveValue(value, scope)
		if err != nil {
			return nil, err
		}
		resolved[key] = next
	}
	return resolved, nil
}

func resolveValue(value any, scope GraphScope) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		switch kind, _ := typed["kind"].(string); kind {
		case "ref":
			path, _ := typed["path"].(string)
			return resolvePathFromScope(path, scope)
		case "literal":
			return typed["value"], nil
		}
		return resolveMapValues(typed, scope)
	case []any:
		resolved := make([]any, 0, len(typed))
		for _, item := range typed {
			next, err := resolveValue(item, scope)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, next)
		}
		return resolved, nil
	case string:
		if matches := graphFullTemplatePattern.FindStringSubmatch(typed); len(matches) == 2 {
			return resolvePathFromScope(matches[1], scope)
		}
		if graphTemplatePattern.MatchString(typed) {
			return renderTemplate(typed, scope)
		}
		return typed, nil
	default:
		return value, nil
	}
}

func renderTemplate(template string, scope GraphScope) (string, error) {
	if !graphTemplatePattern.MatchString(template) {
		return strings.TrimSpace(template), nil
	}
	var renderErr error
	rendered := graphTemplatePattern.ReplaceAllStringFunc(template, func(token string) string {
		if renderErr != nil {
			return token
		}
		matches := graphTemplatePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		value, err := resolvePathFromScope(matches[1], scope)
		if err != nil {
			renderErr = err
			return token
		}
		return fmt.Sprint(value)
	})
	if renderErr != nil {
		return "", renderErr
	}
	return strings.TrimSpace(rendered), nil
}

func evaluateCondition(expression string, scope GraphScope) (bool, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return false, nil
	}
	for _, operator := range []string{"==", "!="} {
		if !strings.Contains(expression, operator) {
			continue
		}
		parts := strings.SplitN(expression, operator, 2)
		left, err := resolveOperand(parts[0], scope)
		if err != nil {
			return false, err
		}
		right, err := resolveOperand(parts[1], scope)
		if err != nil {
			return false, err
		}
		equal := valuesEqual(left, right)
		if operator == "==" {
			return equal, nil
		}
		return !equal, nil
	}
	return false, fmt.Errorf("unsupported condition expression %q", expression)
}

func resolveOperand(token string, scope GraphScope) (any, error) {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "{} ")
	if literal, ok := parseLiteral(token); ok {
		return literal, nil
	}
	return resolvePathFromScope(token, scope)
}

func parseLiteral(token string) (any, bool) {
	switch token {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	}
	if len(token) >= 2 {
		if (strings.HasPrefix(token, "'") && strings.HasSuffix(token, "'")) ||
			(strings.HasPrefix(token, "\"") && strings.HasSuffix(token, "\"")) {
			return token[1 : len(token)-1], true
		}
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return number, true
	}
	return nil, false
}

func valuesEqual(left any, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func resolvePathFromScope(path string, scope GraphScope) (any, error) {
	path = strings.TrimSpace(path)
	switch {
	case path == "input":
		return scope.Input, nil
	case path == "workflowVars":
		return scope.WorkflowVars, nil
	case path == "nodeOutputs":
		return scope.NodeOutputs, nil
	case path == "foreach.item":
		if scope.ForeachItem == nil {
			return nil, fmt.Errorf("foreach.item is not available outside foreach iteration scope")
		}
		return scope.ForeachItem, nil
	case scope.ForeachAlias != "" && path == "foreach."+scope.ForeachAlias:
		if scope.ForeachItem == nil {
			return nil, fmt.Errorf("foreach.%s is not available outside foreach iteration scope", scope.ForeachAlias)
		}
		return scope.ForeachItem, nil
	case strings.HasPrefix(path, "input."):
		return resolvePath(scope.Input, strings.TrimPrefix(path, "input."))
	case strings.HasPrefix(path, "workflowVars."):
		return resolvePath(scope.WorkflowVars, strings.TrimPrefix(path, "workflowVars."))
	case strings.HasPrefix(path, "nodeOutputs."):
		return resolvePath(scope.NodeOutputs, strings.TrimPrefix(path, "nodeOutputs."))
	case strings.HasPrefix(path, "foreach.item."):
		if scope.ForeachItem == nil {
			return nil, fmt.Errorf("foreach.item is not available outside foreach iteration scope")
		}
		return resolvePath(scope.ForeachItem, strings.TrimPrefix(path, "foreach.item."))
	case scope.ForeachAlias != "" && strings.HasPrefix(path, "foreach."+scope.ForeachAlias+"."):
		if scope.ForeachItem == nil {
			return nil, fmt.Errorf("foreach.%s is not available outside foreach iteration scope", scope.ForeachAlias)
		}
		return resolvePath(scope.ForeachItem, strings.TrimPrefix(path, "foreach."+scope.ForeachAlias+"."))
	default:
		return nil, fmt.Errorf("unsupported scope path %q", path)
	}
}

func resolvePath(root any, path string) (any, error) {
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, fmt.Errorf("path %q not found", path)
			}
			current = next
		case map[string]map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, fmt.Errorf("path %q not found", path)
			}
			current = next
		default:
			return nil, fmt.Errorf("path %q not found", path)
		}
	}
	return current, nil
}

func resolveToolInput(config map[string]any, scope GraphScope) (map[string]any, error) {
	rawInput, ok := config["inputMapping"]
	if !ok {
		rawInput, ok = config["input"]
	}
	if !ok {
		// Align with workflowruntime: default to workflow run input when
		// smart-dag.v2 omits inputMapping on Tool nodes.
		return cloneAnyMap(scope.Input), nil
	}
	resolved, err := resolveValue(rawInput, scope)
	if err != nil {
		return nil, err
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool input must resolve to an object")
	}
	return resolvedMap, nil
}

// resolveOptionalNodeInput mirrors workflowruntime.resolveOptionalInput for
// HTTP / SubWorkflow nodes: prefer config["input"], fall back to
// config["inputMapping"], and return nil (not empty map) when neither is
// present so nodeOutputs.request matches plan_runner simulation semantics.
func resolveOptionalNodeInput(config map[string]any, scope GraphScope) (map[string]any, error) {
	rawInput, ok := config["input"]
	if !ok {
		rawInput, ok = config["inputMapping"]
	}
	if !ok {
		return nil, nil
	}
	resolved, err := resolveValue(rawInput, scope)
	if err != nil {
		return nil, err
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("advanced node input must resolve to an object")
	}
	return resolvedMap, nil
}

// resolveOptionalHTTPInput is an alias kept for call-site clarity in tests.
func resolveOptionalHTTPInput(config map[string]any, scope GraphScope) (map[string]any, error) {
	return resolveOptionalNodeInput(config, scope)
}

func selectConditionBranch(nodeID string, conditionResult bool, branchLabels []string) (string, error) {
	if !conditionResult {
		return "default", nil
	}
	nonDefault := map[string]bool{}
	for _, label := range branchLabels {
		if label != "" && label != "default" {
			nonDefault[label] = true
		}
	}
	switch len(nonDefault) {
	case 0:
		return "default", nil
	case 1:
		for label := range nonDefault {
			return label, nil
		}
	}
	return "", fmt.Errorf("condition node %s has ambiguous non-default branches", nodeID)
}

func normalizeToolResult(result map[string]any) map[string]any {
	normalized := cloneAnyMap(result)
	if nested, ok := result["data"].(map[string]any); ok {
		for key, value := range nested {
			if _, exists := normalized[key]; !exists {
				normalized[key] = value
			}
		}
	}
	return normalized
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func mergeAnyMaps(current map[string]any, updates map[string]any) map[string]any {
	merged := cloneAnyMap(current)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func summarizeValue(value any) string {
	if value == nil {
		return "{}"
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprintf("%v", value)
	}
	summary := strings.TrimSpace(buffer.String())
	if len(summary) > 420 {
		return summary[:420] + "..."
	}
	return summary
}

func nodeLabel(config map[string]any, fallback string) string {
	if label, ok := config["label"].(string); ok && strings.TrimSpace(label) != "" {
		return label
	}
	return fallback
}

func newGraphStep(
	executionID string,
	name string,
	nodeID string,
	nodeType string,
	status domain.ExecutionStepStatus,
	inputSummary string,
	outputSummary string,
) domain.ExecutionStepRecord {
	started := time.Now().UTC()
	stepSlug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	if nodeID != "" {
		stepSlug = nodeID + "-" + stepSlug
	}
	return domain.ExecutionStepRecord{
		ID:                      "step-" + executionID + "-" + stepSlug,
		ExecutionID:             executionID,
		Name:                    name,
		NodeID:                  nodeID,
		NodeType:                nodeType,
		Status:                  status,
		InputSummary:            inputSummary,
		OutputSummary:           outputSummary,
		DurationMS:              1,
		RawPayloadObjectAddress: graphStepPayloadAddress(executionID, nodeID, name, status),
		StartedAt:               started,
		FinishedAt:              started.Add(time.Millisecond),
	}
}

func graphStepPayloadAddress(executionID, nodeID, name string, status domain.ExecutionStepStatus) string {
	parts := []string{"s3://actweave-executions", executionID, "steps"}
	label := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	if nodeID != "" {
		label = nodeID + "-" + label
	}
	label = strings.Trim(label, "-")
	if label == "" {
		label = "system-step"
	}
	statusSlug := strings.ToLower(strings.ReplaceAll(string(status), " ", "-"))
	return strings.Join(parts, "/") + "/" + label + "-" + statusSlug + ".json"
}

func approvalAuditSummary(decision, requestedBy string, requestedAt time.Time, resolvedBy string, resolvedAt time.Time) string {
	parts := []string{"decision=" + decision}
	if strings.TrimSpace(requestedBy) != "" {
		parts = append(parts, "requestedBy="+requestedBy)
	}
	if !requestedAt.IsZero() {
		parts = append(parts, "requestedAt="+requestedAt.UTC().Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(resolvedBy) != "" {
		parts = append(parts, "resolvedBy="+resolvedBy)
	}
	if !resolvedAt.IsZero() {
		parts = append(parts, "resolvedAt="+resolvedAt.UTC().Format(time.RFC3339Nano))
	}
	return strings.Join(parts, " ")
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
