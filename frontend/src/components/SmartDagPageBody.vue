<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 14)
/** Smart DAG page body (ZKL-64 item 14). */
import SmartDagModals from "./SmartDagModals.vue";
import { useSmartDagPageContext } from "../composables/useSmartDagPageContext";

const scp = useSmartDagPageContext();
const {
  smart,
  activeWorkspaceId,
  selectedAgentId,
  selectedNodeId,
  copilotPrompt,
  canvasZoom,
  canvasPan,
  canvasPanning,
  isNarrowViewport,
  blueprintToolbarCompact,
  leftPanelCollapsed,
  rightPanelCollapsed,
  focusMode,
  aiStatus,
  compilerIssues,
  toast,
  canvasContainerRef,
  workspaces,
  workspaceAgents,
  agentHasUsableModel,
  canSendGenerateTurn,
  turnHistory,
  currentBlueprint,
  canvasRenderKey,
  selectedNode,
  hasAiDraft,
  draftGenerated,
  canPublishSmartDraft,
  aiSteps,
  closeToast,
  showToast,
  openBlueprintPicker,
  openSandbox,
  getStatusText,
  getStatusClass,
  getNodeTypeClass,
  getParameterSchema,
  getConnectionPath,
  createBlankBlueprint,
  generateDraft,
  retryLastGenerateTurn,
  closeGenerateSessionWithConfirm,
  startNewGenerateSession,
  finishGeneration,
  acceptAiDraft,
  discardAiDraft,
  applyAutoLayout,
  saveDraft,
  validateSmartWorkflow,
  openInWorkflowEditor,
  publishWorkflow,
  deleteNode,
  startCanvasPan,
  moveCanvasPan,
  endCanvasPan,
  zoomIn,
  zoomOut,
  resetCanvas,
} = scp;
void SmartDagModals;
</script>

<template>
  <div class="smart-orchestration-page">
    <div v-if="toast.show" class="smart-toast" :class="`is-${toast.tone}`" role="status" aria-live="polite">
      <i class="fa-solid" :class="toast.tone === 'error' ? 'fa-circle-exclamation' : 'fa-circle-check'" />
      <span>{{ toast.message }}</span>
      <button type="button" aria-label="关闭提示" title="关闭提示" @click="closeToast">
        <i class="fa-solid fa-xmark" />
      </button>
    </div>
    <div class="smart-screen-warning" role="note">
      <strong>智能编排画布需要桌面宽度</strong>
      <span>当前页面包含左右面板和大画布，建议在 1180px 以上窗口完成编辑。</span>
    </div>
    <div class="smart-narrow-blocker" role="note">
      <strong>当前宽度不支持编辑</strong>
      <span>请切换到 1180px 以上桌面窗口，或使用浏览器全屏后继续编辑智能编排。</span>
    </div>

    <main
      class="smart-orchestration-main"
      :inert="isNarrowViewport ? true : undefined"
      :aria-hidden="isNarrowViewport ? 'true' : undefined"
    >
      <div class="smart-blueprint-toolbar" :class="{ 'is-compact': blueprintToolbarCompact }">
        <div class="smart-blueprint-toolbar-inner">
          <button
            v-if="blueprintToolbarCompact"
            class="smart-blueprint-toolbar-restore-button"
            type="button"
            aria-label="展开蓝图工具栏"
            title="展开蓝图工具栏"
            @click="blueprintToolbarCompact = false"
          >
            <i class="fa-solid fa-up-right-and-down-left-from-center" />
            <span>
              <small>当前蓝图</small>
              <strong>{{ currentBlueprint.name }}</strong>
            </span>
            <span class="smart-status-badge" :class="getStatusClass(currentBlueprint.status)">{{
              getStatusText(currentBlueprint.status)
            }}</span>
          </button>
          <div v-if="blueprintToolbarCompact" class="smart-blueprint-toolbar-compact-actions">
            <button type="button" aria-label="保存画布" title="保存画布" @click="saveDraft()">
              <i class="fa-regular fa-floppy-disk" />
            </button>
            <button type="button" aria-label="打开蓝图" title="打开蓝图" @click="openBlueprintPicker">
              <i class="fa-solid fa-folder-open" />
            </button>
          </div>
          <template v-else>
            <div class="smart-current-blueprint">
              <div>
                <span>当前蓝图</span>
                <strong>{{ currentBlueprint.name }}</strong>
                <small>{{ currentBlueprint.space }} · AI {{ currentBlueprint.aiScore }}%</small>
              </div>
              <div class="smart-current-blueprint-actions">
                <span class="smart-status-badge" :class="getStatusClass(currentBlueprint.status)">{{
                  getStatusText(currentBlueprint.status)
                }}</span>
                <button
                  class="smart-blueprint-toolbar-compact-button"
                  type="button"
                  aria-label="缩小蓝图工具栏"
                  title="缩小蓝图工具栏"
                  @click="blueprintToolbarCompact = true"
                >
                  <i class="fa-solid fa-down-left-and-up-right-to-center" />
                  <span>收起</span>
                </button>
              </div>
            </div>
            <div class="smart-toolbar-actions">
              <button class="smart-toolbar-button" type="button" @click="saveDraft()">
                <i class="fa-regular fa-floppy-disk" />
                保存画布
              </button>
              <button
                class="smart-toolbar-button"
                type="button"
                data-testid="toolbar-auto-layout"
                data-action="auto-layout-smart-canvas"
                title="按拓扑分层展开节点，消除堆叠与交叉"
                :disabled="!currentBlueprint.id || !currentBlueprint.nodes.length"
                @click="applyAutoLayout"
              >
                <i class="fa-solid fa-diagram-project" />
                格式化画布
              </button>
              <button
                class="smart-publish-button"
                type="button"
                :data-readiness="canPublishSmartDraft ? 'ready' : 'blocked'"
                @click="publishWorkflow"
              >
                <i class="fa-solid fa-rocket" />
                发布上线
              </button>
              <button
                class="smart-toolbar-button"
                type="button"
                data-testid="toolbar-open-blueprint"
                @click="openBlueprintPicker"
              >
                <i class="fa-solid fa-folder-open" />
                打开蓝图
              </button>
              <button
                class="smart-dark-button"
                type="button"
                data-testid="toolbar-create-blueprint"
                @click="createBlankBlueprint()"
              >
                <i class="fa-solid fa-plus" />
                新建草稿
              </button>
              <button
                class="smart-icon-button"
                type="button"
                aria-label="编译诊断"
                title="编译诊断"
                @click="validateSmartWorkflow"
              >
                <i class="fa-solid fa-shield-heart" />
              </button>
              <button
                class="smart-icon-button"
                type="button"
                aria-label="打开模拟试运行"
                title="模拟跑"
                @click="openSandbox"
              >
                <i class="fa-solid fa-flask" />
              </button>
            </div>
          </template>
        </div>
      </div>

      <div
        id="canvas-container"
        ref="canvasContainerRef"
        class="smart-canvas-container grid-matrix-bg canvas-grabbable"
        :class="{ 'is-panning': canvasPanning.active }"
        @pointerdown="startCanvasPan"
        @pointermove="moveCanvasPan"
        @pointerup="endCanvasPan"
        @pointercancel="endCanvasPan"
        @wheel.prevent="$event.deltaY < 0 ? zoomIn() : zoomOut()"
      >
        <div class="smart-canvas-hint">
          <i class="fa-regular fa-hand" />
          <span>拖动画布 · 滚轮缩放</span>
        </div>
        <div
          id="canvas-workspace"
          class="smart-canvas-workspace"
          :key="canvasRenderKey"
          :style="{ transform: `translate(${canvasPan.x}px, ${canvasPan.y}px) scale(${canvasZoom})` }"
        >
          <svg id="canvas-svg" class="smart-canvas-svg">
            <g v-for="(conn, idx) in currentBlueprint.connections" :key="`${conn.from}-${conn.to}-${idx}`">
              <path :d="getConnectionPath(conn)" stroke="rgba(20, 184, 166, 0.05)" stroke-width="10" fill="none" />
              <path
                :d="getConnectionPath(conn)"
                stroke="#0d9488"
                stroke-width="2.5"
                fill="none"
                class="connection-path"
              />
            </g>
          </svg>

          <article
            v-for="(node, idx) in currentBlueprint.nodes"
            :id="node.id"
            :key="node.id"
            class="smart-canvas-node"
            :class="{ selected: selectedNodeId === node.id, 'is-ai-draft': node.isAiDraft }"
            :data-node-id="node.id"
            :data-node-idx="idx"
            :style="{ transform: `translate(${node.x}px, ${node.y}px)` }"
            @click.stop="selectedNodeId = node.id"
          >
            <header>
              <span class="smart-node-type" :class="getNodeTypeClass(node.theme)">{{ node.type }}</span>
              <span v-if="node.isAiDraft" class="smart-ai-chip"><i class="fa-solid fa-sparkles" />AI Draft</span>
              <button type="button" aria-label="删除此节点" title="删除此节点" @click.stop="deleteNode(node.id)">
                <i class="fa-solid fa-trash-can" />
              </button>
            </header>
            <div>
              <strong>{{ node.title }}</strong>
              <small>{{ node.desc }}</small>
              <p v-if="node.aiReason">{{ node.aiReason }}</p>
            </div>
            <i v-if="node.type !== 'END'" class="smart-node-port output" />
            <i v-if="node.type !== 'START'" class="smart-node-port input" />
          </article>
        </div>
      </div>

      <div v-if="hasAiDraft" class="smart-ai-draft-banner">
        <div>
          <i class="fa-solid fa-wand-magic-sparkles" />
          <div>
            <strong>AI 已生成正式 Workflow Draft</strong>
            <span>草稿节点已持久化为 workflow.graph.v1。可继续微调、编译、试运行或进入普通编排。</span>
          </div>
        </div>
        <button type="button" @click="discardAiDraft">废弃草稿</button>
        <button type="button" class="primary" @click="acceptAiDraft">接受草稿</button>
      </div>

      <div class="smart-zoom-dock">
        <button type="button" aria-label="放大画布" title="放大画布" @click="zoomIn">
          <i class="fa-solid fa-plus" />
        </button>
        <span>{{ Math.round(canvasZoom * 100) }}%</span>
        <button type="button" aria-label="缩小画布" title="缩小画布" @click="zoomOut">
          <i class="fa-solid fa-minus" />
        </button>
        <i />
        <button type="button" aria-label="适配画布" title="适配画布" @click="resetCanvas">
          <i class="fa-solid fa-expand" />
        </button>
        <i />
        <button
          type="button"
          aria-label="切换专注模式"
          title="专注模式"
          :class="{ active: focusMode }"
          @click="focusMode = !focusMode"
        >
          <i class="fa-solid fa-eye-slash" />
        </button>
      </div>

      <aside class="smart-copilot-panel glass-panel" :class="{ collapsed: leftPanelCollapsed, dimmed: focusMode }">
        <button
          v-if="leftPanelCollapsed"
          class="smart-collapse-button"
          type="button"
          aria-label="展开 AI Copilot 面板"
          title="展开 AI Copilot 面板"
          @click="leftPanelCollapsed = false"
        >
          <i class="fa-solid fa-wand-magic-sparkles" />
        </button>
        <template v-else>
          <header>
            <span><i class="fa-solid fa-sparkles" />AI Copilot</span>
            <button
              type="button"
              aria-label="折叠 AI Copilot 面板"
              title="折叠 AI Copilot 面板"
              @click="leftPanelCollapsed = true"
            >
              <i class="fa-solid fa-angles-left" />
            </button>
          </header>
          <h2>多轮智能生成</h2>
          <p>选择 Workspace 与 Agent 后，用自然语言多轮修订流程；每轮成功后画布按最新 Draft 刷新。</p>
          <p class="smart-publish-bind-hint" data-testid="smart-publish-bind-hint">
            生成满意 ≠ Agent 已可用：须完成编译、试运行、发布，并绑定到 Agent 后，Console 对话台才能调用本 Workflow。
          </p>

          <div class="smart-draft-summary">
            <span>当前草稿</span>
            <b>{{ currentBlueprint.name }}</b>
            <small
              >只保存 Workflow Draft，不自动发布；正式 binding 仅在 publish 之后（默认绑回生成会话 Agent）。{{
                smart.sessionStatus ? `会话 ${smart.sessionStatus}` : "尚未开始会话"
              }}</small
            >
          </div>

          <label>
            <span>业务空间</span>
            <select v-model="activeWorkspaceId" :disabled="aiStatus.isGenerating || smart.generating">
              <option value="" disabled>请选择业务空间</option>
              <option v-for="workspace in workspaces" :key="workspace.id" :value="workspace.id">
                {{ workspace.displayName || workspace.name }}
              </option>
            </select>
          </label>

          <label>
            <span>生成 Agent</span>
            <select
              v-model="selectedAgentId"
              :disabled="aiStatus.isGenerating || smart.generating || !activeWorkspaceId"
            >
              <option value="" disabled>请选择 Agent</option>
              <option v-for="agent in workspaceAgents" :key="agent.id" :value="agent.id">
                {{ agent.name }}{{ agent.modelConfigId ? "" : "（未绑定模型）" }}
              </option>
            </select>
            <em v-if="selectedAgentId && !agentHasUsableModel" class="smart-agent-model-hint">
              当前 Agent 未配置可用模型，请先在 Agent 设置中绑定 Model Config。
            </em>
          </label>

          <section v-if="turnHistory.length" class="smart-turn-history" aria-label="生成轮次历史">
            <strong>多轮对话（生成专用）</strong>
            <article
              v-for="turn in turnHistory"
              :key="turn.turnId"
              class="smart-turn-item"
              :class="{ 'is-failed': !turn.guardOk }"
            >
              <span class="smart-turn-role">你 · #{{ turn.turnIndex }}</span>
              <p>{{ turn.userMessage }}</p>
              <span v-if="turn.assistantMessage" class="smart-turn-role">助手</span>
              <p v-if="turn.assistantMessage">{{ turn.assistantMessage }}</p>
              <small v-if="turn.draftVersion">draftVersion={{ turn.draftVersion }} · {{ turn.status }}</small>
              <small v-else-if="turn.errorCode">{{ turn.errorCode }}</small>
            </article>
          </section>

          <div v-if="smart.lastGuardReport && !smart.lastGuardReport.ok" class="smart-guard-report" role="alert">
            <strong>Guard 拒绝</strong>
            <p v-for="(v, idx) in smart.lastGuardReport.violations" :key="idx">{{ v.code }}：{{ v.message }}</p>
          </div>

          <!-- ZKL-56 DEF-02 / UX-04: persistent recovery card (not toast-only). -->
          <section
            v-if="smart.lastFailure"
            class="smart-recovery-card"
            role="alert"
            data-testid="smart-dag-recovery-card"
            aria-live="assertive"
          >
            <header>
              <strong>本轮生成未完成</strong>
              <span class="smart-recovery-stage">{{ smart.lastFailure.stage || "UNKNOWN" }}</span>
            </header>
            <p class="smart-recovery-message">{{ smart.lastFailure.message }}</p>
            <dl class="smart-recovery-meta">
              <div v-if="smart.lastFailure.code">
                <dt>错误码</dt>
                <dd>{{ smart.lastFailure.code }}</dd>
              </div>
              <div v-if="smart.lastFailure.sessionStatus || smart.sessionStatus">
                <dt>会话</dt>
                <dd>{{ smart.lastFailure.sessionStatus || smart.sessionStatus || "—" }}</dd>
              </div>
              <div v-if="smart.lastFailure.requestId">
                <dt>requestId</dt>
                <dd>{{ smart.lastFailure.requestId }}</dd>
              </div>
              <div v-if="smart.lastFailure.traceId">
                <dt>traceId</dt>
                <dd>{{ smart.lastFailure.traceId }}</dd>
              </div>
            </dl>
            <p class="smart-recovery-hint">上一合法 Draft 与输入已保留；失败不会自动发布。生成中不支持执行中取消。</p>
            <div class="smart-recovery-actions">
              <button
                v-if="smart.recoveryActions.retry"
                type="button"
                class="primary"
                data-testid="smart-dag-retry"
                :disabled="smart.generating || aiStatus.isGenerating"
                @click="retryLastGenerateTurn"
              >
                重试本轮
              </button>
              <button
                v-if="smart.recoveryActions.close"
                type="button"
                data-testid="smart-dag-close-session"
                :disabled="smart.generating || aiStatus.isGenerating"
                @click="closeGenerateSessionWithConfirm"
              >
                关闭会话
              </button>
              <button
                v-if="smart.recoveryActions.fixConfig"
                type="button"
                data-testid="smart-dag-fix-config"
                @click="showToast('请检查 Agent 模型绑定、工具目录或网络后重试。', 'info')"
              >
                修复配置
              </button>
              <button
                v-if="smart.recoveryActions.createNew"
                type="button"
                class="primary"
                data-testid="smart-dag-new-session"
                :disabled="smart.generating"
                @click="startNewGenerateSession"
              >
                新建会话
              </button>
            </div>
          </section>

          <div v-if="smart.missingCapabilities.length" class="smart-missing-capabilities">
            <strong>能力缺口</strong>
            <p v-for="cap in smart.missingCapabilities" :key="cap.id">{{ cap.name }} — {{ cap.reason }}</p>
          </div>

          <label>
            <span>本轮自然语言意图</span>
            <textarea
              v-model="copilotPrompt"
              rows="5"
              placeholder="输入业务目标或修订意图，例如：加审批节点、换支付查询工具。"
              :disabled="(!canSendGenerateTurn && !agentHasUsableModel) || smart.sessionStatus === 'CLOSED'"
            />
            <em>{{ copilotPrompt.length }} 字</em>
          </label>

          <div v-if="aiStatus.isGenerating" class="smart-ai-steps">
            <strong>AI 正在推理编排结构...</strong>
            <span v-for="(step, idx) in aiSteps" :key="step" :class="{ active: aiStatus.activeStep >= idx }"
              >{{ idx + 1 }}. {{ step }}</span
            >
          </div>

          <div class="smart-copilot-actions">
            <button type="button" @click="copilotPrompt = ''">清空输入</button>
            <button type="button" @click="openBlueprintPicker">打开蓝图</button>
            <button
              type="button"
              class="primary"
              :disabled="!canSendGenerateTurn || !copilotPrompt.trim() || smart.sessionStatus === 'CLOSED'"
              :title="
                !agentHasUsableModel
                  ? '请先为 Agent 配置模型'
                  : smart.sessionStatus === 'CLOSED'
                    ? '会话已关闭，请新建会话'
                    : '发送本轮生成'
              "
              @click="generateDraft"
            >
              <i class="fa-solid fa-wand-magic-sparkles" />
              {{
                aiStatus.isGenerating || smart.generating
                  ? "AI 生成中..."
                  : smart.generatedDraft
                    ? "发送本轮修订"
                    : "开始多轮生成"
              }}
            </button>
            <button
              v-if="draftGenerated"
              type="button"
              class="primary"
              :disabled="smart.generating"
              @click="finishGeneration"
            >
              完成生成
            </button>
            <button v-if="draftGenerated" type="button" @click="openInWorkflowEditor">进入普通编排</button>
          </div>
        </template>
      </aside>

      <aside class="smart-property-panel glass-panel" :class="{ collapsed: rightPanelCollapsed, dimmed: focusMode }">
        <button
          v-if="rightPanelCollapsed"
          class="smart-collapse-button"
          type="button"
          aria-label="展开属性面板"
          title="展开属性面板"
          @click="rightPanelCollapsed = false"
        >
          <i class="fa-solid fa-sliders" />
        </button>
        <template v-else>
          <header>
            <div>
              <span><i class="fa-solid fa-sliders" />属性面板</span>
              <h2>{{ selectedNode?.title || "未选择节点" }}</h2>
            </div>
            <button type="button" aria-label="折叠属性面板" title="折叠属性面板" @click="rightPanelCollapsed = true">
              <i class="fa-solid fa-angles-right" />
            </button>
          </header>

          <template v-if="selectedNode">
            <label>
              <span>节点名称</span>
              <input v-model="selectedNode.title" type="text" />
            </label>
            <label>
              <span>说明备注</span>
              <textarea v-model="selectedNode.desc" rows="2" />
            </label>
            <div v-if="selectedNode.aiReason" class="smart-ai-reason">
              <strong><i class="fa-solid fa-shield-halved" />AI 生成说明</strong>
              <p>{{ selectedNode.aiReason }}</p>
            </div>
            <div class="smart-schema-card">
              <span>输入端口 Schema (JSON)</span>
              <code>{{ getParameterSchema(selectedNode.type) }}</code>
            </div>
          </template>
          <div v-else class="smart-panel-empty">在画布上点击任意节点以修改其属性</div>

          <section class="smart-compiler-panel">
            <div>
              <strong><i class="fa-solid fa-circle-exclamation" />编译状态</strong>
              <span>{{ compilerIssues.length }} 条提示</span>
            </div>
            <article v-for="issue in compilerIssues" :key="issue.title">
              <strong>{{ issue.title }}</strong>
              <small>{{ issue.desc }}</small>
            </article>
            <div v-if="!compilerIssues.length" class="smart-compile-ok">
              <i class="fa-solid fa-check" />
              <strong>暂无后端编译问题</strong>
              <small>点击编译诊断后，以 Workflow v1 Compiler 和 Readiness 返回结果为准。</small>
            </div>
          </section>

          <button class="smart-save-draft-button" type="button" @click="saveDraft()">保存 Workflow Draft</button>
        </template>
      </aside>
    </main>
    <SmartDagModals />
  </div>
</template>
