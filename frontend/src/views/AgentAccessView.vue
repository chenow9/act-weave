<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";

import AppSelect, { type AppSelectOption } from "../components/AppSelect.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementSummaryStrip from "../components/ManagementSummaryStrip.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useModalFocus } from "../composables/useModalFocus";
import { useAgentAccessStore, type AgentAccessClient, type AgentAccessCredential, type AgentAccessGrant, type AgentAccessScope } from "../stores/agentAccess";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useWorkspaceStore } from "../stores/workspaces";

type DetailTab = "credentials" | "grants" | "configuration";
type DangerTarget =
  | { kind: "credential"; value: AgentAccessCredential }
  | { kind: "grant"; value: AgentAccessGrant }
  | { kind: "client"; value: AgentAccessClient };

const access = useAgentAccessStore();
const agents = useAgentStore();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const query = ref("");
const activeTab = ref<DetailTab>("credentials");
const actionMessage = ref("");
const actionError = ref("");
const createOpen = ref(false);
const rotateOpen = ref(false);
const grantOpen = ref(false);
const secretOpen = ref(false);
const dangerOpen = ref(false);
const createModalRef = ref<HTMLElement | null>(null);
const rotateModalRef = ref<HTMLElement | null>(null);
const grantModalRef = ref<HTMLElement | null>(null);
const secretModalRef = ref<HTMLElement | null>(null);
const dangerModalRef = ref<HTMLElement | null>(null);
const oneTimeSecret = ref("");
const oneTimeSecretContext = ref("");
const copyNotice = ref("");
const clientIdCopyNotice = ref("");
const dangerTarget = ref<DangerTarget | null>(null);
const confirmationPhrase = ref("");

const createForm = reactive({
  name: "", authMethod: "client_secret_basic" as "client_secret_basic" | "private_key_jwt",
  jwksUri: "", jwkThumbprint: "", publicHint: "", corsOrigins: "", tokenTtlSeconds: 600,
  trustedSubjectIssuer: "", trustedSubjectJwksUri: "",
});
const rotateForm = reactive({
  replacesCredentialId: "", overlapSeconds: 3600, jwkThumbprint: "", publicHint: "",
});
const grantForm = reactive({
  agentId: "", scopes: ["agent:read", "run:create", "run:read", "event:read"] as AgentAccessScope[],
  serviceDecision: false, maxRisk: "low" as "low" | "medium", expiresAt: "",
});

const workspaceId = computed(() => workspaces.activeWorkspaceId || workspaces.items[0]?.id || "");
const hasWorkspaceContext = computed(() => Boolean(workspaceId.value));
const currentUserId = computed(() => auth.user?.id || "");
const canManage = computed(() => Boolean(
  workspaceId.value && currentUserId.value && workspaces.can(workspaceId.value, currentUserId.value, "MANAGE"),
));
const selectedClient = computed(() => access.selectedClient);
const filteredClients = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  if (!needle) return access.clients;
  return access.clients.filter((client) =>
    [client.name, client.clientId, client.authMethod, client.status].some((value) => value.toLocaleLowerCase().includes(needle)),
  );
});
const activeCredentialOptions = computed(() => access.credentials.filter((credential) => !credential.revokedAt));
const activeAgentOptions = computed(() => agents.items.filter((agent) => agent.workspaceId === workspaceId.value && agent.status === "ACTIVE"));
const summaryItems = computed(() => [
  { label: "接入 Client", value: access.clients.length, icon: "fa-solid fa-id-card" },
  { label: "活动凭证", value: access.activeCredentials.length, icon: "fa-solid fa-key", tone: "info" as const },
  { label: "活动 Agent 授权", value: access.activeGrants.length, icon: "fa-solid fa-link", tone: "default" as const },
  { label: "需关注", value: access.clients.filter((client) => client.status === "DISABLED").length, icon: "fa-solid fa-shield-halved", tone: "warning" as const },
]);
const dangerTitle = computed(() => {
  if (dangerTarget.value?.kind === "client") return "禁用 Agent Access Client";
  if (dangerTarget.value?.kind === "grant") return "撤销 Agent 授权";
  return "撤销接入凭证";
});
const dangerDescription = computed(() => {
  if (dangerTarget.value?.kind === "client") return "现有 Token 将在安全版本校验或 SSE 重校验时失效，所有新请求会立即被拒绝。";
  if (dangerTarget.value?.kind === "grant") return "该业务平台将立即失去此 Agent 的全部已授予 Scope，活动事件流会被重新校验。";
  return "凭证撤销不可恢复。请先确认已有可用轮换凭证，避免业务平台中断。";
});

const scopeOptions: Array<{ value: AgentAccessScope; label: string }> = [
  { value: "agent:read", label: "读取 Agent" },
  { value: "conversation:create", label: "创建 Conversation" },
  { value: "conversation:read", label: "读取 Conversation" },
  { value: "run:create", label: "创建 Run" },
  { value: "run:read", label: "读取 Run" },
  { value: "run:cancel", label: "取消 Run" },
  { value: "event:read", label: "读取事件流" },
  { value: "interaction:decide", label: "处理审批交互" },
  { value: "artifact:read", label: "读取 Artifact" },
];

const authMethodOptions: AppSelectOption[] = [
  { label: "Client Secret Basic", value: "client_secret_basic" },
  { label: "Private Key JWT", value: "private_key_jwt" },
];

const maxRiskOptions: AppSelectOption[] = [
  { label: "LOW", value: "low" },
  { label: "MEDIUM", value: "medium" },
];

const rotateCredentialSelectOptions = computed<AppSelectOption[]>(() =>
  activeCredentialOptions.value.map((credential) => ({
    label: `${credential.publicHint} · v${credential.lockVersion}`,
    value: credential.id,
  })),
);

const agentSelectOptions = computed<AppSelectOption[]>(() =>
  activeAgentOptions.value.map((agent) => ({
    label: agent.name,
    value: agent.id,
  })),
);

const selectedAuthMethodLabel = computed(() =>
  selectedClient.value?.authMethod === "private_key_jwt" ? "Private Key JWT" : "Client Secret Basic",
);

useModalFocus({ visible: createOpen, modalRef: createModalRef, onClose: () => (createOpen.value = false) });
useModalFocus({ visible: rotateOpen, modalRef: rotateModalRef, onClose: () => (rotateOpen.value = false) });
useModalFocus({ visible: grantOpen, modalRef: grantModalRef, onClose: () => (grantOpen.value = false) });
useModalFocus({ visible: secretOpen, modalRef: secretModalRef, onClose: closeSecret });
useModalFocus({ visible: dangerOpen, modalRef: dangerModalRef, onClose: closeDanger });

onMounted(loadPage);
onBeforeUnmount(clearSecret);
watch(workspaceId, (next, previous) => {
  if (next && next !== previous) void loadPage();
});

async function loadPage() {
  clearFeedback();
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (!workspaceId.value) return;
    if (currentUserId.value) await workspaces.loadMemberRoles(currentUserId.value, [workspaces.requireWorkspace(workspaceId.value)]);
    await Promise.all([access.load(workspaceId.value), agents.loadAgents({ workspaceId: workspaceId.value })]);
  } catch (error) {
    actionError.value = messageFor(error, "Agent Access 配置加载失败，请稍后重试。");
  }
}

async function selectClient(client: AgentAccessClient) {
  clearFeedback();
  clientIdCopyNotice.value = "";
  await access.loadClientDetail(client.id);
}

function openCreate() {
  if (!canManage.value) return;
  Object.assign(createForm, {
    name: "", authMethod: "client_secret_basic", jwksUri: "", jwkThumbprint: "",
    publicHint: "", corsOrigins: "", tokenTtlSeconds: 600,
    trustedSubjectIssuer: "", trustedSubjectJwksUri: "",
  });
  createOpen.value = true;
}

async function createClient() {
  if (!canManage.value || !createForm.name.trim()) return;
  await runAction(async () => {
    const result = await access.createClient({
      name: createForm.name.trim(), authMethod: createForm.authMethod,
      jwksUri: createForm.authMethod === "private_key_jwt" ? createForm.jwksUri.trim() : undefined,
      jwkThumbprint: createForm.authMethod === "private_key_jwt" ? createForm.jwkThumbprint.trim() : undefined,
      credentialPublicHint: createForm.authMethod === "private_key_jwt" ? createForm.publicHint.trim() : undefined,
      trustedSubjectIssuer: createForm.trustedSubjectIssuer.trim() || undefined,
      trustedSubjectJwksUri: createForm.trustedSubjectJwksUri.trim() || undefined,
      allowedCorsOrigins: createForm.corsOrigins.split(/\r?\n/).map((value) => value.trim()).filter(Boolean),
      tokenTtlSeconds: createForm.tokenTtlSeconds,
    });
    createOpen.value = false;
    if (result.secret) showSecret(result.secret, `Client ${result.client.name}`);
    else actionMessage.value = "Private Key Client 已注册；ActWeave 未生成或保存私钥。";
  }, "Client 创建失败，请检查认证配置。 ");
}

function openRotate() {
  if (!canManage.value || !selectedClient.value) return;
  const replacement = activeCredentialOptions.value[0];
  Object.assign(rotateForm, {
    replacesCredentialId: replacement?.id || "", overlapSeconds: 3600,
    jwkThumbprint: "", publicHint: "",
  });
  rotateOpen.value = true;
}

async function rotateCredential() {
  const client = selectedClient.value;
  const replacement = access.credentials.find((item) => item.id === rotateForm.replacesCredentialId);
  if (!canManage.value || !client || !replacement) return;
  await runAction(async () => {
    const isSecret = client.authMethod === "client_secret_basic";
    const result = await access.rotateCredential(client.id, {
      type: isSecret ? "client_secret" : "jwk",
      replacesCredentialId: replacement.id, replacesLockVersion: replacement.lockVersion,
      overlapSeconds: Number(rotateForm.overlapSeconds),
      jwkThumbprint: isSecret ? undefined : rotateForm.jwkThumbprint.trim(),
      publicHint: isSecret ? undefined : rotateForm.publicHint.trim(),
    });
    rotateOpen.value = false;
    if (result.secret) showSecret(result.secret, `Client ${client.name} 的新凭证`);
    else actionMessage.value = "新 JWK 已登记；旧凭证将在轮换窗口结束时失效。";
  }, "凭证轮换失败，请刷新后重试。");
}

function openGrant() {
  if (!canManage.value || !selectedClient.value) return;
  Object.assign(grantForm, {
    agentId: activeAgentOptions.value[0]?.id || "",
    scopes: ["agent:read", "run:create", "run:read", "event:read"],
    serviceDecision: false, maxRisk: "low", expiresAt: "",
  });
  grantOpen.value = true;
}

async function createGrant() {
  const client = selectedClient.value;
  if (!canManage.value || !client || !grantForm.agentId || !grantForm.scopes.length) return;
  await runAction(async () => {
    await access.createGrant(client.id, {
      agentId: grantForm.agentId, scopes: [...grantForm.scopes],
      policy: grantForm.serviceDecision
        ? { serviceDecision: { enabled: true, maxRisk: grantForm.maxRisk } }
        : {},
      expiresAt: grantForm.expiresAt ? new Date(grantForm.expiresAt).toISOString() : undefined,
    });
    grantOpen.value = false;
    actionMessage.value = "Agent 授权已创建并写入审计日志。";
  }, "Agent 授权创建失败，请检查 Scope 或有效期是否冲突。");
}

async function enableClient(client: AgentAccessClient) {
  if (!canManage.value) return;
  await runAction(async () => {
    await access.setClientStatus(client, "ACTIVE");
    actionMessage.value = "Client 已启用。";
  }, "Client 启用失败；请确认至少存在一个活动凭证。");
}

function askDanger(target: DangerTarget) {
  if (!canManage.value) return;
  dangerTarget.value = target;
  confirmationPhrase.value = "";
  dangerOpen.value = true;
}

async function executeDanger() {
  const target = dangerTarget.value;
  const client = selectedClient.value;
  if (!canManage.value || !target || confirmationPhrase.value !== "REVOKE") return;
  await runAction(async () => {
    if (target.kind === "client") await access.setClientStatus(target.value, "DISABLED");
    else if (target.kind === "credential" && client) await access.revokeCredential(client.id, target.value);
    else if (target.kind === "grant" && client) await access.revokeGrant(client.id, target.value);
    closeDanger();
    actionMessage.value = "高风险变更已执行；安全版本已更新。";
  }, "撤销失败，请刷新资源版本后重试。");
}

function showSecret(secret: string, context: string) {
  oneTimeSecret.value = secret;
  oneTimeSecretContext.value = context;
  copyNotice.value = "";
  secretOpen.value = true;
}

async function copySecret() {
  if (!oneTimeSecret.value) return;
  await navigator.clipboard?.writeText(oneTimeSecret.value);
  copyNotice.value = "已复制。请立即保存到受控密码管理器，关闭窗口后无法再次查看。";
}

async function copyClientId() {
  const clientId = selectedClient.value?.clientId;
  if (!clientId) return;
  try {
    await navigator.clipboard?.writeText(clientId);
    clientIdCopyNotice.value = "已复制 Client ID";
    window.setTimeout(() => {
      if (clientIdCopyNotice.value === "已复制 Client ID") clientIdCopyNotice.value = "";
    }, 2000);
  } catch {
    clientIdCopyNotice.value = "复制失败";
  }
}

function clearSecret() {
  oneTimeSecret.value = "";
  oneTimeSecretContext.value = "";
  copyNotice.value = "";
}

function closeSecret() {
  secretOpen.value = false;
  clearSecret();
}

function closeDanger() {
  dangerOpen.value = false;
  dangerTarget.value = null;
  confirmationPhrase.value = "";
}

async function runAction(action: () => Promise<void>, fallback: string) {
  clearFeedback();
  try {
    await action();
  } catch (error) {
    actionError.value = messageFor(error, fallback);
  }
}

function clearFeedback() {
  actionMessage.value = "";
  actionError.value = "";
}

function messageFor(error: unknown, fallback: string) {
  return error instanceof Error && error.message ? error.message : fallback;
}

function agentName(agentId: string) {
  return agents.items.find((agent) => agent.id === agentId)?.name || agentId;
}

function formatTime(value?: string) {
  if (!value) return "从未";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN");
}

function shortID(value: string) {
  return value.length > 20 ? `${value.slice(0, 12)}…${value.slice(-6)}` : value;
}

function authMethodShort(method: string) {
  return method === "private_key_jwt" ? "PK JWT" : "Secret";
}
</script>

<template>
  <div class="agent-access-page">
    <ManagementPageHeader
      title="Agent Access"
      description="为第三方 Web / App 注册 Workspace 级 Client，管理凭证轮换和 Agent 数据面授权。"
      icon="fa-solid fa-shield-halved"
      eyebrow="ACTWEAVE ACCESS CONTROL"
    >
      <template #actions>
        <button class="ghost-button" type="button" :disabled="access.loading" @click="loadPage">
          <i class="fa-solid fa-rotate-right" />刷新
        </button>
        <button v-if="canManage" data-testid="create-client" class="primary-button" type="button" @click="openCreate">
          <i class="fa-solid fa-plus" />注册 Client
        </button>
      </template>
    </ManagementPageHeader>

    <WorkspaceContextState v-if="!hasWorkspaceContext" feature="Agent Access" @retry="loadPage" />
    <template v-else>
      <p v-if="!canManage" data-testid="readonly-notice" class="readonly-notice">
        <i class="fa-solid fa-eye" />当前 Workspace 角色仅可查看接入配置；创建、轮换和撤销操作需要 OWNER 或 ADMIN。
      </p>
      <p v-if="actionMessage" class="action-feedback success" role="status">{{ actionMessage }}</p>
      <p v-if="actionError || access.error" class="action-feedback error" role="alert">{{ actionError || access.error }}</p>
      <ManagementSummaryStrip :items="summaryItems" />

      <section class="access-workbench" :aria-busy="access.loading">
        <aside class="client-rail">
          <label class="client-search">
            <i class="fa-solid fa-magnifying-glass" />
            <input v-model="query" type="search" placeholder="搜索 Client ID 或名称" aria-label="搜索 Agent Access Client" />
          </label>
          <div v-if="access.loading && !access.hasLoaded" class="empty-state">正在加载接入配置…</div>
          <div v-else-if="!filteredClients.length" class="empty-state">
            <i class="fa-solid fa-shield" />
            <strong>尚未注册 Client</strong>
            <span>注册后即可为第三方平台签发独立凭证和 Agent Scope。</span>
          </div>
          <button
            v-for="client in filteredClients"
            :key="client.id"
            class="client-card"
            :class="{ selected: client.id === access.selectedClientId }"
            type="button"
            @click="selectClient(client)"
          >
            <span class="client-card-icon"><i :class="client.authMethod === 'private_key_jwt' ? 'fa-solid fa-fingerprint' : 'fa-solid fa-key'" /></span>
            <span class="client-card-copy">
              <b>{{ client.name }}</b>
              <small>{{ authMethodShort(client.authMethod) }} · {{ shortID(client.clientId) }}</small>
            </span>
            <span class="status-pill" :class="client.status.toLocaleLowerCase()">{{ client.status === "ACTIVE" ? "活动" : "已禁用" }}</span>
          </button>
        </aside>

        <main v-if="selectedClient" class="client-detail" :aria-busy="access.detailLoading">
          <header class="detail-header">
            <div class="detail-title-block">
              <div class="detail-title-row">
                <h2>{{ selectedClient.name }}</h2>
                <span class="status-pill" :class="selectedClient.status.toLocaleLowerCase()">
                  {{ selectedClient.status === "ACTIVE" ? "活动" : "已禁用" }}
                </span>
              </div>
              <div class="client-id-row">
                <span class="client-id-label">Client ID</span>
                <code class="client-id-value" :title="selectedClient.clientId">{{ selectedClient.clientId }}</code>
                <button class="copy-id-button" type="button" aria-label="复制 Client ID" @click="copyClientId">
                  <i class="fa-regular fa-copy" />
                </button>
                <span v-if="clientIdCopyNotice" class="copy-id-notice" role="status">{{ clientIdCopyNotice }}</span>
              </div>
            </div>
            <div v-if="canManage" class="detail-actions">
              <button v-if="selectedClient.status === 'DISABLED'" class="ghost-button" type="button" @click="enableClient(selectedClient)">启用</button>
              <button v-else class="danger-button" type="button" @click="askDanger({ kind: 'client', value: selectedClient })">禁用 Client</button>
            </div>
          </header>

          <div class="identity-chips" aria-label="Client 摘要">
            <div class="identity-chip">
              <span>认证方式</span>
              <strong>{{ selectedAuthMethodLabel }}</strong>
            </div>
            <div class="identity-chip">
              <span>Token TTL</span>
              <strong>{{ selectedClient.tokenTtlSeconds }} 秒</strong>
            </div>
            <div class="identity-chip">
              <span>Service Principal</span>
              <strong :title="selectedClient.servicePrincipalId">{{ shortID(selectedClient.servicePrincipalId) }}</strong>
            </div>
            <div class="identity-chip">
              <span>最近更新</span>
              <strong>{{ formatTime(selectedClient.updatedAt) }}</strong>
            </div>
          </div>

          <nav class="detail-tabs" aria-label="Agent Access 详情">
            <button type="button" :class="{ active: activeTab === 'credentials' }" @click="activeTab = 'credentials'">
              凭证 <span>{{ access.credentials.length }}</span>
            </button>
            <button type="button" :class="{ active: activeTab === 'grants' }" @click="activeTab = 'grants'">
              Agent 授权 <span>{{ access.grants.length }}</span>
            </button>
            <button type="button" :class="{ active: activeTab === 'configuration' }" @click="activeTab = 'configuration'">
              接入配置
            </button>
          </nav>

          <section v-if="activeTab === 'credentials'" class="detail-section">
            <div class="section-heading">
              <div>
                <h3>凭证生命周期</h3>
                <p>只展示公开 Hint 和使用时间；认证材料不可恢复。</p>
              </div>
              <button v-if="canManage" class="primary-button compact" type="button" @click="openRotate">
                <i class="fa-solid fa-rotate" />轮换凭证
              </button>
            </div>
            <article v-for="credential in access.credentials" :key="credential.id" class="resource-card">
              <span class="resource-icon"><i :class="credential.type === 'jwk' ? 'fa-solid fa-fingerprint' : 'fa-solid fa-key'" /></span>
              <div class="resource-main">
                <strong>{{ credential.type === "jwk" ? "JWK" : "Client Secret" }} · {{ credential.publicHint }}</strong>
                <span>创建 {{ formatTime(credential.createdAt) }} · 最后使用 {{ formatTime(credential.lastUsedAt) }}</span>
                <small v-if="credential.expiresAt">有效至 {{ formatTime(credential.expiresAt) }}</small>
              </div>
              <div class="resource-actions">
                <span class="status-pill" :class="credential.revokedAt ? 'revoked' : 'active'">{{ credential.revokedAt ? "已撤销" : "活动" }}</span>
                <button v-if="canManage && !credential.revokedAt" class="text-danger" type="button" @click="askDanger({ kind: 'credential', value: credential })">撤销</button>
              </div>
            </article>
            <div v-if="!access.credentials.length" class="inline-empty">
              <strong>暂无凭证</strong>
              <span>轮换或创建 Client 后会在这里展示公开 Hint。</span>
            </div>
          </section>

          <section v-else-if="activeTab === 'grants'" class="detail-section">
            <div class="section-heading">
              <div>
                <h3>Agent 数据面授权</h3>
                <p>每个 Grant 只包含受控数据面 Scope，不授予 Workspace 管理能力。</p>
              </div>
              <button v-if="canManage" class="primary-button compact" type="button" @click="openGrant">
                <i class="fa-solid fa-link" />授权 Agent
              </button>
            </div>
            <article v-for="grant in access.grants" :key="grant.id" class="resource-card">
              <span class="resource-icon"><i class="fa-solid fa-robot" /></span>
              <div class="resource-main">
                <strong>{{ agentName(grant.agentId) }}</strong>
                <div class="scope-list"><code v-for="scope in grant.scopes" :key="scope">{{ scope }}</code></div>
                <small>有效期：{{ formatTime(grant.validFrom) }} → {{ grant.expiresAt ? formatTime(grant.expiresAt) : "长期" }}</small>
              </div>
              <div class="resource-actions">
                <span class="status-pill" :class="grant.status.toLocaleLowerCase()">{{ grant.status === "ACTIVE" ? "活动" : "已撤销" }}</span>
                <button v-if="canManage && grant.status === 'ACTIVE'" class="text-danger" type="button" @click="askDanger({ kind: 'grant', value: grant })">撤销</button>
              </div>
            </article>
            <div v-if="!access.grants.length" class="inline-empty">
              <strong>尚未授权 Agent</strong>
              <span>为业务平台创建最小权限 Grant，绑定可调用的 Agent 与 Scope。</span>
            </div>
          </section>

          <section v-else class="detail-section configuration-section">
            <div class="section-heading">
              <div>
                <h3>接入与信任配置</h3>
                <p>这些值参与浏览器接入、JWT 验证和外部 Subject 信任边界。</p>
              </div>
            </div>
            <dl class="config-list">
              <div><dt>JWKS URI</dt><dd>{{ selectedClient.jwksUri || "不适用（Secret 认证）" }}</dd></div>
              <div><dt>Trusted Subject Issuer</dt><dd>{{ selectedClient.trustedSubjectIssuer || "未启用 Token Exchange" }}</dd></div>
              <div><dt>Trusted Subject JWKS</dt><dd>{{ selectedClient.trustedSubjectJwksUri || "—" }}</dd></div>
              <div>
                <dt>CORS Origins</dt>
                <dd>
                  <code v-for="origin in selectedClient.allowedCorsOrigins" :key="origin">{{ origin }}</code>
                  <span v-if="!selectedClient.allowedCorsOrigins.length">仅服务端/BFF 接入</span>
                </dd>
              </div>
            </dl>
            <p class="config-note">
              <i class="fa-solid fa-circle-info" />
              认证方式和信任根是安全边界。v1 通过注册新 Client 变更，不在原 Client 上静默替换。
            </p>
          </section>
        </main>
        <main v-else class="client-detail empty-detail">
          <i class="fa-solid fa-arrow-left" />
          <strong>选择一个 Client 查看接入详情</strong>
          <span>或点击右上角「注册 Client」开始接入第三方平台。</span>
        </main>
      </section>
    </template>

    <div v-if="createOpen" class="modal-backdrop" @click.self="createOpen = false">
      <section ref="createModalRef" class="modal-card access-modal" role="dialog" aria-modal="true" aria-label="注册 Agent Access Client" tabindex="-1">
        <div class="modal-card-head">
          <div>
            <span>注册外部 Client</span>
            <h3>注册 Agent Access Client</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭" @click="createOpen = false"><i class="fa-solid fa-xmark" /></button>
        </div>
        <div class="modal-form access-modal-form form-grid">
          <label class="span-2"><span>Client 名称</span><input v-model="createForm.name" data-modal-initial-focus placeholder="例如：会员运营 App" /></label>
          <label>
            <span>认证方式</span>
            <AppSelect v-model="createForm.authMethod" :options="authMethodOptions" aria-label="认证方式" />
          </label>
          <label><span>Token TTL（秒）</span><input v-model.number="createForm.tokenTtlSeconds" type="number" min="60" max="900" /></label>
          <template v-if="createForm.authMethod === 'private_key_jwt'">
            <label class="span-2"><span>JWKS URI</span><input v-model="createForm.jwksUri" placeholder="https://platform.example/.well-known/jwks.json" /></label>
            <label><span>JWK Thumbprint（Base64URL）</span><input v-model="createForm.jwkThumbprint" /></label>
            <label><span>公开 Hint / kid</span><input v-model="createForm.publicHint" placeholder="kid-prod-2026-01" /></label>
          </template>
          <label class="span-2"><span>CORS Origins（每行一个精确 HTTPS Origin）</span><textarea v-model="createForm.corsOrigins" rows="3" placeholder="https://app.example.com" /></label>
          <label><span>Trusted Subject Issuer（可选）</span><input v-model="createForm.trustedSubjectIssuer" placeholder="https://identity.example.com" /></label>
          <label><span>Subject JWKS URI（可选）</span><input v-model="createForm.trustedSubjectJwksUri" placeholder="https://identity.example.com/jwks" /></label>
        </div>
        <div class="access-modal-footer">
          <button class="ghost-button" type="button" @click="createOpen = false">取消</button>
          <button class="primary-button" type="button" :disabled="access.mutating || !createForm.name.trim()" @click="createClient">
            {{ access.mutating ? "创建中…" : "创建 Client" }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="rotateOpen" class="modal-backdrop" @click.self="rotateOpen = false">
      <section ref="rotateModalRef" class="modal-card access-modal compact-modal" role="dialog" aria-modal="true" aria-label="轮换 Agent Access 凭证" tabindex="-1">
        <div class="modal-card-head">
          <div>
            <span>安全轮换</span>
            <h3>轮换 Credential</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭" @click="rotateOpen = false"><i class="fa-solid fa-xmark" /></button>
        </div>
        <div class="modal-form access-modal-form form-grid">
          <label class="span-2">
            <span>被替换凭证</span>
            <AppSelect
              v-model="rotateForm.replacesCredentialId"
              :options="rotateCredentialSelectOptions"
              aria-label="被替换凭证"
            />
          </label>
          <label class="span-2">
            <span>并存窗口（秒，最多 86400）</span>
            <input v-model.number="rotateForm.overlapSeconds" type="number" min="1" max="86400" />
            <small>窗口结束前请完成业务平台切换。</small>
          </label>
          <template v-if="selectedClient?.authMethod === 'private_key_jwt'">
            <label><span>新 JWK Thumbprint</span><input v-model="rotateForm.jwkThumbprint" /></label>
            <label><span>新公开 Hint / kid</span><input v-model="rotateForm.publicHint" /></label>
          </template>
        </div>
        <div class="access-modal-footer">
          <button class="ghost-button" type="button" @click="rotateOpen = false">取消</button>
          <button class="primary-button" type="button" :disabled="access.mutating || !rotateForm.replacesCredentialId" @click="rotateCredential">
            开始安全轮换
          </button>
        </div>
      </section>
    </div>

    <div v-if="grantOpen" class="modal-backdrop" @click.self="grantOpen = false">
      <section ref="grantModalRef" class="modal-card access-modal" role="dialog" aria-modal="true" aria-label="授权 Agent" tabindex="-1">
        <div class="modal-card-head">
          <div>
            <span>最小权限</span>
            <h3>授权 Agent 数据面能力</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭" @click="grantOpen = false"><i class="fa-solid fa-xmark" /></button>
        </div>
        <div class="modal-form access-modal-form">
          <label>
            <span>Agent</span>
            <AppSelect v-model="grantForm.agentId" :options="agentSelectOptions" aria-label="授权 Agent" filterable />
          </label>
          <fieldset class="scope-grid">
            <legend>数据面 Scope</legend>
            <label v-for="scope in scopeOptions" :key="scope.value">
              <input v-model="grantForm.scopes" type="checkbox" :value="scope.value" />
              <span><b>{{ scope.label }}</b><code>{{ scope.value }}</code></span>
            </label>
          </fieldset>
          <label class="decision-toggle">
            <input v-model="grantForm.serviceDecision" type="checkbox" />
            <span>
              <b>允许纯 Service Principal 处理低风险审批</b>
              <small>仅适用于 Agent Policy 同时允许的 LOW / MEDIUM 风险。</small>
            </span>
          </label>
          <label v-if="grantForm.serviceDecision">
            <span>最高风险</span>
            <AppSelect v-model="grantForm.maxRisk" :options="maxRiskOptions" aria-label="最高风险" />
          </label>
          <label><span>到期时间（可选）</span><input v-model="grantForm.expiresAt" type="datetime-local" /></label>
        </div>
        <div class="access-modal-footer">
          <button class="ghost-button" type="button" @click="grantOpen = false">取消</button>
          <button class="primary-button" type="button" :disabled="access.mutating || !grantForm.agentId || !grantForm.scopes.length" @click="createGrant">
            创建最小权限 Grant
          </button>
        </div>
      </section>
    </div>

    <div v-if="secretOpen" class="modal-backdrop critical" @click.self="closeSecret">
      <section ref="secretModalRef" class="modal-card access-modal secret-modal" role="dialog" aria-modal="true" aria-label="一次性 Client Secret" tabindex="-1">
        <div class="modal-card-head">
          <div>
            <span>一次性密钥</span>
            <h3>立即保存接入密钥</h3>
          </div>
        </div>
        <div class="modal-form access-modal-form">
          <p class="secret-warning">
            <i class="fa-solid fa-triangle-exclamation" />
            这是 {{ oneTimeSecretContext }} 的唯一明文展示。ActWeave 不保存明文，关闭后无法恢复。
          </p>
          <code data-testid="one-time-secret" class="secret-value">{{ oneTimeSecret }}</code>
          <button class="copy-secret" type="button" data-modal-initial-focus @click="copySecret">
            <i class="fa-regular fa-copy" />复制到剪贴板
          </button>
          <p v-if="copyNotice" class="copy-notice" role="status">{{ copyNotice }}</p>
        </div>
        <div class="access-modal-footer">
          <button class="primary-button" type="button" @click="closeSecret">我已安全保存，关闭</button>
        </div>
      </section>
    </div>

    <div v-if="dangerOpen" class="modal-backdrop critical" @click.self="closeDanger">
      <section ref="dangerModalRef" class="modal-card access-modal danger-modal" role="alertdialog" aria-modal="true" :aria-label="dangerTitle" tabindex="-1">
        <div class="modal-card-head">
          <div>
            <span>高风险变更</span>
            <h3>{{ dangerTitle }}</h3>
          </div>
          <button class="icon-action-button" type="button" aria-label="关闭" @click="closeDanger"><i class="fa-solid fa-xmark" /></button>
        </div>
        <div class="modal-form access-modal-form">
          <p class="danger-copy">{{ dangerDescription }}</p>
          <label>
            <span>输入 <code>REVOKE</code> 进行二次确认</span>
            <input v-model="confirmationPhrase" data-modal-initial-focus autocomplete="off" />
          </label>
        </div>
        <div class="access-modal-footer">
          <button class="ghost-button" type="button" @click="closeDanger">取消</button>
          <button
            data-testid="confirm-danger"
            class="danger-button solid"
            type="button"
            :disabled="access.mutating || confirmationPhrase !== 'REVOKE'"
            @click="executeDanger"
          >
            确认执行不可恢复变更
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.agent-access-page {
  display: grid;
  gap: 16px;
  min-width: 0;
}

.readonly-notice,
.action-feedback {
  margin: 0;
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #f8fafc;
  color: #475569;
  font-size: 13px;
  line-height: 1.5;
}

.readonly-notice i {
  margin-right: 8px;
  color: #2563eb;
}

.action-feedback.success {
  border-color: #a7f3d0;
  background: #ecfdf5;
  color: #047857;
}

.action-feedback.error {
  border-color: #fecdd3;
  background: #fff1f2;
  color: #be123c;
}

.access-workbench {
  display: grid;
  grid-template-columns: minmax(260px, 300px) minmax(0, 1fr);
  min-height: min(640px, calc(100vh - 220px));
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 10px 30px -18px rgba(15, 23, 42, 0.12);
}

.client-rail {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
  padding: 14px;
  overflow: auto;
  border-right: 1px solid #eef2f7;
  background: #f8fafc;
}

.client-search {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
  padding: 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #94a3b8;
}

.client-search input {
  width: 100%;
  height: 38px;
  border: 0;
  outline: 0;
  background: transparent;
  color: #0f172a;
  font-size: 13px;
}

.client-card {
  display: grid;
  width: 100%;
  grid-template-columns: 36px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  padding: 11px;
  border: 1px solid transparent;
  border-radius: 12px;
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.client-card:hover {
  border-color: #e2e8f0;
  background: #fff;
}

.client-card.selected {
  border-color: #a7f3d0;
  background: #fff;
  box-shadow: 0 4px 16px rgba(15, 159, 110, 0.08);
}

.client-card-icon,
.resource-icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 10px;
  background: #ecfdf5;
  color: #047857;
  font-size: 13px;
}

.client-card-copy {
  min-width: 0;
}

.client-card-copy b,
.client-card-copy small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-card-copy b {
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
}

.client-card-copy small {
  margin-top: 3px;
  color: #64748b;
  font-size: 11px;
  font-weight: 500;
}

.status-pill {
  display: inline-flex;
  align-items: center;
  min-height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  background: #f1f5f9;
  color: #475569;
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}

.status-pill.active {
  background: #ecfdf5;
  color: #047857;
}

.status-pill.disabled,
.status-pill.revoked {
  background: #fff1f2;
  color: #be123c;
}

.client-detail {
  min-width: 0;
  min-height: 0;
  padding: 22px 24px 28px;
  overflow: auto;
}

.detail-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-start;
}

.detail-title-block {
  min-width: 0;
  flex: 1 1 auto;
}

.detail-title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.detail-title-row h2 {
  margin: 0;
  color: #0f172a;
  font-size: 22px;
  font-weight: 750;
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.client-id-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  min-width: 0;
}

.client-id-label {
  flex: 0 0 auto;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.client-id-value {
  min-width: 0;
  max-width: min(100%, 520px);
  overflow: hidden;
  color: #334155;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.copy-id-button {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 28px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #94a3b8;
  cursor: pointer;
}

.copy-id-button:hover,
.copy-id-button:focus-visible {
  background: #ecfdf5;
  color: #047857;
}

.copy-id-notice {
  color: #047857;
  font-size: 11px;
  font-weight: 650;
}

.detail-actions,
.section-heading,
.resource-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.identity-chips {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin: 18px 0 8px;
}

.identity-chip {
  min-width: 0;
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #f8fafc;
}

.identity-chip span {
  display: block;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 700;
}

.identity-chip strong {
  display: block;
  margin-top: 6px;
  overflow: hidden;
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-tabs {
  display: flex;
  gap: 4px;
  margin-top: 8px;
  border-bottom: 1px solid #e2e8f0;
}

.detail-tabs button {
  padding: 12px 14px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: #64748b;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.detail-tabs button.active {
  border-color: #0f9f6e;
  color: #047857;
}

.detail-tabs span {
  margin-left: 6px;
  padding: 1px 7px;
  border-radius: 999px;
  background: #f1f5f9;
  color: #475569;
  font-size: 11px;
}

.detail-section {
  padding-top: 18px;
}

.section-heading {
  justify-content: space-between;
  margin-bottom: 12px;
}

.section-heading h3 {
  margin: 0;
  color: #0f172a;
  font-size: 15px;
  font-weight: 750;
}

.section-heading p {
  margin: 4px 0 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}

.resource-card {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  padding: 14px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
}

.resource-main {
  min-width: 0;
}

.resource-main strong,
.resource-main span,
.resource-main small {
  display: block;
}

.resource-main strong {
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
}

.resource-main span,
.resource-main small {
  margin-top: 4px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.45;
}

.resource-actions {
  flex: 0 0 auto;
  justify-content: flex-end;
}

.scope-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.scope-list code,
.config-list dd code {
  padding: 3px 8px;
  border-radius: 6px;
  background: #f1f5f9;
  color: #0f766e;
  font-size: 11px;
}

.text-danger {
  border: 0;
  background: transparent;
  color: #be123c;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.inline-empty,
.empty-state,
.empty-detail {
  display: grid;
  place-items: center;
  gap: 8px;
  padding: 40px 16px;
  color: #64748b;
  font-size: 13px;
  text-align: center;
}

.inline-empty strong,
.empty-state strong,
.empty-detail strong {
  color: #334155;
  font-size: 14px;
}

.empty-state i,
.empty-detail i {
  font-size: 22px;
  color: #94a3b8;
}

.config-list {
  display: grid;
  grid-template-columns: 1fr 1fr;
  margin: 0;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
}

.config-list div {
  min-width: 0;
  padding: 14px 16px;
  border-bottom: 1px solid #e2e8f0;
}

.config-list div:nth-child(odd) {
  border-right: 1px solid #e2e8f0;
}

.config-list dt {
  color: #94a3b8;
  font-size: 11px;
  font-weight: 700;
}

.config-list dd {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 6px 0 0;
  color: #334155;
  font-size: 13px;
  overflow-wrap: anywhere;
}

.config-note {
  margin: 12px 0 0;
  padding: 12px 14px;
  border-radius: 10px;
  background: #f8fafc;
  color: #475569;
  font-size: 12px;
  line-height: 1.55;
}

.config-note i {
  margin-right: 6px;
  color: #2563eb;
}

.compact {
  min-height: 34px !important;
  padding: 0 12px !important;
  font-size: 12px !important;
}

.danger-button {
  min-height: 36px;
  padding: 0 14px;
  border: 1px solid #fecdd3;
  border-radius: 10px;
  background: #fff;
  color: #be123c;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.danger-button.solid {
  border-color: #e11d48;
  background: #e11d48;
  color: #fff;
}

.danger-button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.access-modal {
  width: min(680px, 100%);
  max-height: calc(100vh - 48px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.access-modal.compact-modal,
.access-modal.danger-modal,
.access-modal.secret-modal {
  width: min(520px, 100%);
}

.access-modal-form {
  min-height: 0;
  overflow: auto;
}

.access-modal-form.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.access-modal-form.form-grid .span-2 {
  grid-column: span 2;
}

.access-modal-form > label,
.access-modal-form .form-grid > label {
  display: grid;
  gap: 6px;
}

.access-modal-form label > span,
.scope-grid legend {
  color: #64748b;
  font-size: 12px;
  font-weight: 700;
}

.access-modal-form input,
.access-modal-form textarea {
  width: 100%;
  box-sizing: border-box;
  min-height: 40px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  color: #0f172a;
  font: inherit;
  font-size: 13px;
  outline: 0;
}

.access-modal-form textarea {
  min-height: 84px;
  resize: vertical;
}

.access-modal-form input:focus,
.access-modal-form textarea:focus {
  border-color: rgba(15, 159, 110, 0.45);
  box-shadow: 0 0 0 3px rgba(15, 159, 110, 0.1);
}

.access-modal-form label small {
  color: #94a3b8;
  font-size: 11px;
  line-height: 1.45;
}

.access-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 14px 18px 16px;
  border-top: 1px solid #eef2f7;
}

.scope-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
  padding: 12px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #f8fafc;
}

.scope-grid label,
.decision-toggle {
  display: flex !important;
  align-items: flex-start;
  gap: 8px;
  padding: 10px;
  border-radius: 10px;
  background: #fff;
  border: 1px solid #eef2f7;
}

.scope-grid input,
.decision-toggle input {
  width: auto !important;
  min-height: auto !important;
  margin-top: 2px;
}

.scope-grid b,
.decision-toggle b {
  display: block;
  color: #0f172a;
  font-size: 12px;
  font-weight: 700;
}

.scope-grid code,
.decision-toggle small {
  display: block;
  margin-top: 3px;
  color: #64748b;
  font-size: 11px;
}

.secret-warning,
.danger-copy {
  margin: 0;
  padding: 12px 14px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.55;
}

.secret-warning {
  border: 1px solid #fde68a;
  background: #fffbeb;
  color: #92400e;
}

.danger-copy {
  border: 1px solid #fecdd3;
  background: #fff1f2;
  color: #9f1239;
}

.secret-value {
  display: block;
  padding: 14px;
  border-radius: 10px;
  background: #0f172a;
  color: #a7f3d0;
  font-size: 12px;
  line-height: 1.65;
  overflow-wrap: anywhere;
  user-select: all;
}

.copy-secret {
  justify-self: start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 9px;
  background: #fff;
  color: #334155;
  font-size: 12px;
  cursor: pointer;
}

.copy-notice {
  margin: 0;
  color: #047857;
  font-size: 12px;
}

.modal-backdrop.critical {
  background: rgba(31, 12, 16, 0.5);
}

@media (max-width: 960px) {
  .access-workbench {
    grid-template-columns: 1fr;
  }

  .client-rail {
    max-height: 240px;
    border-right: 0;
    border-bottom: 1px solid #eef2f7;
  }

  .identity-chips {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 640px) {
  .client-detail {
    padding: 16px;
  }

  .detail-header,
  .section-heading {
    flex-direction: column;
    align-items: flex-start;
  }

  .identity-chips,
  .config-list,
  .access-modal-form.form-grid,
  .scope-grid {
    grid-template-columns: 1fr;
  }

  .config-list div:nth-child(odd),
  .access-modal-form.form-grid .span-2 {
    border-right: 0;
    grid-column: auto;
  }

  .resource-card {
    grid-template-columns: 36px minmax(0, 1fr);
  }

  .resource-actions {
    grid-column: 2 / -1;
    justify-content: flex-start;
  }
}
</style>
