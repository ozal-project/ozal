<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Button } from "$lib/components/ui/button";
  import DnsRecord from "$lib/DnsRecord.svelte";
  import { admin } from "$lib/adminToken.svelte";
  import { fetchZoneSetup, applyZoneSetup, type Zone, type ZoneSetup, type SetupStep } from "$lib/api";
  // EDGE-009: what a domain still needs at its DNS provider and proxy, as a
  // checklist. With a Cloudflare token on the host one button does every
  // step; without it each step says what to click in Cloudflare.
  // onReady lets the card open itself while steps remain.
  let { zone, editable = false, onApplied, onReady }: { zone: Zone; editable?: boolean; onApplied?: () => void; onReady?: (ready: boolean) => void } = $props();
  let setup = $state<ZoneSetup | null>(null);
  let error = $state("");
  let busy = $state<"" | "check" | "apply">("");
  let result = $state<{ text: string; ok: boolean } | null>(null);

  const visible = $derived(setup?.steps.filter((s) => s.state !== "not_needed") ?? []);
  const doable = $derived(!!setup?.provider && visible.some((s) => s.state === "todo" && s.action));
  const left = $derived(visible.filter((s) => s.state !== "done").length);
  $effect(() => { const r = setup?.ready; if (r !== undefined) untrack(() => onReady?.(r)); });
  const lamp: Record<SetupStep["state"], string> = { done: "lamp-on", todo: "lamp-wait", blocked: "lamp-off", unknown: "lamp-warn", not_needed: "" };
  const word: Record<SetupStep["state"], string> = { done: "Done", todo: "To do", blocked: "Needs you", unknown: "Check by hand", not_needed: "" };
  const actionWord: Record<string, string> = { create_cname: "created the DNS record for certificates", set_strict: "set encryption to Full (strict)", upload_certificate: "uploaded this edge's proxy certificate", enable_origin_pull: "turned on Authenticated Origin Pulls" };

  async function load() {
    busy = busy || "check";
    try { setup = await fetchZoneSetup(zone.name); error = ""; }
    catch (e) { error = (e as Error).message; }
    finally { if (busy === "check") busy = ""; }
  }
  onMount(() => { void load(); });

  async function apply() {
    busy = "apply"; result = null;
    try {
      setup = await applyZoneSetup(admin.token.trim(), zone.name);
      const done = (setup.applied ?? []).map((a) => actionWord[a] ?? a);
      result = { ok: true, text: done.length ? `Done: ${done.join(", ")}.` : "Nothing was left to do." };
      onApplied?.();
    } catch (e) {
      result = { ok: false, text: (e as Error).message };
      await load();
    } finally { busy = ""; }
  }
</script>

<section class="grid gap-3" aria-label={`Setup of ${zone.name}`}>
  {#if !setup && !error}
    <p class="text-muted-foreground text-sm" role="status">Checking {zone.name} at its DNS provider…</p>
  {:else if error}
    <p class="text-destructive text-sm" role="alert">{error}</p>
  {:else if setup}
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-sm font-medium">{setup.ready ? "Everything is in place" : left === 1 ? "One step left" : `${left} steps left`}</p>
      <div class="flex gap-2">
        <Button variant="ghost" size="sm" disabled={!!busy} onclick={load}>{busy === "check" ? "Checking…" : "Check again"}</Button>
        {#if doable && editable}<Button size="sm" disabled={!!busy} onclick={apply}>{busy === "apply" ? "Setting up…" : "Set up in Cloudflare"}</Button>{/if}
      </div>
    </div>
    <ol class="grid gap-2">
      {#each visible as step (step.name)}
        <li class="bg-background rounded-lg border p-3">
          <div class="flex flex-wrap items-baseline justify-between gap-2">
            <p class="flex items-center gap-2 text-sm font-medium"><span class={["lamp", lamp[step.state]]} aria-hidden="true"></span>{step.title}</p>
            <span class="text-muted-foreground text-xs">{word[step.state]}</span>
          </div>
          <p class="text-muted-foreground mt-1 text-sm">{step.detail}</p>
          {#if step.state !== "done" && !(doable && step.action)}
            {#if step.name === "challenge" && zone.challenge.delegated}
              <div class="mt-2"><DnsRecord type="CNAME" name={zone.challenge.name} value={zone.challenge.target} /></div>
              <p class="text-muted-foreground mt-1 text-xs">Leave the proxy (Cloudflare's orange cloud) off for this record.</p>
            {:else if step.manual}
              <p class="mt-2 text-sm">{step.manual}</p>
            {/if}
          {/if}
        </li>
      {/each}
    </ol>
    {#if result}<p class={["text-sm", result.ok ? "text-success" : "text-destructive"]} role="status">{result.text}</p>{/if}
    {#if setup.error}<p class="text-muted-foreground text-xs">{setup.error}</p>{/if}
    {#if !setup.provider && !setup.ready}
      <p class="text-muted-foreground bg-muted/40 rounded-lg border p-3 text-xs leading-relaxed">
        Ozal can do these steps for you. Give the host a Cloudflare API token limited to your domains, with <span class="text-foreground">DNS · Edit</span>, <span class="text-foreground">Zone Settings · Edit</span> and <span class="text-foreground">SSL and Certificates · Edit</span>, as <code class="font-mono">dns.cloudflare.token</code> in its configuration. A button then sets up each new domain.
      </p>
    {/if}
  {/if}
</section>
