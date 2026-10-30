import { test } from "node:test";
import assert from "node:assert/strict";
import { accessScope, accessSummary, applications, attention, certificateView, changes, headline, streamNotices } from "./overview.ts";
import type { AccessEvent, RouteStatus, StatusResponse } from "./api.ts";

const route = (over: Partial<RouteStatus> = {}): RouteStatus => ({
  access: "public", policy: "", kind: "HTTP proxy", is_static: false, is_tunnel: false, host: "app.example.com",
  target: "http://127.0.0.1:3000", health_path: "/", state: "Healthy", state_class: "healthy", latency: "2ms",
  last_check: "", last_transition: "", last_error: "", ...over,
});
const status = (routes: RouteStatus[], over: Partial<StatusResponse> = {}): StatusResponse => ({ generated_at: "2026-10-25T18:00:00Z", routes, ...over });
const event = (type: string, over: Partial<AccessEvent> = {}): AccessEvent => ({ time: "2026-10-25T18:12:00Z", type, ...over });

test("the headline counts configured routes without claiming they are serving", () => {
  assert.equal(headline(status([route(), route({ host: "b", state_class: "unknown" })]), 0), "2 app routes configured. No active alerts.");
  assert.equal(headline(status([route(), route({ host: "b", state_class: "unhealthy" })]), 1), "2 app routes configured. One thing needs you.");
  assert.equal(headline(status([route(), route({ host: "b", state_class: "unhealthy" }), route({ host: "c", state_class: "unhealthy" })]), 2), "3 app routes configured. 2 things need you.");
  assert.equal(headline(status([route({ is_static: true, state_class: "unknown" }), route({ host: "b" })]), 0), "1 app route and 1 site route configured. No active alerts.");
  assert.equal(headline(status([]), 0), "No routes configured. No active alerts.");
  assert.equal(headline(null, 0), "Loading…");
});

test("an unhealthy route is attention; an unknown one is not an alarm", () => {
  const items = attention(status([route({ host: "sigea.example.com", state_class: "unhealthy", last_error: "connection refused", last_transition: "2026-10-25T18:31:00Z" }), route({ host: "new.example.com", state_class: "unknown" })]));
  assert.equal(items.length, 1);
  assert.equal(items[0].title, "sigea.example.com is not answering");
  assert.match(items[0].detail, /nothing is listening at 127\.0\.0\.1:3000|down: /);
  assert.equal(items[0].raw, "connection refused", "the exact error stays available");
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
  assert.equal(hand.source, "uploaded via the admin token");
  const api = changes([event("pages.deployed", { host: "docs", actor: "127.0.0.1:56198" })])[0];
  assert.equal(api.source, "uploaded via the admin API", "a throwaway client port is never shown");
});

test("a failed deploy is shown as a failure and carries no undo", () => {
  const [fail] = changes([event("pages.deploy_refused", { host: "portfolio", code: "deployer.token_invalid", deployment: { project: "portfolio" } })]);
  assert.equal(fail.tone, "bad");
  assert.equal(fail.title, "An update to portfolio did not go live");
  assert.equal(fail.undo, undefined);
  assert.match(fail.detail, /deployer\.token_invalid/);
});

test("a workflow proposal is reviewable activity and makes no deployment claim", () => {
  const [proposal] = changes([event("github.workflow_proposed", { host: "blog" })]);
  assert.equal(proposal.title, "A publishing workflow was proposed for blog");
  assert.equal(proposal.tone, "neutral");
  assert.equal(proposal.undo, undefined);
  assert.match(proposal.detail, /Publication is not yet verified/);
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

test("a new passkey names the person, never the login page it was created on", () => {
  const people = new Map([["sb:850d", "ana@acme.dev"]]);
  const [named] = changes([event("passkey.registered", { host: "auth.example.com", actor: "sb:850d", code: "device_bound" })], people);
  assert.equal(named.title, "ana@acme.dev added a passkey");
  assert.equal(named.detail, "A passkey that stays on one device.");
  const [unknown] = changes([event("passkey.registered", { host: "auth.example.com", actor: "sb:other", code: "synced" })], people);
  assert.equal(unknown.title, "Someone added a passkey", "an id that maps to nobody is not shown as if it were a name");
});

const cert = (over: Partial<NonNullable<StatusResponse["certificate"]>> = {}) => ({
  host: "*.example.com, example.com", names: ["*.example.com", "example.com"], mode: "ACME DNS-01", valid: true,
  not_after: "2027-01-28T00:08:22Z", last_outcome: "renewed", last_error: "", last_attempt: "", next_attempt: "2026-12-29T00:08:22Z", pending: 0, ...over,
});

test("the certificate view lists every name, the days left and the next renewal", () => {
  const view = certificateView(cert(), new Date("2026-10-30T12:00:00Z"));
  assert.deepEqual(view?.names, ["*.example.com", "example.com"]);
  assert.match(view!.expires, /\(89 days left\)/);
  assert.equal(view?.tone, "good");
  assert.equal(certificateView(undefined, new Date()), null);
});

test("an older edge without names still shows them from host", () => {
  assert.deepEqual(certificateView(cert({ names: undefined }), new Date("2026-10-30T00:00:00Z"))?.names, ["*.example.com", "example.com"]);
});

test("a certificate close to its end is attention even though it still works", () => {
  const now = new Date("2027-01-20T00:00:00Z");
  assert.equal(certificateView(cert(), now)?.tone, "bad");
  const items = attention(status([], { certificate: cert() }), now);
  assert.deepEqual(items.map((i) => i.kind), ["certificate"]);
  assert.match(items[0].detail, /expires soon/);
  assert.equal(attention(status([], { certificate: cert() }), new Date("2026-10-30T00:00:00Z")).length, 0);
});

test("an expired certificate says so", () => {
  const view = certificateView(cert({ valid: false }), new Date("2027-02-01T00:00:00Z"));
  assert.match(view!.expires, /^expired on /);
  assert.equal(view?.tone, "bad");
});

test("the application list puts what is down first and leaves sites to the Sites list", () => {
  const rows = applications([
    route({ host: "wiki.example.com:8443", access: "Protected" }),
    route({ host: "blog.example.com", is_static: true }),
    route({ host: "reports.example.com", state_class: "unhealthy" }),
    route({ host: "api.example.com", state_class: "unknown" }),
  ]);
  assert.deepEqual(rows.map((r) => [r.name, r.state, r.label]), [
    ["reports.example.com", "down", "not answering"],
    ["api.example.com", "unchecked", "public"],
    ["wiki.example.com", "up", "sign-in"],
  ]);
  assert.equal(rows[2].host, "wiki.example.com:8443", "the link keeps the configured host");
});

const whole = { missed: 0, restarted: false, partial: false };

test("rows already on screen never hide a lost stream, missed events or a restart", () => {
  assert.deepEqual(streamNotices("live", whole, 12), []);
  assert.deepEqual(streamNotices("lost", whole, 0), [], "with nothing shown, the empty states already say it");
  assert.match(streamNotices("lost", whole, 12)[0], /stopped.*out of date/);
  assert.match(streamNotices("reconnected", { ...whole, missed: 3 }, 12)[0], /^3 events did not reach this page.*incomplete/);
  assert.match(streamNotices("live", { ...whole, restarted: true }, 12)[0], /restarted/);
  assert.equal(streamNotices("lost", { missed: 1, restarted: true, partial: true }, 5).length, 3);
});

test("the access counts say which events they cover", () => {
  const at = new Date("2026-10-30T15:39:00Z");
  assert.equal(accessScope("live", whole, at, 7), "All 7 events since this edge started.");
  assert.match(accessScope("live", { ...whole, partial: true }, at, 200), /^Last 200 events, since .*Older ones are no longer held\.$/);
  assert.match(accessScope("live", { ...whole, restarted: true }, at, 9), /across a restart/);
  assert.equal(accessScope("lost", whole, null, 0), "The event stream is unavailable.");
  assert.equal(accessScope("locked", whole, null, 0), "Sign in to see access decisions.");
});
