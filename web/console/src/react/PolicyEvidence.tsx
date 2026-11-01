import type { PolicyContext, PolicySignal } from "$lib/api";

export function PolicyEvidence({ context }: { context?: PolicyContext }) {
  if (!context) return null;
  const rows: { label: string; signal: PolicySignal<string | boolean> }[] = [
    { label: "Session method", signal: context.session.method }, { label: "Authenticated at", signal: context.session.auth_time },
    { label: "Authenticator verified", signal: context.session.user_verified }, { label: "Device bound", signal: context.session.device_bound },
    { label: "Arrival path", signal: context.network.arrival_path }, { label: "Visitor IP source", signal: context.network.visitor_ip_source },
    { label: "Route", signal: context.resource.route }, { label: "Resource owner", signal: context.resource.owner },
    { label: "Data class", signal: context.resource.data_class }, { label: "Resource sensitivity", signal: context.resource.sensitivity },
    { label: "HTTP method", signal: context.action.http_method }, { label: "Path class", signal: context.action.path_class },
    { label: "Risky action", signal: context.action.risky }, { label: "Device first seen", signal: context.history.device_first_seen },
  ];
  const value = (signal: PolicySignal<string | boolean>) => signal.value == null ? "Unknown" : typeof signal.value === "boolean" ? signal.value ? "Yes" : "No" : signal.value || "Empty value";
  const observed = (iso: string) => { const date = new Date(iso); return Number.isNaN(date.getTime()) ? "time unavailable" : date.toLocaleString(); };
  return <details className="border-border mt-2 rounded-md border text-xs"><summary className="text-muted-foreground hover:text-foreground cursor-pointer px-2.5 py-2 font-medium focus-visible:outline-ring focus-visible:outline-2 focus-visible:outline-offset-2">Decision evidence <span className="ml-1 font-mono">v{context.version}</span></summary><dl className="divide-border grid grid-cols-1 divide-y px-2.5 pb-1 sm:grid-cols-2 sm:gap-x-4">{rows.map(({ label, signal }) => <div key={label} className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-2 py-2"><dt className="text-muted-foreground truncate">{label}</dt><dd className="text-right font-medium">{value(signal)}</dd><dd className="text-muted-foreground col-span-2 truncate">Source: <span className="font-mono">{signal.source || "unknown"}</span></dd><dd className="text-muted-foreground col-span-2">Observed <time dateTime={signal.observed_at}>{observed(signal.observed_at)}</time></dd></div>)}</dl></details>;
}
