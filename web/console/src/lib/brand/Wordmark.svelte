<script lang="ts">
  import Logo from "./Logo.svelte";
  let { sub = "access console" }: { sub?: string } = $props();
</script>

<span class="flex items-center gap-3">
  <Logo size={34} />
  <span class="leading-none">
    <span class="block font-mono text-[0.95rem] font-semibold tracking-tight">switchboard<span class="text-primary">_</span></span>
    <span class="text-muted-foreground mt-1 block font-mono text-[0.62rem] tracking-[0.2em] uppercase">{sub}</span>
  </span>
</span>
