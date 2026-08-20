import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it } from "vitest";

import { navItems } from "../config/navigation";
import { useAuthStore } from "../stores/auth";
import type { User } from "../types/domain";
import { mapSmartDagQuery, router } from "./index";
import { safePostLoginPath } from "./redirect";

describe("platform administrator route and navigation", () => {
  beforeEach(async () => {
    setActivePinia(createPinia());
    await router.push("/overview");
  });

  it("marks the user management navigation as platform-admin only", () => {
    expect(navItems.find((item) => item.id === "users")).toMatchObject({
      route: "/users",
      platformAdminOnly: true,
    });
  });

  it("allows platform administrators to enter user management", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "admin-token";
    auth.user = userFixture("PLATFORM_ADMIN");
    await router.push("/users");
    expect(router.currentRoute.value.name).toBe("users");
  });

  it("redirects ordinary users away from user management", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "user-token";
    auth.user = userFixture("USER");
    await router.push("/users");
    expect(router.currentRoute.value.name).toBe("overview");
  });

  it("forces mustChangePassword users onto /change-password from business routes and login", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "temp-token";
    auth.user = userFixture("USER");
    auth.mustChangePassword = true;

    // Navigate via a different path so the guard always runs (not a same-route no-op).
    await router.push("/workspaces");
    expect(router.currentRoute.value.name).toBe("change-password");

    await router.push("/login");
    expect(router.currentRoute.value.name).toBe("change-password");

    await router.push("/users");
    expect(router.currentRoute.value.name).toBe("change-password");
  });

  it("sends authenticated users without must-change away from /change-password", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "ok-token";
    auth.user = userFixture("USER");
    auth.mustChangePassword = false;

    await router.push("/change-password");
    expect(router.currentRoute.value.name).toBe("overview");
  });

  it("keeps smart-dag as a named redirect into the workflow generate dock", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "user-token";
    auth.user = userFixture("USER");

    await router.push({
      name: "smart-dag",
      query: {
        workspaceId: "ws-ops",
        workflowId: "wf-9",
        agentId: "ag-2",
        reviseSource: "compile",
        feedbackSummary: "end missing",
        feedbackIssues: "EndDisconnected",
        compilationId: "comp-1",
      },
    });

    expect(router.getRoutes().some((route) => route.name === "smart-dag")).toBe(true);
    expect(router.currentRoute.value.name).toBe("workflow");
    expect(router.currentRoute.value.query).toEqual({
      generate: "1",
      edit: "wf-9",
      workspaceId: "ws-ops",
      agentId: "ag-2",
      reviseSource: "compile",
      feedbackSummary: "end missing",
      feedbackIssues: "EndDisconnected",
      compilationId: "comp-1",
    });
  });

  it("accepts only in-app paths after login", () => {
    expect(safePostLoginPath("/model-apis")).toBe("/model-apis");
    expect(safePostLoginPath("/agents?x=1")).toBe("/agents?x=1");
    expect(safePostLoginPath("//evil.example")).toBeNull();
    expect(safePostLoginPath("https://evil.example")).toBeNull();
    expect(safePostLoginPath("/login")).toBeNull();
    expect(safePostLoginPath("/change-password")).toBeNull();
    expect(safePostLoginPath(undefined)).toBeNull();
  });

  it("maps legacy smart-dag query keys onto generate-dock query", () => {
    expect(
      mapSmartDagQuery({
        workspaceId: "ws-ops",
        workflowId: ["wf-9"],
        agentId: "ag-2",
        extra: "drop-me",
      }),
    ).toEqual({
      generate: "1",
      edit: "wf-9",
      workspaceId: "ws-ops",
      agentId: "ag-2",
    });
    expect(mapSmartDagQuery({})).toEqual({ generate: "1" });
  });

  it("sends unauthenticated visitors from /change-password to login", async () => {
    const auth = useAuthStore();
    auth.initialized = true;
    auth.token = "";
    auth.user = null;
    auth.mustChangePassword = false;

    await router.push("/change-password");
    expect(router.currentRoute.value.name).toBe("login");
  });
});

function userFixture(platformRole: User["platformRole"]): User {
  return {
    id: "user-1",
    username: "route.user",
    displayName: "Route User",
    status: "ACTIVE",
    platformRole,
    locale: "zh-CN",
    timezone: "Asia/Singapore",
    createdAt: "2026-07-15T03:00:00Z",
    updatedAt: "2026-07-15T03:00:00Z",
    lockVersion: 1,
    role: platformRole === "PLATFORM_ADMIN" ? "Platform Admin" : "User",
  };
}
