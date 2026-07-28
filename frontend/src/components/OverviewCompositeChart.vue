<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = withDefaults(
  defineProps<{
    labels: string[];
    runs: number[];
    tools: number[];
    /** 0–100 success rate per day */
    runRates: number[];
    height?: number;
  }>(),
  { height: 320 },
);

type TooltipState = {
  visible: boolean;
  x: number;
  y: number;
  label: string;
  runs: number;
  tools: number;
  rate: number;
};

const tooltip = ref<TooltipState>({
  visible: false,
  x: 0,
  y: 0,
  label: "",
  runs: 0,
  tools: 0,
  rate: 0,
});
/** Active day column for axis-pointer style hover band. */
const hoverIndex = ref<number | null>(null);
const wrapRef = ref<HTMLElement | null>(null);
const containerWidth = ref(640);

const padL = 40;
const padR = 44;
const padT = 20;
const padB = 32;

/** Fit entire range into the panel width — no horizontal scroll. */
const chartWidth = computed(() => Math.max(320, containerWidth.value));
const plotH = computed(() => props.height - padT - padB);
const plotW = computed(() => Math.max(40, chartWidth.value - padL - padR));
const maxCount = computed(() => Math.max(...props.runs, ...props.tools, 1));

let resizeObserver: ResizeObserver | null = null;

function measure() {
  const el = wrapRef.value;
  if (!el) return;
  const w = Math.floor(el.clientWidth);
  if (w > 0) containerWidth.value = w;
}

onMounted(() => {
  measure();
  if (typeof ResizeObserver !== "undefined" && wrapRef.value) {
    resizeObserver = new ResizeObserver(() => measure());
    resizeObserver.observe(wrapRef.value);
  } else {
    window.addEventListener("resize", measure);
  }
});

onBeforeUnmount(() => {
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.removeEventListener("resize", measure);
});

watch(
  () => props.labels.length,
  () => {
    // Re-measure after layout settles when range length changes.
    requestAnimationFrame(measure);
  },
);

function shortLabel(label: string) {
  const n = props.labels.length;
  // Dense ranges: show MM-DD only; very dense: every other / every third label handled in template.
  return label.length >= 10 ? label.slice(5) : label;
}

function showLabel(i: number) {
  const n = props.labels.length;
  if (n <= 14) return true;
  if (n <= 31) return i % 2 === 0 || i === n - 1;
  if (n <= 62) return i % 3 === 0 || i === n - 1;
  const step = Math.ceil(n / 12);
  return i % step === 0 || i === n - 1;
}

function xCenter(i: number) {
  const n = Math.max(props.labels.length, 1);
  return padL + (i + 0.5) * (plotW.value / n);
}

function slotWidth() {
  return plotW.value / Math.max(props.labels.length, 1);
}

function barX(i: number, seriesIndex: 0 | 1) {
  const barW = barWidth();
  const gap = Math.min(3, Math.max(1, barW * 0.15));
  const groupW = barW * 2 + gap;
  return xCenter(i) - groupW / 2 + seriesIndex * (barW + gap);
}

function barWidth() {
  // Keep both bars readable but allow compression for long ranges.
  return Math.max(2, Math.min(14, slotWidth() * 0.28));
}

function barHeight(value: number) {
  return Math.max(value > 0 ? 2 : 0, (value / maxCount.value) * plotH.value);
}

function barY(value: number) {
  return padT + plotH.value - barHeight(value);
}

function rateY(rate: number) {
  const r = Math.min(100, Math.max(0, rate));
  return padT + plotH.value - (r / 100) * plotH.value;
}

function countY(tick: number) {
  return padT + plotH.value - (tick / maxCount.value) * plotH.value;
}

const linePoints = computed(() =>
  props.runRates.map((rate, i) => `${xCenter(i).toFixed(2)},${rateY(rate).toFixed(2)}`).join(" "),
);

const areaPath = computed(() => {
  if (!props.labels.length) return "";
  const top = props.runRates
    .map((rate, i) => `${i === 0 ? "M" : "L"}${xCenter(i).toFixed(2)},${rateY(rate).toFixed(2)}`)
    .join(" ");
  const lastX = xCenter(props.labels.length - 1);
  const firstX = xCenter(0);
  const baseY = padT + plotH.value;
  return `${top} L${lastX.toFixed(2)},${baseY} L${firstX.toFixed(2)},${baseY} Z`;
});

const yTicks = computed(() => {
  const max = maxCount.value;
  return [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(max * f));
});

const pointRadius = computed(() => (props.labels.length > 40 ? 2.5 : props.labels.length > 20 ? 3 : 4));

function showDay(i: number, event: MouseEvent) {
  const host = wrapRef.value;
  if (!host) return;
  const rect = host.getBoundingClientRect();
  hoverIndex.value = i;
  // Prefer tooltip to the right of the column; flip left near the edge.
  const colMid = ((padL + (i + 0.5) * slotWidth()) / chartWidth.value) * rect.width;
  const preferRight = colMid + 12;
  const tipX = preferRight + 160 > rect.width ? Math.max(8, colMid - 168) : preferRight;
  tooltip.value = {
    visible: true,
    x: tipX,
    y: Math.max(12, event.clientY - rect.top - 8),
    label: props.labels[i] || "",
    runs: props.runs[i] ?? 0,
    tools: props.tools[i] ?? 0,
    rate: props.runRates[i] ?? 0,
  };
}

function moveTooltip(event: MouseEvent) {
  if (!tooltip.value.visible || !wrapRef.value || hoverIndex.value == null) return;
  const rect = wrapRef.value.getBoundingClientRect();
  const i = hoverIndex.value;
  const colMid = ((padL + (i + 0.5) * slotWidth()) / chartWidth.value) * rect.width;
  const preferRight = colMid + 12;
  const tipX = preferRight + 160 > rect.width ? Math.max(8, colMid - 168) : preferRight;
  tooltip.value = {
    ...tooltip.value,
    x: tipX,
    y: Math.max(12, event.clientY - rect.top - 8),
  };
}

function hideTooltip() {
  hoverIndex.value = null;
  tooltip.value = { ...tooltip.value, visible: false };
}

function bandX(i: number) {
  return padL + i * slotWidth();
}

function formatPct(rate: number) {
  return `${rate.toFixed(1)}%`;
}
</script>

<template>
  <div ref="wrapRef" class="overview-composite-wrap">
    <svg
      class="overview-composite-chart"
      :viewBox="`0 0 ${chartWidth} ${height}`"
      :height="height"
      preserveAspectRatio="xMidYMid meet"
      role="img"
      aria-label="流量与运行质量趋势"
      @mousemove="moveTooltip"
      @mouseleave="hideTooltip"
    >
      <defs>
        <linearGradient id="ov-run-bar" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#2dd4bf" />
          <stop offset="100%" stop-color="#0d9488" />
        </linearGradient>
        <linearGradient id="ov-tool-bar" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#818cf8" />
          <stop offset="100%" stop-color="#4f46e5" />
        </linearGradient>
        <linearGradient id="ov-rate-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="rgba(251, 113, 133, 0.22)" />
          <stop offset="100%" stop-color="rgba(251, 113, 133, 0)" />
        </linearGradient>
      </defs>

      <g>
        <line
          v-for="tick in yTicks"
          :key="`g-${tick}`"
          :x1="padL"
          :y1="countY(tick)"
          :x2="chartWidth - padR"
          :y2="countY(tick)"
          stroke="#f1f5f9"
          stroke-dasharray="4 4"
        />
        <text
          v-for="tick in yTicks"
          :key="`yl-${tick}`"
          :x="padL - 8"
          :y="countY(tick) + 3"
          text-anchor="end"
          class="overview-composite-axis"
        >
          {{ tick }}
        </text>
        <text
          v-for="pct in [0, 50, 100]"
          :key="`yr-${pct}`"
          :x="chartWidth - padR + 8"
          :y="rateY(pct) + 3"
          text-anchor="start"
          class="overview-composite-axis"
        >
          {{ pct }}%
        </text>
      </g>

      <!-- Axis-pointer style vertical band (behind series) -->
      <rect
        v-if="hoverIndex != null"
        class="overview-composite-band"
        :x="bandX(hoverIndex)"
        :y="padT"
        :width="slotWidth()"
        :height="plotH"
        pointer-events="none"
      />

      <g>
        <template v-for="(label, i) in labels" :key="`bars-${label}`">
          <rect
            :x="barX(i, 0)"
            :y="barY(runs[i] ?? 0)"
            :width="barWidth()"
            :height="barHeight(runs[i] ?? 0)"
            fill="url(#ov-run-bar)"
            rx="2"
            pointer-events="none"
            :class="{ 'overview-composite-bar--dim': hoverIndex != null && hoverIndex !== i }"
          />
          <rect
            :x="barX(i, 1)"
            :y="barY(tools[i] ?? 0)"
            :width="barWidth()"
            :height="barHeight(tools[i] ?? 0)"
            fill="url(#ov-tool-bar)"
            rx="2"
            pointer-events="none"
            :class="{ 'overview-composite-bar--dim': hoverIndex != null && hoverIndex !== i }"
          />
        </template>
      </g>

      <path v-if="labels.length" :d="areaPath" fill="url(#ov-rate-area)" pointer-events="none" />
      <polyline
        v-if="labels.length"
        :points="linePoints"
        fill="none"
        stroke="#fb7185"
        stroke-width="2.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        pointer-events="none"
      />
      <circle
        v-for="(rate, i) in runRates"
        :key="`pt-${i}`"
        :cx="xCenter(i)"
        :cy="rateY(rate)"
        :r="pointRadius"
        fill="#fff"
        stroke="#fb7185"
        stroke-width="2"
        pointer-events="none"
      />

      <g>
        <rect
          v-for="(label, i) in labels"
          :key="`hit-${label}`"
          :x="padL + i * slotWidth()"
          :y="padT"
          :width="slotWidth()"
          :height="plotH"
          fill="transparent"
          class="overview-composite-hit"
          @mouseenter="showDay(i, $event)"
          @mousemove="showDay(i, $event)"
        />
        <text
          v-for="(label, i) in labels"
          v-show="showLabel(i)"
          :key="`xl-${label}`"
          :x="xCenter(i)"
          :y="height - 10"
          text-anchor="middle"
          class="overview-composite-axis"
        >
          {{ shortLabel(label) }}
        </text>
      </g>
    </svg>

    <div
      v-if="tooltip.visible"
      class="overview-chart-tooltip overview-composite-tooltip"
      :style="{ left: `${tooltip.x}px`, top: `${tooltip.y}px` }"
      role="tooltip"
    >
      <strong>{{ tooltip.label }}</strong>
      <p><i style="background: #0d9488" /><span>Agent Run</span><b>{{ tooltip.runs }}</b></p>
      <p><i style="background: #4f46e5" /><span>工具调用</span><b>{{ tooltip.tools }}</b></p>
      <p><i style="background: #fb7185" /><span>链路成功率</span><b>{{ formatPct(tooltip.rate) }}</b></p>
    </div>
  </div>
</template>
