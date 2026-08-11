package a2ui

import "strings"

// PrepareOptions controls terminal assistant content materialization for completeRun.
type PrepareOptions struct {
	// EnableA2UI is the run-frozen snapshot flag (sessioncontext.EnableA2UIFromSnapshot).
	EnableA2UI bool
	// ProjectionEnabled is usually a2ui.ProjectionEnabled(); injectable for tests.
	// When false, fences are stripped but a2ui is never persisted.
	ProjectionEnabled bool
}

// PrepareResult is the durable body for RecordAssistantResult plus emit classification.
type PrepareResult struct {
	// Content is always non-empty when Full was non-empty (KD-16 fallback).
	Content string
	// Result is the observability classification.
	Result EmitResult
	// AttachedA2UI is true only when Content is a v1 envelope with an a2ui part.
	AttachedA2UI bool
	// Text is the human-readable text part (outer/stripped); may be empty with a2ui.
	Text string
	// Payload is non-nil only when AttachedA2UI.
	Payload *Payload
}

// PrepareAssistantContent materializes terminal model text into durable chat content.
//
// Call only from completeRun / final assistant persist — never mid-tool intermediate
// turns (design §4.4). Preflight of protocol projection is the caller's
// responsibility when AttachedA2UI is true: if preflight fails, call
// DegradeToTextOnly and persist plain text instead (never SUCCEEDED-without-completed).
func PrepareAssistantContent(full string, opts PrepareOptions) PrepareResult {
	if full == "" {
		return PrepareResult{Content: "", Result: EmitNone}
	}

	text, payload, splitResult := SplitTextAndA2UI(full)

	// No fence → plain full (capability off or model chose text-only).
	if splitResult == EmitNone {
		return PrepareResult{Content: full, Result: EmitNone, Text: full}
	}

	// Emergency env off: strip fences only; never persist a2ui.
	if !opts.ProjectionEnabled {
		plain := NonEmptyOrFallback(text, full)
		return PrepareResult{Content: plain, Result: EmitProjectionOff, Text: plain}
	}

	// Capability off: strip fences if present; never attach a2ui.
	if !opts.EnableA2UI {
		plain := NonEmptyOrFallback(text, full)
		return PrepareResult{Content: plain, Result: EmitStrippedDisabled, Text: plain}
	}

	// Invalid extract → text-only degrade with non-empty fallback.
	switch splitResult {
	case EmitInvalidJSON, EmitTooLarge:
		plain := NonEmptyOrFallback(text, full)
		return PrepareResult{Content: plain, Result: splitResult, Text: plain}
	}

	// Valid payload (ok / ok_empty_text / truncated).
	if payload == nil {
		plain := NonEmptyOrFallback(text, full)
		return PrepareResult{Content: plain, Result: EmitInvalidJSON, Text: plain}
	}

	durable, err := SerializeAssistantDurable(text, payload)
	if err != nil || durable == "" {
		plain := NonEmptyOrFallback(text, full)
		return PrepareResult{Content: plain, Result: EmitInvalidJSON, Text: plain}
	}
	return PrepareResult{
		Content:      durable,
		Result:       splitResult,
		AttachedA2UI: true,
		Text:         text,
		Payload:      payload,
	}
}

// DegradeToTextOnly drops a2ui after preflight failure while keeping non-empty plain text.
func DegradeToTextOnly(full string, prepared PrepareResult) PrepareResult {
	plain := prepared.Text
	if strings.TrimSpace(plain) == "" {
		text, _, _ := SplitTextAndA2UI(full)
		plain = NonEmptyOrFallback(text, full)
	}
	return PrepareResult{
		Content:      plain,
		Result:       EmitProjectionRejected,
		AttachedA2UI: false,
		Text:         plain,
	}
}
