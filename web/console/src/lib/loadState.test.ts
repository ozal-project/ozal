// node --test web/console/src/lib/*.test.ts (scripts/verify.sh)
import { test } from "node:test";
import assert from "node:assert/strict";
import { afterLoad } from "./loadState.ts";

test("a success after 'not enabled' clears it", () => {
  let state = afterLoad({ ok: false, disabled: true, message: "pages is not enabled on this edge" });
  assert.deepEqual(state, { disabled: true, error: "pages is not enabled on this edge" });
  state = afterLoad({ ok: true });
  assert.deepEqual(state, { disabled: false, error: "" });
});

test("an ordinary failure is an error, not 'not enabled'", () => {
  assert.deepEqual(afterLoad({ ok: false, disabled: false, message: "HTTP 503" }), { disabled: false, error: "HTTP 503" });
});
