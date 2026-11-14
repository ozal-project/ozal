import assert from "node:assert/strict";
import test from "node:test";
import { rank, score, type Result } from "./search.ts";

const items: Result[] = [
  { kind: "section", label: "People", detail: "Who can sign in", to: "/people" },
  { kind: "section", label: "Domains", detail: "Domains, DNS and certificates", to: "/domains" },
  { kind: "application", label: "wiki.example.com", detail: "Private", to: "/routes?host=wiki.example.com" },
  { kind: "person", label: "Ana Pérez", detail: "ana@example.com", to: "/people?person=ana%40example.com" },
  { kind: "domain", label: "example.com", detail: "Certificate", to: "/domains" },
];

test("a whole match beats a prefix, which beats a word start or a substring", () => {
  assert.ok(score("people", "People") > score("peo", "People"));
  assert.ok(score("peo", "People") > score("ex", "wiki.example.com"));
  assert.ok(score("ex", "wiki.example.com") > score("xam", "wiki.example.com"));
  assert.equal(score("zzz", "People"), 0);
  assert.equal(score("", "People"), 0);
});

test("every word must match, in the label or the detail", () => {
  assert.ok(score("ana example", "Ana Pérez", "ana@example.com") > 0, "the email is searchable");
  assert.equal(score("ana wiki", "Ana Pérez", "ana@example.com"), 0);
});

test("results are ranked, places first on ties, and an empty query lists sections", () => {
  assert.deepEqual(rank("example", items).map((r) => r.label), ["example.com", "wiki.example.com", "Ana Pérez"]);
  assert.deepEqual(rank("", items).map((r) => r.kind), ["section", "section"]);
  assert.deepEqual(rank("domain", items).map((r) => r.label), ["Domains"]);
  assert.equal(rank("a", items, 2).length, 2);
});

test("typed punctuation is matched literally", () => {
  assert.doesNotThrow(() => rank("(wiki", items));
  assert.deepEqual(rank("wiki.", items).map((r) => r.label), ["wiki.example.com"]);
});
