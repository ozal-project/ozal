// node --test --experimental-strip-types src/lib/tracker.test.ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { closures, doneIds, openTasks, shortTitle, tracks } from "./tracker.ts";

const todo = `
| ID | Task | State | Done when |
|---|---|---|---|
| \`TUN-002\` | Named persistent connectors: configuration and reload. | In progress; merged | x |
| \`ID-005\` | Phase 2 of the [native login](architecture/identity-login.md) (step-up). | Planned (after \`ID-003\`) | x |
| \`CTX-007\` | Denial with a way out | Idea (2026-10-22) | x |
| \`REL-007\` | Dependency advisories | Blocked on upstream fix | x |
| \`ARCH-009\` | Identity integration with apps that already authenticate | Ready (decision pending) | x |
| \`EDGE-004\` | Multiple upstreams \`a|b\` per route | Implemented 2026-10-21, not deployed | x |
| \`TUN-002\` | A duplicate row later in the file | Planned | x |
`;

const implemented = `
## PAGES-008 — authenticated project deploy endpoint — 2026-10-25 (\`adbc3d1\`)
## TUN-002 (part) — one connector, several hosts — 2026-10-19
## Platform-aware migration fixtures — 2026-10-21 (committed with this change)
## Phase 3 — control plane (2026-09-21)
## Go toolchain 1.27.3 (GO-2026-6809) — 2026-10-21

| \`SEC-001\` | a finding | closed |
| \`UI-002\` | console app | closed |
`;

test("open tasks keep their state and first clause, once each", () => {
  const open = openTasks(todo);
  assert.deepEqual(open.map((t) => [t.id, t.state]), [
    ["TUN-002", "progress"], ["ID-005", "planned"], ["CTX-007", "idea"],
    ["REL-007", "blocked"], ["ARCH-009", "ready"], ["EDGE-004", "progress"],
  ]);
  assert.equal(open[0].title, "Named persistent connectors");
  assert.equal(open[1].title, "Phase 2 of the native login", "links are reduced to their text");
});

test("a task closed in part stays open, and only real areas count", () => {
  const open = openTasks(todo);
  const done = doneIds(open, implemented);
  assert.ok(done.has("PAGES-008"));
  assert.ok(done.has("UI-002"), "IDs in closure tables count");
  assert.ok(!done.has("TUN-002"), "TUN-002 (part) is still open in TO_IMPLEMENT");
  assert.ok(!done.has("GO-2026"), "an advisory number is not a task");
  assert.ok(!done.has("SEC-001"), "SEC is not a roadmap track");
});

test("closures read both heading styles, newest first", () => {
  const log = closures(implemented);
  assert.equal(log.length, 5);
  assert.deepEqual(log[0], { date: "2026-10-25", id: "PAGES-008", title: "Authenticated project deploy endpoint" });
  assert.ok(log.some((c) => c.title === "Phase 3 — control plane" && c.id === ""), "an entry without an ID keeps its whole title");
  assert.equal(log.at(-1)?.date, "2026-09-21");
});

test("tracks group by area and put work in progress first", () => {
  const open = openTasks(todo);
  const byTrack = tracks(open, doneIds(open, implemented));
  const edge = byTrack.find((t) => t.name === "Edge & tunnels")!;
  assert.deepEqual(edge.open.map((t) => t.id), ["TUN-002", "EDGE-004"]);
  assert.equal(byTrack.find((t) => t.name === "Sites & deploys")?.closed, 1);
  assert.ok(!byTrack.some((t) => t.open.length + t.closed === 0), "empty tracks are left out");
});

test("long titles are cut at a word", () => {
  const t = shortTitle("Validate Cloudflare proxy and Ozal coexistence and a measurable path to reduce provider dependence over time");
  assert.ok(t.length <= 84 && t.endsWith("…") && !t.endsWith(" …"));
});
