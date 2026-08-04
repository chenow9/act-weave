<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import type { WorkflowReadiness } from "../../types/domain";

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    readiness?: WorkflowReadiness;
    compact?: boolean;
  }>(),
  {
    compact: false,
  },
);

const stageLabels = computed<Record<string, string>>(() => ({
  DraftMissing: t("workflow.stageDraftMissing"),
  CompileRequired: t("workflow.stageCompileRequired"),
  CompileFailed: t("workflow.stageCompileFailed"),
  TrialRequired: t("workflow.stageTrialRequired"),
  PublishReady: t("workflow.stagePublishReady"),
  Published: t("workflow.statusPublished"),
  Disabled: t("workflow.statusDisabled"),
}));

const fallbackActions = computed<Record<string, string>>(() => ({
  DraftMissing: t("workflow.actionDraftMissing"),
  CompileRequired: t("workflow.actionCompileRequired"),
  CompileFailed: t("workflow.actionCompileFailed"),
  TrialRequired: t("workflow.actionTrialRequired"),
  PublishReady: t("workflow.actionPublishReady"),
  Published: t("workflow.actionPublished"),
  Disabled: t("workflow.actionDisabled"),
}));

const blockerActionLabels = computed<Record<string, string>>(() => ({
  draft_missing: fallbackActions.value.DraftMissing,
  compile_required: fallbackActions.value.CompileRequired,
  compile_failed: fallbackActions.value.CompileFailed,
  trial_required: fallbackActions.value.TrialRequired,
  workflow_disabled: fallbackActions.value.Disabled,
}));

const blockerMessageLabels = computed<Record<string, string>>(() => ({
  draft_missing: t("workflow.blockerDraftMissing"),
  compile_required: t("workflow.blockerCompileRequired"),
  compile_failed: t("workflow.blockerCompileFailed"),
  trial_required: t("workflow.blockerTrialRequired"),
  workflow_disabled: t("workflow.blockerDisabled"),
}));

const nextAction = computed(() => {
  const blocker = props.readiness?.blockers?.find((candidate) => candidate.action.trim());
  if (blocker) {
    return blockerActionLabels.value[blocker.code] || blocker.action;
  }

  return fallbackActions.value[props.readiness?.stage || ""] || t("workflow.waitReadiness");
});

const checklist = computed(() => [
  { key: "draft", label: t("workflow.summaryDraft"), ready: Boolean(props.readiness?.hasDraft) },
  {
    key: "compile",
    label: t("workflow.readinessCompile"),
    ready: Boolean(props.readiness?.compilationCurrent && props.readiness?.compilationValid),
  },
  {
    key: "trial",
    label: t("workflow.trialRun"),
    ready: Boolean(props.readiness?.trialCurrent && props.readiness?.trialSuccessful),
  },
  {
    key: "publish",
    label: t("workflow.readinessPublish"),
    ready: Boolean(props.readiness?.published || props.readiness?.canPublish),
  },
]);
</script>

<template>
  <section class="workflow-readiness-panel" :class="{ compact }">
    <div class="workflow-readiness-head">
      <span class="workflow-readiness-stage">{{ stageLabels[readiness?.stage || ""] || t("workflow.stageUnknown") }}</span>
      <strong>{{ nextAction }}</strong>
    </div>

    <div class="workflow-readiness-checklist">
      <span v-for="item in checklist" :key="item.key" class="workflow-readiness-check" :class="{ ready: item.ready }">
        <i :class="item.ready ? 'fa-solid fa-check' : 'fa-solid fa-clock'" />
        {{ item.label }}
      </span>
    </div>

    <div v-if="!compact && readiness?.blockers?.length" class="workflow-readiness-blockers">
      <article
        v-for="blocker in readiness.blockers"
        :key="`${blocker.code}-${blocker.nodeId || blocker.edgeId || blocker.fieldPath || ''}`"
      >
        <span>{{ blocker.severity }}</span>
        <strong>{{ blockerMessageLabels[blocker.code] || blocker.message }}</strong>
        <small>{{ blockerActionLabels[blocker.code] || blocker.action }}</small>
      </article>
    </div>
  </section>
</template>
