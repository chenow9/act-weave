<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import type { WorkflowRevisionDiff } from "../../types/domain";

const props = defineProps<{
  diff?: WorkflowRevisionDiff;
  emptyText?: string;
}>();

const { t, locale } = useI18n();

const nodeChanges = computed(() => props.diff?.nodeChanges ?? []);
const edgeChanges = computed(() => props.diff?.edgeChanges ?? []);
const snapshotChanges = computed(() => {
  const changes = props.diff?.changes;
  if (!changes) return [];
  return [
    { key: "draft", label: t("workflow.snapshotDraft"), changed: changes.draft },
    { key: "spec", label: t("workflow.snapshotSpec"), changed: changes.spec },
    { key: "plan", label: t("workflow.snapshotPlan"), changed: changes.plan },
    { key: "planHash", label: t("workflow.snapshotPlanHash"), changed: changes.planHash },
  ];
});

function formatDate(value?: string) {
  if (!value) return t("workflow.notCompared");
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return value;
  return new Intl.DateTimeFormat(locale.value === "zh-CN" ? "zh-CN" : "en", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(timestamp));
}

function statusClass(changeType?: string) {
  return (changeType || "unknown").toLowerCase();
}

function nodeChangeSummary(change: WorkflowRevisionDiff["nodeChanges"][number]) {
  if (change.changeType === "TypeChanged") {
    return `${change.leftType || "-"} -> ${change.rightType || "-"}`;
  }
  if (change.changeType === "DataChanged") {
    return t("workflow.nodeConfigChanged");
  }
  return change.rightType || change.leftType || t("workflow.nodeGeneric");
}

function edgeChangeSummary(change: WorkflowRevisionDiff["edgeChanges"][number]) {
  if (change.changeType === "BranchChanged") {
    return `${change.leftBranch || "default"} -> ${change.rightBranch || "default"}`;
  }
  return `${change.sourceNodeId || "-"} -> ${change.targetNodeId || "-"}`;
}
</script>

<template>
  <section class="workflow-revision-diff">
    <header class="workflow-revision-diff-head">
      <div>
        <span>{{ t("workflow.revisionDiff") }}</span>
        <strong v-if="diff">{{ diff.leftRevisionId }} -> {{ diff.rightRevisionId }}</strong>
        <strong v-else>{{ t("workflow.noDiffSelected") }}</strong>
      </div>
      <small>{{ formatDate(diff?.comparedAt) }}</small>
    </header>

    <div v-if="diff" class="workflow-revision-diff-body">
      <div v-if="snapshotChanges.length" class="workflow-revision-diff-group">
        <h4>{{ t("workflow.immutableSnapshotChanges") }}</h4>
        <div class="workflow-revision-diff-list">
          <article v-for="change in snapshotChanges" :key="change.key" class="workflow-revision-diff-item">
            <span class="status-pill" :class="change.changed ? 'datachanged' : 'unchanged'">{{
              change.changed ? "Changed" : "Unchanged"
            }}</span>
            <span>
              <strong>{{ change.label }}</strong>
              <small>{{ change.changed ? t("workflow.snapshotDiffers") : t("workflow.snapshotSame") }}</small>
            </span>
          </article>
        </div>
      </div>

      <template v-else>
        <div class="workflow-revision-diff-group">
          <h4>{{ t("workflow.nodeChanges") }}</h4>
          <div v-if="nodeChanges.length" class="workflow-revision-diff-list">
            <article
              v-for="change in nodeChanges"
              :key="`${change.nodeId}-${change.changeType}`"
              class="workflow-revision-diff-item"
            >
              <span class="status-pill" :class="statusClass(change.changeType)">{{ change.changeType }}</span>
              <span>
                <strong>{{ change.nodeId }}</strong>
                <small>{{ nodeChangeSummary(change) }}</small>
              </span>
            </article>
          </div>
          <p v-else>{{ t("workflow.noNodeChanges") }}</p>
        </div>

        <div class="workflow-revision-diff-group">
          <h4>{{ t("workflow.edgeChanges") }}</h4>
          <div v-if="edgeChanges.length" class="workflow-revision-diff-list">
            <article
              v-for="change in edgeChanges"
              :key="`${change.edgeId}-${change.changeType}`"
              class="workflow-revision-diff-item"
            >
              <span class="status-pill" :class="statusClass(change.changeType)">{{ change.changeType }}</span>
              <span>
                <strong>{{ change.edgeId }}</strong>
                <small>{{ edgeChangeSummary(change) }}</small>
              </span>
            </article>
          </div>
          <p v-else>{{ t("workflow.noEdgeChanges") }}</p>
        </div>
      </template>
    </div>

    <p v-else>{{ emptyText || t("workflow.diffEmptyDefault") }}</p>
  </section>
</template>

<style scoped>
.workflow-revision-diff {
  display: grid;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--aw-border-soft);
  border-radius: 10px;
  background: #fff;
}

.workflow-revision-diff-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.workflow-revision-diff-head span,
.workflow-revision-diff-head small {
  color: var(--aw-muted);
  font-size: 12px;
}

.workflow-revision-diff-head strong {
  display: block;
  margin-top: 4px;
  color: var(--aw-text);
  font-size: 14px;
}

.workflow-revision-diff-body,
.workflow-revision-diff-group,
.workflow-revision-diff-list {
  display: grid;
  gap: 8px;
}

.workflow-revision-diff-group h4 {
  margin: 0;
  color: var(--aw-text);
  font-size: 13px;
}

.workflow-revision-diff-group p {
  margin: 0;
  color: var(--aw-muted);
  font-size: 12px;
}

.workflow-revision-diff-item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 10px;
  align-items: start;
  padding: 10px;
  border: 1px solid var(--aw-border-soft);
  border-radius: 8px;
  background: #f8fafc;
}

.workflow-revision-diff-item strong,
.workflow-revision-diff-item small {
  display: block;
}

.workflow-revision-diff-item strong {
  color: var(--aw-text);
  font-size: 13px;
}

.workflow-revision-diff-item small {
  color: var(--aw-muted);
  font-size: 12px;
}
</style>
