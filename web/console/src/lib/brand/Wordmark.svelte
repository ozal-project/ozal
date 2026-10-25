<script lang="ts">
  import Logo from "./Logo.svelte";
  let { sub = "console" }: { sub?: string } = $props();
</script>

<span class="flex items-center gap-2.5">
  <Logo size={26} />
  <span class="leading-none">
    <span class="block text-[0.98rem] font-semibold tracking-tight">switchboard</span>
    <span class="text-muted-foreground mt-1 block font-mono text-[0.65rem] tracking-wide">{sub}</span>
  </span>
</span>
