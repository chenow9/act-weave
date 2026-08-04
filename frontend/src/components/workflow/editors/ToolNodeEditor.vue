<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../../AppSelect.vue";
import type { Tool, ToolRequestParam, ToolResponseField, WorkflowGraphNode } from "../../../types/domain";

const { t } = useI18n();

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

const selectedToolId = computed(() => {
  const raw = props.node.data?.toolId;
  return typeof raw === "string" ? raw.trim() : "";
});
const publishedTools = computed(() => props.tools.filter((tool) => tool.status === "Published"));
const selectedTool = computed(
  () =>
    props.tools.find((tool) => tool.id === selectedToolId.value) ||
    publishedTools.value.find((tool) => tool.id === selectedToolId.value),
);
const toolSelectOptions = computed(() => {
  const byId = new Map<string, { label: string; value: string }>();

  const addOption = (value: string, label?: string) => {
    const id = value?.trim();
    if (!id) return;
    const text = (label || "").trim() || id;
    // Prefer a human name if we already stored a bare id as label.
    const existing = byId.get(id);
    if (!existing || existing.label === id) {
      byId.set(id, { label: text, value: id });
    }
  };

  for (const tool of publishedTools.value) {
    addOption(tool.id, tool.name);
  }
  if (props.toolOptions?.length) {
    const publishedIds = new Set(publishedTools.value.map((tool) => tool.id));
    for (const option of props.toolOptions) {
      // Keep published catalog options; also keep the currently bound tool.
      if (publishedIds.has(option.value) || option.value === selectedToolId.value) {
        addOption(option.value, option.label);
      }
    }
  }
  // Always surface the currently bound tool so el-select can resolve a label.
  if (selectedToolId.value) {
    addOption(selectedToolId.value, selectedTool.value?.name);
  }

  return [...byId.values()].sort((a, b) => a.label.localeCompare(b.label, "zh-CN"));
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
      <span>{{ t("workflow.tool") }}</span>
      <AppSelect
        class="workflow-tool-select"
        :model-value="selectedToolId"
        :options="toolSelectOptions"
        :placeholder="t('workflow.selectPublishedTool')"
        filterable
        :fit-input-width="true"
        placement="bottom-start"
        :aria-label="t('workflow.selectPublishedTool')"
        @update:model-value="updateTool(String($event || ''))"
      />
    </label>

    <div v-if="!hasPublishedTools" class="workflow-inspector-empty compact">
      <strong>{{ t("workflow.noPublishedTools") }}</strong>
      <small>{{ t("workflow.noPublishedToolsHint") }}</small>
    </div>

    <div v-if="props.toolCatalogError" class="workflow-tool-catalog-error" role="status">
      <strong>{{ t("workflow.toolCatalogLoadFailedTitle") }}</strong>
      <small>{{ props.toolCatalogError }}</small>
    </div>

    <section class="workflow-inspector-vars workflow-tool-required-params">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.requiredParams") }}</strong>
        <small>{{ t("workflow.userInputCount", { n: requiredUserParams.length }) }}</small>
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
          <div class="workflow-tool-mapping-mode" role="group" :aria-label="t('workflow.paramMappingModeAria')">
            <button
              type="button"
              data-action="mapping-kind-ref"
              :class="{ active: mappingKind(param.name) === 'ref' }"
              @click="setMappingKind(param.name, 'ref')"
            >
              {{ t("workflow.mapVariable") }}
            </button>
            <button
              type="button"
              data-action="mapping-kind-literal"
              :class="{ active: mappingKind(param.name) === 'literal' }"
              @click="setMappingKind(param.name, 'literal')"
            >
              {{ t("workflow.mapLiteral") }}
            </button>
          </div>
          <AppSelect
            v-if="mappingKind(param.name) === 'ref'"
            class="workflow-tool-variable-select"
            :model-value="mappingRefPath(param.name)"
            :options="variableOptions"
            :placeholder="t('workflow.selectVariable')"
            @update:model-value="setRefMapping(param.name, String($event))"
          />
          <input
            v-else
            :name="`tool-param-${param.name}-literal`"
            :value="mappingLiteralValue(param.name)"
            :placeholder="t('workflow.enterLiteral')"
            @input="setLiteralMapping(param.name, ($event.target as HTMLInputElement).value)"
          />
        </article>
      </div>
      <div v-else class="workflow-inspector-empty compact">
        <small>{{ t("workflow.noRequiredParams") }}</small>
      </div>
    </section>

    <section class="workflow-inspector-vars workflow-tool-output-preview">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.outputSchema") }}</strong>
        <small>{{ t("workflow.fieldCount", { n: selectedTool?.responseFields.length || 0 }) }}</small>
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
