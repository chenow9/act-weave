<script setup lang="ts">
import "./workspaces-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useModelConfigStore } from "../stores/modelConfigs";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  Agent,
  SortOrder,
  Workspace,
  WorkspaceListQuery,
  WorkspaceMemberCandidate,
  WorkspaceRole,
} from "../types/domain";

type WorkspaceStatusFilter = "ALL" | Workspace["status"];
type WorkspaceModeFilter = "ALL" | Workspace["mode"];
type WorkspaceDetailTab = "overview" | "members" | "agents";
const router = useRouter();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const agents = useAgentStore();
const modelConfigs = useModelConfigStore();

const query = ref("");
const statusFilter = ref<WorkspaceStatusFilter>("ALL");
const modeFilter = ref<WorkspaceModeFilter>("ALL");
const showWorkspaceModal = ref(false);
const workspaceModalMode = ref<"create" | "edit">("create");
const detailWorkspaceId = ref("");
const workspaceDetailTab = ref<WorkspaceDetailTab>("overview");
const draftWorkspace = ref<Workspace>(newWorkspace());
const workspaceActionNote = ref("");
const workspaceDeleteTarget = ref<Workspace | null>(null);
const workspaceDeleteConfirmName = ref("");
const workspaceStatusTarget = ref<Workspace | null>(null);
const workspaceDetailFilteredOut = ref(false);
const workspaceFormTouched = ref(false);
const workspaceActionTone = ref<"success" | "error">("success");
const workspaceModalRef = ref<HTMLElement | null>(null);
const workspaceStatusModalRef = ref<HTMLElement | null>(null);
const workspaceDeleteModalRef = ref<HTMLElement | null>(null);
const workspaceNameInputRef = ref<HTMLInputElement | null>(null);
const workspaceDeleteInputRef = ref<HTMLInputElement | null>(null);
const workspaceDetailPageRef = ref<HTMLElement | null>(null);
const lastFocusBeforeModal = ref<HTMLElement | null>(null);
const workspaceSaving = ref(false);
const workspaceStatusSaving = ref(false);
const workspaceDeleteSaving = ref(false);
const pageInitialLoading = ref(true);
const workspacePageError = ref("");
const workspaceToastTimer = ref<ReturnType<typeof window.setTimeout> | null>(null);
const selectedWorkspaceIds = ref<Set<string>>(new Set());
const newMemberUserId = ref("");
const newMemberRole = ref<WorkspaceRole>("VIEWER");
const memberCandidateQuery = ref("");
const memberCandidates = ref<WorkspaceMemberCandidate[]>([]);
const memberCandidatesLoading = ref(false);
const memberCandidatesError = ref("");
const workspaceNamePattern = /^[A-Za-z][A-Za-z0-9_-]{2,31}$/;

const modeOptions = [
  { label: "Production", value: "Production" },
  { label: "Sandbox", value: "Sandbox" },
];
const workspaceRoleLabels: Record<WorkspaceRole, string> = {
  OWNER: "所有者",
  ADMIN: "管理员",
  EDITOR: "编辑者",
  OPERATOR: "操作员",
  VIEWER: "查看者",
};
const assignableWorkspaceRoleOptions = (["ADMIN", "EDITOR", "OPERATOR", "VIEWER"] as WorkspaceRole[]).map((role) => ({
  label: workspaceRoleLabels[role],
  value: role,
}));
const workspaceRoleOptions = (["OWNER", "ADMIN", "EDITOR", "OPERATOR", "VIEWER"] as WorkspaceRole[]).map((role) => ({
  label: workspaceRoleLabels[role],
  value: role,
}));
const memberCandidateOptions = computed(() =>
  memberCandidates.value.map((candidate) => ({
    label: `${candidate.displayName} (@${candidate.username}) · ${candidate.platformRole}`,
    value: candidate.userId,
  })),
);

const visibleWorkspaceIds = computed(() => workspaces.pageItems.map((workspace) => workspace.id));
const checkedWorkspaceKeys = computed(() => Array.from(selectedWorkspaceIds.value));
const allPageWorkspacesSelected = computed(
  () =>
    visibleWorkspaceIds.value.length > 0 &&
    visibleWorkspaceIds.value.every((workspaceId) => selectedWorkspaceIds.value.has(workspaceId)),
);
const _somePageWorkspacesSelected = computed(
  () =>
    visibleWorkspaceIds.value.some((workspaceId) => selectedWorkspaceIds.value.has(workspaceId)) &&
    !allPageWorkspacesSelected.value,
);

function setCheckedWorkspaceKeys(keys: Array<string | number>) {
  selectedWorkspaceIds.value = new Set(keys.map(String));
}
const workspaceColumns = computed<ManagementListColumn<Workspace>[]>(() => [
  { key: "selection", label: "选择", width: 48, headerAlign: "center" },
  {
    key: "identity",
    label: "业务空间",
    width: 240,
    sortable: true,
    sortKey: "name",
    getValue: (workspace) => `${workspace.name} ${workspace.displayName}`,
  },
  {
    key: "mode",
    label: "环境",
    width: 124,
    hidable: true,
    sortable: true,
    sortKey: "mode",
    getValue: (workspace) => workspace.mode,
  },
  { key: "defaultAgent", label: "默认 Agent", width: 180, hidable: true, getValue: getDefaultAgentLabel },
  {
    key: "model",
    label: "模型配置",
    width: 210,
    hidable: true,
    getValue: (workspace) => getModelConfigLabel(workspace.modelConfigId),
  },
  {
    key: "status",
    label: "状态",
    width: 124,
    hidable: true,
    sortable: true,
    sortKey: "status",
    getValue: (workspace) => workspace.status,
  },
  {
    key: "updatedAt",
    label: "最近修改",
    width: 130,
    hidable: true,
    sortable: true,
    sortKey: "updatedAt",
    getValue: formatWorkspaceUpdatedAt,
  },
  {
    key: "createdBy",
    label: "创建者",
    width: 180,
    hidable: true,
    defaultHidden: true,
    sortable: true,
    sortKey: "createdBy",
    getValue: (workspace) => workspaceActorLabel(workspace, "created"),
  },
  {
    key: "updatedBy",
    label: "修改者",
    width: 180,
    hidable: true,
    defaultHidden: true,
    sortable: true,
    sortKey: "updatedBy",
    getValue: (workspace) => workspaceActorLabel(workspace, "updated"),
  },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
]);

const hasWorkspaceRecords = computed(() => workspaces.summary.total > 0 || workspaces.pageItems.length > 0);
const workspaceSummaryItems = computed<ManagementSummaryItem[]>(() => {
  // D9-A: cards use full accessible-set summary from the server, not the current page.
  const total = workspaces.summary.total;
  const active = workspaces.summary.active;
  const production = workspaces.summary.production;
  const boundAgents = workspaces.summary.boundAgents;
  return [
    { label: "空间总数", value: total, icon: "fa-solid fa-layer-group" },
    {
      label: "在线空间",
      value: active,
      note: total ? `${((active / total) * 100).toFixed(1)}%` : "0%",
      icon: "fa-solid fa-circle-check",
    },
    { label: "生产环境", value: production, icon: "fa-solid fa-cubes" },
    { label: "已绑定 Agent", value: boundAgents, icon: "fa-solid fa-user-gear" },
  ];
});
const workspaceFilterOptions = computed<Array<{ label: string; value: WorkspaceStatusFilter; tone?: string }>>(() => [
  { label: "所有状态", value: "ALL" },
  { label: "正常", value: "Active", tone: "success" },
  { label: "停用", value: "Disabled", tone: "danger" },
]);
const workspaceModeFilterOptions = computed<Array<{ label: string; value: WorkspaceModeFilter }>>(() => [
  { label: "全部环境", value: "ALL" },
  { label: "生产", value: "Production" },
  { label: "沙箱", value: "Sandbox" },
]);

const detailWorkspace = computed(
  () => workspaces.items.find((workspace) => workspace.id === detailWorkspaceId.value) || null,
);
const hasActiveFilters = computed(
  () => Boolean(query.value.trim()) || statusFilter.value !== "ALL" || modeFilter.value !== "ALL",
);
const currentWorkspaceAgents = computed(() => {
  const workspace = detailWorkspace.value;
  if (!workspace) return [];
  return agents.items.filter((agent) => agent.workspaceId === workspace.id);
});
const displayedWorkspaceAgents = computed<Agent[]>(() => {
  return [...currentWorkspaceAgents.value].sort((left, right) => {
    if (left.isDefault !== right.isDefault) return left.isDefault ? -1 : 1;
    return left.name.localeCompare(right.name, "zh-Hans");
  });
});
const workspaceAgentCount = computed(() => currentWorkspaceAgents.value.length);
const workspaceModalTitle = computed(() => (workspaceModalMode.value === "create" ? "新建业务空间" : "修改业务空间"));
const workspaceNameError = computed(() => {
  const name = draftWorkspace.value.name.trim();
  if (!name) return "英文名称必填";
  if (!workspaceNamePattern.test(name)) return "字母开头，3-32 位，仅支持字母/数字/_/-";
  return "";
});
const workspaceDisplayNameError = computed(() => (draftWorkspace.value.displayName.trim() ? "" : "中文描述必填"));
const workspaceFormErrors = computed(() => {
  const errors: string[] = [];
  if (workspaceNameError.value) errors.push(workspaceNameError.value);
  if (workspaceDisplayNameError.value) errors.push(workspaceDisplayNameError.value);
  return errors;
});
const workspaceNameInvalid = computed(() => workspaceFormTouched.value && Boolean(workspaceNameError.value));
const workspaceDisplayNameInvalid = computed(
  () => workspaceFormTouched.value && Boolean(workspaceDisplayNameError.value),
);
const canSaveWorkspaceDraft = computed(() => workspaceFormErrors.value.length === 0);
const workspaceFormMissingHint = computed(() => {
  if (canSaveWorkspaceDraft.value) return "";
  const missing = [workspaceNameError.value && "英文名称", workspaceDisplayNameError.value && "中文描述"].filter(
    Boolean,
  );
  return `请填写：${missing.join("、")}`;
});
const canConfirmWorkspaceDelete = computed(() => {
  const workspace = workspaceDeleteTarget.value;
  return Boolean(workspace && workspaceDeleteConfirmName.value.trim() === workspace.name);
});
const workspaceDeleteNameError = computed(() => {
  const workspace = workspaceDeleteTarget.value;
  if (!workspace || !workspaceDeleteConfirmName.value) return "";
  if (workspaceDeleteConfirmName.value.trim() === workspace.name) return "";
  return `名称需与 ${workspace.name} 完全一致，区分大小写并忽略首尾空格。`;
});
const pendingWorkspaceStatusAction = computed(() => {
  const workspace = workspaceStatusTarget.value;
  if (!workspace) return null;
  return workspace.status === "Active" ? "disable" : "enable";
});
onMounted(() => void loadWorkspacePage());

onBeforeUnmount(() => clearWorkspaceToast());

async function loadWorkspacePage() {
  pageInitialLoading.value = true;
  workspacePageError.value = "";
  try {
    await Promise.all([workspaces.load(), workspaces.loadWorkspacePage({ page: 1 }), modelConfigs.loadModelConfigs()]);
    // Role projection comes from list/detail currentUserRole (ZKL-64); no member N+1.
    workspacePageError.value = workspaces.pageError || "";
  } catch (error) {
    workspacePageError.value = workspaceLoadErrorMessage(error);
    if (getHttpStatus(error) === 401) {
      auth.logout();
    }
  } finally {
    pageInitialLoading.value = false;
  }
}

async function reloadWorkspacePage() {
  await loadWorkspacePage();
}

function workspaceLoadErrorMessage(error: unknown) {
  if (getHttpStatus(error) === 401) {
    return "会话已失效，请重新登录。";
  }
  return "业务空间加载失败，请检查网络或后端服务后重试。";
}

function getHttpStatus(error: unknown) {
  const candidate = error as { response?: { status?: number }; status?: number };
  return candidate.response?.status ?? candidate.status;
}

watch(
  () => [detailWorkspaceId.value, visibleWorkspaceIds.value.join(",")],
  () => reconcileWorkspaceListContext(),
);

function newWorkspace(): Workspace {
  return {
    id: "",
    name: "",
    displayName: "",
    mode: "Sandbox",
    status: "Active",
    defaultAgentId: "",
    modelConfigId: "",
    settings: {},
    createdBy: "",
    updatedBy: "",
    lockVersion: 0,
    healthScore: 0,
  };
}

function workspaceActorLabel(workspace: Workspace, actor: "created" | "updated") {
  const username = actor === "created" ? workspace.createdByUsername : workspace.updatedByUsername;
  const id = actor === "created" ? workspace.createdBy : workspace.updatedBy;
  return username ? `@${username}` : id || "-";
}

function workspaceActorTitle(workspace: Workspace, actor: "created" | "updated") {
  const username = actor === "created" ? workspace.createdByUsername : workspace.updatedByUsername;
  const id = actor === "created" ? workspace.createdBy : workspace.updatedBy;
  if (username && id) return `@${username} · 用户 ID：${id}`;
  return id || username || "-";
}

function openWorkspaceDetail(workspace: Workspace) {
  workspaces.selectWorkspace(workspace.id);
  detailWorkspaceId.value = workspace.id;
  workspaceDetailTab.value = "overview";
  workspaceDetailFilteredOut.value = false;
  resetMemberCandidatePicker();
  void Promise.all([loadWorkspaceMemberContext(workspace.id), agents.loadAgents({ workspaceId: workspace.id })]);
  void nextTick(() => workspaceDetailPageRef.value?.focus());
}

function closeWorkspaceDetail() {
  const workspaceId = detailWorkspaceId.value;
  detailWorkspaceId.value = "";
  workspaceDetailTab.value = "overview";
  resetMemberCandidatePicker();
  void nextTick(() => {
    const target = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-workspace-detail-id]")).find(
      (candidate) => candidate.dataset.workspaceDetailId === workspaceId,
    );
    target?.focus();
  });
}

function selectWorkspaceDetailTab(tab: WorkspaceDetailTab) {
  workspaceDetailTab.value = tab;
  if (tab === "agents" && detailWorkspaceId.value) {
    void agents.loadAgents({ workspaceId: detailWorkspaceId.value });
  }
}

async function loadWorkspaceMemberContext(workspaceId: string) {
  await workspaces.loadMembers(workspaceId);
  if (workspaces.can(workspaceId, "MANAGE")) {
    await loadMemberCandidates(workspaceId);
  }
}

async function loadMemberCandidates(workspaceId: string) {
  memberCandidatesLoading.value = true;
  memberCandidatesError.value = "";
  try {
    memberCandidates.value = await workspaces.searchMemberCandidates(workspaceId, memberCandidateQuery.value, 20);
    if (!memberCandidates.value.some((candidate) => candidate.userId === newMemberUserId.value)) {
      newMemberUserId.value = "";
    }
  } catch {
    memberCandidates.value = [];
    newMemberUserId.value = "";
    memberCandidatesError.value = "候选用户加载失败，请重试。";
  } finally {
    memberCandidatesLoading.value = false;
  }
}

function resetMemberCandidatePicker() {
  newMemberUserId.value = "";
  memberCandidateQuery.value = "";
  memberCandidates.value = [];
  memberCandidatesError.value = "";
}

function clearWorkspaceFilters() {
  query.value = "";
  statusFilter.value = "ALL";
  modeFilter.value = "ALL";
  clearWorkspaceListContext();
  void loadWorkspaceRegistry({ query: "", page: 1 });
}

function setWorkspaceStatusFilter(value: WorkspaceStatusFilter) {
  statusFilter.value = value;
  clearWorkspaceListContext();
  void loadWorkspaceRegistry({ page: 1 });
}

function setWorkspaceModeFilter(value: WorkspaceModeFilter) {
  modeFilter.value = value;
  clearWorkspaceListContext();
  void loadWorkspaceRegistry({ page: 1 });
}

async function loadWorkspaceRegistry(overrides: WorkspaceListQuery = {}) {
  const sortBy = overrides.sortBy !== undefined ? overrides.sortBy : workspaces.listQuery.sortBy;
  const sortOrder = sortBy
    ? overrides.sortOrder !== undefined
      ? overrides.sortOrder
      : workspaces.listQuery.sortOrder
    : undefined;
  const result = await workspaces.loadWorkspacePage({
    query: overrides.query ?? query.value,
    status: statusFilter.value === "ALL" ? undefined : statusFilter.value,
    mode: modeFilter.value === "ALL" ? undefined : (modeFilter.value as "Production" | "Sandbox"),
    page: overrides.page ?? workspaces.pagination.page,
    pageSize: overrides.pageSize ?? workspaces.pagination.pageSize,
    sortBy,
    sortOrder,
  });

  return result;
}

async function refreshWorkspaceCatalogAndPage(overrides: WorkspaceListQuery = {}) {
  await Promise.all([workspaces.load(), loadWorkspaceRegistry(overrides)]);
  const pageSize = Math.max(1, workspaces.pagination.pageSize);
  const maxPage = Math.max(1, Math.ceil(workspaces.pagination.total / pageSize));
  if (workspaces.pagination.page > maxPage) {
    await loadWorkspaceRegistry({ ...overrides, page: maxPage, pageSize });
  }
  reconcileWorkspaceListContext();
}

function clearWorkspaceListContext() {
  clearWorkspaceSelection();
  detailWorkspaceId.value = "";
  workspaceDetailTab.value = "overview";
  workspaceDetailFilteredOut.value = false;
  resetMemberCandidatePicker();
}

function setWorkspaceSearch(value: string) {
  query.value = value;
  clearWorkspaceListContext();
  void loadWorkspaceRegistry({ query: value, page: 1 });
}

function changeWorkspacePage(pagination: { page: number; pageSize: number }) {
  clearWorkspaceListContext();
  void loadWorkspaceRegistry(pagination);
}

function changeWorkspaceSort(sort: { sortBy?: string; sortOrder?: SortOrder }) {
  clearWorkspaceListContext();
  void loadWorkspaceRegistry({ page: 1, sortBy: sort.sortBy || "", sortOrder: sort.sortOrder });
}

function toggleWorkspaceSelection(workspace: Workspace, checked: boolean) {
  const next = new Set(selectedWorkspaceIds.value);
  if (checked) {
    next.add(workspace.id);
  } else {
    next.delete(workspace.id);
  }
  selectedWorkspaceIds.value = next;
}

function _togglePageWorkspaceSelection(checked: boolean) {
  const next = new Set(selectedWorkspaceIds.value);
  for (const workspaceId of visibleWorkspaceIds.value) {
    if (checked) {
      next.add(workspaceId);
    } else {
      next.delete(workspaceId);
    }
  }
  selectedWorkspaceIds.value = next;
}

function clearWorkspaceSelection() {
  selectedWorkspaceIds.value = new Set();
}

async function bulkSetSelectedWorkspaceStatus(status: Workspace["status"]) {
  if (workspaceStatusSaving.value || !selectedWorkspaceIds.value.size) return;
  workspaceStatusSaving.value = true;
  try {
    const selected = workspaces.pageItems.filter(
      (workspace) =>
        selectedWorkspaceIds.value.has(workspace.id) &&
        workspace.status !== status &&
        workspaces.can(workspace.id, "MANAGE"),
    );
    for (const workspace of selected) {
      if (status === "Active") {
        await workspaces.enableWorkspace(workspace.id);
      } else {
        await workspaces.disableWorkspace(workspace.id);
      }
    }
    await refreshWorkspaceCatalogAndPage();
    showWorkspaceToast(`已${status === "Active" ? "启用" : "停用"} ${selected.length} 个业务空间`);
    clearWorkspaceSelection();
  } finally {
    workspaceStatusSaving.value = false;
  }
}

function openCreateWorkspace() {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  draftWorkspace.value = {
    ...newWorkspace(),
    modelConfigId: modelConfigs.items[0]?.id || "",
  };
  workspaceFormTouched.value = false;
  clearWorkspaceToast();
  workspaceModalMode.value = "create";
  showWorkspaceModal.value = true;
  void focusWorkspaceModal();
}

function openEditWorkspace(workspace: Workspace) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  draftWorkspace.value = { ...workspace };
  workspaceFormTouched.value = false;
  clearWorkspaceToast();
  workspaceModalMode.value = "edit";
  showWorkspaceModal.value = true;
  void focusWorkspaceModal();
}

function closeWorkspaceModal() {
  showWorkspaceModal.value = false;
  restoreLastFocus();
}

async function focusWorkspaceModal() {
  await nextTick();
  const target =
    workspaceModalMode.value === "create"
      ? workspaceNameInputRef.value
      : workspaceModalRef.value?.querySelector<HTMLInputElement>("input:not([disabled])");
  target?.focus();
}

async function focusDialog(dialog: HTMLElement | null, preferredTarget?: HTMLElement | null) {
  await nextTick();
  const target =
    preferredTarget ||
    dialog?.querySelector<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
  target?.focus();
}

function restoreLastFocus() {
  void nextTick(() => {
    lastFocusBeforeModal.value?.focus();
    lastFocusBeforeModal.value = null;
  });
}

function trapModalFocus(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  const modal = workspaceModalRef.value || workspaceStatusModalRef.value || workspaceDeleteModalRef.value;
  if (!modal) return;
  const focusable = Array.from(
    modal.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => !element.hasAttribute("disabled") && element.offsetParent !== null);
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function showWorkspaceToast(message: string, tone: "success" | "error" = "success") {
  if (workspaceToastTimer.value) {
    window.clearTimeout(workspaceToastTimer.value);
  }
  workspaceActionTone.value = tone;
  workspaceActionNote.value = message;
  const duration = tone === "error" ? 8000 : Math.max(6000, Math.min(10000, message.length * 180));
  workspaceToastTimer.value = window.setTimeout(() => {
    clearWorkspaceToast();
  }, duration);
}

function clearWorkspaceToast() {
  if (workspaceToastTimer.value) {
    window.clearTimeout(workspaceToastTimer.value);
    workspaceToastTimer.value = null;
  }
  workspaceActionNote.value = "";
}

async function saveDraftWorkspace() {
  if (workspaceSaving.value) return;
  workspaceFormTouched.value = true;
  if (!canSaveWorkspaceDraft.value) {
    showWorkspaceToast(workspaceFormErrors.value[0] || "请补全业务空间信息", "error");
    return;
  }
  workspaceSaving.value = true;
  try {
    if (workspaceModalMode.value === "edit") {
      const updated = await workspaces.updateWorkspace(draftWorkspace.value.id, draftWorkspace.value);
      await refreshWorkspaceCatalogAndPage();
      setWorkspaceDetailIfVisible(updated);
      showWorkspaceToast(`${updated.name} 已保存`);
    } else {
      const created = await workspaces.createWorkspace(draftWorkspace.value);
      await Promise.all([agents.loadAgents({ workspaceId: created.id }), refreshWorkspaceCatalogAndPage({ page: 1 })]);
      setWorkspaceDetailIfVisible(created);
      showWorkspaceToast(`${created.name} 已创建`);
    }
    closeWorkspaceModal();
  } finally {
    workspaceSaving.value = false;
  }
}

/** List-row menu: view → edit → lifecycle → danger (ZKL-33 menu-only). */
function workspaceMenuActions(workspace: Workspace): ManagementRowAction[] {
  const actions: ManagementRowAction[] = [{ key: "view", label: "查看详情", icon: "fa-solid fa-eye", tone: "primary" }];
  if (workspaces.can(workspace.id, "EDIT")) {
    actions.push({ key: "edit", label: "编辑信息", icon: "fa-solid fa-pen" });
  }
  if (workspaces.can(workspace.id, "MANAGE")) {
    actions.push({
      key: "toggle",
      label: workspace.status === "Active" ? "停用空间" : "启用空间",
      icon: workspace.status === "Active" ? "fa-solid fa-power-off" : "fa-solid fa-circle-play",
    });
  }
  if (workspaces.can(workspace.id, "DELETE")) {
    actions.push({ key: "delete", label: "删除空间", icon: "fa-solid fa-trash", tone: "danger" });
  }
  return actions;
}

/** Detail header keeps only lifecycle/danger overflow (page-header actions stay outside scope). */
function workspaceDetailMenuActions(workspace: Workspace): ManagementRowAction[] {
  return workspaceMenuActions(workspace).filter((action) => action.key === "toggle" || action.key === "delete");
}

function workspaceMembers(workspaceId: string) {
  return workspaces.membersByWorkspace[workspaceId] || [];
}

async function addWorkspaceMember(workspaceId: string) {
  const userId = newMemberUserId.value.trim();
  if (!userId) return;
  await workspaces.addMember(workspaceId, userId, newMemberRole.value);
  newMemberUserId.value = "";
  await loadMemberCandidates(workspaceId);
}

async function changeWorkspaceMemberRole(workspaceId: string, userId: string, role: WorkspaceRole) {
  await workspaces.changeMemberRole(workspaceId, userId, role);
}

async function removeWorkspaceMember(workspaceId: string, userId: string) {
  await workspaces.removeMember(workspaceId, userId);
  await loadMemberCandidates(workspaceId);
}

function handleWorkspaceRowAction(action: string, workspace: Workspace) {
  if (action === "view") openWorkspaceDetail(workspace);
  else if (action === "edit") openEditWorkspace(workspace);
  else if (action === "toggle") void toggleWorkspace(workspace);
  else if (action === "delete") deleteWorkspace(workspace);
}

async function toggleWorkspace(workspace: Workspace) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  workspaceStatusTarget.value = workspace;
  clearWorkspaceToast();
  void nextTick(() => focusDialog(workspaceStatusModalRef.value));
}

function closeWorkspaceStatusConfirm() {
  workspaceStatusTarget.value = null;
  restoreLastFocus();
}

async function confirmWorkspaceStatusChange() {
  if (workspaceStatusSaving.value) return;
  const workspace = workspaceStatusTarget.value;
  if (!workspace) return;
  workspaceStatusSaving.value = true;
  try {
    if (workspace.status === "Active") {
      const updated = await workspaces.disableWorkspace(workspace.id);
      showWorkspaceToast(`${updated.name} 已停用`);
    } else {
      const updated = await workspaces.enableWorkspace(workspace.id);
      showWorkspaceToast(`${updated.name} 已启用`);
    }
    await refreshWorkspaceCatalogAndPage();
    clearWorkspaceSelection();
    closeWorkspaceStatusConfirm();
  } finally {
    workspaceStatusSaving.value = false;
  }
}

function deleteWorkspace(workspace: Workspace) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  workspaceDeleteTarget.value = workspace;
  workspaceDeleteConfirmName.value = "";
  clearWorkspaceToast();
  void nextTick(() => focusDialog(workspaceDeleteModalRef.value, workspaceDeleteInputRef.value));
}

function closeWorkspaceDeleteConfirm() {
  workspaceDeleteTarget.value = null;
  workspaceDeleteConfirmName.value = "";
  restoreLastFocus();
}

async function confirmDeleteWorkspace() {
  if (workspaceDeleteSaving.value) return;
  const workspace = workspaceDeleteTarget.value;
  if (!workspace || !canConfirmWorkspaceDelete.value) return;
  workspaceDeleteSaving.value = true;
  try {
    await workspaces.deleteWorkspace(workspace.id);
    await refreshWorkspaceCatalogAndPage();
    clearWorkspaceSelection();
    if (detailWorkspaceId.value === workspace.id) {
      detailWorkspaceId.value = "";
    }
    showWorkspaceToast(`${workspace.name} 已删除`);
    closeWorkspaceDeleteConfirm();
  } finally {
    workspaceDeleteSaving.value = false;
  }
}

function goModelConfigs() {
  void router.push({ name: "model-apis" });
}

function goLogin() {
  auth.logout();
  void router.push({ name: "login" });
}

function statusTone(status: string) {
  return status.toLowerCase();
}

function displayWorkspaceStatus(status: Workspace["status"]) {
  return status === "Active" ? "在线" : "离线";
}

function modeTone(mode: Workspace["mode"]) {
  return mode.toLowerCase();
}

function getModelConfigLabel(configId: string) {
  const config = modelConfigs.items.find((item) => item.id === configId);
  return config ? `${config.name} (${config.modelName})` : "未绑定推理模型";
}

function getDefaultAgentLabel(workspace: Workspace) {
  return (
    agents.items.find((agent) => agent.id === workspace.defaultAgentId)?.name ||
    workspace.defaultAgentId ||
    "未绑定 Agent"
  );
}

function formatWorkspaceUpdatedAt(workspace: Workspace) {
  if (!workspace.updatedAt) return "-";
  const timestamp = Date.parse(workspace.updatedAt);
  if (!Number.isFinite(timestamp)) return workspace.updatedAt;
  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
  if (elapsedMinutes < 1) return "刚刚";
  if (elapsedMinutes < 60) return `${elapsedMinutes} 分钟前`;
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) return `${elapsedHours} 小时前`;
  return `${Math.floor(elapsedHours / 24)} 天前`;
}

async function copyWorkspaceId() {
  const workspace = detailWorkspace.value;
  if (!workspace) return;
  try {
    await navigator.clipboard?.writeText(workspace.id);
    showWorkspaceToast(`${workspace.name} 物理 ID 已复制`);
  } catch {
    showWorkspaceToast("复制失败，请手动复制物理 ID", "error");
  }
}

function setWorkspaceDetailIfVisible(workspace: Workspace) {
  if (!visibleWorkspaceIds.value.includes(workspace.id)) {
    detailWorkspaceId.value = "";
    return;
  }
  workspaces.selectWorkspace(workspace.id);
  detailWorkspaceId.value = workspace.id;
  workspaceDetailTab.value = "overview";
  workspaceDetailFilteredOut.value = false;
}

function reconcileWorkspaceListContext() {
  const visibleIds = new Set(visibleWorkspaceIds.value);
  const selectedIds = [...selectedWorkspaceIds.value].filter((workspaceId) => visibleIds.has(workspaceId));
  if (selectedIds.length !== selectedWorkspaceIds.value.size) {
    selectedWorkspaceIds.value = new Set(selectedIds);
  }
  if (!detailWorkspaceId.value) return;
  if (visibleWorkspaceIds.value.includes(detailWorkspaceId.value)) {
    workspaceDetailFilteredOut.value = false;
    return;
  }
  workspaceDetailFilteredOut.value = hasActiveFilters.value || workspaces.pagination.page > 1;
  detailWorkspaceId.value = "";
}
</script>

<template>
  <div
    class="page-grid workspace-grid"
    :class="{
      'management-page-grid': !workspacePageError && hasWorkspaceRecords && !detailWorkspace,
    }"
    v-loading="pageInitialLoading"
  >
    <ManagementPageHeader
      v-if="!detailWorkspace"
      class="span-12"
      title="业务空间"
      description="独立管理运行环境、模型策略与默认 Agent。"
      icon="fa-solid fa-layer-group"
    >
      <template #actions>
        <button class="primary-button" type="button" @click="openCreateWorkspace">
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          新建业务空间
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip
      v-if="!workspacePageError && hasWorkspaceRecords && !detailWorkspace"
      class="span-12"
      :items="workspaceSummaryItems"
    />

    <section v-if="workspacePageError" class="workspace-load-error span-12" role="alert">
      <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
      <div>
        <span>加载失败</span>
        <h3>{{ workspacePageError }}</h3>
        <p>当前没有展示空数据，以免把接口或会话异常误判为尚未初始化。</p>
      </div>
      <div class="workspace-load-error-actions">
        <button class="ghost-button" type="button" :disabled="pageInitialLoading" @click="reloadWorkspacePage">
          {{ pageInitialLoading ? "重试中..." : "重新加载" }}
        </button>
        <button v-if="workspacePageError.includes('会话')" class="primary-button" type="button" @click="goLogin">
          重新登录
        </button>
      </div>
    </section>

    <section v-else-if="!hasWorkspaceRecords" class="workspace-init-empty span-12">
      <div class="workspace-init-panel">
        <span class="status-pill">尚未初始化业务空间</span>
        <i class="fa-solid fa-layer-group" aria-hidden="true" />
        <h3>创建第一个业务空间</h3>
        <p>业务空间用于隔离 Agent、工具、编排和模型配置。创建后可按需配置模型、创建 Agent 并接入服务与工具。</p>
        <div class="workspace-init-actions">
          <button class="primary-button" type="button" @click="openCreateWorkspace">创建业务空间</button>
          <button class="ghost-button" type="button" @click="goModelConfigs">先配置模型 API</button>
        </div>
        <div class="workspace-init-steps">
          <span><b>1</b>创建空间</span>
          <span><b>2</b>确认模型配置</span>
          <span><b>3</b>创建 Agent 并接入服务和工具</span>
        </div>
      </div>
    </section>

    <template v-else>
      <section v-if="!detailWorkspace" class="workspace-list-card management-list-card span-12">
        <header class="workspace-resource-mode-bar">
          <div>
            <span>列表管理</span>
            <strong>{{ workspaces.pagination.total }} 个匹配空间</strong>
            <small
              >全量目录共 {{ workspaces.items.length }} 个业务空间；当前页由 v1 目录在客户端筛选、排序与分页。</small
            >
          </div>
          <p class="workspace-narrow-notice">字段较多时可横向滚动，固定列保持可见。</p>
        </header>

        <p v-if="workspaceDetailFilteredOut" class="workspace-filter-context-note">
          已清空详情，请从当前筛选结果中重新选择业务空间。
        </p>

        <ManagementList
          class="workspace-management-list"
          :rows="workspaces.pageItems"
          :columns="workspaceColumns"
          row-key="id"
          :sticky-left-keys="['selection', 'identity']"
          :sticky-right-keys="['actions']"
          storage-key="actweave:workspaces:columns"
          selection-tone="neutral"
          :checked-row-keys="checkedWorkspaceKeys"
          :loading="workspaces.pageLoading"
          :error="workspaces.pageError"
          :has-loaded="workspaces.pageHasLoaded"
          :search="query"
          search-placeholder="搜索空间名称 / 创建者 / 修改者..."
          search-aria-label="搜索业务空间"
          :reset-disabled="!hasActiveFilters"
          :pagination="workspaces.pagination"
          :sort-by="workspaces.listQuery.sortBy"
          :sort-order="workspaces.listQuery.sortOrder"
          @select-row="openWorkspaceDetail"
          @update:checked-row-keys="setCheckedWorkspaceKeys"
          @update:search="setWorkspaceSearch"
          @reset="clearWorkspaceFilters"
          @page-change="changeWorkspacePage"
          @sort-change="changeWorkspaceSort"
        >
          <template #batch-actions>
            <button
              class="workspace-batch-action"
              type="button"
              data-action="bulk-enable"
              :disabled="workspaceStatusSaving"
              @click="bulkSetSelectedWorkspaceStatus('Active')"
            >
              批量启用
            </button>
            <button
              class="workspace-batch-action is-danger"
              type="button"
              data-action="bulk-disable"
              :disabled="workspaceStatusSaving"
              @click="bulkSetSelectedWorkspaceStatus('Disabled')"
            >
              批量停用
            </button>
          </template>
          <template #filters>
            <!--            <label class="workspace-page-selection-toggle">-->
            <!--              <input-->
            <!--                type="checkbox"-->
            <!--                :checked="allPageWorkspacesSelected"-->
            <!--                :indeterminate.prop="somePageWorkspacesSelected"-->
            <!--                aria-label="选择当前页全部业务空间"-->
            <!--                @change="togglePageWorkspaceSelection(($event.target as HTMLInputElement).checked)"-->
            <!--              />-->
            <!--              <span>选择当前页</span>-->
            <!--            </label>-->
            <ManagementSegmentedFilter
              :model-value="modeFilter"
              :options="workspaceModeFilterOptions"
              ariaLabel="业务空间环境筛选"
              @update:model-value="setWorkspaceModeFilter($event as WorkspaceModeFilter)"
            />
            <ManagementSegmentedFilter
              :model-value="statusFilter"
              :options="workspaceFilterOptions"
              ariaLabel="业务空间状态筛选"
              @update:model-value="setWorkspaceStatusFilter($event as WorkspaceStatusFilter)"
            />
          </template>

          <template #cell-selection="{ row: workspace }">
            <label v-if="workspaces.can(workspace.id, 'MANAGE')" class="workspace-checkbox-hitarea" @click.stop>
              <input
                type="checkbox"
                :checked="selectedWorkspaceIds.has(workspace.id)"
                :aria-label="'选择' + workspace.name"
                @change="toggleWorkspaceSelection(workspace, ($event.target as HTMLInputElement).checked)"
              />
            </label>
          </template>
          <template #cell-identity="{ row: workspace }">
            <button
              type="button"
              class="workspace-resource-name workspace-identity-button"
              :data-workspace-detail-id="workspace.id"
              :aria-label="`查看 ${workspace.displayName || workspace.name} 详情`"
              @click.stop="openWorkspaceDetail(workspace)"
            >
              <span :class="['workspace-resource-icon', statusTone(workspace.status)]">
                <i class="fa-solid fa-cube" aria-hidden="true" />
              </span>
              <span>
                <strong class="aw-table-title" :title="workspace.name">{{ workspace.name }}</strong>
                <small class="aw-table-subtitle" :title="workspace.displayName">{{ workspace.displayName }}</small>
              </span>
            </button>
          </template>
          <template #cell-status="{ row: workspace }">
            <span :class="['status-pill', 'aw-table-pill', statusTone(workspace.status)]">
              <i class="workspace-status-dot" aria-hidden="true" />
              {{ displayWorkspaceStatus(workspace.status) }}
            </span>
          </template>
          <template #cell-mode="{ row: workspace }">
            <span :class="['workspace-mode-pill', 'aw-table-pill', modeTone(workspace.mode)]">{{
              workspace.mode
            }}</span>
          </template>
          <template #cell-defaultAgent="{ row: workspace }">
            <span class="workspace-agent-chip aw-table-pill" :title="getDefaultAgentLabel(workspace)"
              ><i class="fa-solid fa-user-gear" aria-hidden="true" />{{ getDefaultAgentLabel(workspace) }}</span
            >
          </template>
          <template #cell-model="{ row: workspace }">
            <span class="workspace-model-name aw-table-meta" :title="getModelConfigLabel(workspace.modelConfigId)"
              ><i class="fa-solid fa-brain" aria-hidden="true" /><span>{{
                getModelConfigLabel(workspace.modelConfigId)
              }}</span></span
            >
          </template>
          <template #cell-updatedAt="{ row: workspace }"
            ><span class="workspace-updated-at aw-table-meta">{{ formatWorkspaceUpdatedAt(workspace) }}</span></template
          >
          <template #cell-createdBy="{ row: workspace }">
            <span class="workspace-owner-name aw-table-meta" :title="workspaceActorTitle(workspace, 'created')">{{
              workspaceActorLabel(workspace, "created")
            }}</span>
          </template>
          <template #cell-updatedBy="{ row: workspace }">
            <span class="workspace-owner-name aw-table-meta" :title="workspaceActorTitle(workspace, 'updated')">{{
              workspaceActorLabel(workspace, "updated")
            }}</span>
          </template>
          <template #cell-actions="{ row: workspace }">
            <ManagementRowActions
              :menu-actions="workspaceMenuActions(workspace)"
              @action="handleWorkspaceRowAction($event, workspace)"
            />
          </template>

          <template #empty>
            <div class="workspace-filter-empty">
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <strong>没有匹配的业务空间</strong>
              <span>请修改关键词、状态或环境筛选。</span>
              <button class="ghost-button" type="button" @click="clearWorkspaceFilters">重置所有过滤条件</button>
            </div>
          </template>
          <template #error>
            <div class="workspace-load-error" role="alert">
              <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
              <span>{{ workspaces.pageError || "业务空间列表加载失败" }}</span>
              <button class="ghost-button" type="button" @click="loadWorkspaceRegistry()">重试</button>
            </div>
          </template>
        </ManagementList>
      </section>

      <section
        v-else
        ref="workspaceDetailPageRef"
        class="workspace-detail-page span-12"
        tabindex="-1"
        :aria-labelledby="'workspace-detail-title'"
        @keydown.esc="closeWorkspaceDetail"
      >
        <header class="workspace-detail-page-header">
          <div class="workspace-detail-heading-block">
            <button type="button" class="workspace-detail-back" @click="closeWorkspaceDetail">
              <i class="fa-solid fa-arrow-left" aria-hidden="true" />
              返回业务空间列表
            </button>
            <div class="workspace-detail-heading">
              <span class="workspace-detail-heading-icon"><i class="fa-solid fa-cube" aria-hidden="true" /></span>
              <div>
                <span>Workspace Detail</span>
                <h2 id="workspace-detail-title">{{ detailWorkspace.displayName || detailWorkspace.name }}</h2>
                <div class="workspace-detail-heading-meta">
                  <p>{{ detailWorkspace.name }}</p>
                  <span :class="['status-pill', statusTone(detailWorkspace.status)]">
                    <i class="workspace-status-dot" aria-hidden="true" />
                    {{ displayWorkspaceStatus(detailWorkspace.status) }}
                  </span>
                  <span :class="['workspace-mode-pill', modeTone(detailWorkspace.mode)]">{{
                    detailWorkspace.mode
                  }}</span>
                </div>
              </div>
            </div>
          </div>
          <div class="workspace-detail-page-actions">
            <button type="button" class="ghost-button" @click="copyWorkspaceId">
              <i class="fa-regular fa-copy" aria-hidden="true" />
              复制物理 ID
            </button>
            <button
              v-if="workspaces.can(detailWorkspace.id, 'EDIT')"
              type="button"
              class="primary-button workspace-detail-edit"
              @click="openEditWorkspace(detailWorkspace)"
            >
              <i class="fa-solid fa-sliders" aria-hidden="true" />
              修改空间
            </button>
            <ManagementRowActions
              v-if="workspaceDetailMenuActions(detailWorkspace).length"
              class="workspace-detail-more-actions"
              :menu-actions="workspaceDetailMenuActions(detailWorkspace)"
              @action="handleWorkspaceRowAction($event, detailWorkspace)"
            />
          </div>
        </header>

        <nav class="workspace-detail-tabs" role="tablist" aria-label="业务空间详情分区">
          <button
            id="workspace-detail-tab-overview"
            type="button"
            role="tab"
            :aria-selected="workspaceDetailTab === 'overview'"
            aria-controls="workspace-detail-panel-overview"
            :class="{ active: workspaceDetailTab === 'overview' }"
            @click="selectWorkspaceDetailTab('overview')"
          >
            <i class="fa-solid fa-chart-pie" aria-hidden="true" />概览
          </button>
          <button
            id="workspace-detail-tab-members"
            type="button"
            role="tab"
            :aria-selected="workspaceDetailTab === 'members'"
            aria-controls="workspace-detail-panel-members"
            :class="{ active: workspaceDetailTab === 'members' }"
            @click="selectWorkspaceDetailTab('members')"
          >
            <i class="fa-solid fa-user-group" aria-hidden="true" />成员
            <em>{{ workspaceMembers(detailWorkspace.id).length }}</em>
          </button>
          <button
            id="workspace-detail-tab-agents"
            type="button"
            role="tab"
            :aria-selected="workspaceDetailTab === 'agents'"
            aria-controls="workspace-detail-panel-agents"
            :class="{ active: workspaceDetailTab === 'agents' }"
            @click="selectWorkspaceDetailTab('agents')"
          >
            <i class="fa-solid fa-robot" aria-hidden="true" />Agent <em>{{ workspaceAgentCount }}</em>
          </button>
        </nav>

        <div class="workspace-detail-page-body">
          <section
            v-if="workspaceDetailTab === 'overview'"
            id="workspace-detail-panel-overview"
            class="workspace-detail-tab-panel"
            role="tabpanel"
            aria-labelledby="workspace-detail-tab-overview"
          >
            <div class="workspace-detail-summary-grid">
              <article>
                <span><i class="fa-solid fa-user-group" aria-hidden="true" />空间成员</span
                ><strong>{{ workspaceMembers(detailWorkspace.id).length }}</strong
                ><small>拥有空间访问权限的成员</small>
              </article>
              <article>
                <span><i class="fa-solid fa-robot" aria-hidden="true" />协作 Agent</span
                ><strong>{{ workspaceAgentCount }}</strong
                ><small>当前空间内的活动 Agent</small>
              </article>
              <article>
                <span><i class="fa-solid fa-shield-halved" aria-hidden="true" />隔离环境</span
                ><strong>{{ detailWorkspace.mode === "Production" ? "生产" : "沙箱" }}</strong
                ><small>资源与执行策略独立隔离</small>
              </article>
            </div>

            <div class="workspace-detail-overview-grid">
              <article class="workspace-detail-content-card">
                <header class="workspace-detail-section-title">
                  <span><i class="fa-solid fa-fingerprint" aria-hidden="true" /></span>
                  <div><strong>空间身份与归属</strong><small>用于权限校验、审计和资源隔离的基础信息</small></div>
                </header>
                <dl class="workspace-detail-metadata">
                  <div>
                    <dt>空间物理 ID</dt>
                    <dd :title="detailWorkspace.id">{{ detailWorkspace.id }}</dd>
                  </div>
                  <div>
                    <dt>环境模式</dt>
                    <dd>{{ detailWorkspace.mode }}</dd>
                  </div>
                  <div>
                    <dt>创建者</dt>
                    <dd :title="workspaceActorTitle(detailWorkspace, 'created')">
                      {{ workspaceActorLabel(detailWorkspace, "created") }}
                    </dd>
                  </div>
                  <div>
                    <dt>最后修改者</dt>
                    <dd :title="workspaceActorTitle(detailWorkspace, 'updated')">
                      {{ workspaceActorLabel(detailWorkspace, "updated") }}
                    </dd>
                  </div>
                </dl>
              </article>

              <article class="workspace-detail-content-card">
                <header class="workspace-detail-section-title">
                  <span><i class="fa-solid fa-microchip" aria-hidden="true" /></span>
                  <div><strong>默认模型配置</strong><small>空间内 Agent 默认使用的推理模型</small></div>
                </header>
                <div class="workspace-model-readonly-card">
                  <span class="workspace-model-readonly-value">
                    <i class="fa-solid fa-microchip" aria-hidden="true" />
                    {{ getModelConfigLabel(detailWorkspace.modelConfigId) }}
                  </span>
                  <p>模型凭据与连接参数由模型 API 配置统一维护，此处仅展示当前绑定关系。</p>
                  <button type="button" class="workspace-model-config-link" @click="goModelConfigs">
                    前往模型 API 配置
                    <i class="fa-solid fa-arrow-right" aria-hidden="true" />
                  </button>
                </div>
              </article>
            </div>
          </section>

          <section
            v-else-if="workspaceDetailTab === 'members'"
            id="workspace-detail-panel-members"
            class="workspace-detail-tab-panel"
            role="tabpanel"
            aria-labelledby="workspace-detail-tab-members"
          >
            <article class="workspace-detail-content-card workspace-detail-members-card">
              <header class="workspace-detail-section-title workspace-detail-section-title--split">
                <span><i class="fa-solid fa-user-shield" aria-hidden="true" /></span>
                <div><strong>空间成员与角色</strong><small>为平台用户分配该空间内的最小必要权限</small></div>
                <em>{{ workspaceMembers(detailWorkspace.id).length }} 人</em>
              </header>

              <div v-if="workspaces.can(detailWorkspace.id, 'MANAGE')" class="workspace-member-add">
                <div class="workspace-member-picker">
                  <label for="workspace-member-search-input">搜索用户</label>
                  <div class="workspace-member-search">
                    <input
                      id="workspace-member-search-input"
                      v-model.trim="memberCandidateQuery"
                      aria-label="搜索候选成员"
                      placeholder="输入用户名或显示名称"
                      @keyup.enter="loadMemberCandidates(detailWorkspace.id)"
                    />
                    <button
                      type="button"
                      class="icon-action-button"
                      aria-label="搜索候选用户"
                      :disabled="memberCandidatesLoading"
                      @click="loadMemberCandidates(detailWorkspace.id)"
                    >
                      <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
                    </button>
                  </div>
                  <label class="workspace-member-candidate-select">
                    <span>候选用户</span>
                    <AppSelect
                      v-model="newMemberUserId"
                      :options="memberCandidateOptions"
                      :placeholder="memberCandidatesLoading ? '正在加载候选用户…' : '请选择要添加的用户'"
                      :disabled="memberCandidatesLoading"
                      filterable
                      aria-label="选择成员用户"
                    />
                  </label>
                  <small v-if="memberCandidatesError" class="workspace-member-candidate-error" role="alert">{{
                    memberCandidatesError
                  }}</small>
                  <small v-else-if="!memberCandidatesLoading && !memberCandidates.length"
                    >没有可添加的正常状态用户</small
                  >
                </div>
                <label class="workspace-member-role-field">
                  <span>空间角色</span>
                  <AppSelect
                    v-model="newMemberRole"
                    :options="assignableWorkspaceRoleOptions"
                    aria-label="新成员角色"
                  />
                </label>
                <button
                  type="button"
                  class="primary-button workspace-member-add-button"
                  :disabled="!newMemberUserId"
                  @click="addWorkspaceMember(detailWorkspace.id)"
                >
                  <i class="fa-solid fa-user-plus" aria-hidden="true" />添加成员
                </button>
              </div>

              <div class="workspace-member-list">
                <div
                  v-for="member in workspaceMembers(detailWorkspace.id)"
                  :key="member.userId"
                  class="workspace-member-row"
                >
                  <span class="workspace-member-identity">
                    <i class="fa-solid fa-user" aria-hidden="true" />
                    <span
                      ><strong>{{ member.userId }}</strong
                      ><small>{{ workspaceRoleLabels[member.role] }}</small></span
                    >
                  </span>
                  <label>
                    <AppSelect
                      :model-value="member.role"
                      :options="workspaceRoleOptions"
                      aria-label="成员角色"
                      :disabled="!workspaces.can(detailWorkspace.id, 'MANAGE') || member.role === 'OWNER'"
                      @update:model-value="
                        changeWorkspaceMemberRole(detailWorkspace.id, member.userId, String($event) as WorkspaceRole)
                      "
                    />
                  </label>
                  <button
                    v-if="workspaces.can(detailWorkspace.id, 'MANAGE') && member.role !== 'OWNER'"
                    type="button"
                    class="icon-action-button danger"
                    aria-label="移除成员"
                    @click="removeWorkspaceMember(detailWorkspace.id, member.userId)"
                  >
                    <i class="fa-solid fa-user-minus" aria-hidden="true" />
                  </button>
                </div>
                <div v-if="!workspaceMembers(detailWorkspace.id).length" class="workspace-detail-empty-state">
                  <i class="fa-solid fa-user-group" aria-hidden="true" />
                  <strong>暂无空间成员</strong>
                  <span>使用上方表单添加第一个成员。</span>
                </div>
              </div>
            </article>
          </section>

          <section
            v-else
            id="workspace-detail-panel-agents"
            class="workspace-detail-tab-panel"
            role="tabpanel"
            aria-labelledby="workspace-detail-tab-agents"
          >
            <article class="workspace-detail-content-card">
              <header class="workspace-detail-section-title workspace-detail-section-title--split">
                <span><i class="fa-solid fa-robot" aria-hidden="true" /></span>
                <div>
                  <strong>空间内协作 Agent</strong><small>按默认 Agent 优先排序，便于快速确认空间执行入口</small>
                </div>
                <em>{{ workspaceAgentCount }} 个活动 Agent</em>
              </header>
              <div
                class="workspace-detail-agent-list"
                v-loading="agents.loading"
                :class="{ 'is-refreshing': agents.loading }"
              >
                <div v-for="agent in displayedWorkspaceAgents" :key="agent.id" class="workspace-detail-agent">
                  <i class="fa-solid fa-robot" aria-hidden="true" />
                  <span
                    ><strong>{{ agent.name }}</strong
                    ><small>{{ agent.roleDescription }}</small></span
                  >
                  <em v-if="agent.isDefault" class="workspace-detail-agent-default">默认 Agent</em>
                </div>
                <div v-if="!displayedWorkspaceAgents.length" class="workspace-detail-empty-state">
                  <i class="fa-solid fa-robot" aria-hidden="true" />
                  <strong>暂无活动 Agent</strong>
                  <span>创建或启用 Agent 后会显示在这里。</span>
                </div>
              </div>
            </article>
          </section>
        </div>
      </section>
    </template>
    <Transition name="modal-fade">
      <div
        v-if="showWorkspaceModal"
        class="modal-backdrop"
        data-testid="workspace-modal-backdrop"
        @keydown.esc.stop.prevent="closeWorkspaceModal"
        @keydown="trapModalFocus"
      >
        <section
          ref="workspaceModalRef"
          class="modal-card workspace-modal-card"
          role="dialog"
          aria-modal="true"
          :aria-label="workspaceModalTitle"
          @keydown.esc.stop.prevent="closeWorkspaceModal"
        >
          <header class="modal-card-head">
            <div>
              <span>Workspace Setup</span>
              <h3>{{ workspaceModalTitle }}</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              title="关闭"
              aria-label="关闭业务空间表单"
              @click="closeWorkspaceModal"
            >
              <i class="fa-solid fa-xmark" aria-hidden="true" />
            </button>
          </header>

          <div class="modal-form">
            <label class="modal-field">
              <span>英文名称 <em aria-label="必填">*</em></span>
              <input
                v-model.trim="draftWorkspace.name"
                ref="workspaceNameInputRef"
                placeholder="例如: customer-service"
                :disabled="workspaceModalMode === 'edit'"
                :aria-invalid="workspaceNameInvalid"
                aria-describedby="workspace-name-helper"
                required
                @blur="workspaceFormTouched = true"
              />
              <small id="workspace-name-helper"
                >以字母开头，3-32 位，仅支持字母、数字、下划线和中划线。创建后不再修改。</small
              >
              <small v-if="workspaceNameInvalid" class="field-error">{{ workspaceNameError }}</small>
            </label>
            <label class="modal-field">
              <span>中文描述 <em aria-label="必填">*</em></span>
              <input
                v-model.trim="draftWorkspace.displayName"
                placeholder="例如: 客户服务业务空间"
                :aria-invalid="workspaceDisplayNameInvalid"
                required
                @blur="workspaceFormTouched = true"
              />
              <small v-if="workspaceDisplayNameInvalid" class="field-error">{{ workspaceDisplayNameError }}</small>
            </label>
            <div class="modal-field">
              <span>环境模式</span>
              <div class="workspace-modal-segment" role="radiogroup" aria-label="环境模式">
                <button
                  v-for="option in modeOptions"
                  :key="option.value"
                  type="button"
                  role="radio"
                  :aria-checked="draftWorkspace.mode === option.value"
                  :class="[
                    'workspace-segment-option',
                    `tone-${option.value.toLowerCase()}`,
                    { selected: draftWorkspace.mode === option.value },
                  ]"
                  @click="draftWorkspace.mode = option.value"
                >
                  <i v-if="draftWorkspace.mode === option.value" class="fa-solid fa-circle-check" aria-hidden="true" />
                  {{ option.value === "Sandbox" ? "Sandbox (沙箱)" : "Production (生产)" }}
                </button>
              </div>
            </div>
            <div v-if="workspaceFormTouched && workspaceFormErrors.length" class="workspace-form-errors" role="alert">
              <span v-for="error in workspaceFormErrors" :key="error">{{ error }}</span>
            </div>
            <p v-if="workspaceFormMissingHint" class="form-helper workspace-form-missing">
              {{ workspaceFormMissingHint }}
            </p>
            <p class="form-helper">
              {{
                workspaceModalMode === "create"
                  ? "创建业务空间后，请在「Agent 管理」中按需创建 Agent，并绑定模型与工具。"
                  : "保存后会影响该空间下后续新请求使用的属性配置。"
              }}
            </p>
          </div>

          <div class="workspace-modal-actions">
            <button class="ghost-button" type="button" :disabled="workspaceSaving" @click="closeWorkspaceModal">
              取消返回
            </button>
            <button
              class="primary-button"
              type="button"
              :disabled="workspaceSaving || !canSaveWorkspaceDraft"
              @click="saveDraftWorkspace"
            >
              {{
                workspaceSaving
                  ? workspaceModalMode === "create"
                    ? "创建中..."
                    : "保存中..."
                  : workspaceModalMode === "create"
                    ? "创建业务空间"
                    : "同步更新属性"
              }}
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div
        v-if="workspaceStatusTarget"
        class="modal-backdrop workspace-confirm-backdrop"
        @click.self="closeWorkspaceStatusConfirm"
        @keydown.esc.stop.prevent="closeWorkspaceStatusConfirm"
        @keydown="trapModalFocus"
      >
        <section
          ref="workspaceStatusModalRef"
          class="modal-card workspace-confirm-card"
          role="dialog"
          aria-modal="true"
          aria-label="业务空间状态变更确认"
          @keydown.esc.stop.prevent="closeWorkspaceStatusConfirm"
        >
          <header class="modal-card-head">
            <div>
              <span>Lifecycle Guard</span>
              <h3>{{ pendingWorkspaceStatusAction === "disable" ? "停用业务空间" : "启用业务空间" }}</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              title="关闭"
              aria-label="关闭状态变更确认"
              @click="closeWorkspaceStatusConfirm"
            >
              <i class="fa-solid fa-xmark" aria-hidden="true" />
            </button>
          </header>

          <div class="workspace-confirm-body">
            <i
              :class="
                pendingWorkspaceStatusAction === 'disable' ? 'fa-solid fa-circle-pause' : 'fa-solid fa-circle-play'
              "
              aria-hidden="true"
            />
            <div>
              <strong>{{ workspaceStatusTarget.name }}</strong>
              <p v-if="pendingWorkspaceStatusAction === 'disable'">
                停用后该空间下的 Agent / Tool / Workflow 入口会从运行链路中隔离，后续可重新启用。
              </p>
              <p v-else>启用后该空间会重新进入运行链路，请确认模型、Agent 和工具配置已经就绪。</p>
              <ul class="workspace-lifecycle-effects">
                <li>新请求会被阻止或恢复进入该空间。</li>
                <li>运行中的执行不会在此处自动取消。</li>
                <li>若当前顶部空间选择为该空间，后续入口会按最新状态刷新。</li>
              </ul>
            </div>
          </div>

          <div class="workspace-modal-actions">
            <button
              class="ghost-button"
              type="button"
              :disabled="workspaceStatusSaving"
              @click="closeWorkspaceStatusConfirm"
            >
              取消
            </button>
            <button
              class="primary-button"
              type="button"
              :class="{ danger: pendingWorkspaceStatusAction === 'disable' }"
              :disabled="workspaceStatusSaving"
              @click="confirmWorkspaceStatusChange"
            >
              {{
                workspaceStatusSaving
                  ? "处理中..."
                  : pendingWorkspaceStatusAction === "disable"
                    ? "确认停用"
                    : "确认启用"
              }}
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <Transition name="modal-fade">
      <div
        v-if="workspaceDeleteTarget"
        class="modal-backdrop workspace-confirm-backdrop"
        @click.self="closeWorkspaceDeleteConfirm"
        @keydown.esc.stop.prevent="closeWorkspaceDeleteConfirm"
        @keydown="trapModalFocus"
      >
        <section
          ref="workspaceDeleteModalRef"
          class="modal-card workspace-confirm-card"
          role="dialog"
          aria-modal="true"
          aria-label="删除业务空间确认"
          @keydown.esc.stop.prevent="closeWorkspaceDeleteConfirm"
        >
          <header class="modal-card-head">
            <div>
              <span>Danger Zone</span>
              <h3>删除业务空间</h3>
            </div>
            <button
              class="icon-action-button"
              type="button"
              title="关闭"
              aria-label="关闭删除确认"
              @click="closeWorkspaceDeleteConfirm"
            >
              <i class="fa-solid fa-xmark" aria-hidden="true" />
            </button>
          </header>

          <div class="workspace-confirm-body">
            <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
            <div>
              <strong>{{ workspaceDeleteTarget.name }}</strong>
              <p>删除后会移除该业务边界，并影响其下关联的 Agent / Tool / Workflow。此操作当前不可在页面内撤销。</p>
            </div>
          </div>
          <label class="modal-field workspace-confirm-input">
            <span
              >请输入空间名称 <em>{{ workspaceDeleteTarget.name }}</em> 以确认删除</span
            >
            <input
              ref="workspaceDeleteInputRef"
              v-model.trim="workspaceDeleteConfirmName"
              autocomplete="off"
              :aria-invalid="workspaceDeleteConfirmName.length > 0 && !canConfirmWorkspaceDelete"
              aria-describedby="workspace-delete-name-helper workspace-delete-name-error"
            />
            <small id="workspace-delete-name-helper"
              >需精确匹配空间英文名称；首尾空格会被自动忽略，大小写必须一致。</small
            >
            <small v-if="workspaceDeleteNameError" id="workspace-delete-name-error" class="field-error">{{
              workspaceDeleteNameError
            }}</small>
          </label>

          <div class="workspace-modal-actions">
            <button
              class="ghost-button"
              type="button"
              :disabled="workspaceDeleteSaving"
              @click="closeWorkspaceDeleteConfirm"
            >
              取消
            </button>
            <button
              class="primary-button danger"
              type="button"
              :disabled="workspaceDeleteSaving || !canConfirmWorkspaceDelete"
              @click="confirmDeleteWorkspace"
            >
              <i class="fa-solid fa-trash" aria-hidden="true" />
              {{ workspaceDeleteSaving ? "删除中..." : "删除空间" }}
            </button>
          </div>
        </section>
      </div>
    </Transition>

    <div
      v-if="workspaceActionNote && !showWorkspaceModal"
      :class="['action-toast', workspaceActionTone === 'error' && 'error']"
      role="status"
      aria-live="polite"
    >
      <i
        :class="workspaceActionTone === 'error' ? 'fa-solid fa-circle-exclamation' : 'fa-solid fa-circle-check'"
        aria-hidden="true"
      />
      <span>{{ workspaceActionNote }}</span>
      <button type="button" aria-label="关闭反馈提示" @click="clearWorkspaceToast">
        <i class="fa-solid fa-xmark" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
