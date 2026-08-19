<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";

import {
  groupNavItemsBySection,
  navItems,
  primaryNavigationIds,
  sectionLabelKey,
  type NavItem,
} from "../config/navigation";
import { i18n } from "../i18n";
import type { AppLocale } from "../i18n/types";
import { setLocale } from "../services/locale";
import { useAuthStore } from "../stores/auth";
import { useOverviewStore } from "../stores/overview";
import { useWorkspaceStore } from "../stores/workspaces";

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const workspaces = useWorkspaceStore();
const overview = useOverviewStore();
const workspaceBootstrapReady = ref(false);
const navigationOpen = ref(false);
const navigationQuery = ref("");
const workspaceMenuOpen = ref(false);
const workspaceQuery = ref("");
const switcherResults = ref(workspaces.items);
const switcherSearching = ref(false);
let workspaceSearchTimer: ReturnType<typeof setTimeout> | null = null;
const profileMenuOpen = ref(false);
const signOutError = ref("");
const navigationRef = ref<HTMLElement | null>(null);
const navigationSearchInput = ref<HTMLInputElement | null>(null);
const workspaceSwitcherRef = ref<HTMLElement | null>(null);
const workspaceSearchInput = ref<HTMLInputElement | null>(null);
const profileMenuRef = ref<HTMLElement | null>(null);

const primaryIdSet = new Set<string>(primaryNavigationIds);

const visibleNavItems = computed(() =>
  navItems.filter((item) => !item.platformAdminOnly || auth.user?.platformRole === "PLATFORM_ADMIN"),
);
const activeModule = computed(() => String(route.path.split("/")[1] || "overview"));
const activeNavItem = computed(
  () => visibleNavItems.value.find((item) => item.id === activeModule.value) || visibleNavItems.value[0],
);
const primaryNavigation = computed(() =>
  primaryNavigationIds
    .map((id) => visibleNavItems.value.find((item) => item.id === id))
    .filter((item): item is NavItem => Boolean(item)),
);

function navLabel(item: NavItem, forLocale?: AppLocale): string {
  if (forLocale) {
    return String(i18n.global.t(item.labelKey, {}, { locale: forLocale }));
  }
  return t(item.labelKey);
}

function sectionLabel(sectionId: NavItem["sectionId"], forLocale?: AppLocale): string {
  const key = sectionLabelKey(sectionId);
  if (forLocale) {
    return String(i18n.global.t(key, {}, { locale: forLocale }));
  }
  return t(key);
}

const filteredNavigationItems = computed(() => {
  const query = navigationQuery.value.trim().toLocaleLowerCase();
  if (!query) return visibleNavItems.value;
  // KD15: match id + both language labels + section labels
  return visibleNavItems.value.filter((item) => {
    const haystack = [
      item.id,
      navLabel(item, "zh-CN"),
      navLabel(item, "en"),
      sectionLabel(item.sectionId, "zh-CN"),
      sectionLabel(item.sectionId, "en"),
    ];
    return haystack.some((value) => value.toLocaleLowerCase().includes(query));
  });
});

const groupedNavigation = computed(() =>
  groupNavItemsBySection(
    navigationQuery.value.trim()
      ? filteredNavigationItems.value
      : filteredNavigationItems.value.filter((item) => !primaryIdSet.has(item.id)),
  ),
);
const visiblePrimaryNavigation = computed(() => {
  if (!navigationQuery.value.trim()) return primaryNavigation.value;
  return [];
});
const hasNavigationResults = computed(() => filteredNavigationItems.value.length > 0);
const showPrimaryShortcuts = computed(() => visiblePrimaryNavigation.value.length > 0);
const groupedNavigationLabel = computed(() =>
  navigationQuery.value.trim() ? t("nav.allModules") : t("nav.moreModules"),
);
const activeWorkspace = computed(() => workspaces.activeWorkspace);
const filteredWorkspaces = computed(() => switcherResults.value);
const showWorkspaceSwitcher = computed(
  () => workspaces.items.length > 0 && activeModule.value !== "overview" && activeModule.value !== "workspaces",
);
const userInitials = computed(() => {
  const source = auth.user?.displayName || auth.user?.username || "AW";
  const parts = source.trim().split(/\s+/).filter(Boolean);
  return (parts.length > 1 ? `${parts[0][0]}${parts.at(-1)?.[0] || ""}` : source.slice(0, 2)).toUpperCase();
});
const currentLocale = computed<AppLocale>(() => (locale.value === "zh-CN" ? "zh-CN" : "en"));

onMounted(async () => {
  document.addEventListener("pointerdown", handleDocumentPointerdown);
  document.addEventListener("keydown", handleDocumentKeydown);
  try {
    await workspaces.load();
    workspaceBootstrapReady.value = true;
    if (activeModule.value === "overview") {
      await overview.load();
    }
  } catch (error) {
    if (getHttpStatus(error) === 401) {
      auth.clearSession();
      void router.push({ name: "login" });
    }
  } finally {
    workspaceBootstrapReady.value = true;
  }
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handleDocumentPointerdown);
  document.removeEventListener("keydown", handleDocumentKeydown);
});

watch(
  () => route.path,
  () => {
    closeNavigation();
    if (workspaceBootstrapReady.value && activeModule.value === "overview") {
      void overview.load();
    }
  },
);

function getHttpStatus(error: unknown) {
  const candidate = error as { response?: { status?: number }; status?: number };
  return candidate.response?.status ?? candidate.status;
}

function moduleMeta(item?: NavItem) {
  if (!item) return t("common.console");
  if (item.id === "workspaces") {
    const total = workspaces.summary.total || workspaces.items.length;
    return t("nav.accessibleSpaces", { count: total });
  }
  const section = sectionLabel(item.sectionId);
  return item.badge ? `${section} · ${item.badge}` : section;
}

watch(
  () => workspaces.items,
  (items) => {
    if (!workspaceQuery.value.trim()) switcherResults.value = items;
  },
  { immediate: true },
);

watch(workspaceQuery, (query) => {
  if (workspaceSearchTimer) clearTimeout(workspaceSearchTimer);
  const trimmed = query.trim();
  if (!trimmed) {
    switcherResults.value = workspaces.items;
    switcherSearching.value = false;
    return;
  }
  workspaceSearchTimer = setTimeout(() => {
    void runWorkspaceRemoteSearch(trimmed);
  }, 250);
});

async function runWorkspaceRemoteSearch(query: string) {
  switcherSearching.value = true;
  try {
    const page = await workspaces.fetchWorkspacePage({ page: 1, pageSize: 20, query });
    switcherResults.value = page.items;
    for (const item of page.items) {
      workspaces.upsertInList("items", item);
    }
  } catch {
    // Keep last good switcher list on search failure.
  } finally {
    switcherSearching.value = false;
  }
}

function workspaceDisplayName(workspace = activeWorkspace.value) {
  return workspace?.displayName || workspace?.name || t("shell.selectWorkspace");
}

function workspaceModeLabel(mode?: string) {
  return mode === "Production" ? t("shell.modeProduction") : t("shell.modeSandbox");
}

async function openNavigation() {
  workspaceMenuOpen.value = false;
  profileMenuOpen.value = false;
  navigationOpen.value = true;
  await nextTick();
  navigationSearchInput.value?.focus();
}

function toggleNavigation() {
  if (navigationOpen.value) closeNavigation(true);
  else void openNavigation();
}

function closeNavigation(restoreFocus = false) {
  navigationOpen.value = false;
  navigationQuery.value = "";
  if (navigationRef.value) navigationRef.value.scrollTop = 0;
  void nextTick(() => {
    const navigation = navigationRef.value;
    if (!navigation) return;
    if (restoreFocus) navigation.querySelector<HTMLButtonElement>(".fluid-trigger")?.focus({ preventScroll: true });
    navigation.scrollTop = 0;
  });
}

function toggleWorkspaceMenu() {
  navigationOpen.value = false;
  profileMenuOpen.value = false;
  workspaceMenuOpen.value = !workspaceMenuOpen.value;
  workspaceQuery.value = "";
  if (workspaceMenuOpen.value) void nextTick(() => workspaceSearchInput.value?.focus());
}

function closeWorkspaceMenu() {
  workspaceMenuOpen.value = false;
  workspaceQuery.value = "";
}

function selectWorkspace(workspaceId: string) {
  if (!workspaceId) return;
  if (workspaceId !== workspaces.activeWorkspaceId) workspaces.selectWorkspace(workspaceId);
  closeWorkspaceMenu();
}

function goWorkspaceManagement() {
  closeWorkspaceMenu();
  void router.push({ name: "workspaces" });
}

function toggleProfileMenu() {
  navigationOpen.value = false;
  workspaceMenuOpen.value = false;
  profileMenuOpen.value = !profileMenuOpen.value;
}

function goRuntimeStatus() {
  profileMenuOpen.value = false;
  void router.push({ name: "workflow" });
}

function goNotifications() {
  profileMenuOpen.value = false;
  void router.push({ name: "logs" });
}

async function logout() {
  if (auth.loading) return;
  profileMenuOpen.value = false;
  signOutError.value = "";
  try {
    await auth.logout();
    await router.push({ name: "login" });
  } catch {
    signOutError.value = auth.error || t("auth.signOutFailed");
  }
}

async function switchLanguage(next: AppLocale) {
  if (next === currentLocale.value) {
    profileMenuOpen.value = false;
    return;
  }
  await setLocale(next, {
    syncServer: Boolean(auth.user),
    lockVersion: auth.user?.lockVersion,
    onUserUpdated: (user) => auth.applyUser(user),
  });
  profileMenuOpen.value = false;
}

function handleDocumentPointerdown(event: PointerEvent) {
  if (!(event.target instanceof Node)) return;
  if (navigationOpen.value && !navigationRef.value?.contains(event.target)) closeNavigation();
  if (workspaceMenuOpen.value && !workspaceSwitcherRef.value?.contains(event.target)) closeWorkspaceMenu();
  if (profileMenuOpen.value && !profileMenuRef.value?.contains(event.target)) profileMenuOpen.value = false;
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "k") {
    event.preventDefault();
    void openNavigation();
    return;
  }
  if (event.key !== "Escape") return;
  if (profileMenuOpen.value) profileMenuOpen.value = false;
  else if (workspaceMenuOpen.value) closeWorkspaceMenu();
  else if (navigationOpen.value) closeNavigation(true);
}
</script>

<template>
  <div class="app-shell">
    <header class="topbar app-topbar">
      <router-link class="app-brand" to="/overview" :aria-label="t('nav.backToOverview')">
        <span class="app-brand-mark" aria-hidden="true"><i class="fa-solid fa-circle-nodes" /></span>
        <span>{{ t("common.appTitle") }}</span>
      </router-link>

      <section
        ref="navigationRef"
        class="fluid-island"
        :class="{ open: navigationOpen }"
        :aria-label="t('nav.mainNav')"
      >
        <button class="fluid-trigger" type="button" :aria-expanded="navigationOpen" @click="toggleNavigation">
          <span class="live-orb" aria-hidden="true" />
          <span class="fluid-current"
            ><b>{{ activeNavItem ? navLabel(activeNavItem) : t("common.console") }}</b
            ><small>{{ moduleMeta(activeNavItem) }}</small></span
          >
          <i class="fa-solid fa-chevron-down fluid-chevron" aria-hidden="true" />
        </button>

        <div class="fluid-content" :aria-hidden="!navigationOpen" :inert="!navigationOpen">
          <div class="island-tools">
            <label class="island-search">
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input
                ref="navigationSearchInput"
                v-model="navigationQuery"
                type="search"
                :placeholder="t('nav.searchPlaceholder')"
                :aria-label="t('nav.searchAria')"
                data-testid="nav-search"
              />
            </label>
            <button class="island-close" type="button" :aria-label="t('nav.closeNav')" @click="closeNavigation(true)">
              <i class="fa-solid fa-xmark" />
            </button>
          </div>

          <div v-if="showPrimaryShortcuts" class="island-section">
            <span class="island-section-label">{{ t("nav.pinned") }}</span>
            <div class="island-grid">
              <router-link
                v-for="item in visiblePrimaryNavigation"
                :key="item.id"
                class="island-card island-module"
                :class="{ active: item.id === activeModule }"
                :to="item.route"
                :title="navLabel(item)"
                @click="closeNavigation(true)"
              >
                <span class="island-module-icon"><i :class="item.icon" aria-hidden="true" /></span>
                <span
                  ><b>{{ navLabel(item) }}</b
                  ><small>{{ moduleMeta(item) }}</small></span
                >
              </router-link>
            </div>
          </div>

          <div v-if="groupedNavigation.length" class="island-section island-section--all">
            <span class="island-section-label">{{ groupedNavigationLabel }}</span>
            <div class="island-groups">
              <section v-for="group in groupedNavigation" :key="group.sectionId" class="island-group">
                <span class="island-group-title">{{ sectionLabel(group.sectionId) }}</span>
                <router-link
                  v-for="item in group.items"
                  :key="item.id"
                  class="island-row island-module"
                  :class="{ active: item.id === activeModule }"
                  :to="item.route"
                  :title="navLabel(item)"
                  :data-nav-id="item.id"
                  @click="closeNavigation(true)"
                >
                  <span class="island-module-icon"><i :class="item.icon" aria-hidden="true" /></span>
                  <span>{{ navLabel(item) }}</span>
                  <small v-if="item.badge">{{ item.badge }}</small>
                </router-link>
              </section>
            </div>
          </div>

          <p v-if="!hasNavigationResults" class="island-empty">{{ t("nav.noMatch") }}</p>
          <footer class="island-footer">
            <span>{{ t("nav.currentWorkspace", { name: workspaceDisplayName() }) }}</span>
          </footer>
        </div>
      </section>

      <div class="topbar-right top-right">
        <div v-if="showWorkspaceSwitcher" ref="workspaceSwitcherRef" class="workspace-switcher">
          <button
            class="workspace-switcher-trigger workspace-switch"
            data-testid="workspace-switcher"
            type="button"
            aria-haspopup="dialog"
            :aria-expanded="workspaceMenuOpen"
            :aria-label="t('shell.switchWorkspaceAria', { name: workspaceDisplayName() })"
            @click="toggleWorkspaceMenu"
          >
            <i class="fa-solid fa-layer-group" aria-hidden="true" />
            <span>{{ workspaceDisplayName() }}</span>
            <i class="fa-solid fa-chevron-down workspace-switcher-chevron" aria-hidden="true" />
          </button>
          <section
            v-if="workspaceMenuOpen"
            class="workspace-switcher-menu"
            role="dialog"
            :aria-label="t('shell.selectWorkspace')"
          >
            <header>
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input
                ref="workspaceSearchInput"
                v-model="workspaceQuery"
                type="search"
                :placeholder="t('shell.searchWorkspaces')"
                :aria-label="t('shell.searchWorkspaces')"
              />
            </header>
            <div class="workspace-switcher-options" role="listbox" :aria-label="t('shell.accessibleWorkspaces')">
              <button
                v-for="workspace in filteredWorkspaces"
                :key="workspace.id"
                type="button"
                role="option"
                :data-workspace-id="workspace.id"
                :aria-selected="workspace.id === workspaces.activeWorkspaceId"
                :disabled="workspace.status === 'DISABLED'"
                @click="selectWorkspace(workspace.id)"
              >
                <span class="workspace-option-icon"><i class="fa-solid fa-layer-group" aria-hidden="true" /></span>
                <span class="workspace-option-copy"
                  ><strong>{{ workspaceDisplayName(workspace) }}</strong
                  ><small>{{ workspace.name }}</small></span
                >
                <span class="workspace-option-meta">
                  <em>{{ workspaceModeLabel(workspace.mode) }}</em>
                  <em v-if="workspace.status === 'DISABLED'" class="disabled">{{ t("common.disabled") }}</em>
                </span>
              </button>
              <p v-if="!filteredWorkspaces.length">{{ t("shell.noMatchingWorkspaces") }}</p>
            </div>
            <footer>
              <button type="button" @click="goWorkspaceManagement">
                <i class="fa-solid fa-gear" aria-hidden="true" />{{ t("shell.manageWorkspaces") }}
              </button>
            </footer>
          </section>
        </div>

        <div ref="profileMenuRef" class="profile-menu-wrap">
          <button
            class="user-avatar avatar"
            type="button"
            data-testid="user-menu-trigger"
            :aria-label="t('shell.openUserMenu')"
            :aria-expanded="profileMenuOpen"
            @click="toggleProfileMenu"
          >
            {{ userInitials }}
          </button>
          <section
            v-if="profileMenuOpen"
            class="profile-menu"
            :aria-label="t('shell.userMenu')"
            data-testid="user-menu"
          >
            <header>
              <strong>{{ auth.user?.displayName || auth.user?.username }}</strong
              ><small>{{ auth.user?.role }}</small>
            </header>
            <div
              class="profile-menu-section"
              data-testid="language-switcher"
              role="group"
              :aria-label="t('common.language')"
            >
              <span class="profile-menu-section-label">{{ t("common.language") }}</span>
              <button
                type="button"
                class="profile-menu-choice"
                data-testid="lang-zh-CN"
                :class="{ active: currentLocale === 'zh-CN' }"
                :aria-pressed="currentLocale === 'zh-CN'"
                @click="switchLanguage('zh-CN')"
              >
                <i class="fa-solid fa-language" aria-hidden="true" />
                <span>{{ t("common.languageZh") }}</span>
                <i
                  v-if="currentLocale === 'zh-CN'"
                  class="fa-solid fa-check profile-menu-choice-check"
                  aria-hidden="true"
                />
              </button>
              <button
                type="button"
                class="profile-menu-choice"
                data-testid="lang-en"
                :class="{ active: currentLocale === 'en' }"
                :aria-pressed="currentLocale === 'en'"
                @click="switchLanguage('en')"
              >
                <i class="fa-solid fa-language" aria-hidden="true" />
                <span>{{ t("common.languageEn") }}</span>
                <i
                  v-if="currentLocale === 'en'"
                  class="fa-solid fa-check profile-menu-choice-check"
                  aria-hidden="true"
                />
              </button>
            </div>
            <button type="button" @click="goRuntimeStatus">
              <i class="fa-solid fa-wave-square" aria-hidden="true" />{{ t("shell.runtimeStatus") }}
            </button>
            <button type="button" @click="goNotifications">
              <i class="fa-regular fa-bell" aria-hidden="true" />{{ t("shell.notificationsAudit") }}
            </button>
            <button
              class="logout-button"
              type="button"
              :aria-label="t('shell.signOut')"
              :disabled="auth.loading"
              :aria-busy="auth.loading"
              data-testid="sign-out"
              @click="logout"
            >
              <i class="fa-solid fa-power-off" aria-hidden="true" />{{
                auth.loading ? t("shell.signingOut") : t("shell.signOut")
              }}
            </button>
          </section>
        </div>
      </div>
    </header>

    <button
      v-if="navigationOpen"
      class="nav-scrim open"
      type="button"
      :aria-label="t('nav.closeNav')"
      @click="closeNavigation(true)"
    />

    <section v-if="signOutError" class="session-action-error" role="alert" data-testid="sign-out-error">
      <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
      <span
        ><strong>{{ t("shell.signOutFailed") }}</strong
        ><small>{{ signOutError }}</small></span
      >
      <button type="button" :disabled="auth.loading" @click="logout">
        {{ auth.loading ? t("shell.signingOut") : t("shell.retrySignOut") }}
      </button>
    </section>

    <main class="main-shell">
      <section :class="['content-area', { 'content-area--workspace': activeModule === 'chat' }]">
        <router-view v-if="workspaceBootstrapReady" :key="workspaces.activeWorkspaceId || 'no-workspace'" />
      </section>
    </main>
  </div>
</template>
