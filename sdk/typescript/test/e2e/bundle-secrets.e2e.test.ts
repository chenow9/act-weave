import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const distDir = join(dirname(fileURLToPath(import.meta.url)), "../../dist");

/** Patterns that must never appear in the published SDK bundle. */
const FORBIDDEN = [
  /awsk_live_/i,
  /client_secret\s*[:=]\s*["'][^"']+["']/i,
  /BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY/,
  /subject-token-value/,
  /sdk-contract-exchanged-token/,
  /demo-secret/,
  /bff-secret/,
  /mint-secret/,
];

describe("M9-T7 bundle secret scan", () => {
  it("dist/ exists and contains no test secrets or long-lived credentials", () => {
    const st = statSync(distDir);
    expect(st.isDirectory()).toBe(true);

    const files: string[] = [];
    function walk(dir: string): void {
      for (const name of readdirSync(dir)) {
        const full = join(dir, name);
        if (statSync(full).isDirectory()) {
          walk(full);
        } else if (/\.(js|d\.ts|map)$/.test(name)) {
          files.push(full);
        }
      }
    }
    walk(distDir);
    expect(files.length).toBeGreaterThan(0);

    const hits: string[] = [];
    for (const file of files) {
      const text = readFileSync(file, "utf8");
      for (const pattern of FORBIDDEN) {
        if (pattern.test(text)) {
          hits.push(`${file}: ${pattern}`);
        }
      }
    }
    expect(hits).toEqual([]);
  });
});
