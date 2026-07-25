<script setup lang="ts">
import { computed } from "vue";

import { unwrapWorkflowVariableRef } from "../../utils/workflow-graph";

const props = defineProps<{
  modelValue?: string;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: string): void;
}>();

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
      <strong>变量选择器</strong>
      <small>{{ options.length }} 个可用变量</small>
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
      <span v-if="!options.length" class="workflow-token muted">暂无可用变量</span>
    </div>
  </section>
</template>
