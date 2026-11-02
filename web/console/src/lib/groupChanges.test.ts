import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { groupMemberChanges, membershipAfterChange, parseGroupNames } from "./groupChanges.ts";

describe("group membership changes", () => {
  it("plans additions and removals from the displayed membership", () => {
    assert.deepEqual(groupMemberChanges(["a@example.test", "b@example.test"], ["b@example.test", "c@example.test"]), [
      { email: "c@example.test", join: true },
      { email: "a@example.test", join: false },
    ]);
  });

  it("does not produce duplicate changes when input contains duplicates", () => {
    assert.deepEqual(groupMemberChanges(["a@example.test", "a@example.test"], ["a@example.test", "b@example.test", "b@example.test"]), [
      { email: "b@example.test", join: true },
    ]);
  });

  it("keeps a person's other groups and sorts the resulting membership", () => {
    assert.deepEqual(membershipAfterChange(["zeta", "staff"], "ops", true), ["ops", "staff", "zeta"]);
    assert.deepEqual(membershipAfterChange(["ops", "staff"], "ops", false), ["staff"]);
  });

  it("parses trimmed, unique, case-sensitive group names", () => {
    assert.deepEqual(parseGroupNames(" staff,ops, staff ,,Ops "), ["Ops", "ops", "staff"]);
  });
});
