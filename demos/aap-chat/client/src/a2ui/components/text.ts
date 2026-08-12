import type { A2UIComponentNode, A2UIRenderCtx } from "../generated/catalog.gen";
import { escapeHtml } from "../html";

/**
 * Text is escaped and never parsed as Markdown: the catalog defines it as plain
 * text, and interpreting it would turn agent output into markup.
 */
export function renderText(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const text = escapeHtml(ctx.resolveString(node.text));
  switch (node.variant) {
    case "heading":
      return `<h3 class="a2ui-title">${text}</h3>`;
    case "caption":
      return `<p class="a2ui-text a2ui-text-caption">${text}</p>`;
    default:
      return `<p class="a2ui-text">${text}</p>`;
  }
}
