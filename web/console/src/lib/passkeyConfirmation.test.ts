import { test } from "node:test";
import assert from "node:assert/strict";
import { confirmWithPasskey } from "./passkey.ts";
import { withPasskeyProof } from "./accessRequests.ts";

test("HTTPS confirmation navigates without retrying a mutation with the old session", async () => {
  const originalFetch = globalThis.fetch;
  const seen: string[] = [];
  const went: string[] = [];
  const globals = globalThis as unknown as Record<string, unknown>;
  const originals = new Map(["location", "sessionStorage"].map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  Object.defineProperty(globalThis, "location", { configurable: true, value: { assign: (url: string) => went.push(url) } });
  Object.defineProperty(globalThis, "sessionStorage", { configurable: true, value: { setItem() {} } });
  let mutations = 0;
  try {
    globalThis.fetch = (async (url: string) => {
      seen.push(url);
      if (url.endsWith("/step-up/begin")) return new Response(JSON.stringify({ code: "login_host_required", error: "confirm on the HTTPS site" }), { status: 409 });
      if (url.endsWith("/state")) return new Response(JSON.stringify({ login_host: "https://auth.example.com" }));
      if (url.endsWith("/remote/start")) return new Response(JSON.stringify({ url: "https://auth.example.com/_/auth/admin?tx=confirm" }));
      assert.fail(`unexpected call ${url}`);
    }) as typeof fetch;
    const confirmation = withPasskeyProof("old-session", async () => {
      mutations++;
      throw Object.assign(new Error("proof required"), { code: "passkey_required" });
    }, confirmWithPasskey);
    const result = await Promise.race([
      confirmation.then(() => "retried"),
      new Promise((resolve) => setTimeout(() => resolve("navigating"), 50)),
    ]);
    assert.equal(result, "navigating");
    assert.equal(mutations, 1);
    assert.deepEqual(went, ["https://auth.example.com/_/auth/admin?tx=confirm"]);
    assert.deepEqual(seen, ["/_/api/admin/step-up/begin", "/_/api/admin/state", "/_/api/admin/remote/start"]);
  } finally {
    globalThis.fetch = originalFetch;
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globals[key];
    }
  }
});
