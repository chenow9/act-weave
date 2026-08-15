import { describe, expect, it } from "vitest";

import { groupNavItemsBySection, navItems, navSectionOrder, primaryNavigationIds } from "./navigation";

describe("navigation information architecture", () => {
  it("uses Space → Build → Connect → Run → Govern section order", () => {
    expect([...navSectionOrder]).toEqual(["space", "build", "connect", "run", "govern"]);
    for (const item of navItems) {
      expect(navSectionOrder).toContain(item.sectionId);
    }
  });

  it("keeps primary shortcuts as a subset of full nav (not a separate universe)", () => {
    const ids = new Set(navItems.map((item) => item.id));
    for (const id of primaryNavigationIds) {
      expect(ids.has(id)).toBe(true);
    }
    expect([...primaryNavigationIds]).toEqual(["agents", "tools", "workflow", "chat"]);
  });

  it("groups every item under ordered sections without dropping primaries", () => {
    const groups = groupNavItemsBySection(navItems);
    expect(groups.map((g) => g.sectionId)).toEqual(["space", "build", "connect", "run", "govern"]);

    const allIds = groups.flatMap((g) => g.items.map((item) => item.id));
    expect(allIds).toEqual(expect.arrayContaining([...primaryNavigationIds]));
    expect(allIds).toHaveLength(navItems.length);
  });

  it("places related modules together", () => {
    const byId = Object.fromEntries(navItems.map((item) => [item.id, item]));
    expect(byId.workflow.sectionId).toBe("build");
    expect(byId.agents.sectionId).toBe("build");
    expect(byId["agent-access"].sectionId).toBe("connect");
    expect(byId.providers.sectionId).toBe("connect");
    expect(byId.chat.sectionId).toBe("run");
    expect(byId.logs.sectionId).toBe("govern");
    expect(byId.users.sectionId).toBe("govern");
  });

  it("uses i18n label keys instead of hard-coded Chinese labels", () => {
    for (const item of navItems) {
      expect(item.labelKey.startsWith("nav.item.")).toBe(true);
    }
  });

  it("does not expose a standalone Smart Orchestration item", () => {
    expect(navItems.some((item) => item.id === "smart-dag" || item.route === "/smart-dag")).toBe(false);
    expect(navItems.some((item) => item.labelKey === "nav.item.smartDag")).toBe(false);
  });
});
