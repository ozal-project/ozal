// Runs the passkey login page script (internal/passkey/assets/passkey.js)
// against a minimal fake DOM and checks what each variant shows: which
// methods are offered, the step-up wording, and that a browser without
// WebAuthn still gets the provider button. No browser needed; run by
// scripts/verify.sh when node is installed.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const src = readFileSync(new URL("../internal/passkey/assets/passkey.js", import.meta.url), "utf8");

function render(search, { oidc = "", webauthn = true, page = "login", fetch = () => new Promise(() => {}), assign = () => {}, caps, get = () => new Promise(() => {}), create = () => new Promise(() => {}), hash = "" } = {}) {
  const els = {};
  const el = (id) => (els[id] ??= {
    id, textContent: "", hidden: id === "oidc" || id === "recovery-warning", open: false, href: "", disabled: false, dataset: {}, children: [], on: {},
    addEventListener(ev, fn) { this.on[ev] = fn; }, focus() {}, setAttribute() {}, append(...c) { this.children.push(...c); }, replaceChildren(...c) { this.children = c; },
    querySelector(q) { return q === "a" ? this.children.find((c) => c.href) || null : null; },
  });
  ["title", "intro", "action", "oidc", "status", "first-time", "username", "sites", "devices", "who", "email", "signout", "apps", "manage", "choices", "choice-list", "use-phone", "use-key", "recovery-warning"].forEach(el);
  const label = { hidden: false };
  const head = [];
  let ready;
  const document = {
    head: { appendChild: (node) => head.push(node) },
    title: "Sign in · Ozal",
    body: { dataset: { page, oidc } },
    getElementById: (id) => els[id],
    querySelector: (q) => (q === "label[for=username]" ? label : null),
    createElement: () => el("x" + Math.random()),
    addEventListener: (ev, fn) => { if (ev === "DOMContentLoaded") ready = fn; },
  };
  const ctx = {
    document, URL, URLSearchParams, encodeURIComponent, AbortController, atob, btoa, JSON, Date, String, Uint8Array,
    window: webauthn ? { PublicKeyCredential: Object.assign(function () {}, caps === undefined ? {} : { getClientCapabilities: caps }) } : {},
    setTimeout,
    location: { search, hash, pathname: "/_/auth/" + page, origin: "https://auth.example.com", assign },
    history: { replaceState() {} },
    localStorage: { setItem() {}, getItem: () => null, removeItem() {} },
    navigator: webauthn ? { credentials: { get, create } } : {},
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
    get recoveryWarning() { return !els["recovery-warning"].hidden; },
    get actionLabel() { return els.action.textContent; },
    get devices() { return els.devices.children; },
    get apps() { return els.apps.children.map((a) => [a.href, a.textContent, (a.children[0] || {}).textContent || ""]); },
    who: () => (els.who.hidden ? "" : els.email.textContent),
    manageShown: () => !els.manage.hidden,
    get choices() { return els.choices.hidden ? "hidden" : els.choices.open ? "open" : "closed"; },
    get choiceOrder() { return els["choice-list"].children.map((item) => item.dataset.choice); },
    // The checked choice and the label of each, as rendered.
    get checked() { return (els["choice-list"].children.find((item) => item.children[0].checked) || { dataset: {} }).dataset.choice; },
    get choiceLabels() { return els["choice-list"].children.map((item) => item.children[1].textContent); },
    pick: (choice) => els["choice-list"].children.find((item) => item.dataset.choice === choice).children[0].on.change(),
    get siteLinks() { return els.sites.children.map((a) => a.textContent); },
    click: (id = "action") => els[id].on.click(),
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

// Where a passkey can live (ID-017): the person picks the place before the
// prompt and that becomes a hint to the browser. Capabilities only reorder
// and recommend; no choice is hidden, and no or a failed answer keeps the
// default order without a recommendation.
const tick = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
const all = ["device", "manager", "phone", "key"];
const enrol = (opts = {}) => render("", { page: "enrol", hash: "#invite-token", ...opts });
page = enrol();
await tick();
assert.equal(page.choices, "closed", "the enrolment page asks where to keep the passkey before the prompt");
assert.deepEqual(page.choiceOrder, all, "an old browser that cannot answer shows every choice");
assert.equal(page.checked, "device");
assert.ok(page.choiceLabels.every((label) => !/recommended/.test(label)), "nothing is recommended without an answer");
assert.match(page.choiceLabels[1], /password manager/);
page = enrol({ caps: async () => ({ userVerifyingPlatformAuthenticator: false, hybridTransport: true }) });
await tick();
assert.deepEqual(page.choiceOrder, ["manager", "phone", "key", "device"], "no built-in authenticator: the password manager is suggested first, none hidden");
assert.equal(page.checked, "manager");
assert.match(page.choiceLabels[0], /recommended here/);
page = enrol({ caps: async () => ({ passkeyPlatformAuthenticator: true }) });
await tick();
assert.deepEqual(page.choiceOrder, all);
assert.match(page.choiceLabels[0], /This device · recommended here/);
page = enrol({ caps: async () => { throw new Error("not allowed"); } });
await tick();
assert.deepEqual(page.choiceOrder, all, "a failing answer is unknown, not unsupported");
page = enrol({ caps: () => new Promise(() => {}) });
await tick(1100);
assert.deepEqual(page.choiceOrder, all, "a browser that never answers still shows every choice");
page = enrol({ caps: () => new Promise((resolve) => setTimeout(() => resolve({ userVerifyingPlatformAuthenticator: false }), 20)) });
page.pick("key");
await tick(40);
assert.equal(page.checked, "key", "a later answer never overrides what the person picked");

// The choice reaches the browser as a hint; the password manager adds none
// (its extension takes the prompt whatever the hint).
const creationReply = async (url) => ({ ok: true, json: async () => (url.endsWith("/info") ? { recovery: false } : url.endsWith("/begin") ? { publicKey: { challenge: "AAAA", user: { id: "AAAA" } } } : { sites: [] }) });
const created = [];
const credential = { id: "c", rawId: new Uint8Array([1]).buffer, type: "public-key", getClientExtensionResults: () => ({}), response: { clientDataJSON: new Uint8Array([1]).buffer, attestationObject: new Uint8Array([1]).buffer } };
for (const [choice, hints] of [["phone", ["hybrid"]], ["key", ["security-key"]], ["device", ["client-device"]], ["manager", undefined]]) {
  page = enrol({ fetch: creationReply, create: async (request) => { created.push(request.publicKey.hints && [...request.publicKey.hints]); return credential; } });
  await tick();
  page.pick(choice);
  page.click();
  await tick();
  assert.deepEqual(created.at(-1), hints, `${choice} sends ${hints}`);
}
assert.deepEqual(page.siteLinks, ["Add a backup passkey somewhere else"], "a first passkey is followed by the suggestion of a backup");
assert.match(page.status, /second one in another place/);

// Recovery is disclosed from server metadata before the first authenticator
// prompt. The explicit button acknowledges replacement, never a silent create.
let promptCount = 0;
let infoReply;
page = enrol({
  fetch: async (url) => url.endsWith("/info") ? new Promise((resolve) => { infoReply = resolve; }) : creationReply(url),
  create: async () => { promptCount++; return credential; },
});
assert.equal(page.passkey, "disabled", "the invitation is checked before enabling creation");
assert.equal(promptCount, 0);
infoReply({ ok: true, json: async () => ({ recovery: true }) });
await tick();
assert.equal(page.recoveryWarning, true, "recovery consequences are visible before the prompt");
assert.equal(page.actionLabel, "Replace my passkeys");
assert.equal(promptCount, 0, "preflight never starts an authenticator");
await page.click();
assert.equal(promptCount, 1);

page = enrol({ fetch: creationReply });
await tick();
assert.equal(page.recoveryWarning, false, "ordinary enrolment is not called recovery");
assert.equal(page.actionLabel, "Create passkey");

for (const broken of [async () => { throw new Error("offline"); }, async () => ({ ok: true, json: async () => ({}) })]) {
  const calls = [];
  let failInfo = true;
  page = enrol({
    fetch: async (url) => { calls.push(url); return failInfo ? broken() : { ok: true, json: async () => ({ recovery: true }) }; },
    create: async () => { throw new Error("must not prompt"); },
  });
  await tick();
  assert.equal(page.actionLabel, "Check link again");
  assert.equal(page.recoveryWarning, false);
  failInfo = false;
  await page.click();
  assert.equal(page.recoveryWarning, true);
  assert.equal(page.actionLabel, "Replace my passkeys");
  assert.ok(calls.every((url) => url.endsWith("/info")), "a retry checks metadata; the person must click again to replace");
}

page = render(rt + "&reauth=method&methods=oidc", { oidc: "Okta" });
assert.equal(page.choices, "hidden", "no passkey guidance where only the provider is allowed");
page = render(rt, { webauthn: false });
assert.equal(page.choices, "open", "a browser without passkeys still learns where a passkey can live");
assert.match(page.status, /another browser|a device that does/);
page = enrol({ webauthn: false });
assert.notEqual(page.choices, "hidden");
assert.match(page.status, /Copy this page's address/, "the invitation can move to a browser that supports passkeys");

// Signing in keeps one button for the passkey at hand; the passkey
// somewhere else is one step away, steered by the same hints.
const notAllowed = Object.assign(new Error("closed"), { name: "NotAllowedError" });
const asked = [];
page = render(rt, {
  fetch: async () => ({ ok: true, json: async () => ({ publicKey: { challenge: "AAAA" } }) }),
  get: async (request) => { if (request.mediation) return new Promise(() => {}); asked.push(request.publicKey.hints && [...request.publicKey.hints]); throw notAllowed; },
});
await tick();
assert.equal(page.choices, "closed");
page.click();
await tick();
assert.equal(page.choices, "open", "a prompt that ended without a passkey unfolds the other places");
assert.match(page.status, /no passkey for this site/);
page.click("use-phone");
await tick();
page.click("use-key");
await tick();
assert.deepEqual(asked, [undefined, ["hybrid"], ["security-key"]], "the main button adds no hint; the shortcuts steer to the phone or the key");
page = render("", { page: "devices", fetch: () => new Promise(() => {}) });
assert.equal(page.choices, "closed", "adding a passkey asks where to keep it too");

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

console.log("passkey pages: login, enrolment, recovery preflight, choices and devices OK");
