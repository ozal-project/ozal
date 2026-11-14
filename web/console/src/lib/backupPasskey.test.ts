import { test } from "node:test";
import assert from "node:assert/strict";
import { backupState, needsBackup, readLater, rememberLater } from "./backupPasskey.ts";

test("a second passkey is suggested only when exactly one is known", () => {
  assert.equal(needsBackup([{ id: "a" }], false), true);
  assert.equal(needsBackup([{ id: "a" }, { id: "b" }], false), false, "a backup already exists");
  assert.equal(needsBackup([], false), false, "a host login or an empty list is not a one-passkey account");
  assert.equal(needsBackup(null, false), false, "an unknown list claims nothing");
  assert.equal(needsBackup([{ id: "a" }], true), false, "not now hides it for this tab");
});

test("the provider is a hint: never proof that an account is protected", () => {
  const bitwarden = { provider: "Bitwarden", provider_known: true, synced: true };
  const google = { provider: "Google Password Manager", provider_known: true, synced: true };
  const hello = { provider: "Windows Hello", provider_known: true, synced: false };
  const unknown = { provider: "Security key or device passkey", provider_known: false, synced: false };
  assert.equal(backupState([bitwarden]), "one");
  assert.equal(backupState([bitwarden, { ...bitwarden }]), "same-provider", "two synced passkeys of one provider may share one vault");
  assert.equal(backupState([bitwarden, google]), "separate");
  assert.equal(backupState([hello, { ...hello }]), "unknown", "two device-bound passkeys of one model may be independent");
  assert.equal(backupState([bitwarden, unknown]), "unknown", "an unknown provider tells nothing");
  assert.equal(backupState([{ id: "a" } as never, { id: "b" } as never]), "unknown", "an older edge without providers");
  assert.equal(backupState(null), "unknown");

  assert.equal(needsBackup([bitwarden, { ...bitwarden }], false), true, "all in one provider: suggest another place");
  assert.equal(needsBackup([bitwarden, google], false), false);
  assert.equal(needsBackup([bitwarden, unknown], false), false, "unknown claims nothing either way");
  assert.equal(needsBackup([bitwarden, { ...bitwarden }], true), false);
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
