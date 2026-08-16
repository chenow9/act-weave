<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import type { WorkflowGraphNodeType } from "../../types/domain";
import { workflowNodeLibrary } from "./workflow-node-visual";

const props = defineProps<{
  variableRefs: string[];
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (event: "add-node", nodeType: WorkflowGraphNodeType): void;
}>();

const { t } = useI18n();
const emptyVariableRef = "{{input.orderId}}";

function addNode(nodeType: WorkflowGraphNodeType) {
  if (props.disabled) {
    return;
  }
  emit("add-node", nodeType);
}

const nodeLibrary = computed(() =>
  workflowNodeLibrary().map((item) => ({
    type: item.type,
    icon: item.icon,
    accent: item.accent,
    title: t(item.labelKey),
    description: t(item.descKey),
  })),
);
</script>

<template>
  <aside class="workflow-node-palette" :aria-disabled="props.disabled ? 'true' : undefined">
    <div class="workflow-panel-heading">
      <span>{{ t("workflow.nodeLibrary") }}</span>
      <h3>Workflow Blocks</h3>
      <p>{{ t("workflow.nodeLibraryHint") }}</p>
    </div>

    <div class="workflow-node-library">
      <button
        v-for="node in nodeLibrary"
        :key="node.type"
        class="workflow-node-library-item"
        type="button"
        :disabled="props.disabled"
        @click="addNode(node.type)"
      >
        <i :class="node.icon" :style="{ color: node.accent, background: `${node.accent}1f` }" />
        <span>
          <strong>{{ node.title }}</strong>
          <small>{{ node.description }}</small>
        </span>
      </button>
    </div>

    <section class="workflow-variable-library">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.variableRefs") }}</strong>
        <small>{{ t("workflow.variableRefsHint") }}</small>
      </div>
      <div class="workflow-token-list">
        <span v-for="variable in props.variableRefs" :key="variable" class="workflow-token">{{ variable }}</span>
        <span v-if="!props.variableRefs.length" class="workflow-token muted">{{ emptyVariableRef }}</span>
      </div>
    </section>
  </aside>
</template>
