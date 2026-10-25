import { test } from "node:test";
import assert from "node:assert/strict";
import { accessSummary, attention, changes, headline } from "./overview.ts";
import type { AccessEvent, RouteStatus, StatusResponse } from "./api.ts";

const route = (over: Partial<RouteStatus> = {}): RouteStatus => ({
  access: "public", policy: "", kind: "HTTP proxy", is_static: false, is_tunnel: false, host: "app.example.com",
  target: "http://127.0.0.1:3000", health_path: "/", state: "Healthy", state_class: "healthy", latency: "2ms",
  last_check: "", last_transition: "", last_error: "", ...over,
});
const status = (routes: RouteStatus[], over: Partial<StatusResponse> = {}): StatusResponse => ({ generated_at: "2026-10-25T18:00:00Z", routes, ...over });
const event = (type: string, over: Partial<AccessEvent> = {}): AccessEvent => ({ time: "2026-10-25T18:12:00Z", type, ...over });

test("the headline counts what is serving and says when something needs the operator", () => {
  assert.equal(headline(status([route(), route({ host: "b" })]), 0), "Serving 2 apps. Everything is answering.");
  assert.equal(headline(status([route(), route({ host: "b", state_class: "unhealthy" })]), 1), "Serving 2 apps. One thing needs you.");
  assert.equal(headline(status([route(), route({ host: "b", state_class: "unhealthy" }), route({ host: "c", state_class: "unhealthy" })]), 2), "Serving 3 apps. 2 things need you.");
  assert.equal(headline(status([route({ is_static: true }), route({ host: "b" })]), 0), "Serving 1 app and 1 site. Everything is answering.");
  assert.equal(headline(null, 0), "Loading…");
});

test("an unhealthy route is attention; an unknown one is not an alarm", () => {
  const items = attention(status([route({ host: "sigea.example.com", state_class: "unhealthy", last_error: "connection refused", last_transition: "2026-10-25T18:31:00Z" }), route({ host: "new.example.com", state_class: "unknown" })]));
  assert.equal(items.length, 1);
  assert.equal(items[0].title, "sigea.example.com is not answering");
  assert.match(items[0].detail, /connection refused/);
  assert.equal(items[0].action?.page, "/routes");
});

test("certificate and audit problems surface, healthy ones do not", () => {
  assert.equal(attention(status([], { certificate: { host: "x", mode: "acme", valid: true, not_after: "", last_outcome: "ok", last_error: "", last_attempt: "", next_attempt: "", pending: 0 }, audit: { available: true, failure_count: 0 } })).length, 0);
  const items = attention(status([], {
    certificate: { host: "x", mode: "acme", valid: false, not_after: "", last_outcome: "failed", last_error: "dns challenge failed", last_attempt: "", next_attempt: "", pending: 0 },
    audit: { available: true, failure_count: 3, last_failure_at: "2026-10-25T17:00:00Z" },
  }));
  assert.deepEqual(items.map((i) => i.kind), ["certificate", "audit"]);
});

test("changes keep operator events and drop access and login noise", () => {
  const list = changes([
    event("access.allowed"), event("login.succeeded"), event("access.denied"),
    event("config.reloaded"), event("pages.deployed", { host: "portfolio" }),
  ]);
  assert.deepEqual(list.map((c) => c.type), ["config.reloaded", "pages.deployed"]);
});

test("a deploy from CI says where it came from; a manual one says who", () => {
  const [ci] = changes([event("pages.deployed", { host: "portfolio", actor: "github", deployment: { project: "portfolio", repository_id: 1, ref: "refs/heads/main", commit_sha: "631fb79abcdef012345" } })]);
  assert.equal(ci.title, "portfolio was updated");
  assert.equal(ci.source, "GitHub Actions · main · 631fb79abcde");
  assert.equal(ci.undo?.site, "portfolio");
  const [hand] = changes([event("pages.deployed", { host: "custodia-site", actor: "admin" })]);
  assert.equal(hand.source, "uploaded by hand (admin)");
});

test("a failed deploy is shown as a failure and carries no undo", () => {
  const [fail] = changes([event("pages.deploy_refused", { host: "portfolio", code: "deployer.token_invalid", deployment: { project: "portfolio" } })]);
  assert.equal(fail.tone, "bad");
  assert.equal(fail.title, "An update to portfolio did not go live");
  assert.equal(fail.undo, undefined);
  assert.match(fail.detail, /deployer\.token_invalid/);
});

test("the access summary counts decisions and ranks refusal reasons", () => {
  const sum = accessSummary([
    event("access.allowed"), event("access.allowed"), event("access.allowed"),
    event("access.denied", { code: "missing_required_group" }), event("access.denied", { code: "missing_required_group" }), event("access.denied", { code: "authentication_required" }),
    event("login.started"), event("access.rate_limited"),
  ]);
  assert.equal(sum.allowed, 3);
  assert.equal(sum.refused, 3);
  assert.equal(sum.rateLimited, 1);
  assert.equal(sum.loginsStarted, 1);
  assert.deepEqual(sum.reasons[0], { code: "missing_required_group", text: "Not in the group the app needs", count: 2 });
  assert.equal(sum.reasons[1].text, "Not signed in");
});

test("an unknown refusal code is shown as is, never dropped", () => {
  const sum = accessSummary([event("access.denied", { code: "something_new" })]);
  assert.deepEqual(sum.reasons, [{ code: "something_new", text: "something_new", count: 1 }]);
});
