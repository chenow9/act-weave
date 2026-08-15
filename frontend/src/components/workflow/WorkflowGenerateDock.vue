<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import {
  WORKFLOW_GENERATE_PROMPT_MAX,
  failureCtas,
  generateFailureDisplayKey,
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
}>();

const { t } = useI18n();

const examples = computed(() => [
  t("workflow.generateExample1"),
  t("workflow.generateExample2"),
  t("workflow.generateExample3"),
]);

const promptTooLong = computed(() => [...props.prompt].length > WORKFLOW_GENERATE_PROMPT_MAX);

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

function applyExample(example: string) {
  emit("update:prompt", example);
}

function onPromptInput(event: Event) {
  const target = event.target;
  if (target instanceof HTMLTextAreaElement) {
    emit("update:prompt", target.value);
  }
}

function submitGenerate() {
  if (!canSubmit.value) return;
  emit("submit");
}

function onPromptKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
    event.preventDefault();
    submitGenerate();
  }
}

function failureMessage(row: Extract<TranscriptRow, { kind: "failure" }>) {
  const key = generateFailureDisplayKey(row.code);
  return row.code === "AGENT_MODEL_REQUIRED" || row.code === "GUARD_REJECTED" || !row.message ? t(key) : row.message;
}
</script>

<template>
  <section class="workflow-generate-dock" :aria-busy="props.generating || props.generateBusy ? 'true' : 'false'">
    <div class="workflow-generate-dock-head">
      <div class="workflow-panel-heading">
        <span>{{ t("workflow.generateDockTitle") }}</span>
        <h3>{{ t("workflow.generateDockTitle") }}</h3>
        <p>{{ t("workflow.generateDockHint") }}</p>
      </div>
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

    <div class="workflow-generate-transcript" aria-live="polite">
      <article v-for="row in props.transcript" :key="row.id" class="workflow-generate-bubble" :class="`is-${row.kind}`">
        <template v-if="row.kind === 'user'">
          <span class="workflow-generate-bubble-role">{{ t("workflow.generateYou") }}</span>
          <p>{{ row.text }}</p>
        </template>
        <template v-else-if="row.kind === 'assistant'">
          <span class="workflow-generate-bubble-role">{{ t("workflow.generateAssistant") }}</span>
          <p>{{ row.text }}</p>
          <small v-if="row.draftVersion">{{ t("workflow.generateDraftReady", { n: row.draftVersion }) }}</small>
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
          <p>{{ t("workflow.generateInProgress") }}</p>
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

    <textarea
      class="workflow-generate-prompt"
      rows="7"
      :value="props.prompt"
      :maxlength="WORKFLOW_GENERATE_PROMPT_MAX"
      :aria-label="t('workflow.generateDockTitle')"
      :aria-invalid="promptTooLong ? 'true' : 'false'"
      :placeholder="
        props.hasPersistableDraft ? t('workflow.generatePlaceholderRevise') : t('workflow.generatePlaceholder')
      "
      :disabled="props.generating || props.generateBusy || props.sessionClosed"
      @input="onPromptInput"
      @keydown="onPromptKeydown"
    />

    <div v-if="!props.transcript.length" class="workflow-generate-examples">
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

    <div class="workflow-generate-char-count">
      {{ t("workflow.generateCharCount", { n: props.prompt.length }) }}
    </div>

    <footer class="workflow-generate-footer">
      <div class="workflow-generate-agent">
        <button
          class="intent-chip workflow-generate-agent-chip"
          type="button"
          data-action="generate-agent-chip"
          :class="{ warning: !props.selectedAgentUsable || !props.agents.length }"
          :disabled="props.generating || props.generateBusy || props.agentsLoadState === 'loading'"
          @click="emit('toggle-agent-popover')"
        >
          {{ chipLabel }}
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
      <button
        class="primary-button"
        type="button"
        data-action="submit-generate"
        :disabled="!canSubmit"
        @click="submitGenerate"
      >
        {{ submitLabel }}
      </button>
    </footer>
  </section>
</template>
