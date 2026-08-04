<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import { unwrapWorkflowVariableRef } from "../../utils/workflow-graph";

const props = defineProps<{
  modelValue?: string;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: string): void;
}>();

const { t } = useI18n();

const options = computed(() =>
  props.variableRefs
    .map((value) => unwrapWorkflowVariableRef(value))
    .filter(Boolean)
    .map((value) => ({
      label: value,
      value,
      active: value === props.modelValue,
    })),
);
</script>

<template>
  <section class="workflow-variable-picker">
    <div class="workflow-section-caption">
      <strong>{{ t("workflow.variablePicker") }}</strong>
      <small>{{ t("workflow.availableVarCount", { n: options.length }) }}</small>
    </div>
    <div class="workflow-variable-picker-list">
      <button
        v-for="option in options"
        :key="option.value"
        class="workflow-token workflow-variable-picker-option"
        :class="{ active: option.active }"
        :data-value="option.value"
        type="button"
        @click="emit('update:modelValue', option.value)"
      >
        {{ option.label }}
      </button>
      <span v-if="!options.length" class="workflow-token muted">{{ t("workflow.noAvailableVars") }}</span>
    </div>
  </section>
</template>
