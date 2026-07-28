<script setup lang="ts">
import { computed } from "vue";

import type { WorkflowReadiness } from "../../types/domain";

const props = withDefaults(
  defineProps<{
    readiness?: WorkflowReadiness;
    compact?: boolean;
  }>(),
  {
    compact: false,
  },
);

const stageLabels: Record<string, string> = {
  DraftMissing: "缺少草稿",
  CompileRequired: "需要编译",
  CompileFailed: "编译失败",
  TrialRequired: "需要试运行",
  PublishReady: "可发布",
  Published: "已发布",
  Disabled: "已停用",
};

const fallbackActions: Record<string, string> = {
  DraftMissing: "先创建或保存流程草稿，再继续校验。",
  CompileRequired: "保存或检查当前草稿，生成最新编译结果。",
  CompileFailed: "先修复编译问题，再继续试运行或发布。",
  TrialRequired: "运行当前已编译草稿的试运行。",
  PublishReady: "当前草稿已通过试运行，可以发布给 Agent 调用。",
  Published: "已发布版本可供 Agent 调用。",
  Disabled: "启用流程后才能继续校验、试运行或发布。",
};

const blockerActionLabels: Record<string, string> = {
  draft_missing: fallbackActions.DraftMissing,
  compile_required: fallbackActions.CompileRequired,
  compile_failed: fallbackActions.CompileFailed,
  trial_required: fallbackActions.TrialRequired,
  workflow_disabled: fallbackActions.Disabled,
};

const blockerMessageLabels: Record<string, string> = {
  draft_missing: "缺少流程草稿。",
  compile_required: "当前草稿需要重新编译。",
  compile_failed: "流程草稿编译失败。",
  trial_required: "当前已编译草稿需要先通过试运行。",
  workflow_disabled: "流程已停用。",
};

const nextAction = computed(() => {
  const blocker = props.readiness?.blockers?.find((candidate) => candidate.action.trim());
  if (blocker) {
    return blockerActionLabels[blocker.code] || blocker.action;
  }

  return fallbackActions[props.readiness?.stage || ""] || "等待后端返回就绪状态。";
});

const checklist = computed(() => [
  { key: "draft", label: "草稿", ready: Boolean(props.readiness?.hasDraft) },
  {
    key: "compile",
    label: "编译",
    ready: Boolean(props.readiness?.compilationCurrent && props.readiness?.compilationValid),
  },
  { key: "trial", label: "试运行", ready: Boolean(props.readiness?.trialCurrent && props.readiness?.trialSuccessful) },
  { key: "publish", label: "发布", ready: Boolean(props.readiness?.published || props.readiness?.canPublish) },
]);
</script>

<template>
  <section class="workflow-readiness-panel" :class="{ compact }">
    <div class="workflow-readiness-head">
      <span class="workflow-readiness-stage">{{ stageLabels[readiness?.stage || ""] || "状态未知" }}</span>
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
