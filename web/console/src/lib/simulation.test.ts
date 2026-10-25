// node --test web/console/src/lib/*.test.ts (scripts/verify.sh)
import { test } from "node:test";
import assert from "node:assert/strict";
import { freshness, inputKey, type Inputs } from "./simulation.ts";

const ana: Inputs = { host: "app.example.test", mode: "user", email: "ana@example.test", verified: true, groups: "eng", method: "", deviceBound: false, userVerified: "unknown", deviceFirstSeen: "unknown", requestMethod: "" };

test("a result answers the inputs it was computed for", () => {
  assert.equal(freshness({ key: inputKey(ana) }, ana, false), "current");
  assert.equal(freshness(null, ana, false), "none");
});

test("editing the form turns the result into a previous answer", () => {
  const result = { key: inputKey(ana) };
  for (const change of [{ email: "outsider@example.test" }, { host: "other.example.test" }, { verified: false }, { groups: "eng, ops" }, { method: "oidc" as const }, { method: "passkey" as const, deviceBound: true }, { method: "passkey" as const, userVerified: "false" as const }, { method: "passkey" as const, deviceFirstSeen: "true" as const }, { requestMethod: "POST" }, { mode: "anon" as const }]) {
    assert.equal(freshness(result, { ...ana, ...change }, false), "previous", JSON.stringify(change));
  }
});

test("a failed run after it also does", () => {
  assert.equal(freshness({ key: inputKey(ana) }, ana, true), "previous");
});

test("spacing does not change the question", () => {
  assert.equal(inputKey(ana), inputKey({ ...ana, email: "  ana@example.test ", groups: " eng ,, " }));
  assert.equal(inputKey({ ...ana, mode: "anon" }), inputKey({ ...ana, mode: "anon", email: "x@y", groups: "z" }));
});

test("context-rule session evidence changes the question", () => {
  assert.notEqual(inputKey(ana), inputKey({ ...ana, method: "oidc" }));
  assert.notEqual(inputKey({ ...ana, method: "passkey" }), inputKey({ ...ana, method: "passkey", deviceBound: true }));
  assert.notEqual(inputKey({ ...ana, method: "passkey" }), inputKey({ ...ana, method: "passkey", userVerified: "true" }));
  assert.notEqual(inputKey({ ...ana, method: "passkey" }), inputKey({ ...ana, method: "passkey", deviceFirstSeen: "true" }));
  assert.equal(inputKey({ ...ana, method: "oidc" }), inputKey({ ...ana, method: "oidc", userVerified: "true" }));
  assert.equal(inputKey({ ...ana, method: "oidc" }), inputKey({ ...ana, method: "oidc", deviceFirstSeen: "true" }));
  assert.equal(inputKey({ ...ana, mode: "anon" }), inputKey({ ...ana, mode: "anon", method: "oidc", deviceBound: true }));
  assert.notEqual(inputKey(ana), inputKey({ ...ana, requestMethod: "DELETE" }));
  assert.equal(inputKey({ ...ana, mode: "anon" }), inputKey({ ...ana, mode: "anon", requestMethod: "POST" }));
});
