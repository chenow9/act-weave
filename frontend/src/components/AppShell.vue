<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

import { groupNavItemsBySection, navItems, primaryNavigationIds, type NavItem } from "../config/navigation";
import { useAuthStore } from "../stores/auth";
import { useOverviewStore } from "../stores/overview";
import { useWorkspaceStore } from "../stores/workspaces";

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
const filteredNavigationItems = computed(() => {
  const query = navigationQuery.value.trim().toLocaleLowerCase();
  if (!query) return visibleNavItems.value;
  return visibleNavItems.value.filter((item) =>
    [item.label, item.section, item.id].some((value) => value.toLocaleLowerCase().includes(query)),
  );
});
/** 方案 1：完整分组，常用项仍出现在列表中（不再从分组剔除）。 */
const groupedNavigation = computed(() => groupNavItemsBySection(filteredNavigationItems.value));
const visiblePrimaryNavigation = computed(() => {
  if (!navigationQuery.value.trim()) return primaryNavigation.value;
  return filteredNavigationItems.value.filter((item) => primaryIdSet.has(item.id));
});
const hasNavigationResults = computed(() => filteredNavigationItems.value.length > 0);
const showPrimaryShortcuts = computed(() => visiblePrimaryNavigation.value.length > 0);
const activeWorkspace = computed(() => workspaces.activeWorkspace);
const filteredWorkspaces = computed(() => switcherResults.value);
const userInitials = computed(() => {
  const source = auth.user?.displayName || auth.user?.username || "AW";
  const parts = source.trim().split(/\s+/).filter(Boolean);
  return (parts.length > 1 ? `${parts[0][0]}${parts.at(-1)?.[0] || ""}` : source.slice(0, 2)).toUpperCase();
});

onMounted(async () => {
  document.addEventListener("pointerdown", handleDocumentPointerdown);
  document.addEventListener("keydown", handleDocumentKeydown);
  try {
    await workspaces.load();
    workspaceBootstrapReady.value = true;
    // Overview is platform-wide (all accessible spaces), not the top-bar current workspace.
    if (activeModule.value === "overview") {
      await overview.load();
    }
  } catch (error) {
    if (getHttpStatus(error) === 401) {
      auth.logout();
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
  if (!item) return "管理控制台";
  if (item.id === "workspaces") {
    const total = workspaces.summary.total || workspaces.items.length;
    return `${total} 个可访问空间`;
  }
  return item.badge ? `${item.section} · ${item.badge}` : item.section;
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
  return workspace?.displayName || workspace?.name || "选择业务空间";
}

function workspaceModeLabel(mode?: string) {
  return mode === "Production" ? "生产" : "沙箱";
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

function logout() {
  profileMenuOpen.value = false;
  auth.logout();
  void router.push({ name: "login" });
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
      <router-link class="app-brand" to="/overview" aria-label="返回空间总览">
        <span class="app-brand-mark" aria-hidden="true"><i class="fa-solid fa-circle-nodes" /></span>
        <span>ACTWEAVE 织行</span>
      </router-link>

      <section ref="navigationRef" class="fluid-island" :class="{ open: navigationOpen }" aria-label="主导航">
        <button class="fluid-trigger" type="button" :aria-expanded="navigationOpen" @click="toggleNavigation">
          <span class="live-orb" aria-hidden="true" />
          <span class="fluid-current"
            ><b>{{ activeNavItem?.label }}</b
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
                placeholder="搜索模块或工作区域…"
                aria-label="搜索模块"
              />
            </label>
            <button class="island-close" type="button" aria-label="关闭导航中心" @click="closeNavigation(true)">
              <i class="fa-solid fa-xmark" />
            </button>
          </div>

          <div v-if="showPrimaryShortcuts" class="island-section">
            <span class="island-section-label">常用</span>
            <div class="island-grid">
              <router-link
                v-for="item in visiblePrimaryNavigation"
                :key="item.id"
                class="island-card island-module"
                :class="{ active: item.id === activeModule }"
                :to="item.route"
                @click="closeNavigation(true)"
              >
                <span class="island-module-icon"><i :class="item.icon" aria-hidden="true" /></span>
                <span
                  ><b>{{ item.label }}</b
                  ><small>{{ moduleMeta(item) }}</small></span
                >
              </router-link>
            </div>
          </div>

          <div v-if="groupedNavigation.length" class="island-section island-section--all">
            <span class="island-section-label">全部模块</span>
            <div class="island-groups">
              <section v-for="group in groupedNavigation" :key="group.section" class="island-group">
                <span class="island-group-title">{{ group.section }}</span>
                <router-link
                  v-for="item in group.items"
                  :key="item.id"
                  class="island-row island-module"
                  :class="{ active: item.id === activeModule }"
                  :to="item.route"
                  @click="closeNavigation(true)"
                >
                  <span class="island-module-icon"><i :class="item.icon" aria-hidden="true" /></span>
                  <span>{{ item.label }}</span>
                  <small v-if="item.badge">{{ item.badge }}</small>
                </router-link>
              </section>
            </div>
          </div>

          <p v-if="!hasNavigationResults" class="island-empty">没有匹配的模块</p>
          <footer class="island-footer">
            <span>当前空间：{{ workspaceDisplayName() }}</span>
          </footer>
        </div>
      </section>

      <div class="topbar-right top-right">
        <!-- 空间总览是全平台聚合视图，不展示「当前业务空间」切换器。 -->
        <div
          v-if="workspaces.items.length && activeModule !== 'overview'"
          ref="workspaceSwitcherRef"
          class="workspace-switcher"
        >
          <button
            class="workspace-switcher-trigger workspace-switch"
            data-testid="workspace-switcher"
            type="button"
            aria-haspopup="dialog"
            :aria-expanded="workspaceMenuOpen"
            :aria-label="`切换当前业务空间：${workspaceDisplayName()}`"
            @click="toggleWorkspaceMenu"
          >
            <i class="fa-solid fa-layer-group" aria-hidden="true" />
            <span>{{ workspaceDisplayName() }}</span>
            <i class="fa-solid fa-chevron-down workspace-switcher-chevron" aria-hidden="true" />
          </button>
          <section v-if="workspaceMenuOpen" class="workspace-switcher-menu" role="dialog" aria-label="选择业务空间">
            <header>
              <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
              <input
                ref="workspaceSearchInput"
                v-model="workspaceQuery"
                type="search"
                placeholder="搜索业务空间"
                aria-label="搜索业务空间"
              />
            </header>
            <div class="workspace-switcher-options" role="listbox" aria-label="可访问的业务空间">
              <button
                v-for="workspace in filteredWorkspaces"
                :key="workspace.id"
                type="button"
                role="option"
                :data-workspace-id="workspace.id"
                :aria-selected="workspace.id === workspaces.activeWorkspaceId"
                :disabled="workspace.status === 'Disabled'"
                @click="selectWorkspace(workspace.id)"
              >
                <span class="workspace-option-icon"><i class="fa-solid fa-layer-group" aria-hidden="true" /></span>
                <span class="workspace-option-copy"
                  ><strong>{{ workspaceDisplayName(workspace) }}</strong
                  ><small>{{ workspace.name }}</small></span
                >
                <span class="workspace-option-meta">
                  <em>{{ workspaceModeLabel(workspace.mode) }}</em>
                  <em v-if="workspace.status === 'Disabled'" class="disabled">已停用</em>
                </span>
              </button>
              <p v-if="!filteredWorkspaces.length">没有匹配的业务空间</p>
            </div>
            <footer>
              <button type="button" @click="goWorkspaceManagement">
                <i class="fa-solid fa-gear" aria-hidden="true" />管理业务空间
              </button>
            </footer>
          </section>
        </div>

        <div ref="profileMenuRef" class="profile-menu-wrap">
          <button
            class="user-avatar avatar"
            type="button"
            aria-label="打开用户菜单"
            :aria-expanded="profileMenuOpen"
            @click="toggleProfileMenu"
          >
            {{ userInitials }}
          </button>
          <section v-if="profileMenuOpen" class="profile-menu" aria-label="用户菜单">
            <header>
              <strong>{{ auth.user?.displayName || auth.user?.username }}</strong
              ><small>{{ auth.user?.role }}</small>
            </header>
            <button type="button" @click="goRuntimeStatus">
              <i class="fa-solid fa-wave-square" aria-hidden="true" />运行时状态
            </button>
            <button type="button" @click="goNotifications">
              <i class="fa-regular fa-bell" aria-hidden="true" />通知与审计
            </button>
            <button class="logout-button" type="button" aria-label="退出登录" @click="logout">
              <i class="fa-solid fa-power-off" aria-hidden="true" />退出登录
            </button>
          </section>
        </div>
      </div>
    </header>

    <button
      v-if="navigationOpen"
      class="nav-scrim open"
      type="button"
      aria-label="关闭导航中心"
      @click="closeNavigation(true)"
    />

    <main class="main-shell">
      <section :class="['content-area', { 'content-area--workspace': activeModule === 'chat' }]">
        <router-view v-if="workspaceBootstrapReady" :key="workspaces.activeWorkspaceId || 'no-workspace'" />
      </section>
    </main>
  </div>
</template>
