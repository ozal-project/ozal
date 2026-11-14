import assert from "node:assert/strict";
import test from "node:test";
import { passkeyName, passkeyPlace } from "./passkeyName.ts";

const base = { id: "a", created_at: "2026-11-14T00:00:00Z" };

test("a passkey is called by its owner's name, else its known provider", () => {
  assert.equal(passkeyName({ ...base, label: "YubiKey", provider: "Bitwarden", provider_known: true }), "YubiKey");
  assert.equal(passkeyName({ ...base, provider: "Bitwarden", provider_known: true }), "Bitwarden");
  assert.equal(passkeyPlace({ ...base, label: "Work", provider: "Bitwarden", provider_known: true }), "Bitwarden");
  assert.equal(passkeyPlace({ ...base, provider: "Bitwarden", provider_known: true }), "", "not repeated under its own name");
});

test("a kind of passkey is never shown as the place it lives", () => {
  const synced = { ...base, provider: "Synced passkey", provider_known: false };
  assert.equal(passkeyName(synced), "Passkey");
  assert.equal(passkeyPlace(synced), "");
  assert.equal(passkeyName({ ...base }), "Passkey", "an older edge without providers");
});
