<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Freshness from "$lib/Freshness.svelte";
  import { Button } from "$lib/components/ui/button";
  import { fetchPeople, fetchSites, fetchStatus, PagesDisabledError, type AccessEvent, type Site, type StatusResponse } from "$lib/api";
  import { accessSummary, attention, changes, headline } from "$lib/overview";
  import { subjectsOf } from "$lib/wording.ts";
  import { emptyFeed, receive } from "$lib/feed";
  import { LatestRequest } from "$lib/latestRequest";
  import { afterLoad } from "$lib/loadState";
  import { navigate } from "$lib/router.svelte";
  import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";
  import CheckIcon from "@lucide/svelte/icons/circle-check";

  const MAX = 500;

  let status = $state<StatusResponse | null>(null);
  let statusError = $state("");
  let statusAt = $state<number | null>(null);
  let sites = $state<Site[] | null>(null);
  let sitesError = $state("");
  let pagesOff = $state(false);
  let subjects = $state(new Map<string, string>());
  let events = $state<AccessEvent[]>([]);
  let feed = $state(emptyFeed());
  let stream = $state<"connecting" | "live" | "lost">("connecting");
  const statusRequest = new LatestRequest();
  const sitesRequest = new LatestRequest();

  const needs = $derived(attention(status));
  const summary = $derived(accessSummary(events));
  const recent = $derived(changes(events, subjects).slice(0, 8));
  const oldest = $derived(events.length ? new Date(events[events.length - 1].time) : null);
  const decisions = $derived(summary.allowed + summary.refused);
  // The last deploy event of each site, newest first in `events`: where the live version came from.
  const lastDeploy = (site: string) => events.find((e) => e.type === "pages.deployed" && e.host === site);

  const clock = (iso: string) => {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "—" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  };
  const since = (iso: string) => {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "time unknown";
    const mins = Math.max(0, Math.round((Date.now() - d.getTime()) / 60_000));
    return mins < 1 ? "just now" : mins < 60 ? `${mins} min ago` : mins < 1440 ? `${Math.round(mins / 60)} h ago` : `${Math.round(mins / 1440)} d ago`;
  };
  const tones = { good: "bg-success/15 text-success", bad: "bg-destructive/15 text-destructive", neutral: "bg-muted text-muted-foreground" } as const;
  const marks = { good: "↑", bad: "×", neutral: "•" } as const;

  async function loadStatus() {
    const run = statusRequest.start();
    try {
      const next = await fetchStatus(run.signal);
      if (!run.isCurrent()) return;
      status = next;
      statusError = afterLoad({ ok: true }).error;
      statusAt = Date.now();
    } catch (e) {
      if (!run.isCurrent()) return;
      statusError = afterLoad({ ok: false, disabled: false, message: (e as Error).message }).error;
    } finally {
      run.finish();
    }
  }

  async function loadSites() {
    const run = sitesRequest.start();
    try {
      const next = await fetchSites(run.signal);
      if (!run.isCurrent()) return;
      sites = next;
      sitesError = "";
      pagesOff = false;
    } catch (e) {
      if (!run.isCurrent()) return;
      if (e instanceof PagesDisabledError) {
        pagesOff = true;
        sitesError = "";
      } else {
        sitesError = (e as Error).message;
      }
    } finally {
      run.finish();
    }
  }

  onMount(() => {
    const loadSubjects = () => fetchPeople().then((p) => (subjects = subjectsOf(p.users))).catch(() => {});
    loadStatus();
    loadSites();
    loadSubjects();
    const timers = [setInterval(loadStatus, 15_000), setInterval(loadSites, 30_000), setInterval(loadSubjects, 30_000)];
    const es = new EventSource("/_/events");
    es.onopen = () => (stream = "live");
    es.onerror = () => (stream = "lost");
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
    return () => { timers.forEach(clearInterval); es.close(); statusRequest.cancel(); sitesRequest.cancel(); };
  });
</script>

<PageHeader eyebrow="Operate" title="Overview" intro={status ? headline(status, needs.length) : "Reading the edge…"}>
  <Freshness observedAt={statusAt} error={statusError} staleAfterMs={45_000} />
</PageHeader>

{#if statusError && !status}
  <p class="border-destructive/40 bg-destructive/10 mb-6 rounded-xl border px-4 py-3 text-sm" role="alert">The edge did not answer ({statusError}). Is Switchboard running?</p>
{/if}

<div class="mb-6 grid gap-3" aria-live="polite">
  {#each needs as item (item.title)}
    <div class="border-destructive/40 from-destructive/10 to-card flex flex-wrap items-center gap-4 rounded-2xl border bg-gradient-to-r px-4 py-3.5">
      <TriangleAlertIcon class="text-destructive size-5 shrink-0" aria-hidden="true" />
      <div class="min-w-0 flex-1">
        <p class="font-semibold">{item.title}</p>
        <p class="text-muted-foreground text-sm" title={item.raw}>{item.detail}</p>
      </div>
      {#if item.action}<Button variant="outline" size="sm" onclick={() => navigate(item.action!.page, { host: item.action!.host })}>{item.action.label}</Button>{/if}
    </div>
  {/each}
  {#if status && needs.length === 0}
    <p class="text-muted-foreground flex items-center gap-2 text-sm"><CheckIcon class="text-success size-4" aria-hidden="true" />Nothing needs you right now.</p>
  {/if}
</div>

<div class="grid gap-5 lg:grid-cols-[1.4fr_1fr]">
  <Panel label="What changed" title="Recent changes" sub="Deploys, reloads, people and connectors, newest first." bodyClass="p-0">
    {#if recent.length === 0}
      <p class="text-muted-foreground px-5 py-8 text-sm">{stream === "connecting" ? "Connecting to the live stream…" : "No changes since this edge started."}</p>
    {:else}
      <ul>
        {#each recent as c, i (c.time + c.type + i)}
          <li class="border-border grid grid-cols-[4.6rem_1.6rem_1fr_auto] items-start gap-3 border-b px-5 py-3.5 last:border-b-0">
            <span class="text-muted-foreground pt-0.5 font-mono text-xs whitespace-nowrap">{clock(c.time)}</span>
            <span class={["mt-0.5 grid size-5 place-items-center rounded-md text-[0.7rem]", tones[c.tone]]} aria-hidden="true">{marks[c.tone]}</span>
            <div class="min-w-0">
              <p class="text-sm">{c.title}</p>
              {#if c.source}<p class="text-muted-foreground mt-0.5 font-mono text-xs break-words">{c.source}</p>{/if}
              {#if c.detail}<p class="text-muted-foreground mt-0.5 text-xs break-words">{c.detail}</p>{/if}
            </div>
            {#if c.undo}<Button variant="outline" size="sm" onclick={() => navigate("/sites")} aria-label={`Review the versions of ${c.undo.site} in Sites`}>Review versions</Button>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>

  <Panel label="Access" title="Who got in" sub={oldest ? `Since ${clock(oldest.toISOString())} · last ${events.length} events held in memory.` : "Waiting for the first events."} bodyClass="p-0">
    <div class="border-border grid grid-cols-3 border-b">
      {#each [{ n: summary.allowed, label: "requests let in", lamp: "on" }, { n: summary.refused, label: "requests refused", lamp: "off" }, { n: summary.loginsStarted, label: "sign-ins started", lamp: "wait" }] as s (s.label)}
        <div class="border-border border-r px-5 py-4 last:border-r-0">
          <p class="text-[1.7rem] leading-none font-semibold tracking-tight tabular-nums">{s.n}</p>
          <p class="text-muted-foreground mt-2 flex items-center gap-1.5 text-xs"><span class={["lamp", `lamp-${s.lamp}`]} aria-hidden="true"></span>{s.label}</p>
        </div>
      {/each}
    </div>
    <div class="px-5 py-4">
      <p class="plate mb-3">Why people were refused</p>
      {#if summary.reasons.length === 0}
        <p class="text-muted-foreground text-sm">{decisions === 0 ? "No decisions yet." : "Nobody was refused."}</p>
      {:else}
        <ul class="grid gap-2 text-sm">
          {#each summary.reasons.slice(0, 4) as r (r.code)}
            <li class="flex items-baseline justify-between gap-3"><span>{r.text}</span><span class="text-muted-foreground font-mono text-xs tabular-nums">{r.count}</span></li>
          {/each}
        </ul>
      {/if}
      {#if summary.rateLimited > 0}<p class="text-muted-foreground mt-3 text-xs">{summary.rateLimited} request{summary.rateLimited === 1 ? "" : "s"} slowed down by rate limits.</p>{/if}
      <p class="mt-4"><Button variant="ghost" size="sm" class="-ml-2" onclick={() => navigate("/activity")}>Open the access log →</Button></p>
    </div>
  </Panel>
</div>

<div class="mt-5">
  <Panel label="Sites" title="What is live, and where it came from" bodyClass="p-0">
    {#if sitesError && sites !== null}
      <p class="border-warning/40 bg-warning/[0.06] border-b px-5 py-3 text-sm" role="status">Showing the last successful site read. Refresh failed: {sitesError}</p>
    {/if}
    {#if pagesOff}
      <p class="text-muted-foreground px-5 py-8 text-sm">Static sites are not enabled on this edge.</p>
    {:else if sitesError && sites === null}
      <p class="text-destructive px-5 py-6 text-sm" role="alert">Sites could not be read ({sitesError}). <button class="underline" onclick={loadSites}>Retry</button></p>
    {:else if sites === null}
      <p class="text-muted-foreground px-5 py-8 text-sm" aria-live="polite">Reading sites…</p>
    {:else if sites.length === 0}
      <p class="text-muted-foreground px-5 py-8 text-sm">No site has been published yet.</p>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead><tr class="text-muted-foreground border-border border-b text-left font-mono text-xs">
            <th class="px-5 py-2.5 font-medium">Site</th><th class="px-5 py-2.5 font-medium">Live version</th><th class="px-5 py-2.5 font-medium">Came from</th><th class="px-5 py-2.5 font-medium">Versions</th><th class="px-5 py-2.5"><span class="sr-only">Actions</span></th>
          </tr></thead>
          <tbody>
            {#each sites as site (site.name)}
              {@const deploy = lastDeploy(site.name)}
              <tr class="border-border border-b last:border-b-0">
                <td class="px-5 py-3.5 font-medium">{site.name}</td>
                <td class="px-5 py-3.5"><span class="bg-success/15 text-success rounded-md px-1.5 py-1 font-mono text-xs">{site.live ? site.live.slice(0, 7) : "none"}</span> <span class="text-muted-foreground ml-1 text-xs">{site.changed_at ? since(site.changed_at) : ""}</span></td>
                <td class="text-muted-foreground px-5 py-3.5 text-xs">{#if deploy}{changes([deploy], subjects)[0].source}{:else}<span title="Only deploys since this edge started are remembered here">earlier than this session</span>{/if}</td>
                <td class="text-muted-foreground px-5 py-3.5 font-mono text-xs">{site.deployments.length}</td>
                <td class="px-5 py-3.5 text-right"><Button variant="outline" size="sm" onclick={() => navigate("/sites")}>Open</Button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Panel>
</div>
