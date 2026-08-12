<script setup lang="ts">
/**
 * The chart component. Everything drawn here comes from semantic data —
 * labels, values, a unit and a value format — and every visual decision is
 * Console's own, which is what lets the same surface look native in two clients.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import { chartGeometry, formatValue, radialCenter, type ChartGeometry } from "../chart-geometry";
import { useA2UIValues } from "../context";
import {
  A2UI_LIMITS,
  A2UI_VALUE_FORMATS,
  isA2UIChartType,
  type A2UIChartType,
  type A2UIComponentNode,
  type A2UIValueFormat,
} from "../generated/catalog.gen";
import A2UIPlaceholder from "../A2UIPlaceholder.vue";

const STACKABLE: ReadonlySet<A2UIChartType> = new Set<A2UIChartType>(["bar", "hbar", "area"]);
const SINGLE_SERIES: ReadonlySet<A2UIChartType> = new Set<A2UIChartType>(["pie", "donut"]);

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();
const { t } = useI18n();

const chartType = computed(() => (isA2UIChartType(props.node.chartType) ? props.node.chartType : undefined));
const title = computed(() => values.string(props.node.title));
const unit = computed(() => (typeof props.node.unit === "string" ? props.node.unit : ""));
const valueFormat = computed<A2UIValueFormat>(() => {
  const format = props.node.valueFormat;
  return (A2UI_VALUE_FORMATS as readonly string[]).includes(format as string) ? (format as A2UIValueFormat) : "plain";
});

/**
 * The server enforces these limits too. Applying them again means a surface from
 * another producer cannot drive this renderer into unbounded work.
 */
const series = computed(() => {
  const type = chartType.value;
  if (!type) return [];
  let resolved = values.series(props.node.series).slice(0, A2UI_LIMITS.maxChartSeries);
  if (SINGLE_SERIES.has(type)) resolved = resolved.slice(0, 1);
  return resolved.map((entry) => ({ ...entry, points: entry.points.slice(0, A2UI_LIMITS.maxChartPoints) }));
});

const stacked = computed(() => props.node.stacked === true && !!chartType.value && STACKABLE.has(chartType.value));

const format = (value: number) => formatValue(value, valueFormat.value, unit.value);

const geometry = computed<ChartGeometry | undefined>(() => {
  const type = chartType.value;
  if (!type || !series.value.length) return undefined;
  return chartGeometry({
    chartType: type,
    series: series.value,
    stacked: stacked.value,
    format,
    seriesName: (index, name) => name ?? t("a2ui.seriesFallback", { index: index + 1 }),
  });
});

const badge = computed(() => {
  const type = chartType.value;
  if (!type) return "";
  const kind = t(`a2ui.chartTypes.${type}`);
  return unit.value ? `${kind} · ${unit.value}` : kind;
});
</script>

<template>
  <A2UIPlaceholder v-if="!chartType" :reason="t('a2ui.chartWithoutType')" />
  <figure v-else class="a2ui-chart" :data-a2ui-chart="chartType" :data-a2ui-stacked="stacked ? 'true' : undefined">
    <div class="a2ui-chart-head">
      <h3 v-if="title" class="a2ui-title">{{ title }}</h3>
      <span class="a2ui-chart-badge">{{ badge }}</span>
    </div>

    <p v-if="!geometry" class="a2ui-empty">{{ t("a2ui.chartNoData") }}</p>

    <template v-else>
      <div class="a2ui-chart-body" role="img" :aria-label="title || badge">
        <svg
          :class="['a2ui-chart-svg', { 'a2ui-chart-svg-pie': geometry.radial }]"
          :viewBox="`0 0 ${geometry.width} ${geometry.height}`"
          preserveAspectRatio="xMidYMid meet"
        >
          <line
            v-for="line in geometry.grid"
            :key="line.key"
            class="a2ui-chart-grid"
            :x1="line.x1"
            :y1="line.y1"
            :x2="line.x2"
            :y2="line.y2"
          />
          <text
            v-for="tick in geometry.valueLabels"
            :key="tick.key"
            class="a2ui-chart-axis"
            :x="tick.x"
            :y="tick.y"
            :text-anchor="tick.anchor"
          >
            {{ tick.text }}
          </text>

          <rect
            v-for="bar in geometry.bars"
            :key="bar.key"
            :x="bar.x"
            :y="bar.y"
            :width="bar.width"
            :height="bar.height"
            rx="3"
            :fill="bar.color"
            opacity="0.92"
          >
            <title>{{ bar.title }}</title>
          </rect>

          <template v-for="line in geometry.lines" :key="line.key">
            <path v-if="line.area" :d="line.area" :fill="line.color" opacity="0.18" />
            <path
              :d="line.line"
              fill="none"
              :stroke="line.color"
              stroke-width="2.5"
              stroke-linejoin="round"
              stroke-linecap="round"
            />
            <circle v-for="dot in line.dots" :key="dot.key" :cx="dot.cx" :cy="dot.cy" r="3.5" :fill="line.color">
              <title>{{ dot.title }}</title>
            </circle>
          </template>

          <path
            v-for="slice in geometry.slices"
            :key="slice.key"
            :d="slice.path"
            :fill="slice.color"
            stroke="#fff"
            stroke-width="1.5"
          >
            <title>{{ slice.title }}</title>
          </path>
          <text
            v-if="geometry.radial && !geometry.slices.length"
            class="a2ui-chart-axis"
            :x="geometry.width / 2"
            :y="geometry.height / 2"
            text-anchor="middle"
          >
            {{ t("a2ui.chartNoPositive") }}
          </text>
          <text
            v-if="geometry.centerLabel"
            class="a2ui-chart-center"
            :x="radialCenter.x"
            :y="radialCenter.y + 4"
            text-anchor="middle"
          >
            {{ geometry.centerLabel }}
          </text>

          <text
            v-for="label in geometry.categoryLabels"
            :key="label.key"
            class="a2ui-chart-axis"
            :x="label.x"
            :y="label.y"
            :text-anchor="label.anchor"
          >
            {{ label.text }}
          </text>
        </svg>
      </div>

      <ul
        v-if="geometry.legend.length"
        :class="['a2ui-chart-legend', { 'a2ui-chart-legend-series': !geometry.radial }]"
      >
        <li v-for="item in geometry.legend" :key="item.key">
          <span class="a2ui-chart-swatch" :style="{ background: item.color }" aria-hidden="true" />
          <span>{{ item.label }}</span>
          <strong v-if="item.value">{{ item.value }}</strong>
          <em v-if="item.share">{{ item.share }}</em>
        </li>
      </ul>
    </template>
  </figure>
</template>
