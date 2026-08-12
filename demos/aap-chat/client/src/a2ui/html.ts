import { escapeHtml } from "../markdown";

export { escapeHtml };

/** Escapes a value for use inside a single- or double-quoted attribute. */
export function escapeAttr(value: string): string {
  return escapeHtml(value).replaceAll("'", "&#39;");
}

/**
 * Renders a class list, dropping empty entries so callers can pass conditionals
 * inline.
 */
export function classAttr(...names: Array<string | false | undefined>): string {
  const kept = names.filter((name): name is string => Boolean(name));
  return kept.length ? ` class="${escapeAttr(kept.join(" "))}"` : "";
}
