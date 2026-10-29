<script lang="ts">
  import Summary from "$lib/Summary.svelte";
  import { onMount, untrack } from "svelte";
  import { admin, can, endSession } from "$lib/adminToken.svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import * as Table from "$lib/components/ui/table";
  import * as Select from "$lib/components/ui/select";
  import { Input } from "$lib/components/ui/input";
  import { Button } from "$lib/components/ui/button";
  import { fetchOverview, fetchPeople, type AccessEvent, type RouteOverview } from "$lib/api";
  import { navigate } from "$lib/router.svelte";
  import { LatestRequest } from "$lib/latestRequest";
  import Freshness from "$lib/Freshness.svelte";
  import PolicyEvidence from "$lib/PolicyEvidence.svelte";
  import { emptyFeed, receive } from "$lib/feed";
  import { openEventStream } from "$lib/eventStream";
  import { eventReason, shortId, subjectsOf, whoActed as actedBy } from "$lib/wording.ts";
  import PauseIcon from "@lucide/svelte/icons/pause";
  import PlayIcon from "@lucide/svelte/icons/play";
  import SearchIcon from "@lucide/svelte/icons/search";
  import Logo from "$lib/brand/Logo.svelte";

  const MAX = 500;
  const labels: Record<string, string> = {
    "github.workflow_proposed": "Publishing workflow proposed",
    "access.allowed": "Access allowed", "access.denied": "Access denied", "access.rate_limited": "Rate limited", "access.step_up": "Login requested again", "passkey.registered": "Passkey added", "passkey.revoked": "Passkey revoked", "user.invited": "Person invited", "user.disabled": "Person disabled", "user.enabled": "Person enabled",
    "login.started": "Login started", "login.failed": "Login failed", "login.succeeded": "Login succeeded",
    "route.created": "Route added", "route.create_failed": "Route creation failed", "route.deleted": "Route removed", "route.delete_failed": "Route removal failed", "config.reloaded": "Configuration reloaded", "config.reload_failed": "Configuration reload failed",
    "tunnel.connected": "Tunnel connected", "tunnel.disconnected": "Tunnel disconnected", "tunnel.rejected": "Tunnel rejected", "connector.enrolled": "Connector enrolled", "connector.rotated": "Connector certificate rotated", "connector.revoked": "Connector revoked", "notify.chat_paired": "Alert chat paired",
    "origin.denied": "Origin pull rejected",
    "operator.signed_in": "Signed in to the console", "operator.signed_out": "Signed out of the console", "operator.login_issued": "Host login issued", "operator.revoked": "Console session ended",
    "admin.invited": "Administrator invited", "admin.enrolment_issued": "Setup link issued", "admin.recovery_issued": "Recovery link issued", "admin.device_link_issued": "Link for another browser issued",
    "admin.passkey_registered": "Administrator passkey created", "admin.passkey_added": "Administrator passkey added", "admin.sign_in_failed": "Console sign-in failed", "session.revoked": "Session ended",
    "pages.deployed": "Site deployed", "dns.record_created": "DNS record created", "pages.rolled_back": "Site rolled back", "pages.refused": "Deployment refused", "pages.deploy_refused": "Project deployment refused", "pages.failed": "Deployment failed",
  };
  const categories = [
    { value: "all", label: "All events" }, { value: "access", label: "Access decisions" }, { value: "login", label: "Logins" },
    { value: "changes", label: "Configuration changes" }, { value: "tunnel", label: "Tunnels" }, { value: "pages", label: "Deployments" },
  ];

  let events = $state<AccessEvent[]>([]);
  let frozen = $state<AccessEvent[] | null>(null);
  let query = $state("");
  let category = $state("all");
  let stream = $state<"locked" | "connecting" | "live" | "reconnected" | "lost">("locked");
  // Identity and gap bookkeeping, so a reconnect never doubles the table and
  // missed events are said, not hidden (BUG-009).
  let feed = $state(emptyFeed());
  let routes = $state<RouteOverview[] | null>(null);
  let routesError = $state(false);
  let routesObservedAt = $state<number | null>(null);
  let routesRefreshing = $state(false);
  const routesRequest = new LatestRequest();

  const outcome = (e: AccessEvent) =>
    e.error || /[._](denied|failed|rejected|refused)$/.test(e.type) ? "deny" : /[._](allowed|succeeded|deployed|rolled_back)$/.test(e.type) ? "allow" : "info";
  const group = (e: AccessEvent) =>
    e.type.startsWith("access.") || e.type.startsWith("origin.") ? "access" : e.type.startsWith("login.") || e.type.startsWith("operator.") || e.type.startsWith("admin.sign") ? "login" : e.type.startsWith("tunnel.") || e.type.startsWith("connector.") ? "tunnel" : e.type.startsWith("pages.") ? "pages" : "changes";

  // A deployment's reason is its 64-character content hash: the first 12 tell builds apart.
  const reasonText = (e: AccessEvent) => (e.reason && /^[0-9a-f]{40,}$/.test(e.reason) ? shortId(e.reason) : eventReason(e.reason));
  // A console session's code is its internal grant id: noise next to who and when.
  const codeText = (e: AccessEvent) => (/^(operator|admin|session)\./.test(e.type) ? "" : e.code ?? "");
  // Passkey sessions leave an opaque id in events; the people list says whose it is.
  let subjects = $state(new Map<string, string>());
  const whoActed = (actor?: string) => actedBy(actor, subjects);
  const shown = $derived(frozen ?? events);
  const deploymentSource = (e: AccessEvent) => e.deployment ? [e.deployment.project, e.deployment.repository_id ? `repository ${e.deployment.repository_id}` : "", e.deployment.ref, e.deployment.commit_sha?.slice(0, 12)].filter(Boolean).join(" · ") : "";
  const filtered = $derived(
    shown.filter((e) => (category === "all" || group(e) === category) &&
      (!query || [e.type, e.actor, e.host, e.code, e.reason, e.error, e.remote, e.deployment?.project, e.deployment?.ref, e.deployment?.commit_sha].some((v) => (v ?? "").toLowerCase().includes(query.toLowerCase())))),
  );
  const denied = $derived(shown.filter((e) => e.type === "access.denied").length);
  const protectedCount = $derived(routes?.filter((r) => r.protected).length ?? 0);

  const time = (iso: string) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? { t: "—", d: "" } : { t: d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }), d: d.toLocaleDateString() };
  };

  async function loadRoutes() {
    const request = routesRequest.start();
    routesRefreshing = routes !== null;
    try {
      const next = await fetchOverview(request.signal);
      if (!request.isCurrent()) return;
      routes = next;
      routesError = false;
      routesObservedAt = Date.now();
    } catch {
      if (!request.isCurrent()) return;
      routesError = true;
    } finally {
      if (request.isCurrent()) routesRefreshing = false;
      request.finish();
    }
  }

  function inspect(host?: string) {
    if (host) navigate("/routes", { host });
  }

  const loadSubjects = () => {
    const token = admin.token.trim();
    if (!token || !can("people")) return;
    void fetchPeople().then((p) => { if (admin.token.trim() === token) subjects = subjectsOf(p.users); }).catch(() => {});
  };
  $effect(() => { const token = admin.token.trim(); subjects = new Map(); if (token) untrack(loadSubjects); });
  $effect(() => {
    const token = admin.token.trim();
    events = []; frozen = null; feed = emptyFeed();
    if (!token) { stream = "locked"; return; }
    return openEventStream({ token, onDenied: () => endSession(), onState: (state) => (stream = state), onEvent: (message) => {
      try {
        const e = JSON.parse(message.data) as AccessEvent;
        if (typeof e.type !== "string" || typeof e.time !== "string") return;
        const r = receive(feed, e.id ?? message.id);
        feed = r.state;
        if (r.accept) events = [e, ...events].slice(0, MAX);
      } catch { /* ignore a malformed event */ }
    } });
  });
  onMount(() => {
    loadRoutes();
    const refresh = setInterval(() => { loadRoutes(); loadSubjects(); }, 30_000);
    return () => { clearInterval(refresh); routesRequest.cancel(); };
  });

  const streamText = { locked: "Sign in to view activity", connecting: "Connecting…", live: "Live event stream", reconnected: "Live · reconnected", lost: "Connection lost · retrying" };
</script>

<PageHeader title="Activity" intro="Every decision and change, who made it and why.">
  <span class="border-border bg-card flex items-center gap-2 rounded-full border px-3.5 py-1.5 font-mono text-xs">
    <span class={["lamp", stream === "lost" ? "lamp-off" : stream === "connecting" || stream === "locked" ? "lamp-wait" : "lamp-on"]} aria-hidden="true"></span>
    {streamText[stream]}
  </span>
</PageHeader>

{#if feed.missed > 0 || feed.restarted}
  <div class="border-warning/40 bg-warning/[0.06] mb-6 rounded-lg border px-4 py-3 text-sm" role="status">
    {#if feed.missed > 0}<p><strong>{feed.missed} {feed.missed === 1 ? "event" : "events"} missed</strong> by this view (a slow connection, or history the server no longer holds): the table is incomplete. It is a live view, not an audit log.</p>{/if}
    {#if feed.restarted}<p>Ozal restarted while this view was open; earlier rows are from the previous run.</p>{/if}
  </div>
{/if}

<Summary label="Activity summary" items={[{ value: shown.length, label: "events" }, { value: denied, label: "refused", tone: denied ? "bad" : undefined }]} />

<div class="grid grid-cols-[minmax(0,1fr)] gap-6 min-[1500px]:grid-cols-[minmax(0,1fr)_320px]">
  <Panel label="Decision feed" title={`${filtered.length} shown · ${shown.length} retained`} sub={stream === "locked" ? "Sign in to load events" : frozen ? "Paused — new events are held back" : stream === "lost" ? "Stream unavailable — retrying" : "Newest first"} class="min-w-0">
    {#snippet actions()}
      <Button variant="outline" size="sm" onclick={() => (frozen = frozen ? null : [...events])} aria-pressed={frozen !== null}>
        {#if frozen}<PlayIcon class="size-3.5" /> Resume{:else}<PauseIcon class="size-3.5" /> Pause{/if}
      </Button>
    {/snippet}
    <div class="mb-4 flex flex-wrap gap-3">
      <div class="relative min-w-0 flex-[1_1_14rem]">
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
      <ul class="space-y-3 md:hidden">
        {#each filtered as e, i (e.time + e.type + i)}
          {@const o = outcome(e)}
          {@const t = time(e.time)}
          <li class="border-border rounded-lg border p-3">
            <div class="flex items-start justify-between gap-3">
              <div class="flex min-w-0 items-center gap-2 font-medium"><span class={["lamp shrink-0", o === "allow" ? "lamp-on" : o === "deny" ? "lamp-off" : "lamp-idle"]} aria-hidden="true"></span><span class="break-words">{labels[e.type] ?? e.type}</span></div>
              <time class="text-muted-foreground shrink-0 font-mono text-xs" datetime={e.time} title={e.time}>{t.t}</time>
            </div>
            <div class="mt-2 pl-5 text-sm">{whoActed(e.actor) || "No actor recorded"}</div>
            {#if e.host}<a href={`/routes?host=${encodeURIComponent(e.host)}`} onclick={(event) => { event.preventDefault(); inspect(e.host); }} class="text-muted-foreground mt-0.5 ml-5 block break-all font-mono text-xs underline underline-offset-4">{e.host}</a>{/if}
            {#if codeText(e) || e.reason || e.error}<p class="text-muted-foreground mt-2 text-xs"><span class="font-mono">{codeText(e)}</span>{codeText(e) && (e.reason || e.error) ? " · " : ""}{[reasonText(e), e.error].filter(Boolean).join(" · ")}</p>{/if}
            {#if e.remote}<p class="text-muted-foreground mt-1 text-xs">Peer <span class="font-mono">{e.remote}</span></p>{/if}
            {#if e.deployment}<p class="text-muted-foreground mt-2 break-all font-mono text-xs">{deploymentSource(e)}</p>{/if}
            <PolicyEvidence context={e.context} />
          </li>
        {/each}
      </ul>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard users need to scroll this table region) -->
      <div class="hidden overflow-x-auto md:block" role="region" tabindex="0" aria-label="Access activity events">
        <Table.Root>
          <Table.Header><Table.Row><Table.Head class="plate">Decision / event</Table.Head><Table.Head class="plate">Who / where</Table.Head><Table.Head class="plate text-right">Time</Table.Head></Table.Row></Table.Header>
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
                  {#if codeText(e) || e.reason || e.error}<div class="text-muted-foreground mt-1 pl-5 text-xs"><span class="font-mono">{codeText(e)}</span>{codeText(e) && (e.reason || e.error) ? " · " : ""}{[reasonText(e), e.error].filter(Boolean).join(" · ")}</div>{/if}
                  {#if e.deployment}<p class="text-muted-foreground mt-2 break-all pl-5 font-mono text-xs">{deploymentSource(e)}</p>{/if}
                  <PolicyEvidence context={e.context} />
                </Table.Cell>
                <Table.Cell class="align-top">
                  <div class="text-sm">{whoActed(e.actor) || "No actor recorded"}</div>
                  {#if e.host}
                    <a class="text-muted-foreground hover:text-foreground focus-visible:outline-ring font-mono text-xs underline underline-offset-4 focus-visible:outline-2" href={`/routes?host=${encodeURIComponent(e.host)}`} onclick={(event) => { event.preventDefault(); inspect(e.host); }}>{e.host}</a>
                  {:else}<div class="text-muted-foreground font-mono text-xs">No route recorded</div>{/if}
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
    <Panel label="Access by hostname" sub="Who each hostname lets in">
      <div class="mb-3"><Freshness observedAt={routesObservedAt} refreshing={routesRefreshing} error={routesError ? "Route configuration could not be refreshed" : ""} /></div>
      {#if !routes}
        {#if routesError}<p class="text-destructive text-sm">Routes could not be read. <button class="underline" onclick={loadRoutes}>Retry</button></p>
        {:else}<p class="text-muted-foreground text-sm">Loading routes…</p>{/if}
      {:else if routes.length === 0}
        <p class="text-muted-foreground text-sm">No routes are configured.</p>
      {:else}
        <ul class="divide-border -my-2 divide-y">
          {#each routes as r (r.host)}
            <li class="flex items-start gap-3 py-3">
              <span class={["lamp mt-1.5", r.protected ? "lamp-wait" : "lamp-on"]} aria-hidden="true"></span>
              <div class="min-w-0 flex-1">
                <a href={`/routes?host=${encodeURIComponent(r.host)}`} onclick={(event) => { event.preventDefault(); inspect(r.host); }} class="truncate font-mono text-[0.8rem] underline underline-offset-4" title={r.host}>{r.host}</a>
                <div class="text-muted-foreground mt-0.5 text-xs">{r.protected ? "Login · " : "Public · "}{r.policy}</div>
                {#if r.connector}<div class="text-muted-foreground font-mono text-xs">{r.connector} · {r.connection}</div>{/if}
              </div>
              <Button variant="ghost" size="sm" onclick={() => inspect(r.host)}>Inspect</Button>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>
    <div class="border-border rounded-xl border border-dashed p-5">
      <p class="plate mb-2">About this view</p>
      <div class="text-muted-foreground space-y-2 text-sm leading-relaxed">
        <p>The feed replays recent in-memory events. A restart clears history; slow clients may miss events.</p>
      </div>
    </div>
  </div>
</div>
