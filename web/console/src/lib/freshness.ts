// Milliseconds since a reading. The reading and the ticking clock both come
// from this tab's Date.now(), so a reading taken just after the last tick would
// be "in the future": that is lag, not an unknown time.
export const elapsed = (nowMs: number, observedAt: number): number => Math.max(0, nowMs - observedAt);

export function ageLabel(ageMs: number): string {
  if (!Number.isFinite(ageMs) || ageMs < 0) return "time unknown";
  if (ageMs < 10_000) return "just now";
  if (ageMs < 60_000) return `${Math.floor(ageMs / 1_000)} seconds ago`;
  const minutes = Math.floor(ageMs / 60_000);
  return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
}

export function isStale(ageMs: number, failed: boolean, staleAfterMs = 60_000): boolean {
  return failed || !Number.isFinite(ageMs) || ageMs > staleAfterMs;
}
