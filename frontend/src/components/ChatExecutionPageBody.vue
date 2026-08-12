<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 15)
/** Chat execution page body (ZKL-64 item 15). */
/* prettier-ignore */
import { useI18n } from "vue-i18n";
import A2UISurface from "./a2ui/A2UISurface.vue";
import DebugOutboundCredentialPanel from "./DebugOutboundCredentialPanel.vue";
import { useChatExecutionPageContext } from "../composables/useChatExecutionPageContext";

const { t } = useI18n();
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
          <span>{{ t("chat.historySessions") }}</span>
        </button>

        <div class="orchestrator-context-shell">
          <div class="orchestrator-context-group">
            <span class="orchestrator-context-label">{{ t("chat.newSessionConfig") }}</span>
            <div class="chat-context-dropdown" @click.stop>
              <button
                ref="workspaceDropdownTrigger"
                type="button"
                :aria-label="t('chat.selectWorkspaceAria')"
                :title="t('chat.selectWorkspaceAria')"
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
                :aria-label="t('chat.selectWorkspaceAria')"
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
                :aria-label="t('chat.selectAgentAria')"
                :title="t('chat.selectAgentAria')"
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
                :aria-label="t('chat.selectAgentAria')"
                @keydown="handleDropdownMenuKeydown($event, 'agent')"
              >
                <p>{{ t("chat.selectAgentPrompt") }}</p>
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
              :title="contextSelectionDirty ? t('chat.createWithConfig') : t('chat.createSession')"
              @click="newSession"
            >
              <i class="fa-solid fa-plus" aria-hidden="true" />
              <span>{{
                contextLoading
                  ? t("chat.loadingConfig")
                  : contextSelectionDirty
                    ? t("chat.applyAndCreate")
                    : t("chat.newSession")
              }}</span>
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
        <span>{{ t("chat.runtimeDetail") }}</span>
      </button>
    </header>

    <main class="chat-workbench">
      <section class="chat-conversation-panel">
        <div class="runtime-console-header">
          <div class="runtime-console-header-content">
            <div class="runtime-console-title">
              <div class="runtime-title-row">
                <h1>{{ t("chat.consoleTitle") }}</h1>
                <b :class="statusBadgeClass(chat.runStatus)">{{ runStatusLabel }}</b>
                <button
                  v-if="chat.activeSession?.status === 'ACTIVE'"
                  class="chat-inline-action"
                  type="button"
                  :title="t('chat.archiveTitle')"
                  :disabled="archivingSession"
                  :aria-busy="archivingSession ? 'true' : undefined"
                  @click="archiveCurrentSession"
                >
                  {{ archivingSession ? t("chat.archiving") : t("chat.archive") }}
                </button>
              </div>
              <p>
                <span>{{ chat.activeSession?.title || t("chat.defaultSessionTitle") }}</span>
                <i aria-hidden="true">/</i>
                <span>{{
                  activeSessionWorkspace?.displayName || activeSessionWorkspace?.name || t("chat.noWorkspace")
                }}</span>
                <i aria-hidden="true">/</i>
                <span>{{ activeSessionAgent?.name || chat.activeSession?.agentId || activeAgentLabel }}</span>
                <b
                  v-if="activeSessionReadOnly"
                  class="runtime-agent-state"
                  :title="t('chat.agentUnavailableTitle', { status: activeSessionAgentStatusLabel })"
                >
                  {{ activeSessionAgentStatusLabel }}
                </b>
              </p>
            </div>
            <div class="runtime-summary-list" :aria-label="t('chat.runtimeSummaryAria')">
              <span
                ><small>{{ t("chat.intent") }}</small
                ><strong>{{ runtimeIntentLabel }}</strong></span
              >
              <span
                ><small>{{ t("chat.capabilitiesThisTurn") }}</small
                ><strong>{{ capabilityCountLabel }}</strong></span
              >
            </div>
          </div>
        </div>

        <section class="debug-console-banner" role="status" data-testid="debug-console-nonprod-banner">
          <i class="fa-solid fa-flask" aria-hidden="true" />
          <div>
            <strong>{{ t("chat.debugBannerTitle") }}</strong>
            <p>{{ t("chat.debugBannerBody") }}</p>
          </div>
        </section>
        <div class="debug-subject-bar" data-testid="debug-console-subject" :aria-label="t('chat.currentSubjectAria')">
          <span
            ><small>{{ t("chat.currentSubject") }}</small
            ><strong>{{ currentSubjectLabel }}</strong></span
          >
          <span v-if="outboundAttachmentId"
            ><small>{{ t("chat.outboundBinding") }}</small
            ><strong>{{ t("chat.boundNextMessage") }}</strong></span
          >
          <span v-else
            ><small>{{ t("chat.outboundBinding") }}</small
            ><strong>{{ t("chat.notBound") }}</strong></span
          >
        </div>

        <section v-if="activeSessionReadOnly" class="chat-session-agent-alert" role="status">
          <div class="chat-session-agent-alert-copy">
            <i class="fa-solid fa-user-slash" aria-hidden="true" />
            <div>
              <strong>{{ t("chat.linkedAgentStatus", { status: activeSessionAgentStatusLabel }) }}</strong>
              <p>
                {{
                  t("chat.sessionReadOnlyBody", {
                    name: activeSessionAgent?.name || chat.activeSession?.agentId,
                  })
                }}
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
            <span>{{
              contextLoading ? t("chat.loadingAgents") : t("chat.newSessionWithAgent", { name: activeAgentLabel })
            }}</span>
          </button>
        </section>

        <div
          ref="chatScrollArea"
          class="chat-scroll-area"
          role="log"
          :aria-label="t('chat.messagesAria')"
          aria-live="polite"
          aria-relevant="additions text"
          :aria-busy="conversationBusy"
          @scroll.passive="handleConversationScroll"
        >
          <div v-if="!chat.messages.length" class="chat-empty-state">
            <span><i class="fa-solid fa-robot" aria-hidden="true" /></span>
            <strong>{{ t("chat.emptyTitle") }}</strong>
            <p>{{ t("chat.emptyBody") }}</p>
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
                  <div
                    v-if="message.content"
                    class="assistant-bubble"
                    v-html="renderMessageMarkdown(message.content)"
                  />
                  <!--
                    A reply is still being produced. Without this, a stream that
                    pauses — the agent thinking, or writing a surface, which is
                    delivered whole at the end and so streams no text — looks
                    exactly like a finished answer.
                  -->
                  <p v-if="message.status === 'PROCESSING'" class="assistant-working" role="status">
                    <span class="assistant-working-dots" aria-hidden="true"><span /><span /><span /></span>
                    <span>{{ t("chat.assistantWorking") }}</span>
                  </p>
                  <A2UISurface
                    v-for="(surface, surfaceIndex) in message.a2ui ?? []"
                    :key="`${message.id}-a2ui-${surfaceIndex}`"
                    :surface="surface"
                    :uid="`${message.id}-${surfaceIndex}`"
                  />
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
                <strong>{{ t("chat.riskGateTitle") }}</strong>
              </div>
              <span>MANUAL GATE</span>
            </div>
            <p>{{ t("chat.riskGateBody") }}</p>
            <ul>
              <li v-for="reason in chat.pendingConfirmation.riskReasons" :key="reason">
                <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
                <span>{{ reason }}</span>
              </li>
            </ul>
            <div class="risk-confirm-row">
              <span v-if="!chat.pendingResumeToken" role="status">{{ t("chat.resumeTokenMissing") }}</span>
              <button
                type="button"
                :disabled="confirming || activeSessionReadOnly"
                :aria-busy="confirming"
                @click="cancelConfirmation"
              >
                <i class="fa-solid fa-ban" aria-hidden="true" />
                {{ t("chat.cancelAction") }}
              </button>
              <button
                type="button"
                :disabled="confirming || activeSessionReadOnly || !chat.pendingResumeToken"
                :aria-busy="confirming"
                @click="confirm"
              >
                <i class="fa-solid fa-unlock-keyhole" aria-hidden="true" />
                {{ confirming ? t("chat.authorizing") : t("chat.authorizeRun") }}
              </button>
            </div>
          </section>
        </div>

        <button v-if="hasUnreadMessages" class="chat-new-message-button" type="button" @click="revealLatestMessages">
          <i class="fa-solid fa-arrow-down" aria-hidden="true" />
          <span>{{ t("chat.newMessages") }}</span>
        </button>

        <div class="chat-composer-dock">
          <p v-if="actionError" id="chat-action-error" class="chat-action-error" role="alert" aria-live="assertive">
            {{ actionError }}
          </p>
          <div v-if="!chat.messages.length && !activeSessionReadOnly" class="prompt-suggestion-strip">
            <span>{{ t("chat.noShortcuts") }}</span>
          </div>
          <div
            v-if="chat.activeSession && !activeSessionReadOnly"
            class="debug-outbound-dock"
            data-testid="debug-outbound-credential-panel"
          >
            <label v-if="requiresPassthroughToken && passthroughConnections.length > 1" class="debug-connection-picker">
              {{ t("chat.passthroughConnection") }}
              <select v-model="debugPassthroughConnectionId" :aria-label="t('chat.selectPassthroughConnection')">
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
              :aria-label="t('chat.composerAria')"
              :placeholder="composerPlaceholder"
              :disabled="activeSessionReadOnly"
              :aria-describedby="actionError ? 'chat-composer-shortcut chat-action-error' : 'chat-composer-shortcut'"
              @keydown.enter.exact.prevent="send"
            />
            <span id="chat-composer-shortcut" class="chat-sr-only">{{ t("chat.composerShortcut") }}</span>
            <button
              type="button"
              :disabled="sending || activeSessionReadOnly || !composer.trim()"
              :aria-busy="sending"
              @click="send"
            >
              <i class="fa-solid" :class="sending ? 'fa-spinner' : 'fa-paper-plane'" aria-hidden="true" />
              <span>{{ sending ? t("chat.sending") : t("chat.send") }}</span>
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
            :aria-label="t('chat.historyAria')"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="chat-session-head">
              <div>
                <span>{{ t("chat.historySessions") }}</span>
                <h2>{{ t("chat.executionRecords") }}</h2>
              </div>
              <div class="chat-panel-head-actions">
                <button
                  class="chat-panel-icon-button primary"
                  type="button"
                  :title="t('chat.createSession')"
                  :aria-label="t('chat.createSession')"
                  @click="newSession"
                >
                  <i class="fa-solid fa-plus" aria-hidden="true" />
                </button>
                <button
                  ref="sessionPanelCloseButton"
                  class="chat-panel-icon-button"
                  type="button"
                  :title="t('chat.closeHistory')"
                  :aria-label="t('chat.closeHistory')"
                  @click="closeSidePanel"
                >
                  <i class="fa-solid fa-xmark" aria-hidden="true" />
                </button>
              </div>
            </div>
            <label class="chat-session-search">
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input v-model="sessionKeyword" type="text" :placeholder="t('chat.filterSessions')" />
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
                      {{ session.status === "ARCHIVED" ? t("chat.archived") : t("chat.unavailable") }}
                    </b>
                  </small>
                  <small>{{ sessionTime(session.updatedAt) }}</small>
                </span>
              </button>

              <div v-if="!filteredSessions.length" class="chat-session-empty">
                <i class="fa-regular fa-message" aria-hidden="true" />
                <strong>{{ t("chat.noMatchTitle") }}</strong>
                <small>{{ t("chat.noMatchBody") }}</small>
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
            :aria-label="t('chat.runtimeDetail')"
            tabindex="-1"
            @keydown.tab="trapSidePanelFocus"
          >
            <div class="runtime-monitor-head">
              <div>
                <span>{{ t("chat.runtimeDetail") }}</span>
                <h2>{{ chat.latestRun ? t("chat.runAndTrace") : t("chat.waitingRun") }}</h2>
              </div>
              <button
                ref="runtimePanelCloseButton"
                class="chat-panel-icon-button"
                type="button"
                :title="t('chat.closeRuntime')"
                :aria-label="t('chat.closeRuntime')"
                @click="closeSidePanel"
              >
                <i class="fa-solid fa-xmark" aria-hidden="true" />
              </button>
            </div>

            <div class="runtime-monitor-body">
              <section class="runtime-decision-card">
                <div>
                  <span>{{ t("chat.decisionTarget") }}</span>
                  <small>{{ chat.latestRun?.triggerType || t("chat.pendingIdentify") }}</small>
                </div>
                <strong>{{ runtimeTargetReleaseId || t("chat.noCapabilityRelease") }}</strong>
                <p>{{ runtimeSummary }}</p>
                <div class="runtime-decision-meta">
                  <span>{{ t("chat.riskLevel") }}</span>
                  <b>{{ chat.pendingConfirmation?.riskLevel || t("chat.riskStandard") }}</b>
                </div>
              </section>

              <section class="runtime-step-section">
                <div class="runtime-section-title">
                  <span>{{ t("chat.executionChain") }}</span>
                  <small>{{ t("chat.eventCount", { n: latestRunStepRows.length }) }}</small>
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
                  <strong>{{ t("chat.noEventsTitle") }}</strong>
                  <small>{{ t("chat.noEventsBody") }}</small>
                </div>
              </section>

              <details class="runtime-policy-card" :open="chat.pendingConfirmation?.status === 'PENDING'">
                <summary>
                  <span><i class="fa-solid fa-shield-halved" aria-hidden="true" />{{ t("chat.securityPolicy") }}</span>
                  <span class="runtime-disclosure-state">
                    <small v-if="chat.pendingConfirmation?.status === 'PENDING'">{{
                      t("chat.needsConfirmation")
                    }}</small>
                    <i class="fa-solid fa-angle-down" aria-hidden="true" />
                  </span>
                </summary>
                <div class="runtime-policy-detail">
                  <i class="fa-solid fa-shield-halved" aria-hidden="true" />
                  <span>
                    <strong>{{ t("chat.sensitiveAccess") }}</strong>
                    <small>{{ t("chat.sensitiveAccessBody") }}</small>
                  </span>
                </div>
              </details>

              <details class="runtime-trace-section">
                <summary>
                  <span><i class="fa-solid fa-bug" aria-hidden="true" />{{ t("chat.techTrace") }}</span>
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
