<script setup lang="ts">
import WorkflowVariablePicker from "../WorkflowVariablePicker.vue";
import type { WorkflowGraphNode } from "../../../types/domain";

const props = defineProps<{
  node: WorkflowGraphNode;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

function currentPath() {
  const output = props.node.data.output;
  if (!output || typeof output !== "object" || Array.isArray(output)) {
    return "";
  }
  const path = (output as Record<string, unknown>).path;
  return typeof path === "string" ? path : "";
}

function updatePath(path: string) {
  emit("update-node-data", {
    key: "output",
    value: {
      kind: "ref",
      path,
    },
  });
}
</script>

<template>
  <section class="workflow-end-node-editor">
    <WorkflowVariablePicker class="workflow-end-node-variable-picker" :model-value="currentPath()" :variable-refs="variableRefs" @update:model-value="updatePath" />

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>输出映射</strong>
        <small>结构化 ref</small>
      </div>
      <pre>{{ JSON.stringify(node.data.output || { kind: 'ref', path: '' }, null, 2) }}</pre>
    </section>
  </section>
</template>
