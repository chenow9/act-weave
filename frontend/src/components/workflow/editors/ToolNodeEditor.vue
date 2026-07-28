<script setup lang="ts">
import { computed } from "vue";

import AppSelect from "../../AppSelect.vue";
import type { Tool, ToolRequestParam, ToolResponseField, WorkflowGraphNode } from "../../../types/domain";

type MappingValue = { kind: "ref"; path: string } | { kind: "literal"; value: unknown };

const props = defineProps<{
  node: WorkflowGraphNode;
  tools: Tool[];
  toolOptions?: Array<{ label: string; value: string }>;
  variableRefs: string[];
  toolCatalogError?: string;
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const selectedToolId = computed(() => (typeof props.node.data.toolId === "string" ? props.node.data.toolId : ""));
const publishedTools = computed(() => props.tools.filter((tool) => tool.status === "Published"));
const selectedTool = computed(() => publishedTools.value.find((tool) => tool.id === selectedToolId.value));
const toolSelectOptions = computed(() => {
  if (props.toolOptions?.length) {
    const publishedIds = new Set(publishedTools.value.map((tool) => tool.id));
    return props.toolOptions.filter((option) => publishedIds.has(option.value));
  }
  return publishedTools.value.map((tool) => ({ label: `${tool.name} · ${tool.id}`, value: tool.id }));
});
const hasPublishedTools = computed(() => toolSelectOptions.value.length > 0);
const requiredUserParams = computed(() =>
  (selectedTool.value?.requestParams || []).filter((param) => param.required && param.valueSource !== "SystemDefault"),
);
const variableOptions = computed(() =>
  props.variableRefs
    .map((ref) => unwrapVariableRef(ref))
    .filter(Boolean)
    .map((ref) => ({ label: ref, value: ref })),
);
const outputSchema = computed(() => responseFieldsToOutputSchema(selectedTool.value?.responseFields || []));

function currentInputMapping() {
  const mapping = props.node.data.inputMapping;
  return mapping && typeof mapping === "object" && !Array.isArray(mapping)
    ? { ...(mapping as Record<string, unknown>) }
    : {};
}

function mappingFor(paramName: string): MappingValue | undefined {
  const value = currentInputMapping()[paramName];
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  const record = value as Record<string, unknown>;
  if (record.kind === "ref" && typeof record.path === "string") {
    return { kind: "ref", path: record.path };
  }
  if (record.kind === "literal") {
    return { kind: "literal", value: record.value };
  }
  return undefined;
}

function mappingKind(paramName: string) {
  return mappingFor(paramName)?.kind || "ref";
}

function mappingRefPath(paramName: string) {
  const mapping = mappingFor(paramName);
  return mapping?.kind === "ref" ? mapping.path : "";
}

function mappingLiteralValue(paramName: string) {
  const mapping = mappingFor(paramName);
  const value = mapping?.kind === "literal" ? mapping.value : "";
  return typeof value === "string" ? value : value == null ? "" : JSON.stringify(value);
}

function updateTool(toolId: string) {
  const tool = publishedTools.value.find((item) => item.id === toolId);
  emit("update-node-data", {
    key: "__merge",
    value: {
      toolId,
      inputMapping: {},
      outputSchema: responseFieldsToOutputSchema(tool?.responseFields || []),
    },
  });
}

function setMappingKind(paramName: string, kind: MappingValue["kind"]) {
  const next = currentInputMapping();
  if (kind === "ref") {
    delete next[paramName];
  } else {
    next[paramName] = { kind: "literal", value: "" };
  }
  emitToolData(next);
}

function setRefMapping(paramName: string, path: string) {
  const next = currentInputMapping();
  next[paramName] = { kind: "ref", path };
  emitToolData(next);
}

function setLiteralMapping(paramName: string, value: string) {
  const next = currentInputMapping();
  next[paramName] = { kind: "literal", value };
  emitToolData(next);
}

function emitToolData(inputMapping: Record<string, unknown>) {
  emit("update-node-data", {
    key: "__merge",
    value: {
      inputMapping,
      outputSchema: responseFieldsToOutputSchema(selectedTool.value?.responseFields || []),
    },
  });
}

function unwrapVariableRef(value: string) {
  return value.replace(/^\{\{/, "").replace(/\}\}$/, "");
}

function responseFieldsToOutputSchema(fields: ToolResponseField[]) {
  return {
    type: "object",
    properties: Object.fromEntries(
      fields.map((field) => [
        field.name,
        {
          type: field.type || field.schema?.type || "unknown",
          description: field.description || field.name,
        },
      ]),
    ),
  };
}

function paramSummary(param: ToolRequestParam) {
  return [param.type || "unknown", param.location].filter(Boolean).join(" · ");
}
</script>

<template>
  <section class="workflow-tool-node-editor">
    <label class="drawer-field">
      <span>工具</span>
      <AppSelect
        class="workflow-tool-select"
        :model-value="selectedToolId"
        :options="toolSelectOptions"
        placeholder="选择已发布工具"
        @update:model-value="updateTool(String($event))"
      />
    </label>

    <div v-if="!hasPublishedTools" class="workflow-inspector-empty compact">
      <strong>还没有可绑定的已发布工具</strong>
      <small>当前 Agent 还没有 Published Tool。先去工具管理发布一个可调用工具，再回到这里完成绑定。</small>
    </div>

    <div v-if="props.toolCatalogError" class="workflow-tool-catalog-error" role="status">
      <strong>工具目录加载失败</strong>
      <small>{{ props.toolCatalogError }}</small>
    </div>

    <section class="workflow-inspector-vars workflow-tool-required-params">
      <div class="workflow-section-caption">
        <strong>必填参数</strong>
        <small>{{ requiredUserParams.length }} 个用户输入</small>
      </div>

      <div v-if="requiredUserParams.length" class="workflow-tool-param-list">
        <article
          v-for="param in requiredUserParams"
          :key="param.name"
          class="workflow-tool-param-row"
          :data-param-name="param.name"
        >
          <div class="workflow-tool-param-head">
            <strong>{{ param.name }}</strong>
            <small>{{ paramSummary(param) }}</small>
          </div>
          <p v-if="param.description">{{ param.description }}</p>
          <div class="workflow-tool-mapping-mode" role="group" aria-label="参数映射方式">
            <button
              type="button"
              data-action="mapping-kind-ref"
              :class="{ active: mappingKind(param.name) === 'ref' }"
              @click="setMappingKind(param.name, 'ref')"
            >
              变量
            </button>
            <button
              type="button"
              data-action="mapping-kind-literal"
              :class="{ active: mappingKind(param.name) === 'literal' }"
              @click="setMappingKind(param.name, 'literal')"
            >
              固定值
            </button>
          </div>
          <AppSelect
            v-if="mappingKind(param.name) === 'ref'"
            class="workflow-tool-variable-select"
            :model-value="mappingRefPath(param.name)"
            :options="variableOptions"
            placeholder="选择变量"
            @update:model-value="setRefMapping(param.name, String($event))"
          />
          <input
            v-else
            :name="`tool-param-${param.name}-literal`"
            :value="mappingLiteralValue(param.name)"
            placeholder="输入固定值"
            @input="setLiteralMapping(param.name, ($event.target as HTMLInputElement).value)"
          />
        </article>
      </div>
      <div v-else class="workflow-inspector-empty compact">
        <small>当前工具没有需要用户提供的必填参数。</small>
      </div>
    </section>

    <section class="workflow-inspector-vars workflow-tool-output-preview">
      <div class="workflow-section-caption">
        <strong>输出 Schema</strong>
        <small>{{ selectedTool?.responseFields.length || 0 }} 个字段</small>
      </div>
      <div v-if="selectedTool?.responseFields.length" class="workflow-tool-schema-list">
        <span v-for="field in selectedTool.responseFields" :key="field.name" class="workflow-token">
          {{ field.name }} · {{ field.type }}
        </span>
      </div>
      <pre>{{ JSON.stringify(outputSchema, null, 2) }}</pre>
    </section>
  </section>
</template>
