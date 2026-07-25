<script setup lang="ts">
import { ref, watch } from "vue";

import AppSelect from "../../AppSelect.vue";
import type { WorkflowGraphNode } from "../../../types/domain";

const props = defineProps<{
  node: WorkflowGraphNode;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const inputModeOptions = [
  { label: "变量映射", value: "mapping" },
  { label: "JSON 输入", value: "json" },
];
const variableOptions = props.variableRefs
  .map((value) => value.replace(/^\{\{/, "").replace(/\}\}$/, ""))
  .filter(Boolean)
  .map((value) => ({ label: value, value }));
const inputMode = ref<"mapping" | "json">("mapping");
const mappingDraft = ref<Array<{ key: string; path: string }>>([]);
const rawInputText = ref("{}");

watch(
  () => [props.node.data.input, props.node.data.inputMapping] as const,
  ([rawInput, rawMapping]) => {
    inputMode.value = rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? "json" : "mapping";
    rawInputText.value = JSON.stringify(rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? rawInput : {}, null, 2);
    mappingDraft.value = normalizeMappingDraft(rawMapping);
    if (!mappingDraft.value.length) {
      mappingDraft.value = [{ key: "value", path: "" }];
    }
  },
  { immediate: true, deep: true },
);

function workflowId() {
  const value = props.node.data.workflowId;
  return typeof value === "string" ? value : "";
}

function mappingEntries() {
  return mappingDraft.value;
}

function mappingRefValue(key: string) {
  return mappingDraft.value.find((entry) => entry.key === key)?.path || "";
}

function renameMappingKey(index: number, nextKey: string) {
  const previousKey = mappingDraft.value[index]?.key;
  if (!previousKey || !nextKey || previousKey === nextKey) {
    return;
  }
  mappingDraft.value = mappingDraft.value.map((entry, currentIndex) => (currentIndex === index ? { ...entry, key: nextKey } : entry));
  emitInputMapping();
}

function setRefMapping(key: string, path: string) {
  mappingDraft.value = mappingDraft.value.map((entry) => (entry.key === key ? { ...entry, path } : entry));
  emitInputMapping();
}

function addMappingRow() {
  mappingDraft.value = [...mappingDraft.value, { key: `field${mappingDraft.value.length + 1}`, path: "" }];
  emitInputMapping();
}

function removeMappingRow(key: string) {
  mappingDraft.value = mappingDraft.value.filter((entry) => entry.key !== key);
  if (!mappingDraft.value.length) {
    mappingDraft.value = [{ key: "value", path: "" }];
  }
  emitInputMapping();
}

function emitInputMapping() {
  emit("update-node-data", {
    key: "__merge",
    value: {
      inputMapping: Object.fromEntries(
        mappingDraft.value
          .filter((entry) => entry.key.trim() && entry.path.trim())
          .map((entry) => [entry.key, { kind: "ref", path: entry.path }]),
      ),
    },
  });
}

function currentRawInput() {
  const rawInput = props.node.data.input;
  return rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? rawInput : {};
}

function updateInputMode(mode: string) {
  inputMode.value = mode === "json" ? "json" : "mapping";
  if (mode === "json") {
    emit("update-node-data", {
      key: "__merge",
      value: {
        input: currentRawInput(),
        inputMapping: undefined,
      },
    });
    return;
  }
  emit("update-node-data", {
    key: "__merge",
    value: {
      input: undefined,
      inputMapping: Object.fromEntries(
        mappingDraft.value
          .filter((entry) => entry.key.trim() && entry.path.trim())
          .map((entry) => [entry.key, { kind: "ref", path: entry.path }]),
      ),
    },
  });
}

function updateRawInput(value: string) {
  rawInputText.value = value;
  try {
    emit("update-node-data", {
      key: "__merge",
      value: {
        input: JSON.parse(value),
        inputMapping: undefined,
      },
    });
  } catch {}
}

function normalizeMappingDraft(rawMapping: unknown) {
  if (!rawMapping || typeof rawMapping !== "object" || Array.isArray(rawMapping)) {
    return [];
  }
  return Object.entries(rawMapping as Record<string, unknown>).map(([key, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return { key, path: "" };
    }
    const path = (value as Record<string, unknown>).path;
    return { key, path: typeof path === "string" ? path : "" };
  });
}
</script>

<template>
  <section class="workflow-subworkflow-node-editor">
    <label class="drawer-field">
      <span>目标 Workflow ID</span>
      <input
        name="node-subworkflow-id"
        :value="workflowId()"
        placeholder="输入已发布 Workflow ID"
        @input="emit('update-node-data', { key: 'workflowId', value: ($event.target as HTMLInputElement).value })"
      />
    </label>

    <section class="workflow-inspector-vars">
      <div class="workflow-section-caption">
        <strong>输入绑定</strong>
        <small>支持 `input` 或 `inputMapping`</small>
      </div>
      <div class="workflow-tool-mapping-mode" role="group" aria-label="SubWorkflow 输入模式">
        <button type="button" :class="{ active: inputMode === 'mapping' }" @click="updateInputMode('mapping')">变量映射</button>
        <button type="button" :class="{ active: inputMode === 'json' }" @click="updateInputMode('json')">JSON 输入</button>
      </div>

      <div v-if="inputMode === 'mapping'" class="workflow-tool-param-list workflow-advanced-input-list">
        <article
          v-for="(entry, index) in mappingEntries()"
          :key="`${entry.key}-${index}`"
          class="workflow-tool-param-row"
          :data-entry-key="entry.key"
        >
          <label class="drawer-field">
            <span>输入字段</span>
            <input
              :name="`subworkflow-input-key-${index}`"
              :value="entry.key"
              placeholder="例如 orderId"
              @input="renameMappingKey(index, ($event.target as HTMLInputElement).value)"
            />
          </label>
          <AppSelect
            class="workflow-advanced-input-select"
            :model-value="mappingRefValue(entry.key)"
            :options="variableOptions"
            placeholder="选择变量"
            @update:model-value="setRefMapping(entry.key, String($event))"
          />
          <button type="button" class="ghost-button" @click="removeMappingRow(entry.key)">删除字段</button>
        </article>
        <button type="button" class="ghost-button" @click="addMappingRow">添加字段</button>
      </div>

      <label v-else class="drawer-field">
        <span>JSON 输入</span>
        <textarea
          name="node-subworkflow-input-json"
          rows="6"
          :value="rawInputText"
          spellcheck="false"
          @input="updateRawInput(($event.target as HTMLTextAreaElement).value)"
        />
      </label>
    </section>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>运行语义</strong>
        <small>published workflow</small>
      </div>
      <p>当前运行时会执行已发布 workflow revision，记录 workflowId、解析后的输入和 completed 状态摘要。</p>
    </section>
  </section>
</template>
