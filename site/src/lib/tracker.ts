// Parses the project trackers, which the page imports as raw text at build
// time: the roadmap and changelog on the page are the trackers themselves, a
// rebuild is the only way to update them, and nothing is retyped by hand.

export type State = "progress" | "ready" | "blocked" | "planned" | "idea";

export interface Task {
  id: string;
  area: string;
  title: string;
  state: State;
}

export interface Track {
  name: string;
  blurb: string;
  areas: string[];
  open: Task[];
  closed: number;
}

export interface Closure {
  date: string;
  id: string;
  title: string;
}

const ID = /^`?([A-Z]+)-(\d+)`?$/;

// A tracker title is a full sentence with links and code; the page shows its
// first clause.
export function shortTitle(text: string): string {
  let t = text
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/[`*]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  const cut = t.slice(12).search(/(?<=[a-z0-9)])[.:;](\s|$)|\s\(|\s—\s/);
  if (cut >= 0) t = t.slice(0, cut + 12);
  t = t.replace(/[.:;,]$/, "");
  return t.length > 84 ? t.slice(0, 83).replace(/\s\S*$/, "") + "…" : t;
}

function stateOf(cells: string[]): State {
  for (const cell of cells) {
    const c = cell.replace(/[`*]/g, "").trim();
    if (/^(In progress|Implemented|Verify)\b/.test(c)) return "progress";
    if (/^Ready\b/.test(c)) return "ready";
    if (/^Blocked\b/.test(c)) return "blocked";
    if (/^Planned\b/.test(c)) return "planned";
    if (/^Idea\b/.test(c)) return "idea";
  }
  return "planned";
}

export function openTasks(markdown: string): Task[] {
  const seen = new Set<string>();
  const tasks: Task[] = [];
  for (const line of markdown.split("\n")) {
    if (!line.startsWith("|")) continue;
    const cells = line.trim().replace(/^\||\|$/g, "").split("|").map((c) => c.trim());
    const m = cells[0]?.match(ID);
    if (!m || cells.length < 3) continue;
    const id = `${m[1]}-${m[2]}`;
    if (seen.has(id)) continue;
    seen.add(id);
    tasks.push({ id, area: m[1], title: shortTitle(cells[1]), state: stateOf(cells.slice(2)) });
  }
  return tasks;
}

// IMPLEMENTED.md records each closure as "## <ID> — <title> — <YYYY-MM-DD> (<commits>)",
// or, in the oldest entries, "## <title> (<YYYY-MM-DD>…)". Some closures cover
// part of a task or no task ID at all.
export function closures(markdown: string): Closure[] {
  const out: Closure[] = [];
  for (const line of markdown.split("\n")) {
    const m = line.match(/^## (.+?) — (20\d\d-\d\d-\d\d)\b/) ?? line.match(/^## (.+?) \((20\d\d-\d\d-\d\d)\b/);
    if (!m) continue;
    const parts = m[1].split(" — ");
    const id = parts.length > 1 ? (parts[0].match(/^([A-Z]+-\d+)/)?.[1] ?? "") : "";
    const title = (id ? parts.slice(1).join(" — ") : m[1]).replace(/`/g, "");
    out.push({ date: m[2], id, title: title.charAt(0).toUpperCase() + title.slice(1) });
  }
  return out.sort((a, b) => b.date.localeCompare(a.date));
}

// A task is done when IMPLEMENTED.md names it and TO_IMPLEMENT.md no longer
// lists it: a closure of one part ("TUN-002 (part)") leaves the task open.
export function doneIds(open: Task[], markdown: string): Set<string> {
  const stillOpen = new Set(open.map((t) => t.id));
  const areas = new Set(TRACKS.flatMap(([, , a]) => a));
  const ids = new Set<string>();
  const add = (id: string) => {
    if (!stillOpen.has(id) && areas.has(id.split("-")[0])) ids.add(id);
  };
  for (const m of markdown.matchAll(/^## .*?\b([A-Z]+-\d+)\b/gm)) add(m[1]);
  for (const m of markdown.matchAll(/^\| *`?([A-Z]+-\d+)`? *\|/gm)) add(m[1]);
  return ids;
}

const TRACKS: [string, string, string[]][] = [
  ["Identity & login", "Passkeys, invitations, recovery and OIDC", ["ID"]],
  ["Access context", "Decisions that weigh the device, the moment and what is at stake", ["CTX"]],
  ["Edge & tunnels", "Routing, certificates, outbound connectors, portability", ["EDGE", "TUN"]],
  ["Sites & deploys", "Static sites from CI, previews and domains", ["PAGES"]],
  ["Console, CLI & alerts", "Operator journeys, live events, Telegram", ["UI", "CLI", "NTF", "SITE"]],
  ["Audit & trust", "A tamper-evident log with signed checkpoints", ["AUD"]],
  ["Shield", "Rate limits and challenges in front of every route", ["SHD"]],
  ["Release & architecture", "Install, upgrade, storage and fixes", ["REL", "ARCH", "BUG"]],
  ["Labs", "Learning tracks: performance, threat model, SRE, formal methods", ["LAB"]],
];

const ORDER: Record<State, number> = { progress: 0, ready: 1, blocked: 2, planned: 3, idea: 4 };

export function tracks(open: Task[], done: Set<string>): Track[] {
  return TRACKS.map(([name, blurb, areas]) => ({
    name,
    blurb,
    areas,
    open: open.filter((t) => areas.includes(t.area)).sort((a, b) => ORDER[a.state] - ORDER[b.state]),
    closed: [...done].filter((id) => areas.includes(id.split("-")[0])).length,
  })).filter((t) => t.open.length + t.closed > 0);
}
