import { createRouter, createWebHistory } from "vue-router";

import AppShell from "../components/AppShell.vue";
import { useAuthStore } from "../stores/auth";
import LoginView from "../views/LoginView.vue";
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
