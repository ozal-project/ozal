import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CheckCircle2, Circle, Copy, RefreshCw } from "lucide-react";
import { fetchConnectors, type Connector } from "$lib/api";
import { connectorCommandName, connectorDaysLeft, connectorProgress, connectorState, validConnectorName } from "$lib/connectorView";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { useSession } from "./session";

const stateLabel = { revoked: "Revoked", connected: "Connected", waiting: "Not connected", idle: "No app assigned" } as const;
const stateLamp = { revoked: "lamp-off", connected: "lamp-on", waiting: "lamp-wait", idle: "lamp-idle" } as const;
const steps = [
  { title: "Create the identity on the private host", detail: "The private key is generated there and never leaves it.", location: "on the private host" },
  { title: "Sign the request with your CA", detail: "Move only connector.csr to the machine that holds your CA, then bring the certificate back.", location: "on the CA machine" },
  { title: "Check the certificate on the private host", detail: "Confirms that the certificate matches the key before anything is enrolled.", location: "on the private host" },
  { title: "Enrol it on the edge", detail: "Review the fingerprint first. Enrolment stays with whoever controls the edge host.", location: "on the edge host" },
  { title: "Connect and add the application", detail: "Replace the capitalised values with your app host and the edge address, then start the tunnel from the private host. Add the app in Applications and test an allowed and a refused visitor.", location: "on the private host" },
];

export function Connectors() {
  const { session } = useSession();
  const token = session?.token ?? "";
  const [records, setRecords] = useState<Connector[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [readAt, setReadAt] = useState("");
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const read = useRef<AbortController | null>(null);
  const requestID = useRef(0);

  const nameOk = validConnectorName(name);
  const active = useMemo(() => (records ?? []).filter((connector) => !connector.revoked), [records]);
  const connected = active.filter((connector) => connector.connected).length;
  const expiring = active.filter((connector) => { const days = connectorDaysLeft(connector); return days !== null && days < 10; }).length;
  const mine = nameOk ? records?.find((connector) => connector.name === name && !connector.revoked) : undefined;

  const load = useCallback(async (quiet = false) => {
    if (!token.trim()) return;
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    const id = ++requestID.current;
    if (!quiet) setLoading(true);
    setError("");
    try {
      const next = await fetchConnectors(token.trim(), controller.signal);
      if (controller.signal.aborted || requestID.current !== id) return;
      setRecords(next);
      setReadAt(new Date().toLocaleTimeString());
    } catch (cause) {
      if (!controller.signal.aborted && requestID.current === id) setError((cause as Error).message);
    } finally {
      if (requestID.current === id) { setLoading(false); read.current = null; }
    }
  }, [token]);
  useEffect(() => {
    setRecords(null); setError(""); setReadAt("");
    if (token) void load();
    return () => { requestID.current++; read.current?.abort(); read.current = null; };
  }, [token, load]);
  useEffect(() => {
    if (!adding || !nameOk || !token) return;
    const poll = window.setInterval(() => void load(true), 5_000);
    return () => window.clearInterval(poll);
  }, [adding, nameOk, token, load]);

  const stepCommand = (step: number) => {
    const selectedName = connectorCommandName(name);
    switch (step) {
      case 0: return `ozal connector prepare -dir ./connector-identity -name ${selectedName}`;
      case 1: return "ozal connector sign -csr connector.csr -ca-dir ./ca -out connector.crt";
      case 2: return "ozal connector check -dir ./connector-identity -ca ./ca.crt";
      case 3: return `sudo -u ozal ozal connector enroll -config /etc/ozal/config.json -name ${selectedName} -cert connector.crt`;
      default: return "ozal tunnel connect -cert ./connector-identity/connector.crt -key ./connector-identity/connector.key -ca ./ca.crt -host APP.example.com -edge EDGE_PRIVATE_IP:9443 -server-name EDGE_CERT_NAME -upstream http://127.0.0.1:3000";
    }
  };
  const progress = connectorProgress(mine);
  const expiryDate = (connector: Connector) => connector.not_after ? new Date(connector.not_after).toLocaleDateString() : "";

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Network</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Connectors</h1><p className="text-muted-foreground mt-1 text-sm">Private hosts that publish applications without opening a port.</p></div><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={loading} onClick={() => void load()}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />{loading ? "Reading…" : records ? "Refresh" : "Read connector registry"}</Button><Button onClick={() => { setAdding((value) => !value); if (!adding && !records) void load(); }}>{adding ? "Close guide" : "Connect a private host"}</Button></div></header>
    {error && <p role={records ? "status" : "alert"} className={`${records ? "border-warning/40 bg-warning/[0.06] rounded-md border p-3" : "text-destructive"} -mt-3 text-sm`}>{records ? "Showing the last successful registry read. Refresh failed: " : ""}{error}</p>}
    <dl className="mb-2 flex flex-wrap gap-x-8 gap-y-3" aria-label="Connectors summary">{[[records ? active.length : "—", "enrolled", false], [records ? connected : "—", "connected", false], [records ? expiring : "—", "certificates expiring soon", expiring > 0]] .map(([value, label, warning]) => <div key={String(label)} className="flex items-baseline gap-2"><dt className="sr-only">{String(label)}</dt><dd className={`text-xl font-medium tabular-nums ${warning ? "text-warning" : ""}`}>{value}</dd><span className="text-muted-foreground text-sm" aria-hidden="true">{label}</span></div>)}</dl>

    {adding && <section className="panel mb-2" aria-label="New private host"><div className="panel-head"><div><h2 className="text-sm font-medium">Connect it in five steps</h2><p className="text-muted-foreground mt-0.5 text-xs">Each step shows the exact command. This page watches the registry and ticks a step when it sees the result.</p></div></div><div className="grid gap-5 p-5"><div className="grid max-w-sm gap-2"><label htmlFor="connector-name" className="text-sm">Name for this host</label><Input id="connector-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="homelab" autoComplete="off" spellCheck={false} aria-invalid={name !== "" && !nameOk} />{name && !nameOk && <p className="text-destructive text-xs" role="alert">Use lowercase letters, digits and single dashes.</p>}</div><ol className="grid gap-5">{steps.map((step, index) => { const done = index + 1 >= 4 && index + 1 <= progress; const number = index + 1; return <li key={step.title} className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3"><span className="text-muted-foreground mt-0.5" aria-hidden="true">{done ? <CheckCircle2 className="text-success size-5" /> : <Circle className="size-5" />}</span><div className="min-w-0"><h3 className="text-sm font-medium">{number}. {step.title} <span className="text-muted-foreground font-normal">· {step.location}</span>{done && <span className="sr-only"> (done)</span>}</h3><p className="text-muted-foreground mt-1 mb-2 text-sm">{step.detail}</p><CopyCommand label={`Copy step ${number} command`} command={stepCommand(index)} />{number === 4 && nameOk && <p className="mt-2 text-xs" role="status">{mine ? `Enrolled: certificate ${mine.sha256.slice(0, 12)}…${mine.not_after ? `, valid until ${expiryDate(mine)}` : ""}.` : "Waiting for the edge to list this name…"}</p>}{number === 5 && nameOk && <p className="mt-2 text-xs" role="status">{mine ? mine.connected ? "Connected. The tunnel is up." : "Enrolled but not connected yet." : "Not enrolled yet."}</p>}</div></li>; })}</ol><p className="text-muted-foreground border-t pt-4 text-xs">The identity is valid for 30 days and renews with the existing renewal agent, which is not set up from this screen. A connector installs no runtime or database.</p></div></section>}

    {readAt && <p className="text-muted-foreground -mt-4 text-xs">Registry read at {readAt}.</p>}
    {records === null && loading ? <div className="text-muted-foreground border-y py-5 text-sm" aria-busy="true">Reading connector registry…</div> : records && records.length ? <div className="grid gap-4 lg:grid-cols-2">{records.map((connector) => { const state = connectorState(connector); const days = connectorDaysLeft(connector); return <article key={`${connector.name}-${connector.sha256}`} className="panel p-5"><div className="flex items-start justify-between gap-3"><h2 className="flex items-center gap-2 text-base font-semibold"><span className={`lamp ${stateLamp[state]}`} aria-hidden="true" />{connector.name}</h2><span className="text-muted-foreground font-mono text-xs">{stateLabel[state]}</span></div><dl className="mt-4 grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 text-sm"><dt className="text-muted-foreground">Serves</dt><dd className="break-all">{connector.hosts.length ? connector.hosts.join(", ") : "No application yet"}</dd><dt className="text-muted-foreground">Certificate</dt><dd>{days === null ? "Expiry unknown" : days < 0 ? <span className="text-destructive">Expired</span> : <span className={days < 10 ? "text-warning" : ""}>{days} days left</span>}<span className="text-muted-foreground block font-mono text-xs break-all">{connector.sha256.slice(0, 24)}…</span></dd></dl>{!connector.revoked && <details className="mt-4 text-sm"><summary className="text-muted-foreground hover:text-foreground cursor-pointer">Revoke this connector</summary><p className="mt-3">Stops access to the hosts above within about 5 seconds. Application data is not deleted. Run it on the edge host:</p><div className="mt-2"><CopyCommand label={`Copy revoke command for ${connector.name}`} command={`sudo -u ozal ozal connector revoke -config /etc/ozal/config.json -name ${connector.name}`} /></div><p className="text-muted-foreground mt-2 text-xs">Then stop the connector service on its own host. Keep the shared CA; revoke only this identity.</p></details>}</article>; })}</div> : records ? <div className="panel px-5 py-10 text-center"><p className="font-medium">No private hosts connected</p><p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Direct upstreams and static sites do not need a connector. Connect a host when an app lives behind a home or office network.</p><Button className="mt-4" onClick={() => setAdding(true)}>Connect a private host</Button></div> : records === null && error ? <Button size="sm" variant="outline" onClick={() => void load()}>Retry registry read</Button> : null}
  </div>;
}

function CopyCommand({ command, label }: { command: string; label: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText(command); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <div className="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 break-all font-mono text-[0.78rem] leading-relaxed whitespace-pre-wrap">{command}</code><button type="button" onClick={() => void copy()} aria-label={label} className="text-muted-foreground hover:text-foreground hover:bg-accent shrink-0 rounded-md p-1.5 focus-visible:outline-2">{copied ? <CheckCircle2 className="text-success size-4" aria-hidden="true" /> : <Copy className="size-4" aria-hidden="true" />}</button><span className="sr-only" role="status">{copied ? "Copied" : ""}</span></div>;
}
