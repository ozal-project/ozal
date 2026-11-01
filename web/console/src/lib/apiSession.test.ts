import assert from "node:assert/strict";
import { test } from "node:test";
import { configureAPISession, sessionFetch } from "./apiSession.ts";

test("API sends the composed session credential and ends only that credential on 401", async () => {
  const original = globalThis.fetch;
  let token = "react-session";
  let ended = 0;
  let sent = "";
  configureAPISession({ token: () => token, unauthorized: () => { ended++; } });
  globalThis.fetch = async (_input, init) => {
    sent = new Headers(init?.headers).get("Authorization") ?? "";
    return new Response("{}", { status: 401 });
  };
  try {
    await sessionFetch("/_/api/status");
    assert.equal(sent, "Bearer react-session");
    assert.equal(ended, 1);

    globalThis.fetch = async () => {
      token = "new-session";
      return new Response("{}", { status: 401 });
    };
    await sessionFetch("/_/api/status");
    assert.equal(ended, 1, "a late 401 must not end a newer session");
  } finally {
    configureAPISession({ token: () => "", unauthorized: () => {} });
    globalThis.fetch = original;
  }
});
