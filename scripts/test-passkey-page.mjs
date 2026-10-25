// Runs the passkey login page script (internal/passkey/assets/passkey.js)
// against a minimal fake DOM and checks what each variant shows: which
// methods are offered, the step-up wording, and that a browser without
// WebAuthn still gets the provider button. No browser needed; run by
// scripts/verify.sh when node is installed.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const src = readFileSync(new URL("../internal/passkey/assets/passkey.js", import.meta.url), "utf8");

function render(search, { oidc = "", webauthn = true, page = "login", fetch = () => new Promise(() => {}), assign = () => {} } = {}) {
  const els = {};
  const el = (id) => (els[id] ??= {
    id, textContent: "", hidden: id === "oidc", href: "", disabled: false, dataset: {}, children: [],
    addEventListener() {}, focus() {}, setAttribute() {}, append(...c) { this.children.push(...c); }, replaceChildren(...c) { this.children = c; },
  });
  ["title", "intro", "action", "oidc", "status", "first-time", "username", "sites", "devices", "who", "email", "signout", "apps", "manage"].forEach(el);
  const label = { hidden: false };
  const head = [];
  let ready;
  const document = {
    head: { appendChild: (node) => head.push(node) },
    title: "Sign in · Switchboard",
    body: { dataset: { page, oidc } },
    getElementById: (id) => els[id],
    querySelector: (q) => (q === "label[for=username]" ? label : null),
    createElement: () => el("x" + Math.random()),
    addEventListener: (ev, fn) => { if (ev === "DOMContentLoaded") ready = fn; },
  };
  const ctx = {
    document, URL, URLSearchParams, encodeURIComponent, AbortController, atob, btoa, JSON, Date, String, Uint8Array,
    window: webauthn ? { PublicKeyCredential: function () {} } : {},
    location: { search, hash: "", pathname: "/_/auth/" + page, origin: "https://auth.example.com", assign },
    history: { replaceState() {} },
    localStorage: { setItem() {}, getItem: () => null, removeItem() {} },
    navigator: webauthn ? { credentials: { get: () => new Promise(() => {}) } } : {},
    PublicKeyCredential: webauthn ? {} : undefined,
    fetch,
  };
  vm.createContext(ctx);
  vm.runInContext(src, ctx);
  ready();
  return {
    // Live reads: a page that loads data answers after render() returns.
    get title() { return els.title.textContent; },
    get intro() { return els.intro.textContent; },
    get passkey() { return els.action.hidden ? "hidden" : els.action.disabled ? "disabled" : "shown"; },
    get oidc() { return els.oidc.hidden ? "" : els.oidc.textContent; },
    get href() { return els.oidc.href; },
    get firstTime() { return !els["first-time"].hidden; },
    get status() { return els.status.textContent; },
    get devices() { return els.devices.children; },
    get apps() { return els.apps.children.map((a) => [a.href, a.textContent, (a.children[0] || {}).textContent || ""]); },
    who: () => (els.who.hidden ? "" : els.email.textContent),
    manageShown: () => !els.manage.hidden,
    get preconnect() { return head.filter((n) => n.rel === "preconnect").map((n) => n.href); },
  };
}

const rt = "?return_to=" + encodeURIComponent("https://private.example.com/x");

let page = render(rt);
assert.equal(page.passkey, "shown");
assert.deepEqual(page.preconnect, ["https://private.example.com"], "the destination's connection is opened during the prompt");
assert.equal(page.oidc, "", "no provider configured: no provider button");
assert.equal(page.firstTime, true);

page = render(rt, { oidc: "Okta" });
assert.equal(page.oidc, "Sign in with Okta");
assert.equal(page.href, "/_/auth/oidc/start" + rt, "the provider keeps the destination");
assert.equal(page.passkey, "shown", "both methods offered, none picked");

page = render(rt + "&reauth=1", { oidc: "Okta" });
assert.equal(page.title, "Confirm it's you");
assert.match(page.intro, /a while ago/);
assert.equal(page.firstTime, false);

page = render(rt + "&reauth=device");
assert.match(page.intro, /stays on one device/);

page = render(rt + "&reauth=method&methods=passkey", { oidc: "Okta" });
assert.equal(page.oidc, "", "a passkey-only route hides the provider");
assert.equal(page.passkey, "shown");

page = render(rt + "&reauth=method&methods=oidc", { oidc: "Okta" });
assert.equal(page.passkey, "hidden", "an OIDC-only route hides the passkey");
assert.equal(page.oidc, "Sign in with Okta");
assert.match(page.intro, /sign in with Okta/);

page = render(rt, { oidc: "Okta", webauthn: false });
assert.equal(page.oidc, "Sign in with Okta", "no WebAuthn: the provider still works");
assert.match(page.status, /does not support passkeys.*Okta/);

page = render(rt, { webauthn: false });
assert.equal(page.passkey, "disabled");
assert.match(page.status, /does not support passkeys/);

// The devices page lists the passkeys and never offers removing the only one.
const listing = (keys) => () => Promise.resolve({ ok: true, json: () => Promise.resolve({ email: "ana@example.com", passkeys: keys, sites: ["https://private.example.com/"] }) });
const key = (id, synced) => ({ id, provider: synced ? "Apple Passwords" : "Security key or device passkey", icon: synced ? "apple" : "key", created: "2026-10-21T12:00:00Z", synced });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
page = render("", { page: "devices", fetch: listing([key("aa11bb22cc33", false)]) });
await settle();
assert.equal(page.devices.length, 1);
assert.equal(page.devices[0].children[0].textContent, "Security key or device passkey", "named by provider, no hex ID shown");
assert.equal(page.devices[0].children[2].children[0].textContent, "This device only", "the badge opens the second line");
assert.equal(page.devices[0].children[3].src, "/_/auth/icons/key.svg", "a device passkey shows the key icon");
assert.equal(page.devices[0].children[1].disabled, true, "the only passkey cannot be removed");
page = render("", { page: "devices", fetch: listing([key("aa11", false), key("dd44", true)]) });
await settle();
assert.equal(page.devices.length, 2);
assert.equal(page.devices[1].children[1].disabled, false);
assert.equal(page.devices[1].children[0].textContent, "Apple Passwords");
assert.equal(page.devices[1].children[2].children[0].textContent, "Synced");
assert.equal(page.devices[1].children[3].src, "/_/auth/icons/apple.svg", "the provider's logo");

// The portal shows what the server lists, needs no WebAuthn, and sends a
// signed-out person to log in and come back.
async function portal(reply, opts = {}) {
  const calls = [];
  const assigned = [];
  const page = render("", {
    page: "me",
    webauthn: false,
    fetch: async (url, init) => {
      calls.push([url, init && init.method]);
      return reply;
    },
    ...opts,
    assign: (url) => assigned.push(url),
  });
  await new Promise((r) => setTimeout(r, 10));
  return { page, calls, assigned };
}
const ok = (body) => ({ ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) });

let shown = await portal(ok({ email: "ana@example.com", method: "passkey", passkeys: true, apps: [{ host: "wiki.example.com", url: "https://wiki.example.com/", note: "May ask you to sign in again" }] }));
assert.deepEqual(shown.calls[0], ["/_/auth/me/data", "POST"]);
assert.deepEqual(shown.page.apps, [["https://wiki.example.com/", "wiki.example.com", "May ask you to sign in again"]], "the note is visible, not only a tooltip");
assert.equal(shown.page.who(), "ana@example.com");
assert.equal(shown.page.manageShown(), true);
assert.match(shown.page.intro, /These are the apps/);

shown = await portal(ok({ email: "bob@example.com", method: "oidc", passkeys: false, apps: [] }));
assert.deepEqual(shown.page.apps, []);
assert.equal(shown.page.manageShown(), false, "no passkey management for a provider login");
assert.match(shown.page.intro, /No app is open to you yet/);

shown = await portal({ ok: false, status: 401, json: async () => ({ error: "You are signed out. Sign in again." }), text: async () => "" });
assert.deepEqual(shown.assigned, ["/_/auth/login?return_to=" + encodeURIComponent("https://auth.example.com/_/auth/me")], "a signed-out person logs in and comes back to the portal");
assert.equal(shown.page.who(), "", "nothing about a person is shown without a session");

console.log("passkey pages: 13 variants OK");
