package workflowruntime

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var templatePattern = regexp.MustCompile(`\{\{\s*([^{}]+)\s*\}\}`)
var fullTemplatePattern = regexp.MustCompile(`^\s*\{\{\s*([^{}]+)\s*\}\}\s*$`)

type ExecutionScope struct {
	Input        map[string]any
	WorkflowVars map[string]any
	NodeOutputs  map[string]map[string]any
	ForeachItem  any
	ForeachAlias string
}

func newExecutionScope(ctx ExecutionContext) ExecutionScope {
	scope := ctx.Scope
	scope.Input = cloneMap(ctx.Input)
	if scope.Input == nil {
		scope.Input = map[string]any{}
	}
	if scope.WorkflowVars == nil {
		scope.WorkflowVars = map[string]any{}
	}
	if scope.NodeOutputs == nil {
		scope.NodeOutputs = map[string]map[string]any{}
	}
	return scope
}

func resolveMapValues(values map[string]any, scope ExecutionScope) (map[string]any, error) {
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

func resolveValue(value any, scope ExecutionScope) (any, error) {
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
		if matches := fullTemplatePattern.FindStringSubmatch(typed); len(matches) == 2 {
			return resolvePathFromScope(matches[1], scope)
		}
		if templatePattern.MatchString(typed) {
			return renderTemplate(typed, scope)
		}
		return typed, nil
	default:
		return value, nil
	}
}

func renderTemplate(template string, scope ExecutionScope) (string, error) {
	if !templatePattern.MatchString(template) {
		return strings.TrimSpace(template), nil
	}
	var renderErr error
	rendered := templatePattern.ReplaceAllStringFunc(template, func(token string) string {
		if renderErr != nil {
			return token
		}
		matches := templatePattern.FindStringSubmatch(token)
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

func evaluateCondition(expression string, scope ExecutionScope) (bool, error) {
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

func resolveOperand(token string, scope ExecutionScope) (any, error) {
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
		if (strings.HasPrefix(token, "'") && strings.HasSuffix(token, "'")) || (strings.HasPrefix(token, "\"") && strings.HasSuffix(token, "\"")) {
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

func resolvePathFromScope(path string, scope ExecutionScope) (any, error) {
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
