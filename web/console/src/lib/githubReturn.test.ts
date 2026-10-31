// node --test web/console/src/lib/*.test.ts (scripts/verify.sh)
import { test } from "node:test";
import assert from "node:assert/strict";
import { returningFromGitHub, leaveForGitHub, returnTarget, clearReturn } from "./githubReturn.ts";

const store = new Map<string, string>();
Object.assign(globalThis, { sessionStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => store.set(k, v), removeItem: (k: string) => store.delete(k) } });

test("only GitHub's own return parameters count as coming back", () => {
  assert.equal(returningFromGitHub("?code=abc&state=xyz"), true);
  assert.equal(returningFromGitHub("?setup_action=install&installation_id=1"), true);
  assert.equal(returningFromGitHub("?code=abc"), false);
  assert.equal(returningFromGitHub("?host=app.example.com"), false);
});

test("the console returns to the page the operator left from", () => {
  clearReturn();
  assert.equal(returnTarget(), "/applications");
  leaveForGitHub("/integrations");
  assert.equal(returnTarget(), "/integrations");
  clearReturn();
  assert.equal(returnTarget(), "/applications");
});

test("a stored value that is not a known page falls back to Applications", () => {
  store.set("ozal.github.return", "https://evil.example/");
  assert.equal(returnTarget(), "/applications");
});

test("blocked storage never breaks the return", () => {
  Object.assign(globalThis, { sessionStorage: { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("blocked"); }, removeItem() { throw new Error("blocked"); } } });
  leaveForGitHub("/integrations");
  clearReturn();
  assert.equal(returnTarget(), "/applications");
});
