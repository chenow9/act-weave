<script setup lang="ts">
import { useI18n } from "vue-i18n";

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

const { t } = useI18n();

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
      <span>{{ t("workflow.collectionRef") }}</span>
      <AppSelect
        class="workflow-advanced-input-select"
        :model-value="collectionPath()"
        :options="variableOptions"
        :placeholder="t('workflow.selectArrayVar')"
        @update:model-value="updateCollection(String($event))"
      />
    </label>

    <label class="drawer-field">
      <span>{{ t("workflow.itemAlias") }}</span>
      <input
        name="node-foreach-item-alias"
        :value="itemAlias()"
        :placeholder="t('workflow.itemAliasPh')"
        @input="emit('update-node-data', { key: 'itemAlias', value: ($event.target as HTMLInputElement).value })"
      />
    </label>

    <label class="drawer-field">
      <span>{{ t("workflow.concurrency") }}</span>
      <input
        name="node-foreach-concurrency"
        inputmode="numeric"
        :value="concurrencyValue()"
        :placeholder="t('workflow.concurrencyPh')"
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
        <strong>{{ t("workflow.iterationVars") }}</strong>
        <small>{{ t("workflow.downstreamUsable") }}</small>
      </div>
      <div class="workflow-token-list">
        <span class="workflow-token">foreach.item</span>
        <span class="workflow-token">foreach.item.id</span>
      </div>
    </section>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.runtimeSemantics") }}</strong>
        <small>foreach controller</small>
      </div>
      <p>{{ t("workflow.foreachRuntimeHint") }}</p>
    </section>
  </section>
</template>
