<script setup lang="ts">
import { computed, getCurrentInstance } from "vue";
import { useI18n } from "vue-i18n";

import { ensureVxe } from "../plugins/register-vxe";
import type { ToolSchemaNode } from "../types/domain";

const { t } = useI18n();
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

const props = withDefaults(
  defineProps<{
    nodes: ToolSchemaNode[];
    title?: string;
    summaryTitle?: string;
    summaryDescription?: string;
    emptyText?: string;
    variant?: "default" | "spec";
    defaultLocation?: string;
    showLocation?: boolean;
    expandAll?: boolean;
    compact?: boolean;
  }>(),
  {
    expandAll: true,
    compact: false,
  },
);

const tableRows = computed(() => props.nodes.map((node) => toTableRow(node)));
const showLocationColumn = computed(() => {
  if (props.showLocation === false) return false;
  if (props.showLocation === true) return true;
  return variantMode.value === "spec" || containsLocation(props.nodes) || Boolean(props.defaultLocation);
});
const variantMode = computed(() => props.variant || "default");
const summaryText = computed(() => {
  const total = countNodes(props.nodes);
  const required = countRequiredNodes(props.nodes);
  const depth = maxDepth(props.nodes);
  return t("tools.schemaSummary", { total, required, depth });
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
  const defaultLabels: Record<string, string> = {
    string: t("tools.typeString"),
    integer: t("tools.typeInteger"),
    number: t("tools.typeNumber"),
    boolean: t("tools.typeBoolean"),
    object: t("tools.typeObject"),
    array: t("tools.typeArray"),
  };
  const specLabels: Record<string, string> = {
    string: "STRING",
    integer: "INTEGER",
    number: "NUMBER",
    boolean: "BOOLEAN",
    object: "OBJECT",
    array: "ARRAY",
  };
  return (variant === "spec" ? specLabels : defaultLabels)[type] || type;
}

function locationLabel(location: string | undefined, variant: "default" | "spec") {
  const defaultLabels: Record<string, string> = {
    Path: t("tools.locPath"),
    Query: t("tools.locQuery"),
    Body: t("tools.locBody"),
    Header: t("tools.locHeader"),
    Response: t("tools.locResponse"),
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
    return t("tools.arrayItem");
  }
  return row.name || t("tools.unnamedFieldParen");
}

function buildPath(row: SchemaTableRow, parentPath = ""): string {
  const segment = nameLabel(row);
  if (row.__kind === "array-item") {
    return parentPath ? `${parentPath}[]` : "[]";
  }
  return parentPath ? `${parentPath}.${segment}` : segment;
}

/** Show key path only when it adds information beyond the field name. */
function showPathHint(row: SchemaTableRow) {
  const name = nameLabel(row);
  return Boolean(row.__path && row.__path !== name);
}

function requiredLabel(required: boolean, variant: "default" | "spec") {
  if (variant === "spec") {
    return required ? "YES" : "Optional";
  }
  return required ? t("common.required") : t("common.optional");
}

function descriptionDisplay(description: string | undefined, variant: "default" | "spec") {
  const text = (description || "").trim();
  if (text) return text;
  return variant === "spec" ? t("tools.noDescriptionDash") : "—";
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
  <div class="tool-schema-view" :class="{ 'tool-schema-view-spec': variantMode === 'spec', compact }">
    <div v-if="title" class="tool-schema-view-head" :class="{ 'tool-schema-view-head-spec': variantMode === 'spec' }">
      <div class="tool-schema-view-head-main">
        <strong>{{ summaryTitle || title }}</strong>
        <span v-if="summaryDescription">{{ summaryDescription }}</span>
      </div>
      <span class="tool-schema-view-summary">{{
        variantMode === "spec" ? t("tools.activeFields", { n: countActiveFields(nodes) }) : summaryText
      }}</span>
    </div>
    <div v-if="tableRows.length" class="tool-schema-tree" :class="{ 'tool-schema-tree-spec': variantMode === 'spec' }">
      <vxe-table
        class="tool-schema-vxe-table tool-schema-vxe-table-readonly"
        data-vxe-ui-theme="light"
        border="none"
        round
        :size="variantMode === 'spec' || compact ? 'small' : 'medium'"
        :height="variantMode === 'spec' ? '100%' : undefined"
        :data="tableRowsWithDepth"
        :row-config="{ keyField: 'id', useKey: true, isHover: true }"
        :tree-config="{ childrenField: 'children', expandAll, showLine: true }"
        show-overflow="title"
      >
        <vxe-column
          v-if="showLocationColumn && variantMode !== 'spec'"
          field="location"
          :title="t('tools.schemaColLocation')"
          width="140"
        >
          <template #default="{ row }">
            <span class="tool-schema-cell-text">
              {{ locationLabel(row.__locationLabel, variantMode) }}
            </span>
          </template>
        </vxe-column>
        <vxe-column
          field="name"
          :title="t('tools.schemaColFieldName')"
          tree-node
          :min-width="variantMode === 'spec' ? 220 : 220"
        >
          <template #default="{ row }">
            <div
              class="tool-schema-view-name-block"
              :class="{ 'tool-schema-view-name-block-spec': variantMode === 'spec' }"
              :title="row.__path || nameLabel(row)"
            >
              <template v-if="variantMode === 'spec'">
                <div class="tool-schema-view-keyline tool-schema-view-keyline-spec">
                  <span class="tool-schema-view-spec-indent" :style="{ width: `${row.__depth * 22}px` }" />
                  <span v-if="row.__depth > 0" class="tool-schema-view-branch-spec">└</span>
                  <span class="tool-schema-view-name" :class="{ 'is-root': row.__depth === 0 }">{{
                    nameLabel(row)
                  }}</span>
                </div>
              </template>
              <template v-else>
                <!-- Scheme R: key only; path as muted hint when nested -->
                <div class="tool-schema-view-keyline">
                  <span class="tool-schema-view-name" :class="{ 'is-root': row.__depth === 0 }">{{
                    nameLabel(row)
                  }}</span>
                </div>
                <span v-if="showPathHint(row)" class="tool-schema-view-path">{{ row.__path }}</span>
              </template>
            </div>
          </template>
        </vxe-column>
        <vxe-column
          v-if="showLocationColumn && variantMode === 'spec'"
          field="location"
          :title="t('tools.schemaColLocationShort')"
          width="92"
        >
          <template #default="{ row }">
            <span class="tool-schema-cell-text tool-schema-cell-text-spec">
              {{ locationLabel(row.__locationLabel, variantMode) }}
            </span>
          </template>
        </vxe-column>
        <vxe-column
          field="type"
          :title="variantMode === 'spec' ? t('tools.schemaColDataType') : t('tools.schemaColType')"
          :width="variantMode === 'spec' ? 108 : 112"
        >
          <template #default="{ row }">
            <span class="tool-schema-view-type" :data-type="row.type">{{ typeLabel(row.type, variantMode) }}</span>
          </template>
        </vxe-column>
        <vxe-column
          field="required"
          :title="variantMode === 'spec' ? t('tools.schemaColRequiredState') : t('tools.schemaColRequired')"
          :width="variantMode === 'spec' ? 96 : 88"
        >
          <template #default="{ row }">
            <span class="tool-schema-view-required" :class="{ optional: !row.required, required: row.required }">{{
              requiredLabel(Boolean(row.required), variantMode)
            }}</span>
          </template>
        </vxe-column>
        <vxe-column
          field="description"
          :title="variantMode === 'spec' ? t('tools.schemaColFieldDesc') : t('tools.schemaColDesc')"
          :min-width="variantMode === 'spec' ? 220 : 240"
        >
          <template #default="{ row }">
            <span class="tool-schema-view-description" :class="{ 'is-empty': !String(row.description || '').trim() }">{{
              descriptionDisplay(row.description, variantMode)
            }}</span>
          </template>
        </vxe-column>
      </vxe-table>
    </div>
    <div v-else class="tool-schema-empty">{{ emptyText || t("tools.noStructuredFields") }}</div>
  </div>
</template>
