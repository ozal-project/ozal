// Passkey enrolment and login for Switchboard (ID-003). The server sends
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

function creationOptions(options) {
  const pk = options.publicKey;
  pk.challenge = b64url.decode(pk.challenge);
  pk.user.id = b64url.decode(pk.user.id);
  for (const c of pk.excludeCredentials || []) c.id = b64url.decode(c.id);
  return pk;
}

function requestOptions(options) {
  const pk = options.publicKey;
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
      : "The passkey was not created: the prompt was closed or it timed out. Try again.";
  }
  if (error.name === "InvalidStateError") return "This device already has a passkey for this account.";
  // Other browser errors carry technical text meant for developers; say what
  // the visitor can do instead. Server errors are plain Errors with a message
  // written for people, and are shown as they are.
  if (typeof DOMException !== "undefined" && error instanceof DOMException) {
    return error.name === "SecurityError"
      ? "This page cannot use passkeys here. Open the sign-in link again from the site you were visiting."
      : "This browser could not use a passkey here. Try again, or use another browser or device.";
  }
  return error.message || "Something went wrong.";
}

// A destination remembered from the login page, so an invitation opened in
// the same browser can return there. The server validates it again; this is
// a convenience, never a trust decision.
const pendingKey = "switchboard.returnTo";
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
function finish(result, message) {
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
    link("/_/auth/devices", "Manage your passkeys"),
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
    document.title = "Confirm it's you · Switchboard";
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

function setupLogin(button, returnTo) {
  let pending = null;

  async function login(conditional) {
    if (pending) pending.abort();
    const controller = new AbortController();
    pending = controller;
    const options = await post("/_/auth/login/begin", { return_to: returnTo, conditional });
    const request = { publicKey: requestOptions(options), signal: controller.signal };
    if (conditional) request.mediation = "conditional";
    const credential = await navigator.credentials.get(request);
    button.disabled = true;
    show("Checking…");
    const result = await post("/_/auth/login/finish", credentialJSON(credential));
    finish(result, "You are signed in.");
  }

  function fail(error) {
    button.disabled = false;
    if (error.name !== "AbortError") show(explain(error, "login"), "error");
  }

  button.addEventListener("click", () => login(false).catch(fail));
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
  button.addEventListener("click", async () => {
    button.disabled = true;
    show("Waiting for your device…");
    try {
      const options = await post("/_/auth/enrol/begin", { token, return_to: returnTo });
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options) });
      const result = await post("/_/auth/enrol/finish", credentialJSON(credential));
      finish(result, "Your passkey is saved and you are signed in.");
    } catch (error) {
      button.disabled = false;
      show(explain(error, "enrol"), "error");
    }
  });
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
      const title = el("span", "provider", key.provider);
      title.append(el("span", key.synced ? "badge" : "badge bound", key.synced ? "Synced" : "This device only"));
      const meta = el("span", "meta", "Added " + ago(key.created) + (key.last_used ? " · last used " + ago(key.last_used) : " · not used yet"));
      const remove = el("button", "", "Remove");
      remove.type = "button";
      remove.disabled = data.passkeys.length === 1;
      remove.title = remove.disabled ? "Your only passkey cannot be removed" : "";
      remove.setAttribute("aria-label", "Remove the " + key.provider + " passkey");
      remove.addEventListener("click", () => {
        if (confirm("Remove the " + key.provider + " passkey? You will be signed out everywhere.")) revoke(key.id).catch(fail);
      });
      item.append(title, remove, meta);
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
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options) });
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

document.addEventListener("DOMContentLoaded", () => {
  const button = document.getElementById("action");
  const kind = document.body.dataset.page;
  const page = kind === "login" ? setupLoginPage(button) : null;
  if (!window.PublicKeyCredential || !navigator.credentials) {
    button.disabled = true;
    if (!page || page.passkeys) {
      show("This browser does not support passkeys." + (page && page.provider ? " You can still sign in with " + page.provider + "." : ""), "error");
    }
    return;
  }
  if (kind === "enrol") setupEnrol(button);
  else if (kind === "devices") setupDevices(button);
  else if (page.passkeys) setupLogin(button, page.returnTo);
});
