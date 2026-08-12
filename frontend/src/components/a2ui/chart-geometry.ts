/**
 * Chart geometry for A2UI surfaces. No chart library.
 *
 * The surface describes what was measured; every visual decision — palette,
 * padding, gridline density, label truncation — is made here. That separation is
 * why Console and the demo may look different while drawing the same data.
 *
 * These functions return coordinates and paths, never markup: the SVG elements
 * are written in the Vue template, so nothing here can inject anything.
 */

import type { A2UIChartSeries, A2UIChartType, A2UIValueFormat } from "./generated/catalog.gen";

const PALETTE = [
  "#0d9488",
  "#2563eb",
  "#d97706",
  "#7c3aed",
  "#dc2626",
  "#0891b2",
  "#059669",
  "#db2777",
  "#4f46e5",
  "#ca8a04",
];

const WIDTH = 400;
const HEIGHT = 220;
const PAD = { left: 44, right: 14, top: 16, bottom: 40 };
const HBAR_PAD = { left: 96, right: 52, top: 8, bottom: 8 };
const HBAR_ROW = 28;
const RADIAL = { width: 280, height: 220, cx: 110, cy: 110, radius: 88, inner: 48 };

export interface ChartInput {
  chartType: A2UIChartType;
  series: A2UIChartSeries[];
  stacked: boolean;
  /** Formats a value for a tick or tooltip, unit included. */
  format: (value: number) => string;
  /** Names a series in tooltips and the legend when the surface left it unnamed. */
  seriesName: (index: number, name?: string) => string;
}

export interface ChartBar {
  key: string;
  x: number;
  y: number;
  width: number;
  height: number;
  color: string;
  title: string;
}

export interface ChartDot {
  key: string;
  cx: number;
  cy: number;
  title: string;
}

export interface ChartLine {
  key: string;
  color: string;
  line: string;
  /** Set for area charts only. */
  area?: string;
  dots: ChartDot[];
}

export interface ChartSlice {
  key: string;
  path: string;
  color: string;
  title: string;
}

export interface ChartGridLine {
  key: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

export interface ChartAxisLabel {
  key: string;
  x: number;
  y: number;
  text: string;
  anchor: "start" | "middle" | "end";
}

export interface ChartLegendItem {
  key: string;
  color: string;
  label: string;
  /** Present for pie and donut, where a slice's size is the point of the legend. */
  value?: string;
  share?: string;
}

export interface ChartGeometry {
  width: number;
  height: number;
  /** Radial charts drop the axes, so the template renders them differently. */
  radial: boolean;
  bars: ChartBar[];
  lines: ChartLine[];
  slices: ChartSlice[];
  grid: ChartGridLine[];
  valueLabels: ChartAxisLabel[];
  categoryLabels: ChartAxisLabel[];
  /** Total in the middle of a donut. */
  centerLabel?: string;
  legend: ChartLegendItem[];
}

export function chartGeometry(input: ChartInput): ChartGeometry {
  switch (input.chartType) {
    case "bar":
      return barGeometry(input, false);
    case "hbar":
      return barGeometry(input, true);
    case "line":
      return lineGeometry(input, false);
    case "area":
      return lineGeometry(input, true);
    case "pie":
      return radialGeometry(input, false);
    case "donut":
      return radialGeometry(input, true);
  }
}

/** Category labels in first-seen order, which is the order a model listed them. */
export function unionLabels(series: A2UIChartSeries[]): string[] {
  const seen = new Set<string>();
  const labels: string[] = [];
  for (const entry of series) {
    for (const point of entry.points) {
      if (!seen.has(point.label)) {
        seen.add(point.label);
        labels.push(point.label);
      }
    }
  }
  return labels;
}

export function formatValue(value: number, format: A2UIValueFormat, unit: string): string {
  if (!Number.isFinite(value)) return "—";
  const body = (() => {
    switch (format) {
      case "compact":
        return compact(value);
      case "percent":
        return `${trimNumber(value)}%`;
      case "currency":
        return grouped(value);
      case "plain":
        return trimNumber(value);
    }
  })();
  // percent carries its own symbol; a unit alongside it would read as a second one.
  if (!unit || format === "percent") return body;
  return `${body} ${unit}`;
}

function compact(value: number): string {
  const magnitude = Math.abs(value);
  if (magnitude >= 1_000_000_000) return `${trimNumber(value / 1_000_000_000, 1)}B`;
  if (magnitude >= 1_000_000) return `${trimNumber(value / 1_000_000, 1)}M`;
  if (magnitude >= 1_000) return `${trimNumber(value / 1_000, 1)}k`;
  return trimNumber(value);
}

function grouped(value: number): string {
  const [whole, fraction] = trimNumber(value, 2).split(".");
  const withSeparators = (whole ?? "").replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return fraction ? `${withSeparators}.${fraction}` : withSeparators;
}

function trimNumber(value: number, digits = 2): string {
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(digits).replace(/\.?0+$/, "");
}

/** A value-axis span that always includes zero, so bars share a baseline. */
interface Span {
  min: number;
  max: number;
}

function valueSpan(input: ChartInput, labels: string[]): Span {
  const totals: number[] = [0];
  if (input.stacked) {
    for (const label of labels) {
      let positive = 0;
      let negative = 0;
      for (const entry of input.series) {
        const value = valueAt(entry, label);
        if (value >= 0) positive += value;
        else negative += value;
      }
      totals.push(positive, negative);
    }
  } else {
    for (const entry of input.series) {
      for (const point of entry.points) totals.push(point.value);
    }
  }
  const max = niceBound(Math.max(...totals));
  const min = -niceBound(-Math.min(...totals));
  return { min, max: max === min ? max + 1 : max };
}

/** Rounds a bound out to 1, 2 or 5 times a power of ten so ticks read cleanly. */
function niceBound(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  const exponent = Math.floor(Math.log10(value));
  const fraction = value / 10 ** exponent;
  const stepped = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 5 ? 5 : 10;
  return stepped * 10 ** exponent;
}

function valueAt(entry: A2UIChartSeries, label: string): number {
  return entry.points.find((point) => point.label === label)?.value ?? 0;
}

function colorFor(index: number): string {
  return PALETTE[index % PALETTE.length] as string;
}

function truncate(text: string, limit: number): string {
  return text.length <= limit ? text : `${text.slice(0, Math.max(limit - 1, 1))}…`;
}

function round(value: number): number {
  return Math.round(value * 10) / 10;
}

function ticks(span: Span, count: number): number[] {
  return Array.from({ length: count + 1 }, (_unused, index) => span.min + ((span.max - span.min) * index) / count);
}

/** Bars, vertical or horizontal, grouped or stacked. */
function barGeometry(input: ChartInput, horizontal: boolean): ChartGeometry {
  const labels = unionLabels(input.series);
  const span = valueSpan(input, labels);
  const pad = horizontal ? HBAR_PAD : PAD;
  const width = WIDTH;
  const height = horizontal ? pad.top + pad.bottom + Math.max(labels.length, 1) * HBAR_ROW : HEIGHT;
  const categoryExtent = (horizontal ? height - pad.top - pad.bottom : width - pad.left - pad.right) || 1;
  const valueExtent = (horizontal ? width - pad.left - pad.right : height - pad.top - pad.bottom) || 1;
  const slot = categoryExtent / Math.max(labels.length, 1);

  // Horizontal bars grow rightwards and vertical bars grow upwards, so the value
  // axis runs the other way for one of them.
  const valueToOffset = (value: number) => {
    const ratio = (value - span.min) / (span.max - span.min);
    return horizontal ? ratio * valueExtent : (1 - ratio) * valueExtent;
  };

  const gap = 0.2;
  const bandSize = slot * (1 - gap);
  const bars: ChartBar[] = [];
  labels.forEach((label, categoryIndex) => {
    const bandStart = categoryIndex * slot + (slot * gap) / 2;
    let positiveTop = 0;
    let negativeTop = 0;
    input.series.forEach((entry, seriesIndex) => {
      const value = valueAt(entry, label);
      const size = input.stacked ? bandSize : bandSize / input.series.length;
      const start = input.stacked ? bandStart : bandStart + seriesIndex * size;
      const from = input.stacked ? (value >= 0 ? positiveTop : negativeTop) : 0;
      const to = from + value;
      if (input.stacked) {
        if (value >= 0) positiveTop = to;
        else negativeTop = to;
      }
      const near = Math.min(valueToOffset(from), valueToOffset(to));
      const thickness = Math.abs(valueToOffset(from) - valueToOffset(to));
      const named = entry.name ? `${entry.name} · ` : "";
      bars.push({
        key: `${categoryIndex}-${seriesIndex}`,
        x: round(horizontal ? pad.left + near : pad.left + start),
        y: round(horizontal ? pad.top + start : pad.top + near),
        width: round(horizontal ? Math.max(thickness, 0) : Math.max(size, 1)),
        height: round(horizontal ? Math.max(size, 1) : Math.max(thickness, 0)),
        color: colorFor(seriesIndex),
        title: `${named}${label} = ${input.format(value)}`,
      });
    });
  });

  const categoryLabels: ChartAxisLabel[] = labels.map((label, index) => {
    const center = pad.top + index * slot + slot / 2;
    return horizontal
      ? { key: `c${index}`, x: pad.left - 8, y: round(center + 4), text: truncate(label, 12), anchor: "end" }
      : {
          key: `c${index}`,
          x: round(pad.left + index * slot + slot / 2),
          y: height - 12,
          text: truncate(label, 8),
          anchor: "middle",
        };
  });

  const grid: ChartGridLine[] = [];
  const valueLabels: ChartAxisLabel[] = [];
  if (horizontal) {
    ticks(span, 4).forEach((tick, index) => {
      const x = round(pad.left + valueToOffset(tick));
      grid.push({ key: `v${index}`, x1: x, y1: pad.top, x2: x, y2: height - pad.bottom });
      valueLabels.push({ key: `v${index}`, x, y: height - 2, text: input.format(tick), anchor: "middle" });
    });
  } else {
    ticks(span, 4).forEach((tick, index) => {
      const y = round(PAD.top + (1 - (tick - span.min) / (span.max - span.min)) * (height - PAD.top - PAD.bottom));
      grid.push({ key: `v${index}`, x1: PAD.left, y1: y, x2: WIDTH - PAD.right, y2: y });
      valueLabels.push({ key: `v${index}`, x: PAD.left - 6, y: round(y + 3), text: input.format(tick), anchor: "end" });
    });
  }

  return {
    width,
    height: Math.max(height, 80),
    radial: false,
    bars,
    lines: [],
    slices: [],
    grid,
    valueLabels,
    categoryLabels,
    legend: seriesLegend(input),
  };
}

function lineGeometry(input: ChartInput, area: boolean): ChartGeometry {
  const labels = unionLabels(input.series);
  const span = valueSpan(input, labels);
  const plotWidth = WIDTH - PAD.left - PAD.right;
  const plotHeight = HEIGHT - PAD.top - PAD.bottom;
  const steps = Math.max(labels.length - 1, 1);
  const toX = (index: number) => round(PAD.left + (index / steps) * plotWidth);
  const toY = (value: number) => round(PAD.top + (1 - (value - span.min) / (span.max - span.min)) * plotHeight);

  const cumulative = new Map<string, number>();
  const lines: ChartLine[] = input.series.map((entry, seriesIndex) => {
    const points = labels.map((label, index) => {
      const value = valueAt(entry, label);
      const base = input.stacked ? (cumulative.get(label) ?? 0) : 0;
      const top = base + value;
      if (input.stacked) cumulative.set(label, top);
      return { x: toX(index), y: toY(top), baseY: toY(base), value, label };
    });
    const line = points.map((point, index) => `${index === 0 ? "M" : "L"}${point.x},${point.y}`).join(" ");
    const named = entry.name ? `${entry.name} · ` : "";
    return {
      key: `s${seriesIndex}`,
      color: colorFor(seriesIndex),
      line,
      ...(area && points.length
        ? {
            area: `${line} ${[...points]
              .reverse()
              .map((point) => `L${point.x},${point.baseY}`)
              .join(" ")} Z`,
          }
        : {}),
      dots: points.map((point, index) => ({
        key: `d${index}`,
        cx: point.x,
        cy: point.y,
        title: `${named}${point.label} = ${input.format(point.value)}`,
      })),
    };
  });

  const grid: ChartGridLine[] = [];
  const valueLabels: ChartAxisLabel[] = [];
  ticks(span, 4).forEach((tick, index) => {
    const y = toY(tick);
    grid.push({ key: `v${index}`, x1: PAD.left, y1: y, x2: WIDTH - PAD.right, y2: y });
    valueLabels.push({ key: `v${index}`, x: PAD.left - 6, y: round(y + 3), text: input.format(tick), anchor: "end" });
  });

  return {
    width: WIDTH,
    height: HEIGHT,
    radial: false,
    bars: [],
    lines,
    slices: [],
    grid,
    valueLabels,
    categoryLabels: labels.map((label, index) => ({
      key: `c${index}`,
      x: toX(index),
      y: HEIGHT - 12,
      text: truncate(label, 8),
      anchor: "middle",
    })),
    legend: seriesLegend(input),
  };
}

function radialGeometry(input: ChartInput, donut: boolean): ChartGeometry {
  // The catalog admits one series here, and the write path rejects more.
  const points = (input.series[0]?.points ?? []).filter((point) => point.value > 0);
  const total = points.reduce((sum, point) => sum + point.value, 0);
  const inner = donut ? RADIAL.inner : 0;

  const slices: ChartSlice[] = [];
  let angle = -Math.PI / 2;
  if (total > 0) {
    points.forEach((point, index) => {
      const sweep = (point.value / total) * Math.PI * 2;
      const start = angle;
      const end = angle + sweep;
      angle = end;
      const large = sweep > Math.PI ? 1 : 0;
      const outerStart = onCircle(RADIAL.radius, start);
      const outerEnd = onCircle(RADIAL.radius, end);
      const path = inner
        ? `M${outerStart} A${RADIAL.radius},${RADIAL.radius} 0 ${large} 1 ${outerEnd} ` +
          `L${onCircle(inner, end)} A${inner},${inner} 0 ${large} 0 ${onCircle(inner, start)} Z`
        : `M${RADIAL.cx},${RADIAL.cy} L${outerStart} A${RADIAL.radius},${RADIAL.radius} 0 ${large} 1 ${outerEnd} Z`;
      const share = ((point.value / total) * 100).toFixed(1);
      slices.push({
        key: `p${index}`,
        path,
        color: colorFor(index),
        title: `${point.label} = ${input.format(point.value)} (${share}%)`,
      });
    });
  }

  return {
    width: RADIAL.width,
    height: RADIAL.height,
    radial: true,
    bars: [],
    lines: [],
    slices,
    grid: [],
    valueLabels: [],
    categoryLabels: [],
    ...(donut && total > 0 ? { centerLabel: input.format(total) } : {}),
    legend: pointLegend(input),
  };
}

function onCircle(radius: number, angle: number): string {
  return `${round(RADIAL.cx + radius * Math.cos(angle))},${round(RADIAL.cy + radius * Math.sin(angle))}`;
}

/**
 * A legend names what a reader cannot infer: series identity where several are
 * drawn, and slice share where sizes are the message.
 */
function seriesLegend(input: ChartInput): ChartLegendItem[] {
  if (input.series.length <= 1) return [];
  return input.series.map((entry, index) => ({
    key: `s${index}`,
    color: colorFor(index),
    label: input.seriesName(index, entry.name),
  }));
}

function pointLegend(input: ChartInput): ChartLegendItem[] {
  const points = input.series[0]?.points ?? [];
  const total = points.reduce((sum, point) => sum + Math.abs(point.value), 0) || 1;
  return points.map((point, index) => ({
    key: `p${index}`,
    color: colorFor(index),
    label: point.label,
    value: input.format(point.value),
    share: `${((Math.abs(point.value) / total) * 100).toFixed(1)}%`,
  }));
}

export const chartPalette = PALETTE;
export const radialCenter = { x: RADIAL.cx, y: RADIAL.cy };
