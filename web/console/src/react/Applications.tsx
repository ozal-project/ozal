import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ArrowUpRight, Boxes, ExternalLink, Layers, Plug, Plus, RefreshCw, Server } from "lucide-react";
import { fetchConnectors, fetchGroups, fetchPublishing, fetchZones, setupStatus, type ApplicationRoute, type SetupStatus } from "$lib/api";
import { servedBy, whoMayEnter } from "$lib/wording";
import { Button } from "$lib/components/ui/button/react";
import { RowMenu } from "./RowMenu";
import { useSession } from "./session";
import { ApplicationWizard } from "./ApplicationWizard";
import { DomainConnect } from "./DomainConnect";
import { ArtifactUpload } from "./ArtifactUpload";
import { Modal } from "./Modal";

type GitHubSource = { repository: string; branch: string };
type WizardTarget = { route: ApplicationRoute | null; githubReturn?: boolean };

export function Applications() {
  const { session } = useSession();
  const navigate = useNavigate();
  const token = session?.token ?? "";
  const canApplications = !!session?.tools.includes("applications");
  const canConnectors = !!session?.tools.includes("connectors");
  const canPublishing = !!session?.tools.includes("publishing");
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [groups, setGroups] = useState<string[]>([]);
  const [zones, setZones] = useState<string[]>([]);
  const [connectors, setConnectors] = useState<string[] | null>(null);
  const [sources, setSources] = useState<Record<string, GitHubSource>>({});
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [wizard, setWizard] = useState<WizardTarget | null>(null);
  const [connectHost, setConnectHost] = useState<string | null>(null);
  const [removeRoute, setRemoveRoute] = useState<ApplicationRoute | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState("");
  const [publishSite, setPublishSite] = useState<string | null>(null);

  async function load() {
    if (!token) return;
    setLoading(true); setError("");
    try {
      const [next, catalog, domains, links, publishing] = await Promise.all([
        setupStatus(token), fetchGroups(token).catch(() => null), fetchZones().catch(() => null),
        canConnectors ? fetchConnectors(token).catch(() => null) : Promise.resolve(null),
        canPublishing ? fetchPublishing(token).catch(() => null) : Promise.resolve(null),
      ]);
      const bySite = Object.fromEntries((publishing?.projects ?? []).flatMap((project) => project.deployers.flatMap((deployer) => {
        if (deployer.type !== "github") return [];
        const workflow = deployer.github.workflow_ref ?? "";
        const cut = workflow.indexOf("/.github/workflows/");
        return [[project.site, { repository: cut > 0 ? workflow.slice(0, cut) : "GitHub", branch: deployer.github.ref.replace(/^refs\/heads\//, "") }]];
      })));
      setStatus(next); setSources(bySite);
      if (catalog) setGroups(catalog.map((group) => group.name));
      setZones(domains?.available ? domains.zones.map((zone) => zone.name) : []);
      setConnectors(links ? links.filter((connector) => !connector.revoked).map((connector) => connector.name) : null);
    } catch (cause) { setError((cause as Error).message); }
    finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, [token]);
  useEffect(() => {
    const query = new URLSearchParams(window.location.search);
    if ((query.has("code") && query.has("state")) || query.has("setup_action") || query.has("installation_id")) {
      setWizard({ route: null, githubReturn: true });
    }
  }, []);

  const scheme = status?.tls_enabled ? "https" : "http";
  const routes = status?.routes ?? [];
  const signIn = !!status?.methods.length;
  const privateCount = useMemo(() => routes.filter((route) => !!(route.access || route.auth_required)).length, [routes]);
  async function confirmRemove() {
    if (!removeRoute || !status || !token) return;
    setRemoveBusy(true); setRemoveError("");
    try {
      const response = await fetch(`/_/api/apps/${encodeURIComponent(removeRoute.host)}`, { method: "DELETE", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ expected: status.config_sha256 }), signal: AbortSignal.timeout(15_000) });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Removal could not be confirmed. Refresh before retrying.");
      setRemoveRoute(null); await load();
    } catch (cause) { setRemoveError((cause as Error).message); }
    finally { setRemoveBusy(false); }
  }

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Access</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Applications</h1><p className="text-muted-foreground mt-1 text-sm">Each address Ozal serves: what is behind it and who can open it.</p><nav className="console-tabs" aria-label="Application views">{canApplications && <Link aria-current="page" className="console-tab" to="/applications">Applications</Link>}<Link className="console-tab" to="/routes">Routing &amp; health</Link></nav></div><div className="flex gap-2"><Button variant="ghost" size="icon-sm" aria-label="Refresh configuration" title="Refresh" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-4 ${loading ? "animate-spin" : ""}`} aria-hidden="true" /></Button>{canApplications && <Button size="sm" onClick={() => setWizard({ route: null })} disabled={!status}><Plus className="size-3.5" aria-hidden="true" />Add application</Button>}</div></header>
    {error && <p className="text-destructive -mt-3 text-sm" role="alert">{error}</p>}
    {status && !signIn && <div className="border-warning/40 bg-warning/[0.06] rounded-lg border px-4 py-3 text-sm"><p className="font-medium">Sign-in is not set up yet, so every application is public.</p><p className="text-muted-foreground mt-0.5">To make an app private, enable passkeys (<code className="font-mono text-xs">auth.passkeys</code>) or an external login in this server's configuration. <Link className="link" to="/integrations">How sign-in works</Link></p></div>}
    {!status ? <p className="text-muted-foreground text-sm" role="status">{loading ? "Reading this server's applications…" : "Loading…"}</p> : routes.length === 0 ? <div className="bg-card px-5 py-12 text-center"><p className="font-medium">Nothing online yet</p><p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Add an application: what serves it, its address and who can open it. Ozal then shows the one DNS record it needs and checks it for you.</p>{canApplications && <Button className="mt-4" onClick={() => setWizard({ route: null })}><Plus className="size-4" aria-hidden="true" />Add application</Button>}</div> : <><ul className="bg-card divide-y rounded-lg" aria-label="Applications">{routes.map((app) => <ApplicationRow key={app.host} app={app} name={status.app_names?.[app.host]} scheme={scheme} source={app.site ? sources[app.site] : undefined} canApplications={canApplications} onEdit={() => setWizard({ route: app })} onConnect={() => setConnectHost(app.host)} onPublish={() => setPublishSite(app.site ?? null)} onRemove={() => { setRemoveError(""); setRemoveRoute(app); }} onMenu={(action) => { if (action === "activity") navigate("/activity"); else if (action === "routes") navigate("/routes"); else if (action === "sites") navigate("/sites"); else if (action === "connectors") navigate("/connectors"); }} />)}</ul><p className="text-muted-foreground -mt-3 text-xs">{routes.length} {routes.length === 1 ? "application" : "applications"} · {privateCount} private · {routes.length - privateCount} public</p></>}

    {wizard && status && <ApplicationWizard key={wizard.route?.host ?? (wizard.githubReturn ? "github-return" : "new")} route={wizard.route} githubReturn={wizard.githubReturn} status={status} groups={groups} zones={zones} connectors={connectors} token={token} canPublishing={canPublishing} onChanged={load} onClose={() => setWizard(null)} onSaved={async () => { setWizard(null); await load(); }} />}
    {connectHost && <DomainConnect host={connectHost} https={!!status?.tls_enabled} token={token} onClose={() => setConnectHost(null)} />}
    {publishSite && <ArtifactUpload site={publishSite} token={token} onClose={() => setPublishSite(null)} />}
    {removeRoute && <ConfirmModal open onClose={() => setRemoveRoute(null)} title="Remove this application hostname?" target={removeRoute.host} consequence="This hostname will stop forwarding or serving its site. Retained publications and application data are not deleted. DNS stays as it is." detail="The server refuses removal of the last route or a project’s last domain. Disconnect the publisher first when retiring that project." confirm="Remove application" busy={removeBusy} error={removeError} onConfirm={() => void confirmRemove()} />}
  </div>;
}

function ApplicationRow({ app, name, scheme, source, canApplications, onEdit, onConnect, onPublish, onRemove, onMenu }: { app: ApplicationRoute; name?: string; scheme: string; source?: GitHubSource; canApplications: boolean; onEdit(): void; onConnect(): void; onPublish(): void; onRemove(): void; onMenu(action: "activity" | "routes" | "sites" | "connectors"): void }) {
  const access = whoMayEnter(app);
  const AppIcon = app.redirect ? ArrowUpRight : app.site ? Layers : app.connector ? Plug : app.root ? Boxes : Server;
  return <li className="grid gap-x-6 gap-y-3 px-4 py-4 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto] md:items-center"><div className="flex min-w-0 items-center gap-3"><span className="bg-muted text-muted-foreground grid size-9 shrink-0 place-items-center rounded-lg"><AppIcon className="size-4" aria-hidden="true" /></span><div className="min-w-0"><div className="flex items-center gap-1.5"><h2 className="truncate text-sm font-medium">{name || app.host}</h2><a href={`${scheme}://${app.host}/`} target="_blank" rel="noreferrer" className="text-muted-foreground hover:text-foreground shrink-0" aria-label={`Open ${app.host} in a new tab`}><ExternalLink className="size-3.5" aria-hidden="true" /></a></div><p className="text-muted-foreground truncate text-xs">{name && <><span className="font-mono">{app.host}</span> · </>}{servedBy(app)}{source && <> · from <span className="font-mono">{source.repository}</span> · {source.branch}</>}</p></div></div><p className={`flex min-w-0 items-center gap-2 pl-12 text-xs md:pl-0 ${access.tone === "public" ? "text-warning" : "text-muted-foreground"}`}><span className={`lamp ${access.tone === "public" ? "lamp-warn" : "lamp-on"}`} aria-hidden="true" /><span className="truncate">{access.text}</span></p><div className="flex items-center gap-2 pl-12 md:justify-end md:pl-0">{canApplications && <Button variant="outline" size="sm" aria-label={`Edit ${app.host}`} onClick={onEdit}>Edit</Button>}<RowMenu label={`More actions for ${app.host}`} items={[{ label: "DNS & HTTPS", hint: "The DNS record it needs and a live check", onSelect: onConnect },...(app.site ? [{ label: "Publish a new version", hint: "Upload new files for this website", onSelect: onPublish },{ label: "Versions & rollback", hint: "Go back to an earlier version", onSelect: () => onMenu("sites") }] : []),...(app.connector ? [{ label: "Connector", hint: `Status of ${app.connector}`, onSelect: () => onMenu("connectors") }] : []),{ label: "Who opened it", hint: "Access decisions for this address", onSelect: () => onMenu("activity") },...(canApplications ? [{ label: "Remove", hint: "Stop serving this address", destructive: true, onSelect: onRemove }] : [])]} /></div></li>;
}

function ConfirmModal({ open, onClose, title, target, consequence, detail, confirm, busy, error, onConfirm }: { open: boolean; onClose(): void; title: string; target: string; consequence: string; detail: string; confirm: string; busy: boolean; error: string; onConfirm(): void }) {
  return <Modal open={open} onOpenChange={(next) => { if (!next) onClose(); }} title={title} intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={target} mono /><ReviewRow label="Current" value="Configured route" /><ReviewRow label="Requested" value="No route for this hostname" /><ReviewRow label="Consequence" value={consequence} /><ReviewRow label="Details" value={detail} /></dl>{error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}<div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={onClose}>Cancel</Button><Button variant="destructive" disabled={busy} onClick={onConfirm}>{busy ? "Removing…" : confirm}</Button></div></Modal>;
}
function ReviewRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className={mono ? "break-all font-mono" : ""}>{value}</dd></div>; }
