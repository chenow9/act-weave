<script setup lang="ts">
import { useI18n } from "vue-i18n";
const { t } = useI18n();
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents studio panel (ZKL-64 item 16). */
import AppSelect from "./AppSelect.vue";
import AgentPromptDiffViewer from "./AgentPromptDiffViewer.vue";
import AgentDelegationPanel from "./AgentDelegationPanel.vue";
import { useAgentsPageContext } from "../composables/useAgentsPageContext";

import { computed } from "vue";

const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  studioMode, draftAgent, savingAgent, agentStudioPanelRef, agentNameInputRef, promptDetailDialogRef, agentStudioInlineWarning, pendingPromptSaveReview, weavePreviewAgent, acceptingPromptRevision, workspaceOptions, modelConfigOptions, studioTitle, agentNameError, agentWorkspaceError,
  agentModelError, agentRoleError, agentPromptError, promptLineCount, promptPreviewText, canSaveAgent, originalPrompt, promptSaveDiff, pendingPromptText, weavePreviewDiff, agentSaveButtonLabel, canEnhanceDraftPrompt, formatSignedDelta, isEnhancing, toggleDraftStatus,
  agentContextMode, agentContextMaxInputTokens, agentContextMaxRecentTurns,
  agentContextSummaryMaxTokens, agentContextSummaryMinEvictedTurns, agentContextSummaryMaxPasses,
  agentContextIncludeCompactionSummary, agentContextAdvancedOpen, toggleAgentContextAdvanced,
  setAgentContextMode, setAgentContextMaxInput, setAgentContextMaxTurns,
  setAgentContextSummaryMaxTokens, setAgentContextSummaryMinEvictedTurns, setAgentContextSummaryMaxPasses,
  setAgentContextIncludeCompactionSummary,
  closeStudio,
  requestCloseStudio, trapAgentModalFocus, enhancePrompt, applyWeavePreview, cancelWeavePreview, confirmPromptSaveReview, cancelPromptSaveReview, saveDraftAgent
} = scp;

const agentContextModeOptions = [
  { label: t("agents.modeTokenWindow"), value: "token_window" },
  { label: t("agents.modeRolling"), value: "rolling_summary" },
  { label: t("agents.modeInherit"), value: "" },
  { label: t("agents.modeDisabled"), value: "disabled" },
];

/** Options for delegation target picker (exclude self when possible). */
const delegationAgentOptions = computed(() => {
  const items = (scp.agents?.items ?? []) as { id: string; name: string }[];
  if (items.length) return items.map((a) => ({ id: a.id, name: a.name }));
  return [];
});

void AppSelect;
void AgentPromptDiffViewer;
void AgentDelegationPanel;
</script>

<template>
  <Transition name="modal-fade">
    <div
      v-if="studioMode"
      class="modal-backdrop agent-studio-backdrop"
      @click.self="requestCloseStudio('backdrop')"
      @keydown.esc="requestCloseStudio('keyboard')"
      @keydown="trapAgentModalFocus"
    >
      <section
        ref="agentStudioPanelRef"
        class="modal-card agent-studio-panel"
        role="dialog"
        aria-modal="true"
        :aria-label="studioTitle"
      >
        <header class="agent-studio-shell">
          <div class="agent-studio-title">
            <button class="agent-back-button" type="button" @click="requestCloseStudio('back')">
              <i class="fa-solid fa-chevron-left" aria-hidden="true" />
              <span>{{ t("agents.backToList") }}</span>
            </button>
            <span class="agent-studio-divider" aria-hidden="true" />
            <div class="agent-studio-heading">
              <p class="agent-studio-eyebrow">
                {{ studioMode === "create" ? t("agents.modeCreate") : t("agents.modeEdit") }}
              </p>
              <h3 :title="studioMode === 'edit' && draftAgent.name ? draftAgent.name : undefined">
                {{
                  studioMode === "create" ? t("agents.createTitle") : draftAgent.name?.trim() || t("agents.untitled")
                }}
              </h3>
            </div>
          </div>
          <div class="agent-studio-actions">
            <button class="ghost-button" type="button" :disabled="savingAgent" @click="closeStudio">
              {{ t("agents.discard") }}
            </button>
            <button class="primary-button" type="button" :disabled="!canSaveAgent" @click="saveDraftAgent">
              <i :class="['fa-solid', savingAgent ? 'fa-spinner fa-spin' : 'fa-circle-check']" aria-hidden="true" />
              <span>{{ agentSaveButtonLabel }}</span>
            </button>
          </div>
        </header>

        <p v-if="agentStudioInlineWarning" class="agent-studio-inline-warning" role="alert">
          <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
          <span>{{ agentStudioInlineWarning }}</span>
        </p>

        <div class="agent-studio-body">
          <section class="agent-studio-section agent-parameters-panel">
            <header>
              <span><i class="fa-solid fa-sliders" aria-hidden="true" /> {{ t("agents.params") }}</span>
            </header>
            <div class="agent-studio-fields">
              <label class="modal-field">
                <span>{{ t("agents.runName") }} <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  ref="agentNameInputRef"
                  v-model="draftAgent.name"
                  type="text"
                  required
                  aria-required="true"
                  :aria-label="t('agents.runName')"
                  :aria-invalid="Boolean(agentNameError)"
                  :aria-describedby="agentNameError ? 'agent-name-error' : undefined"
                  :placeholder="t('agents.runNamePh')"
                />
                <small v-if="agentNameError" id="agent-name-error" class="field-error">{{ agentNameError }}</small>
              </label>
              <label class="modal-field">
                <span>{{ t("agents.bindWorkspace") }} <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.workspaceId"
                  :options="workspaceOptions"
                  :placeholder="t('agents.selectWorkspace')"
                  :aria-label="t('agents.bindWorkspace')"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentWorkspaceError)"
                  :aria-describedby="agentWorkspaceError ? 'agent-workspace-error' : undefined"
                />
                <small v-if="agentWorkspaceError" id="agent-workspace-error" class="field-error">{{
                  agentWorkspaceError
                }}</small>
              </label>
              <label class="modal-field">
                <span>{{ t("agents.decisionModel") }} <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.modelConfigId"
                  :options="modelConfigOptions"
                  :placeholder="t('agents.selectModel')"
                  :aria-label="t('agents.decisionModel')"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentModelError)"
                  :aria-describedby="agentModelError ? 'agent-model-error' : undefined"
                />
                <small v-if="agentModelError" id="agent-model-error" class="field-error">{{ agentModelError }}</small>
              </label>
              <label class="modal-field">
                <span>{{ t("agents.roleDuty") }} <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  v-model="draftAgent.roleDescription"
                  type="text"
                  required
                  aria-required="true"
                  :aria-label="t('agents.roleDuty')"
                  :aria-invalid="Boolean(agentRoleError)"
                  :aria-describedby="agentRoleError ? 'agent-role-error' : undefined"
                  :placeholder="t('agents.rolePh')"
                />
                <small v-if="agentRoleError" id="agent-role-error" class="field-error">{{ agentRoleError }}</small>
              </label>
              <div class="agent-status-toggle">
                <div>
                  <p>{{ t("agents.activeRun") }}</p>
                  <small>{{ t("agents.activeRunHint") }}</small>
                </div>
                <button
                  type="button"
                  role="switch"
                  :aria-label="t('agents.toggleActive')"
                  :aria-checked="draftAgent.status === 'ACTIVE'"
                  :class="{ active: draftAgent.status === 'ACTIVE' }"
                  @click="toggleDraftStatus"
                >
                  <span />
                </button>
              </div>

              <div class="agent-context-policy">
                <header>
                  <strong>{{ t("agents.contextPolicy") }}</strong>
                  <small>{{ t("agents.contextPolicyHint") }}</small>
                </header>
                <label class="modal-field">
                  <span>{{ t("agents.contextMode") }}</span>
                  <AppSelect
                    class="agent-studio-select"
                    :model-value="agentContextMode"
                    :options="agentContextModeOptions"
                    :placeholder="t('agents.selectContextMode')"
                    :aria-label="t('agents.contextMode')"
                    @update:model-value="setAgentContextMode(String($event ?? ''))"
                  />
                </label>
                <p class="agent-context-policy-hint">
                  <template v-if="agentContextMode === 'token_window' || !agentContextMode">
                    {{ t("agents.contextHintTokenWindow") }}
                  </template>
                  <template v-else-if="agentContextMode === 'rolling_summary'">
                    {{ t("agents.contextHintRolling") }}
                  </template>
                  <template v-else-if="agentContextMode === 'disabled'">
                    {{ t("agents.contextHintDisabled") }}
                  </template>
                  {{ t("agents.contextHintModelWindow") }}
                </p>
                <template v-if="agentContextMode === 'token_window' || agentContextMode === 'rolling_summary'">
                  <button
                    type="button"
                    class="agent-context-advanced-toggle"
                    :class="{ open: agentContextAdvancedOpen }"
                    :aria-expanded="agentContextAdvancedOpen"
                    @click="toggleAgentContextAdvanced"
                  >
                    <i class="fa-solid fa-sliders" aria-hidden="true" />
                    <span>{{ t("agents.contextAdvanced") }}</span>
                    <i class="fa-solid fa-chevron-down agent-context-advanced-chevron" aria-hidden="true" />
                  </button>
                  <div v-if="agentContextAdvancedOpen" class="agent-context-advanced">
                    <div class="agent-context-policy-limits">
                      <label class="modal-field">
                        <span>{{ t("agents.maxInputTokens") }}</span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="mono"
                          :value="agentContextMaxInputTokens"
                          @input="setAgentContextMaxInput(Number(($event.target as HTMLInputElement).value))"
                        />
                      </label>
                      <label class="modal-field">
                        <span>{{ t("agents.maxRecentTurns") }}</span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="mono"
                          :value="agentContextMaxRecentTurns"
                          @input="setAgentContextMaxTurns(Number(($event.target as HTMLInputElement).value))"
                        />
                      </label>
                    </div>
                    <div v-if="agentContextMode === 'rolling_summary'" class="agent-context-policy-limits">
                      <label class="modal-field">
                        <span>{{ t("agents.summaryMaxTokens") }}</span>
                        <input
                          type="number"
                          min="1"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMaxTokens"
                          @input="setAgentContextSummaryMaxTokens(Number(($event.target as HTMLInputElement).value))"
                        />
                      </label>
                      <label class="modal-field">
                        <span>{{ t("agents.summaryMinEvicted") }}</span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMinEvictedTurns"
                          @input="
                            setAgentContextSummaryMinEvictedTurns(Number(($event.target as HTMLInputElement).value))
                          "
                        />
                      </label>
                      <label class="modal-field">
                        <span>{{ t("agents.summaryMaxPasses") }}</span>
                        <input
                          type="number"
                          min="1"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMaxPasses"
                          @input="setAgentContextSummaryMaxPasses(Number(($event.target as HTMLInputElement).value))"
                        />
                      </label>
                    </div>
                    <p class="agent-context-policy-hint">
                      {{ t("agents.contextAdvancedHint") }}
                    </p>
                    <div class="agent-context-aap-disclosure">
                      <div class="agent-context-aap-toggle">
                        <div>
                          <p>{{ t("agents.aapCompactSummary") }}</p>
                          <small>{{ t("agents.aapCompactSummaryHint") }}</small>
                        </div>
                        <button
                          type="button"
                          role="switch"
                          :aria-label="t('agents.aapCompactSummary')"
                          :aria-checked="agentContextIncludeCompactionSummary"
                          :class="{ active: agentContextIncludeCompactionSummary }"
                          @click="setAgentContextIncludeCompactionSummary(!agentContextIncludeCompactionSummary)"
                        >
                          <span />
                        </button>
                      </div>
                      <p class="agent-context-policy-hint agent-context-permanence-warning">
                        {{ t("agents.compactionSummaryPermanenceWarning") }}
                      </p>
                    </div>
                  </div>
                </template>
              </div>
            </div>
          </section>

          <AgentDelegationPanel
            v-if="studioMode === 'edit' && draftAgent.id && draftAgent.workspaceId"
            :workspace-id="draftAgent.workspaceId"
            :agent-id="draftAgent.id"
            :agent-options="delegationAgentOptions"
          />
          <!-- Create mode: bindings need a persisted agentId — show deferred hint only. -->
          <section v-else-if="studioMode === 'create'" class="agent-studio-section agent-delegation-deferred">
            <header>
              <span><i class="fa-solid fa-sitemap" aria-hidden="true" /> {{ t("agents.collabExternal") }}</span>
              <span class="agent-delegation-deferred-badge">{{ t("agents.configureAfterCreate") }}</span>
            </header>
            <div class="agent-delegation-deferred-body">
              <i class="fa-solid fa-lock" aria-hidden="true" />
              <div>
                <p>{{ t("agents.collabDeferredBody") }}</p>
                <small>{{ t("agents.collabDeferredHint") }}</small>
              </div>
            </div>
          </section>

          <section class="agent-studio-section studio-prompt-editor">
            <header>
              <span
                ><i class="fa-solid fa-code" aria-hidden="true" />
                {{ studioMode === "create" ? t("agents.initialSystemPrompt") : t("agents.aiRewriteRequest") }}
                <b v-if="studioMode === 'create'" class="required-mark" aria-hidden="true">*</b></span
              >
              <button
                class="agent-weave-button"
                type="button"
                :disabled="!canEnhanceDraftPrompt"
                :title="canEnhanceDraftPrompt ? t('agents.aiEnhanceTitle') : t('agents.aiEnhanceDisabled')"
                aria-describedby="agent-weave-helper"
                @click="enhancePrompt"
              >
                <i
                  :class="[
                    'fa-solid',
                    isEnhancing(draftAgent.id || 'create-draft') ? 'fa-spinner fa-spin' : 'fa-wand-magic-sparkles',
                  ]"
                  aria-hidden="true"
                />
                <span>{{ t("agents.aiEnhance") }}</span>
              </button>
            </header>
            <div
              class="agent-prompt-overview"
              :aria-label="
                studioMode === 'create' ? t('agents.promptPreviewAriaCreate') : t('agents.promptPreviewAriaEdit')
              "
            >
              <div>
                <strong>{{ t("agents.firstParagraphPreview") }}</strong>
                <p class="agent-prompt-preview-text">{{ promptPreviewText }}</p>
              </div>
              <dl>
                <div>
                  <dt>{{ t("agents.lineCount") }}</dt>
                  <dd>{{ promptLineCount }}</dd>
                </div>
                <div>
                  <dt>{{ t("agents.charCount") }}</dt>
                  <dd>{{ draftAgent.systemPrompt?.length || 0 }}</dd>
                </div>
              </dl>
            </div>
            <div
              class="agent-prompt-editor-box"
              :class="{ 'is-weaving': isEnhancing(draftAgent.id || 'create-draft') }"
            >
              <textarea
                v-model="draftAgent.systemPrompt"
                rows="8"
                :required="studioMode === 'create'"
                :aria-required="studioMode === 'create'"
                :aria-label="studioMode === 'create' ? t('agents.systemPrompt') : t('agents.aiRewriteRequest')"
                :aria-invalid="Boolean(agentPromptError)"
                :aria-describedby="agentPromptError ? 'agent-prompt-error agent-weave-helper' : 'agent-weave-helper'"
                :placeholder="studioMode === 'create' ? t('agents.promptPhCreate') : t('agents.promptPhEdit')"
              />
            </div>
            <div class="agent-prompt-meter">
              <span
                ><i class="fa-solid fa-calculator" aria-hidden="true" /> {{ t("agents.charLength") }}:
                <strong>{{ draftAgent.systemPrompt?.length || 0 }}</strong></span
              >
              <span id="agent-weave-helper">{{
                studioMode === "create" || !draftAgent.id ? t("agents.weaveHelperCreate") : t("agents.weaveHelperEdit")
              }}</span>
            </div>
            <small v-if="agentPromptError" id="agent-prompt-error" class="field-error agent-prompt-error">{{
              agentPromptError
            }}</small>
          </section>
        </div>
      </section>
    </div>
  </Transition>

  <Transition name="modal-fade">
    <div
      v-if="pendingPromptSaveReview"
      class="modal-backdrop agent-prompt-save-review-modal"
      @click.self="cancelPromptSaveReview"
      @keydown.esc="cancelPromptSaveReview"
      @keydown="trapAgentModalFocus"
    >
      <section
        ref="promptDetailDialogRef"
        class="modal-card agent-prompt-save-review-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="t('agents.promptSaveReviewAria')"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-shield-halved" aria-hidden="true" />
            <span>
              <strong>{{ t("agents.promptSaveReviewTitle") }}</strong>
              <small>AGENT: {{ pendingPromptSaveReview.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :title="t('common.close')"
            :aria-label="t('agents.closePromptReview')"
            @click="cancelPromptSaveReview"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert">
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            {{ t("agents.promptSaveReviewAlert") }}
          </p>
          <div class="agent-prompt-diff-summary">
            <span
              ><b>{{ promptSaveDiff.beforeChars }}</b> {{ t("agents.charsOriginal") }}</span
            >
            <span
              ><b>{{ promptSaveDiff.afterChars }}</b> {{ t("agents.charsNew") }}</span
            >
            <span
              ><b>{{ formatSignedDelta(promptSaveDiff.charDelta) }}</b> {{ t("agents.charsDelta") }}</span
            >
            <span
              ><b>{{ formatSignedDelta(promptSaveDiff.lineDelta) }}</b> {{ t("agents.linesDelta") }}</span
            >
          </div>
          <AgentPromptDiffViewer
            :before="originalPrompt"
            :after="pendingPromptText"
            :before-label="t('agents.historyVersion')"
            :after-label="t('agents.currentDraft')"
            :title="t('agents.diffTitle')"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="savingAgent" @click="cancelPromptSaveReview">
            {{ t("agents.backToEdit") }}
          </button>
          <button class="primary-button" type="button" :disabled="savingAgent" @click="confirmPromptSaveReview">
            <i :class="['fa-solid', savingAgent ? 'fa-spinner fa-spin' : 'fa-circle-check']" aria-hidden="true" />
            <span>{{ savingAgent ? t("agents.saving") : t("agents.confirmSaveApply") }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Transition>

  <Transition name="modal-fade">
    <div
      v-if="weavePreviewAgent"
      class="modal-backdrop agent-weave-preview-modal"
      @click.self="cancelWeavePreview"
      @keydown.esc="cancelWeavePreview"
      @keydown="trapAgentModalFocus"
    >
      <section
        ref="promptDetailDialogRef"
        class="modal-card agent-weave-preview-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="t('agents.weavePreviewAria')"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />
            <span>
              <strong>{{ t("agents.weavePreviewTitle") }}</strong>
              <small>AGENT: {{ draftAgent.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :title="t('common.close')"
            :aria-label="t('agents.closeWeavePreview')"
            @click="cancelWeavePreview"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert neutral">
            <i class="fa-solid fa-circle-info" aria-hidden="true" />
            {{ t("agents.weavePreviewAlert") }}
          </p>
          <div class="agent-prompt-diff-summary">
            <span
              ><b>{{ weavePreviewDiff.beforeChars }}</b> {{ t("agents.charsCurrent") }}</span
            >
            <span
              ><b>{{ weavePreviewDiff.afterChars }}</b> {{ t("agents.charsPreview") }}</span
            >
            <span
              ><b>{{ formatSignedDelta(weavePreviewDiff.charDelta) }}</b> {{ t("agents.charsDelta") }}</span
            >
            <span
              ><b>{{ formatSignedDelta(weavePreviewDiff.lineDelta) }}</b> {{ t("agents.linesDelta") }}</span
            >
          </div>
          <AgentPromptDiffViewer
            :before="draftAgent.systemPrompt || ''"
            :after="weavePreviewAgent.output || ''"
            :before-label="t('agents.currentRequest')"
            :after-label="t('agents.aiSuggestion')"
            :title="t('agents.diffTitle')"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" @click="cancelWeavePreview">{{ t("common.cancel") }}</button>
          <button class="primary-button" type="button" :disabled="acceptingPromptRevision" @click="applyWeavePreview">
            <i
              :class="acceptingPromptRevision ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-check'"
              aria-hidden="true"
            />
            <span>{{
              acceptingPromptRevision
                ? studioMode === "create"
                  ? t("agents.applying")
                  : t("agents.accepting")
                : studioMode === "create"
                  ? t("agents.applyToDraft")
                  : t("agents.acceptAsNewVersion")
            }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Transition>
</template>
