<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";

import AppSelect, { type AppSelectOption } from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { useUserStore, type CreateUserInput, type UpdateUserProfileInput } from "../stores/users";
import type { PlatformRole, User, UserStatus } from "../types/domain";

const users = useUserStore();
const filters = reactive({ query: "", status: "" as UserStatus | "", platformRole: "" as PlatformRole | "" });
const userSummaryItems = computed<ManagementSummaryItem[]>(() => [
  { label: "用户总数", value: users.pagination.total, icon: "fa-solid fa-users" },
  { label: "当前页有效用户", value: users.activeUsers.length, icon: "fa-solid fa-user-check" },
  {
    label: "当前页平台管理员",
    value: users.items.filter((user) => user.platformRole === "PLATFORM_ADMIN").length,
    icon: "fa-solid fa-user-shield",
    tone: "info",
  },
  { label: "当前页用户", value: users.items.length, icon: "fa-solid fa-list" },
]);
const createVisible = ref(false);
const profileUser = ref<User | null>(null);
const workspaceUser = ref<User | null>(null);
const resetUser = ref<User | null>(null);
const pendingAction = ref<{ kind: "role" | "status" | "unlock"; user: User; value?: string } | null>(null);
const feedback = ref("");
const feedbackTone = ref<"success" | "error">("success");
const temporaryPassword = ref("");
const createDraft = reactive<CreateUserInput>({
  username: "",
  email: "",
  displayName: "",
  password: "",
  platformRole: "USER",
  locale: "zh-CN",
  timezone: "Asia/Singapore",
});
const profileDraft = reactive<UpdateUserProfileInput>({ displayName: "", email: "", locale: "zh-CN", timezone: "Asia/Singapore" });

const userColumns: ManagementListColumn<User>[] = [
  { key: "identity", label: "用户", width: 250, getValue: (user) => `${user.displayName} ${user.username} ${user.email || ""}` },
  { key: "status", label: "状态", width: 116, getValue: (user) => user.status },
  { key: "platformRole", label: "平台角色", width: 150, getValue: (user) => user.platformRole },
  { key: "locale", label: "语言 / 时区", width: 170, getValue: (user) => `${user.locale} ${user.timezone}` },
  { key: "lastLoginAt", label: "最近登录", width: 190, getValue: (user) => user.lastLoginAt || "" },
  { key: "actions", label: "操作", width: 68, align: "right", headerAlign: "center" },
];
const statusFilterOptions: AppSelectOption[] = [
  { label: "全部状态", value: "" },
  { label: "正常", value: "ACTIVE" },
  { label: "已锁定", value: "LOCKED" },
  { label: "已停用", value: "DISABLED" },
];
const platformRoleFilterOptions: AppSelectOption[] = [
  { label: "全部角色", value: "" },
  { label: "平台管理员", value: "PLATFORM_ADMIN" },
  { label: "普通用户", value: "USER" },
];
const platformRoleOptions: AppSelectOption[] = platformRoleFilterOptions.slice(1);
const localeOptions: AppSelectOption[] = [
  { label: "简体中文（zh-CN）", value: "zh-CN" },
  { label: "繁體中文（zh-TW）", value: "zh-TW" },
  { label: "English (Singapore)（en-SG）", value: "en-SG" },
  { label: "English (US)（en-US）", value: "en-US" },
  { label: "English (UK)（en-GB）", value: "en-GB" },
  { label: "日本語（ja-JP）", value: "ja-JP" },
  { label: "한국어（ko-KR）", value: "ko-KR" },
];
const timezoneOptions: AppSelectOption[] = supportedTimezoneValues().map((timezone) => ({ label: timezone, value: timezone }));
const createLocaleOptions = computed(() => optionsWithCurrent(localeOptions, createDraft.locale));
const createTimezoneOptions = computed(() => optionsWithCurrent(timezoneOptions, createDraft.timezone));
const profileLocaleOptions = computed(() => optionsWithCurrent(localeOptions, profileDraft.locale));
const profileTimezoneOptions = computed(() => optionsWithCurrent(timezoneOptions, profileDraft.timezone));
const createValidationIssues = computed(() => {
  const missingFields: string[] = [];
  if (!createDraft.username.trim()) missingFields.push("用户名");
  if (!createDraft.displayName.trim()) missingFields.push("显示名称");
  if (!createDraft.password) missingFields.push("临时密码");
  if (!createDraft.platformRole) missingFields.push("平台角色");
  if (!createDraft.locale.trim()) missingFields.push("语言");
  if (!createDraft.timezone.trim()) missingFields.push("时区");

  if (missingFields.length) return [`请填写必填项：${formatChineseList(missingFields)}。`];
  if (createDraft.password.length < 12) {
    return [`临时密码至少需要 12 位（当前 ${createDraft.password.length} 位）。`];
  }
  return [];
});
const canCreate = computed(() => createValidationIssues.value.length === 0);
const createDisabledReason = computed(() => {
  if (users.actionLoading) return "正在创建用户，请稍候。";
  return createValidationIssues.value[0] || "";
});
const canSaveProfile = computed(() => Boolean(profileUser.value && profileDraft.displayName?.trim() && profileDraft.locale?.trim() && profileDraft.timezone?.trim()));
const pendingActionUsesDangerTone = computed(() => {
  const action = pendingAction.value;
  return Boolean(action && (action.kind === "role" || (action.kind === "status" && action.value === "DISABLED")));
});

function supportedTimezoneValues() {
  const fallback = [
    "Asia/Singapore", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "Asia/Seoul",
    "Europe/London", "Europe/Paris", "America/New_York", "America/Los_Angeles", "Australia/Sydney",
  ];
  const supported = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : fallback;
  return Array.from(new Set(["UTC", ...supported]));
}

function optionsWithCurrent(options: AppSelectOption[], current: string | undefined) {
  const value = current?.trim();
  if (!value || options.some((option) => option.value === value)) return options;
  return [{ label: value, value }, ...options];
}

function formatChineseList(items: string[]) {
  if (items.length <= 1) return items[0] || "";
  return `${items.slice(0, -1).join("、")}和${items.at(-1)}`;
}
const pendingActionTitle = computed(() => {
  const action = pendingAction.value;
  if (!action) return "确认用户操作";
  if (action.kind === "role") return action.value === "PLATFORM_ADMIN" ? "授予平台管理员" : "移除平台管理员";
  if (action.kind === "unlock") return "解锁用户";
  return action.value === "ACTIVE" ? "启用用户" : "停用用户";
});
const pendingActionDescription = computed(() => {
  const action = pendingAction.value;
  if (!action) return "";
  if (action.kind === "role") {
    return action.value === "PLATFORM_ADMIN"
      ? `将 ${action.user.username} 提升为平台管理员，并撤销其现有登录会话。`
      : `将 ${action.user.username} 降为普通用户。最后一个有效平台管理员不能被降级。`;
  }
  if (action.kind === "unlock") return `清除 ${action.user.username} 的账号锁定和登录失败计数。`;
  return action.value === "ACTIVE"
    ? `重新允许 ${action.user.username} 登录平台。`
    : `禁止 ${action.user.username} 登录并撤销其现有登录会话。`;
});

onMounted(() => void loadUsers());

async function loadUsers(page = users.pagination.page, pageSize = users.pagination.pageSize) {
  clearFeedback();
  try {
    await users.loadUsers({
      query: filters.query,
      status: filters.status || undefined,
      platformRole: filters.platformRole || undefined,
      page,
      pageSize,
    });
  } catch {
    showStoreError("加载用户失败");
  }
}

function resetFilters() {
  filters.query = "";
  filters.status = "";
  filters.platformRole = "";
  void loadUsers(1);
}

function setUserSearch(value: string) {
  filters.query = value;
  void loadUsers(1);
}

function setStatusFilter(value: string | number | boolean) {
  filters.status = value as UserStatus | "";
  void loadUsers(1);
}

function setPlatformRoleFilter(value: string | number | boolean) {
  filters.platformRole = value as PlatformRole | "";
  void loadUsers(1);
}

function changeUserPage(pagination: { page: number; pageSize: number }) {
  void loadUsers(pagination.page, pagination.pageSize);
}

function openCreate() {
  Object.assign(createDraft, {
    username: "", email: "", displayName: "", password: "",
    platformRole: "USER", locale: "zh-CN", timezone: "Asia/Singapore",
  });
  createVisible.value = true;
  clearFeedback();
}

async function submitCreate() {
  if (!canCreate.value) return;
  try {
    const created = await users.createUser({ ...createDraft, email: createDraft.email?.trim() || undefined });
    createVisible.value = false;
    showFeedback(`${created.username} 已创建，首次登录必须修改密码。`);
    await loadUsers(1);
  } catch {
    showStoreError("创建用户失败");
  }
}

function openProfile(user: User) {
  profileUser.value = user;
  Object.assign(profileDraft, {
    displayName: user.displayName,
    email: user.email || "",
    locale: user.locale,
    timezone: user.timezone,
  });
  clearFeedback();
}

async function saveProfile() {
  if (!profileUser.value || !canSaveProfile.value) return;
  try {
    const updated = await users.updateProfile(profileUser.value, {
      ...profileDraft,
      email: profileDraft.email?.trim() || undefined,
    });
    profileUser.value = null;
    showFeedback(`${updated.username} 的资料已更新。`);
  } catch {
    showStoreError("更新用户资料失败");
  }
}

function requestRoleChange(user: User) {
  pendingAction.value = {
    kind: "role",
    user,
    value: user.platformRole === "PLATFORM_ADMIN" ? "USER" : "PLATFORM_ADMIN",
  };
  clearFeedback();
}

function requestStatusChange(user: User) {
  pendingAction.value = user.status === "LOCKED"
    ? { kind: "unlock", user }
    : { kind: "status", user, value: user.status === "ACTIVE" ? "DISABLED" : "ACTIVE" };
  clearFeedback();
}

async function confirmSecurityAction() {
  const action = pendingAction.value;
  if (!action) return;
  try {
    let updated: User;
    if (action.kind === "role") {
      updated = await users.changePlatformRole(action.user, action.value as PlatformRole);
    } else if (action.kind === "unlock") {
      updated = await users.unlockUser(action.user);
    } else {
      updated = await users.setStatus(action.user, action.value as UserStatus);
    }
    pendingAction.value = null;
    showFeedback(`${updated.username} 的权限状态已更新。`);
  } catch {
    showStoreError("权限操作失败");
  }
}

function openReset(user: User) {
  resetUser.value = user;
  temporaryPassword.value = "";
  clearFeedback();
}

async function submitResetPassword() {
  if (!resetUser.value || temporaryPassword.value.length < 12) return;
  const username = resetUser.value.username;
  try {
    await users.resetPassword(resetUser.value.id, temporaryPassword.value);
    resetUser.value = null;
    temporaryPassword.value = "";
    showFeedback(`${username} 的临时密码已重置，现有会话已撤销。`);
  } catch {
    showStoreError("重置密码失败");
  }
}

async function openWorkspaces(user: User) {
  workspaceUser.value = user;
  clearFeedback();
  try {
    await users.loadUserWorkspaces(user.id, true);
  } catch {
    showStoreError("加载用户的业务空间失败");
  }
}

function showStoreError(fallback: string) {
  const error = users.error;
  feedback.value = error ? `${error.message || fallback}${error.requestId ? `（请求 ID：${error.requestId}）` : ""}` : fallback;
  feedbackTone.value = "error";
}

function showFeedback(message: string) {
  feedback.value = message;
  feedbackTone.value = "success";
}

function clearFeedback() {
  feedback.value = "";
}

function statusLabel(status: UserStatus) {
  return ({ ACTIVE: "正常", LOCKED: "已锁定", DISABLED: "已停用" } as const)[status];
}

function roleLabel(role: PlatformRole) {
  return role === "PLATFORM_ADMIN" ? "平台管理员" : "普通用户";
}

function workspaceRoleLabel(role: string) {
  return ({ OWNER: "所有者", ADMIN: "管理员", EDITOR: "编辑者", OPERATOR: "操作员", VIEWER: "查看者" } as Record<string, string>)[role] || role;
}

function userMenuActions(user: User): ManagementRowAction[] {
  return [
    { key: "profile", label: "编辑用户资料", shortLabel: "编辑资料", icon: "fa-solid fa-user-pen" },
    { key: "workspaces", label: "查看业务空间", shortLabel: "业务空间", icon: "fa-solid fa-layer-group" },
    {
      key: "role",
      label: user.platformRole === "PLATFORM_ADMIN" ? "降为普通用户" : "设为平台管理员",
      icon: "fa-solid fa-user-shield",
      tone: user.platformRole === "PLATFORM_ADMIN" ? "danger" : "primary",
    },
    {
      key: "status",
      label: user.status === "LOCKED" ? "解锁用户" : user.status === "ACTIVE" ? "停用用户" : "启用用户",
      icon: user.status === "LOCKED" ? "fa-solid fa-unlock-keyhole" : "fa-solid fa-power-off",
      tone: user.status === "ACTIVE" ? "danger" : "default",
    },
    { key: "reset", label: "重置密码", icon: "fa-solid fa-key", tone: "danger" },
  ];
}

function handleUserRowAction(action: string, user: User) {
  if (action === "profile") openProfile(user);
  else if (action === "workspaces") void openWorkspaces(user);
  else if (action === "role") requestRoleChange(user);
  else if (action === "status") requestStatusChange(user);
  else if (action === "reset") openReset(user);
}

function formatDate(value?: string) {
  if (!value) return "从未登录";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
</script>

<template>
  <div class="page-grid user-access-grid management-page-grid" v-loading="users.loading">
    <ManagementPageHeader
      class="span-12"
      title="用户与权限"
      description="管理平台用户、平台角色、账号状态与业务空间成员关系。"
      icon="fa-solid fa-users"
      eyebrow="Identity & Access Management"
    >
      <template #actions>
        <button class="primary-button" type="button" @click="openCreate">
          <i class="fa-solid fa-user-plus" aria-hidden="true" />新建用户
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="userSummaryItems" />

    <section class="user-access-panel management-list-card span-12">
      <div v-if="feedback" :class="['user-access-feedback', feedbackTone]" :role="feedbackTone === 'error' ? 'alert' : 'status'">{{ feedback }}</div>

      <ManagementList
        class="user-management-list"
        :rows="users.items"
        :columns="userColumns"
        row-key="id"
        :sticky-left-keys="['identity']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:users:columns"
        :selectable="false"
        :loading="users.loading"
        :error="users.error?.message || null"
        :has-loaded="users.hasLoaded"
        :search="filters.query"
        search-placeholder="搜索用户名、显示名或邮箱..."
        search-aria-label="搜索用户"
        reset-label="清除筛选"
        reset-aria-label="清除用户筛选条件"
        :reset-disabled="!filters.query && !filters.status && !filters.platformRole"
        :pagination="users.pagination"
        @update:search="setUserSearch"
        @reset="resetFilters"
        @page-change="changeUserPage"
      >
        <template #filters>
          <AppSelect
            class="user-management-filter"
            :model-value="filters.status"
            :options="statusFilterOptions"
            placeholder="全部状态"
            aria-label="账号状态筛选"
            @update:model-value="setStatusFilter"
          />
          <AppSelect
            class="user-management-filter"
            :model-value="filters.platformRole"
            :options="platformRoleFilterOptions"
            placeholder="全部角色"
            aria-label="平台角色筛选"
            @update:model-value="setPlatformRoleFilter"
          />
        </template>
        <template #cell-identity="{ row: user }">
          <div class="user-identity-cell">
            <span class="user-identity-avatar" aria-hidden="true">{{ user.displayName.slice(0, 1).toUpperCase() }}</span>
            <span>
              <strong class="aw-table-title">{{ user.displayName }}</strong>
              <small class="aw-table-subtitle">@{{ user.username }} · {{ user.email || "未设置邮箱" }}</small>
            </span>
          </div>
        </template>
        <template #cell-status="{ row: user }">
          <span :class="['user-status-badge', 'aw-table-pill', user.status.toLowerCase()]">{{ statusLabel(user.status) }}</span>
        </template>
        <template #cell-platformRole="{ row: user }">
          <span :class="['user-role-badge', 'aw-table-pill', user.platformRole === 'PLATFORM_ADMIN' && 'admin']">{{ roleLabel(user.platformRole) }}</span>
        </template>
        <template #cell-locale="{ row: user }">
          <span class="user-locale-cell">
            <strong class="aw-table-title">{{ user.locale }}</strong>
            <small class="aw-table-subtitle">{{ user.timezone }}</small>
          </span>
        </template>
        <template #cell-lastLoginAt="{ row: user }">
          <span class="user-last-login aw-table-meta">{{ formatDate(user.lastLoginAt) }}</span>
        </template>
        <template #cell-actions="{ row: user }">
          <ManagementRowActions
            class="user-row-actions"
            :menu-actions="userMenuActions(user)"
            menu-label="用户安全操作"
            @action="handleUserRowAction($event, user)"
          />
        </template>
        <template #empty>
          <div class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-users" aria-hidden="true" /></div>
            <h2>{{ filters.query || filters.status || filters.platformRole ? '没有匹配的用户' : '暂无平台用户' }}</h2>
            <p>{{ filters.query || filters.status || filters.platformRole ? '调整搜索词、账号状态或平台角色后再试。' : '创建用户后可在此维护平台权限与业务空间关系。' }}</p>
            <button v-if="filters.query || filters.status || filters.platformRole" class="ghost-button" type="button" @click="resetFilters">清除筛选</button>
            <button v-else class="primary-button" type="button" @click="openCreate">新建用户</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <div v-if="createVisible" class="modal-backdrop" @click.self="createVisible = false">
      <section class="modal-card user-access-modal" role="dialog" aria-modal="true" aria-label="新建平台用户">
        <header><div><span>CREATE USER</span><h3>新建平台用户</h3></div><button class="icon-action-button" type="button" aria-label="关闭" @click="createVisible = false"><i class="fa-solid fa-xmark" aria-hidden="true" /></button></header>
        <form class="user-access-form" @submit.prevent="submitCreate">
          <label><span>用户名 <span class="user-field-required" aria-hidden="true">*</span></span><input v-model.trim="createDraft.username" required autocomplete="off" /></label>
          <label><span>显示名称 <span class="user-field-required" aria-hidden="true">*</span></span><input v-model.trim="createDraft.displayName" required /></label>
          <label><span>邮箱</span><input v-model.trim="createDraft.email" type="email" /></label>
          <label><span>临时密码 <span class="user-field-required" aria-hidden="true">*</span></span><input v-model="createDraft.password" type="password" minlength="12" required autocomplete="new-password" /><small>至少 12 位；首次登录必须修改。</small></label>
          <label><span>平台角色 <span class="user-field-required" aria-hidden="true">*</span></span><AppSelect v-model="createDraft.platformRole" :options="platformRoleOptions" aria-label="新用户平台角色" :aria-required="true" /></label>
          <label><span>语言 <span class="user-field-required" aria-hidden="true">*</span></span><AppSelect v-model="createDraft.locale" :options="createLocaleOptions" aria-label="新用户语言" :aria-required="true" filterable /></label>
          <label><span>时区 <span class="user-field-required" aria-hidden="true">*</span></span><AppSelect v-model="createDraft.timezone" :options="createTimezoneOptions" aria-label="新用户时区" :aria-required="true" filterable /></label>
          <footer>
            <p
              v-if="createDisabledReason"
              id="create-user-disabled-reason"
              class="user-create-disabled-reason"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            ><i class="fa-solid fa-circle-info" aria-hidden="true" />{{ createDisabledReason }}</p>
            <button class="ghost-button" type="button" @click="createVisible = false">取消</button>
            <button
              class="primary-button"
              type="submit"
              :disabled="!canCreate || users.actionLoading"
              :aria-describedby="createDisabledReason ? 'create-user-disabled-reason' : undefined"
              :aria-busy="users.actionLoading"
            >{{ users.actionLoading ? '创建中…' : '创建用户' }}</button>
          </footer>
        </form>
      </section>
    </div>

    <div v-if="profileUser" class="modal-backdrop" @click.self="profileUser = null">
      <section class="modal-card user-access-modal" role="dialog" aria-modal="true" aria-label="编辑用户资料">
        <header><div><span>PROFILE</span><h3>编辑 {{ profileUser.username }}</h3></div><button class="icon-action-button" type="button" aria-label="关闭" @click="profileUser = null"><i class="fa-solid fa-xmark" aria-hidden="true" /></button></header>
        <form class="user-access-form" @submit.prevent="saveProfile">
          <label><span>显示名称 <span class="user-field-required" aria-hidden="true">*</span></span><input v-model.trim="profileDraft.displayName" required /></label>
          <label><span>邮箱</span><input v-model.trim="profileDraft.email" type="email" /></label>
          <label><span>语言 <span class="user-field-required" aria-hidden="true">*</span></span><AppSelect :model-value="profileDraft.locale || ''" :options="profileLocaleOptions" aria-label="用户语言" :aria-required="true" filterable @update:model-value="profileDraft.locale = String($event)" /></label>
          <label><span>时区 <span class="user-field-required" aria-hidden="true">*</span></span><AppSelect :model-value="profileDraft.timezone || ''" :options="profileTimezoneOptions" aria-label="用户时区" :aria-required="true" filterable @update:model-value="profileDraft.timezone = String($event)" /></label>
          <footer><button class="ghost-button" type="button" @click="profileUser = null">取消</button><button class="primary-button" type="submit" :disabled="!canSaveProfile || users.actionLoading">保存资料</button></footer>
        </form>
      </section>
    </div>

    <div v-if="pendingAction" class="modal-backdrop" @click.self="pendingAction = null">
      <section class="modal-card user-access-confirm" role="dialog" aria-modal="true" :aria-label="pendingActionTitle">
        <span>SECURITY COMMAND</span><h3>{{ pendingActionTitle }}</h3><p>{{ pendingActionDescription }}</p>
        <div v-if="feedbackTone === 'error' && feedback" class="user-access-feedback error" role="alert">{{ feedback }}</div>
        <footer><button class="ghost-button" type="button" @click="pendingAction = null">取消</button><button class="primary-button" :class="{ danger: pendingActionUsesDangerTone }" type="button" :disabled="users.actionLoading" @click="confirmSecurityAction">确认执行</button></footer>
      </section>
    </div>

    <div v-if="resetUser" class="modal-backdrop" @click.self="resetUser = null">
      <section class="modal-card user-access-confirm" role="dialog" aria-modal="true" aria-label="重置用户密码">
        <span>CREDENTIAL RESET</span><h3>重置 {{ resetUser.username }} 的密码</h3><p>提交后会撤销该用户全部登录会话，并要求下次登录修改密码。</p>
        <label><span>临时密码 <span class="user-field-required" aria-hidden="true">*</span></span><input v-model="temporaryPassword" type="password" minlength="12" required autocomplete="new-password" /></label>
        <footer><button class="ghost-button" type="button" @click="resetUser = null">取消</button><button class="primary-button danger" type="button" :disabled="temporaryPassword.length < 12 || users.actionLoading" @click="submitResetPassword">重置密码</button></footer>
      </section>
    </div>

    <div v-if="workspaceUser" class="modal-backdrop" @click.self="workspaceUser = null">
      <section class="modal-card user-access-modal" role="dialog" aria-modal="true" aria-label="用户业务空间">
        <header><div><span>WORKSPACE MEMBERSHIP</span><h3>{{ workspaceUser.username }} 的业务空间</h3></div><button class="icon-action-button" type="button" aria-label="关闭" @click="workspaceUser = null"><i class="fa-solid fa-xmark" aria-hidden="true" /></button></header>
        <div class="user-workspace-list">
          <article v-for="membership in users.membershipsByUser[workspaceUser.id] || []" :key="membership.workspaceId">
            <div><strong>{{ membership.workspaceDisplayName }}</strong><small>{{ membership.workspaceSlug }}</small></div>
            <span class="user-workspace-role">{{ workspaceRoleLabel(membership.role) }}</span><em :class="['user-workspace-status', { disabled: membership.disabledAt || membership.workspaceStatus !== 'ACTIVE' }]">{{ membership.disabledAt ? '成员已停用' : membership.workspaceStatus === 'ACTIVE' ? '空间正常' : '空间停用' }}</em>
          </article>
          <p v-if="!(users.membershipsByUser[workspaceUser.id] || []).length">该用户尚未加入业务空间。</p>
        </div>
      </section>
    </div>
  </div>
</template>
