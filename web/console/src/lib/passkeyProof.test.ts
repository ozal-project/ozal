import { test } from "node:test";
import assert from "node:assert/strict";
import { addPasskey, issueDeviceLink } from "./passkey.ts";

const reply = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status });
const proofRequired = () => reply({ error: "Confirm it's you", code: "passkey_required" }, 403);
const options = { ceremony: "registration", options: { publicKey: { challenge: "AAAA", user: { id: "AQ" } } } };
const proofOptions = { ceremony: "proof", options: { publicKey: { challenge: "AAAA" } } };
const created = {
  id: "new", rawId: new Uint8Array([2]).buffer, type: "public-key",
  getClientExtensionResults: () => ({}),
  response: { clientDataJSON: new Uint8Array([3]).buffer, attestationObject: new Uint8Array([4]).buffer },
};
const asserted = {
  id: "existing", rawId: new Uint8Array([1]).buffer, type: "public-key",
  getClientExtensionResults: () => ({}),
  response: { clientDataJSON: new Uint8Array([3]).buffer, authenticatorData: new Uint8Array([4]).buffer, signature: new Uint8Array([5]).buffer },
};

async function browser(run: (state: { creates: number; confirms: number; cancel: boolean }) => Promise<void>) {
  const originalFetch = globalThis.fetch;
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const state = { creates: 0, confirms: 0, cancel: false };
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { credentials: {
    create: async () => { state.creates++; return created; },
    get: async () => {
      state.confirms++;
      if (state.cancel) throw new DOMException("closed", "NotAllowedError");
      return asserted;
    },
  } } });
  try { await run(state); }
  finally {
    globalThis.fetch = originalFetch;
    if (originalNavigator) Object.defineProperty(globalThis, "navigator", originalNavigator);
    else Reflect.deleteProperty(globalThis, "navigator");
  }
}

for (const action of ["add", "link"]) {
  test(`${action}: an old proof confirms once before creating access`, async () => browser(async (state) => {
    let confirmed = false;
    const calls: string[] = [];
    globalThis.fetch = (async (url: string, init: RequestInit) => {
      calls.push(url);
      assert.equal((init.headers as Record<string, string>).Authorization, "Bearer session");
      if (url.endsWith("/step-up/begin")) return reply(proofOptions);
      if (url.endsWith("/step-up/finish")) { confirmed = true; return reply({ verified: true }); }
      if (url.endsWith("/passkeys/begin")) return confirmed ? reply(options) : proofRequired();
      if (url.endsWith("/passkeys/finish")) return reply({ added: true });
      if (url.endsWith("/admin/devices")) return confirmed ? reply({ token: "one-use", expires: "later" }) : proofRequired();
      assert.fail(`unexpected request ${url}`);
    }) as typeof fetch;
    if (action === "add") await addPasskey("session");
    else assert.equal((await issueDeviceLink("session")).token, "one-use");
    assert.equal(state.confirms, 1);
    assert.equal(state.creates, action === "add" ? 1 : 0);
    const path = action === "add" ? "/_/api/admin/passkeys/begin" : "/_/api/admin/devices";
    assert.deepEqual(calls.slice(0, 4), [path, "/_/api/admin/step-up/begin", "/_/api/admin/step-up/finish", path]);
  }));
}

test("an expiring registration proof retries the same response without recreating the passkey", async () => browser(async (state) => {
  let confirmed = false;
  const bodies: string[] = [];
  let begins = 0;
  globalThis.fetch = (async (url: string, init: RequestInit) => {
    if (url.endsWith("/passkeys/begin")) { begins++; return reply(options); }
    if (url.endsWith("/passkeys/finish")) {
      assert.equal((init.headers as Record<string, string>)["X-Ozal-Ceremony"], "registration");
      bodies.push(init.body as string);
      return confirmed ? reply({ added: true }) : proofRequired();
    }
    if (url.endsWith("/step-up/begin")) return reply(proofOptions);
    if (url.endsWith("/step-up/finish")) { confirmed = true; return reply({ verified: true }); }
    assert.fail(`unexpected request ${url}`);
  }) as typeof fetch;
  await addPasskey("session");
  assert.equal(begins, 1);
  assert.equal(state.creates, 1);
  assert.equal(state.confirms, 1);
  assert.equal(bodies.length, 2);
  assert.equal(bodies[0], bodies[1]);
}));

test("cancelled confirmation creates no passkey or device link", async () => browser(async (state) => {
  state.cancel = true;
  let mutations = 0;
  globalThis.fetch = (async (url: string) => {
    if (url.endsWith("/step-up/begin")) return reply(proofOptions);
    mutations++;
    return proofRequired();
  }) as typeof fetch;
  await assert.rejects(addPasskey("session"), { name: "NotAllowedError" });
  await assert.rejects(issueDeviceLink("session"), { name: "NotAllowedError" });
  assert.equal(mutations, 2, "each original request was refused and never retried");
  assert.equal(state.creates, 0);
}));

test("confirmation retries once and an ordinary refusal does not trigger it", async () => browser(async (state) => {
  let mutations = 0;
  globalThis.fetch = (async (url: string) => {
    if (url.endsWith("/step-up/begin")) return reply(proofOptions);
    if (url.endsWith("/step-up/finish")) return reply({ verified: true });
    mutations++;
    return proofRequired();
  }) as typeof fetch;
  await assert.rejects(issueDeviceLink("session"), { code: "passkey_required" });
  assert.equal(mutations, 2);
  assert.equal(state.confirms, 1);
  globalThis.fetch = (async () => reply({ error: "Forbidden" }, 403)) as typeof fetch;
  await assert.rejects(addPasskey("session"), /Forbidden/);
  assert.equal(state.confirms, 1);
  assert.equal(state.creates, 0);
}));
