<script lang="ts">
  import type { RouteStatus } from "$lib/api";
  import { navigate } from "$lib/router.svelte";
  import Logo from "./Logo.svelte";
  import ArrowIcon from "@lucide/svelte/icons/arrow-right";

  let { routes, allowed, refused }: { routes: RouteStatus[]; allowed: number; refused: number } = $props();
  const shown = $derived(routes.slice(0, 8));
  const more = $derived(routes.length - shown.length);
  const name = (host: string) => host.replace(/:\d+$/, "");
  function open(event: MouseEvent, host: string) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault(); navigate("/routes", { host });
  }
</script>

<section class="panel" aria-label="Your edge right now">
  <div class="panel-head">
    <div><h2 class="text-sm font-medium">Your edge right now</h2><p class="text-muted-foreground mt-1 text-xs">{allowed} let in · {refused} refused, in the retained events</p></div>
  </div>
  <div class="edge-journey">
    <div class="edge-threshold" aria-label="Visitors pass through Ozal">
      <span class="text-muted-foreground text-xs">Visitors</span><ArrowIcon class="edge-direction size-4" aria-hidden="true" />
      <div class="edge-threshold-mark"><Logo size={40} /><span class="mt-2 text-sm font-medium">Ozal</span><span class="text-muted-foreground mt-1 text-xs">Access decisions</span></div>
    </div>
    <ul class="edge-paths">
      {#each shown as route (route.host)}
        <li><a class="edge-route" href={`/routes?host=${encodeURIComponent(route.host)}`} onclick={(event) => open(event, route.host)}>
          <span class={["lamp", route.state_class === "unhealthy" ? "lamp-off" : route.state_class === "healthy" ? "edge-live" : "lamp-idle"]} aria-hidden="true"></span>
          <span class="min-w-0 truncate text-xs" title={route.host}>{name(route.host)}</span>
          <span class="text-muted-foreground text-xs">{route.state_class === "unhealthy" ? "not answering" : route.is_static ? "site" : route.access === "Protected" ? "sign-in" : "public"}</span>
        </a></li>
      {:else}<li class="text-muted-foreground py-4 text-sm">No application routes configured.</li>{/each}
      {#if more > 0}<li class="text-muted-foreground pt-3 text-xs">{more} more in Applications</li>{/if}
    </ul>
  </div>
</section>
