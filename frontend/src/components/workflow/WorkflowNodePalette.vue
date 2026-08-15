<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import type { WorkflowGraphNodeType } from "../../types/domain";

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

const nodeLibrary = computed(() => [
  {
    type: "Start" as const,
    icon: "fa-solid fa-play",
    title: t("workflow.nodeStart"),
    description: t("workflow.nodeStartDesc"),
  },
  {
    type: "Tool" as const,
    icon: "fa-solid fa-plug",
    title: t("workflow.nodeTool"),
    description: t("workflow.nodeToolDesc"),
  },
  {
    type: "Condition" as const,
    icon: "fa-solid fa-code-branch",
    title: t("workflow.nodeCondition"),
    description: t("workflow.nodeConditionDesc"),
  },
  {
    type: "SubWorkflow" as const,
    icon: "fa-solid fa-diagram-project",
    title: t("workflow.nodeSubWorkflow"),
    description: t("workflow.nodeSubWorkflowDesc"),
  },
  {
    type: "Transform" as const,
    icon: "fa-solid fa-shuffle",
    title: t("workflow.nodeTransform"),
    description: t("workflow.nodeTransformDesc"),
  },
  {
    type: "Parallel" as const,
    icon: "fa-solid fa-grip-lines-vertical",
    title: t("workflow.nodeParallel"),
    description: t("workflow.nodeParallelDesc"),
  },
  {
    type: "ForEach" as const,
    icon: "fa-solid fa-repeat",
    title: t("workflow.nodeForEach"),
    description: t("workflow.nodeForEachDesc"),
  },
  {
    type: "Approval" as const,
    icon: "fa-solid fa-clipboard-check",
    title: t("workflow.nodeApproval"),
    description: t("workflow.nodeApprovalDesc"),
  },
  {
    type: "End" as const,
    icon: "fa-solid fa-flag-checkered",
    title: t("workflow.nodeEnd"),
    description: t("workflow.nodeEndDesc"),
  },
]);
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
        <i :class="node.icon" />
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
