package workflowtranslator

import (
	"fmt"
	"strings"
)

// GraphEngineVersion is the graph-build code version segment of the compile
// cache key (design §4.2). Bump when PR11+ graph_builder / node lambdas change
// semantics so cached Runnables invalidate across processes.
//
// Format is free-form but must stay stable for a given code revision.
// Bumped when graph-build semantics change (PR13d: ForEach scoped under eino_core).
const GraphEngineVersion = "eino-graph-v5"

// CacheKeyParts holds the inputs for a graph compile cache key.
//
//	cacheKey = (workspaceID, revisionID, planHash, engineVersion)
type CacheKeyParts struct {
	WorkspaceID   string
	RevisionID    string
	PlanHash      string
	EngineVersion string
	// Engine is optional; when set it is included so eino_core and eino
	// graphs never share a cache entry for the same plan.
	Engine string
}

// CacheKey returns a stable, opaque string for graph compile cache lookup.
// Empty fields are preserved as empty segments so callers can detect missing
// identity without silent coalescing of different plans.
//
// Layout (pipe-delimited, no escaping — IDs/hashes are platform-controlled):
//
//	ws|{workspaceID}|rev|{revisionID}|plan|{planHash}|engver|{engineVersion}[|mode|{engine}]
func CacheKey(workspaceID, revisionID, planHash, engineVersion string) string {
	return CacheKeyFrom(CacheKeyParts{
		WorkspaceID:   workspaceID,
		RevisionID:    revisionID,
		PlanHash:      planHash,
		EngineVersion: engineVersion,
	})
}

// CacheKeyFrom builds a cache key from structured parts. Empty EngineVersion
// defaults to GraphEngineVersion so call sites need not hard-code the constant.
func CacheKeyFrom(parts CacheKeyParts) string {
	engineVersion := strings.TrimSpace(parts.EngineVersion)
	if engineVersion == "" {
		engineVersion = GraphEngineVersion
	}
	key := fmt.Sprintf(
		"ws|%s|rev|%s|plan|%s|engver|%s",
		strings.TrimSpace(parts.WorkspaceID),
		strings.TrimSpace(parts.RevisionID),
		strings.TrimSpace(parts.PlanHash),
		engineVersion,
	)
	if engine := strings.ToLower(strings.TrimSpace(parts.Engine)); engine != "" {
		key += "|mode|" + engine
	}
	return key
}
