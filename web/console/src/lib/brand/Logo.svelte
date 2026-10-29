<script lang="ts">
  // Umbral: a doorway, and the light of the open door. Ivory arch, camel
  // light, graphite leaf. Animated, the door opens.
  let { size = 28, animated = false, class: className = "" }: { size?: number; animated?: boolean; class?: string } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" class={["logo", animated && "logo-live", className]} aria-hidden="true">
  <path class="logo-arch" d="M5 30V14.5A11 11 0 0 1 27 14.5V30H21.6V14.8A5.6 5.6 0 0 0 10.4 14.8V30Z" fill="var(--primary)" />
  <path class="logo-leaf" d="M10.4 30V14.8A5.6 5.6 0 0 1 16 9.2V30Z" fill="var(--depth)" />
  <path class="logo-light" d="M16 9.2A5.6 5.6 0 0 1 21.6 14.8V30H16Z" fill="var(--signal)" />
</svg>
