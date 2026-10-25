import { test } from "node:test";
import assert from "node:assert/strict";
import { ageLabel, elapsed, isStale } from "./freshness.ts";

test("freshness labels keep age and failure distinct from a successful current read", () => {
  assert.equal(ageLabel(2_000), "just now");
  assert.equal(ageLabel(23_000), "23 seconds ago");
  assert.equal(ageLabel(120_000), "2 minutes ago");
  assert.equal(isStale(1_000, false), false);
  assert.equal(isStale(1_000, true), true);
  assert.equal(isStale(61_000, false), true);
});

test("a reading taken just after the last clock tick is not an unknown time", () => {
  assert.equal(elapsed(1_000, 4_000), 0);
  assert.equal(ageLabel(elapsed(1_000, 4_000)), "just now");
  assert.equal(elapsed(9_000, 4_000), 5_000);
});
