import { createRouter, createWebHistory } from "vue-router";

import AppShell from "../components/AppShell.vue";
import { useAuthStore } from "../stores/auth";
import LoginView from "../views/LoginView.vue";
import ChangePasswordView from "../views/ChangePasswordView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/login", name: "login", component: LoginView },
    // Outside AppShell: forced password change recovery (ZKL-63 HIGH-03 / D6=A).
    { path: "/change-password", name: "change-password", component: ChangePasswordView },
    {
      path: "/",
      component: AppShell,
      meta: { requiresAuth: true },
      children: [
        { path: "", redirect: "/overview" },
        { path: "overview", name: "overview", component: () => import("../views/OverviewView.vue") },
        { path: "workspaces", name: "workspaces", component: () => import("../views/WorkspacesView.vue") },
        { path: "agents", name: "agents", component: () => import("../views/AgentsView.vue") },
        { path: "agent-access", name: "agent-access", component: () => import("../views/AgentAccessView.vue") },
        { path: "providers", name: "providers", component: () => import("../views/ProvidersView.vue") },
        {
          path: "connections",
          name: "connections",
          component: () => import("../views/ServiceConnectionsView.vue"),
        },
        {
          path: "openapi-imports",
          name: "openapi-imports",
          component: () => import("../views/OpenAPIImportsView.vue"),
        },
        { path: "model-apis", name: "model-apis", component: () => import("../views/ModelAPIConfigsView.vue") },
        { path: "tools", name: "tools", component: () => import("../views/ToolsView.vue") },
        { path: "workflow", name: "workflow", component: () => import("../views/WorkflowView.vue") },
        { path: "smart-dag", name: "smart-dag", component: () => import("../views/SmartDagView.vue") },
        { path: "chat", name: "chat", component: () => import("../views/ChatExecutionView.vue") },
        {
          path: "logs",
          name: "logs",
          component: () => import("../views/AuditLogsView.vue"),
          meta: { requiresPlatformAdmin: true },
        },
        {
          path: "users",
          name: "users",
          component: () => import("../views/UserAccessView.vue"),
          meta: { requiresPlatformAdmin: true },
        },
        // D8-A: unknown path → NotFound inside AppShell (not MVP placeholder).
        { path: ":pathMatch(.*)*", name: "not-found", component: () => import("../views/NotFoundView.vue") },
      ],
    },
  ],
});

router.beforeEach(async (to) => {
  const auth = useAuthStore();
  if (!auth.initialized) {
    await auth.restoreSession();
  }

  // Forced password change takes priority over normal app navigation.
  if (auth.token && auth.mustChangePassword) {
    if (to.name !== "change-password") {
      return { name: "change-password" };
    }
    return true;
  }

  if (to.name === "change-password") {
    if (!auth.token) {
      return { name: "login" };
    }
    // Authenticated users who do not need a password change stay in the app.
    return { name: "overview" };
  }

  if (to.meta.requiresAuth && !auth.token) {
    return { name: "login" };
  }
  if (to.meta.requiresPlatformAdmin && auth.user?.platformRole !== "PLATFORM_ADMIN") {
    return { name: "overview" };
  }
  if (to.name === "login" && auth.token) {
    return { name: "overview" };
  }
  return true;
});
