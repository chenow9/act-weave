<script setup lang="ts">
import { ref } from "vue";

import { useModalFocus } from "../composables/useModalFocus";
import ToolSchemaDualPane from "./ToolSchemaDualPane.vue";

import type { ToolSchemaNode } from "../types/domain";

const props = withDefaults(
  defineProps<{
    modelValue: ToolSchemaNode[];
    visible: boolean;
    title: string;
    description: string;
    rootLabel: string;
    embedded?: boolean;
  }>(),
  {
    embedded: false,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: ToolSchemaNode[]];
  "update:visible": [value: boolean];
}>();

const workbenchRef = ref<HTMLElement | null>(null);

useModalFocus({
  visible: () => props.visible,
  modalRef: workbenchRef,
  onClose: () => updateVisible(false),
});

function updateVisible(nextValue: boolean) {
  emit("update:visible", nextValue);
}
</script>

<template>
  <div
    v-if="props.visible"
    :class="props.embedded ? 'tool-contract-workbench-embedded' : 'modal-backdrop tool-contract-workbench-modal'"
    @click.self="!props.embedded && updateVisible(false)"
  >
    <section
      ref="workbenchRef"
      class="tool-contract-workbench"
      :role="props.embedded ? 'region' : 'dialog'"
      :aria-modal="props.embedded ? undefined : 'true'"
      :aria-label="title"
    >
      <div class="tool-contract-workbench-head">
        <div class="tool-contract-workbench-head-copy">
          <div class="tool-contract-workbench-head-title-line">
            <strong>{{ title }}</strong>
            <span class="tool-contract-workbench-head-badge">参数详情</span>
          </div>
        </div>
        <div class="tool-contract-workbench-actions">
          <button
            class="tool-contract-workbench-close"
            type="button"
            aria-label="关闭工作台"
            data-modal-initial-focus
            @click="updateVisible(false)"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </div>
      </div>

      <div class="tool-contract-workbench-body">
        <ToolSchemaDualPane
          :model-value="props.modelValue"
          :title="title"
          :description="description"
          :root-label="rootLabel"
        />
      </div>
    </section>
  </div>
</template>
