// Browser acceptance for a visitor's own passkeys and sessions against an
// isolated real edge: each passkey is named after the browser it was made
// in and renamed in place, the one behind this sign-in is marked, and each
// session names its browser. A synced passkey is the same credential copied
// to a second virtual authenticator, as a password manager would.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/visitor-passkeys-acceptance.mjs
//   SHOTS=/some/dir also saves desktop and phone screenshots of both pages.
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright-core";
import { startDemoEdge } from "./edge.mjs";

const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
const shots = process.env.SHOTS;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");
const MAC = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36";
const IPHONE = "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1";
const root = await mkdtemp(join(tmpdir(), "sb-visitor-passkeys-"));
let edge, browser, step = "start";
const errors = [];

const device = async (userAgent, viewport) => {
  const context = await browser.newContext({ userAgent, viewport });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  return { context, page, cdp, authenticatorId };
};
const shot = async (page, name) => { if (shots) await page.screenshot({ path: join(shots, `${name}.png`), fullPage: true }); };

try {
  edge = await startDemoEdge({ binary, dir: root, dynamicPorts: true });
  const LOGIN = edge.loginURL;
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });

  step = "Ana enrols on her laptop";
  const laptop = await device(MAC, { width: 1280, height: 900 });
  await laptop.page.goto((await edge.invite("ana@acme.dev", "Ana")).link);
  await laptop.page.locator("#action").click();
  await laptop.page.waitForSelector("#who:not([hidden])", { timeout: 20_000 });

  step = "the passkey is named after the browser it was made in and marked as this sign-in";
  await laptop.page.goto(`${LOGIN}/_/auth/devices`);
  const item = laptop.page.locator("#devices li").first();
  await item.locator(".provider").waitFor();
  assert.equal(await item.locator(".provider").textContent(), "Chrome on macOS");
  assert.equal(await item.locator(".badge.current").textContent(), "Used for this sign-in");

  step = "Ana renames it in place";
  await item.getByRole("button", { name: "Rename the Chrome on macOS passkey" }).click();
  const field = item.getByRole("textbox", { name: "Name for this passkey" });
  assert.equal(await field.inputValue(), "Chrome on macOS", "the field starts with the current name");
  await field.press("Escape");
  assert.equal(await field.isHidden(), true, "Escape closes the field");
  await item.getByRole("button", { name: "Rename the Chrome on macOS passkey" }).click();
  await field.fill("Work laptop");
  await field.press("Enter");
  await laptop.page.locator("#devices li .provider", { hasText: "Work laptop" }).waitFor();
  await laptop.page.locator("#status", { hasText: "Renamed to Work laptop." }).waitFor();
  await shot(laptop.page, "devices-desktop");

  step = "the same passkey, synced to her phone, signs in there";
  const { credentials } = await laptop.cdp.send("WebAuthn.getCredentials", { authenticatorId: laptop.authenticatorId });
  assert.equal(credentials.length, 1);
  const phone = await device(IPHONE, { width: 390, height: 844 });
  await phone.cdp.send("WebAuthn.addCredential", { authenticatorId: phone.authenticatorId, credential: credentials[0] });
  await phone.page.goto(`${LOGIN}/_/auth/login`);
  // Autofill may sign in before the button is pressed. The click must settle
  // here: left pending, it would press the next page's #action ("Add a
  // passkey").
  const pressed = phone.page.locator("#action").click({ timeout: 5_000 }).catch(() => {});
  await phone.page.waitForURL(`${LOGIN}/_/auth/me`, { timeout: 20_000 });
  await pressed;

  step = "the phone's portal names both sessions by browser";
  const sessions = phone.page.locator("#sessions li");
  await sessions.nth(1).waitFor();
  // Sessions created in the same second may list in either order.
  const names = (await sessions.locator("strong").allTextContents()).sort();
  assert.deepEqual(names, ["Chrome on macOS", "Safari on iPhone · this browser"], "both browsers, this one marked");
  const thisOne = phone.page.locator("#sessions li", { hasText: "Safari on iPhone · this browser" });
  assert.equal(await thisOne.getByRole("button").textContent(), "Sign out");
  await shot(phone.page, "me-phone");

  step = "the passkey page on the phone marks the same passkey and its last browser";
  await phone.page.goto(`${LOGIN}/_/auth/devices`);
  const phoneItem = phone.page.locator("#devices li").first();
  await phoneItem.locator(".provider", { hasText: "Work laptop" }).waitFor();
  assert.equal(await phoneItem.locator(".badge.current").count(), 1);
  assert.match(await phoneItem.locator(".meta").textContent(), /Safari on iPhone/);
  assert.equal(await phone.page.locator('#status[data-kind="error"]').count(), 0, "the page shows no error");
  const scroll = await phone.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(scroll <= 0, `the phone page scrolls sideways by ${scroll}px`);
  await shot(phone.page, "devices-phone");

  step = "ending the laptop's session from the phone signs the laptop out";
  await phone.page.goto(`${LOGIN}/_/auth/me`);
  const laptopSession = phone.page.locator("#sessions li", { hasText: "Chrome on macOS" });
  await laptopSession.getByRole("button", { name: "End" }).click();
  await laptopSession.getByRole("button", { name: "End session" }).click();
  await phone.page.locator("#sessions li").nth(1).waitFor({ state: "detached" });
  await laptop.page.goto(`${LOGIN}/_/auth/me`);
  await laptop.page.waitForURL((u) => u.pathname === "/_/auth/login", { timeout: 20_000 });
  await shot(laptop.page, "laptop-signed-out");

  assert.deepEqual(errors, [], "page errors");
  console.log("Visitor passkeys acceptance passed: browser-named passkeys, rename in place, this-sign-in mark, synced passkey on a second browser, sessions named by browser, ending another session, no sideways scroll on a phone.");
} catch (error) {
  console.error(`FAILED at: ${step}`);
  throw error;
} finally {
  await browser?.close().catch(() => {});
  await edge?.stop().catch(() => {});
  await rm(root, { recursive: true, force: true });
}
