import { test } from "node:test";
import assert from "node:assert/strict";
import { adminBadge, adminDetails, passkeyName } from "./personAccounts.ts";

test("the badge says the email is also an administrator, or a disabled one", () => {
  assert.equal(adminBadge({}), "Also an administrator");
  assert.equal(adminBadge({ disabled: true }), "Administrator · disabled");
});

test("without the administrators tool no account detail is shown", () => {
  assert.equal(adminDetails({}), null);
  assert.equal(adminDetails({ disabled: true }), null);
});

test("the details name the tools, the console passkeys and a missing backup", () => {
  const all = ["administrators", "applications", "connectors", "people", "publishing", "sites", "updates"];
  const one = adminDetails({ id: "a1", tools: all, passkeys: 1 }, "en-US");
  assert.deepEqual(one?.rows.slice(0, 2), [["Tools", "Every tool except installing updates"], ["Console passkeys", "1 passkey"]]);
  assert.deepEqual(one?.rows[2], ["Last sign-in", "never"]);
  assert.equal(one?.noBackup, true, "a single console passkey has no backup");
  const two = adminDetails({ id: "a1", tools: ["people"], passkeys: 2, last_used: "2026-11-03T01:24:00Z" }, "en-US");
  assert.equal(two?.rows[1][1], "2 passkeys");
  assert.match(two?.rows[2][1] ?? "", /Nov 3/);
  assert.equal(two?.noBackup, false);
  assert.equal(adminDetails({ id: "a1", passkeys: 1, disabled: true })?.noBackup, false, "a disabled account needs no backup warning");
});

test("a passkey shows its owner's name and keeps the provider beside it", () => {
  assert.deepEqual(passkeyName({ provider: "Bitwarden", label: "Work laptop" }), { name: "Work laptop", provider: "Bitwarden" });
  assert.deepEqual(passkeyName({ provider: "Bitwarden", label: "Bitwarden" }), { name: "Bitwarden", provider: null });
  assert.deepEqual(passkeyName({ provider: "Apple Passwords" }), { name: "Apple Passwords", provider: null });
});
