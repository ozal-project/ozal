import { test } from "node:test";
import assert from "node:assert/strict";
import { needsBackup, readLater, rememberLater } from "./backupPasskey.ts";

test("a second passkey is suggested only when exactly one is known", () => {
  assert.equal(needsBackup([{ id: "a" }], false), true);
  assert.equal(needsBackup([{ id: "a" }, { id: "b" }], false), false, "a backup already exists");
  assert.equal(needsBackup([], false), false, "a host login or an empty list is not a one-passkey account");
  assert.equal(needsBackup(null, false), false, "an unknown list claims nothing");
  assert.equal(needsBackup([{ id: "a" }], true), false, "not now hides it for this tab");
});

test("not now is remembered for the tab, and broken storage only shows the suggestion again", () => {
  const items = new Map<string, string>();
  const store = { getItem: (k: string) => items.get(k) ?? null, setItem: (k: string, v: string) => void items.set(k, v) };
  assert.equal(readLater(store), false);
  rememberLater(store);
  assert.equal(readLater(store), true);

  const refusing = { getItem: () => { throw new Error("denied"); }, setItem: () => { throw new Error("denied"); } };
  assert.doesNotThrow(() => rememberLater(refusing));
  assert.equal(readLater(refusing), false);
  assert.equal(readLater(undefined), false);
});
