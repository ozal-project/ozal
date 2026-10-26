// What the demo asks the engine and how it words the answer. The decision
// itself comes from engine.wasm (the edge's own code); nothing here decides
// access. Kept free of the DOM so it can be tested.

export type Access = {
  allowed_domains?: string[];
  allowed_emails?: string[];
  max_auth_age_minutes?: number;
  // Evaluated with the person's session: deny when the passkey is not bound.
  context_rule?: { when: { session_device_bound: boolean }; then: "deny" };
};
export type App = { host: string; blurb: string; access?: Access };

// Four routes that show four ways a policy answers: a domain, one person with
// a condition on the device, a domain with a recent login, and nobody to ask.
export const APPS: Record<string, App> = {
  grafana: { host: "grafana.acme.dev", blurb: "Only people at acme.dev", access: { allowed_domains: ["acme.dev"] } },
  billing: { host: "billing.acme.dev", blurb: "Only Ana, and only from a passkey that stays on one device", access: { allowed_emails: ["ana@acme.dev"], context_rule: { when: { session_device_bound: false }, then: "deny" } } },
  wiki: { host: "wiki.acme.dev", blurb: "People at acme.dev who signed in within 15 minutes", access: { allowed_domains: ["acme.dev"], max_auth_age_minutes: 15 } },
  blog: { host: "blog.acme.dev", blurb: "Public, no login" },
};

export const PEOPLE: Record<string, string> = { ana: "ana@acme.dev", carla: "carla@acme.dev", bob: "bob@other.org" };

export type Visitor = { who: string; verified: boolean; method: string; bound: boolean };

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
      groups: [],
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

// plainDecision says what happened in a sentence a visitor to the page can
// follow. The engine's own words stay one click away, in its full answer.
export function plainDecision(report: Report, app: string, visitor: Visitor): string {
  const { host, access } = APPS[app];
  const email = PEOPLE[visitor.who];
  const d = report.decision;
  if (!access) return `${host} is public: anyone may open it, with no login.`;
  switch (d.code) {
    case "authentication_required":
      return "Nobody is signed in, so the visitor is sent to log in first.";
    case "domain_not_allowed":
      return `Only addresses at ${(access.allowed_domains ?? []).join(" or ")} may open ${host}; ${email} is not one of them.`;
    case "email_not_allowed":
      return `Only ${(access.allowed_emails ?? []).join(" or ")} may open ${host}; ${email} is not on the list.`;
    case "email_not_verified":
      return `The provider has not confirmed that ${email} really belongs to them, so the engine does not trust the address.`;
    case "context_rule_denied":
    case "context_signal_unknown":
      return `${host} only opens from a passkey that stays on one device; this sign-in does not qualify.`;
    case "allowed": {
      if (!hasCondition(d.reason)) return `${email} may open ${host}.`;
      const minutes = /last (\d+) min/.exec(d.reason)?.[1];
      return minutes
        ? `${email} is allowed, but only with a sign-in from the last ${minutes} minutes; an older session is sent to log in again.`
        : `${email} is allowed, as long as the sign-in meets the app's extra requirement.`;
    }
    default:
      return d.reason;
  }
}

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
