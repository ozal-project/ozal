import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy, ExternalLink, RefreshCw } from "lucide-react";
import { checkConnection, createRecord, type CheckState, type Connection } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { Modal } from "./Modal";

const lamp = (state: CheckState) => state === "ok" ? "lamp-on" : state === "bad" ? "lamp-off" : "lamp-wait";
const steps = (report: Connection) => [{ label: "DNS", ...report.dns }, { label: "Certificate", ...report.certificate }, { label: "Reaches this edge", ...report.reach }];

export function DomainConnect({ host, https, token, onClose, inline = false }: { host: string; https: boolean; token: string; onClose?(): void; inline?: boolean }) {
  const [report, setReport] = useState<Connection | null>(null);
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState("");
  const [copied, setCopied] = useState(false);
  const attempts = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const liveReport = useRef<Connection | null>(null);

  const check = useCallback(async () => {
    if (controller.current && !controller.current.signal.aborted) return;
    setChecking(true);
    const next = new AbortController(); controller.current = next;
    try { const result = await checkConnection(token, host, next.signal); if (!next.signal.aborted) { setReport(result); liveReport.current = result; setError(""); } }
    catch (cause) { if (!next.signal.aborted) setError((cause as Error).message); }
    finally { if (controller.current === next) controller.current = null; if (!next.signal.aborted) { setChecking(false); attempts.current += 1; } }
  }, [host, token]);
  useEffect(() => {
    attempts.current = 0; liveReport.current = null; setReport(null); void check();
    const timer = window.setInterval(() => { if (!liveReport.current?.connected && attempts.current < 120 && document.visibilityState === "visible") void check(); }, 5_000);
    return () => { window.clearInterval(timer); controller.current?.abort(); controller.current = null; };
  }, [check]);

  const needsRecord = !!report?.record && report.dns.state !== "ok";
  async function create() {
    setCreating(true); setError(""); setCreated("");
    try { const record = await createRecord(token, host); setCreated(`${record.type} record created. DNS usually answers within a minute.`); attempts.current = 0; await check(); }
    catch (cause) { setError((cause as Error).message); }
    finally { setCreating(false); }
  }
  async function copy() {
    if (!report?.record) return;
    const record = report.record;
    try { await navigator.clipboard.writeText(`${record.name} ${record.type} ${record.content}`); setCopied(true); window.setTimeout(() => setCopied(false), 2000); } catch { setCopied(false); }
  }

  const content = <>
    <div className="grid gap-5">
      {report?.record ? <section aria-labelledby="domain-record"><div className="mb-2 flex items-baseline justify-between gap-3"><h3 id="domain-record" className="text-sm font-medium">{needsRecord ? "Add this DNS record" : "DNS record"}</h3><span className="text-muted-foreground text-xs">at your DNS provider</span></div><div className="bg-muted/40 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-lg border p-3 font-mono text-xs sm:grid-cols-[auto_minmax(0,1.6fr)_minmax(0,1fr)_auto] sm:items-center"><span className="text-muted-foreground sm:hidden">Type</span><span className="font-semibold">{report.record.type}</span><span className="text-muted-foreground sm:hidden">Name</span><span className="break-all">{report.record.name}</span><span className="text-muted-foreground sm:hidden">Points to</span><span className="break-all">{report.record.content}</span><span className="text-muted-foreground sm:hidden">Proxy</span><span className="font-sans">{report.record.proxied ? "Proxied" : "DNS only"}</span></div><div className="mt-2 flex flex-wrap items-center gap-2"><Button variant="outline" size="sm" onClick={() => void copy()}>{copied ? <Check className="text-success size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}{copied ? "Copied" : "Copy record"}</Button>{report.provider === "cloudflare" && needsRecord && <Button size="sm" disabled={creating} onClick={() => setCreateOpen(true)}>{creating ? "Creating…" : "Create it in Cloudflare"}</Button>}</div></section> : report && report.dns.state !== "ok" ? <p className="text-muted-foreground text-sm">Point <span className="text-foreground font-mono">{host}</span> at this server's public address at your DNS provider. To show the exact record here, set <code className="font-mono">dns.target</code> in the configuration.</p> : null}
      <section aria-label={`Connection of ${host}`} aria-live="polite">{!report ? <p className="text-muted-foreground flex items-center gap-2 text-sm"><span className="lamp lamp-wait" aria-hidden="true" />Checking {host}…</p> : <ol className="divide-y rounded-lg border">{steps(report).map((step) => <li key={step.label} className="flex gap-3 px-4 py-3"><span className={`lamp mt-1.5 ${lamp(step.state)}`} aria-hidden="true" /><div className="min-w-0"><p className="text-sm font-medium">{step.label} <span className="sr-only">: {step.state === "ok" ? "done" : step.state === "bad" ? "needs attention" : "waiting"}</span></p><p className="text-muted-foreground text-sm">{step.detail}</p></div></li>)}</ol>}</section>
      {created && <p className="text-sm" role="status">{created}</p>}{error && <p className="text-destructive text-sm" role="alert">{error}</p>}
      <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-muted-foreground text-xs">{report?.connected ? <>Connected. Visitors reach this edge at {host}.</> : attempts.current >= 120 ? "Stopped checking automatically." : "Checking again every few seconds."}</p><div className="flex gap-2">{report?.connected && <a className="inline-flex h-8 items-center gap-1 rounded-lg border px-2.5 text-[0.8rem] font-medium" href={`${https ? "https" : "http"}://${host}/`} target="_blank" rel="noreferrer">Open <ExternalLink className="size-3.5" aria-hidden="true" /></a>}<Button variant="ghost" size="sm" disabled={checking} onClick={() => { attempts.current = 0; void check(); }}><RefreshCw className={`size-3.5 ${checking ? "animate-spin" : ""}`} aria-hidden="true" />Check now</Button></div></div>
    </div>
    <Modal open={createOpen} onOpenChange={setCreateOpen} title="Create this DNS record?" intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={host} mono /><ReviewRow label="Current" value="No address record" /><ReviewRow label="Requested" value={report?.record ? `${report.record.type} → ${report.record.content}${report.record.proxied ? " (proxied)" : ""}` : ""} /><ReviewRow label="Consequence" value="Public DNS for this hostname will point at this edge. A record that already exists is never replaced." /><ReviewRow label="Details" value="The record is created with the provider token from the configuration. Remove it at the provider to undo." /></dl><div className="mt-5 flex justify-end gap-2"><Button variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button><Button disabled={creating} onClick={() => { setCreateOpen(false); void create(); }}>{creating ? "Creating…" : "Create record"}</Button></div></Modal>
  </>;
  return inline ? content : <Modal open onOpenChange={(open) => { if (!open) onClose?.(); }} title={`DNS & HTTPS · ${host}`} intro="The one DNS record this address needs, and whether visitors reach this server." wide>{content}</Modal>;
}

function ReviewRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className={mono ? "break-all font-mono" : ""}>{value}</dd></div>; }
