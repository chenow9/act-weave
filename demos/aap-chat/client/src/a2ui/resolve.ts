/**
 * Value resolution for A2UI surfaces.
 *
 * A member is either a literal or a binding — `{ "path": "/pointer" }` into the
 * surface dataModel. Everything the renderers read goes through here, so a bound
 * value and an inline one are indistinguishable downstream.
 *
 * The server has already validated the surface, so these functions do not report
 * errors: they return an empty or default value and let the caller draw nothing.
 * Being lenient here is deliberate, since a surface may also arrive from another
 * producer whose catalog is newer than ours.
 */

import type { A2UIChartSeries, A2UIDataBinding } from "./generated/catalog.gen";

export function isDataBinding(value: unknown): value is A2UIDataBinding {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const path = (value as Record<string, unknown>).path;
  return typeof path === "string" && path.startsWith("/");
}

/**
 * Walks an RFC 6901 JSON Pointer. Array indices are supported so a binding can
 * address one element of a dataset.
 */
export function resolvePointer(root: unknown, pointer: string): unknown {
  if (pointer === "" || pointer === "/") return root;
  if (!pointer.startsWith("/")) return undefined;
  let node: unknown = root;
  for (const rawSegment of pointer.slice(1).split("/")) {
    const segment = rawSegment.replaceAll("~1", "/").replaceAll("~0", "~");
    if (Array.isArray(node)) {
      const index = Number(segment);
      if (!Number.isInteger(index) || index < 0 || index >= node.length) return undefined;
      node = node[index];
      continue;
    }
    if (typeof node === "object" && node !== null) {
      node = (node as Record<string, unknown>)[segment];
      continue;
    }
    return undefined;
  }
  return node;
}

/** Follows a binding, or returns the literal unchanged. */
export function resolveValue(value: unknown, dataModel: unknown): unknown {
  return isDataBinding(value) ? resolvePointer(dataModel, value.path) : value;
}

export function resolveString(value: unknown, dataModel: unknown): string {
  const resolved = resolveValue(value, dataModel);
  if (typeof resolved === "string") return resolved;
  if (typeof resolved === "number" || typeof resolved === "boolean") return String(resolved);
  return "";
}

export function resolveBoolean(value: unknown, dataModel: unknown): boolean {
  return resolveValue(value, dataModel) === true;
}

/** A ChoicePicker value is one selection, several, or a binding to either. */
export function resolveChoiceValues(value: unknown, dataModel: unknown): string[] {
  const resolved = resolveValue(value, dataModel);
  if (typeof resolved === "string") return [resolved];
  if (Array.isArray(resolved)) return resolved.filter((item): item is string => typeof item === "string");
  return [];
}

/**
 * Reads chart series, keeping only entries shaped like the catalog's ChartSeries.
 * A bound dataset is held to the same shape as an inline one.
 */
export function resolveSeries(value: unknown, dataModel: unknown): A2UIChartSeries[] {
  const resolved = resolveValue(value, dataModel);
  if (!Array.isArray(resolved)) return [];
  const series: A2UIChartSeries[] = [];
  for (const entry of resolved) {
    if (typeof entry !== "object" || entry === null) continue;
    const points = (entry as Record<string, unknown>).points;
    if (!Array.isArray(points)) continue;
    const name = (entry as Record<string, unknown>).name;
    series.push({
      ...(typeof name === "string" ? { name } : {}),
      points: points.flatMap((point) => {
        if (typeof point !== "object" || point === null) return [];
        const { label, value: pointValue } = point as Record<string, unknown>;
        if (typeof label !== "string" || typeof pointValue !== "number" || !Number.isFinite(pointValue)) {
          return [];
        }
        return [{ label, value: pointValue }];
      }),
    });
  }
  return series.filter((entry) => entry.points.length > 0);
}
