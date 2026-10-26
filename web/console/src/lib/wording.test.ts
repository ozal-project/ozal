import { test } from "node:test";
import assert from "node:assert/strict";
import { friendlyHealth, shortId, subjectsOf, whoActed } from "./wording.ts";

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
