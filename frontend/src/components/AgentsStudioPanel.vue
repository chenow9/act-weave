<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 16)
/** Agents studio panel (ZKL-64 item 16). */
import AppSelect from "./AppSelect.vue";
import AgentPromptDiffViewer from "./AgentPromptDiffViewer.vue";
import { useAgentsPageContext } from "../composables/useAgentsPageContext";

const scp = useAgentsPageContext();
/* prettier-ignore */
const {
  studioMode, draftAgent, savingAgent, agentStudioPanelRef, agentNameInputRef, promptDetailDialogRef, agentStudioInlineWarning, pendingPromptSaveReview, weavePreviewAgent, acceptingPromptRevision, workspaceOptions, modelConfigOptions, studioTitle, agentNameError, agentWorkspaceError,
  agentModelError, agentRoleError, agentPromptError, promptLineCount, promptPreviewText, canSaveAgent, originalPrompt, promptSaveDiff, pendingPromptText, weavePreviewDiff, agentSaveButtonLabel, canEnhanceDraftPrompt, formatSignedDelta, isEnhancing, toggleDraftStatus, closeStudio,
  requestCloseStudio, trapAgentModalFocus, enhancePrompt, applyWeavePreview, cancelWeavePreview, confirmPromptSaveReview, cancelPromptSaveReview, saveDraftAgent
} = scp;
void AppSelect;
void AgentPromptDiffViewer;
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
              <span>返回注册中心</span>
            </button>
            <span class="agent-studio-divider" aria-hidden="true" />
            <div>
              <span>{{ draftAgent.id || "AUTO_ID" }}</span>
              <h3>{{ studioMode === "create" ? "新建 Agent" : "Agent 属性微调空间" }}</h3>
            </div>
          </div>
          <div class="agent-studio-actions">
            <button class="ghost-button" type="button" :disabled="savingAgent" @click="closeStudio">放弃改动</button>
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
              <span
                ><i class="fa-solid fa-sliders" aria-hidden="true" /> AGENT PARAMETERS ORCHESTRATION /
                属性参数配置</span
              >
            </header>
            <div class="agent-studio-fields">
              <label class="modal-field">
                <span>Agent 运行名称 <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  ref="agentNameInputRef"
                  v-model="draftAgent.name"
                  type="text"
                  required
                  aria-required="true"
                  aria-label="Agent 运行名称"
                  :aria-invalid="Boolean(agentNameError)"
                  :aria-describedby="agentNameError ? 'agent-name-error' : undefined"
                  placeholder="例如: 智能风控审查官"
                />
                <small v-if="agentNameError" id="agent-name-error" class="field-error">{{ agentNameError }}</small>
              </label>
              <label class="modal-field">
                <span>绑定业务空间 <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.workspaceId"
                  :options="workspaceOptions"
                  placeholder="选择业务空间"
                  aria-label="绑定业务空间"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentWorkspaceError)"
                  :aria-describedby="agentWorkspaceError ? 'agent-workspace-error' : undefined"
                />
                <small v-if="agentWorkspaceError" id="agent-workspace-error" class="field-error">{{
                  agentWorkspaceError
                }}</small>
              </label>
              <label class="modal-field">
                <span>决策大模型 <b class="required-mark" aria-hidden="true">*</b></span>
                <AppSelect
                  class="agent-studio-select"
                  v-model="draftAgent.modelConfigId"
                  :options="modelConfigOptions"
                  placeholder="选择模型配置"
                  aria-label="决策大模型"
                  :aria-required="true"
                  :aria-invalid="Boolean(agentModelError)"
                  :aria-describedby="agentModelError ? 'agent-model-error' : undefined"
                />
                <small v-if="agentModelError" id="agent-model-error" class="field-error">{{ agentModelError }}</small>
              </label>
              <label class="modal-field">
                <span>场景决策职责 <b class="required-mark" aria-hidden="true">*</b></span>
                <input
                  v-model="draftAgent.roleDescription"
                  type="text"
                  required
                  aria-required="true"
                  aria-label="场景决策职责"
                  :aria-invalid="Boolean(agentRoleError)"
                  :aria-describedby="agentRoleError ? 'agent-role-error' : undefined"
                  placeholder="简述其在协同链路下的核心边界..."
                />
                <small v-if="agentRoleError" id="agent-role-error" class="field-error">{{ agentRoleError }}</small>
              </label>
              <div class="agent-status-toggle">
                <div>
                  <p>激活运行状态</p>
                  <small>允许平台对该 Agent 注入生产流量并开启心跳监测。</small>
                </div>
                <button
                  type="button"
                  role="switch"
                  aria-label="切换 Agent 激活运行状态"
                  :aria-checked="draftAgent.status === 'ACTIVE'"
                  :class="{ active: draftAgent.status === 'ACTIVE' }"
                  @click="toggleDraftStatus"
                >
                  <span />
                </button>
              </div>
            </div>
          </section>

          <section class="agent-studio-section studio-prompt-editor">
            <header>
              <span
                ><i class="fa-solid fa-code" aria-hidden="true" />
                {{ studioMode === "create" ? "SYSTEM PROMPT / 初始提示词" : "PROMPT ENHANCEMENT INPUT / 增强指令" }}
                <b v-if="studioMode === 'create'" class="required-mark" aria-hidden="true">*</b></span
              >
              <button
                class="agent-weave-button"
                type="button"
                :disabled="!canEnhanceDraftPrompt"
                :title="draftAgent.id ? 'AI 智能整理 System Prompt' : '保存 Agent 后可使用 AI 智能整理'"
                aria-describedby="agent-weave-helper"
                @click="enhancePrompt"
              >
                <i
                  :class="['fa-solid', isEnhancing(draftAgent.id) ? 'fa-spinner fa-spin' : 'fa-wand-magic-sparkles']"
                  aria-hidden="true"
                />
                <span>AI 智能整理 (Weaving)</span>
              </button>
            </header>
            <div
              class="agent-prompt-overview"
              :aria-label="studioMode === 'create' ? 'System Prompt 首段预览' : 'Prompt 增强输入首段预览'"
            >
              <div>
                <strong>首段预览</strong>
                <p class="agent-prompt-preview-text">{{ promptPreviewText }}</p>
              </div>
              <dl>
                <div>
                  <dt>行数</dt>
                  <dd>{{ promptLineCount }}</dd>
                </div>
                <div>
                  <dt>字符</dt>
                  <dd>{{ draftAgent.systemPrompt?.length || 0 }}</dd>
                </div>
              </dl>
            </div>
            <div class="agent-prompt-editor-box" :class="{ 'is-weaving': isEnhancing(draftAgent.id) }">
              <textarea
                v-model="draftAgent.systemPrompt"
                rows="8"
                :required="studioMode === 'create'"
                :aria-required="studioMode === 'create'"
                :aria-label="studioMode === 'create' ? 'System Prompt' : 'Prompt 增强输入'"
                :aria-invalid="Boolean(agentPromptError)"
                :aria-describedby="agentPromptError ? 'agent-prompt-error agent-weave-helper' : 'agent-weave-helper'"
                :placeholder="
                  studioMode === 'create'
                    ? '作为智能决策主控：负责解析当前业务输入...'
                    : '描述希望模型如何增强当前 Prompt；服务端不会回传现有 Prompt 原文。'
                "
              />
            </div>
            <div class="agent-prompt-meter">
              <span
                ><i class="fa-solid fa-calculator" aria-hidden="true" /> 字符长度:
                <strong>{{ draftAgent.systemPrompt?.length || 0 }}</strong></span
              >
              <span id="agent-weave-helper">{{
                draftAgent.id
                  ? "输入增强要求后先预览，再显式采纳为不可变 Prompt Revision。"
                  : "初始 Prompt 仅在创建请求中提交。"
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
        aria-label="生产 Agent Prompt 变更审查"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-shield-halved" aria-hidden="true" />
            <span>
              <strong>生产 Agent Prompt 变更审查</strong>
              <small>AGENT: {{ pendingPromptSaveReview.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭 Prompt 变更审查"
            @click="cancelPromptSaveReview"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert">
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            当前 Agent 处于 Active，保存后会影响生产流量中的 System Prompt。请确认差异后再生效。
          </p>
          <div class="agent-prompt-diff-summary">
            <span
              ><b>{{ promptSaveDiff.beforeChars }}</b> 原字符</span
            >
            <span
              ><b>{{ promptSaveDiff.afterChars }}</b> 新字符</span
            >
            <span
              ><b>{{ formatSignedDelta(promptSaveDiff.charDelta) }}</b> 字符变化</span
            >
            <span
              ><b>{{ formatSignedDelta(promptSaveDiff.lineDelta) }}</b> 行变化</span
            >
          </div>
          <AgentPromptDiffViewer
            :before="originalPrompt"
            :after="pendingPromptText"
            before-label="历史版本"
            after-label="当前草稿"
            title="System Prompt Diff Viewer"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" :disabled="savingAgent" @click="cancelPromptSaveReview">
            返回编辑
          </button>
          <button class="primary-button" type="button" :disabled="savingAgent" @click="confirmPromptSaveReview">
            <i :class="['fa-solid', savingAgent ? 'fa-spinner fa-spin' : 'fa-circle-check']" aria-hidden="true" />
            <span>{{ savingAgent ? "保存中..." : "确认保存并生效" }}</span>
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
        aria-label="AI 智能整理预览"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-wand-magic-sparkles" aria-hidden="true" />
            <span>
              <strong>AI 智能整理预览</strong>
              <small>AGENT: {{ draftAgent.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭 AI 整理预览"
            @click="cancelWeavePreview"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert neutral">
            <i class="fa-solid fa-circle-info" aria-hidden="true" />
            预览不会修改 Agent；采纳会再次执行后端增强命令并创建不可变 Prompt Revision，原始输入与输出永久留存。
          </p>
          <div class="agent-prompt-diff-summary">
            <span
              ><b>{{ weavePreviewDiff.beforeChars }}</b> 当前字符</span
            >
            <span
              ><b>{{ weavePreviewDiff.afterChars }}</b> 预览字符</span
            >
            <span
              ><b>{{ formatSignedDelta(weavePreviewDiff.charDelta) }}</b> 字符变化</span
            >
            <span
              ><b>{{ formatSignedDelta(weavePreviewDiff.lineDelta) }}</b> 行变化</span
            >
          </div>
          <AgentPromptDiffViewer
            :before="draftAgent.systemPrompt || ''"
            :after="weavePreviewAgent.output || ''"
            before-label="增强输入"
            after-label="AI 预览"
            title="AI Prompt Preview Diff Viewer"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" @click="cancelWeavePreview">取消</button>
          <button class="primary-button" type="button" :disabled="acceptingPromptRevision" @click="applyWeavePreview">
            <i
              :class="acceptingPromptRevision ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-check'"
              aria-hidden="true"
            />
            <span>{{ acceptingPromptRevision ? "采纳中..." : "采纳为新 Revision" }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Transition>
</template>
