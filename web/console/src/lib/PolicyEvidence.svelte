<script lang="ts">
  import type { PolicyContext, PolicySignal } from "$lib/api";

  let { context } = $props<{ context?: PolicyContext }>();

  const rows = $derived(context ? [
    { label: "Session method", signal: context.session.method },
    { label: "Authenticated at", signal: context.session.auth_time },
    { label: "Authenticator verified", signal: context.session.user_verified },
    { label: "Device bound", signal: context.session.device_bound },
    { label: "Arrival path", signal: context.network.arrival_path },
    { label: "Visitor IP source", signal: context.network.visitor_ip_source },
    { label: "Route", signal: context.resource.route },
    { label: "Resource owner", signal: context.resource.owner },
    { label: "Data class", signal: context.resource.data_class },
    { label: "Resource sensitivity", signal: context.resource.sensitivity },
    { label: "HTTP method", signal: context.action.http_method },
    { label: "Path class", signal: context.action.path_class },
    { label: "Risky action", signal: context.action.risky },
    { label: "Device first seen", signal: context.history.device_first_seen },
  ] : []);

  function value<T>(signal: PolicySignal<T>): string {
    if (signal.value === null || signal.value === undefined) return "Unknown";
    if (typeof signal.value === "boolean") return signal.value ? "Yes" : "No";
    return String(signal.value) || "Empty value";
  }

  function observedAt(timestamp: string): string {
    const parsed = new Date(timestamp);
    return Number.isNaN(parsed.getTime()) ? "time unavailable" : parsed.toLocaleString();
  }
</script>

{#if context}
  <details class="border-border mt-2 rounded-md border text-xs">
    <summary class="text-muted-foreground hover:text-foreground cursor-pointer px-2.5 py-2 font-medium focus-visible:outline-ring focus-visible:outline-2 focus-visible:outline-offset-2">
      Decision evidence <span class="ml-1 font-mono">v{context.version}</span>
    </summary>
    <dl class="divide-border grid grid-cols-1 divide-y px-2.5 pb-1 sm:grid-cols-2 sm:gap-x-4">
      {#each rows as row (row.label)}
        <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-2 py-2">
          <dt class="text-muted-foreground truncate">{row.label}</dt>
          <dd class="text-right font-medium">{value(row.signal)}</dd>
          <dd class="text-muted-foreground col-span-2 truncate">Source: <span class="font-mono">{row.signal.source || "unknown"}</span></dd>
          <dd class="text-muted-foreground col-span-2">Observed <time datetime={row.signal.observed_at}>{observedAt(row.signal.observed_at)}</time></dd>
        </div>
      {/each}
    </dl>
  </details>
{/if}
