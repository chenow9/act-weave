<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { useModalFocus } from "../composables/useModalFocus";
import { useConnectionsStore } from "../stores/connections";
import { useToolsStore } from "../stores/tools";
import type { OutboundCredentialsEnvelope, Tool, ToolTestExecutionResult } from "../types/domain";
import { buildDefaultToolTestInput, collectToolTestParams } from "../utils/tool-test-inputs";

const props = defineProps<{
  modelValue: boolean;
  tool: Tool | null;
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: boolean): void;
}>();

const toolsStore = useToolsStore();
const connectionsStore = useConnectionsStore();
const inputDraft = ref<Record<string, unknown>>({});
const running = ref(false);
const result = ref<ToolTestExecutionResult | null>(null);
const errorMessage = ref("");
const modalRef = ref<HTMLElement | null>(null);
/** Write-only passthrough token — never put in Pinia / result / history. */
const passthroughToken = ref("");
const passthroughExpiresAt = ref("");
const passthroughConnectionId = ref("");

const effectiveParams = computed(() => (props.tool ? collectToolTestParams(props.tool) : []));
const groupedParams = computed(() => {
  if (!props.tool) return [];
  return ["Path", "Query", "Header", "Body"]
    .map((location) => ({
      location,
      params: effectiveParams.value.filter((param) => param.location === location),
    }))
    .filter((group) => group.params.length > 0);
});
const testBlockedReason = computed(() => {
  const tool = props.tool;
  if (!tool?.versions.length) return "";
  const editableVersion = [...tool.versions].reverse().find((version) => version.lifecycleStatus !== "PUBLISHED");
  if (editableVersion) return "";
  return "当前只有已发布版本，不能直接重测。请先编辑 Tool 创建新的 Draft Version，再执行测试。";
});

const toolConnection = computed(() => {
  const connectionId = props.tool?.connectionId;
  if (!connectionId) return undefined;
  const connections = connectionsStore.serviceConnections || [];
  return connections.find((c) => c.id === connectionId);
});
const requiresPassthrough = computed(() => toolConnection.value?.outboundMode === "REQUEST_PASSTHROUGH");

watch(
  () => props.tool,
  (tool) => {
    inputDraft.value = tool ? buildDefaultToolTestInput(tool) : {};
    result.value = null;
    errorMessage.value = "";
    passthroughToken.value = "";
    // datetime-local default: now + 1h (server requires expiresAt strictly in the future)
    passthroughExpiresAt.value = toDatetimeLocalValue(new Date(Date.now() + 60 * 60 * 1000));
    passthroughConnectionId.value = tool?.connectionId || "";
  },
  { immediate: true },
);

/** Format Date for <input type="datetime-local"> (local wall clock, no seconds required). */
function toDatetimeLocalValue(date: Date) {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Strip accidental "Bearer " prefix — connection inject already adds it. */
function normalizePassthroughToken(raw: string) {
  return raw.trim().replace(/^Bearer\s+/i, "").trim();
}

useModalFocus({
  visible: () => props.modelValue,
  modalRef,
  onClose: closeDialog,
});

function formatInputField(value: unknown) {
  if (typeof value === "string") return value;
  return JSON.stringify(value ?? {}, null, 2);
}

function parseComplexInput(value: string) {
  try {
    return value.trim() ? JSON.parse(value) : {};
  } catch {
    return value;
  }
}

function buildOutboundEnvelope(): OutboundCredentialsEnvelope | undefined {
  if (!requiresPassthrough.value) return undefined;
  const connectionId = passthroughConnectionId.value || props.tool?.connectionId || "";
  const token = normalizePassthroughToken(passthroughToken.value);
  if (!connectionId || !token) {
    errorMessage.value = "透传 Connection 需要一次性业务 Token 与 Connection。";
    return undefined;
  }
  // Prefer explicit UI value; fall back to +1h. Must be strictly after server "now".
  const expiresDate = passthroughExpiresAt.value
    ? new Date(passthroughExpiresAt.value)
    : new Date(Date.now() + 60 * 60 * 1000);
  if (Number.isNaN(expiresDate.getTime())) {
    errorMessage.value = "过期时间格式无效，请重新选择（本地时间）。";
    return undefined;
  }
  // Require at least ~2 minutes remaining to absorb clock skew + request RTT.
  if (expiresDate.getTime() <= Date.now() + 2 * 60 * 1000) {
    errorMessage.value = "过期时间必须晚于当前时间至少 2 分钟（当前设置已过期或过近）。";
    return undefined;
  }
  return {
    schemaVersion: "outbound-credentials.v1",
    bindings: [
      {
        connectionId,
        credentialType: "ACCESS_TOKEN",
        value: token,
        expiresAt: expiresDate.toISOString(),
      },
    ],
  };
}

async function runTest() {
  if (!props.tool || running.value || testBlockedReason.value) return;
  running.value = true;
  errorMessage.value = "";
  let envelope: OutboundCredentialsEnvelope | undefined;
  try {
    if (requiresPassthrough.value) {
      envelope = buildOutboundEnvelope();
      if (!envelope) {
        running.value = false;
        return;
      }
      result.value = (await toolsStore.testToolWithOutbound(
        props.tool.id,
        inputDraft.value,
        envelope,
      )) as ToolTestExecutionResult;
    } else {
      result.value = await toolsStore.testTool(props.tool.id, inputDraft.value);
    }
    errorMessage.value = formatToolTestError(result.value) || result.value.errorMessage || "";
  } catch (error) {
    errorMessage.value = toolTestActionError(error);
  } finally {
    // Wipe write-only token after request (success or fail); never keep in component long-term.
    passthroughToken.value = "";
    if (envelope) {
      envelope.bindings.forEach((b) => {
        b.value = "";
      });
    }
    running.value = false;
  }
}

function toolTestActionError(error: unknown) {
  const payload = (error as { response?: { data?: { error?: string | { message?: string; code?: string } } } })
    .response?.data?.error;
  const code = typeof payload === "object" && payload ? payload.code : undefined;
  const message = typeof payload === "string" ? payload : payload?.message;
  if (code === "OUTBOUND_CREDENTIAL_EXPIRED" || /no longer available|expired/i.test(message || "")) {
    return "出站 Token 信封已过期：请把「过期时间」调到当前时间之后（建议 +1 小时），并重新粘贴业务 Token。";
  }
  if (code === "OUTBOUND_CREDENTIAL_INVALID" || /envelope is not valid/i.test(message || "")) {
    return "出站凭据无效：请检查 Token 是否为空/含换行、Connection 是否匹配，以及过期时间是否为将来时间。Token 勿重复加 Bearer 前缀。";
  }
  if (typeof message === "string" && message) return message;
  return error instanceof Error && error.message ? error.message : "执行测试失败";
}

function closeDialog() {
  passthroughToken.value = "";
  passthroughExpiresAt.value = "";
  emit("update:modelValue", false);
}

function responseMessage(value: unknown) {
  if (typeof value !== "object" || value === null) return "";
  const record = value as Record<string, unknown>;
  return typeof record.msg === "string" ? record.msg : typeof record.message === "string" ? record.message : "";
}

function formatResponseBody(body: unknown) {
  if (body === undefined || body === null) return "(无响应体)";
  if (typeof body === "string") {
    try {
      return JSON.stringify(JSON.parse(body), null, 2);
    } catch {
      return body;
    }
  }
  return JSON.stringify(body, null, 2);
}

function formatToolTestError(testResult: ToolTestExecutionResult) {
  const upstreamMessage = responseMessage(testResult.responseBody);
  const credentialFailure =
    testResult.responseStatus === 401 ||
    testResult.responseStatus === 403 ||
    /401|403/.test(testResult.errorMessage) ||
    /令牌|token|凭证|过期|无效/i.test(upstreamMessage);

  if (!credentialFailure) return "";

  const detail = upstreamMessage || testResult.errorMessage || `HTTP ${testResult.responseStatus}`;
  return `服务连接凭证无效或已过期：${detail}。请到服务连接更新凭证后重新执行测试。`;
}

function updateBooleanInput(paramName: string, event: Event) {
  inputDraft.value[paramName] = (event.target as HTMLInputElement).checked;
}

function updateNumberInput(paramName: string, event: Event) {
  const value = (event.target as HTMLInputElement).value;
  inputDraft.value[paramName] = value === "" ? 0 : Number(value);
}

function updateStringInput(paramName: string, event: Event) {
  inputDraft.value[paramName] = (event.target as HTMLInputElement).value;
}

function updateComplexInput(paramName: string, event: Event) {
  inputDraft.value[paramName] = parseComplexInput((event.target as HTMLTextAreaElement).value);
}
</script>

<template>
  <div v-if="modelValue" class="modal-backdrop tool-test-modal" @click.self="closeDialog">
    <section
      ref="modalRef"
      class="modal-card tool-test-modal-card"
      role="dialog"
      aria-modal="true"
      aria-label="测试工具"
    >
      <header class="modal-card-head">
        <div>
          <span>Tool Runtime Test</span>
          <h3>测试工具</h3>
        </div>
        <button
          class="icon-action-button"
          type="button"
          aria-label="关闭测试工具"
          data-modal-initial-focus
          @click="closeDialog"
        >
          <i class="fa-solid fa-xmark" aria-hidden="true" />
        </button>
      </header>

      <div class="tool-test-dialog-grid">
        <section class="tool-test-form-card">
          <header class="tool-test-section-header">
            <strong>{{ tool?.name || "未选择工具" }}</strong>
            <span>按参数契约生成默认测试入参，可直接修改后执行。</span>
          </header>

          <div v-for="group in groupedParams" :key="group.location" class="tool-test-param-group">
            <h4>{{ group.location }}</h4>
            <div v-for="param in group.params" :key="`${group.location}-${param.name}`" class="tool-test-param-row">
              <label>{{ param.name }}</label>
              <label v-if="param.type === 'boolean'" class="tool-test-checkbox">
                <input
                  type="checkbox"
                  :checked="Boolean(inputDraft[param.name])"
                  @change="updateBooleanInput(param.name, $event)"
                />
                <span>{{ Boolean(inputDraft[param.name]) ? "true" : "false" }}</span>
              </label>
              <input
                v-else-if="param.type === 'integer' || param.type === 'number'"
                class="tool-test-input"
                type="number"
                :step="param.type === 'integer' ? '1' : 'any'"
                :value="Number(inputDraft[param.name] ?? 0)"
                @input="updateNumberInput(param.name, $event)"
              />
              <input
                v-else-if="param.type === 'string'"
                class="tool-test-input"
                type="text"
                :value="String(inputDraft[param.name] ?? '')"
                @input="updateStringInput(param.name, $event)"
              />
              <textarea
                v-else
                class="tool-test-input tool-test-textarea"
                rows="4"
                :value="formatInputField(inputDraft[param.name])"
                @input="updateComplexInput(param.name, $event)"
              ></textarea>
            </div>
          </div>

          <section
            v-if="requiresPassthrough"
            class="tool-test-outbound-envelope"
            data-testid="tool-test-outbound-envelope"
            aria-label="出站透传凭据（一次性）"
          >
            <header>
              <strong>出站请求透传</strong>
              <span>Token 为 write-only，不会写入测试结果、历史或本地存储。</span>
            </header>
            <label>
              业务 Token
              <input
                v-model="passthroughToken"
                type="password"
                autocomplete="new-password"
                data-testid="tool-test-passthrough-token"
                placeholder="一次性业务 Token"
                :disabled="running"
              />
            </label>
            <label>
              过期时间
              <input
                v-model="passthroughExpiresAt"
                type="datetime-local"
                data-testid="tool-test-passthrough-expires"
                :disabled="running"
              />
            </label>
          </section>

          <div class="tool-test-dialog-actions">
            <p v-if="testBlockedReason" class="tool-test-error" role="alert">{{ testBlockedReason }}</p>
            <button class="ghost-button" type="button" @click="closeDialog">取消</button>
            <button
              class="primary-button"
              type="button"
              :disabled="running || Boolean(testBlockedReason)"
              @click="runTest"
            >
              <i class="fa-solid fa-vial" aria-hidden="true" />
              {{ running ? "执行中..." : "执行测试" }}
            </button>
          </div>
        </section>

        <section class="tool-test-result-card">
          <header class="tool-test-result-summary">
            <strong>{{ result ? (result.passed ? "测试通过" : "测试失败") : "等待执行" }}</strong>
            <span v-if="result">HTTP {{ result.responseStatus }}</span>
            <span v-if="result">{{ result.latencyMs }}ms</span>
          </header>

          <p v-if="errorMessage" class="tool-test-error">{{ errorMessage }}</p>

          <div v-if="result" class="tool-test-result-panels">
            <div>
              <h4>请求入参</h4>
              <pre class="tool-test-json-block">{{ JSON.stringify(result.requestInput, null, 2) }}</pre>
            </div>
            <div>
              <h4>上游响应原文</h4>
              <pre class="tool-test-json-block">{{ formatResponseBody(result.responseBody) }}</pre>
            </div>
          </div>
        </section>
      </div>
    </section>
  </div>
</template>

<style scoped>
.tool-test-outbound-envelope {
  margin: 12px 0;
  padding: 10px 12px;
  border: 1px solid #dbeafe;
  border-radius: 8px;
  background: #f8fafc;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.tool-test-outbound-envelope header {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.tool-test-outbound-envelope header span {
  font-size: 12px;
  color: #64748b;
}
.tool-test-outbound-envelope label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}
.tool-test-outbound-envelope input {
  padding: 6px 8px;
}
</style>
