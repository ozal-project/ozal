import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { Pause, Play } from "lucide-react";
import { Button, buttonVariants } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "$lib/components/ui/select/react";
import { fetchOverview, fetchPeople, type AccessEvent, type RouteOverview } from "$lib/api";
import { emptyFeed, receive, type FeedState } from "$lib/feed";
import { openEventStream } from "$lib/eventStream";
import { eventReason, shortId, subjectsOf, whoActed as actedBy } from "$lib/wording.ts";
import { PolicyEvidence } from "./PolicyEvidence";
import { useSession } from "./session";

const maxEvents = 500;
const labels: Record<string, string> = {
  "github.workflow_proposed": "Publishing workflow proposed", "access.allowed": "Access allowed", "access.denied": "Access denied", "access.audit_failed": "Access blocked: audit unavailable", "access.rate_limited": "Rate limited", "access.step_up": "Login requested again", "passkey.registered": "Passkey added", "passkey.revoked": "Passkey revoked", "user.invited": "Person invited", "user.recovered": "Account recovered", "user.disabled": "Person disabled", "user.enabled": "Person enabled",
  "login.started": "Login started", "login.failed": "Login failed", "login.succeeded": "Login succeeded", "route.created": "Route added", "route.create_failed": "Route creation failed", "route.deleted": "Route removed", "route.delete_failed": "Route removal failed", "config.reloaded": "Configuration reloaded", "config.reload_failed": "Configuration reload failed",
  "tunnel.connected": "Tunnel connected", "tunnel.disconnected": "Tunnel disconnected", "tunnel.rejected": "Tunnel rejected", "connector.enrolled": "Connector enrolled", "connector.rotated": "Connector certificate rotated", "connector.revoked": "Connector revoked", "notify.chat_paired": "Alert chat paired", "origin.denied": "Origin pull rejected",
  "operator.signed_in": "Signed in to the console", "operator.signed_out": "Signed out of the console", "operator.login_issued": "Host login issued", "operator.revoked": "Console session ended", "operator.step_up": "Confirmed with a passkey", "admin.invited": "Administrator invited", "admin.enrolment_issued": "Setup link issued", "admin.recovery_issued": "Recovery link issued", "admin.device_link_issued": "Link for another browser issued", "admin.passkey_registered": "Administrator passkey created", "admin.passkey_added": "Administrator passkey added", "admin.sign_in_failed": "Console sign-in failed", "session.revoked": "Session ended",
  "pages.deployed": "Site deployed", "dns.record_created": "DNS record created", "pages.rolled_back": "Site rolled back", "pages.refused": "Deployment refused", "pages.deploy_refused": "Project deployment refused", "pages.failed": "Deployment failed", "update.started": "Update started",
};
const categories = [
  { value: "all", label: "All events" }, { value: "access", label: "Access decisions" }, { value: "login", label: "Logins" },
  { value: "changes", label: "Configuration changes" }, { value: "tunnel", label: "Tunnels" }, { value: "pages", label: "Deployments" },
];
type Category = (typeof categories)[number]["value"];
const group = (event: AccessEvent): Category => event.type.startsWith("access.") || event.type.startsWith("origin.") ? "access" : event.type.startsWith("login.") || event.type.startsWith("operator.") || event.type.startsWith("admin.sign") ? "login" : event.type.startsWith("tunnel.") || event.type.startsWith("connector.") ? "tunnel" : event.type.startsWith("pages.") ? "pages" : "changes";
const outcome = (event: AccessEvent) => event.error || /[._](denied|failed|rejected|refused)$/.test(event.type) ? "deny" : /[._](allowed|succeeded|deployed|rolled_back)$/.test(event.type) ? "allow" : "info";
const time = (iso: string) => { const date = new Date(iso); return Number.isNaN(date.getTime()) ? { clock: "—", date: "" } : { clock: date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }), date: date.toLocaleDateString() }; };
const reasonText = (event: AccessEvent) => event.reason && /^[0-9a-f]{40,}$/.test(event.reason) ? shortId(event.reason) : eventReason(event.reason);
const codeText = (event: AccessEvent) => /^(operator|admin|session)\./.test(event.type) ? "" : event.code ?? "";
const deploySource = (event: AccessEvent) => event.deployment ? [event.deployment.project, event.deployment.repository_id ? `repository ${event.deployment.repository_id}` : "", event.deployment.ref, event.deployment.commit_sha?.slice(0, 12)].filter(Boolean).join(" · ") : "";

export function Activity() {
  const { session, end } = useSession();
  const token = session?.token ?? "";
  const allowPeople = !!session?.tools.includes("people");
  const [events, setEvents] = useState<AccessEvent[]>([]);
  const [frozen, setFrozen] = useState<AccessEvent[] | null>(null);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<Category>("all");
  const [stream, setStream] = useState<"locked" | "connecting" | "live" | "reconnected" | "lost">("connecting");
  const [feed, setFeed] = useState<FeedState>(emptyFeed);
  const feedRef = useRef<FeedState>(emptyFeed());
  const [routes, setRoutes] = useState<RouteOverview[] | null>(null);
  const [routesError, setRoutesError] = useState(false);
  const [routesObservedAt, setRoutesObservedAt] = useState<number | null>(null);
  const [routesRefreshing, setRoutesRefreshing] = useState(false);
  const [subjects, setSubjects] = useState<Map<string, string>>(() => new Map());
  const shown = frozen ?? events;
  const denied = shown.filter((event) => event.type === "access.denied").length;
  const filtered = useMemo(() => shown.filter((event) => (category === "all" || group(event) === category) && (!query || [event.type, event.actor, event.host, event.code, event.reason, event.error, event.remote, event.deployment?.project, event.deployment?.ref, event.deployment?.commit_sha].some((value) => (value ?? "").toLowerCase().includes(query.toLowerCase())))), [shown, category, query]);
  const whoActed = (actor?: string) => actedBy(actor, subjects);

  useEffect(() => {
    setEvents([]); setFrozen(null); feedRef.current = emptyFeed(); setFeed(feedRef.current);
    if (!token) { setStream("locked"); return; }
    return openEventStream({ token, onDenied: () => end("This session is no longer valid. Sign in again."), onState: setStream, onEvent: (message) => {
      try {
        const event = JSON.parse(message.data) as AccessEvent;
        if (typeof event.type !== "string" || typeof event.time !== "string") return;
        const next = receive(feedRef.current, event.id ?? message.id);
        feedRef.current = next.state; setFeed(next.state);
        if (next.accept) setEvents((current) => [event, ...current].slice(0, maxEvents));
      } catch { /* Ignore a malformed event record. */ }
    } });
  }, [token, end]);

  useEffect(() => {
    const controller = new AbortController();
    let current = true;
    let routeRequest: AbortController | null = null;
    const loadRoutes = async () => {
      routeRequest?.abort();
      const request = new AbortController();
      routeRequest = request;
      setRoutesRefreshing(true);
      try { const next = await fetchOverview(request.signal); if (!current || request.signal.aborted) return; setRoutes(next); setRoutesError(false); setRoutesObservedAt(Date.now()); }
      catch { if (current && !request.signal.aborted && !controller.signal.aborted) setRoutesError(true); }
      finally { if (current && routeRequest === request) setRoutesRefreshing(false); }
    };
    const loadSubjects = async () => {
      if (!allowPeople) return;
      try { const value = await fetchPeople(controller.signal); if (current) setSubjects(subjectsOf(value.users)); } catch { /* Actor labels are optional context. */ }
    };
    setSubjects(new Map()); void loadRoutes(); void loadSubjects();
    const timer = setInterval(() => { void loadRoutes(); void loadSubjects(); }, 30_000);
    return () => { current = false; controller.abort(); routeRequest?.abort(); clearInterval(timer); };
  }, [token, allowPeople]);

  const streamText = { locked: "Sign in to view activity", connecting: "Connecting…", live: "Live event stream", reconnected: "Live · reconnected", lost: "Connection lost · retrying" };
  const protectedCount = routes?.filter((route) => route.protected).length ?? 0;
  const paused = frozen !== null;

  return <div className="grid min-w-0 content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Decisions and changes</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Activity</h1><p className="text-muted-foreground mt-1 text-sm">Every decision and change, who made it and why.</p></div><span className="border-border bg-card flex items-center gap-2 rounded-full border px-3.5 py-1.5 font-mono text-xs"><span className={`lamp ${stream === "lost" ? "lamp-off" : stream === "connecting" || stream === "locked" ? "lamp-wait" : "lamp-on"}`} aria-hidden="true" />{streamText[stream]}</span></header>

    {(feed.missed > 0 || feed.restarted) && <div className="border-warning/40 bg-warning/[0.06] rounded-lg border px-4 py-3 text-sm" role="status">{feed.missed > 0 && <p><strong>{feed.missed} {feed.missed === 1 ? "event" : "events"} missed</strong> by this view (a slow connection, or history the server no longer holds): the table is incomplete. It is a live view, not an audit log.</p>}{feed.restarted && <p>Ozal restarted while this view was open; earlier rows are from the previous run.</p>}</div>}

    <section className="bg-card flex flex-wrap items-center gap-8 rounded-lg px-5 py-4" aria-label="Activity summary"><div><p className="plate">Events</p><p className="mt-1 text-xl font-semibold tabular-nums">{shown.length}</p></div><div><p className="plate">Refused</p><p className={`mt-1 text-xl font-semibold tabular-nums ${denied ? "text-destructive" : ""}`}>{denied}</p></div></section>

    <div className="grid min-w-0 grid-cols-1 gap-6 min-[1500px]:grid-cols-[minmax(0,1fr)_320px]">
      <section className="bg-card min-w-0 rounded-lg p-5" aria-labelledby="activity-feed-title">
        <header className="flex flex-wrap items-start justify-between gap-3"><div><p className="plate">Decision feed</p><h2 id="activity-feed-title" className="mt-1 text-base font-medium">{filtered.length} shown · {shown.length} retained</h2><p className="text-muted-foreground mt-1 text-xs">{stream === "locked" ? "Sign in to load events" : paused ? "Paused — new events are held back" : stream === "lost" ? "Stream unavailable — retrying" : "Newest first"}</p></div><Button variant="outline" size="sm" onClick={() => setFrozen(paused ? null : [...events])} aria-pressed={paused}>{paused ? <Play className="size-3.5" aria-hidden="true" /> : <Pause className="size-3.5" aria-hidden="true" />}{paused ? "Resume" : "Pause"}</Button></header>
        <div className="my-4 flex flex-wrap gap-3"><div className="min-w-0 flex-[1_1_14rem]"><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search actor, host or detail" aria-label="Search events" /></div><Select value={category} onValueChange={(value) => setCategory(value as Category)}><SelectTrigger className="w-full sm:w-52" aria-label="Event category"><SelectValue /></SelectTrigger><SelectContent>{categories.map((item) => <SelectItem value={item.value} key={item.value}>{item.label}</SelectItem>)}</SelectContent></Select></div>
        {filtered.length === 0 ? <div className="border-border flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-16 text-center"><ActivityMark /><p className="font-medium">{shown.length ? "No matching events" : "The board is quiet"}</p><p className="text-muted-foreground max-w-md text-sm">{shown.length ? "Try another category or search term." : "Lamps light here when a protected route is used. Public-route requests do not produce access events."}</p></div> : <>
          <ul className="grid gap-3 md:hidden">{filtered.map((event, index) => <EventCard key={`${event.time}-${event.type}-${index}`} event={event} whoActed={whoActed} />)}</ul>
          <div className="hidden overflow-x-auto md:block" role="region" tabIndex={0} aria-label="Access activity events"><table className="w-full text-sm"><thead><tr className="border-border border-b"><th className="plate px-2 py-3 text-left">Decision / event</th><th className="plate px-2 py-3 text-left">Who / where</th><th className="plate px-2 py-3 text-right">Time</th></tr></thead><tbody>{filtered.map((event, index) => <tr key={`${event.time}-${event.type}-${index}`} className="border-border border-b align-top"><td className="px-2 py-3"><EventSummary event={event} /><PolicyEvidence context={event.context} /></td><td className="px-2 py-3"><p>{whoActed(event.actor) || "No actor recorded"}</p>{event.host ? <Link className="text-muted-foreground hover:text-foreground font-mono text-xs underline underline-offset-4" to={`/routes?host=${encodeURIComponent(event.host)}`}>{event.host}</Link> : <p className="text-muted-foreground font-mono text-xs">No route recorded</p>}{event.remote && <p className="text-muted-foreground font-mono text-xs">{event.remote}</p>}</td><td className="px-2 py-3 text-right font-mono text-xs tabular-nums"><EventTime iso={event.time} /></td></tr>)}</tbody></table></div>
        </>}
      </section>

      <aside className="grid min-w-0 grid-cols-[minmax(0,1fr)] content-start gap-6">
        <section className="bg-card rounded-lg p-5" aria-labelledby="activity-routes"><header><p className="plate">Who each address lets in</p><h2 id="activity-routes" className="mt-1 text-base font-medium">{protectedCount} private · {routes ? routes.length - protectedCount : "…"} public</h2><p className="text-muted-foreground mt-1 text-xs">Test access shows the decision for a person you describe.</p><p className="text-muted-foreground mt-3 font-mono text-[0.7rem]" role={routesError ? "alert" : "status"}>{routesError ? "Route configuration could not be refreshed" : routesRefreshing ? "Refreshing routes…" : routesObservedAt ? `Routes updated ${new Date(routesObservedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "Loading routes…"}</p></header>
          {!routes ? <p className="text-muted-foreground mt-4 text-sm">{routesError ? "Routes could not be read. Try again shortly." : "Loading routes…"}</p> : routes.length === 0 ? <p className="text-muted-foreground mt-4 text-sm">No routes are configured.</p> : <ul className="divide-border mt-2 divide-y">{routes.map((route) => <li key={route.host} className="flex items-start gap-3 py-3"><span className={`lamp mt-1.5 ${route.protected ? "lamp-on" : "lamp-warn"}`} aria-hidden="true" /><div className="min-w-0 flex-1"><p className="truncate text-sm" title={route.host}>{route.host}</p><p className={`mt-0.5 text-xs ${route.protected ? "text-muted-foreground" : "text-warning"}`}>{route.protected ? "Private" : "Public"} · {route.policy}</p>{route.connector && <p className="text-muted-foreground text-xs">Through {route.connector} · {route.connection}</p>}</div><Link to={`/routes?host=${encodeURIComponent(route.host)}`} className={buttonVariants({ variant: "ghost", size: "sm" })} aria-label={`Test who can open ${route.host}`}>Test access</Link></li>)}</ul>}
        </section>
        <section className="border-border rounded-xl border border-dashed p-5"><p className="plate mb-2">About this view</p><p className="text-muted-foreground text-sm leading-relaxed">This list shows recent events since the server started; a restart clears it. For a permanent record, enable the audit log in the server configuration.</p></section>
      </aside>
    </div>
  </div>;
}

function EventTime({ iso }: { iso: string }) { const value = time(iso); return <><time dateTime={iso} title={iso}>{value.clock}</time><div className="text-muted-foreground">{value.date}</div></>; }
function EventSummary({ event }: { event: AccessEvent }) {
  const type = outcome(event);
  const code = codeText(event);
  return <><div className="flex items-center gap-2.5 font-medium"><span className={`lamp ${type === "allow" ? "lamp-on" : type === "deny" ? "lamp-off" : "lamp-idle"}`} aria-hidden="true" />{labels[event.type] ?? event.type}</div>{(code || event.reason || event.error) && <div className="text-muted-foreground mt-1 pl-5 text-xs">{code && <span className="font-mono">{code}</span>}{code && (event.reason || event.error) ? " · " : ""}{[reasonText(event), event.error].filter(Boolean).join(" · ")}</div>}{event.deployment && <p className="text-muted-foreground mt-2 break-all pl-5 font-mono text-xs">{deploySource(event)}</p>}</>;
}
function EventCard({ event, whoActed }: { event: AccessEvent; whoActed: (actor?: string) => string }) {
  const type = outcome(event);
  const code = codeText(event);
  return <li className="border-border rounded-lg border p-3"><div className="flex items-start justify-between gap-3"><div className="flex min-w-0 items-center gap-2 font-medium"><span className={`lamp shrink-0 ${type === "allow" ? "lamp-on" : type === "deny" ? "lamp-off" : "lamp-idle"}`} aria-hidden="true" /><span className="break-words">{labels[event.type] ?? event.type}</span></div><time className="text-muted-foreground shrink-0 font-mono text-xs" dateTime={event.time}>{time(event.time).clock}</time></div><p className="mt-2 pl-5 text-sm">{whoActed(event.actor) || "No actor recorded"}</p>{event.host && <Link to={`/routes?host=${encodeURIComponent(event.host)}`} className="text-muted-foreground mt-0.5 ml-5 block break-all font-mono text-xs underline underline-offset-4">{event.host}</Link>}{(code || event.reason || event.error) && <p className="text-muted-foreground mt-2 text-xs">{code && <span className="font-mono">{code}</span>}{code && (event.reason || event.error) ? " · " : ""}{[reasonText(event), event.error].filter(Boolean).join(" · ")}</p>}{event.remote && <p className="text-muted-foreground mt-1 text-xs">Peer <span className="font-mono">{event.remote}</span></p>}{event.deployment && <p className="text-muted-foreground mt-2 break-all font-mono text-xs">{deploySource(event)}</p>}<PolicyEvidence context={event.context} /></li>;
}

function ActivityMark() {
  return <svg width="44" height="44" viewBox="0 0 32 32" aria-hidden="true" className="opacity-80"><path d="M5 30V14.5A11 11 0 0 1 27 14.5V30h-5.4V14.8a5.6 5.6 0 0 0-11.2 0V30Z" fill="var(--primary)" /><path d="M10.4 30V14.8A5.6 5.6 0 0 1 16 9.2V30Z" fill="var(--depth)" /></svg>;
}
