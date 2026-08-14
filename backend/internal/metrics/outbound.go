package metrics

// ObserveOutboundPublish increments aap_outbound_publish_total{result}.
// result ∈ {ok, denied, error, disabled, unsupported}.
func (c *AAPCollector) ObserveOutboundPublish(result string) {
	if c == nil {
		return
	}
	switch result {
	case "ok", "denied", "error", "disabled", "unsupported":
	default:
		result = "error"
	}
	c.labeled.add("aap_outbound_publish_total", map[string]string{"result": result}, 1)
}

// ObserveOutboundAttachPreflightFail increments aap_outbound_attach_preflight_fail_total.
func (c *AAPCollector) ObserveOutboundAttachPreflightFail() {
	if c == nil {
		return
	}
	c.labeled.add("aap_outbound_attach_preflight_fail_total", nil, 1)
}

// ObserveOutboundTurnFiles records per-turn attached file count (0/1/2/4/8 buckets).
func (c *AAPCollector) ObserveOutboundTurnFiles(n int) {
	if c == nil {
		return
	}
	bucket := "0"
	switch {
	case n <= 0:
		bucket = "0"
	case n == 1:
		bucket = "1"
	case n == 2:
		bucket = "2"
	case n <= 4:
		bucket = "4"
	default:
		bucket = "8"
	}
	c.labeled.add("aap_outbound_turn_files", map[string]string{"result": bucket}, 1)
}

// ObserveOutboundIngestBytes records one ingest size sample.
func (c *AAPCollector) ObserveOutboundIngestBytes(n int64) {
	if c == nil || n < 0 {
		return
	}
	c.labeled.add("aap_outbound_ingest_bytes", nil, uint64(n))
}
