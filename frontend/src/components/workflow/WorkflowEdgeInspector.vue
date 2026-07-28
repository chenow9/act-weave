<script lang="ts">
export const WORKFLOW_BRANCH_OPTIONS = [
  { label: "默认分支", value: "default" },
  { label: "条件成立", value: "true" },
  { label: "条件不成立", value: "false" },
  { label: "成功", value: "success" },
  { label: "失败", value: "failure" },
];

const WORKFLOW_BRANCH_SELECT_OPTIONS = [{ label: "无分支标签", value: "" }, ...WORKFLOW_BRANCH_OPTIONS];
</script>

<script setup lang="ts">
import AppSelect from "../AppSelect.vue";
import type { WorkflowGraphEdge } from "../../types/domain";

const props = defineProps<{
  edge?: WorkflowGraphEdge;
}>();

const emit = defineEmits<{
  (event: "update-edge-data", payload: { key: string; value: unknown }): void;
}>();

function edgeDataValue(key: string) {
  const value = props.edge?.data?.[key];
  return typeof value === "string" ? value : "";
}
</script>

<template>
  <section class="workflow-edge-inspector">
    <div class="workflow-panel-heading">
      <span>连线面板</span>
      <h3>{{ props.edge?.id || "选择一条连线" }}</h3>
      <p>
        {{ props.edge ? "配置连线分支后，画布标签和运行时路由会使用同一个值。" : "点击画布中的连线后在这里编辑。" }}
      </p>
    </div>

    <div v-if="props.edge" class="workflow-inspector-form">
      <div class="workflow-inspector-meta">
        <span>连线 ID {{ props.edge.id }}</span>
        <span
          >{{ props.edge.sourceNodeId }}:{{ props.edge.sourcePort }} -> {{ props.edge.targetNodeId }}:{{
            props.edge.targetPort
          }}</span
        >
      </div>

      <section class="workflow-inspector-vars">
        <div class="workflow-section-caption">
          <strong>分支标签</strong>
          <small>edge.data.branch</small>
        </div>
        <label class="drawer-field">
          <span>分支标签</span>
          <AppSelect
            class="workflow-branch-select"
            :model-value="edgeDataValue('branch')"
            :options="WORKFLOW_BRANCH_SELECT_OPTIONS"
            placeholder="选择分支"
            @update:model-value="emit('update-edge-data', { key: 'branch', value: $event })"
          />
        </label>
      </section>
    </div>

    <div v-else class="workflow-inspector-empty">
      <i class="fa-solid fa-route" />
      <strong>还没有选中连线</strong>
      <small>点击画布中的连线后，在这里配置分支标签。</small>
    </div>
  </section>
</template>
