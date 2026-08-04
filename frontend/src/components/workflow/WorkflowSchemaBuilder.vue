<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../AppSelect.vue";
import type { WorkflowSchemaFieldDraft } from "../../utils/workflow-graph";

const { t } = useI18n();

const props = defineProps<{
  fields: WorkflowSchemaFieldDraft[];
}>();

const emit = defineEmits<{
  (event: "update:fields", value: WorkflowSchemaFieldDraft[]): void;
}>();

const fieldTypeOptions = ["string", "integer", "number", "boolean", "object", "array"].map((type) => ({
  label: type,
  value: type,
}));

const nextIndex = computed(() => props.fields.length);

function updateField(index: number, patch: Partial<WorkflowSchemaFieldDraft>) {
  emit(
    "update:fields",
    props.fields.map((field, currentIndex) => (currentIndex === index ? { ...field, ...patch } : field)),
  );
}

function addField() {
  emit("update:fields", [
    ...props.fields,
    {
      key: "",
      type: "string",
      required: false,
      description: "",
      enumValues: [],
      example: "",
    },
  ]);
}

function removeField(index: number) {
  emit(
    "update:fields",
    props.fields.filter((_, currentIndex) => currentIndex !== index),
  );
}

function updateEnumValues(index: number, value: string) {
  updateField(index, {
    enumValues: value
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
  });
}
</script>

<template>
  <section class="workflow-schema-builder">
    <div class="workflow-section-caption">
      <strong>{{ t("workflow.inputSchema") }}</strong>
      <small>{{ t("workflow.fieldCount", { n: props.fields.length }) }}</small>
    </div>

    <div class="workflow-schema-field-list">
      <article v-for="(field, index) in props.fields" :key="`${field.key}-${index}`" class="workflow-schema-field-card">
        <div class="workflow-schema-field-card-header">
          <strong>{{ t("workflow.fieldN", { n: index + 1 }) }}</strong>
          <button
            class="ghost-button workflow-schema-remove"
            type="button"
            :data-action="`remove-schema-field-${index}`"
            @click="removeField(index)"
          >
            {{ t("workflow.deleteField") }}
          </button>
        </div>

        <label class="drawer-field">
          <span>{{ t("workflow.fieldKey") }}</span>
          <input
            :name="`schema-field-key-${index}`"
            :value="field.key"
            @input="updateField(index, { key: ($event.target as HTMLInputElement).value })"
          />
        </label>

        <label class="drawer-field">
          <span>{{ t("workflow.fieldType") }}</span>
          <AppSelect
            :name="`schema-field-type-${index}`"
            :model-value="field.type"
            :options="fieldTypeOptions"
            :aria-label="t('workflow.fieldTypeAria', { n: index + 1 })"
            @update:model-value="updateField(index, { type: String($event) })"
          />
        </label>

        <label class="drawer-field">
          <span>{{ t("workflow.fieldDescription") }}</span>
          <input
            :name="`schema-field-description-${index}`"
            :value="field.description"
            @input="updateField(index, { description: ($event.target as HTMLInputElement).value })"
          />
        </label>

        <label class="drawer-field">
          <span>{{ t("workflow.enumValues") }}</span>
          <input
            :name="`schema-field-enum-${index}`"
            :value="field.enumValues.join(',')"
            :placeholder="t('workflow.enumPh')"
            @input="updateEnumValues(index, ($event.target as HTMLInputElement).value)"
          />
        </label>

        <label class="drawer-field">
          <span>{{ t("workflow.exampleValue") }}</span>
          <input
            :name="`schema-field-example-${index}`"
            :value="field.example"
            @input="updateField(index, { example: ($event.target as HTMLInputElement).value })"
          />
        </label>

        <label class="workflow-schema-required-toggle">
          <input
            :name="`schema-field-required-${index}`"
            :checked="field.required"
            type="checkbox"
            @change="updateField(index, { required: ($event.target as HTMLInputElement).checked })"
          />
          <span>{{ t("workflow.requiredField") }}</span>
        </label>
      </article>
    </div>

    <button class="primary-button full" data-action="add-schema-field" type="button" @click="addField">
      {{ t("workflow.addField", { n: nextIndex + 1 }) }}
    </button>
  </section>
</template>
