<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import * as Table from "$lib/components/ui/table";
  import * as Select from "$lib/components/ui/select";
  import { Input } from "$lib/components/ui/input";
  import { Button } from "$lib/components/ui/button";
  import { fetchOverview, type AccessEvent, type RouteOverview } from "$lib/api";
  import { emptyFeed, receive } from "$lib/feed";
  import PauseIcon from "@lucide/svelte/icons/pause";
  import PlayIcon from "@lucide/svelte/icons/play";
  import SearchIcon from "@lucide/svelte/icons/search";
  import Logo from "$lib/brand/Logo.svelte";

  const MAX = 500;
  const labels: Record<string, string> = {
    "access.allowed": "Access allowed", "access.denied": "Access denied", "access.rate_limited": "Rate limited", "access.step_up": "Login requested again", "passkey.registered": "Passkey added", "passkey.revoked": "Passkey revoked", "user.invited": "Person invited", "user.disabled": "Person disabled", "user.enabled": "Person enabled",
    "login.started": "Login started", "login.failed": "Login failed", "login.succeeded": "Login succeeded",
    "route.created": "Route added", "route.deleted": "Route removed", "config.reloaded": "Configuration reloaded",
    "tunnel.connected": "Tunnel connected", "tunnel.disconnected": "Tunnel disconnected", "tunnel.rejected": "Tunnel rejected",
    "origin.denied": "Origin pull rejected",
    "pages.deployed": "Site deployed", "pages.rolled_back": "Site rolled back", "pages.refused": "Deployment refused",
  };
  const categories = [
    { value: "all", label: "All events" }, { value: "access", label: "Access decisions" }, { value: "login", label: "Logins" },
    { value: "changes", label: "Configuration changes" }, { value: "tunnel", label: "Tunnels" }, { value: "pages", label: "Deployments" },
  ];

  let events = $state<AccessEvent[]>([]);
  let frozen = $state<AccessEvent[] | null>(null);
  let query = $state("");
  let category = $state("all");
  let stream = $state<"connecting" | "live" | "reconnected" | "lost">("connecting");
  // Identity and gap bookkeeping, so a reconnect never doubles the table and
  // missed events are said, not hidden (BUG-009).
  let feed = $state(emptyFeed());
  let routes = $state<RouteOverview[] | null>(null);
  let routesError = $state(false);

  const outcome = (e: AccessEvent) =>
    e.error || /\.(denied|failed|rejected|refused)$/.test(e.type) ? "deny" : /\.(allowed|succeeded|deployed|rolled_back)$/.test(e.type) ? "allow" : "info";
  const group = (e: AccessEvent) =>
    e.type.startsWith("access.") || e.type.startsWith("origin.") ? "access" : e.type.startsWith("login.") ? "login" : e.type.startsWith("tunnel.") ? "tunnel" : e.type.startsWith("pages.") ? "pages" : "changes";

  const shown = $derived(frozen ?? events);
  const filtered = $derived(
    shown.filter((e) => (category === "all" || group(e) === category) &&
      (!query || [e.type, e.actor, e.host, e.code, e.reason, e.error, e.remote].some((v) => (v ?? "").toLowerCase().includes(query.toLowerCase())))),
  );
  const denied = $derived(shown.filter((e) => e.type === "access.denied").length);
  const protectedCount = $derived(routes?.filter((r) => r.protected).length ?? 0);

  const time = (iso: string) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? { t: "—", d: "" } : { t: d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }), d: d.toLocaleDateString() };
  };

  async function loadRoutes() {
    try { routes = await fetchOverview(); routesError = false; } catch { routesError = true; }
  }

  onMount(() => {
    loadRoutes();
    const refresh = setInterval(loadRoutes, 30_000);
    let reconnecting = false;
    const es = new EventSource("/_/events");
    es.onopen = () => { stream = reconnecting ? "reconnected" : "live"; reconnecting = true; };
    es.onerror = () => { stream = "lost"; };
    es.onmessage = (m) => {
      try {
        const e = JSON.parse(m.data) as AccessEvent;
        if (typeof e.type !== "string" || typeof e.time !== "string") return;
        const r = receive(feed, e.id ?? m.lastEventId);
        feed = r.state;
        if (!r.accept) return;
        events = [e, ...events].slice(0, MAX);
      } catch { /* ignore a malformed event */ }
    };
    return () => { es.close(); clearInterval(refresh); };
  });

  const streamText = { connecting: "Connecting…", live: "Live event stream", reconnected: "Live · reconnected", lost: "Connection lost · retrying" };
</script>

<PageHeader eyebrow="Access control" title="Access activity" intro="Every line Switchboard connected or refused, who asked, and why.">
  <span class="border-border bg-card flex items-center gap-2 rounded-full border px-3.5 py-1.5 font-mono text-xs">
    <span class={["lamp", stream === "lost" ? "lamp-off" : stream === "connecting" ? "lamp-wait" : "lamp-on"]} aria-hidden="true"></span>
    {streamText[stream]}
  </span>
</PageHeader>

{#if feed.missed > 0 || feed.restarted}
  <div class="border-warning/40 bg-warning/[0.06] mb-6 rounded-lg border px-4 py-3 text-sm" role="status">
    {#if feed.missed > 0}<p><strong>{feed.missed} {feed.missed === 1 ? "event" : "events"} missed</strong> by this view (a slow connection, or history the server no longer holds): the table is incomplete. It is a live view, not an audit log.</p>{/if}
    {#if feed.restarted}<p>Switchboard restarted while this view was open; earlier rows are from the previous run.</p>{/if}
  </div>
{/if}

<div class="mb-6 grid gap-4 sm:grid-cols-3">
  <Stat label="Events retained" value={shown.length} note={`Most recent ${MAX} in this view`} lamp={shown.length ? "on" : "idle"} />
  <Stat label="Refused" value={denied} note="Access denied within the retained events" lamp={denied > 0 ? "off" : "idle"} />
  <Stat label="Lines requiring login" value={routes ? protectedCount : "—"} lamp={routes ? "wait" : "idle"}
    note={routes ? `${protectedCount} of ${routes.length} configured routes` : routesError ? "Route configuration unavailable" : "Reading routing configuration…"} />
</div>

<div class="grid gap-6 lg:grid-cols-[1fr_320px]">
  <Panel index="01" label="Decision feed" title={`${filtered.length} shown · ${shown.length} retained`} sub={frozen ? "Paused — new events are held back" : "Newest first, live"} class="min-w-0">
    {#snippet actions()}
      <Button variant="outline" size="sm" onclick={() => (frozen = frozen ? null : [...events])} aria-pressed={frozen !== null}>
        {#if frozen}<PlayIcon class="size-3.5" /> Resume{:else}<PauseIcon class="size-3.5" /> Pause{/if}
      </Button>
    {/snippet}
    <div class="mb-4 flex flex-wrap gap-3">
      <div class="relative min-w-56 flex-1">
        <SearchIcon class="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
        <Input bind:value={query} placeholder="Search actor, host or detail" class="pl-8" aria-label="Search events" />
      </div>
      <Select.Root type="single" bind:value={category}>
        <Select.Trigger class="w-52" aria-label="Event category">{categories.find((c) => c.value === category)?.label}</Select.Trigger>
        <Select.Content>{#each categories as c (c.value)}<Select.Item value={c.value} label={c.label} />{/each}</Select.Content>
      </Select.Root>
    </div>

    {#if filtered.length === 0}
      <div class="border-border flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-16 text-center">
        <Logo size={44} class="opacity-80" />
        <p class="font-medium">{shown.length ? "No matching events" : "The board is quiet"}</p>
        <p class="text-muted-foreground max-w-md text-sm">
          {shown.length ? "Try another category or search term." : "Lamps light here when a protected route is used. Public-route requests do not produce access events."}
        </p>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <Table.Root>
          <Table.Header><Table.Row><Table.Head class="plate">Decision / event</Table.Head><Table.Head class="plate">Caller / line</Table.Head><Table.Head class="plate text-right">Time</Table.Head></Table.Row></Table.Header>
          <Table.Body>
            {#each filtered as e, i (e.time + e.type + i)}
              {@const o = outcome(e)}
              {@const t = time(e.time)}
              <Table.Row>
                <Table.Cell class="align-top">
                  <div class="flex items-center gap-2.5 font-medium">
                    <span class={["lamp", o === "allow" ? "lamp-on" : o === "deny" ? "lamp-off" : "lamp-idle"]} aria-hidden="true"></span>
                    {labels[e.type] ?? e.type}
                  </div>
                  {#if e.code || e.reason || e.error}<div class="text-muted-foreground mt-1 pl-5 text-xs"><span class="font-mono">{e.code}</span>{e.code && (e.reason || e.error) ? " · " : ""}{[e.reason, e.error].filter(Boolean).join(" · ")}</div>{/if}
                </Table.Cell>
                <Table.Cell class="align-top">
                  <div class="text-sm">{e.actor ?? "No actor recorded"}</div>
                  <div class="text-muted-foreground font-mono text-xs">{e.host ?? "No route recorded"}</div>
                  {#if e.remote}<div class="text-muted-foreground font-mono text-xs">{e.remote}</div>{/if}
                </Table.Cell>
                <Table.Cell class="text-right align-top font-mono text-xs tabular-nums"><time datetime={e.time} title={e.time}>{t.t}</time><div class="text-muted-foreground">{t.d}</div></Table.Cell>
              </Table.Row>
            {/each}
          </Table.Body>
        </Table.Root>
      </div>
    {/if}
  </Panel>

  <div class="space-y-6">
    <Panel index="02" label="Line access" sub="Current login requirements">
      {#if routesError}
        <p class="text-destructive text-sm">Routes could not be read. <button class="underline" onclick={loadRoutes}>Retry</button></p>
      {:else if !routes}
        <p class="text-muted-foreground text-sm">Loading routes…</p>
      {:else}
        <ul class="divide-border -my-2 divide-y">
          {#each routes as r (r.host)}
            <li class="flex items-start gap-3 py-3">
              <span class={["lamp mt-1.5", r.protected ? "lamp-wait" : "lamp-on"]} aria-hidden="true"></span>
              <div class="min-w-0">
                <div class="truncate font-mono text-[0.8rem]" title={r.host}>{r.host}</div>
                <div class="text-muted-foreground mt-0.5 text-xs">{r.protected ? "Login · " : "Public · "}{r.policy}</div>
                {#if r.connector}<div class="text-muted-foreground font-mono text-xs">{r.connector} · {r.connection}</div>{/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>
    <div class="border-border rounded-xl border border-dashed p-5">
      <p class="plate mb-2">About this view</p>
      <div class="text-muted-foreground space-y-2 text-sm leading-relaxed">
        <p>The feed replays recent in-memory events. A restart clears history; slow clients may miss events.</p>
        <p>Host processes, backups and service controls belong in Server Ops.</p>
      </div>
    </div>
  </div>
</div>
