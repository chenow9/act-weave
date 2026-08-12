package a2ui

// Structural limits. MaxComponents matches ChildList.maxItems in the catalog;
// MaxTreeDepth bounds renderer recursion.
const (
	MaxComponents = 64
	MaxTreeDepth  = 16
	RootID        = "root"
)

// validateComponentGraph enforces the tree invariants JSON Schema cannot state.
// Input must already be schema-valid, so component objects and their id/children
// members have the expected types.
//
// The producer is strict here even though renderers must degrade gracefully on
// dangling references: a surface we emit ourselves should never contain one.
func validateComponentGraph(components []any) *Diagnostic {
	if len(components) > MaxComponents {
		return newDiagnostic(ReasonGraph, "/components", "maxItems", "", "at most 64 components")
	}

	byID := make(map[string]map[string]any, len(components))
	indexByID := make(map[string]int, len(components))
	for index, raw := range components {
		component, _ := raw.(map[string]any)
		id, _ := component["id"].(string)
		if _, duplicate := byID[id]; duplicate {
			return newDiagnostic(ReasonGraph, pointerAppend("/components", index, "id"), "unique", "",
				"an id used by no other component")
		}
		byID[id] = component
		indexByID[id] = index
	}

	if _, exists := byID[RootID]; !exists {
		return newDiagnostic(ReasonGraph, "/components", "root", "",
			`exactly one component with id "root"`)
	}

	for index, raw := range components {
		component, _ := raw.(map[string]any)
		name, _ := component["component"].(string)
		for position, childID := range childReferences(component) {
			if _, exists := byID[childID]; exists {
				continue
			}
			return newDiagnostic(ReasonGraph,
				childPointer(component, index, position), "reference", name,
				"the id of a component declared in this surface")
		}
	}

	visited := make(map[string]bool, len(components))
	if diagnostic := walkComponentTree(RootID, byID, indexByID, visited, make(map[string]bool), 1); diagnostic != nil {
		return diagnostic
	}
	for _, raw := range components {
		component, _ := raw.(map[string]any)
		id, _ := component["id"].(string)
		if !visited[id] {
			return newDiagnostic(ReasonGraph, pointerAppend("/components", indexByID[id]), "reachable", "",
				`every component reachable from "root"`)
		}
	}
	return nil
}

func walkComponentTree(
	id string,
	byID map[string]map[string]any,
	indexByID map[string]int,
	visited map[string]bool,
	onPath map[string]bool,
	depth int,
) *Diagnostic {
	if depth > MaxTreeDepth {
		return newDiagnostic(ReasonGraph, pointerAppend("/components", indexByID[id]), "maxDepth", "",
			"a tree at most 16 levels deep")
	}
	if onPath[id] {
		return newDiagnostic(ReasonGraph, pointerAppend("/components", indexByID[id]), "acyclic", "",
			"a tree without cycles")
	}
	onPath[id] = true
	visited[id] = true
	for _, childID := range childReferences(byID[id]) {
		if _, exists := byID[childID]; !exists {
			continue
		}
		if diagnostic := walkComponentTree(childID, byID, indexByID, visited, onPath, depth+1); diagnostic != nil {
			return diagnostic
		}
	}
	onPath[id] = false
	return nil
}

// childReferences lists the ids a component points at: Card uses a single child,
// containers use a list.
func childReferences(component map[string]any) []string {
	if component == nil {
		return nil
	}
	if child, ok := component["child"].(string); ok {
		return []string{child}
	}
	children, ok := component["children"].([]any)
	if !ok {
		return nil
	}
	references := make([]string, 0, len(children))
	for _, value := range children {
		if id, ok := value.(string); ok {
			references = append(references, id)
		}
	}
	return references
}

func childPointer(component map[string]any, index int, position int) string {
	if _, single := component["child"].(string); single {
		return pointerAppend("/components", index, "child")
	}
	return pointerAppend("/components", index, "children", position)
}
