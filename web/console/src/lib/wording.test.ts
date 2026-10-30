import { test } from "node:test";
import assert from "node:assert/strict";
import { applicationAccess, eventReason, friendlyHealth, shortId, subjectsOf, whoActed, servedBy, whoMayEnter } from "./wording.ts";

test("an admin request names the admin API, not a throwaway port", () => {
  assert.equal(whoActed("127.0.0.1:56198"), "the admin API");
  assert.equal(whoActed("[::1]:41022"), "the admin API");
  assert.equal(whoActed("admin"), "the admin token");
  assert.equal(whoActed("signal:SIGHUP"), "a reload signal (SIGHUP)");
  assert.equal(whoActed("ana@acme.dev"), "ana@acme.dev");
  assert.equal(whoActed(undefined), "");
});

test("a refused connection says nothing is listening, and where", () => {
  assert.equal(friendlyHealth('Get "http://127.0.0.1:19199/": dial tcp 127.0.0.1:19199: connect: connection refused'), "nothing is listening at 127.0.0.1:19199");
});

test("other health failures are put in plain words", () => {
  assert.equal(friendlyHealth('Get "http://app/": dial tcp: lookup app: no such host'), "its hostname does not exist");
  assert.equal(friendlyHealth("context deadline exceeded (Client.Timeout exceeded while awaiting headers)"), "it took too long to answer");
  assert.equal(friendlyHealth("x509: certificate signed by unknown authority"), "its certificate is not trusted");
  assert.equal(friendlyHealth("unexpected status 503"), "it answered with an error (503)");
});

test("an error nobody anticipated is shown, trimmed, never hidden", () => {
  assert.equal(friendlyHealth("boom"), "boom");
  assert.equal(friendlyHealth("x".repeat(200)).length, 90);
  assert.equal(friendlyHealth(""), "it did not answer its health check");
});

test("a content hash is shown by its first characters", () => {
  assert.equal(shortId("c886e1677de3260377acd4983e94258fce0c3de6f125136ea247e11d75810788"), "c886e1677de3");
  assert.equal(shortId("short"), "short");
  assert.equal(shortId(undefined), "");
});

test("a passkey session's opaque id is shown as the person's email when the console knows them", () => {
  const people = subjectsOf([{ email: "bob@other.org", subject: "sb:850d973b" }, { email: "old@acme.dev" }]);
  assert.equal(whoActed("sb:850d973b", people), "bob@other.org");
  assert.equal(whoActed("sb:unknown", people), "sb:unknown", "an id nobody knows is shown as it is, never hidden");
  assert.equal(whoActed("sb:850d973b"), "sb:850d973b", "without the list the id stays");
  assert.equal(people.size, 1, "a person with no subject cannot be matched");
});

test("operator and administrator actors read as people", () => {
  assert.equal(whoActed("operator:Saint"), "Saint (host login)");
  assert.equal(whoActed("admin:ana@acme.dev"), "ana@acme.dev");
  assert.equal(whoActed("local"), "the host");
});

test("tool lists in reasons get spaces", () => {
  assert.equal(eventReason("tools: administrators,people,sites"), "tools: administrators, people, sites");
  assert.equal(eventReason("passkey"), "passkey");
  assert.equal(eventReason(undefined), "");
});

// An operator must not mistake an email, device or context restriction for
// permission granted to every signed-in visitor.
test("application access summaries preserve identity and session restrictions", () => {
  assert.equal(applicationAccess({}), "Public · no login");
  assert.equal(applicationAccess({ auth_required: true }), "Any signed-in person");
  assert.equal(applicationAccess({ access: {} }), "Any signed-in person");
  assert.equal(applicationAccess({ access: { allowed_domains: ["example.test"] } }), "Approved email addresses or domains");
  assert.equal(applicationAccess({ access: { allowed_emails: ["person@example.test"] } }), "Approved email addresses or domains");
  assert.equal(applicationAccess({ access: { required_groups: ["ops"] } }), "Members of");
  assert.equal(applicationAccess({ access: { required_groups: ["ops"], allowed_domains: ["example.test"] } }), "Group members with an approved email or domain");
  for (const policy of [{ methods: ["passkey"] }, { require_device_bound: true }, { max_auth_age_minutes: 15 }, { context_rule: { when: { session_device_bound: false }, then: "deny" } }]) {
    assert.equal(applicationAccess({ access: policy }), "Sign-in with extra checks");
  }
  assert.equal(applicationAccess({ access: { required_groups: ["ops"], methods: ["passkey"] } }), "Group members · extra checks");
});

test("what serves an application reads without knowing the config", () => {
  assert.equal(servedBy({ target: "http://127.0.0.1:3000" }), "App on this server · port 3000");
  assert.equal(servedBy({ target: "http://10.0.0.5:8080" }), "App at 10.0.0.5:8080");
  assert.equal(servedBy({ redirect: "https://ozal.dev" }), "Redirects to ozal.dev");
  assert.equal(servedBy({ redirect: "https://ozal.dev/archive" }), "Redirects to ozal.dev/archive");
  assert.equal(servedBy({ site: "blog" }), "Static website · blog");
  assert.equal(servedBy({ connector: "homelab" }), "Machine connected through homelab");
  assert.equal(servedBy({ upstreams: ["a", "b"] }), "2 servers, shared between them");
});

test("who may enter is one sentence with a public or private tone", () => {
  assert.deepEqual(whoMayEnter({}), { tone: "public", text: "Public · anyone with the link" });
  assert.deepEqual(whoMayEnter({ access: {} }), { tone: "private", text: "Private · anyone with an account" });
  assert.deepEqual(whoMayEnter({ access: { required_groups: ["ops", "staff"] } }), { tone: "private", text: "Private · members of ops and staff" });
  assert.deepEqual(whoMayEnter({ access: { allowed_domains: ["acme.dev"] } }), { tone: "private", text: "Private · @acme.dev" });
  assert.equal(whoMayEnter({ auth_required: true }).tone, "private");
});
