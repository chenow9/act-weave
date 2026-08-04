<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 12)
/** Tool editor panel (ZKL-64 item 12). */
import { useI18n } from "vue-i18n";
import AppSelect from "./AppSelect.vue";
import ToolContractHybridEditor from "./ToolContractHybridEditor.vue";
import ToolFlatContractEditor from "./ToolFlatContractEditor.vue";
import { useToolsPageContext } from "../composables/useToolsPageContext";

/* eslint-disable @typescript-eslint/no-unused-vars -- inject surface for template */
const { t } = useI18n();
const scp = useToolsPageContext();
const {
  router,
  toolEditorVisible,
  toolEditorModalRef,
  toolEditorTitle,
  draftStep,
  toolEditorSteps,
  draftTool,
  draftError,
  saveState,
  saveStateLabel,
  hasUnsavedToolChanges,
  runtimeAdvancedOpen,
  contractEditorTab,
  contractEditorTabs,
  methodOptions,
  contentTypeOptions,
  backoffPolicyOptions,
  rateLimitPolicyOptions,
  toolStatusOptions,
  toolStatusHelperText,
  workspaceOptions,
  serviceConnectionOptions,
  draftConnection,
  requestTransportContract,
  requestBodyContract,
  responseBodyContract,
  activeRequestFlatContract,
  requestContractCount,
  responseContractCount,
  completedBaseRequiredCount,
  draftSuggestionCount,
  draftCompletionPercent,
  endpointPreviewLabel,
  connectionDomainLabel,
  connectionBasePathLabel,
  authModeLabel,
  serviceConnectionStatusLabel,
  environmentLabel,
  backoffPolicyMeta,
  workspaceDisplayLabel,
  statusClass,
  toolStatusLabel,
  draftStepState,
  draftStepCanProceed,
  isDraftStepComplete,
  goToDraftStep,
  goPreviousStep,
  goNextStep,
  closeToolEditor,
  persistDraftTool,
  saveDraftTool,
  contractEditorTabCount,
  contractEditorHint,
  addErrorMapping,
  removeErrorMapping,
} = scp;
void AppSelect;
void ToolContractHybridEditor;
void ToolFlatContractEditor;
/* eslint-enable @typescript-eslint/no-unused-vars */
</script>

<template>
  <div
    v-if="toolEditorVisible"
    class="modal-backdrop tool-editor-backdrop tool-registration-workspace"
    @click.self="closeToolEditor"
  >
    <section
      ref="toolEditorModalRef"
      class="modal-card tool-editor-modal-card tool-registration-card tool-hybrid-registration-card"
      role="dialog"
      aria-modal="true"
      :aria-label="toolEditorTitle"
    >
      <div class="tool-hybrid-topbar">
        <div class="tool-hybrid-title-block">
          <span class="tool-hybrid-title-icon" aria-hidden="true"><i class="fa-solid fa-screwdriver-wrench" /></span>
          <div>
            <h3>{{ toolEditorTitle }}</h3>
            <p>{{ t("tools.editorSubtitle") }}</p>
          </div>
        </div>
        <nav class="tool-hybrid-progress" :aria-label="t('tools.createStepsAria')">
          <template v-for="(step, index) in toolEditorSteps" :key="step[0]">
            <button
              :class="draftStepState(index + 1)"
              :aria-current="draftStep === index + 1 ? 'step' : undefined"
              type="button"
              @click="goToDraftStep(index + 1)"
            >
              <b
                ><i v-if="draftStepState(index + 1) === 'done'" class="fa-solid fa-check" />{{
                  draftStepState(index + 1) === "done" ? "" : index + 1
                }}</b
              >
              <span>{{ step[0] }}</span>
            </button>
            <i v-if="index < toolEditorSteps.length - 1" class="tool-hybrid-step-bar" />
          </template>
        </nav>
        <button
          class="tool-hybrid-close"
          type="button"
          :aria-label="t('tools.closeEditorAria', { title: toolEditorTitle })"
          data-modal-initial-focus
          @click="closeToolEditor"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </div>

      <div class="tool-step-panel tool-hybrid-step-panel" :class="{ 'is-contract-step': draftStep === 2 }">
        <template v-if="draftStep === 1">
          <div class="tool-hybrid-basics-layout">
            <div class="tool-hybrid-form-stack">
              <section class="tool-hybrid-form-section">
                <div class="tool-hybrid-section-head">
                  <div><span>01</span><strong>{{ t("tools.sectionBasics") }}</strong></div>
                  <small>{{ t("tools.sectionBasicsHelp") }}</small>
                </div>
                <label class="drawer-field"
                  ><span>{{ t("tools.toolName") }} <b>*</b></span
                  ><input v-model="draftTool.name" :placeholder="t('tools.namePlaceholder')"
                /></label>
                <label class="drawer-field">
                  <span>{{ t("tools.workspace") }} <b>*</b></span>
                  <AppSelect
                    v-model="draftTool.workspaceId"
                    :options="workspaceOptions"
                    :placeholder="t('tools.selectWorkspace')"
                  />
                </label>
              </section>

              <section class="tool-hybrid-form-section">
                <div class="tool-hybrid-section-head">
                  <div><span>02</span><strong>{{ t("tools.sectionAction") }}</strong></div>
                  <small>{{ t("tools.sectionActionHelp") }}</small>
                </div>
                <div class="tool-endpoint-fields">
                  <label class="drawer-field tool-method-field"
                    ><span>Method <b>*</b></span
                    ><AppSelect v-model="draftTool.method" :options="methodOptions"
                  /></label>
                  <label class="drawer-field"
                    ><span>Endpoint Path <b>*</b></span
                    ><input v-model="draftTool.path" class="mono" placeholder="/api/resource/{id}"
                  /></label>
                  <label class="drawer-field"
                    ><span>Content-Type</span><AppSelect v-model="draftTool.contentType" :options="contentTypeOptions"
                  /></label>
                </div>
                <label class="drawer-field"
                  ><span>{{ t("tools.actionDescription") }}</span
                  ><textarea
                    v-model="draftTool.description"
                    rows="3"
                    :placeholder="t('tools.descriptionPlaceholder')"
                  />
                </label>
                <div class="tool-endpoint-preview">
                  <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
                  <strong>{{ endpointPreviewLabel() }}</strong>
                </div>
              </section>
            </div>

            <aside class="connection-reference-card tool-connection-summary-card tool-hybrid-connection-card">
              <label class="drawer-field">
                <span>{{ t("tools.serviceConnection") }} <b>*</b></span>
                <AppSelect
                  v-model="draftTool.connectionId"
                  :options="serviceConnectionOptions"
                  :placeholder="t('tools.selectServiceConnection')"
                />
              </label>
              <div class="connection-reference-head tool-connection-summary-head">
                <i class="fa-solid fa-server" />
                <div>
                  <strong>{{ draftConnection?.name || t("tools.noConnectionSelected") }}</strong>
                  <small>{{ t("tools.inheritConnectionHelp") }}</small>
                </div>
                <span
                  class="status-pill tool-connection-summary-status"
                  :class="statusClass(draftConnection?.status || 'Disabled')"
                  >{{ draftConnection?.status || t("tools.connNotConfigured") }}</span
                >
              </div>
              <div class="tool-connection-summary-grid">
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-globe" />
                  <div>
                    <span>{{ t("tools.serviceDomain") }}</span
                    ><strong class="tool-connection-summary-value mono">{{
                      connectionDomainLabel(draftConnection)
                    }}</strong>
                  </div>
                </div>
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-route" />
                  <div>
                    <span>Base Path</span
                    ><strong class="tool-connection-summary-value mono">{{
                      connectionBasePathLabel(draftConnection)
                    }}</strong>
                  </div>
                </div>
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-key" />
                  <div>
                    <span>{{ t("tools.authMode") }}</span
                    ><strong class="tool-connection-summary-value">{{ authModeLabel(draftConnection) }}</strong>
                  </div>
                </div>
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-layer-group" />
                  <div>
                    <span>{{ t("tools.environment") }}</span
                    ><strong class="tool-connection-summary-value">{{
                      environmentLabel(draftConnection?.environment || "")
                    }}</strong>
                  </div>
                </div>
              </div>
              <div class="tool-status-readonly tool-hybrid-draft-note">
                <span class="status-pill" :class="statusClass(draftTool.status)">{{
                  toolStatusLabel(draftTool.status)
                }}</span>
                <small>{{ t("tools.draftAfterSave") }}</small>
              </div>
              <button
                class="ghost-button full tool-connection-summary-action"
                type="button"
                @click="router.push('/connections')"
              >
                {{ t("tools.manageConnections") }}
              </button>
            </aside>
          </div>
        </template>

        <template v-else-if="draftStep === 2">
          <div class="tool-contract-context-bar">
            <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
            <strong class="mono">{{ draftTool.path || "/" }}</strong>
            <i />
            <span
              >{{ t("tools.inheritConnection")
              }}<b>{{ draftConnection?.name || t("tools.noConnectionSelected") }}</b></span
            >
            <i />
            <span>Capability Binding：<b>{{ t("tools.capabilityBindingAfterPublish") }}</b></span>
            <button type="button" @click="goToDraftStep(1)">{{ t("tools.editEndpoint") }}</button>
          </div>

          <div class="tool-contract-body-wrap">
            <aside class="tool-contract-side-tabs">
              <div role="tablist" :aria-label="t('tools.contractGroupsAria')">
                <button
                  v-for="tab in contractEditorTabs"
                  :key="tab.value"
                  type="button"
                  role="tab"
                  :aria-selected="contractEditorTab === tab.value"
                  :class="{
                    active: contractEditorTab === tab.value,
                    supplemental: tab.value === 'Response' || tab.value === 'Errors',
                    'section-start': tab.value === 'Response',
                  }"
                  @click="contractEditorTab = tab.value"
                >
                  <span>{{ tab.label }}</span
                  ><b>{{ contractEditorTabCount(tab.value) }}</b>
                </button>
              </div>
              <p>{{ contractEditorHint(contractEditorTab) }}</p>
            </aside>

            <section
              class="tool-contract-main-panel"
              role="tabpanel"
              :aria-label="t('tools.contractPanelAria', { tab: contractEditorTab })"
            >
              <ToolFlatContractEditor
                v-if="contractEditorTab === 'Path' || contractEditorTab === 'Query' || contractEditorTab === 'Header'"
                v-model="activeRequestFlatContract"
                :location="contractEditorTab"
              />
              <ToolContractHybridEditor
                v-else-if="contractEditorTab === 'Body'"
                v-model="requestBodyContract"
                :title="t('tools.requestBodyTitle')"
                :description="t('tools.requestBodyDesc')"
                root-label="Request Body Contract"
                compact
              />
              <ToolContractHybridEditor
                v-else-if="contractEditorTab === 'Response'"
                v-model="responseBodyContract"
                :title="t('tools.successResponseTitle')"
                :description="t('tools.successResponseDesc')"
                root-label="Response Contract"
                compact
              />
              <div v-else class="tool-error-mapping-panel">
                <div class="tool-error-mapping-head">
                  <div>
                    <strong>{{ t("tools.errorMappingTitle") }}</strong
                    ><span>{{ t("tools.errorMappingDesc") }}</span>
                  </div>
                  <button type="button" @click="addErrorMapping">
                    <i class="fa-solid fa-plus" /> {{ t("tools.addMapping") }}
                  </button>
                </div>
                <div v-if="draftTool.errorMappings.length" class="tool-error-mapping-table">
                  <div class="tool-error-mapping-row tool-error-mapping-header">
                    <span>HTTP Status</span><span>Error Code</span><span>{{ t("tools.agentAdvice") }}</span
                    ><span>{{ t("tools.actions") }}</span>
                  </div>
                  <div
                    v-for="(mapping, index) in draftTool.errorMappings"
                    :key="`${index}-${mapping.errorCode}`"
                    class="tool-error-mapping-row"
                  >
                    <input
                      v-model="mapping.protocolStatus"
                      inputmode="numeric"
                      aria-label="HTTP Status"
                      placeholder="409"
                    />
                    <input
                      v-model="mapping.errorCode"
                      class="mono"
                      aria-label="Error Code"
                      placeholder="STATE_LOCKED"
                    />
                    <input
                      v-model="mapping.agentAdvice"
                      :aria-label="t('tools.agentAdvice')"
                      :placeholder="t('tools.agentAdvicePlaceholder')"
                    />
                    <button
                      class="tool-flat-delete"
                      type="button"
                      :aria-label="t('tools.deleteErrorMappingAria', { name: mapping.errorCode || index + 1 })"
                      @click="removeErrorMapping(index)"
                    >
                      <i class="fa-solid fa-xmark" />
                    </button>
                  </div>
                </div>
                <div v-else class="tool-schema-empty"><span>{{ t("tools.noErrorMappings") }}</span></div>
              </div>
            </section>
          </div>
        </template>

        <template v-else>
          <div class="tool-review-heading">
            <div><span>03</span><strong>{{ t("tools.confirmSaveDraft") }}</strong></div>
            <small>{{ t("tools.confirmSaveDraftHelp") }}</small>
          </div>
          <div class="tool-review-summary-grid">
            <section>
              <i class="fa-solid fa-wand-magic-sparkles" />
              <div>
                <span>Tool</span><strong>{{ draftTool.name || t("tools.unnamedTool") }}</strong
                ><small
                  >{{ workspaceDisplayLabel(draftTool.workspaceId) }} ·
                  {{ t("tools.capabilityBindingIndependent") }}</small
                >
              </div>
              <button type="button" @click="goToDraftStep(1)">{{ t("common.edit") }}</button>
            </section>
            <section>
              <i class="fa-solid fa-link" />
              <div>
                <span>Endpoint</span><strong class="mono">{{ draftTool.method }} {{ draftTool.path || "/" }}</strong
                ><small>{{ draftConnection?.name || t("tools.noConnectionSelected") }}</small>
              </div>
              <button type="button" @click="goToDraftStep(1)">{{ t("common.edit") }}</button>
            </section>
            <section>
              <i class="fa-solid fa-diagram-project" />
              <div>
                <span>{{ t("tools.contract") }}</span
                ><strong>{{
                  t("tools.contractIoSummary", { request: requestContractCount, response: responseContractCount })
                }}</strong
                ><small>{{ t("tools.errorMappingCount", { n: draftTool.errorMappings.length }) }}</small>
              </div>
              <button type="button" @click="goToDraftStep(2)">{{ t("common.edit") }}</button>
            </section>
            <section>
              <i class="fa-solid fa-gauge-high" />
              <div>
                <span>{{ t("tools.runtimePolicy") }}</span
                ><strong>{{
                  t("tools.runtimeSummary", { timeout: draftTool.timeoutSeconds, retry: draftTool.retryCount })
                }}</strong
                ><small>{{ backoffPolicyMeta(draftTool.backoffPolicy).label }} · {{ draftTool.rateLimitPolicy }}</small>
              </div>
              <button type="button" @click="runtimeAdvancedOpen = !runtimeAdvancedOpen">
                {{ runtimeAdvancedOpen ? t("tools.collapse") : t("tools.configure") }}
              </button>
            </section>
          </div>

          <section class="tool-runtime-disclosure" :class="{ open: runtimeAdvancedOpen }">
            <button
              type="button"
              :aria-expanded="runtimeAdvancedOpen"
              @click="runtimeAdvancedOpen = !runtimeAdvancedOpen"
            >
              <span
                ><i class="fa-solid fa-sliders" /><strong>{{ t("tools.advancedRuntime") }}</strong
                ><small>{{ t("tools.advancedRuntimeHelp") }}</small></span
              >
              <i :class="runtimeAdvancedOpen ? 'fa-solid fa-chevron-up' : 'fa-solid fa-chevron-down'" />
            </button>
            <div v-if="runtimeAdvancedOpen" class="tool-runtime-policy-inline">
              <div class="form-two">
                <label class="drawer-field"
                  ><span>{{ t("tools.timeoutSeconds") }}</span
                  ><input v-model.number="draftTool.timeoutSeconds" type="number" min="1"
                /></label>
                <label class="drawer-field"
                  ><span>{{ t("tools.retryCount") }}</span
                  ><input v-model.number="draftTool.retryCount" type="number" min="0"
                /></label>
              </div>
              <div class="form-two">
                <label class="drawer-field"
                  ><span>{{ t("tools.backoffPolicy") }}</span
                  ><AppSelect
                    v-model="draftTool.backoffPolicy"
                    :options="backoffPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
                /></label>
                <label class="drawer-field"
                  ><span>{{ t("tools.rateLimitPolicy") }}</span
                  ><AppSelect
                    v-model="draftTool.rateLimitPolicy"
                    :options="rateLimitPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
                /></label>
              </div>
              <label class="drawer-field"
                ><span>{{ t("tools.idempotencyPolicy") }}</span
                ><input v-model="draftTool.idempotencyPolicy"
              /></label>
            </div>
          </section>

          <div class="tool-draft-save-note">
            <i class="fa-solid fa-circle-info" />
            <div>
              <strong>{{ t("tools.saveAsDraftNoteTitle") }}</strong
              ><span>{{ t("tools.saveAsDraftNoteBody") }}</span>
            </div>
          </div>
        </template>
      </div>

      <p v-if="draftError" class="form-error tool-hybrid-form-error" role="alert">{{ draftError }}</p>

      <div class="tool-hybrid-footer">
        <div class="tool-hybrid-completion">
          <span
            >{{ t("tools.completion") }} <b>{{ draftCompletionPercent }}%</b></span
          ><i><b :style="{ width: `${draftCompletionPercent}%` }" /></i>
        </div>
        <div class="tool-hybrid-stat">
          {{ t("tools.baseRequired") }} <b>{{ completedBaseRequiredCount }}/5</b>
        </div>
        <div v-if="draftSuggestionCount" class="tool-hybrid-stat warning">
          <i />{{ t("tools.suggestCheck", { n: draftSuggestionCount }) }}
        </div>
        <div v-else class="tool-hybrid-stat">
          <i class="fa-solid fa-circle-check" />{{ t("tools.contractConfigured") }}
        </div>
        <span class="tool-editor-action-spacer" />
        <button class="ghost" type="button" @click="closeToolEditor">{{ t("common.cancel") }}</button>
        <button type="button" :disabled="saveState === 'saving'" @click="persistDraftTool(false)">
          {{ t("tools.saveDraft") }}
        </button>
        <button type="button" :disabled="draftStep === 1" @click="goPreviousStep">
          {{ t("tools.previousStep") }}
        </button>
        <button
          v-if="draftStep < toolEditorSteps.length"
          class="primary"
          type="button"
          :disabled="!draftStepCanProceed()"
          @click="goNextStep"
        >
          {{ t("tools.nextStep") }}
        </button>
        <button
          v-else
          class="primary"
          type="button"
          :disabled="!isDraftStepComplete(1) || !isDraftStepComplete(2) || saveState === 'saving'"
          @click="saveDraftTool"
        >
          {{ t("tools.finish") }}
        </button>
      </div>
    </section>
  </div>
</template>
