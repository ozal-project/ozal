import assert from "node:assert/strict";
import test from "node:test";
import { allowed, groups, placeOf, target, visibleGroups } from "./navigation.ts";

test("every page has one place, tabs included", () => {
  assert.deepEqual([placeOf("/routes").group, placeOf("/routes").section.label], ["Access", "Applications"]);
  assert.deepEqual([placeOf("/groups").group, placeOf("/groups").section.label], ["Access", "People"]);
  assert.deepEqual([placeOf("/administrators").group, placeOf("/administrators").section.label], ["Settings", "Administrators"]);
  assert.equal(placeOf("/").section.label, "Overview");
  assert.equal(placeOf("/nowhere").section.label, "Overview", "an unknown path is the Overview, as the router sends it");
  const paths = groups.flatMap((g) => g.sections.flatMap((s) => [s.path, ...(s.also ?? [])]));
  assert.equal(new Set(paths).size, paths.length, "no path belongs to two sections");
});

test("a session sees only the sections its tools open", () => {
  const names = (tools: string[]) => visibleGroups(tools).flatMap((g) => g.sections.map((s) => s.label));
  assert.deepEqual(names([]), ["Overview", "Applications", "Sites", "Activity", "Integrations"]);
  assert.ok(names(["people", "administrators"]).includes("Administrators"));
  assert.ok(!names(["people"]).includes("Administrators"));
  assert.equal(visibleGroups([]).some((g) => g.name === "Network"), false, "an empty group is not shown");
  const domains = groups.flatMap((g) => g.sections).find((s) => s.path === "/domains")!;
  assert.equal(allowed(domains, ["applications"]), true);
});

test("Applications falls back to its read-only view without the tool", () => {
  const apps = placeOf("/applications").section;
  assert.equal(target(apps, []), "/routes");
  assert.equal(target(apps, ["applications"]), "/applications");
});
