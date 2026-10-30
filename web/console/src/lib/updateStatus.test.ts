import { test } from "node:test";
import assert from "node:assert/strict";
import { availableUpdateNotice, updateAge } from "./updateStatus.ts";

test("update status distinguishes no recorded check, recent and old records", () => {
	const now = Date.parse("2026-10-30T12:00:00Z");
	assert.equal(updateAge(undefined, now), "never");
	assert.equal(updateAge("2026-10-30T11:00:00Z", now), "fresh");
	assert.equal(updateAge("2026-10-29T10:00:00Z", now), "stale");
});

test("only a recent available release creates a toast notice", () => {
	const now = Date.parse("2026-10-30T12:00:00Z");
	assert.deepEqual(availableUpdateNotice({ result: "available", checked_at: "2026-10-30T11:00:00Z", installed: "1.2.0", latest: "1.3.0" }, now), {
		version: "1.3.0",
	});
	assert.equal(availableUpdateNotice({ result: "available", checked_at: "2026-10-29T10:00:00Z", installed: "1.2.0", latest: "1.3.0" }, now), null);
	assert.equal(availableUpdateNotice({ result: "current", checked_at: "2026-10-30T11:00:00Z", installed: "1.3.0", latest: "1.3.0" }, now), null);
	assert.equal(availableUpdateNotice({ result: "available", checked_at: "2026-10-30T11:00:00Z", installed: "1.2.0" }, now), null);
});
