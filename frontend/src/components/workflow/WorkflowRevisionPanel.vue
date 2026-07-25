<script setup lang="ts">
import { computed } from "vue";

import type { WorkflowReadiness, WorkflowRevision, WorkflowStatus } from "../../types/domain";

const props = defineProps<{
  revisions: WorkflowRevision[];
  readiness?: WorkflowReadiness;
  busyRevisionId?: string;
  workflowStatus?: WorkflowStatus;
  disableBusy?: boolean;
  emptyText?: string;
}>();

const emit = defineEmits<{
  activate: [revisionId: string];
  rollback: [revisionId: string];
  compare: [leftRevisionId: string, rightRevisionId: string];
  disable: [];
}>();

const sortedRevisions = computed(() =>
  [...props.revisions].sort((a, b) => Date.parse(b.createdAt || "") - Date.parse(a.createdAt || "")),
);

const activeRevisionId = computed(() => props.readiness?.activeRevisionId || "");
const latestRevisionId = computed(() => props.readiness?.latestRevisionId || sortedRevisions.value[0]?.revisionId || "");
const isDisabled = computed(() => props.workflowStatus === "Disabled");

function formatDate(value?: string) {
  if (!value) return "未记录";
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(timestamp));
}

function shortHash(value?: string) {
  if (!value) return "未计算";
  return value.replace(/^sha256:/, "").slice(0, 12);
}
</script>

<template>
  <section class="workflow-revision-panel">
    <div class="workflow-revision-head">
      <div>
        <span>发布版本</span>
        <strong>Active {{ activeRevisionId || "未设置" }}</strong>
        <small>Latest {{ latestRevisionId || "暂无" }}</small>
      </div>
      <button class="ghost-button" type="button" :disabled="disableBusy || isDisabled" @click="emit('disable')">
        {{ isDisabled ? "已停用" : "停用新执行" }}
      </button>
    </div>

    <div v-if="sortedRevisions.length" class="workflow-revision-list">
      <article
        v-for="revision in sortedRevisions"
        :key="revision.revisionId"
        class="workflow-revision-item"
        :class="{ active: revision.revisionId === activeRevisionId }"
      >
        <div>
          <strong>{{ revision.revisionId }}</strong>
          <small>{{ formatDate(revision.createdAt) }} · {{ shortHash(revision.planHash) }}</small>
        </div>
        <span v-if="revision.revisionId === activeRevisionId" class="status-pill published">Active</span>
        <span v-else-if="revision.revisionId === latestRevisionId" class="status-pill review">Latest</span>
        <span v-else class="status-pill draft">History</span>
        <div class="workflow-revision-actions">
          <button
            class="ghost-button"
            type="button"
            :disabled="revision.revisionId === activeRevisionId || busyRevisionId === revision.revisionId"
            @click="emit('activate', revision.revisionId)"
          >
            激活
          </button>
          <button
            class="ghost-button"
            type="button"
            :disabled="revision.revisionId === activeRevisionId || busyRevisionId === revision.revisionId"
            @click="emit('rollback', revision.revisionId)"
          >
            回滚
          </button>
          <button
            class="ghost-button"
            type="button"
            :disabled="!activeRevisionId || revision.revisionId === activeRevisionId"
            @click="emit('compare', activeRevisionId, revision.revisionId)"
          >
            对比
          </button>
        </div>
      </article>
    </div>
    <p v-else>{{ props.emptyText || "还没有发布版本。完成试运行并发布后，版本会显示在这里。" }}</p>
  </section>
</template>
