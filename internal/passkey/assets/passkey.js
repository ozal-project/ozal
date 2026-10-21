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

function explain(error) {
  if (error.name === "NotAllowedError") return "The passkey prompt was closed or timed out. Try again.";
  if (error.name === "InvalidStateError") return "This device already has a passkey for this account.";
  return error.message || "Something went wrong.";
}

function setupLogin(button) {
  const returnTo = new URLSearchParams(location.search).get("return_to") || "";
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
    if (result.redirect) location.assign(result.redirect);
    else show("You are signed in.", "ok");
  }

  function fail(error) {
    button.disabled = false;
    if (error.name !== "AbortError") show(explain(error), "error");
  }

  button.addEventListener("click", () => login(false).catch(fail));
  if (PublicKeyCredential.isConditionalMediationAvailable) {
    PublicKeyCredential.isConditionalMediationAvailable().then((ok) => {
      if (ok) login(true).catch(fail);
    });
  }
}

function setupEnrol(button) {
  // The token arrives in the fragment, which browsers never send to a
  // server or a link preview. Take it out of the address bar and history.
  const token = location.hash.slice(1);
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
      const options = await post("/_/auth/enrol/begin", { token });
      const credential = await navigator.credentials.create({ publicKey: creationOptions(options) });
      await post("/_/auth/enrol/finish", credentialJSON(credential));
      show("Your passkey is saved and you are signed in. You can close this page.", "ok");
    } catch (error) {
      button.disabled = false;
      show(explain(error), "error");
    }
  });
}

document.addEventListener("DOMContentLoaded", () => {
  const button = document.getElementById("action");
  if (!window.PublicKeyCredential || !navigator.credentials) {
    button.disabled = true;
    show("This browser does not support passkeys.", "error");
    return;
  }
  if (document.body.dataset.page === "enrol") setupEnrol(button);
  else setupLogin(button);
});
