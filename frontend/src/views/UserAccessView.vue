<script setup lang="ts">
import "./user-access-page.css";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute } from "vue-router";

import AppSelect, { type AppSelectOption } from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSummaryStrip, { type ManagementSummaryItem } from "../components/ManagementSummaryStrip.vue";
import { getI18nLocale } from "../i18n";
import { useUserStore, type CreateUserInput, type UpdateUserProfileInput } from "../stores/users";
import type { PlatformRole, User, UserStatus } from "../types/domain";

const { t } = useI18n();
const users = useUserStore();
const route = useRoute();
const filters = reactive({ query: "", status: "" as UserStatus | "", platformRole: "" as PlatformRole | "" });
const userSummaryItems = computed<ManagementSummaryItem[]>(() => [
  { label: t("users.summaryTotal"), value: users.pagination.total, icon: "fa-solid fa-users" },
  { label: t("users.summaryActivePage"), value: users.activeUsers.length, icon: "fa-solid fa-user-check" },
  {
    label: t("users.summaryAdminsPage"),
    value: users.items.filter((user) => user.platformRole === "PLATFORM_ADMIN").length,
    icon: "fa-solid fa-user-shield",
    tone: "info",
  },
  { label: t("users.summaryPageUsers"), value: users.items.length, icon: "fa-solid fa-list" },
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
const profileDraft = reactive<UpdateUserProfileInput>({
  displayName: "",
  email: "",
  locale: "zh-CN",
  timezone: "Asia/Singapore",
});

const userColumns = computed<ManagementListColumn<User>[]>(() => [
  {
    key: "identity",
    label: t("users.colUser"),
    width: 250,
    getValue: (user) => `${user.displayName} ${user.username} ${user.email || ""}`,
  },
  { key: "status", label: t("users.colStatus"), width: 116, getValue: (user) => user.status },
  { key: "platformRole", label: t("users.colPlatformRole"), width: 150, getValue: (user) => user.platformRole },
  {
    key: "locale",
    label: t("users.colLocaleTimezone"),
    width: 170,
    getValue: (user) => `${user.locale} ${user.timezone}`,
  },
  { key: "lastLoginAt", label: t("users.colLastLogin"), width: 190, getValue: (user) => user.lastLoginAt || "" },
  { key: "actions", label: t("users.colActions"), width: 68, align: "right", headerAlign: "center" },
]);
const statusFilterOptions = computed<AppSelectOption[]>(() => [
  { label: t("users.statusAll"), value: "" },
  { label: t("users.statusActive"), value: "ACTIVE" },
  { label: t("users.statusLocked"), value: "LOCKED" },
  { label: t("users.statusDisabled"), value: "DISABLED" },
]);
const platformRoleFilterOptions = computed<AppSelectOption[]>(() => [
  { label: t("users.roleAll"), value: "" },
  { label: t("users.rolePlatformAdmin"), value: "PLATFORM_ADMIN" },
  { label: t("users.roleUser"), value: "USER" },
]);
const platformRoleOptions = computed(() => platformRoleFilterOptions.value.slice(1));
const localeOptions = computed<AppSelectOption[]>(() => [
  { label: t("users.localeZhCN"), value: "zh-CN" },
  { label: t("users.localeZhTW"), value: "zh-TW" },
  { label: t("users.localeEnSG"), value: "en-SG" },
  { label: t("users.localeEnUS"), value: "en-US" },
  { label: t("users.localeEnGB"), value: "en-GB" },
  { label: t("users.localeJaJP"), value: "ja-JP" },
  { label: t("users.localeKoKR"), value: "ko-KR" },
]);
const timezoneOptions: AppSelectOption[] = supportedTimezoneValues().map((timezone) => ({
  label: timezone,
  value: timezone,
}));
const createLocaleOptions = computed(() => optionsWithCurrent(localeOptions.value, createDraft.locale));
const createTimezoneOptions = computed(() => optionsWithCurrent(timezoneOptions, createDraft.timezone));
const profileLocaleOptions = computed(() => optionsWithCurrent(localeOptions.value, profileDraft.locale));
const profileTimezoneOptions = computed(() => optionsWithCurrent(timezoneOptions, profileDraft.timezone));
const createValidationIssues = computed(() => {
  const missingFields: string[] = [];
  if (!createDraft.username.trim()) missingFields.push(t("users.fieldUsername"));
  if (!createDraft.displayName.trim()) missingFields.push(t("users.fieldDisplayName"));
  if (!createDraft.password) missingFields.push(t("users.fieldTempPassword"));
  if (!createDraft.platformRole) missingFields.push(t("users.fieldPlatformRole"));
  if (!createDraft.locale.trim()) missingFields.push(t("users.fieldLocale"));
  if (!createDraft.timezone.trim()) missingFields.push(t("users.fieldTimezone"));

  if (missingFields.length) return [t("users.fillRequired", { fields: formatFieldList(missingFields) })];
  if (createDraft.password.length < 12) {
    return [t("users.passwordMinLength", { n: createDraft.password.length })];
  }
  return [];
});
const canCreate = computed(() => createValidationIssues.value.length === 0);
const createDisabledReason = computed(() => {
  if (users.actionLoading) return t("users.creatingWait");
  return createValidationIssues.value[0] || "";
});
const canSaveProfile = computed(() =>
  Boolean(
    profileUser.value &&
    profileDraft.displayName?.trim() &&
    profileDraft.locale?.trim() &&
    profileDraft.timezone?.trim(),
  ),
);
const pendingActionUsesDangerTone = computed(() => {
  const action = pendingAction.value;
  return Boolean(action && (action.kind === "role" || (action.kind === "status" && action.value === "DISABLED")));
});

function supportedTimezoneValues() {
  const fallback = [
    "Asia/Singapore",
    "Asia/Shanghai",
    "Asia/Hong_Kong",
    "Asia/Tokyo",
    "Asia/Seoul",
    "Europe/London",
    "Europe/Paris",
    "America/New_York",
    "America/Los_Angeles",
    "Australia/Sydney",
  ];
  const supported = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : fallback;
  return Array.from(new Set(["UTC", ...supported]));
}

function optionsWithCurrent(options: AppSelectOption[], current: string | undefined) {
  const value = current?.trim();
  if (!value || options.some((option) => option.value === value)) return options;
  return [{ label: value, value }, ...options];
}

function formatFieldList(items: string[]) {
  if (items.length <= 1) return items[0] || "";
  try {
    return new Intl.ListFormat(getI18nLocale() === "zh-CN" ? "zh-CN" : "en", {
      style: "long",
      type: "conjunction",
    }).format(items);
  } catch {
    return items.join(", ");
  }
}
const pendingActionTitle = computed(() => {
  const action = pendingAction.value;
  if (!action) return t("users.confirmAction");
  if (action.kind === "role") return action.value === "PLATFORM_ADMIN" ? t("users.grantAdmin") : t("users.removeAdmin");
  if (action.kind === "unlock") return t("users.unlockUser");
  return action.value === "ACTIVE" ? t("users.enableUser") : t("users.disableUser");
});
const pendingActionDescription = computed(() => {
  const action = pendingAction.value;
  if (!action) return "";
  const username = action.user.username;
  if (action.kind === "role") {
    return action.value === "PLATFORM_ADMIN"
      ? t("users.promoteAdminDesc", { username })
      : t("users.demoteUserDesc", { username });
  }
  if (action.kind === "unlock") return t("users.unlockDesc", { username });
  return action.value === "ACTIVE" ? t("users.enableDesc", { username }) : t("users.disableDesc", { username });
});

function applyRouteSearch() {
  const q = route.query.q;
  if (typeof q === "string" && q.trim()) {
    filters.query = q.trim();
  }
}

onMounted(() => {
  applyRouteSearch();
  void loadUsers(1);
});

watch(
  () => route.query.q,
  () => {
    applyRouteSearch();
    void loadUsers(1);
  },
);

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
    showStoreError(t("users.loadFailed"));
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
    username: "",
    email: "",
    displayName: "",
    password: "",
    platformRole: "USER",
    locale: "zh-CN",
    timezone: "Asia/Singapore",
  });
  createVisible.value = true;
  clearFeedback();
}

async function submitCreate() {
  if (!canCreate.value) return;
  try {
    const created = await users.createUser({ ...createDraft, email: createDraft.email?.trim() || undefined });
    createVisible.value = false;
    showFeedback(t("users.created", { username: created.username }));
    await loadUsers(1);
  } catch {
    showStoreError(t("users.createFailed"));
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
    showFeedback(t("users.profileUpdated", { username: updated.username }));
  } catch {
    showStoreError(t("users.profileUpdateFailed"));
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
  pendingAction.value =
    user.status === "LOCKED"
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
    showFeedback(t("users.permissionUpdated", { username: updated.username }));
  } catch {
    showStoreError(t("users.permissionFailed"));
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
    showFeedback(t("users.passwordReset", { username }));
  } catch {
    showStoreError(t("users.passwordResetFailed"));
  }
}

async function openWorkspaces(user: User) {
  workspaceUser.value = user;
  clearFeedback();
  try {
    await users.loadUserWorkspaces(user.id, true);
  } catch {
    showStoreError(t("users.workspacesLoadFailed"));
  }
}

function showStoreError(fallback: string) {
  const error = users.error;
  feedback.value = error
    ? `${error.message || fallback}${error.requestId ? t("users.requestIdSuffix", { id: error.requestId }) : ""}`
    : fallback;
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
  return (
    {
      ACTIVE: t("users.statusActive"),
      LOCKED: t("users.statusLocked"),
      DISABLED: t("users.statusDisabled"),
    } as const
  )[status];
}

function roleLabel(role: PlatformRole) {
  return role === "PLATFORM_ADMIN" ? t("users.rolePlatformAdmin") : t("users.roleUser");
}

function workspaceRoleLabel(role: string) {
  const map: Record<string, string> = {
    OWNER: t("users.roleOwner"),
    ADMIN: t("users.roleAdmin"),
    EDITOR: t("users.roleEditor"),
    OPERATOR: t("users.roleOperator"),
    VIEWER: t("users.roleViewer"),
  };
  return map[role] || role;
}

function userMenuActions(user: User): ManagementRowAction[] {
  return [
    {
      key: "profile",
      label: t("users.editProfile"),
      shortLabel: t("users.editProfileShort"),
      icon: "fa-solid fa-user-pen",
    },
    {
      key: "workspaces",
      label: t("users.viewWorkspaces"),
      shortLabel: t("users.viewWorkspacesShort"),
      icon: "fa-solid fa-layer-group",
    },
    {
      key: "role",
      label: user.platformRole === "PLATFORM_ADMIN" ? t("users.demoteToUser") : t("users.promoteToAdmin"),
      icon: "fa-solid fa-user-shield",
      tone: user.platformRole === "PLATFORM_ADMIN" ? "danger" : "primary",
    },
    {
      key: "status",
      label:
        user.status === "LOCKED"
          ? t("users.unlockUser")
          : user.status === "ACTIVE"
            ? t("users.disableUser")
            : t("users.enableUser"),
      icon: user.status === "LOCKED" ? "fa-solid fa-unlock-keyhole" : "fa-solid fa-power-off",
      tone: user.status === "ACTIVE" ? "danger" : "default",
    },
    { key: "reset", label: t("users.resetPassword"), icon: "fa-solid fa-key", tone: "danger" },
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
  if (!value) return t("users.neverLoggedIn");
  return new Intl.DateTimeFormat(getI18nLocale(), { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
</script>

<template>
  <div class="page-grid user-access-grid management-page-grid" v-loading="users.loading">
    <ManagementPageHeader
      class="span-12"
      :title="t('users.title')"
      :description="t('users.description')"
      icon="fa-solid fa-users"
      :eyebrow="t('users.eyebrow')"
    >
      <template #actions>
        <button class="primary-button" type="button" @click="openCreate">
          <i class="fa-solid fa-user-plus" aria-hidden="true" />{{ t("users.newUser") }}
        </button>
      </template>
    </ManagementPageHeader>

    <ManagementSummaryStrip class="span-12" :items="userSummaryItems" />

    <section class="user-access-panel management-list-card span-12">
      <div
        v-if="feedback"
        :class="['user-access-feedback', feedbackTone]"
        :role="feedbackTone === 'error' ? 'alert' : 'status'"
      >
        {{ feedback }}
      </div>

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
        :search-placeholder="t('users.searchPlaceholder')"
        :search-aria-label="t('users.searchAria')"
        :reset-label="t('users.clearFilters')"
        :reset-aria-label="t('users.clearFiltersAria')"
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
            :placeholder="t('users.statusAll')"
            :aria-label="t('users.statusFilterAria')"
            @update:model-value="setStatusFilter"
          />
          <AppSelect
            class="user-management-filter"
            :model-value="filters.platformRole"
            :options="platformRoleFilterOptions"
            :placeholder="t('users.roleAll')"
            :aria-label="t('users.roleFilterAria')"
            @update:model-value="setPlatformRoleFilter"
          />
        </template>
        <template #cell-identity="{ row: user }">
          <div class="user-identity-cell">
            <span class="user-identity-avatar" aria-hidden="true">{{
              user.displayName.slice(0, 1).toUpperCase()
            }}</span>
            <span>
              <strong class="aw-table-title">{{ user.displayName }}</strong>
              <small class="aw-table-subtitle">@{{ user.username }} · {{ user.email || t("users.emailNotSet") }}</small>
            </span>
          </div>
        </template>
        <template #cell-status="{ row: user }">
          <span :class="['user-status-badge', 'aw-table-pill', user.status.toLowerCase()]">{{
            statusLabel(user.status)
          }}</span>
        </template>
        <template #cell-platformRole="{ row: user }">
          <span :class="['user-role-badge', 'aw-table-pill', user.platformRole === 'PLATFORM_ADMIN' && 'admin']">{{
            roleLabel(user.platformRole)
          }}</span>
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
            :menu-label="t('users.menuLabel')"
            @action="handleUserRowAction($event, user)"
          />
        </template>
        <template #empty>
          <div class="empty-state registry-empty-state management-registry-empty-state">
            <div class="management-empty-state-icon"><i class="fa-solid fa-users" aria-hidden="true" /></div>
            <h2>
              {{
                filters.query || filters.status || filters.platformRole
                  ? t("users.noMatchTitle")
                  : t("users.emptyTitle")
              }}
            </h2>
            <p>
              {{
                filters.query || filters.status || filters.platformRole ? t("users.noMatchBody") : t("users.emptyBody")
              }}
            </p>
            <button
              v-if="filters.query || filters.status || filters.platformRole"
              class="ghost-button"
              type="button"
              @click="resetFilters"
            >
              {{ t("users.clearFilters") }}
            </button>
            <button v-else class="primary-button" type="button" @click="openCreate">{{ t("users.newUser") }}</button>
          </div>
        </template>
      </ManagementList>
    </section>

    <div v-if="createVisible" class="modal-backdrop" @click.self="createVisible = false">
      <section
        class="modal-card user-access-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="t('users.createModalAria')"
      >
        <header>
          <div>
            <span>CREATE USER</span>
            <h3>{{ t("users.createTitle") }}</h3>
          </div>
          <button
            class="icon-action-button"
            type="button"
            :aria-label="t('users.close')"
            @click="createVisible = false"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <form class="user-access-form" @submit.prevent="submitCreate">
          <label
            ><span>{{ t("users.fieldUsername") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><input v-model.trim="createDraft.username" required autocomplete="off"
          /></label>
          <label
            ><span>{{ t("users.fieldDisplayName") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><input v-model.trim="createDraft.displayName" required
          /></label>
          <label
            ><span>{{ t("users.fieldEmail") }}</span
            ><input v-model.trim="createDraft.email" type="email"
          /></label>
          <label
            ><span
              >{{ t("users.fieldTempPassword") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><input
              v-model="createDraft.password"
              type="password"
              minlength="12"
              required
              autocomplete="new-password"
            /><small>{{ t("users.passwordHint") }}</small></label
          >
          <label
            ><span
              >{{ t("users.fieldPlatformRole") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><AppSelect
              v-model="createDraft.platformRole"
              :options="platformRoleOptions"
              :aria-label="t('users.newUserRoleAria')"
              :aria-required="true"
          /></label>
          <label
            ><span>{{ t("users.fieldLocale") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><AppSelect
              v-model="createDraft.locale"
              :options="createLocaleOptions"
              :aria-label="t('users.newUserLocaleAria')"
              :aria-required="true"
              filterable
          /></label>
          <label
            ><span>{{ t("users.fieldTimezone") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><AppSelect
              v-model="createDraft.timezone"
              :options="createTimezoneOptions"
              :aria-label="t('users.newUserTimezoneAria')"
              :aria-required="true"
              filterable
          /></label>
          <footer>
            <p
              v-if="createDisabledReason"
              id="create-user-disabled-reason"
              class="user-create-disabled-reason"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            >
              <i class="fa-solid fa-circle-info" aria-hidden="true" />{{ createDisabledReason }}
            </p>
            <button class="ghost-button" type="button" @click="createVisible = false">{{ t("users.cancel") }}</button>
            <button
              class="primary-button"
              type="submit"
              :disabled="!canCreate || users.actionLoading"
              :aria-describedby="createDisabledReason ? 'create-user-disabled-reason' : undefined"
              :aria-busy="users.actionLoading"
            >
              {{ users.actionLoading ? t("users.creating") : t("users.createUser") }}
            </button>
          </footer>
        </form>
      </section>
    </div>

    <div v-if="profileUser" class="modal-backdrop" @click.self="profileUser = null">
      <section
        class="modal-card user-access-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="t('users.editProfileModalAria')"
      >
        <header>
          <div>
            <span>PROFILE</span>
            <h3>{{ t("users.editProfileTitle", { username: profileUser.username }) }}</h3>
          </div>
          <button class="icon-action-button" type="button" :aria-label="t('users.close')" @click="profileUser = null">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <form class="user-access-form" @submit.prevent="saveProfile">
          <label
            ><span>{{ t("users.fieldDisplayName") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><input v-model.trim="profileDraft.displayName" required
          /></label>
          <label
            ><span>{{ t("users.fieldEmail") }}</span
            ><input v-model.trim="profileDraft.email" type="email"
          /></label>
          <label
            ><span>{{ t("users.fieldLocale") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><AppSelect
              :model-value="profileDraft.locale || ''"
              :options="profileLocaleOptions"
              :aria-label="t('users.userLocaleAria')"
              :aria-required="true"
              filterable
              @update:model-value="profileDraft.locale = String($event)"
          /></label>
          <label
            ><span>{{ t("users.fieldTimezone") }} <span class="user-field-required" aria-hidden="true">*</span></span
            ><AppSelect
              :model-value="profileDraft.timezone || ''"
              :options="profileTimezoneOptions"
              :aria-label="t('users.userTimezoneAria')"
              :aria-required="true"
              filterable
              @update:model-value="profileDraft.timezone = String($event)"
          /></label>
          <footer>
            <button class="ghost-button" type="button" @click="profileUser = null">{{ t("users.cancel") }}</button
            ><button class="primary-button" type="submit" :disabled="!canSaveProfile || users.actionLoading">
              {{ t("users.saveProfile") }}
            </button>
          </footer>
        </form>
      </section>
    </div>

    <div v-if="pendingAction" class="modal-backdrop" @click.self="pendingAction = null">
      <section class="modal-card user-access-confirm" role="dialog" aria-modal="true" :aria-label="pendingActionTitle">
        <span>SECURITY COMMAND</span>
        <h3>{{ pendingActionTitle }}</h3>
        <p>{{ pendingActionDescription }}</p>
        <div v-if="feedbackTone === 'error' && feedback" class="user-access-feedback error" role="alert">
          {{ feedback }}
        </div>
        <footer>
          <button class="ghost-button" type="button" @click="pendingAction = null">{{ t("users.cancel") }}</button
          ><button
            class="primary-button"
            :class="{ danger: pendingActionUsesDangerTone }"
            type="button"
            :disabled="users.actionLoading"
            @click="confirmSecurityAction"
          >
            {{ t("users.confirmExecute") }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="resetUser" class="modal-backdrop" @click.self="resetUser = null">
      <section
        class="modal-card user-access-confirm"
        role="dialog"
        aria-modal="true"
        :aria-label="t('users.resetModalAria')"
      >
        <span>CREDENTIAL RESET</span>
        <h3>{{ t("users.resetTitle", { username: resetUser.username }) }}</h3>
        <p>{{ t("users.resetBody") }}</p>
        <label
          ><span>{{ t("users.fieldTempPassword") }} <span class="user-field-required" aria-hidden="true">*</span></span
          ><input v-model="temporaryPassword" type="password" minlength="12" required autocomplete="new-password"
        /></label>
        <footer>
          <button class="ghost-button" type="button" @click="resetUser = null">{{ t("users.cancel") }}</button
          ><button
            class="primary-button danger"
            type="button"
            :disabled="temporaryPassword.length < 12 || users.actionLoading"
            @click="submitResetPassword"
          >
            {{ t("users.resetPassword") }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="workspaceUser" class="modal-backdrop" @click.self="workspaceUser = null">
      <section
        class="modal-card user-access-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="t('users.workspacesModalAria')"
      >
        <header>
          <div>
            <span>WORKSPACE MEMBERSHIP</span>
            <h3>{{ t("users.workspacesTitle", { username: workspaceUser.username }) }}</h3>
          </div>
          <button class="icon-action-button" type="button" :aria-label="t('users.close')" @click="workspaceUser = null">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>
        <div class="user-workspace-list">
          <article v-for="membership in users.membershipsByUser[workspaceUser.id] || []" :key="membership.workspaceId">
            <div>
              <strong>{{ membership.workspaceDisplayName }}</strong
              ><small>{{ membership.workspaceSlug }}</small>
            </div>
            <span class="user-workspace-role">{{ workspaceRoleLabel(membership.role) }}</span
            ><em
              :class="[
                'user-workspace-status',
                { disabled: membership.disabledAt || membership.workspaceStatus !== 'ACTIVE' },
              ]"
              >{{
                membership.disabledAt
                  ? t("users.memberDisabled")
                  : membership.workspaceStatus === "ACTIVE"
                    ? t("users.workspaceActive")
                    : t("users.workspaceDisabled")
              }}</em
            >
          </article>
          <p v-if="!(users.membershipsByUser[workspaceUser.id] || []).length">{{ t("users.noWorkspaces") }}</p>
        </div>
      </section>
    </div>
  </div>
</template>
