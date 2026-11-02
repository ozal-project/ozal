// Verify that opening a GitHub workflow pull request requires a separate
// confirmation after reviewing the generated workflow.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-publishing-acceptance.mjs
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

const root = await mkdtemp(join(tmpdir(), "ozal-react-publishing-"));
const reactEnv = { ...process.env, OZAL_REACT_OUT_DIR: join(root, "react-dist") };
let edge, preview, browser, page, step = "start";
const errors = [];
const workflowCalls = [];

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
  await context.addInitScript(() => {
    const violations = [];
    Object.defineProperty(window, "__cspViolations", { value: violations });
    addEventListener("securitypolicyviolation", (event) => violations.push({ directive: event.effectiveDirective, blocked: event.blockedURI, sample: event.sample }));
  });
  page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));

  const project = {
    name: "demo", site: "demo", hostnames: ["demo.example.com"],
    deployers: [{ type: "github", github: { repository_id: 41, repository_owner_id: 73, ref: "refs/heads/main", workflow_ref: "octo/demo/.github/workflows/deploy.yml@refs/heads/main" } }],
  };
  await page.route("**/_/api/sites", (route) => route.fulfill({ json: { sites: [] } }));
  await page.route("**/_/api/status", (route) => route.fulfill({ json: { generated_at: new Date().toISOString(), routes: [] } }));
  await page.route("**/_/api/github/repositories**", (route) => route.fulfill({ json: { configured: true, installed: true, repositories: [{ id: 41, owner: { id: 73 }, full_name: "octo/demo", default_branch: "main", private: true }], page: 1, more: false } }));
  await page.route("**/_/api/projects", (route) => route.fulfill({ json: { projects: [project], sites: ["demo"], config_sha256: "d".repeat(64) } }));
  await page.route("**/_/api/projects/demo/workflow", async (route) => {
    const body = route.request().postDataJSON();
    workflowCalls.push(body);
    if (body.apply) return route.fulfill({ status: 201, json: { applied: true, pull_request: { url: "https://github.com/octo/demo/pull/8", number: 8, branch: "ozal/publish-demo" } } });
    return route.fulfill({ json: {
      applied: false,
      expected: "d".repeat(64),
      plan: { project: "demo", repository: "octo/demo", ref: "refs/heads/main", filename: "deploy.yml", endpoint: "https://deploy.example.net", site_url: "https://demo.example.com", directory: ".", output: "dist", base: "main", branch: "ozal/publish-demo", path: ".github/workflows/deploy.yml", content: "name: Publish", digest: "e".repeat(64) },
    } });
  });

  step = "host-issued operator sign-in";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "React publishing acceptance", "-url", previewURL]);
  const loginURL = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(loginURL, "the host printed no one-use login URL");
  await page.goto(loginURL);
  await page.locator("#main-content h1", { hasText: "Overview" }).waitFor();

  step = "review workflow, cancel confirmation, then explicitly authorize pull request";
  await page.goto(`${previewURL}/sites`);
  await page.getByRole("button", { name: "Prepare publishing workflow for demo" }).click();
  const dialog = page.getByRole("dialog", { name: "Publish demo from GitHub" });
  await dialog.getByRole("button", { name: "Preview workflow" }).click();
  await dialog.getByText("Add .github/workflows/deploy.yml to octo/demo").waitFor();
  assert.equal(workflowCalls.length, 1, "preview should be the only request before final confirmation");

  step = "open final pull request confirmation";
  await dialog.getByRole("button", { name: "Review pull request" }).click({ timeout: 5000 });
  const confirmation = page.getByRole("dialog", { name: "Open this pull request?" });
  await confirmation.waitFor();
  assert.equal(workflowCalls.length, 1, "opening the confirmation stage sent a GitHub write request");
  await confirmation.getByRole("button", { name: "Cancel" }).click();
  assert.equal(workflowCalls.length, 1, "cancelling the final confirmation sent a GitHub write request");

  step = "reopen final pull request confirmation after cancellation";
  await dialog.getByRole("button", { name: "Review pull request" }).click({ timeout: 5000 });
  await page.getByRole("dialog", { name: "Open this pull request?" }).getByRole("button", { name: "Open pull request" }).click();
  await dialog.getByText("Pull request created.").waitFor();
  assert.equal(workflowCalls.length, 2, "explicit confirmation did not create exactly one pull request");
  assert.deepEqual(workflowCalls[0], { directory: ".", output: "dist", hostname: "demo.example.com", page: 1 });
  assert.deepEqual(workflowCalls[1], { directory: ".", output: "dist", hostname: "demo.example.com", page: 1, expected: "d".repeat(64), digest: "e".repeat(64), apply: true });
  const cspViolations = await page.evaluate(() => window.__cspViolations);
  assert.deepEqual(cspViolations, [], `CSP violations occurred while using modal dialogs: ${JSON.stringify(cspViolations)}`);
  assert.deepEqual(errors, [], "uncaught browser errors occurred");
  console.log("React publishing acceptance passed: workflow preview, explicit final review, cancellation without a write, then one confirmed pull request. GitHub responses were fixtures.");
} catch (error) {
  console.error(`React publishing acceptance failed at ${step}: ${error.message}`);
  if (page) console.error(JSON.stringify(await page.evaluate(() => ({ dialogs: [...document.querySelectorAll("dialog")].map((dialog) => ({ open: dialog.open, name: dialog.getAttribute("aria-label"), text: dialog.innerText.slice(0, 500) })), body: document.body.innerText.slice(-1200) })).catch(() => null)));
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
