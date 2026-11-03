import { test } from "node:test";
import assert from "node:assert/strict";
import { initials, passkeyLine, toolSummary } from "./accountMenu.ts";

test("initials take the first and last word", () => {
  assert.equal(initials("Octavio Santacruz"), "OS");
  assert.equal(initials("ana"), "A");
  assert.equal(initials("  Maria de la Paz  "), "MP");
  assert.equal(initials(""), "?");
});

test("the tool summary names the usual grants in words", () => {
  const all = ["administrators", "applications", "connectors", "people", "publishing", "sites", "updates", "updates.install"];
  assert.equal(toolSummary(all), "every tool");
  assert.equal(toolSummary(all.filter((t) => t !== "updates.install")), "every tool except installing updates");
  assert.equal(toolSummary(["people", "sites"]), "2 of 8 tools");
  assert.equal(toolSummary([]), "no tools");
});

test("a passkey line says which one opened this session", () => {
  const base = { id: "a", created_at: "2026-11-02T20:00:00Z" };
  assert.deepEqual(passkeyLine({ ...base, label: "Chrome on macOS", current: true }, "en-US"), { name: "Chrome on macOS", detail: "added Nov 2", current: true });
  assert.equal(passkeyLine({ ...base, last_used_at: "2026-11-03T01:00:00Z" }, "en-US").detail, "added Nov 2 · last used Nov 3");
  assert.equal(passkeyLine({ ...base, label: "  " }, "en-US").name, "Passkey", "a blank label is not a name");
  assert.equal(passkeyLine(base, "en-US").detail, "added Nov 2 · not used yet");
});
