<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import {
  WORKFLOW_GENERATE_PROMPT_MAX,
  assistantNodePills,
  failureCtas,
  generateFailureDisplayKey,
  latestTranscriptDraftVersion,
  visibleReviseIssues,
  type GenerateAgentOption,
  type TranscriptRow,
  type WorkflowGenerateAgentsLoadState,
  type WorkflowGenerateFailureCtaKey,
} from "../../composables/workflow-generate-dock";
import type { SmartDagFailureState } from "../../stores/smartdag";
import type {
  FailureFeedback,
  SmartDAGGuardReport,
  SmartDAGMissingCapability,
  SmartDAGNodeExplanation,
  SmartDAGReasoningStep,
} from "../../types/domain";

const props = withDefaults(
  defineProps<{
    hasWorkspaceContext: boolean;
    prompt: string;
    sheet?: boolean;
    generating?: boolean;
    generateBusy?: boolean;
    hasPersistableDraft?: boolean;
    isDirty?: boolean;
    showDirtyChip?: boolean;
    agentsLoadState?: WorkflowGenerateAgentsLoadState;
    selectedAgentId?: string;
    selectedAgentName?: string;
    selectedAgentUsable?: boolean;
    agents?: GenerateAgentOption[];
    agentPopoverOpen?: boolean;
    transcript?: TranscriptRow[];
    lastFailure?: SmartDagFailureState;
    lastGuardReport?: SmartDAGGuardReport;
    pendingFailureFeedback?: FailureFeedback | null;
    failureFeedbackBannerHidden?: boolean;
    reasoningSteps?: SmartDAGReasoningStep[];
    nodeExplanations?: SmartDAGNodeExplanation[];
    missingCapabilities?: SmartDAGMissingCapability[];
    sessionClosed?: boolean;
    restorePending?: boolean;
    endSessionConfirmVisible?: boolean;
  }>(),
  {
    sheet: false,
    generating: false,
    generateBusy: false,
    hasPersistableDraft: false,
    isDirty: false,
    showDirtyChip: false,
    agentsLoadState: "loaded",
    selectedAgentId: "",
    selectedAgentName: "",
    selectedAgentUsable: true,
    agents: () => [],
    agentPopoverOpen: false,
    transcript: () => [],
    reasoningSteps: () => [],
    nodeExplanations: () => [],
    missingCapabilities: () => [],
    sessionClosed: false,
    restorePending: false,
    endSessionConfirmVisible: false,
    failureFeedbackBannerHidden: false,
  },
);

const emit = defineEmits<{
  (event: "close-sheet"): void;
  (event: "update:prompt", value: string): void;
  (event: "submit"): void;
  (event: "select-agent", agentId: string): void;
  (event: "toggle-agent-popover"): void;
  (event: "failure-cta", key: WorkflowGenerateFailureCtaKey): void;
  (event: "confirm-end-session"): void;
  (event: "cancel-end-session"): void;
  (event: "hide-failure-feedback-banner"): void;
  (event: "dismiss-failure-feedback"): void;
  (event: "focus-node", nodeId: string): void;
}>();

const { t } = useI18n();

const transcriptRef = ref<HTMLElement | null>(null);
const promptRef = ref<HTMLTextAreaElement | null>(null);

const examples = computed(() => [
  t("workflow.generateExample1"),
  t("workflow.generateExample2"),
  t("workflow.generateExample3"),
]);

const promptTooLong = computed(() => [...props.prompt].length > WORKFLOW_GENERATE_PROMPT_MAX);
const promptNearLimit = computed(() => [...props.prompt].length >= WORKFLOW_GENERATE_PROMPT_MAX - 200);

const canSubmit = computed(() => {
  if (!props.hasWorkspaceContext || props.generating || props.generateBusy || props.sessionClosed) return false;
  if (props.restorePending || props.agentsLoadState === "loading") return false;
  if (!props.selectedAgentUsable) return false;
  const nextPrompt = props.prompt.trim();
  return Boolean(nextPrompt) && !promptTooLong.value;
});

const submitLabel = computed(() => {
  if (props.generating) return t("workflow.generateSubmitting");
  if (props.generateBusy && props.isDirty) return t("workflow.generateSavingThenSend");
  if (props.hasPersistableDraft) return t("workflow.generateRevise");
  return t("workflow.generateSubmit");
});

const lastAssistantId = computed(() => {
  const assistants = props.transcript.filter((row) => row.kind === "assistant");
  return assistants.at(-1)?.id || "";
});

const draftVersion = computed(() => latestTranscriptDraftVersion(props.transcript));

const nodePills = computed(() => assistantNodePills(props.nodeExplanations));

const recoveryCtas = computed(() =>
  props.lastFailure && !props.generating
    ? failureCtas(props.lastFailure.code, props.lastFailure.sessionStatus || (props.sessionClosed ? "CLOSED" : "OPEN"))
    : [],
);

const showReviseBanner = computed(() => Boolean(props.pendingFailureFeedback) && !props.failureFeedbackBannerHidden);

const reviseBannerIssues = computed(() => visibleReviseIssues(props.pendingFailureFeedback?.issues || []));

const reviseBannerSource = computed(() =>
  props.pendingFailureFeedback?.source === "trial"
    ? t("workflow.generateReviseBannerTrial")
    : t("workflow.generateReviseBannerCompile"),
);

const guardViolations = computed(() =>
  props.lastFailure?.code === "GUARD_REJECTED" && props.lastGuardReport && !props.lastGuardReport.ok
    ? props.lastGuardReport.violations
    : [],
);

const chipLabel = computed(() => {
  if (props.agentsLoadState === "loading") return t("workflow.generateLoadingAgents");
  if (!props.agents.length) return t("workflow.generateNoAgents");
  if (!props.selectedAgentUsable) return t("workflow.generateModelRequired");
  if (props.selectedAgentName) return t("workflow.generateAgentChip", { name: props.selectedAgentName });
  return t("workflow.generateAgentMissing");
});

const chipShortLabel = computed(() => {
  if (props.agentsLoadState === "loading") return t("workflow.generateLoadingAgents");
  if (!props.agents.length) return t("workflow.generateNoAgentsShort");
  if (!props.selectedAgentUsable) return t("workflow.generateModelRequiredShort");
  if (props.selectedAgentName) return props.selectedAgentName;
  return t("workflow.generateAgentMissing");
});

const showEmptyHint = computed(() => !props.transcript.length && !props.sessionClosed);

const showEndSession = computed(
  () => !props.sessionClosed && props.transcript.some((row) => row.kind === "user" || row.kind === "assistant"),
);

const visibleTranscript = computed(() => {
  if (!props.lastFailure || props.generating) return props.transcript;
  const lastFailureRow = [...props.transcript].reverse().find((row) => row.kind === "failure");
  if (!lastFailureRow) return props.transcript;
  return props.transcript.filter((row) => row.id !== lastFailureRow.id);
});

function applyExample(example: string) {
  emit("update:prompt", example);
}

function onPromptInput(event: Event) {
  const target = event.target;
  if (target instanceof HTMLTextAreaElement) {
    emit("update:prompt", target.value);
    resizePrompt(target);
  }
}

function resizePrompt(el: HTMLTextAreaElement) {
  el.style.height = "auto";
  el.style.height = `${Math.min(Math.max(el.scrollHeight, 40), 120)}px`;
}

function submitGenerate() {
  if (!canSubmit.value) return;
  emit("submit");
}

function onPromptKeydown(event: KeyboardEvent) {
  if (event.key !== "Enter") return;
  if (event.isComposing || event.keyCode === 229) return;
  if (event.shiftKey) return;
  event.preventDefault();
  submitGenerate();
}

function failureMessage(row: Extract<TranscriptRow, { kind: "failure" }>) {
  const key = generateFailureDisplayKey(row.code);
  if (
    row.code === "AGENT_MODEL_REQUIRED" ||
    row.code === "GUARD_REJECTED" ||
    row.code === "INTERNAL_ERROR" ||
    row.code === "LLM_JOB_FAILED" ||
    row.code === "NETWORK_ERROR" ||
    !row.message ||
    /request could not be completed/i.test(row.message)
  ) {
    return t(key);
  }
  return row.message;
}

function scrollTranscriptToEnd() {
  const el = transcriptRef.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
}

watch(
  () => [props.transcript.length, props.generating, props.lastFailure?.code, props.endSessionConfirmVisible],
  () => {
    void nextTick(scrollTranscriptToEnd);
  },
);

watch(
  () => props.prompt,
  () => {
    if (promptRef.value) resizePrompt(promptRef.value);
  },
);
</script>

<template>
  <section class="workflow-generate-dock" :aria-busy="props.generating || props.generateBusy ? 'true' : 'false'">
    <div class="workflow-generate-dock-head">
      <div class="workflow-generate-dock-title">
        <h3>
          {{ t("workflow.generateDockTitle") }}
          <span v-if="draftVersion" class="workflow-generate-version">{{
            t("workflow.generateVersionChip", { n: draftVersion })
          }}</span>
        </h3>
        <p v-if="showEmptyHint">{{ t("workflow.generateDockHint") }}</p>
      </div>
      <div class="workflow-generate-dock-head-actions">
        <button
          v-if="showEndSession"
          class="ghost-button workflow-generate-end-session"
          type="button"
          data-action="end-generate-session"
          :disabled="props.generating || props.generateBusy"
          :aria-label="t('workflow.generateEndSession')"
          @click="emit('failure-cta', 'end-session')"
        >
          {{ t("workflow.generateEndConfirmYes") }}
        </button>
        <button
          v-if="props.sheet"
          class="ghost-button workflow-generate-sheet-done"
          type="button"
          data-action="close-generate-sheet"
          :disabled="props.generating || props.generateBusy"
          @click="emit('close-sheet')"
        >
          {{ t("workflow.generateSheetDone") }}
        </button>
      </div>
    </div>

    <section v-if="showReviseBanner" class="workflow-generate-revise-banner" data-testid="generate-revise-banner">
      <div class="workflow-generate-revise-banner-head">
        <strong>{{ t("workflow.generateReviseBannerTitle") }}</strong>
        <button
          class="ghost-button"
          type="button"
          data-action="hide-failure-feedback-banner"
          :aria-label="t('workflow.generateReviseBannerClose')"
          @click="emit('hide-failure-feedback-banner')"
        >
          {{ t("workflow.generateReviseBannerClose") }}
        </button>
      </div>
      <p>{{ reviseBannerSource }}</p>
      <ul v-if="reviseBannerIssues.preview.length">
        <li v-for="(issue, index) in reviseBannerIssues.preview" :key="`${issue.code}-${index}`">
          {{ issue.message }}
        </li>
      </ul>
      <details v-if="reviseBannerIssues.extra.length" class="workflow-generate-revise-more">
        <summary>{{ t("workflow.generateReviseBannerMore", { n: reviseBannerIssues.extra.length }) }}</summary>
        <ul>
          <li v-for="(issue, index) in reviseBannerIssues.extra" :key="`${issue.code}-extra-${index}`">
            {{ issue.message }}
          </li>
        </ul>
      </details>
      <button
        class="ghost-button"
        type="button"
        data-action="dismiss-failure-feedback"
        @click="emit('dismiss-failure-feedback')"
      >
        {{ t("workflow.generateReviseDismissFeedback") }}
      </button>
    </section>

    <div ref="transcriptRef" class="workflow-generate-transcript" aria-live="polite">
      <div v-if="!props.transcript.length" class="workflow-generate-empty">
        <p>{{ t("workflow.generateEmptyChat") }}</p>
        <div class="workflow-generate-examples">
          <span>{{ t("workflow.generateTryExamples") }}</span>
          <div class="workflow-generate-example-list">
            <button
              v-for="example in examples"
              :key="example"
              class="workflow-generate-example"
              type="button"
              @click="applyExample(example)"
            >
              {{ example }}
            </button>
          </div>
        </div>
      </div>

      <article
        v-for="row in visibleTranscript"
        :key="row.id"
        class="workflow-generate-bubble"
        :class="`is-${row.kind}`"
      >
        <template v-if="row.kind === 'user'">
          <span class="workflow-generate-bubble-role">{{ t("workflow.generateYou") }}</span>
          <p>{{ row.text }}</p>
        </template>
        <template v-else-if="row.kind === 'assistant'">
          <span class="workflow-generate-bubble-role">{{ t("workflow.generateAssistant") }}</span>
          <p>{{ row.text }}</p>
          <small v-if="row.draftVersion">{{ t("workflow.generateDraftReady", { n: row.draftVersion }) }}</small>
          <div v-if="row.id === lastAssistantId && nodePills.length" class="workflow-generate-node-pills">
            <span class="workflow-generate-node-pills-label">{{ t("workflow.generateChangedNodes") }}</span>
            <button
              v-for="pill in nodePills"
              :key="pill.nodeId"
              class="workflow-generate-node-pill"
              type="button"
              :data-node-pill="pill.nodeId"
              @click="emit('focus-node', pill.nodeId)"
            >
              {{ pill.label }}
            </button>
          </div>
          <details v-if="row.id === lastAssistantId && props.reasoningSteps.length" class="workflow-generate-reasoning">
            <summary>{{ t("workflow.generateReasoningToggle") }}</summary>
            <ol>
              <li v-for="step in props.reasoningSteps" :key="step.id">
                <strong>{{ step.label }}</strong>
                <span v-if="step.detail">{{ step.detail }}</span>
              </li>
            </ol>
          </details>
          <div v-if="row.id === lastAssistantId && props.missingCapabilities.length" class="workflow-generate-missing">
            <strong>{{ t("workflow.generateMissingCapabilities") }}</strong>
            <p v-for="cap in props.missingCapabilities" :key="cap.id">{{ cap.name }} — {{ cap.reason }}</p>
          </div>
        </template>
        <template v-else-if="row.kind === 'pending'">
          <span class="workflow-generate-bubble-role">{{ t("workflow.generateAssistant") }}</span>
          <p class="workflow-generate-pending-text">
            <span class="workflow-generate-pending-dots" aria-hidden="true"><i /><i /><i /></span>
            {{ t("workflow.generateInProgress") }}
          </p>
        </template>
        <template v-else>
          <p>{{ failureMessage(row) }}</p>
        </template>
      </article>

      <section
        v-if="props.lastFailure && !props.generating"
        class="workflow-generate-recovery"
        role="alert"
        data-testid="smart-dag-recovery-card"
      >
        <strong>{{ t("workflow.generateFailureTitle") }}</strong>
        <p v-if="props.lastFailure.code === 'SMART_DAG_TURN_IN_PROGRESS'">
          {{ t("workflow.generateInProgress") }}
        </p>
        <p v-else>
          {{
            failureMessage({
              kind: "failure",
              id: "card",
              code: props.lastFailure.code,
              message: props.lastFailure.message,
            })
          }}
        </p>
        <ul v-if="guardViolations.length" class="workflow-generate-guard-violations">
          <li v-for="(violation, index) in guardViolations" :key="`${violation.code}-${index}`">
            {{ violation.message }}
          </li>
        </ul>
        <div v-if="recoveryCtas.length" class="workflow-generate-recovery-actions">
          <button
            v-for="cta in recoveryCtas"
            :key="cta.key"
            type="button"
            :class="cta.kind === 'primary' ? 'primary-button' : 'ghost-button'"
            :data-action="`generate-failure-${cta.key}`"
            @click="emit('failure-cta', cta.key)"
          >
            {{ t(cta.labelKey) }}
          </button>
        </div>
        <details class="workflow-generate-tech">
          <summary>{{ t("workflow.generateTechDetails") }}</summary>
          <dl>
            <div v-if="props.lastFailure.code">
              <dt>{{ t("workflow.generateErrorCode") }}</dt>
              <dd>{{ props.lastFailure.code }}</dd>
            </div>
            <div v-if="props.lastFailure.requestId">
              <dt>requestId</dt>
              <dd>{{ props.lastFailure.requestId }}</dd>
            </div>
            <div v-if="props.lastFailure.traceId">
              <dt>traceId</dt>
              <dd>{{ props.lastFailure.traceId }}</dd>
            </div>
            <div v-if="props.lastFailure.sessionId">
              <dt>sessionId</dt>
              <dd>{{ props.lastFailure.sessionId }}</dd>
            </div>
            <div v-if="props.lastFailure.sessionLockVersion">
              <dt>sessionLockVersion</dt>
              <dd>{{ props.lastFailure.sessionLockVersion }}</dd>
            </div>
          </dl>
        </details>
      </section>
    </div>

    <div
      v-if="props.endSessionConfirmVisible"
      class="workflow-generate-end-confirm"
      role="alertdialog"
      data-testid="generate-end-session-confirm"
    >
      <p>{{ t("workflow.generateEndConfirm") }}</p>
      <div class="workflow-generate-recovery-actions">
        <button
          class="primary-button"
          type="button"
          data-action="confirm-end-generate"
          @click="emit('confirm-end-session')"
        >
          {{ t("workflow.generateEndConfirmYes") }}
        </button>
        <button
          class="ghost-button"
          type="button"
          data-action="cancel-end-generate"
          @click="emit('cancel-end-session')"
        >
          {{ t("workflow.generateEndConfirmNo") }}
        </button>
      </div>
    </div>

    <div v-if="props.sessionClosed" class="workflow-generate-closed">
      <p>{{ t("workflow.generateResumeClosed") }}</p>
      <button
        class="primary-button"
        type="button"
        data-action="generate-failure-new-attempt"
        @click="emit('failure-cta', 'new-attempt')"
      >
        {{ t("workflow.generateReopen") }}
      </button>
    </div>

    <footer v-else class="workflow-generate-composer">
      <textarea
        ref="promptRef"
        class="workflow-generate-prompt"
        rows="1"
        :value="props.prompt"
        :maxlength="WORKFLOW_GENERATE_PROMPT_MAX"
        :aria-label="t('workflow.generateDockTitle')"
        :aria-invalid="promptTooLong ? 'true' : 'false'"
        :aria-describedby="
          promptNearLimit ? 'workflow-generate-char-count workflow-generate-shortcut' : 'workflow-generate-shortcut'
        "
        :placeholder="
          props.hasPersistableDraft ? t('workflow.generatePlaceholderRevise') : t('workflow.generatePlaceholder')
        "
        :disabled="props.generating || props.generateBusy"
        @input="onPromptInput"
        @keydown="onPromptKeydown"
      />
      <span id="workflow-generate-shortcut" class="workflow-generate-sr-only">{{
        t("workflow.generateComposerShortcut")
      }}</span>
      <div class="workflow-generate-composer-bar">
        <div class="workflow-generate-agent">
          <button
            class="intent-chip workflow-generate-agent-chip"
            type="button"
            data-action="generate-agent-chip"
            :class="{ warning: !props.selectedAgentUsable || !props.agents.length }"
            :disabled="props.generating || props.generateBusy || props.agentsLoadState === 'loading'"
            :aria-label="chipLabel"
            :title="chipLabel"
            @click="emit('toggle-agent-popover')"
          >
            {{ chipShortLabel }}
          </button>
          <div v-if="props.agentPopoverOpen" class="workflow-generate-agent-popover" role="listbox">
            <p v-if="!props.agents.length">{{ t("workflow.generateNoAgents") }}</p>
            <button
              v-for="agent in props.agents"
              :key="agent.id"
              type="button"
              role="option"
              :aria-selected="agent.id === props.selectedAgentId ? 'true' : 'false'"
              :data-agent-id="agent.id"
              :disabled="!agent.usable"
              @click="emit('select-agent', agent.id)"
            >
              {{ agent.name }}
            </button>
          </div>
        </div>
        <span v-if="props.showDirtyChip" class="intent-chip">{{ t("workflow.generateDirtyChip") }}</span>
        <span
          v-if="promptNearLimit || promptTooLong"
          id="workflow-generate-char-count"
          class="workflow-generate-char-count"
        >
          {{ t("workflow.generateCharCount", { n: [...props.prompt].length }) }}
        </span>
        <button
          class="primary-button workflow-generate-send"
          type="button"
          data-action="submit-generate"
          :disabled="!canSubmit"
          :aria-label="submitLabel"
          @click="submitGenerate"
        >
          {{ submitLabel }}
        </button>
      </div>
    </footer>
  </section>
</template>
