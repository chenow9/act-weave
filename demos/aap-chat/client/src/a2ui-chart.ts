/**
 * Display-only A2UI chart renderer (SVG, no external chart library).
 *
 * Supported component / surface kinds:
 *   Chart | BarChart | LineChart | AreaChart | PieChart | DonutChart | DoughnutChart
 *   HorizontalBarChart | StatsChart | StatisticChart
 *
 * Chart type aliases (props.chartType / type / kind / style):
 *   bar | column | line | area | pie | donut | doughnut | hbar | horizontalBar
 *
 * Data shapes normalized:
 *   - data: [{ label|name|x|category, value|y|count|amount }, ...]
 *   - labels + values | data (numbers)
 *   - labels + series: [{ name, data|values: number[] }, ...]
 *   - series only (index labels)
 *   - categories + series
 */

import { escapeHtml } from "./markdown";

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

const MAX_POINTS = 32;
const MAX_SERIES = 8;

export type ChartKind = "bar" | "hbar" | "line" | "area" | "pie" | "donut";

export interface ChartPoint {
  label: string;
  value: number;
}

export interface ChartSeries {
  name: string;
  points: ChartPoint[];
}

export function isChartSurface(obj: Record<string, unknown>): boolean {
  const t = String(obj.type || obj.kind || obj.component || "").toLowerCase();
  if (isChartKindName(t)) return true;
  if (obj.chartType != null || obj.chartKind != null) return true;
  // Heuristic: has series/data suitable for charts and not a form
  if (Array.isArray(obj.fields)) return false;
  if (Array.isArray(obj.series) || Array.isArray(obj.datasets)) return true;
  if (Array.isArray(obj.data) && obj.data.length > 0) {
    const first = obj.data[0];
    if (typeof first === "number") return true;
    if (isObject(first)) {
      const o = first as Record<string, unknown>;
      if (
        o.value != null ||
        o.y != null ||
        o.count != null ||
        o.amount != null ||
        (o.label != null && typeof o.value === "number")
      ) {
        return true;
      }
    }
  }
  return false;
}

export function isChartKindName(name: string): boolean {
  const k = name.toLowerCase().replace(/[-_\s]/g, "");
  return (
    k === "chart" ||
    k === "barchart" ||
    k === "columnchart" ||
    k === "linechart" ||
    k === "areachart" ||
    k === "piechart" ||
    k === "donutchart" ||
    k === "doughnutchart" ||
    k === "horizontalbarchart" ||
    k === "hbar" ||
    k === "statschart" ||
    k === "statisticchart" ||
    k === "statistics" ||
    k === "stat"
  );
}

export function renderChart(props: Record<string, unknown>): string {
  const title = asString(props.title || props.label || props.heading || props.name);
  const description = asString(props.description || props.subtitle || props.caption);
  const unit = asString(props.unit || props.yUnit || props.valueUnit);
  const chartKind = resolveChartKind(props);
  const series = normalizeSeries(props);

  if (!series.length || !series.some((s) => s.points.length)) {
    return `<div class="a2ui-chart a2ui-chart-empty">
      ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
      <p class="a2ui-empty">（统计图无有效数据）</p>
    </div>`;
  }

  const body =
    chartKind === "pie" || chartKind === "donut"
      ? renderPieSvg(series[0], chartKind === "donut")
      : chartKind === "line" || chartKind === "area"
        ? renderLineSvg(series, chartKind === "area")
        : chartKind === "hbar"
          ? renderHBarSvg(series)
          : renderBarSvg(series);

  const legend =
    series.length > 1 || chartKind === "pie" || chartKind === "donut"
      ? renderLegend(series, chartKind)
      : "";

  const kindLabel = chartKindLabel(chartKind);

  return `
    <div class="a2ui-chart" data-a2ui-chart="${escapeAttr(chartKind)}" role="img" aria-label="${escapeAttr(title || kindLabel)}">
      <div class="a2ui-chart-head">
        ${title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : ""}
        <span class="a2ui-chart-badge">${escapeHtml(kindLabel)}${unit ? ` · ${escapeHtml(unit)}` : ""}</span>
      </div>
      ${description ? `<p class="a2ui-desc">${escapeHtml(description)}</p>` : ""}
      <div class="a2ui-chart-body">
        ${body}
      </div>
      ${legend}
    </div>
  `;
}

function resolveChartKind(props: Record<string, unknown>): ChartKind {
  const fromName = String(props.component || props.type || props.kind || "").toLowerCase();
  if (fromName.includes("pie")) return "pie";
  if (fromName.includes("donut") || fromName.includes("doughnut")) return "donut";
  if (fromName.includes("line")) return "line";
  if (fromName.includes("area")) return "area";
  if (fromName.includes("horizontal") || fromName === "hbar" || fromName.includes("hbar")) return "hbar";
  if (fromName.includes("bar") || fromName.includes("column")) return "bar";

  const explicit = String(
    props.chartType || props.chartKind || props.chartStyle || props.style || props.variant || "",
  )
    .toLowerCase()
    .replace(/[-_\s]/g, "");

  if (explicit === "pie") return "pie";
  if (explicit === "donut" || explicit === "doughnut") return "donut";
  if (explicit === "line") return "line";
  if (explicit === "area") return "area";
  if (
    explicit === "hbar" ||
    explicit === "horizontalbar" ||
    explicit === "horizontal" ||
    explicit === "barhorizontal"
  ) {
    return "hbar";
  }
  if (explicit === "bar" || explicit === "column" || explicit === "barchart") return "bar";

  // orientation hint
  const ori = String(props.orientation || props.direction || "").toLowerCase();
  if (ori === "horizontal" || ori === "h") return "hbar";

  return "bar";
}

function chartKindLabel(kind: ChartKind): string {
  switch (kind) {
    case "bar":
      return "柱状图";
    case "hbar":
      return "条形图";
    case "line":
      return "折线图";
    case "area":
      return "面积图";
    case "pie":
      return "饼图";
    case "donut":
      return "环图";
  }
}

function normalizeSeries(props: Record<string, unknown>): ChartSeries[] {
  // labels + series / datasets
  const labels = toStringArray(props.labels || props.categories || props.x || props.xLabels);
  const seriesRaw = props.series ?? props.datasets ?? props.lines;

  if (Array.isArray(seriesRaw) && seriesRaw.length) {
    const out: ChartSeries[] = [];
    for (let i = 0; i < Math.min(seriesRaw.length, MAX_SERIES); i++) {
      const s = seriesRaw[i];
      if (typeof s === "number") {
        // treat as single series values without labels — skip here
        continue;
      }
      if (Array.isArray(s)) {
        out.push({
          name: `系列 ${i + 1}`,
          points: zipPoints(labels, toNumberArray(s)),
        });
        continue;
      }
      if (!isObject(s)) continue;
      const name = asString(s.name || s.label || s.title || s.id) || `系列 ${i + 1}`;
      const values = toNumberArray(s.data ?? s.values ?? s.y ?? s.points ?? s.items);
      if (values.length) {
        // points may be objects
        if (Array.isArray(s.data) && s.data.length && isObject(s.data[0])) {
          out.push({ name, points: pointsFromObjects(s.data as unknown[]).slice(0, MAX_POINTS) });
        } else if (Array.isArray(s.points) && s.points.length && isObject(s.points[0])) {
          out.push({ name, points: pointsFromObjects(s.points as unknown[]).slice(0, MAX_POINTS) });
        } else {
          out.push({ name, points: zipPoints(labels, values).slice(0, MAX_POINTS) });
        }
      }
    }
    if (out.length) return out;
  }

  // labels + values
  if (labels.length) {
    const values = toNumberArray(props.values ?? props.data ?? props.y);
    if (values.length) {
      return [{ name: asString(props.seriesName || props.metric || "数值") || "数值", points: zipPoints(labels, values) }];
    }
  }

  // data array of objects or numbers
  if (Array.isArray(props.data)) {
    if (props.data.length && typeof props.data[0] === "number") {
      const values = toNumberArray(props.data);
      return [
        {
          name: asString(props.seriesName || props.metric || "数值") || "数值",
          points: values.map((v, i) => ({ label: String(i + 1), value: v })),
        },
      ];
    }
    const pts = pointsFromObjects(props.data);
    if (pts.length) {
      return [{ name: asString(props.seriesName || props.metric || "数值") || "数值", points: pts.slice(0, MAX_POINTS) }];
    }
  }

  // items / points at root
  if (Array.isArray(props.items)) {
    const pts = pointsFromObjects(props.items);
    if (pts.length) {
      return [{ name: asString(props.seriesName || "数值") || "数值", points: pts.slice(0, MAX_POINTS) }];
    }
  }
  if (Array.isArray(props.points)) {
    const pts = pointsFromObjects(props.points);
    if (pts.length) {
      return [{ name: asString(props.seriesName || "数值") || "数值", points: pts.slice(0, MAX_POINTS) }];
    }
  }

  return [];
}

function pointsFromObjects(arr: unknown[]): ChartPoint[] {
  const out: ChartPoint[] = [];
  for (let i = 0; i < Math.min(arr.length, MAX_POINTS); i++) {
    const item = arr[i];
    if (typeof item === "number" && Number.isFinite(item)) {
      out.push({ label: String(i + 1), value: item });
      continue;
    }
    if (!isObject(item)) continue;
    const label =
      asString(item.label ?? item.name ?? item.x ?? item.category ?? item.key ?? item.month ?? item.date) ||
      String(i + 1);
    const value = toNumber(item.value ?? item.y ?? item.count ?? item.amount ?? item.total ?? item.n);
    if (value == null) continue;
    out.push({ label, value });
  }
  return out;
}

function zipPoints(labels: string[], values: number[]): ChartPoint[] {
  const n = Math.min(Math.max(labels.length, values.length), MAX_POINTS);
  const out: ChartPoint[] = [];
  for (let i = 0; i < n; i++) {
    out.push({
      label: labels[i] != null && labels[i] !== "" ? labels[i] : String(i + 1),
      value: values[i] ?? 0,
    });
  }
  return out;
}

function renderBarSvg(series: ChartSeries[]): string {
  const labels = unionLabels(series);
  const allVals = series.flatMap((s) => s.points.map((p) => p.value));
  const maxV = niceMax(Math.max(...allVals, 0));
  const range = maxV || 1;

  const W = 400;
  const H = 220;
  const padL = 40;
  const padR = 12;
  const padT = 16;
  const padB = 40;
  const plotW = W - padL - padR;
  const plotH = H - padT - padB;
  const groupCount = Math.max(labels.length, 1);
  const groupW = plotW / groupCount;
  const barGap = 0.18;
  const seriesN = series.length;
  const barW = (groupW * (1 - barGap)) / seriesN;

  let bars = "";
  let xLabels = "";
  for (let gi = 0; gi < labels.length; gi++) {
    const gx = padL + gi * groupW + (groupW * barGap) / 2;
    for (let si = 0; si < seriesN; si++) {
      const pt = series[si].points.find((p) => p.label === labels[gi]) ?? series[si].points[gi];
      const v = Math.max(pt?.value ?? 0, 0);
      const y = padT + plotH * ((maxV - v) / range);
      const bh = (v / range) * plotH;
      const x = gx + si * barW;
      const color = PALETTE[si % PALETTE.length];
      const title = `${series[si].name}: ${labels[gi]} = ${formatNum(v)}`;
      bars += `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${Math.max(barW - 1, 1).toFixed(1)}" height="${Math.max(bh, 0).toFixed(1)}" rx="3" fill="${color}" opacity="0.92"><title>${escapeHtml(title)}</title></rect>`;
    }
    const lx = padL + gi * groupW + groupW / 2;
    const lab = truncate(labels[gi], 8);
    xLabels += `<text x="${lx.toFixed(1)}" y="${(H - 12).toFixed(1)}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml(lab)}</text>`;
  }

  // y ticks
  let yTicks = "";
  for (let t = 0; t <= 4; t++) {
    const val = maxV - (range * t) / 4;
    const y = padT + (plotH * t) / 4;
    yTicks += `<line x1="${padL}" y1="${y.toFixed(1)}" x2="${W - padR}" y2="${y.toFixed(1)}" class="a2ui-chart-grid" />`;
    yTicks += `<text x="${padL - 6}" y="${(y + 3).toFixed(1)}" text-anchor="end" class="a2ui-chart-axis">${escapeHtml(formatNum(val))}</text>`;
  }

  return `<svg class="a2ui-chart-svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">
    ${yTicks}
    ${bars}
    ${xLabels}
  </svg>`;
}

function renderHBarSvg(series: ChartSeries[]): string {
  // Use first series primarily; multi-series as grouped rows
  const primary = series[0];
  const points = primary.points.slice(0, MAX_POINTS);
  const maxV = niceMax(Math.max(...points.map((p) => p.value), 0));
  const rowH = 28;
  const W = 400;
  const padL = 88;
  const padR = 48;
  const padT = 8;
  const padB = 8;
  const H = padT + padB + points.length * rowH;
  const plotW = W - padL - padR;

  let rows = "";
  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    const y = padT + i * rowH + 4;
    const w = maxV > 0 ? (p.value / maxV) * plotW : 0;
    const color = PALETTE[i % PALETTE.length];
    rows += `<text x="${padL - 8}" y="${(y + 12).toFixed(1)}" text-anchor="end" class="a2ui-chart-axis">${escapeHtml(truncate(p.label, 10))}</text>`;
    rows += `<rect x="${padL}" y="${y}" width="${Math.max(w, 0).toFixed(1)}" height="16" rx="4" fill="${color}" opacity="0.9"><title>${escapeHtml(`${p.label}: ${formatNum(p.value)}`)}</title></rect>`;
    rows += `<text x="${(padL + w + 6).toFixed(1)}" y="${(y + 12).toFixed(1)}" class="a2ui-chart-axis">${escapeHtml(formatNum(p.value))}</text>`;
  }

  // multi-series note: stack additional as thin overlays not implemented; legend only
  return `<svg class="a2ui-chart-svg" viewBox="0 0 ${W} ${Math.max(H, 80)}" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">
    ${rows}
  </svg>`;
}

function renderLineSvg(series: ChartSeries[], area: boolean): string {
  const labels = unionLabels(series);
  const allVals = series.flatMap((s) => s.points.map((p) => p.value));
  const maxV = niceMax(Math.max(...allVals, 0));
  const minV = Math.min(0, ...allVals);
  const range = maxV - minV || 1;

  const W = 400;
  const H = 220;
  const padL = 40;
  const padR = 12;
  const padT = 16;
  const padB = 40;
  const plotW = W - padL - padR;
  const plotH = H - padT - padB;
  const n = Math.max(labels.length - 1, 1);

  let yTicks = "";
  for (let t = 0; t <= 4; t++) {
    const val = maxV - (range * t) / 4;
    const y = padT + (plotH * t) / 4;
    yTicks += `<line x1="${padL}" y1="${y.toFixed(1)}" x2="${W - padR}" y2="${y.toFixed(1)}" class="a2ui-chart-grid" />`;
    yTicks += `<text x="${padL - 6}" y="${(y + 3).toFixed(1)}" text-anchor="end" class="a2ui-chart-axis">${escapeHtml(formatNum(val))}</text>`;
  }

  let paths = "";
  let xLabels = "";
  for (let si = 0; si < series.length; si++) {
    const color = PALETTE[si % PALETTE.length];
    const pts = labels.map((lab, i) => {
      const p = series[si].points.find((x) => x.label === lab) ?? series[si].points[i];
      const v = p?.value ?? 0;
      const x = padL + (i / n) * plotW;
      const y = padT + ((maxV - v) / range) * plotH;
      return { x, y, v, lab };
    });
    const d = pts.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
    if (area && pts.length) {
      const baseY = padT + ((maxV - 0) / range) * plotH;
      const areaD = `${d} L${pts[pts.length - 1].x.toFixed(1)},${baseY.toFixed(1)} L${pts[0].x.toFixed(1)},${baseY.toFixed(1)} Z`;
      paths += `<path d="${areaD}" fill="${color}" opacity="0.18" />`;
    }
    paths += `<path d="${d}" fill="none" stroke="${color}" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round" />`;
    for (const p of pts) {
      paths += `<circle cx="${p.x.toFixed(1)}" cy="${p.y.toFixed(1)}" r="3.5" fill="${color}"><title>${escapeHtml(`${series[si].name}: ${p.lab} = ${formatNum(p.v)}`)}</title></circle>`;
    }
  }

  for (let i = 0; i < labels.length; i++) {
    const x = padL + (i / n) * plotW;
    xLabels += `<text x="${x.toFixed(1)}" y="${(H - 12).toFixed(1)}" text-anchor="middle" class="a2ui-chart-axis">${escapeHtml(truncate(labels[i], 8))}</text>`;
  }

  return `<svg class="a2ui-chart-svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">
    ${yTicks}
    ${paths}
    ${xLabels}
  </svg>`;
}

function renderPieSvg(series: ChartSeries, donut: boolean): string {
  const points = series.points.filter((p) => p.value > 0).slice(0, MAX_POINTS);
  const total = points.reduce((s, p) => s + Math.abs(p.value), 0) || 1;
  const W = 280;
  const H = 220;
  const cx = 110;
  const cy = 110;
  const r = 88;
  const inner = donut ? 48 : 0;

  let angle = -Math.PI / 2;
  let paths = "";
  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    const slice = (Math.abs(p.value) / total) * Math.PI * 2;
    const a0 = angle;
    const a1 = angle + slice;
    angle = a1;
    const color = PALETTE[i % PALETTE.length];
    const large = slice > Math.PI ? 1 : 0;
    const x0 = cx + r * Math.cos(a0);
    const y0 = cy + r * Math.sin(a0);
    const x1 = cx + r * Math.cos(a1);
    const y1 = cy + r * Math.sin(a1);
    let d: string;
    if (inner > 0) {
      const ix0 = cx + inner * Math.cos(a1);
      const iy0 = cy + inner * Math.sin(a1);
      const ix1 = cx + inner * Math.cos(a0);
      const iy1 = cy + inner * Math.sin(a0);
      d = `M${x0.toFixed(1)},${y0.toFixed(1)} A${r},${r} 0 ${large} 1 ${x1.toFixed(1)},${y1.toFixed(1)} L${ix0.toFixed(1)},${iy0.toFixed(1)} A${inner},${inner} 0 ${large} 0 ${ix1.toFixed(1)},${iy1.toFixed(1)} Z`;
    } else {
      d = `M${cx},${cy} L${x0.toFixed(1)},${y0.toFixed(1)} A${r},${r} 0 ${large} 1 ${x1.toFixed(1)},${y1.toFixed(1)} Z`;
    }
    const pct = ((Math.abs(p.value) / total) * 100).toFixed(1);
    paths += `<path d="${d}" fill="${color}" stroke="#fff" stroke-width="1.5"><title>${escapeHtml(`${p.label}: ${formatNum(p.value)} (${pct}%)`)}</title></path>`;
  }

  const center =
    donut
      ? `<text x="${cx}" y="${cy + 4}" text-anchor="middle" class="a2ui-chart-center">${escapeHtml(formatNum(total))}</text>`
      : "";

  return `<svg class="a2ui-chart-svg a2ui-chart-svg-pie" viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet" xmlns="http://www.w3.org/2000/svg">
    ${paths}
    ${center}
  </svg>`;
}

function renderLegend(series: ChartSeries[], kind: ChartKind): string {
  if (kind === "pie" || kind === "donut") {
    const pts = series[0]?.points ?? [];
    const total = pts.reduce((s, p) => s + Math.abs(p.value), 0) || 1;
    return `<ul class="a2ui-chart-legend">${pts
      .map((p, i) => {
        const pct = ((Math.abs(p.value) / total) * 100).toFixed(1);
        return `<li><span class="a2ui-chart-swatch" style="background:${PALETTE[i % PALETTE.length]}"></span><span>${escapeHtml(p.label)}</span><strong>${escapeHtml(formatNum(p.value))}</strong><em>${pct}%</em></li>`;
      })
      .join("")}</ul>`;
  }
  if (series.length <= 1) return "";
  return `<ul class="a2ui-chart-legend a2ui-chart-legend-series">${series
    .map((s, i) => {
      return `<li><span class="a2ui-chart-swatch" style="background:${PALETTE[i % PALETTE.length]}"></span><span>${escapeHtml(s.name)}</span></li>`;
    })
    .join("")}</ul>`;
}

function unionLabels(series: ChartSeries[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const s of series) {
    for (const p of s.points) {
      if (!seen.has(p.label)) {
        seen.add(p.label);
        out.push(p.label);
      }
    }
  }
  return out.slice(0, MAX_POINTS);
}

function niceMax(v: number): number {
  if (!Number.isFinite(v) || v <= 0) return 1;
  const exp = Math.floor(Math.log10(v));
  const f = v / Math.pow(10, exp);
  let nf: number;
  if (f <= 1) nf = 1;
  else if (f <= 2) nf = 2;
  else if (f <= 5) nf = 5;
  else nf = 10;
  return nf * Math.pow(10, exp);
}

function formatNum(n: number): string {
  if (!Number.isFinite(n)) return "—";
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (Math.abs(n) >= 10_000) return `${(n / 1000).toFixed(1)}k`;
  if (Number.isInteger(n)) return String(n);
  return n.toFixed(2).replace(/\.?0+$/, "");
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, Math.max(n - 1, 1)) + "…";
}

function toStringArray(raw: unknown): string[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((x, i) => (x == null || x === "" ? String(i + 1) : String(x))).slice(0, MAX_POINTS);
}

function toNumberArray(raw: unknown): number[] {
  if (!Array.isArray(raw)) return [];
  const out: number[] = [];
  for (const x of raw) {
    if (isObject(x)) {
      const v = toNumber(x.value ?? x.y ?? x.count ?? x.amount);
      if (v != null) out.push(v);
    } else {
      const v = toNumber(x);
      if (v != null) out.push(v);
    }
  }
  return out.slice(0, MAX_POINTS);
}

function toNumber(v: unknown): number | null {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v.replace(/,/g, ""));
    if (Number.isFinite(n)) return n;
  }
  return null;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function asString(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return "";
}

function escapeAttr(value: string): string {
  return escapeHtml(value).replaceAll("'", "&#39;");
}
