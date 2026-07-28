<script setup lang="ts">
import { computed } from "vue";

import AppSelect from "../AppSelect.vue";
import type { WorkflowSchemaFieldDraft } from "../../utils/workflow-graph";

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
      <strong>输入 Schema</strong>
      <small>{{ props.fields.length }} 个字段</small>
    </div>

    <div class="workflow-schema-field-list">
      <article v-for="(field, index) in props.fields" :key="`${field.key}-${index}`" class="workflow-schema-field-card">
        <div class="workflow-schema-field-card-header">
          <strong>字段 {{ index + 1 }}</strong>
          <button
            class="ghost-button workflow-schema-remove"
            type="button"
            :data-action="`remove-schema-field-${index}`"
            @click="removeField(index)"
          >
            删除
          </button>
        </div>

        <label class="drawer-field">
          <span>字段 Key</span>
          <input
            :name="`schema-field-key-${index}`"
            :value="field.key"
            @input="updateField(index, { key: ($event.target as HTMLInputElement).value })"
          />
        </label>

        <label class="drawer-field">
          <span>字段类型</span>
          <AppSelect
            :name="`schema-field-type-${index}`"
            :model-value="field.type"
            :options="fieldTypeOptions"
            :aria-label="`字段 ${index + 1} 类型`"
            @update:model-value="updateField(index, { type: String($event) })"
          />
        </label>

        <label class="drawer-field">
          <span>字段描述</span>
          <input
            :name="`schema-field-description-${index}`"
            :value="field.description"
            @input="updateField(index, { description: ($event.target as HTMLInputElement).value })"
          />
        </label>

        <label class="drawer-field">
          <span>枚举值</span>
          <input
            :name="`schema-field-enum-${index}`"
            :value="field.enumValues.join(',')"
            placeholder="可选，逗号分隔"
            @input="updateEnumValues(index, ($event.target as HTMLInputElement).value)"
          />
        </label>

        <label class="drawer-field">
          <span>示例值</span>
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
          <span>必填字段</span>
        </label>
      </article>
    </div>

    <button class="primary-button full" data-action="add-schema-field" type="button" @click="addField">
      添加字段 {{ nextIndex + 1 }}
    </button>
  </section>
</template>
