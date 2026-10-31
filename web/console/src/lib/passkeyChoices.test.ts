import { test } from "node:test";
import assert from "node:assert/strict";
import { choices, passkeyChoices, readCapabilities, recommendedChoice, withHints } from "./passkeyChoices.ts";

const all = ["device", "manager", "phone", "key"];

test("capabilities reorder and recommend the passkey choices; none is hidden", () => {
  assert.deepEqual(passkeyChoices(null), all);
  assert.equal(recommendedChoice(null), "", "nothing is recommended without an answer");
  assert.deepEqual(passkeyChoices({ userVerifyingPlatformAuthenticator: false, hybridTransport: true }), ["manager", "phone", "key", "device"]);
  assert.equal(recommendedChoice({ userVerifyingPlatformAuthenticator: false }), "manager", "no built-in authenticator: a password manager");
  assert.deepEqual(passkeyChoices({ passkeyPlatformAuthenticator: true, hybridTransport: false }), ["device", "manager", "key", "phone"]);
  assert.equal(recommendedChoice({ passkeyPlatformAuthenticator: true }), "device");
  assert.deepEqual(passkeyChoices({}), all, "an incomplete answer keeps the default order");
});

test("each place becomes a browser hint; the password manager adds none", () => {
  assert.deepEqual(withHints({ challenge: "x" }, choices.phone.hints), { challenge: "x", hints: ["hybrid"] });
  assert.deepEqual(withHints({ challenge: "x" }, choices.key.hints), { challenge: "x", hints: ["security-key"] });
  assert.deepEqual(withHints({ challenge: "x" }, choices.device.hints), { challenge: "x", hints: ["client-device"] });
  assert.deepEqual(withHints({ challenge: "x" }, choices.manager.hints), { challenge: "x" });
  assert.match(choices.manager.detail, /or any other/, "products are examples, not requirements");
});

test("a browser that cannot, fails to or never answers is unknown, not unsupported", async () => {
  assert.equal(await readCapabilities(undefined), null);
  assert.equal(await readCapabilities({ getClientCapabilities: async () => { throw new Error("denied"); } }), null);
  assert.equal(await readCapabilities({ getClientCapabilities: () => new Promise(() => {}) }, 10), null);
  assert.deepEqual(await readCapabilities({ isUserVerifyingPlatformAuthenticatorAvailable: async () => false }), { userVerifyingPlatformAuthenticator: false });
  assert.deepEqual(await readCapabilities({ getClientCapabilities: async () => ({ hybridTransport: true }) }), { hybridTransport: true });
});
