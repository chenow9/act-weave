<script setup lang="ts">
import type { WorkflowCompilationIssue } from "../../types/domain";

const props = defineProps<{
  issues: WorkflowCompilationIssue[];
  selectedNodeId: string;
  selectedEdgeId?: string;
  /** Show P4.3 CTA when compile issues present. */
  showReviseCta?: boolean;
}>();

const emit = defineEmits<{
  (event: "focus-node", nodeId: string): void;
  (event: "focus-edge", edgeId: string): void;
  (event: "revise-from-failure"): void;
}>();

function severityLabel(severity: string) {
  if (severity === "error") return "错误";
  if (severity === "warning") return "警告";
  return severity;
}

function focusIssue(issue: WorkflowCompilationIssue) {
  if (issue.edgeId) {
    emit("focus-edge", issue.edgeId);
    return;
  }
  if (issue.nodeId) {
    emit("focus-node", issue.nodeId);
  }
}
</script>

<template>
  <section class="workflow-issues-panel">
    <div class="workflow-section-caption">
      <strong>编译问题</strong>
      <small>{{ props.issues.length }} 条</small>
    </div>

    <button
      v-if="props.showReviseCta && props.issues.length"
      data-action="revise-from-compile-failure"
      class="workflow-revise-cta"
      type="button"
      title="回到智能编排，按问题修订出新 Draft（不自动发布）"
      @click="emit('revise-from-failure')"
    >
      按问题修订草稿
    </button>

    <div v-if="props.issues.length" class="workflow-issue-list">
      <button
        v-for="issue in props.issues"
        :key="`${issue.code}-${issue.nodeId || issue.edgeId || issue.fieldPath || issue.message}`"
        class="workflow-issue-item"
        :class="{
          active:
            (issue.edgeId && issue.edgeId === props.selectedEdgeId) ||
            (!issue.edgeId && issue.nodeId && issue.nodeId === props.selectedNodeId),
        }"
        type="button"
        @click="focusIssue(issue)"
      >
        <span class="status-pill" :class="issue.severity">{{ severityLabel(issue.severity) }}</span>
        <strong>{{ issue.message }}</strong>
        <small>{{ issue.nodeId || issue.edgeId || issue.sourceStage }}</small>
      </button>
    </div>

    <div v-else class="workflow-issues-empty">
      <i class="fa-solid fa-circle-check" />
      <span>当前草稿还没有返回编译问题。</span>
    </div>
  </section>
</template>

<style scoped>
.workflow-revise-cta {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  margin: 0 0 10px;
  padding: 8px 12px;
  border: 1px solid rgba(37, 99, 235, 0.28);
  border-radius: 10px;
  background: rgba(37, 99, 235, 0.08);
  color: #1d4ed8;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.workflow-revise-cta:hover {
  background: rgba(37, 99, 235, 0.14);
}
</style>
