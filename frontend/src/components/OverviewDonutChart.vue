<script setup lang="ts">
import { computed, ref } from "vue";

export type DonutSlice = {
  id: string;
  name: string;
  value: number;
  /** Optional secondary label shown as title tooltip */
  meta?: string;
};

const props = withDefaults(
  defineProps<{
    slices: DonutSlice[];
    colors?: string[];
    size?: number;
    emptyText?: string;
    valueLabel?: string;
  }>(),
  {
    size: 112,
    emptyText: "暂无数据",
    valueLabel: "合计",
    colors: () => [
      "#0f9f6e",
      "#14b8a6",
      "#3b82f6",
      "#6366f1",
      "#8b5cf6",
      "#f59e0b",
      "#f43f5e",
      "#64748b",
    ],
  },
);

const hoverId = ref<string | null>(null);

const total = computed(() => props.slices.reduce((s, x) => s + Math.max(0, x.value), 0));

const cx = computed(() => props.size / 2);
const cy = computed(() => props.size / 2);
const outerR = computed(() => props.size * 0.44);
const innerR = computed(() => props.size * 0.28);

type ArcSeg = DonutSlice & {
  color: string;
  start: number;
  end: number;
  path: string;
  pct: number;
};

function polar(r: number, angle: number) {
  const a = angle - Math.PI / 2;
  return { x: cx.value + r * Math.cos(a), y: cy.value + r * Math.sin(a) };
}

function donutPath(start: number, end: number): string {
  const large = end - start > Math.PI ? 1 : 0;
  const o0 = polar(outerR.value, start);
  const o1 = polar(outerR.value, end);
  const i1 = polar(innerR.value, end);
  const i0 = polar(innerR.value, start);
  return [
    `M ${o0.x} ${o0.y}`,
    `A ${outerR.value} ${outerR.value} 0 ${large} 1 ${o1.x} ${o1.y}`,
    `L ${i1.x} ${i1.y}`,
    `A ${innerR.value} ${innerR.value} 0 ${large} 0 ${i0.x} ${i0.y}`,
    "Z",
  ].join(" ");
}

const segments = computed((): ArcSeg[] => {
  const t = total.value;
  if (t <= 0) return [];
  let cursor = 0;
  return props.slices
    .filter((s) => s.value > 0)
    .map((s, i) => {
      const frac = s.value / t;
      const start = cursor;
      const end = cursor + frac * Math.PI * 2;
      cursor = end;
      return {
        ...s,
        color: props.colors[i % props.colors.length]!,
        start,
        end,
        path: donutPath(start, end),
        pct: frac * 100,
      };
    });
});

const active = computed(() => {
  if (hoverId.value) {
    return segments.value.find((s) => s.id === hoverId.value) ?? null;
  }
  return null;
});

const centerValue = computed(() => (active.value ? active.value.value : total.value));
const centerSub = computed(() => {
  if (active.value) return `${active.value.pct.toFixed(0)}%`;
  return props.valueLabel;
});
</script>

<template>
  <div class="overview-donut">
    <div v-if="!segments.length" class="overview-empty compact">{{ emptyText }}</div>
    <template v-else>
      <svg
        class="overview-donut-svg"
        :viewBox="`0 0 ${size} ${size}`"
        :width="size"
        :height="size"
        role="img"
      >
        <path
          v-for="seg in segments"
          :key="seg.id"
          :d="seg.path"
          :fill="seg.color"
          :class="['overview-donut-slice', { dim: hoverId && hoverId !== seg.id, active: hoverId === seg.id }]"
          @mouseenter="hoverId = seg.id"
          @mouseleave="hoverId = null"
        >
          <title>{{ seg.name }}{{ seg.meta ? ` · ${seg.meta}` : "" }} · {{ seg.value }} ({{ seg.pct.toFixed(1) }}%)</title>
        </path>
        <circle :cx="cx" :cy="cy" :r="innerR - 1" fill="#fff" pointer-events="none" />
        <text :x="cx" :y="cy - 5" text-anchor="middle" class="overview-donut-center-value">
          {{ centerValue }}
        </text>
        <text :x="cx" :y="cy + 11" text-anchor="middle" class="overview-donut-center-label">
          {{ centerSub }}
        </text>
      </svg>

      <ul class="overview-donut-legend">
        <li
          v-for="seg in segments"
          :key="seg.id"
          :class="{ active: hoverId === seg.id }"
          :title="`${seg.name}${seg.meta ? ` · ${seg.meta}` : ''} · ${seg.value} (${seg.pct.toFixed(1)}%)`"
          @mouseenter="hoverId = seg.id"
          @mouseleave="hoverId = null"
        >
          <i :style="{ background: seg.color }" aria-hidden="true" />
          <span class="name">{{ seg.name }}</span>
          <span class="val">{{ seg.value }}</span>
          <span class="pct">{{ seg.pct.toFixed(0) }}%</span>
        </li>
      </ul>
    </template>
  </div>
</template>
