#!/usr/bin/env node
/**
 * ZKL-64 D4-A: hard budgets on Vite build output (gzip).
 * entry JS ≤ 450 KiB gzip, entry CSS ≤ 120 KiB gzip, any single route chunk JS ≤ 350 KiB gzip.
 */
import { createGzip } from "node:zlib";
import { createReadStream, readdirSync, existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { pipeline } from "node:stream/promises";
import { Writable } from "node:stream";

const dist = join(process.cwd(), "dist");
const assets = join(dist, "assets");
const ENTRY_JS_GZIP_KIB = 450;
const ENTRY_CSS_GZIP_KIB = 120;
const ROUTE_JS_GZIP_KIB = 350;

async function gzipSize(filePath) {
  let size = 0;
  const counter = new Writable({
    write(chunk, _enc, cb) {
      size += chunk.length;
      cb();
    },
  });
  await pipeline(createReadStream(filePath), createGzip(), counter);
  return size;
}

function kib(bytes) {
  return (bytes / 1024).toFixed(2);
}

async function main() {
  if (!existsSync(assets)) {
    console.error("dist/assets missing — run npm run build first");
    process.exit(1);
  }
  const files = readdirSync(assets);
  const jsFiles = files.filter((f) => f.endsWith(".js"));
  const cssFiles = files.filter((f) => f.endsWith(".css"));
  const indexHtml = readFileSync(join(dist, "index.html"), "utf8");
  const entryJs = jsFiles.find((f) => indexHtml.includes(f)) || jsFiles.find((f) => f.startsWith("index-"));
  const entryCss = cssFiles.find((f) => indexHtml.includes(f)) || cssFiles.find((f) => f.startsWith("index-"));
  if (!entryJs || !entryCss) {
    console.error("could not resolve entry JS/CSS from index.html");
    process.exit(1);
  }

  const failures = [];
  const entryJsSize = await gzipSize(join(assets, entryJs));
  const entryCssSize = await gzipSize(join(assets, entryCss));
  console.log(`entry JS  ${entryJs}: ${kib(entryJsSize)} KiB gzip (budget ${ENTRY_JS_GZIP_KIB})`);
  console.log(`entry CSS ${entryCss}: ${kib(entryCssSize)} KiB gzip (budget ${ENTRY_CSS_GZIP_KIB})`);
  if (entryJsSize / 1024 > ENTRY_JS_GZIP_KIB) failures.push(`entry JS gzip ${kib(entryJsSize)} > ${ENTRY_JS_GZIP_KIB}`);
  if (entryCssSize / 1024 > ENTRY_CSS_GZIP_KIB) failures.push(`entry CSS gzip ${kib(entryCssSize)} > ${ENTRY_CSS_GZIP_KIB}`);

  for (const f of jsFiles) {
    if (f === entryJs) continue;
    const size = await gzipSize(join(assets, f));
    console.log(`route JS  ${f}: ${kib(size)} KiB gzip (budget ${ROUTE_JS_GZIP_KIB})`);
    if (size / 1024 > ROUTE_JS_GZIP_KIB) failures.push(`route JS ${f} gzip ${kib(size)} > ${ROUTE_JS_GZIP_KIB}`);
  }

  if (failures.length) {
    console.error("\nbundle:check FAILED:\n" + failures.map((f) => ` - ${f}`).join("\n"));
    process.exit(1);
  }
  console.log("\nbundle:check PASSED");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
