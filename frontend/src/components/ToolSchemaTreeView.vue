<script setup lang="ts">
import { computed, getCurrentInstance } from "vue";

import { ensureVxe } from "../plugins/register-vxe";
import type { ToolSchemaNode } from "../types/domain";

const app = getCurrentInstance()?.appContext.app;
if (app) ensureVxe(app);

type SchemaRowKind = "node" | "array-item";

interface SchemaTableRow extends ToolSchemaNode {
  children?: SchemaTableRow[];
  __kind: SchemaRowKind;
  __depth: number;
  __path: string;
  __locationLabel: string;
}

const props = defineProps<{
  nodes: ToolSchemaNode[];
  title?: string;
  summaryTitle?: string;
  summaryDescription?: string;
  emptyText?: string;
  variant?: "default" | "spec";
  defaultLocation?: string;
}>();

const tableRows = computed(() => props.nodes.map((node) => toTableRow(node)));
const showLocationColumn = computed(
  () => variantMode.value === "spec" || containsLocation(props.nodes) || Boolean(props.defaultLocation),
);
const variantMode = computed(() => props.variant || "default");
const summaryText = computed(() => {
  const total = countNodes(props.nodes);
  const required = countRequiredNodes(props.nodes);
  const depth = maxDepth(props.nodes);
  return `${total} 个节点 · ${required} 个必填 · ${depth} 层结构`;
});

function toTableRow(node: ToolSchemaNode, kind: SchemaRowKind = "node"): SchemaTableRow {
  const row: SchemaTableRow = {
    ...node,
    children: [],
    __kind: kind,
    __depth: 0,
    __path: "",
    __locationLabel: "",
  };

  if (node.type === "object") {
    row.children = (node.children || []).map((child) => toTableRow(child));
  } else if (node.type === "array" && node.item) {
    row.children = [toTableRow(node.item, "array-item")];
  }

  return row;
}

const tableRowsWithDepth = computed(() => annotateDepth(tableRows.value, 0, "", props.defaultLocation || ""));

function containsLocation(nodes: ToolSchemaNode[]): boolean {
  return nodes.some(
    (node) =>
      Boolean(node.location) ||
      containsLocation(node.children || []) ||
      (node.item ? containsLocation([node.item]) : false),
  );
}

function typeLabel(type: string, variant: "default" | "spec") {
  const chineseLabels: Record<string, string> = {
    string: "字符串",
    integer: "整数",
    number: "数字",
    boolean: "布尔值",
    object: "对象",
    array: "数组",
  };
  const specLabels: Record<string, string> = {
    string: "STRING",
    integer: "INTEGER",
    number: "NUMBER",
    boolean: "BOOLEAN",
    object: "OBJECT",
    array: "ARRAY",
  };
  return (variant === "spec" ? specLabels : chineseLabels)[type] || type;
}

function locationLabel(location: string | undefined, variant: "default" | "spec") {
  const defaultLabels: Record<string, string> = {
    Path: "路径参数",
    Query: "查询参数",
    Body: "请求体",
    Header: "请求头",
    Response: "响应体",
  };
  const specLabels: Record<string, string> = {
    Path: "Path",
    Query: "Query",
    Body: "Body",
    Header: "Header",
    Response: "Response",
  };
  return location ? (variant === "spec" ? specLabels[location] : defaultLabels[location]) || location : "-";
}

function nameLabel(row: SchemaTableRow) {
  if (row.__kind === "array-item") {
    return "数组元素";
  }
  return row.name || "(未命名字段)";
}

function buildPath(row: SchemaTableRow, parentPath = ""): string {
  const segment = nameLabel(row);
  if (row.__kind === "array-item") {
    return parentPath ? `${parentPath}[]` : "[]";
  }
  return parentPath ? `${parentPath}.${segment}` : segment;
}

function depthOf(row: SchemaTableRow) {
  return row.__depth;
}

function nodeTag(row: SchemaTableRow) {
  if (row.__kind === "array-item") return "数组元素";
  if (row.type === "object") return "对象";
  if (row.type === "array") return "数组";
  return "字段";
}

function annotateDepth(rows: SchemaTableRow[], depth = 0, parentPath = "", inheritedLocation = ""): SchemaTableRow[] {
  return rows.map((row) => ({
    ...row,
    __depth: depth,
    __path: buildPath(row, parentPath),
    __locationLabel: row.location || inheritedLocation || "",
    children: row.children
      ? annotateDepth(row.children, depth + 1, buildPath(row, parentPath), row.location || inheritedLocation || "")
      : [],
  }));
}

function countActiveFields(nodes: ToolSchemaNode[]): number {
  return countNodes(nodes);
}

function countNodes(nodes: ToolSchemaNode[]): number {
  return nodes.reduce(
    (total, node) => total + 1 + countNodes(node.children || []) + (node.item ? countNodes([node.item]) : 0),
    0,
  );
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

function maxDepth(nodes: ToolSchemaNode[], depth = 1): number {
  if (!nodes.length) return 0;
  return Math.max(
    ...nodes.map((node) => {
      const childDepth = maxDepth(node.children || [], depth + 1);
      const itemDepth = node.item ? maxDepth([node.item], depth + 1) : 0;
      return Math.max(depth, childDepth, itemDepth);
    }),
  );
}
</script>

<template>
  <div class="tool-schema-view" :class="{ 'tool-schema-view-spec': variantMode === 'spec' }">
    <div v-if="title" class="tool-schema-view-head" :class="{ 'tool-schema-view-head-spec': variantMode === 'spec' }">
      <div class="tool-schema-view-head-main">
        <strong>{{ summaryTitle || title }}</strong>
        <span v-if="summaryDescription">{{ summaryDescription }}</span>
      </div>
      <span class="tool-schema-view-summary">{{
        variantMode === "spec" ? `${countActiveFields(nodes)} 个活跃字段` : summaryText
      }}</span>
    </div>
    <div v-if="tableRows.length" class="tool-schema-tree" :class="{ 'tool-schema-tree-spec': variantMode === 'spec' }">
      <vxe-table
        class="tool-schema-vxe-table tool-schema-vxe-table-readonly"
        data-vxe-ui-theme="light"
        border="none"
        round
        :size="variantMode === 'spec' ? 'small' : 'medium'"
        :height="variantMode === 'spec' ? '100%' : undefined"
        :data="tableRowsWithDepth"
        :row-config="{ keyField: 'id', useKey: true, isHover: true }"
        :tree-config="{ childrenField: 'children', expandAll: true, showLine: true }"
        show-overflow="title"
      >
        <vxe-column v-if="showLocationColumn && variantMode !== 'spec'" field="location" title="参数位置" width="140">
          <template #default="{ row }">
            <span class="tool-schema-cell-text">
              {{ locationLabel(row.__locationLabel, variantMode) }}
            </span>
          </template>
        </vxe-column>
        <vxe-column
          field="name"
          :title="variantMode === 'spec' ? '字段名' : '字段名 (Key Path)'"
          tree-node
          :min-width="variantMode === 'spec' ? 220 : 280"
        >
          <template #default="{ row }">
            <div
              class="tool-schema-view-name-block"
              :class="{ 'tool-schema-view-name-block-spec': variantMode === 'spec' }"
            >
              <template v-if="variantMode === 'spec'">
                <div class="tool-schema-view-keyline tool-schema-view-keyline-spec" :title="row.__path">
                  <span class="tool-schema-view-spec-indent" :style="{ width: `${row.__depth * 22}px` }" />
                  <span v-if="row.__depth > 0" class="tool-schema-view-branch-spec">└</span>
                  <span class="tool-schema-view-name" :class="{ 'is-root': row.__depth === 0 }">{{
                    nameLabel(row)
                  }}</span>
                </div>
              </template>
              <template v-else>
                <div class="tool-schema-view-keyline">
                  <span v-if="depthOf(row) > 0" class="tool-schema-view-branch">└─</span>
                  <span class="tool-schema-view-node-tag">{{ nodeTag(row) }}</span>
                  <span class="tool-schema-view-name" :class="{ 'is-root': depthOf(row) === 0 }">{{
                    nameLabel(row)
                  }}</span>
                </div>
                <span class="tool-schema-view-path">{{ row.__path }}</span>
              </template>
            </div>
          </template>
        </vxe-column>
        <vxe-column v-if="showLocationColumn && variantMode === 'spec'" field="location" title="位置" width="92">
          <template #default="{ row }">
            <span class="tool-schema-cell-text tool-schema-cell-text-spec">
              {{ locationLabel(row.__locationLabel, variantMode) }}
            </span>
          </template>
        </vxe-column>
        <vxe-column
          field="type"
          :title="variantMode === 'spec' ? '数据类型' : '字段类型'"
          :width="variantMode === 'spec' ? 108 : 140"
        >
          <template #default="{ row }">
            <span class="tool-schema-view-type">{{ typeLabel(row.type, variantMode) }}</span>
          </template>
        </vxe-column>
        <vxe-column
          field="required"
          :title="variantMode === 'spec' ? '必填状态' : '是否必填'"
          :width="variantMode === 'spec' ? 96 : 120"
        >
          <template #default="{ row }">
            <span class="tool-schema-view-required" :class="{ optional: !row.required }">{{
              row.required ? "YES" : "Optional"
            }}</span>
          </template>
        </vxe-column>
        <vxe-column
          field="description"
          :title="variantMode === 'spec' ? '字段说明' : '字段描述 (Metadata)'"
          :min-width="variantMode === 'spec' ? 220 : 300"
        >
          <template #default="{ row }">
            <span v-if="variantMode === 'spec'" class="tool-schema-view-description">{{
              row.description || "暂无说明"
            }}</span>
            <div v-else class="tool-schema-view-description-card">
              <span class="tool-schema-view-description">{{ row.description || "暂无说明" }}</span>
            </div>
          </template>
        </vxe-column>
      </vxe-table>
    </div>
    <div v-else class="tool-schema-empty">{{ emptyText || "暂无结构化字段" }}</div>
  </div>
</template>
