// The console's search (Ctrl K): one list of places and things, ranked by how
// well each matches what was typed. It only ranks what the session already
// read; it never asks the edge for more than the pages themselves show.

export type Kind = "section" | "application" | "person" | "site" | "domain";
export type Result = { kind: Kind; label: string; detail: string; to: string };

const kindOrder: Record<Kind, number> = { section: 0, application: 1, person: 2, site: 3, domain: 4 };
export const kindLabel: Record<Kind, string> = { section: "Section", application: "Application", person: "Person", site: "Site", domain: "Domain" };

// score is how well text matches the query: a whole match beats a prefix,
// a prefix beats the start of a word, which beats anywhere; 0 is no match.
// Every word of the query must match the label or the detail.
export function score(query: string, label: string, detail = ""): number {
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return 0;
  const l = label.toLowerCase(), d = detail.toLowerCase();
  let total = 0;
  for (const w of words) {
    const s = l === w ? 100 : l.startsWith(w) ? 60 : new RegExp(`(^|[\\s.@/_-])${escape(w)}`).test(l) ? 40 : l.includes(w) ? 20 : d.includes(w) ? 8 : 0;
    if (s === 0) return 0;
    total += s;
  }
  return total;
}

// rank keeps the matches, best first; ties go by kind (places before
// things) and then alphabetically. An empty query lists the sections.
export function rank(query: string, items: readonly Result[], limit = 12): Result[] {
  if (!query.trim()) return items.filter((item) => item.kind === "section").slice(0, limit);
  return items
    .map((item) => ({ item, s: score(query, item.label, item.detail) }))
    .filter(({ s }) => s > 0)
    .sort((a, b) => b.s - a.s || kindOrder[a.item.kind] - kindOrder[b.item.kind] || a.item.label.localeCompare(b.item.label))
    .slice(0, limit)
    .map(({ item }) => item);
}

function escape(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
