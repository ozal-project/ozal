// node --test web/console/src/lib/*.test.ts (scripts/verify.sh)
import { test } from "node:test";
import assert from "node:assert/strict";
import { freshness, inputKey, type Inputs } from "./simulation.ts";

const ana: Inputs = { host: "app.example.test", mode: "user", email: "ana@example.test", verified: true, groups: "eng" };

test("a result answers the inputs it was computed for", () => {
  assert.equal(freshness({ key: inputKey(ana) }, ana, false), "current");
  assert.equal(freshness(null, ana, false), "none");
});

test("editing the form turns the result into a previous answer", () => {
  const result = { key: inputKey(ana) };
  for (const change of [{ email: "outsider@example.test" }, { host: "other.example.test" }, { verified: false }, { groups: "eng, ops" }, { mode: "anon" as const }]) {
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
