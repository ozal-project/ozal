import { useEffect, useMemo, useState } from "react";
import { Check, Cloud, Copy } from "lucide-react";
import { applyIngress, fetchIngress, type Ingress, type TunnelStepState } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { useSession } from "./session";

const lamps: Record<TunnelStepState, string> = { done: "lamp-on", todo: "lamp-wait", blocked: "lamp-off", unknown: "lamp-warn", waiting: "" };
const words: Record<TunnelStepState, string> = { done: "Done", todo: "To do", blocked: "Needs you", unknown: "Check again", waiting: "Waiting" };
const actionWords: Record<string, string> = { create_tunnel: "created the tunnel and saved its connector token on this host", set_ingress: "pointed the tunnel at this edge" };

/**
 * The Cloudflare Tunnel this edge is published through (ingress.mode
 * "cloudflare_tunnel"). Ozal creates the tunnel with the API token; the
 * operator only starts cloudflared on the host. No token is ever shown here.
 */
export function TunnelPanel({ ingress, editable, onChange }: { ingress: Ingress; editable: boolean; onChange(next: Ingress): void }) {
  const { session } = useSession();
  const [busy, setBusy] = useState<"" | "check" | "apply">("");
  const [result, setResult] = useState<{ text: string; ok: boolean } | null>(null);
  const steps = ingress.steps ?? [];
  const doable = editable && steps.some((step) => step.state === "todo" && step.action);
  const left = steps.filter((step) => step.state !== "done").length;
  const created = steps.some((step) => step.name === "tunnel" && step.state === "done");
  const connector = steps.find((step) => step.name === "connector" && step.state === "todo" && step.manual);

  useEffect(() => {
    // While cloudflared is being started, check every few seconds.
    if (!connector) return;
    const timer = window.setInterval(() => void check(true), 5_000);
    return () => window.clearInterval(timer);
  }, [connector?.manual]);

  async function check(quiet = false) {
    if (!quiet) setBusy("check");
    try { onChange(await fetchIngress()); if (!quiet) setResult(null); }
    catch (cause) { if (!quiet) setResult({ ok: false, text: (cause as Error).message }); }
    finally { if (!quiet) setBusy(""); }
  }
  async function apply() {
    if (!session?.token) return;
    setBusy("apply"); setResult(null);
    try {
      const next = await applyIngress(session.token);
      onChange(next);
      const done = (next.applied ?? []).map((action) => actionWords[action] ?? action);
      setResult({ ok: true, text: done.length ? `Done: ${done.join(", ")}.` : "Nothing was left to do." });
    } catch (cause) { setResult({ ok: false, text: (cause as Error).message }); await check(true); }
    finally { setBusy(""); }
  }

  return <section className="bg-card rounded-lg" aria-labelledby="tunnel-title">
    <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-5">
      <div className="flex min-w-0 items-start gap-3"><span className="bg-muted grid size-9 shrink-0 place-items-center rounded-lg"><Cloud className="text-muted-foreground size-4" aria-hidden="true" /></span><div className="min-w-0"><h2 id="tunnel-title" className="text-base font-medium">Cloudflare Tunnel</h2><p className="text-muted-foreground mt-0.5 max-w-2xl text-sm">This edge has no public address. Cloudflare serves HTTPS for your addresses and sends visitors through a tunnel that cloudflared opens from this host. No port is open to the internet.</p></div></div>
      <span className="flex items-center gap-2 text-sm"><span className={`lamp ${ingress.ready ? "lamp-on" : "lamp-wait"}`} aria-hidden="true" />{ingress.ready ? "Connected" : left === 1 ? "One step left" : `${left} steps left`}</span>
    </div>
    {created && <dl className="grid gap-x-8 gap-y-3 px-5 pt-4 text-sm sm:grid-cols-2"><div className="min-w-0"><dt className="text-muted-foreground text-xs">Tunnel</dt><dd className="mt-0.5 truncate">{ingress.name} <span className="text-muted-foreground font-mono text-xs">{ingress.id}</span></dd></div><div className="min-w-0"><dt className="text-muted-foreground text-xs">Records point at</dt><dd className="mt-0.5 flex items-center gap-1"><code className="min-w-0 truncate font-mono text-xs">{ingress.target}</code>{ingress.target && <CopyButton label="tunnel address" text={ingress.target} />}</dd></div></dl>}
    <ol className="grid gap-2 px-5 pt-4" aria-label="Tunnel steps">{steps.map((step) => <li key={step.name} className="bg-background rounded-lg border p-3"><div className="flex flex-wrap items-baseline justify-between gap-2"><p className="flex items-center gap-2 text-sm font-medium"><span className={`lamp ${lamps[step.state]}`} aria-hidden="true" />{step.title}</p><span className="text-muted-foreground text-xs">{words[step.state]}</span></div><p className="text-muted-foreground mt-1 text-sm">{step.detail}</p>{step.manual && step.state !== "done" && <div className="mt-2 grid gap-1"><div className="bg-muted/40 flex items-center gap-2 rounded-md border px-3 py-2"><code className="min-w-0 flex-1 break-all font-mono text-xs">{step.manual}</code><CopyButton label="command" text={step.manual} /></div><p className="text-muted-foreground text-xs">Install cloudflared from Cloudflare's packages first. The unit reads the token Ozal saved; it is never shown here.</p></div>}</li>)}</ol>
    {ingress.error && <p className="text-muted-foreground px-5 pt-2 text-xs">{ingress.error}</p>}
    {result && <p className={`px-5 pt-2 text-sm ${result.ok ? "text-success" : "text-destructive"}`} role="status">{result.text}</p>}
    {ingress.ready && <p className="text-muted-foreground px-5 pt-2 text-sm">Add an application and create its record from its DNS panel: a proxied CNAME to the tunnel address.</p>}
    <div className="mt-3 flex flex-wrap gap-2 border-t px-5 py-3"><Button variant="outline" size="sm" disabled={!!busy} onClick={() => void check()}>{busy === "check" ? "Checking…" : "Check again"}</Button>{doable && <Button size="sm" disabled={!!busy} onClick={() => void apply()}>{busy === "apply" ? "Setting up…" : created ? "Repair tunnel" : "Create tunnel"}</Button>}</div>
  </section>;
}

function CopyButton({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText(text); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <><button type="button" className="text-muted-foreground hover:text-foreground hover:bg-accent shrink-0 rounded p-1" aria-label={`Copy ${label}`} onClick={() => void copy()}>{copied ? <Check className="text-success size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}</button><span className="sr-only" role="status">{copied ? "Copied" : ""}</span></>;
}

/** Loads the ingress once; Domains shows the tunnel card only in that mode. */
export function useIngress() {
  const [ingress, setIngress] = useState<Ingress | null>(null);
  useEffect(() => { fetchIngress().then(setIngress).catch(() => setIngress(null)); }, []);
  return useMemo(() => ({ ingress, setIngress }), [ingress]);
}
