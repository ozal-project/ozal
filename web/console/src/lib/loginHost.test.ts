import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { challengeFor, finishLoginHost, newVerifier, startLoginHost } from "./loginHost.ts";

test("the edge receives only the SHA-256 of a fresh verifier", async () => {
  const a = newVerifier(), b = newVerifier();
  assert.notEqual(a, b);
  assert.match(a, /^[A-Za-z0-9_-]{43}$/, "256 random bits, base64url");
  const expected = createHash("sha256").update(a).digest("base64url");
  assert.equal(await challengeFor(a), expected);
});

test("the verifier stays in this tab and is used once", async () => {
  const stored = new Map<string, string>();
  const store = { setItem: (k: string, v: string) => void stored.set(k, v), getItem: (k: string) => stored.get(k) ?? null, removeItem: (k: string) => void stored.delete(k) };
  const sent: Record<string, unknown>[] = [];
  globalThis.fetch = (async (_url: string, init: RequestInit) => {
    sent.push(JSON.parse(String(init.body)));
    return new Response(JSON.stringify(sent.length === 1 ? { url: "https://auth.example.com/_/auth/admin?tx=t" } : { token: "s" }), { status: 200 });
  }) as typeof fetch;
  const url = await startLoginHost({ token: "enrol-link", hints: ["hybrid"] }, store);
  assert.equal(url, "https://auth.example.com/_/auth/admin?tx=t");
  const verifier = [...stored.values()][0];
  assert.equal(sent[0].verifier, undefined, "the verifier never leaves the tab at the start");
  assert.equal(sent[0].challenge, await challengeFor(verifier));
  assert.equal(sent[0].token, "enrol-link");

  await finishLoginHost("code", store);
  assert.equal(sent[1].verifier, verifier);
  assert.equal(stored.size, 0, "used once");
  await assert.rejects(finishLoginHost("code", store), /another tab/, "a tab without the verifier cannot redeem");
});
