// The page state after one load of a polled resource. Every load decides the
// whole state, success included: a flag set by one failure (Pages not enabled
// on this edge) must clear when a later load succeeds (BUG-011).

export type LoadState = { disabled: boolean; error: string };

export type LoadOutcome = { ok: true } | { ok: false; disabled: boolean; message: string };

export function afterLoad(outcome: LoadOutcome): LoadState {
  if (outcome.ok) return { disabled: false, error: "" };
  return { disabled: outcome.disabled, error: outcome.message };
}
