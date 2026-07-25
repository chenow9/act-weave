<script setup lang="ts">
import { computed } from "vue";

import AppSelect from "./AppSelect.vue";
import ToolSchemaTreeNodeEditor from "./ToolSchemaTreeNodeEditor.vue";

import type { ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";

type SchemaRowKind = "node" | "array-item";

interface SchemaTableRow extends ToolSchemaNode {
  children?: SchemaTableRow[];
  __kind: SchemaRowKind;
  __ownerId?: string;
}

const props = withDefaults(
  defineProps<{
    modelValue: ToolSchemaNode[];
    title: string;
    description: string;
    rootLabel: string;
    locationEnabled?: boolean;
    mode?: "table" | "tree";
  }>(),
  {
    locationEnabled: false,
    mode: "table",
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: ToolSchemaNode[]];
}>();

const typeOptions: Array<{ label: string; value: ToolSchemaNodeType }> = [
  { label: "字符串", value: "string" },
  { label: "整数", value: "integer" },
  { label: "数字", value: "number" },
  { label: "布尔值", value: "boolean" },
  { label: "对象", value: "object" },
  { label: "数组", value: "array" },
];
const yesNoOptions = [
  { label: "必填", value: "true" },
  { label: "选填", value: "false" },
];
const locationOptions = [
  { label: "路径参数", value: "Path" },
  { label: "查询参数", value: "Query" },
  { label: "请求体", value: "Body" },
  { label: "请求头", value: "Header" },
];

const summaryText = computed(() => {
  const total = countNodes(props.modelValue);
  const required = countRequiredNodes(props.modelValue);
  return `${total} 个节点 · ${required} 个必填`;
});

const tableRows = computed(() => props.modelValue.map((node) => toTableRow(node)));
const isTreeMode = computed(() => props.mode === "tree");

function createNode(name = "", location = props.locationEnabled ? "Body" : ""): ToolSchemaNode {
  return {
    id: `schema-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name,
    type: "string",
    description: "",
    required: true,
    location: props.locationEnabled ? location || "Body" : undefined,
    children: [],
    item: null,
    additionalProperties: null,
  };
}

function createArrayItemNode(location = ""): ToolSchemaNode {
  return {
    ...createNode("", location),
    name: "",
  };
}

function toTableRow(node: ToolSchemaNode, kind: SchemaRowKind = "node", ownerId?: string): SchemaTableRow {
  const row: SchemaTableRow = {
    ...node,
    children: [],
    __kind: kind,
    __ownerId: ownerId,
  };

  if (node.type === "object") {
    row.children = (node.children || []).map((child) => toTableRow(child));
  } else if (node.type === "array") {
    row.children = [toTableRow(node.item || createArrayItemNode(node.location || ""), "array-item", node.id)];
  }

  return row;
}

function countNodes(nodes: ToolSchemaNode[]): number {
  return nodes.reduce((total, node) => total + 1 + countNodes(node.children || []) + (node.item ? countNodes([node.item]) : 0), 0);
}

function countRequiredNodes(nodes: ToolSchemaNode[]): number {
  return nodes.reduce(
    (total, node) =>
      total +
      (node.required ? 1 : 0) +
      countRequiredNodes(node.children || []) +
      (node.item ? countRequiredNodes([node.item]) : 0),
    0,
  );
}

function isArrayItemRow(row: SchemaTableRow) {
  return row.__kind === "array-item";
}

function showChildAction(row: SchemaTableRow) {
  return row.type === "object";
}

function showDeleteAction(row: SchemaTableRow) {
  return row.__kind !== "array-item";
}

function arrayItemLabel(row: SchemaTableRow) {
  return row.type === "object" ? "数组元素对象" : row.type === "array" ? "数组元素数组" : "数组元素";
}

function emitModelValue(nextValue: ToolSchemaNode[]) {
  emit("update:modelValue", nextValue);
}

function addRootNode() {
  emitModelValue([...props.modelValue, createNode()]);
}

function updateRowField<K extends keyof ToolSchemaNode>(row: SchemaTableRow, key: K, value: ToolSchemaNode[K]) {
  const updater = (current: ToolSchemaNode): ToolSchemaNode => {
    const nextNode: ToolSchemaNode = { ...current, [key]: value };
    if (key === "type") {
      if (value === "object") {
        nextNode.children = nextNode.children || [];
        nextNode.item = null;
      } else if (value === "array") {
        nextNode.item = nextNode.item || createArrayItemNode(current.location || "");
        nextNode.children = [];
      } else {
        nextNode.children = [];
        nextNode.item = null;
      }
    }
    if (key === "location" && props.locationEnabled && typeof value === "string" && nextNode.type === "array" && nextNode.item) {
      nextNode.item = { ...nextNode.item, location: value };
    }
    return nextNode;
  };

  if (isArrayItemRow(row)) {
    emitModelValue(updateArrayItemNode(props.modelValue, row.__ownerId || "", updater));
    return;
  }

  emitModelValue(updateSchemaNode(props.modelValue, row.id, updater));
}

function addChildRow(row: SchemaTableRow) {
  const updater = (current: ToolSchemaNode): ToolSchemaNode => ({
    ...current,
    children: [...(current.children || []), createNode("", current.location || "")],
  });

  if (isArrayItemRow(row)) {
    emitModelValue(updateArrayItemNode(props.modelValue, row.__ownerId || "", updater));
    return;
  }

  emitModelValue(updateSchemaNode(props.modelValue, row.id, updater));
}

function deleteRow(row: SchemaTableRow) {
  if (!showDeleteAction(row)) return;
  emitModelValue(removeSchemaNode(props.modelValue, row.id));
}

function updateRootNode(targetId: string, nextNode: ToolSchemaNode) {
  emitModelValue(updateSchemaNode(props.modelValue, targetId, () => nextNode));
}

function removeRootNode(targetId: string) {
  emitModelValue(removeSchemaNode(props.modelValue, targetId));
}

function updateSchemaNode(nodes: ToolSchemaNode[], targetId: string, updater: (node: ToolSchemaNode) => ToolSchemaNode): ToolSchemaNode[] {
  return nodes.map((node) => updateSchemaNodeEntry(node, targetId, updater));
}

function updateSchemaNodeEntry(node: ToolSchemaNode, targetId: string, updater: (node: ToolSchemaNode) => ToolSchemaNode): ToolSchemaNode {
  if (node.id === targetId) {
    return updater(node);
  }

  return {
    ...node,
    children: (node.children || []).map((child) => updateSchemaNodeEntry(child, targetId, updater)),
    item: node.item ? updateSchemaNodeEntry(node.item, targetId, updater) : node.item,
  };
}

function updateArrayItemNode(nodes: ToolSchemaNode[], ownerId: string, updater: (node: ToolSchemaNode) => ToolSchemaNode): ToolSchemaNode[] {
  return nodes.map((node) => updateArrayItemNodeEntry(node, ownerId, updater));
}

function updateArrayItemNodeEntry(node: ToolSchemaNode, ownerId: string, updater: (node: ToolSchemaNode) => ToolSchemaNode): ToolSchemaNode {
  if (node.id === ownerId) {
    return {
      ...node,
      item: updater(node.item || createArrayItemNode(node.location || "")),
    };
  }

  return {
    ...node,
    children: (node.children || []).map((child) => updateArrayItemNodeEntry(child, ownerId, updater)),
    item: node.item ? updateArrayItemNodeEntry(node.item, ownerId, updater) : node.item,
  };
}

function removeSchemaNode(nodes: ToolSchemaNode[], targetId: string): ToolSchemaNode[] {
  return nodes
    .filter((node) => node.id !== targetId)
    .map((node) => ({
      ...node,
      children: removeSchemaNode(node.children || [], targetId),
      item: node.item ? removeSchemaNode([node.item], targetId)[0] || null : node.item,
    }));
}
</script>

<template>
  <div class="editable-schema-card tool-schema-editor-shell">
    <div class="editable-schema-head">
      <div>
        <strong>{{ title }}</strong>
        <span>{{ description }}</span>
      </div>
      <button class="ghost-button" type="button" @click="addRootNode">添加字段</button>
    </div>
    <div class="tool-schema-summary-strip">
      <span>{{ rootLabel }}</span>
      <strong>{{ summaryText }}</strong>
    </div>
    <div v-if="isTreeMode && modelValue.length" class="tool-contract-tree-shell">
      <div class="tool-contract-tree-body">
        <ToolSchemaTreeNodeEditor
          v-for="node in modelValue"
          :key="node.id"
          :node="node"
          :depth="0"
          :location-enabled="locationEnabled"
          @update:node="updateRootNode(node.id, $event)"
          @remove="removeRootNode(node.id)"
        />
      </div>
    </div>
    <div v-else-if="tableRows.length" class="tool-schema-tree editable-schema-table">
      <vxe-table
        class="tool-schema-vxe-table tool-schema-vxe-table-editable"
        border="none"
        round
        size="medium"
        :data="tableRows"
        :row-config="{ keyField: 'id', useKey: true, isHover: true }"
        :tree-config="{ childrenField: 'children', expandAll: true, showLine: true }"
        show-overflow="title"
      >
        <vxe-column v-if="locationEnabled" field="location" title="参数位置" width="160">
          <template #default="{ row }">
            <span v-if="isArrayItemRow(row)" class="tool-schema-row-meta">继承上级</span>
            <AppSelect
              v-else
              class="tool-schema-cell-select"
              :model-value="row.location || 'Body'"
              :options="locationOptions"
              compact
              @update:model-value="updateRowField(row, 'location', String($event))"
            />
          </template>
        </vxe-column>
        <vxe-column field="name" title="字段名称" tree-node min-width="220">
          <template #default="{ row }">
            <span v-if="isArrayItemRow(row)" class="tool-schema-row-meta">{{ arrayItemLabel(row) }}</span>
            <input
              v-else
              class="tool-schema-cell-input"
              :value="row.name"
              placeholder="输入字段名称"
              @input="updateRowField(row, 'name', ($event.target as HTMLInputElement).value)"
            />
          </template>
        </vxe-column>
        <vxe-column field="type" title="字段类型" width="160">
          <template #default="{ row }">
            <AppSelect
              class="tool-schema-cell-select"
              :model-value="row.type"
              :options="typeOptions"
              compact
              @update:model-value="updateRowField(row, 'type', $event as ToolSchemaNodeType)"
            />
          </template>
        </vxe-column>
        <vxe-column field="required" title="是否必填" width="140">
          <template #default="{ row }">
            <AppSelect
              class="tool-schema-cell-select"
              :model-value="row.required ? 'true' : 'false'"
              :options="yesNoOptions"
              compact
              @update:model-value="updateRowField(row, 'required', $event === 'true')"
            />
          </template>
        </vxe-column>
        <vxe-column field="description" title="字段说明" min-width="260">
          <template #default="{ row }">
            <input
              class="tool-schema-cell-input"
              :value="row.description"
              placeholder="补充字段说明"
              @input="updateRowField(row, 'description', ($event.target as HTMLInputElement).value)"
            />
          </template>
        </vxe-column>
        <vxe-column title="操作" width="160" align="center">
          <template #default="{ row }">
            <div class="tool-schema-row-actions">
              <button v-if="showChildAction(row)" class="icon-action-button" type="button" title="添加子字段" @click="addChildRow(row)">
                <i class="fa-solid fa-plus" />
              </button>
              <button v-if="showDeleteAction(row)" class="icon-action-button danger" type="button" title="删除字段" @click="deleteRow(row)">
                <i class="fa-solid fa-trash" />
              </button>
            </div>
          </template>
        </vxe-column>
      </vxe-table>
    </div>
    <div v-else class="tool-schema-empty">
      <i class="fa-solid fa-sitemap" />
      <span>还没有字段，添加后可继续配置对象、数组和嵌套节点。</span>
    </div>
  </div>
</template>
