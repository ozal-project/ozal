// Exercise React sign-in, account passkey management and device links against
// an isolated Ozal edge and Chrome virtual authenticators.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-passkey-acceptance.mjs
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

const root = await mkdtemp(join(tmpdir(), "ozal-react-passkeys-"));
let edge, preview, browser, step = "start";
const failures = [];

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

async function device() {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (error) => failures.push(error.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const credentials = async () => (await cdp.send("WebAuthn.getCredentials", { authenticatorId })).credentials.length;
  return { context, page, credentials };
}

async function admins() {
  const response = await fetch(`${edge.consoleURL}/_/api/admins`, { headers: { Authorization: `Bearer ${edge.token}` } });
  assert.equal(response.status, 200, "administrator listing failed");
  return (await response.json()).admins;
}

try {
  step = "start disposable edge and React production preview";
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
  await exec("npm", ["run", "react:build"], { cwd: join(process.cwd(), "web/console"), maxBuffer: 4 << 20 });
  const port = await freePort();
  const previewURL = `http://ozal.localhost:${port}`;
  preview = spawn(process.execPath, [join(process.cwd(), "web/console/node_modules/vite/bin/vite.js"), "preview", "--config", "vite.react.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
    cwd: join(process.cwd(), "web/console"), env: { ...process.env, OZAL_ADMIN: edge.consoleURL }, stdio: ["ignore", "pipe", "pipe"],
  });
  preview.stdout.resume(); preview.stderr.resume();
  await waitForPreview(previewURL);
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });

  step = "first administrator enrolment and backup notice";
  const laptop = await device();
  await laptop.page.goto(`${previewURL}/overview`);
  await laptop.page.getByRole("heading", { name: "Set up the first administrator" }).waitFor();
  const { stdout } = await exec(binary, ["console", "enrol", "-config", join(edge.dir, "config.json"), "-url", previewURL]);
  const enrolLink = stdout.split("\n").find((line) => line.includes("#enrol="));
  assert(enrolLink, "the host printed no enrolment link");
  await laptop.page.goto(enrolLink.trim());
  await laptop.page.getByLabel("Full name").fill("React Owner");
  await laptop.page.getByLabel("Email").fill("react-owner@acme.dev");
  await laptop.page.getByRole("button", { name: "Create my passkey" }).click();
  await laptop.page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
  assert.equal(await laptop.credentials(), 1, "enrolment did not save a passkey in the browser");
  assert.equal(new URL(laptop.page.url()).hash, "", "the one-use credential remained in the URL");
  await laptop.page.getByRole("heading", { name: "Add a second passkey" }).waitFor();

  step = "account menu device link, second device enrolment and passkey inventory";
  await laptop.page.getByRole("button", { name: "React Owner" }).click();
  const account = laptop.page.getByRole("dialog", { name: "Your session" });
  await account.getByRole("button", { name: "Sign in on another browser" }).click();
  const deviceLink = await account.getByLabel("One-use device link").inputValue();
  assert.equal(new URL(deviceLink).hostname, "ozal.localhost", "device link did not use the console host");
  const phone = await device();
  await phone.page.goto(deviceLink);
  await phone.page.getByText("This adds a passkey for").waitFor();
  await phone.page.getByRole("button", { name: "Create my passkey" }).click();
  await phone.page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
  assert.equal(await phone.credentials(), 1, "the second device did not receive a passkey");
  assert.equal((await admins()).find((admin) => admin.email === "react-owner@acme.dev").passkeys, 2);
  await laptop.page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await laptop.page.getByRole("heading", { name: "Add a second passkey" }).waitFor({ state: "hidden" });

  step = "view and remove the other device passkey with review";
  await account.getByRole("button", { name: "Your passkeys" }).click();
  const passkeys = laptop.page.getByRole("dialog", { name: "Your passkeys" });
  await passkeys.getByText("this session").waitFor();
  assert.equal(await passkeys.getByRole("listitem").count(), 2, "the passkey list missed one device");
  await passkeys.getByRole("button", { name: /^Remove / }).click();
  const review = laptop.page.getByRole("dialog", { name: "Remove this passkey?" });
  await review.getByText("It can no longer sign in to this console").waitFor();
  await review.getByRole("button", { name: "Remove passkey" }).click();
  await passkeys.getByText("was removed. It can no longer sign in.").waitFor();
  assert.equal((await admins()).find((admin) => admin.email === "react-owner@acme.dev").passkeys, 1, "removal did not revoke the selected passkey");

  step = "sign out and sign in with the remaining passkey";
  await laptop.page.keyboard.press("Escape");
  await laptop.page.getByRole("button", { name: "React Owner" }).click();
  await laptop.page.getByRole("button", { name: "Sign out" }).click();
  await laptop.page.getByRole("button", { name: "Sign in with passkey" }).waitFor();
  await laptop.page.getByRole("button", { name: "Sign in with passkey" }).click();
  await laptop.page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
  step = "mobile account menu and nested passkey dialog";
  await laptop.page.setViewportSize({ width: 390, height: 844 });
  await laptop.page.getByRole("button", { name: "Open menu" }).click();
  const mobileMenu = laptop.page.getByRole("dialog", { name: "Console navigation" });
  await mobileMenu.waitFor();
  await mobileMenu.getByRole("button", { name: "React Owner" }).click();
  const mobileAccount = mobileMenu.getByRole("dialog", { name: "Your session" });
  await mobileAccount.getByRole("button", { name: "Your passkeys" }).click();
  const mobilePasskeys = laptop.page.getByRole("dialog", { name: "Your passkeys" });
  await mobilePasskeys.getByText("this session").waitFor();
  await laptop.page.keyboard.press("Escape");
  await mobilePasskeys.waitFor({ state: "hidden" });
  assert.equal(await mobileMenu.evaluate((dialog) => dialog.open), true, "closing passkey management also closed the mobile directory");
  await laptop.page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "React Owner");
  await mobileMenu.getByRole("button", { name: "Close menu" }).click();
  assert.deepEqual(failures, [], "uncaught browser errors occurred");
  console.log("React passkey acceptance passed: bootstrap enrolment, one-use URL cleanup, backup notice, account device link, second-device enrolment, passkey inventory and reviewed removal, passkey sign-out/sign-in, mobile passkey dialog and focus return.");
} catch (error) {
  console.error(`React passkey acceptance failed at ${step}: ${error.message}`);
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
