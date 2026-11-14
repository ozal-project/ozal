import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { ArrowRight, BadgeCheck, LockKeyhole, RefreshCw } from "lucide-react";
import { Button, buttonVariants } from "$lib/components/ui/button/react";
import { Skeleton } from "$lib/components/ui/skeleton/react";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "$lib/components/ui/table/react";
import { fetchStatus, type RouteStatus, type StatusResponse } from "$lib/api";
import { ageLabel, elapsed, isStale } from "$lib/freshness";
import { friendlyHealth } from "$lib/wording.ts";
import { useSession } from "./session";

const lampFor = { healthy: "lamp-on", unhealthy: "lamp-off", unknown: "lamp-idle" };
const stamp = (iso: string, includeDate = false) => { const date = new Date(iso); return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString([], includeDate ? { dateStyle: "medium", timeStyle: "short" } : { hour: "2-digit", minute: "2-digit" }); };

export function Routes() {
  const { session } = useSession();
  const canApplications = !!session?.tools.includes("applications");
  const location = useLocation();
  const navigate = useNavigate();
  const [data, setData] = useState<StatusResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [observedAt, setObservedAt] = useState<number | null>(null);
  const [selectedHost, setSelectedHost] = useState("");
  const request = useRef<AbortController | null>(null);

  const load = async () => {
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    setLoading(true);
    try {
      const next = await fetchStatus(controller.signal);
      if (controller.signal.aborted) return;
      setData(next); setError(""); setObservedAt(Date.now());
      setSelectedHost((selected) => selected && next.routes.some((route) => route.host === selected) ? selected : "");
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).name === "TimeoutError" ? "Request timed out" : (cause as Error).message); }
    finally { if (!controller.signal.aborted && request.current === controller) setLoading(false); }
  };

  useEffect(() => {
    const requested = new URLSearchParams(location.search).get("host") ?? "";
    if (!data) { setSelectedHost(requested); return; }
    setSelectedHost(data.routes.some((route) => route.host === requested) ? requested : "");
  }, [location.search, data]);
  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 15_000);
    return () => { clearInterval(timer); request.current?.abort(); };
  }, []);

  const inspect = (host: string) => { setSelectedHost(host); navigate(`/routes?host=${encodeURIComponent(host)}`); };
  const routes = data?.routes ?? [];
  const protectedCount = routes.filter((route) => route.access === "Protected").length;
  const unhealthy = routes.filter((route) => route.state_class === "unhealthy").length;
  const selected = routes.find((route) => route.host === selectedHost);

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Applications</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Hostnames</h1><p className="text-muted-foreground mt-1 text-sm">Every hostname Ozal answers, its access rule and whether the app behind it responds.</p><nav className="console-tabs" aria-label="Application views">{canApplications && <Link className="console-tab" to="/applications">Manage</Link>}<Link aria-current="page" className="console-tab" to="/routes">Routing &amp; health</Link></nav></div><div className="flex flex-col items-start gap-2 sm:items-end"><Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button><Freshness observedAt={observedAt} refreshing={loading && data !== null} error={error} /></div></header>

    <section className="bg-card flex flex-wrap gap-x-8 gap-y-3 rounded-lg px-5 py-4" aria-label="Routing summary">{[{ value: data ? routes.length : "—", label: "hostnames" }, { value: data ? protectedCount : "—", label: "require login" }, { value: data ? unhealthy : "—", label: "not answering", bad: unhealthy > 0 }].map((item) => <div key={item.label}><p className="plate">{item.label}</p><p className={`mt-1 text-xl font-semibold tabular-nums ${item.bad ? "text-destructive" : ""}`}>{item.value}</p></div>)}</section>

    {data?.audit && !data.audit.available && <div className="border-destructive/40 bg-destructive/[0.06] rounded-lg border p-4" role="alert"><p className="font-medium">Durable access audit unavailable</p><p className="text-muted-foreground mt-1 text-sm">Some access events may be missing. Required routes fail closed; best-effort routes continue serving.</p><p className="text-muted-foreground mt-2 font-mono text-xs">{data.audit.failure_count} failures since process start{data.audit.last_failure_at ? ` · last ${stamp(data.audit.last_failure_at, true)}` : ""}</p></div>}

    {data?.certificate && <section className="bg-card rounded-lg p-5" aria-labelledby="origin-certificate"><div className="flex flex-wrap items-center justify-between gap-3"><div><p className="plate">Origin certificate</p><h2 id="origin-certificate" className="mt-1 text-base font-medium">{data.certificate.host}</h2><p className="text-muted-foreground mt-1 text-sm">{data.certificate.mode} · {data.certificate.valid ? `valid until ${data.certificate.not_after}` : "not valid"}{data.certificate.pending ? ` · ${data.certificate.pending} pending` : ""}</p></div><span className="flex items-center gap-2 font-mono text-xs"><BadgeCheck className="size-4" aria-hidden="true" /><span className={`lamp ${data.certificate.valid ? "lamp-on" : "lamp-off"}`} aria-hidden="true" />{data.certificate.valid ? "valid" : "invalid"}</span></div><p className={`${data.certificate.last_error ? "text-destructive" : "text-muted-foreground"} mt-4 text-sm`}>{data.certificate.last_error || `Last outcome: ${data.certificate.last_outcome || "—"} · next attempt ${data.certificate.next_attempt || "—"}`}</p></section>}

    <section className="bg-card min-w-0 rounded-lg" aria-labelledby="configured-routes"><header className="px-5 py-4"><p className="plate">Routes</p><h2 id="configured-routes" className="mt-1 text-base font-medium">Hostnames</h2><p className="text-muted-foreground mt-1 text-xs">Updated every 15 seconds</p></header>
      {!data ? error ? <p className="text-destructive p-5 text-sm" role="alert">Status could not be read ({error}). <button className="underline" onClick={() => void load()}>Retry</button></p> : <div className="space-y-3 p-5" aria-busy="true" aria-label="Loading routes">{[1, 2, 3].map((key) => <Skeleton key={key} className="h-12 w-full" />)}</div> : routes.length === 0 ? <p className="text-muted-foreground p-5 text-sm">No routes configured.</p> : <>
        {error && <p className="border-warning/40 bg-warning/[0.06] m-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read; refresh failed: {error}</p>}
        <ul className="grid gap-3 p-3 md:hidden" aria-label="Configured routes">{routes.map((route) => <li key={route.host} className="border-border rounded-lg border p-3"><div className="flex items-start justify-between gap-3"><div className="min-w-0"><p className="break-all font-mono text-sm">{route.host}</p><p className="text-muted-foreground text-xs">{route.kind}</p></div><Button variant="outline" size="sm" onClick={() => inspect(route.host)} aria-label={`Inspect route ${route.host}`}>Inspect</Button></div><dl className="mt-3 grid grid-cols-[5rem_1fr] gap-x-2 gap-y-1 text-xs"><dt className="text-muted-foreground">Access</dt><dd>{route.access} · {route.policy}</dd><dt className="text-muted-foreground">Health</dt><dd className="flex items-center gap-2"><span className={`lamp ${lampFor[route.state_class]}`} aria-hidden="true" />{route.state} · {route.latency}</dd></dl><TransportDetails route={route} /></li>)}</ul>
        <div className="hidden overflow-x-auto md:block" role="region" tabIndex={0} aria-label="Configured routes"><Table><TableHeader><TableRow><TableHead className="plate pl-5">Hostname</TableHead><TableHead className="plate">Access</TableHead><TableHead className="plate">Upstream</TableHead><TableHead className="plate">Health</TableHead><TableHead className="plate pr-5 text-right">Latency</TableHead><TableHead><span className="sr-only">Actions</span></TableHead></TableRow></TableHeader><TableBody>{routes.map((route) => <TableRow key={route.host}><TableCell className="max-w-80 min-w-56 pl-5 align-top"><div className="break-all font-mono text-[0.82rem]">{route.host}</div><div className="text-muted-foreground text-xs">{route.kind}</div></TableCell><TableCell className="align-top"><span className={`inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[0.7rem] uppercase ${route.access === "Protected" ? "border-signal/40 text-signal" : "border-border text-muted-foreground"}`}>{route.access === "Protected" && <LockKeyhole className="size-3" aria-hidden="true" />}{route.access === "Protected" ? "login" : "public"}</span><div className="text-muted-foreground mt-1 text-xs">{route.policy}</div></TableCell><TableCell className="max-w-56 align-top"><div className="text-muted-foreground font-mono text-xs break-all">{route.target}</div><div className="text-muted-foreground text-xs">probe {route.health_path}</div></TableCell><TableCell className="max-w-72 align-top whitespace-normal"><span className="flex items-center gap-2 text-sm"><span className={`lamp ${lampFor[route.state_class]}`} aria-hidden="true" />{route.state}</span>{route.last_error && <div className="text-destructive mt-1 text-xs" title={route.last_error}>{friendlyHealth(route.last_error)}</div>}{route.last_check !== "-" && <div className="text-muted-foreground mt-1 font-mono text-[0.7rem]">checked {stamp(route.last_check)}</div>}</TableCell><TableCell className="pr-3 text-right align-top font-mono text-xs tabular-nums">{route.latency}</TableCell><TableCell className="pr-4 align-top"><Button variant="outline" size="sm" onClick={() => inspect(route.host)} aria-label={`Inspect route ${route.host}`}>Inspect</Button></TableCell></TableRow>)}</TableBody></Table></div>
      </>}
    </section>

    {selected && <section className="bg-card rounded-lg p-5" aria-labelledby="route-inspector"><p className="plate">Route inspector</p><h2 id="route-inspector" className="mt-1 text-base font-medium">{selected.host}</h2><p className="text-muted-foreground mt-1 text-sm">Current status projection. Detailed allowlists remain behind the policy API.</p><dl className="mt-5 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[9rem_1fr]"><dt className="text-muted-foreground">Access</dt><dd>{selected.access} · {selected.policy}</dd><dt className="text-muted-foreground">Transport</dt><dd>{selected.kind}{selected.is_tunnel ? ` · ${selected.target}` : ""}</dd><dt className="text-muted-foreground">Upstream</dt><dd className="font-mono break-all">{selected.target}</dd><dt className="text-muted-foreground">Observed signal</dt><dd>{selected.state}{selected.last_check !== "-" ? ` · checked ${stamp(selected.last_check, true)}` : ""}</dd>{selected.last_error && <><dt className="text-muted-foreground">Last probe error</dt><dd className="text-destructive break-all">{friendlyHealth(selected.last_error)}<span className="text-muted-foreground block font-mono">{selected.last_error}</span></dd></>}</dl><div className="mt-5 flex flex-wrap items-center gap-3"><Link className={buttonVariants()} to="/activity">See its access decisions <ArrowRight className="size-4" aria-hidden="true" /></Link><span className="text-muted-foreground text-xs">To ask why someone would be refused, run <code className="font-mono">ozal policy explain -host {selected.host} -email person@example.com</code> on the host.</span></div></section>}
    <p className="text-muted-foreground font-mono text-[0.7rem]">{data ? `generated ${stamp(data.generated_at)} · ` : ""}access routing only</p>
  </div>;
}

function Freshness({ observedAt, refreshing, error }: { observedAt: number | null; refreshing: boolean; error: string }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 10_000); return () => clearInterval(timer); }, []);
  const age = observedAt == null ? "not loaded" : ageLabel(elapsed(now, observedAt));
  const stale = observedAt !== null && isStale(elapsed(now, observedAt), !!error);
  const state = observedAt == null ? "Not loaded" : error ? "Last read failed" : refreshing ? "Refreshing" : stale ? "Stale" : "Current";
  return <p className="text-muted-foreground inline-flex flex-wrap items-center gap-2 font-mono text-[0.7rem]" title={error || undefined}><span className={`lamp ${observedAt == null ? "lamp-idle" : error || stale ? "lamp-off" : refreshing ? "lamp-wait" : "lamp-on"}`} aria-hidden="true" />{state} · {age}{error && <span className="text-destructive">· {error}</span>}</p>;
}
function TransportDetails({ route }: { route: RouteStatus }) {
  return <details className="mt-3 text-xs"><summary className="text-muted-foreground cursor-pointer">Transport details</summary><dl className="mt-2 grid gap-1"><dt className="text-muted-foreground">Upstream</dt><dd className="break-all font-mono">{route.target}</dd><dt className="text-muted-foreground">Probe</dt><dd className="break-all font-mono">{route.health_path}</dd>{route.last_error && <><dt className="text-destructive">Last error</dt><dd className="break-all">{friendlyHealth(route.last_error)}<span className="text-muted-foreground block font-mono">{route.last_error}</span></dd></>}</dl></details>;
}
