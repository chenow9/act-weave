/**
 * SVG drawing for A2UI charts. No external chart library.
 *
 * These functions receive resolved semantic data and own every visual decision:
 * palette, geometry, gridlines, label density. The surface never describes any of
 * it, which is why the demo and Console are allowed to look different.
 */

import type { A2UIChartSeries, A2UIChartType, A2UIValueFormat } from "./generated/catalog.gen";
import { escapeAttr, escapeHtml } from "./html";

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

export interface ChartDrawing {
  series: A2UIChartSeries[];
  /** Category labels in first-seen order across all series. */
  labels: string[];
  stacked: boolean;
  /** Formats a value for a tooltip or axis tick, unit included. */
  format(value: number): string;
}

export function drawChart(chartType: A2UIChartType, drawing: ChartDrawing): string {
  switch (chartType) {
    case "bar":
      return drawBars(drawing, false);
    case "hbar":
      return drawBars(drawing, true);
    case "line":
      return drawLine(drawing, false);
    case "area":
      return drawLine(drawing, true);
    case "pie":
      return drawPie(drawing, false);
    case "donut":
      return drawPie(drawing, true);
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
  const withSeparators = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
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

function valueSpan(drawing: ChartDrawing): Span {
  const totals: number[] = [0];
  if (drawing.stacked) {
    for (const label of drawing.labels) {
      let positive = 0;
      let negative = 0;
      for (const entry of drawing.series) {
        const value = valueAt(entry, label);
        if (value >= 0) positive += value;
        else negative += value;
      }
      totals.push(positive, negative);
    }
  } else {
    for (const entry of drawing.series) {
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
  return PALETTE[index % PALETTE.length];
}

function truncate(text: string, limit: number): string {
  return text.length <= limit ? text : `${text.slice(0, Math.max(limit - 1, 1))}…`;
}

/** Bars, vertical or horizontal, grouped or stacked. */
function drawBars(drawing: ChartDrawing, horizontal: boolean): string {
  const { labels, series, stacked } = drawing;
  const span = valueSpan(drawing);
  const pad = horizontal ? HBAR_PAD : PAD;
  const width = WIDTH;
  const height = horizontal ? pad.top + pad.bottom + Math.max(labels.length, 1) * HBAR_ROW : HEIGHT;
  const categoryExtent = (horizontal ? height - pad.top - pad.bottom : width - pad.left - pad.right) || 1;
  const valueExtent = (horizontal ? width - pad.left - pad.right : height - pad.top - pad.bottom) || 1;
  const slot = categoryExtent / Math.max(labels.length, 1);

  // Horizontal bars grow rightwards, vertical bars grow upwards, so the value
  // axis runs the other way for one of them.
  const valueToOffset = (value: number) => {
    const ratio = (value - span.min) / (span.max - span.min);
    return horizontal ? ratio * valueExtent : (1 - ratio) * valueExtent;
  };

  const rect = (categoryStart: number, categorySize: number, from: number, to: number) => {
    const a = valueToOffset(from);
    const b = valueToOffset(to);
    const near = Math.min(a, b);
    const size = Math.abs(a - b);
    return horizontal
      ? {
          x: pad.left + near,
          y: pad.top + categoryStart,
          width: Math.max(size, 0),
          height: Math.max(categorySize, 1),
        }
      : {
          x: pad.left + categoryStart,
          y: pad.top + near,
          width: Math.max(categorySize, 1),
          height: Math.max(size, 0),
        };
  };

  const gap = 0.2;
  const bandSize = slot * (1 - gap);
  let bars = "";
  labels.forEach((label, categoryIndex) => {
    const bandStart = categoryIndex * slot + (slot * gap) / 2;
    let positiveTop = 0;
    let negativeTop = 0;
    series.forEach((entry, seriesIndex) => {
      const value = valueAt(entry, label);
      const size = stacked ? bandSize : bandSize / series.length;
      const start = stacked ? bandStart : bandStart + seriesIndex * size;
      const from = stacked ? (value >= 0 ? positiveTop : negativeTop) : 0;
      const to = from + value;
      if (stacked) {
        if (value >= 0) positiveTop = to;
        else negativeTop = to;
      }
      const box = rect(start, size, from, to);
      const name = entry.name ? `${entry.name} · ` : "";
      bars +=
        `<rect x="${box.x.toFixed(1)}" y="${box.y.toFixed(1)}" width="${box.width.toFixed(1)}" ` +
        `height="${box.height.toFixed(1)}" rx="3" fill="${colorFor(seriesIndex)}" opacity="0.92">` +
        `<title>${escapeHtml(`${name}${label} = ${drawing.format(value)}`)}</title></rect>`;
    });
  });

  const categoryLabels = labels
    .map((label, index) => {
      const center = pad.top + index * slot + slot / 2;
      return horizontal
        ? `<text x="${pad.left - 8}" y="${(center + 4).toFixed(1)}" text-anchor="end" class="a2ui-chart-axis">${escapeHtml(truncate(label, 12))}</text>`
        : `<text x="${(pad.left + index * slot + slot / 2).toFixed(1)}" y="${height - 12}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml(truncate(label, 8))}</text>`;
    })
    .join("");

  const grid = horizontal
    ? valueTicks(span, 4)
        .map((tick) => {
          const x = pad.left + valueToOffset(tick);
          return (
            `<line x1="${x.toFixed(1)}" y1="${pad.top}" x2="${x.toFixed(1)}" y2="${height - pad.bottom}" class="a2ui-chart-grid" />` +
            `<text x="${x.toFixed(1)}" y="${height - 2}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml(drawing.format(tick))}</text>`
          );
        })
        .join("")
    : horizontalGrid(span, drawing, height);

  return svg(width, Math.max(height, 80), `${grid}${bars}${categoryLabels}`);
}

function drawLine(drawing: ChartDrawing, area: boolean): string {
  const { labels, series, stacked } = drawing;
  const span = valueSpan(drawing);
  const plotWidth = WIDTH - PAD.left - PAD.right;
  const plotHeight = HEIGHT - PAD.top - PAD.bottom;
  const steps = Math.max(labels.length - 1, 1);
  const toX = (index: number) => PAD.left + (index / steps) * plotWidth;
  const toY = (value: number) => PAD.top + (1 - (value - span.min) / (span.max - span.min)) * plotHeight;

  const cumulative = new Map<string, number>();
  let paths = "";
  series.forEach((entry, seriesIndex) => {
    const color = colorFor(seriesIndex);
    const points = labels.map((label, index) => {
      const value = valueAt(entry, label);
      const base = stacked ? (cumulative.get(label) ?? 0) : 0;
      const top = base + value;
      if (stacked) cumulative.set(label, top);
      return { x: toX(index), y: toY(top), baseY: toY(base), value, label };
    });
    const line = points.map((point, index) => `${index === 0 ? "M" : "L"}${point.x.toFixed(1)},${point.y.toFixed(1)}`).join(" ");
    if (area && points.length) {
      const back = [...points]
        .reverse()
        .map((point) => `L${point.x.toFixed(1)},${point.baseY.toFixed(1)}`)
        .join(" ");
      paths += `<path d="${line} ${back} Z" fill="${color}" opacity="0.18" />`;
    }
    paths += `<path d="${line}" fill="none" stroke="${color}" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round" />`;
    for (const point of points) {
      const name = entry.name ? `${entry.name} · ` : "";
      paths +=
        `<circle cx="${point.x.toFixed(1)}" cy="${point.y.toFixed(1)}" r="3.5" fill="${color}">` +
        `<title>${escapeHtml(`${name}${point.label} = ${drawing.format(point.value)}`)}</title></circle>`;
    }
  });

  const categoryLabels = labels
    .map(
      (label, index) =>
        `<text x="${toX(index).toFixed(1)}" y="${HEIGHT - 12}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml(truncate(label, 8))}</text>`,
    )
    .join("");

  return svg(WIDTH, HEIGHT, `${horizontalGrid(span, drawing, HEIGHT)}${paths}${categoryLabels}`);
}

function drawPie(drawing: ChartDrawing, donut: boolean): string {
  const points = (drawing.series[0]?.points ?? []).filter((point) => point.value > 0);
  const total = points.reduce((sum, point) => sum + point.value, 0);
  const width = 280;
  const height = 220;
  const center = { x: 110, y: 110 };
  const radius = 88;
  const inner = donut ? 48 : 0;

  if (!points.length || total <= 0) {
    return svg(
      width,
      height,
      `<text x="${center.x}" y="${center.y}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml("无正值可展示")}</text>`,
    );
  }

  let angle = -Math.PI / 2;
  let slices = "";
  points.forEach((point, index) => {
    const sweep = (point.value / total) * Math.PI * 2;
    const start = angle;
    const end = angle + sweep;
    angle = end;
    const large = sweep > Math.PI ? 1 : 0;
    const outerStart = onCircle(center, radius, start);
    const outerEnd = onCircle(center, radius, end);
    const path = inner
      ? `M${outerStart} A${radius},${radius} 0 ${large} 1 ${outerEnd} L${onCircle(center, inner, end)} A${inner},${inner} 0 ${large} 0 ${onCircle(center, inner, start)} Z`
      : `M${center.x},${center.y} L${outerStart} A${radius},${radius} 0 ${large} 1 ${outerEnd} Z`;
    const share = ((point.value / total) * 100).toFixed(1);
    slices +=
      `<path d="${path}" fill="${colorFor(index)}" stroke="#fff" stroke-width="1.5">` +
      `<title>${escapeHtml(`${point.label} = ${drawing.format(point.value)}（${share}%）`)}</title></path>`;
  });

  const middle = donut
    ? `<text x="${center.x}" y="${center.y + 4}" text-anchor="middle" class="a2ui-chart-center">${escapeHtml(drawing.format(total))}</text>`
    : "";

  return svg(width, height, `${slices}${middle}`, "a2ui-chart-svg-pie");
}

function onCircle(center: { x: number; y: number }, radius: number, angle: number): string {
  return `${(center.x + radius * Math.cos(angle)).toFixed(1)},${(center.y + radius * Math.sin(angle)).toFixed(1)}`;
}

function valueTicks(span: Span, count: number): number[] {
  return Array.from({ length: count + 1 }, (_unused, index) => span.min + ((span.max - span.min) * index) / count);
}

function horizontalGrid(span: Span, drawing: ChartDrawing, height: number): string {
  const plotHeight = height - PAD.top - PAD.bottom;
  return valueTicks(span, 4)
    .map((tick) => {
      const y = PAD.top + (1 - (tick - span.min) / (span.max - span.min)) * plotHeight;
      return (
        `<line x1="${PAD.left}" y1="${y.toFixed(1)}" x2="${WIDTH - PAD.right}" y2="${y.toFixed(1)}" class="a2ui-chart-grid" />` +
        `<text x="${PAD.left - 6}" y="${(y + 3).toFixed(1)}" text-anchor="end" class="a2ui-chart-axis">${escapeHtml(drawing.format(tick))}</text>`
      );
    })
    .join("");
}

function svg(width: number, height: number, body: string, extraClass = ""): string {
  return (
    `<svg class="a2ui-chart-svg${extraClass ? ` ${extraClass}` : ""}" viewBox="0 0 ${width} ${height}" ` +
    `preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">${body}</svg>`
  );
}

/**
 * Legend entries name what a reader cannot infer: series identity for multi-series
 * charts, slice share for pie and donut.
 */
export function renderLegend(chartType: A2UIChartType, drawing: ChartDrawing): string {
  if (chartType === "pie" || chartType === "donut") {
    const points = drawing.series[0]?.points ?? [];
    const total = points.reduce((sum, point) => sum + Math.abs(point.value), 0) || 1;
    if (!points.length) return "";
    const items = points
      .map((point, index) => {
        const share = ((Math.abs(point.value) / total) * 100).toFixed(1);
        return (
          `<li><span class="a2ui-chart-swatch" style="background:${escapeAttr(colorFor(index))}"></span>` +
          `<span>${escapeHtml(point.label)}</span><strong>${escapeHtml(drawing.format(point.value))}</strong>` +
          `<em>${escapeHtml(share)}%</em></li>`
        );
      })
      .join("");
    return `<ul class="a2ui-chart-legend">${items}</ul>`;
  }
  if (drawing.series.length <= 1) return "";
  const items = drawing.series
    .map(
      (entry, index) =>
        `<li><span class="a2ui-chart-swatch" style="background:${escapeAttr(colorFor(index))}"></span>` +
        `<span>${escapeHtml(entry.name ?? `系列 ${index + 1}`)}</span></li>`,
    )
    .join("");
  return `<ul class="a2ui-chart-legend a2ui-chart-legend-series">${items}</ul>`;
}
