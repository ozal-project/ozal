<script lang="ts">
  import type { Snippet } from "svelte";
  import { route, navigate, type Page } from "$lib/router.svelte";
  // The page says what it is once: the title matches its name in the menu.
  // Related views of the same thing are tabs under it, not separate entries.
  let { title, intro, tabs, children }: { eyebrow?: string; title: string; intro: string; tabs?: { path: Page; label: string }[]; children?: Snippet } = $props();
  const go = (e: MouseEvent, path: Page) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    navigate(path);
  };
</script>

<div class={tabs ? "mb-6" : "mb-8"}>
  <div class="flex flex-wrap items-end justify-between gap-4">
    <div class="max-w-2xl">
      <h1 tabindex="-1" class="text-2xl leading-tight font-semibold tracking-tight outline-none">{title}</h1>
      <p class="text-muted-foreground mt-1.5 text-sm">{intro}</p>
    </div>
    {#if children}<div class="flex flex-wrap items-center gap-2">{@render children()}</div>{/if}
  </div>
  {#if tabs}
    <nav class="border-border mt-6 flex gap-6 border-b" aria-label={`${title} views`}>
      {#each tabs as tab (tab.path)}
        <a href={tab.path} onclick={(e) => go(e, tab.path)} aria-current={route.path === tab.path ? "page" : undefined}
          class="tab text-muted-foreground hover:text-foreground aria-[current=page]:text-foreground -mb-px border-b-2 border-transparent pb-2.5 text-sm transition-colors">{tab.label}</a>
      {/each}
    </nav>
  {/if}
</div>
