package aapfile

import (
	"context"
	"testing"
)

func TestOutboundCollectorRebuildAndSnapshotPrefersDB(t *testing.T) {
	name := "a.csv"
	listed := []File{
		{ID: "019f0000-0000-7000-8000-00000000f001", Filename: &name, SizeBytes: 10},
		{ID: "019f0000-0000-7000-8000-00000000f002", Filename: &name, SizeBytes: 20},
	}
	c := NewOutboundCollector(&memLister{files: listed}, publishTestWorkspace, publishTestAgent, publishTestRun, MaxOutboundTurnBytes)
	if err := c.RebuildFromDB(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.TryReserve(MaxOutboundFilesPerTurn, 1); err == nil {
		t.Fatal("expected turn limit after rebuild")
	}
	if err := c.TryReserve(1, 1); err != nil {
		t.Fatalf("two used, six remaining: %v", err)
	}
	c.Release(1, 1)
	got, err := c.Snapshot(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("snapshot=%v err=%v", got, err)
	}
}

func TestOutboundCollectorReleaseOnOverflow(t *testing.T) {
	c := NewOutboundCollector(&memLister{}, publishTestWorkspace, publishTestAgent, publishTestRun, 10)
	if err := c.TryReserve(1, 11); err == nil {
		t.Fatal("expected byte overflow")
	}
	if err := c.TryReserve(1, 4); err != nil {
		t.Fatal(err)
	}
}
