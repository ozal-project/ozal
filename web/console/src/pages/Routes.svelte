<script lang="ts">
  import { friendlyHealth } from "$lib/wording.ts";
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import * as Table from "$lib/components/ui/table";
  import { Button } from "$lib/components/ui/button";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { fetchStatus, type StatusResponse } from "$lib/api";
  import { route, navigate } from "$lib/router.svelte";
  import { LatestRequest } from "$lib/latestRequest";
  import Freshness from "$lib/Freshness.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import LockIcon from "@lucide/svelte/icons/lock";
  import CertIcon from "@lucide/svelte/icons/badge-check";
  import ArrowRightIcon from "@lucide/svelte/icons/arrow-right";

  let data = $state<StatusResponse | null>(null);
  let error = $state("");
  let loading = $state(false);
  let observedAt = $state<number | null>(null);
  let selectedHost = $state("");
  const request = new LatestRequest();

  async function load() {
    const run = request.start();
    loading = true;
    try {
      const next = await fetchStatus(run.signal);
      if (!run.isCurrent()) return;
      data = next;
      error = "";
      observedAt = Date.now();
      if (selectedHost && !next.routes.some((r) => r.host === selectedHost)) selectedHost = "";
    } catch (e) {
      if (!run.isCurrent()) return;
      error = run.timedOut() ? "Request timed out" : (e as Error).message;
    } finally {
      if (run.isCurrent()) loading = false;
      run.finish();
    }
  }

  onMount(() => {
    selectedHost = new URLSearchParams(route.search).get("host") ?? "";
    load();
    const timer = setInterval(load, 15_000);
    return () => { clearInterval(timer); request.cancel(); };
  });

  const counts = $derived({
    total: data?.routes.length ?? 0,
    protected: data?.routes.filter((r) => r.access === "Protected").length ?? 0,
    unhealthy: data?.routes.filter((r) => r.state_class === "unhealthy").length ?? 0,
  });
  const selected = $derived(data?.routes.find((r) => r.host === selectedHost));
  const lampFor = { healthy: "lamp-on", unhealthy: "lamp-off", unknown: "lamp-idle" };

  $effect(() => {
    const requested = new URLSearchParams(route.search).get("host") ?? "";
    if (!data) return;
    selectedHost = data.routes.some((r) => r.host === requested) ? requested : "";
  });

  function inspect(host: string) {
    selectedHost = host;
    navigate("/routes", { host });
  }
</script>

<PageHeader eyebrow="Routing" title="Routes & hostnames" intro="Every hostname Switchboard answers, who may open it, and whether the app behind it responds. These are routing keys, not published DNS.">
  <div class="flex flex-col items-start gap-2 sm:items-end">
    <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
    <Freshness observedAt={observedAt} refreshing={loading && data !== null} error={error} />
  </div>
</PageHeader>

<div class="mb-5 flex flex-wrap gap-x-4 gap-y-1 rounded-lg border px-3 py-2 text-sm sm:hidden" aria-label="Route summary">
  <span>{counts.total} routes</span><span>{counts.protected} require login</span><span>{counts.unhealthy} unhealthy</span>
</div>
<div class="mb-6 hidden gap-4 sm:grid sm:grid-cols-3">
  <Stat label="Hostnames" value={data ? counts.total : "—"} note="Routes in the active snapshot" lamp={data ? "on" : "idle"} />
  <Stat label="Require login" value={data ? counts.protected : "—"} note="Protected by an access policy" lamp={data && counts.protected ? "wait" : "idle"} />
  <Stat label="Unhealthy upstreams" value={data ? counts.unhealthy : "—"} note="The app behind it fails its health check" lamp={counts.unhealthy > 0 ? "off" : data ? "on" : "idle"} />
</div>

{#if data?.audit && !data.audit.available}
  <div class="border-destructive/40 bg-destructive/[0.06] mb-6 rounded-lg border p-4" role="alert">
    <p class="font-medium">Durable access audit unavailable</p>
    <p class="text-muted-foreground mt-1 text-sm">Some access events may be missing. Required routes fail closed; best-effort routes continue serving.</p>
    <p class="text-muted-foreground mt-2 font-mono text-xs">{data.audit.failure_count} failures since process start{data.audit.last_failure_at ? ` · last ${new Date(data.audit.last_failure_at).toLocaleString()}` : ""}</p>
  </div>
{/if}

{#if data?.certificate}
  {@const c = data.certificate}
  <Panel label="Origin certificate" title={c.host} sub={`${c.mode} · ${c.valid ? `valid until ${c.not_after}` : "not valid"}${c.pending ? ` · ${c.pending} pending` : ""}`} class="mb-6">
    {#snippet actions()}<span class="flex items-center gap-2 font-mono text-xs"><CertIcon class="size-4" /><span class={["lamp", c.valid ? "lamp-on" : "lamp-off"]}></span>{c.valid ? "valid" : "invalid"}</span>{/snippet}
    {#if c.last_error}<p class="text-destructive text-sm">{c.last_error}</p>{:else}<p class="text-muted-foreground text-sm">Last outcome: {c.last_outcome || "—"} · next attempt {c.next_attempt || "—"}</p>{/if}
  </Panel>
{/if}

<Panel label="Routes" title="Hostnames" sub="Updated every 15 seconds" bodyClass="p-0">
  {#if !data}
    {#if error}<p class="text-destructive p-5 text-sm">Status could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
    {:else}<div class="space-y-3 p-5" aria-busy="true" aria-label="Loading routes">{#each [1, 2, 3] as i (i)}<Skeleton class="h-12 w-full" />{/each}</div>{/if}
  {:else if data.routes.length === 0}
    <p class="text-muted-foreground p-5 text-sm">No routes configured.</p>
  {:else}
    {#if error}<p class="border-warning/40 bg-warning/[0.06] m-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read; refresh failed: {error}</p>{/if}
    <ul class="space-y-3 p-3 md:hidden" aria-label="Configured routes">
      {#each data.routes as r (r.host)}
        <li class="border-border rounded-lg border p-3">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0"><p class="break-all font-mono text-sm">{r.host}</p><p class="text-muted-foreground text-xs">{r.kind}</p></div>
            <Button variant="outline" size="sm" onclick={() => inspect(r.host)} aria-label={`Inspect route ${r.host}`}>Inspect</Button>
          </div>
          <dl class="mt-3 grid grid-cols-[5rem_1fr] gap-x-2 gap-y-1 text-xs">
            <dt class="text-muted-foreground">Access</dt><dd>{r.access} · {r.policy}</dd>
            <dt class="text-muted-foreground">Health</dt><dd class="flex items-center gap-2"><span class={["lamp", lampFor[r.state_class] ?? "lamp-idle"]} aria-hidden="true"></span>{r.state} · {r.latency}</dd>
          </dl>
          <details class="mt-3 text-xs"><summary class="text-muted-foreground cursor-pointer">Transport details</summary><dl class="mt-2 grid gap-1"><dt class="text-muted-foreground">Upstream</dt><dd class="break-all font-mono">{r.target}</dd><dt class="text-muted-foreground">Probe</dt><dd class="break-all font-mono">{r.health_path}</dd>{#if r.last_error}<dt class="text-destructive">Last error</dt><dd class="break-all">{friendlyHealth(r.last_error)}<span class="text-muted-foreground block font-mono">{r.last_error}</span></dd>{/if}</dl></details>
        </li>
      {/each}
    </ul>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard users need to scroll this table region) -->
    <div class="hidden overflow-x-auto md:block" role="region" tabindex="0" aria-label="Configured routes">
      <Table.Root>
        <Table.Header><Table.Row>
          <Table.Head class="plate pl-5">Hostname</Table.Head><Table.Head class="plate">Access</Table.Head><Table.Head class="plate">Upstream</Table.Head><Table.Head class="plate">Health</Table.Head><Table.Head class="plate pr-5 text-right">Latency</Table.Head><Table.Head><span class="sr-only">Actions</span></Table.Head>
        </Table.Row></Table.Header>
        <Table.Body>
          {#each data.routes as r (r.host)}
            <Table.Row>
              <Table.Cell class="max-w-80 min-w-56 pl-5 align-top"><div class="font-mono text-[0.82rem] break-all">{r.host}</div><div class="text-muted-foreground text-xs">{r.kind}</div></Table.Cell>
              <Table.Cell class="align-top">
                <span class={["inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[0.7rem] uppercase", r.access === "Protected" ? "border-primary/40 text-primary" : "border-border text-muted-foreground"]}>
                  {#if r.access === "Protected"}<LockIcon class="size-3" />{/if}{r.access === "Protected" ? "login" : "public"}
                </span>
                <div class="text-muted-foreground mt-1 text-xs">{r.policy}</div>
              </Table.Cell>
              <Table.Cell class="max-w-56 align-top"><div class="text-muted-foreground font-mono text-xs break-all">{r.target}</div><div class="text-muted-foreground text-xs">probe {r.health_path}</div></Table.Cell>
              <Table.Cell class="max-w-72 align-top whitespace-normal">
                <span class="flex items-center gap-2 text-sm"><span class={["lamp", lampFor[r.state_class] ?? "lamp-idle"]} aria-hidden="true"></span>{r.state}</span>
                {#if r.last_error}<div class="text-destructive mt-1 text-xs" title={r.last_error}>{friendlyHealth(r.last_error)}</div>{/if}
                {#if r.last_check !== "-"}<div class="text-muted-foreground mt-1 font-mono text-[0.7rem]">checked {new Date(r.last_check).toLocaleTimeString()}</div>{/if}
              </Table.Cell>
              <Table.Cell class="pr-3 text-right align-top font-mono text-xs tabular-nums">{r.latency}</Table.Cell>
              <Table.Cell class="pr-4 align-top"><Button variant="outline" size="sm" onclick={() => inspect(r.host)} aria-label={`Inspect route ${r.host}`}>Inspect</Button></Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</Panel>

{#if selected}
  <Panel label="Route inspector" title={selected.host} sub="Current status projection. Detailed allowlists remain behind the policy API." class="mt-6">
    <dl class="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[9rem_1fr]">
      <dt class="text-muted-foreground">Access</dt><dd>{selected.access} · {selected.policy}</dd>
      <dt class="text-muted-foreground">Transport</dt><dd>{selected.kind}{selected.is_tunnel ? ` · ${selected.target}` : ""}</dd>
      <dt class="text-muted-foreground">Upstream</dt><dd class="font-mono break-all">{selected.target}</dd>
      <dt class="text-muted-foreground">Observed signal</dt><dd>{selected.state}{selected.last_check !== "-" ? ` · checked ${new Date(selected.last_check).toLocaleString()}` : ""}</dd>
      {#if selected.last_error}<dt class="text-muted-foreground">Last probe error</dt><dd class="text-destructive break-all">{friendlyHealth(selected.last_error)}<span class="text-muted-foreground block font-mono">{selected.last_error}</span></dd>{/if}
    </dl>
    <div class="mt-5 flex flex-wrap items-center gap-3">
      <Button onclick={() => navigate("/activity")}>See its access decisions <ArrowRightIcon class="size-4" /></Button>
      <span class="text-muted-foreground text-xs">To ask why someone would be refused, run <code class="font-mono">switchboard policy explain -host {selected.host} -email person@example.com</code> on the host.</span>
    </div>
  </Panel>
{/if}

<p class="text-muted-foreground mt-4 font-mono text-[0.7rem]">{data ? `generated ${new Date(data.generated_at).toLocaleTimeString()} · ` : ""}access routing only</p>
