import { test } from "node:test";
import assert from "node:assert/strict";
import { EventDecoder, openEventStream } from "./eventStream.ts";

test("decodes split event records and ignores heartbeat comments", () => {
  const decoder = new EventDecoder();
  assert.deepEqual(decoder.push(": ping\n\nid: ab-1\ndata: {\"type\":"), []);
  assert.deepEqual(decoder.push("\"access.allowed\"}\n\nid: ab-2\r\ndata: first\r\ndata: second\r\n\r\n"), [
    { id: "ab-1", data: '{"type":"access.allowed"}' },
    { id: "ab-2", data: "first\nsecond" },
  ]);
});

test("rejects an unbounded incomplete record", () => {
  const decoder = new EventDecoder();
  assert.throws(() => decoder.push("x".repeat(1_048_577)), /too large/);
});

test("reconnects with the last event ID and puts the credential only in the header", async () => {
  const originalFetch = globalThis.fetch;
  const requests: { url: string; headers: Headers }[] = [];
  const encoder = new TextEncoder();
  globalThis.fetch = async (input, init) => {
    requests.push({ url: String(input), headers: new Headers(init?.headers) });
    const id = requests.length === 1 ? "ab-1" : "ab-2";
    const body = new ReadableStream({ start(controller) { controller.enqueue(encoder.encode(`id: ${id}\ndata: {"type":"test"}\n\n`)); controller.close(); } });
    return new Response(body, { headers: { "Content-Type": "text/event-stream" } });
  };
  let stop = () => {};
  try {
    const received = new Promise<string[]>((resolve) => {
      const ids: string[] = [];
      stop = openEventStream({ token: "synthetic-token", onState: () => {}, onEvent: (event) => { ids.push(event.id); if (ids.length === 2) resolve(ids); } });
    });
    const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error("reconnect timed out")), 5000));
    assert.deepEqual(await Promise.race([received, timeout]), ["ab-1", "ab-2"]);
    assert.equal(requests[0].url, "/_/events");
    assert.equal(requests[0].headers.get("Authorization"), "Bearer synthetic-token");
    assert.equal(requests[1].headers.get("Last-Event-ID"), "ab-1");
  } finally { stop(); globalThis.fetch = originalFetch; }
});

test("a refused credential stops the stream instead of retrying", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response("", { status: 401 }); };
  try {
    const denied = new Promise<void>((resolve) => { openEventStream({ token: "revoked", onState: () => {}, onEvent: () => {}, onDenied: resolve }); });
    await denied;
    await new Promise((resolve) => setTimeout(resolve, 2500));
    assert.equal(calls, 1);
  } finally { globalThis.fetch = originalFetch; }
});
