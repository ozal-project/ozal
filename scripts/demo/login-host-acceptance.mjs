// Browser acceptance for administrator passkeys on the login host (ID-021)
// against an isolated real edge with admin.login_host. The console stays on
// ozal.localhost; the passkey is made and used on the login host's own name,
// where every authenticator works, and the console gets its session back.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/login-host-acceptance.mjs
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright-core";
import { startDemoEdge, DOMAIN } from "./edge.mjs";

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");
let consoleURL;
let loginURL;
const root = await mkdtemp(join(tmpdir(), "ozal-login-host-"));
let edge, browser, step = "start";
const errors = [];

try {
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, adminLoginHost: true, dynamicPorts: true });
  consoleURL = `http://ozal.localhost:${edge.adminPort}`;
  loginURL = edge.loginURL;
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const inConsole = () => page.locator("#main-content h1", { hasText: "Overview" }).waitFor();

  step = "first administrator, passkey made on the login host";
  const { stdout } = await exec(binary, ["console", "enrol", "-config", join(edge.dir, "config.json"), "-url", consoleURL]);
  const link = stdout.split("\n").find((l) => l.includes("#enrol=")).trim();
  await page.goto(link);
  await page.getByText("this edge's sign-in address").waitFor();
  await page.getByLabel("Full name").fill("Olivia Owner");
  await page.getByLabel("Email").fill("olivia@acme.dev");
  await page.getByRole("button", { name: "Create my passkey" }).click();
  await page.waitForURL((url) => url.origin === loginURL && url.pathname === "/_/auth/admin");
  assert.equal(new URL(page.url()).hash, "", "nothing secret in the login host's URL");
  assert.ok(!page.url().includes("enrol"), "the enrolment link never reaches the login host's URL");
  await page.getByRole("button", { name: "Create a passkey" }).click();
  await inConsole();
  assert.equal(new URL(page.url()).origin, consoleURL, "back on the console");
  assert.equal(new URL(page.url()).hash, "", "the code left the address bar");
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
  assert.equal(credentials.length, 1);
  assert.equal(credentials[0].rpId, DOMAIN, "the passkey belongs to the login host's domain, not ozal.localhost");

  step = "sign out and in again through the login host";
  await page.getByRole("button", { name: "Olivia Owner", exact: true }).click();
  await page.locator("#user-menu").getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Sign in with passkey" }).click();
  await page.waitForURL((url) => url.origin === loginURL);
  await page.getByRole("button", { name: "Continue with a passkey" }).click();
  await inConsole();

  step = "the login host hides the ceremony without a transaction";
  const unknown = await fetch(`${loginURL}/_/auth/does-not-exist`);
  for (const path of ["/_/auth/admin", "/_/auth/admin?tx=forged", "/_/auth/admin.js"]) {
    const res = await fetch(loginURL + path);
    assert.equal(res.status, unknown.status, `${path} answered differently from an unknown path`);
  }

  assert.deepEqual(errors, [], "no uncaught browser errors");
  console.log("Administrator passkeys on the login host: first administrator enrols there, the console receives the session, sign-in again by the same route, the passkey's RP is the login domain, and the login host hides the ceremony without a transaction");
} catch (error) {
  console.error(`Login-host acceptance failed at ${step}: ${error.message}`);
  if (process.env.SB_DEBUG) for (const c of browser?.contexts() ?? []) for (const p of c.pages()) console.error(`--- ${p.url()}\n` + (await p.locator("body").innerText().catch(() => "")).slice(0, 800));
  process.exitCode = 1;
} finally {
  await browser?.close();
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
