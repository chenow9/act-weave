/**
 * Reading an A2UI surface.
 *
 * The surface is a flat component graph plus an optional data model. Any member
 * of a component is either a literal or a JSON Pointer into that data model, and
 * a client must never tell the two apart by inspecting the value's shape. These
 * helpers are that rule, so a client is not left reimplementing RFC 6901 to draw
 * a chart.
 *
 * Nothing here renders. What a component looks like is the client's decision;
 * what a component *means* is the catalog's, and the catalog is generated into
 * ./generated/a2ui.gen.ts from the same file the server validates against.
 */

import type {
  A2UIChartSeries,
  A2UIChartType,
  A2UIComponentNode,
  A2UIDataBinding,
  A2UISurface,
  A2UIValueFormat,
} from "./generated/a2ui.gen.js";
import { A2UI_CATALOG_ID, A2UI_VALUE_FORMATS, isA2UIChartType } from "./generated/a2ui.gen.js";

/** True when a member is a pointer into the data model rather than a value. */
export function isA2UIDataBinding(value: unknown): value is A2UIDataBinding {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const path = (value as Record<string, unknown>).path;
  return typeof path === "string" && path.startsWith("/");
}

/**
 * Resolve a component member against the surface: a literal passes through, a
 * binding is followed into `surface.dataModel`.
 *
 * Returns undefined when a binding points nowhere. That is not an error to
 * report to the user — a surface may bind a member the data model has not filled
 * in — so degrade to whatever your renderer shows for an absent member.
 */
export function resolveBinding(surface: A2UISurface, value: unknown): unknown {
  if (!isA2UIDataBinding(value)) return value;
  return resolvePointer(surface.dataModel, value.path);
}

/** RFC 6901 lookup. Returns undefined for anything the pointer does not reach. */
function resolvePointer(root: unknown, pointer: string): unknown {
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

/**
 * A chart of a surface, with its data already resolved and its members read off
 * the node.
 *
 * The surface carries measurements, never pixels or formatted strings: there is
 * no colour, no axis range and no rendered label to inherit. `unit` and
 * `valueFormat` say how a value should read; the rest is the client's chart
 * library.
 */
export interface A2UIChart {
  /** Component id, unique within the surface. */
  id: string;
  chartType: A2UIChartType;
  /** Series in declared order, malformed entries dropped (see iterCharts). */
  series: A2UIChartSeries[];
  title?: string;
  /** Unit of every value, e.g. "万元" or "%". Never baked into the numbers. */
  unit?: string;
  valueFormat?: A2UIValueFormat;
  /**
   * Bars stack instead of sitting side by side. Only bar and hbar carry it; the
   * server rejects it on the other shapes, so it is false everywhere else.
   */
  stacked: boolean;
  /** The component as it arrived, for members this view does not name. */
  node: A2UIComponentNode;
}

/**
 * Every chart of a surface, in component order, with bound series resolved.
 *
 * A point missing a label or carrying a non-finite value is dropped, and a
 * series left with no points is dropped with it: a partially broken series must
 * not read as a real measurement of zero. A chart with no series survives so a
 * client can show that a chart was intended and had no data.
 */
export function iterCharts(surface: A2UISurface): A2UIChart[] {
  const charts: A2UIChart[] = [];
  for (const node of surface.components ?? []) {
    if (!isA2UIComponentNode(node) || node.component !== "Chart") continue;
    const chartType = node.chartType;
    if (!isA2UIChartType(chartType)) continue;
    const title = resolveBinding(surface, node.title);
    const unit = node.unit;
    const valueFormat = node.valueFormat;
    charts.push({
      id: node.id,
      chartType,
      series: resolveChartSeries(surface, node.series),
      ...(typeof title === "string" && title !== "" ? { title } : {}),
      ...(typeof unit === "string" ? { unit } : {}),
      ...(isA2UIValueFormat(valueFormat) ? { valueFormat } : {}),
      stacked: node.stacked === true,
      node,
    });
  }
  return charts;
}

/** Chart data, inline or bound, with malformed entries dropped. */
function resolveChartSeries(surface: A2UISurface, value: unknown): A2UIChartSeries[] {
  const resolved = resolveBinding(surface, value);
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

/**
 * True when this surface declares a catalog this SDK build knows.
 *
 * Check it before rendering: a surface from a newer catalog may use components
 * that do not exist here, and the honest response is to fall back to the text
 * part of the message rather than draw a partial UI.
 */
export function isKnownA2UICatalog(surface: unknown): surface is A2UISurface {
  if (typeof surface !== "object" || surface === null) return false;
  const value = surface as Record<string, unknown>;
  return Array.isArray(value.components) && value.catalogId === A2UI_CATALOG_ID;
}

function isA2UIValueFormat(value: unknown): value is A2UIValueFormat {
  return typeof value === "string" && (A2UI_VALUE_FORMATS as readonly string[]).includes(value);
}

function isA2UIComponentNode(value: unknown): value is A2UIComponentNode {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const node = value as Record<string, unknown>;
  return typeof node.id === "string" && typeof node.component === "string";
}
