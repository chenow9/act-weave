<script setup lang="ts">
import type { WorkflowGraphNode } from "../../../types/domain";

const props = defineProps<{
  node: WorkflowGraphNode;
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

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
      <span>分支列表</span>
      <input
        name="node-parallel-branches"
        :value="branchesValue()"
        placeholder="例如 risk-check, inventory-sync"
        @input="updateBranches(($event.target as HTMLInputElement).value)"
      />
    </label>

    <section class="workflow-inspector-vars">
      <div class="workflow-section-caption">
        <strong>当前分支</strong>
        <small>{{ Array.isArray(node.data.branches) ? node.data.branches.length : 0 }} 个</small>
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
        <strong>运行语义</strong>
        <small>parallel branch summary</small>
      </div>
      <p>当前运行时会记录 branches 数组，并按分支顺序写入 trace 与节点输出摘要，不会静默返回占位成功。</p>
    </section>
  </section>
</template>
