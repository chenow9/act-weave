<script setup lang="ts">
/**
 * Manage internal Agent→Agent bindings and A2A remotes for one agent.
 * Styles reuse existing agent-studio section patterns.
 */
import { computed, onMounted, ref, watch } from "vue";
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

const props = defineProps<{
  workspaceId: string;
  agentId: string;
  /** Other agents in workspace for target select. */
  agentOptions: { id: string; name: string }[];
}>();

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
const allowAuthNone = computed(() => capabilities.value.allowAuthNone);
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
    error.value = e instanceof Error ? e.message : "加载委派配置失败";
  } finally {
    loading.value = false;
  }
}

async function onCreate() {
  error.value = "";
  if (!form.value.targetAgentId || !form.value.callableName.trim()) {
    error.value = "请填写目标 Agent 与调用名称";
    return;
  }
  if (!/^[a-z][a-z0-9_]*$/.test(form.value.callableName.trim())) {
    error.value = "callableName 需为小写字母开头的标识符";
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
    error.value = e instanceof Error ? e.message : "创建绑定失败";
  }
}

async function onDisable(b: AgentDelegationBinding) {
  error.value = "";
  try {
    await disableDelegationBinding(props.workspaceId, b.id, b.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "禁用失败";
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
    error.value = e instanceof Error ? e.message : "启用失败";
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
    error.value = e instanceof Error ? e.message : "保存绑定失败";
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
    error.value = e instanceof Error ? e.message : "启用暴露失败";
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
    error.value = e instanceof Error ? e.message : "保存暴露失败";
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
    error.value = e instanceof Error ? e.message : "启用远端失败";
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
    error.value = e instanceof Error ? e.message : "保存远端失败";
  }
}

async function onCreateExposure() {
  error.value = "";
  cardPreview.value = "";
  if (!exposureForm.value.publicName.trim()) {
    error.value = "请填写公开名称";
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
    error.value = e instanceof Error ? e.message : "创建 A2A 暴露失败";
  }
}

async function onDisableExposure(exp: AgentA2AExposure) {
  error.value = "";
  try {
    await disableA2AExposure(props.workspaceId, exp.id, exp.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "禁用暴露失败";
  }
}

async function onPreviewCard(exp: AgentA2AExposure) {
  error.value = "";
  try {
    const card = await previewA2AAgentCard(props.workspaceId, exp.id);
    cardPreview.value = JSON.stringify(card, null, 2);
  } catch (e) {
    error.value = e instanceof Error ? e.message : "预览 Agent Card 失败";
  }
}

async function onDisableRemote(r: AgentA2ARemoteBinding) {
  error.value = "";
  try {
    await disableA2ARemote(props.workspaceId, r.id, r.version);
    await reload();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "禁用远端失败";
  }
}

async function onCreateRemote() {
  error.value = "";
  const hosts = remoteForm.value.allowedHosts
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  if (!remoteForm.value.callableName || !remoteForm.value.endpointUrl || hosts.length === 0) {
    error.value = "远端绑定需填写名称、endpoint 与 allowedHosts";
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
    error.value = e instanceof Error ? e.message : "创建远端绑定失败";
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
      <span><i class="fa-solid fa-sitemap" aria-hidden="true" /> Agent 委派与 A2A</span>
    </header>
    <p class="muted small">
      显式绑定后，父 Agent 可通过稳定调用名委托子 Agent（Eino AgentTool）。默认 TASK_ONLY，不泄漏 system
      prompt。
    </p>
    <p v-if="error" class="agent-studio-inline-warning" role="alert">{{ error }}</p>
    <p v-if="loading" class="muted">加载中…</p>

    <h4>内部委派绑定</h4>
    <ul v-if="bindings.length" class="binding-list">
      <li v-for="b in bindings" :key="b.id" class="edit-row">
        <div class="row-main">
          <code>{{ b.callableName }}</code>
          → {{ agentName(b.targetAgentId) }}
          <span class="pill">{{ b.mode }}</span>
          <span class="pill">{{ b.contextPolicy }}</span>
          <span class="pill" :class="{ off: !b.enabled }">{{ b.enabled ? "启用" : "禁用" }}</span>
          <button type="button" class="ghost-button small" :disabled="!b.enabled" @click="onDisable(b)">
            软禁用
          </button>
          <button type="button" class="ghost-button small" :disabled="b.enabled" @click="onEnableBinding(b)">
            重新启用
          </button>
          <button type="button" class="ghost-button small" data-testid="save-binding" @click="onSaveBinding(b)">
            保存
          </button>
        </div>
        <div class="row-edit">
          <label>
            调用名
            <input v-model="b.callableName" data-testid="edit-binding-callable" />
          </label>
          <label>
            目标 Agent
            <select v-model="b.targetAgentId" data-testid="edit-binding-target">
              <option
                v-for="opt in agentOptions.filter((a) => a.id !== agentId)"
                :key="opt.id"
                :value="opt.id"
              >
                {{ opt.name }}
              </option>
            </select>
          </label>
          <label>
            模式
            <select v-model="b.mode" data-testid="edit-binding-mode">
              <option value="INLINE">INLINE</option>
              <option value="TASK">TASK</option>
            </select>
          </label>
          <label>
            contextPolicy
            <input :value="b.contextPolicy" disabled title="当前仅 TASK_ONLY" />
          </label>
          <label class="wide">
            描述
            <input v-model="b.description" placeholder="描述" data-testid="edit-binding-desc" />
          </label>
        </div>
      </li>
    </ul>
    <p v-else class="muted small">暂无内部绑定</p>

    <div class="delegation-form">
      <label>
        目标 Agent
        <select v-model="form.targetAgentId">
          <option value="">选择…</option>
          <option
            v-for="opt in agentOptions.filter((a) => a.id !== agentId)"
            :key="opt.id"
            :value="opt.id"
          >
            {{ opt.name }}
          </option>
        </select>
      </label>
      <label>
        调用名 (callableName)
        <input v-model="form.callableName" placeholder="call_helper" />
      </label>
      <label>
        模式
        <select v-model="form.mode">
          <option value="INLINE">INLINE（同 Run）</option>
          <option value="TASK">TASK（独立 child Run）</option>
        </select>
      </label>
      <label class="wide">
        描述
        <input v-model="form.description" placeholder="何时调用该子 Agent" />
      </label>
      <button type="button" class="primary-button small" @click="onCreate">添加绑定</button>
    </div>

    <h4>A2A Inbound 暴露（Agent Card / 鉴权）</h4>
    <p class="muted small">
      仅 allowlist 中的 Agent 可通过 <code>/a2a/workspaces/:wid/agents/:id</code> 被外部调用。生产默认
      AGENT_ACCESS（Bearer 令牌）。
    </p>
    <ul v-if="exposures.length" class="binding-list">
      <li v-for="e in exposures" :key="e.id" class="edit-row">
        <div class="row-main">
          <strong>{{ e.publicName }}</strong>
          <span class="pill">{{ e.authMode }}</span>
          <span class="pill" :class="{ off: !e.enabled }">{{ e.enabled ? "启用" : "禁用" }}</span>
          <button type="button" class="ghost-button small" @click="onPreviewCard(e)">Agent Card</button>
          <button type="button" class="ghost-button small" :disabled="!e.enabled" @click="onDisableExposure(e)">
            软禁用
          </button>
          <button type="button" class="ghost-button small" :disabled="e.enabled" @click="onEnableExposure(e)">
            重新启用
          </button>
          <button type="button" class="ghost-button small" data-testid="save-exposure" @click="onSaveExposure(e)">
            保存
          </button>
        </div>
        <div class="row-edit">
          <label>
            公开名称
            <input v-model="e.publicName" />
          </label>
          <label>
            鉴权
            <select v-model="e.authMode" data-testid="exposure-auth-mode">
              <option
                v-for="mode in capabilities.authModes"
                :key="mode"
                :value="mode"
              >
                {{ mode }}
              </option>
            </select>
          </label>
          <label class="wide">
            描述
            <input v-model="e.publicDescription" />
          </label>
        </div>
      </li>
    </ul>
    <p v-else class="muted small">当前 Agent 未对外暴露</p>
    <div class="delegation-form">
      <label>
        公开名称
        <input v-model="exposureForm.publicName" placeholder="Public Helper" />
      </label>
      <label>
        鉴权模式
        <select v-model="exposureForm.authMode" data-testid="new-exposure-auth-mode">
          <option
            v-for="mode in capabilities.authModes"
            :key="'new-' + mode"
            :value="mode"
          >
            {{ mode === "AGENT_ACCESS" ? "AGENT_ACCESS（推荐）" : mode }}
          </option>
        </select>
      </label>
      <label class="wide">
        公开描述
        <input v-model="exposureForm.publicDescription" placeholder="A2A skill description" />
      </label>
      <button type="button" class="primary-button small" @click="onCreateExposure">启用 Inbound 暴露</button>
    </div>
    <pre v-if="cardPreview" class="card-preview">{{ cardPreview }}</pre>

    <h4>外部 A2A 远端（Outbound）</h4>
    <ul v-if="remotes.length" class="binding-list">
      <li v-for="r in remotes" :key="r.id" class="edit-row">
        <div class="row-main">
          <code>{{ r.callableName }}</code>
          → {{ r.endpointUrl }}
          <span class="pill">A2A</span>
          <span v-if="r.agentCardUrl" class="pill">card</span>
          <span v-if="r.authSecretRef" class="pill">secret-ref</span>
          <span class="pill">{{ r.timeoutMs }}ms</span>
          <span class="pill" :class="{ off: !r.enabled }">{{ r.enabled ? "启用" : "禁用" }}</span>
          <button
            type="button"
            class="ghost-button small"
            :disabled="!r.enabled"
            @click="onDisableRemote(r)"
          >
            软禁用
          </button>
          <button type="button" class="ghost-button small" :disabled="r.enabled" @click="onEnableRemote(r)">
            重新启用
          </button>
          <button type="button" class="ghost-button small" data-testid="save-remote" @click="onSaveRemote(r)">
            保存
          </button>
        </div>
        <div class="row-edit">
          <label>
            调用名
            <input v-model="r.callableName" data-testid="edit-remote-callable" />
          </label>
          <label class="wide">
            Endpoint
            <input v-model="r.endpointUrl" data-testid="edit-remote-endpoint" />
          </label>
          <label class="wide">
            Agent Card URL
            <input v-model="r.agentCardUrl" data-testid="edit-remote-card" />
          </label>
          <label class="wide">
            Allowed hosts（逗号分隔）
            <input
              data-testid="edit-remote-hosts"
              :value="(r.allowedHosts || []).join(',')"
              @input="
                r.allowedHosts = ($event.target as HTMLInputElement).value
                  .split(',')
                  .map((s) => s.trim())
                  .filter(Boolean)
              "
            />
          </label>
          <label>
            Auth secret ref
            <input v-model="r.authSecretRef" autocomplete="off" data-testid="edit-remote-secret" />
          </label>
          <label>
            Timeout ms
            <input v-model.number="r.timeoutMs" type="number" min="1000" data-testid="edit-remote-timeout" />
          </label>
          <label class="wide">
            描述
            <input v-model="r.description" data-testid="edit-remote-desc" />
          </label>
        </div>
      </li>
    </ul>
    <p v-else class="muted small">暂无远端绑定</p>
    <div class="delegation-form">
      <label>
        调用名
        <input v-model="remoteForm.callableName" placeholder="remote_analyst" />
      </label>
      <label>
        超时 (ms)
        <input v-model.number="remoteForm.timeoutMs" type="number" min="1000" max="600000" />
      </label>
      <label class="wide">
        Endpoint URL (https)
        <input v-model="remoteForm.endpointUrl" placeholder="https://agent.example/a2a" />
      </label>
      <label class="wide">
        Agent Card URL（可选；填写后 discovery 失败硬失败）
        <input v-model="remoteForm.agentCardUrl" placeholder="https://agent.example/.well-known/agent-card.json" />
      </label>
      <label class="wide">
        Allowed hosts (逗号分隔)
        <input v-model="remoteForm.allowedHosts" placeholder="agent.example" />
      </label>
      <label class="wide">
        Auth secret ref（只存引用，不回显密文）
        <input
          v-model="remoteForm.authSecretRef"
          placeholder="secret:&lt;workspaceId&gt;:&lt;secretId&gt;"
          autocomplete="off"
        />
      </label>
      <button type="button" class="primary-button small" @click="onCreateRemote">添加远端</button>
    </div>
  </section>
</template>

<style scoped>
.agent-delegation-panel h4 {
  margin: 1rem 0 0.5rem;
  font-size: 0.9rem;
}
.binding-list {
  list-style: none;
  padding: 0;
  margin: 0 0 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.binding-list li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.85rem;
}
.binding-list li.edit-row {
  flex-direction: column;
  align-items: stretch;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  padding: 0.5rem 0.6rem;
}
.row-main {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
}
.row-edit {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.4rem 0.6rem;
  margin-top: 0.4rem;
}
.row-edit label {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  font-size: 0.75rem;
}
.row-edit label.wide {
  grid-column: 1 / -1;
}
.row-edit input,
.row-edit select {
  border: 1px solid #e5e7eb;
  border-radius: 0.35rem;
  padding: 0.3rem 0.45rem;
}
.pill {
  background: #f3f4f6;
  border-radius: 999px;
  padding: 0.1rem 0.5rem;
  font-size: 0.75rem;
}
.pill.off {
  color: #9ca3af;
}
.delegation-form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.5rem 0.75rem;
  margin-bottom: 1rem;
}
.delegation-form label {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  font-size: 0.8rem;
}
.delegation-form label.wide {
  grid-column: 1 / -1;
}
.delegation-form input,
.delegation-form select {
  border: 1px solid #e5e7eb;
  border-radius: 0.4rem;
  padding: 0.35rem 0.5rem;
}
.small {
  font-size: 0.8rem;
}
.primary-button.small,
.ghost-button.small {
  padding: 0.35rem 0.75rem;
  font-size: 0.8rem;
}
.muted {
  color: #6b7280;
}
.card-preview {
  font-size: 0.75rem;
  background: #f9fafb;
  border: 1px solid #e5e7eb;
  border-radius: 0.4rem;
  padding: 0.5rem;
  max-height: 12rem;
  overflow: auto;
  margin: 0 0 1rem;
}
</style>
