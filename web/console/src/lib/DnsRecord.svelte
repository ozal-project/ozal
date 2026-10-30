<script lang="ts">
  import CopyIcon from "@lucide/svelte/icons/copy";
  import CheckIcon from "@lucide/svelte/icons/check";
  // One DNS record to create at the provider, laid out as provider forms ask
  // for it (type, name, value), with each value copyable on its own.
  let { type, name, value }: { type: string; name: string; value: string } = $props();
  let copied = $state("");
  async function copy(what: string, text: string) {
    try { await navigator.clipboard.writeText(text); copied = what; setTimeout(() => { if (copied === what) copied = ""; }, 1800); } catch { /* clipboard refused */ }
  }
</script>

{#snippet field(label: string, text: string)}
  <div class="min-w-0">
    <p class="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">{label}</p>
    <div class="flex items-center gap-1">
      <code class="min-w-0 flex-1 font-mono text-xs break-all">{text}</code>
      <button type="button" class="text-muted-foreground hover:text-foreground hover:bg-accent shrink-0 rounded p-1" aria-label={`Copy ${label.toLowerCase()} ${text}`} onclick={() => copy(label, text)}>
        {#if copied === label}<CheckIcon class="text-success size-3.5" />{:else}<CopyIcon class="size-3.5" />{/if}
      </button>
    </div>
  </div>
{/snippet}

<div class="bg-background grid gap-3 rounded-lg border p-3 sm:grid-cols-[4.5rem_minmax(0,1fr)_minmax(0,1fr)]">
  <div>
    <p class="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">Type</p>
    <code class="font-mono text-xs font-semibold">{type}</code>
  </div>
  {@render field("Name", name)}
  {@render field("Value", value)}
</div>
<span class="sr-only" role="status">{copied ? "Copied" : ""}</span>
