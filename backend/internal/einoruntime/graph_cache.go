package einoruntime

import (
	"context"
	"sync"

	"actweave/backend/internal/domain"
	"actweave/backend/internal/workflowtranslator"
)

// GraphCache is a process-local compile cache keyed by workflowtranslator.CacheKey
// (design §4.2). Invalidation on process restart is acceptable; multi-replica
// each maintain their own cache.
type GraphCache struct {
	mu    sync.RWMutex
	items map[string]*CompiledWorkflowGraph

	// Build is used when a key misses. Defaults to BuildWorkflowGraph with
	// the cache's BuildConfig.
	BuildConfig GraphBuildConfig
}

// NewGraphCache constructs an empty compile cache.
func NewGraphCache(cfg GraphBuildConfig) *GraphCache {
	return &GraphCache{
		items:       make(map[string]*CompiledWorkflowGraph),
		BuildConfig: cfg,
	}
}

// CacheKeyFor builds the stable cache key for a plan identity.
// planHash may be empty for trial runs; callers should still pass revision when known.
func CacheKeyFor(workspaceID, revisionID, planHash, engine string) string {
	return workflowtranslator.CacheKeyFrom(workflowtranslator.CacheKeyParts{
		WorkspaceID:   workspaceID,
		RevisionID:    revisionID,
		PlanHash:      planHash,
		EngineVersion: workflowtranslator.GraphEngineVersion,
		Engine:        engine,
	})
}

// Get returns a cached compiled graph when present.
func (c *GraphCache) Get(key string) (*CompiledWorkflowGraph, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	g, ok := c.items[key]
	return g, ok
}

// Put stores a compiled graph under key.
func (c *GraphCache) Put(key string, graph *CompiledWorkflowGraph) {
	if c == nil || graph == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]*CompiledWorkflowGraph)
	}
	c.items[key] = graph
}

// GetOrBuild returns a cached graph or compiles and stores one.
func (c *GraphCache) GetOrBuild(
	ctx context.Context,
	key string,
	plan domain.CompiledExecutionPlan,
) (*CompiledWorkflowGraph, error) {
	if c == nil {
		return BuildWorkflowGraph(ctx, plan, GraphBuildConfig{})
	}
	if key != "" {
		if g, ok := c.Get(key); ok {
			return g, nil
		}
	}
	g, err := BuildWorkflowGraph(ctx, plan, c.BuildConfig)
	if err != nil {
		return nil, err
	}
	if key != "" {
		c.Put(key, g)
	}
	return g, nil
}

// Len returns the number of cached graphs (test helper).
func (c *GraphCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
