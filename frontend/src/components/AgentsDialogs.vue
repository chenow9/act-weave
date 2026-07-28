<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents dialogs (ZKL-64 item 16). */

import { useAgentsPageContext } from "../composables/useAgentsPageContext";

const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  agentActionNote, agentActionTone, promptDetailAgent, agentDeleting, agentDeleteTarget, agentDeleteConfirmName, promptDetailDialogRef, agentDeleteDialogRef, agentDeleteInputRef, capabilityAgent, capabilityLoading, capabilitySavingId, capabilityDrafts, promptDetailVisible, capabilityCatalog, canConfirmAgentDelete,
  agentDeleteNameError, closePromptDetail, trapAgentModalFocus, clearAgentToast, closeAgentDeleteConfirm, requestCloseAgentDeleteConfirm, confirmDeleteAgent, closeCapabilityBindings, currentCapabilityBinding, setCapabilityVersionPolicy, capabilityVersionPolicyOptions, saveCapabilityBinding, removeCapabilityBinding
} = scp;
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
        :aria-label="promptDetailAgent ? `${promptDetailAgent.name} · Prompt Revision` : 'Prompt Revision 详情'"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-rectangle-list" aria-hidden="true" />
            <span>
              <strong>Prompt Revision Audit</strong>
              <small>AGENT: {{ promptDetailAgent?.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭 Prompt Revision 详情"
            @click="closePromptDetail"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-prompt-revision-readonly">
          <strong>{{ promptDetailAgent?.currentPromptRevisionId || "尚未创建 Prompt Revision" }}</strong>
          <p>Agent Read DTO 只返回当前 Revision ID，不返回 Prompt 明文。增强输入与输出由后端 StoredObject 永久保留。</p>
        </div>
        <footer class="agent-prompt-detail-footer">
          <span>LOCK VERSION: {{ promptDetailAgent?.lockVersion || 0 }}</span>
          <button class="primary-button" type="button" @click="closePromptDetail">关闭窗口</button>
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
        :aria-label="`${capabilityAgent.name} Capability Binding`"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-link" aria-hidden="true" />
            <span
              ><strong>Capability Binding</strong><small>AGENT: {{ capabilityAgent.id }}</small></span
            >
          </div>
          <button
            class="icon-action-button"
            type="button"
            aria-label="关闭 Capability Binding"
            :disabled="Boolean(capabilitySavingId)"
            @click="closeCapabilityBindings"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <div class="agent-capability-body">
          <p>
            Tool 与 Workflow 是 Workspace 级 Capability；此处只管理 Agent 的 follow/pin、Connection 选择和启用状态。
          </p>
          <div v-if="capabilityLoading" class="agent-capability-empty">正在加载统一 Capability Catalog...</div>
          <div v-else-if="!capabilityCatalog.length" class="agent-capability-empty">
            当前 Workspace 尚无已发布 Capability。
          </div>
          <article v-for="capability in capabilityCatalog" v-else :key="capability.id" class="agent-capability-item">
            <header>
              <div>
                <span>{{ capability.kind }}</span
                ><strong>{{ capability.name }}</strong
                ><small>{{ capability.description }}</small>
              </div>
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
                  capabilityDrafts[capability.id].versionPolicy === "PINNED" ? "Pinned Release ID" : "Resolved Release"
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
                <span>Connection ID（可选）</span>
                <input
                  v-model.trim="capabilityDrafts[capability.id].connectionId"
                  class="mono"
                  placeholder="同 Workspace 且与 Capability Provider 兼容"
                />
              </label>
              <label class="agent-capability-enabled"
                ><input v-model="capabilityDrafts[capability.id].enabled" type="checkbox" /><span
                  >启用该 Binding</span
                ></label
              >
            </div>
            <footer>
              <button
                v-if="currentCapabilityBinding(capability.id)"
                class="ghost-button danger"
                type="button"
                :disabled="Boolean(capabilitySavingId)"
                @click="removeCapabilityBinding(capability)"
              >
                解绑
              </button>
              <button
                class="primary-button"
                type="button"
                :disabled="Boolean(capabilitySavingId)"
                @click="saveCapabilityBinding(capability)"
              >
                <i v-if="capabilitySavingId === capability.id" class="fa-solid fa-spinner fa-spin" />{{
                  currentCapabilityBinding(capability.id) ? "更新 Binding" : "绑定 Capability"
                }}
              </button>
            </footer>
          </article>
        </div>
        <footer class="agent-prompt-detail-footer">
          <button
            class="ghost-button"
            type="button"
            :disabled="Boolean(capabilitySavingId)"
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
              <strong>Delete Agent</strong>
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
