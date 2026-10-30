import { admin, endSession } from "$lib/adminToken.svelte";

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
  names?: string[];
  mode: string;
  valid: boolean;
  not_after: string;
  last_outcome: string;
  last_error: string;
  last_attempt: string;
  next_attempt: string;
  pending: number;
};

export type AuditStatus = { available: boolean; failure_count: number; last_failure_at?: string };
export type StatusResponse = { generated_at: string; certificate?: CertificateStatus; audit?: AuditStatus; routes: RouteStatus[] };

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
  context?: PolicyContext;
  deployment?: { project: string; repository_id?: number; ref?: string; commit_sha?: string };
};

export type PolicySignal<T> = { value: T | null; source: string; observed_at: string };
export type PolicyContext = {
  version: number;
  session: {
    method: PolicySignal<string>;
    auth_time: PolicySignal<string>;
    user_verified: PolicySignal<boolean>;
    device_bound: PolicySignal<boolean>;
  };
  network: {
    arrival_path: PolicySignal<string>;
    visitor_ip_source: PolicySignal<string>;
  };
  resource: {
    route: PolicySignal<string>;
    owner: PolicySignal<string>;
    data_class: PolicySignal<string>;
    sensitivity: PolicySignal<string>;
  };
  action: {
    http_method: PolicySignal<string>;
    path_class: PolicySignal<string>;
    risky: PolicySignal<boolean>;
  };
  history: { device_first_seen: PolicySignal<boolean> };
};

export type Report = {
  route: string;
  protected: boolean;
  policy?: {
    allowed_emails?: string[];
    allowed_domains?: string[];
    required_groups?: string[];
    context_rule?: {
      when: {
        session_method?: "oidc" | "passkey";
        session_device_bound?: boolean;
        resource_sensitivity?: "public" | "internal" | "confidential" | "restricted";
        action_risky?: boolean;
        device_first_seen?: boolean;
      };
      then: "deny" | "step_up";
    };
  };
  principal?: { email: string; email_verified: boolean; groups: string[]; method?: string; device_bound?: boolean; user_verified?: boolean; device_first_seen?: boolean };
  decision: { allowed: boolean; step_up: boolean; code: string; reason: string };
  context: PolicyContext;
  scope: "authorization_only" | "gate_only" | "public_route";
};

const boundedSignal = (signal?: AbortSignal, ms = 15_000) => {
  const timeout = AbortSignal.timeout(ms);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
};

// authedGet sends this tab's credential. Reads of the edge's state need one,
// even on loopback; a 401 means it was revoked or the host changed the token.
async function authedGet(url: string, signal?: AbortSignal): Promise<Response> {
  const token = admin.token.trim();
  const res = await fetch(url, { cache: "no-store", headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: boundedSignal(signal) });
  if (res.status === 401 && token && admin.token.trim() === token) endSession();
  return res;
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await authedGet(url, signal);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json() as Promise<T>;
}

export const fetchOverview = (signal?: AbortSignal) => getJSON<{ routes: RouteOverview[] }>("/_/overview", signal).then((r) => r.routes ?? []);
export const fetchStatus = (signal?: AbortSignal) => getJSON<StatusResponse>("/_/api/status", signal);

export type UpdateNodeStatus = { role: "edge" | "connector"; name: string; installed?: string; latest?: string; sha256?: string; result: string };
export type UpdateStatus = { checked_at?: string; installed?: string; latest?: string; result: string; reason?: string; job_id?: string; phase?: string; nodes?: UpdateNodeStatus[] };
export const fetchUpdateStatus = (signal?: AbortSignal) => getJSON<UpdateStatus>("/_/api/updates/status", signal);

export class ExplainError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

// explain asks the engine. The token is passed per call and never stored.
export async function explain(token: string, host: string, principal: Report["principal"] | null, requestMethod: string, signal?: AbortSignal): Promise<Report> {
  const res = await fetch("/_/api/policy/explain", {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ host, principal, ...(requestMethod ? { request_method: requestMethod } : {}) }),
    signal: boundedSignal(signal),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new ExplainError("The admin token was rejected.", 401);
  if (!res.ok) throw new ExplainError(body.error ?? `Request failed (HTTP ${res.status}).`, res.status);
  return body as Report;
}

// Static-site deployments (PAGES-002).
export type Deployment = { site: string; id: string; created_at: string; files: number; bytes: number; live: boolean };
export type Site = { name: string; live: string; changed_at: string; deployments: Deployment[] };
export type ProjectDeployer =
  | { type: "github"; github: { repository_id: number; repository_owner_id: number; ref: string; workflow_ref?: string } }
  | { type: "token"; token_configured: boolean };
export type Project = { name: string; site: string; hostnames: string[]; deployers: ProjectDeployer[] };

export type PublishingState = { projects: Project[]; sites: string[]; config_sha256: string };
export async function fetchPublishing(token: string, signal?: AbortSignal): Promise<PublishingState> {
  const res = await fetch("/_/api/projects", {
    cache: "no-store",
    headers: { Authorization: `Bearer ${token}` },
    signal: boundedSignal(signal),
  });
  if (res.status === 401) throw new Error("The admin token was rejected.");
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}
export const fetchProjects = async (token: string, signal?: AbortSignal): Promise<Project[]> => (await fetchPublishing(token, signal)).projects ?? [];

export class PagesDisabledError extends Error {}

export async function fetchSites(signal?: AbortSignal): Promise<Site[]> {
  const res = await authedGet("/_/api/sites", signal);
  if (res.status === 404) throw new PagesDisabledError("Pages are not enabled on this edge.");
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return ((await res.json()) as { sites: Site[] }).sites ?? [];
}

// rollback makes a retained deployment live. Like explain, the token is
// passed per call and never stored.
export async function rollback(token: string, site: string, id: string, signal?: AbortSignal): Promise<Deployment> {
  const res = await fetch(`/_/api/sites/${encodeURIComponent(site)}/rollback`, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ id }),
    signal: boundedSignal(signal),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new Error("The admin token was rejected.");
  if (!res.ok) throw new Error(body.error ?? `Request failed (HTTP ${res.status}).`);
  return body as Deployment;
}

// People and passkeys (operator side of ID-003/ID-008).
export type PersonPasskey = { id: string; provider: string; synced: boolean; created: string; last_used?: string; revoked?: string };
export type Person = { email: string; subject?: string; name?: string; groups: string[]; created: string; disabled?: string; passkeys: PersonPasskey[] };
export type People = { enabled: boolean; users: Person[]; protected_hosts: string[] };
export type Invitation = { email: string; link: string; expires_at: string };

export async function fetchPeople(signal?: AbortSignal): Promise<People> {
  const res = await authedGet("/_/api/people", signal);
  if (res.status === 404) return { enabled: false, users: [], protected_hosts: [] };
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const body = (await res.json()) as People;
  return { enabled: body.enabled, users: body.users ?? [], protected_hosts: body.protected_hosts ?? [] };
}

async function adminPost<T>(token: string, url: string, payload?: unknown, signal?: AbortSignal, timeoutMs?: number): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    cache: "no-store",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: payload === undefined ? undefined : JSON.stringify(payload),
    signal: boundedSignal(signal, timeoutMs),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) throw new Error("The admin token was rejected.");
  if (!res.ok) throw new Error(body.error ?? `Request failed (HTTP ${res.status}).`);
  return body as T;
}

export const invitePerson = (token: string, invite: { email: string; name: string; groups: string[]; return_to: string }, signal?: AbortSignal) =>
  adminPost<Invitation>(token, "/_/api/people/invite", invite, signal);
export const setDisabled = (token: string, email: string, disabled: boolean, signal?: AbortSignal) =>
  adminPost(token, `/_/api/people/${encodeURIComponent(email)}/${disabled ? "disable" : "enable"}`, undefined, signal);
export const revokePasskey = (token: string, email: string, id: string, signal?: AbortSignal) =>
  adminPost(token, `/_/api/people/${encodeURIComponent(email)}/passkeys/${encodeURIComponent(id)}/revoke`, undefined, signal);

export type Group = { name: string; source: "local"; members: string[]; hosts: string[] };
export async function fetchGroups(token: string, signal?: AbortSignal): Promise<Group[]> {
  const res = await fetch("/_/api/groups", { cache: "no-store", headers: { Authorization: `Bearer ${token}` }, signal: boundedSignal(signal) });
  if (!res.ok) throw new Error(`Groups could not be read (HTTP ${res.status}).`);
  return ((await res.json()) as { groups: Group[] }).groups;
}
export async function setGroups(token: string, email: string, expected: string[], groups: string[], signal?: AbortSignal): Promise<void> {
  const res = await fetch(`/_/api/people/${encodeURIComponent(email)}/groups`, {
    method: "PUT", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ expected, groups }), signal: boundedSignal(signal),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `Membership change failed (HTTP ${res.status}).`);
}

export type ApplicationRoute = { host: string; target?: string; upstreams?: string[]; connector?: string; site?: string; root?: string; spa?: boolean; health_path?: string; auth_required?: boolean; access?: Record<string, unknown>; resource?: Record<string, unknown>; audit_mode?: string };
export type SetupStatus = { config_sha256: string; methods: string[]; pages_enabled: boolean; tls_enabled: boolean; tls_mode: "off" | "automatic" | "dns01"; dns: { target: string; proxied: boolean; provider: "" | "cloudflare" }; login_url: string; admin_socket_configured: boolean; routes: ApplicationRoute[] };
export type ApplicationDraft = { route: ApplicationRoute; expected: string; access_mode: "private" | "public" };
export type ApplicationPlan = ApplicationDraft & { url: string; dns: string; tls: string; checks: string[] };
export async function setupStatus(token: string, signal?: AbortSignal): Promise<SetupStatus> {
  const res = await fetch("/_/api/setup", { cache: "no-store", headers: { Authorization: `Bearer ${token}` }, signal: boundedSignal(signal) });
  if (!res.ok) throw new Error(`Sign in as admin to read setup (HTTP ${res.status}).`);
  return res.json();
}
export const planApplication = (token: string, draft: ApplicationDraft, signal?: AbortSignal) => adminPost<ApplicationPlan>(token, "/_/api/apps/plan", draft, signal);
export const saveApplication = (token: string, draft: ApplicationDraft, signal?: AbortSignal) => adminPost(token, "/_/api/apps", draft, signal);

// UI-006: is a hostname connected to this edge, and which record does it need?
export type CheckState = "ok" | "wait" | "bad";
export type DNSRecord = { type: string; name: string; content: string; proxied: boolean };
export type Connection = { host: string; connected: boolean; dns: { state: CheckState; detail: string }; certificate: { state: CheckState; detail: string }; reach: { state: CheckState; detail: string }; record?: DNSRecord; provider?: "cloudflare"; addresses: string[] };
export async function checkConnection(token: string, host: string, signal?: AbortSignal): Promise<Connection> {
  // A check resolves DNS and makes one public request: allow it longer than an API call.
  const limit = AbortSignal.timeout(30_000);
  const res = await fetch(`/_/api/apps/${encodeURIComponent(host)}/check`, { method: "POST", cache: "no-store", headers: { Authorization: `Bearer ${token}` }, signal: signal ? AbortSignal.any([signal, limit]) : limit });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `The connection could not be checked (HTTP ${res.status}).`);
  return body as Connection;
}
export const createRecord = (token: string, host: string, signal?: AbortSignal) => adminPost<DNSRecord>(token, `/_/api/apps/${encodeURIComponent(host)}/dns`, undefined, signal);
export async function deploySite(token: string, site: string, file: File): Promise<{ id: string }> {
  const res = await fetch(`/_/api/sites/${encodeURIComponent(site)}/deployments`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/gzip" }, body: file, signal: AbortSignal.timeout(600_000) });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `Publish failed (HTTP ${res.status}).`);
  return body;
}

export type Repository = { id: number; owner: { id: number }; full_name: string; default_branch: string; private: boolean };
export type Repositories = { configured: boolean; repositories: Repository[]; page?: number; more?: boolean };
export async function githubRepositories(token: string, page = 1, signal?: AbortSignal): Promise<Repositories> {
  const res = await fetch(`/_/api/github/repositories?page=${page}`, { cache: "no-store", headers: { Authorization: `Bearer ${token}` }, signal: boundedSignal(signal) });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `GitHub unavailable (HTTP ${res.status}).`);
  return body;
}
export const bindGitHubProject = (token: string, project: { name: string; site: string; repository_id: number; page: number; ref: string; workflow: string; expected: string }) => adminPost<{ workflow_ref: string; deploy_host_configured: boolean }>(token, "/_/api/projects", project);
export type WorkflowInput = { directory: string; output: string; hostname: string; page: number };
export type WorkflowPlan = { project: string; repository: string; ref: string; filename: string; endpoint: string; site_url: string; directory: string; output: string; base: string; branch: string; path: string; content: string; digest: string };
export type WorkflowPreview = { applied: false; plan: WorkflowPlan; expected: string };
export type WorkflowResult = { applied: true; pull_request: { url: string; number: number; branch: string } };
export async function proposeWorkflow(token: string, project: string, input: WorkflowInput & { expected?: string; digest?: string; apply?: boolean }, signal: AbortSignal): Promise<WorkflowPreview | WorkflowResult> {
  const res = await fetch(`/_/api/projects/${encodeURIComponent(project)}/workflow`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify(input), signal });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(`${body.error ?? `Workflow proposal failed (HTTP ${res.status}).`}${body.branch ? ` Proposal branch: ${body.branch}.` : ""}`);
  return body;
}
export async function disconnectProject(token: string, name: string, expected: string): Promise<void> {
  const res = await fetch(`/_/api/projects/${encodeURIComponent(name)}`, { method: "DELETE", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ expected }), signal: boundedSignal() });
  if (!res.ok) throw new Error("Project disconnect could not be confirmed. Refresh and reconcile before retrying.");
}

export type Connector = { name: string; sha256: string; created: string; revoked?: string; not_after?: string; hosts: string[]; connected: boolean };
export async function fetchConnectors(token: string, signal?: AbortSignal): Promise<Connector[]> {
  const res = await fetch("/_/api/connectors", { cache: "no-store", headers: { Authorization: `Bearer ${token}` }, signal: boundedSignal(signal) });
  if (res.status === 404) return [];
  if (!res.ok) throw new Error(`Connector registry could not be read (HTTP ${res.status}).`);
  return ((await res.json()) as { connectors: Connector[] }).connectors;
}

export type AdminAccount = { id: string; name: string; email: string; tools: string[]; disabled: boolean; passkeys: number; created_at: string; last_used_at?: string; you?: boolean };
export async function fetchAdmins(signal?: AbortSignal): Promise<AdminAccount[]> {
  const res = await authedGet("/_/api/admins", signal);
  if (res.status === 404) return [];
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return ((await res.json()) as { admins: AdminAccount[] }).admins ?? [];
}
export const inviteAdmin = (token: string, email: string, tools: string[], name?: string, signal?: AbortSignal) =>
  adminPost<{ token: string; expires: string; email: string; tools: string[] }>(token, "/_/api/admins/invite", { email, tools, name }, signal);
export const setAdminDisabled = (token: string, id: string, disabled: boolean, signal?: AbortSignal) =>
  adminPost(token, `/_/api/admins/${encodeURIComponent(id)}/${disabled ? "disable" : "enable"}`, undefined, signal);
export const recoverAdmin = (token: string, id: string, signal?: AbortSignal) =>
  adminPost<{ token: string; expires: string; email: string }>(token, `/_/api/admins/${encodeURIComponent(id)}/recover`, undefined, signal);

// EDGE-008: domains (zones), each with its own certificate.
export type ZoneState = "pending" | "queued" | "issuing" | "active" | "renewing" | "renewal_failed" | "expiring" | "failed";
export type Zone = {
  name: string;
  source: "config" | "runtime";
  state: ZoneState;
  names: string[];
  not_after?: string;
  renews_at?: string;
  last_attempt?: string;
  next_attempt?: string;
  last_error?: string;
  challenge: { name: string; target: string; delegated: boolean };
  hosts: string[];
};
export type Zones = { config_sha256: string; available: boolean; setup?: boolean; provider?: string; zones: Zone[] };
export type ZoneCheck = { zone: string; state?: ZoneState; challenge: { state: CheckState; detail: string; record?: DNSRecord } };
export const fetchZones = (signal?: AbortSignal) => getJSON<Zones>("/_/api/zones", signal);
// EDGE-009: what a domain still needs at its DNS provider and proxy.
export type SetupStepState = "done" | "todo" | "blocked" | "unknown" | "not_needed";
export type SetupStep = { name: "challenge" | "encryption" | "origin_pull"; state: SetupStepState; action?: string; title: string; detail: string; manual?: string };
export type ZoneSetup = { zone: string; provider?: string; ready: boolean; steps: SetupStep[]; error?: string; applied?: string[] };
export const fetchZoneSetup = (name: string, signal?: AbortSignal) => getJSON<ZoneSetup>(`/_/api/zones/${encodeURIComponent(name)}/setup`, signal);
export const applyZoneSetup = (token: string, name: string) => adminPost<ZoneSetup>(token, `/_/api/zones/${encodeURIComponent(name)}/setup`, undefined, undefined, 75_000);
export const addZone = (token: string, name: string, expected: string) => adminPost<{ config_sha256: string }>(token, "/_/api/zones", { name, expected });
export const checkZone = (token: string, name: string) => adminPost<ZoneCheck>(token, `/_/api/zones/${encodeURIComponent(name)}/check`);
export const renewZone = (token: string, name: string) => adminPost<{ status: string }>(token, `/_/api/zones/${encodeURIComponent(name)}/renew`);
export async function removeZone(token: string, name: string, expected: string): Promise<void> {
  const res = await fetch(`/_/api/zones/${encodeURIComponent(name)}`, { method: "DELETE", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ expected }), signal: boundedSignal() });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `The domain could not be removed (HTTP ${res.status}).`);
}
