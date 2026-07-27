import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it } from "vitest";

import { navItems } from "../config/navigation";
import { useAuthStore } from "../stores/auth";
import type { User } from "../types/domain";
import { router } from "./index";

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
