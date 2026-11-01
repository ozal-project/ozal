// The API client is shared by the Svelte reference app and the React cutover.
// Each composition root supplies its own in-memory session source.
export type APISession = { token: () => string; unauthorized: () => void };

let current: APISession = { token: () => "", unauthorized: () => {} };

export function configureAPISession(session: APISession) {
  current = session;
}

export function apiSession() {
  return current;
}

export async function sessionFetch(url: string, init: RequestInit = {}, signal?: AbortSignal, ms = 15_000) {
  const auth = current;
  const token = auth.token().trim();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  else headers.delete("Authorization");
  const timeout = AbortSignal.timeout(ms);
  const bounded = signal ? AbortSignal.any([signal, timeout]) : timeout;
  const response = await fetch(url, { ...init, cache: "no-store", headers, signal: bounded });
  if (response.status === 401 && token && auth.token().trim() === token) auth.unauthorized();
  return response;
}
