<script lang="ts">
  import CopyIcon from "@lucide/svelte/icons/copy";
  import CheckIcon from "@lucide/svelte/icons/check";
  // A command to run on a host, with one-click copy. The text is shown as is:
  // nothing here runs it.
  let { command, label = "Copy command" }: { command: string; label?: string } = $props();
  let done = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  async function copy() {
    try { await navigator.clipboard.writeText(command); done = true; clearTimeout(timer); timer = setTimeout(() => (done = false), 1800); }
    catch { done = false; }
  }
</script>

<div class="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3">
  <code class="min-w-0 flex-1 font-mono text-[0.78rem] leading-relaxed break-all whitespace-pre-wrap">{command}</code>
  <button type="button" onclick={copy} aria-label={label} class="text-muted-foreground hover:text-foreground hover:bg-accent focus-visible:outline-ring shrink-0 rounded-md p-1.5 focus-visible:outline-2">
    {#if done}<CheckIcon class="text-success size-4" />{:else}<CopyIcon class="size-4" />{/if}
  </button>
  <span class="sr-only" role="status">{done ? "Copied" : ""}</span>
</div>
