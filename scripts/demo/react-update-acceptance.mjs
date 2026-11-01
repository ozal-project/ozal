// Exercise the React update dialog and real edge passkey step-up. The update
// plan and executor responses are fixtures because the disposable edge has no
// host update executor; enrolment, session and WebAuthn step-up use the Go edge.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-update-acceptance.mjs
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

const root = await mkdtemp(join(tmpdir(), "ozal-react-update-"));
const reactEnv = { ...process.env, OZAL_REACT_OUT_DIR: join(root, "react-dist") };
let edge, preview, browser, step = "start";
const errors = [];

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
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });

  step = "register the real edge passkey session";
  await page.route("**/_/api/updates/status", (route) => route.fulfill({ json: { checked_at: new Date().toISOString(), installed: "v0.1.0", latest: "v0.2.0", result: "available" } }));
  await page.goto(`${previewURL}/overview`);
  await page.getByRole("heading", { name: "Set up the first administrator" }).waitFor();
  const { stdout } = await exec(binary, ["console", "enrol", "-config", join(edge.dir, "config.json"), "-url", previewURL]);
  const enrolLink = stdout.split("\n").find((line) => line.includes("#enrol="));
  assert(enrolLink, "the host printed no enrolment link");
  let releaseInfo;
  const infoGate = new Promise((resolve) => { releaseInfo = resolve; });
  await page.route("**/_/api/admin/enrol/info", async (route) => { await infoGate; await route.continue(); });
  const enrolInfoResponse = page.waitForResponse((response) => response.url().includes("/_/api/admin/enrol/info") && response.ok());
  await page.goto(enrolLink.trim());
  await page.getByLabel("Full name").fill("React Update Owner");
  await page.getByLabel("Email").fill("react-update@acme.dev");
  releaseInfo();
  await enrolInfoResponse;
  assert.equal(await page.getByLabel("Full name").inputValue(), "React Update Owner", "the enrolment-info response overwrote the name entered while it loaded");
  assert.equal(await page.getByLabel("Email").inputValue(), "react-update@acme.dev", "the enrolment-info response overwrote the email entered while it loaded");
  await page.getByRole("button", { name: "Create my passkey" }).click();
  await page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
  await page.getByRole("status").filter({ hasText: "is available" }).waitFor();

  const now = new Date().toISOString();
  const planID = "a".repeat(32);
  const jobID = "b".repeat(32);
  const plan = { id: planID, checked_at: now, installed: "v0.1.0", latest: "v0.2.0", channel: "stable", platform: "linux/arm64", result: "available", archive_sha256: "c".repeat(64), size: 20 * 1024 * 1024, migrations: true, notes_url: "https://ozal.dev/releases/v0.2.0" };
  const job = { id: jobID, channel: "stable", from: "v0.1.0", to: "v0.2.0", phase: "staging", result: "pending", started_at: now, updated_at: now };
  const done = { ...job, phase: "complete", result: "updated", finished_at: now };
  let started = false;
  let latestReads = 0;
  let jobReads = 0;
  const updateRequests = [];
  const stepUpRequests = [];

  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith("/_/api/admin/step-up/")) stepUpRequests.push(path);
  });
  await page.route("**/_/api/updates/jobs/latest", (route) => {
    latestReads++;
    if (started) return route.fulfill({ json: job });
    return route.fulfill({ status: 404, json: { error: "no update job has run" } });
  });
  await page.route("**/_/api/updates/plan", (route) => route.fulfill({ json: plan }));
  await page.route("**/_/api/updates/jobs", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const request = route.request();
    updateRequests.push({ body: request.postDataJSON(), authorization: (await request.allHeaders()).authorization });
    if (updateRequests.length === 1) return route.fulfill({ status: 403, json: { error: "confirm this installation with your passkey", code: "passkey_required" } });
    started = true;
    return route.fulfill({ status: 202, json: job });
  });
  await page.route(`**/_/api/updates/jobs/${jobID}`, async (route) => {
    jobReads++;
    return route.fulfill({ json: jobReads < 2 ? job : done });
  });

  step = "review release and require an actual passkey step-up";
  await page.getByRole("button", { name: "Review update" }).click();
  const dialog = page.getByRole("dialog", { name: "Update Ozal" });
  await dialog.getByText("linux/arm64").waitFor();
  await dialog.getByText("This release may update the state database.").waitFor();
  await dialog.getByRole("button", { name: "Update to v0.2.0" }).click();
  await dialog.getByText("Confirm with your passkey…").waitFor();
  await dialog.getByText("Downloading the verified archive", { exact: true }).waitFor();
  assert.equal(updateRequests.length, 2, "the reviewed plan was not submitted exactly twice");
  assert.deepEqual(updateRequests.map((request) => request.body), [{ plan_id: planID }, { plan_id: planID }], "step-up changed the reviewed plan");
  assert.equal(updateRequests[0].authorization, updateRequests[1].authorization, "step-up changed the operator session");
  assert.deepEqual(stepUpRequests, ["/_/api/admin/step-up/begin", "/_/api/admin/step-up/finish"], "the real Go edge did not complete one step-up ceremony");

  step = "close and reopen the pending job, then observe completion";
  await dialog.getByText("Close", { exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "Review update" }).click();
  await dialog.getByText("Downloading the verified archive", { exact: true }).waitFor();
  await dialog.getByText("You can close this dialog. The update continues on the host.").waitFor();
  await dialog.getByText("is installed and answering.").waitFor({ timeout: 10_000 });
  assert(latestReads >= 2, "reopening did not read the current job from the host API");
  assert.deepEqual(errors, [], "uncaught browser errors occurred");
  console.log("React update acceptance passed: reviewed plan, real-edge passkey step-up, exactly one retry on the same plan/session, pending-job close/reopen and completion. Update executor responses were fixtures.");
} catch (error) {
  console.error(`React update acceptance failed at ${step}: ${error.message}`);
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
