import { createRouter, createWebHistory, type LocationQuery } from "vue-router";

import AppShell from "../components/AppShell.vue";
import { useAuthStore } from "../stores/auth";
import LoginView from "../views/LoginView.vue";
import ChangePasswordView from "../views/ChangePasswordView.vue";

const SMART_DAG_PASSTHROUGH_QUERY_KEYS = [
  "workspaceId",
  "agentId",
  "reviseSource",
  "feedbackSummary",
  "feedbackIssues",
  "compilationId",
] as const;

function firstQueryValue(value: LocationQuery[string]): string {
  const first = Array.isArray(value) ? value[0] : value;
  return typeof first === "string" ? first.trim() : "";
}

/** Map legacy /smart-dag query keys onto the editor generate-dock contract. */
export function mapSmartDagQuery(query: LocationQuery): LocationQuery {
  const next: LocationQuery = { generate: "1" };
  const workflowId = firstQueryValue(query.workflowId);
  if (workflowId) {
    next.edit = workflowId;
  }
  for (const key of SMART_DAG_PASSTHROUGH_QUERY_KEYS) {
    const value = firstQueryValue(query[key]);
    if (value) {
      next[key] = value;
    }
  }
  return next;
}

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
        { path: "tools/new", name: "tool-new", component: () => import("../views/ToolWorkspaceView.vue") },
        { path: "tools/:toolId/edit", name: "tool-edit", component: () => import("../views/ToolWorkspaceView.vue") },
        { path: "tools/:toolId", name: "tool-detail", component: () => import("../views/ToolWorkspaceView.vue") },
        { path: "workflow", name: "workflow", component: () => import("../views/WorkflowView.vue") },
        // Keep the named alias for one Console release so old /smart-dag links open the editor dock.
        {
          path: "smart-dag",
          name: "smart-dag",
          redirect: (to) => ({ name: "workflow", query: mapSmartDagQuery(to.query) }),
        },
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
