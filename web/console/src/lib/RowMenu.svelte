<script lang="ts">
  import { tick } from "svelte";
  import ChevronIcon from "@lucide/svelte/icons/chevron-down";
  // The secondary actions of one row, each with its name written out: no
  // action is only an icon. Arrow keys move, Escape closes and returns focus.
  type Item = { label: string; hint?: string; onSelect: () => void; destructive?: boolean };
  let { label, items }: { label: string; items: Item[] } = $props();
  const id = $props.id();
  let open = $state(false);
  let root = $state<HTMLElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  const entries = () => [...(root?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
  async function show() { open = true; await tick(); entries()[0]?.focus(); }
  function close(focus = true) { open = false; if (focus) trigger?.focus(); }
  function keys(e: KeyboardEvent) {
    if (!open) return;
    const list = entries(), at = list.indexOf(document.activeElement as HTMLElement);
    if (e.key === "Escape") { e.preventDefault(); close(); }
    else if (e.key === "ArrowDown") { e.preventDefault(); list[(at + 1) % list.length]?.focus(); }
    else if (e.key === "ArrowUp") { e.preventDefault(); list[(at - 1 + list.length) % list.length]?.focus(); }
    else if (e.key === "Tab") close(false);
  }
  function pick(item: Item) { close(false); item.onSelect(); }
</script>

<svelte:window onclick={(e) => { if (open && root && !root.contains(e.target as Node)) close(false); }} />

<div class="relative" bind:this={root} onkeydown={keys} role="presentation">
  <button bind:this={trigger} type="button" aria-haspopup="menu" aria-expanded={open} aria-controls={`${id}-menu`} aria-label={label}
    class="border-input hover:bg-accent focus-visible:ring-ring/50 inline-flex h-8 items-center gap-1 rounded-md border px-2.5 text-[0.8rem] font-medium focus-visible:ring-3 focus-visible:outline-none"
    onclick={() => (open ? close() : void show())}>More <ChevronIcon class="text-muted-foreground size-3.5" aria-hidden="true" /></button>
  {#if open}
    <div id={`${id}-menu`} role="menu" aria-label={label} class="bg-popover border-border animate-in fade-in-0 zoom-in-95 absolute right-0 z-30 mt-1 w-64 rounded-lg border p-1 shadow-2xl duration-100">
      {#each items as item (item.label)}
        <button type="button" role="menuitem" tabindex="-1" aria-label={item.label} title={item.hint} onclick={() => pick(item)}
          class={["hover:bg-accent focus:bg-accent block w-full rounded-md px-2.5 py-2 text-left text-sm outline-none", item.destructive && "text-destructive"]}>
          <span class="block">{item.label}</span>
          {#if item.hint}<span class="text-muted-foreground block text-xs">{item.hint}</span>{/if}
        </button>
      {/each}
    </div>
  {/if}
</div>
