export type RequestRun = {
  signal: AbortSignal;
  isCurrent(): boolean;
  timedOut(): boolean;
  finish(): void;
};

// LatestRequest stops an old poll when a newer poll starts and still protects
// the UI from APIs that finish after their abort signal was sent.
export class LatestRequest {
  private active: { controller: AbortController; timer: ReturnType<typeof setTimeout>; timedOut: boolean } | null = null;

  start(timeoutMs = 15_000): RequestRun {
    this.cancel();
    const request = {
      controller: new AbortController(),
      timer: setTimeout(() => {
        request.timedOut = true;
        request.controller.abort(new DOMException("Request timed out", "TimeoutError"));
      }, timeoutMs),
      timedOut: false,
    };
    this.active = request;
    return {
      signal: request.controller.signal,
      isCurrent: () => this.active === request,
      timedOut: () => request.timedOut,
      finish: () => {
        clearTimeout(request.timer);
        if (this.active === request) this.active = null;
      },
    };
  }

  cancel(): void {
    if (!this.active) return;
    clearTimeout(this.active.timer);
    this.active.controller.abort(new DOMException("Request stopped", "AbortError"));
    this.active = null;
  }
}
