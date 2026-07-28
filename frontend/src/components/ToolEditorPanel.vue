<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 12)
/** Tool editor panel (ZKL-64 item 12). */
import AppSelect from "./AppSelect.vue";
import ToolContractHybridEditor from "./ToolContractHybridEditor.vue";
import ToolFlatContractEditor from "./ToolFlatContractEditor.vue";
import { useToolsPageContext } from "../composables/useToolsPageContext";

/* eslint-disable @typescript-eslint/no-unused-vars -- inject surface for template */
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
            <p>配置 Agent 可调用的业务接口动作</p>
          </div>
        </div>
        <nav class="tool-hybrid-progress" aria-label="Tool 创建步骤">
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
          :aria-label="`关闭${toolEditorTitle}`"
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
                  <div><span>01</span><strong>基础信息</strong></div>
                  <small>用于 Agent 识别和团队管理</small>
                </div>
                <label class="drawer-field"
                  ><span>Tool 名称 <b>*</b></span
                  ><input v-model="draftTool.name" placeholder="例如：拦截发货"
                /></label>
                <label class="drawer-field">
                  <span>业务空间 <b>*</b></span>
                  <AppSelect v-model="draftTool.workspaceId" :options="workspaceOptions" placeholder="选择业务空间" />
                </label>
              </section>

              <section class="tool-hybrid-form-section">
                <div class="tool-hybrid-section-head">
                  <div><span>02</span><strong>接口动作</strong></div>
                  <small>连接地址与认证由服务连接继承</small>
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
                  ><span>动作说明</span
                  ><textarea v-model="draftTool.description" rows="3" placeholder="说明这个 Tool 会执行什么业务动作" />
                </label>
                <div class="tool-endpoint-preview">
                  <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
                  <strong>{{ endpointPreviewLabel() }}</strong>
                </div>
              </section>
            </div>

            <aside class="connection-reference-card tool-connection-summary-card tool-hybrid-connection-card">
              <label class="drawer-field">
                <span>服务连接 <b>*</b></span>
                <AppSelect
                  v-model="draftTool.connectionId"
                  :options="serviceConnectionOptions"
                  placeholder="选择服务连接"
                />
              </label>
              <div class="connection-reference-head tool-connection-summary-head">
                <i class="fa-solid fa-server" />
                <div>
                  <strong>{{ draftConnection?.name || "未选择服务连接" }}</strong>
                  <small>统一继承域名、Base Path 与认证方式。</small>
                </div>
                <span
                  class="status-pill tool-connection-summary-status"
                  :class="statusClass(draftConnection?.status || 'Disabled')"
                  >{{ draftConnection?.status || "未配置" }}</span
                >
              </div>
              <div class="tool-connection-summary-grid">
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-globe" />
                  <div>
                    <span>服务域名</span
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
                    <span>认证方式</span
                    ><strong class="tool-connection-summary-value">{{ authModeLabel(draftConnection) }}</strong>
                  </div>
                </div>
                <div class="tool-connection-summary-meta">
                  <i class="fa-solid fa-layer-group" />
                  <div>
                    <span>环境</span
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
                <small>保存后进入草稿状态，测试与发布在详情页继续。</small>
              </div>
              <button
                class="ghost-button full tool-connection-summary-action"
                type="button"
                @click="router.push('/connections')"
              >
                管理服务连接
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
              >继承服务连接：<b>{{ draftConnection?.name || "未选择服务连接" }}</b></span
            >
            <i />
            <span>Capability Binding：<b>发布后独立配置</b></span>
            <button type="button" @click="goToDraftStep(1)">编辑接口</button>
          </div>

          <div class="tool-contract-body-wrap">
            <aside class="tool-contract-side-tabs">
              <div role="tablist" aria-label="契约分组">
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

            <section class="tool-contract-main-panel" role="tabpanel" :aria-label="`${contractEditorTab} 契约`">
              <ToolFlatContractEditor
                v-if="contractEditorTab === 'Path' || contractEditorTab === 'Query' || contractEditorTab === 'Header'"
                v-model="activeRequestFlatContract"
                :location="contractEditorTab"
              />
              <ToolContractHybridEditor
                v-else-if="contractEditorTab === 'Body'"
                v-model="requestBodyContract"
                title="请求体 Body"
                description="复杂结构使用字段树维护；JSON 是只读派生预览。"
                root-label="Request Body Contract"
                compact
              />
              <ToolContractHybridEditor
                v-else-if="contractEditorTab === 'Response'"
                v-model="responseBodyContract"
                title="成功响应"
                description="描述 Agent 可以读取和引用的响应字段。"
                root-label="Response Contract"
                compact
              />
              <div v-else class="tool-error-mapping-panel">
                <div class="tool-error-mapping-head">
                  <div><strong>错误映射</strong><span>把协议错误翻译为 Agent 可理解、可执行的建议。</span></div>
                  <button type="button" @click="addErrorMapping"><i class="fa-solid fa-plus" /> 新增映射</button>
                </div>
                <div v-if="draftTool.errorMappings.length" class="tool-error-mapping-table">
                  <div class="tool-error-mapping-row tool-error-mapping-header">
                    <span>HTTP Status</span><span>Error Code</span><span>Agent 建议</span><span>操作</span>
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
                    <input v-model="mapping.agentAdvice" aria-label="Agent 建议" placeholder="停止执行并转人工确认" />
                    <button
                      class="tool-flat-delete"
                      type="button"
                      :aria-label="`删除错误映射 ${mapping.errorCode || index + 1}`"
                      @click="removeErrorMapping(index)"
                    >
                      <i class="fa-solid fa-xmark" />
                    </button>
                  </div>
                </div>
                <div v-else class="tool-schema-empty"><span>暂无错误映射，点击“新增映射”开始配置。</span></div>
              </div>
            </section>
          </div>
        </template>

        <template v-else>
          <div class="tool-review-heading">
            <div><span>03</span><strong>确认并保存草稿</strong></div>
            <small>测试调用与发布将在保存后的 Tool 详情中完成。</small>
          </div>
          <div class="tool-review-summary-grid">
            <section>
              <i class="fa-solid fa-wand-magic-sparkles" />
              <div>
                <span>Tool</span><strong>{{ draftTool.name || "未命名 Tool" }}</strong
                ><small>{{ workspaceDisplayLabel(draftTool.workspaceId) }} · Capability Binding 独立管理</small>
              </div>
              <button type="button" @click="goToDraftStep(1)">编辑</button>
            </section>
            <section>
              <i class="fa-solid fa-link" />
              <div>
                <span>Endpoint</span><strong class="mono">{{ draftTool.method }} {{ draftTool.path || "/" }}</strong
                ><small>{{ draftConnection?.name || "未选择服务连接" }}</small>
              </div>
              <button type="button" @click="goToDraftStep(1)">编辑</button>
            </section>
            <section>
              <i class="fa-solid fa-diagram-project" />
              <div>
                <span>契约</span><strong>{{ requestContractCount }} 入参 · {{ responseContractCount }} 出参</strong
                ><small>{{ draftTool.errorMappings.length }} 条错误映射</small>
              </div>
              <button type="button" @click="goToDraftStep(2)">编辑</button>
            </section>
            <section>
              <i class="fa-solid fa-gauge-high" />
              <div>
                <span>运行策略</span
                ><strong>{{ draftTool.timeoutSeconds }}s 超时 · {{ draftTool.retryCount }} 次重试</strong
                ><small>{{ backoffPolicyMeta(draftTool.backoffPolicy).label }} · {{ draftTool.rateLimitPolicy }}</small>
              </div>
              <button type="button" @click="runtimeAdvancedOpen = !runtimeAdvancedOpen">
                {{ runtimeAdvancedOpen ? "收起" : "配置" }}
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
                ><i class="fa-solid fa-sliders" /><strong>高级运行策略</strong
                ><small>默认值适合大多数 HTTP 工具</small></span
              >
              <i :class="runtimeAdvancedOpen ? 'fa-solid fa-chevron-up' : 'fa-solid fa-chevron-down'" />
            </button>
            <div v-if="runtimeAdvancedOpen" class="tool-runtime-policy-inline">
              <div class="form-two">
                <label class="drawer-field"
                  ><span>超时时间（秒）</span><input v-model.number="draftTool.timeoutSeconds" type="number" min="1"
                /></label>
                <label class="drawer-field"
                  ><span>重试次数</span><input v-model.number="draftTool.retryCount" type="number" min="0"
                /></label>
              </div>
              <div class="form-two">
                <label class="drawer-field"
                  ><span>退避策略</span
                  ><AppSelect
                    v-model="draftTool.backoffPolicy"
                    :options="backoffPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
                /></label>
                <label class="drawer-field"
                  ><span>限流策略</span
                  ><AppSelect
                    v-model="draftTool.rateLimitPolicy"
                    :options="rateLimitPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
                /></label>
              </div>
              <label class="drawer-field"><span>幂等策略</span><input v-model="draftTool.idempotencyPolicy" /></label>
            </div>
          </section>

          <div class="tool-draft-save-note">
            <i class="fa-solid fa-circle-info" />
            <div>
              <strong>保存后状态：草稿</strong
              ><span>草稿不会立即开放给 Agent。保存后可在详情页执行测试，通过后再发布。</span>
            </div>
          </div>
        </template>
      </div>

      <p v-if="draftError" class="form-error tool-hybrid-form-error" role="alert">{{ draftError }}</p>

      <div class="tool-hybrid-footer">
        <div class="tool-hybrid-completion">
          <span
            >完成度 <b>{{ draftCompletionPercent }}%</b></span
          ><i><b :style="{ width: `${draftCompletionPercent}%` }" /></i>
        </div>
        <div class="tool-hybrid-stat">
          基础必填 <b>{{ completedBaseRequiredCount }}/5</b>
        </div>
        <div v-if="draftSuggestionCount" class="tool-hybrid-stat warning"><i />建议检查 {{ draftSuggestionCount }}</div>
        <div v-else class="tool-hybrid-stat"><i class="fa-solid fa-circle-check" />契约已配置</div>
        <span class="tool-editor-action-spacer" />
        <button class="ghost" type="button" @click="closeToolEditor">取消</button>
        <button type="button" :disabled="saveState === 'saving'" @click="persistDraftTool(false)">保存草稿</button>
        <button type="button" :disabled="draftStep === 1" @click="goPreviousStep">上一步</button>
        <button
          v-if="draftStep < toolEditorSteps.length"
          class="primary"
          type="button"
          :disabled="!draftStepCanProceed()"
          @click="goNextStep"
        >
          下一步
        </button>
        <button
          v-else
          class="primary"
          type="button"
          :disabled="!isDraftStepComplete(1) || !isDraftStepComplete(2) || saveState === 'saving'"
          @click="saveDraftTool"
        >
          完成
        </button>
      </div>
    </section>
  </div>
</template>
