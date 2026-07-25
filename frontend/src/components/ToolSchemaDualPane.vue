<script setup lang="ts">
import { computed, nextTick, ref } from "vue";

import type { ToolSchemaNode } from "../types/domain";

type LinkedSchemaRowKind = "node" | "array-item" | "additional-properties";

interface LinkedSchemaRow {
  fieldId: string;
  name: string;
  type: string;
  required: boolean;
  description: string;
  depth: number;
  path: string;
  location: string;
  kind: LinkedSchemaRowKind;
}

interface LinkedJsonLine {
  key: string;
  text: string;
  fieldId: string;
}

const props = defineProps<{
  modelValue: ToolSchemaNode[];
  title: string;
  description: string;
  rootLabel: string;
}>();

const anchorPalette = [
  "#0f766e",
  "#2563eb",
  "#b45309",
  "#be123c",
  "#7c3aed",
  "#047857",
  "#c2410c",
  "#0369a1",
];

const dualPaneRef = ref<HTMLElement | null>(null);
const hoveredFieldId = ref("");
const selectedFieldId = ref("");

const linkedRows = computed(() => buildLinkedRows(props.modelValue));
const linkedJsonLines = computed(() => buildLinkedJsonLines(props.modelValue));
const linkedFieldIds = computed(() => linkedRows.value.map((row) => row.fieldId));
const specDefaultLocation = computed(() =>
  props.rootLabel.includes("Request") ? "Body" : props.rootLabel.includes("Response") ? "Response" : "",
);
const summaryText = computed(() => {
  const total = linkedRows.value.length;
  const required = linkedRows.value.filter((row) => row.required).length;
  const maxDepth = linkedRows.value.reduce((depth, row) => Math.max(depth, row.depth + 1), 0);
  return `${total} 个节点 · ${required} 个必填 · ${maxDepth} 层结构`;
});

function buildLinkedRows(nodes: ToolSchemaNode[], depth = 0, parentPath = "", inheritedLocation = ""): LinkedSchemaRow[] {
  return nodes.flatMap((node, index) => {
    const fieldId = schemaFieldId(node, `${parentPath || "root"}.${node.name || index}`);
    const name = node.name || "(未命名字段)";
    const path = parentPath ? `${parentPath}.${name}` : name;
    const location = node.location || inheritedLocation;
    const row: LinkedSchemaRow = {
      fieldId,
      name,
      type: node.type,
      required: node.required,
      description: node.description || "",
      depth,
      path,
      location,
      kind: "node",
    };
    const childRows =
      node.type === "object"
        ? buildLinkedRows(node.children || [], depth + 1, path, location)
        : node.type === "array" && node.item
          ? buildArrayItemRows(node.item, depth + 1, path, location)
          : [];
    const additionalRows = node.additionalProperties
      ? buildAdditionalPropertyRows(node.additionalProperties, depth + 1, path, location)
      : [];
    return [row, ...childRows, ...additionalRows];
  });
}

function buildArrayItemRows(node: ToolSchemaNode, depth: number, parentPath: string, inheritedLocation: string): LinkedSchemaRow[] {
  const fieldId = schemaFieldId(node, `${parentPath}.items`);
  const path = `${parentPath}[]`;
  const row: LinkedSchemaRow = {
    fieldId,
    name: "数组元素",
    type: node.type,
    required: node.required,
    description: node.description || "",
    depth,
    path,
    location: node.location || inheritedLocation,
    kind: "array-item",
  };
  const childRows = node.type === "object" ? buildLinkedRows(node.children || [], depth + 1, path, node.location || inheritedLocation) : [];
  return [row, ...childRows];
}

function buildAdditionalPropertyRows(node: ToolSchemaNode, depth: number, parentPath: string, inheritedLocation: string): LinkedSchemaRow[] {
  const fieldId = schemaFieldId(node, `${parentPath}.additionalProperties`);
  const path = `${parentPath}.{key}`;
  const row: LinkedSchemaRow = {
    fieldId,
    name: "动态键值",
    type: node.type,
    required: node.required,
    description: node.description || "",
    depth,
    path,
    location: node.location || inheritedLocation,
    kind: "additional-properties",
  };
  const childRows = node.type === "object" ? buildLinkedRows(node.children || [], depth + 1, path, node.location || inheritedLocation) : [];
  return [row, ...childRows];
}

function buildLinkedJsonLines(nodes: ToolSchemaNode[]): LinkedJsonLine[] {
  const lines: LinkedJsonLine[] = [{ key: "root-open", text: "{", fieldId: "" }];
  nodes.forEach((node, index) => {
    appendNodeJsonLines(lines, node, node.name || `field${index + 1}`, 1, index === nodes.length - 1, `root.${node.name || index}`);
  });
  lines.push({ key: "root-close", text: "}", fieldId: "" });
  return lines;
}

function appendNodeJsonLines(
  lines: LinkedJsonLine[],
  node: ToolSchemaNode,
  name: string,
  indent: number,
  isLast: boolean,
  path: string,
) {
  const fieldId = schemaFieldId(node, path);
  const entries = buildNodeJsonEntries(node, indent + 1, path, fieldId);
  const lineIndex = lines.filter((line) => line.fieldId === fieldId).length;
  lines.push({ key: `${fieldId}:${lineIndex}`, text: `${indentText(indent)}${JSON.stringify(name)}: {`, fieldId });
  entries.forEach((entry, index) => {
    const entryLines = index === entries.length - 1 ? entry : withCommaOnLastLine(entry);
    lines.push(...entryLines);
  });
  lines.push({
    key: `${fieldId}:${lineIndex + entries.length + 1}`,
    text: `${indentText(indent)}}${isLast ? "" : ","}`,
    fieldId,
  });
}

function buildNodeJsonEntries(node: ToolSchemaNode, indent: number, path: string, fieldId: string): LinkedJsonLine[][] {
  const entries: LinkedJsonLine[][] = [
    [jsonPropertyLine(fieldId, path, "type", indent, "type", node.type)],
    [jsonPropertyLine(fieldId, path, "required", indent, "required", node.required)],
  ];

  if (node.description) {
    entries.push([jsonPropertyLine(fieldId, path, "description", indent, "description", node.description)]);
  }
  if (node.format) {
    entries.push([jsonPropertyLine(fieldId, path, "format", indent, "format", node.format)]);
  }
  if (node.nullable !== undefined) {
    entries.push([jsonPropertyLine(fieldId, path, "nullable", indent, "nullable", node.nullable)]);
  }
  if (node.example !== undefined) {
    entries.push([jsonPropertyLine(fieldId, path, "example", indent, "example", node.example)]);
  }
  if (node.defaultValue !== undefined) {
    entries.push([jsonPropertyLine(fieldId, path, "default", indent, "default", node.defaultValue)]);
  }
  if (node.valueSource) {
    entries.push([jsonPropertyLine(fieldId, path, "valueSource", indent, "valueSource", node.valueSource)]);
  }
  if (node.enumValues?.length) {
    entries.push([jsonPropertyLine(fieldId, path, "enum", indent, "enum", node.enumValues)]);
  }
  if (node.type === "object") {
    entries.push(buildObjectPropertiesEntry(node, indent, path, fieldId));
  }
  if (node.type === "array" && node.item) {
    entries.push(buildArrayItemsEntry(node.item, indent, `${path}.items`, fieldId));
  }
  if (node.additionalProperties) {
    entries.push(buildAdditionalPropertiesEntry(node.additionalProperties, indent, `${path}.additionalProperties`, fieldId));
  }

  return entries;
}

function buildObjectPropertiesEntry(node: ToolSchemaNode, indent: number, path: string, fieldId: string): LinkedJsonLine[] {
  const entry: LinkedJsonLine[] = [{ key: `${fieldId}:properties-open`, text: `${indentText(indent)}"properties": {`, fieldId }];
  const children = node.children || [];
  children.forEach((child, index) => {
    appendNodeJsonLines(entry, child, child.name || `field${index + 1}`, indent + 1, index === children.length - 1, `${path}.properties.${child.name || index}`);
  });
  entry.push({ key: `${fieldId}:properties-close`, text: `${indentText(indent)}}`, fieldId });
  return entry;
}

function buildArrayItemsEntry(node: ToolSchemaNode, indent: number, path: string, fieldId: string): LinkedJsonLine[] {
  const entry: LinkedJsonLine[] = [{ key: `${fieldId}:items-open`, text: `${indentText(indent)}"items": {`, fieldId }];
  for (const itemEntry of buildNodeJsonEntries(node, indent + 1, path, schemaFieldId(node, path))) {
    entry.push(...itemEntry);
  }
  entry.push({ key: `${fieldId}:items-close`, text: `${indentText(indent)}}`, fieldId });
  return entry;
}

function buildAdditionalPropertiesEntry(node: ToolSchemaNode, indent: number, path: string, fieldId: string): LinkedJsonLine[] {
  const entry: LinkedJsonLine[] = [{ key: `${fieldId}:additional-open`, text: `${indentText(indent)}"additionalProperties": {`, fieldId }];
  for (const additionalEntry of buildNodeJsonEntries(node, indent + 1, path, schemaFieldId(node, path))) {
    entry.push(...additionalEntry);
  }
  entry.push({ key: `${fieldId}:additional-close`, text: `${indentText(indent)}}`, fieldId });
  return entry;
}

function jsonPropertyLine(fieldId: string, path: string, key: string, indent: number, suffix: string, value: unknown): LinkedJsonLine {
  return {
    key: `${fieldId}:${suffix}:${path}`,
    text: `${indentText(indent)}${JSON.stringify(key)}: ${JSON.stringify(value)}`,
    fieldId,
  };
}

function withCommaOnLastLine(lines: LinkedJsonLine[]): LinkedJsonLine[] {
  return lines.map((line, index) => (index === lines.length - 1 ? { ...line, text: `${line.text},` } : line));
}

function schemaFieldId(node: ToolSchemaNode, fallback: string) {
  return node.id || fallback.replace(/[^a-zA-Z0-9_-]+/g, "-");
}

function indentText(depth: number) {
  return "  ".repeat(depth);
}

function colorForField(fieldId: string) {
  const index = Math.max(0, linkedFieldIds.value.indexOf(fieldId));
  return anchorPalette[index % anchorPalette.length];
}

function anchorStyle(fieldId: string) {
  return fieldId ? { "--schema-anchor-color": colorForField(fieldId) } : undefined;
}

function isInteractive(fieldId: string) {
  return Boolean(fieldId);
}

function isLinked(fieldId: string) {
  return Boolean(fieldId && hoveredFieldId.value === fieldId);
}

function isSelected(fieldId: string) {
  return Boolean(fieldId && selectedFieldId.value === fieldId);
}

function hoverField(fieldId: string) {
  hoveredFieldId.value = fieldId;
}

async function selectField(fieldId: string, scrollTarget: "json" | "table") {
  if (!fieldId) return;
  selectedFieldId.value = fieldId;
  await nextTick();
  const selector =
    scrollTarget === "json"
      ? `[data-schema-line-key^="${cssEscape(fieldId)}:"]`
      : `tr[data-schema-field-id="${cssEscape(fieldId)}"]`;
  dualPaneRef.value?.querySelector(selector)?.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function cssEscape(value: string) {
  return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(value) : value.replace(/"/g, '\\"');
}

function typeLabel(type: string) {
  const labels: Record<string, string> = {
    string: "STRING",
    integer: "INTEGER",
    number: "NUMBER",
    boolean: "BOOLEAN",
    object: "OBJECT",
    array: "ARRAY",
  };
  return labels[type] || type.toUpperCase();
}

function locationLabel(row: LinkedSchemaRow) {
  const location = row.location || specDefaultLocation.value;
  const labels: Record<string, string> = {
    Path: "Path",
    Query: "Query",
    Body: "Body",
    Header: "Header",
    Response: "Response",
  };
  return location ? labels[location] || location : "-";
}

function fieldNameLabel(row: LinkedSchemaRow) {
  if (row.kind === "array-item") return "数组元素";
  if (row.kind === "additional-properties") return "动态键值";
  return row.name;
}
</script>

<template>
  <div ref="dualPaneRef" class="tool-contract-dual-pane tool-schema-linked-workbench">
    <section class="tool-contract-editor-shell tool-contract-editor-shell-ide tool-schema-linked-json-pane" aria-label="JSON 只读映射预览">
      <div class="tool-contract-editor-caption">
        <div class="tool-contract-editor-dots">
          <span />
          <span />
          <span />
        </div>
        <div class="tool-contract-editor-caption-main">
          <span class="tool-contract-editor-filename">payload_schema.jsonc</span>
          <span class="tool-contract-editor-readonly-badge">只读映射预览</span>
        </div>
      </div>
      <div class="tool-schema-linked-json" role="list" aria-label="JSON 字段映射">
        <button
          v-for="(line, index) in linkedJsonLines"
          :key="line.key"
          class="tool-schema-linked-code-line"
          :class="{ 'is-linked': isLinked(line.fieldId), 'is-selected': isSelected(line.fieldId), 'is-static': !isInteractive(line.fieldId) }"
          type="button"
          role="listitem"
          :data-schema-field-id="line.fieldId || undefined"
          :data-schema-line-key="line.key"
          :style="anchorStyle(line.fieldId)"
          :disabled="!isInteractive(line.fieldId)"
          @mouseenter="hoverField(line.fieldId)"
          @mouseleave="hoverField('')"
          @click="selectField(line.fieldId, 'table')"
        >
          <span class="tool-schema-linked-line-number">{{ index + 1 }}</span>
          <code>{{ line.text }}</code>
        </button>
      </div>
      <div class="tool-contract-editor-footer">
        <span>JSON 由结构化契约实时派生，参数详情中不可直接编辑。</span>
        <span>点击字段可固定选中态</span>
      </div>
    </section>

    <section class="tool-schema-view tool-schema-view-spec tool-schema-linked-table-pane" aria-label="结构化字段映射">
      <div class="tool-schema-view-head tool-schema-view-head-spec">
        <div class="tool-schema-view-head-main">
          <strong>结构化字段说明</strong>
          <span>{{ description }}</span>
        </div>
        <span class="tool-schema-view-summary">{{ summaryText }}</span>
      </div>
      <div v-if="linkedRows.length" class="tool-schema-linked-table-shell">
        <table class="tool-schema-linked-table">
          <thead>
            <tr>
              <th>字段名</th>
              <th>位置</th>
              <th>数据类型</th>
              <th>必填状态</th>
              <th>字段说明</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in linkedRows"
              :key="row.fieldId"
              :class="{ 'is-linked': isLinked(row.fieldId), 'is-selected': isSelected(row.fieldId) }"
              :data-schema-field-id="row.fieldId"
              :style="anchorStyle(row.fieldId)"
              @mouseenter="hoverField(row.fieldId)"
              @mouseleave="hoverField('')"
              @click="selectField(row.fieldId, 'json')"
            >
              <td>
                <div class="tool-schema-linked-field-name" :title="row.path">
                  <span class="tool-schema-view-spec-indent" :style="{ width: `${row.depth * 22}px` }" />
                  <span v-if="row.depth > 0" class="tool-schema-view-branch-spec">└</span>
                  <span class="tool-schema-view-name" :class="{ 'is-root': row.depth === 0 }">{{ fieldNameLabel(row) }}</span>
                </div>
              </td>
              <td><span class="tool-schema-cell-text tool-schema-cell-text-spec">{{ locationLabel(row) }}</span></td>
              <td><span class="tool-schema-view-type">{{ typeLabel(row.type) }}</span></td>
              <td><span class="tool-schema-view-required" :class="{ optional: !row.required }">{{ row.required ? "YES" : "Optional" }}</span></td>
              <td><span class="tool-schema-view-description">{{ row.description || "暂无说明" }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="tool-schema-empty">暂无结构化字段</div>
    </section>
  </div>
</template>
