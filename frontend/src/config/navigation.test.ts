import { describe, expect, it } from "vitest";

import {
  groupNavItemsBySection,
  navItems,
  navSectionOrder,
  primaryNavigationIds,
} from "./navigation";

describe("navigation information architecture", () => {
  it("uses the 空间 → 构建 → 接入 → 运行 → 治理 section order", () => {
    expect([...navSectionOrder]).toEqual(["空间", "构建", "接入", "运行", "治理"]);
    for (const item of navItems) {
      expect(navSectionOrder).toContain(item.section);
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
    expect(groups.map((g) => g.section)).toEqual(["空间", "构建", "接入", "运行", "治理"]);

    const allIds = groups.flatMap((g) => g.items.map((item) => item.id));
    expect(allIds).toEqual(expect.arrayContaining([...primaryNavigationIds]));
    expect(allIds).toHaveLength(navItems.length);
  });

  it("places related modules together", () => {
    const byId = Object.fromEntries(navItems.map((item) => [item.id, item]));
    expect(byId.workflow.section).toBe("构建");
    expect(byId["smart-dag"].section).toBe("构建");
    expect(byId.agents.section).toBe("构建");
    expect(byId["agent-access"].section).toBe("接入");
    expect(byId.providers.section).toBe("接入");
    expect(byId.chat.section).toBe("运行");
    expect(byId.logs.section).toBe("治理");
    expect(byId.users.section).toBe("治理");
  });
});
