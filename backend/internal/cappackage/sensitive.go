package cappackage

import "strings"

var forbiddenKeyFragments = []string{
	"password",
	"secretvalue",
	"tokenvalue",
	"apikeyvalue",
	"authorization",
	"refreshtoken",
}

func containsSensitiveValue(value any) bool {
	switch typed := value.(type) {
	case Object:
		return containsSensitiveValue(map[string]any(typed))
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			for _, forbidden := range forbiddenKeyFragments {
				if strings.Contains(normalized, forbidden) {
					return true
				}
			}
			if containsSensitiveValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveValue(child) {
				return true
			}
		}
	}
	return false
}

func containsSensitiveGraph(graph Graph) bool {
	if containsSensitiveValue(graph.UI) {
		return true
	}
	for _, node := range graph.Nodes {
		if containsSensitiveValue(node.Data) || containsSensitiveValue(node.UI) {
			return true
		}
	}
	for _, edge := range graph.Edges {
		if containsSensitiveValue(edge.Data) || containsSensitiveValue(edge.UI) {
			return true
		}
	}
	return false
}
