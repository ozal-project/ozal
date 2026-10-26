// node --test --experimental-strip-types src/lib/engine.test.ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { APPS, describe, requestFor, stagesFor, type Report, type Visitor } from "./engine.ts";

const visitor = (over: Partial<Visitor> = {}): Visitor => ({ who: "ana", groups: [], verified: true, method: "passkey", bound: false, ...over });
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
  assert.equal((requestFor("wiki", visitor({ who: "ana" })) as { principal: { email: string } }).principal.email, "ana@acme.dev");
  assert.equal((requestFor("wiki", visitor({ who: "bob" })) as { principal: { email: string } }).principal.email, "bob@other.org");
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
  assert.deepEqual(describe(report({ decision: { ...base, allowed: false, code: "missing_required_group", reason: "x" } })), { state: "refused", title: "Refused" });
  assert.deepEqual(describe(report({ decision: { ...base, allowed: true, code: "allowed", reason: "ok" } })), { state: "allowed", title: "Let in" });
  assert.equal(describe(report({ decision: { ...base, allowed: true, code: "allowed", reason: "allowed; the session must also have a login within the last 15 min (max_auth_age_minutes)" } })).state, "again");
  assert.equal(describe(report({ decision: { allowed: false, step_up: true, code: "context_step_up_required", reason: "x" } })).title, "Asks to sign in again");
});

test("the pipeline stops where the engine stopped, and says what it does not simulate", () => {
  const gate = stagesFor(report({ scope: "gate_only", decision: { allowed: false, step_up: false, code: "authentication_required", reason: "x" } }));
  assert.deepEqual(gate.map((s) => [s.name, s.state]), [["trust", "skip"], ["ratelimit", "skip"], ["identify", "no"], ["authorize", "skip"], ["forward", "skip"]]);
  const refused = stagesFor(report({ decision: { allowed: false, step_up: false, code: "missing_required_group", reason: "x" } }));
  assert.deepEqual(refused.slice(2).map((s) => [s.name, s.state]), [["identify", "ok"], ["authorize", "no"], ["forward", "skip"]]);
  const open = stagesFor(report({ decision: { allowed: true, step_up: false, code: "allowed", reason: "ok" } }));
  assert.equal(open.at(-1)?.state, "ok");
  assert.equal(open[0].note, "not simulated here", "the demo does not claim to run rate limiting or the transport");
  const pub = stagesFor(report({ scope: "public_route", protected: false, decision: { allowed: true, step_up: false, code: "allowed", reason: "public" } }));
  assert.equal(pub.find((s) => s.name === "identify")?.state, "skip");
});
