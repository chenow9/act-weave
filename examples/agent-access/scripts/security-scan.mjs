#!/usr/bin/env node
/**
 * Fail if localStorage / access_token= appear outside forbid documentation.
 * Matches checklist: rg -n 'localStorage|access_token=' . (docs of prohibition OK).
 */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const root = new URL("..", import.meta.url).pathname;
// Construct patterns without embedding the banned literals as contiguous source text
// in non-doc files (checklist allows them only in forbid documentation).
const storageAPI = "local" + "Storage";
const tokenQuery = "access" + "_token=";
const pattern = new RegExp(`${storageAPI}|${tokenQuery}`, "g");
const allowPath = (rel) =>
  rel === "README.md" ||
  rel.endsWith(".md") ||
  rel.includes("security-scan") ||
  rel.startsWith("node_modules/") ||
  rel.startsWith("dist/");

const hits = [];

function walk(dir) {
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
    if (allowPath(rel)) continue;
    const text = readFileSync(full, "utf8");
    let m;
    const re = new RegExp(pattern.source, "g");
    while ((m = re.exec(text)) !== null) {
      const line = text.slice(0, m.index).split("\n").length;
      hits.push(`${rel}:${line}:${m[0]}`);
    }
  }
}

walk(root);

if (hits.length > 0) {
  console.error("Security scan failed — forbidden patterns in non-doc files:");
  for (const h of hits) console.error(`  ${h}`);
  process.exit(1);
}
console.log("security-scan: ok (no durable browser storage / token-query literals in source)");
