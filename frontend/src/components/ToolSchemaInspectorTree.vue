<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import type { ToolSchemaNode } from "../types/domain";

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    nodes: ToolSchemaNode[];
    rootLabel?: string;
    expandAll?: boolean;
    emptyText?: string;
  }>(),
  {
    rootLabel: "Body",
    expandAll: false,
    emptyText: "",
  },
);

interface InspectorRow {
  node: ToolSchemaNode;
  depth: number;
}

const expandedIds = ref<Set<string>>(new Set());
const selectedId = ref("");
const hoverTip = ref<{ text: string; x: number; y: number; placeAbove: boolean } | null>(null);
let hoverTimer = 0;

const typeShort: Record<string, string> = {
  string: "str",
  integer: "int",
  number: "num",
  boolean: "bool",
  object: "obj",
  array: "arr",
};

function typeShortLabel(type: string) {
  return typeShort[type] || type.slice(0, 3);
}

function rowHasChildren(node: ToolSchemaNode) {
  return node.type === "object" || node.type === "array";
}

function childNodes(node: ToolSchemaNode): ToolSchemaNode[] {
  if (node.type === "object") return node.children || [];
  if (node.type === "array" && node.item) return [node.item];
  return [];
}

function collectExpandableIds(nodes: ToolSchemaNode[]): string[] {
  return nodes.flatMap((node) => {
    const children = childNodes(node);
    return children.length ? [node.id, ...collectExpandableIds(children)] : [];
  });
}

function flattenVisible(nodes: ToolSchemaNode[], depth: number): InspectorRow[] {
  return nodes.flatMap((node) => {
    const children = childNodes(node);
    const nested = expandedIds.value.has(node.id) ? flattenVisible(children, depth + 1) : [];
    return [{ node, depth }, ...nested];
  });
}

function syncExpanded() {
  expandedIds.value = props.expandAll ? new Set(collectExpandableIds(props.nodes)) : new Set();
}

watch(
  () => [props.nodes, props.expandAll] as const,
  () => {
    syncExpanded();
    if (!selectedId.value || !flattenVisible(props.nodes, 1).some((row) => row.node.id === selectedId.value)) {
      selectedId.value = props.nodes[0]?.id || "";
    }
  },
  { immediate: true, deep: true },
);

const hintId = computed(() => `tool-schema-inspector-hint-${props.rootLabel}`);
const rows = computed(() => flattenVisible(props.nodes, 1));

const selectedNode = computed(() => rows.value.find((row) => row.node.id === selectedId.value)?.node || null);

function toggleNode(node: ToolSchemaNode) {
  if (!rowHasChildren(node)) return;
  const next = new Set(expandedIds.value);
  if (next.has(node.id)) next.delete(node.id);
  else next.add(node.id);
  expandedIds.value = next;
}

function selectRow(node: ToolSchemaNode) {
  selectedId.value = node.id;
}

function nameOf(node: ToolSchemaNode) {
  if (node.name) return node.name;
  if (node.type === "object" || node.type === "array") return t("tools.arrayItem");
  return t("tools.unnamedField");
}

function typeText(node: ToolSchemaNode) {
  return node.format ? `${node.type}(${node.format})` : node.type;
}

function rowTitle(node: ToolSchemaNode) {
  return (node.description || "").trim();
}

function tipPosition(target: HTMLElement) {
  const rect = target.getBoundingClientRect();
  const width = Math.min(320, Math.max(180, window.innerWidth - 24));
  const x = Math.min(Math.max(12, rect.left + 48), window.innerWidth - width - 12);
  const below = rect.bottom + 8;
  const y = below + 72 > window.innerHeight ? rect.top - 8 : below;
  return { x, y, placeAbove: below + 72 > window.innerHeight };
}

function showHoverTip(event: MouseEvent, node: ToolSchemaNode) {
  const text = rowTitle(node);
  if (!text) {
    hideHoverTip();
    return;
  }
  const target = event.currentTarget as HTMLElement;
  const place = () => {
    hoverTip.value = { text, ...tipPosition(target) };
  };
  if (hoverTip.value) {
    place();
    return;
  }
  window.clearTimeout(hoverTimer);
  hoverTimer = window.setTimeout(place, 40);
}

function hideHoverTip() {
  window.clearTimeout(hoverTimer);
  hoverTip.value = null;
}

onMounted(() => {
  window.addEventListener("scroll", hideHoverTip, true);
  window.addEventListener("resize", hideHoverTip);
});

onBeforeUnmount(() => {
  hideHoverTip();
  window.removeEventListener("scroll", hideHoverTip, true);
  window.removeEventListener("resize", hideHoverTip);
});
</script>

<template>
  <div v-if="nodes.length" class="tool-schema-inspector" role="tree" :aria-label="rootLabel">
    <div class="tool-schema-inspector-row is-root">
      <span class="tool-schema-inspector-toggle"><i class="fa-solid fa-chevron-down" /></span>
      <b class="obj">obj</b>
      <strong>{{ rootLabel }}</strong>
      <small>object</small>
      <em>✓</em>
    </div>
    <button
      v-for="row in rows"
      :key="row.node.id"
      type="button"
      role="treeitem"
      class="tool-schema-inspector-row"
      :class="{ selected: selectedId === row.node.id }"
      :aria-selected="selectedId === row.node.id"
      :aria-expanded="rowHasChildren(row.node) ? expandedIds.has(row.node.id) : undefined"
      :aria-describedby="rowTitle(row.node) && selectedId === row.node.id ? hintId : undefined"
      :style="{ '--tree-depth': String(row.depth) }"
      @mouseenter="showHoverTip($event, row.node)"
      @mouseleave="hideHoverTip"
      @focus="showHoverTip($event, row.node)"
      @blur="hideHoverTip"
      @click="selectRow(row.node)"
    >
      <span class="tool-schema-inspector-toggle" @click.stop="toggleNode(row.node)">
        <i
          v-if="rowHasChildren(row.node)"
          :class="expandedIds.has(row.node.id) ? 'fa-solid fa-chevron-down' : 'fa-solid fa-chevron-right'"
        />
      </span>
      <b :class="typeShortLabel(row.node.type)">{{ typeShortLabel(row.node.type) }}</b>
      <strong>{{ nameOf(row.node) }}</strong>
      <small>{{ typeText(row.node) }}</small>
      <em v-if="row.node.required">✓</em>
    </button>
    <p v-if="selectedNode && rowTitle(selectedNode)" :id="hintId" class="tool-schema-inspector-hint">
      {{ rowTitle(selectedNode) }}
    </p>
    <Teleport to="body">
      <div
        v-if="hoverTip"
        class="tool-schema-inspector-tooltip"
        :class="{ above: hoverTip.placeAbove }"
        role="tooltip"
        :style="{ left: `${hoverTip.x}px`, top: `${hoverTip.y}px` }"
      >
        {{ hoverTip.text }}
      </div>
    </Teleport>
  </div>
  <div v-else class="tool-schema-inspector-empty">{{ emptyText }}</div>
</template>

<style scoped>
.tool-schema-inspector {
  min-width: 0;
  padding: 4px 0 2px;
}

.tool-schema-inspector-row {
  width: calc(100% - var(--tree-depth, 0) * 20px);
  min-height: 34px;
  display: grid;
  grid-template-columns: 14px 28px minmax(88px, auto) minmax(72px, 1fr) 16px;
  align-items: center;
  gap: 8px;
  margin-left: calc(var(--tree-depth, 0) * 20px);
  padding: 6px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #111827;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.tool-schema-inspector-row.is-root {
  width: 100%;
  margin-left: 0;
  cursor: default;
}

.tool-schema-inspector-row:hover {
  background: #f8fafc;
}

.tool-schema-inspector-row.is-root:hover {
  background: transparent;
}

.tool-schema-inspector-row.selected {
  background: var(--aw-cyan-soft, #ccfbf1);
  box-shadow: inset 3px 0 0 var(--aw-cyan, #0d9488);
}

.tool-schema-inspector-toggle {
  width: 14px;
  color: #94a3b8;
  font-size: 9px;
  text-align: center;
}

.tool-schema-inspector-row b {
  min-width: 28px;
  padding: 1px 5px;
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-weight: 700;
  line-height: 1.4;
  text-align: center;
}

.tool-schema-inspector-row b.str {
  background: #dbeafe;
  color: #1d4ed8;
}

.tool-schema-inspector-row b.obj {
  background: #fef3c7;
  color: #b45309;
}

.tool-schema-inspector-row b.arr {
  background: #ede9fe;
  color: #6d28d9;
}

.tool-schema-inspector-row b.num,
.tool-schema-inspector-row b.int,
.tool-schema-inspector-row b.bool {
  background: #dcfce7;
  color: #15803d;
}

.tool-schema-inspector-row strong {
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-schema-inspector-row small {
  color: #94a3b8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
}

.tool-schema-inspector-row em {
  color: var(--aw-cyan, #0d9488);
  font-size: 12px;
  font-style: normal;
}

.tool-schema-inspector-hint {
  margin: 8px 10px 2px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}

.tool-schema-inspector-empty {
  padding: 20px 0;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}

.tool-schema-inspector-tooltip {
  position: fixed;
  z-index: 40;
  max-width: min(320px, calc(100vw - 24px));
  padding: 8px 10px;
  border: 1px solid rgb(15 23 42 / 0.08);
  border-radius: 8px;
  background: #0f172a;
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.22);
  color: #f8fafc;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.5;
  pointer-events: none;
  transform: translateY(0);
}

.tool-schema-inspector-tooltip.above {
  transform: translateY(-100%);
}

@media (max-width: 720px) {
  .tool-schema-inspector-row {
    grid-template-columns: 14px 28px minmax(0, 1fr) 16px;
  }

  .tool-schema-inspector-row small {
    display: none;
  }
}
</style>
