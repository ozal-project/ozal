<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { admin, can, endSession } from "$lib/adminToken.svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Freshness from "$lib/Freshness.svelte";
  import { Button } from "$lib/components/ui/button";
  import { fetchPeople, fetchSites, fetchStatus, PagesDisabledError, type AccessEvent, type Site, type StatusResponse } from "$lib/api";
  import { accessScope, accessSummary, applications, attention, certificateView, changes, headline, streamNotices, type StreamState } from "$lib/overview";
  import { subjectsOf } from "$lib/wording.ts";
  import { emptyFeed, receive } from "$lib/feed";
  import { openEventStream } from "$lib/eventStream";
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
  let stream = $state<StreamState>("locked");
  // This page dropped its own oldest events past MAX: the counts no longer start at the first one.
  let trimmed = $state(false);
  const statusRequest = new LatestRequest();
  const sitesRequest = new LatestRequest();

  const needs = $derived(attention(status));
  const certificate = $derived(certificateView(status?.certificate, new Date()));
  const summary = $derived(accessSummary(events));
  const recent = $derived(changes(events, subjects).slice(0, 5));
  // Only the newest deploy of a site offers its versions; older ones were already replaced.
  const undoAt = $derived(new Set(recent.flatMap((c, i) => (c.undo && recent.findIndex((d) => d.undo?.site === c.undo!.site) === i ? [i] : []))));
  const oldest = $derived(events.length ? new Date(events[events.length - 1].time) : null);
  const decisions = $derived(summary.allowed + summary.refused);
  const coverage = $derived({ ...feed, partial: feed.partial || trimmed });
  const notices = $derived(streamNotices(stream, coverage, events.length));
  const apps = $derived(applications(status?.routes ?? []));
  const APPS_SHOWN = 8;
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
  function openRoute(event: MouseEvent, host: string) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault(); navigate("/routes", { host });
  }
  const lamps = { down: "lamp-off", up: "edge-live", unchecked: "lamp-idle" } as const;
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
    if (!can("sites")) return;
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

  const loadSubjects = () => {
    const token = admin.token.trim();
    if (!token || !can("people")) return;
    void fetchPeople().then((p) => { if (admin.token.trim() === token) subjects = subjectsOf(p.users); }).catch(() => {});
  };
  $effect(() => {
    const token = admin.token.trim();
    sitesRequest.cancel(); sites = null; sitesError = ""; pagesOff = false; subjects = new Map();
    if (token) untrack(() => { loadSubjects(); void loadSites(); });
  });
  $effect(() => {
    const token = admin.token.trim();
    events = []; feed = emptyFeed(); trimmed = false;
    if (!token) { stream = "locked"; return; }
    return openEventStream({ token, onDenied: () => endSession(), onState: (state) => (stream = state), onEvent: (message) => {
      try {
        const e = JSON.parse(message.data) as AccessEvent;
        if (typeof e.type !== "string" || typeof e.time !== "string") return;
        const r = receive(feed, e.id ?? message.id);
        feed = r.state;
        if (r.accept) { if (events.length >= MAX) trimmed = true; events = [e, ...events].slice(0, MAX); }
      } catch { /* ignore a malformed event */ }
    } });
  });
  onMount(() => {
    loadStatus();
    const timers = [setInterval(loadStatus, 15_000), setInterval(loadSites, 30_000), setInterval(loadSubjects, 30_000)];
    return () => { timers.forEach(clearInterval); statusRequest.cancel(); sitesRequest.cancel(); };
  });
</script>

<PageHeader title="Overview" intro={status ? headline(status, needs.length) : "Reading the edge…"}>
  <Freshness observedAt={statusAt} error={statusError} staleAfterMs={45_000} />
</PageHeader>

{#if statusError && !status}
  <p class="border-destructive/40 bg-destructive/10 mb-6 rounded-xl border px-4 py-3 text-sm" role="alert">The edge did not answer ({statusError}). Is Ozal running?</p>
{/if}

<div class="mb-6 grid gap-3" aria-live="polite">
  {#each needs as item (item.title)}
    <div class="bg-card flex flex-wrap items-center gap-4 rounded-lg px-4 py-3.5">
      <TriangleAlertIcon class="text-destructive size-5 shrink-0" aria-hidden="true" />
      <div class="min-w-0 basis-[calc(100%-2.25rem)] sm:flex-1 sm:basis-auto">
        <p class="font-semibold">{item.title}</p>
        <p class="text-muted-foreground text-sm" title={item.raw}>{item.detail}</p>
      </div>
      {#if item.action}<Button class="ml-9 sm:ml-0" variant="outline" size="sm" onclick={() => navigate(item.action!.page, { host: item.action!.host })}>{item.action.label}</Button>{/if}
    </div>
  {/each}
  {#if status && needs.length === 0}
    <p class="text-muted-foreground flex items-center gap-2 text-sm"><CheckIcon class="text-success size-4" aria-hidden="true" />Nothing needs you right now.</p>
  {/if}
</div>


{#if notices.length}
  <div class="border-warning/40 bg-warning/[0.06] mb-3 grid gap-1 rounded-lg border px-4 py-3 text-sm" role="status">
    {#each notices as text (text)}<p>{text}</p>{/each}
  </div>
{/if}

<section class="bg-card mb-5 flex flex-wrap items-center gap-x-8 gap-y-3 rounded-lg px-5 py-4" aria-labelledby="overview-access">
  <div class="min-w-0">
    <h2 id="overview-access" class="text-sm font-medium">Who got in</h2>
    <p class="text-muted-foreground mt-0.5 text-xs">{accessScope(stream, coverage, oldest, events.length)}</p>
  </div>
  {#if decisions > 0 || summary.loginsStarted > 0}
    <dl class="flex flex-wrap gap-x-6 gap-y-2">
      {#each [{ n: summary.allowed, label: "let in", lamp: "on" }, { n: summary.refused, label: "refused", lamp: "off" }, { n: summary.loginsStarted, label: "sign-ins started", lamp: "wait" }] as s (s.label)}
        <div class="flex items-baseline gap-2">
          <dt class="sr-only">{s.label}</dt>
          <dd class="text-xl leading-none font-semibold tabular-nums">{s.n}</dd>
          <span class="text-muted-foreground flex items-center gap-1.5 text-xs" aria-hidden="true"><span class={["lamp", `lamp-${s.lamp}`]}></span>{s.label}</span>
        </div>
      {/each}
    </dl>
  {:else if oldest}
    <p class="text-muted-foreground text-sm">No access decisions yet.</p>
  {/if}
  {#if summary.reasons.length > 0}
    <div class="border-border min-w-0 basis-full border-t pt-3 lg:flex-1 lg:basis-auto lg:border-t-0 lg:border-l lg:pt-0 lg:pl-6">
      <p class="plate mb-1">Why people were refused</p>
      <ul class="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        {#each summary.reasons.slice(0, 3) as r (r.code)}<li>{r.text} <span class="text-muted-foreground font-mono text-xs tabular-nums">{r.count}</span></li>{/each}
      </ul>
      {#if summary.rateLimited > 0}<p class="text-muted-foreground mt-1 text-xs">{summary.rateLimited} request{summary.rateLimited === 1 ? "" : "s"} slowed down by rate limits.</p>{/if}
    </div>
  {/if}
  <Button variant="ghost" size="sm" class="-mr-2 ml-auto" onclick={() => navigate("/activity")}>Access log →</Button>
</section>

<div class="grid items-start gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(0,1fr)]">
  <Panel label="What changed" title="Recent changes" sub="Deploys, reloads, people and connectors, newest first." bodyClass="p-0">
    {#if recent.length === 0}
      <p class="text-muted-foreground px-5 pb-6 text-sm">{stream === "locked" ? "Sign in to see recent changes." : stream === "connecting" ? "Connecting to the live stream…" : stream === "lost" ? "The event stream is unavailable." : "No changes since this edge started."}</p>
    {:else}
      <ul class="border-border border-t">
        {#each recent as c, i (c.time + c.type + i)}
          <li class="border-border grid grid-cols-[1.25rem_minmax(0,1fr)] items-start gap-x-3 gap-y-2 border-b px-5 py-3.5 last:border-b-0 sm:grid-cols-[4.6rem_1.25rem_minmax(0,1fr)_auto]">
            <span class="text-muted-foreground hidden pt-0.5 text-xs whitespace-nowrap sm:block">{clock(c.time)}</span>
            <span class={["mt-0.5 grid size-5 place-items-center rounded-md text-[0.7rem]", tones[c.tone]]} aria-hidden="true">{marks[c.tone]}</span>
            <div class="min-w-0">
              <p class="text-muted-foreground mb-1 text-xs sm:hidden">{clock(c.time)}</p>
              <p class="text-sm">{c.title}</p>
              {#if c.source}<p class="text-muted-foreground mt-0.5 font-mono text-xs break-words">{c.source}</p>{/if}
              {#if c.detail}<p class="text-muted-foreground mt-0.5 text-xs break-words">{c.detail}</p>{/if}
            </div>
            {#if c.undo && undoAt.has(i)}<div class="col-start-2 sm:col-start-auto"><Button variant="outline" size="sm" onclick={() => navigate("/sites")} aria-label={`Review the versions of ${c.undo.site} in Sites`}>Review versions</Button></div>{/if}
          </li>
        {/each}
      </ul>
      <div class="border-border border-t px-5 py-3"><Button variant="ghost" size="sm" class="-ml-2" onclick={() => navigate("/activity")}>All activity →</Button></div>
    {/if}
  </Panel>

  <div class="grid gap-5">
    <Panel label="Applications" sub={status ? `${apps.length} behind Ozal${apps.some((a) => a.state === "down") ? `, ${apps.filter((a) => a.state === "down").length} not answering` : ""}` : "Reading the edge…"} bodyClass="pb-2">
      {#snippet actions()}<Button variant="ghost" size="sm" class="-mr-2" onclick={() => navigate("/applications")}>All →</Button>{/snippet}
      {#if status && apps.length === 0}
        <p class="text-muted-foreground px-5 pb-4 text-sm">No application routes configured.</p>
      {:else}
        <ul aria-label="Applications and their state">
          {#each apps.slice(0, APPS_SHOWN) as app (app.host)}
            <li><a class="app-row" href={`/routes?host=${encodeURIComponent(app.host)}`} onclick={(event) => openRoute(event, app.host)}>
              <span class={["lamp", lamps[app.state]]} aria-hidden="true"></span>
              <span class="min-w-0 truncate text-sm" title={app.host}>{app.name}</span>
              <span class={["text-xs", app.state === "down" ? "text-destructive" : "text-muted-foreground"]}>{app.label}<span class="sr-only">{app.state === "up" ? ", answering" : app.state === "unchecked" ? ", not checked yet" : ""}</span></span>
            </a></li>
          {/each}
        </ul>
        {#if apps.length > APPS_SHOWN}<p class="text-muted-foreground px-5 py-2 text-xs">{apps.length - APPS_SHOWN} more in Applications</p>{/if}
      {/if}
    </Panel>

    <Panel label="Sites" sub="What is live, and where it came from" bodyClass="p-0">
      {#snippet actions()}{#if can("sites") && !pagesOff}<Button variant="ghost" size="sm" class="-mr-2" onclick={() => navigate("/sites")}>All →</Button>{/if}{/snippet}
      {#if sitesError && sites !== null}
        <p class="border-warning/40 bg-warning/[0.06] border-y px-5 py-3 text-sm" role="status">Showing the last successful site read. Refresh failed: {sitesError}</p>
      {/if}
      {#if !can("sites")}
        <p class="text-muted-foreground px-5 pb-5 text-sm">This session does not include the Sites tool, so deployments are not listed here.</p>
      {:else if pagesOff}
        <p class="text-muted-foreground px-5 pb-5 text-sm">Static sites are not enabled on this edge.</p>
      {:else if sitesError && sites === null}
        <p class="text-destructive px-5 pb-5 text-sm" role="alert">Sites could not be read ({sitesError}). <button class="underline" onclick={loadSites}>Retry</button></p>
      {:else if sites === null}
        <p class="text-muted-foreground px-5 pb-5 text-sm" aria-live="polite">Reading sites…</p>
      {:else if sites.length === 0}
        <p class="text-muted-foreground px-5 pb-5 text-sm">No site has been published yet.</p>
      {:else}
        <ul class="divide-border border-border divide-y border-t" aria-label="Live site versions">
          {#each sites as site (site.name)}
            {@const deploy = lastDeploy(site.name)}
            <li class="px-5 py-3">
              <div class="flex items-baseline justify-between gap-3">
                <h3 class="min-w-0 truncate text-sm font-medium" title={site.name}>{site.name}</h3>
                <span class="shrink-0 text-xs"><span class="bg-success/15 text-success rounded-md px-1.5 py-0.5 font-mono">{site.live ? site.live.slice(0, 7) : "none"}</span><span class="text-muted-foreground ml-2">{site.deployments.length} version{site.deployments.length === 1 ? "" : "s"}</span></span>
              </div>
              <p class="text-muted-foreground mt-1 text-xs break-words">{#if deploy}{changes([deploy], subjects)[0].source}{:else}<span title="Only deploys since this edge started are remembered here">Published before this session</span>{/if}{site.changed_at ? ` · ${since(site.changed_at)}` : ""}</p>
            </li>
          {/each}
        </ul>
      {/if}
    </Panel>

    {#if certificate}
      <Panel label="Certificate" sub={certificate.note}>
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
          <dt class="text-muted-foreground">Covers</dt>
          <dd class="flex min-w-0 flex-wrap gap-1.5">{#each certificate.names as name (name)}<code class="bg-muted rounded-md px-1.5 py-0.5 text-xs break-all">{name}</code>{/each}</dd>
          <dt class="text-muted-foreground">Valid until</dt>
          <dd class={certificate.tone === "bad" ? "text-destructive" : ""}>{certificate.expires}</dd>
          <dt class="text-muted-foreground">Next renewal</dt>
          <dd>{certificate.renews}</dd>
        </dl>
      </Panel>
    {/if}
  </div>
</div>
