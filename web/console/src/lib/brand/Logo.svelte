<script lang="ts">
  // Umbral: a doorway, and the light of the open door. Ivory arch, camel
  // light, graphite leaf. Animated, the arch rises, the leaf swings open on
  // its hinge and a beam of light passes through the doorway; it comes to
  // rest on the same mark as the static one.
  let { size = 28, animated = false, class: className = "" }: { size?: number; animated?: boolean; class?: string } = $props();
  // Several marks share a page: their clip and gradient ids must not collide.
  const uid = $props.id();
  const doorway = "M10.4 30V14.8A5.6 5.6 0 0 1 21.6 14.8V30Z";
</script>

<svg width={size} height={size} viewBox="0 0 32 32" class={["logo", animated && "logo-live", className]} aria-hidden="true">
  <defs>
    <clipPath id="{uid}-door"><path d={doorway} /></clipPath>
    <linearGradient id="{uid}-beam" x1="0" x2="1" y1="0" y2="0">
      <stop offset="0" stop-color="var(--primary)" stop-opacity="0" />
      <stop offset="0.5" stop-color="var(--primary)" stop-opacity="0.8" />
      <stop offset="1" stop-color="var(--primary)" stop-opacity="0" />
    </linearGradient>
  </defs>
  <g clip-path="url(#{uid}-door)">
    <path class="logo-light" d={doorway} fill="var(--signal)" />
    <rect class="logo-beam" x="4" y="8" width="5" height="24" fill="url(#{uid}-beam)" opacity="0" />
    <path class="logo-leaf" d="M10.4 30V14.8A5.6 5.6 0 0 1 16 9.2V30Z" fill="var(--depth)" />
  </g>
  <path class="logo-arch" d="M5 30V14.5A11 11 0 0 1 27 14.5V30H21.6V14.8A5.6 5.6 0 0 0 10.4 14.8V30Z" fill="var(--primary)" />
</svg>
