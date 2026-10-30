// Browser acceptance for administrator passkeys (UI-007/UI-008/UI-010) against
// an isolated real edge. Each browser context is a separate device with its
// own Chrome virtual authenticator, so credentials never leave this run.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/passkey-acceptance.mjs
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright-core";
import { startDemoEdge, ADMIN_PORT } from "./edge.mjs";

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");
const consoleURL = `http://ozal.localhost:${ADMIN_PORT}`;
const root = await mkdtemp(join(tmpdir(), "sb-passkeys-"));
let edge, browser, step = "start";
const errors = [];

const host = async (...args) => {
  const { stdout } = await exec(binary, ["console", ...args, "-config", join(edge.dir, "config.json"), "-url", consoleURL]);
  const link = stdout.split("\n").find((l) => l.includes("#enrol="));
  assert(link, "the host printed no enrolment link");
  return link.trim();
};
// A device: a fresh context whose page carries a virtual platform authenticator.
const device = async () => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const credentials = async () => (await cdp.send("WebAuthn.getCredentials", { authenticatorId })).credentials.length;
  return { context, page, credentials };
};
const inConsole = (page) => page.locator("#main-content h1", { hasText: "Overview" }).waitFor();
const signedOut = (page) => page.getByRole("button", { name: "Sign in with your passkey" }).waitFor();
const menu = async (page, name) => { await page.getByRole("button", { name, exact: true }).click(); return page.locator("#user-menu"); };
// A revoked session ends on the console's next request: either the page
// already noticed (event stream, renewal) or the next navigation makes it.
const endedOnNextRequest = async (page) => {
  const people = page.locator('nav a[href="/people"]');
  if (await people.isVisible()) await people.click({ timeout: 5_000 }).catch(() => {});
  await signedOut(page);
};
const signOut = async (page, name) => { await (await menu(page, name)).getByRole("button", { name: "Sign out" }).click(); await signedOut(page); };
const admins = async () => {
  const res = await fetch(`http://127.0.0.1:${ADMIN_PORT}/_/api/admins`, { headers: { Authorization: `Bearer ${edge.token}` } });
  assert.equal(res.status, 200, "administrator listing failed");
  return (await res.json()).admins;
};

try {
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });

  step = "first administrator from the host's setup link";
  const laptop = await device();
  await laptop.page.goto(consoleURL + "/overview");
  await laptop.page.getByRole("heading", { name: "Set up the first administrator" }).waitFor();
  await laptop.page.goto(await host("enrol"));
  await laptop.page.getByLabel("Full name").fill("Olivia Owner");
  await laptop.page.getByLabel("Email").fill("olivia@acme.dev");
  await laptop.page.getByRole("button", { name: "Create my passkey" }).click();
  await inConsole(laptop.page);
  assert.equal(await laptop.credentials(), 1, "enrolment did not store a passkey on the device");
  assert.equal(new URL(laptop.page.url()).hash, "", "the setup token stayed in the address bar");
  await assert.rejects(host("enrol"), "a second first-administrator link was issued");

  step = "passkey sign-in after signing out";
  await signOut(laptop.page, "Olivia Owner");
  await laptop.page.getByRole("button", { name: "Sign in with your passkey" }).click();
  await inConsole(laptop.page);
  assert.match(await (await menu(laptop.page, "Olivia Owner")).textContent(), /Passkey · 6 of 6 tools/);
  await laptop.page.keyboard.press("Escape");

  step = "another browser through a one-use device link";
  const deviceLink = await (async () => {
    const m = await menu(laptop.page, "Olivia Owner");
    await m.getByRole("button", { name: "Sign in on another browser" }).click();
    return (await m.locator("code").textContent()).trim();
  })();
  await laptop.page.keyboard.press("Escape");
  const phone = await device();
  await phone.page.goto(deviceLink);
  await phone.page.getByText("This adds a passkey from this browser for").waitFor();
  await phone.page.getByRole("button", { name: "Create my passkey" }).click();
  await inConsole(phone.page);
  await signOut(phone.page, "Olivia Owner");
  await phone.page.getByRole("button", { name: "Sign in with your passkey" }).click();
  await inConsole(phone.page);
  assert.equal((await admins()).find((a) => a.email === "olivia@acme.dev").passkeys, 2, "the second browser's passkey was not added");
  const reused = await device();
  await reused.page.goto(deviceLink);
  await reused.page.getByRole("alert").waitFor();
  assert.equal(await reused.page.getByRole("button", { name: "Create my passkey" }).isDisabled(), true, "a used device link offered enrolment");
  assert.equal(await reused.credentials(), 0, "a used device link enrolled again");
  await reused.context.close();

  step = "remove the passkey of a lost device";
  await (await menu(laptop.page, "Olivia Owner")).getByRole("button", { name: "Your passkeys" }).click();
  const own = laptop.page.getByRole("dialog", { name: "Your passkeys" });
  await own.getByText("this session").waitFor();
  assert.equal(await own.getByRole("listitem").count(), 2);
  assert.equal(await own.getByRole("button", { name: /^Remove / }).count(), 1, "the passkey of this session must not offer removal");
  await own.getByRole("button", { name: /^Remove / }).click();
  await laptop.page.getByRole("heading", { name: "Remove this passkey?" }).waitFor();
  await laptop.page.keyboard.press("Escape");
  assert.equal((await admins()).find((a) => a.email === "olivia@acme.dev").passkeys, 2, "cancelling removed a passkey");
  await own.getByRole("button", { name: /^Remove / }).click();
  await laptop.page.getByRole("button", { name: "Remove passkey" }).click();
  await own.getByText("can no longer sign in").waitFor();
  assert.equal(await own.getByRole("listitem").count(), 1);
  assert.equal((await admins()).find((a) => a.email === "olivia@acme.dev").passkeys, 1);
  await own.getByRole("button", { name: "Close" }).click();
  // On a phone the menu lives in the drawer; the dialog must still show.
  await laptop.page.setViewportSize({ width: 390, height: 844 });
  await laptop.page.getByRole("button", { name: "Open menu" }).click();
  await (await menu(laptop.page, "Olivia Owner")).getByRole("button", { name: "Your passkeys" }).click();
  await own.getByText("this session").waitFor();
  assert.ok(await own.isVisible(), "the passkeys dialog is hidden on a phone");
  await laptop.page.keyboard.press("Escape");
  await own.waitFor({ state: "hidden" });
  assert.equal(await laptop.page.locator("#mobile-menu").evaluate((dialog) => dialog.open), true, "Escape from passkeys closed the parent navigation");
  await laptop.page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "Olivia Owner");
  await laptop.page.getByRole("button", { name: "Close menu" }).click();
  await laptop.page.setViewportSize({ width: 1280, height: 900 });
  await endedOnNextRequest(phone.page);
  await phone.page.getByRole("button", { name: "Sign in with your passkey" }).click();
  await phone.page.getByRole("alert").waitFor();
  assert.equal(await phone.page.locator("#main-content h1").count(), 0, "the removed passkey still signed in");

  step = "invite an administrator with one tool";
  await laptop.page.locator('nav[aria-label="Console"] a[href="/people"]').first().click();
  await laptop.page.locator('nav[aria-label="People views"] a[href="/administrators"]').click();
  await laptop.page.locator("#main-content h1", { hasText: "People" }).waitFor();
  await laptop.page.getByRole("button", { name: "Add administrator" }).first().click();
  await laptop.page.locator("#adm-email").fill("bea@acme.dev");
  await laptop.page.locator("#adm-name").fill("Bea");
  await laptop.page.getByRole("checkbox", { name: /^People/ }).check();
  await laptop.page.getByRole("button", { name: "Create invitation" }).click();
  const invitation = (await laptop.page.getByText("Invitation for bea@acme.dev").locator("..").locator("code").textContent()).trim();
  const bea = await device();
  await bea.page.goto(invitation);
  await bea.page.getByText("Invited as").waitFor();
  assert.equal(await bea.page.getByLabel("Full name").inputValue(), "Bea");
  await bea.page.getByRole("button", { name: "Create my passkey" }).click();
  await inConsole(bea.page);
  assert.match(await (await menu(bea.page, "Bea")).textContent(), /1 of 6 tools/);
  await bea.page.keyboard.press("Escape");
  assert.deepEqual((await admins()).find((a) => a.email === "bea@acme.dev").tools, ["people"]);

  step = "reset another administrator's passkeys";
  // A reload keeps the session: the edge resumes it from its HttpOnly cookie.
  await laptop.page.reload();
  await laptop.page.locator("#main-content h1", { hasText: "People" }).waitFor();
  assert.equal(new URL(laptop.page.url()).pathname, "/administrators", "the reload did not keep the session on the same view");
  assert.equal(await laptop.page.evaluate(() => document.cookie), "", "the session cookie must not be readable by page script");
  await laptop.page.locator("li", { hasText: "bea@acme.dev" }).getByRole("button", { name: "Reset passkeys" }).click();
  const recovery = (await laptop.page.getByText("Recovery link for bea@acme.dev").locator("..").locator("code").textContent()).trim();
  step = "recovery link on a new device";
  const beaNew = await device();
  await beaNew.page.goto(recovery);
  await beaNew.page.getByText("This link replaces the passkeys of").waitFor();
  await beaNew.page.getByRole("button", { name: "Create my passkey" }).click();
  await inConsole(beaNew.page);
  step = "the lost device is signed out and its passkey refused";
  // Her open session on the lost device ends on its next request.
  await endedOnNextRequest(bea.page);
  await bea.page.getByRole("button", { name: "Sign in with your passkey" }).click();
  await bea.page.getByRole("alert").waitFor();
  assert.equal(await bea.page.locator("#main-content h1").count(), 0, "a replaced passkey still signed in");
  assert.equal((await admins()).find((a) => a.email === "bea@acme.dev").passkeys, 1, "recovery kept the old passkey");

  assert.deepEqual(errors, [], "no uncaught browser errors");
  console.log("Administrator passkeys: first-run enrol once, passkey sign-in, device link for another browser (one use), removing a lost passkey ends its session, scoped invitation, recovery revokes old passkey and session");
} catch (error) {
  console.error(`Passkey acceptance failed at ${step}: ${error.message}`);
  if (process.env.SB_DEBUG) for (const [i, c] of (browser?.contexts() ?? []).entries()) for (const p of c.pages()) console.error(`--- device ${i} ${p.url()}\n` + (await p.locator("body").innerText().catch(() => "")).slice(0, 600));
  process.exitCode = 1;
} finally {
  await browser?.close();
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
