<script lang="ts">
  // A gauge on the panel: engraved label, big mono reading, a lamp for state.
  let { label, value, note, lamp = "idle" }: { label: string; value: string | number; note: string; lamp?: "on" | "off" | "wait" | "idle" } = $props();
</script>

<div class="panel px-5 py-4">
  <div class="flex items-center justify-between"><p class="plate">{label}</p><span class={["lamp", `lamp-${lamp}`]} aria-hidden="true"></span></div>
  <p class="mt-2 font-mono text-[2.1rem] leading-none font-medium tabular-nums">{value}</p>
  <p class="text-muted-foreground mt-2 text-xs">{note}</p>
</div>
