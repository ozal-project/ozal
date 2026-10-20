<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import * as Table from "$lib/components/ui/table";
  import { Button } from "$lib/components/ui/button";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { fetchStatus, type StatusResponse } from "$lib/api";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import LockIcon from "@lucide/svelte/icons/lock";
  import CertIcon from "@lucide/svelte/icons/badge-check";

  let data = $state<StatusResponse | null>(null);
  let error = $state("");
  let loading = $state(false);

  async function load() {
    loading = true;
    try { data = await fetchStatus(); error = ""; } catch (e) { error = (e as Error).message; } finally { loading = false; }
  }
  onMount(() => { load(); const t = setInterval(load, 15_000); return () => clearInterval(t); });

  const counts = $derived({
    total: data?.routes.length ?? 0,
    protected: data?.routes.filter((r) => r.access === "Protected").length ?? 0,
    unhealthy: data?.routes.filter((r) => r.state_class === "unhealthy").length ?? 0,
  });
  const lampFor = { healthy: "lamp-on", unhealthy: "lamp-off", unknown: "lamp-idle" };
</script>

<PageHeader eyebrow="Routing" title="Routes & hostnames" intro="Every line Switchboard answers, who may be patched through, and whether the far end picks up. Hostnames here are routing keys, not published DNS.">
  <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
</PageHeader>

<div class="mb-6 grid gap-4 sm:grid-cols-3">
  <Stat label="Configured lines" value={data ? counts.total : "—"} note="Routes in the active snapshot" lamp={data ? "on" : "idle"} />
  <Stat label="Require login" value={data ? counts.protected : "—"} note="Protected by an access policy" lamp={data && counts.protected ? "wait" : "idle"} />
  <Stat label="Unhealthy upstreams" value={data ? counts.unhealthy : "—"} note="Far end not answering its probe" lamp={counts.unhealthy > 0 ? "off" : data ? "on" : "idle"} />
</div>

{#if data?.certificate}
  {@const c = data.certificate}
  <Panel index="00" label="Origin certificate" title={c.host} sub={`${c.mode} · ${c.valid ? `valid until ${c.not_after}` : "not valid"}${c.pending ? ` · ${c.pending} pending` : ""}`} class="mb-6">
    {#snippet actions()}<span class="flex items-center gap-2 font-mono text-xs"><CertIcon class="size-4" /><span class={["lamp", c.valid ? "lamp-on" : "lamp-off"]}></span>{c.valid ? "valid" : "invalid"}</span>{/snippet}
    {#if c.last_error}<p class="text-destructive text-sm">{c.last_error}</p>{:else}<p class="text-muted-foreground text-sm">Last outcome: {c.last_outcome || "—"} · next attempt {c.next_attempt || "—"}</p>{/if}
  </Panel>
{/if}

<Panel index="01" label="Patch panel" title="Lines" sub="Updated every 15 seconds" bodyClass="p-0">
  {#if error}
    <p class="text-destructive p-5 text-sm">Status could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !data}
    <div class="space-y-3 p-5">{#each [1, 2, 3] as i (i)}<Skeleton class="h-12 w-full" />{/each}</div>
  {:else if data.routes.length === 0}
    <p class="text-muted-foreground p-5 text-sm">No routes configured.</p>
  {:else}
    <div class="overflow-x-auto">
      <Table.Root>
        <Table.Header><Table.Row>
          <Table.Head class="plate pl-5">Line</Table.Head><Table.Head class="plate">Access</Table.Head><Table.Head class="plate">Far end</Table.Head><Table.Head class="plate">Signal</Table.Head><Table.Head class="plate pr-5 text-right">Latency</Table.Head>
        </Table.Row></Table.Header>
        <Table.Body>
          {#each data.routes as r (r.host)}
            <Table.Row>
              <Table.Cell class="pl-5 align-top"><div class="font-mono text-[0.82rem]">{r.host}</div><div class="text-muted-foreground text-xs">{r.kind}</div></Table.Cell>
              <Table.Cell class="align-top">
                <span class={["inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[0.7rem] uppercase", r.access === "Protected" ? "border-primary/40 text-primary" : "border-border text-muted-foreground"]}>
                  {#if r.access === "Protected"}<LockIcon class="size-3" />{/if}{r.access === "Protected" ? "login" : "public"}
                </span>
                <div class="text-muted-foreground mt-1 text-xs">{r.policy}</div>
              </Table.Cell>
              <Table.Cell class="align-top"><div class="text-muted-foreground font-mono text-xs break-all">{r.target}</div><div class="text-muted-foreground text-xs">probe {r.health_path}</div></Table.Cell>
              <Table.Cell class="max-w-80 align-top whitespace-normal">
                <span class="flex items-center gap-2 text-sm"><span class={["lamp", lampFor[r.state_class] ?? "lamp-idle"]} aria-hidden="true"></span>{r.state}</span>
                {#if r.last_error}<div class="text-destructive mt-1 line-clamp-2 font-mono text-[0.7rem] break-all" title={r.last_error}>{r.last_error}</div>{/if}
                {#if r.last_check !== "-"}<div class="text-muted-foreground mt-1 font-mono text-[0.7rem]">checked {new Date(r.last_check).toLocaleTimeString()}</div>{/if}
              </Table.Cell>
              <Table.Cell class="pr-5 text-right align-top font-mono text-xs tabular-nums">{r.latency}</Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</Panel>
<p class="text-muted-foreground mt-4 font-mono text-[0.7rem]">{data ? `generated ${new Date(data.generated_at).toLocaleTimeString()} · ` : ""}service lifecycle and host health live in Server Ops</p>
