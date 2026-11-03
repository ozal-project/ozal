import assert from "node:assert/strict";
import test from "node:test";
import { outcomeOf, withPasskeyProof } from "./accessRequests.ts";

test("the status answer maps to what the requester sees", () => {
  assert.deepEqual(outcomeOf(200, { state: "pending" }), { state: "pending" });
  assert.deepEqual(outcomeOf(200, { state: "approved", token: "t0ken" }), { state: "approved", token: "t0ken" });
  assert.deepEqual(outcomeOf(200, { state: "denied" }), { state: "denied" });
  assert.deepEqual(outcomeOf(404, {}), { state: "gone" });
});

test("an approval without a link, or an unknown answer, is an error and not a success", () => {
  assert.throws(() => outcomeOf(200, { state: "approved" }), /could not check/);
  assert.throws(() => outcomeOf(200, { state: "maybe" }), /could not check/);
  assert.throws(() => outcomeOf(500, {}), /could not check/);
});

function passkeyRequired() {
  return Object.assign(new Error("confirm"), { code: "passkey_required" });
}

test("approval asks for a passkey only when required and retries once", async () => {
  const calls: string[] = [];
  let runs = 0;
  const result = await withPasskeyProof(" session ", async () => {
    calls.push("run");
    if (++runs === 1) throw passkeyRequired();
    return "ok";
  }, async (token) => { calls.push(`confirm:${token}`); });
  assert.equal(result, "ok");
  assert.deepEqual(calls, ["run", "confirm:session", "run"]);
});

test("other failures and a cancelled passkey do not retry", async () => {
  let runs = 0;
  await assert.rejects(withPasskeyProof("s", async () => { runs++; throw new Error("nope"); }, async () => {}), /nope/);
  assert.equal(runs, 1);
  runs = 0;
  await assert.rejects(withPasskeyProof("s", async () => { runs++; throw passkeyRequired(); }, async () => { throw new Error("cancelled"); }), /cancelled/);
  assert.equal(runs, 1);
});
