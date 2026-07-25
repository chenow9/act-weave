<script setup lang="ts">
/**
 * 运行调试台 — one-shot outbound credential attach panel (UI v0.1 / checklist #13).
 *
 * Constraints:
 * - Token inputs are password-type, never written to Pinia / localStorage / history.
 * - After attach, only `outboundCredentialAttachmentId` is retained for the next message.
 * - On send / fail / leave, clear local token fields and the attachment id.
 * - Broker-only agents: hide the required token form (emit requiresPassthrough=false).
 */
import { onBeforeUnmount, ref, watch } from "vue";
import type { OutboundCredentialAttachmentResult, OutboundCredentialsEnvelope } from "../types/domain";

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
    errorText.value = "请填写 Connection 与业务 Token";
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
    errorText.value = e instanceof Error ? e.message : "出站凭据绑定失败";
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
  <section
    v-if="requiresPassthrough !== false"
    class="debug-outbound-panel"
    aria-label="出站透传凭据（一次性）"
  >
    <header class="debug-outbound-header">
      <strong>出站请求透传</strong>
      <span class="debug-outbound-hint">Token 不会写入会话、日志或本地存储；发送后仅使用一次性 attachment ID。</span>
    </header>
    <div class="debug-outbound-fields">
      <label>
        业务 Token
        <input
          v-model="tokenValue"
          type="password"
          autocomplete="new-password"
          placeholder="一次性业务 Token"
          :disabled="busy || !!attachmentId"
        />
      </label>
      <label>
        过期时间（本地）
        <input v-model="expiresAtLocal" type="datetime-local" :disabled="busy || !!attachmentId" />
      </label>
      <button type="button" class="debug-outbound-attach" :disabled="busy || !!attachmentId" @click="onAttach">
        {{ attachmentId ? "已绑定" : "绑定出站凭据" }}
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
        清除绑定
      </button>
    </div>
    <p v-if="attachmentId" class="debug-outbound-ok">
      已绑定（将随下一条消息消费）· 过期 {{ expiresAt }}
    </p>
    <p v-if="errorText" class="debug-outbound-error">{{ errorText }}</p>
  </section>
  <section v-else class="debug-outbound-panel broker-only" aria-label="Broker 出站说明">
    <p class="debug-outbound-hint">
      当前 Agent 仅需 Broker / OBO：将以你的内部用户 Subject 换取短期 Token，无需填写业务 Token。
    </p>
  </section>
</template>

<style scoped>
.debug-outbound-panel {
  border: 1px solid var(--el-border-color, #dcdfe6);
  border-radius: 8px;
  padding: 12px;
  margin: 8px 0;
  background: var(--el-fill-color-blank, #fff);
}
.debug-outbound-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 8px;
}
.debug-outbound-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
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
}
.debug-outbound-fields input[type="password"],
.debug-outbound-fields input[type="datetime-local"] {
  min-width: 200px;
  padding: 6px 8px;
}
.debug-outbound-attach,
.debug-outbound-clear {
  padding: 6px 12px;
  cursor: pointer;
}
.debug-outbound-ok {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--el-color-success, #67c23a);
}
.debug-outbound-error {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--el-color-danger, #f56c6c);
}
.broker-only {
  background: var(--el-fill-color-light, #f5f7fa);
}
</style>
