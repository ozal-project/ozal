// The access log shown on the page is the log of the recorded tour: real
// decisions from a real edge with demo data (scripts/demo/tour.mjs exports
// them). This turns them into rows a visitor can read.

export type Sample = { time: string; who: string; host: string; type: string; code: string; reason: string };
export type Row = { id: string; time: string; who: string; app: string; ok: boolean; status: string; reason: string; detail: string };

const sentence = (text: string) => (text ? text.charAt(0).toUpperCase() + text.slice(1).replace(/\.?$/, ".") : "");

export function rows(samples: Sample[], limit = 8): Row[] {
  const out: Row[] = [];
  for (const s of [...samples].sort((a, b) => b.time.localeCompare(a.time))) {
    const ok = s.type === "access.allowed";
    if (!ok && s.type !== "access.denied") continue;
    // A browser asks for the page and then for /favicon.ico: one decision, one row.
    const previous = out.at(-1);
    if (previous && previous.who === s.who && previous.app === s.host && previous.ok === ok && Math.abs(Date.parse(previous.id) - Date.parse(s.time)) <= 2000) continue;
    out.push({
      id: s.time,
      time: s.time.slice(11, 19),
      who: s.who,
      app: s.host,
      ok,
      status: ok ? "Let in" : "Refused",
      reason: ok ? "The policy allows it" : sentence(s.reason).replace(/\.$/, ""),
      detail: ok
        ? `${s.who} opened ${s.host}. The policy allowed it, so the request went through.`
        : `${s.who} asked for ${s.host}. ${sentence(s.reason)} The person saw a page that says so and leads back to the apps they can open. The decision code is ${s.code}.`,
    });
    if (out.length === limit) break;
  }
  return out;
}
