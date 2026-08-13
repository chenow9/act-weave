package chatruntimebridge

import (
	"context"
	"encoding/json"
	"errors"

	"actweave/backend/internal/contextwindow"
	"actweave/backend/internal/einoruntime"
	"actweave/backend/internal/execution"
	"actweave/backend/internal/metrics"
	"actweave/backend/internal/modelconfig"
)

// resolveDisclosureMode is the single root+child mapping from frozen
// capabilities, user policy, and catalog to a builder ToolSearchMode.
// It does not consult runtime.toolDisclosure; callers apply that gate after
// selecting platform_bounded or carry_all.
func resolveDisclosureMode(
	caps modelconfig.AgenticCapabilities,
	policy modelconfig.ToolDisclosurePolicy,
	catalog *einoruntime.ToolCatalogSnapshot,
) (einoruntime.ToolSearchMode, error) {
	calling := caps.ToolCalling
	if calling == "" && caps.SchemaVersion == modelconfig.AgenticCapabilitiesSchemaV1 {
		calling = modelconfig.ToolCallingNativeClientSearch
	}
	empty := catalogIsEmpty(catalog)
	switch calling {
	case modelconfig.ToolCallingNativeClientSearch:
		if policy.SchemaVersion != "" || policy.Mode != "" {
			return "", modelconfig.ErrToolDisclosureInvalid
		}
		return einoruntime.ToolSearchModeClientBounded, nil
	case modelconfig.ToolCallingFunctionCalling:
		if empty {
			return einoruntime.ToolSearchModeNone, nil
		}
		switch policy.Mode {
		case "", modelconfig.DisclosureModePlatformOnDemand:
			return einoruntime.ToolSearchModePlatformBounded, nil
		case modelconfig.DisclosureModeCarryAll:
			if businessToolCount(catalog) > modelconfig.CarryAllHardLimit {
				return "", modelconfig.ErrToolCarryAllTooLarge
			}
			return einoruntime.ToolSearchModeCarryAll, nil
		default:
			return "", modelconfig.ErrToolDisclosureInvalid
		}
	case modelconfig.ToolCallingNone:
		if !empty {
			return "", modelconfig.ErrAgentModelToolsUnsupported
		}
		return einoruntime.ToolSearchModeNone, nil
	default:
		return "", modelconfig.ErrToolDisclosureInvalid
	}
}

func catalogIsEmpty(catalog *einoruntime.ToolCatalogSnapshot) bool {
	return catalog == nil || catalog.Len() == 0
}

func businessToolCount(catalog *einoruntime.ToolCatalogSnapshot) int {
	if catalog == nil {
		return 0
	}
	n := 0
	for _, e := range catalog.Entries() {
		if !e.PlatformControl {
			n++
		}
	}
	return n
}

func parseFrozenDisclosureInputs(cfg modelconfig.Config) (modelconfig.AgenticCapabilities, modelconfig.ToolDisclosurePolicy, error) {
	caps, _, err := modelconfig.ParseAgenticCapabilities(cfg.AgenticCapabilities)
	if err != nil {
		return modelconfig.AgenticCapabilities{}, modelconfig.ToolDisclosurePolicy{}, err
	}
	policy, _, err := modelconfig.ParseToolDisclosurePolicy(cfg.ToolDisclosurePolicy)
	if err != nil {
		return modelconfig.AgenticCapabilities{}, modelconfig.ToolDisclosurePolicy{}, err
	}
	return caps, policy, nil
}

func (b *Bridge) applyDisclosureRollout(workspaceID string, mode einoruntime.ToolSearchMode) error {
	switch mode {
	case einoruntime.ToolSearchModePlatformBounded, einoruntime.ToolSearchModeCarryAll:
		if b == nil || !b.toolDisclosure.AllowsWorkspace(workspaceID) {
			return modelconfig.ErrToolDisclosureNotRolledOut
		}
	}
	return nil
}

func (b *Bridge) resolveFrozenDisclosure(
	workspaceID string,
	cfg modelconfig.Config,
	catalog *einoruntime.ToolCatalogSnapshot,
) (einoruntime.ToolSearchMode, string, error) {
	caps, policy, err := parseFrozenDisclosureInputs(cfg)
	if err != nil {
		return "", "", err
	}
	mode, err := resolveDisclosureMode(caps, policy, catalog)
	if err != nil {
		if errors.Is(err, modelconfig.ErrToolCarryAllTooLarge) {
			metrics.Disclosure().ObserveRejected(metrics.DisclosureCodeCarryAllTooLarge)
			metrics.Disclosure().ObserveCarryAllRejected(metrics.DisclosureGateRunStart)
		}
		return "", "", err
	}
	if err := b.applyDisclosureRollout(workspaceID, mode); err != nil {
		if errors.Is(err, modelconfig.ErrToolDisclosureNotRolledOut) {
			metrics.Disclosure().ObserveRejected(metrics.DisclosureCodeNotRolledOut)
		}
		return "", "", err
	}
	return mode, caps.ToolCalling, nil
}

type observeDisclosureAssemblyKey struct{}

func withDisclosureAssemblyObserve(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, observeDisclosureAssemblyKey{}, true)
}

func observeDisclosureAssembly(ctx context.Context, mode einoruntime.ToolSearchMode, toolCalling string) {
	if ctx == nil {
		return
	}
	ok, _ := ctx.Value(observeDisclosureAssemblyKey{}).(bool)
	if !ok {
		return
	}
	metrics.Disclosure().ObserveModeRun(string(mode), toolCalling)
}

func disclosureVerifiedFlags(mode einoruntime.ToolSearchMode, hasTools bool) (clientVerified, functionCallingVerified bool) {
	switch mode {
	case einoruntime.ToolSearchModeClientBounded:
		return hasTools, false
	case einoruntime.ToolSearchModePlatformBounded, einoruntime.ToolSearchModeCarryAll:
		return false, true
	default:
		return false, false
	}
}

func promptCacheDisclosureMode(mode einoruntime.ToolSearchMode) string {
	switch mode {
	case einoruntime.ToolSearchModePlatformBounded, einoruntime.ToolSearchModeCarryAll, einoruntime.ToolSearchModeNone:
		return string(mode)
	default:
		return ""
	}
}

func assemblyFieldsForDisclosure(mode einoruntime.ToolSearchMode) (searchMode, estimatorVersion string) {
	switch mode {
	case einoruntime.ToolSearchModePlatformBounded:
		return execution.AssemblyToolSearchModePlatformBounded, contextwindow.EstimatorVersionAgenticOpenAIResponsesV2
	case einoruntime.ToolSearchModeCarryAll:
		return execution.AssemblyToolSearchModeCarryAll, contextwindow.EstimatorVersionAgenticOpenAIResponsesV2
	case einoruntime.ToolSearchModeNone:
		return execution.AssemblyToolSearchModeNone, contextwindow.EstimatorVersionAgenticOpenAIResponsesV2
	default:
		return execution.AssemblyToolSearchModeClientBounded, contextwindow.EstimatorVersionAgenticOpenAIResponsesV1
	}
}

func toolExposureForDisclosure(catalog *einoruntime.ToolCatalogSnapshot, mode einoruntime.ToolSearchMode) contextwindow.ToolExposureEstimate {
	switch mode {
	case einoruntime.ToolSearchModeNone:
		return contextwindow.ToolExposureEstimate{DisclosureMode: contextwindow.DisclosureModeNone}
	case einoruntime.ToolSearchModeCarryAll:
		return toolExposureCarryAll(catalog)
	case einoruntime.ToolSearchModePlatformBounded:
		exp := toolExposureFromCatalog(catalog)
		name, desc, params := einoruntime.PlatformCatalogSearchEstimate()
		exp.Immediate = append([]contextwindow.ToolSchema{{
			Name: name, Description: desc, Parameters: params,
		}}, exp.Immediate...)
		exp.DisclosureMode = contextwindow.DisclosureModePlatformBounded
		return exp
	default:
		exp := toolExposureFromCatalog(catalog)
		exp.DisclosureMode = contextwindow.DisclosureModeClientBounded
		return exp
	}
}

func toolExposureCarryAll(catalog *einoruntime.ToolCatalogSnapshot) contextwindow.ToolExposureEstimate {
	out := contextwindow.ToolExposureEstimate{DisclosureMode: contextwindow.DisclosureModeCarryAll}
	if catalog == nil || catalog.Len() == 0 {
		return out
	}
	for _, e := range catalog.Entries() {
		params := e.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		out.Immediate = append(out.Immediate, contextwindow.ToolSchema{
			Name: e.Name, Description: e.Description, Parameters: append(json.RawMessage(nil), params...),
		})
	}
	return out
}

type frozenDisclosureKey struct{}

type frozenDisclosure struct {
	Mode        einoruntime.ToolSearchMode
	ToolCalling string
}

func withFrozenDisclosure(ctx context.Context, d frozenDisclosure) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, frozenDisclosureKey{}, d)
}

func frozenDisclosureFrom(ctx context.Context) (frozenDisclosure, bool) {
	if ctx == nil {
		return frozenDisclosure{}, false
	}
	d, ok := ctx.Value(frozenDisclosureKey{}).(frozenDisclosure)
	return d, ok
}
