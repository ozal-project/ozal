// Runs the passkey login page script (internal/passkey/assets/passkey.js)
// against a minimal fake DOM and checks what each variant shows: which
// methods are offered, the step-up wording, and that a browser without
// WebAuthn still gets the provider button. No browser needed; run by
// scripts/verify.sh when node is installed.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const src = readFileSync(new URL("../internal/passkey/assets/passkey.js", import.meta.url), "utf8");

function render(search, { oidc = "", webauthn = true, page = "login", fetch = () => new Promise(() => {}), assign = () => {}, caps, get = () => new Promise(() => {}), create = () => new Promise(() => {}), hash = "", uvpa, storage = {} } = {}) {
  const els = {};
  const el = (id) => (els[id] ??= {
    id, textContent: "", value: "", src: "", hidden: id === "oidc" || id === "recovery-warning" || id === "receipt" || id === "receipt-return" || id === "receipt-icon" || id === "bridge", open: false, href: "", disabled: false, dataset: {}, children: [], on: {},
    addEventListener(ev, fn) { this.on[ev] = fn; }, focus() {}, setAttribute() {}, append(...c) { this.children.push(...c); }, replaceChildren(...c) { this.children = c; },
    querySelector(q) { return q === "a" ? this.children.find((c) => c.href) || null : null; },
  });
  ["title", "intro", "action", "oidc", "status", "first-time", "username", "sites", "devices", "who", "email", "signout", "apps", "manage", "choices", "choice-list", "use-phone", "use-key", "recovery-warning", "sessions", "signout-all", "signout-all-confirm", "also-admin", "receipt", "receipt-where", "receipt-icon", "receipt-return", "receipt-form", "receipt-name", "receipt-continue", "bridge", "bridge-continue", "bridge-add", "bridge-never"].forEach(el);
  const label = { hidden: false };
  const head = [];
  let ready;
  const document = {
    head: { appendChild: (node) => head.push(node) },
    title: "Sign in · Ozal",
    body: { dataset: { page, oidc } },
    getElementById: (id) => els[id],
    querySelector: (q) => (q === "label[for=username]" ? label : null),
    querySelectorAll: () => [],
    createElement: () => el("x" + Math.random()),
    addEventListener: (ev, fn) => { if (ev === "DOMContentLoaded") ready = fn; },
  };
  const ctx = {
    document, URL, URLSearchParams, encodeURIComponent, AbortController, atob, btoa, JSON, Date, String, Uint8Array, DOMException,
    window: webauthn ? { PublicKeyCredential: Object.assign(function () {}, caps === undefined ? {} : { getClientCapabilities: caps }) } : {},
    setTimeout,
    location: { search, hash, pathname: "/_/auth/" + page, origin: "https://auth.example.com", assign },
    history: { replaceState() {} },
    localStorage: { setItem(k, v) { storage[k] = String(v); }, getItem: (k) => storage[k] ?? null, removeItem(k) { delete storage[k]; } },
    navigator: webauthn ? { credentials: { get, create } } : {},
    PublicKeyCredential: webauthn ? (uvpa == null ? {} : { isUserVerifyingPlatformAuthenticatorAvailable: uvpa }) : undefined,
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
    el: (id) => els[id],
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
// An enrolment that returns its passkey stops on a receipt: where it was
// saved only when the provider is known, and an optional name saved with the
// ordinary rename. A failed name never blocks continuing.
function receiptPage(passkey, renames, renameFails = false) {
  return enrol({
    fetch: async (url, init) => {
      if (url.endsWith("/info")) return { ok: true, json: async () => ({ recovery: false }) };
      if (url.endsWith("/begin")) return { ok: true, json: async () => ({ publicKey: { challenge: "AAAA", user: { id: "AAAA" } } }) };
      if (url.endsWith("/rename")) {
        renames.push(JSON.parse(init.body));
        return renameFails ? { ok: false, status: 500, json: async () => ({ error: "storage unavailable" }) } : { ok: true, json: async () => ({}) };
      }
      return { ok: true, json: async () => ({ redirect: "", sites: [], passkey }) };
    },
    create: async () => credential,
  });
}
const submit = (p) => p.el("receipt-form").on.submit({ preventDefault() {} });

let renames = [];
page = receiptPage({ id: "abcd", provider: "Bitwarden", provider_known: true, icon: "bitwarden" }, renames);
await tick();
await page.click();
await tick();
assert.equal(page.el("receipt").hidden, false, "a new passkey is followed by its receipt");
assert.equal(page.el("receipt-where").textContent, "Saved in Bitwarden.");
assert.equal(page.el("receipt-icon").src, "/_/auth/icons/bitwarden.svg");
assert.match(page.el("receipt-return").textContent, /Bitwarden extension/);
assert.equal(page.el("receipt-return").hidden, false);
assert.deepEqual(page.siteLinks, [], "nothing moves on before Continue");
page.el("receipt-name").value = "  Work laptop ";
await submit(page);
assert.deepEqual(renames, [{ id: "abcd", label: "Work laptop" }], "the name is saved with the ordinary rename");
assert.equal(page.el("receipt").hidden, true);
assert.deepEqual(page.siteLinks, ["Add a backup passkey somewhere else"]);

renames = [];
page = receiptPage({ id: "abcd", provider: "Synced passkey", provider_known: false, icon: "synced" }, renames);
await tick();
await page.click();
await tick();
assert.doesNotMatch(page.el("receipt-where").textContent, /Synced passkey/, "a kind of passkey is never shown as a place");
assert.match(page.el("receipt-where").textContent, /did not say where/);
assert.equal(page.el("receipt-icon").hidden, true);
assert.equal(page.el("receipt-return").hidden, true);
await submit(page);
assert.deepEqual(renames, [], "no name, no rename");
assert.deepEqual(page.siteLinks, ["Add a backup passkey somewhere else"]);

renames = [];
page = receiptPage({ id: "abcd", provider: "Bitwarden", provider_known: true, icon: "bitwarden" }, renames, true);
await tick();
await page.click();
await tick();
page.el("receipt-name").value = "Laptop";
await submit(page);
assert.match(page.status, /name was not saved/);
assert.equal(page.el("receipt-continue").textContent, "Continue without a name");
assert.equal(page.el("receipt").hidden, false, "a failed name stays on the receipt once");
await submit(page);
assert.equal(renames.length, 1, "the same failed name is not retried");
assert.equal(page.el("receipt").hidden, true, "the person continues without it");

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

// The bridge: a sign-in with a passkey on another device or a key offers to
// keep one here too, only where this device has its own authenticator, and
// never in the way: Continue goes on at once.
const buf = () => new Uint8Array([1]).buffer;
const signedWith = (attachment) => ({ id: "c", rawId: buf(), type: "public-key", authenticatorAttachment: attachment, getClientExtensionResults: () => ({}), response: { clientDataJSON: buf(), authenticatorData: buf(), signature: buf(), userHandle: buf() } });
async function bridgePage({ attachment = "cross-platform", uvpa = async () => true, storage = {}, search = rt, addFails = false } = {}) {
  const went = [];
  const madeWith = [];
  const p = render(search, {
    uvpa, storage,
    assign: (url) => went.push(url),
    fetch: async (url) => {
      if (url.endsWith("/login/begin")) return { ok: true, json: async () => ({ publicKey: { challenge: "AAAA" } }) };
      if (url.endsWith("/login/finish")) return { ok: true, json: async () => ({ redirect: "https://private.example.com/x" }) };
      if (url.endsWith("/add/begin")) return addFails ? { ok: false, status: 403, json: async () => ({ error: "Confirm it's you" }) } : { ok: true, json: async () => ({ publicKey: { challenge: "AAAA", user: { id: "AAAA" } } }) };
      if (url.endsWith("/add/finish")) return { ok: true, json: async () => ({ id: "ab", provider: "Bitwarden", provider_known: true }) };
      return { ok: true, json: async () => ({}) };
    },
    get: async (request) => (request.mediation ? new Promise(() => {}) : signedWith(attachment)),
    create: async (request) => { madeWith.push(request.publicKey.hints && [...request.publicKey.hints]); return credential; },
  });
  await tick();
  p.click();
  await tick(20);
  return { p, went, madeWith };
}

let b = await bridgePage();
assert.equal(b.p.el("bridge").hidden, false, "another device's passkey offers one for this device");
assert.deepEqual(b.went, [], "the offer waits for the person");
await b.p.click("bridge-continue");
assert.deepEqual(b.went, ["https://private.example.com/x"], "Continue goes on without creating anything");
assert.deepEqual(b.madeWith, []);

b = await bridgePage();
await b.p.click("bridge-add");
await tick();
assert.deepEqual(b.madeWith, [["client-device"]], "the new passkey is steered to this device");
assert.deepEqual(b.went, ["https://private.example.com/x"]);
assert.match(b.p.status, /Bitwarden/);

b = await bridgePage({ addFails: true });
await b.p.click("bridge-add");
await tick();
assert.match(b.p.status, /continue without it/);
assert.deepEqual(b.went, [], "a failed add leaves the choice to the person");
assert.equal(b.p.el("bridge").hidden, false);
await b.p.click("bridge-continue");
assert.deepEqual(b.went, ["https://private.example.com/x"]);

const kept = {};
b = await bridgePage({ storage: kept });
await b.p.click("bridge-never");
assert.deepEqual(b.went, ["https://private.example.com/x"]);
b = await bridgePage({ storage: kept });
assert.equal(b.p.el("bridge").hidden, true, "a refusal is remembered on this device");
assert.deepEqual(b.went, ["https://private.example.com/x"]);

for (const [why, options] of [
  ["a passkey of this device", { attachment: "platform" }],
  ["no authenticator here", { uvpa: async () => false }],
  ["a browser that cannot tell", { uvpa: null }],
  ["a failing check", { uvpa: async () => { throw new Error("no"); } }],
  ["a step-up", { search: rt + "&reauth=1" }],
]) {
  b = await bridgePage(options);
  assert.equal(b.p.el("bridge").hidden, true, `no offer after ${why}`);
  assert.deepEqual(b.went, ["https://private.example.com/x"], `${why}: straight on`);
}
b = await bridgePage({ uvpa: () => new Promise(() => {}) });
await tick(1600);
assert.equal(b.p.el("bridge").hidden, true, "a browser that never answers does not hold the sign-in");
assert.deepEqual(b.went, ["https://private.example.com/x"]);

// Browser errors other than NotAllowedError name what the person can do.
for (const [name, said] of [["NotSupportedError", /cannot use this kind of passkey/], ["SecurityError", /Open the sign-in link again/], ["UnknownError", /could not use a passkey here/]]) {
  page = render(rt, {
    fetch: async () => ({ ok: true, json: async () => ({ publicKey: { challenge: "AAAA" } }) }),
    get: async (request) => { if (request.mediation) return new Promise(() => {}); throw new DOMException("browser detail", name); },
  });
  await tick();
  page.click();
  await tick();
  assert.match(page.status, said, name);
  assert.doesNotMatch(page.status, /browser detail/, "the browser's technical text is not shown");
}

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
assert.ok(shown.calls.some(([url, method]) => url === "/_/auth/me/data" && method === "POST"), "the portal reads its data with POST");
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

// Destructive actions ask in the page, never through confirm() (this fake
// window has none, so a native dialog would throw here).
{
  const posts = [];
  const fetchRecord = (url, init) => {
    posts.push(url);
    return Promise.resolve({ ok: true, json: () => Promise.resolve(url.endsWith("/list") ? { email: "ana@example.com", passkeys: [key("aa11", false), key("dd44", true)], sites: [] } : {}) });
  };
  page = render("", { page: "devices", fetch: fetchRecord });
  await settle();
  const item = page.devices[1];
  const remove = item.children[1];
  const ask = item.children[4];
  assert.equal(ask.hidden, true, "no question before the button is used");
  remove.on.click();
  assert.equal(remove.hidden, true, "the button gives way to the question");
  assert.equal(ask.hidden, false);
  assert.match(ask.children[0].textContent, /Remove the Apple Passwords passkey\? You will be signed out everywhere/);
  assert.equal(ask.children[1].textContent, "Remove");
  assert.ok(!posts.some((u) => u.includes("revoke")), "asking removes nothing");
  ask.children[2].on.click();
  assert.equal(ask.hidden, true, "Cancel closes the question");
  assert.equal(remove.hidden, false, "and brings the button back");
  remove.on.click();
  ask.on.keydown({ key: "Escape" });
  assert.equal(remove.hidden, false, "Escape backs out too");
  assert.ok(!posts.some((u) => u.includes("revoke")), "backing out removes nothing");
  remove.on.click();
  ask.children[1].on.click();
  await settle();
  assert.ok(posts.some((u) => u.includes("/_/auth/devices/revoke")), "confirming removes the passkey");
}
{
  const posts = [];
  const assigned = [];
  const sessions = { sessions: [{ id: "s1", current: true, method: "passkey", last_seen: "2026-11-02T20:00:00Z", expires: "2026-11-09T20:00:00Z" }] };
  page = render("", {
    page: "me", webauthn: false, assign: (u) => assigned.push(u),
    fetch: (url) => { posts.push(url); return Promise.resolve({ ok: true, status: 200, json: async () => (url.endsWith("/sessions") ? sessions : { email: "ana@example.com", method: "passkey", passkeys: true, apps: [] }) }); },
  });
  await settle();
  const end = page.el("sessions").children[0].children[1];
  const askEnd = page.el("sessions").children[0].children[2];
  end.on.click();
  assert.equal(askEnd.hidden, false);
  assert.equal(askEnd.children[1].textContent, "Sign out", "ending this session says it signs you out");
  assert.ok(!posts.some((u) => u.endsWith("/revoke")), "asking ends nothing");
  page.click("signout-all");
  const all = page.el("signout-all-confirm");
  assert.equal(all.hidden, false, "sign out everywhere asks in the page");
  assert.match(all.children[0].textContent, /Your passkeys stay registered/);
  all.children[1].on.click();
  await settle();
  assert.ok(posts.some((u) => u.endsWith("/_/auth/me/sessions/revoke")), "confirming signs out everywhere");
  assert.deepEqual(assigned, ["/_/auth/login"]);
}

// Each passkey shows its owner's name, marks the one behind this sign-in,
// and is renamed in place.
{
  const posts = [];
  const keys = [
    { ...key("aa11", true), provider: "Bitwarden", label: "Work laptop", current: true, last_client: "Chrome on macOS" },
    { ...key("dd44", true), label: "" },
  ];
  const fetchRecord = (url, init) => {
    posts.push([url, init && init.body]);
    return Promise.resolve({ ok: true, json: () => Promise.resolve(url.endsWith("/list") ? { email: "ana@example.com", passkeys: keys, sites: [] } : { label: "Phone" }) });
  };
  page = render("", { page: "devices", fetch: fetchRecord });
  await settle();
  const [named, plain] = page.devices;
  assert.equal(named.children[0].textContent, "Work laptop", "the owner's name is the title");
  assert.equal(plain.children[0].textContent, "Apple Passwords", "without one, the provider names it");
  const meta = named.children[2].children;
  assert.equal(meta[1].textContent, "Used for this sign-in");
  assert.match(meta[2].textContent, /^Bitwarden · Added .* · Chrome on macOS$/, "the provider and the last browser stay visible");
  assert.doesNotMatch(meta[2].textContent, /not used yet/, "the passkey behind this sign-in is in use");
  assert.match(plain.children[2].children.at(-1).textContent, /not used yet/);
  assert.ok(!plain.children[2].children.some((c) => c.textContent === "Used for this sign-in"), "only the current passkey is marked");
  const renameButton = plain.children[6];
  const form = plain.children[5];
  assert.equal(renameButton.textContent, "Rename");
  assert.equal(form.hidden, true);
  renameButton.on.click();
  assert.equal(form.hidden, false, "Rename opens the field in place");
  assert.equal(form.input.value, "Apple Passwords", "prefilled with the current name");
  form.on.keydown({ key: "Escape" });
  assert.equal(form.hidden, true, "Escape closes it");
  assert.ok(!posts.some(([u]) => u.endsWith("/rename")), "closing renames nothing");
  renameButton.on.click();
  form.input.value = "  Phone ";
  await form.on.submit({ preventDefault() {} });
  const sent = posts.find(([u]) => u.endsWith("/_/auth/devices/rename"));
  assert.deepEqual(JSON.parse(sent[1]), { id: "dd44", label: "Phone" }, "the trimmed name goes to the server");
}

// The portal names each session's browser and says when the email is also
// an administrator, whose passkeys are separate.
{
  const sessions = { sessions: [
    { id: "s1", current: true, method: "passkey", client: "Safari on iPhone", last_seen: "2026-11-02T20:00:00Z", expires: "2026-11-09T20:00:00Z" },
    { id: "s2", current: false, method: "passkey", last_seen: "2026-11-01T20:00:00Z", expires: "2026-11-08T20:00:00Z" },
  ] };
  const me = (admin) => render("", {
    page: "me", webauthn: false,
    fetch: (url) => Promise.resolve({ ok: true, status: 200, json: async () => (url.endsWith("/sessions") ? sessions : { email: "ana@example.com", method: "passkey", passkeys: true, apps: [], also_admin: admin }) }),
  });
  page = me(true);
  await settle();
  const [current, other] = page.el("sessions").children;
  assert.equal(current.children[0].children[0].textContent, "Safari on iPhone · this browser");
  assert.match(current.children[0].children[1].textContent, /^Passkey · active now · ends /);
  assert.equal(other.children[0].children[0].textContent, "A browser", "an older session without a browser still reads");
  assert.equal(current.children[1].textContent, "Sign out");
  assert.equal(other.children[1].textContent, "End");
  const notice = page.el("also-admin");
  assert.equal(notice.hidden, false);
  assert.match(notice.textContent, /ana@example\.com · Ozal console/);
  page = me(false);
  await settle();
  assert.equal(page.el("also-admin").hidden, true, "no notice for a visitor only");
}

console.log("passkey pages: login, bridge, enrolment, receipt, recovery preflight, choices, devices, rename and sessions OK");
