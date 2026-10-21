<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import * as Table from "$lib/components/ui/table";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { fetchSites, rollback, PagesDisabledError, type Site } from "$lib/api";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import UndoIcon from "@lucide/svelte/icons/undo-2";

  let sites = $state<Site[] | null>(null);
  let error = $state("");
  let disabled = $state(false);
  let loading = $state(false);
  // The token is shared by the console pages, in this tab's memory only.
  import { admin } from "$lib/adminToken.svelte";
  import { afterLoad } from "$lib/loadState";
  let pending = $state("");
  // Feedback belongs next to the button that caused it: a message at the top
  // of the page is out of sight when the click happened further down.
  let notice = $state<{ site: string; text: string; ok: boolean } | null>(null);
  let tokenInput = $state<HTMLInputElement | null>(null);

  async function load() {
    loading = true;
    try {
      sites = await fetchSites();
      ({ disabled, error } = afterLoad({ ok: true }));
    } catch (e) {
      ({ disabled, error } = afterLoad({ ok: false, disabled: e instanceof PagesDisabledError, message: (e as Error).message }));
    } finally {
      loading = false;
    }
  }
  onMount(() => { load(); const t = setInterval(load, 15_000); return () => clearInterval(t); });

  async function restore(site: string, id: string) {
    notice = null;
    if (!admin.token.trim()) {
      notice = { site, text: "Paste the admin token above first; it is needed to change the live build.", ok: false };
      tokenInput?.scrollIntoView({ behavior: "smooth", block: "center" });
      tokenInput?.focus({ preventScroll: true });
      return;
    }
    pending = id;
    try {
      await rollback(admin.token.trim(), site, id);
      notice = { site, text: `${site} now serves ${id.slice(0, 12)}.`, ok: true };
      await load();
    } catch (e) {
      notice = { site, text: (e as Error).message, ok: false };
    } finally {
      pending = "";
    }
  }

  const size = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / (1 << 20)).toFixed(1)} MiB`);
  const when = (iso: string) => new Date(iso).toLocaleString();
  const total = $derived(sites?.reduce((n, s) => n + s.deployments.length, 0) ?? 0);
</script>

<PageHeader eyebrow="Pages" title="Sites & deployments" intro="Static sites Switchboard serves from its own store. Every deployment is immutable and named by its content hash; going live or back is one switch, never a half-copied site.">
  <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
</PageHeader>

{#if disabled}
  <Panel label="Pages" title="Not enabled on this edge">
    <p class="text-muted-foreground text-sm">Add a <code class="font-mono">pages</code> block (an absolute <code class="font-mono">dir</code>) and start Switchboard with <code class="font-mono">-db</code>. Routes then serve a deployment with <code class="font-mono">"site": "name"</code>.</p>
  </Panel>
{:else}
  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Stat label="Sites" value={sites ? sites.length : "—"} note="With at least one deployment" lamp={sites ? "on" : "idle"} />
    <Stat label="Retained builds" value={sites ? total : "—"} note="Ready for an instant rollback" lamp={total ? "on" : "idle"} />
    <Stat label="Deploy from a terminal" value="CLI" note="switchboard site deploy -site NAME ./dist" lamp="wait" />
  </div>

  <Panel index="00" label="Rollback" title="Admin token" sub="Needed only to change which build is live. Kept in this tab's memory for every page, never stored." class="mb-6">
    <div class="flex max-w-xl flex-col gap-2">
      <Label for="token" class="text-muted-foreground flex items-center gap-1.5 font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase"><KeyIcon class="size-3.5" /> Admin token</Label>
      <Input id="token" type="password" bind:ref={tokenInput} bind:value={admin.token} placeholder="Bearer token from admin.token" autocomplete="off" spellcheck={false} class="font-mono" />
    </div>
  </Panel>

  {#if error}
    <p class="text-destructive text-sm">Deployments could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !sites}
    <div class="space-y-3">{#each [1, 2] as i (i)}<Skeleton class="h-24 w-full" />{/each}</div>
  {:else if sites.length === 0}
    <Panel label="Sites" title="Nothing deployed yet">
      <p class="text-muted-foreground text-sm">Upload a build with <code class="font-mono">switchboard site deploy -site NAME ./dist</code>; it appears here with its history.</p>
    </Panel>
  {:else}
    <div class="space-y-6">
      {#each sites as site, i (site.name)}
        <Panel index={String(i + 1).padStart(2, "0")} label="Site" title={site.name} sub={`live since ${when(site.changed_at)}`} bodyClass="p-0">
          {#if notice?.site === site.name}
            <p class={["border-b px-5 py-3 text-sm", notice.ok ? "text-foreground" : "text-destructive"]} role="status">{notice.text}</p>
          {/if}
          <div class="overflow-x-auto">
            <Table.Root>
              <Table.Header><Table.Row>
                <Table.Head class="plate pl-5">Deployment</Table.Head><Table.Head class="plate">Uploaded</Table.Head><Table.Head class="plate">Contents</Table.Head><Table.Head class="plate pr-5 text-right">State</Table.Head>
              </Table.Row></Table.Header>
              <Table.Body>
                {#each site.deployments as d (d.id)}
                  <Table.Row>
                    <Table.Cell class="pl-5"><span class="font-mono text-[0.8rem]" title={d.id}>{d.id.slice(0, 12)}</span></Table.Cell>
                    <Table.Cell class="text-muted-foreground font-mono text-xs">{when(d.created_at)}</Table.Cell>
                    <Table.Cell class="text-muted-foreground font-mono text-xs">{d.files} files · {size(d.bytes)}</Table.Cell>
                    <Table.Cell class="pr-5 text-right">
                      {#if d.live}
                        <span class="inline-flex items-center gap-2 font-mono text-xs"><span class="lamp lamp-on" aria-hidden="true"></span>live</span>
                      {:else}
                        <Button variant="outline" size="sm" disabled={pending !== ""} onclick={() => restore(site.name, d.id)}>
                          <UndoIcon class="size-3.5" /> {pending === d.id ? "Switching…" : "Make live"}
                        </Button>
                      {/if}
                    </Table.Cell>
                  </Table.Row>
                {/each}
              </Table.Body>
            </Table.Root>
          </div>
        </Panel>
      {/each}
    </div>
  {/if}
{/if}
