<script lang="ts">
  import type { Snippet } from "svelte";
  // A switchboard panel: an engraved plate (index + label), an optional
  // title and actions, then the body.
  let { index, label, title, sub, actions, children, class: className = "", bodyClass = "p-5" }:
    { index?: string; label: string; title?: string; sub?: string; actions?: Snippet; children: Snippet; class?: string; bodyClass?: string } = $props();
</script>

<section class={["panel", className]}>
  <header class="panel-head">
    <div class="min-w-0">
      <p class="plate">{label}</p>
      {#if title}<h2 class="mt-1 text-base font-semibold tracking-tight">{title}</h2>{/if}
      {#if sub}<p class="text-muted-foreground text-sm">{sub}</p>{/if}
    </div>
    {@render actions?.()}
  </header>
  <div class={bodyClass}>{@render children()}</div>
</section>
