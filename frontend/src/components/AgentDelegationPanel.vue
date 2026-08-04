<script setup lang="ts">
/**
 * Manage internal Agent→Agent bindings and A2A remotes for one agent.
 * Styles reuse existing agent-studio section patterns.
 */
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { AgentA2AExposure, AgentA2ARemoteBinding, AgentDelegationBinding } from "../types/domain";
import {
  createA2AExposure,
  createA2ARemote,
  createDelegationBinding,
  disableA2AExposure,
  disableA2ARemote,
  disableDelegationBinding,
  getA2ACapabilities,
  listA2AExposures,
  listA2ARemotes,
  listDelegationBindings,
  previewA2AAgentCard,
  updateA2AExposure,
  updateA2ARemote,
  updateDelegationBinding,
  type A2ACapabilities,
} from "../services/agentDelegation";
import AppSelect from "./AppSelect.vue";
import type { AppSelectOption } from "./AppSelect.vue";

const props = defineProps<{
  workspaceId: string;
  agentId: string;
  /** Other agents in workspace for target select. */
  agentOptions: { id: string; name: string }[];
}>();

const { t } = useI18n();

const bindings = ref<AgentDelegationBinding[]>([]);
const remotes = ref<AgentA2ARemoteBinding[]>([]);
const exposures = ref<AgentA2AExposure[]>([]);
const loading = ref(false);
const error = ref("");
const cardPreview = ref("");
const capabilities = ref<A2ACapabilities>({
  allowAuthNone: false,
  authModes: ["AGENT_ACCESS"],
  softDisable: true,
});
const form = ref({
  targetAgentId: "",
  callableName: "",
  description: "",
  mode: "INLINE" as "INLINE" | "TASK",
});
const remoteForm = ref({
  callableName: "",
  description: "",
  endpointUrl: "",
  agentCardUrl: "",
  allowedHosts: "",
  authSecretRef: "",
  timeoutMs: 60000,
});
const exposureForm = ref({
  publicName: "",
  publicDescription: "",
  authMode: "AGENT_ACCESS" as "AGENT_ACCESS" | "NONE",
});

/** Target agents for create / edit pickers (exclude self). */
const targetAgentSelectOptions = computed<AppSelectOption[]>(() =>
  props.agentOptions.filter((a) => a.id !== props.agentId).map((a) => ({ label: a.name, value: a.id })),
);

/** User-facing labels; API still uses INLINE / TASK / AGENT_ACCESS / NONE. */
const modeSelectOptions = computed<AppSelectOption[]>(() => [
  { label: t("agents.adpModeInline"), value: "INLINE" },
  { label: t("agents.adpModeTask"), value: "TASK" },
]);

const authModeSelectOptions = computed<AppSelectOption[]>(() =>
  capabilities.value.authModes.map((mode) => ({
    label: authModeLabel(mode),
    value: mode,
  })),
);

function asMode(value: string | number | boolean): "INLINE" | "TASK" {
  return value === "TASK" ? "TASK" : "INLINE";
}

function asAuthMode(value: string | number | boolean): "AGENT_ACCESS" | "NONE" {
  return value === "NONE" ? "NONE" : "AGENT_ACCESS";
}

function modeLabel(mode?: string) {
  return mode === "TASK" ? t("agents.adpModeTaskShort") : t("agents.adpModeInlineShort");
}

function authModeLabel(mode?: string) {
  if (mode === "NONE") return t("agents.adpAuthNone");
  return t("agents.adpAuthToken");
}

function authModeShort(mode?: string) {
  return mode === "NONE" ? t("agents.adpAuthNoneShort") : t("agents.adpAuthTokenShort");
}

/** Collapsible blocks — all closed by default so the studio stays compact. */
type AdpBlockKey = "internal" | "inbound" | "outbound";
const openBlocks = ref<Record<AdpBlockKey, boolean>>({
  internal: false,
  inbound: false,
  outbound: false,
});

function toggleBlock(key: AdpBlockKey) {
  openBlocks.value[key] = !openBlocks.value[key];
}

function isBlockOpen(key: AdpBlockKey) {
  return openBlocks.value[key];
}

async function reload() {
  if (!props.workspaceId || !props.agentId) return;
  loading.value = true;
  error.value = "";
  try {
    capabilities.value = await getA2ACapabilities(props.workspaceId);
    // If server disallows NONE and form currently has NONE, fall back to AGENT_ACCESS.
    if (!capabilities.value.allowAuthNone && exposureForm.value.authMode === "NONE") {
      exposureForm.value.authMode = "AGENT_ACCESS";
    }
    bindings.value = await listDelegationBindings(props.workspaceId, props.agentId);
    remotes.value = await listA2ARemotes(props.workspaceId, props.agentId);
    const all = await listA2AExposures(props.workspaceId);
    exposures.value = all.filter((e) => e.agentId === props.agentId);
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpLoadFailed");
  } finally {
    loading.value = false;
  }
}

async function onCreate() {
  error.value = "";
  if (!form.value.targetAgentId || !form.value.callableName.trim()) {
    error.value = t("agents.adpNeedTargetAndName");
    return;
  }
  if (!/^[a-z][a-z0-9_]*$/.test(form.value.callableName.trim())) {
    error.value = t("agents.adpCallableNameInvalid");
    return;
  }
  try {
    await createDelegationBinding(props.workspaceId, props.agentId, {
      targetAgentId: form.value.targetAgentId,
      callableName: form.value.callableName.trim(),
      description: form.value.description,
      mode: form.value.mode,
      contextPolicy: "TASK_ONLY",
      enabled: true,
    });
    form.value = { targetAgentId: "", callableName: "", description: "", mode: "INLINE" };
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpCreateBindingFailed");
  }
}

async function onDisable(b: AgentDelegationBinding) {
  error.value = "";
  try {
    await disableDelegationBinding(props.workspaceId, b.id, b.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpDisableFailed");
  }
}

async function onEnableBinding(b: AgentDelegationBinding) {
  error.value = "";
  try {
    await updateDelegationBinding(props.workspaceId, b.id, {
      expectedVersion: b.version,
      enabled: true,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpEnableFailed");
  }
}

async function onSaveBinding(b: AgentDelegationBinding) {
  error.value = "";
  try {
    await updateDelegationBinding(props.workspaceId, b.id, {
      expectedVersion: b.version,
      targetAgentId: b.targetAgentId,
      callableName: b.callableName,
      description: b.description,
      mode: b.mode,
      contextPolicy: b.contextPolicy,
      enabled: b.enabled,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpSaveBindingFailed");
  }
}

async function onEnableExposure(exp: AgentA2AExposure) {
  error.value = "";
  try {
    await updateA2AExposure(props.workspaceId, exp.id, {
      expectedVersion: exp.version,
      enabled: true,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpEnableExposureFailed");
  }
}

async function onSaveExposure(exp: AgentA2AExposure) {
  error.value = "";
  try {
    await updateA2AExposure(props.workspaceId, exp.id, {
      expectedVersion: exp.version,
      publicName: exp.publicName,
      publicDescription: exp.publicDescription,
      authMode: exp.authMode,
      enabled: exp.enabled,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpSaveExposureFailed");
  }
}

async function onEnableRemote(r: AgentA2ARemoteBinding) {
  error.value = "";
  try {
    await updateA2ARemote(props.workspaceId, r.id, {
      expectedVersion: r.version,
      enabled: true,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpEnableRemoteFailed");
  }
}

async function onSaveRemote(r: AgentA2ARemoteBinding) {
  error.value = "";
  try {
    await updateA2ARemote(props.workspaceId, r.id, {
      expectedVersion: r.version,
      callableName: r.callableName,
      description: r.description,
      endpointUrl: r.endpointUrl,
      agentCardUrl: r.agentCardUrl || undefined,
      allowedHosts: r.allowedHosts,
      authSecretRef: r.authSecretRef || undefined,
      timeoutMs: r.timeoutMs,
      enabled: r.enabled,
    });
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpSaveRemoteFailed");
  }
}

async function onCreateExposure() {
  error.value = "";
  cardPreview.value = "";
  if (!exposureForm.value.publicName.trim()) {
    error.value = t("agents.adpNeedPublicName");
    return;
  }
  try {
    await createA2AExposure(props.workspaceId, {
      agentId: props.agentId,
      publicName: exposureForm.value.publicName.trim(),
      publicDescription: exposureForm.value.publicDescription,
      authMode: exposureForm.value.authMode,
      enabled: true,
    });
    exposureForm.value = { publicName: "", publicDescription: "", authMode: "AGENT_ACCESS" };
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpCreateExposureFailed");
  }
}

async function onDisableExposure(exp: AgentA2AExposure) {
  error.value = "";
  try {
    await disableA2AExposure(props.workspaceId, exp.id, exp.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpDisableExposureFailed");
  }
}

async function onPreviewCard(exp: AgentA2AExposure) {
  error.value = "";
  try {
    const card = await previewA2AAgentCard(props.workspaceId, exp.id);
    cardPreview.value = JSON.stringify(card, null, 2);
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpPreviewCardFailed");
  }
}

async function onDisableRemote(r: AgentA2ARemoteBinding) {
  error.value = "";
  try {
    await disableA2ARemote(props.workspaceId, r.id, r.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpDisableRemoteFailed");
  }
}

async function onCreateRemote() {
  error.value = "";
  const hosts = remoteForm.value.allowedHosts
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  if (!remoteForm.value.callableName || !remoteForm.value.endpointUrl || hosts.length === 0) {
    error.value = t("agents.adpRemoteFieldsRequired");
    return;
  }
  try {
    await createA2ARemote(props.workspaceId, props.agentId, {
      callableName: remoteForm.value.callableName.trim(),
      description: remoteForm.value.description,
      endpointUrl: remoteForm.value.endpointUrl.trim(),
      agentCardUrl: remoteForm.value.agentCardUrl.trim() || undefined,
      allowedHosts: hosts,
      authSecretRef: remoteForm.value.authSecretRef.trim() || undefined,
      timeoutMs: remoteForm.value.timeoutMs,
      enabled: true,
    });
    remoteForm.value = {
      callableName: "",
      description: "",
      endpointUrl: "",
      agentCardUrl: "",
      allowedHosts: "",
      authSecretRef: "",
      timeoutMs: 60000,
    };
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : t("agents.adpCreateRemoteFailed");
  }
}

function agentName(id: string) {
  return props.agentOptions.find((a) => a.id === id)?.name || id.slice(0, 8);
}

watch(
  () => [props.workspaceId, props.agentId],
  () => {
    void reload();
  },
);

onMounted(() => {
  void reload();
});
</script>

<template>
  <section class="agent-studio-section agent-delegation-panel">
    <header>
      <span><i class="fa-solid fa-sitemap" aria-hidden="true" /> {{ t("agents.collabExternal") }}</span>
    </header>

    <div class="adp-body">
      <p class="adp-intro">
        {{ t("agents.adpIntro") }}
      </p>

      <p v-if="error" class="agent-studio-inline-warning" role="alert">
        <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
        <span>{{ error }}</span>
      </p>

      <div v-if="loading" class="adp-loading" role="status">
        <i class="fa-solid fa-spinner fa-spin" aria-hidden="true" />
        <span>{{ t("agents.adpLoading") }}</span>
      </div>

      <!-- Internal delegation -->
      <section class="adp-block" :class="{ open: isBlockOpen('internal') }" aria-labelledby="adp-internal-title">
        <button
          type="button"
          class="adp-block-header"
          :aria-expanded="isBlockOpen('internal')"
          aria-controls="adp-internal-body"
          @click="toggleBlock('internal')"
        >
          <div class="adp-block-title">
            <span class="adp-block-icon" aria-hidden="true">
              <i class="fa-solid fa-link" />
            </span>
            <div>
              <strong id="adp-internal-title">{{ t("agents.adpInternalTitle") }}</strong>
              <small>{{ t("agents.adpInternalSubtitle") }}</small>
            </div>
          </div>
          <div class="adp-block-meta">
            <span class="adp-count" :data-empty="bindings.length === 0">{{ bindings.length }}</span>
            <i
              class="fa-solid fa-chevron-down adp-chevron"
              :class="{ open: isBlockOpen('internal') }"
              aria-hidden="true"
            />
          </div>
        </button>

        <div v-show="isBlockOpen('internal')" id="adp-internal-body" class="adp-block-body">
          <p class="adp-hint">
            {{ t("agents.adpInternalHint") }}
          </p>

          <ul v-if="bindings.length" class="adp-list">
            <li v-for="b in bindings" :key="b.id" class="adp-card" :class="{ disabled: !b.enabled }">
              <div class="adp-card-top">
                <div class="adp-card-identity">
                  <code class="adp-callable">{{ b.callableName }}</code>
                  <i class="fa-solid fa-arrow-right adp-arrow" aria-hidden="true" />
                  <span class="adp-target">{{ agentName(b.targetAgentId) }}</span>
                  <span class="adp-pill" :title="b.mode">{{ modeLabel(b.mode) }}</span>
                  <span class="adp-pill muted" :title="t('agents.adpTaskContextOnlyTitle')">{{
                    t("agents.adpTaskContextOnly")
                  }}</span>
                  <span class="adp-pill" :class="b.enabled ? 'on' : 'off'">
                    {{ b.enabled ? t("agents.adpEnabled") : t("agents.adpDisabled") }}
                  </span>
                </div>
                <div class="adp-card-actions">
                  <button type="button" class="ghost-button small" :disabled="!b.enabled" @click="onDisable(b)">
                    {{ t("agents.adpDisable") }}
                  </button>
                  <button type="button" class="ghost-button small" :disabled="b.enabled" @click="onEnableBinding(b)">
                    {{ t("agents.adpReenable") }}
                  </button>
                  <button type="button" class="ghost-button small" data-testid="save-binding" @click="onSaveBinding(b)">
                    <i class="fa-solid fa-check" aria-hidden="true" />
                    {{ t("common.save") }}
                  </button>
                </div>
              </div>
              <div class="adp-card-fields">
                <label class="modal-field">
                  <span>{{ t("agents.adpCallableName") }}</span>
                  <input v-model="b.callableName" data-testid="edit-binding-callable" />
                  <small class="adp-help">{{ t("agents.adpCallableNameHelp") }}</small>
                </label>
                <label class="modal-field" data-testid="edit-binding-target">
                  <span>{{ t("agents.adpHelpAgent") }}</span>
                  <AppSelect
                    class="adp-select"
                    :model-value="b.targetAgentId"
                    :options="targetAgentSelectOptions"
                    :placeholder="t('agents.adpSelectAgent')"
                    :aria-label="t('agents.adpHelpAgent')"
                    @update:model-value="b.targetAgentId = String($event)"
                  />
                </label>
                <label class="modal-field" data-testid="edit-binding-mode">
                  <span>{{ t("agents.adpExecMode") }}</span>
                  <AppSelect
                    class="adp-select"
                    :model-value="b.mode"
                    :options="modeSelectOptions"
                    :placeholder="t('agents.adpSelectExecMode')"
                    :aria-label="t('agents.adpExecMode')"
                    @update:model-value="b.mode = asMode($event)"
                  />
                  <small class="adp-help">{{ t("agents.adpModeHelp") }}</small>
                </label>
                <label class="modal-field">
                  <span>{{ t("agents.adpContextScope") }}</span>
                  <input
                    :value="t('agents.adpTaskContextOnlyValue')"
                    disabled
                    :title="t('agents.adpTaskContextOnlyFixedTitle')"
                  />
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpUsageNotes") }}</span>
                  <input
                    v-model="b.description"
                    :placeholder="t('agents.adpUsageNotesPh')"
                    data-testid="edit-binding-desc"
                  />
                </label>
              </div>
            </li>
          </ul>
          <div v-else class="adp-empty">
            <i class="fa-regular fa-folder-open" aria-hidden="true" />
            <span>{{ t("agents.adpEmptyInternal") }}</span>
          </div>

          <div class="adp-form">
            <div class="adp-form-label">
              <i class="fa-solid fa-plus" aria-hidden="true" />
              <span>{{ t("agents.adpAddCollabRelation") }}</span>
            </div>
            <div class="adp-form-grid">
              <label class="modal-field">
                <span>{{ t("agents.adpHelpAgent") }}</span>
                <AppSelect
                  class="adp-select"
                  :model-value="form.targetAgentId"
                  :options="targetAgentSelectOptions"
                  :placeholder="t('agents.adpSelectAgentPh')"
                  :aria-label="t('agents.adpHelpAgent')"
                  @update:model-value="form.targetAgentId = String($event)"
                />
              </label>
              <label class="modal-field">
                <span>{{ t("agents.adpCallableName") }}</span>
                <input v-model="form.callableName" placeholder="e.g. order_lookup" />
                <small class="adp-help">{{ t("agents.adpCallableNameHelpStable") }}</small>
              </label>
              <label class="modal-field">
                <span>{{ t("agents.adpExecMode") }}</span>
                <AppSelect
                  class="adp-select"
                  :model-value="form.mode"
                  :options="modeSelectOptions"
                  :placeholder="t('agents.adpSelectExecMode')"
                  :aria-label="t('agents.adpExecMode')"
                  @update:model-value="form.mode = asMode($event)"
                />
                <small class="adp-help">
                  {{ t("agents.adpModeHelpDetail") }}
                </small>
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpUsageNotes") }}</span>
                <input v-model="form.description" :placeholder="t('agents.adpUsageNotesFormPh')" />
              </label>
            </div>
            <div class="adp-form-actions">
              <button type="button" class="primary-button small" @click="onCreate">
                <i class="fa-solid fa-plus" aria-hidden="true" />
                {{ t("agents.adpAddCollab") }}
              </button>
            </div>
          </div>
        </div>
      </section>

      <!-- A2A Inbound -->
      <section class="adp-block" :class="{ open: isBlockOpen('inbound') }" aria-labelledby="adp-inbound-title">
        <button
          type="button"
          class="adp-block-header"
          :aria-expanded="isBlockOpen('inbound')"
          aria-controls="adp-inbound-body"
          @click="toggleBlock('inbound')"
        >
          <div class="adp-block-title">
            <span class="adp-block-icon inbound" aria-hidden="true">
              <i class="fa-solid fa-download" />
            </span>
            <div>
              <strong id="adp-inbound-title">{{ t("agents.adpInboundTitle") }}</strong>
              <small>{{ t("agents.adpInboundSubtitle") }}</small>
            </div>
          </div>
          <div class="adp-block-meta">
            <span class="adp-count" :data-empty="exposures.length === 0">{{ exposures.length }}</span>
            <i
              class="fa-solid fa-chevron-down adp-chevron"
              :class="{ open: isBlockOpen('inbound') }"
              aria-hidden="true"
            />
          </div>
        </button>

        <div v-show="isBlockOpen('inbound')" id="adp-inbound-body" class="adp-block-body">
          <p class="adp-hint">
            {{ t("agents.adpInboundHint") }}
          </p>

          <ul v-if="exposures.length" class="adp-list">
            <li v-for="e in exposures" :key="e.id" class="adp-card" :class="{ disabled: !e.enabled }">
              <div class="adp-card-top">
                <div class="adp-card-identity">
                  <strong class="adp-public-name">{{ e.publicName }}</strong>
                  <span class="adp-pill" :title="e.authMode">{{ authModeShort(e.authMode) }}</span>
                  <span class="adp-pill" :class="e.enabled ? 'on' : 'off'">
                    {{ e.enabled ? t("agents.adpEnabled") : t("agents.adpDisabled") }}
                  </span>
                </div>
                <div class="adp-card-actions">
                  <button type="button" class="ghost-button small" @click="onPreviewCard(e)">
                    <i class="fa-solid fa-id-card" aria-hidden="true" />
                    {{ t("agents.adpViewCard") }}
                  </button>
                  <button type="button" class="ghost-button small" :disabled="!e.enabled" @click="onDisableExposure(e)">
                    {{ t("agents.adpDisable") }}
                  </button>
                  <button type="button" class="ghost-button small" :disabled="e.enabled" @click="onEnableExposure(e)">
                    {{ t("agents.adpReenable") }}
                  </button>
                  <button
                    type="button"
                    class="ghost-button small"
                    data-testid="save-exposure"
                    @click="onSaveExposure(e)"
                  >
                    <i class="fa-solid fa-check" aria-hidden="true" />
                    {{ t("common.save") }}
                  </button>
                </div>
              </div>
              <div class="adp-card-fields">
                <label class="modal-field">
                  <span>{{ t("agents.adpPublicName") }}</span>
                  <input v-model="e.publicName" />
                  <small class="adp-help">{{ t("agents.adpPublicNameHelp") }}</small>
                </label>
                <label class="modal-field" data-testid="exposure-auth-mode">
                  <span>{{ t("agents.adpWhoCanCall") }}</span>
                  <AppSelect
                    class="adp-select"
                    :model-value="e.authMode"
                    :options="authModeSelectOptions"
                    :placeholder="t('agents.adpSelectAccessControl')"
                    :aria-label="t('agents.adpWhoCanCall')"
                    @update:model-value="e.authMode = asAuthMode($event)"
                  />
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpCapabilityBlurb") }}</span>
                  <input v-model="e.publicDescription" :placeholder="t('agents.adpCapabilityBlurbPh')" />
                </label>
              </div>
            </li>
          </ul>
          <div v-else class="adp-empty">
            <i class="fa-solid fa-shield-halved" aria-hidden="true" />
            <span>{{ t("agents.adpEmptyInbound") }}</span>
          </div>

          <div class="adp-form">
            <div class="adp-form-label">
              <i class="fa-solid fa-globe" aria-hidden="true" />
              <span>{{ t("agents.adpOpenExternal") }}</span>
            </div>
            <div class="adp-form-grid">
              <label class="modal-field">
                <span>{{ t("agents.adpPublicName") }}</span>
                <input v-model="exposureForm.publicName" :placeholder="t('agents.adpPublicNamePh')" />
                <small class="adp-help">{{ t("agents.adpPublicNameHelpShort") }}</small>
              </label>
              <label class="modal-field" data-testid="new-exposure-auth-mode">
                <span>{{ t("agents.adpWhoCanCall") }}</span>
                <AppSelect
                  class="adp-select"
                  :model-value="exposureForm.authMode"
                  :options="authModeSelectOptions"
                  :placeholder="t('agents.adpSelectAccessControl')"
                  :aria-label="t('agents.adpWhoCanCall')"
                  @update:model-value="exposureForm.authMode = asAuthMode($event)"
                />
                <small class="adp-help">{{ t("agents.adpAuthHelpProd") }}</small>
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpCapabilityBlurb") }}</span>
                <input v-model="exposureForm.publicDescription" :placeholder="t('agents.adpCapabilityBlurbFormPh')" />
              </label>
            </div>
            <div class="adp-form-actions">
              <button type="button" class="primary-button small" @click="onCreateExposure">
                <i class="fa-solid fa-globe" aria-hidden="true" />
                {{ t("agents.adpOpenExternalBtn") }}
              </button>
            </div>
          </div>

          <pre v-if="cardPreview" class="adp-card-preview">{{ cardPreview }}</pre>
        </div>
      </section>

      <!-- A2A Outbound -->
      <section class="adp-block" :class="{ open: isBlockOpen('outbound') }" aria-labelledby="adp-outbound-title">
        <button
          type="button"
          class="adp-block-header"
          :aria-expanded="isBlockOpen('outbound')"
          aria-controls="adp-outbound-body"
          @click="toggleBlock('outbound')"
        >
          <div class="adp-block-title">
            <span class="adp-block-icon outbound" aria-hidden="true">
              <i class="fa-solid fa-share-from-square" />
            </span>
            <div>
              <strong id="adp-outbound-title">{{ t("agents.adpOutboundTitle") }}</strong>
              <small>{{ t("agents.adpOutboundSubtitle") }}</small>
            </div>
          </div>
          <div class="adp-block-meta">
            <span class="adp-count" :data-empty="remotes.length === 0">{{ remotes.length }}</span>
            <i
              class="fa-solid fa-chevron-down adp-chevron"
              :class="{ open: isBlockOpen('outbound') }"
              aria-hidden="true"
            />
          </div>
        </button>

        <div v-show="isBlockOpen('outbound')" id="adp-outbound-body" class="adp-block-body">
          <p class="adp-hint">
            {{ t("agents.adpOutboundHint") }}
          </p>

          <ul v-if="remotes.length" class="adp-list">
            <li v-for="r in remotes" :key="r.id" class="adp-card" :class="{ disabled: !r.enabled }">
              <div class="adp-card-top">
                <div class="adp-card-identity">
                  <code class="adp-callable">{{ r.callableName }}</code>
                  <i class="fa-solid fa-arrow-right adp-arrow" aria-hidden="true" />
                  <span class="adp-endpoint" :title="r.endpointUrl">{{ r.endpointUrl }}</span>
                  <span class="adp-pill muted">{{ t("agents.adpExternal") }}</span>
                  <span v-if="r.agentCardUrl" class="adp-pill muted">{{ t("agents.adpCardConfigured") }}</span>
                  <span v-if="r.authSecretRef" class="adp-pill muted">{{ t("agents.adpSecretConfigured") }}</span>
                  <span class="adp-pill muted">{{
                    t("agents.adpTimeoutSeconds", { n: Math.round((r.timeoutMs || 0) / 1000) })
                  }}</span>
                  <span class="adp-pill" :class="r.enabled ? 'on' : 'off'">
                    {{ r.enabled ? t("agents.adpEnabled") : t("agents.adpDisabled") }}
                  </span>
                </div>
                <div class="adp-card-actions">
                  <button type="button" class="ghost-button small" :disabled="!r.enabled" @click="onDisableRemote(r)">
                    {{ t("agents.adpDisable") }}
                  </button>
                  <button type="button" class="ghost-button small" :disabled="r.enabled" @click="onEnableRemote(r)">
                    {{ t("agents.adpReenable") }}
                  </button>
                  <button type="button" class="ghost-button small" data-testid="save-remote" @click="onSaveRemote(r)">
                    <i class="fa-solid fa-check" aria-hidden="true" />
                    {{ t("common.save") }}
                  </button>
                </div>
              </div>
              <div class="adp-card-fields">
                <label class="modal-field">
                  <span>{{ t("agents.adpCallableName") }}</span>
                  <input v-model="r.callableName" data-testid="edit-remote-callable" />
                  <small class="adp-help">{{ t("agents.adpCallableNameHelpShort") }}</small>
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpServiceUrl") }}</span>
                  <input v-model="r.endpointUrl" data-testid="edit-remote-endpoint" />
                  <small class="adp-help">{{ t("agents.adpServiceUrlHelp") }}</small>
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpCardUrlOptional") }}</span>
                  <input v-model="r.agentCardUrl" data-testid="edit-remote-card" />
                  <small class="adp-help">{{ t("agents.adpCardUrlHelp") }}</small>
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpAllowedHosts") }}</span>
                  <input
                    data-testid="edit-remote-hosts"
                    :value="(r.allowedHosts || []).join(', ')"
                    @input="
                      r.allowedHosts = ($event.target as HTMLInputElement).value
                        .split(',')
                        .map((s) => s.trim())
                        .filter(Boolean)
                    "
                  />
                  <small class="adp-help">{{ t("agents.adpAllowedHostsHelp") }}</small>
                </label>
                <label class="modal-field">
                  <span>{{ t("agents.adpSecretRef") }}</span>
                  <input v-model="r.authSecretRef" autocomplete="off" data-testid="edit-remote-secret" />
                  <small class="adp-help">{{ t("agents.adpSecretRefHelp") }}</small>
                </label>
                <label class="modal-field">
                  <span>{{ t("agents.adpTimeoutMs") }}</span>
                  <input v-model.number="r.timeoutMs" type="number" min="1000" data-testid="edit-remote-timeout" />
                </label>
                <label class="modal-field wide">
                  <span>{{ t("agents.adpUsageNotes") }}</span>
                  <input v-model="r.description" data-testid="edit-remote-desc" />
                </label>
              </div>
            </li>
          </ul>
          <div v-else class="adp-empty">
            <i class="fa-solid fa-satellite-dish" aria-hidden="true" />
            <span>{{ t("agents.adpEmptyOutbound") }}</span>
          </div>

          <div class="adp-form">
            <div class="adp-form-label">
              <i class="fa-solid fa-plus" aria-hidden="true" />
              <span>{{ t("agents.adpAddExternal") }}</span>
            </div>
            <div class="adp-form-grid">
              <label class="modal-field">
                <span>{{ t("agents.adpCallableName") }}</span>
                <input v-model="remoteForm.callableName" placeholder="e.g. external_analyst" />
                <small class="adp-help">{{ t("agents.adpCallableNameHelpShort") }}</small>
              </label>
              <label class="modal-field">
                <span>{{ t("agents.adpTimeoutMs") }}</span>
                <input v-model.number="remoteForm.timeoutMs" type="number" min="1000" max="600000" />
                <small class="adp-help">{{ t("agents.adpTimeoutDefaultHelp") }}</small>
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpServiceUrlHttps") }}</span>
                <input v-model="remoteForm.endpointUrl" placeholder="https://agent.example.com/a2a" />
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpCardUrlOptional") }}</span>
                <input
                  v-model="remoteForm.agentCardUrl"
                  placeholder="https://agent.example.com/.well-known/agent-card.json"
                />
                <small class="adp-help">{{ t("agents.adpCardUrlDiscoverHelp") }}</small>
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpAllowedHosts") }}</span>
                <input v-model="remoteForm.allowedHosts" placeholder="agent.example.com" />
                <small class="adp-help">{{ t("agents.adpAllowedHostsStrictHelp") }}</small>
              </label>
              <label class="modal-field wide">
                <span>{{ t("agents.adpSecretRefOptional") }}</span>
                <input
                  v-model="remoteForm.authSecretRef"
                  :placeholder="t('agents.adpSecretRefPh')"
                  autocomplete="off"
                />
                <small class="adp-help">{{ t("agents.adpSecretRefHelpNoPlaintext") }}</small>
              </label>
            </div>
            <div class="adp-form-actions">
              <button type="button" class="primary-button small" @click="onCreateRemote">
                <i class="fa-solid fa-plus" aria-hidden="true" />
                {{ t("agents.adpAddExternal") }}
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>

<style scoped>
/* ── Panel shell ── */
.agent-delegation-panel {
  --adp-control-h: 40px;
}

.adp-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px;
}

.adp-intro {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.55;
}

.adp-intro code {
  padding: 1px 5px;
  color: #0f766e;
  background: #f0fdfa;
  border: 1px solid #ccfbf1;
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
}

.adp-loading {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  padding: 10px 12px;
  color: #64748b;
  background: #f8fafc;
  border: 1px dashed #e2e8f0;
  border-radius: 10px;
  font-size: 12px;
}

.adp-loading i {
  color: #0d9488;
}

/* ── Blocks ── */
.adp-block {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 0;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  overflow: hidden;
}

.adp-block.open {
  background: #f8fafc;
}

.adp-block-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  margin: 0;
  padding: 12px 14px;
  color: inherit;
  text-align: left;
  background: transparent;
  border: 0;
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.adp-block-header:hover {
  background: #f1f5f9;
}

.adp-block-header:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.45);
  outline-offset: -2px;
}

.adp-block-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 0 14px 14px;
  border-top: 1px solid #e2e8f0;
  padding-top: 12px;
}

.adp-block-meta {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
}

.adp-chevron {
  color: #94a3b8;
  font-size: 11px;
  transition:
    transform 0.15s ease,
    color 0.15s ease;
}

.adp-chevron.open {
  color: #0f766e;
  transform: rotate(180deg);
}

.adp-block-title {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}

.adp-block-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #0f766e;
  background: #ecfdf5;
  border: 1px solid #a7f3d0;
  border-radius: 8px;
  font-size: 12px;
}

.adp-block-icon.inbound {
  color: #1d4ed8;
  background: #eff6ff;
  border-color: #bfdbfe;
}

.adp-block-icon.outbound {
  color: #7c3aed;
  background: #f5f3ff;
  border-color: #ddd6fe;
}

.adp-block-title strong {
  display: block;
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.3;
}

.adp-block-title small {
  display: block;
  margin-top: 2px;
  color: #64748b;
  font-size: 11px;
  font-weight: 400;
  line-height: 1.4;
}

.adp-count {
  flex-shrink: 0;
  min-width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 8px;
  color: #0f766e;
  background: #ecfdf5;
  border: 1px solid #a7f3d0;
  border-radius: 999px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.adp-count[data-empty="true"] {
  color: #94a3b8;
  background: #f1f5f9;
  border-color: #e2e8f0;
}

.adp-hint {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}

.adp-hint code {
  padding: 1px 5px;
  color: #334155;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  word-break: break-all;
}

/* ── Empty ── */
.adp-empty {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 52px;
  padding: 12px 14px;
  color: #94a3b8;
  background: #fff;
  border: 1px dashed #e2e8f0;
  border-radius: 10px;
  font-size: 12px;
}

.adp-empty i {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #cbd5e1;
  background: #f8fafc;
  border-radius: 8px;
  font-size: 12px;
}

/* ── Cards ── */
.adp-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.adp-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 12px 14px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}

.adp-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.05);
}

.adp-card.disabled {
  opacity: 0.72;
  background: #fafbfc;
}

.adp-card-top {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px 12px;
}

.adp-card-identity {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
  min-width: 0;
  flex: 1 1 220px;
}

.adp-callable {
  padding: 2px 8px;
  color: #0f766e;
  background: #f0fdfa;
  border: 1px solid #99f6e4;
  border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.4;
}

.adp-arrow {
  color: #cbd5e1;
  font-size: 10px;
}

.adp-target {
  color: #0f172a;
  font-size: 12px;
  font-weight: 600;
}

.adp-public-name {
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
}

.adp-endpoint {
  max-width: 280px;
  overflow: hidden;
  color: #475569;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.adp-pill {
  display: inline-flex;
  align-items: center;
  min-height: 22px;
  padding: 0 8px;
  color: #334155;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1;
  white-space: nowrap;
}

.adp-pill.muted {
  color: #64748b;
  font-weight: 600;
}

.adp-pill.on {
  color: #047857;
  background: #ecfdf5;
  border-color: #a7f3d0;
}

.adp-pill.off {
  color: #94a3b8;
  background: #f8fafc;
  border-color: #e2e8f0;
}

.adp-card-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-shrink: 0;
}

.adp-card-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 12px;
  padding-top: 12px;
  border-top: 1px solid #f1f5f9;
}

.adp-card-fields .modal-field.wide,
.adp-form-grid .modal-field.wide {
  grid-column: 1 / -1;
}

.adp-card-fields .modal-field > span,
.adp-form-grid .modal-field > span {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.adp-card-fields .modal-field input,
.adp-form-grid .modal-field input {
  height: var(--adp-control-h);
  min-height: var(--adp-control-h);
  padding: 0 12px;
  color: #1e293b;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  font-size: 12px;
}

.adp-card-fields .modal-field input:focus,
.adp-form-grid .modal-field input:focus {
  background: #fff;
  border-color: rgba(13, 148, 136, 0.45);
  box-shadow: 0 0 0 3px rgba(13, 148, 136, 0.1);
}

.adp-card-fields .modal-field input:disabled {
  color: #94a3b8;
  background: #f1f5f9;
  cursor: not-allowed;
}

.adp-field-hint {
  margin-left: 4px;
  color: #94a3b8;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-style: normal;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: none;
}

.adp-help {
  margin: 2px 0 0;
  color: #94a3b8;
  font-family:
    Inter,
    -apple-system,
    BlinkMacSystemFont,
    "Segoe UI",
    Roboto,
    sans-serif;
  font-size: 11px;
  font-weight: 400;
  letter-spacing: 0;
  line-height: 1.4;
  text-transform: none;
}

/* AppSelect — match studio control height / surface */
.adp-select {
  display: block;
  width: 100%;
  min-width: 0;
}

.adp-select :deep(.app-select-accessibility-shell),
.adp-select.app-select-accessibility-shell,
:deep(.adp-select.app-select-accessibility-shell),
:deep(.adp-select .app-select-accessibility-shell) {
  display: block;
  width: 100%;
}

.adp-card-fields :deep(.adp-select .el-select),
.adp-form-grid :deep(.adp-select .el-select),
.adp-card-fields :deep(.app-select),
.adp-form-grid :deep(.app-select) {
  width: 100%;
}

.adp-card-fields :deep(.adp-select .el-select__wrapper),
.adp-form-grid :deep(.adp-select .el-select__wrapper),
.adp-card-fields :deep(.app-select .el-select__wrapper),
.adp-form-grid :deep(.app-select .el-select__wrapper) {
  min-height: var(--adp-control-h);
  height: var(--adp-control-h);
  padding: 0 12px;
  background: #f8fafc;
  border-radius: 8px;
  box-shadow: 0 0 0 1px #e2e8f0 inset;
  font-size: 12px;
}

.adp-card-fields :deep(.adp-select .el-select__wrapper:hover),
.adp-form-grid :deep(.adp-select .el-select__wrapper:hover),
.adp-card-fields :deep(.app-select .el-select__wrapper:hover),
.adp-form-grid :deep(.app-select .el-select__wrapper:hover) {
  box-shadow: 0 0 0 1px #cbd5e1 inset;
}

.adp-card-fields :deep(.adp-select .el-select__wrapper.is-focused),
.adp-form-grid :deep(.adp-select .el-select__wrapper.is-focused),
.adp-card-fields :deep(.app-select .el-select__wrapper.is-focused),
.adp-form-grid :deep(.app-select .el-select__wrapper.is-focused) {
  background: #fff;
  box-shadow:
    0 0 0 1px rgba(13, 148, 136, 0.45) inset,
    0 0 0 3px rgba(13, 148, 136, 0.1);
}

/* ── Create forms ── */
.adp-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}

.adp-form-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #334155;
  font-size: 12px;
  font-weight: 700;
}

.adp-form-label i {
  color: #0d9488;
  font-size: 11px;
}

.adp-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 12px;
}

.adp-form-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding-top: 2px;
}

.adp-form-actions .primary-button,
.adp-card-actions .ghost-button {
  min-height: 34px;
  padding: 0 12px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
}

.adp-form-actions .primary-button.small,
.adp-card-actions .ghost-button.small {
  min-height: 34px;
  padding: 0 12px;
  font-size: 12px;
}

.adp-form-actions .primary-button {
  background: #0f172a;
  border-color: #0f172a;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.12);
}

.adp-form-actions .primary-button:hover,
.adp-form-actions .primary-button:focus {
  background: #1e293b;
  border-color: #1e293b;
}

.adp-card-actions .ghost-button {
  color: #475569;
  background: #fff;
  border: 1px solid #e2e8f0;
  box-shadow: none;
}

.adp-card-actions .ghost-button:hover:not(:disabled),
.adp-card-actions .ghost-button:focus:not(:disabled) {
  color: #0f172a;
  background: #f8fafc;
  border-color: #cbd5e1;
}

.adp-card-actions .ghost-button:disabled {
  opacity: 0.45;
}

.adp-card-preview {
  margin: 0;
  max-height: 14rem;
  overflow: auto;
  padding: 12px 14px;
  color: #e2e8f0;
  background: #0f172a;
  border: 1px solid #1e293b;
  border-radius: 10px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1.55;
}

@media (max-width: 720px) {
  .adp-card-fields,
  .adp-form-grid {
    grid-template-columns: 1fr;
  }

  .adp-card-top {
    flex-direction: column;
  }

  .adp-card-actions {
    justify-content: flex-start;
    width: 100%;
  }

  .adp-endpoint {
    max-width: 100%;
  }
}
</style>
