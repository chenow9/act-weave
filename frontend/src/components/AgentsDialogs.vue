<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents dialogs (ZKL-64 item 16). */

import { ref, watch } from "vue";
import { useAgentsPageContext } from "../composables/useAgentsPageContext";

const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  agentActionNote, agentActionTone, promptDetailAgent, agentDeleting, agentDeleteTarget, agentDeleteConfirmName, promptDetailDialogRef, agentDeleteDialogRef, agentDeleteInputRef, capabilityAgent, capabilityLoading, capabilitySavingId, capabilityBatchBusy, capabilityDrafts, promptDetailVisible, capabilityCatalog, canConfirmAgentDelete,
  capabilitySelectedCount, capabilityUnboundCount, capabilityBindableUnboundCount, capabilitySelectedBoundCount, capabilitySelectedUnboundCount, capabilityActionsBusy,
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
    copyFeedback.value = "已复制原文";
  } catch {
    promptTab.value = "raw";
    copyFeedback.value = "请手动复制原文";
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
        :aria-label="promptDetailAgent ? `${promptDetailAgent.name} · 系统提示词` : '系统提示词'"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-rectangle-list" aria-hidden="true" />
            <span>
              <strong>系统提示词</strong>
              <small>{{ promptDetailAgent?.name }} · {{ promptDetailAgent?.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭系统提示词"
            @click="closePromptDetail"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-prompt-revision-readonly">
          <div v-if="currentPromptLoading" class="agent-prompt-state" aria-live="polite">正在加载系统提示词…</div>
          <div v-else-if="currentPromptError" class="agent-prompt-state agent-prompt-state-error" aria-live="polite">
            {{ currentPromptError }}
          </div>
          <template v-else-if="currentPromptBody">
            <div class="agent-prompt-meta">
              <span>版本 #{{ currentPromptMeta?.revisionNo || "—" }}</span>
              <span>来源：{{ currentPromptMeta?.source || "—" }}</span>
              <span>更新时间：{{ currentPromptMeta?.createdAt || "—" }}</span>
            </div>
            <div class="agent-prompt-tabs" role="tablist">
              <button
                type="button"
                role="tab"
                :aria-selected="promptTab === 'render'"
                :class="{ active: promptTab === 'render' }"
                @click="promptTab = 'render'"
              >
                渲染预览
              </button>
              <button
                type="button"
                role="tab"
                :aria-selected="promptTab === 'raw'"
                :class="{ active: promptTab === 'raw' }"
                @click="promptTab = 'raw'"
              >
                查看原文
              </button>
              <button type="button" class="text-button" @click="copyPromptRaw">复制原文</button>
              <span v-if="copyFeedback" class="agent-prompt-copy-feedback" aria-live="polite">{{ copyFeedback }}</span>
            </div>
            <div
              v-if="promptTab === 'render'"
              class="agent-prompt-markdown"
              v-html="promptDetailHTML"
            />
            <pre v-else class="agent-prompt-raw"><code>{{ currentPromptBody }}</code></pre>
          </template>
          <div v-else class="agent-prompt-state">当前没有可显示的系统提示词。</div>
        </div>
        <footer class="agent-prompt-detail-footer">
          <span>锁版本：{{ promptDetailAgent?.lockVersion || 0 }}</span>
          <button class="primary-button" type="button" @click="closePromptDetail">关闭</button>
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
        :aria-label="`${capabilityAgent.name} 能力绑定`"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-link" aria-hidden="true" />
            <span
              ><strong>能力绑定</strong><small>AGENT: {{ capabilityAgent.id }}</small></span
            >
          </div>
          <button
            class="icon-action-button"
            type="button"
            aria-label="关闭能力绑定"
            :disabled="capabilityActionsBusy"
            @click="closeCapabilityBindings"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <div class="agent-capability-body">
          <p>
            Tool 与 Workflow 是 Workspace 级能力；此处只管理 Agent 的跟随/固定版本、连接选择和启用状态。支持勾选后批量绑定或解绑。
          </p>

          <div v-if="!capabilityLoading && capabilityCatalog.length" class="agent-capability-batch-bar">
            <div class="agent-capability-batch-meta">
              <span>未绑定 {{ capabilityUnboundCount }} · 可绑定 {{ capabilityBindableUnboundCount }}</span>
              <span v-if="capabilitySelectedCount">已选 {{ capabilitySelectedCount }}</span>
            </div>
            <div class="agent-capability-batch-actions">
              <button
                class="ghost-button"
                type="button"
                data-action="select-unbound-capabilities"
                :disabled="capabilityActionsBusy || capabilityBindableUnboundCount === 0"
                @click="selectUnboundCapabilities"
              >
                全选未绑定
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="select-all-capabilities"
                :disabled="capabilityActionsBusy || !capabilityCatalog.length"
                @click="selectAllCapabilities"
              >
                全选
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="clear-capability-selection"
                :disabled="capabilityActionsBusy || capabilitySelectedCount === 0"
                @click="clearCapabilitySelection"
              >
                清空
              </button>
              <button
                class="ghost-button"
                type="button"
                data-action="batch-unbind-capabilities"
                :disabled="capabilityActionsBusy || capabilitySelectedBoundCount === 0"
                @click="batchUnbindCapabilities"
              >
                批量解绑{{ capabilitySelectedBoundCount ? ` (${capabilitySelectedBoundCount})` : "" }}
              </button>
              <button
                class="primary-button"
                type="button"
                data-action="batch-bind-selected-capabilities"
                :disabled="capabilityActionsBusy || capabilitySelectedCount === 0"
                @click="batchBindCapabilities({ mode: 'selected' })"
              >
                <i v-if="capabilityBatchBusy" class="fa-solid fa-spinner fa-spin" />
                批量绑定选中{{ capabilitySelectedCount ? ` (${capabilitySelectedCount})` : "" }}
              </button>
              <button
                class="primary-button agent-capability-batch-bind-all"
                type="button"
                data-action="batch-bind-all-unbound"
                :disabled="capabilityActionsBusy || capabilityBindableUnboundCount === 0"
                title="将所有可绑定且尚未绑定的能力一次性绑定到此 Agent"
                @click="batchBindCapabilities({ mode: 'all-unbound' })"
              >
                <i v-if="capabilityBatchBusy" class="fa-solid fa-spinner fa-spin" />
                绑定全部未绑定{{
                  capabilityBindableUnboundCount ? ` (${capabilityBindableUnboundCount})` : ""
                }}
              </button>
            </div>
          </div>

          <div v-if="capabilityLoading" class="agent-capability-empty">正在加载能力目录…</div>
          <div v-else-if="!capabilityCatalog.length" class="agent-capability-empty">
            当前 Workspace 尚无已发布能力。
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
                  :aria-label="`选择 ${capability.name}`"
                  @change="
                    toggleCapabilitySelection(
                      capability.id,
                      ($event.target as HTMLInputElement).checked,
                    )
                  "
                />
                <div>
                  <span>{{ capability.kind }}</span
                  ><strong>{{ capability.name }}</strong
                  ><small>{{ capability.description }}</small>
                </div>
              </label>
              <em>{{ currentCapabilityBinding(capability.id) ? "已绑定" : "未绑定" }}</em>
            </header>
            <div v-if="capabilityDrafts[capability.id]" class="agent-capability-fields">
              <label class="modal-field select-field">
                <span>版本策略</span>
                <AppSelect
                  :model-value="capabilityDrafts[capability.id].versionPolicy"
                  :options="capabilityVersionPolicyOptions(capability)"
                  :aria-label="`${capability.name} 版本策略`"
                  @update:model-value="setCapabilityVersionPolicy(capability, String($event))"
                />
              </label>
              <label class="modal-field">
                <span>{{
                  capabilityDrafts[capability.id].versionPolicy === "PINNED" ? "固定版本 ID" : "当前生效版本"
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
                <span>连接 ID（可选）</span>
                <input
                  v-model.trim="capabilityDrafts[capability.id].connectionId"
                  class="mono"
                  placeholder="同 Workspace 且与能力提供方兼容"
                />
              </label>
              <label class="agent-capability-enabled"
                ><input v-model="capabilityDrafts[capability.id].enabled" type="checkbox" /><span
                  >启用该绑定</span
                ></label
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
                解绑
              </button>
              <button
                class="primary-button"
                type="button"
                :disabled="capabilityActionsBusy"
                @click="saveCapabilityBinding(capability)"
              >
                <i v-if="capabilitySavingId === capability.id" class="fa-solid fa-spinner fa-spin" />{{
                  currentCapabilityBinding(capability.id) ? "更新绑定" : "绑定能力"
                }}
              </button>
            </footer>
          </article>
        </div>
        <footer class="agent-prompt-detail-footer">
          <button
            class="ghost-button"
            type="button"
            :disabled="capabilityActionsBusy"
            @click="closeCapabilityBindings"
          >
            关闭
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
        aria-label="删除 Agent 确认"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            <span>
              <strong>删除 Agent</strong>
              <small>AGENT: {{ agentDeleteTarget.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭删除确认"
            @click="closeAgentDeleteConfirm"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-delete-body">
          <strong>{{ agentDeleteTarget.name }}</strong>
          <p>删除后会移除该 Agent，并影响其默认绑定、可用 Tool 和 Workflow 调度入口。此操作当前不可在页面内撤销。</p>
          <div class="agent-delete-impact">
            <span
              ><b>{{ agentDeleteTarget.isDefault ? "是" : "否" }}</b> 默认 Agent</span
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
            >请输入 Agent 名称 <em>{{ agentDeleteTarget.name }}</em> 以确认删除</span
          >
          <input
            ref="agentDeleteInputRef"
            v-model.trim="agentDeleteConfirmName"
            autocomplete="off"
            :aria-invalid="agentDeleteConfirmName.length > 0 && !canConfirmAgentDelete"
            aria-describedby="agent-delete-name-helper agent-delete-name-error"
          />
          <small id="agent-delete-name-helper">需精确匹配 Agent 名称；首尾空格会被自动忽略，大小写必须一致。</small>
          <small v-if="agentDeleteNameError" id="agent-delete-name-error" class="field-error">{{
            agentDeleteNameError
          }}</small>
        </label>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="agentDeleting" @click="closeAgentDeleteConfirm">
            取消
          </button>
          <button
            class="primary-button danger"
            type="button"
            :disabled="agentDeleting || !canConfirmAgentDelete"
            @click="confirmDeleteAgent"
          >
            <i :class="['fa-solid', agentDeleting ? 'fa-spinner fa-spin' : 'fa-trash']" aria-hidden="true" />
            <span>{{ agentDeleting ? "删除中..." : "删除 Agent" }}</span>
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
    <button type="button" aria-label="关闭反馈提示" @click="clearAgentToast">
      <i class="fa-solid fa-xmark" aria-hidden="true" />
    </button>
  </div>
</template>
