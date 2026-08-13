# demos/aap-chat Natural Conversation E2E (corrected methodology)

| Field | Value |
|-------|-------|
| **Date** | 2026-08-12 |
| **Method** | Natural user messages only — **no** forced fence copy-paste |
| **Agent** | 平台助手 with `enableA2UI: true` (run snapshot frozen) |
| **Demo** | Live AAP `http://127.0.0.1:5188` |
| **No deletes** | Yes |

## Why previous test was wrong

Earlier e2e told the model to copy a fixed `<<<A2UI>>>` block verbatim. That only proved extract/render plumbing, **not** “LLM decides A2UI when useful”.

Correct product path (prompt appendix `a2ui-prompt.v1`):

- Default natural language
- Attach A2UI only when declarative UI is clearly better (forms, structured fields)

## Turns

### Turn 1 — business request (no UI keyword)

**User:**  
「我想预约一次产品演示，需要登记：联系人姓名、公司名称、手机号、以及希望演示的日期。请帮我处理。」

**Result:** Run completed, **text only** (markdown-style blank fields).  
`multiparty_a2ui = false`  
→ Model chose **not** to emit A2UI (valid under “do not attach on every reply”).

### Turn 2 — still natural, stronger form intent

**User:**  
「请用一个结构化表单收集这些信息，我在对话里不好逐项打字。字段：姓名、公司、手机、演示日期。」

**Result:** Run completed with:

- Prose: 「可以，直接在下面表单里填写这 4 项信息…」
- Demo **A2UI display-only card** with surface:
  - type: form
  - title: 产品演示预约登记
  - fields: name / company / mobile / demoDate

→ Model **chose A2UI** without being given fence JSON to copy.

## Verdict

| Claim | Status |
|-------|--------|
| enableA2UI config active for live runs | PASS |
| Normal chat works without forcing A2UI | PASS (turn 1 text-only) |
| LLM can opt into A2UI for form-like needs | PASS (turn 2) |
| Demo shows text + A2UI preview card | PASS |
| Forced fence copy is not a valid product e2e | Acknowledged; superseded by this run |

**Overall: methodology-corrected demos E2E PASS.**
