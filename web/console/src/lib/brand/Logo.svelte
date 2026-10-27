<script lang="ts">
  // Two points joined by a line: the brand mark. Animated, the cord draws in
  // and a signal travels it from one point to the other.
  let { size = 28, animated = false, class: className = "" }: { size?: number; animated?: boolean; class?: string } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 26 26" class={["logo", animated && "logo-live", className]} aria-hidden="true">
  <path class="logo-cord" d="M6 10.5 C6 19, 20 7, 20 15.5" stroke="var(--foreground)" stroke-width="2" fill="none" stroke-linecap="round" pathLength="1" />
  <circle class="logo-from" cx="6" cy="7" r="3.2" fill="var(--foreground)" />
  <circle class="logo-to" cx="20" cy="19" r="3.2" fill="var(--primary)" />
  {#if animated}<circle class="logo-signal" r="1.4" fill="var(--primary)" />{/if}
</svg>
