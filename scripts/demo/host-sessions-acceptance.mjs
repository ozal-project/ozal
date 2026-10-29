// Browser acceptance for host sessions behind the central login (ID-015)
// against an isolated real edge. The login lives on auth.demo.localhost; two
// private apps live on sites its cookie cannot reach (other.localhost,
// third.localhost: Chrome treats each *.localhost name as its own site), so
// every cookie, redirect and SameSite rule here is the browser's own.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/host-sessions-acceptance.mjs
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright-core";
import { startDemoEdge, PORT, LOGIN } from "./edge.mjs";

const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");
const NOTES = `http://notes.other.localhost:${PORT}`;
const CRM = `http://crm.third.localhost:${PORT}`;
const root = await mkdtemp(join(tmpdir(), "sb-hostsessions-"));
let edge, browser, step = "start";
const errors = [];
const servers = [];

// Each upstream names itself and echoes whether a session cookie reached it.
const upstream = (port, title) => new Promise((ok) => {
  const server = createServer((req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(`<!doctype html><title>${title}</title><h1>${title}</h1><p id="path">${req.url}</p><p id="cookies">${/sb_session|sb_handoff/.test(req.headers.cookie ?? "") ? "leaked" : "clean"}</p>`);
  }).listen(port, "127.0.0.1", () => ok(server));
  servers.push(server);
});

const device = async () => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  return { context, page };
};
const onLoginPage = (page) => page.waitForURL((u) => u.origin === LOGIN && u.pathname === "/_/auth/login");
const sessionCookie = async (context, url) => (await context.cookies(url)).find((c) => c.name === "sb_session");
// Where the browser's cookies lead without following them: "app" when the
// app answers, "code" when the login host approves a handoff (a live central
// session), "login" when it asks to sign in.
const reach = async (context, url) => {
  for (let i = 0; i < 5; i++) {
    const res = await context.request.get(url, { maxRedirects: 0 });
    const next = res.headers().location;
    if (!next) return res.status() === 200 ? "app" : `status ${res.status()}`;
    url = new URL(next, url).href;
    if (url.includes("/_/auth/handoff?code=")) return "code";
    if (url.startsWith(`${LOGIN}/_/auth/login`)) return "login";
  }
  return "loop";
};

// Walk the handoff one redirect at a time with the context's own cookie
// store, and stop before the code is redeemed. (Playwright does not route
// requests that are redirect hops.)
const captureCode = async (context, url) => {
  for (let i = 0; i < 5; i++) {
    const res = await context.request.get(url, { maxRedirects: 0 });
    const next = res.headers().location;
    assert(next, `no redirect from ${url}: ${res.status()}`);
    url = new URL(next, url).href;
    if (url.includes("/_/auth/handoff?code=")) return url;
  }
  throw new Error("no handoff code observed");
};

try {
  await upstream(19111, "Notes");
  await upstream(19112, "CRM");
  edge = await startDemoEdge({
    binary, dir: join(root, "edge"),
    extraRoutes: [
      { host: `notes.other.localhost:${PORT}`, target: "http://127.0.0.1:19111", access: { allowed_domains: ["acme.dev"] } },
      { host: `crm.third.localhost:${PORT}`, target: "http://127.0.0.1:19112", access: { allowed_emails: ["nobody@acme.dev"] } },
    ],
  });
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });

  step = "Ana enrols on the login host";
  const laptop = await device();
  await laptop.page.goto((await edge.invite("ana@acme.dev", "Ana")).link);
  await laptop.page.locator("#action").click();
  await laptop.page.waitForSelector("#who:not([hidden])", { timeout: 20_000 });

  step = "signed in centrally, an app on another site opens with no prompt";
  await laptop.page.goto(`${NOTES}/reports?q=1`);
  await laptop.page.locator("h1", { hasText: "Notes" }).waitFor();
  assert.equal(laptop.page.url(), `${NOTES}/reports?q=1`, "did not land where the visitor was going");
  assert.equal(await laptop.page.locator("#cookies").textContent(), "clean", "a session or binding cookie reached the upstream");
  const central = await sessionCookie(laptop.context, LOGIN);
  const notesSession = await sessionCookie(laptop.context, NOTES);
  assert(central && notesSession, "missing central or host session");
  assert.equal(notesSession.domain, "notes.other.localhost", "the host session is not host-only");
  assert.notEqual(notesSession.value, central.value, "the app got the central session itself");
  assert.equal(notesSession.httpOnly, true);
  assert(!(await laptop.context.cookies(NOTES)).some((c) => c.value === central.value), "the central cookie is sent to the app's site");

  step = "the app's policy still decides";
  const denied = await laptop.page.goto(`${CRM}/`);
  assert.equal(denied.status(), 403, "an app whose policy excludes Ana let her in");

  step = "a captured code is worthless in another browser and burns";
  const thief = await device();
  await laptop.context.clearCookies({ domain: "notes.other.localhost", name: "sb_session" });
  const stolen = await captureCode(laptop.context, `${NOTES}/`);
  assert.equal((await thief.page.goto(stolen)).status(), 400, "another browser redeemed a captured code");
  assert.equal((await laptop.page.goto(stolen)).status(), 400, "a code was redeemed after a failed attempt");
  assert.equal(await sessionCookie(thief.context, NOTES), undefined, "the thief got a host session");

  step = "a browser with no central session is sent to sign in, then lands on the app";
  const phone = await device();
  await phone.page.goto(`${NOTES}/inbox`);
  await onLoginPage(phone.page);
  const returnTo = new URL(phone.page.url()).searchParams.get("return_to");
  assert(returnTo?.startsWith(`${LOGIN}/_/auth/continue?tx=`), `login page returns to ${returnTo}`);

  step = "logout on the app ends the whole login";
  await laptop.page.goto(`${NOTES}/`);
  await laptop.page.locator("h1", { hasText: "Notes" }).waitFor();
  // Over HTTP with the browser's cookies: a login page left open would sign
  // straight back in with the virtual authenticator (conditional mediation).
  const appLogout = await laptop.context.request.post(`${NOTES}/_/auth/logout`, { maxRedirects: 0 });
  assert.equal(appLogout.status(), 303);
  assert.equal(appLogout.headers().location, `${LOGIN}/_/auth/login`);
  assert.equal(await sessionCookie(laptop.context, NOTES), undefined, "the app logout left its cookie");
  assert.equal(await reach(laptop.context, `${NOTES}/`), "login", "after an app logout the login still approves handoffs");

  step = "signing in again from the app comes back to it";
  // The login page may sign in by itself (conditional mediation); either
  // way the visitor ends on the app path they asked for.
  await laptop.page.goto(`${NOTES}/after-login`);
  const notes = laptop.page.locator("h1", { hasText: "Notes" });
  await Promise.race([notes.waitFor({ timeout: 20_000 }), onLoginPage(laptop.page).then(() => laptop.page.locator("#action").click()).catch(() => {})]);
  await notes.waitFor({ timeout: 20_000 });
  assert.equal(new URL(laptop.page.url()).pathname, "/after-login");

  step = "logout on the login host ends the app's session";
  const before = await sessionCookie(laptop.context, NOTES);
  assert.equal((await laptop.context.request.post(`${LOGIN}/_/auth/logout`, { maxRedirects: 0 })).status(), 303);
  // Present the old host cookie as a copy would: the server must refuse it,
  // and the login no longer approves a handoff.
  await laptop.context.addCookies([{ ...before }]);
  assert.equal(await reach(laptop.context, `${NOTES}/`), "login", "the app's session outlived the central logout");

  assert.deepEqual(errors, [], "page errors");
  console.log("host sessions acceptance: OK");
} catch (error) {
  console.error(`failed during: ${step}`);
  throw error;
} finally {
  await browser?.close();
  await edge?.stop();
  await Promise.all(servers.map((s) => new Promise((r) => s.close(r))));
  await rm(root, { recursive: true, force: true });
}
