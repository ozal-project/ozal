// The console streams events with a bearer header, never in a URL. The edge
// emits UTF-8 SSE records with id and data fields. Bound partial input so an
// interrupted or malformed stream cannot grow this tab's memory forever.
export type StreamEvent = { id: string; data: string };
const MAX_BUFFER = 1_048_576;

export class EventDecoder {
  private buffer = "";
  private id = "";
  private data: string[] = [];
  private recordSize = 0;

  push(chunk: string): StreamEvent[] {
    this.buffer += chunk;
    if (this.buffer.length > MAX_BUFFER) throw new Error("event stream record too large");
    const out: StreamEvent[] = [];
    let end: number;
    while ((end = this.buffer.indexOf("\n")) >= 0) {
      let line = this.buffer.slice(0, end);
      this.buffer = this.buffer.slice(end + 1);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      if (!line) {
        if (this.data.length) out.push({ id: this.id, data: this.data.join("\n") });
        this.data = []; this.recordSize = 0;
      } else if (!line.startsWith(":")) {
        this.recordSize += line.length + 1;
        if (this.recordSize > MAX_BUFFER) throw new Error("event stream record too large");
        const split = line.indexOf(":");
        const field = split < 0 ? line : line.slice(0, split);
        let value = split < 0 ? "" : line.slice(split + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        if (field === "id" && !value.includes("\0")) this.id = value;
        if (field === "data") this.data.push(value);
      }
    }
    return out;
  }
}

type StreamOptions = {
  token: string;
  onEvent: (event: StreamEvent) => void;
  onState: (state: "connecting" | "live" | "reconnected" | "lost") => void;
  // The edge refused the credential (revoked, expired): stop retrying.
  onDenied?: () => void;
};

export function openEventStream({ token, onEvent, onState, onDenied }: StreamOptions): () => void {
  const controller = new AbortController();
  let lastID = "";
  let connected = false;
  const wait = () => new Promise<void>((resolve) => {
    const done = () => { clearTimeout(timer); controller.signal.removeEventListener("abort", done); resolve(); };
    const timer = setTimeout(done, 2000);
    controller.signal.addEventListener("abort", done, { once: true });
    if (controller.signal.aborted) done();
  });
  async function run() {
    while (!controller.signal.aborted) {
      try {
        const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
        if (lastID) headers["Last-Event-ID"] = lastID;
        const response = await fetch("/_/events", { headers, cache: "no-store", signal: controller.signal });
        if (controller.signal.aborted) break;
        if (response.status === 401) {
          await response.body?.cancel();
          controller.abort();
          onDenied?.();
          break;
        }
        if (!response.ok || !response.body || !response.headers.get("content-type")?.startsWith("text/event-stream")) {
          await response.body?.cancel();
          throw new Error("event stream unavailable");
        }
        onState(connected ? "reconnected" : "live");
        connected = true;
        const reader = response.body.getReader();
        const text = new TextDecoder();
        const decoder = new EventDecoder();
        try {
          while (!controller.signal.aborted) {
            const { done, value } = await reader.read();
            if (done || controller.signal.aborted) break;
            for (const event of decoder.push(text.decode(value, { stream: true }))) {
              if (event.id) lastID = event.id;
              onEvent(event);
            }
          }
        } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      } catch {
        // Report connectivity, never the credential or a raw network error.
      }
      if (!controller.signal.aborted) { onState("lost"); await wait(); }
    }
  }
  onState("connecting");
  void run();
  return () => controller.abort();
}
