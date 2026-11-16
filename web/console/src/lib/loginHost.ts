// Administrator ceremonies on the HTTPS login host (ID-021). Many
// authenticators refuse http://ozal.localhost, so when the edge announces a
// login host the console sends the person there and gets its session back with
// a one-use code. The code is worthless without the verifier this tab keeps;
// the edge only ever sees its SHA-256.
import type { AdminSession } from "$lib/passkey";

const key = "ozal.loginHostVerifier";

const b64url = (bytes: Uint8Array) => {
  let bin = "";
  for (const byte of bytes) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

export function newVerifier(): string {
  return b64url(crypto.getRandomValues(new Uint8Array(32)));
}

export async function challengeFor(verifier: string): Promise<string> {
  return b64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier))));
}

export type LoginHostStart = { token?: string; name?: string; email?: string; hints?: string[] };

// startLoginHost asks this edge for a transaction and returns where to go. The
// verifier survives the round trip in this tab's session storage only.
export async function startLoginHost(request: LoginHostStart, store: Pick<Storage, "setItem"> = sessionStorage): Promise<string> {
  const verifier = newVerifier();
  const res = await fetch("/_/api/admin/remote/start", {
    method: "POST", cache: "no-store", signal: AbortSignal.timeout(15_000),
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...request, challenge: await challengeFor(verifier) }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error || "The sign-in could not start.");
  store.setItem(key, verifier);
  return (data as { url: string }).url;
}

// finishLoginHost trades the code the login host returned for a session. The
// verifier is used once, whatever the answer.
export async function finishLoginHost(code: string, store: Pick<Storage, "getItem" | "removeItem"> = sessionStorage): Promise<AdminSession> {
  const verifier = store.getItem(key) ?? "";
  store.removeItem(key);
  if (!verifier) throw new Error("This sign-in was started in another tab or window. Start again here.");
  const res = await fetch("/_/api/admin/remote/finish", {
    method: "POST", cache: "no-store", signal: AbortSignal.timeout(15_000),
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code, verifier }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error || "The sign-in could not be completed. Start again.");
  return data as AdminSession;
}

// Confirmation on the public RP is a full sign-in round trip. The original
// mutation is not replayed across navigation: retry it after returning with
// a freshly authenticated session. A loopback-only installation stays local.
export async function reauthenticateOnLoginHost(
  navigate: (url: string) => void = (url) => location.assign(url),
  store?: Pick<Storage, "setItem">,
): Promise<boolean> {
  const res = await fetch("/_/api/admin/state", { cache: "no-store", signal: AbortSignal.timeout(15_000) });
  if (!res.ok) throw new Error("The sign-in settings could not be read. Try again.");
  const state = await res.json() as { login_host?: string };
  if (!state.login_host) return false;
  navigate(await startLoginHost({}, store));
  return true;
}
