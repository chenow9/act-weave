<script lang="ts">
import { tt } from "../../i18n/tt";

/** Branch option values shared with the canvas edge labels. */
export const WORKFLOW_BRANCH_VALUES = ["default", "true", "false", "success", "failure"] as const;

export function getWorkflowBranchOptions() {
  return [
    { label: tt("workflow.branchDefault"), value: "default" },
    { label: tt("workflow.branchTrue"), value: "true" },
    { label: tt("workflow.branchFalse"), value: "false" },
    { label: tt("workflow.branchSuccess"), value: "success" },
    { label: tt("workflow.branchFailure"), value: "failure" },
  ];
}

/** @deprecated Prefer getWorkflowBranchOptions() so labels follow the active locale. */
export const WORKFLOW_BRANCH_OPTIONS = getWorkflowBranchOptions();
</script>

<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../AppSelect.vue";
import type { WorkflowGraphEdge } from "../../types/domain";

const props = defineProps<{
  edge?: WorkflowGraphEdge;
}>();

const emit = defineEmits<{
  (event: "update-edge-data", payload: { key: string; value: unknown }): void;
}>();

const { t } = useI18n();

const branchSelectOptions = computed(() => [
  { label: t("workflow.branchNone"), value: "" },
  { label: t("workflow.branchDefault"), value: "default" },
  { label: t("workflow.branchTrue"), value: "true" },
  { label: t("workflow.branchFalse"), value: "false" },
  { label: t("workflow.branchSuccess"), value: "success" },
  { label: t("workflow.branchFailure"), value: "failure" },
]);

function edgeDataValue(key: string) {
  const value = props.edge?.data?.[key];
  return typeof value === "string" ? value : "";
}
</script>

<template>
  <section class="workflow-edge-inspector">
    <div class="workflow-panel-heading">
      <span>{{ t("workflow.edgePanel") }}</span>
      <h3>{{ props.edge?.id || t("workflow.selectEdge") }}</h3>
      <p>
        {{ props.edge ? t("workflow.edgeHintSelected") : t("workflow.edgeHintEmpty") }}
      </p>
    </div>

    <div v-if="props.edge" class="workflow-inspector-form">
      <div class="workflow-inspector-meta">
        <span>{{ t("workflow.edgeId", { id: props.edge.id }) }}</span>
        <span
          >{{ props.edge.sourceNodeId }}:{{ props.edge.sourcePort }} -> {{ props.edge.targetNodeId }}:{{
            props.edge.targetPort
          }}</span
        >
      </div>

      <section class="workflow-inspector-vars">
        <div class="workflow-section-caption">
          <strong>{{ t("workflow.branchLabel") }}</strong>
          <small>edge.data.branch</small>
        </div>
        <label class="drawer-field">
          <span>{{ t("workflow.branchLabel") }}</span>
          <AppSelect
            class="workflow-branch-select"
            :model-value="edgeDataValue('branch')"
            :options="branchSelectOptions"
            :placeholder="t('workflow.selectBranch')"
            @update:model-value="emit('update-edge-data', { key: 'branch', value: $event })"
          />
        </label>
      </section>
    </div>

    <div v-else class="workflow-inspector-empty">
      <i class="fa-solid fa-route" />
      <strong>{{ t("workflow.noEdgeSelected") }}</strong>
      <small>{{ t("workflow.noEdgeSelectedHint") }}</small>
    </div>
  </section>
</template>
