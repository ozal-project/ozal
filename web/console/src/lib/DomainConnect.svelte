<script lang="ts">
  import { onMount } from "svelte";
  import { Button } from "$lib/components/ui/button";
  import { admin } from "$lib/adminToken.svelte";
  import { checkConnection, createRecord, type Connection, type CheckState } from "$lib/api";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import CopyIcon from "@lucide/svelte/icons/copy";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import ExternalIcon from "@lucide/svelte/icons/external-link";

  // UI-006: everything a hostname needs to reach this edge, on one panel.
  // It names the one DNS record, can create it through a configured
  // provider, and re-checks on its own until a public request arrives here.
  let { host, https }: { host: string; https: boolean } = $props();
  let report = $state<Connection | null>(null);
  let error = $state("");
  let checking = $state(false);
  let creating = $state(false);
  let createOpen = $state(false);
  let created = $state("");
  let copied = $state(false);
  let attempts = $state(0);
  let controller: AbortController | null = null;

  async function check() {
    if (checking) return;
    checking = true;
    controller = new AbortController();
    try { report = await checkConnection(admin.token.trim(), host, controller.signal); error = ""; }
    catch (e) { if (!controller.signal.aborted) error = (e as Error).message; }
    finally { checking = false; attempts++; }
  }
  // Check every few seconds while something is still pending, for up to ten
  // minutes; a hidden tab waits.
  onMount(() => {
    void check();
    const timer = setInterval(() => { if (!report?.connected && attempts < 120 && document.visibilityState === "visible") void check(); }, 5_000);
    return () => { clearInterval(timer); controller?.abort(); };
  });

  async function create() {
    creating = true; error = ""; created = "";
    try {
      const record = await createRecord(admin.token.trim(), host);
      created = `${record.type} record created. DNS usually answers within a minute.`;
      attempts = 0;
      await check();
    } catch (e) { error = (e as Error).message; }
    finally { creating = false; }
  }
  async function copy() {
    if (!report?.record) return;
    const r = report.record;
    await navigator.clipboard.writeText(`${r.name} ${r.type} ${r.content}`).then(() => { copied = true; setTimeout(() => (copied = false), 2000); }).catch(() => {});
  }

  const lamp = (state: CheckState) => (state === "ok" ? "lamp-on" : state === "bad" ? "lamp-off" : "lamp-wait");
  const steps = $derived(report ? [
    { label: "DNS", ...report.dns },
    { label: "Certificate", ...report.certificate },
    { label: "Reaches this edge", ...report.reach },
  ] : []);
  const needsRecord = $derived(!!report?.record && report.dns.state !== "ok");
</script>

<div class="grid gap-5">
  {#if report?.record}
    <section aria-labelledby={`record-${host}`}>
      <div class="mb-2 flex items-baseline justify-between gap-3">
        <h3 id={`record-${host}`} class="text-sm font-medium">{needsRecord ? "Add this DNS record" : "DNS record"}</h3>
        <span class="text-muted-foreground text-xs">at your DNS provider</span>
      </div>
      <div class="bg-muted/40 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-lg border p-3 font-mono text-xs sm:grid-cols-[auto_minmax(0,1.6fr)_minmax(0,1fr)_auto] sm:items-center">
        <span class="text-muted-foreground sm:hidden">Type</span><span class="font-semibold">{report.record.type}</span>
        <span class="text-muted-foreground sm:hidden">Name</span><span class="break-all">{report.record.name}</span>
        <span class="text-muted-foreground sm:hidden">Points to</span><span class="break-all">{report.record.content}</span>
        <span class="text-muted-foreground sm:hidden">Proxy</span><span class="font-sans">{report.record.proxied ? "Proxied" : "DNS only"}</span>
      </div>
      <div class="mt-2 flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onclick={copy}><CopyIcon class="size-3.5" /> {copied ? "Copied" : "Copy record"}</Button>
        {#if report.provider === "cloudflare" && needsRecord}
          <Button size="sm" disabled={creating} onclick={() => (createOpen = true)}>{creating ? "Creating…" : "Create it in Cloudflare"}</Button>
        {/if}
      </div>
    </section>
  {:else if report && report.dns.state !== "ok"}
    <p class="text-muted-foreground text-sm">Point <span class="text-foreground font-mono">{host}</span> at this server's public address at your DNS provider. To show the exact record here, set <code class="font-mono">dns.target</code> in the configuration.</p>
  {/if}

  <section aria-label={`Connection of ${host}`} aria-live="polite">
    {#if !report}
      <p class="text-muted-foreground flex items-center gap-2 text-sm"><span class="lamp lamp-wait" aria-hidden="true"></span>Checking {host}…</p>
    {:else}
      <ol class="divide-y rounded-lg border">
        {#each steps as step (step.label)}
          <li class="flex gap-3 px-4 py-3">
            <span class={["lamp mt-1.5", lamp(step.state)]} aria-hidden="true"></span>
            <div class="min-w-0">
              <p class="text-sm font-medium">{step.label} <span class="sr-only">: {step.state === "ok" ? "done" : step.state === "bad" ? "needs attention" : "waiting"}</span></p>
              <p class="text-muted-foreground text-sm">{step.detail}</p>
            </div>
          </li>
        {/each}
      </ol>
    {/if}
  </section>

  {#if created}<p class="text-sm" role="status">{created}</p>{/if}
  {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}

  <div class="flex flex-wrap items-center justify-between gap-2">
    <p class="text-muted-foreground text-xs">
      {#if report?.connected}Connected. Visitors reach this edge at {host}.
      {:else if attempts >= 120}Stopped checking automatically.
      {:else}Checking again every few seconds.{/if}
    </p>
    <div class="flex gap-2">
      {#if report?.connected}<Button variant="outline" size="sm" href={`${https ? "https" : "http"}://${host}/`} target="_blank" rel="noreferrer"><ExternalIcon class="size-3.5" /> Open</Button>{/if}
      <Button variant="ghost" size="sm" disabled={checking} onclick={() => { attempts = 0; void check(); }}><RefreshIcon class={["size-3.5", checking && "animate-spin"]} /> Check now</Button>
    </div>
  </div>
</div>

<ConfirmAction bind:open={createOpen} title="Create this DNS record?" target={host} current="No address record" requested={report?.record ? `${report.record.type} → ${report.record.content}${report.record.proxied ? " (proxied)" : ""}` : ""} consequence="Public DNS for this hostname will point at this edge. A record that already exists is never replaced." detail="The record is created with the provider token from the configuration. Remove it at the provider to undo." confirmLabel="Create record" onConfirm={() => void create()} />
