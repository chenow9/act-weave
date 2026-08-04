<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 12)
/** Tool detail panel (ZKL-64 item 12). */
import { useI18n } from "vue-i18n";
import ToolSchemaTreeView from "./ToolSchemaTreeView.vue";
import { useToolsPageContext } from "../composables/useToolsPageContext";
import { buildToolPublishChecklist } from "../utils/tool-governance";

/* eslint-disable @typescript-eslint/no-unused-vars -- inject surface for template */
const { t } = useI18n();
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
      :aria-label="t('tools.detailAria')"
    >
      <div class="modal-card-head">
        <div>
          <span>Tool Runtime</span>
          <h3>{{ t("tools.detailTitle") }}</h3>
        </div>
        <button
          class="icon-action-button"
          type="button"
          :aria-label="t('tools.closeDetailAria')"
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
          <div class="tool-detail-status-stack" :aria-label="t('tools.statusLayersAria')">
            <span
              class="tool-status-pill"
              data-status-layer="lifecycle"
              :title="lifecycleStatus(detailTool).description"
              :class="[statusClass(detailTool.status), governanceToneClass(lifecycleStatus(detailTool).tone)]"
              ><i />{{ lifecycleStatus(detailTool).label }}</span
            >
            <span
              class="tool-status-pill"
              data-status-layer="test"
              :title="testStatus(detailTool).description"
              :class="governanceToneClass(testStatus(detailTool).tone)"
              ><i />{{ testStatus(detailTool).label }}</span
            >
            <span
              class="tool-status-pill"
              data-status-layer="run"
              :title="runStatus(detailTool).description"
              :class="governanceToneClass(runStatus(detailTool).tone)"
              ><i />{{ runStatus(detailTool).label }}</span
            >
          </div>
        </div>
        <div class="tool-detail-governance-strip">
          <span><b>{{ t("tools.version") }}</b>{{ toolVersionLabel(detailTool) }}</span>
          <span><b>{{ t("tools.lastTest") }}</b>{{ toolLastTestSummary(detailTool) }}</span>
          <span><b>Capability Binding</b>{{ agentImpactLabel(detailTool) }}</span>
          <span><b>{{ t("tools.impactSurface") }}</b>{{ t("tools.impactByBinding") }}</span>
        </div>
        <p class="form-helper">{{ detailTool.description }}</p>

        <div class="tool-detail-tabs" role="tablist" :aria-label="t('tools.detailTabsAria')">
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
              <i class="fa-solid fa-user-gear" /><span>{{ t("tools.lastMaintained") }}</span
              ><strong>{{ detailTool.updatedBy || detailTool.createdBy || "-" }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-code-branch" /><span>{{ t("tools.version") }}</span
              ><strong>{{ toolVersionLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" /><span>{{ t("tools.workspace") }}</span
              ><strong>{{ workspaceDisplayLabel(detailTool.workspaceId) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-cubes" /><span>{{ t("tools.sourceProvider") }}</span
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
              <i class="fa-solid fa-server" /><span>{{ t("tools.serviceConnection") }}</span
              ><strong>{{ detailConnection?.name || t("tools.connectionNotFound") }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-circle-check" /><span>{{ t("tools.connectionStatus") }}</span
              ><strong>{{ serviceConnectionStatusLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-globe" /><span>{{ t("tools.serviceDomain") }}</span
              ><strong>{{ connectionDomainLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-route" /><span>Base Path</span><strong>{{ connectionBasePathLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-key" /><span>{{ t("tools.authMode") }}</span
              ><strong>{{ authModeLabel() }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" /><span>{{ t("tools.environment") }}</span
              ><strong>{{ environmentLabel(detailConnection?.environment || "") }}</strong>
            </div>
            <button
              class="ghost-button tool-detail-maintenance-action"
              type="button"
              @click="router.push('/connections')"
            >
              <i class="fa-solid fa-screwdriver-wrench" />
              <span>{{ t("tools.goMaintainConnection") }}</span>
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
            :title="t('tools.requestParamsTitle')"
            :empty-text="t('tools.noRequestParams')"
          />
          <ToolSchemaTreeView
            :nodes="detailRequestContract.bodyNodes"
            :title="t('tools.requestBodyStructure')"
            :empty-text="t('tools.noRequestBody')"
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
          <ToolSchemaTreeView
            :nodes="detailResponseNodes"
            :title="t('tools.responseResult')"
            :empty-text="t('tools.noResponseStructure')"
          />
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
              <i class="fa-solid fa-clock" /><span>{{ t("tools.timeout") }}</span
              ><strong>{{ timeoutLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-rotate" /><span>{{ t("tools.retryCount") }}</span
              ><strong>{{ retryLabel(detailTool) }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-arrow-trend-up" /><span>{{ t("tools.backoffPolicy") }}</span
              ><strong>{{ backoffPolicyMeta(detailTool.runtimePolicy.backoffPolicy).label }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-gauge-high" /><span>{{ t("tools.rateLimitPolicy") }}</span
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
              <strong>{{ t("tools.testMethod") }}</strong><span>{{ t("tools.testMethodHelp") }}</span>
            </div>
            <div class="tool-test-result">
              <strong>{{ t("tools.currentStatus") }}</strong><span>{{ toolStatusLabel(detailTool.status) }}</span>
            </div>
            <div class="tool-test-result">
              <strong>{{ t("tools.lastResult") }}</strong><span>{{ toolLastTestSummary(detailTool) }}</span>
            </div>
            <div class="tool-test-result">
              <strong>{{ t("tools.testDetail") }}</strong><span>{{ toolLastTestDetail(detailTool) }}</span>
            </div>
            <div id="tool-publish-readiness" class="tool-test-result">
              <strong>{{ t("tools.publishCondition") }}</strong
              ><span>{{ toolPublishReadinessLabel(detailTool) }}</span>
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
              <button class="primary-button" type="button" @click="openToolTestDialog(detailTool)">
                {{ t("tools.runTest") }}
              </button>
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
