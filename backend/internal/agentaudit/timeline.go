package agentaudit

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// AggregateListItem builds one list row from runs sharing a trace_id.
func AggregateListItem(runs []RunFact, stepCount int) TraceListItem {
	if len(runs) == 0 {
		return TraceListItem{}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
	first, last := runs[0], runs[len(runs)-1]
	item := TraceListItem{
		TraceID:   first.TraceID,
		StartedAt: first.StartedAt,
		Status:    aggregateStatus(runs),
		Model:     modelNameFromSnapshot(last.ModelSnapshot),
		UserLabel: userLabel(first),
		StepCount: stepCount,
		RunIDs:    runIDs(runs),
	}
	if finished, ok := aggregateFinishedAt(runs); ok {
		item.FinishedAt = &finished
		latency := finished.Sub(first.StartedAt).Milliseconds()
		if latency < 0 {
			latency = 0
		}
		item.LatencyMs = &latency
	}
	return item
}

func aggregateStatus(runs []RunFact) string {
	anyRunning := false
	anyFailed := false
	for _, run := range runs {
		switch strings.ToUpper(strings.TrimSpace(run.Status)) {
		case "RUNNING", "WAITING_CONFIRMATION", "WAITING_INTERACTION", "ACCEPTED":
			anyRunning = true
		case "FAILED", "CANCELLED", "TIMED_OUT":
			anyFailed = true
		}
	}
	if anyRunning {
		return "running"
	}
	if anyFailed {
		return "error"
	}
	return "success"
}

func aggregateFinishedAt(runs []RunFact) (time.Time, bool) {
	var latest time.Time
	for _, run := range runs {
		if run.FinishedAt == nil {
			return time.Time{}, false
		}
		if run.FinishedAt.After(latest) {
			latest = *run.FinishedAt
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func runIDs(runs []RunFact) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}

func userLabel(run RunFact) string {
	if strings.TrimSpace(run.TriggeredByID) == "" {
		return run.TriggeredByType
	}
	if run.TriggeredByType != "" {
		return run.TriggeredByType + ":" + run.TriggeredByID
	}
	return run.TriggeredByID
}

func modelNameFromSnapshot(snapshot json.RawMessage) string {
	if len(snapshot) == 0 {
		return "-"
	}
	var parsed map[string]any
	if json.Unmarshal(snapshot, &parsed) != nil {
		return "-"
	}
	for _, key := range []string{"modelName", "model", "name"} {
		if value, ok := parsed[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "-"
}

// BuildTimeline constructs ordered steps for one trace from domain facts.
// debugMode controls whether raw model/tool fields are exposed as plain.
func BuildTimeline(
	runs []RunFact,
	messages []MessageFact,
	steps []StepFact,
	debugMode bool,
) TraceDetail {
	if len(runs) == 0 {
		return TraceDetail{DebugMode: debugMode, Steps: []Step{}}
	}
	list := AggregateListItem(runs, 0)
	base := list.StartedAt
	var out []Step

	// User inputs first (per run, chronological).
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	for _, msg := range messages {
		if !strings.EqualFold(msg.Role, "USER") {
			continue
		}
		content, state := presentText(msg.Content, debugMode, true)
		out = append(out, Step{
			Type: "input", Title: "用户输入",
			TimeOffsetMs: offsetMs(base, msg.CreatedAt),
			Content:      content, ContentState: state, RunID: msg.RunID,
		})
	}

	sort.Slice(steps, func(i, j int) bool {
		if steps[i].StartedAt.Equal(steps[j].StartedAt) {
			if steps[i].SequenceNo != steps[j].SequenceNo {
				return steps[i].SequenceNo < steps[j].SequenceNo
			}
			return steps[i].ID < steps[j].ID
		}
		return steps[i].StartedAt.Before(steps[j].StartedAt)
	})

	for _, step := range steps {
		switch strings.ToUpper(strings.TrimSpace(step.StepType)) {
		case "CONTEXT_COMPACTION":
			out = append(out, withAttribution(compactStep(base, step, debugMode), step))
		case "MODEL":
			out = append(out, withAttribution(modelReasoningStep(base, step, debugMode), step))
		case "TOOL", "WORKFLOW":
			out = append(out, withAttribution(toolStep(base, step, debugMode), step))
		case "AGENT_DELEGATION":
			out = append(out, withAttribution(delegationStep(base, step, debugMode), step))
		default:
			// Unknown step types still surface so audit never silently drops evidence.
			out = append(out, withAttribution(unknownStep(base, step), step))
		}
	}

	for _, msg := range messages {
		if !strings.EqualFold(msg.Role, "ASSISTANT") {
			continue
		}
		content, state := presentText(msg.Content, debugMode, true)
		out = append(out, Step{
			Type: "output", Title: "最终输出",
			TimeOffsetMs: offsetMs(base, msg.CreatedAt),
			Content:      content, ContentState: state, RunID: msg.RunID,
		})
	}

	// Stable timeline by time offset then type order (compact before MODEL).
	typeOrder := map[string]int{
		"input": 0, "context_compaction": 1, "reasoning": 2,
		"agent_delegation": 3, "tool": 4, "output": 5, "unknown": 6,
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TimeOffsetMs != out[j].TimeOffsetMs {
			return out[i].TimeOffsetMs < out[j].TimeOffsetMs
		}
		return typeOrder[out[i].Type] < typeOrder[out[j].Type]
	})

	// Nest steps under AGENT_DELEGATION frames by parent_step_id / delegation_id.
	out = nestDelegationTree(out)

	detail := TraceDetail{
		TraceID: list.TraceID, StartedAt: list.StartedAt, FinishedAt: list.FinishedAt,
		LatencyMs: list.LatencyMs, Status: list.Status, Model: list.Model,
		UserLabel: list.UserLabel, DebugMode: debugMode, Steps: out, RunIDs: list.RunIDs,
		StepTotal: len(out), StepOffset: 0, StepLimit: len(out), HasMore: false,
	}
	return detail
}

func withAttribution(s Step, fact StepFact) Step {
	s.AgentID = fact.AgentID
	s.DelegationID = fact.DelegationID
	s.ParentDelegationID = fact.ParentDelegationID
	s.ParentStepID = fact.ParentStepID
	if s.Status == "" {
		s.Status = fact.Status
	}
	// Propagate joined delegation identity when present (nested MODEL/TOOL under del).
	if fact.ChildRunID != "" && s.ChildRunID == "" {
		s.ChildRunID = fact.ChildRunID
	}
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func delegationStep(base time.Time, step StepFact, debugMode bool) Step {
	title := "Agent 调用"
	// Prefer authoritative agent_run_delegations columns; fall back to input_summary.
	caller, target, mode, protocol, origin := step.CallerAgentID, step.TargetAgentID, step.Mode, step.Protocol, step.Origin
	depth := step.Depth
	extRef, childRun := step.ExternalAgentRef, step.ChildRunID
	var in map[string]any
	_ = json.Unmarshal(step.InputSummary, &in)
	if in != nil {
		if caller == "" {
			caller, _ = in["callerAgentId"].(string)
		}
		if target == "" {
			target, _ = in["targetAgentId"].(string)
		}
		if mode == "" {
			mode, _ = in["mode"].(string)
		}
		if protocol == "" {
			protocol, _ = in["protocol"].(string)
		}
		if origin == "" {
			origin, _ = in["origin"].(string)
		}
		if extRef == "" {
			extRef, _ = in["externalAgentRef"].(string)
		}
		if depth == 0 {
			if d, ok := in["depth"].(float64); ok {
				depth = int(d)
			}
		}
		if childRun == "" {
			if c, ok := in["childRunId"].(string); ok {
				childRun = c
			}
		}
		name, _ := in["callableName"].(string)
		if name != "" {
			title = "Agent 调用: " + name
		}
	}
	// Origin-aware path in title:
	// EXTERNAL inbound: externalAgentRef → targetAgentId
	// INTERNAL: callerAgentId → targetAgentId
	// outbound A2A: callerAgentId → externalAgentRef
	title = title + delegationTitlePath(origin, protocol, caller, target, extRef)
	status := firstNonEmptyStr(step.DelegationStatus, step.Status)
	result := Step{
		Type: "agent_delegation", Title: title,
		TimeOffsetMs: offsetMs(base, step.StartedAt),
		RunID:        step.RunID, StepID: step.ID,
		AgentID: step.AgentID, DelegationID: step.DelegationID,
		ParentDelegationID: step.ParentDelegationID, ParentStepID: step.ParentStepID,
		CallerAgentID: caller, TargetAgentID: target, ExternalRef: extRef,
		Mode: mode, Protocol: protocol, Origin: origin, Depth: depth,
		Status: status, ChildRunID: childRun,
		RemoteTaskID: step.RemoteTaskID, RemoteContextID: step.RemoteContextID,
		RemoteMessageID: step.RemoteMessageID, RemoteEndpointRef: step.RemoteEndpointRef,
		ProtocolStatus: step.ProtocolStatus,
		InputTokens:    step.InputTokens, OutputTokens: step.OutputTokens, TotalTokens: step.TotalTokens,
		TokensKnown: step.TokensKnown,
		// Always emit attempt/retry for real AGENT_DELEGATION (including 0/0 pre-dispatch).
		AttemptCount: intPtr(step.AttemptCount), RetryCount: intPtr(step.RetryCount),
		Collapsed: depth > 0,
	}
	if step.DelegationLatencyMs != nil {
		result.LatencyMs = step.DelegationLatencyMs
	} else if step.FinishedAt != nil {
		latency := step.FinishedAt.Sub(step.StartedAt).Milliseconds()
		if latency < 0 {
			latency = 0
		}
		result.LatencyMs = &latency
	}
	params, pState := presentJSON(nil, step.InputSummary, debugMode, true)
	out, oState := presentJSON(nil, step.OutputSummary, debugMode, true)
	result.Params, result.ParamsState = params, pState
	result.Result, result.ResultState = out, oState
	result.ErrorCode = firstNonEmptyStr(step.DelegationErrorCode)
	result.ErrorMessage = firstNonEmptyStr(step.DelegationErrorMsg)
	var outMap map[string]any
	if json.Unmarshal(step.OutputSummary, &outMap) == nil {
		if result.ErrorCode == "" {
			if c, ok := outMap["errorCode"].(string); ok {
				result.ErrorCode = c
			}
		}
		if result.ErrorMessage == "" {
			if m, ok := outMap["message"].(string); ok {
				result.ErrorMessage = m
			}
		}
	}
	if strings.EqualFold(status, "FAILED") || strings.EqualFold(status, "CANCELLED") ||
		strings.EqualFold(status, "TIMED_OUT") {
		result.Title = result.Title + " [" + strings.ToUpper(status) + "]"
	}
	return result
}

func unknownStep(base time.Time, step StepFact) Step {
	return Step{
		Type: "unknown", Title: "步骤: " + step.StepType,
		TimeOffsetMs: offsetMs(base, step.StartedAt),
		RunID:        step.RunID, StepID: step.ID, Status: step.Status,
		Params: step.InputSummary, Result: step.OutputSummary,
	}
}

// delegationTitlePath formats the human path segment for agent_delegation titles.
// EXTERNAL: externalAgentRef → target; INTERNAL: caller → target;
// outbound A2A (non-EXTERNAL with extRef): caller → externalAgentRef.
func delegationTitlePath(origin, protocol, caller, target, extRef string) string {
	origin = strings.ToUpper(strings.TrimSpace(origin))
	protocol = strings.ToUpper(strings.TrimSpace(protocol))
	caller, target, extRef = strings.TrimSpace(caller), strings.TrimSpace(target), strings.TrimSpace(extRef)
	switch {
	case origin == "EXTERNAL":
		if extRef != "" && target != "" {
			return " (" + extRef + " → " + shortID(target) + ")"
		}
		if extRef != "" {
			return " → " + extRef
		}
		if target != "" {
			return " → " + shortID(target)
		}
	case protocol == "A2A" && extRef != "" && origin != "EXTERNAL":
		if caller != "" {
			return " (" + shortID(caller) + " → " + extRef + ")"
		}
		return " → " + extRef
	default:
		if caller != "" && target != "" {
			return " (" + shortID(caller) + " → " + shortID(target) + ")"
		}
		if target != "" {
			return " → " + shortID(target)
		}
	}
	return ""
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// nestDelegationTree moves steps with parentStepId / parent_delegation under
// their AGENT_DELEGATION parent. Nested agent_delegation frames (A→B→C) attach
// recursively so depth is preserved for any call chain.
func nestDelegationTree(flat []Step) []Step {
	byID := map[string]int{}
	for i := range flat {
		if flat[i].StepID != "" {
			byID[flat[i].StepID] = i
		}
	}
	// Also index by delegationId → agent_delegation step.
	byDel := map[string]int{}
	for i := range flat {
		if flat[i].Type == "agent_delegation" && flat[i].DelegationID != "" {
			byDel[flat[i].DelegationID] = i
		}
		// Delegation step's own StepID is the parent for nested children.
		if flat[i].Type == "agent_delegation" && flat[i].StepID != "" {
			byDel["step:"+flat[i].StepID] = i
		}
	}
	attached := map[int]bool{}
	for i := range flat {
		if flat[i].Type == "input" || flat[i].Type == "output" {
			continue
		}
		// Root-level agent_delegation (no parent) stays at top; nested ones attach.
		parentIdx := -1
		if flat[i].ParentStepID != "" {
			if idx, ok := byID[flat[i].ParentStepID]; ok && flat[idx].Type == "agent_delegation" {
				parentIdx = idx
			}
		}
		if parentIdx < 0 && flat[i].DelegationID != "" && flat[i].Type != "agent_delegation" {
			if idx, ok := byDel[flat[i].DelegationID]; ok {
				parentIdx = idx
			}
		}
		// Nested agent_delegation: ParentStepID (INLINE) or ParentDelegationID (TASK).
		if parentIdx < 0 && flat[i].Type == "agent_delegation" && flat[i].ParentStepID != "" {
			if idx, ok := byID[flat[i].ParentStepID]; ok && flat[idx].Type == "agent_delegation" {
				parentIdx = idx
			}
		}
		if parentIdx < 0 && flat[i].Type == "agent_delegation" && flat[i].ParentDelegationID != "" {
			if idx, ok := byDel[flat[i].ParentDelegationID]; ok {
				parentIdx = idx
			}
		}
		if parentIdx < 0 || parentIdx == i {
			continue
		}
		// Copy value before append to avoid aliasing issues when nesting recursively.
		child := flat[i]
		flat[parentIdx].Children = append(flat[parentIdx].Children, child)
		attached[i] = true
	}
	if len(attached) == 0 {
		return flat
	}
	// Reconstruct tree by walking non-attached roots and collecting children by parent map.
	childrenOf := map[int][]int{}
	for i := range flat {
		if !attached[i] {
			continue
		}
		parentIdx := -1
		if flat[i].ParentStepID != "" {
			if idx, ok := byID[flat[i].ParentStepID]; ok && flat[idx].Type == "agent_delegation" {
				parentIdx = idx
			}
		}
		if parentIdx < 0 && flat[i].DelegationID != "" && flat[i].Type != "agent_delegation" {
			if idx, ok := byDel[flat[i].DelegationID]; ok {
				parentIdx = idx
			}
		}
		if parentIdx < 0 && flat[i].Type == "agent_delegation" && flat[i].ParentDelegationID != "" {
			if idx, ok := byDel[flat[i].ParentDelegationID]; ok {
				parentIdx = idx
			}
		}
		if parentIdx >= 0 {
			childrenOf[parentIdx] = append(childrenOf[parentIdx], i)
		}
	}
	var build func(idx int) Step
	build = func(idx int) Step {
		s := flat[idx]
		s.Children = nil
		for _, cIdx := range childrenOf[idx] {
			s.Children = append(s.Children, build(cIdx))
		}
		return s
	}
	out := make([]Step, 0, len(flat)-len(attached))
	for i := range flat {
		if attached[i] {
			continue
		}
		if flat[i].Type == "agent_delegation" {
			out = append(out, build(i))
		} else {
			out = append(out, flat[i])
		}
	}
	return out
}

// PageTimelineSteps returns one page of the built timeline without reordering.
// limit/offset apply to the final ordered Steps slice (input/reasoning/tool/output).
func PageTimelineSteps(detail TraceDetail, filter DetailFilter) TraceDetail {
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultDetailStepLimit
	}
	if limit > MaxDetailStepLimit {
		limit = MaxDetailStepLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	total := len(detail.Steps)
	detail.StepTotal = total
	detail.StepLimit = limit
	detail.StepOffset = offset
	if total == 0 || offset >= total {
		detail.Steps = []Step{}
		detail.HasMore = false
		return detail
	}
	end := offset + limit
	if end > total {
		end = total
	}
	// Copy slice window so callers can retain the full list if needed.
	page := make([]Step, end-offset)
	copy(page, detail.Steps[offset:end])
	detail.Steps = page
	detail.HasMore = end < total
	return detail
}

func modelReasoningStep(base time.Time, step StepFact, debugMode bool) Step {
	result := Step{
		Type: "reasoning", Title: "大模型推理",
		TimeOffsetMs: offsetMs(base, step.StartedAt),
		RunID:        step.RunID, StepID: step.ID,
	}
	if step.FinishedAt != nil {
		latency := step.FinishedAt.Sub(step.StartedAt).Milliseconds()
		if latency < 0 {
			latency = 0
		}
		result.LatencyMs = &latency
	}
	reasoning := ""
	if step.ModelTurn != nil {
		if value, ok := step.ModelTurn["reasoning"].(string); ok {
			reasoning = strings.TrimSpace(value)
		}
	}
	if reasoning == "" {
		result.Content = MissingReasoningText
		result.ContentState = ContentMissing
		return result
	}
	if !debugMode {
		result.Content = MissingReasoningText
		result.ContentState = ContentRedacted
		return result
	}
	result.Content = reasoning
	result.ContentState = ContentPlain
	return result
}

// compactStep renders CONTEXT_COMPACTION permanently visible metadata.
// Summary body is never taken from protocol JSONB or step output_summary text fields.
// Content stays empty here; Service.hydrateCompactSummaryBodies fills it only when
// server debugMode=true via SummaryBodyReader(ADMIN_AUDIT) on encrypted objects.
func compactStep(base time.Time, step StepFact, debugMode bool) Step {
	const fallbackTitle = "上下文 Compact 失败；已退化为 token_window"
	_ = debugMode // body hydration is service-layer only (debug gate + reader)
	result := Step{
		Type: "context_compaction", Title: "上下文 Compact",
		TimeOffsetMs: offsetMs(base, step.StartedAt),
		RunID:        step.RunID, StepID: step.ID,
		ContentState: ContentRedacted,
	}
	if step.FinishedAt != nil {
		latency := step.FinishedAt.Sub(step.StartedAt).Milliseconds()
		if latency < 0 {
			latency = 0
		}
		result.LatencyMs = &latency
	}
	// Parse body-free output_summary for fixed metadata.
	var out map[string]any
	_ = json.Unmarshal(step.OutputSummary, &out)
	// Protocol canary: even if a buggy writer put "summary" in output_summary, ignore it.
	delete(out, "summary")
	delete(out, "injectedSummary")
	delete(out, "body")
	resStr, _ := out["result"].(string)
	switch strings.ToLower(strings.TrimSpace(resStr)) {
	case "fallback":
		result.Title = fallbackTitle
	case "failed":
		result.Title = "上下文 Compact 失败"
	case "completed":
		result.Title = "上下文 Compact 完成"
	default:
		if strings.EqualFold(step.Status, "FAILED") {
			result.Title = fallbackTitle
		}
	}
	// Params always body-free metadata (safe for non-debug and for UI mask).
	meta := map[string]any{
		"result": resStr, "status": step.Status,
	}
	for _, key := range []string{
		"fallbackFrom", "fallbackTo", "fallbackStage", "errorCode",
		"beforeTokens", "afterTokens", "passes", "reused", "summaryId", "summaryDigest",
	} {
		if v, ok := out[key]; ok {
			meta[key] = v
		}
	}
	// Fixed D6-A fields when fallback.
	if strings.EqualFold(resStr, "fallback") {
		if meta["fallbackFrom"] == nil {
			meta["fallbackFrom"] = "rolling_summary"
		}
		if meta["fallbackTo"] == nil {
			meta["fallbackTo"] = "token_window"
		}
	}
	raw, _ := json.Marshal(meta)
	result.Params = raw
	result.ParamsState = ContentPlain
	// Never put summary body into Content from step output (protocol bypass guard).
	result.Content = ""
	return result
}

func toolStep(base time.Time, step StepFact, debugMode bool) Step {
	name := strings.TrimSpace(step.ToolName)
	if name == "" {
		name = toolNameFromSummary(step.InputSummary)
	}
	if name == "" {
		name = "tool"
	}
	result := Step{
		Type: "tool", Title: "工具调用: " + name,
		TimeOffsetMs: offsetMs(base, step.StartedAt),
		RunID:        step.RunID, StepID: step.ID, InvocationID: step.InvocationID,
	}
	if step.FinishedAt != nil {
		latency := step.FinishedAt.Sub(step.StartedAt).Milliseconds()
		if latency < 0 {
			latency = 0
		}
		result.LatencyMs = &latency
	}

	params, paramsState := presentJSON(step.ToolParams, step.InputSummary, debugMode, step.ToolPayloadAvailable)
	toolResult, resultState := presentJSON(step.ToolResult, step.OutputSummary, debugMode, step.ToolPayloadAvailable)
	result.Params = params
	result.ParamsState = paramsState
	result.Result = toolResult
	result.ResultState = resultState
	return result
}

func toolNameFromSummary(summary json.RawMessage) string {
	var parsed map[string]any
	if json.Unmarshal(summary, &parsed) != nil {
		return ""
	}
	for _, key := range []string{"callableName", "name", "toolName"} {
		if value, ok := parsed[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func presentText(content string, debugMode, available bool) (string, ContentState) {
	content = strings.TrimSpace(content)
	if content == "" {
		if !available {
			return "（密文不可读）", ContentCipher
		}
		return "", ContentMissing
	}
	if debugMode {
		return content, ContentPlain
	}
	if len([]rune(content)) > 80 {
		return string([]rune(content)[:80]) + "…", ContentRedacted
	}
	return content, ContentRedacted
}

func presentJSON(
	preferred json.RawMessage,
	fallback json.RawMessage,
	debugMode bool,
	rawAvailable bool,
) (json.RawMessage, ContentState) {
	if debugMode {
		if len(preferred) > 0 && string(preferred) != "null" {
			return preferred, ContentPlain
		}
		if len(fallback) > 0 && string(fallback) != "null" {
			return fallback, ContentPlain
		}
		if !rawAvailable {
			return json.RawMessage(`{"_state":"cipher"}`), ContentCipher
		}
		return json.RawMessage(`{}`), ContentMissing
	}
	// debug off: only fallback summary, never preferred raw bodies.
	if len(fallback) > 0 && string(fallback) != "null" {
		return redactJSON(fallback), ContentRedacted
	}
	return json.RawMessage(`{"_state":"redacted"}`), ContentRedacted
}

func redactJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{"_state":"redacted"}`)
	}
	return mustRaw(maskValue(value))
}

func maskValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey(key) {
				out[key] = "********"
				continue
			}
			out[key] = maskValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = maskValue(child)
		}
		return out
	default:
		return typed
	}
}

func sensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, needle := range []string{
		"password", "secret", "token", "authorization", "api_key", "apikey",
		"private_key", "credential", "cookie", "email", "phone", "body", "to",
	} {
		if lower == needle || strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func mustRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func offsetMs(base, at time.Time) int64 {
	if at.IsZero() || base.IsZero() {
		return 0
	}
	ms := at.Sub(base).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func intPtr(v int) *int {
	return &v
}
