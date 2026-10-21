// Typed views of the loopback admin API. The console never decides access:
// verdicts always come from the server (auth.Explain).

export type RouteOverview = {
  host: string;
  protected: boolean;
  kind: string;
  policy: string;
  connector?: string;
  connection?: string;
};

export type RouteStatus = {
  access: string;
  policy: string;
  kind: string;
  is_static: boolean;
  is_tunnel: boolean;
  host: string;
  target: string;
  health_path: string;
  state: string;
  state_class: "healthy" | "unhealthy" | "unknown";
  latency: string;
  last_check: string;
  last_transition: string;
  last_error: string;
};

export type CertificateStatus = {
  host: string;
  mode: string;
  valid: boolean;
  not_after: string;
  last_outcome: string;
  last_error: string;
  last_attempt: string;
  next_attempt: string;
  pending: number;
};

export type StatusResponse = { generated_at: string; certificate?: CertificateStatus; routes: RouteStatus[] };

export type AccessEvent = {
  id?: string; // "<epoch>-<seq>", see $lib/feed
  time: string;
  type: string;
  actor?: string;
  host?: string;
  code?: string;
  reason?: string;
  error?: string;
  remote?: string;
};

export type Report = {
  route: string;
  protected: boolean;
  policy?: { allowed_emails?: string[]; allowed_domains?: string[]; required_groups?: string[] };
  principal?: { email: string; email_verified: boolean; groups: string[] };
  decision: { allowed: boolean; code: string; reason: string };
  scope: "authorization_only" | "gate_only" | "public_route";
};

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json() as Promise<T>;
}

export const fetchOverview = () => getJSON<{ routes: RouteOverview[] }>("/_/overview").then((r) => r.routes ?? []);
export const fetchStatus = () => getJSON<StatusResponse>("/_/api/status");

export class ExplainError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

// explain asks the engine. The token is passed per call and never stored.
export async function explain(token: string, host: string, principal: Report["principal"] | null): Promise<Report> {
  const res = await fetch("/_/api/policy/explain", {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ host, principal }),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new ExplainError("The admin token was rejected.", 401);
  if (!res.ok) throw new ExplainError(body.error ?? `Request failed (HTTP ${res.status}).`, res.status);
  return body as Report;
}

// Static-site deployments (PAGES-002).
export type Deployment = { site: string; id: string; created_at: string; files: number; bytes: number; live: boolean };
export type Site = { name: string; live: string; changed_at: string; deployments: Deployment[] };

export class PagesDisabledError extends Error {}

export async function fetchSites(): Promise<Site[]> {
  const res = await fetch("/_/api/sites", { cache: "no-store" });
  if (res.status === 404) throw new PagesDisabledError("Pages are not enabled on this edge.");
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return ((await res.json()) as { sites: Site[] }).sites ?? [];
}

// rollback makes a retained deployment live. Like explain, the token is
// passed per call and never stored.
export async function rollback(token: string, site: string, id: string): Promise<Deployment> {
  const res = await fetch(`/_/api/sites/${encodeURIComponent(site)}/rollback`, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ id }),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new Error("The admin token was rejected.");
  if (!res.ok) throw new Error(body.error ?? `Request failed (HTTP ${res.status}).`);
  return body as Deployment;
}

// People and passkeys (operator side of ID-003/ID-008).
export type PersonPasskey = { id: string; provider: string; synced: boolean; created: string; last_used?: string; revoked?: string };
export type Person = { email: string; name?: string; groups: string[]; created: string; disabled?: string; passkeys: PersonPasskey[] };
export type People = { enabled: boolean; users: Person[]; protected_hosts: string[] };
export type Invitation = { email: string; link: string; expires_at: string };

export async function fetchPeople(): Promise<People> {
  const res = await fetch("/_/api/people", { cache: "no-store" });
  if (res.status === 404) return { enabled: false, users: [], protected_hosts: [] };
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const body = (await res.json()) as People;
  return { enabled: body.enabled, users: body.users ?? [], protected_hosts: body.protected_hosts ?? [] };
}

async function adminPost<T>(token: string, url: string, payload?: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: payload === undefined ? undefined : JSON.stringify(payload),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new Error("The admin token was rejected.");
  if (!res.ok) throw new Error(body.error ?? `Request failed (HTTP ${res.status}).`);
  return body as T;
}

export const invitePerson = (token: string, invite: { email: string; name: string; groups: string[]; return_to: string; recover: boolean }) =>
  adminPost<Invitation>(token, "/_/api/people/invite", invite);
export const setDisabled = (token: string, email: string, disabled: boolean) =>
  adminPost(token, `/_/api/people/${encodeURIComponent(email)}/${disabled ? "disable" : "enable"}`);
export const revokePasskey = (token: string, email: string, id: string) =>
  adminPost(token, `/_/api/people/${encodeURIComponent(email)}/passkeys/${encodeURIComponent(id)}/revoke`);
