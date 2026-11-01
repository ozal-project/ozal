// Administrator passkey ceremony on the login host (ID-021). The console tab
// that sent the person here holds the verifier; this page only runs the
// ceremony and returns to the console with a one-use code.
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

async function post(path, body, ceremony) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(ceremony ? { "X-Ozal-Ceremony": ceremony } : {}) },
    credentials: "omit",
    body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || "This step could not be completed; start again from the console.");
  return data;
}

function show(message, kind) {
  const status = document.getElementById("status");
  status.textContent = message;
  status.dataset.kind = kind || "";
}

function explain(error, create) {
  if (error.name === "NotAllowedError") {
    return create
      ? "The passkey was not created: the prompt was closed or it timed out. Try again, or choose another place to keep it."
      : "The sign-in did not finish: the prompt was closed, it timed out, or no passkey here belongs to an administrator. Try again.";
  }
  if (error.name === "InvalidStateError") return "This place already has a passkey for this account.";
  if (typeof DOMException !== "undefined" && error instanceof DOMException) {
    // On this page the address is the edge's own sign-in host, so a refused
    // address is a setting to check on the host, not a link to open again.
    if (error.name === "SecurityError") return "This browser refused passkeys for this address. Ask the host to check that auth.passkeys.login_url names this sign-in address.";
    if (error.name === "NotSupportedError") return "This browser cannot use this kind of passkey. Update it, or use another browser, your phone or a security key.";
    return "This browser could not use a passkey here. Try again, or use another browser.";
  }
  return error.message || "Something went wrong.";
}

document.addEventListener("DOMContentLoaded", () => {
  const button = document.getElementById("action");
  const { tx, kind, hints } = document.body.dataset;
  const create = kind === "create";
  const steer = (hints || "").split(" ").filter(Boolean);
  const query = "?tx=" + encodeURIComponent(tx);
  if (create) {
    document.getElementById("title").textContent = "Create your console passkey";
    button.textContent = "Create a passkey";
  }
  if (!window.PublicKeyCredential || !navigator.credentials) {
    button.disabled = true;
    show("This browser does not support passkeys. Open the console in a browser that does.", "error");
    return;
  }
  button.addEventListener("click", async () => {
    button.disabled = true;
    show("Waiting for your device…");
    try {
      const begun = await post("/_/auth/admin/begin" + query, {});
      const pk = begun.options.publicKey;
      if (steer.length) pk.hints = steer;
      pk.challenge = b64url.decode(pk.challenge);
      let credential;
      if (create) {
        pk.user.id = b64url.decode(pk.user.id);
        for (const c of pk.excludeCredentials || []) c.id = b64url.decode(c.id);
        credential = await navigator.credentials.create({ publicKey: pk });
      } else {
        for (const c of pk.allowCredentials || []) c.id = b64url.decode(c.id);
        credential = await navigator.credentials.get({ publicKey: pk });
      }
      const result = await post("/_/auth/admin/finish" + query, credentialJSON(credential), begun.ceremony);
      show("Done. Returning to the console…", "ok");
      location.assign(result.redirect);
    } catch (error) {
      button.disabled = false;
      show(explain(error, create), "error");
    }
  });
});
