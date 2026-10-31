import { test } from "node:test";
import assert from "node:assert/strict";
import { passkeyChoices, readCapabilities } from "./passkeyChoices.ts";

const all = ["device", "manager", "phone", "key"];

test("capabilities only reorder the passkey choices; none is hidden", () => {
  assert.deepEqual(passkeyChoices(null), all);
  assert.deepEqual(passkeyChoices({ userVerifyingPlatformAuthenticator: false, hybridTransport: true }), ["phone", "manager", "key", "device"]);
  assert.deepEqual(passkeyChoices({ passkeyPlatformAuthenticator: true, hybridTransport: false }), ["device", "manager", "key", "phone"]);
  assert.deepEqual(passkeyChoices({}), all, "an incomplete answer keeps the default order");
});

test("a browser that cannot, fails to or never answers is unknown, not unsupported", async () => {
  assert.equal(await readCapabilities(undefined), null);
  assert.equal(await readCapabilities({ getClientCapabilities: async () => { throw new Error("denied"); } }), null);
  assert.equal(await readCapabilities({ getClientCapabilities: () => new Promise(() => {}) }, 10), null);
  assert.deepEqual(await readCapabilities({ isUserVerifyingPlatformAuthenticatorAvailable: async () => false }), { userVerifyingPlatformAuthenticator: false });
  assert.deepEqual(await readCapabilities({ getClientCapabilities: async () => ({ hybridTransport: true }) }), { hybridTransport: true });
});
