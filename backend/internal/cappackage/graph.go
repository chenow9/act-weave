package cappackage

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func graphFromJSON(raw json.RawMessage) (Graph, error) {
	raw = bytesTrim(raw)
	if len(raw) == 0 {
		return Graph{SchemaVersion: DefaultGraphSchema, Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}
	var graph Graph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return Graph{}, err
	}
	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = DefaultGraphSchema
	}
	if graph.Nodes == nil {
		graph.Nodes = []GraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []GraphEdge{}
	}
	return graph, nil
}

func graphJSON(graph Graph) (json.RawMessage, error) {
	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = DefaultGraphSchema
	}
	if graph.Nodes == nil {
		graph.Nodes = []GraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []GraphEdge{}
	}
	return json.Marshal(graph)
}

func rewriteGraphForExport(graph Graph, toolSlugByID, workflowSlugByID map[string]string) Graph {
	nodes := make([]GraphNode, len(graph.Nodes))
	for i, node := range graph.Nodes {
		node.Data = cloneObject(node.Data)
		switch node.Type {
		case "Tool":
			replaceIDWithSlug(node.Data, "toolId", "toolSlug", toolSlugByID)
		case "SubWorkflow":
			replaceIDWithSlug(node.Data, "workflowId", "workflowSlug", workflowSlugByID)
		}
		nodes[i] = node
	}
	graph.Nodes = nodes
	return graph
}

func rewriteGraphForImport(graph Graph, toolIDBySlug, workflowIDBySlug map[string]string) (Graph, []string) {
	var missing []string
	nodes := make([]GraphNode, len(graph.Nodes))
	for i, node := range graph.Nodes {
		node.Data = cloneObject(node.Data)
		switch node.Type {
		case "Tool":
			if reason := replaceSlugWithID(node.Data, "toolSlug", "toolId", toolIDBySlug); reason != "" {
				missing = append(missing, "node "+node.ID+": "+reason)
			}
		case "SubWorkflow":
			if reason := replaceSlugWithID(node.Data, "workflowSlug", "workflowId", workflowIDBySlug); reason != "" {
				missing = append(missing, "node "+node.ID+": "+reason)
			}
		}
		nodes[i] = node
	}
	graph.Nodes = nodes
	return graph, missing
}

func replaceIDWithSlug(data Object, idKey, slugKey string, slugByID map[string]string) {
	if data == nil {
		return
	}
	raw, _ := data[idKey].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if slug, ok := slugByID[raw]; ok && slug != "" {
		data[slugKey] = slug
		delete(data, idKey)
		return
	}
	if _, err := uuid.Parse(raw); err != nil {
		data[slugKey] = raw
		delete(data, idKey)
	}
}

func replaceSlugWithID(data Object, slugKey, idKey string, idBySlug map[string]string) string {
	if data == nil {
		data = Object{}
	}
	slug, _ := data[slugKey].(string)
	slug = strings.TrimSpace(slug)
	if slug == "" {
		if fallback, _ := data[idKey].(string); strings.TrimSpace(fallback) != "" {
			slug = strings.TrimSpace(fallback)
		}
	}
	if slug == "" {
		return slugKey + " is missing"
	}
	id, ok := idBySlug[slug]
	if !ok || id == "" {
		return slugKey + " " + slug + " was not found in this workspace"
	}
	data[idKey] = id
	delete(data, slugKey)
	return ""
}

func cloneObject(value Object) Object {
	if value == nil {
		return Object{}
	}
	cloned := make(Object, len(value))
	for key, child := range value {
		cloned[key] = child
	}
	return cloned
}

func bytesTrim(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
