import { createRouter, createWebHistory } from "vue-router";

import AppShell from "../components/AppShell.vue";
import { useAuthStore } from "../stores/auth";
import LoginView from "../views/LoginView.vue";
import ChangePasswordView from "../views/ChangePasswordView.vue";
import AgentsView from "../views/AgentsView.vue";
import AgentAccessView from "../views/AgentAccessView.vue";
import ModelAPIConfigsView from "../views/ModelAPIConfigsView.vue";
import OpenAPIImportsView from "../views/OpenAPIImportsView.vue";
import OverviewView from "../views/OverviewView.vue";
import PlaceholderView from "../views/PlaceholderView.vue";
import ProvidersView from "../views/ProvidersView.vue";
import ServiceConnectionsView from "../views/ServiceConnectionsView.vue";
import ToolsView from "../views/ToolsView.vue";
import WorkspacesView from "../views/WorkspacesView.vue";

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
        { path: "overview", name: "overview", component: OverviewView },
        { path: "workspaces", name: "workspaces", component: WorkspacesView },
		{ path: "agents", name: "agents", component: AgentsView },
		{ path: "agent-access", name: "agent-access", component: AgentAccessView },
        { path: "providers", name: "providers", component: ProvidersView },
        { path: "connections", name: "connections", component: ServiceConnectionsView },
        { path: "openapi-imports", name: "openapi-imports", component: OpenAPIImportsView },
        { path: "model-apis", name: "model-apis", component: ModelAPIConfigsView },
        { path: "tools", name: "tools", component: ToolsView },
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
        { path: ":moduleId", name: "placeholder", component: PlaceholderView },
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
