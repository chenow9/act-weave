<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import type { WorkflowReadiness, WorkflowRevision, WorkflowStatus } from "../../types/domain";
import { getI18nLocale } from "../../i18n";

const { t } = useI18n();

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
const latestRevisionId = computed(
  () => props.readiness?.latestRevisionId || sortedRevisions.value[0]?.revisionId || "",
);
const isDisabled = computed(() => props.workflowStatus === "Disabled");

function formatDate(value?: string) {
  if (!value) return t("workflow.notRecorded");
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return value;
  return new Intl.DateTimeFormat(getI18nLocale() === "zh-CN" ? "zh-CN" : "en-US", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(timestamp));
}

function shortHash(value?: string) {
  if (!value) return t("workflow.notComputed");
  return value.replace(/^sha256:/, "").slice(0, 12);
}

/** Display helper: first 8 … last 4 for long IDs; full value stays in title / accessible name. */
function displayRevisionId(value?: string) {
  if (!value) return "";
  if (value.length <= 16) return value;
  return `${value.slice(0, 8)}…${value.slice(-4)}`;
}

function statusLabel(revisionId: string) {
  if (revisionId === activeRevisionId.value) return "Active";
  if (revisionId === latestRevisionId.value) return "Latest";
  return "History";
}

function statusTone(revisionId: string) {
  if (revisionId === activeRevisionId.value) return "published";
  if (revisionId === latestRevisionId.value) return "review";
  return "draft";
}
</script>

<template>
  <section class="workflow-revision-panel">
    <div class="workflow-revision-head">
      <div class="workflow-revision-head-title-row">
        <span class="workflow-revision-head-title">{{ t("workflow.publishedVersions") }}</span>
        <button
          class="ghost-button workflow-revision-disable-button"
          type="button"
          :disabled="disableBusy || isDisabled"
          @click="emit('disable')"
        >
          {{ isDisabled ? t("workflow.alreadyDisabled") : t("workflow.disableNewRuns") }}
        </button>
      </div>
      <div class="workflow-revision-meta">
        <div class="workflow-revision-meta-card" data-testid="workflow-revision-active-meta">
          <span class="workflow-revision-meta-label">Active</span>
          <strong class="workflow-revision-meta-id" :title="activeRevisionId || undefined">{{
            activeRevisionId ? displayRevisionId(activeRevisionId) : t("workflow.notSet")
          }}</strong>
        </div>
        <div class="workflow-revision-meta-card" data-testid="workflow-revision-latest-meta">
          <span class="workflow-revision-meta-label">Latest</span>
          <strong class="workflow-revision-meta-id" :title="latestRevisionId || undefined">{{
            latestRevisionId ? displayRevisionId(latestRevisionId) : t("workflow.noneYet")
          }}</strong>
        </div>
      </div>
    </div>

    <div v-if="sortedRevisions.length" class="workflow-revision-list">
      <article
        v-for="revision in sortedRevisions"
        :key="revision.revisionId"
        class="workflow-revision-item"
        :class="{ active: revision.revisionId === activeRevisionId }"
        :data-revision-id="revision.revisionId"
      >
        <div class="workflow-revision-info">
          <strong class="workflow-revision-id" :title="revision.revisionId">{{
            displayRevisionId(revision.revisionId)
          }}</strong>
          <small class="workflow-revision-meta-line">
            {{ formatDate(revision.createdAt) }} · {{ shortHash(revision.planHash) }}
          </small>
        </div>
        <span class="status-pill workflow-revision-status" :class="statusTone(revision.revisionId)">{{
          statusLabel(revision.revisionId)
        }}</span>
        <div class="workflow-revision-actions">
          <button
            class="ghost-button"
            type="button"
            :disabled="revision.revisionId === activeRevisionId || busyRevisionId === revision.revisionId"
            :aria-busy="busyRevisionId === revision.revisionId ? 'true' : undefined"
            @click="emit('activate', revision.revisionId)"
          >
            {{ t("workflow.activate") }}
          </button>
          <button
            class="ghost-button"
            type="button"
            :disabled="revision.revisionId === activeRevisionId || busyRevisionId === revision.revisionId"
            :aria-busy="busyRevisionId === revision.revisionId ? 'true' : undefined"
            @click="emit('rollback', revision.revisionId)"
          >
            {{ t("workflow.rollback") }}
          </button>
          <button
            class="ghost-button"
            type="button"
            :disabled="!activeRevisionId || revision.revisionId === activeRevisionId"
            @click="emit('compare', activeRevisionId, revision.revisionId)"
          >
            {{ t("workflow.compare") }}
          </button>
        </div>
      </article>
    </div>
    <p v-else class="workflow-revision-empty">
      {{ props.emptyText || t("workflow.revisionEmptyWithWorkflow") }}
    </p>
  </section>
</template>
