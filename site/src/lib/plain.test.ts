import { test } from "node:test";
import assert from "node:assert/strict";
import { PLAIN } from "./plain.ts";

test("plain names are short, read as words, and never carry a tracker code", () => {
  for (const [id, text] of Object.entries(PLAIN)) {
    assert.match(id, /^[A-Z]+-\d+$/);
    assert.ok(text.length >= 12 && text.length <= 60, `${id}: ${text.length} characters`);
    assert.equal(/\b[A-Z]{2,}-\d{3}\b/.test(text), false, `${id} names a tracker code`);
    assert.equal(/group/i.test(text), false, `${id} mentions groups, which are not a product feature yet`);
  }
});
