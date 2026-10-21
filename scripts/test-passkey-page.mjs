// Runs the passkey login page script (internal/passkey/assets/passkey.js)
// against a minimal fake DOM and checks what each variant shows: which
// methods are offered, the step-up wording, and that a browser without
// WebAuthn still gets the provider button. No browser needed; run by
// scripts/verify.sh when node is installed.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const src = readFileSync(new URL("../internal/passkey/assets/passkey.js", import.meta.url), "utf8");

function render(search, { oidc = "", webauthn = true } = {}) {
  const els = {};
  const el = (id) => (els[id] ??= { id, textContent: "", hidden: id === "oidc", href: "", disabled: false, dataset: {}, addEventListener() {}, replaceChildren() {}, focus() {} });
  ["title", "intro", "action", "oidc", "status", "first-time", "username", "sites"].forEach(el);
  const label = { hidden: false };
  let ready;
  const document = {
    title: "Sign in · Switchboard",
    body: { dataset: { page: "login", oidc } },
    getElementById: (id) => els[id],
    querySelector: (q) => (q === "label[for=username]" ? label : null),
    createElement: () => el("x" + Math.random()),
    addEventListener: (ev, fn) => { if (ev === "DOMContentLoaded") ready = fn; },
  };
  const ctx = {
    document, URLSearchParams, encodeURIComponent, AbortController, atob, btoa, JSON, Date, String, Uint8Array,
    window: webauthn ? { PublicKeyCredential: function () {} } : {},
    location: { search, hash: "", pathname: "/_/auth/login", assign() {} },
    history: { replaceState() {} },
    localStorage: { setItem() {}, getItem: () => null, removeItem() {} },
    navigator: webauthn ? { credentials: { get: () => new Promise(() => {}) } } : {},
    PublicKeyCredential: webauthn ? {} : undefined,
    fetch: () => new Promise(() => {}),
  };
  vm.createContext(ctx);
  vm.runInContext(src, ctx);
  ready();
  return {
    title: els.title.textContent,
    intro: els.intro.textContent,
    passkey: els.action.hidden ? "hidden" : els.action.disabled ? "disabled" : "shown",
    oidc: els.oidc.hidden ? "" : els.oidc.textContent,
    href: els.oidc.href,
    firstTime: !els["first-time"].hidden,
    status: els.status.textContent,
  };
}

const rt = "?return_to=" + encodeURIComponent("https://private.example.com/x");

let page = render(rt);
assert.equal(page.passkey, "shown");
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

console.log("passkey page: 8 variants OK");
