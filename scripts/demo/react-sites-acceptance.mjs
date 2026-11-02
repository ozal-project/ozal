// Verify that Sites can recover after Pages is enabled and that restoring a
// retained deployment reviews the mapped hostname before making one write.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-sites-acceptance.mjs
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { spawn } from "node:child_process";
import { promisify } from "node:util";
import { chromium } from "playwright-core";
import { startDemoEdge } from "./edge.mjs";

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");

const root = await mkdtemp(join(tmpdir(), "ozal-react-sites-"));
const reactEnv = { ...process.env, OZAL_REACT_OUT_DIR: join(root, "react-dist") };
let edge, preview, browser, step = "start";
let pagesEnabled = false;
let liveID = "a".repeat(40);
let siteReads = 0;
const rollbackRequests = [];
const errors = [];
const now = new Date().toISOString();
const versions = [
  { site: "docs", id: "a".repeat(40), created_at: now, files: 4, bytes: 8192 },
  { site: "docs", id: "b".repeat(40), created_at: new Date(Date.now() - 86_400_000).toISOString(), files: 3, bytes: 4096 },
];

async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once("error", reject).listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  return port;
}

async function waitForPreview(url) {
  for (let i = 0; i < 100; i++) {
    if (preview.exitCode !== null) throw new Error("React preview exited before becoming ready");
    try { if ((await fetch(url)).ok) return; } catch { /* Vite is starting. */ }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("React preview did not become ready");
}

try {
  step = "start disposable edge and React production preview";
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, dynamicPorts: true });
  await exec("npm", ["run", "react:build"], { cwd: join(process.cwd(), "web/console"), env: reactEnv, maxBuffer: 4 << 20 });
  const port = await freePort();
  const previewURL = `http://ozal.localhost:${port}`;
  preview = spawn(process.execPath, [join(process.cwd(), "web/console/node_modules/vite/bin/vite.js"), "preview", "--config", "vite.react.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
    cwd: join(process.cwd(), "web/console"), env: { ...reactEnv, OZAL_ADMIN: edge.consoleURL }, stdio: ["ignore", "pipe", "pipe"],
  });
  preview.stdout.resume(); preview.stderr.resume();
  await waitForPreview(previewURL);
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));

  await page.route("**/_/api/sites", (route) => {
    siteReads++;
    if (!pagesEnabled) return route.fulfill({ status: 404, json: { error: "Pages are not enabled on this edge." } });
    return route.fulfill({ json: { sites: [{ name: "docs", live: liveID, changed_at: now, deployments: versions.map((version) => ({ ...version, live: version.id === liveID })) }] } });
  });
  await page.route("**/_/api/status", (route) => route.fulfill({ json: { generated_at: new Date().toISOString(), routes: [{ host: "docs.example.com", is_static: true, target: "site:docs" }] } }));
  await page.route("**/_/api/projects", (route) => route.fulfill({ json: { projects: [], sites: ["docs"], config_sha256: "d".repeat(64) } }));
  await page.route("**/_/api/github/repositories**", (route) => route.fulfill({ json: { configured: false, repositories: [], page: 1, more: false } }));
  await page.route("**/_/api/sites/docs/rollback", async (route) => {
    rollbackRequests.push({ body: route.request().postDataJSON(), authorization: (await route.request().allHeaders()).authorization });
    liveID = "b".repeat(40);
    return route.fulfill({ json: { ...versions[1], live: true } });
  });

  step = "host-issued operator sign-in";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "React Sites acceptance", "-url", previewURL]);
  const loginURL = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(loginURL, "the host printed no one-use login URL");
  const overviewSitesRead = page.waitForResponse((response) => response.url().includes("/_/api/sites"));
  await page.goto(loginURL);
  await page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
  await overviewSitesRead;
  assert.equal(siteReads, 1, "Overview did not load its live-sites summary exactly once");
  siteReads = 0;

  step = "recover Sites after Pages becomes available";
  await page.goto(`${previewURL}/sites`);
  await page.getByRole("heading", { name: "Not enabled on this edge" }).waitFor();
  const refresh = page.getByRole("button", { name: "Refresh sites" });
  await refresh.waitFor();
  assert.equal(siteReads, 1, "initial Pages check did not run exactly once");
  pagesEnabled = true;
  await refresh.click();
  await page.getByRole("heading", { name: "docs", exact: true }).waitFor();
  assert.equal(siteReads, 2, "refresh did not retry the Pages endpoint");

  step = "review rollback hostnames and ensure cancellation is read-only";
  await page.getByRole("button", { name: `Restore ${"b".repeat(12)} for docs` }).click();
  const review = page.getByRole("dialog", { name: "Restore this version?" });
  await review.getByText("docs.example.com").waitFor();
  await review.getByRole("button", { name: "Cancel" }).click();
  assert.equal(rollbackRequests.length, 0, "cancelling rollback sent a mutation request");

  step = "confirm rollback and reconcile the live deployment";
  await page.getByRole("button", { name: `Restore ${"b".repeat(12)} for docs` }).click();
  await review.getByRole("button", { name: "Restore version" }).click();
  await page.getByRole("status").filter({ hasText: "docs now serves bbbbbbbbbbbb" }).waitFor();
  assert.equal(rollbackRequests.length, 1, "rollback was not sent exactly once");
  assert.deepEqual(rollbackRequests[0].body, { id: "b".repeat(40) }, "rollback did not target the reviewed deployment");
  await page.getByRole("list", { name: "Deployments for docs" }).getByText("live", { exact: true }).waitFor();
  assert.deepEqual(errors, [], "uncaught browser errors occurred");
  console.log("React Sites acceptance passed: refresh recovers from Pages-disabled state; rollback review shows mapped host, cancel sends no write, and confirmation restores the selected deployment.");
} catch (error) {
  console.error(`React Sites acceptance failed at ${step}: ${error.message}`);
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (preview && preview.exitCode === null) {
    const exited = new Promise((resolve) => preview.once("exit", resolve));
    preview.kill("SIGTERM");
    await Promise.race([exited, new Promise((resolve) => setTimeout(() => { preview.kill("SIGKILL"); resolve(); }, 5000))]);
  }
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
