<script setup lang="ts">
/**
 * Run console — one-shot outbound credential attach panel (UI v0.1 / checklist #13).
 *
 * Constraints:
 * - Token inputs are password-type, never written to Pinia / localStorage / history.
 * - After attach, only `outboundCredentialAttachmentId` is retained for the next message.
 * - On send / fail / leave, clear local token fields and the attachment id.
 * - Broker-only agents: hide the required token form (emit requiresPassthrough=false).
 */
import { onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { OutboundCredentialAttachmentResult, OutboundCredentialsEnvelope } from "../types/domain";

const { t } = useI18n();

const props = defineProps<{
  workspaceId: string;
  sessionId: string;
  /** When false, hide Token inputs (Broker-only debug). */
  requiresPassthrough?: boolean;
  connectionId?: string;
  attach: (body: OutboundCredentialsEnvelope) => Promise<OutboundCredentialAttachmentResult>;
}>();

const emit = defineEmits<{
  (e: "attachment", id: string | null): void;
}>();

const tokenValue = ref("");
const expiresAtLocal = ref("");
const attachmentId = ref<string | null>(null);
const expiresAt = ref<string | null>(null);
const errorText = ref("");
const busy = ref(false);

function clearSecrets() {
  tokenValue.value = "";
  expiresAtLocal.value = "";
}

function clearAttachment() {
  attachmentId.value = null;
  expiresAt.value = null;
  emit("attachment", null);
}

async function onAttach() {
  errorText.value = "";
  if (!props.requiresPassthrough) {
    return;
  }
  if (!props.connectionId || !tokenValue.value.trim()) {
    errorText.value = t("chat.attachNeedConnectionToken");
    return;
  }
  const exp = expiresAtLocal.value
    ? new Date(expiresAtLocal.value).toISOString()
    : new Date(Date.now() + 10 * 60 * 1000).toISOString();
  const body: OutboundCredentialsEnvelope = {
    schemaVersion: "outbound-credentials.v1",
    bindings: [
      {
        connectionId: props.connectionId,
        credentialType: "ACCESS_TOKEN",
        value: tokenValue.value,
        expiresAt: exp,
      },
    ],
  };
  busy.value = true;
  try {
    const result = await props.attach(body);
    attachmentId.value = result.outboundCredentialAttachmentId;
    expiresAt.value = result.expiresAt;
    emit("attachment", attachmentId.value);
    clearSecrets();
  } catch (e) {
    errorText.value = e instanceof Error ? e.message : t("chat.attachFailed");
    clearSecrets();
    clearAttachment();
  } finally {
    busy.value = false;
    // Best-effort wipe of the request body reference.
    body.bindings.forEach((b) => {
      b.value = "";
    });
  }
}

watch(
  () => props.sessionId,
  () => {
    clearSecrets();
    clearAttachment();
  },
);

onBeforeUnmount(() => {
  clearSecrets();
  clearAttachment();
});

defineExpose({ clearSecrets, clearAttachment, attachmentId });
</script>

<template>
  <section v-if="requiresPassthrough !== false" class="debug-outbound-panel" :aria-label="t('chat.outboundAria')">
    <header class="debug-outbound-header">
      <strong>{{ t("chat.outboundTitle") }}</strong>
      <span class="debug-outbound-hint">{{ t("chat.outboundHint") }}</span>
    </header>
    <div class="debug-outbound-fields">
      <label>
        {{ t("chat.businessToken") }}
        <input
          v-model="tokenValue"
          type="password"
          autocomplete="new-password"
          :placeholder="t('chat.businessTokenPh')"
          :disabled="busy || !!attachmentId"
        />
      </label>
      <label>
        {{ t("chat.expiresLocal") }}
        <input v-model="expiresAtLocal" type="datetime-local" :disabled="busy || !!attachmentId" />
      </label>
      <button
        type="button"
        class="debug-outbound-attach"
        :disabled="busy || !!attachmentId"
        :aria-busy="busy ? 'true' : undefined"
        @click="onAttach"
      >
        {{ attachmentId ? t("chat.bound") : busy ? t("chat.binding") : t("chat.bindOutbound") }}
      </button>
      <button
        v-if="attachmentId"
        type="button"
        class="debug-outbound-clear"
        @click="
          clearSecrets();
          clearAttachment();
        "
      >
        {{ t("chat.clearBinding") }}
      </button>
    </div>
    <p v-if="attachmentId" class="debug-outbound-ok">
      {{ t("chat.boundExpires", { at: expiresAt }) }}
    </p>
    <p v-if="errorText" class="debug-outbound-error">{{ errorText }}</p>
  </section>
  <section v-else class="debug-outbound-panel broker-only" :aria-label="t('chat.brokerAria')">
    <p class="debug-outbound-hint">{{ t("chat.brokerOnlyHint") }}</p>
  </section>
</template>

<style scoped>
.debug-outbound-panel {
  border: 1px solid var(--aw-border, #e2e8f0);
  border-radius: 8px;
  padding: 12px;
  margin: 8px 0;
  background: #fff;
}
.debug-outbound-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 8px;
}
.debug-outbound-hint {
  font-size: 12px;
  color: #64748b;
}
.debug-outbound-fields {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: flex-end;
}
.debug-outbound-fields label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
  font-weight: 600;
  color: #475569;
}
.debug-outbound-fields input[type="password"],
.debug-outbound-fields input[type="datetime-local"] {
  min-width: 200px;
  min-height: 32px;
  padding: 6px 10px;
  border: 1px solid var(--aw-border, #e2e8f0);
  border-radius: 6px;
  background: #fff;
  color: #0f172a;
  font-size: 12px;
  font-weight: 500;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    background-color 0.16s ease,
    opacity 0.16s ease;
}
.debug-outbound-fields input[type="password"]:hover:not(:disabled),
.debug-outbound-fields input[type="datetime-local"]:hover:not(:disabled) {
  border-color: rgba(13, 148, 136, 0.35);
}
.debug-outbound-fields input[type="password"]:focus-visible,
.debug-outbound-fields input[type="datetime-local"]:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.debug-outbound-fields input[type="password"]:disabled,
.debug-outbound-fields input[type="datetime-local"]:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  background: #f8fafc;
}
.debug-outbound-attach,
.debug-outbound-clear {
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 12px;
  border: 1px solid var(--aw-border, #e2e8f0);
  border-radius: 6px;
  background: #f8fafc;
  color: #475569;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  transition:
    color 0.16s ease,
    background-color 0.16s ease,
    border-color 0.16s ease,
    transform 0.16s ease,
    opacity 0.16s ease;
}
.debug-outbound-attach:hover:not(:disabled),
.debug-outbound-clear:hover:not(:disabled) {
  color: var(--aw-cyan, #0d9488);
  background: #fff;
  border-color: rgba(13, 148, 136, 0.35);
}
.debug-outbound-attach:focus-visible,
.debug-outbound-clear:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.debug-outbound-attach:active:not(:disabled),
.debug-outbound-clear:active:not(:disabled) {
  transform: scale(0.98);
}
.debug-outbound-attach:disabled,
.debug-outbound-clear:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.debug-outbound-ok {
  margin: 8px 0 0;
  font-size: 12px;
  color: #047857;
}
.debug-outbound-error {
  margin: 8px 0 0;
  font-size: 12px;
  color: #b91c1c;
}
.broker-only {
  background: #f8fafc;
}
</style>
