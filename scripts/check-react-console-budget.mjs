import { readdir, readFile } from "node:fs/promises";
import { basename } from "node:path";
import { fileURLToPath } from "node:url";

const dist = fileURLToPath(new URL("../.local/ui-012/react-dist/index.html", import.meta.url));
const html = await readFile(dist, "utf8");
const outputAssetNames = (await readdir(new URL("../.local/ui-012/react-dist/assets/", import.meta.url)))
  .filter((name) => /\.(?:js|css)$/.test(name));
let buildOutput = "";
for await (const chunk of process.stdin) buildOutput += chunk;

const assets = new Map();
const cleanOutput = buildOutput.replace(/\u001b\[[0-9;]*m/g, "");
for (const line of cleanOutput.split(/\r?\n/)) {
  const match = line.match(/\/assets\/([^\s]+\.(?:js|css))\s+[\d.]+\s+kB\s+│\s+gzip:\s*([\d.]+)\s+kB/);
  if (match) assets.set(match[1], Number(match[2]));
}

const missingAssets = outputAssetNames.filter((name) => !assets.has(name));
const unexpectedAssets = [...assets.keys()].filter((name) => !outputAssetNames.includes(name));
if (missingAssets.length || unexpectedAssets.length) {
  throw new Error(`React console build report does not match emitted JS/CSS assets (missing: ${missingAssets.join(", ") || "none"}; unexpected: ${unexpectedAssets.join(", ") || "none"})`);
}

const initialScripts = new Set();
for (const [tag] of html.matchAll(/<(?:script|link)\b[^>]*>/gi)) {
  const source = tag.match(/\bsrc="([^"]+)"/i)?.[1];
  const href = tag.match(/\bhref="([^"]+)"/i)?.[1];
  if (/<script\b/i.test(tag) && source && /\.js(?:$|\?)/.test(source)) {
    initialScripts.add(basename(new URL(source, "https://ozal.invalid").pathname));
  }
  if (/<link\b/i.test(tag) && /\bmodulepreload\b/i.test(tag) && href && /\.js(?:$|\?)/.test(href)) {
    initialScripts.add(basename(new URL(href, "https://ozal.invalid").pathname));
  }
}

const allScripts = [...assets.keys()].filter((name) => name.endsWith(".js"));
const stylesheets = [...assets.keys()].filter((name) => name.endsWith(".css"));
if (initialScripts.size === 0 || [...initialScripts].some((name) => !assets.has(name))) {
  throw new Error("React console build report has no recognized initial JavaScript asset");
}
if (allScripts.length === initialScripts.size) {
  throw new Error("React console build report has no lazy JavaScript chunk");
}
if (stylesheets.length === 0) {
  throw new Error("React console build report has no CSS assets");
}

const entryKB = [...initialScripts].reduce((sum, name) => sum + assets.get(name), 0);
const lazyKB = allScripts.filter((name) => !initialScripts.has(name)).reduce((sum, name) => sum + assets.get(name), 0);
const cssKB = stylesheets.reduce((sum, name) => sum + assets.get(name), 0);
const totalKB = entryKB + lazyKB + cssKB;
const limits = { entry: 110, lazy: 200, css: 20, total: 260 };
const failures = [
  ["entry", entryKB, limits.entry],
  ["lazy console", lazyKB, limits.lazy],
  ["CSS", cssKB, limits.css],
  ["total", totalKB, limits.total],
].filter(([, actual, limit]) => actual > limit);

console.log(`React console gzip: entry ${entryKB.toFixed(2)}/${limits.entry} KB; lazy ${lazyKB.toFixed(2)}/${limits.lazy} KB; CSS ${cssKB.toFixed(2)}/${limits.css} KB; total ${totalKB.toFixed(2)}/${limits.total} KB`);
if (failures.length) {
  for (const [name, actual, limit] of failures) {
    console.error(`React console ${name} is ${actual.toFixed(2)} KB, over the ${limit} KB budget`);
  }
  process.exitCode = 1;
}
