import { test } from "node:test";
import assert from "node:assert/strict";
import { LatestRequest } from "./latestRequest.ts";

test("starting a newer poll aborts and invalidates the previous response", () => {
  const latest = new LatestRequest();
  const older = latest.start();
  const newer = latest.start();

  assert.equal(older.signal.aborted, true);
  assert.equal(older.isCurrent(), false);
  assert.equal(newer.isCurrent(), true);

  newer.finish();
  assert.equal(newer.isCurrent(), false);
  latest.cancel();
});

test("unmount cancellation aborts the active request", () => {
  const latest = new LatestRequest();
  const request = latest.start();

  latest.cancel();

  assert.equal(request.signal.aborted, true);
  assert.equal(request.isCurrent(), false);
});
