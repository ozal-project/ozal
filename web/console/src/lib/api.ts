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
