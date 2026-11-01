import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { ArrowUpRight, RefreshCw, Undo2 } from "lucide-react";
import { fetchSites, fetchStatus, rollback, PagesDisabledError, type Deployment, type Site, type StatusResponse } from "$lib/api";
import { ageLabel, elapsed } from "$lib/freshness";
import { Button } from "$lib/components/ui/button/react";
import { Modal } from "./Modal";
import { useSession } from "./session";
import { Publishing } from "./Publishing";

type RestoreReview = { site: Site; deployment: Deployment; current?: Deployment; hosts: string[] };
type Notice = { text: string; ok: boolean };

export function Sites() {
  const { session } = useSession();
  const token = session?.token ?? "";
  const [sites, setSites] = useState<Site[] | null>(null);
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [statusError, setStatusError] = useState("");
  const [disabled, setDisabled] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [observedAt, setObservedAt] = useState<number | null>(null);
  const [now, setNow] = useState(Date.now());
  const [review, setReview] = useState<RestoreReview | null>(null);
  const [busy, setBusy] = useState<Record<string, AbortController>>({});
  const [notices, setNotices] = useState<Record<string, Notice>>({});
  const sitesRequest = useRef<AbortController | null>(null);
  const statusRequest = useRef<AbortController | null>(null);
  const busyRef = useRef(busy);
  busyRef.current = busy;

  const load = useCallback(async () => {
    if (!token) return;
    sitesRequest.current?.abort();
    statusRequest.current?.abort();
    const siteController = new AbortController();
    const statusController = new AbortController();
    sitesRequest.current = siteController;
    statusRequest.current = statusController;
    setLoading(true);
    setError("");
    const [siteResult, statusResult] = await Promise.allSettled([
      fetchSites(siteController.signal), fetchStatus(statusController.signal),
    ]);
    if (sitesRequest.current !== siteController) return;
    if (siteResult.status === "fulfilled") {
      setSites(siteResult.value);
      setDisabled(false);
      setError("");
      setObservedAt(Date.now());
    } else if (!(siteResult.reason instanceof DOMException && siteResult.reason.name === "AbortError")) {
      setDisabled(siteResult.reason instanceof PagesDisabledError);
      setError((siteResult.reason as Error).message);
    }
    if (statusResult.status === "fulfilled") {
      setStatus(statusResult.value);
      setStatusError("");
    } else if (!(statusResult.reason instanceof DOMException && statusResult.reason.name === "AbortError")) {
      setStatusError((statusResult.reason as Error).message);
    }
    setLoading(false);
  }, [token]);

  useEffect(() => {
    void load();
    const poll = window.setInterval(() => void load(), 15_000);
    const clock = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => {
      window.clearInterval(poll);
      window.clearInterval(clock);
      sitesRequest.current?.abort();
      statusRequest.current?.abort();
      Object.values(busyRef.current).forEach((controller) => controller.abort());
    };
  }, [load]);

  const versions = useMemo(() => sites?.reduce((count, site) => count + site.deployments.length, 0) ?? 0, [sites]);
  const age = observedAt === null ? "" : ageLabel(elapsed(now, observedAt));
  const siteBusy = (name: string) => Object.keys(busy).some((key) => key.startsWith(`${name}:`));

  function prepareRestore(site: Site, deployment: Deployment) {
    if (!status || statusError) {
      setNotices((current) => ({ ...current, [site.name]: { text: "Hostname mappings could not be refreshed. Retry before changing the live build so you can see which addresses are affected.", ok: false } }));
      return;
    }
    const hosts = status.routes.filter((route) => route.is_static && route.target === `site:${site.name}`).map((route) => route.host);
    setReview({ site, deployment, current: site.deployments.find((entry) => entry.live), hosts });
  }

  async function confirmRestore() {
    const action = review;
    if (!action) return;
    setReview(null);
    const key = `${action.site.name}:${action.deployment.id}`;
    const controller = new AbortController();
    setBusy((current) => ({ ...current, [key]: controller }));
    setNotices((current) => { const next = { ...current }; delete next[action.site.name]; return next; });
    try {
      await rollback(token, action.site.name, action.deployment.id, controller.signal);
      setNotices((current) => ({ ...current, [action.site.name]: { text: `${action.site.name} now serves ${action.deployment.id.slice(0, 12)}.`, ok: true } }));
      await load();
    } catch (cause) {
      const uncertain = controller.signal.aborted || (cause as Error).name === "TimeoutError";
      setNotices((current) => ({ ...current, [action.site.name]: { text: uncertain ? "The response was interrupted. The build may have changed; refreshing to reconcile what is live." : (cause as Error).message, ok: false } }));
      if (uncertain) await load();
    } finally {
      setBusy((current) => { const next = { ...current }; delete next[key]; return next; });
    }
  }

  function stopRestore(name: string, id: string) {
    busy[`${name}:${id}`]?.abort(new DOMException("Stopped by operator", "AbortError"));
  }

  if (disabled) return <div className="grid content-start gap-5"><header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Publish</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Sites</h1><p className="text-muted-foreground mt-1 text-sm">Static websites Ozal serves and their retained builds.</p></div><div className="grid justify-items-start gap-1 sm:justify-items-end"><Button variant="outline" size="sm" aria-label="Refresh sites" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button><p className="text-muted-foreground text-xs" role={error ? "status" : undefined}>{error ? `Pages could not be read. Refresh failed: ${error}` : "Pages are not enabled on this edge."}</p></div></header><section className="panel"><p className="plate">Pages</p><h2 className="mt-1 text-lg font-medium">Not enabled on this edge</h2><p className="text-muted-foreground mt-2 text-sm">Add a <code className="font-mono">pages</code> block with an absolute <code className="font-mono">dir</code> and start Ozal with <code className="font-mono">-db</code>. Routes can then serve deployments with <code className="font-mono">"site": "name"</code>.</p></section></div>;

  return <div className="grid min-w-0 grid-cols-1 content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Publish</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Sites</h1><p className="text-muted-foreground mt-1 max-w-2xl text-sm">Static websites Ozal serves. Every published version is kept, so going back to an earlier one is one click.</p></div><div className="grid justify-items-start gap-1 sm:justify-items-end"><Button variant="outline" size="sm" aria-label="Refresh sites" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button><p className="text-muted-foreground text-xs" role={error ? "status" : undefined}>{error ? `Showing the last successful read. Refresh failed: ${error}` : age ? `Updated ${age}` : "Reading deployments…"}</p></div></header>

    {statusError && <p className="text-destructive text-sm" role="alert">Hostname mappings could not be refreshed. Retry before changing a live build. <button className="underline" onClick={() => void load()}>Retry</button></p>}
    {error && !sites ? <p className="text-destructive text-sm" role="alert">Deployments could not be read ({error}). <button className="underline" onClick={() => void load()}>Retry</button></p> : !sites ? <p className="text-muted-foreground text-sm" role="status" aria-busy="true">Reading deployments…</p> : <>
      <dl className="grid gap-3 sm:grid-cols-2" aria-label="Sites summary"><Summary value={sites.length} label="websites" /><Summary value={versions} label="versions kept" /></dl>
      {sites.length === 0 ? <section className="panel"><p className="plate">Sites</p><h2 className="mt-1 text-lg font-medium">Nothing deployed yet</h2><p className="text-muted-foreground mt-2 text-sm">Add a static site in Applications and upload its build archive there. Its deployment history appears here.</p><Link to="/applications" className="mt-4 inline-flex items-center gap-1 text-sm underline underline-offset-4">Go to Applications <ArrowUpRight className="size-3.5" aria-hidden="true" /></Link></section> : <div className="grid gap-5">{sites.map((site) => <SiteHistory key={site.name} site={site} busy={siteBusy(site.name)} busyIDs={busy} notice={notices[site.name]} onRestore={(deployment) => prepareRestore(site, deployment)} onStop={(id) => stopRestore(site.name, id)} />)}</div>}
    </>}

    <Publishing token={token} canPublish={!!session?.tools.includes("publishing")} sites={sites ?? []} onChanged={load} />

    <Modal open={!!review} onOpenChange={(open) => { if (!open) setReview(null); }} title="Restore this version?" intro="Review change">
      {review && <><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={review.site.name} /><ReviewRow label="Current" value={review.current ? `Live ${review.current.id.slice(0, 12)}` : "No live deployment recorded"} /><ReviewRow label="Requested" value={`Live ${review.deployment.id.slice(0, 12)}`} /><ReviewRow label="Consequence" value="Requests to the mapped hostnames will receive the selected build as soon as the server switches it." /><ReviewRow label="Mapped hostnames" value={review.hosts.length ? review.hosts.join(", ") : "None"} /></dl><div className="mt-5 flex justify-end gap-2"><Button variant="outline" onClick={() => setReview(null)}>Cancel</Button><Button onClick={() => void confirmRestore()}>Restore version</Button></div></>}
    </Modal>
  </div>;
}

function SiteHistory({ site, busy, busyIDs, notice, onRestore, onStop }: { site: Site; busy: boolean; busyIDs: Record<string, AbortController>; notice?: Notice; onRestore(deployment: Deployment): void; onStop(id: string): void }) {
  return <section className="panel overflow-hidden" aria-labelledby={`site-${site.name}`}><header className="panel-head"><div className="min-w-0"><p className="plate">Site</p><h2 id={`site-${site.name}`} className="mt-1 break-all text-base font-medium">{site.name}</h2><p className="text-muted-foreground mt-0.5 text-xs">Live since {date(site.changed_at)}</p></div><span className="lamp lamp-on" aria-hidden="true" /></header>
    {notice && <p className={`border-b px-5 py-3 text-sm ${notice.ok ? "text-foreground" : "text-destructive"}`} role="status">{notice.text}</p>}
    <ul className="divide-border divide-y" aria-label={`Deployments for ${site.name}`}>{site.deployments.map((deployment) => { const key = `${site.name}:${deployment.id}`; return <li key={deployment.id} className="grid gap-3 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-center sm:px-5"><div className="min-w-0"><p className="break-all font-mono text-sm" title={deployment.id}>{deployment.id.slice(0, 12)}</p><p className="text-muted-foreground mt-0.5 text-xs">{date(deployment.created_at)}</p></div><p className="text-muted-foreground text-xs">{deployment.files} files · {formatSize(deployment.bytes)}</p><div className="flex items-center gap-2">{deployment.live ? <span className="inline-flex items-center gap-2 font-mono text-xs"><span className="lamp lamp-on" aria-hidden="true" />live</span> : <Button variant="outline" size="sm" disabled={busy} onClick={() => onRestore(deployment)} aria-label={`Restore ${deployment.id.slice(0, 12)} for ${site.name}`}><Undo2 className="size-3.5" aria-hidden="true" />{busyIDs[key] ? "Waiting…" : "Restore"}</Button>}{busyIDs[key] && <Button variant="ghost" size="sm" onClick={() => onStop(deployment.id)}>Stop waiting</Button>}</div></li>; })}</ul>
  </section>;
}

function Summary({ value, label }: { value: number; label: string }) { return <div className="bg-card rounded-lg px-4 py-3"><dt className="plate">{label}</dt><dd className="mt-1 text-xl font-medium tabular-nums">{value}</dd></div>; }
function ReviewRow({ label, value }: { label: string; value: string }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[8rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className="break-words">{value}</dd></div>; }
function formatSize(bytes: number) { return bytes < 1024 ? `${bytes} B` : bytes < 1 << 20 ? `${(bytes / 1024).toFixed(1)} KiB` : `${(bytes / (1 << 20)).toFixed(1)} MiB`; }
function date(value: string) { const parsed = new Date(value); return Number.isNaN(parsed.valueOf()) ? "Time unknown" : parsed.toLocaleString(); }
