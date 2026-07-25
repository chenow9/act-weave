<script setup lang="ts">
import { computed, ref } from "vue";

const props = withDefaults(
  defineProps<{
    values: number[];
    labels?: string[];
    width?: number;
    height?: number;
    stroke?: string;
    fill?: string;
    unit?: string;
  }>(),
  {
    labels: () => [],
    width: 280,
    height: 72,
    stroke: "#0d9488",
    fill: "rgba(13, 148, 136, 0.1)",
    unit: "",
  },
);

const wrapRef = ref<HTMLElement | null>(null);
const tip = ref({ visible: false, x: 0, y: 0, label: "", value: "" });

const path = computed(() => {
  const vals = props.values.length ? props.values : [0];
  const max = Math.max(...vals, 1);
  const min = Math.min(...vals, 0);
  const span = Math.max(max - min, 1);
  const w = props.width;
  const h = props.height;
  const pad = 6;
  const step = vals.length > 1 ? (w - pad * 2) / (vals.length - 1) : 0;
  const points = vals.map((v, i) => {
    const x = pad + i * step;
    const y = h - pad - ((v - min) / span) * (h - pad * 2);
    return { x, y, v, i };
  });
  if (points.length === 1) {
    return {
      line: `M ${points[0].x} ${points[0].y}`,
      area: "",
      points,
    };
  }
  const line = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(" ");
  const area = `${line} L ${points[points.length - 1].x.toFixed(1)} ${h - pad} L ${points[0].x.toFixed(1)} ${h - pad} Z`;
  return { line, area, points };
});

function onPointEnter(index: number, event: MouseEvent) {
  const host = wrapRef.value;
  if (!host) return;
  const rect = host.getBoundingClientRect();
  const value = props.values[index] ?? 0;
  const label = props.labels[index] || `#${index + 1}`;
  tip.value = {
    visible: true,
    x: event.clientX - rect.left + 10,
    y: event.clientY - rect.top - 8,
    label,
    value: props.unit ? `${formatNum(value)}${props.unit}` : formatNum(value),
  };
}

function hideTip() {
  tip.value.visible = false;
}

function formatNum(n: number) {
  if (!Number.isFinite(n)) return "—";
  return Number.isInteger(n) ? String(n) : n.toFixed(1);
}
</script>

<template>
  <div ref="wrapRef" class="overview-spark-wrap">
    <svg class="overview-sparkline" :viewBox="`0 0 ${width} ${height}`" role="img" @mouseleave="hideTip">
      <path v-if="path.area" :d="path.area" :fill="fill" />
      <path
        :d="path.line"
        fill="none"
        :stroke="stroke"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle
        v-for="p in path.points"
        :key="p.i"
        :cx="p.x"
        :cy="p.y"
        r="4"
        fill="transparent"
        class="overview-spark-hit"
        @mouseenter="onPointEnter(p.i, $event)"
        @mousemove="onPointEnter(p.i, $event)"
      />
      <circle
        v-for="p in path.points"
        :key="`dot-${p.i}`"
        :cx="p.x"
        :cy="p.y"
        r="2.5"
        :fill="stroke"
        opacity="0.85"
        pointer-events="none"
      />
    </svg>
    <div
      v-if="tip.visible"
      class="overview-chart-tooltip overview-chart-tooltip--compact"
      :style="{ left: `${tip.x}px`, top: `${tip.y}px` }"
    >
      <strong>{{ tip.label }}</strong>
      <p><b>{{ tip.value }}</b></p>
    </div>
  </div>
</template>
