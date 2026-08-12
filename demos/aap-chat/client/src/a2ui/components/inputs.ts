/**
 * Input renderers. Fields are fillable so the surface feels like a real form,
 * but nothing is ever submitted: the catalog defines no action, and AAP
 * advertises actions:false.
 */

import type { A2UIComponentNode, A2UIRenderCtx } from "../generated/catalog.gen";
import { A2UI_ENUMS } from "../generated/catalog.gen";
import { escapeAttr, escapeHtml } from "../html";

type TextFieldVariant = (typeof A2UI_ENUMS.TextField.variant)[number];
type DateTimeMode = (typeof A2UI_ENUMS.DateTimeInput.mode)[number];

// Total records: adding a variant to the catalog fails the build here rather than
// silently falling back to a plain text box.
const TEXT_FIELD_TYPES: Record<TextFieldVariant, string> = {
  shortText: "text",
  longText: "textarea",
  number: "number",
  email: "email",
  tel: "tel",
  date: "date",
  password: "password",
};

const DATE_TIME_TYPES: Record<DateTimeMode, string> = {
  date: "date",
  time: "time",
  datetime: "datetime-local",
};

export function renderTextField(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const id = controlId(node);
  const label = labelFor(id, ctx.resolveString(node.label), node.required === true);
  const placeholder = ctx.resolveString(node.placeholder);
  const value = ctx.resolveString(node.value);
  const variant = isTextFieldVariant(node.variant) ? node.variant : "shortText";
  const type = TEXT_FIELD_TYPES[variant];
  const required = node.required === true ? " required" : "";

  if (type === "textarea") {
    return `<div class="a2ui-field">
      ${label}
      <textarea id="${escapeAttr(id)}" class="a2ui-input" rows="3" placeholder="${escapeAttr(placeholder)}"${required}>${escapeHtml(value)}</textarea>
    </div>`;
  }
  return `<div class="a2ui-field">
    ${label}
    <input id="${escapeAttr(id)}" class="a2ui-input" type="${escapeAttr(type)}" value="${escapeAttr(value)}" placeholder="${escapeAttr(placeholder)}"${required} />
  </div>`;
}

export function renderCheckBox(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const id = controlId(node);
  const checked = ctx.resolveBoolean(node.value) ? " checked" : "";
  return `<div class="a2ui-field a2ui-field-check">
    <label class="a2ui-check-label" for="${escapeAttr(id)}">
      <input type="checkbox" id="${escapeAttr(id)}"${checked} />
      <span>${escapeHtml(ctx.resolveString(node.label))}</span>
    </label>
  </div>`;
}

export function renderChoicePicker(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const id = controlId(node);
  const label = labelFor(id, ctx.resolveString(node.label), false);
  const selected = new Set(ctx.resolveChoiceValues(node.value));
  const multiple = node.multiple === true;
  const options = Array.isArray(node.options) ? node.options : [];

  const rendered = options
    .flatMap((option) => {
      if (typeof option !== "object" || option === null) return [];
      const { value, label: optionLabel } = option as Record<string, unknown>;
      if (typeof value !== "string" || typeof optionLabel !== "string") return [];
      return [
        `<option value="${escapeAttr(value)}"${selected.has(value) ? " selected" : ""}>${escapeHtml(optionLabel)}</option>`,
      ];
    })
    .join("");

  return `<div class="a2ui-field">
    ${label}
    <select id="${escapeAttr(id)}" class="a2ui-input"${multiple ? ' multiple size="4"' : ""}>
      ${multiple ? "" : `<option value="">请选择…</option>`}
      ${rendered || `<option value="" disabled>（无选项）</option>`}
    </select>
  </div>`;
}

export function renderDateTimeInput(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const id = controlId(node);
  const label = labelFor(id, ctx.resolveString(node.label), false);
  const mode = isDateTimeMode(node.mode) ? node.mode : "date";
  return `<div class="a2ui-field">
    ${label}
    <input id="${escapeAttr(id)}" class="a2ui-input" type="${escapeAttr(DATE_TIME_TYPES[mode])}" value="${escapeAttr(ctx.resolveString(node.value))}" />
  </div>`;
}

/**
 * Buttons are display-only. The disabled state and the title are the honest
 * signal that pressing it can do nothing.
 */
export function renderButton(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const label = ctx.resolveString(node.label);
  const primary = node.variant === "primary" ? " a2ui-btn-primary" : node.variant === "borderless" ? " a2ui-btn-borderless" : "";
  return `<div class="a2ui-field a2ui-field-action">
    <button type="button" class="a2ui-btn${primary}" disabled data-a2ui-action title="此 surface 为展示态，未接入 UI 动作">${escapeHtml(label)}</button>
  </div>`;
}

function controlId(node: A2UIComponentNode): string {
  return `a2ui-${node.id}`;
}

function labelFor(id: string, text: string, required: boolean): string {
  const mark = required ? `<span class="a2ui-req" aria-hidden="true">*</span>` : "";
  return `<label class="a2ui-label" for="${escapeAttr(id)}">${escapeHtml(text)}${mark}</label>`;
}

function isTextFieldVariant(value: unknown): value is TextFieldVariant {
  return typeof value === "string" && (A2UI_ENUMS.TextField.variant as readonly string[]).includes(value);
}

function isDateTimeMode(value: unknown): value is DateTimeMode {
  return typeof value === "string" && (A2UI_ENUMS.DateTimeInput.mode as readonly string[]).includes(value);
}
