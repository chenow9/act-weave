/**
 * Chart component renderer: reads the catalog contract, hands semantic data to
 * the drawing functions. No shape detection — `chartType` and `series` are
 * exactly where the catalog says they are.
 */

import type { A2UIChartType, A2UIComponentNode, A2UIRenderCtx, A2UIValueFormat } from "./generated/catalog.gen";
import { A2UI_LIMITS, A2UI_VALUE_FORMATS, isA2UIChartType } from "./generated/catalog.gen";
import { ChartDrawing, drawChart, formatValue, renderLegend, unionLabels } from "./chart-svg";
import { installChartHover } from "./chart-hover";
import { escapeAttr, escapeHtml } from "./html";

const CHART_LABELS: Record<A2UIChartType, string> = {
  bar: "柱状图",
  hbar: "条形图",
  line: "折线图",
  area: "面积图",
  pie: "饼图",
  donut: "环图",
};

const STACKABLE: ReadonlySet<A2UIChartType> = new Set<A2UIChartType>(["bar", "hbar", "area"]);
const SINGLE_SERIES: ReadonlySet<A2UIChartType> = new Set<A2UIChartType>(["pie", "donut"]);

export function renderChart(node: A2UIComponentNode, ctx: A2UIRenderCtx<string>): string {
  const chartType = node.chartType;
  if (!isA2UIChartType(chartType)) {
    return ctx.placeholder(`Chart 缺少可识别的 chartType`);
  }

  const title = ctx.resolveString(node.title);
  const unit = typeof node.unit === "string" ? node.unit : "";
  const valueFormat: A2UIValueFormat = isValueFormat(node.valueFormat) ? node.valueFormat : "plain";
  const format = (value: number) => formatValue(value, valueFormat, unit);

  let series = ctx.resolveSeries(node.series).slice(0, A2UI_LIMITS.maxChartSeries);
  if (SINGLE_SERIES.has(chartType)) series = series.slice(0, 1);
  series = series.map((entry) => ({ ...entry, points: entry.points.slice(0, A2UI_LIMITS.maxChartPoints) }));

  const badge = `${CHART_LABELS[chartType]}${unit ? ` · ${unit}` : ""}`;
  if (!series.length) {
    return `<div class="a2ui-chart a2ui-chart-empty">${heading(title)}<p class="a2ui-empty">（统计图无有效数据）</p></div>`;
  }

  const drawing: ChartDrawing = {
    series,
    labels: unionLabels(series),
    stacked: node.stacked === true && STACKABLE.has(chartType),
    format,
  };

  // Charts are rendered as markup, so hover is wired once, by delegation, rather
  // than per figure.
  installChartHover();
  const chart = drawChart(chartType, drawing);

  return `
    <figure class="a2ui-chart" data-a2ui-chart="${escapeAttr(chartType)}"${drawing.stacked ? ' data-a2ui-stacked="true"' : ""}>
      <div class="a2ui-chart-head">
        ${heading(title)}
        <span class="a2ui-chart-badge">${escapeHtml(badge)}</span>
      </div>
      <div class="a2ui-chart-body" role="img" aria-label="${escapeAttr(title || badge)}">
        ${chart.svg}
        ${chart.tips}
      </div>
      ${renderLegend(chartType, drawing)}
    </figure>
  `;
}

function heading(title: string): string {
  return title ? `<h3 class="a2ui-title">${escapeHtml(title)}</h3>` : "";
}

function isValueFormat(value: unknown): value is A2UIValueFormat {
  return typeof value === "string" && (A2UI_VALUE_FORMATS as readonly string[]).includes(value);
}
