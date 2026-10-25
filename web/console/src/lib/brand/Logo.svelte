<script lang="ts">
  // Two points joined by a line: the brand mark.
  let { size = 28, class: className = "" }: { size?: number; class?: string } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 26 26" class={className} aria-hidden="true">
  <circle cx="6" cy="7" r="3.2" fill="var(--foreground)" />
  <circle cx="20" cy="19" r="3.2" fill="var(--primary)" />
  <path d="M6 10.5 C6 19, 20 7, 20 15.5" stroke="var(--foreground)" stroke-width="2" fill="none" stroke-linecap="round" />
</svg>
