import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "$lib/components/ui/button/react";
import { Badge } from "$lib/components/ui/badge/react";
import { fetchAccessHistory, fetchPeople, fetchStatus, type AccessEvent, type AccessHistorySummary, type StatusResponse } from "$lib/api";
import { accessScope, accessSummary, applications, attention, certificateView, changes, headline, historyScope, historySummary, streamNotices, whoGotIn, type StreamState } from "$lib/overview";
import { emptyFeed, receive, type FeedState } from "$lib/feed";
import { openEventStream } from "$lib/eventStream";
import { subjectsOf } from "$lib/wording.ts";
import { useSession } from "./session";
import { BackupPasskeyNotice } from "./BackupPasskeyNotice";

const maxEvents = 500;
const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const since = (iso: string) => {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "time unknown";
  const minutes = Math.max(0, Math.round((Date.now() - date.getTime()) / 60_000));
  return minutes < 1 ? "just now" : minutes < 60 ? `${minutes} min ago` : minutes < 1440 ? `${Math.round(minutes / 60)} h ago` : `${Math.round(minutes / 1440)} d ago`;
};

export function Overview() {
  const { session, end } = useSession();
  const token = session?.token ?? "";
  const peopleAllowed = !!session?.tools.includes("people");
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [statusError, setStatusError] = useState("");
  const [statusAt, setStatusAt] = useState<number | null>(null);
  const [history, setHistory] = useState<AccessHistorySummary | null>(null);
  const [events, setEvents] = useState<AccessEvent[]>([]);
  const [feed, setFeed] = useState<FeedState>(emptyFeed);
  const [stream, setStream] = useState<StreamState>("connecting");
  const [subjects, setSubjects] = useState<Map<string, string>>(() => new Map());
  const [trimmed, setTrimmed] = useState(false);
  const feedRef = useRef<FeedState>(emptyFeed());
  const historyRefresh = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    const controller = new AbortController();
    const loadStatus = async () => {
      try {
        const value = await fetchStatus(controller.signal);
        setStatus(value); setStatusError(""); setStatusAt(Date.now());
      } catch (error) { if (!controller.signal.aborted) setStatusError((error as Error).message); }
    };
    const loadHistory = async () => {
      try { const value = await fetchAccessHistory("?limit=1", controller.signal); setHistory(value?.summary ?? null); }
      catch { /* live access events remain available when history is not */ }
    };
    const loadSubjects = async () => {
      if (!peopleAllowed) return;
      try { const value = await fetchPeople(controller.signal); setSubjects(subjectsOf(value.users)); }
      catch { /* people names are optional context for the overview */ }
    };
    void loadStatus(); void loadHistory(); void loadSubjects();
    const timers = [setInterval(loadStatus, 15_000), setInterval(loadHistory, 30_000), setInterval(loadSubjects, 30_000)];
    return () => { controller.abort(); timers.forEach(clearInterval); };
  }, [token, peopleAllowed]);

  useEffect(() => {
    setEvents([]); feedRef.current = emptyFeed(); setFeed(feedRef.current); setTrimmed(false);
    if (!token) { setStream("locked"); return; }
    const closeStream = openEventStream({
      token,
      onDenied: () => end("This session is no longer valid. Sign in again."),
      onState: setStream,
      onEvent: (message) => {
        try {
          const event = JSON.parse(message.data) as AccessEvent;
          if (typeof event.type !== "string" || typeof event.time !== "string") return;
          const result = receive(feedRef.current, event.id ?? message.id);
          feedRef.current = result.state;
          setFeed(result.state);
          if (!result.accept) return;
          setEvents((current) => { if (current.length >= maxEvents) setTrimmed(true); return [event, ...current].slice(0, maxEvents); });
          if (event.type.startsWith("access.")) {
            clearTimeout(historyRefresh.current);
            historyRefresh.current = window.setTimeout(() => {
            void fetchAccessHistory("?limit=1").then((value) => setHistory(value?.summary ?? null)).catch(() => {});
            }, 3_000);
          }
        } catch { /* Ignore malformed server event records. */ }
      },
    });
    return () => { closeStream(); clearTimeout(historyRefresh.current); };
  }, [token, end]);

  const needs = useMemo(() => attention(status), [status]);
  const summary = useMemo(() => history ? historySummary(history) : accessSummary(events), [history, events]);
  const recent = useMemo(() => changes(events, subjects).slice(0, 5), [events, subjects]);
  const apps = useMemo(() => applications(status?.routes ?? []), [status]);
  const certificate = certificateView(status?.certificate, new Date());
  const oldest = events.length ? new Date(events[events.length - 1].time) : null;
  const coverage = { ...feed, partial: feed.partial || trimmed };
  const notices = streamNotices(stream, coverage, events.length);
  const people = history ? whoGotIn(history, subjects) : [];

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-3">
      <div><p className="plate">At a glance</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Overview</h1><p className="text-muted-foreground mt-1 text-sm">{headline(status, needs.length)}</p></div>
      <p className="text-muted-foreground text-xs" role={statusError && !status ? "alert" : "status"}>{statusError && !status ? `The edge did not answer (${statusError}). Is Ozal running?` : statusAt ? `Updated ${new Date(statusAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "Reading the edge…"}</p>
    </header>

    {session?.passkey && <BackupPasskeyNotice token={token} />}

    <section aria-label="Needs attention" aria-live="polite" className="grid gap-3">
      {needs.map((item) => <article key={item.title} className="bg-card flex flex-wrap items-center gap-4 rounded-lg px-4 py-3.5"><span className="text-destructive text-xl" aria-hidden="true">!</span><div className="min-w-0 flex-1"><h2 className="font-semibold">{item.title}</h2><p className="text-muted-foreground text-sm" title={item.raw}>{item.detail}</p></div>{item.action && <Button variant="outline" size="sm" onClick={() => location.assign(item.action?.page ?? "/overview")}>{item.action.label}</Button>}</article>)}
      {status && needs.length === 0 && <p className="text-muted-foreground flex items-center gap-2 text-sm"><span className="text-success" aria-hidden="true">✓</span>Nothing needs you right now.</p>}
    </section>

    {notices.length > 0 && <div className="border-warning/40 bg-warning/[0.06] grid gap-1 rounded-lg border px-4 py-3 text-sm" role="status">{notices.map((notice) => <p key={notice}>{notice}</p>)}</div>}

    <section className="bg-card flex flex-wrap items-center gap-x-8 gap-y-3 rounded-lg px-5 py-4" aria-labelledby="overview-access">
      <div className="min-w-0"><h2 id="overview-access" className="text-sm font-medium">Who got in</h2><p className="text-muted-foreground mt-0.5 text-xs">{history ? historyScope(history) : accessScope(stream, coverage, oldest, events.length)}</p></div>
      <dl className="flex flex-wrap gap-x-6 gap-y-2">{[
        { n: summary.allowed, label: "let in" }, { n: summary.refused, label: "refused" }, history ? { n: history.step_up, label: "asked to sign in again" } : { n: summary.loginsStarted, label: "sign-ins started" },
      ].map((item) => <div key={item.label} className="flex items-baseline gap-2"><dt className="sr-only">{item.label}</dt><dd className="text-xl leading-none font-semibold tabular-nums">{item.n}</dd><span className="text-muted-foreground text-xs" aria-hidden="true">{item.label}</span></div>)}</dl>
      {summary.reasons.length > 0 && <div className="border-border min-w-0 basis-full border-t pt-3"><p className="plate mb-1">Why people were refused</p><ul className="flex flex-wrap gap-x-4 gap-y-1 text-sm">{summary.reasons.slice(0, 3).map((reason) => <li key={reason.code}>{reason.text} <span className="text-muted-foreground font-mono text-xs tabular-nums">{reason.count}</span></li>)}</ul></div>}
      {people.length > 0 && <div className="border-border min-w-0 basis-full border-t pt-3"><p className="plate mb-1">People let in, most recent first</p><ul className="grid gap-1 text-sm sm:grid-cols-2">{people.map((person) => <li key={`${person.who}-${person.app}`} className="min-w-0 break-words"><span className="font-medium">{person.who}</span> <span className="text-muted-foreground">→ {person.app} · {person.count}× · {since(person.last)}</span></li>)}</ul></div>}
    </section>

    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(0,1fr)]">
      <section className="bg-card min-w-0 rounded-lg" aria-labelledby="recent-changes"><header className="px-5 py-4"><p className="plate">What changed</p><h2 id="recent-changes" className="mt-1 text-base font-medium">Recent changes</h2><p className="text-muted-foreground mt-1 text-xs">Deploys, reloads, people and connectors, newest first.</p></header>
        {recent.length === 0 ? <p className="text-muted-foreground px-5 pb-6 text-sm">{stream === "connecting" ? "Connecting to the live stream…" : stream === "lost" ? "The event stream is unavailable." : "No changes since this edge started."}</p> : <ul className="border-border border-t">{recent.map((item, index) => <li key={`${item.time}-${item.type}-${index}`} className="border-border grid grid-cols-[4.6rem_1.25rem_minmax(0,1fr)] items-start gap-3 border-b px-5 py-3.5 last:border-0"><time className="text-muted-foreground pt-0.5 text-xs">{clock(item.time)}</time><Badge variant={item.tone === "bad" ? "destructive" : item.tone === "good" ? "default" : "secondary"} aria-label={item.tone}>{item.tone === "good" ? "↑" : item.tone === "bad" ? "×" : "•"}</Badge><div className="min-w-0"><p className="text-sm">{item.title}</p>{item.source && <p className="text-muted-foreground mt-0.5 font-mono text-xs break-words">{item.source}</p>}{item.detail && <p className="text-muted-foreground mt-0.5 text-xs break-words">{item.detail}</p>}</div></li>)}</ul>}
      </section>

      <div className="grid gap-5">
        <section className="bg-card min-w-0 rounded-lg" aria-labelledby="overview-apps"><header className="px-5 py-4"><p className="plate">Applications</p><h2 id="overview-apps" className="mt-1 text-base font-medium">{status ? `${apps.length} configured` : "Reading the edge…"}</h2></header>{apps.length === 0 && status ? <p className="text-muted-foreground px-5 pb-5 text-sm">No application routes configured.</p> : <ul aria-label="Applications and their state">{apps.slice(0, 8).map((app) => <li key={app.host}><a className="app-row" href={`/routes?host=${encodeURIComponent(app.host)}`}><span className={`lamp ${app.state === "down" ? "lamp-off" : app.state === "up" ? "edge-live" : "lamp-idle"}`} aria-hidden="true" /><span className="min-w-0 truncate text-sm">{app.name}</span><span className={`text-xs ${app.state === "down" ? "text-destructive" : "text-muted-foreground"}`}>{app.label}</span></a></li>)}</ul>}</section>
        {certificate && <section className="bg-card rounded-lg px-5 py-4" aria-labelledby="overview-certificate"><p className="plate">Certificate</p><h2 id="overview-certificate" className="mt-1 text-base font-medium">{certificate.tone === "bad" ? "Needs attention" : "Valid"}</h2><dl className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm"><dt className="text-muted-foreground">Covers</dt><dd className="break-words">{certificate.domains.length ? certificate.domains.join(", ") : certificate.names.join(", ")}</dd><dt className="text-muted-foreground">Valid until</dt><dd>{certificate.expires}</dd><dt className="text-muted-foreground">Next renewal</dt><dd>{certificate.renews}</dd></dl></section>}
      </div>
    </div>
  </div>;
}
