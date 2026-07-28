import { describe, expect, it } from "vitest";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

/**
 * ZKL-64 item 17: Integration facade deleted.
 * Domain ownership lives in providers/connections/tools/openapiImports stores.
 */
describe("integration facade removal (ZKL-64 item 17)", () => {
  it("does not ship the compatibility facade store", () => {
    expect(existsSync(resolve(__dirname, "integration.ts"))).toBe(false);
  });

  it("ships the four domain stores", () => {
    expect(existsSync(resolve(__dirname, "providers.ts"))).toBe(true);
    expect(existsSync(resolve(__dirname, "connections.ts"))).toBe(true);
    expect(existsSync(resolve(__dirname, "tools.ts"))).toBe(true);
    expect(existsSync(resolve(__dirname, "openapiImports.ts"))).toBe(true);
  });
});
