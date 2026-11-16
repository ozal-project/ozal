import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { Search } from "lucide-react";
import { fetchPeople, fetchSites, fetchStatus, fetchZones } from "$lib/api";
import { target, visibleGroups } from "$lib/navigation";
import { kindLabel, rank, type Result } from "$lib/search";

// Ctrl K (⌘K): go to any place, application, person, site or domain by
// typing. It reads only what this session may already see, when it opens,
// and keeps nothing after it closes.
export function CommandSearch({ open, onOpenChange, tools }: { open: boolean; onOpenChange(open: boolean): void; tools: readonly string[] }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const listID = useId();
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<Result[]>([]);
  const [active, setActive] = useState(0);
  const [loading, setLoading] = useState(false);

  const sections = useMemo<Result[]>(() => visibleGroups(tools).flatMap((group) => group.sections.map((section) => ({
    kind: "section" as const, label: section.label, detail: [group.name, section.purpose].filter(Boolean).join(" · "), to: target(section, tools),
  }))), [tools]);

  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (!open) { if (element.open) element.close(); return; }
    if (!element.open) element.showModal();
    setQuery(""); setActive(0); setItems(sections);
    requestAnimationFrame(() => input.current?.focus());
    const controller = new AbortController();
    setLoading(true);
    // Each list is optional: one that is not allowed or fails is left out.
    const reads: Promise<Result[]>[] = [
      fetchStatus(controller.signal).then((status) => status.routes.map((route) => ({ kind: "application" as const, label: route.host, detail: route.access || route.kind, to: `/routes?host=${encodeURIComponent(route.host)}` }))),
    ];
    if (tools.includes("people")) reads.push(fetchPeople(controller.signal).then((people) => people.users.map((person) => ({ kind: "person" as const, label: person.name || person.email, detail: person.email, to: `/people?person=${encodeURIComponent(person.email)}` }))));
    if (tools.includes("sites")) reads.push(fetchSites(controller.signal).then((sites) => sites.map((site) => ({ kind: "site" as const, label: site.name, detail: "Site", to: "/sites" }))));
    if (tools.includes("applications")) reads.push(fetchZones(controller.signal).then((zones) => zones.zones.map((zone) => ({ kind: "domain" as const, label: zone.name, detail: "Domain", to: "/domains" }))));
    void Promise.allSettled(reads).then((settled) => {
      if (controller.signal.aborted) return;
      setItems([...sections, ...settled.flatMap((result) => result.status === "fulfilled" ? result.value : [])]);
      setLoading(false);
    });
    return () => controller.abort();
  }, [open, sections, tools]);

  const results = useMemo(() => rank(query, items), [query, items]);
  useEffect(() => setActive(0), [query]);

  const go = (result: Result | undefined) => {
    if (!result) return;
    onOpenChange(false);
    navigate(result.to);
  };
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowDown") { event.preventDefault(); setActive((i) => Math.min(i + 1, results.length - 1)); }
    else if (event.key === "ArrowUp") { event.preventDefault(); setActive((i) => Math.max(i - 1, 0)); }
    else if (event.key === "Enter") { event.preventDefault(); go(results[active]); }
  };

  return <dialog ref={dialog} className="command-search" aria-label="Search the console" onClose={() => onOpenChange(false)} onClick={(event) => { if (event.target === dialog.current) onOpenChange(false); }}>
    <div className="command-search-field">
      <Search className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
      <input ref={input} value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={onKeyDown} placeholder="Go to a section, application, person, site or domain"
        role="combobox" aria-expanded="true" aria-controls={listID} aria-autocomplete="list" aria-activedescendant={results[active] ? `${listID}-${active}` : undefined} aria-label="Search the console" autoComplete="off" spellCheck={false} />
      <kbd className="command-search-key">Esc</kbd>
    </div>
    <ul id={listID} role="listbox" aria-label="Results" className="command-search-list">
      {results.map((result, index) => <li key={`${result.kind}-${result.to}-${result.label}`} id={`${listID}-${index}`} role="option" aria-selected={index === active} className="command-search-item" onMouseMove={() => setActive(index)} onClick={() => go(result)}>
        <span className="min-w-0"><span className="command-search-label">{result.label}</span><span className="command-search-detail">{result.detail}</span></span>
        <span className="command-search-kind">{kindLabel[result.kind]}</span>
      </li>)}
      {results.length === 0 && <li className="command-search-empty" role="presentation">{loading ? "Reading…" : `Nothing matches “${query}”.`}</li>}
    </ul>
  </dialog>;
}
