<script setup lang="ts">
import { computed, ref, watch } from "vue";

import AppSelect from "./AppSelect.vue";
import { serializeContractNodesToJson } from "../utils/tool-schema-json";
import type { ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";

interface TreeRow {
  node: ToolSchemaNode;
  depth: number;
}

const props = withDefaults(
  defineProps<{
    modelValue: ToolSchemaNode[];
    title: string;
    description: string;
    rootLabel: string;
    compact?: boolean;
  }>(),
  { compact: false },
);

const emit = defineEmits<{
  "update:modelValue": [value: ToolSchemaNode[]];
}>();

const editorMode = ref<"structured" | "json">("structured");
const selectedNodeId = ref("");
const expandedNodeIds = ref<Set<string>>(new Set());
const fieldTypeOptions = ["string", "integer", "number", "boolean", "object", "array"].map((type) => ({
  label: type,
  value: type,
}));

const jsonPreview = computed(() => serializeContractNodesToJson(props.modelValue));
const treeRows = computed(() => flattenVisibleNodes(props.modelValue));
const selectedNode = computed(() => findNode(props.modelValue, selectedNodeId.value));

watch(
  () => props.modelValue,
  (nodes) => {
    const allNodes = flattenAllNodes(nodes);
    if (!selectedNodeId.value || !allNodes.some((node) => node.id === selectedNodeId.value)) {
      selectedNodeId.value = allNodes.find((node) => node.type === "object")?.id || allNodes[0]?.id || "";
    }
    const nextExpanded = new Set(expandedNodeIds.value);
    allNodes.filter((node) => node.type === "object").forEach((node) => nextExpanded.add(node.id));
    expandedNodeIds.value = nextExpanded;
  },
  { immediate: true, deep: true },
);

function flattenAllNodes(nodes: ToolSchemaNode[]): ToolSchemaNode[] {
  return nodes.flatMap((node) => [
    node,
    ...flattenAllNodes(node.children || []),
    ...(node.item ? flattenAllNodes([node.item]) : []),
  ]);
}

function flattenVisibleNodes(nodes: ToolSchemaNode[], depth = 0): TreeRow[] {
  return nodes.flatMap((node) => {
    const children =
      node.type === "object" ? node.children || [] : node.type === "array" && node.item ? [node.item] : [];
    const childRows = expandedNodeIds.value.has(node.id) ? flattenVisibleNodes(children, depth + 1) : [];
    return [{ node, depth }, ...childRows];
  });
}

function findNode(nodes: ToolSchemaNode[], id: string): ToolSchemaNode | null {
  for (const node of nodes) {
    if (node.id === id) return node;
    const child = findNode(node.children || [], id);
    if (child) return child;
    if (node.item) {
      const item = findNode([node.item], id);
      if (item) return item;
    }
  }
  return null;
}

function createNode(name = ""): ToolSchemaNode {
  return {
    id: `schema-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name,
    type: "string",
    description: "",
    required: true,
    children: [],
    item: null,
    additionalProperties: null,
  };
}

function emitNodes(nodes: ToolSchemaNode[]) {
  emit("update:modelValue", nodes);
}

function addRootNode() {
  const nextNode = createNode("");
  selectedNodeId.value = nextNode.id;
  emitNodes([...props.modelValue, nextNode]);
}

function addField() {
  if (selectedNode.value?.type === "object") {
    addChildNode();
    return;
  }
  addRootNode();
}

function updateSelectedField<K extends keyof ToolSchemaNode>(key: K, value: ToolSchemaNode[K]) {
  if (!selectedNode.value) return;
  emitNodes(
    updateNode(props.modelValue, selectedNode.value.id, (node) => {
      const nextNode: ToolSchemaNode = { ...node, [key]: value };
      if (key === "type") {
        if (value === "object") {
          nextNode.children = nextNode.children || [];
          nextNode.item = null;
          expandedNodeIds.value = new Set(expandedNodeIds.value).add(node.id);
        } else if (value === "array") {
          nextNode.children = [];
          nextNode.item = nextNode.item || createNode("item");
          expandedNodeIds.value = new Set(expandedNodeIds.value).add(node.id);
        } else {
          nextNode.children = [];
          nextNode.item = null;
        }
      }
      return nextNode;
    }),
  );
}

function addChildNode() {
  if (!selectedNode.value || selectedNode.value.type !== "object") return;
  const child = createNode("");
  expandedNodeIds.value = new Set(expandedNodeIds.value).add(selectedNode.value.id);
  selectedNodeId.value = child.id;
  emitNodes(
    updateNode(props.modelValue, selectedNode.value.id, (node) => ({
      ...node,
      children: [...(node.children || []), child],
    })),
  );
}

function duplicateSelectedNode() {
  if (!selectedNode.value) return;
  const duplicate = cloneWithNewIds(selectedNode.value);
  duplicate.name = selectedNode.value.name ? `${selectedNode.value.name}_copy` : "";
  selectedNodeId.value = duplicate.id;
  emitNodes(insertAfterNode(props.modelValue, selectedNode.value.id, duplicate));
}

function deleteSelectedNode() {
  if (!selectedNode.value) return;
  const nextNodes = removeNode(props.modelValue, selectedNode.value.id);
  selectedNodeId.value = flattenAllNodes(nextNodes)[0]?.id || "";
  emitNodes(nextNodes);
}

function toggleNode(node: ToolSchemaNode) {
  if (node.type !== "object" && node.type !== "array") return;
  const next = new Set(expandedNodeIds.value);
  if (next.has(node.id)) next.delete(node.id);
  else next.add(node.id);
  expandedNodeIds.value = next;
}

function updateNode(
  nodes: ToolSchemaNode[],
  targetId: string,
  updater: (node: ToolSchemaNode) => ToolSchemaNode,
): ToolSchemaNode[] {
  return nodes.map((node) => {
    if (node.id === targetId) return updater(node);
    return {
      ...node,
      children: updateNode(node.children || [], targetId, updater),
      item: node.item ? updateNode([node.item], targetId, updater)[0] || null : node.item,
    };
  });
}

function insertAfterNode(nodes: ToolSchemaNode[], targetId: string, duplicate: ToolSchemaNode): ToolSchemaNode[] {
  return nodes.flatMap((node) => {
    if (node.id === targetId) return [node, duplicate];
    return [
      {
        ...node,
        children: insertAfterNode(node.children || [], targetId, duplicate),
        item: node.item ? insertAfterNode([node.item], targetId, duplicate)[0] || null : node.item,
      },
    ];
  });
}

function removeNode(nodes: ToolSchemaNode[], targetId: string): ToolSchemaNode[] {
  return nodes
    .filter((node) => node.id !== targetId)
    .map((node) => ({
      ...node,
      children: removeNode(node.children || [], targetId),
      item: node.item ? removeNode([node.item], targetId)[0] || null : node.item,
    }));
}

function cloneWithNewIds(node: ToolSchemaNode): ToolSchemaNode {
  return {
    ...node,
    id: createNode().id,
    children: (node.children || []).map(cloneWithNewIds),
    item: node.item ? cloneWithNewIds(node.item) : node.item,
    additionalProperties: node.additionalProperties
      ? cloneWithNewIds(node.additionalProperties)
      : node.additionalProperties,
  };
}

function typeShortLabel(type: ToolSchemaNodeType) {
  return (
    { string: "str", integer: "int", number: "num", boolean: "bool", object: "obj", array: "arr" } as Record<
      ToolSchemaNodeType,
      string
    >
  )[type];
}

function rowHasChildren(node: ToolSchemaNode) {
  return node.type === "object" || node.type === "array";
}
</script>

<template>
  <section class="tool-hybrid-contract-editor" :class="{ compact }" :aria-label="title">
    <header v-if="!compact" class="tool-hybrid-contract-head">
      <div>
        <strong>{{ title }}</strong
        ><span>{{ description }}</span>
      </div>
    </header>

    <div class="tool-hybrid-tree-toolbar">
      <div class="tool-hybrid-tree-actions">
        <button type="button" @click="addField"><i class="fa-solid fa-plus" /> 添加字段</button>
        <button type="button" :disabled="!selectedNode" @click="duplicateSelectedNode">
          <i class="fa-regular fa-copy" /> 复制
        </button>
        <button type="button" :disabled="!selectedNode" @click="deleteSelectedNode">
          <i class="fa-solid fa-trash" /> 删除
        </button>
      </div>
      <div class="tool-contract-mode-tabs" role="tablist" aria-label="契约编辑模式">
        <button
          type="button"
          role="tab"
          :aria-selected="editorMode === 'structured'"
          :class="{ active: editorMode === 'structured' }"
          @click="editorMode = 'structured'"
        >
          结构化
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="editorMode === 'json'"
          :class="{ active: editorMode === 'json' }"
          @click="editorMode = 'json'"
        >
          JSON
        </button>
      </div>
    </div>

    <div
      v-if="editorMode === 'structured'"
      class="tool-hybrid-structured-workspace"
      role="tabpanel"
      aria-label="结构化契约编辑"
    >
      <div class="tool-hybrid-contract-tree" role="tree" aria-label="字段树">
        <div class="tool-hybrid-root-row">
          <span class="tool-hybrid-tree-toggle"><i class="fa-solid fa-chevron-down" /></span><b class="obj">obj</b
          ><strong>{{ rootLabel.includes("Response") ? "Response" : "Body" }}</strong
          ><small>object</small><em>✓</em>
        </div>
        <button
          v-for="row in treeRows"
          :key="row.node.id"
          type="button"
          role="treeitem"
          :aria-selected="selectedNodeId === row.node.id"
          :aria-expanded="rowHasChildren(row.node) ? expandedNodeIds.has(row.node.id) : undefined"
          :class="{ selected: selectedNodeId === row.node.id }"
          :style="{ '--tree-depth': String(row.depth + 1) }"
          @click="selectedNodeId = row.node.id"
        >
          <span class="tool-hybrid-tree-toggle" @click.stop="toggleNode(row.node)"
            ><i
              v-if="rowHasChildren(row.node)"
              :class="expandedNodeIds.has(row.node.id) ? 'fa-solid fa-chevron-down' : 'fa-solid fa-chevron-right'"
          /></span>
          <b :class="typeShortLabel(row.node.type)">{{ typeShortLabel(row.node.type) }}</b>
          <strong>{{ row.node.name || "未命名字段" }}</strong>
          <small>{{ row.node.type }}{{ row.node.format ? `(${row.node.format})` : "" }}</small>
          <em v-if="row.node.required">✓</em>
        </button>
        <div v-if="!treeRows.length" class="tool-hybrid-tree-empty">还没有字段，点击“添加字段”开始配置。</div>
      </div>

      <aside class="tool-hybrid-contract-inspector" aria-label="字段属性">
        <template v-if="selectedNode">
          <h4>字段属性</h4>
          <label
            ><span>字段名称</span
            ><input
              :value="selectedNode.name"
              @input="updateSelectedField('name', ($event.target as HTMLInputElement).value)"
          /></label>
          <label>
            <span>数据类型</span>
            <AppSelect
              :model-value="selectedNode.type"
              :options="fieldTypeOptions"
              aria-label="字段数据类型"
              @update:model-value="updateSelectedField('type', String($event) as ToolSchemaNodeType)"
            />
          </label>
          <div class="tool-hybrid-required-row">
            <span>必填</span
            ><button
              type="button"
              role="switch"
              :aria-checked="selectedNode.required"
              :class="{ on: selectedNode.required }"
              @click="updateSelectedField('required', !selectedNode.required)"
            >
              <i />
            </button>
          </div>
          <label
            ><span>字段说明</span
            ><textarea
              :value="selectedNode.description"
              rows="3"
              @input="updateSelectedField('description', ($event.target as HTMLTextAreaElement).value)"
            />
          </label>
          <label v-if="selectedNode.example"
            ><span>示例值</span><code class="tool-hybrid-example-box">{{ selectedNode.example }}</code></label
          >
        </template>
        <div v-else class="tool-hybrid-inspector-empty">从左侧选择一个字段查看属性。</div>
      </aside>
    </div>

    <div v-else class="tool-contract-json-preview" role="tabpanel" aria-label="JSON 契约预览">
      <div class="tool-contract-json-caption">只读预览 · 由字段树自动生成，编辑请切回“结构化”</div>
      <pre><code>{{ jsonPreview }}</code></pre>
    </div>
  </section>
</template>

<style scoped>
.tool-hybrid-contract-editor {
  color: var(--aw-text);
}
.tool-hybrid-contract-head {
  margin-bottom: 10px;
}
.tool-hybrid-contract-head strong,
.tool-hybrid-contract-head span {
  display: block;
}
.tool-hybrid-contract-head strong {
  font-size: 14px;
}
.tool-hybrid-contract-head span {
  margin-top: 3px;
  color: var(--aw-muted);
  font-size: 12px;
}
.tool-hybrid-tree-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.tool-hybrid-tree-actions {
  display: flex;
  gap: 8px;
}
.tool-hybrid-tree-actions button,
.tool-contract-mode-tabs button {
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 1px solid var(--aw-border);
  border-radius: 6px;
  background: #f8fafc;
  padding: 5px 10px;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}
.tool-hybrid-tree-actions button:hover {
  border-color: rgb(13 148 136 / 0.35);
  background: #fff;
  color: var(--aw-cyan);
}
.tool-hybrid-tree-actions button:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.tool-contract-mode-tabs {
  display: flex;
  gap: 2px;
  overflow: hidden;
  padding: 2px;
  border: 1px solid var(--aw-border);
  border-radius: 7px;
  background: #f1f5f9;
}
.tool-contract-mode-tabs button {
  min-height: 30px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: #94a3b8;
}
.tool-contract-mode-tabs button.active {
  background: #fff;
  color: #0f766e;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.08);
  font-weight: 700;
}
.tool-hybrid-structured-workspace {
  display: flex;
  gap: 20px;
  min-height: 380px;
}
.tool-hybrid-contract-tree {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: 0;
}
.tool-hybrid-root-row,
.tool-hybrid-contract-tree > button {
  width: calc(100% - var(--tree-depth, 0) * 20px);
  min-height: 34px;
  display: grid;
  grid-template-columns: 14px 22px minmax(100px, auto) minmax(120px, 1fr) 16px;
  align-items: center;
  gap: 8px;
  margin-left: calc(var(--tree-depth, 0) * 20px);
  padding: 6px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #111827;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.tool-hybrid-root-row {
  width: 100%;
  cursor: default;
}
.tool-hybrid-contract-tree > button:hover {
  background: #f8fafc;
}
.tool-hybrid-contract-tree > button.selected {
  background: var(--aw-cyan-soft);
  box-shadow: inset 3px 0 0 var(--aw-cyan);
}
.tool-hybrid-tree-toggle {
  width: 14px;
  color: #94a3b8;
  font-size: 9px;
  text-align: center;
}
.tool-hybrid-contract-tree b {
  width: 20px;
  padding: 1px 4px;
  border-radius: 4px;
  font-family: "SF Mono", Consolas, Menlo, monospace;
  font-size: 10px;
  text-align: center;
}
.tool-hybrid-contract-tree b.str {
  background: #dbeafe;
  color: #1d4ed8;
}
.tool-hybrid-contract-tree b.obj {
  background: #fef3c7;
  color: #b45309;
}
.tool-hybrid-contract-tree b.arr {
  background: #ede9fe;
  color: #6d28d9;
}
.tool-hybrid-contract-tree b.num,
.tool-hybrid-contract-tree b.int,
.tool-hybrid-contract-tree b.bool {
  background: #dcfce7;
  color: #15803d;
}
.tool-hybrid-contract-tree strong {
  font-size: 13px;
  font-weight: 600;
}
.tool-hybrid-contract-tree small {
  color: #94a3b8;
  font-family: "SF Mono", Consolas, Menlo, monospace;
  font-size: 11px;
}
.tool-hybrid-contract-tree em {
  color: var(--aw-cyan);
  font-size: 12px;
  font-style: normal;
}
.tool-hybrid-tree-empty {
  padding: 50px 0;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}
.tool-hybrid-contract-inspector {
  width: 290px;
  flex: 0 0 290px;
  align-self: flex-start;
  padding: 16px;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
  background: #f8fafc;
}
.tool-hybrid-contract-inspector h4 {
  margin: 0 0 14px;
  color: #64748b;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.tool-hybrid-contract-inspector label {
  display: block;
  margin-bottom: 13px;
}
.tool-hybrid-contract-inspector label > span,
.tool-hybrid-required-row > span {
  display: block;
  margin-bottom: 6px;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
}
.tool-hybrid-contract-inspector input,
.tool-hybrid-contract-inspector textarea {
  width: 100%;
  min-height: 40px;
  border: 1px solid var(--aw-border);
  border-radius: 6px;
  background: #fff;
  padding: 8px 10px;
  color: var(--aw-text);
  font: inherit;
  font-size: 12px;
}
.tool-hybrid-contract-inspector input:focus,
.tool-hybrid-contract-inspector textarea:focus {
  outline: 0;
  border-color: rgb(13 148 136 / 0.55);
  box-shadow: 0 0 0 3px var(--aw-cyan-soft);
}
.tool-hybrid-contract-inspector :deep(.app-select .el-select__wrapper) {
  min-height: 40px;
  background: #fff;
}
.tool-hybrid-contract-inspector textarea {
  min-height: 54px;
  resize: vertical;
}
.tool-hybrid-example-box {
  display: block;
  overflow-x: auto;
  padding: 10px 12px;
  border-radius: 7px;
  background: #1e293b;
  color: #86efac;
  font:
    11.5px/1.7 ui-monospace,
    SFMono-Regular,
    Menlo,
    Monaco,
    Consolas,
    monospace;
  white-space: pre;
}
.tool-hybrid-required-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 13px;
}
.tool-hybrid-required-row > span {
  margin: 0;
}
.tool-hybrid-required-row button {
  width: 34px;
  height: 19px;
  position: relative;
  border: 0;
  border-radius: 10px;
  background: #cbd5e1;
  cursor: pointer;
}
.tool-hybrid-required-row button i {
  width: 15px;
  height: 15px;
  position: absolute;
  top: 2px;
  left: 2px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.2);
  transition: left 0.12s;
}
.tool-hybrid-required-row button.on {
  background: var(--aw-cyan);
}
.tool-hybrid-required-row button.on i {
  left: 17px;
}
.tool-hybrid-inspector-empty {
  padding: 50px 0;
  color: #9ca3af;
  font-size: 13px;
  text-align: center;
}
.tool-contract-json-preview {
  overflow: hidden;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
}
.tool-contract-json-caption {
  padding: 8px 14px;
  border-bottom: 1px solid var(--aw-border);
  background: #f8fafc;
  color: #94a3b8;
  font-size: 11px;
}
.tool-contract-json-preview pre {
  max-height: 420px;
  margin: 0;
  overflow: auto;
  padding: 16px 18px;
  background: #1e293b;
  color: #e2e8f0;
  font-family: "SF Mono", Consolas, Menlo, monospace;
  font-size: 12.5px;
  line-height: 1.7;
}
@media (max-width: 820px) {
  .tool-hybrid-structured-workspace {
    flex-direction: column;
  }
  .tool-hybrid-contract-inspector {
    width: 100%;
    flex-basis: auto;
  }
}
</style>
