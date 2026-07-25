import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("security scan", () => {
  it("forbids durable-storage and token-query literals outside documentation", () => {
    const hits: string[] = [];
    // Built without writing the forbidden literals into this assertion file itself.
    const storageAPI = "local" + "Storage";
    const tokenQuery = "access" + "_token=";
    const pattern = new RegExp(`${storageAPI}|${tokenQuery}`, "g");

    function walk(dir: string): void {
      for (const name of readdirSync(dir)) {
        if (name === "node_modules" || name === ".git") continue;
        const full = join(dir, name);
        const st = statSync(full);
        if (st.isDirectory()) {
          walk(full);
          continue;
        }
        if (!/\.(ts|js|mjs|cjs|json|tsx|jsx)$/.test(name) && name !== ".env.example") {
          continue;
        }
        const rel = relative(root, full);
        if (rel.endsWith(".md") || rel.includes("security-scan")) continue;
        const text = readFileSync(full, "utf8");
        let m: RegExpExecArray | null;
        const re = new RegExp(pattern.source, "g");
        while ((m = re.exec(text)) !== null) {
          const line = text.slice(0, m.index).split("\n").length;
          hits.push(`${rel}:${line}:${m[0]}`);
        }
      }
    }

    walk(root);
    expect(hits).toEqual([]);
  });

  it("scripts/security-scan.mjs exits 0", () => {
    const out = execFileSync(process.execPath, [join(root, "scripts/security-scan.mjs")], {
      encoding: "utf8",
    });
    expect(out).toMatch(/ok/);
  });

  it("README documents the storage prohibitions", () => {
    const readme = readFileSync(join(root, "README.md"), "utf8");
    // Forbidden patterns are allowed only in documentation of the prohibition.
    expect(readme).toMatch(/localStorage/);
    expect(readme).toMatch(/access_token=/);
    expect(readme).toMatch(/BFF/);
    expect(readme).toMatch(/Token Exchange/);
  });
});
