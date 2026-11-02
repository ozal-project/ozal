// Exercise the React activity screen with a controlled SSE stream and a
// disposable authenticated Go edge for the remaining APIs.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-activity-acceptance.mjs
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

const root = await mkdtemp(join(tmpdir(), "ozal-react-activity-"));
const reactEnv = { ...process.env, OZAL_REACT_OUT_DIR: join(root, "react-dist") };
let edge, preview, browser, page, step = "start";
const errors = [];
let activeCSP;

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
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, dynamicPorts: true, auditAccess: true });
  await exec("npm", ["run", "react:build"], { cwd: join(process.cwd(), "web/console"), env: reactEnv, maxBuffer: 4 << 20 });
  const port = await freePort();
  const previewURL = `http://127.0.0.1:${port}`;
  preview = spawn(process.execPath, [join(process.cwd(), "web/console/node_modules/vite/bin/vite.js"), "preview", "--config", "vite.react.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
    cwd: join(process.cwd(), "web/console"), env: { ...reactEnv, OZAL_ADMIN: edge.consoleURL }, stdio: ["ignore", "pipe", "pipe"],
  });
  preview.stdout.resume(); preview.stderr.resume();
  await waitForPreview(previewURL);
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.addInitScript(() => {
    const state = { requests: [], controller: null, fail: false };
    const cspViolations = [];
    Object.defineProperty(window, "__activityCSPViolations", { value: cspViolations });
    addEventListener("securitypolicyviolation", (event) => cspViolations.push({ directive: event.effectiveDirective, policy: event.originalPolicy, blocked: event.blockedURI, sample: event.sample, source: event.sourceFile, line: event.lineNumber }));
    const nativeFetch = window.fetch.bind(window);
    const publish = (id, event) => {
      if (!state.controller) throw new Error("no active event stream");
      state.controller.enqueue(new TextEncoder().encode(`id: ${id}\ndata: ${JSON.stringify(event)}\n\n`));
    };
    const api = {
      requests: state.requests,
      publish,
      publishBatch(chunk) {
        if (!state.controller) throw new Error("no active event stream");
        state.controller.enqueue(new TextEncoder().encode(chunk));
      },
      drop() { state.fail = true; state.controller?.error(new Error("acceptance disconnect")); state.controller = null; },
      restore() { state.fail = false; },
    };
    Object.defineProperty(window, "__activityAcceptance", { value: api });
    window.fetch = (input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (new URL(url, location.href).pathname !== "/_/events") return nativeFetch(input, init);
      const headers = new Headers(init?.headers);
      state.requests.push({ authorization: headers.get("Authorization"), lastEventId: headers.get("Last-Event-ID") });
      if (state.fail) return Promise.reject(new TypeError("acceptance stream offline"));
      return Promise.resolve(new Response(new ReadableStream({ start(controller) { state.controller = controller; } }), { headers: { "Content-Type": "text/event-stream" } }));
    };
  });
  page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error") errors.push({ text: message.text(), location: message.location() }); });

  step = "host-issued operator sign-in";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "React activity acceptance", "-url", previewURL]);
  const loginURL = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(loginURL, "the host printed no one-use login URL");
  await page.goto(loginURL);
  await page.getByRole("heading", { name: "Overview" }).waitFor();

  step = "live event delivery and deduplication";
  const activityResponse = await page.goto(`${previewURL}/activity`);
  activeCSP = { header: activityResponse?.headers()["content-security-policy"], meta: await page.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute("content") };
  await page.getByRole("heading", { name: "Activity" }).waitFor();
  await page.getByText("Live event stream").waitFor();
  const event = (type, actor, host, reason = "") => ({ type, actor, host, reason, remote: "127.0.0.1", time: new Date().toISOString() });
  const publish = (id, value) => page.evaluate(({ id, value }) => window.__activityAcceptance.publish(id, value), { id, value });
  const count = async (shown, retained = shown) => page.getByRole("heading", { name: `${shown} shown · ${retained} retained`, exact: true }).waitFor();
  const denied = event("access.denied", "alice@example.test", "grafana.example.test", "Group required");
  await publish("a-1", denied);
  await page.getByRole("row").filter({ hasText: "grafana.example.test" }).getByText("Access denied").waitFor();
  await count(1);
  await publish("a-1", denied);
  await page.waitForTimeout(100);
  await count(1);
  assert.equal(await page.locator("tbody tr").count(), 1, "a duplicate event ID appeared twice");

  step = "category and text filters";
  await publish("a-2", event("operator.signed_in", "operator@example.test", "console"));
  await count(2);
  await page.getByRole("combobox", { name: "Event category" }).selectOption("access");
  await count(1, 2);
  await page.getByRole("textbox", { name: "Search events" }).fill("grafana");
  await count(1, 2);
  await page.getByRole("textbox", { name: "Search events" }).fill("no-such-host");
  await page.getByText("No matching events").waitFor();
  await page.getByRole("textbox", { name: "Search events" }).fill("");
  await page.getByRole("combobox", { name: "Event category" }).selectOption("all");
  await count(2);

  step = "pause holds new events until resume";
  await page.getByRole("button", { name: "Pause" }).click();
  await publish("a-3", event("access.allowed", "bob@example.test", "wiki.example.test"));
  await count(2);
  await page.getByRole("button", { name: "Resume" }).click();
  await count(3);
  await page.getByRole("row").filter({ hasText: "wiki.example.test" }).getByText("Access allowed").waitFor();

  step = "lost stream recovery resumes from last event ID";
  await page.evaluate(() => window.__activityAcceptance.drop());
  await page.getByText("Connection lost · retrying").waitFor();
  await page.evaluate(() => window.__activityAcceptance.restore());
  await page.getByText("Live · reconnected").waitFor({ timeout: 12_000 });
  const requests = await page.evaluate(() => window.__activityAcceptance.requests);
  assert(requests.length >= 2, "the event stream did not reconnect");
  assert.match(requests[0].authorization ?? "", /^Bearer .+/, "the event stream omitted bearer authentication");
  assert.equal(requests.at(-1).lastEventId, "a-3", "reconnection did not send Last-Event-ID");

  step = "bounded feed retains only the newest 500 events";
  const batch = Array.from({ length: 510 }, (_, index) => {
    const seq = 4 + index;
    return `id: a-${seq}\ndata: ${JSON.stringify({ type: "access.allowed", actor: `person-${seq}@example.test`, host: "wiki.example.test", time: new Date().toISOString() })}\n\n`;
  }).join("");
  await page.evaluate((chunk) => window.__activityAcceptance.publishBatch(chunk), batch);
  await page.getByRole("heading", { name: "500 shown · 500 retained", exact: true }).waitFor({ timeout: 10_000 });
  assert.equal(await page.locator("tbody tr").count(), 500, "the DOM retained more than 500 desktop event rows");

  for (const width of [1440, 1024, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    const size = await page.evaluate(() => ({ width: document.documentElement.scrollWidth, viewport: innerWidth }));
    assert(size.width <= width, `horizontal overflow at ${width}px: ${JSON.stringify(size)}`);
  }
  const cspViolations = await page.evaluate(() => window.__activityCSPViolations);
  assert.deepEqual(cspViolations, [], `browser reported CSP violations: ${JSON.stringify(cspViolations)}`);
  assert.deepEqual(errors, [], `browser reported errors: ${JSON.stringify(errors)}`);
  console.log("React activity acceptance passed: live SSE fixture, deduplication, category/search filters, pause/resume, lost/reconnected state with Last-Event-ID, 500-event retention, and no horizontal overflow at four widths.");
} catch (error) {
  console.error(`React activity acceptance failed during ${step}: ${error.message}`);
  if (activeCSP) console.error(JSON.stringify(activeCSP));
  if (page) console.error(JSON.stringify(await page.evaluate(() => window.__activityCSPViolations).catch(() => [])));
  if (page) console.error((await page.locator("body").innerText().catch(() => "")).slice(0, 1000));
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (preview && preview.exitCode === null) {
    preview.kill("SIGTERM");
    await new Promise((resolve) => preview.once("exit", resolve));
  }
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
