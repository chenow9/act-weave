/**
 * Binding resolution for A2UI surfaces.
 *
 * A component member is either a literal or a JSON Pointer into the surface
 * dataModel, and a renderer must never tell the two apart by itself. The
 * generated contract states that rule; this file is Console's implementation of
 * it, held to the same shared fixtures as the demo's.
 */

import type { A2UIChartSeries, A2UIDataBinding } from "./generated/catalog.gen";

export function isDataBinding(value: unknown): value is A2UIDataBinding {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const path = (value as Record<string, unknown>).path;
  return typeof path === "string" && path.startsWith("/");
}

/** RFC 6901 lookup. Returns undefined for anything the pointer does not reach. */
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

/** One selection, several, or a binding to either. */
export function resolveChoiceValues(value: unknown, dataModel: unknown): string[] {
  const resolved = resolveValue(value, dataModel);
  if (typeof resolved === "string") return [resolved];
  if (Array.isArray(resolved)) return resolved.filter((item): item is string => typeof item === "string");
  return [];
}

/**
 * Chart data, inline or bound. Malformed points are dropped rather than drawn as
 * zero, so a partially broken series cannot read as a real measurement.
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
