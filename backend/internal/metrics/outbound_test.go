package metrics

import "testing"

func TestOutboundMetricNamesAndLabels(t *testing.T) {
	c := NewAAPCollector()
	c.ObserveOutboundIngestBytes(12)
	c.ObserveOutboundTurnFiles(1)
	c.ObserveOutboundSnapshotFail()
	snap := c.Snapshot()
	if snap.Labeled["aap_outbound_ingest_bytes_total"] != 12 {
		t.Fatalf("ingest bytes total=%v", snap.Labeled)
	}
	if snap.Labeled["aap_outbound_turn_files|bucket=1"] != 1 {
		t.Fatalf("turn files=%v", snap.Labeled)
	}
	if snap.Labeled["aap_outbound_snapshot_fail_total"] != 1 {
		t.Fatalf("snapshot fail=%v", snap.Labeled)
	}
}
