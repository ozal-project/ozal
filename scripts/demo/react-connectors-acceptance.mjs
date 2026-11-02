// Exercise connector states and the five-step guide in the production React
// preview. The registry is a fixture; the console session uses a disposable
// local Go edge.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-connectors-acceptance.mjs
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

const root = await mkdtemp(join(tmpdir(), "ozal-react-connectors-"));
const reactEnv = { ...process.env, OZAL_REACT_OUT_DIR: join(root, "react-dist") };
let edge, preview, browser, step = "start";
let registry = [];
let authorized = false;
const errors = [];
const daysFromNow = (days) => new Date(Date.now() + days * 86_400_000).toISOString();
const connector = (name, changes = {}) => ({ name, sha256: name.charCodeAt(0).toString(16).padStart(2, "0").repeat(32), created: new Date().toISOString(), hosts: [], connected: false, ...changes });

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
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, dynamicPorts: true });
  await exec("npm", ["run", "react:build"], { cwd: join(process.cwd(), "web/console"), env: reactEnv, maxBuffer: 4 << 20 });
  const port = await freePort();
  const previewURL = `http://127.0.0.1:${port}`;
  preview = spawn(process.execPath, [join(process.cwd(), "web/console/node_modules/vite/bin/vite.js"), "preview", "--config", "vite.react.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
    cwd: join(process.cwd(), "web/console"), env: { ...reactEnv, OZAL_ADMIN: edge.consoleURL }, stdio: ["ignore", "pipe", "pipe"],
  });
  preview.stdout.resume(); preview.stderr.resume();
  await waitForPreview(previewURL);
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
  await page.route("**/_/api/connectors", async (route) => {
    authorized ||= !!(await route.request().allHeaders()).authorization;
    await route.fulfill({ json: { connectors: registry } });
  });

  step = "host-issued operator sign-in";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "React connectors acceptance", "-url", previewURL]);
  const loginURL = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(loginURL, "the host printed no one-use login URL");
  await page.goto(loginURL);
  await page.getByRole("heading", { name: "Overview" }).waitFor();

  step = "all registry states and certificate expiry";
  registry = [
    connector("connected-edge", { hosts: ["app.example.test"], connected: true, not_after: daysFromNow(40) }),
    connector("home-edge", { hosts: ["private.example.test"], not_after: daysFromNow(5) }),
    connector("idle-edge"),
    connector("revoked-edge", { revoked: new Date().toISOString(), connected: true, not_after: daysFromNow(-2) }),
  ];
  await page.goto(`${previewURL}/connectors`);
  await page.getByRole("heading", { name: "Connectors" }).waitFor();
  await page.getByRole("heading", { name: "connected-edge" }).waitFor();
  assert.equal(authorized, true, "the registry request omitted the bearer token");
  const card = (name) => page.locator("article").filter({ has: page.getByRole("heading", { name }) });
  assert.match(await card("connected-edge").innerText(), /Connected/);
  assert.match(await card("home-edge").innerText(), /Not connected/);
  assert.match(await card("home-edge").innerText(), /days left/);
  assert.match(await card("idle-edge").innerText(), /No app assigned/);
  assert.match(await card("idle-edge").innerText(), /Expiry unknown/);
  assert.match(await card("revoked-edge").innerText(), /Revoked/);
  assert.match(await card("revoked-edge").innerText(), /Expired/);
  assert.match(await page.locator("[aria-label='Connectors summary']").innerText(), /3\s+enrolled[\s\S]*1\s+connected[\s\S]*1\s+certificates expiring soon/);

  step = "five-step guide validates names and shell commands";
  await page.getByRole("button", { name: "Connect a private host" }).first().click();
  const guide = page.getByRole("region", { name: "New private host" });
  await guide.getByRole("heading", { name: "Connect it in five steps" }).waitFor();
  assert.equal(await guide.locator("ol > li").count(), 5, "the guide did not show all five steps");
  const name = guide.getByLabel("Name for this host");
  await name.fill("home; touch /tmp/not-run");
  await guide.getByRole("alert").getByText("Use lowercase letters, digits and single dashes.").waitFor();
  assert.match(await guide.locator("ol > li").nth(0).locator("code").innerText(), /-name NAME$/);
  await name.fill("home-edge");
  await guide.getByText("Enrolled: certificate").waitFor();
  const enrolStep = guide.locator("ol > li").nth(3);
  const connectStep = guide.locator("ol > li").nth(4);
  assert.equal(await enrolStep.locator("svg.text-success").count(), 1, "observed enrolment did not complete step four");
  assert.equal(await connectStep.locator("svg.text-success").count(), 0, "unobserved connectivity completed step five");
  assert.match(await enrolStep.locator("code").innerText(), /-name home-edge/);
  assert.match(await connectStep.locator("code").innerText(), /-host APP\.example\.com/);
  assert.match(await connectStep.innerText(), /Enrolled but not connected yet/);

  step = "observed connection completes step five";
  registry = registry.map((item) => item.name === "home-edge" ? { ...item, connected: true } : item);
  await page.getByRole("button", { name: "Refresh" }).click();
  await guide.getByText("Connected. The tunnel is up.").waitFor();
  assert.equal(await connectStep.locator("svg.text-success").count(), 1, "observed connectivity did not complete step five");
  assert.match(await card("home-edge").innerText(), /Connected/);

  for (const width of [1440, 1024, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    const size = await page.evaluate(() => ({ width: document.documentElement.scrollWidth, viewport: innerWidth }));
    assert(size.width <= width, `horizontal overflow at ${width}px: ${JSON.stringify(size)}`);
  }
  assert.deepEqual(errors, [], `browser reported errors: ${JSON.stringify(errors)}`);
  console.log("React connectors acceptance passed: bearer-protected registry, revoked/connected/waiting/idle states, expired/unknown/expiring certificates, safe five-step commands, observed enrolment and connection, and no horizontal overflow at four widths.");
} catch (error) {
  console.error(`React connectors acceptance failed during ${step}: ${error.message}`);
  if (pageURL(browser)) console.error((await browser.contexts()[0]?.pages()[0]?.locator("body").innerText().catch(() => ""))?.slice(0, 1200));
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

function pageURL(instance) { return !!instance?.contexts().length; }
