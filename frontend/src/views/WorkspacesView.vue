<script setup lang="ts">
import "./workspaces-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

import AppSelect from "../components/AppSelect.vue";
import ManagementDialog from "../components/ManagementDialog.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { useAgentStore } from "../stores/agents";
import { useAuthStore } from "../stores/auth";
import { useWorkspaceStore } from "../stores/workspaces";
import { restoreFocus } from "../utils/focus-modality";
import { isProductionWorkspaceMode } from "../utils/workspace-mode";
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
const { t } = useI18n();
const workspaces = useWorkspaceStore();
const agents = useAgentStore();

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
  { label: "Production", value: "PRODUCTION" },
  { label: "Sandbox", value: "SANDBOX" },
];
const workspaceRoleLabelKeys: Record<WorkspaceRole, string> = {
  OWNER: "workspaces.roleOwner",
  ADMIN: "workspaces.roleAdmin",
  EDITOR: "workspaces.roleEditor",
  OPERATOR: "workspaces.roleOperator",
  VIEWER: "workspaces.roleViewer",
};
function workspaceRoleLabel(role: WorkspaceRole) {
  return t(workspaceRoleLabelKeys[role]);
}
const assignableWorkspaceRoleOptions = computed(() =>
  (["ADMIN", "EDITOR", "OPERATOR", "VIEWER"] as WorkspaceRole[]).map((role) => ({
    label: workspaceRoleLabel(role),
    value: role,
  })),
);
const workspaceRoleOptions = computed(() =>
  (["OWNER", "ADMIN", "EDITOR", "OPERATOR", "VIEWER"] as WorkspaceRole[]).map((role) => ({
    label: workspaceRoleLabel(role),
    value: role,
  })),
);
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
  { key: "selection", label: t("workspaces.colSelect"), width: 48, headerAlign: "center" },
  {
    key: "identity",
    label: t("workspaces.colWorkspace"),
    width: 240,
    sortable: true,
    sortKey: "name",
    getValue: (workspace) => `${workspace.name} ${workspace.displayName}`,
  },
  {
    key: "mode",
    label: t("workspaces.colMode"),
    width: 124,
    hidable: true,
    sortable: true,
    sortKey: "mode",
    getValue: (workspace) => workspace.mode,
  },
  {
    key: "defaultAgent",
    label: t("workspaces.colDefaultAgent"),
    width: 180,
    hidable: true,
    getValue: getDefaultAgentLabel,
  },
  {
    key: "status",
    label: t("workspaces.colStatus"),
    width: 124,
    hidable: true,
    sortable: true,
    sortKey: "status",
    getValue: (workspace) => workspace.status,
  },
  {
    key: "updatedAt",
    label: t("workspaces.colUpdated"),
    width: 130,
    hidable: true,
    sortable: true,
    sortKey: "updatedAt",
    getValue: formatWorkspaceUpdatedAt,
  },
  {
    key: "createdBy",
    label: t("workspaces.colCreator"),
    width: 180,
    hidable: true,
    defaultHidden: true,
    sortable: true,
    sortKey: "createdBy",
    getValue: (workspace) => workspaceActorLabel(workspace, "created"),
  },
  {
    key: "updatedBy",
    label: t("workspaces.colUpdater"),
    width: 180,
    hidable: true,
    defaultHidden: true,
    sortable: true,
    sortKey: "updatedBy",
    getValue: (workspace) => workspaceActorLabel(workspace, "updated"),
  },
  { key: "actions", label: t("workspaces.colActions"), width: 68, align: "right", headerAlign: "center" },
]);

const hasWorkspaceRecords = computed(() => workspaces.summary.total > 0 || workspaces.pageItems.length > 0);
const workspaceSummaryItems = computed<ManagementSummaryItem[]>(() => {
  // D9-A: cards use full accessible-set summary from the server, not the current page.
  const total = workspaces.summary.total;
  const active = workspaces.summary.active;
  const production = workspaces.summary.production;
  const boundAgents = workspaces.summary.boundAgents;
  return [
    { label: t("workspaces.total"), value: total, icon: "fa-solid fa-layer-group" },
    {
      label: t("workspaces.onlineSpaces"),
      value: active,
      note: total ? `${((active / total) * 100).toFixed(1)}%` : "0%",
      icon: "fa-solid fa-circle-check",
    },
    { label: t("workspaces.production"), value: production, icon: "fa-solid fa-cubes" },
    { label: t("workspaces.boundAgents"), value: boundAgents, icon: "fa-solid fa-user-gear" },
  ];
});
const workspaceFilterOptions = computed<Array<{ label: string; value: WorkspaceStatusFilter; tone?: string }>>(() => [
  { label: t("workspaces.statusAll"), value: "ALL" },
  { label: t("workspaces.statusActive"), value: "ACTIVE", tone: "success" },
  { label: t("workspaces.statusDisabled"), value: "DISABLED", tone: "danger" },
]);
const workspaceModeFilterOptions = computed<Array<{ label: string; value: WorkspaceModeFilter }>>(() => [
  { label: t("workspaces.modeAll"), value: "ALL" },
  { label: t("workspaces.modeProduction"), value: "PRODUCTION" },
  { label: t("workspaces.modeSandbox"), value: "SANDBOX" },
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
const workspaceModalTitle = computed(() =>
  workspaceModalMode.value === "create" ? t("workspaces.create") : t("workspaces.edit"),
);
const workspaceNameError = computed(() => {
  const name = draftWorkspace.value.name.trim();
  if (!name) return t("workspaces.nameRequired");
  if (!workspaceNamePattern.test(name)) return t("workspaces.namePattern");
  return "";
});
const workspaceDisplayNameError = computed(() =>
  draftWorkspace.value.displayName.trim() ? "" : t("workspaces.displayNameRequired"),
);
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
  const missing = [
    workspaceNameError.value && t("workspaces.fieldName"),
    workspaceDisplayNameError.value && t("workspaces.fieldDisplayName"),
  ].filter(Boolean);
  return t("workspaces.fillRequired", { fields: missing.join(", ") });
});
const canConfirmWorkspaceDelete = computed(() => {
  const workspace = workspaceDeleteTarget.value;
  return Boolean(workspace && workspaceDeleteConfirmName.value.trim() === workspace.name);
});
const workspaceDeleteNameError = computed(() => {
  const workspace = workspaceDeleteTarget.value;
  if (!workspace || !workspaceDeleteConfirmName.value) return "";
  if (workspaceDeleteConfirmName.value.trim() === workspace.name) return "";
  return t("workspaces.confirmNameHint", { name: workspace.name });
});
const pendingWorkspaceStatusAction = computed(() => {
  const workspace = workspaceStatusTarget.value;
  if (!workspace) return null;
  return workspace.status === "ACTIVE" ? "disable" : "enable";
});
onMounted(() => void loadWorkspacePage());

onBeforeUnmount(() => clearWorkspaceToast());

async function loadWorkspacePage() {
  pageInitialLoading.value = true;
  workspacePageError.value = "";
  try {
    await Promise.all([workspaces.load(), workspaces.loadWorkspacePage({ page: 1 })]);
    // Role projection comes from list/detail currentUserRole (ZKL-64); no member N+1.
    workspacePageError.value = workspaces.pageError || "";
  } catch (error) {
    workspacePageError.value = workspaceLoadErrorMessage(error);
    if (getHttpStatus(error) === 401) {
      void auth.expireSession();
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
    return t("workspaces.sessionExpired");
  }
  return t("workspaces.loadFailed");
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
    mode: "SANDBOX",
    status: "ACTIVE",
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
  if (username && id) return t("workspaces.userIdLabel", { username, id });
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
    memberCandidatesError.value = t("workspaces.candidatesFailed");
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
    mode: modeFilter.value === "ALL" ? undefined : (modeFilter.value as "PRODUCTION" | "SANDBOX"),
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
      if (status === "ACTIVE") {
        await workspaces.enableWorkspace(workspace.id);
      } else {
        await workspaces.disableWorkspace(workspace.id);
      }
    }
    await refreshWorkspaceCatalogAndPage();
    showWorkspaceToast(
      t("workspaces.bulkStatus", {
        action: status === "ACTIVE" ? t("workspaces.enabled") : t("workspaces.disabled"),
        n: selected.length,
      }),
    );
    clearWorkspaceSelection();
  } finally {
    workspaceStatusSaving.value = false;
  }
}

function openCreateWorkspace() {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  draftWorkspace.value = {
    ...newWorkspace(),
    // Workspace default model is not managed in this UI; Agent binds model explicitly.
    modelConfigId: "",
  };
  workspaceFormTouched.value = false;
  clearWorkspaceToast();
  workspaceModalMode.value = "create";
  showWorkspaceModal.value = true;
}

function openEditWorkspace(workspace: Workspace) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  draftWorkspace.value = { ...workspace };
  workspaceFormTouched.value = false;
  clearWorkspaceToast();
  workspaceModalMode.value = "edit";
  showWorkspaceModal.value = true;
}

function closeWorkspaceModal() {
  showWorkspaceModal.value = false;
  restoreLastFocus();
}

function restoreLastFocus() {
  void nextTick(() => {
    restoreFocus(lastFocusBeforeModal.value);
    lastFocusBeforeModal.value = null;
  });
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
    showWorkspaceToast(workspaceFormErrors.value[0] || t("workspaces.formIncomplete"), "error");
    return;
  }
  workspaceSaving.value = true;
  try {
    if (workspaceModalMode.value === "edit") {
      const updated = await workspaces.updateWorkspace(draftWorkspace.value.id, draftWorkspace.value);
      await refreshWorkspaceCatalogAndPage();
      setWorkspaceDetailIfVisible(updated);
      showWorkspaceToast(t("workspaces.saveOk", { name: updated.name }));
    } else {
      const created = await workspaces.createWorkspace(draftWorkspace.value);
      await Promise.all([agents.loadAgents({ workspaceId: created.id }), refreshWorkspaceCatalogAndPage({ page: 1 })]);
      setWorkspaceDetailIfVisible(created);
      showWorkspaceToast(t("workspaces.createOk", { name: created.name }));
    }
    closeWorkspaceModal();
  } finally {
    workspaceSaving.value = false;
  }
}

/** List-row menu: view → edit → lifecycle → danger (ZKL-33 menu-only). */
function workspaceMenuActions(workspace: Workspace): ManagementRowAction[] {
  const actions: ManagementRowAction[] = [
    { key: "view", label: t("workspaces.viewDetails"), icon: "fa-solid fa-eye", tone: "primary" },
  ];
  if (workspaces.can(workspace.id, "EDIT")) {
    actions.push({ key: "edit", label: t("workspaces.editInfo"), icon: "fa-solid fa-pen" });
  }
  if (workspaces.can(workspace.id, "MANAGE")) {
    actions.push({
      key: "toggle",
      label: workspace.status === "ACTIVE" ? t("workspaces.disableSpace") : t("workspaces.enableSpace"),
      icon: workspace.status === "ACTIVE" ? "fa-solid fa-power-off" : "fa-solid fa-circle-play",
    });
  }
  if (workspaces.can(workspace.id, "DELETE")) {
    actions.push({ key: "delete", label: t("workspaces.deleteSpace"), icon: "fa-solid fa-trash", tone: "danger" });
  }
  return actions;
}

/** Detail header keeps copy/lifecycle/danger overflow (page-header actions stay outside scope). */
function workspaceDetailMenuActions(workspace: Workspace): ManagementRowAction[] {
  return [
    { key: "copy-id", label: t("workspaces.copyId"), icon: "fa-regular fa-copy" },
    ...workspaceMenuActions(workspace).filter((action) => action.key === "toggle" || action.key === "delete"),
  ];
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
  else if (action === "copy-id") void copyWorkspaceId(workspace);
}

async function toggleWorkspace(workspace: Workspace) {
  lastFocusBeforeModal.value = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  workspaceStatusTarget.value = workspace;
  clearWorkspaceToast();
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
    if (workspace.status === "ACTIVE") {
      const updated = await workspaces.disableWorkspace(workspace.id);
      showWorkspaceToast(t("workspaces.disabledOk", { name: updated.name }));
    } else {
      const updated = await workspaces.enableWorkspace(workspace.id);
      showWorkspaceToast(t("workspaces.enabledOk", { name: updated.name }));
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
    showWorkspaceToast(t("workspaces.deletedOk", { name: workspace.name }));
    closeWorkspaceDeleteConfirm();
  } finally {
    workspaceDeleteSaving.value = false;
  }
}

function goLogin() {
  auth.clearSession();
  void router.push({ name: "login" });
}

function statusTone(status: string) {
  return status.toLowerCase();
}

function displayWorkspaceStatus(status: Workspace["status"]) {
  return status === "ACTIVE" ? t("workspaces.online") : t("workspaces.offline");
}

function displayWorkspaceMode(mode: Workspace["mode"]) {
  return isProductionWorkspaceMode(mode) ? t("workspaces.prod") : t("workspaces.sandbox");
}

function modeTone(mode: Workspace["mode"]) {
  return mode.toLowerCase();
}

function getDefaultAgentLabel(workspace: Workspace) {
  return (
    agents.items.find((agent) => agent.id === workspace.defaultAgentId)?.name ||
    workspace.defaultAgentId ||
    t("workspaces.noDefaultAgent")
  );
}

function formatWorkspaceUpdatedAt(workspace: Workspace) {
  if (!workspace.updatedAt) return "-";
  const timestamp = Date.parse(workspace.updatedAt);
  if (!Number.isFinite(timestamp)) return workspace.updatedAt;
  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60_000));
  if (elapsedMinutes < 1) return t("workspaces.justNow");
  if (elapsedMinutes < 60) return t("workspaces.minutesAgo", { n: elapsedMinutes });
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) return t("workspaces.hoursAgo", { n: elapsedHours });
  return t("workspaces.daysAgo", { n: Math.floor(elapsedHours / 24) });
}

async function copyWorkspaceId(workspace = detailWorkspace.value) {
  if (!workspace) return;
  try {
    await navigator.clipboard?.writeText(workspace.id);
    showWorkspaceToast(t("workspaces.idCopied", { name: workspace.name }));
  } catch {
    showWorkspaceToast(t("workspaces.copyFailed"), "error");
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
      'management-page-grid': !workspacePageError && !detailWorkspace,
    }"
    v-loading="pageInitialLoading"
  >
    <ManagementPageHeader
      v-if="!detailWorkspace"
      class="span-12"
      :title="t('workspaces.title')"
      :description="t('workspaces.subtitle')"
      :eyebrow="t('nav.section.space')"
      icon="fa-solid fa-layer-group"
    >
      <template #actions>
        <button class="primary-button" type="button" @click="openCreateWorkspace">
          <i class="fa-solid fa-circle-plus" aria-hidden="true" />
          {{ t("workspaces.create") }}
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip
      v-if="!workspacePageError && !detailWorkspace"
      class="span-12"
      :items="workspaceSummaryItems"
    />

    <section v-if="workspacePageError" class="workspace-load-error span-12" role="alert">
      <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
      <div>
        <span>{{ t("workspaces.loadFailedTitle") }}</span>
        <h3>{{ workspacePageError }}</h3>
        <p>{{ t("workspaces.errorEmptyHint") }}</p>
      </div>
      <div class="workspace-load-error-actions">
        <button class="ghost-button" type="button" :disabled="pageInitialLoading" @click="reloadWorkspacePage">
          {{ pageInitialLoading ? t("workspaces.retrying") : t("workspaces.reload") }}
        </button>
        <button
          v-if="
            workspacePageError.includes(t('workspaces.sessionMarker')) ||
            workspacePageError.includes('session') ||
            workspacePageError.includes('Session')
          "
          class="primary-button"
          type="button"
          @click="goLogin"
        >
          {{ t("workspaces.relogin") }}
        </button>
      </div>
    </section>

    <template v-else>
      <section v-if="!detailWorkspace" class="workspace-list-card management-list-card span-12">
        <header v-if="hasWorkspaceRecords" class="workspace-resource-mode-bar">
          <div>
            <span>{{ t("workspaces.listManage") }}</span>
            <strong>{{ t("workspaces.matchCount", { n: workspaces.pagination.total }) }}</strong>
            <small>{{ t("workspaces.fullCatalogHint", { n: workspaces.items.length }) }}</small>
          </div>
          <p class="workspace-narrow-notice">{{ t("workspaces.narrowNotice") }}</p>
        </header>

        <p v-if="workspaceDetailFilteredOut" class="workspace-filter-context-note">
          {{ t("workspaces.detailCleared") }}
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
          :search-placeholder="t('workspaces.searchPlaceholder')"
          :search-aria-label="t('workspaces.searchAria')"
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
              @click="bulkSetSelectedWorkspaceStatus('ACTIVE')"
            >
              {{ t("workspaces.batchEnable") }}
            </button>
            <button
              class="workspace-batch-action is-danger"
              type="button"
              data-action="bulk-disable"
              :disabled="workspaceStatusSaving"
              @click="bulkSetSelectedWorkspaceStatus('DISABLED')"
            >
              {{ t("workspaces.batchDisable") }}
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
              :ariaLabel="t('workspaces.modeFilterAria')"
              @update:model-value="setWorkspaceModeFilter($event as WorkspaceModeFilter)"
            />
            <ManagementSegmentedFilter
              :model-value="statusFilter"
              :options="workspaceFilterOptions"
              :ariaLabel="t('workspaces.statusFilterAria')"
              @update:model-value="setWorkspaceStatusFilter($event as WorkspaceStatusFilter)"
            />
          </template>

          <template #cell-selection="{ row: workspace }">
            <label v-if="workspaces.can(workspace.id, 'MANAGE')" class="workspace-checkbox-hitarea" @click.stop>
              <input
                type="checkbox"
                :checked="selectedWorkspaceIds.has(workspace.id)"
                :aria-label="t('workspaces.selectWorkspace', { name: workspace.name })"
                @change="toggleWorkspaceSelection(workspace, ($event.target as HTMLInputElement).checked)"
              />
            </label>
          </template>
          <template #cell-identity="{ row: workspace }">
            <button
              type="button"
              class="workspace-resource-name workspace-identity-button"
              :data-workspace-detail-id="workspace.id"
              :aria-label="t('workspaces.viewWorkspaceDetail', { name: workspace.displayName || workspace.name })"
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
              displayWorkspaceMode(workspace.mode)
            }}</span>
          </template>
          <template #cell-defaultAgent="{ row: workspace }">
            <span class="workspace-agent-chip aw-table-pill" :title="getDefaultAgentLabel(workspace)"
              ><i class="fa-solid fa-user-gear" aria-hidden="true" />{{ getDefaultAgentLabel(workspace) }}</span
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
            <div v-if="!hasWorkspaceRecords" class="empty-state registry-empty-state management-registry-empty-state">
              <div class="management-empty-state-icon">
                <i class="fa-solid fa-layer-group" aria-hidden="true" />
              </div>
              <h2>{{ t("workspaces.emptyTitle") }}</h2>
              <p>
                {{ t("workspaces.emptyBody") }}
              </p>
              <button class="primary-button" type="button" @click="openCreateWorkspace">
                {{ t("workspaces.createShort") }}
              </button>
            </div>
            <div v-else class="empty-state registry-empty-state management-registry-empty-state">
              <div class="management-empty-state-icon">
                <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              </div>
              <h2>{{ t("workspaces.noMatchTitle") }}</h2>
              <p>{{ t("workspaces.noMatchHint") }}</p>
              <button class="ghost-button" type="button" @click="clearWorkspaceFilters">
                {{ t("workspaces.resetFilters") }}
              </button>
            </div>
          </template>
          <template #error>
            <div class="workspace-load-error" role="alert">
              <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
              <span>{{ workspaces.pageError || t("workspaces.listLoadFailed") }}</span>
              <button class="ghost-button" type="button" @click="loadWorkspaceRegistry()">
                {{ t("workspaces.retry") }}
              </button>
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
              {{ t("workspaces.backToList") }}
            </button>
            <div class="workspace-detail-heading">
              <span class="workspace-detail-heading-icon"><i class="fa-solid fa-cube" aria-hidden="true" /></span>
              <div>
                <span>{{ t("workspaces.detailEyebrow") }}</span>
                <h2 id="workspace-detail-title">{{ detailWorkspace.displayName || detailWorkspace.name }}</h2>
                <div class="workspace-detail-heading-meta">
                  <p>{{ detailWorkspace.name }}</p>
                  <span :class="['status-pill', statusTone(detailWorkspace.status)]">
                    <i class="workspace-status-dot" aria-hidden="true" />
                    {{ displayWorkspaceStatus(detailWorkspace.status) }}
                  </span>
                  <span :class="['workspace-mode-pill', modeTone(detailWorkspace.mode)]">{{
                    displayWorkspaceMode(detailWorkspace.mode)
                  }}</span>
                </div>
              </div>
            </div>
          </div>
          <div class="workspace-detail-page-actions">
            <button
              v-if="workspaces.can(detailWorkspace.id, 'EDIT')"
              type="button"
              class="primary-button workspace-detail-edit"
              @click="openEditWorkspace(detailWorkspace)"
            >
              <i class="fa-solid fa-sliders" aria-hidden="true" />
              {{ t("workspaces.editSpace") }}
            </button>
            <ManagementRowActions
              v-if="workspaceDetailMenuActions(detailWorkspace).length"
              class="workspace-detail-more-actions"
              :menu-actions="workspaceDetailMenuActions(detailWorkspace)"
              @action="handleWorkspaceRowAction($event, detailWorkspace)"
            />
          </div>
        </header>

        <nav class="workspace-detail-tabs" role="tablist" :aria-label="t('workspaces.detailTabsAria')">
          <button
            id="workspace-detail-tab-overview"
            type="button"
            role="tab"
            :aria-selected="workspaceDetailTab === 'overview'"
            aria-controls="workspace-detail-panel-overview"
            :class="{ active: workspaceDetailTab === 'overview' }"
            @click="selectWorkspaceDetailTab('overview')"
          >
            <i class="fa-solid fa-chart-pie" aria-hidden="true" />{{ t("workspaces.tabOverview") }}
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
            <i class="fa-solid fa-user-group" aria-hidden="true" />{{ t("workspaces.tabMembers") }}
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
                <span><i class="fa-solid fa-user-group" aria-hidden="true" />{{ t("workspaces.membersTitle") }}</span
                ><strong>{{ workspaceMembers(detailWorkspace.id).length }}</strong
                ><small>{{ t("workspaces.membersSubtitle") }}</small>
              </article>
              <article>
                <span><i class="fa-solid fa-robot" aria-hidden="true" />{{ t("workspaces.collabAgents") }}</span
                ><strong>{{ workspaceAgentCount }}</strong
                ><small>{{ t("workspaces.collabAgentsSub") }}</small>
              </article>
              <article>
                <span><i class="fa-solid fa-shield-halved" aria-hidden="true" />{{ t("workspaces.isolationEnv") }}</span
                ><strong>{{ displayWorkspaceMode(detailWorkspace.mode) }}</strong>
                ><small>{{ t("workspaces.isolationHint") }}</small>
              </article>
            </div>

            <div class="workspace-detail-overview-grid">
              <article class="workspace-detail-content-card">
                <header class="workspace-detail-section-title">
                  <span><i class="fa-solid fa-fingerprint" aria-hidden="true" /></span>
                  <div>
                    <strong>{{ t("workspaces.identityTitle") }}</strong
                    ><small>{{ t("workspaces.identitySub") }}</small>
                  </div>
                </header>
                <dl class="workspace-detail-metadata">
                  <div>
                    <dt>{{ t("workspaces.nameEn") }}</dt>
                    <dd :title="detailWorkspace.name">{{ detailWorkspace.name }}</dd>
                  </div>
                  <div>
                    <dt>{{ t("workspaces.envMode") }}</dt>
                    <dd>{{ displayWorkspaceMode(detailWorkspace.mode) }}</dd>
                  </div>
                  <div>
                    <dt>{{ t("workspaces.creator") }}</dt>
                    <dd :title="workspaceActorTitle(detailWorkspace, 'created')">
                      {{ workspaceActorLabel(detailWorkspace, "created") }}
                    </dd>
                  </div>
                  <div>
                    <dt>{{ t("workspaces.lastUpdater") }}</dt>
                    <dd :title="workspaceActorTitle(detailWorkspace, 'updated')">
                      {{ workspaceActorLabel(detailWorkspace, "updated") }}
                    </dd>
                  </div>
                </dl>
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
                <div>
                  <strong>{{ t("workspaces.membersRolesTitle") }}</strong
                  ><small>{{ t("workspaces.membersRolesSub") }}</small>
                </div>
                <em>{{ t("workspaces.memberCount", { n: workspaceMembers(detailWorkspace.id).length }) }}</em>
              </header>

              <div v-if="workspaces.can(detailWorkspace.id, 'MANAGE')" class="workspace-member-add">
                <div class="workspace-member-picker">
                  <label for="workspace-member-search-input">{{ t("workspaces.searchUsersLabel") }}</label>
                  <div class="workspace-member-search">
                    <input
                      id="workspace-member-search-input"
                      v-model.trim="memberCandidateQuery"
                      :aria-label="t('workspaces.searchCandidates')"
                      :placeholder="t('workspaces.searchUsersPh')"
                      @keyup.enter="loadMemberCandidates(detailWorkspace.id)"
                    />
                    <button
                      type="button"
                      class="icon-action-button"
                      :aria-label="t('workspaces.searchUsers')"
                      :disabled="memberCandidatesLoading"
                      @click="loadMemberCandidates(detailWorkspace.id)"
                    >
                      <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
                    </button>
                  </div>
                  <label class="workspace-member-candidate-select">
                    <span>{{ t("workspaces.candidateUsers") }}</span>
                    <AppSelect
                      v-model="newMemberUserId"
                      :options="memberCandidateOptions"
                      :placeholder="
                        memberCandidatesLoading ? t('workspaces.loadingCandidates') : t('workspaces.selectUserToAdd')
                      "
                      :disabled="memberCandidatesLoading"
                      filterable
                      :aria-label="t('workspaces.selectMember')"
                    />
                  </label>
                </div>
                <label class="workspace-member-role-field">
                  <span>{{ t("workspaces.spaceRole") }}</span>
                  <AppSelect
                    v-model="newMemberRole"
                    :options="assignableWorkspaceRoleOptions"
                    :aria-label="t('workspaces.newMemberRole')"
                  />
                </label>
                <button
                  type="button"
                  class="primary-button workspace-member-add-button"
                  :disabled="!newMemberUserId"
                  @click="addWorkspaceMember(detailWorkspace.id)"
                >
                  <i class="fa-solid fa-user-plus" aria-hidden="true" />{{ t("workspaces.addMember") }}
                </button>
                <small
                  v-if="memberCandidatesError"
                  class="workspace-member-hint workspace-member-candidate-error"
                  role="alert"
                  >{{ memberCandidatesError }}</small
                >
                <small v-else-if="!memberCandidatesLoading && !memberCandidates.length" class="workspace-member-hint">{{
                  t("workspaces.noActiveUsers")
                }}</small>
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
                      ><small>{{ workspaceRoleLabel(member.role) }}</small></span
                    >
                  </span>
                  <label>
                    <AppSelect
                      :model-value="member.role"
                      :options="workspaceRoleOptions"
                      :aria-label="t('workspaces.memberRole')"
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
                    :aria-label="t('workspaces.removeMember')"
                    @click="removeWorkspaceMember(detailWorkspace.id, member.userId)"
                  >
                    <i class="fa-solid fa-user-minus" aria-hidden="true" />
                  </button>
                </div>
                <div v-if="!workspaceMembers(detailWorkspace.id).length" class="workspace-detail-empty-state">
                  <i class="fa-solid fa-user-group" aria-hidden="true" />
                  <strong>{{ t("workspaces.noMembers") }}</strong>
                  <span>{{ t("workspaces.noMembersHint") }}</span>
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
                  <strong>{{ t("workspaces.agentsInSpaceTitle") }}</strong
                  ><small>{{ t("workspaces.agentsInSpaceSub") }}</small>
                </div>
                <em>{{ t("workspaces.activeAgentCount", { n: workspaceAgentCount }) }}</em>
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
                  <em v-if="agent.isDefault" class="workspace-detail-agent-default">{{
                    t("workspaces.defaultAgentBadge")
                  }}</em>
                </div>
                <div v-if="!displayedWorkspaceAgents.length" class="workspace-detail-empty-state">
                  <i class="fa-solid fa-robot" aria-hidden="true" />
                  <strong>{{ t("workspaces.noActiveAgents") }}</strong>
                  <span>{{ t("workspaces.noActiveAgentsHint") }}</span>
                </div>
              </div>
            </article>
          </section>
        </div>
      </section>
    </template>
    <ManagementDialog
      :open="showWorkspaceModal"
      :title="workspaceModalTitle"
      :eyebrow="workspaceModalMode === 'create' ? t('common.dialogCreate') : t('common.dialogEdit')"
      icon="fa-solid fa-layer-group"
      :close-aria-label="t('workspaces.closeFormAria')"
      :close-disabled="workspaceSaving"
      :close-on-backdrop="false"
      testid="workspace-modal-backdrop"
      card-class="workspace-modal-card"
      footer-class="workspace-modal-actions"
      @close="closeWorkspaceModal"
    >
      <label class="modal-field">
        <span>{{ t("workspaces.nameEn") }} <em :aria-label="t('workspaces.required')">*</em></span>
        <input
          v-model.trim="draftWorkspace.name"
          ref="workspaceNameInputRef"
          :data-modal-initial-focus="workspaceModalMode === 'create' ? true : undefined"
          :placeholder="t('workspaces.nameExamplePh')"
          :disabled="workspaceModalMode === 'edit'"
          :aria-invalid="workspaceNameInvalid"
          aria-describedby="workspace-name-helper"
          required
          @blur="workspaceFormTouched = true"
        />
        <small id="workspace-name-helper">{{ t("workspaces.nameHelp") }}</small>
        <small v-if="workspaceNameInvalid" class="field-error">{{ workspaceNameError }}</small>
      </label>
      <label class="modal-field">
        <span>{{ t("workspaces.displayName") }} <em :aria-label="t('workspaces.required')">*</em></span>
        <input
          v-model.trim="draftWorkspace.displayName"
          :data-modal-initial-focus="workspaceModalMode === 'edit' ? true : undefined"
          :placeholder="t('workspaces.displayNamePlaceholder')"
          :aria-invalid="workspaceDisplayNameInvalid"
          required
          @blur="workspaceFormTouched = true"
        />
        <small v-if="workspaceDisplayNameInvalid" class="field-error">{{ workspaceDisplayNameError }}</small>
      </label>
      <div class="modal-field">
        <span>{{ t("workspaces.envMode") }}</span>
        <div class="workspace-modal-segment" role="radiogroup" :aria-label="t('workspaces.envModeAria')">
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
            {{ option.value === "SANDBOX" ? t("workspaces.modeSandboxLabel") : t("workspaces.modeProductionLabel") }}
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
        {{ workspaceModalMode === "create" ? t("workspaces.createHint") : t("workspaces.saveAffectsConfig") }}
      </p>
      <template #footer>
        <button class="ghost-button" type="button" :disabled="workspaceSaving" @click="closeWorkspaceModal">
          {{ t("workspaces.cancel") }}
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
                ? t("workspaces.creating")
                : t("workspaces.saving")
              : workspaceModalMode === "create"
                ? t("workspaces.submitCreate")
                : t("workspaces.saveChanges")
          }}
        </button>
      </template>
    </ManagementDialog>

    <ManagementDialog
      :open="Boolean(workspaceStatusTarget)"
      :title="pendingWorkspaceStatusAction === 'disable' ? t('workspaces.disableTitle') : t('workspaces.enableTitle')"
      :eyebrow="t('common.dialogStatus')"
      :icon="pendingWorkspaceStatusAction === 'disable' ? 'fa-solid fa-circle-pause' : 'fa-solid fa-circle-play'"
      :tone="pendingWorkspaceStatusAction === 'disable' ? 'danger' : 'default'"
      size="sm"
      :aria-label="t('workspaces.statusConfirmAria')"
      :close-aria-label="t('workspaces.closeStatusConfirm')"
      :close-disabled="workspaceStatusSaving"
      card-class="workspace-confirm-card"
      footer-class="workspace-modal-actions"
      @close="closeWorkspaceStatusConfirm"
    >
      <div v-if="workspaceStatusTarget" class="workspace-confirm-body">
        <div>
          <strong>{{ workspaceStatusTarget.name }}</strong>
          <p v-if="pendingWorkspaceStatusAction === 'disable'">
            {{ t("workspaces.disableBodyLong") }}
          </p>
          <p v-else>{{ t("workspaces.enableBodyLong") }}</p>
          <ul class="workspace-lifecycle-effects">
            <li>{{ t("workspaces.statusBullet1") }}</li>
            <li>{{ t("workspaces.statusBullet2") }}</li>
            <li>{{ t("workspaces.statusBullet3") }}</li>
          </ul>
        </div>
      </div>
      <template #footer>
        <button
          class="ghost-button"
          type="button"
          data-modal-initial-focus
          :disabled="workspaceStatusSaving"
          @click="closeWorkspaceStatusConfirm"
        >
          {{ t("workspaces.cancel") }}
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
              ? t("workspaces.processing")
              : pendingWorkspaceStatusAction === "disable"
                ? t("workspaces.confirmDisable")
                : t("workspaces.confirmEnable")
          }}
        </button>
      </template>
    </ManagementDialog>

    <ManagementDialog
      :open="Boolean(workspaceDeleteTarget)"
      :title="t('workspaces.deleteTitle')"
      :eyebrow="t('common.dialogDanger')"
      icon="fa-solid fa-triangle-exclamation"
      tone="danger"
      size="sm"
      :aria-label="t('workspaces.deleteConfirmAria')"
      :close-aria-label="t('workspaces.closeDeleteConfirm')"
      :close-disabled="workspaceDeleteSaving"
      card-class="workspace-confirm-card"
      footer-class="workspace-modal-actions"
      @close="closeWorkspaceDeleteConfirm"
    >
      <div v-if="workspaceDeleteTarget" class="workspace-confirm-body">
        <div>
          <strong>{{ workspaceDeleteTarget.name }}</strong>
          <p>{{ t("workspaces.deleteBodyLong") }}</p>
        </div>
      </div>
      <label v-if="workspaceDeleteTarget" class="modal-field workspace-confirm-input">
        <span>{{ t("workspaces.typeNameConfirm", { name: workspaceDeleteTarget.name }) }}</span>
        <input
          ref="workspaceDeleteInputRef"
          v-model.trim="workspaceDeleteConfirmName"
          data-modal-initial-focus
          autocomplete="off"
          :aria-invalid="workspaceDeleteConfirmName.length > 0 && !canConfirmWorkspaceDelete"
          aria-describedby="workspace-delete-name-helper workspace-delete-name-error"
        />
        <small id="workspace-delete-name-helper">{{ t("workspaces.exactNameMatch") }}</small>
        <small v-if="workspaceDeleteNameError" id="workspace-delete-name-error" class="field-error">{{
          workspaceDeleteNameError
        }}</small>
      </label>
      <template #footer>
        <button
          class="ghost-button"
          type="button"
          :disabled="workspaceDeleteSaving"
          @click="closeWorkspaceDeleteConfirm"
        >
          {{ t("workspaces.cancel") }}
        </button>
        <button
          class="primary-button danger"
          type="button"
          :disabled="workspaceDeleteSaving || !canConfirmWorkspaceDelete"
          @click="confirmDeleteWorkspace"
        >
          <i class="fa-solid fa-trash" aria-hidden="true" />
          {{ workspaceDeleteSaving ? t("workspaces.deleting") : t("workspaces.deleteSpace") }}
        </button>
      </template>
    </ManagementDialog>

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
      <button type="button" :aria-label="t('workspaces.closeToast')" @click="clearWorkspaceToast">
        <i class="fa-solid fa-xmark" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
