<script lang="ts">
  import { onMount } from "svelte";
  import { ageLabel, isStale } from "$lib/freshness";

  let { observedAt, refreshing = false, error = "", staleAfterMs = 60_000 } = $props<{
    observedAt: number | null;
    refreshing?: boolean;
    error?: string;
    staleAfterMs?: number;
  }>();

  let nowMs = $state<number>(Date.now());
  onMount(() => {
    const timer = setInterval(() => (nowMs = Date.now()), 10_000);
    return () => clearInterval(timer);
  });

  const ageText = $derived(observedAt === null ? "not loaded" : ageLabel(nowMs - observedAt));
  const isOld = $derived(observedAt !== null && isStale(nowMs - observedAt, Boolean(error), staleAfterMs));
  const statusLabel = $derived(observedAt === null ? "Not loaded" : error ? "Last read failed" : refreshing ? "Refreshing" : isOld ? "Stale" : "Current");
  const lamp = $derived(observedAt === null ? "lamp-idle" : error || isOld ? "lamp-off" : refreshing ? "lamp-wait" : "lamp-on");
</script>

<p class="text-muted-foreground inline-flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[0.7rem]" title={error || undefined}>
  <span class={[
    "lamp",
    lamp,
  ]} aria-hidden="true"></span>
  <span>{statusLabel} · {ageText}</span>
  {#if error}<span class="text-destructive">· {error}</span>{/if}
</p>
