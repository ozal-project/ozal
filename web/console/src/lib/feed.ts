// Live activity bookkeeping (BUG-009). Every event carries "<epoch>-<seq>":
// the epoch names one Ozal process, the sequence counts its events.
// The view keeps an event once per identity (never by time or type: two real
// events can share both), and a jump in the sequence means events this view
// never received: a slow connection, or history the server no longer holds.

export type FeedState = {
  seen: Set<string>;
  epoch: string;
  last: number;
  missed: number;
  restarted: boolean;
  // The current run's first event reached this view with a sequence above 1:
  // earlier events of this run were already gone from the server's history.
  partial: boolean;
};

export const emptyFeed = (): FeedState => ({ seen: new Set(), epoch: "", last: 0, missed: 0, restarted: false, partial: false });

const SEEN_LIMIT = 2000;

export function receive(state: FeedState, id: string | undefined): { state: FeedState; accept: boolean } {
  const match = /^([0-9a-f]+)-(\d+)$/.exec(id ?? "");
  if (!match) return { state, accept: true }; // no identity: cannot dedupe, nor count
  if (state.seen.has(id!)) return { state, accept: false };
  const [, epoch, raw] = match;
  const seq = Number(raw);
  const seen = new Set(state.seen);
  seen.add(id!);
  if (seen.size > SEEN_LIMIT) seen.delete(seen.values().next().value!);
  const next: FeedState = { ...state, seen };
  if (epoch !== state.epoch) {
    // A new process numbers from 1 again; what came before is not a gap.
    next.restarted = state.epoch !== "";
    next.partial = seq > 1;
    next.epoch = epoch;
    next.last = seq;
    return { state: next, accept: true };
  }
  if (state.last > 0 && seq > state.last + 1) next.missed = state.missed + (seq - state.last - 1);
  next.last = Math.max(state.last, seq);
  return { state: next, accept: true };
}
