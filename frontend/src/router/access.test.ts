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
