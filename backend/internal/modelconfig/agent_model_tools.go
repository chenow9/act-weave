package modelconfig

// AgentModelToolCheck is the catalog/edge snapshot for a bind or model-swap gate.
// CatalogCount is len(ListForAgent), or the proposed count after Bind.
// Delegation edges make the Agent tool-bearing but are not added to CatalogCount.
type AgentModelToolCheck struct {
	AgentID            string
	CatalogCount       int
	HasDelegationEdges bool
	// RequireVerified is true when swapping modelConfigId on a tool-bearing
	// Agent: the target must be VERIFIED and toolCalling != none. Bind only
	// rejects parsed toolCalling=none; {} / unverified is not none.
	RequireVerified bool
}

// AssertAgentModelToolCompatibility fails closed when a tool-bearing Agent
// would be paired with a model that cannot call tools, or when carry-all
// would exceed CarryAllHardLimit.
//
// Empty capability documents never count as native. none / missing verified
// tool capability is evaluated before the carry-all hard limit so the error
// code matches the capability defect.
func AssertAgentModelToolCompatibility(cfg Config, check AgentModelToolCheck) error {
	toolBearing := check.CatalogCount > 0 || check.HasDelegationEdges
	if !toolBearing {
		return nil
	}

	calling, parsed := probeToolCalling(cfg)
	if check.RequireVerified {
		if cfg.Status != StatusVerified || !parsed || calling == ToolCallingNone {
			return ErrAgentModelToolsUnsupported
		}
	} else if parsed && calling == ToolCallingNone {
		return ErrAgentModelToolsUnsupported
	}

	if check.CatalogCount <= CarryAllHardLimit {
		return nil
	}
	policy, _, err := ParseToolDisclosurePolicy(cfg.ToolDisclosurePolicy)
	if err != nil {
		return err
	}
	if policy.Mode != DisclosureModeCarryAll {
		return nil
	}
	return CarryAllTooLargeError{
		AgentID: check.AgentID,
		Count:   check.CatalogCount,
		Limit:   CarryAllHardLimit,
	}
}

// probeToolCalling reports the parsed toolCalling value. {} / unverified /
// unreadable documents return parsed=false and must not be treated as native.
func probeToolCalling(cfg Config) (string, bool) {
	if IsUnverifiedAgenticCapabilities(cfg.AgenticCapabilities) {
		return "", false
	}
	caps, _, err := ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil || caps.ToolCalling == "" {
		return "", false
	}
	return caps.ToolCalling, true
}
