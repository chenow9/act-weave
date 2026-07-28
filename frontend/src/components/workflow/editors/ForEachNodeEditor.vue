<script setup lang="ts">
import AppSelect from "../../AppSelect.vue";
import WorkflowVariablePicker from "../WorkflowVariablePicker.vue";
import type { WorkflowGraphNode } from "../../../types/domain";

const props = defineProps<{
  node: WorkflowGraphNode;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const variableOptions = props.variableRefs
  .map((value) => value.replace(/^\{\{/, "").replace(/\}\}$/, ""))
  .filter(Boolean)
  .map((value) => ({ label: value, value }));

function collectionPath() {
  const collection = props.node.data.collection;
  if (!collection || typeof collection !== "object" || Array.isArray(collection)) {
    return "";
  }
  const path = (collection as Record<string, unknown>).path;
  return typeof path === "string" ? path : "";
}

function itemAlias() {
  const value = props.node.data.itemAlias;
  return typeof value === "string" ? value : "";
}

function concurrencyValue() {
  const value = props.node.data.concurrency;
  return typeof value === "number" ? String(value) : typeof value === "string" ? value : "";
}

function updateCollection(path: string) {
  emit("update-node-data", {
    key: "collection",
    value: {
      kind: "ref",
      path,
    },
  });
}

function updateConcurrency(value: string) {
  const trimmed = value.trim();
  emit("update-node-data", {
    key: "concurrency",
    value: trimmed === "" ? undefined : Number(trimmed),
  });
}

function outputPath() {
  const output = props.node.data.output;
  if (!output || typeof output !== "object" || Array.isArray(output)) {
    return "";
  }
  const items = (output as Record<string, unknown>).items;
  if (!items || typeof items !== "object" || Array.isArray(items)) {
    return "";
  }
  const path = (items as Record<string, unknown>).path;
  return typeof path === "string" ? path : "";
}

function updateOutputPath(path: string) {
  emit("update-node-data", {
    key: "output",
    value: {
      items: {
        kind: "ref",
        path,
      },
      count: {
        kind: "ref",
        path: path ? path.replace(/\.items$/, ".count") : "",
      },
    },
  });
}
</script>

<template>
  <section class="workflow-foreach-node-editor">
    <label class="drawer-field">
      <span>集合引用</span>
      <AppSelect
        class="workflow-advanced-input-select"
        :model-value="collectionPath()"
        :options="variableOptions"
        placeholder="选择数组变量"
        @update:model-value="updateCollection(String($event))"
      />
    </label>

    <label class="drawer-field">
      <span>迭代别名</span>
      <input
        name="node-foreach-item-alias"
        :value="itemAlias()"
        placeholder="例如 order"
        @input="emit('update-node-data', { key: 'itemAlias', value: ($event.target as HTMLInputElement).value })"
      />
    </label>

    <label class="drawer-field">
      <span>并发度</span>
      <input
        name="node-foreach-concurrency"
        inputmode="numeric"
        :value="concurrencyValue()"
        placeholder="例如 3"
        @input="updateConcurrency(($event.target as HTMLInputElement).value)"
      />
    </label>

    <WorkflowVariablePicker
      class="workflow-end-node-variable-picker"
      :model-value="outputPath()"
      :variable-refs="variableRefs"
      @update:model-value="updateOutputPath"
    />

    <section class="workflow-inspector-vars">
      <div class="workflow-section-caption">
        <strong>可用迭代变量</strong>
        <small>下游可引用</small>
      </div>
      <div class="workflow-token-list">
        <span class="workflow-token">foreach.item</span>
        <span class="workflow-token">foreach.item.id</span>
      </div>
    </section>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>运行语义</strong>
        <small>foreach controller</small>
      </div>
      <p>
        当前运行时会解析 collection、itemAlias、concurrency，并让下游节点在循环作用域中使用 `foreach.item` /
        `foreach.alias` 路径，同时支持 loop output mapping。
      </p>
    </section>
  </section>
</template>
