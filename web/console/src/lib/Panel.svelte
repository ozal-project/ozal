<script lang="ts">
  import type { Snippet } from "svelte";
  // A panel: its title (or, without one, its label) and actions, then the body.
  let { index, label, title, sub, actions, children, class: className = "", bodyClass = "p-5" }:
    { index?: string; label: string; title?: string; sub?: string; actions?: Snippet; children: Snippet; class?: string; bodyClass?: string } = $props();
</script>

<section class={["panel", className]}>
  <header class="panel-head">
    <div class="min-w-0">
      <h2 class="text-sm font-medium">{title ?? label}</h2>
      {#if sub}<p class="text-muted-foreground mt-0.5 text-xs">{sub}</p>{/if}
    </div>
    {@render actions?.()}
  </header>
  <div class={bodyClass}>{@render children()}</div>
</section>
