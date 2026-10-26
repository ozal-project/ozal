import { test } from "node:test";
import assert from "node:assert/strict";
import { rows, type Sample } from "./board.ts";

const at = (second: number, over: Partial<Sample> = {}): Sample => ({
  time: `2026-10-26T01:49:${String(second).padStart(2, "0")}Z`, who: "ana@acme.dev", host: "grafana", type: "access.allowed", code: "", reason: "", ...over,
});

test("rows are newest first, and say what happened in words", () => {
  const list = rows([at(14), at(37, { who: "bob@other.org", type: "access.denied", code: "domain_not_allowed", reason: "account's email domain is not on the allowed list" })]);
  assert.deepEqual(list.map((r) => [r.time, r.who, r.status]), [["01:49:37", "bob@other.org", "Refused"], ["01:49:14", "ana@acme.dev", "Let in"]]);
  assert.equal(list[0].reason, "Account's email domain is not on the allowed list");
  assert.match(list[0].detail, /bob@other\.org asked for grafana\. Account's email domain is not on the allowed list\./);
  assert.match(list[0].detail, /decision code is domain_not_allowed/);
  assert.equal(list[1].reason, "The policy allows it");
});

test("the page request and the favicon request are one row", () => {
  assert.equal(rows([at(19, { host: "billing" }), at(20, { host: "billing" }), at(24, { host: "wiki" })]).length, 2);
  assert.equal(rows([at(10), at(20)]).length, 2, "the same person opening the same app later is another row");
});

test("only decisions become rows, and the board holds at most eight", () => {
  const many = Array.from({ length: 30 }, (_, i) => at(i * 2, { host: `app${i}` }));
  assert.equal(rows(many).length, 8);
  assert.equal(rows([at(1, { type: "login.succeeded" })]).length, 0);
});
