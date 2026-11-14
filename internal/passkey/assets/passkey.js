// Passkey enrolment and login for Ozal (ID-003). The server sends
// WebAuthn options with binary fields as base64url; the browser API wants
// ArrayBuffers, and the server wants base64url back.
"use strict";

const b64url = {
  decode(text) {
    let s = text.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    const binary = atob(s);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
  },
  encode(buffer) {
    let binary = "";
    for (const byte of new Uint8Array(buffer)) binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  },
};

async function post(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || "The request failed.");
  return data;
}

// hints, when the person said where they keep passkeys, steer the browser's
// prompt there; a browser that does not know hints ignores the member.
function creationOptions(options, hints) {
  const pk = options.publicKey;
  if (hints && hints.length) pk.hints = hints;
  pk.challenge = b64url.decode(pk.challenge);
  pk.user.id = b64url.decode(pk.user.id);
  for (const c of pk.excludeCredentials || []) c.id = b64url.decode(c.id);
  return pk;
}

function requestOptions(options, hints) {
  const pk = options.publicKey;
  if (hints && hints.length) pk.hints = hints;
  pk.challenge = b64url.decode(pk.challenge);
  for (const c of pk.allowCredentials || []) c.id = b64url.decode(c.id);
  return pk;
}

function credentialJSON(credential) {
  const r = credential.response;
  const out = {
    id: credential.id,
    rawId: b64url.encode(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: { clientDataJSON: b64url.encode(r.clientDataJSON) },
  };
  if (credential.authenticatorAttachment) out.authenticatorAttachment = credential.authenticatorAttachment;
  if (r.attestationObject) {
    out.response.attestationObject = b64url.encode(r.attestationObject);
    if (r.getTransports) out.response.transports = r.getTransports();
  } else {
    out.response.authenticatorData = b64url.encode(r.authenticatorData);
    out.response.signature = b64url.encode(r.signature);
    if (r.userHandle) out.response.userHandle = b64url.encode(r.userHandle);
  }
  return out;
}

const statusLine = () => document.getElementById("status");

function show(message, kind) {
  statusLine().textContent = message;
  statusLine().dataset.kind = kind || "";
}

// The browser reports a closed prompt, a timeout and "no passkey for this
// site on this device" all as NotAllowedError, on purpose (a site must not
// learn which passkeys a device holds). So the message names every case and
// never claims which one happened.
function explain(error, page) {
  if (error.name === "NotAllowedError") {
    return page === "login"
      ? "The sign-in did not finish: the prompt was closed, it timed out, or this device has no passkey for this site. If you have not created one yet, you need the invitation link (see below)."
      : "The passkey was not created: the prompt was closed or it timed out. If it asked for something you do not have, choose another place to keep it and try again.";
  }
  if (error.name === "InvalidStateError") return "This device already has a passkey for this account.";
  // Other browser errors carry technical text meant for developers; say what
  // the visitor can do instead. Server errors are plain Errors with a message
  // written for people, and are shown as they are.
  if (typeof DOMException !== "undefined" && error instanceof DOMException) {
    if (error.name === "SecurityError") return "This page cannot use passkeys here. Open the sign-in link again from the site you were visiting.";
    if (error.name === "NotSupportedError") return "This browser cannot use this kind of passkey. Update it, or use another browser, your phone or a security key.";
    return "This browser could not use a passkey here. Try again, or use another browser or device.";
  }
  return error.message || "Something went wrong.";
}

// Where a passkey can live (ID-017). A desktop without a built-in
// authenticator (most Linux machines) jumps straight to "insert your
// security key"; letting the person say first where they keep passkeys, and
// passing that to the browser as a hint, keeps that from being a dead end.
// Hints are a preference the browser may ignore; nothing here promises that
// a choice works in this browser, and no product is required.
const choices = {
  device: { label: "This device", detail: "Windows Hello, Touch ID or the screen lock of this phone or tablet.", hints: ["client-device"] },
  manager: {
    label: "My password manager",
    detail: "Its browser extension takes the prompt: for example Bitwarden (or self-hosted Vaultwarden), 1Password, Proton Pass or KeePassXC, or any other that saves passkeys.",
    hints: [],
  },
  phone: { label: "My phone", detail: "Scan the QR code the browser shows; this computer needs Bluetooth.", hints: ["hybrid"] },
  key: { label: "A security key", detail: "Plugged in by USB or held to NFC.", hints: ["security-key"] },
};

// recommendedChoice is the one worth suggesting on this browser, or "" when
// the browser did not say enough to suggest anything.
function recommendedChoice(caps) {
  const device = caps ? caps.userVerifyingPlatformAuthenticator ?? caps.passkeyPlatformAuthenticator : undefined;
  return device === true ? "device" : device === false ? "manager" : "";
}

// passkeyChoices orders the choices for this browser: the recommended one
// first, what it confirms next, what it denies last, and every choice stays.
// Its answer can be missing or incomplete, and only the person knows what
// they have.
function passkeyChoices(caps) {
  const order = ["device", "manager", "phone", "key"];
  const recommended = recommendedChoice(caps);
  const said = {
    device: caps ? caps.userVerifyingPlatformAuthenticator ?? caps.passkeyPlatformAuthenticator : undefined,
    phone: caps ? caps.hybridTransport : undefined,
  };
  const rank = (choice) => (choice === recommended ? -1 : said[choice] === true ? 0 : said[choice] === false ? 2 : 1);
  return order.slice().sort((a, b) => rank(a) - rank(b) || order.indexOf(a) - order.indexOf(b));
}

// readCapabilities asks the browser what it supports, briefly. No answer,
// an error or an old browser all mean "unknown", never "unsupported".
async function readCapabilities(timeout = 1000) {
  const credential = window.PublicKeyCredential;
  const ask = async () => {
    if (credential && typeof credential.getClientCapabilities === "function") return await credential.getClientCapabilities();
    if (credential && typeof credential.isUserVerifyingPlatformAuthenticatorAvailable === "function") {
      return { userVerifyingPlatformAuthenticator: await credential.isUserVerifyingPlatformAuthenticatorAvailable() };
    }
    return null;
  };
  try {
    return await Promise.race([ask(), new Promise((resolve) => setTimeout(() => resolve(null), timeout))]);
  } catch {
    return null;
  }
}

// The choice for a new passkey. Until the person picks one, a later and
// better-informed render may move the default; after that it never does.
const keep = { choice: "", picked: false };
const keepHints = () => (choices[keep.choice] || { hints: [] }).hints;

function renderChoices(caps) {
  const list = document.getElementById("choice-list");
  if (!list) return;
  const order = passkeyChoices(caps);
  const recommended = recommendedChoice(caps);
  if (!keep.picked) keep.choice = order[0];
  list.replaceChildren(...order.map((choice) => {
    const item = document.createElement("label");
    item.className = "choice";
    item.dataset.choice = choice;
    const input = document.createElement("input");
    input.type = "radio";
    input.name = "keep";
    input.value = choice;
    input.checked = choice === keep.choice;
    input.addEventListener("change", () => { keep.choice = choice; keep.picked = true; });
    const title = document.createElement("strong");
    title.textContent = choices[choice].label + (choice === recommended ? " · recommended here" : "");
    const detail = document.createElement("span");
    detail.textContent = choices[choice].detail;
    item.append(input, title, detail);
    return item;
  }));
}

function setupChoices(visible) {
  const box = document.getElementById("choices");
  if (!box) return;
  box.hidden = !visible;
  if (!visible) return;
  renderChoices(null);
  readCapabilities().then(renderChoices);
}

// openChoices unfolds the login page's other places after a prompt that
// ended without a passkey: the browser does not say why.
function openChoices(error) {
  const box = document.getElementById("choices");
  if (box && !box.hidden && error && error.name === "NotAllowedError") box.open = true;
}

// A destination remembered from the login page, so an invitation opened in
// the same browser can return there. The server validates it again; this is
// a convenience, never a trust decision.
const pendingKey = "ozal.returnTo";
const pendingTTL = 30 * 60 * 1000;
function rememberReturn(url) {
  try { localStorage.setItem(pendingKey, JSON.stringify({ url, at: Date.now() })); } catch {}
}
function pendingReturn() {
  try {
    const saved = JSON.parse(localStorage.getItem(pendingKey) || "null");
    return saved && Date.now() - saved.at < pendingTTL ? String(saved.url) : "";
  } catch { return ""; }
}
function forgetReturn() { try { localStorage.removeItem(pendingKey); } catch {} }

// After a sign-in or enrolment: go back to the validated destination, or
// say so and offer the protected sites as explicit links.
// backup, after a first enrolment, suggests a second passkey kept somewhere
// else: losing the only one is the costliest way to get stuck.
function finish(result, message, backup) {
  forgetReturn();
  if (result.redirect) {
    show(message + " Taking you back…", "ok");
    location.assign(result.redirect);
    return;
  }
  show(message, "ok");
  const link = (href, text) => {
    const a = document.createElement("a");
    a.href = href;
    a.textContent = text;
    return a;
  };
  const nav = document.getElementById("sites");
  nav.replaceChildren(
    ...(result.sites || []).map((url) => link(url, "Continue to " + new URL(url).host)),
    link("/_/auth/devices", backup ? "Add a backup passkey somewhere else" : "Manage your passkeys"),
  );
  nav.hidden = false;
  nav.querySelector("a").focus();
}

// warm opens the connection (DNS, TCP, TLS) to where the visitor goes next
// while they are still at the passkey prompt: from far away each new
// connection costs several hundred milliseconds. The server validates the
// destination later; this only opens a connection to it.
function warm(url) {
  try {
    const origin = new URL(url).origin;
    if (origin === location.origin || !/^https?:$/.test(new URL(url).protocol)) return;
    const link = document.createElement("link");
    link.rel = "preconnect";
    link.href = origin;
    document.head.appendChild(link);
  } catch {}
}

// setupLoginPage prepares the login page whatever the browser supports:
// every configured method is offered and the page never picks one for the
// visitor (no automatic redirect to the provider). On a step-up it says why.
function setupLoginPage(button) {
  const params = new URLSearchParams(location.search);
  const returnTo = params.get("return_to") || "";
  if (returnTo) {
    rememberReturn(returnTo);
    warm(returnTo);
  }
  const provider = document.body.dataset.oidc || "";
  const methods = (params.get("methods") || "").split(",").filter(Boolean);
  const allows = (method) => methods.length === 0 || methods.includes(method);
  const oidc = document.getElementById("oidc");
  if (provider && allows("oidc")) {
    oidc.textContent = "Sign in with " + provider;
    oidc.href = "/_/auth/oidc/start" + (returnTo ? "?return_to=" + encodeURIComponent(returnTo) : "");
    oidc.hidden = false;
  }
  const reauth = params.get("reauth");
  if (reauth === "1" || reauth === "device" || reauth === "method") {
    document.getElementById("title").textContent = "Confirm it's you";
    document.getElementById("intro").textContent = {
      "1": "This site asks for your passkey again because your last sign-in was a while ago.",
      device: "This site needs a passkey that stays on one device, such as a security key. A passkey synced through iCloud or Google is not enough here.",
      method: allows("passkey")
        ? "This site needs you to sign in with a passkey."
        : "This site needs you to sign in with " + (provider || "single sign-on") + ".",
    }[reauth];
    document.getElementById("first-time").hidden = true;
    document.title = "Confirm it's you · Ozal";
  }
  const passkeys = allows("passkey");
  if (!passkeys) {
    button.hidden = true;
    document.getElementById("username").hidden = true;
    document.querySelector("label[for=username]").hidden = true;
    document.getElementById("first-time").hidden = true;
  }
  return { returnTo, passkeys, provider: provider && allows("oidc") ? provider : "" };
}

// The bridge: after a sign-in with a passkey on another device or a security
// key, offer to keep one on this device too. It never blocks: Continue goes on
// at once, and a refusal is remembered on this device. Only where this device
// has its own authenticator, and never in a step-up, where the site asked for
// something specific.
const bridgeKey = "ozal.bridge.never";
async function offerBridge(credential) {
  if (!credential || credential.authenticatorAttachment !== "cross-platform") return false;
  if (new URLSearchParams(location.search).get("reauth")) return false;
  try { if (localStorage.getItem(bridgeKey)) return false; } catch {}
  const check = PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable;
  if (typeof check !== "function") return false;
  // A browser that does not answer quickly is treated as "no": signing in
  // must not wait on the offer.
  const answer = await Promise.race([check.call(PublicKeyCredential).catch(() => false), new Promise((resolve) => setTimeout(() => resolve(false), 1500))]);
  return answer === true;
}

function bridge(result) {
  const section = document.getElementById("bridge");
  if (!section) return finish(result, "You are signed in.");
  for (const id of ["title", "intro", "username", "action", "oidc", "choices", "first-time"]) {
    const el = document.getElementById(id);
    if (el) el.hidden = true;
  }
  const label = document.querySelector("label[for=username]");
  if (label) label.hidden = true;
  show("");
  section.hidden = false;
  const go = document.getElementById("bridge-continue");
  const add = document.getElementById("bridge-add");
  const never = document.getElementById("bridge-never");
  const done = (message) => { section.hidden = true; finish(result, message); };
  go.addEventListener("click", () => done("You are signed in."));
  never.addEventListener("click", () => {
    try { localStorage.setItem(bridgeKey, "1"); } catch {}
    done("You are signed in.");
  });
  add.addEventListener("click", async () => {
    add.disabled = true;
    show("Follow your device's prompt…");
    try {
      const options = await post("/_/auth/devices/add/begin", {});
      const created = await navigator.credentials.create({ publicKey: creationOptions(options, choices.device.hints) });
      const added = await post("/_/auth/devices/add/finish", credentialJSON(created));
      done((added.provider_known ? "Passkey saved in " + added.provider + "." : "Passkey saved on this device.") + " You are signed in.");
    } catch (error) {
      add.disabled = false;
      show(explain(error, "enrol") + " You can continue without it.", "error");
    }
  });
  go.focus();
}

function setupLogin(button, returnTo) {
  let pending = null;

  async function login(conditional, hints) {
    if (pending) pending.abort();
    const controller = new AbortController();
    pending = controller;
    const options = await post("/_/auth/login/begin", { return_to: returnTo, conditional });
    const request = { publicKey: requestOptions(options, hints), signal: controller.signal };
    if (conditional) request.mediation = "conditional";
    const credential = await navigator.credentials.get(request);
    button.disabled = true;
    show("Checking…");
    const result = await post("/_/auth/login/finish", credentialJSON(credential));
    if (await offerBridge(credential)) bridge(result);
    else finish(result, "You are signed in.");
  }

  function fail(error) {
    button.disabled = false;
    if (error.name !== "AbortError") show(explain(error, "login"), "error");
    openChoices(error);
  }

  button.addEventListener("click", () => login(false).catch(fail));
  // The passkey somewhere else: the same sign-in, steered to that place.
  for (const [id, choice] of [["use-phone", "phone"], ["use-key", "key"]]) {
    const other = document.getElementById(id);
    if (other) other.addEventListener("click", () => login(false, choices[choice].hints).catch(fail));
  }
  if (PublicKeyCredential.isConditionalMediationAvailable) {
    PublicKeyCredential.isConditionalMediationAvailable().then((ok) => {
      // The silent autofill attempt fails quietly: the button is still
      // there, and its errors are the ones worth showing.
      if (ok) login(true).catch((error) => { button.disabled = false; if (error.name !== "AbortError") console.debug("passkey autofill:", error.name); });
    });
  }
}

function setupEnrol(button) {
  // The token arrives in the fragment, which browsers never send to a
  // server or a link preview. Take it out of the address bar and history.
  const token = location.hash.slice(1);
  const returnTo = new URLSearchParams(location.search).get("return_to") || pendingReturn();
  if (returnTo) warm(returnTo);
  history.replaceState(null, "", location.pathname);
  if (!token) {
    button.disabled = true;
    show("This link has no invitation in it. Ask the operator for a new one.", "error");
    return;
  }
  let ready = false;
  async function checkLink() {
    button.disabled = true;
    show("Checking your invitation…");
    try {
      const info = await post("/_/auth/enrol/info", { token });
      if (typeof info.recovery !== "boolean") throw new Error("The invitation could not be checked. Try again.");
      document.getElementById("recovery-warning").hidden = !info.recovery;
      button.textContent = info.recovery ? "Replace my passkeys" : "Create passkey";
      ready = true;
      show("");
    } catch (error) {
      button.textContent = "Check link again";
      show(error.message, "error");
    } finally {
      button.disabled = false;
    }
  }
  checkLink();
  button.addEventListener("click", async () => {
    // A failed preflight never falls through to an authenticator prompt.
    if (!ready) { await checkLink(); return; }
    button.disabled = true;
    show("Waiting for your device…");
    try {
      const options = await post("/_/auth/enrol/begin", { token, return_to: returnTo });
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options, keepHints()) });
      const result = await post("/_/auth/enrol/finish", credentialJSON(credential));
      receipt(result);
    } catch (error) {
      button.disabled = false;
      show(explain(error, "enrol"), "error");
      openChoices(error);
    }
  });
}

// Where a known provider's passkeys can be used again. Only for providers
// whose reach is documented; the wording stays conditional because it also
// depends on the browser and the system.
const returnHints = {
  "Bitwarden": "To use it in another browser, install the Bitwarden extension there and unlock it.",
  "1Password": "To use it in another browser, install the 1Password extension there and unlock it.",
  "Google Password Manager": "Chrome signed in to the same Google account, and your Android phone, can offer it.",
  "Apple Passwords": "Your Apple devices signed in to the same Apple Account can offer it.",
};

// receipt follows a new enrolment: where the passkey was saved when the
// provider says so (never a guess), an optional name, then the destination.
// The name uses the ordinary rename, which the fresh session allows; a name
// that cannot be saved never keeps the person from continuing.
function receipt(result) {
  const created = result.passkey;
  if (!created || !created.id) {
    finish(result, "Your passkey is saved and you are signed in. A second one in another place (another device, your password manager or a key) keeps you from being locked out if you lose this one.", true);
    return;
  }
  for (const id of ["choices", "action", "recovery-warning"]) {
    const el = document.getElementById(id);
    if (el) el.hidden = true;
  }
  document.querySelectorAll("main > .hint").forEach((el) => { el.hidden = true; });
  document.getElementById("title").textContent = "Your passkey is saved";
  document.getElementById("intro").textContent = "You are signed in.";
  const where = document.getElementById("receipt-where");
  const icon = document.getElementById("receipt-icon");
  if (created.provider_known) {
    where.textContent = "Saved in " + created.provider + ".";
    icon.src = "/_/auth/icons/" + created.icon + ".svg";
    icon.hidden = false;
    const hint = returnHints[created.provider];
    if (hint) {
      const line = document.getElementById("receipt-return");
      line.textContent = hint;
      line.hidden = false;
    }
  } else {
    where.textContent = "Your device did not say where it saved it. A name helps you find it later, such as where you chose to keep it.";
  }
  document.getElementById("receipt").hidden = false;
  show("");
  const input = document.getElementById("receipt-name");
  const go = document.getElementById("receipt-continue");
  // A name that fails is reported once; submitting the same name again
  // continues without it, a different name is tried.
  let failed = null;
  input.addEventListener("input", () => {
    go.textContent = input.value.trim() === failed ? "Continue without a name" : "Continue";
  });
  document.getElementById("receipt-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const label = input.value.trim();
    let named = false;
    if (label && label !== failed) {
      go.disabled = true;
      try {
        await post("/_/auth/devices/rename", { id: created.id, label });
        named = true;
      } catch (error) {
        failed = label;
        go.disabled = false;
        go.textContent = "Continue without a name";
        show("The name was not saved: " + error.message, "error");
        return;
      }
    }
    document.getElementById("receipt").hidden = true;
    finish(result, named ? "Saved as “" + label + "”." : "Your passkey is saved.", true);
  });
  input.focus();
}

// ago turns a timestamp into "just now", "3 hours ago", "Oct 21".
function ago(iso) {
  const then = new Date(iso);
  const seconds = (Date.now() - then.getTime()) / 1000;
  const n = (value, unit) => value + " " + unit + (value === 1 ? "" : "s") + " ago";
  if (seconds < 90) return "just now";
  if (seconds < 3600) return n(Math.round(seconds / 60), "minute");
  if (seconds < 86400) return n(Math.round(seconds / 3600), "hour");
  if (seconds < 7 * 86400) return n(Math.round(seconds / 86400), "day");
  return then.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

// askInline swaps a destructive button for an in-page question in box: the
// action, Cancel, and Escape to back out. A native confirm() would show the
// browser's own "auth.example.com says" dialog instead of this page.
function askInline(button, box, question, action, onYes) {
  const make = (className, text) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = className;
    b.textContent = text;
    return b;
  };
  const text = document.createElement("span");
  text.textContent = question;
  const yes = make("danger", action);
  const no = make("link-button", "Cancel");
  box.className = "confirm";
  box.setAttribute("role", "group");
  box.setAttribute("aria-label", question);
  box.replaceChildren(text, yes, no);
  const close = (refocus) => {
    box.hidden = true;
    box.replaceChildren();
    button.hidden = false;
    if (refocus) button.focus();
  };
  no.addEventListener("click", () => close(true));
  box.addEventListener("keydown", (event) => { if (event.key === "Escape") close(true); });
  yes.addEventListener("click", () => { close(false); onYes(); });
  button.hidden = true;
  box.hidden = false;
  yes.focus();
}

// setupDevices lists the visitor's passkeys and lets them add or remove one
// (ID-008). The server checks every request against a recent passkey proof;
// when that lapses it answers 403 and the page sends the visitor to confirm.
function setupDevices(button) {
  const list = document.getElementById("devices");
  const relogin = () => location.assign("/_/auth/login?return_to=" + encodeURIComponent(location.origin + "/_/auth/devices") + "&reauth=1&methods=passkey");
  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  async function load() {
    const data = await post("/_/auth/devices/list", {});
    document.getElementById("email").textContent = data.email;
    document.getElementById("who").hidden = false;
    list.replaceChildren(...data.passkeys.map((key) => {
      const item = el("li");
      // The owner's name for it, else where it lives; the provider stays on
      // the second line when the two differ.
      const name = key.label || key.provider;
      const title = el("span", "provider", name);
      // The badge opens the second line, so a long provider name never wraps around it.
      const meta = el("span", "meta");
      // Enrolment signs in without recording a use, so the passkey behind
      // this sign-in never reads "not used yet".
      const used = key.last_used ? " · last used " + ago(key.last_used) : key.current ? "" : " · not used yet";
      const details = ["Added " + ago(key.created) + used];
      if (key.label && key.label !== key.provider) details.unshift(key.provider);
      if (key.last_client) details.push(key.last_client);
      meta.append(el("span", key.synced ? "badge" : "badge bound", key.synced ? "Synced" : "This device only"));
      if (key.current) meta.append(el("span", "badge current", "Used for this sign-in"));
      const renameButton = el("button", "rename-button", "Rename");
      renameButton.type = "button";
      renameButton.setAttribute("aria-label", "Rename the " + name + " passkey");
      meta.append(el("span", "details", details.join(" · ")));
      const form = renameForm(key, name, renameButton);
      renameButton.addEventListener("click", () => {
        renameButton.hidden = true;
        form.hidden = false;
        form.input.focus();
      });
      // The provider's logo; its name is already the title, so the image is decoration.
      const icon = el("img", "icon");
      icon.src = "/_/auth/icons/" + (key.icon || "key") + ".svg";
      icon.alt = "";
      icon.width = icon.height = 22;
      const remove = el("button", "", "Remove");
      remove.type = "button";
      remove.disabled = data.passkeys.length === 1;
      remove.title = remove.disabled ? "Your only passkey cannot be removed" : "";
      remove.setAttribute("aria-label", "Remove the " + name + " passkey");
      const ask = el("span", "confirm");
      ask.hidden = true;
      remove.addEventListener("click", () => askInline(remove, ask,
        "Remove the " + name + " passkey? You will be signed out everywhere.", "Remove", () => revoke(key.id).catch(fail)));
      item.append(title, remove, meta, icon, ask, form, renameButton);
      return item;
    }));
    const nav = document.getElementById("sites");
    const sites = data.sites || [];
    nav.replaceChildren(nav.querySelector("h2") || el("span"), ...sites.map((url) => {
      const a = el("a", "", new URL(url).host);
      a.href = url;
      return a;
    }));
    nav.hidden = sites.length === 0;
  }

  // renameForm edits a passkey's name in place: Enter or Save keeps it,
  // Escape or Cancel leaves it as it was.
  function renameForm(key, name, opener) {
    const form = el("form", "rename");
    form.hidden = true;
    const input = el("input");
    input.type = "text";
    input.value = name;
    input.maxLength = 60;
    input.autocomplete = "off";
    input.setAttribute("aria-label", "Name for this passkey");
    const save = el("button", "save", "Save");
    save.type = "submit";
    const cancel = el("button", "link-button", "Cancel");
    cancel.type = "button";
    const close = () => {
      form.hidden = true;
      input.value = name;
      opener.hidden = false;
      opener.focus();
    };
    cancel.addEventListener("click", close);
    form.addEventListener("keydown", (event) => { if (event.key === "Escape") close(); });
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const label = input.value.trim();
      if (!label || label === name) return close();
      save.disabled = true;
      try {
        await post("/_/auth/devices/rename", { id: key.id, label });
        show("Renamed to " + label + ".", "ok");
        await load();
      } catch (error) {
        save.disabled = false;
        fail(error);
      }
    });
    form.append(input, save, cancel);
    form.input = input;
    return form;
  }

  async function revoke(id) {
    show("Removing…");
    await post("/_/auth/devices/revoke", { id });
    show("Removed. You are signed out everywhere; sign in again with another passkey.", "ok");
    list.replaceChildren();
    button.disabled = true;
    document.getElementById("who").hidden = true;
  }

  function fail(error) {
    button.disabled = false;
    if (/Confirm it's you/.test(error.message)) return relogin();
    show(explain(error, "enrol"), "error");
    openChoices(error);
  }

  document.getElementById("signout").addEventListener("click", async () => {
    await fetch("/_/auth/logout", { method: "POST", credentials: "same-origin" }).catch(() => {});
    location.assign("/_/auth/login");
  });

  button.addEventListener("click", async () => {
    button.disabled = true;
    show("Waiting for your device…");
    try {
      const options = await post("/_/auth/devices/add/begin", {});
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options, keepHints()) });
      const added = await post("/_/auth/devices/add/finish", credentialJSON(credential));
      show(added.provider + " passkey added.", "ok");
      button.disabled = false;
      await load();
    } catch (error) {
      fail(error);
    }
  });
  load().catch(fail);
}

// setupMe is the portal (ID-012): who is signed in and the apps they may open.
// The server decides the list; the page only shows it. Nothing here needs
// WebAuthn, so it runs on any browser.
function setupMe() {
  const apps = document.getElementById("apps");
  const intro = document.getElementById("intro");
  const login = "/_/auth/login?return_to=" + encodeURIComponent(location.origin + "/_/auth/me");

  async function load() {
    const data = await post("/_/auth/me/data", {});
    document.getElementById("email").textContent = data.email;
    document.getElementById("who").hidden = false;
    document.getElementById("manage").hidden = !data.passkeys;
    const admin = document.getElementById("also-admin");
    if (admin) {
      admin.hidden = !data.also_admin;
      admin.textContent = data.also_admin
        ? `This email is also an administrator. Console passkeys are separate: your password manager saves them as “${data.email} · Ozal console”, and you manage them from the console. Use the other one here.`
        : "";
    }
    apps.replaceChildren(...data.apps.map((app) => {
      const a = document.createElement("a");
      a.href = app.url;
      a.textContent = app.host;
      if (app.note) {
        const note = document.createElement("small");
        note.textContent = app.note;
        a.append(note);
      }
      return a;
    }));
    intro.textContent = data.apps.length
      ? "These are the apps you can open. Pick one to go there."
      : "No app is open to you yet. Ask the person who runs this to give you access.";
  }

  async function loadSessions() {
    const list = document.getElementById("sessions");
    if (!list) return;
    const data = await post("/_/auth/me/sessions", {});
    list.replaceChildren(...data.sessions.map((session) => {
      const li = document.createElement("li");
      // The browser it came from names it; older sessions did not record one.
      const text = document.createElement("p");
      const where = document.createElement("strong");
      where.textContent = (session.client || "A browser") + (session.current ? " · this browser" : "");
      const when = document.createElement("span");
      const method = session.method === "passkey" ? "Passkey" : "Provider";
      when.textContent = `${method} · ${session.current ? "active now" : "last used " + ago(session.last_seen)} · ends ${new Date(session.expires).toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;
      text.append(where, when);
      const button = document.createElement("button");
      button.type = "button"; button.className = "link-button"; button.textContent = session.current ? "Sign out" : "End";
      const ask = document.createElement("span");
      ask.hidden = true;
      button.addEventListener("click", () => askInline(button, ask,
        session.current ? "End this session and sign out?" : "End this other session?",
        session.current ? "Sign out" : "End session",
        async () => {
          button.disabled = true;
          try {
            const result = await post("/_/auth/me/sessions/revoke", { id: session.id });
            if (result.signed_out) location.assign("/_/auth/login");
            else await loadSessions();
          } catch (error) { show(error.message, "error"); button.disabled = false; }
        }));
      li.append(text, button, ask); return li;
    }));
  }
  const signoutAll = document.getElementById("signout-all");
  signoutAll?.addEventListener("click", () => askInline(signoutAll, document.getElementById("signout-all-confirm"),
    "Sign out all your sessions, including this one? Your passkeys stay registered.", "Sign out everywhere",
    async () => {
      try { await post("/_/auth/me/sessions/revoke", { all: true }); location.assign("/_/auth/login"); }
      catch (error) { show(error.message, "error"); }
    }));
  loadSessions().catch((error) => show(error.message, "error"));

  document.getElementById("signout").addEventListener("click", async () => {
    await fetch("/_/auth/logout", { method: "POST", credentials: "same-origin" }).catch(() => {});
    location.assign("/_/auth/login");
  });

  load().catch((error) => {
    if (/signed out/i.test(error.message)) return location.assign(login);
    intro.textContent = "";
    show(explain(error, "enrol"), "error");
  });
}

document.addEventListener("DOMContentLoaded", () => {
  const button = document.getElementById("action");
  const kind = document.body.dataset.page;
  if (kind === "me") return setupMe();
  const page = kind === "login" ? setupLoginPage(button) : null;
  const webauthn = Boolean(window.PublicKeyCredential && navigator.credentials);
  const passkeys = !page || page.passkeys;
  setupChoices(passkeys);
  if (!webauthn) {
    // Not a dead end: say where a passkey can work instead, keep the
    // provider if there is one, and leave the invitation in the address bar
    // so it can be copied to another browser.
    button.disabled = true;
    if (passkeys) {
      const elsewhere = {
        login: "Open this page in a browser or on a device that does, or ask the person who runs this site for help.",
        enrol: "Copy this page's address and open it in a browser or on a device that does; the invitation still works there.",
        devices: "Manage your passkeys from a browser or a device that supports them.",
      }[kind] || "";
      show("This browser does not support passkeys. " + elsewhere + (page && page.provider ? " You can still sign in with " + page.provider + "." : ""), "error");
      const box = document.getElementById("choices");
      if (box) box.open = true;
    }
    return;
  }
  if (kind === "enrol") setupEnrol(button);
  else if (kind === "devices") setupDevices(button);
  else if (page.passkeys) setupLogin(button, page.returnTo);
});
