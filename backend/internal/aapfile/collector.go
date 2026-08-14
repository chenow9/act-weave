package aapfile

import (
	"context"
	"fmt"
	"sync"
)

// GeneratedLister lists AGENT_OUTPUT files for a run (HITL rebuild).
type GeneratedLister interface {
	ListGeneratedForRun(ctx context.Context, workspaceID, agentID, runID string) ([]File, error)
}

// OutboundCollector tracks per-run publish quota.
// Memory reservations prevent oversell in one drive; Snapshot prefers DB rows.
type OutboundCollector struct {
	mu          sync.Mutex
	lister      GeneratedLister
	workspaceID string
	agentID     string
	runID       string
	maxFiles    int64
	maxBytes    int64
	reservedN   int64
	reservedB   int64
	mem         []File
	rebuilt     bool
}

// NewOutboundCollector builds a collector for one run. Call RebuildFromDB on resume.
func NewOutboundCollector(
	lister GeneratedLister,
	workspaceID, agentID, runID string,
	maxBytes int64,
) *OutboundCollector {
	if maxBytes <= 0 {
		maxBytes = MaxOutboundTurnBytes
	}
	return &OutboundCollector{
		lister:      lister,
		workspaceID: workspaceID,
		agentID:     agentID,
		runID:       runID,
		maxFiles:    MaxOutboundFilesPerTurn,
		maxBytes:    maxBytes,
	}
}

// RebuildFromDB loads used quota from AGENT_OUTPUT rows for this run.
func (c *OutboundCollector) RebuildFromDB(ctx context.Context) error {
	if c == nil {
		return ErrInvalid
	}
	files, err := c.list(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyListed(files)
	return nil
}

// TryReserve reserves file count and bytes for an upcoming ingest.
func (c *OutboundCollector) TryReserve(files, extraBytes int64) error {
	if c == nil {
		return ErrInvalid
	}
	if files < 0 || extraBytes < 0 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reservedN+files > c.maxFiles {
		return fmt.Errorf("%w: %s", ErrFailed, ErrorCodeOutboundTurnLimit)
	}
	if c.reservedB+extraBytes > c.maxBytes {
		return fmt.Errorf("%w: %s", ErrFailed, ErrorCodeSizeExceeded)
	}
	c.reservedN += files
	c.reservedB += extraBytes
	return nil
}

// Release undoes a reservation after ingest or validation failure.
func (c *OutboundCollector) Release(files, extraBytes int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reservedN -= files
	c.reservedB -= extraBytes
	if c.reservedN < 0 {
		c.reservedN = 0
	}
	if c.reservedB < 0 {
		c.reservedB = 0
	}
}

// Remember records a successful ingest for in-drive Snapshot acceleration.
func (c *OutboundCollector) Remember(file File) {
	if c == nil || file.ID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, existing := range c.mem {
		if existing.ID == file.ID {
			return
		}
	}
	c.mem = append(c.mem, file)
}

// Snapshot prefers DB rows for this run; memory is used when the list fails.
func (c *OutboundCollector) Snapshot(ctx context.Context) ([]File, error) {
	if c == nil {
		return nil, ErrInvalid
	}
	files, err := c.list(ctx)
	if err == nil {
		c.mu.Lock()
		c.applyListed(files)
		out := append([]File(nil), c.mem...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Lock()
	out := append([]File(nil), c.mem...)
	c.mu.Unlock()
	if len(out) > 0 {
		return out, nil
	}
	return nil, err
}

func (c *OutboundCollector) list(ctx context.Context) ([]File, error) {
	if c.lister == nil {
		return nil, ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.lister.ListGeneratedForRun(ctx, c.workspaceID, c.agentID, c.runID)
}

func (c *OutboundCollector) applyListed(files []File) {
	var n, b int64
	mem := make([]File, 0, len(files))
	for _, file := range files {
		n++
		b += file.SizeBytes
		mem = append(mem, file)
	}
	// Keep in-drive reservations that have not been listed yet.
	if c.reservedN < n {
		c.reservedN = n
	}
	if c.reservedB < b {
		c.reservedB = b
	}
	c.mem = mem
	c.rebuilt = true
}
