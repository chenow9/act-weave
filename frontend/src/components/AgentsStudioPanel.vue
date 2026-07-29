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
  agentModelError, agentRoleError, agentPromptError, promptLineCount, promptPreviewText, canSaveAgent, originalPrompt, promptSaveDiff, pendingPromptText, weavePreviewDiff, agentSaveButtonLabel, canEnhanceDraftPrompt, formatSignedDelta, isEnhancing, toggleDraftStatus,
  agentContextMode, agentContextMaxInputTokens, agentContextMaxRecentTurns,
  agentContextSummaryMaxTokens, agentContextSummaryMinEvictedTurns, agentContextSummaryMaxPasses,
  agentContextAdvancedOpen, toggleAgentContextAdvanced,
  setAgentContextMode, setAgentContextMaxInput, setAgentContextMaxTurns,
  setAgentContextSummaryMaxTokens, setAgentContextSummaryMinEvictedTurns, setAgentContextSummaryMaxPasses,
  closeStudio,
  requestCloseStudio, trapAgentModalFocus, enhancePrompt, applyWeavePreview, cancelWeavePreview, confirmPromptSaveReview, cancelPromptSaveReview, saveDraftAgent
} = scp;

const agentContextModeOptions = [
  { label: "Token 窗口（推荐，大多数场景）", value: "token_window" },
  { label: "滚动摘要（长会话，可选）", value: "rolling_summary" },
  { label: "继承 / 不启用（全量历史）", value: "" },
  { label: "关闭窗口管理", value: "disabled" },
];

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
              <span>返回列表</span>
            </button>
            <span class="agent-studio-divider" aria-hidden="true" />
            <div>
              <span>{{ draftAgent.id || "创建后自动生成 ID" }}</span>
              <h3>{{ studioMode === "create" ? "新建 Agent" : "编辑 Agent" }}</h3>
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
                ><i class="fa-solid fa-sliders" aria-hidden="true" /> Agent 参数</span
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

              <div class="agent-context-policy">
                <header>
                  <strong>会话上下文策略</strong>
                  <small>对话太长时，平台如何裁剪历史再送给模型。不知道怎么选就保持「Token 窗口」。</small>
                </header>
                <label class="modal-field">
                  <span>上下文模式</span>
                  <AppSelect
                    class="agent-studio-select"
                    :model-value="agentContextMode"
                    :options="agentContextModeOptions"
                    placeholder="选择上下文模式"
                    aria-label="会话上下文模式"
                    @update:model-value="setAgentContextMode(String($event ?? ''))"
                  />
                </label>
                <p class="agent-context-policy-hint">
                  <template v-if="agentContextMode === 'token_window' || !agentContextMode">
                    推荐默认：按绑定模型的窗口自动裁掉过旧历史；通常无需再改高级项。
                  </template>
                  <template v-else-if="agentContextMode === 'rolling_summary'">
                    已套用推荐默认（最近约 20 轮原文 + 摘要参数）。一般不用展开高级选项。
                  </template>
                  <template v-else-if="agentContextMode === 'disabled'">
                    关闭窗口管理后，会话可能随长度增长而变慢或触发上游超限。
                  </template>
                  需先在模型 API 里用预设点好「上下文窗口」（如 128K）。
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
                    <span>高级选项</span>
                    <i class="fa-solid fa-chevron-down agent-context-advanced-chevron" aria-hidden="true" />
                  </button>
                  <div v-if="agentContextAdvancedOpen" class="agent-context-advanced">
                    <div class="agent-context-policy-limits">
                      <label class="modal-field">
                        <span>输入上限（0=跟模型窗口走）</span>
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
                        <span>最近原文轮次（0=不限条数）</span>
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
                        <span>摘要长度上限</span>
                        <input
                          type="number"
                          min="1"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMaxTokens"
                          @input="
                            setAgentContextSummaryMaxTokens(Number(($event.target as HTMLInputElement).value))
                          "
                        />
                      </label>
                      <label class="modal-field">
                        <span>至少淘汰几轮再摘要</span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMinEvictedTurns"
                          @input="
                            setAgentContextSummaryMinEvictedTurns(
                              Number(($event.target as HTMLInputElement).value),
                            )
                          "
                        />
                      </label>
                      <label class="modal-field">
                        <span>摘要最多生成几轮</span>
                        <input
                          type="number"
                          min="1"
                          step="1"
                          class="mono"
                          :value="agentContextSummaryMaxPasses"
                          @input="
                            setAgentContextSummaryMaxPasses(Number(($event.target as HTMLInputElement).value))
                          "
                        />
                      </label>
                    </div>
                    <p class="agent-context-policy-hint">
                      0 表示不额外收紧。滚动摘要默认：摘要 2048、淘汰 4 轮、生成 2 轮、最近 20 轮。
                    </p>
                  </div>
                </template>
              </div>
            </div>
          </section>

          <section class="agent-studio-section studio-prompt-editor">
            <header>
              <span
                ><i class="fa-solid fa-code" aria-hidden="true" />
                {{ studioMode === "create" ? "初始系统提示词" : "AI 整理要求" }}
                <b v-if="studioMode === 'create'" class="required-mark" aria-hidden="true">*</b></span
              >
              <button
                class="agent-weave-button"
                type="button"
                :disabled="!canEnhanceDraftPrompt"
                :title="canEnhanceDraftPrompt ? 'AI 智能整理系统提示词' : '请先完善业务空间、模型和系统提示词'"
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
                <span>AI 智能整理</span>
              </button>
            </header>
            <div
              class="agent-prompt-overview"
              :aria-label="studioMode === 'create' ? '系统提示词首段预览' : 'AI 整理要求首段预览'"
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
            <div
              class="agent-prompt-editor-box"
              :class="{ 'is-weaving': isEnhancing(draftAgent.id || 'create-draft') }"
            >
              <textarea
                v-model="draftAgent.systemPrompt"
                rows="8"
                :required="studioMode === 'create'"
                :aria-required="studioMode === 'create'"
                :aria-label="studioMode === 'create' ? '系统提示词' : 'AI 整理要求'"
                :aria-invalid="Boolean(agentPromptError)"
                :aria-describedby="agentPromptError ? 'agent-prompt-error agent-weave-helper' : 'agent-weave-helper'"
                :placeholder="
                  studioMode === 'create'
                    ? '作为智能决策主控：负责解析当前业务输入...'
                    : '描述希望模型如何整理当前系统提示词；服务端不会回传现有提示词原文。'
                "
              />
            </div>
            <div class="agent-prompt-meter">
              <span
                ><i class="fa-solid fa-calculator" aria-hidden="true" /> 字符长度:
                <strong>{{ draftAgent.systemPrompt?.length || 0 }}</strong></span
              >
              <span id="agent-weave-helper">{{
                studioMode === "create" || !draftAgent.id
                  ? "创建前可直接整理系统提示词；应用到草稿后需再点「创建 Agent」才会保存。"
                  : "整理预览后可采纳为新版本；不会回填当前生效提示词。"
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
        aria-label="生产 Agent 提示词变更审查"
      >
        <header class="agent-prompt-detail-head">
          <div>
            <i class="fa-solid fa-shield-halved" aria-hidden="true" />
            <span>
              <strong>生产 Agent 提示词变更审查</strong>
              <small>AGENT: {{ pendingPromptSaveReview.id }}</small>
            </span>
          </div>
          <button
            class="icon-action-button"
            type="button"
            title="关闭"
            aria-label="关闭提示词变更审查"
            @click="cancelPromptSaveReview"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="agent-risk-review-body">
          <p class="agent-risk-alert">
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            当前 Agent 处于运行中，保存后会影响生产流量中的系统提示词。请确认差异后再生效。
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
            title="变更对比"
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
              <strong>AI 整理预览</strong>
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
            预览不会修改 Agent；采纳会再次执行后端增强命令并创建不可变提示词版本，原始输入与输出永久留存。
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
            before-label="当前要求"
            after-label="AI 建议"
            title="变更对比"
          />
        </div>
        <footer class="agent-prompt-detail-footer">
          <button class="ghost-button" type="button" @click="cancelWeavePreview">取消</button>
          <button class="primary-button" type="button" :disabled="acceptingPromptRevision" @click="applyWeavePreview">
            <i
              :class="acceptingPromptRevision ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-check'"
              aria-hidden="true"
            />
            <span>{{
              acceptingPromptRevision
                ? studioMode === "create"
                  ? "应用中..."
                  : "采纳中..."
                : studioMode === "create"
                  ? "应用到草稿"
                  : "采纳为新版本"
            }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Transition>
</template>
