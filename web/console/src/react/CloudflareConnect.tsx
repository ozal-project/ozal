import { useEffect, useState } from "react";
import { Check, Copy, ExternalLink, ShieldCheck } from "lucide-react";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { fetchCloudflare, forgetCloudflareToken, saveCloudflareToken, type CloudflareStatus } from "$lib/api";

// Domains, before the edge owns its certificates: connect Cloudflare. The
// console checks and keeps the token; one host command, shown here with the
// plan it applies, moves certificates to DNS-01 and restarts. After that the
// page lists the domains and sets each one up.
export function CloudflareConnect({ tlsMode, editable }: { tlsMode: string; editable: boolean }) {
  const [status, setStatus] = useState<CloudflareStatus | null>(null);
  const [error, setError] = useState("");
  const [token, setToken] = useState("");
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [replacing, setReplacing] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetchCloudflare(controller.signal).then((next) => { setStatus(next); setTarget(next.target || next.suggested_target || ""); }, (cause) => { if (!controller.signal.aborted) setError((cause as Error).message); });
    return () => controller.abort();
  }, []);

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true); setError("");
    try {
      const next = await saveCloudflareToken(token.trim(), target.trim());
      setStatus(next); setToken(""); setReplacing(false);
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }
  async function forget() {
    setBusy(true); setError("");
    try { await forgetCloudflareToken(); setStatus(await fetchCloudflare()); setReplacing(false); }
    catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }

  const mode = tlsMode === "automatic"
    ? "Today this edge gets a certificate for each address the first time someone visits it (HTTP-01)."
    : "Today this edge serves plain HTTP; HTTPS is provided in front of it, or not enabled yet.";

  return <section className="bg-card rounded-lg p-6" aria-labelledby="cloudflare-title">
    <div className="flex items-start gap-4">
      <span className="bg-muted grid size-10 shrink-0 place-items-center rounded-lg"><ShieldCheck className="text-muted-foreground size-5" aria-hidden="true" /></span>
      <div className="min-w-0 max-w-2xl">
        <h2 id="cloudflare-title" className="font-medium">Manage domains with Cloudflare</h2>
        <p className="text-muted-foreground mt-1 text-sm">{mode} With Cloudflare, Ozal gets one certificate per domain, covering every address under it, creates the DNS record each application needs and sets the domain up for its proxy.</p>
        {error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}
        {!status ? !error && <p className="text-muted-foreground mt-4 text-sm" role="status">Reading…</p>
          : !status.available ? <p className="text-muted-foreground mt-4 text-sm">This edge runs without a state database, so it has nowhere to keep a token. Start it with <code className="font-mono text-xs">-db</code> to connect Cloudflare here.</p>
          : status.active ? <p className="mt-4 text-sm" role="status">Cloudflare is configured. Restart Ozal if this page still shows this message: the domains appear once the new configuration is loaded.</p>
          : status.saved && !replacing ? <Saved status={status} copied={copied} onCopy={() => { if (status.command) void navigator.clipboard.writeText(status.command).then(() => { setCopied(true); setTimeout(() => setCopied(false), 2000); }); }} editable={editable} busy={busy} onReplace={() => setReplacing(true)} onForget={() => void forget()} />
          : editable ? <form className="mt-5 grid gap-4" onSubmit={(event) => void save(event)}>
            <ol className="text-muted-foreground grid gap-1.5 text-sm">
              <li>1. In Cloudflare, <a className="link inline-flex items-center gap-1" href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noreferrer noopener">create an API token<ExternalLink className="size-3" aria-hidden="true" /></a> for the zones Ozal serves, with:
                <ul className="mt-1 ml-4 grid gap-0.5 font-mono text-xs">{status.permissions.map((p) => <li key={p}>{p}</li>)}</ul></li>
              <li>2. Paste it here. Ozal checks it with Cloudflare and keeps it in a file only it can read; it is never shown again.</li>
            </ol>
            <label className="grid gap-1.5 text-sm"><span className="font-medium">API token</span><Input type="password" autoComplete="off" spellCheck={false} value={token} onChange={(event) => setToken(event.target.value)} required /></label>
            <label className="grid gap-1.5 text-sm"><span className="font-medium">This edge's public address</span><Input value={target} onChange={(event) => setTarget(event.target.value)} placeholder="203.0.113.10 or edge.example.com" required /><span className="text-muted-foreground text-xs">Where visitors reach this server: the DNS records Ozal creates point here.{status.suggested_target ? ` Suggested from your sign-in address: ${status.suggested_target}.` : ""}</span></label>
            <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy || !token.trim() || !target.trim()}>{busy ? "Checking with Cloudflare…" : "Check and save"}</Button>{replacing && <Button variant="ghost" type="button" onClick={() => setReplacing(false)}>Cancel</Button>}</div>
          </form>
          : <p className="text-muted-foreground mt-4 text-sm">Ask an administrator with the Applications tool to connect Cloudflare.</p>}
      </div>
    </div>
  </section>;
}

function Saved({ status, copied, onCopy, editable, busy, onReplace, onForget }: { status: CloudflareStatus; copied: boolean; onCopy(): void; editable: boolean; busy: boolean; onReplace(): void; onForget(): void }) {
  const byZone = (zone: string) => Object.entries(status.plan?.covers ?? {}).filter(([, z]) => z === zone).map(([host]) => host).sort();
  return <div className="mt-5 grid gap-5">
    <p className="flex items-center gap-2 text-sm"><span className="lamp lamp-on" aria-hidden="true" />Token checked: it sees {status.zones?.join(", ") || "no zone"}. Records will point at {status.target}.</p>
    {status.plan_error ? <p className="border-warning/40 bg-warning/[0.06] rounded-md border p-3 text-sm" role="alert">Not ready yet: {status.plan_error}</p> : status.plan && <div>
      <h3 className="text-sm font-medium">What the change does</h3>
      <ul className="mt-2 grid gap-2">{status.plan.zones.map((zone) => <li key={zone} className="border-border rounded-md border px-3 py-2 text-sm"><p className="font-mono text-xs">{zone} · *.{zone}</p><p className="text-muted-foreground mt-0.5 text-xs">Covers {byZone(zone).join(", ")}</p></li>)}</ul>
      {status.plan.local.length > 0 && <p className="text-muted-foreground mt-2 text-xs">No public certificate needed for {status.plan.local.join(", ")}.</p>}
    </div>}
    {status.command && <div>
      <h3 className="text-sm font-medium">Run this once on the server</h3>
      <p className="text-muted-foreground mt-1 text-sm">Changing how the edge gets its certificates stays with the server, like creating the first administrator. The command checks the token again, writes this change to the host configuration, keeps the previous version beside it, and restarts Ozal.</p>
      <div className="bg-background border-border mt-2 flex items-start gap-2 rounded-md border p-3"><code className="min-w-0 flex-1 font-mono text-xs break-all">{status.command}</code><Button variant="ghost" size="icon-sm" aria-label="Copy the command" title="Copy" onClick={onCopy}>{copied ? <Check className="size-4" aria-hidden="true" /> : <Copy className="size-4" aria-hidden="true" />}</Button></div>
      <p className="text-muted-foreground mt-2 text-xs">Afterwards this page lists the domains, and each one is set up from here.</p>
    </div>}
    {editable && <div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" disabled={busy} onClick={onReplace}>Use another token</Button><Button variant="ghost" size="sm" className="text-destructive" disabled={busy} onClick={onForget}>Forget the token</Button></div>}
  </div>;
}
