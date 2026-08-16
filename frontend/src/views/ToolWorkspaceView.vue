<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, provide, ref, watch } from "vue";
import { onBeforeRouteLeave, useRoute } from "vue-router";
import { useI18n } from "vue-i18n";

import AppSelect from "../components/AppSelect.vue";
import ToolContractCanvas from "../components/ToolContractCanvas.vue";
import ToolTestDialog from "../components/ToolTestDialog.vue";
import { useToolsPage } from "../composables/useToolsPage";
import "./tools-page.css";
import "./tool-workspace-page.css";

const { t } = useI18n();
const route = useRoute();
const page = useToolsPage({ surface: "workspace" });
provide("toolsPage", page);

const {
  router,
  toolsStore,
  detailTool,
  detailConnection,
  draftTool,
  draftConnection,
  draftError,
  saveState,
  workspaceLoadError,
  workspaceLoading,
  isEditorSurface,
  hasUnsavedToolChanges,
  methodOf,
  pathOf,
  methodClass,
  statusClass,
  lifecycleStatus,
  runStatus,
  governanceToneClass,
  toolVersionLabel,
  toolLastTestSummary,
  toolPublishReadinessLabel,
  toolPublishButtonLabel,
  toolAvailabilityButtonLabel,
  toolMaintainerLabel,
  serviceConnectionStatusLabel,
  connectionEndpointLabel,
  environmentLabel,
  timeoutLabel,
  retryLabel,
  backoffPolicyMeta,
  rateLimitPolicyMeta,
  workspaceOptions,
  serviceConnectionOptions,
  methodOptions,
  contentTypeOptions,
  backoffPolicyOptions,
  rateLimitPolicyOptions,
  runtimeAdvancedOpen,
  testDialogVisible,
  testDialogTool,
  actionNote,
  actionNoteTone,
  canPublishTool,
  openToolTestDialog,
  publishTool,
  toggleToolAvailability,
  persistDraftTool,
  closeToolEditor,
  leaveToolWorkspace,
  endpointPreviewLabel,
  setActionFeedback,
} = page;

const mode = computed(() => {
  if (route.name === "tool-new") return "create" as const;
  if (route.name === "tool-edit") return "edit" as const;
  return "detail" as const;
});

const title = computed(() => {
  if (mode.value === "create") return t("tools.registerToolTitle");
  if (mode.value === "edit") return draftTool.value.name || t("tools.editToolTitle");
  return detailTool.value?.name || t("tools.detailTitle");
});

const showPublishAction = computed(() => Boolean(detailTool.value && canPublishTool(detailTool.value)));

const testSidebarSummary = computed(() => {
  const tool = detailTool.value;
  if (!tool) return "";
  if (tool.lastTestResult) return toolLastTestSummary(tool);
  if (tool.status === "Published") return t("tools.noConsoleTestPublished");
  return toolLastTestSummary(tool);
});

const hasDetailContract = computed(() => {
  const tool = detailTool.value;
  if (!tool) return false;
  return Boolean(tool.requestParams?.length || tool.responseFields?.length || tool.errorMappings?.length);
});

const detailLede = computed(() => {
  const tool = detailTool.value;
  if (!tool?.description) return "";
  if (tool.description.trim() === tool.name.trim()) return "";
  return tool.description.trim();
});

const ledeExpanded = ref(false);
const ledeEl = ref<HTMLParagraphElement | null>(null);
const ledeOverflows = ref(false);

async function measureLedeOverflow() {
  await nextTick();
  const el = ledeEl.value;
  if (!el) {
    ledeOverflows.value = false;
    return;
  }
  const alreadyClamped = el.classList.contains("clamped");
  if (!alreadyClamped) el.classList.add("clamped");
  ledeOverflows.value = el.scrollHeight > el.clientHeight + 2;
  if (!alreadyClamped) el.classList.remove("clamped");
}

watch(
  () => detailTool.value?.id,
  () => {
    ledeExpanded.value = false;
    void measureLedeOverflow();
  },
);

const detailCallUrl = computed(() => {
  if (!detailTool.value) return "";
  return connectionEndpointLabel(detailConnection.value, pathOf(detailTool.value));
});

const retryDisplay = computed(() => {
  const tool = detailTool.value;
  if (!tool) return "";
  return tool.runtimePolicy.retryCount > 0 ? retryLabel(tool) : t("tools.noRetry");
});

async function copyPath() {
  const path = mode.value === "detail" && detailTool.value ? pathOf(detailTool.value) : draftTool.value.path;
  if (!path) return;
  try {
    await navigator.clipboard.writeText(path);
    setActionFeedback(t("tools.pathCopied"));
  } catch {
    setActionFeedback(t("tools.pathCopyFailed"), "error");
  }
}

function goBack() {
  if (isEditorSurface.value) {
    closeToolEditor();
    return;
  }
  void router.push({ name: "tools" });
}

function openEditor() {
  if (!detailTool.value) return;
  void router.push({ name: "tool-edit", params: { toolId: detailTool.value.id } });
}

onBeforeRouteLeave(() => {
  if (isEditorSurface.value && !leaveToolWorkspace()) return false;
  return true;
});

function beforeUnload(event: BeforeUnloadEvent) {
  if (!hasUnsavedToolChanges.value) return;
  event.preventDefault();
  event.returnValue = "";
}

window.addEventListener("beforeunload", beforeUnload);
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));
</script>

<template>
  <div class="tool-workspace-page" v-loading="workspaceLoading || toolsStore.loading">
    <p v-if="workspaceLoadError" class="form-error" role="alert">{{ workspaceLoadError }}</p>
    <p v-if="draftError" class="form-error" role="alert">{{ draftError }}</p>

    <template v-if="mode === 'detail' && detailTool">
      <header class="tool-workspace-identity">
        <button class="tool-workspace-back" type="button" @click="goBack">
          <i class="fa-solid fa-arrow-left" />
          {{ t("tools.backToTools") }}
        </button>
        <div class="tool-workspace-hero">
          <div class="tool-workspace-identity-lead">
            <span class="method" :class="methodClass(detailTool)">{{ methodOf(detailTool) }}</span>
            <div class="tool-workspace-identity-copy">
              <h1>{{ detailTool.name }}</h1>
              <div class="tool-workspace-path">
                <code>{{ pathOf(detailTool) }}</code>
                <button type="button" class="tool-workspace-copy" :aria-label="t('tools.copyPath')" @click="copyPath">
                  <i class="fa-regular fa-copy" />
                </button>
              </div>
            </div>
          </div>
          <div class="tool-workspace-actions">
            <button class="ghost-button" type="button" @click="openToolTestDialog(detailTool)">
              {{ t("tools.runTest") }}
            </button>
            <button v-if="showPublishAction" class="ghost-button" type="button" @click="publishTool(detailTool)">
              {{ toolPublishButtonLabel(detailTool) }}
            </button>
            <button class="ghost-button" type="button" @click="toggleToolAvailability(detailTool)">
              {{ toolAvailabilityButtonLabel(detailTool) }}
            </button>
            <button class="primary-button" type="button" @click="openEditor">{{ t("common.edit") }}</button>
          </div>
        </div>
        <div class="tool-workspace-statusline" :aria-label="t('tools.statusLayersAria')">
          <span
            class="tool-status-pill"
            :class="[statusClass(detailTool.status), governanceToneClass(lifecycleStatus(detailTool).tone)]"
            >{{ lifecycleStatus(detailTool).label }}</span
          >
          <span
            v-if="runStatus(detailTool).tone === 'danger' || runStatus(detailTool).tone === 'warning'"
            class="tool-workspace-status-note"
            :class="governanceToneClass(runStatus(detailTool).tone)"
            >{{ runStatus(detailTool).label }}</span
          >
          <span class="tool-workspace-status-note muted">{{ toolVersionLabel(detailTool) }}</span>
        </div>
        <div v-if="detailLede" class="tool-workspace-lede-wrap">
          <p ref="ledeEl" class="tool-workspace-lede" :class="{ clamped: ledeOverflows && !ledeExpanded }">
            {{ detailLede }}
          </p>
          <button
            v-if="ledeOverflows"
            class="tool-workspace-lede-toggle"
            type="button"
            @click="ledeExpanded = !ledeExpanded"
          >
            {{ ledeExpanded ? t("tools.collapseDescription") : t("tools.expandDescription") }}
          </button>
        </div>
      </header>

      <div class="tool-workspace-body" :class="{ 'is-empty': !hasDetailContract }">
        <main class="tool-workspace-main">
          <ToolContractCanvas
            mode="view"
            embedded
            :request-params="detailTool.requestParams"
            :response-fields="detailTool.responseFields"
            :error-mappings="detailTool.errorMappings"
          >
            <template #empty-action>
              <button class="ghost-button" type="button" @click="openEditor">{{ t("tools.editContract") }}</button>
            </template>
          </ToolContractCanvas>
        </main>
        <aside class="tool-workspace-aside" :aria-label="t('tools.propertiesAria')">
          <section>
            <h2>{{ t("tools.asideConnection") }}</h2>
            <p class="tool-workspace-fact-title" :title="detailConnection?.name || t('tools.connectionNotFound')">
              {{ detailConnection?.name || t("tools.connectionNotFound") }}
            </p>
            <p class="tool-workspace-fact-meta">
              {{ serviceConnectionStatusLabel() }}
              ·
              {{ environmentLabel(detailConnection?.environment || "") }}
            </p>
            <p class="tool-workspace-fact-url mono" :title="detailCallUrl">{{ detailCallUrl }}</p>
            <button class="tool-workspace-text-link" type="button" @click="router.push('/connections')">
              {{ t("tools.maintainConnection") }}
            </button>
          </section>
          <section>
            <h2>{{ t("tools.asideRuntime") }}</h2>
            <dl>
              <div>
                <dt>{{ t("tools.timeout") }}</dt>
                <dd>{{ timeoutLabel(detailTool) }}</dd>
              </div>
              <div>
                <dt>{{ t("tools.retryCount") }}</dt>
                <dd>{{ retryDisplay }}</dd>
              </div>
              <div>
                <dt>{{ t("tools.backoffPolicy") }}</dt>
                <dd>{{ backoffPolicyMeta(detailTool.runtimePolicy.backoffPolicy).label }}</dd>
              </div>
              <div>
                <dt>{{ t("tools.rateLimitPolicy") }}</dt>
                <dd>{{ rateLimitPolicyMeta(detailTool.runtimePolicy.rateLimitPolicy).label }}</dd>
              </div>
            </dl>
          </section>
          <section>
            <h2>{{ t("tools.asidePublish") }}</h2>
            <dl>
              <div>
                <dt>{{ t("tools.lastResult") }}</dt>
                <dd>{{ testSidebarSummary }}</dd>
              </div>
              <div>
                <dt>{{ t("tools.publishCondition") }}</dt>
                <dd>{{ toolPublishReadinessLabel(detailTool) }}</dd>
              </div>
              <div>
                <dt>{{ t("tools.lastMaintained") }}</dt>
                <dd>{{ toolMaintainerLabel(detailTool) }}</dd>
              </div>
            </dl>
          </section>
        </aside>
      </div>
    </template>

    <template v-else-if="isEditorSurface">
      <header class="tool-workspace-identity editor">
        <button class="tool-workspace-back" type="button" @click="goBack">
          <i class="fa-solid fa-arrow-left" />
          {{ t("tools.backToTools") }}
        </button>
        <div class="tool-workspace-hero">
          <div class="tool-workspace-identity-copy">
            <p class="tool-workspace-kicker">
              {{ mode === "create" ? t("tools.registerToolTitle") : t("tools.editToolTitle") }}
            </p>
            <h1>{{ title }}</h1>
          </div>
          <div class="tool-workspace-actions">
            <button class="ghost-button" type="button" @click="closeToolEditor">{{ t("common.cancel") }}</button>
            <button
              class="ghost-button"
              type="button"
              :disabled="saveState === 'saving'"
              @click="persistDraftTool(false)"
            >
              {{ t("tools.saveDraft") }}
            </button>
            <button
              class="primary-button"
              type="button"
              :disabled="saveState === 'saving'"
              @click="persistDraftTool(true)"
            >
              {{ t("tools.finish") }}
            </button>
          </div>
        </div>
      </header>

      <section class="tool-workspace-editor-basics">
        <div class="tool-workspace-form-grid">
          <label class="drawer-field"
            ><span>{{ t("tools.toolName") }} <b>*</b></span
            ><input v-model="draftTool.name" :placeholder="t('tools.namePlaceholder')"
          /></label>
          <label class="drawer-field">
            <span>{{ t("tools.serviceConnection") }} <b>*</b></span>
            <AppSelect
              v-model="draftTool.connectionId"
              :options="serviceConnectionOptions"
              :placeholder="t('tools.selectServiceConnection')"
            />
          </label>
          <label class="drawer-field tool-method-field"
            ><span>Method <b>*</b></span
            ><AppSelect v-model="draftTool.method" :options="methodOptions"
          /></label>
          <label class="drawer-field"
            ><span>Endpoint Path <b>*</b></span
            ><input v-model="draftTool.path" class="mono" placeholder="/api/resource/{id}"
          /></label>
          <label class="drawer-field">
            <span>{{ t("tools.workspace") }} <b>*</b></span>
            <AppSelect
              v-model="draftTool.workspaceId"
              :options="workspaceOptions"
              :placeholder="t('tools.selectWorkspace')"
            />
          </label>
          <label class="drawer-field"
            ><span>Content-Type</span><AppSelect v-model="draftTool.contentType" :options="contentTypeOptions"
          /></label>
        </div>
        <label class="drawer-field tool-workspace-description"
          ><span>{{ t("tools.actionDescription") }}</span
          ><textarea v-model="draftTool.description" rows="2" :placeholder="t('tools.descriptionPlaceholder')" />
        </label>
        <p v-if="mode === 'edit'" class="tool-workspace-draft-note">{{ t("tools.editCreatesDraft") }}</p>
        <div class="tool-endpoint-preview">
          <span class="method" :class="draftTool.method.toLowerCase()">{{ draftTool.method }}</span>
          <strong>{{ endpointPreviewLabel() }}</strong>
          <small>{{ draftConnection?.name || t("tools.noConnectionSelected") }}</small>
        </div>
      </section>

      <ToolContractCanvas
        mode="edit"
        v-model:request-contract="draftTool.requestContract"
        v-model:response-contract="draftTool.responseContract"
        v-model:error-mappings="draftTool.errorMappings"
      />

      <section class="tool-runtime-disclosure" :class="{ open: runtimeAdvancedOpen }">
        <button type="button" :aria-expanded="runtimeAdvancedOpen" @click="runtimeAdvancedOpen = !runtimeAdvancedOpen">
          <span
            ><i class="fa-solid fa-sliders" /><strong>{{ t("tools.advancedRuntime") }}</strong
            ><small>{{ t("tools.advancedRuntimeHelp") }}</small></span
          >
          <i :class="runtimeAdvancedOpen ? 'fa-solid fa-chevron-up' : 'fa-solid fa-chevron-down'" />
        </button>
        <div v-if="runtimeAdvancedOpen" class="tool-runtime-policy-inline">
          <div class="form-two">
            <label class="drawer-field"
              ><span>{{ t("tools.timeoutSeconds") }}</span
              ><input v-model.number="draftTool.timeoutSeconds" type="number" min="1"
            /></label>
            <label class="drawer-field"
              ><span>{{ t("tools.retryCount") }}</span
              ><input v-model.number="draftTool.retryCount" type="number" min="0"
            /></label>
          </div>
          <div class="form-two">
            <label class="drawer-field"
              ><span>{{ t("tools.backoffPolicy") }}</span
              ><AppSelect
                v-model="draftTool.backoffPolicy"
                :options="backoffPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
            /></label>
            <label class="drawer-field"
              ><span>{{ t("tools.rateLimitPolicy") }}</span
              ><AppSelect
                v-model="draftTool.rateLimitPolicy"
                :options="rateLimitPolicyOptions.map((option) => ({ label: option.label, value: option.value }))"
            /></label>
          </div>
        </div>
      </section>
    </template>

    <div v-if="actionNote" class="action-toast" :class="{ error: actionNoteTone === 'error' }">{{ actionNote }}</div>
    <ToolTestDialog v-model="testDialogVisible" :tool="testDialogTool" />
  </div>
</template>
