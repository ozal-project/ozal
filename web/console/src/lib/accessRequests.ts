// Access requests: a person with no account asks to become an administrator
// and a signed-in administrator decides. The requester's browser keeps a
// secret that only it can use to collect the invitation an approval makes.
export const pollEveryMs = 3000;

// RequestError keeps the edge's stable code, which tells a missing passkey
// proof from an ordinary refusal.
export class RequestError extends Error {
  readonly status: number;
  readonly code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export type AccessTicket = { id: string; secret: string; expires: string };
export type RequestOutcome =
  | { state: "pending" }
  | { state: "approved"; token: string }
  | { state: "denied" }
  | { state: "gone" };

export type PendingRequest = { id: string; name: string; email: string; browser?: string; created_at: string; expires_at: string };

// outcomeOf reads the status endpoint's answer. A 404 means the request
// expired, was answered already, or the secret was wrong; the page treats
// them alike, as the edge does.
export function outcomeOf(status: number, body: { state?: string; token?: string }): RequestOutcome {
  if (status === 404) return { state: "gone" };
  if (status >= 200 && status < 300) {
    if (body.state === "approved" && typeof body.token === "string" && body.token) return { state: "approved", token: body.token };
    if (body.state === "denied") return { state: "denied" };
    if (body.state === "pending") return { state: "pending" };
  }
  throw new Error("The console could not check your request. Try again.");
}

async function publicPost(path: string, body: unknown, signal?: AbortSignal): Promise<{ status: number; data: Record<string, unknown> }> {
  const res = await fetch(path, {
    method: "POST", cache: "no-store", signal: signal ?? AbortSignal.timeout(15_000),
    headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  return { status: res.status, data: await res.json().catch(() => ({})) };
}

export async function submitAccessRequest(name: string, email: string): Promise<AccessTicket> {
  const { status, data } = await publicPost("/_/api/admin/requests", { name, email });
  if (status !== 201) throw new Error(typeof data.error === "string" ? data.error : "The request could not be sent.");
  return data as unknown as AccessTicket;
}

export async function checkAccessRequest(ticket: AccessTicket, signal?: AbortSignal): Promise<RequestOutcome> {
  const { status, data } = await publicPost("/_/api/admin/requests/status", { id: ticket.id, secret: ticket.secret }, signal);
  return outcomeOf(status, data as { state?: string; token?: string });
}

async function adminCall<T>(method: string, token: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    method, cache: "no-store", signal: signal ?? AbortSignal.timeout(15_000),
    headers: { Authorization: `Bearer ${token.trim()}`, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new RequestError(typeof data?.error === "string" ? data.error : `HTTP ${res.status}`, res.status, typeof data?.code === "string" ? data.code : undefined);
  return data as T;
}

export const fetchAccessRequests = (token: string, signal?: AbortSignal) =>
  adminCall<{ enabled: boolean; requests: PendingRequest[] }>("GET", token, "/_/api/admins/requests", undefined, signal);
export const setAccessRequests = (token: string, enabled: boolean) =>
  adminCall<{ enabled: boolean }>("PUT", token, "/_/api/admins/requests/settings", { enabled });
export const denyAccessRequest = (token: string, id: string) =>
  adminCall<{ denied: boolean }>("POST", token, `/_/api/admins/requests/${encodeURIComponent(id)}/deny`);
export const approveAccessRequest = (token: string, id: string, tools: string[]) =>
  adminCall<{ approved: boolean; email: string; tools: string[] }>("POST", token, `/_/api/admins/requests/${encodeURIComponent(id)}/approve`, { tools });

// Approving needs a passkey proof from the last few minutes. Ask for one only
// when the edge says so, and retry exactly once on the same session.
export async function withPasskeyProof<T>(token: string, run: () => Promise<T>, confirm: (token: string) => Promise<void>): Promise<T> {
  try {
    return await run();
  } catch (error) {
    if (!(error instanceof Error) || !("code" in error) || error.code !== "passkey_required") throw error;
    await confirm(token.trim());
    return run();
  }
}
