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
  import { fetchSites, fetchProjects, fetchStatus, rollback, PagesDisabledError, type Site, type Project, type StatusResponse, type Deployment } from "$lib/api";
  import { LatestRequest } from "$lib/latestRequest";
  import Freshness from "$lib/Freshness.svelte";
  import Projects from "./Projects.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import UndoIcon from "@lucide/svelte/icons/undo-2";

  let sites = $state<Site[] | null>(null);
  let error = $state("");
  let disabled = $state(false);
  let loading = $state(false);
  let observedAt = $state<number | null>(null);
  let routeStatus = $state<StatusResponse | null>(null);
  let projects = $state<Project[] | null>(null);
  let projectsError = $state("");
  let projectsLoading = $state(false);
  let routeStatusError = $state("");
  const sitesRequest = new LatestRequest();
  const projectsRequest = new LatestRequest();
  // The token is shared by the console pages, in this tab's memory only.
  import { admin, askForToken, can } from "$lib/adminToken.svelte";
  import { afterLoad } from "$lib/loadState";
  let mutations = $state<Record<string, AbortController>>({});
  let review = $state<{ site: string; deployment: Deployment; current: Deployment | undefined; hosts: string[] } | null>(null);
  let reviewOpen = $state(false);
  // Feedback belongs next to the button that caused it: a message at the top
  // of the page is out of sight when the click happened further down.
  let notices = $state<Record<string, { text: string; ok: boolean }>>({});

  async function loadProjects() {
    const token = admin.token.trim();
    if (!token) {
      projectsRequest.cancel();
      projects = null;
      projectsError = "";
      projectsLoading = false;
      return;
    }
    const run = projectsRequest.start();
    projectsLoading = true;
    projectsError = "";
    try {
      const result = await fetchProjects(token, run.signal);
      if (!run.isCurrent()) return;
      projects = result;
    } catch (e) {
      if (!run.isCurrent()) return;
      projects = null;
      projectsError = (e as Error).message;
    } finally {
      if (run.isCurrent()) projectsLoading = false;
      run.finish();
    }
  }

  async function load() {
    void loadProjects();
    const run = sitesRequest.start();
    loading = true;
    const [siteResult, statusResult] = await Promise.allSettled([fetchSites(run.signal), fetchStatus(run.signal)]);
    if (!run.isCurrent()) return;
    if (statusResult.status === "fulfilled") {
      routeStatus = statusResult.value;
      routeStatusError = "";
    } else {
      routeStatusError = (statusResult.reason as Error).message;
    }
    if (siteResult.status === "fulfilled") {
      sites = siteResult.value;
      ({ disabled, error } = afterLoad({ ok: true }));
      observedAt = Date.now();
    } else {
      const reason = siteResult.reason;
      ({ disabled, error } = afterLoad({ ok: false, disabled: reason instanceof PagesDisabledError, message: (reason as Error).message }));
    }
    loading = false;
    run.finish();
  }
  onMount(() => { load(); const t = setInterval(load, 15_000); return () => { clearInterval(t); sitesRequest.cancel(); projectsRequest.cancel(); Object.values(mutations).forEach((controller) => controller.abort()); }; });

  function restore(site: Site, deployment: Deployment) {
    if (!routeStatus || routeStatusError) {
      notices = { ...notices, [site.name]: { text: "Route mappings could not be refreshed; retry before changing the live build so the affected hostnames are clear.", ok: false } };
      return;
    }
    const hosts = routeStatus.routes.filter((r) => r.is_static && r.target === `site:${site.name}`).map((r) => r.host);
    review = { site: site.name, deployment, current: site.deployments.find((d) => d.live), hosts };
    reviewOpen = true;
  }

  async function commitRestore() {
    const action = review;
    if (!action) return;
    reviewOpen = false;
    if (!admin.token.trim()) {
      notices = { ...notices, [action.site]: { text: "Sign in as admin first (top right); it is needed to change the live build.", ok: false } };
      askForToken();
      return;
    }
    const key = `${action.site}:${action.deployment.id}`;
    const controller = new AbortController();
    mutations = { ...mutations, [key]: controller };
    const nextNotices = { ...notices };
    delete nextNotices[action.site];
    notices = nextNotices;
    try {
      await rollback(admin.token.trim(), action.site, action.deployment.id, controller.signal);
      notices = { ...notices, [action.site]: { text: `${action.site} now serves ${action.deployment.id.slice(0, 12)}.`, ok: true } };
      await load();
    } catch (e) {
      const uncertain = controller.signal.aborted || (e as Error).name === "TimeoutError";
      notices = { ...notices, [action.site]: { text: uncertain ? "The response was interrupted. The build may have changed; refreshing to reconcile what is live." : (e as Error).message, ok: false } };
      if (uncertain) await load();
    } finally {
      const next = { ...mutations };
      delete next[key];
      mutations = next;
    }
  }

  function stopRestore(key: string) { mutations[key]?.abort(new DOMException("Stopped by operator", "AbortError")); }
  function siteBusy(name: string) { return Object.keys(mutations).some((key) => key.startsWith(`${name}:`)); }

  const size = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / (1 << 20)).toFixed(1)} MiB`);
  const when = (iso: string) => new Date(iso).toLocaleString();
  const total = $derived(sites?.reduce((n, s) => n + s.deployments.length, 0) ?? 0);
</script>

<PageHeader eyebrow="Pages" title="Sites & deployments" intro="Static sites Switchboard serves from its own store. Every deployment is immutable and named by its content hash; going live or back is one switch, never a half-copied site.">
  <div class="flex flex-col items-start gap-2 sm:items-end">
    <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
    <Freshness observedAt={observedAt} refreshing={loading && sites !== null} error={error} />
  </div>
</PageHeader>

{#if disabled}
  <Panel label="Pages" title="Not enabled on this edge">
    <p class="text-muted-foreground text-sm">Add a <code class="font-mono">pages</code> block (an absolute <code class="font-mono">dir</code>) and start Switchboard with <code class="font-mono">-db</code>. Routes then serve a deployment with <code class="font-mono">"site": "name"</code>.</p>
  </Panel>
{:else}
  <div class="mb-5 flex flex-wrap gap-x-4 gap-y-1 rounded-lg border px-3 py-2 text-sm sm:hidden" aria-label="Sites summary">
    <span>{sites ? sites.length : "—"} sites</span><span>{total} retained builds</span><span>Deploy with the CLI</span>
  </div>
  <div class="mb-6 hidden gap-4 sm:grid sm:grid-cols-3">
    <Stat label="Sites" value={sites ? sites.length : "—"} note="With at least one deployment" lamp={sites ? "on" : "idle"} />
    <Stat label="Retained builds" value={sites ? total : "—"} note="Ready for an instant rollback" lamp={total ? "on" : "idle"} />
    <Stat label="Deploy from a terminal" value="CLI" note="switchboard site deploy -site NAME ./dist" lamp="wait" />
  </div>

  {#if error && sites}<p class="border-warning/40 bg-warning/[0.06] mb-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>{/if}
  {#if error && !sites}
    <p class="text-destructive text-sm">Deployments could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !sites}
    <div class="space-y-3" aria-busy="true" aria-label="Loading deployments">{#each [1, 2] as i (i)}<Skeleton class="h-24 w-full" />{/each}</div>
  {:else if sites.length === 0}
    <Panel label="Sites" title="Nothing deployed yet">
      <p class="text-muted-foreground text-sm">Upload a build with <code class="font-mono">switchboard site deploy -site NAME ./dist</code>; it appears here with its history.</p>
    </Panel>
  {:else}
    <div class="space-y-6">
      {#each sites as site, i (site.name)}
        <Panel label="Site" title={site.name} sub={`live since ${when(site.changed_at)}`} bodyClass="p-0">
          {#if notices[site.name]}
            <p class={["border-b px-5 py-3 text-sm", notices[site.name].ok ? "text-foreground" : "text-destructive"]} role="status">{notices[site.name].text}</p>
          {/if}
          <ul class="space-y-3 p-3 md:hidden" aria-label={`Deployments for ${site.name}`}>
            {#each site.deployments as d (d.id)}
              {@const key = `${site.name}:${d.id}`}
              <li class="border-border rounded-lg border p-3">
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0"><p class="break-all font-mono text-sm" title={d.id}>{d.id.slice(0, 12)}</p><p class="text-muted-foreground text-xs">{when(d.created_at)}</p></div>
                  {#if d.live}<span class="inline-flex shrink-0 items-center gap-2 font-mono text-xs"><span class="lamp lamp-on" aria-hidden="true"></span>live</span>
                  {:else}<Button variant="outline" size="sm" disabled={siteBusy(site.name)} onclick={() => restore(site, d)} aria-label={`Review activation of ${d.id.slice(0, 12)} for ${site.name}`}><UndoIcon class="size-3.5" /> Review</Button>{/if}
                </div>
                <p class="text-muted-foreground mt-2 text-xs">{d.files} files · {size(d.bytes)}</p>
                {#if mutations[key]}<div class="mt-2"><Button variant="ghost" size="sm" onclick={() => stopRestore(key)}>Stop waiting</Button></div>{/if}
              </li>
            {/each}
          </ul>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard users need to scroll this table region) -->
          <div class="hidden overflow-x-auto md:block" role="region" tabindex="0" aria-label={`Deployments for ${site.name}`}>
            <Table.Root>
              <Table.Header><Table.Row>
                <Table.Head class="plate pl-5">Deployment</Table.Head><Table.Head class="plate">Uploaded</Table.Head><Table.Head class="plate">Contents</Table.Head><Table.Head class="plate pr-5 text-right">State</Table.Head>
              </Table.Row></Table.Header>
              <Table.Body>
                {#each site.deployments as d (d.id)}
                  <Table.Row>
                    {@const key = `${site.name}:${d.id}`}
                    <Table.Cell class="pl-5"><span class="font-mono text-[0.8rem]" title={d.id}>{d.id.slice(0, 12)}</span></Table.Cell>
                    <Table.Cell class="text-muted-foreground font-mono text-xs">{when(d.created_at)}</Table.Cell>
                    <Table.Cell class="text-muted-foreground font-mono text-xs">{d.files} files · {size(d.bytes)}</Table.Cell>
                    <Table.Cell class="pr-5 text-right">
                      {#if d.live}
                        <span class="inline-flex items-center gap-2 font-mono text-xs"><span class="lamp lamp-on" aria-hidden="true"></span>live</span>
                      {:else}
                        <Button variant="outline" size="sm" disabled={siteBusy(site.name)} onclick={() => restore(site, d)}>
                          <UndoIcon class="size-3.5" /> {mutations[key] ? "Waiting…" : "Review activation"}
                        </Button>
                        {#if mutations[key]}<Button variant="ghost" size="sm" onclick={() => stopRestore(key)}>Stop waiting</Button>{/if}
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
  <div class="mt-12 border-t pt-8">
    {#if !admin.token || can("publishing")}<Projects embedded />
    {:else}<p class="text-muted-foreground text-sm">CI publishing needs the CI publishing tool, which this session does not include.</p>{/if}
  </div>
{/if}

<ConfirmAction
  bind:open={reviewOpen}
  title="Change the live site build?"
  target={review?.site ?? ""}
  current={review?.current ? `Live ${review.current.id.slice(0, 12)}` : "No live deployment recorded"}
  requested={review ? `Live ${review.deployment.id.slice(0, 12)}` : ""}
  consequence="Requests to the mapped hostnames will receive the selected build as soon as the server switches it."
  detail={review ? `Mapped hostnames: ${review.hosts.length ? review.hosts.join(", ") : "none"}. Previous build remains selectable if retained: ${review.current?.id.slice(0, 12) ?? "none"}.` : ""}
  confirmLabel="Make build live"
  onConfirm={commitRestore}
/>
