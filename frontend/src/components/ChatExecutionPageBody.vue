<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 15)
/** Chat execution page body (ZKL-64 item 15). */
/* prettier-ignore */
import DebugOutboundCredentialPanel from "./DebugOutboundCredentialPanel.vue";
import { useChatExecutionPageContext } from "../composables/useChatExecutionPageContext";

const scp = useChatExecutionPageContext();
/* prettier-ignore */
const {
  workspaces, agents, chat, composer, selectedWorkspaceId, selectedAgentId, sessionKeyword, sending, confirming, archivingSession, contextLoading, actionError, activeSidePanel, sessionPanelTrigger, runtimePanelTrigger, sessionPanelCloseButton, runtimePanelCloseButton, chatScrollArea, workspaceDropdownTrigger, agentDropdownTrigger,
  hasUnreadMessages, outboundAttachmentId, debugOutboundPanel, debugPassthroughConnectionId, chatDropdowns, filteredAgents, activeSessionAgent, activeSessionWorkspace, activeWorkspaceLabel, activeAgentLabel, activeUserLabel, activeUserInitials, contextSelectionDirty, activeSessionReadOnly, activeSessionAgentStatusLabel, composerPlaceholder, filteredSessions, latestRunStepRows, runtimeSummary, runStatusLabel,
  runtimeIntentLabel, capabilityCount, capabilityCountLabel, runtimeTargetReleaseId, conversationBusy, currentSubjectLabel, passthroughConnections, requiresPassthroughToken, effectiveDebugConnectionId, newSession, selectSession, toggleSidePanel, closeSidePanel, trapSidePanelFocus, handleConversationScroll, revealLatestMessages, attachDebugOutboundCredentials, onOutboundAttachment, send, confirm,
  cancelConfirmation, archiveCurrentSession, renderMessageMarkdown, sessionAgentName, toggleChatDropdown, closeChatDropdowns, selectWorkspaceOption, selectAgentOption, handleDropdownMenuKeydown, statusBadgeClass, stepStatusIcon, messageTime, messageDateTimeTitle, messageDateLabel, shouldShowMessageDate, sessionTime
} = scp;
void DebugOutboundCredentialPanel;
</script>

<template>
  <div
    class="orchestrator-console-page"
    v-loading="chat.loading"
    @click="closeChatDropdowns"
    @keydown.esc="closeSidePanel"
  >
    <header class="orchestrator-console-topbar">
      <div class="orchestrator-topbar-leading">
        <button
          ref="sessionPanelTrigger"
          class="orchestrator-panel-trigger"
          type="button"
          aria-controls="chat-session-panel"
          :aria-expanded="activeSidePanel === 'sessions'"
          @click.stop="toggleSidePanel('sessions')"
        >
          <i class="fa-regular fa-clock" aria-hidden="true" />
          <span>历史会话</span>
        </button>

        <div class="orchestrator-context-shell">
          <div class="orchestrator-context-group">
            <span class="orchestrator-context-label">新会话配置</span>
            <div class="chat-context-dropdown" @click.stop>
              <button
                ref="workspaceDropdownTrigger"
                type="button"
                aria-label="选择新会话的业务空间"
                title="选择新会话的业务空间"
                aria-haspopup="menu"
                :aria-expanded="chatDropdowns.workspace"
                @click="toggleChatDropdown('workspace')"
                @keydown.esc.stop="closeChatDropdowns"
              >
                <i class="fa-solid fa-cubes" aria-hidden="true" />
                <span>{{ activeWorkspaceLabel }}</span>
                <i class="fa-solid fa-chevron-down" :class="{ open: chatDropdowns.workspace }" aria-hidden="true" />
              </button>
              <div
                v-if="chatDropdowns.workspace"
                class="chat-context-dropdown-menu"
                data-dropdown="workspace"
                role="menu"
                aria-label="选择新会话的业务空间"
                @keydown="handleDropdownMenuKeydown($event, 'workspace')"
              >
                <button
                  v-for="workspace in workspaces.items"
                  :key="workspace.id"
                  type="button"
                  role="menuitemradio"
                  tabindex="-1"
                  :class="{ selected: workspace.id === selectedWorkspaceId }"
                  :aria-checked="workspace.id === selectedWorkspaceId"
                  @click="selectWorkspaceOption(workspace)"
                >
                  <span>{{ workspace.displayName }}</span>
                  <small>{{ workspace.name }}</small>
                </button>
              </div>
            </div>

            <div class="chat-context-dropdown agent" @click.stop>
              <button
                ref="agentDropdownTrigger"
                type="button"
                aria-label="选择新会话的 Agent"
                title="选择新会话的 Agent"
                aria-haspopup="menu"
                :aria-expanded="chatDropdowns.agent"
                @click="toggleChatDropdown('agent')"
                @keydown.esc.stop="closeChatDropdowns"
              >
                <i class="fa-solid fa-robot" aria-hidden="true" />
                <span>{{ activeAgentLabel }}</span>
                <i class="fa-solid fa-chevron-down" :class="{ open: chatDropdowns.agent }" aria-hidden="true" />
              </button>
              <div
                v-if="chatDropdowns.agent"
                class="chat-context-dropdown-menu agent-menu"
                data-dropdown="agent"
                role="menu"
                aria-label="选择新会话的 Agent"
                @keydown="handleDropdownMenuKeydown($event, 'agent')"
              >
                <p>选择新会话的 Agent</p>
                <button
                  v-for="agent in filteredAgents"
                  :key="agent.id"
                  type="button"
                  role="menuitemradio"
                  tabindex="-1"
                  :class="{ selected: agent.id === selectedAgentId }"
                  :aria-checked="agent.id === selectedAgentId"
                  @click="selectAgentOption(agent)"
                >
                  <span>
                    <strong>{{ agent.name }}</strong>
                    <small>{{ agent.modelConfigId || "Eino Runtime" }}</small>
                  </span>
                  <i class="agent-online-dot" aria-hidden="true" />
                </button>
              </div>
            </div>
            <button
              class="orchestrator-panel-trigger context-new-session-button"
              :class="{ 'has-pending-context': contextSelectionDirty }"
              type="button"
              :disabled="contextLoading || !selectedWorkspaceId || !selectedAgentId"
              :aria-busy="contextLoading"
              :title="contextSelectionDirty ? '使用所选配置创建新会话' : '创建新会话'"
              @click="newSession"
            >
              <i class="fa-solid fa-plus" aria-hidden="true" />
              <span>{{ contextLoading ? "载入配置" : contextSelectionDirty ? "应用并新建" : "新建会话" }}</span>
            </button>
          </div>
        </div>
      </div>
      <button
        ref="runtimePanelTrigger"
        class="orchestrator-panel-trigger"
        type="button"
        aria-controls="chat-runtime-panel"
        :aria-expanded="activeSidePanel === 'runtime'"
        @click.stop="toggleSidePanel('runtime')"
      >
        <i class="fa-solid fa-list-check" aria-hidden="true" />
        <span>运行详情</span>
      </button>
    </header>

    <main class="chat-workbench">
      <section class="chat-conversation-panel">
        <div class="runtime-console-header">
          <div class="runtime-console-header-content">
            <div class="runtime-console-title">
              <div class="runtime-title-row">
                <h1>运行调试台</h1>
                <b :class="statusBadgeClass(chat.runStatus)">{{ runStatusLabel }}</b>
                <button
                  v-if="chat.activeSession?.status === 'ACTIVE'"
                  class="chat-inline-action"
                  type="button"
                  title="归档当前会话（消息会永久保留）"
                  :disabled="archivingSession"
                  :aria-busy="archivingSession ? 'true' : undefined"
                  @click="archiveCurrentSession"
                >
                  {{ archivingSession ? "归档中…" : "归档" }}
                </button>
              </div>
              <p>
                <span>{{ chat.activeSession?.title || "新会话" }}</span>
                <i aria-hidden="true">/</i>
                <span>{{ activeSessionWorkspace?.displayName || activeSessionWorkspace?.name || "未选择空间" }}</span>
                <i aria-hidden="true">/</i>
                <span>{{ activeSessionAgent?.name || chat.activeSession?.agentId || activeAgentLabel }}</span>
                <b
                  v-if="activeSessionReadOnly"
                  class="runtime-agent-state"
                  :title="`会话创建时使用的 Agent 当前${activeSessionAgentStatusLabel}，仅保留历史记录`"
                >
                  {{ activeSessionAgentStatusLabel }}
                </b>
              </p>
            </div>
            <div class="runtime-summary-list" aria-label="当前运行摘要">
              <span
                ><small>意图</small><strong>{{ runtimeIntentLabel }}</strong></span
              >
              <span
                ><small>本轮能力</small><strong>{{ capabilityCountLabel }}</strong></span
              >
            </div>
          </div>
        </div>

        <section class="debug-console-banner" role="status" data-testid="debug-console-nonprod-banner">
          <i class="fa-solid fa-flask" aria-hidden="true" />
          <div>
            <strong>内部运行调试台（非生产）</strong>
            <p>
              用于配置验证与出站身份调试。业务 Token
              不会写入会话正文、历史或本地存储；透传凭据通过下方独立绑定面板一次性提交。
            </p>
          </div>
        </section>
        <div class="debug-subject-bar" data-testid="debug-console-subject" aria-label="当前 Subject">
          <span
            ><small>当前 Subject</small><strong>{{ currentSubjectLabel }}</strong></span
          >
          <span v-if="outboundAttachmentId"><small>出站绑定</small><strong>已绑定（下一条消息消费）</strong></span>
          <span v-else><small>出站绑定</small><strong>未绑定</strong></span>
        </div>

        <section v-if="activeSessionReadOnly" class="chat-session-agent-alert" role="status">
          <div class="chat-session-agent-alert-copy">
            <i class="fa-solid fa-user-slash" aria-hidden="true" />
            <div>
              <strong>关联 Agent {{ activeSessionAgentStatusLabel }}</strong>
              <p>
                “{{
                  activeSessionAgent?.name || chat.activeSession?.agentId
                }}”关联的会话仅可查看，不能继续执行；归档不会删除消息。
              </p>
            </div>
          </div>
          <button
            type="button"
            :disabled="contextLoading || !selectedWorkspaceId || !selectedAgentId"
            :aria-busy="contextLoading"
            @click="newSession"
          >
            <i class="fa-solid fa-plus" aria-hidden="true" />
            <span>{{ contextLoading ? "载入可用 Agent" : `使用 ${activeAgentLabel} 新建会话` }}</span>
          </button>
        </section>

        <div
          ref="chatScrollArea"
          class="chat-scroll-area"
          role="log"
          aria-label="对话消息"
          aria-live="polite"
          aria-relevant="additions text"
          :aria-busy="conversationBusy"
          @scroll.passive="handleConversationScroll"
        >
          <div v-if="!chat.messages.length" class="chat-empty-state">
            <span><i class="fa-solid fa-robot" aria-hidden="true" /></span>
            <strong>开始一次可审计的执行对话</strong>
            <p>描述目标即可。涉及敏感能力时，系统会在执行前请求你的确认。</p>
          </div>

          <template v-for="(message, messageIndex) in chat.messages" :key="message.id">
            <div v-if="shouldShowMessageDate(messageIndex)" class="message-date-separator" role="separator">
              <span>{{ messageDateLabel(message.createdAt) }}</span>
            </div>
            <article class="message-row" :class="message.role.toLowerCase()">
              <div v-if="message.role === 'ASSISTANT'" class="assistant-message-shell">
                <div class="message-avatar assistant" aria-hidden="true"><i class="fa-solid fa-robot" /></div>
                <div>
                  <div class="message-meta">
                    <strong>ActWeave Agent</strong>
                    <time :datetime="message.createdAt" :title="messageDateTimeTitle(message.createdAt)">{{
                      messageTime(message.createdAt)
                    }}</time>
                  </div>
                  <div class="assistant-bubble" v-html="renderMessageMarkdown(message.content)" />
                </div>
              </div>

              <div v-else-if="message.role === 'USER'" class="user-message-shell">
                <div class="message-avatar user" aria-hidden="true">{{ activeUserInitials }}</div>
                <div>
                  <div class="message-meta user">
                    <time :datetime="message.createdAt" :title="messageDateTimeTitle(message.createdAt)">{{
                      messageTime(message.createdAt)
                    }}</time>
                    <strong>{{ activeUserLabel }}</strong>
                  </div>
                  <div class="user-bubble">{{ message.content }}</div>
                </div>
              </div>
            </article>
          </template>

          <section v-if="chat.pendingConfirmation?.status === 'PENDING'" class="risk-gate-card">
            <div class="risk-gate-head">
              <div>
                <i class="fa-solid fa-shield-halved" aria-hidden="true" />
                <strong>高风险拦截：执行前安全确认</strong>
              </div>
              <span>MANUAL GATE</span>
            </div>
            <p>该请求将访问敏感能力或触发高风险动作。只有本次执行的原发起人可以确认或取消。</p>
            <ul>
              <li v-for="reason in chat.pendingConfirmation.riskReasons" :key="reason">
                <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
                <span>{{ reason }}</span>
              </li>
            </ul>
            <div class="risk-confirm-row">
              <span v-if="!chat.pendingResumeToken" role="status">确认凭据未在当前浏览器恢复，请重新发起该操作。</span>
              <button
                type="button"
                :disabled="confirming || activeSessionReadOnly"
                :aria-busy="confirming"
                @click="cancelConfirmation"
              >
                <i class="fa-solid fa-ban" aria-hidden="true" />
                取消操作
              </button>
              <button
                type="button"
                :disabled="confirming || activeSessionReadOnly || !chat.pendingResumeToken"
                :aria-busy="confirming"
                @click="confirm"
              >
                <i class="fa-solid fa-unlock-keyhole" aria-hidden="true" />
                {{ confirming ? "授权中" : "授权执行" }}
              </button>
            </div>
          </section>
        </div>

        <button v-if="hasUnreadMessages" class="chat-new-message-button" type="button" @click="revealLatestMessages">
          <i class="fa-solid fa-arrow-down" aria-hidden="true" />
          <span>有新消息</span>
        </button>

        <div class="chat-composer-dock">
          <p v-if="actionError" id="chat-action-error" class="chat-action-error" role="alert" aria-live="assertive">
            {{ actionError }}
          </p>
          <div v-if="!chat.messages.length && !activeSessionReadOnly" class="prompt-suggestion-strip">
            <span>暂无快捷指令</span>
          </div>
          <div
            v-if="chat.activeSession && !activeSessionReadOnly"
            class="debug-outbound-dock"
            data-testid="debug-outbound-credential-panel"
          >
            <label v-if="requiresPassthroughToken && passthroughConnections.length > 1" class="debug-connection-picker">
              透传 Connection
              <select v-model="debugPassthroughConnectionId" aria-label="选择透传 Connection">
                <option v-for="c in passthroughConnections" :key="c.id" :value="c.id">{{ c.name }}</option>
              </select>
            </label>
            <DebugOutboundCredentialPanel
              ref="debugOutboundPanel"
              :workspace-id="chat.activeSession.workspaceId"
              :session-id="chat.activeSession.id"
              :requires-passthrough="requiresPassthroughToken"
              :connection-id="effectiveDebugConnectionId"
              :attach="attachDebugOutboundCredentials"
              @attachment="onOutboundAttachment"
            />
          </div>
          <div class="chat-input-shell" :class="{ 'is-read-only': activeSessionReadOnly }">
            <textarea
              v-model="composer"
              rows="1"
              aria-label="输入业务指令或目标任务"
              :placeholder="composerPlaceholder"
              :disabled="activeSessionReadOnly"
              :aria-describedby="actionError ? 'chat-composer-shortcut chat-action-error' : 'chat-composer-shortcut'"
              @keydown.enter.exact.prevent="send"
            />
            <span id="chat-composer-shortcut" class="chat-sr-only">按 Enter 发送，按 Shift 加 Enter 换行。</span>
            <button
              type="button"
              :disabled="sending || activeSessionReadOnly || !composer.trim()"
              :aria-busy="sending"
              @click="send"
            >
              <i class="fa-solid" :class="sending ? 'fa-spinner' : 'fa-paper-plane'" aria-hidden="true" />
              <span>{{ sending ? "发送中" : "发送" }}</span>
            </button>
          </div>
        </div>
      </section>

      <Transition name="chat-panel-fade">
        <div
          v-if="activeSidePanel === 'sessions'"
          class="chat-side-panel-backdrop session"
          @click.self="closeSidePanel"
        >
          <aside
            id="chat-session-panel"
            class="chat-session-rail chat-side-panel"
            role="dialog"
            aria-modal="true"
            aria-label="历史会话"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="chat-session-head">
              <div>
                <span>历史会话</span>
                <h2>执行记录</h2>
              </div>
              <div class="chat-panel-head-actions">
                <button
                  class="chat-panel-icon-button primary"
                  type="button"
                  title="创建新会话"
                  aria-label="创建新会话"
                  @click="newSession"
                >
                  <i class="fa-solid fa-plus" aria-hidden="true" />
                </button>
                <button
                  ref="sessionPanelCloseButton"
                  class="chat-panel-icon-button"
                  type="button"
                  title="关闭历史会话"
                  aria-label="关闭历史会话"
                  @click="closeSidePanel"
                >
                  <i class="fa-solid fa-xmark" aria-hidden="true" />
                </button>
              </div>
            </div>
            <label class="chat-session-search">
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input v-model="sessionKeyword" type="text" placeholder="筛选会话..." />
            </label>

            <div class="chat-session-list">
              <button
                v-for="session in filteredSessions"
                :key="session.id"
                class="chat-session-card"
                :class="{ active: session.id === chat.activeSessionId }"
                type="button"
                @click="selectSession(session.id)"
              >
                <strong :title="session.title">{{ session.title }}</strong>
                <span class="chat-session-meta">
                  <small class="chat-session-agent-name">
                    <span>{{ sessionAgentName(session) }}</span>
                    <b
                      v-if="
                        session.status === 'ARCHIVED' || !agents.items.some((agent) => agent.id === session.agentId)
                      "
                    >
                      {{ session.status === "ARCHIVED" ? "已归档" : "不可用" }}
                    </b>
                  </small>
                  <small>{{ sessionTime(session.updatedAt) }}</small>
                </span>
              </button>

              <div v-if="!filteredSessions.length" class="chat-session-empty">
                <i class="fa-regular fa-message" aria-hidden="true" />
                <strong>没有匹配的会话</strong>
                <small>调整关键词，或新建一个执行会话。</small>
              </div>
            </div>
          </aside>
        </div>
      </Transition>

      <Transition name="chat-panel-fade">
        <div v-if="activeSidePanel === 'runtime'" class="chat-side-panel-backdrop runtime" @click.self="closeSidePanel">
          <aside
            id="chat-runtime-panel"
            class="runtime-monitor-panel chat-side-panel"
            role="dialog"
            aria-modal="true"
            aria-label="运行详情"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="runtime-monitor-head">
              <div>
                <span>运行详情</span>
                <h2>{{ chat.latestRun ? "Run 与执行链路" : "等待会话运行" }}</h2>
              </div>
              <button
                ref="runtimePanelCloseButton"
                class="chat-panel-icon-button"
                type="button"
                title="关闭运行详情"
                aria-label="关闭运行详情"
                @click="closeSidePanel"
              >
                <i class="fa-solid fa-xmark" aria-hidden="true" />
              </button>
            </div>

            <div class="runtime-monitor-body">
              <section class="runtime-decision-card">
                <div>
                  <span>决策目标</span>
                  <small>{{ chat.latestRun?.triggerType || "待识别" }}</small>
                </div>
                <strong>{{ runtimeTargetReleaseId || "尚未匹配 Capability Release" }}</strong>
                <p>{{ runtimeSummary }}</p>
                <div class="runtime-decision-meta">
                  <span>风险等级</span>
                  <b>{{ chat.pendingConfirmation?.riskLevel || "标准" }}</b>
                </div>
              </section>

              <section class="runtime-step-section">
                <div class="runtime-section-title">
                  <span>执行链路</span>
                  <small>{{ latestRunStepRows.length }} 个事件</small>
                </div>
                <div v-if="latestRunStepRows.length" class="runtime-step-list">
                  <div v-for="step in latestRunStepRows" :key="step.id" class="runtime-step-row">
                    <i :class="[stepStatusIcon(step.status), step.status]" aria-hidden="true" />
                    <span>
                      <strong :title="step.name">{{ step.name }}</strong>
                      <small :title="step.summary">{{ step.summary }}</small>
                    </span>
                    <b>{{ step.statusLabel }}</b>
                  </div>
                </div>
                <div v-else class="runtime-empty-note">
                  <strong>暂无执行事件</strong>
                  <small>发送指令后，这里会按顺序展示意图分析、能力调用和确认节点。</small>
                </div>
              </section>

              <details class="runtime-policy-card" :open="chat.pendingConfirmation?.status === 'PENDING'">
                <summary>
                  <span><i class="fa-solid fa-shield-halved" aria-hidden="true" />安全策略</span>
                  <span class="runtime-disclosure-state">
                    <small v-if="chat.pendingConfirmation?.status === 'PENDING'">需要确认</small>
                    <i class="fa-solid fa-angle-down" aria-hidden="true" />
                  </span>
                </summary>
                <div class="runtime-policy-detail">
                  <i class="fa-solid fa-shield-halved" aria-hidden="true" />
                  <span>
                    <strong>敏感能力访问</strong>
                    <small>涉及高风险动作时需要人工授权并写入审计链路。</small>
                  </span>
                </div>
              </details>

              <details class="runtime-trace-section">
                <summary>
                  <span><i class="fa-solid fa-bug" aria-hidden="true" />技术信息与 Trace</span>
                  <i class="fa-solid fa-angle-down" aria-hidden="true" />
                </summary>
                <div class="runtime-trace-block">
                  <p>
                    <span>Trace ID:</span><b>{{ chat.latestRun?.traceId || chat.latestExecution?.traceId || "-" }}</b>
                  </p>
                  <p>
                    <span>Run ID:</span><b>{{ chat.latestRunId || "-" }}</b>
                  </p>
                  <p>
                    <span>Target ID:</span><b>{{ runtimeTargetReleaseId || "-" }}</b>
                  </p>
                  <p><span>Runtime:</span><b>Eino Runtime Engine</b></p>
                  <p>
                    <span>Workspace:</span><b>{{ activeWorkspaceLabel }}</b>
                  </p>
                  <p>
                    <span>Capabilities:</span><b>{{ capabilityCount }}</b>
                  </p>
                </div>
              </details>
            </div>
          </aside>
        </div>
      </Transition>
    </main>
  </div>
</template>
