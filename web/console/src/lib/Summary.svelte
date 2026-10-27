<script lang="ts">
  // A page's few numbers on one quiet line. Tone marks only what needs a look.
  type Item = { value: string | number; label: string; tone?: "ok" | "warn" | "bad" };
  let { items, label }: { items: Item[]; label: string } = $props();
</script>

<dl class="mb-8 flex flex-wrap gap-x-8 gap-y-3" aria-label={label}>
  {#each items as item (item.label)}
    <div class="flex items-baseline gap-2">
      <dt class="sr-only">{item.label}</dt>
      <dd class={["font-mono text-xl font-medium tabular-nums", item.tone === "bad" && "text-destructive", item.tone === "warn" && "text-warning"]}>{item.value}</dd>
      <span class="text-muted-foreground text-sm" aria-hidden="true">{item.label}</span>
    </div>
  {/each}
</dl>
