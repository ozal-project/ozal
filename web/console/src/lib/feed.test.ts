// node --test web/console/src/lib/*.test.ts (scripts/verify.sh)
import { test } from "node:test";
import assert from "node:assert/strict";
import { emptyFeed, receive } from "./feed.ts";

function feed(ids: string[]) {
  let state = emptyFeed();
  const accepted: string[] = [];
  for (const id of ids) {
    const r = receive(state, id);
    state = r.state;
    if (r.accept) accepted.push(id);
  }
  return { state, accepted };
}

test("a replay after reconnecting adds nothing twice", () => {
  // The review's reproduction: four events, a disconnect, the same four again.
  const four = ["ab-1", "ab-2", "ab-3", "ab-4"];
  const { state, accepted } = feed([...four, ...four]);
  assert.deepEqual(accepted, four);
  assert.equal(state.missed, 0);
});

test("a jump in the sequence is counted as missed", () => {
  const { state, accepted } = feed(["ab-1", "ab-2", "ab-6", "ab-7"]);
  assert.equal(accepted.length, 4);
  assert.equal(state.missed, 3);
});

test("history that starts late is not a gap", () => {
  assert.equal(feed(["ab-301", "ab-302"]).state.missed, 0);
});

test("a restart is reported, and numbering from 1 again is not a gap", () => {
  const { state, accepted } = feed(["ab-41", "ab-42", "cd-1", "cd-2"]);
  assert.equal(accepted.length, 4);
  assert.equal(state.restarted, true);
  assert.equal(state.missed, 0);
});

test("an event without an identity is shown, not counted", () => {
  const { state, accepted } = feed(["", "ab-1"]);
  assert.equal(accepted.length, 2);
  assert.equal(state.missed, 0);
});

test("history that starts late is partial; one that starts at 1 is whole", () => {
  assert.equal(feed(["ab-301", "ab-302"]).state.partial, true);
  assert.equal(feed(["ab-1", "ab-2"]).state.partial, false);
  assert.equal(feed(["ab-301", "cd-1"]).state.partial, false, "a new run seen from its start is whole again");
});
