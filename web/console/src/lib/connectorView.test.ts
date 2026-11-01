import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { connectorCommandName, connectorDaysLeft, connectorProgress, connectorState, validConnectorName } from "./connectorView.ts";
import type { Connector } from "./api.ts";

const base: Connector = { name: "home-edge", sha256: "a".repeat(64), created: "2026-10-01T00:00:00Z", hosts: [], connected: false };

describe("connector view state", () => {
  it("accepts only lowercase host names with single internal dashes", () => {
    for (const name of ["edge", "home-2", "a0-b9"]) assert.equal(validConnectorName(name), true);
    for (const name of ["", "Home", "-edge", "edge-", "edge--one", "edge_one", "edge.one"]) assert.equal(validConnectorName(name), false);
  });

  it("never puts unvalidated host names into shell commands", () => {
    assert.equal(connectorCommandName("homelab-2"), "homelab-2");
    assert.equal(connectorCommandName("edge; touch /tmp/pwned"), "NAME");
    assert.equal(connectorCommandName("Home"), "NAME");
  });

  it("distinguishes revoked, connected, assigned and idle connectors", () => {
    assert.equal(connectorState({ ...base, revoked: "2026-10-30T00:00:00Z", connected: true }), "revoked");
    assert.equal(connectorState({ ...base, connected: true }), "connected");
    assert.equal(connectorState({ ...base, hosts: ["app.example.test"] }), "waiting");
    assert.equal(connectorState(base), "idle");
  });

  it("counts expired certificates as negative days and unknown expiry as null", () => {
    const now = Date.parse("2026-11-01T00:00:00Z");
    assert.equal(connectorDaysLeft({ ...base, not_after: "2026-10-31T00:00:00Z" }, now), -1);
    assert.equal(connectorDaysLeft({ ...base, not_after: "2026-11-11T00:00:00Z" }, now), 10);
    assert.equal(connectorDaysLeft(base, now), null);
    assert.equal(connectorDaysLeft({ ...base, not_after: "invalid" }, now), null);
  });

  it("marks only observed enrolment and connection as completed guide steps", () => {
    assert.equal(connectorProgress(), 0);
    assert.equal(connectorProgress(base), 4);
    assert.equal(connectorProgress({ ...base, connected: true }), 5);
  });
});
