<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 12)
/** Tool detail panel (ZKL-64 item 12). */
import ToolSchemaTreeView from "./ToolSchemaTreeView.vue";
import { useToolsPageContext } from "../composables/useToolsPageContext";
import { buildToolPublishChecklist } from "../utils/tool-governance";

/* eslint-disable @typescript-eslint/no-unused-vars -- inject surface for template */
const scp = useToolsPageContext();
const {
  toolsStore,
  providersStore,
  router,
  detailTab,
  toolDetailVisible,
  toolDetailModalRef,
  detailTabs,
  detailTool,
  detailConnection,
  detailRequestContract,
  detailResponseNodes,
  methodOf,
  pathOf,
  methodClass,
  statusClass,
  toolStatusLabel,
  lifecycleStatus,
  testStatus,
  runStatus,
  governanceToneClass,
  toolVersionLabel,
  toolLastTestSummary,
  toolLastTestDetail,
  toolPublishReadinessLabel,
  toolPublishButtonLabel,
  toolAvailabilityButtonLabel,
  agentImpactLabel,
  workspaceDisplayLabel,
  providerForTool,
  serviceConnectionStatusLabel,
  connectionDomainLabel,
  connectionBasePathLabel,
  authModeLabel,
  environmentLabel,
  timeoutLabel,
  retryLabel,
  backoffPolicyMeta,
  rateLimitPolicyMeta,
  selectDetailTab,
  handleDetailTabKeydown,
  closeToolDetail,
  openToolTestDialog,
  canPublishTool,
  publishTool,
  toggleToolAvailability,
  normalizeSchemaNode,
} = scp;
void ToolSchemaTreeView;
void buildToolPublishChecklist;
/* eslint-enable @typescript-eslint/no-unused-vars */
</script>

<template>
  <div v-if="toolDetailVisible" class="modal-backdrop" @click.self="closeToolDetail">
    <section
      v-if="detailTool"
      ref="toolDetailModalRef"
      class="modal-card tool-detail-modal-card"
      role="dialog"
      aria-modal="true"
      aria-label="工具详情"
    >
      <div class="modal-card-head">
        <div>
          <span>Tool Runtime</span>
          <h3>工具详情</h3>
        </div>
        <button
          class="icon-action-button"
          type="button"
          aria-label="关闭工具详情"
          data-modal-initial-focus
          @click="closeToolDetail"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </div>
      <div class="tool-detail-modal-body">
        <div class="tool-detail-hero">
          <span class="method" :class="methodClass(detailTool)">{{ methodOf(detailTool) }}</span>
          <div>
            <strong>{{ detailTool.name }}</strong>
            <small class="mono">{{ pathOf(detailTool) }}</small>
          </div>
          <div class="tool-detail-status-stack">
            <span
              class="tool-status-pill"
              :class="[statusClass(detailTool.status), governanceToneClass(lifecycleStatus(detailTool).tone)]"
              ><i />{{ lifecycleStatus(detailTool).label }}</span
            >
            <span class="tool-status-pill" :class="governanceToneClass(testStatus(detailTool).tone)"
              ><i />{{ testStatus(detailTool).label }}</span
            >
            <span class="tool-status-pill" :class="governanceToneClass(runStatus(detailTool).tone)"
              ><i />{{ runStatus(detailTool).label }}</span
            >
          </div>
        </div>
        <div class="tool-detail-governance-strip">
          <span><b>版本</b>{{ toolVersionLabel(detailTool) }}</span>
          <span><b>最近测试</b>{{ toolLastTestSummary(detailTool) }}</span>
          <span><b>Capability Binding</b>{{ agentImpactLabel(detailTool) }}</span>
          <span><b>影响面</b>由独立 Capability Binding 管理</span>
        </div>
        <p class="form-helper">{{ detailTool.description }}</p>

        <div class="tool-detail-tabs" role="tablist" aria-label="工具详情分区">
          <button
            v-for="tab in detailTabs"
            :id="`tool-detail-tab-${tab.id}`"
            :key="tab.id"
            :class="{ active: detailTab === tab.id }"
            type="button"
            role="tab"
            :aria-selected="detailTab === tab.id"
            :aria-controls="`tool-detail-panel-${tab.id}`"
            :tabindex="detailTab === tab.id ? 0 : -1"
            @click="selectDetailTab(tab.id)"
            @keydown="handleDetailTabKeydown($event, tab.id)"
          >
            <i :class="tab.icon" />
            <span>{{ tab.label }}</span>
          </button>
        </div>

        <div
          id="tool-detail-panel-base"
          class="tool-detail-panel"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-base"
          v-show="detailTab === 'base'"
          :hidden="detailTab !== 'base'"
        >
          <div class="tool-config-grid">
            <div class="config-summary-item">
              <i class="fa-solid fa-user-gear" /><span>最近维护</span
              ><strong>{{ detailTool.updatedBy || detailTool.createdBy || "-" }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-code-branch" /><span>版本</span><strong>{{ toolVersionLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" /><span>业务空间</span
              ><strong>{{ workspaceDisplayLabel(detailTool.workspaceId) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-cubes" /><span>来源 Provider</span
              ><strong>{{ providerForTool(detailTool)?.name || detailTool.providerId }}</strong>
            </div>
          </div>
        </div>
        <div
          id="tool-detail-panel-connection"
          class="tool-detail-panel"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-connection"
          v-show="detailTab === 'connection'"
          :hidden="detailTab !== 'connection'"
        >
          <div class="tool-config-grid">
            <div class="config-summary-item">
              <i class="fa-solid fa-server" /><span>服务连接</span
              ><strong>{{ detailConnection?.name || "服务连接未找到" }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-circle-check" /><span>连接状态</span
              ><strong>{{ serviceConnectionStatusLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-globe" /><span>服务域名</span><strong>{{ connectionDomainLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-route" /><span>Base Path</span><strong>{{ connectionBasePathLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-key" /><span>认证方式</span><strong>{{ authModeLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" /><span>环境</span
              ><strong>{{ environmentLabel(detailConnection?.environment || "") }}</strong>
            </div>
            <button
              class="ghost-button tool-detail-maintenance-action"
              type="button"
              @click="router.push('/connections')"
            >
              <i class="fa-solid fa-screwdriver-wrench" />
              <span>去服务连接维护</span>
            </button>
          </div>
        </div>
        <div
          id="tool-detail-panel-request"
          class="tool-detail-panel tool-contract-section-stack"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-request"
          v-show="detailTab === 'request'"
          :hidden="detailTab !== 'request'"
        >
          <ToolSchemaTreeView
            :nodes="
              detailRequestContract.transportParams.map((param) =>
                normalizeSchemaNode({
                  id: `detail-request-${param.location}-${param.name}`,
                  location: param.location,
                  name: param.name,
                  type: (param.type as ToolSchemaNodeType) || 'string',
                  required: param.required,
                  description: param.description,
                }),
              )
            "
            title="请求参数"
            empty-text="暂无请求参数"
          />
          <ToolSchemaTreeView
            :nodes="detailRequestContract.bodyNodes"
            title="请求体 Body"
            empty-text="暂无请求体结构"
          />
        </div>
        <div
          id="tool-detail-panel-response"
          class="tool-detail-panel"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-response"
          v-show="detailTab === 'response'"
          :hidden="detailTab !== 'response'"
        >
          <ToolSchemaTreeView :nodes="detailResponseNodes" title="响应结果" empty-text="暂无响应结构" />
        </div>
        <div
          id="tool-detail-panel-runtime"
          class="tool-detail-panel"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-runtime"
          v-show="detailTab === 'runtime'"
          :hidden="detailTab !== 'runtime'"
        >
          <div class="tool-config-grid">
            <div class="config-summary-item">
              <i class="fa-solid fa-clock" /><span>超时时间</span><strong>{{ timeoutLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-rotate" /><span>重试次数</span><strong>{{ retryLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-arrow-trend-up" /><span>退避策略</span
              ><strong>{{ backoffPolicyMeta(detailTool.runtimePolicy.backoffPolicy).label }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-gauge-high" /><span>限流策略</span
              ><strong>{{ rateLimitPolicyMeta(detailTool.runtimePolicy.rateLimitPolicy).label }}</strong>
            </div>
          </div>
        </div>
        <div
          id="tool-detail-panel-test"
          class="tool-detail-panel"
          role="tabpanel"
          aria-labelledby="tool-detail-tab-test"
          v-show="detailTab === 'test'"
          :hidden="detailTab !== 'test'"
        >
          <div class="tool-test-card">
            <div class="tool-test-request">
              <strong>测试方式</strong><span>打开测试弹窗，填写默认入参并执行真实调用。</span>
            </div>
            <div class="tool-test-result">
              <strong>当前状态</strong><span>{{ toolStatusLabel(detailTool.status) }}</span>
            </div>
            <div class="tool-test-result">
              <strong>最近结果</strong><span>{{ toolLastTestSummary(detailTool) }}</span>
            </div>
            <div class="tool-test-result">
              <strong>测试详情</strong><span>{{ toolLastTestDetail(detailTool) }}</span>
            </div>
            <div id="tool-publish-readiness" class="tool-test-result">
              <strong>发布条件</strong><span>{{ toolPublishReadinessLabel(detailTool) }}</span>
            </div>
            <div class="tool-publish-checklist compact">
              <div
                v-for="item in buildToolPublishChecklist(detailTool, detailConnection, { agentImpactConfirmed: false })"
                :key="item.id"
                class="tool-publish-check-item"
                :class="{
                  passed: item.passed,
                  warning: !item.passed && item.severity === 'warning',
                  error: !item.passed && item.severity === 'error',
                }"
              >
                <i
                  :class="
                    item.passed
                      ? 'fa-solid fa-check'
                      : item.severity === 'warning'
                        ? 'fa-solid fa-triangle-exclamation'
                        : 'fa-solid fa-xmark'
                  "
                />
                <span>{{ item.label }}</span>
              </div>
            </div>
            <div class="tool-test-action-group">
              <button class="primary-button" type="button" @click="openToolTestDialog(detailTool)">执行测试</button>
              <button
                class="ghost-button"
                type="button"
                :disabled="!canPublishTool(detailTool)"
                :aria-describedby="'tool-publish-readiness'"
                @click="publishTool(detailTool)"
              >
                {{ toolPublishButtonLabel(detailTool) }}
              </button>
              <button class="ghost-button" type="button" @click="toggleToolAvailability(detailTool)">
                {{ toolAvailabilityButtonLabel(detailTool) }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
