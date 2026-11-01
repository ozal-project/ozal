import assert from "node:assert/strict";
import test from "node:test";
import { startUpdateWithStepUp } from "./updateStepUp.ts";

function passkeyRequired() {
  return Object.assign(new Error("Recent passkey confirmation required"), { code: "passkey_required" });
}

test("step-up retries the reviewed plan once on the same session", async () => {
  const calls: string[] = [];
  let starts = 0;
  const result = await startUpdateWithStepUp("plan-7", " session-token ", async (id) => {
    calls.push(`start:${id}`);
    if (++starts === 1) throw passkeyRequired();
    return "job-9";
  }, async (token) => { calls.push(`confirm:${token}`); });

  assert.equal(result, "job-9");
  assert.deepEqual(calls, ["start:plan-7", "confirm:session-token", "start:plan-7"]);
});

test("a failed proof does not retry the update", async () => {
  let starts = 0;
  await assert.rejects(startUpdateWithStepUp("plan-7", "session-token", async () => {
    starts++;
    throw passkeyRequired();
  }, async () => { throw new Error("Passkey cancelled"); }), /Passkey cancelled/);
  assert.equal(starts, 1);
});

test("a second passkey requirement is returned without a third attempt", async () => {
  let starts = 0;
  let proofs = 0;
  await assert.rejects(startUpdateWithStepUp("plan-7", "session-token", async () => {
    starts++;
    throw passkeyRequired();
  }, async () => { proofs++; }), (error: unknown) => error instanceof Error && "code" in error && error.code === "passkey_required");
  assert.equal(starts, 2);
  assert.equal(proofs, 1);
});

test("other update errors do not trigger passkey confirmation or a retry", async () => {
  let starts = 0;
  let proofs = 0;
  await assert.rejects(startUpdateWithStepUp("plan-7", "session-token", async () => {
    starts++;
    throw Object.assign(new Error("Operator tool missing"), { code: "forbidden" });
  }, async () => { proofs++; }), /Operator tool missing/);
  assert.equal(starts, 1);
  assert.equal(proofs, 0);
});
