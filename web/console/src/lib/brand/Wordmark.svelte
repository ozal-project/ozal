<script lang="ts">
  import Logo from "./Logo.svelte";
  let { sub = "console", animated = false }: { sub?: string; animated?: boolean } = $props();
</script>

<span class="flex items-center gap-2.5">
  <Logo size={30} {animated} />
  <span class="leading-none">
    <span class="block text-xl font-medium tracking-tight">ozal</span>
    <span class="text-muted-foreground mt-1 block text-xs">{sub}</span>
  </span>
</span>
