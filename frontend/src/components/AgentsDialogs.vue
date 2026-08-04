<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents dialogs (ZKL-64 item 16). */

import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAgentsPageContext } from "../composables/useAgentsPageContext";

const { t } = useI18n();
const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  agentActionNote, agentActionTone, promptDetailAgent, agentDeleting, agentDeleteTarget, agentDeleteConfirmName, promptDetailDialogRef, agentDeleteDialogRef, agentDeleteInputRef, capabilityAgent, capabilityLoading, capabilitySavingId, capabilityBatchBusy, capabilityDrafts, promptDetailVisible, capabilityCatalog, canConfirmAgentDelete,
  capabilitySelectedCount, capabilityUnboundCount, capabilityBindableUnboundCount, capabilitySelectedBoundCount, capabilityActionsBusy,
  agentDeleteNameError, closePromptDetail, trapAgentModalFocus, clearAgentToast, closeAgentDeleteConfirm, requestCloseAgentDeleteConfirm, confirmDeleteAgent, closeCapabilityBindings, currentCapabilityBinding, setCapabilityVersionPolicy, capabilityVersionPolicyOptions, saveCapabilityBinding, removeCapabilityBinding,
  isCapabilitySelected, toggleCapabilitySelection, clearCapabilitySelection, selectUnboundCapabilities, selectAllCapabilities, batchBindCapabilities, batchUnbindCapabilities,
  currentPromptBody, currentPromptMeta, currentPromptLoading, currentPromptError, promptDetailHTML
} = scp;

const promptTab = ref<"render" | "raw">("render");
const copyFeedback = ref("");

// Always open on rendered preview; clear copy feedback when dialog closes.
watch(promptDetailVisible, (visible) => {
  if (visible) {
    promptTab.value = "render";
  }
  copyFeedback.value = "";
});

async function copyPromptRaw() {
  const text = currentPromptBody.value || "";
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    copyFeedback.value = t("agents.copiedRaw");
  } catch {
    promptTab.value = "raw";
    copyFeedback.value = t("agents.copyRawManual");
  }
  window.setTimeout(() => {
    copyFeedback.value = "";
  }, 2500);
}
</script>

<template>
  <Transition name="modal-fade">
    <div
      v-if="promptDetailVisible"
      class="modal-backdrop agent-prompt-detail-modal"
      @click.self="closePromptDetail"
      @keydown.esc="closePromptDetail"
      @keydown="trapAgentModalFocus"
    >
      <section
        ref="promptDetailDialogRef"
        class="modal-card agent-prompt-detail-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="
          promptDetailAgent
            ? t('agents.promptDetailAriaNamed', { name: promptDetailAgent.name })
            : t('agents.systemPrompt')
        "
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-rectangle-list" aria-hidden="true" />
            <span>
              <strong>{{ t("agents.systemPrompt") }}</strong>
              <small>{{ promptDetailAgent?.name }} · {{ promptDetailAgent?.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :title="t('common.close')"
            :aria-label="t('agents.closePromptDetail')"
            @click="closePromptDetail"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-prompt-revision-readonly">
          <div v-if="currentPromptLoading" class="agent-prompt-state" aria-live="polite">
            {{ t("agents.loadingPrompt") }}
          </div>
          <div v-else-if="currentPromptError" class="agent-prompt-state agent-prompt-state-error" aria-live="polite">
            {{ currentPromptError }}
          </div>
          <template v-else-if="currentPromptBody">
            <div class="agent-prompt-meta">
              <span>{{ t("agents.promptVersion", { n: currentPromptMeta?.revisionNo || "—" }) }}</span>
              <span>{{ t("agents.promptSource", { source: currentPromptMeta?.source || "—" }) }}</span>
              <span>{{ t("agents.promptUpdatedAt", { at: currentPromptMeta?.createdAt || "—" }) }}</span>
            </div>
            <div class="agent-prompt-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                :aria-selected="promptTab === 'render'"
                :class="{ active: promptTab === 'render' }"
                @click="promptTab = 'render'"
              >
                {{ t("agents.renderPreview") }}
              </button>
              <button
                type="button"
                role="tab"
                :aria-selected="promptTab === 'raw'"
                :class="{ active: promptTab === 'raw' }"
                @click="promptTab = 'raw'"
              >
                {{ t("agents.viewRaw") }}
              </button>
              <button type="button" class="text-button" @click="copyPromptRaw">{{ t("agents.copyRaw") }}</button>
              <span v-if="copyFeedback" class="agent-prompt-copy-feedback" aria-live="polite">{{ copyFeedback }}</span>
            </div>
            <div v-if="promptTab === 'render'" class="agent-prompt-markdown" v-html="promptDetailHTML" />
            <pre v-else class="agent-prompt-raw"><code>{{ currentPromptBody }}</code></pre>
          </template>
          <div v-else class="agent-prompt-state">{{ t("agents.noPromptDisplayable") }}</div>
        </div>
        <footer class="agent-prompt-detail-footer">
          <span>{{ t("agents.lockVersionLabel", { n: promptDetailAgent?.lockVersion || 0 }) }}</span>
          <button class="primary-button" type="button" @click="closePromptDetail">{{ t("common.close") }}</button>
        </footer>
      </section>
    </div>
  </Transition>

  <Transition name="modal-fade">
    <div
      v-if="capabilityAgent"
      class="modal-backdrop agent-capability-modal"
      @click.self="closeCapabilityBindings"
      @keydown.esc="closeCapabilityBindings"
      @keydown="trapAgentModalFocus"
    >
      <section
        class="modal-card agent-capability-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="t('agents.capabilityBindingsAria', { name: capabilityAgent.name })"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-link" aria-hidden="true" />
            <span
              ><strong>{{ t("agents.capabilities") }}</strong
              ><small>AGENT: {{ capabilityAgent.id }}</small></span
            >
          </div>
          <button
            class="icon-action-button"
            type="button"
            :aria-label="t('agents.closeCapabilityBindings')"
            :disabled="capabilityActionsBusy"
            @click="closeCapabilityBindings"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <div class="agent-capability-body">
          <p>
            {{ t("agents.capabilityDialogIntro") }}
          </p>

          <div v-if="!capabilityLoading && capabilityCatalog.length" class="agent-capability-batch-bar">
            <div class="agent-capability-batch-meta">
              <span>{{
                t("agents.unboundBindableMeta", {
                  unbound: capabilityUnboundCount,
                  bindable: capabilityBindableUnboundCount,
                })
              }}</span>
              <span v-if="capabilitySelectedCount">{{ t("agents.selectedMeta", { n: capabilitySelectedCount }) }}</span>
            </div>
            <div class="agent-capability-batch-actions">
              <button
                class="ghost-button"
                type="button"
                data-action="select-unbound-capabilities"
                :disabled="capabilityActionsBusy || capabilityBindableUnboundCount === 0"
                @click="selectUnboundCapabilities"
              >
                {{ t("agents.selectAllUnbound") }}
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="select-all-capabilities"
                :disabled="capabilityActionsBusy || !capabilityCatalog.length"
                @click="selectAllCapabilities"
              >
                {{ t("agents.selectAll") }}
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="clear-capability-selection"
                :disabled="capabilityActionsBusy || capabilitySelectedCount === 0"
                @click="clearCapabilitySelection"
              >
                {{ t("common.clearSelection") }}
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="batch-unbind-capabilities"
                :disabled="capabilityActionsBusy || capabilitySelectedBoundCount === 0"
                @click="batchUnbindCapabilities"
              >
                {{ t("agents.batchUnbind")
                }}{{ capabilitySelectedBoundCount ? ` (${capabilitySelectedBoundCount})` : "" }}
              </button>
              <button
                class="primary-button"
                type="button"
                data-action="batch-bind-selected-capabilities"
                :disabled="capabilityActionsBusy || capabilitySelectedCount === 0"
                @click="batchBindCapabilities({ mode: 'selected' })"
              >
                <i v-if="capabilityBatchBusy" class="fa-solid fa-spinner fa-spin" />
                {{ t("agents.batchBindSelected")
                }}{{ capabilitySelectedCount ? ` (${capabilitySelectedCount})` : "" }}
              </button>
              <button
                class="primary-button agent-capability-batch-bind-all"
                type="button"
                data-action="batch-bind-all-unbound"
                :disabled="capabilityActionsBusy || capabilityBindableUnboundCount === 0"
                :title="t('agents.batchBindAllTitle')"
                @click="batchBindCapabilities({ mode: 'all-unbound' })"
              >
                <i v-if="capabilityBatchBusy" class="fa-solid fa-spinner fa-spin" />
                {{ t("agents.batchBindAllUnbound")
                }}{{ capabilityBindableUnboundCount ? ` (${capabilityBindableUnboundCount})` : "" }}
              </button>
            </div>
          </div>

          <div v-if="capabilityLoading" class="agent-capability-empty">{{ t("agents.loadingCatalog") }}</div>
          <div v-else-if="!capabilityCatalog.length" class="agent-capability-empty">
            {{ t("agents.noPublishedCapabilities") }}
          </div>
          <article
            v-for="capability in capabilityCatalog"
            v-else
            :key="capability.id"
            class="agent-capability-item"
            :class="{
              'is-selected': isCapabilitySelected(capability.id),
              'is-bound': Boolean(currentCapabilityBinding(capability.id)),
            }"
          >
            <header>
              <label class="agent-capability-select">
                <input
                  type="checkbox"
                  :checked="isCapabilitySelected(capability.id)"
                  :disabled="capabilityActionsBusy"
                  :aria-label="t('agents.selectCapabilityNamed', { name: capability.name })"
                  @change="toggleCapabilitySelection(capability.id, ($event.target as HTMLInputElement).checked)"
                />
                <div>
                  <span>{{ capability.kind }}</span
                  ><strong>{{ capability.name }}</strong
                  ><small>{{ capability.description }}</small>
                </div>
              </label>
              <em>{{ currentCapabilityBinding(capability.id) ? t("agents.bound") : t("agents.unbound") }}</em>
            </header>
            <div v-if="capabilityDrafts[capability.id]" class="agent-capability-fields">
              <label class="modal-field select-field">
                <span>{{ t("agents.versionPolicy") }}</span>
                <AppSelect
                  :model-value="capabilityDrafts[capability.id].versionPolicy"
                  :options="capabilityVersionPolicyOptions(capability)"
                  :aria-label="t('agents.versionPolicyAria', { name: capability.name })"
                  @update:model-value="setCapabilityVersionPolicy(capability, String($event))"
                />
              </label>
              <label class="modal-field">
                <span>{{
                  capabilityDrafts[capability.id].versionPolicy === "PINNED"
                    ? t("agents.pinnedReleaseId")
                    : t("agents.activeReleaseId")
                }}</span>
                <input
                  :value="
                    capabilityDrafts[capability.id].versionPolicy === 'PINNED'
                      ? capability.activeReleaseId || ''
                      : capability.activeRelease?.releaseId || ''
                  "
                  class="mono"
                  disabled
                  readonly
                />
              </label>
              <label class="modal-field">
                <span>{{ t("agents.connectionIdOptional") }}</span>
                <input
                  v-model.trim="capabilityDrafts[capability.id].connectionId"
                  class="mono"
                  :placeholder="t('agents.connectionIdPlaceholder')"
                />
              </label>
              <label class="agent-capability-enabled"
                ><input v-model="capabilityDrafts[capability.id].enabled" type="checkbox" /><span>{{
                  t("agents.enableBinding")
                }}</span></label
              >
            </div>
            <footer>
              <button
                v-if="currentCapabilityBinding(capability.id)"
                class="ghost-button danger"
                type="button"
                :disabled="capabilityActionsBusy"
                @click="removeCapabilityBinding(capability)"
              >
                {{ t("agents.unbind") }}
              </button>
              <button
                class="primary-button"
                type="button"
                :disabled="capabilityActionsBusy"
                @click="saveCapabilityBinding(capability)"
              >
                <i v-if="capabilitySavingId === capability.id" class="fa-solid fa-spinner fa-spin" />{{
                  currentCapabilityBinding(capability.id) ? t("agents.updateBinding") : t("agents.bindCapability")
                }}
              </button>
            </footer>
          </article>
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="capabilityActionsBusy" @click="closeCapabilityBindings">
            {{ t("common.close") }}
          </button>
        </footer>
      </section>
    </div>
  </Transition>

  <Transition name="modal-fade">
    <div
      v-if="agentDeleteTarget"
      class="modal-backdrop agent-delete-backdrop"
      @click.self="requestCloseAgentDeleteConfirm('backdrop')"
      @keydown="trapAgentModalFocus"
    >
      <section
        ref="agentDeleteDialogRef"
        class="modal-card agent-delete-dialog"
        role="dialog"
        aria-modal="true"
        :aria-label="t('agents.deleteConfirmAria')"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            <span>
              <strong>{{ t("agents.deleteAgent") }}</strong>
              <small>AGENT: {{ agentDeleteTarget.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :title="t('common.close')"
            :aria-label="t('agents.closeDeleteConfirm')"
            @click="closeAgentDeleteConfirm"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-delete-body">
          <strong>{{ agentDeleteTarget.name }}</strong>
          <p>{{ t("agents.deleteAgentBody") }}</p>
          <div class="agent-delete-impact">
            <span
              ><b>{{ agentDeleteTarget.isDefault ? t("agents.yes") : t("agents.no") }}</b>
              {{ t("agents.defaultAgentLabel") }}</span
            >
            <span
              ><b>{{ agentDeleteTarget.toolsCount }}</b> Tool</span
            >
            <span
              ><b>{{ agentDeleteTarget.workflowsCount }}</b> Workflow</span
            >
          </div>
        </div>
        <label class="modal-field agent-delete-confirm-input">
          <span
            >{{ t("agents.typeNameToConfirm", { name: agentDeleteTarget.name }) }}</span
          >
          <input
            ref="agentDeleteInputRef"
            v-model.trim="agentDeleteConfirmName"
            autocomplete="off"
            :aria-invalid="agentDeleteConfirmName.length > 0 && !canConfirmAgentDelete"
            aria-describedby="agent-delete-name-helper agent-delete-name-error"
          />
          <small id="agent-delete-name-helper">{{ t("agents.deleteNameHelper") }}</small>
          <small v-if="agentDeleteNameError" id="agent-delete-name-error" class="field-error">{{
            agentDeleteNameError
          }}</small>
        </label>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="agentDeleting" @click="closeAgentDeleteConfirm">
            {{ t("common.cancel") }}
          </button>
          <button
            class="primary-button danger"
            type="button"
            :disabled="agentDeleting || !canConfirmAgentDelete"
            @click="confirmDeleteAgent"
          >
            <i :class="['fa-solid', agentDeleting ? 'fa-spinner fa-spin' : 'fa-trash']" aria-hidden="true" />
            <span>{{ agentDeleting ? t("agents.deleting") : t("agents.deleteAgent") }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Transition>

  <div
    v-if="agentActionNote"
    :class="['action-toast', agentActionTone === 'error' && 'error']"
    role="status"
    aria-live="polite"
  >
    <i
      :class="agentActionTone === 'error' ? 'fa-solid fa-circle-exclamation' : 'fa-solid fa-circle-check'"
      aria-hidden="true"
    />
    <span>{{ agentActionNote }}</span>
    <button type="button" :aria-label="t('agents.closeToastAria')" @click="clearAgentToast">
      <i class="fa-solid fa-xmark" aria-hidden="true" />
    </button>
  </div>
</template>
