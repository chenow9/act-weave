<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import WorkflowSchemaBuilder from "../WorkflowSchemaBuilder.vue";
import {
  buildWorkflowObjectSchema,
  parseWorkflowObjectSchema,
  type WorkflowSchemaFieldDraft,
} from "../../../utils/workflow-graph";
import type { WorkflowGraphNode } from "../../../types/domain";

const { t } = useI18n();

const props = defineProps<{
  node: WorkflowGraphNode;
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const fields = ref<WorkflowSchemaFieldDraft[]>([]);
const mode = ref<"builder" | "raw">("builder");
const rawJson = ref("");
const rawJsonError = ref("");
const lastSyncedSchemaJson = ref("");

const schemaPreview = computed(() => buildWorkflowObjectSchema(fields.value));

watch(
  () => props.node.data.inputSchema,
  (value) => {
    const parsedFields = parseWorkflowObjectSchema(value);
    const normalizedSchema = buildWorkflowObjectSchema(parsedFields);
    lastSyncedSchemaJson.value = JSON.stringify(normalizedSchema);
    fields.value = parsedFields;
    rawJson.value = JSON.stringify(normalizedSchema, null, 2);
    rawJsonError.value = "";
  },
  { immediate: true },
);

watch(
  fields,
  (value) => {
    const schema = buildWorkflowObjectSchema(value);
    const schemaJson = JSON.stringify(schema);
    rawJson.value = JSON.stringify(schema, null, 2);
    if (schemaJson === lastSyncedSchemaJson.value) {
      return;
    }
    lastSyncedSchemaJson.value = schemaJson;
    emit("update-node-data", { key: "inputSchema", value: schema });
  },
  { deep: true },
);

function updateRawJson(value: string) {
  rawJson.value = value;
  rawJsonError.value = "";
  try {
    const parsed = JSON.parse(value);
    fields.value = parseWorkflowObjectSchema(parsed);
  } catch {
    rawJsonError.value = t("workflow.invalidJson");
  }
}
</script>

<template>
  <section class="workflow-start-node-editor">
    <div class="workflow-schema-mode-switch">
      <button type="button" :class="{ active: mode === 'builder' }" @click="mode = 'builder'">
        {{ t("workflow.visualBuilder") }}
      </button>
      <button type="button" :class="{ active: mode === 'raw' }" data-mode="raw-schema" @click="mode = 'raw'">
        JSON
      </button>
    </div>

    <WorkflowSchemaBuilder v-if="mode === 'builder'" :fields="fields" @update:fields="fields = $event" />

    <label v-else class="drawer-field">
      <span>Raw JSON</span>
      <textarea
        name="workflow-schema-raw-json"
        rows="8"
        :value="rawJson"
        spellcheck="false"
        @input="updateRawJson(($event.target as HTMLTextAreaElement).value)"
      />
      <small v-if="rawJsonError" class="workflow-trial-run-error">{{ rawJsonError }}</small>
    </label>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.schemaPreview") }}</strong>
        <small>{{ t("workflow.fieldCount", { n: fields.length }) }}</small>
      </div>
      <pre>{{ JSON.stringify(schemaPreview, null, 2) }}</pre>
    </section>
  </section>
</template>
