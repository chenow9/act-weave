/** Container and separator renderers. Children are always resolved by id. */

import { A2UI_ENUMS, type A2UIComponentNode, type A2UIRenderCtx } from "../generated/catalog.gen";
import { classAttr, escapeHtml } from "../html";

export function renderColumn(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  return `<div${classAttr("a2ui-col", alignClass(node.align), justifyClass(node.justify))}>${children(node, ctx)}</div>`;
}

export function renderRow(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  return `<div${classAttr("a2ui-row", alignClass(node.align), justifyClass(node.justify))}>${children(node, ctx)}</div>`;
}

export function renderCard(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const title = ctx.resolveString(node.title);
  const child = typeof node.child === "string" ? ctx.renderChild(node.child) : ctx.placeholder("Card 缺少 child");
  return `<div class="a2ui-panel">
    ${title ? `<div class="a2ui-panel-title">${escapeHtml(title)}</div>` : ""}
    ${child}
  </div>`;
}

export function renderDivider(): string {
  return `<hr class="a2ui-divider" />`;
}

function children(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  if (!Array.isArray(node.children)) return "";
  return node.children
    .map((id) => (typeof id === "string" ? ctx.renderChild(id) : ctx.placeholder("children 必须是组件 id")))
    .join("");
}

/**
 * Only a value the catalog defines becomes a class, so the stylesheet needs a
 * rule for exactly this list and nothing a sender invents can name a class.
 */
function enumClass(prefix: string, allowed: readonly string[], value: unknown): string | undefined {
  return typeof value === "string" && allowed.includes(value) ? `${prefix}${value}` : undefined;
}

function alignClass(value: unknown): string | undefined {
  return enumClass("a2ui-align-", A2UI_ENUMS.Column.align, value);
}

function justifyClass(value: unknown): string | undefined {
  return enumClass("a2ui-justify-", A2UI_ENUMS.Column.justify, value);
}
