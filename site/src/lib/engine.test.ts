// node --test --experimental-strip-types src/lib/engine.test.ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { APPS, describe, plainDecision, requestFor, stagesFor, type Report, type Visitor } from "./engine.ts";

const visitor = (over: Partial<Visitor> = {}): Visitor => ({ who: "ana", verified: true, method: "passkey", bound: false, ...over });
const report = (over: Partial<Report> & { decision: Report["decision"] }): Report => ({ route: "x", protected: true, scope: "authorization_only", principal: { email: "ana@acme.dev" }, ...over });

test("a signed-out visitor sends no principal, a protected app says it needs auth", () => {
  const req = requestFor("grafana", visitor({ who: "none" })) as { route: { auth_required: boolean }; principal?: unknown };
  assert.equal(req.principal, undefined);
  assert.equal(req.route.auth_required, true);
});

test("the public app needs no login and carries no policy", () => {
  const req = requestFor("blog", visitor()) as { route: { auth_required: boolean; access?: unknown } };
  assert.equal(req.route.auth_required, false);
  assert.equal(req.route.access, undefined);
});

test("device_bound is sent only for a passkey login", () => {
  const passkey = requestFor("billing", visitor({ method: "passkey", bound: true })) as { principal: Record<string, unknown> };
  const oidc = requestFor("billing", visitor({ method: "oidc", bound: true })) as { principal: Record<string, unknown> };
  assert.equal(passkey.principal.device_bound, true);
  assert.equal("device_bound" in oidc.principal, false, "the engine refuses device_bound without a passkey");
});

test("the people on the page are fixed addresses, never typed text", () => {
  for (const [who, email] of [["ana", "ana@acme.dev"], ["carla", "carla@acme.dev"], ["bob", "bob@other.org"]]) {
    assert.equal((requestFor("wiki", visitor({ who })) as { principal: { email: string } }).principal.email, email);
  }
});

test("the page says nothing about groups: they are not a product feature yet", () => {
  for (const app of Object.values(APPS)) assert.equal(JSON.stringify(app).includes("group"), false, app.host);
  const req = requestFor("grafana", visitor()) as { principal: { groups: string[] } };
  assert.deepEqual(req.principal.groups, [], "no group is ever sent");
});

test("the billing app asks for what the engine evaluates with the session, not what the proxy checks later", () => {
  const rule = APPS.billing.access?.context_rule;
  assert.deepEqual(rule, { when: { session_device_bound: false }, then: "deny" });
  assert.equal("require_device_bound" in (APPS.billing.access ?? {}), false, "that field is only stated by the engine, so the demo would show a control that changes nothing");
});

test("each demo app says what its policy requires", () => {
  assert.deepEqual(Object.keys(APPS), ["grafana", "billing", "wiki", "blog"]);
  assert.equal(APPS.blog.access, undefined);
});

test("the verdict separates refused, let in, and let in with a condition", () => {
  const base = { step_up: false };
  assert.deepEqual(describe(report({ decision: { ...base, allowed: false, code: "domain_not_allowed", reason: "x" } })), { state: "refused", title: "Refused" });
  assert.deepEqual(describe(report({ decision: { ...base, allowed: true, code: "allowed", reason: "ok" } })), { state: "allowed", title: "Let in" });
  assert.equal(describe(report({ decision: { ...base, allowed: true, code: "allowed", reason: "allowed; the session must also have a login within the last 15 min (max_auth_age_minutes)" } })).state, "again");
  assert.equal(describe(report({ decision: { allowed: false, step_up: true, code: "context_step_up_required", reason: "x" } })).title, "Asks to sign in again");
});

test("the pipeline stops where the engine stopped, and says what it does not simulate", () => {
  const gate = stagesFor(report({ scope: "gate_only", decision: { allowed: false, step_up: false, code: "authentication_required", reason: "x" } }));
  assert.deepEqual(gate.map((s) => [s.name, s.state]), [["trust", "skip"], ["ratelimit", "skip"], ["identify", "no"], ["authorize", "skip"], ["forward", "skip"]]);
  const refused = stagesFor(report({ decision: { allowed: false, step_up: false, code: "domain_not_allowed", reason: "x" } }));
  assert.deepEqual(refused.slice(2).map((s) => [s.name, s.state]), [["identify", "ok"], ["authorize", "no"], ["forward", "skip"]]);
  const open = stagesFor(report({ decision: { allowed: true, step_up: false, code: "allowed", reason: "ok" } }));
  assert.equal(open.at(-1)?.state, "ok");
  assert.equal(open[0].note, "not simulated here", "the demo does not claim to run rate limiting or the transport");
  const pub = stagesFor(report({ scope: "public_route", protected: false, decision: { allowed: true, step_up: false, code: "allowed", reason: "public" } }));
  assert.equal(pub.find((s) => s.name === "identify")?.state, "skip");
});

test("each answer is explained in a sentence that names the app and the person", () => {
  const say = (app: string, who: string, code: string, reason = "x", allowed = false) =>
    plainDecision(report({ decision: { allowed, step_up: false, code, reason } }), app, visitor({ who }));
  assert.match(say("grafana", "bob", "domain_not_allowed"), /Only addresses at acme\.dev may open grafana\.acme\.dev; bob@other\.org is not one of them/);
  assert.match(say("billing", "carla", "email_not_allowed"), /Only ana@acme\.dev may open billing\.acme\.dev; carla@acme\.dev is not on the list/);
  assert.match(say("grafana", "none", "authentication_required"), /sent to log in first/);
  assert.match(say("billing", "ana", "context_rule_denied"), /stays on one device/);
  assert.match(say("grafana", "ana", "allowed", "allowed", true), /^ana@acme\.dev may open grafana\.acme\.dev\.$/);
  assert.match(say("wiki", "ana", "allowed", "allowed; the session must also have a login within the last 15 min (max_auth_age_minutes)", true), /only with a sign-in from the last 15 minutes/);
  assert.match(say("blog", "none", "allowed", "public route; no access policy", true), /public: anyone may open it/);
  assert.equal(say("grafana", "ana", "something_new", "the engine's own words"), "the engine's own words", "an unknown code falls back to the engine's reason");
});
