<script setup lang="ts">
/**
 * The chart component. Everything drawn here comes from semantic data —
 * labels, values, a unit and a value format — and every visual decision is
 * Console's own, which is what lets the same surface look native in two clients.
 */
import { computed, ref } from "vue";
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
    totalName: t("a2ui.chartTotal"),
  });
});

const badge = computed(() => {
  const type = chartType.value;
  if (!type) return "";
  const kind = t(`a2ui.chartTypes.${type}`);
  return unit.value ? `${kind} · ${unit.value}` : kind;
});

/** Widest tooltip plus its gap, per the stylesheet. */
const TIP_SPACE = 232;

const body = ref<HTMLElement | null>(null);
const active = ref<number | null>(null);
/** Cursor offset inside the plot, and which side of it the tooltip may use. */
const cursor = ref({ x: 0, flip: false });

const activeHit = computed(() => (active.value === null ? undefined : geometry.value?.hits[active.value]));

function trackCursor(index: number, event: MouseEvent): void {
  active.value = index;
  const box = body.value?.getBoundingClientRect();
  if (!box || box.width === 0) return;
  const x = event.clientX - box.left;
  // Where the card would run out of room the tooltip changes sides rather than
  // being clamped, which would make it stop tracking the cursor.
  cursor.value = { x, flip: x + TIP_SPACE > box.width };
}
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
      <div
        ref="body"
        class="a2ui-chart-body"
        :class="{ 'is-hovering': active !== null }"
        role="img"
        :aria-label="title || badge"
        @mouseleave="active = null"
      >
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
            v-if="activeHit?.rect"
            class="a2ui-chart-band"
            :x="activeHit.rect.x"
            :y="activeHit.rect.y"
            :width="activeHit.rect.width"
            :height="activeHit.rect.height"
          />
          <line
            v-if="activeHit?.guide"
            class="a2ui-chart-guide"
            :x1="activeHit.guide.x1"
            :y1="activeHit.guide.y1"
            :x2="activeHit.guide.x2"
            :y2="activeHit.guide.y2"
          />

          <rect
            v-for="bar in geometry.bars"
            :key="bar.key"
            class="a2ui-chart-bar"
            :class="{ 'is-active': bar.hit === active }"
            :x="bar.x"
            :y="bar.y"
            :width="bar.width"
            :height="bar.height"
            rx="3"
            :fill="bar.color"
          />

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
            <circle
              v-for="dot in line.dots"
              :key="dot.key"
              class="a2ui-chart-dot"
              :class="{ 'is-active': dot.hit === active }"
              :cx="dot.cx"
              :cy="dot.cy"
              r="3.5"
              :fill="line.color"
            />
          </template>

          <path
            v-for="slice in geometry.slices"
            :key="slice.key"
            class="a2ui-chart-slice"
            :class="{ 'is-active': slice.hit === active }"
            :d="slice.path"
            :fill="slice.color"
            stroke="#fff"
            stroke-width="1.5"
          />
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

          <!-- Targets come last so they sit above the drawing and receive the cursor. -->
          <template v-for="(hit, hitIndex) in geometry.hits" :key="hit.key">
            <rect
              v-if="hit.rect"
              class="a2ui-chart-hit"
              :x="hit.rect.x"
              :y="hit.rect.y"
              :width="hit.rect.width"
              :height="hit.rect.height"
              @mousemove="trackCursor(hitIndex, $event)"
            />
            <path v-else class="a2ui-chart-hit" :d="hit.path" @mousemove="trackCursor(hitIndex, $event)" />
          </template>
        </svg>

        <div
          v-if="activeHit"
          class="a2ui-chart-tip"
          :class="{ 'is-flipped': cursor.flip }"
          :style="{ left: `${cursor.x}px` }"
        >
          <strong>{{ activeHit.label }}</strong>
          <span v-for="row in activeHit.rows" :key="row.key" class="a2ui-chart-tip-row">
            <span v-if="row.color" class="a2ui-chart-swatch" :style="{ background: row.color }" aria-hidden="true" />
            <span v-if="row.name" class="a2ui-chart-tip-name">{{ row.name }}</span>
            <strong>{{ row.value }}</strong>
            <em v-if="row.share">{{ row.share }}</em>
          </span>
        </div>
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
