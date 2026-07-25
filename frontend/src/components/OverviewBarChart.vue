<script setup lang="ts">
import { computed, ref } from "vue";

const props = withDefaults(
  defineProps<{
    labels: string[];
    series: Array<{ key: string; label: string; color: string; values: number[] }>;
    height?: number;
  }>(),
  { height: 220 },
);

type TooltipState = {
  visible: boolean;
  x: number;
  y: number;
  label: string;
  rows: Array<{ label: string; value: number; color: string }>;
};

const tooltip = ref<TooltipState>({ visible: false, x: 0, y: 0, label: "", rows: [] });
const wrapRef = ref<HTMLElement | null>(null);

const maxValue = computed(() => {
  const all = props.series.flatMap((s) => s.values);
  return Math.max(...all, 1);
});

const chartWidth = computed(() => Math.max(360, props.labels.length * 48));
const groupWidth = 40;
const plotTop = 16;
const plotBottom = 28;

function groupX(dayIndex: number) {
  return 36 + dayIndex * 48;
}

function barX(dayIndex: number, seriesIndex: number) {
  const barWidth = Math.max(5, (groupWidth - 6) / Math.max(props.series.length, 1));
  return groupX(dayIndex) + 3 + seriesIndex * barWidth;
}

function barWidth() {
  return Math.max(5, (groupWidth - 6) / Math.max(props.series.length, 1));
}

function barHeight(value: number) {
  const plotH = props.height - plotTop - plotBottom;
  return Math.max(value > 0 ? 2 : 0, (value / maxValue.value) * plotH);
}

function barY(value: number) {
  return props.height - plotBottom - barHeight(value);
}

function shortLabel(label: string) {
  return label.length >= 10 ? label.slice(5) : label;
}

function showDayTooltip(dayIndex: number, event: MouseEvent) {
  const host = wrapRef.value;
  if (!host) return;
  const rect = host.getBoundingClientRect();
  const rows = props.series.map((serie) => ({
    label: serie.label,
    value: serie.values[dayIndex] ?? 0,
    color: serie.color,
  }));
  tooltip.value = {
    visible: true,
    x: event.clientX - rect.left + 12,
    y: event.clientY - rect.top - 8,
    label: props.labels[dayIndex] || "",
    rows,
  };
}

function moveTooltip(event: MouseEvent) {
  if (!tooltip.value.visible || !wrapRef.value) return;
  const rect = wrapRef.value.getBoundingClientRect();
  tooltip.value = {
    ...tooltip.value,
    x: event.clientX - rect.left + 12,
    y: event.clientY - rect.top - 8,
  };
}

function hideTooltip() {
  tooltip.value = { ...tooltip.value, visible: false };
}

const yTicks = computed(() => {
  const max = maxValue.value;
  const steps = 4;
  return Array.from({ length: steps + 1 }, (_, i) => Math.round((max * i) / steps));
});
</script>

<template>
  <div ref="wrapRef" class="overview-bar-wrap">
    <div class="overview-bar-scroll">
      <svg
        class="overview-bar-chart"
        :viewBox="`0 0 ${chartWidth} ${height}`"
        :style="{ minWidth: `${chartWidth}px`, height: `${height}px` }"
        role="img"
        @mousemove="moveTooltip"
        @mouseleave="hideTooltip"
      >
        <!-- Y grid -->
        <g>
          <line
            v-for="tick in yTicks"
            :key="`yt-${tick}`"
            x1="28"
            :y1="barY(tick)"
            :x2="chartWidth - 8"
            :y2="barY(tick)"
            stroke="#eef2f7"
          />
          <text
            v-for="tick in yTicks"
            :key="`yl-${tick}`"
            x="24"
            :y="barY(tick) + 3"
            text-anchor="end"
            class="overview-bar-axis"
          >
            {{ tick }}
          </text>
        </g>

        <line x1="28" :y1="height - plotBottom" :x2="chartWidth - 8" :y2="height - plotBottom" stroke="#e2e8f0" />

        <!-- Hover hit areas per day -->
        <g>
          <rect
            v-for="(label, di) in labels"
            :key="`hit-${label}`"
            :x="groupX(di)"
            :y="plotTop"
            :width="groupWidth"
            :height="height - plotTop - plotBottom"
            fill="transparent"
            class="overview-bar-hit"
            @mouseenter="showDayTooltip(di, $event)"
            @mousemove="showDayTooltip(di, $event)"
          />
        </g>

        <g v-for="(serie, si) in series" :key="serie.key">
          <rect
            v-for="(value, di) in serie.values"
            :key="`${serie.key}-${di}`"
            :x="barX(di, si)"
            :y="barY(value)"
            :width="barWidth()"
            :height="barHeight(value)"
            :fill="serie.color"
            rx="2"
            pointer-events="none"
          />
        </g>

        <text
          v-for="(label, di) in labels"
          :key="label"
          :x="groupX(di) + groupWidth / 2"
          :y="height - 8"
          text-anchor="middle"
          class="overview-bar-label"
        >
          {{ shortLabel(label) }}
        </text>
      </svg>
    </div>

    <div
      v-if="tooltip.visible"
      class="overview-chart-tooltip"
      :style="{ left: `${tooltip.x}px`, top: `${tooltip.y}px` }"
      role="tooltip"
    >
      <strong>{{ tooltip.label }}</strong>
      <p v-for="row in tooltip.rows" :key="row.label">
        <i :style="{ background: row.color }" />
        <span>{{ row.label }}</span>
        <b>{{ row.value }}</b>
      </p>
    </div>

    <div class="overview-bar-legend">
      <span v-for="serie in series" :key="serie.key">
        <i :style="{ background: serie.color }" />
        {{ serie.label }}
      </span>
    </div>
  </div>
</template>
