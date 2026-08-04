<script setup lang="ts">
import { useI18n } from "vue-i18n";

import type { WorkflowGraphNode } from "../../../types/domain";

const props = defineProps<{
  node: WorkflowGraphNode;
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const { t } = useI18n();

function branchesValue() {
  const branches = props.node.data.branches;
  if (Array.isArray(branches)) {
    return branches.map((value) => String(value)).join(", ");
  }
  return "";
}

function updateBranches(value: string) {
  const branches = value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
  emit("update-node-data", { key: "branches", value: branches });
}
</script>

<template>
  <section class="workflow-parallel-node-editor">
    <label class="drawer-field">
      <span>{{ t("workflow.branchList") }}</span>
      <input
        name="node-parallel-branches"
        :value="branchesValue()"
        :placeholder="t('workflow.branchListPh')"
        @input="updateBranches(($event.target as HTMLInputElement).value)"
      />
    </label>

    <section class="workflow-inspector-vars">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.currentBranches") }}</strong>
        <small>{{
          t("workflow.branchCount", { n: Array.isArray(node.data.branches) ? node.data.branches.length : 0 })
        }}</small>
      </div>
      <div class="workflow-token-list">
        <span
          v-for="branch in Array.isArray(node.data.branches) ? node.data.branches : []"
          :key="String(branch)"
          class="workflow-token"
        >
          {{ String(branch) }}
        </span>
      </div>
    </section>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.runtimeSemantics") }}</strong>
        <small>parallel branch summary</small>
      </div>
      <p>{{ t("workflow.parallelRuntimeHint") }}</p>
    </section>
  </section>
</template>
