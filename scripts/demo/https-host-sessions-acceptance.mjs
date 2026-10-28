// ID-015 / ARCH-010 A: real HTTPS, isolated CA trust and the real routing,
// passkey and SQLite adapters. Linux, agent-browser with --ca-cert, certutil,
// Chrome and this directory's existing playwright-core are prerequisites.
// CHROME=/path/to/chrome node scripts/demo/https-host-sessions-acceptance.mjs
// Cached test certificates exercise TLS serving, not public ACME issuance.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright-core";

const run = promisify(execFile);
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
if (process.platform !== "linux" || !process.env.CHROME) throw new Error("Requires Linux and CHROME; see docs/engineering.md");
const root = await mkdtemp(join(tmpdir(), "sb-https-"));
const session = `sb-https-${process.pid}`;
const metadata = join(root, "ready.json");
const fixtureBin = join(root, "edge.test");
let fixture, browser, fixtureExit, fixtureLog = "", step = "start", fixtureFailed = false;
const errors = [];
const agent = async (...args) => JSON.parse((await run("agent-browser", ["--session", session, "--namespace", session, "--json", ...args], { timeout: 45_000 })).stdout);
try {
  await run("certutil", ["-H"], { timeout: 5000 }).catch((error) => { if (error.code === "ENOENT") throw new Error("certutil is required; install libnss3-tools separately"); });
  await run("go", ["test", "-c", "-o", fixtureBin, "./cmd/switchboard"], { cwd: repo, timeout: 120_000 });
  fixture = spawn(fixtureBin, ["-test.run=^TestHTTPSAcceptanceFixture$", "-test.timeout=11m"], {
    cwd: repo, env: { ...process.env, SWITCHBOARD_HTTPS_FIXTURE: metadata }, stdio: ["pipe", "pipe", "pipe"],
  });
  fixtureExit = once(fixture, "exit").then(([code]) => { fixtureFailed = code !== 0; return code; });
  for (const stream of [fixture.stdout, fixture.stderr]) stream.on("data", (data) => { fixtureLog = (fixtureLog + data).slice(-8000); });
  let edge;
  for (let i = 0; i < 200; i++) {
    try { edge = JSON.parse(await readFile(metadata, "utf8")); break; }
    catch (error) {
      if (fixture.exitCode !== null) throw new Error(`fixture exited: ${fixtureLog}`);
      if (error.code !== "ENOENT" && !(error instanceof SyntaxError)) throw error;
      await delay(50);
    }
  }
  assert(edge, "fixture did not become ready");
  const origin = (host) => `https://${host}:${edge.port}`;
  const notes = origin("notes.other.test"), wiki = origin("wiki.third.test"), covered = origin("app.login.test");
  const control = async (action, email) => {
    const response = await fetch(`${edge.control}/${action}`, { method: "POST", headers: { Authorization: `Bearer ${edge.token}`, "Content-Type": "application/json" }, body: JSON.stringify({ email }) });
    assert(response.ok, `fixture ${action} failed: ${response.status}`);
    return response.status === 204 ? null : response.json();
  };
  const launch = async (trust) => {
    const result = await agent("--executable-path", process.env.CHROME,
      "--args", "--no-sandbox,--no-proxy-server,--host-resolver-rules=MAP *.test 127.0.0.1",
      ...(trust ? ["--ca-cert", edge.ca] : []), "open", "about:blank");
    assert.equal(result.success, true, "agent-browser launch failed");
    const endpoint = await agent("get", "cdp-url");
    assert.equal(endpoint.success, true);
    const url = endpoint.data.url ?? endpoint.data.cdpUrl ?? endpoint.data.cdp_url;
    assert(url, "agent-browser did not return its CDP endpoint");
    return chromium.connectOverCDP(url);
  };
  step = "untrusted CA is rejected";
  browser = await launch(false);
  const untrusted = await browser.newContext();
  const untrustedPage = await untrusted.newPage();
  await assert.rejects(untrustedPage.goto(edge.login), /ERR_CERT_AUTHORITY_INVALID/);
  await browser.close(); browser = null;
  await agent("close");

  step = "trust only the disposable CA";
  browser = await launch(true);
  const unknown = await browser.newContext();
  const unknownPage = await unknown.newPage();
  await assert.rejects(unknownPage.goto(origin("unknown.unconfigured.test")), /ERR_SSL_PROTOCOL_ERROR/);
  await unknown.close();
  const device = async (mobile = false) => {
    const context = await browser.newContext(mobile ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : { viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    page.on("pageerror", (error) => errors.push(error.message));
    const cdp = await context.newCDPSession(page);
    await cdp.send("WebAuthn.enable");
    const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
    return { context, page, cdp, authenticatorId };
  };
  const cookie = async (device, url, name = "sb_session") => (await device.context.cookies(url)).find((item) => item.name === name);
  const enrol = async (email, mobile = false) => {
    const d = await device(mobile);
    await d.page.goto((await control("invite", email)).link);
    await d.page.locator("#action").click();
    await d.page.locator("#who:not([hidden])").waitFor({ timeout: 20_000 });
    await d.cdp.send("WebAuthn.setAutomaticPresenceSimulation", { authenticatorId: d.authenticatorId, enabled: false });
    return d;
  };
  const app = async (d, url) => {
    const response = await d.page.goto(url);
    assert.equal(response.status(), 200, `app response: ${response.status() === 200 ? "OK" : await response.text()}`);
    await d.page.locator("h1", { hasText: "Private app" }).waitFor();
    assert.equal(d.page.url(), url, "requested path was not preserved");
    assert.equal(await d.page.locator("#cookies").textContent(), "clean");
    assert.equal(await d.page.evaluate(() => window.isSecureContext), true);
  };
  const capture = async (d, url, pattern) => {
    let captured;
    // CDP Fetch also intercepts redirect hops; page.route does not.
    let interceptionError;
    const handler = async (event) => {
      captured = event.request.url;
      try { await d.cdp.send("Fetch.failRequest", { requestId: event.requestId, errorReason: "Aborted" }); }
      catch (error) { interceptionError = error; }
    };
    d.cdp.on("Fetch.requestPaused", handler);
    await d.cdp.send("Fetch.enable", { patterns: [{ urlPattern: pattern, requestStage: "Request" }] });
    try {
      await d.page.goto(url).catch((error) => { if (!captured || !/ERR_ABORTED/.test(error.message)) throw error; });
      if (interceptionError) throw interceptionError;
      assert(captured, "expected redirect was not reached");
      return captured;
    }
    finally { await d.cdp.send("Fetch.disable"); d.cdp.off("Fetch.requestPaused", handler); }
  };
  // Stop first at continue to identify this transaction's exact cookie, then
  // at the callback before redemption. Aborting the paused navigation avoids
  // creating an artificial document and lets normal favicon requests run.
  const captureHandoff = async (d, url) => {
    const host = new URL(url);
    await d.context.clearCookies({ domain: host.hostname, name: "sb_session" });
    const transaction = await capture(d, url, `${edge.login}/_/auth/continue?**`);
    const name = `sb_handoff_${new URL(transaction).searchParams.get("tx")}`;
    const binding = await cookie(d, `${host.origin}/_/auth/handoff`, name);
    assert(binding, "transaction binding cookie missing");
    const code = await capture(d, transaction, `${host.origin}/_/auth/handoff?**`);
    return { code, binding };
  };
  const signedOut = (d, url) => capture(d, url, `${edge.login}/_/auth/login?**`);
  const logout = async (d, url) => {
    await app(d, url);
    await d.page.evaluate(async () => { await fetch("/_/auth/logout", { method: "POST", redirect: "manual" }); });
  };

  for (const mobile of [false, true]) {
    step = mobile ? "mobile viewport multi-domain handoff" : "desktop multi-domain handoff";
    const d = await enrol(mobile ? "mobile@acme.dev" : "desktop@acme.dev", mobile);
    step = mobile ? "mobile simultaneous tabs on one host" : "desktop simultaneous tabs on one host";
    const otherPage = await d.context.newPage();
    otherPage.on("pageerror", (error) => errors.push(error.message));
    const otherTab = { context: d.context, page: otherPage, cdp: await d.context.newCDPSession(otherPage) };
    const [first, second] = await Promise.all([
      captureHandoff(d, `${notes}/first-tab?q=1`),
      captureHandoff(otherTab, `${notes}/second-tab?q=2`),
    ]);
    assert.notEqual(first.binding.name, second.binding.name);
    assert.notEqual(first.binding.value, second.binding.value);
    const redeemed = await otherPage.goto(second.code);
    assert.equal(redeemed.status(), 200);
    assert.equal(otherPage.url(), `${notes}/second-tab?q=2`);
    assert.equal(await cookie(d, `${notes}/_/auth/handoff`, second.binding.name), undefined);
    assert(await cookie(d, `${notes}/_/auth/handoff`, first.binding.name), "the other tab's binding was cleared");
    const earlier = await d.page.goto(first.code);
    assert.equal(earlier.status(), 200);
    assert.equal(d.page.url(), `${notes}/first-tab?q=1`);
    assert.equal(await cookie(d, `${notes}/_/auth/handoff`, first.binding.name), undefined);
    await otherPage.close();
    // A manually copied binding outside its scoped path must still be stripped.
    await d.context.addCookies([{ name: "sb_handoff_injected", value: "fixture-binding", domain: "notes.other.test", path: "/", secure: true, httpOnly: true, sameSite: "Lax" }]);
    await app(d, `${notes}/reports?q=1`);
    await d.context.clearCookies({ name: "sb_handoff_injected" });
    await app(d, `${wiki}/team`);
    const central = await cookie(d, edge.login);
    const n = await cookie(d, notes), w = await cookie(d, wiki);
    for (const [c, host] of [[n, "notes.other.test"], [w, "wiki.third.test"]]) {
      assert(c && central);
      assert.equal(c.domain, host);
      assert.equal(c.secure, true);
      assert.equal(c.httpOnly, true);
      assert.equal(c.sameSite, "Lax");
      assert.notEqual(c.value, central.value);
    }
    assert.notEqual(n.value, w.value);
    assert.equal(central.secure, true);
    // Even a copied host cookie cannot authenticate on the other app.
    await d.context.addCookies([{ ...n, domain: "wiki.third.test" }]);
    await capture(d, `${wiki}/copied-cookie`, `${edge.login}/_/auth/continue?**`);
    await d.context.addCookies([w]);
    await app(d, `${covered}/legacy`);
    assert.equal((await cookie(d, covered)).value, central.value, "covered-domain sessions changed");
    assert.equal((await d.page.goto(`${origin("denied.fourth.test")}/`)).status(), 403);

    step = "Secure binding and stolen/cross-host codes";
    const { code, binding } = await captureHandoff(d, `${notes}/`);
    assert(binding && binding.secure && binding.httpOnly && binding.sameSite === "Lax");
    assert.equal(binding.domain, "notes.other.test");
    const thief = await device();
    assert.equal((await thief.page.goto(code)).status(), 400);
    assert.equal((await d.page.goto(code)).status(), 400, "failed redemption did not consume code");
    await thief.context.close();
    const { code: otherCode, binding: otherBinding } = await captureHandoff(d, `${notes}/`);
    await d.context.addCookies([{ ...otherBinding, domain: "wiki.third.test" }]);
    assert.equal((await d.page.goto(otherCode.replace(notes, wiki))).status(), 400, "cross-host code accepted");
    assert.equal((await d.page.goto(otherCode)).status(), 400);

    step = "logout at app revokes sibling and central sessions";
    await app(d, `${notes}/`);
    await logout(d, `${notes}/`);
    await signedOut(d, `${wiki}/`);
    await signedOut(d, `${notes}/`);
    await d.context.close();
  }

  for (const action of ["disable", "revoke", "expire", "logout"]) {
    step = `linked sessions fail closed after ${action}`;
    const email = `${action}@acme.dev`;
    const d = await enrol(email);
    await app(d, `${notes}/`); await app(d, `${wiki}/`);
    if (action === "logout") {
      await d.page.goto(edge.login);
      await d.page.evaluate(async () => { await fetch("/_/auth/logout", { method: "POST", redirect: "manual" }); });
    } else await control(action, email);
    await signedOut(d, `${notes}/`); await signedOut(d, `${wiki}/`);
    await d.context.close();
  }
  assert.deepEqual(errors, [], "page errors");
  console.log("HTTPS host sessions: OK (desktop + mobile emulation; trusted local CA; no public ACME/DNS acceptance)");
} catch (error) {
  console.error(`HTTPS acceptance failed during: ${step}`);
  console.error(fixtureLog);
  throw error;
} finally {
  if (browser) await browser.close().catch(() => { console.error("browser disconnect failed"); process.exitCode = 1; });
  await agent("close").catch(() => {});
  if (fixture) {
    fixture.stdin.end();
    let timeout;
    const code = await Promise.race([fixtureExit, new Promise((resolve) => { timeout = setTimeout(() => { fixture.kill("SIGKILL"); resolve(-1); }, 5000); })]);
    clearTimeout(timeout);
    if (code !== 0 || fixtureFailed) { console.error(fixtureLog); process.exitCode = 1; }
  }
  await rm(root, { recursive: true, force: true });
}
