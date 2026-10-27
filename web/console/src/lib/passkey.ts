// Administrator passkeys. The edge sends WebAuthn options with binary fields
// as base64url; the browser wants ArrayBuffers and the edge wants base64url
// back. The ceremony ID travels in a header, never in a cookie or URL.
export type AdminSession = { token: string; name: string; expires: string; tools: string[] };

const decode = (text: string): ArrayBuffer => {
  let s = text.replace(/-/g, "+").replace(/_/g, "/");
  while (s.length % 4) s += "=";
  const bin = atob(s);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes.buffer;
};
const encode = (buffer: ArrayBuffer): string => {
  let bin = "";
  for (const byte of new Uint8Array(buffer)) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

async function api<T>(path: string, body: unknown, ceremony?: string, bearer?: string): Promise<T> {
  const res = await fetch(path, {
    method: "POST", cache: "no-store", signal: AbortSignal.timeout(300_000),
    headers: { "Content-Type": "application/json", ...(ceremony ? { "X-Switchboard-Ceremony": ceremony } : {}), ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}) },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error || "The request failed.");
  return data as T;
}

function credentialJSON(credential: PublicKeyCredential) {
  const r = credential.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse;
  const out: Record<string, unknown> = {
    id: credential.id, rawId: encode(credential.rawId), type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: { clientDataJSON: encode(r.clientDataJSON) },
  };
  const response = out.response as Record<string, unknown>;
  if (r.attestationObject) {
    response.attestationObject = encode(r.attestationObject);
    if (r.getTransports) response.transports = r.getTransports();
  } else {
    response.authenticatorData = encode(r.authenticatorData);
    response.signature = encode(r.signature);
    if (r.userHandle) response.userHandle = encode(r.userHandle);
  }
  return out;
}

// The browser reports a closed prompt, a timeout and "no passkey here" alike.
export function explainPasskeyError(e: unknown): string {
  const error = e as Error;
  if (error?.name === "NotAllowedError") return "The passkey prompt was closed, timed out, or this device has no passkey for this console.";
  if (error?.name === "InvalidStateError") return "This device already has a passkey for this account.";
  if (typeof DOMException !== "undefined" && e instanceof DOMException) return "This browser could not use a passkey here. Open the console as http://localhost and try again.";
  return error?.message || "Something went wrong.";
}

type Begun = { ceremony: string; options: { publicKey: Record<string, any> } };

export async function signInWithPasskey(): Promise<AdminSession> {
  const begun = await api<Begun>("/_/api/admin/login/begin", {});
  const pk = begun.options.publicKey;
  pk.challenge = decode(pk.challenge);
  for (const c of pk.allowCredentials ?? []) c.id = decode(c.id);
  const credential = (await navigator.credentials.get({ publicKey: pk as PublicKeyCredentialRequestOptions })) as PublicKeyCredential | null;
  if (!credential) throw new Error("No passkey was chosen.");
  return api<AdminSession>("/_/api/admin/login/finish", credentialJSON(credential), begun.ceremony);
}

export async function enrolWithPasskey(token: string, name: string, email: string): Promise<AdminSession> {
  const begun = await api<Begun>("/_/api/admin/enrol/begin", { token, name, email });
  const pk = begun.options.publicKey;
  pk.challenge = decode(pk.challenge);
  pk.user.id = decode(pk.user.id);
  for (const c of pk.excludeCredentials ?? []) c.id = decode(c.id);
  const credential = (await navigator.credentials.create({ publicKey: pk as PublicKeyCredentialCreationOptions })) as PublicKeyCredential | null;
  if (!credential) throw new Error("No passkey was created.");
  return api<AdminSession>("/_/api/admin/enrol/finish", credentialJSON(credential), begun.ceremony);
}

export async function adminState(signal?: AbortSignal): Promise<{ enrolled: boolean; enrol_command?: string } | null> {
  const res = await fetch("/_/api/admin/state", { cache: "no-store", signal });
  if (!res.ok) return null;
  return res.json();
}

// Adds another passkey to the signed-in administrator, so losing one device
// does not lock them out.
export async function addPasskey(session: string): Promise<void> {
  const begun = await api<Begun>("/_/api/admin/passkeys/begin", {}, undefined, session);
  const pk = begun.options.publicKey;
  pk.challenge = decode(pk.challenge);
  pk.user.id = decode(pk.user.id);
  for (const c of pk.excludeCredentials ?? []) c.id = decode(c.id);
  const credential = (await navigator.credentials.create({ publicKey: pk as PublicKeyCredentialCreationOptions })) as PublicKeyCredential | null;
  if (!credential) throw new Error("No passkey was created.");
  await api<{ added: boolean }>("/_/api/admin/passkeys/finish", credentialJSON(credential), begun.ceremony, session);
}

export type EnrolInfo = { kind: "bootstrap" | "invite" | "recover"; email?: string; name?: string };
// What the link already fixes, so the form asks only for what is missing.
export async function enrolInfo(token: string): Promise<EnrolInfo> {
  return api<EnrolInfo>("/_/api/admin/enrol/info", { token });
}
