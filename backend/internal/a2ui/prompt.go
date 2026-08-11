package a2ui

import "strings"

// promptAppendixV1 is the platform a2ui-prompt.v1 instruction block (design §3.2).
// Injected once into Bridge.drive instruction when EnableA2UIFromSnapshot (KD-17).
const promptAppendixV1 = `

## A2UI additive surfaces (a2ui-prompt.v1)

Default to natural language. Do **not** attach A2UI on every reply.
Only when a declarative UI is clearly better for the user (forms, structured
choices, multi-field confirmation), append **one** fenced block after your prose:

<<<A2UI>>>
{"version":"a2ui-surface.v0","catalogId":"standard","surface":{/* declarative UI object */}}
<<<END_A2UI>>>

Rules:
- MVP is **display-oriented**. Do not assume buttons submit or that actions are wired.
- Keep surface a single JSON **object** (not array/string). Prefer compact surfaces.
- Prose outside the fence is the human-readable reply; the fence body is not shown as raw JSON to end users.
- At most one A2UI fence per assistant message.
`

// AppendPromptRules appends a2ui-prompt.v1 rules to a system instruction.
// Idempotent for a single drive() inject: callers must not re-apply on resume
// rebuilds (resume skips history assembly and reuses the frozen agent graph).
func AppendPromptRules(instruction string) string {
	// Avoid accidental double-append if a caller reuses an already-injected string.
	// Check before any mutation so a second call is a pure no-op.
	if strings.Contains(instruction, PromptTemplateV1) ||
		strings.Contains(instruction, FenceStart) {
		return instruction
	}
	instruction = strings.TrimRight(instruction, " \t\r\n")
	if instruction == "" {
		return strings.TrimSpace(promptAppendixV1)
	}
	return instruction + promptAppendixV1
}
