// Console setup by code on the login host. The person types the code their
// server printed; the page shows whose request it is, creates the passkey or
// withdraws the code. The claim token stays in this page's memory only, and
// the page never reads a code from its own address.
"use strict";

const ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";

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
    response: {
      clientDataJSON: b64url.encode(r.clientDataJSON),
      attestationObject: b64url.encode(r.attestationObject),
    },
  };
  if (r.getTransports) out.response.transports = r.getTransports();
  if (credential.authenticatorAttachment) out.authenticatorAttachment = credential.authenticatorAttachment;
  return out;
}

const $ = (id) => document.getElementById(id);
let claim = "";
let timer = 0;
// What this code is for, once claimed: the ended screens name the right
// command to get a new one.
let request = { kind: "", email: "" };

async function post(path, body, ceremony) {
  const headers = { "Content-Type": "application/json" };
  if (claim) headers["X-Ozal-Claim"] = claim;
  if (ceremony) headers["X-Ozal-Ceremony"] = ceremony;
  const response = await fetch("/_/auth/admin/code" + path, {
    method: "POST",
    headers,
    credentials: "omit",
    body: JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(data.error || "This step could not be completed.");
    error.code = data.code || (response.status === 404 ? "gone" : "");
    throw error;
  }
  return data;
}

function show(message, kind) {
  const status = $("status");
  status.textContent = message || "";
  status.dataset.kind = kind || "";
}

function go(step) {
  for (const id of ["code", "check", "passkey", "done", "ended"]) $("step-" + id).hidden = id !== step;
  const order = ["code", "check", "passkey"];
  const at = order.indexOf(step);
  document.querySelectorAll(".steps li").forEach((li, i) => {
    li.classList.toggle("complete", at < 0 ? false : i < at || step === "done");
    if (i === at && step !== "done") li.setAttribute("aria-current", "step");
    else li.removeAttribute("aria-current");
  });
  show("");
  const heading = $("step-" + step).querySelector("h1");
  if (heading) {
    heading.tabIndex = -1;
    heading.focus({ preventScroll: true });
  }
}

// end shows a final state: the code cannot be used any more.
function end(title, text) {
  clearInterval(timer);
  if (request.kind === "recover") {
    $("ended-command").textContent = "sudo ozal console recover -email " + (request.email || "ADMINISTRATOR");
  }
  $("ended-title").textContent = title;
  $("ended-text").textContent = text;
  document.querySelector(".steps").hidden = true;
  go("ended");
}

const finalCodes = {
  code_taken: ["This code was already used", "A code works for one browser only."],
  code_expired: ["This code expired", "A code works for five minutes after the server prints it."],
  ended: ["This setup ended", "The code was cancelled or its time ran out."],
  claim_unknown: ["This setup ended", "The code was cancelled or its time ran out."],
  gone: ["No setup is waiting", "There is no code waiting on this server right now."],
};

function explainPasskey(error) {
  if (error.name === "NotAllowedError") return "The passkey was not created: the prompt was closed or timed out. Try again, or pick another place to keep it.";
  if (error.name === "InvalidStateError") return "This device already has a passkey for this account.";
  if (error.name === "SecurityError") return "This browser refused passkeys for this address. Check that auth.passkeys.login_url names this sign-in address.";
  if (error.name === "NotSupportedError") return "This browser cannot create this kind of passkey. Update it, or use your phone or a security key.";
  return error.message || "Something went wrong.";
}

// format keeps only symbols a code can contain and adds the dash.
function format(value) {
  const symbols = value.toUpperCase().split("").filter((c) => ALPHABET.includes(c)).join("").slice(0, 8);
  return symbols.length > 4 ? symbols.slice(0, 4) + "-" + symbols.slice(4) : symbols;
}

// place shows where an address is, after it in parentheses: the country's
// flag (served by this host, Radian UI SVGs) and its name in the reader's
// language, since few people recognise their own IP. The country comes from
// the server's local table; nothing is looked up elsewhere. Without the flag
// file only the name shows.
function place(country) {
  if (!/^[A-Z]{2}$/.test(country || "")) return null;
  let name = country;
  try {
    name = new Intl.DisplayNames([navigator.language, "en"], { type: "region" }).of(country) || country;
  } catch (_) {
    // An old browser without Intl.DisplayNames keeps the code.
  }
  const img = document.createElement("img");
  img.className = "flag";
  img.src = "/_/auth/admin/code/flags/" + country.toLowerCase() + ".svg";
  img.alt = "";
  img.width = 16;
  img.height = 16;
  img.addEventListener("error", () => img.remove());
  const span = document.createElement("span");
  span.className = "place";
  span.append("(", img, name, ")");
  $("country-credit").hidden = false;
  return span;
}

// credit names the source of the countries, as its licence asks.
function credit(text, url) {
  if (!text) return;
  const line = $("country-credit");
  line.append(" ");
  if (url && /^https:\/\//.test(url)) {
    const link = document.createElement("a");
    link.href = url;
    link.rel = "noreferrer noopener";
    link.target = "_blank";
    link.textContent = text;
    line.append(link);
  } else {
    line.append(text);
  }
}

// address fills a row with an IP, its country when known and an optional
// tag that qualifies it.
function address(id, ip, tag, match, country) {
  const cell = $(id);
  cell.textContent = "";
  cell.append(ip);
  const where = place(country);
  if (where) cell.append(" ", where);
  if (tag) {
    const label = document.createElement("span");
    label.className = "tag" + (match ? " match" : "");
    label.textContent = tag;
    cell.append(label);
  }
}

// fieldProblem shows what is wrong with a field right under it; the email's
// problem takes the place of its hint.
function fieldProblem(id, message) {
  const input = $(id);
  input.setAttribute("aria-invalid", message ? "true" : "false");
  if (id === "name") {
    $("name-error").textContent = message;
    $("name-error").hidden = !message;
    return;
  }
  const help = $("email-help");
  if (!help.dataset.hint) help.dataset.hint = help.textContent;
  help.textContent = message || help.dataset.hint;
  help.classList.toggle("error", Boolean(message));
}

// fieldError puts a code problem right under the field it is about.
function fieldError(message) {
  const help = $("code-help");
  if (!help.dataset.hint) help.dataset.hint = help.textContent;
  help.textContent = message || help.dataset.hint;
  help.classList.toggle("error", Boolean(message));
}

function countdown(seconds) {
  const outs = [$("req-countdown"), $("passkey-countdown")];
  const deadline = Date.now() + seconds * 1000;
  const tick = () => {
    const left = Math.max(0, Math.round((deadline - Date.now()) / 1000));
    for (const out of outs) {
      out.textContent = Math.floor(left / 60) + ":" + String(left % 60).padStart(2, "0");
      out.classList.toggle("low", left < 60);
    }
    if (left === 0) end("Time ran out", "The setup has to finish within fifteen minutes of the code being printed.");
  };
  tick();
  timer = setInterval(tick, 1000);
}

document.addEventListener("DOMContentLoaded", () => {
  const input = $("code");
  const submit = $("code-submit");

  if (!window.PublicKeyCredential || !navigator.credentials) {
    input.disabled = true;
    show("This browser does not support passkeys. Open this page in a current browser, on your phone, or use a security key.", "error");
    return;
  }

  input.addEventListener("input", () => {
    const formatted = format(input.value);
    if (formatted !== input.value) input.value = formatted;
    input.removeAttribute("aria-invalid");
    fieldError("");
    const complete = formatted.replace("-", "").length === 8;
    submit.disabled = !complete;
    if (complete && input.dataset.auto !== formatted) {
      input.dataset.auto = formatted; // submit once per complete code
      $("code-form").requestSubmit();
    }
  });
  input.focus();

  $("code-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (submit.disabled && input.value.replace("-", "").length !== 8) return;
    submit.disabled = true;
    input.disabled = true;
    show("Checking the code…");
    try {
      const claimed = await post("/claim", { code: input.value });
      claim = claimed.claim;
      request = { kind: claimed.kind, email: claimed.email || "" };
      $("req-server").textContent = claimed.server || claimed.host || location.host;
      const at = new Date(claimed.issued);
      const time = isNaN(at) ? "" : " · " + at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
      $("req-operator").textContent = (claimed.operator || "someone") + time;
      if (claimed.from) {
        address("req-from", claimed.from, claimed.from_local ? "local network" : "SSH", false, claimed.from_country);
      } else {
        $("req-from").textContent = "A terminal on the server";
      }
      address("req-browser", claimed.browser || "unknown", claimed.same_network ? "same network" : "", claimed.same_network, claimed.browser_country);
      $("req-grants").textContent = claimed.kind === "recover"
        ? "New passkey for " + (claimed.email || "this administrator")
        : "Full access to the console";
      $("req-code").textContent = input.value;
      credit(claimed.country_credit, claimed.country_credit_url);
      if (claimed.kind === "recover") {
        $("check-intro").textContent = "This replaces every passkey of " + (claimed.email || "this administrator") + ". Continue only if you ran the command on that server just now.";
        $("passkey-title").textContent = "Create your new passkey";
        $("who-fields").hidden = true;
      } else {
        $("name").required = true;
        $("email").required = true;
      }
      countdown(claimed.expires_in || 600);
      go("check");
    } catch (error) {
      input.disabled = false;
      if (finalCodes[error.code] && error.code !== "code_expired" && error.code !== "code_taken") {
        const [title, text] = finalCodes[error.code];
        end(title, text);
        return;
      }
      input.setAttribute("aria-invalid", "true");
      submit.disabled = false;
      show("");
      fieldError(error.message);
      input.focus();
    }
  });

  $("confirm").addEventListener("click", () => {
    go("passkey");
    if (!$("who-fields").hidden) $("name").focus();
  });
  $("back").addEventListener("click", () => go("check"));
  for (const id of ["name", "email"]) $(id).addEventListener("input", () => fieldProblem(id, ""));

  $("who-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = $("name");
    const email = $("email");
    if (!$("who-fields").hidden) {
      fieldProblem("name", name.value.trim() ? "" : "Add your full name.");
      fieldProblem("email", email.value.trim() && email.checkValidity() ? "" : "Add a valid email address.");
      const wrong = [name, email].find((input) => input.getAttribute("aria-invalid") === "true");
      if (wrong) return wrong.focus();
    }
    const create = $("create");
    create.disabled = true;
    $("back").disabled = true;
    show("Follow your device's prompt to save the passkey…");
    try {
      const begun = await post("/begin", { name: name.value.trim(), email: email.value.trim() });
      const pk = begun.options.publicKey;
      pk.challenge = b64url.decode(pk.challenge);
      pk.user.id = b64url.decode(pk.user.id);
      for (const c of pk.excludeCredentials || []) c.id = b64url.decode(c.id);
      const credential = await navigator.credentials.create({ publicKey: pk });
      show("Saving…");
      const done = await post("/finish", credentialJSON(credential), begun.ceremony);
      clearInterval(timer);
      $("done-who").textContent = done.name + " · " + done.email;
      const port = new URL(done.console).port || "80";
      $("done-tunnel").textContent = "ssh -N -L " + port + ":127.0.0.1:" + port + " you@your-server";
      const link = $("done-console");
      link.textContent = done.console;
      link.href = done.console + "/overview";
      go("done");
      show("");
    } catch (error) {
      create.disabled = false;
      $("back").disabled = false;
      if (finalCodes[error.code]) {
        const [title, text] = finalCodes[error.code];
        end(title, text);
        return;
      }
      show(explainPasskey(error), "error");
    }
  });

  // The code counts as cancelled only when the server says so. If the passkey
  // is already being saved it can no longer be cancelled; if the request
  // fails, the person can try again.
  $("withdraw").addEventListener("click", async () => {
    const button = $("withdraw");
    button.disabled = true;
    show("Cancelling the code…");
    try {
      await post("/withdraw", {});
    } catch (error) {
      button.disabled = false;
      if (error.code === "saving") {
        show("The passkey is already being saved, so the code can no longer be cancelled. If this was not you, recover the account from the server.", "error");
        return;
      }
      if (error.code === "ended" || error.code === "claim_unknown" || error.code === "gone") {
        end("This setup already ended", "Nobody can use this code now.");
        return;
      }
      show("The code could not be cancelled: " + (error.message || "no answer from the server") + ". Try again.", "error");
      return;
    }
    end("Code cancelled", "Nobody can use this code now. If you did not run the command, tell whoever manages the server.");
  });

  $("done-tunnel").addEventListener("click", () => {
    navigator.clipboard?.writeText($("done-tunnel").textContent).then(() => show("Copied.", "ok"), () => {});
  });
});
