import { describe, expect, it } from "vitest";

/**
 * ZKL-64 item 8: shell contracts moved to AppShell.access.test.ts (DOM/store/nav behavior).
 * This file remains as a pointer so historical imports/paths do not break CI discovery.
 */
describe("app shell content (migrated)", () => {
  it("defers to AppShell.access behavior coverage", () => {
    expect(true).toBe(true);
  });
});
