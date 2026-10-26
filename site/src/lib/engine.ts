// What the demo asks the engine and how it words the answer. The decision
// itself comes from engine.wasm (the edge's own code); nothing here decides
// access. Kept free of the DOM so it can be tested.

export type Access = {
  required_groups?: string[];
  allowed_domains?: string[];
  max_auth_age_minutes?: number;
  // Evaluated with the person's session: deny when the passkey is not bound.
  context_rule?: { when: { session_device_bound: boolean }; then: "deny" };
};
export type App = { host: string; blurb: string; access?: Access };

// Four routes that show the four ways a policy can say no (or ask again).
export const APPS: Record<string, App> = {
  grafana: { host: "grafana.acme.dev", blurb: "Engineering only", access: { required_groups: ["engineering"] } },
  billing: { host: "billing.acme.dev", blurb: "Finance, and only from a passkey that stays on one device", access: { required_groups: ["finance"], context_rule: { when: { session_device_bound: false }, then: "deny" } } },
  wiki: { host: "wiki.acme.dev", blurb: "Anyone at acme.dev who signed in recently", access: { allowed_domains: ["acme.dev"], max_auth_age_minutes: 15 } },
  blog: { host: "blog.acme.dev", blurb: "Public, no login" },
};

export const PEOPLE: Record<string, string> = { ana: "ana@acme.dev", bob: "bob@other.org" };

export type Visitor = { who: string; groups: string[]; verified: boolean; method: string; bound: boolean };

export function requestFor(app: string, visitor: Visitor) {
  const config = APPS[app];
  const route = { host: config.host, auth_required: Boolean(config.access), ...(config.access ? { access: config.access } : {}) };
  if (visitor.who === "none") return { route };
  const passkey = visitor.method === "passkey";
  return {
    route,
    principal: {
      email: PEOPLE[visitor.who],
      email_verified: visitor.verified,
      groups: visitor.groups,
      method: visitor.method,
      ...(passkey ? { device_bound: visitor.bound } : {}),
    },
  };
}

export type Report = {
  route: string;
  protected: boolean;
  decision: { allowed: boolean; step_up: boolean; code: string; reason: string };
  principal?: { email: string };
  scope: "authorization_only" | "gate_only" | "public_route";
};

// A policy with max_auth_age_minutes, methods or require_device_bound allows a
// person and writes what the session must also have into the reason; the
// proxy then sends them to log in again when the session falls short.
const hasCondition = (reason: string) => reason.startsWith("allowed; the session must also have");

export function describe(report: Report): { state: "allowed" | "refused" | "again"; title: string } {
  const d = report.decision;
  if (d.step_up) return { state: "again", title: "Asks to sign in again" };
  if (!d.allowed) return { state: "refused", title: "Refused" };
  return hasCondition(d.reason) ? { state: "again", title: "Let in, if the login qualifies" } : { state: "allowed", title: "Let in" };
}

export type Stage = { name: string; state: "ok" | "no" | "skip"; note: string };

// stagesFor places the engine's answer in the request pipeline. Only identify
// and authorize are decided by this engine; the rest of the pipeline exists on
// the edge and is not part of the demo, and the page says so.
export function stagesFor(report: Report): Stage[] {
  const d = report.decision;
  const notSimulated = (name: string): Stage => ({ name, state: "skip", note: "not simulated here" });
  const stages: Stage[] = [notSimulated("trust"), notSimulated("ratelimit")];
  if (report.scope === "public_route") {
    stages.push({ name: "identify", state: "skip", note: "not needed: the route is public" }, { name: "authorize", state: "ok", note: "no policy" });
  } else if (report.scope === "gate_only") {
    stages.push({ name: "identify", state: "no", note: "no session: sent to log in" }, { name: "authorize", state: "skip", note: "not reached" });
  } else {
    stages.push({ name: "identify", state: "ok", note: `signed in as ${report.principal?.email ?? "someone"}` });
    stages.push(d.allowed && !d.step_up
      ? { name: "authorize", state: "ok", note: hasCondition(d.reason) ? "allowed, if the session qualifies" : "the policy allows it" }
      : { name: "authorize", state: d.step_up ? "skip" : "no", note: d.step_up ? "allowed, but only after a new login" : d.code });
  }
  const goes = d.allowed && !d.step_up && report.scope !== "gate_only";
  stages.push({ name: "forward", state: goes ? "ok" : "skip", note: goes ? "the request would reach the app" : "not reached" });
  return stages;
}
