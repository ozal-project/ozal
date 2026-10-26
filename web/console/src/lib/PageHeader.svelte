<script lang="ts">
  import type { Snippet } from "svelte";
  let { eyebrow, title, intro, children }: { eyebrow: string; title: string; intro: string; children?: Snippet } = $props();
</script>

<div class="mb-8 flex flex-wrap items-end justify-between gap-4">
  <div class="max-w-3xl">
    <p class="plate mb-3 flex items-center gap-2">
      <svg width="28" height="10" viewBox="0 0 28 10" aria-hidden="true"><path d="M1 9 C 8 9, 10 1, 27 1" class="cord" stroke-width="1.6" /></svg>
      {eyebrow}
    </p>
    <h1 tabindex="-1" class="outline-none text-[2.1rem] leading-tight font-semibold tracking-tight">{title}</h1>
    <p class="text-muted-foreground mt-2 text-[0.95rem]">{intro}</p>
  </div>
  {@render children?.()}
</div>
