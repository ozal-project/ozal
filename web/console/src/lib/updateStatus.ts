import type { UpdateStatus } from "$lib/api";

export type AvailableUpdateNotice = { version: string };

export function updateAge(checkedAt: string | undefined, now = Date.now()): "never" | "fresh" | "stale" {
  if (!checkedAt) return "never";
  const timestamp = Date.parse(checkedAt);
  if (!Number.isFinite(timestamp) || timestamp > now + 5 * 60_000) return "stale";
  return now - timestamp > 24 * 60 * 60_000 ? "stale" : "fresh";
}
export function availableUpdateNotice(status: UpdateStatus, now = Date.now()): AvailableUpdateNotice | null {
	if (status.result !== "available" || updateAge(status.checked_at, now) !== "fresh" || !status.latest || !status.checked_at) return null;
	return { version: status.latest };
}
