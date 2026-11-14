import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { Check, Copy, Plus, RefreshCw, ShieldCheck } from "lucide-react";
import { addZone, applyZoneSetup, checkZone, fetchZones, fetchZoneSetup, removeZone, renewZone, setupStatus, type Zone, type ZoneCheck, type ZoneSetup, type ZoneState, type Zones } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { Modal } from "./Modal";
import { useSession } from "./session";
import { TunnelPanel, useIngress } from "./TunnelPanel";

const states: Record<ZoneState, { label: string; lamp: string; detail: string }> = {
  pending: { label: "Waiting to issue", lamp: "lamp-wait", detail: "The certificate is requested as soon as the DNS record below is in place." },
  queued: { label: "In line", lamp: "lamp-wait", detail: "Another domain is being issued; this one is next." },
  issuing: { label: "Issuing certificate", lamp: "lamp-wait", detail: "Proving to the certificate authority that you control this domain." },
  active: { label: "Active", lamp: "lamp-on", detail: "" },
  renewing: { label: "Renewing", lamp: "lamp-on", detail: "The current certificate keeps working while the new one is issued." },
  renewal_failed: { label: "Renewal failing", lamp: "lamp-warn", detail: "Still secure for now, but the last renewal did not work." },
  expiring: { label: "Expires soon", lamp: "lamp-off", detail: "Renewal has been failing for weeks. Fix the DNS record before it expires." },
  failed: { label: "No certificate", lamp: "lamp-off", detail: "Addresses under this domain cannot use HTTPS yet." },
};
const moving = (state: ZoneState) => ["pending", "queued", "issuing", "renewing"].includes(state);
const date = (iso?: string) => iso ? new Date(iso).toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" }) : "";
const daysLeft = (iso?: string) => iso ? Math.max(0, Math.round((Date.parse(iso) - Date.now()) / 86_400_000)) : 0;

export function Domains() {
  const { session } = useSession();
  const navigate = useNavigate();
  const editable = !!session?.tools.includes("applications");
  const [data, setData] = useState<Zones | null>(null);
  const [tlsMode, setTlsMode] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [notices, setNotices] = useState<Record<string, { text: string; ok: boolean }>>({});
  const [checks, setChecks] = useState<Record<string, ZoneCheck>>({});
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [ready, setReady] = useState<Record<string, boolean>>({});
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const [addOpen, setAddOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [added, setAdded] = useState<string | null>(null);
  const [addError, setAddError] = useState("");
  const [addBusy, setAddBusy] = useState(false);
  const [removing, setRemoving] = useState<Zone | null>(null);
  const { ingress, setIngress } = useIngress();
  const tunnel = ingress?.mode === "cloudflare_tunnel";

  const load = async () => {
    setLoading(true);
    try {
      const next = await fetchZones();
      setData(next); setError("");
      if (!next.available && !tlsMode) setTlsMode((await setupStatus(session?.token ?? "").catch(() => null))?.tls_mode ?? "");
    } catch (cause) { setError((cause as Error).message); }
    finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, []);
  useEffect(() => {
    if (!data?.zones.some((zone) => moving(zone.state))) return;
    const timer = window.setInterval(() => { if (data.zones.some((zone) => moving(zone.state))) void load(); }, 5_000);
    return () => window.clearInterval(timer);
  }, [data]);

  const note = (zone: string, text: string, ok: boolean) => setNotices((old) => ({ ...old, [zone]: { text, ok } }));
  async function check(zone: Zone) {
    setBusy((old) => ({ ...old, [zone.name]: "check" }));
    try {
      const result = await checkZone(session?.token ?? "", zone.name);
      setChecks((old) => ({ ...old, [zone.name]: result }));
      note(zone.name, result.challenge.state === "ok" ? "The DNS record is in place." : result.challenge.detail, result.challenge.state === "ok");
    } catch (cause) { note(zone.name, (cause as Error).message, false); }
    finally { setBusy((old) => ({ ...old, [zone.name]: "" })); }
  }
  async function renew(zone: Zone) {
    setBusy((old) => ({ ...old, [zone.name]: "renew" }));
    try { await renewZone(session?.token ?? "", zone.name); note(zone.name, "Requested. The certificate is being issued now.", true); await load(); }
    catch (cause) { note(zone.name, (cause as Error).message, false); }
    finally { setBusy((old) => ({ ...old, [zone.name]: "" })); }
  }
  async function submitAdd(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!data || !session?.token) return;
    setAddBusy(true); setAddError("");
    try {
      const name = newName.trim().toLowerCase().replace(/\.$/, "").replace(/^https?:\/\//, "").replace(/\/.*$/, "");
      await addZone(session.token, name, data.config_sha256);
      setAdded(name); await load();
    } catch (cause) { setAddError((cause as Error).message); }
    finally { setAddBusy(false); }
  }
  async function remove() {
    if (!removing || !data || !session?.token) return;
    try { await removeZone(session.token, removing.name, data.config_sha256); setRemoving(null); await load(); }
    catch (cause) { note(removing.name, (cause as Error).message, false); setRemoving(null); }
  }
  const addedZone = data?.zones.find((zone) => zone.name === added) ?? null;
  const openAdd = () => { setNewName(""); setAdded(null); setAddError(""); setAddOpen(true); };

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Access</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Domains</h1><p className="text-muted-foreground mt-1 max-w-2xl text-sm">{tunnel ? "Visitors reach your applications through Cloudflare, which serves HTTPS for every address. Ozal creates the tunnel and each application's DNS record." : "Each domain gets its own HTTPS certificate, renewed automatically. Add a domain once and every address directly under it can be used for an application."}</p></div><div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button>{editable && data?.available && <Button size="sm" onClick={openAdd}><Plus className="size-3.5" aria-hidden="true" />Add domain</Button>}</div></header>
    {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
    {tunnel && ingress && <TunnelPanel ingress={ingress} editable={editable} onChange={setIngress} />}
    {data && !data.available && tunnel ? null : data && !data.available ? <section className="bg-card rounded-lg p-6"><div className="flex items-start gap-4"><span className="bg-muted grid size-10 shrink-0 place-items-center rounded-lg"><ShieldCheck className="text-muted-foreground size-5" aria-hidden="true" /></span><div className="max-w-2xl"><h2 className="font-medium">{tlsMode === "automatic" ? "Certificates are issued per address" : "HTTPS is handled outside this edge"}</h2><p className="text-muted-foreground mt-1 text-sm">{tlsMode === "automatic" ? "This edge gets a certificate for each address the first time someone visits it. There is nothing to set up here: add an application and point its DNS at this server." : "This edge serves plain HTTP. HTTPS is either provided in front of it (for example by Cloudflare's proxy) or not enabled yet."}</p><p className="text-muted-foreground mt-3 text-sm">To manage domains here — one certificate per domain covering every address under it — the host needs <code className="font-mono text-xs">tls.dns01</code> in its configuration. The setup is described under “HTTPS and DNS” in the reference guide.</p></div></div></section> : data ? data.zones.length === 0 ? <section className="bg-card px-5 py-12 text-center"><p className="font-medium">No domains yet</p><p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Add the domain you own, such as example.com. Ozal then gets a certificate for it and for every address one level below, like app.example.com.</p>{editable && <Button className="mt-4" onClick={openAdd}><Plus className="size-4" aria-hidden="true" />Add domain</Button>}</section> : <ul className="grid gap-4" aria-label="Domains">{data.zones.map((zone) => <DomainCard key={zone.name} zone={zone} data={data} editable={editable} busy={busy[zone.name] ?? ""} check={checks[zone.name]} notice={notices[zone.name]} ready={ready[zone.name]} touched={!!touched[zone.name]} onReady={(value) => setReady((old) => ({ ...old, [zone.name]: value }))} onApplied={() => { setTouched((old) => ({ ...old, [zone.name]: true })); void load(); }} onCheck={() => void check(zone)} onRenew={() => void renew(zone)} onRemove={() => setRemoving(zone)} onApplications={() => navigate("/applications")} />)}</ul> : !error ? <p className="text-muted-foreground text-sm" role="status">Loading domains…</p> : null}

    <Modal open={addOpen} onOpenChange={(open) => { setAddOpen(open); if (!open) { setAdded(null); setAddError(""); } }} title={added ? `Connect ${added}` : "Add a domain"} intro={added ? data?.provider ? "One button does the rest at Cloudflare. After that, certificates are issued and renewed on their own." : "A few settings at your DNS provider, done once. After that, certificates are issued and renewed on their own." : "The domain you own. Ozal will hold one certificate for it and every address directly under it."} wide>
      {!added ? <form className="grid gap-4" onSubmit={(event) => void submitAdd(event)}><div className="grid gap-2"><Label htmlFor="zone-name">Domain</Label><Input id="zone-name" required value={newName} onChange={(event) => setNewName(event.target.value)} placeholder="example.com" autoComplete="off" spellCheck={false} className="font-mono" /><p className="text-muted-foreground text-xs">Just the domain, without <span className="font-mono">www</span> or <span className="font-mono">https://</span>. Subdomains such as <span className="font-mono">lab.example.com</span> also work.</p></div>{addError && <p className="text-destructive text-sm" role="alert">{addError}</p>}<div className="flex justify-end gap-2"><Button variant="ghost" onClick={() => setAddOpen(false)}>Cancel</Button><Button type="submit" disabled={addBusy || !newName.trim()}>{addBusy ? "Adding…" : "Add domain"}</Button></div></form> : addedZone && data?.setup ? <div className="grid gap-5"><ZoneSetupPanel zone={addedZone} editable={editable} onApplied={() => void load()} /><p className="text-muted-foreground flex items-center gap-2 text-sm"><span className={`lamp ${states[addedZone.state].lamp}`} aria-hidden="true" />Certificate: {states[addedZone.state].label.toLowerCase()}. It is issued on its own once the DNS record is in place.</p><div className="flex justify-end"><Button onClick={() => setAddOpen(false)}>Done</Button></div></div> : addedZone ? <><AddSteps zone={addedZone} busy={busy[addedZone.name] ?? ""} notice={notices[addedZone.name]} onCheck={() => void check(addedZone)} /><div className="mt-6 flex justify-end"><Button onClick={() => setAddOpen(false)}>Done</Button></div></> : <p role="status" className="text-muted-foreground text-sm">Loading domain setup…</p>}
    </Modal>
    <Modal open={!!removing} onOpenChange={(open) => { if (!open) setRemoving(null); }} title="Remove this domain?" intro="Review change"><div className="grid gap-5"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={removing?.name ?? ""} mono /><ReviewRow label="Current" value="Certificate managed by this edge" /><ReviewRow label="Requested" value="No certificate for this domain" /><ReviewRow label="Consequence" value="The edge stops serving and renewing this domain's certificate. Applications must be moved or removed first." /><ReviewRow label="Details" value="Delete the _acme-challenge record at your DNS provider too: while it exists, this edge can still prove control of the domain." /></dl><div className="flex flex-wrap justify-end gap-2"><Button variant="outline" onClick={() => setRemoving(null)}>Cancel</Button><Button variant="destructive" onClick={() => void remove()}>Remove domain</Button></div></div></Modal>
  </div>;
}

function DomainCard({ zone, data, editable, busy, check, notice, ready, touched, onReady, onApplied, onCheck, onRenew, onRemove, onApplications }: { zone: Zone; data: Zones; editable: boolean; busy: string; check?: ZoneCheck; notice?: { text: string; ok: boolean }; ready?: boolean; touched: boolean; onReady(value: boolean): void; onApplied(): void; onCheck(): void; onRenew(): void; onRemove(): void; onApplications(): void }) {
  const state = states[zone.state];
  const needsRecord = zone.challenge.delegated && (["pending", "failed"].includes(zone.state) || check?.challenge.state === "bad");
  return <li className="bg-card rounded-lg"><div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-5"><div className="min-w-0"><h2 className="truncate text-base font-medium">{zone.name}</h2><p className="text-muted-foreground mt-0.5 text-xs">Covers {zone.name} and every address like <span className="font-mono">app.{zone.name}</span>{zone.source === "config" ? " · set in the server's configuration" : ""}</p></div><span className="flex items-center gap-2 text-sm"><span className={`lamp ${state.lamp}`} aria-hidden="true" />{state.label}</span></div>
    <dl className="grid gap-x-8 gap-y-3 px-5 py-4 text-sm sm:grid-cols-3"><div><dt className="text-muted-foreground text-xs">Certificate</dt><dd className="mt-0.5">{zone.not_after ? <>Valid until {date(zone.not_after)} <span className="text-muted-foreground">· {daysLeft(zone.not_after)} days</span></> : "Not issued yet"}</dd></div><div><dt className="text-muted-foreground text-xs">Next renewal</dt><dd className="mt-0.5">{zone.renews_at ? date(zone.renews_at) : zone.next_attempt ? `Retry ${date(zone.next_attempt)}` : "After the first certificate"}</dd></div><div className="min-w-0"><dt className="text-muted-foreground text-xs">Applications</dt><dd className="mt-0.5 truncate">{zone.hosts.length ? <button type="button" className="hover:underline" onClick={onApplications}>{zone.hosts.length} · {zone.hosts.slice(0, 2).join(", ")}{zone.hosts.length > 2 ? "…" : ""}</button> : <span className="text-muted-foreground">None yet</span>}</dd></div></dl>
    {(state.detail || zone.last_error) && <div className="px-5 pb-1">{state.detail && <p className="text-muted-foreground text-sm">{state.detail}</p>}{zone.last_error && zone.state !== "active" && <p className="text-muted-foreground mt-1 break-words font-mono text-xs">Last error: {zone.last_error}</p>}</div>}
    {data.setup ? <details className="group px-5 pt-2 pb-1" open={ready === false || touched}><summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm">{ready === false ? "Finish the setup at your DNS provider" : "Setup at your DNS provider"}</summary><div className="mt-3"><ZoneSetupPanel zone={zone} editable={editable} onApplied={onApplied} onReady={onReady} /></div></details> : zone.challenge.delegated && <details className="group px-5 pt-2 pb-1" open={needsRecord}><summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm">{needsRecord ? "Create this DNS record at your DNS provider" : "DNS record for certificates"}</summary><div className="mt-3 grid gap-2"><p className="text-muted-foreground text-xs">Created once. It lets Ozal prove it controls {zone.name} for every renewal, without access to your DNS account.</p><DnsRecord name={zone.challenge.name} value={zone.challenge.target} /></div></details>}
    {notice && <p className={`px-5 pt-2 text-sm ${notice.ok ? "text-success" : "text-destructive"}`} role="status">{notice.text}</p>}
    {editable && <div className="mt-3 flex flex-wrap gap-2 border-t px-5 py-3">{zone.challenge.delegated && <Button variant="outline" size="sm" disabled={!!busy} onClick={onCheck}>{busy === "check" ? "Checking…" : "Check DNS record"}</Button>}<Button variant="outline" size="sm" disabled={!!busy || zone.state === "issuing" || zone.state === "queued"} onClick={onRenew}>{busy === "renew" ? "Requesting…" : zone.not_after ? "Renew now" : "Issue certificate now"}</Button>{zone.source === "runtime" && <Button variant="ghost" size="sm" className="text-muted-foreground ml-auto" onClick={onRemove}>Remove domain</Button>}</div>}
  </li>;
}

function DnsRecord({ name, value }: { name: string; value: string }) {
  const [copied, setCopied] = useState("");
  async function copy(label: string, text: string) { try { await navigator.clipboard.writeText(text); setCopied(label); window.setTimeout(() => setCopied(""), 1800); } catch { setCopied(""); } }
  return <div className="bg-background grid gap-3 rounded-lg border p-3 sm:grid-cols-[4.5rem_minmax(0,1fr)_minmax(0,1fr)]"><div><p className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">Type</p><code className="font-mono text-xs font-semibold">CNAME</code></div>{([["Name", name], ["Value", value]] as const).map(([label, text]) => <div className="min-w-0" key={label}><p className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">{label}</p><div className="flex items-center gap-1"><code className="min-w-0 flex-1 break-all font-mono text-xs">{text}</code><button type="button" className="text-muted-foreground hover:text-foreground hover:bg-accent shrink-0 rounded p-1" aria-label={`Copy ${label.toLowerCase()} ${text}`} onClick={() => void copy(label, text)}>{copied === label ? <Check className="text-success size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}</button></div></div>)}<span className="sr-only" role="status">{copied ? "Copied" : ""}</span></div>;
}

function AddSteps({ zone, busy, notice, onCheck }: { zone: Zone; busy: string; notice?: { text: string; ok: boolean }; onCheck(): void }) {
  return <ol className="grid gap-5"><li className="grid gap-2"><p className="text-sm font-medium">1. Create this record at the company that runs your DNS</p>{zone.challenge.delegated ? <><DnsRecord name={zone.challenge.name} value={zone.challenge.target} /><p className="text-muted-foreground text-xs">If your provider offers a proxy (Cloudflare's orange cloud), leave it off for this record.</p></> : <p className="text-muted-foreground text-sm">Nothing to create: this edge writes the record itself through your DNS provider's API.</p>}</li><li className="grid gap-2"><p className="text-sm font-medium">2. Check it</p><div className="flex flex-wrap items-center gap-3"><Button variant="outline" size="sm" disabled={!!busy} onClick={onCheck}>{busy === "check" ? "Checking…" : "Check DNS record"}</Button>{notice && <span className={`text-sm ${notice.ok ? "text-success" : "text-muted-foreground"}`} role="status">{notice.text}</span>}</div><p className="text-muted-foreground text-xs">New records can take a few minutes to appear.</p></li><li className="grid gap-2"><p className="text-sm font-medium">3. The certificate is issued automatically</p><p className="text-muted-foreground flex items-center gap-2 text-sm"><span className={`lamp ${states[zone.state].lamp}`} aria-hidden="true" />{states[zone.state].label}</p></li></ol>;
}

function ZoneSetupPanel({ zone, editable, onApplied, onReady }: { zone: Zone; editable: boolean; onApplied(): void; onReady?(ready: boolean): void }) {
  const { session } = useSession();
  const [setup, setSetup] = useState<ZoneSetup | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"" | "check" | "apply">("");
  const [result, setResult] = useState<{ text: string; ok: boolean } | null>(null);
  const visible = useMemo(() => setup?.steps.filter((step) => step.state !== "not_needed") ?? [], [setup]);
  const doable = !!setup?.provider && visible.some((step) => step.state === "todo" && step.action);
  const left = visible.filter((step) => step.state !== "done").length;
  useEffect(() => { void load(); }, [zone.name]);
  useEffect(() => { if (setup) onReady?.(setup.ready); }, [setup?.ready]);
  async function load() { setBusy((current) => current || "check"); try { setSetup(await fetchZoneSetup(zone.name)); setError(""); } catch (cause) { setError((cause as Error).message); } finally { setBusy((current) => current === "check" ? "" : current); } }
  async function apply() { if (!session?.token) return; setBusy("apply"); setResult(null); try { const next = await applyZoneSetup(session.token, zone.name); setSetup(next); const labels: Record<string, string> = { create_cname: "created the DNS record for certificates", set_strict: "set encryption to Full (strict)", upload_certificate: "uploaded this edge's proxy certificate", enable_origin_pull: "turned on Authenticated Origin Pulls", set_always_https: "turned on Always Use HTTPS" }; const done = (next.applied ?? []).map((action) => labels[action] ?? action); setResult({ ok: true, text: done.length ? `Done: ${done.join(", ")}.` : "Nothing was left to do." }); onApplied(); } catch (cause) { setResult({ ok: false, text: (cause as Error).message }); await load(); } finally { setBusy(""); } }
  const lamps = { done: "lamp-on", todo: "lamp-wait", blocked: "lamp-off", unknown: "lamp-warn", not_needed: "" };
  const words = { done: "Done", todo: "To do", blocked: "Needs you", unknown: "Check by hand", not_needed: "" };
  const actionWords: Record<string, string> = { create_cname: "created the DNS record for certificates", set_strict: "set encryption to Full (strict)", upload_certificate: "uploaded this edge's proxy certificate", enable_origin_pull: "turned on Authenticated Origin Pulls", set_always_https: "turned on Always Use HTTPS" };
  return <section className="grid gap-3" aria-label={`Setup of ${zone.name}`}>{!setup && !error ? <p className="text-muted-foreground text-sm" role="status">Checking {zone.name} at its DNS provider…</p> : error ? <p className="text-destructive text-sm" role="alert">{error}</p> : setup && <><div className="flex flex-wrap items-center justify-between gap-2"><p className="text-sm font-medium">{setup.ready ? "Everything is in place" : left === 1 ? "One step left" : `${left} steps left`}</p><div className="flex gap-2"><Button variant="ghost" size="sm" disabled={!!busy} onClick={() => void load()}>{busy === "check" ? "Checking…" : "Check again"}</Button>{doable && editable && <Button size="sm" disabled={!!busy} onClick={() => void apply()}>{busy === "apply" ? "Setting up…" : "Set up in Cloudflare"}</Button>}</div></div><ol className="grid gap-2">{visible.map((step) => <li key={step.name} className="bg-background rounded-lg border p-3"><div className="flex flex-wrap items-baseline justify-between gap-2"><p className="flex items-center gap-2 text-sm font-medium"><span className={`lamp ${lamps[step.state]}`} aria-hidden="true" />{step.title}</p><span className="text-muted-foreground text-xs">{words[step.state]}</span></div><p className="text-muted-foreground mt-1 text-sm">{step.detail}</p>{step.state !== "done" && !(doable && step.action) && <>{step.name === "challenge" && zone.challenge.delegated ? <><div className="mt-2"><DnsRecord name={zone.challenge.name} value={zone.challenge.target} /></div><p className="text-muted-foreground mt-1 text-xs">Leave the proxy (Cloudflare's orange cloud) off for this record.</p></> : step.manual && <p className="mt-2 text-sm">{step.manual}</p>}</>}</li>)}</ol>{result && <p className={`text-sm ${result.ok ? "text-success" : "text-destructive"}`} role="status">{result.text}</p>}{setup.error && <p className="text-muted-foreground text-xs">{setup.error}</p>}{!setup.provider && !setup.ready && <p className="text-muted-foreground bg-muted/40 rounded-lg border p-3 text-xs leading-relaxed">Ozal can do these steps for you. Give the host a Cloudflare API token limited to your domains, with <span className="text-foreground">DNS · Edit</span>, <span className="text-foreground">Zone Settings · Edit</span> and <span className="text-foreground">SSL and Certificates · Edit</span>, as <code className="font-mono">dns.cloudflare.token</code> in its configuration. A button then sets up each new domain.</p>}</>}</section>;
}

function ReviewRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className={`${mono ? "font-mono break-all" : ""}`}>{value}</dd></div>; }
