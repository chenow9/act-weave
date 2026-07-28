<script setup lang="ts">
import AppSelect from "./AppSelect.vue";
import type { ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";

const props = defineProps<{
  modelValue: ToolSchemaNode[];
  location: "Path" | "Query" | "Header";
}>();

const emit = defineEmits<{
  "update:modelValue": [value: ToolSchemaNode[]];
}>();

const descriptions = {
  Path: "Path 参数直接映射到路径中的花括号占位符，通常数量少且不嵌套。",
  Query: "定义接口的查询参数（Query），拼接在 URL 之后，如 ?status=active。",
  Header: "定义请求需要携带的 Header 字段，用于鉴权、追踪等场景。",
};
const fieldTypeOptions = ["string", "integer", "number", "boolean"].map((type) => ({ label: type, value: type }));

function createNode(): ToolSchemaNode {
  return {
    id: `schema-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name: "",
    type: "string",
    description: "",
    required: false,
    location: props.location,
    valueSource: "UserInput",
    children: [],
    item: null,
    additionalProperties: null,
  };
}

function updateField<K extends keyof ToolSchemaNode>(index: number, key: K, value: ToolSchemaNode[K]) {
  emit(
    "update:modelValue",
    props.modelValue.map((node, nodeIndex) => (nodeIndex === index ? { ...node, [key]: value } : node)),
  );
}

function addField() {
  emit("update:modelValue", [...props.modelValue, createNode()]);
}

function removeField(index: number) {
  emit(
    "update:modelValue",
    props.modelValue.filter((_, nodeIndex) => nodeIndex !== index),
  );
}
</script>

<template>
  <section class="tool-flat-contract-editor" :aria-label="`${location} 参数`">
    <p class="tool-flat-contract-description">
      {{ descriptions[location] }}
    </p>

    <div class="tool-flat-contract-table-wrap">
      <table class="tool-flat-contract-table">
        <thead>
          <tr>
            <th>字段名称</th>
            <th>数据类型</th>
            <th>必填</th>
            <th>字段说明</th>
            <th>默认值与来源</th>
            <th><span class="sr-only">操作</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(node, index) in modelValue" :key="node.id">
            <td>
              <input
                :value="node.name"
                placeholder="字段名称"
                @input="updateField(index, 'name', ($event.target as HTMLInputElement).value)"
              />
            </td>
            <td>
              <AppSelect
                class="tool-flat-type-select"
                :model-value="node.type"
                :options="fieldTypeOptions"
                compact
                :aria-label="`${node.name || `字段 ${index + 1}`}数据类型`"
                @update:model-value="updateField(index, 'type', String($event) as ToolSchemaNodeType)"
              />
            </td>
            <td class="tool-flat-required-cell">
              <button
                class="tool-flat-required-toggle"
                :class="{ on: node.required }"
                type="button"
                :aria-label="`${node.name || '当前字段'}${node.required ? '设为选填' : '设为必填'}`"
                :aria-pressed="node.required"
                @click="updateField(index, 'required', !node.required)"
              >
                <i v-if="node.required" class="fa-solid fa-check" />
              </button>
            </td>
            <td>
              <input
                :value="node.description"
                placeholder="字段说明（可选）"
                @input="updateField(index, 'description', ($event.target as HTMLInputElement).value)"
              />
            </td>
            <td>
              <input
                :value="String(node.defaultValue ?? node.valueSource ?? '')"
                placeholder="默认值 / 来源"
                @input="updateField(index, 'defaultValue', ($event.target as HTMLInputElement).value)"
              />
            </td>
            <td>
              <button
                class="tool-flat-delete"
                type="button"
                :aria-label="`删除字段 ${node.name || index + 1}`"
                @click="removeField(index)"
              >
                <i class="fa-solid fa-xmark" />
              </button>
            </td>
          </tr>
          <tr v-if="!modelValue.length">
            <td colspan="6"><div class="tool-flat-empty">暂无参数，点击下方“添加字段”开始配置</div></td>
          </tr>
        </tbody>
      </table>
    </div>
    <button class="tool-flat-add" type="button" @click="addField"><i class="fa-solid fa-plus" /> 添加字段</button>
  </section>
</template>

<style scoped>
.tool-flat-contract-description {
  margin: 0 0 14px;
  color: var(--aw-muted);
  font-size: 12px;
}
.tool-flat-contract-description code {
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--aw-cyan-soft);
  color: #0f766e;
  font-family: "SF Mono", Consolas, Menlo, monospace;
  font-size: 11px;
}
.tool-flat-contract-table-wrap {
  overflow-x: auto;
  border: 1px solid var(--aw-border);
  border-radius: 8px;
}
.tool-flat-contract-table {
  width: 100%;
  border-collapse: collapse;
  font-family: var(--aw-table-font);
}
.tool-flat-contract-table th {
  padding: 9px 10px;
  border-bottom: 1px solid var(--aw-border);
  background: #f8fafc;
  color: var(--aw-table-header-color);
  font-size: var(--aw-table-header-size);
  font-weight: var(--aw-table-header-weight);
  text-align: left;
}
.tool-flat-contract-table th:nth-child(1) {
  width: 20%;
}
.tool-flat-contract-table th:nth-child(2) {
  width: 14%;
}
.tool-flat-contract-table th:nth-child(3) {
  width: 8%;
  text-align: center;
}
.tool-flat-contract-table th:nth-child(4) {
  width: 26%;
}
.tool-flat-contract-table th:nth-child(5) {
  width: 22%;
}
.tool-flat-contract-table td {
  padding: 8px 10px;
  border-bottom: 1px solid var(--aw-border-soft);
  background: #fff;
  color: var(--aw-table-body-color);
  font-size: var(--aw-table-body-size);
  font-weight: var(--aw-table-body-weight);
  vertical-align: middle;
}
.tool-flat-contract-table tbody tr:last-child td {
  border-bottom: 0;
}
.tool-flat-contract-table input {
  width: 100%;
  min-height: 36px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  padding: 6px 8px;
  color: var(--aw-text);
  font: inherit;
  font-size: var(--aw-table-body-size);
}
.tool-flat-contract-table input:hover {
  border-color: var(--aw-border);
  background: #f8fafc;
}
.tool-flat-contract-table input:focus {
  outline: 0;
  border-color: rgb(13 148 136 / 0.55);
  background: #fff;
  box-shadow: 0 0 0 3px var(--aw-cyan-soft);
}
.tool-flat-type-select {
  min-width: 96px;
}
.tool-flat-required-cell {
  text-align: center;
}
.tool-flat-required-toggle {
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1.5px solid var(--aw-border);
  border-radius: 4px;
  background: #fff;
  color: #fff;
  cursor: pointer;
}
.tool-flat-required-toggle.on {
  border-color: var(--aw-cyan);
  background: var(--aw-cyan);
}
.tool-flat-required-toggle i {
  font-size: 10px;
}
.tool-flat-delete {
  border: 0;
  background: transparent;
  color: #94a3b8;
  cursor: pointer;
}
.tool-flat-delete:hover {
  color: var(--aw-red);
}
.tool-flat-empty {
  padding: 50px 0;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}
.tool-flat-add {
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 12px;
  padding: 0 12px;
  border: 1px solid var(--aw-border);
  border-radius: 6px;
  background: #f8fafc;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}
.tool-flat-add:hover {
  border-color: rgb(13 148 136 / 0.35);
  background: #fff;
  color: var(--aw-cyan);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}
</style>
