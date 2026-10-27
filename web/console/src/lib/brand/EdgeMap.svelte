<script lang="ts">
  import type { RouteStatus } from "$lib/api";
  import { navigate } from "$lib/router.svelte";

  // The edge at a glance: visitors reach Switchboard, Switchboard decides and
  // patches each request through to an application. A healthy cord carries a
  // signal; a broken one is red and still.
  let { routes, allowed, refused }: { routes: RouteStatus[]; allowed: number; refused: number } = $props();

  const MAX = 8;
  const ROW = 46;
  const shown = $derived(routes.slice(0, MAX));
  const more = $derived(routes.length - shown.length);
  const rows = $derived(Math.max(shown.length + (more > 0 ? 1 : 0), 3));
  const height = $derived(rows * ROW + 24);
  const mid = $derived(height / 2);
  const rowY = (i: number) => 12 + ROW / 2 + i * ROW;
  const name = (host: string) => host.replace(/:\d+$/, "");
  const cord = (y: number) => `M372 ${mid} C470 ${mid}, 470 ${y}, 566 ${y}`;
  const open = (e: MouseEvent, host: string) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    navigate("/routes", { host });
  };
  const access = (r: RouteStatus) => (r.access === "Protected" ? "sign-in" : "public");
</script>

<div class="panel mb-8 overflow-hidden">
  <div class="flex flex-wrap items-baseline justify-between gap-2 px-5 pt-4">
    <h2 class="text-sm font-medium">Your edge right now</h2>
    <p class="text-muted-foreground text-xs">{allowed} let in · {refused} refused, in the retained events</p>
  </div>

  <svg viewBox={`0 0 900 ${height}`} class="edgemap hidden w-full sm:block" role="group" aria-label={`Visitors reach Switchboard, which serves ${routes.length} applications`}>
    <path d={`M120 ${mid} L288 ${mid}`} class="em-cord" />
    <path d={`M120 ${mid} L288 ${mid}`} class="em-signal em-d0" pathLength="1" />
    <g transform={`translate(70 ${mid})`}>
      <circle r="22" class="em-node" />
      <circle r="5" cy="-6" class="em-glyph" /><path d="M-10 12 C-10 2, 10 2, 10 12" class="em-glyph-line" />
      <text y="42" text-anchor="middle" class="em-label">Visitors</text>
    </g>
    <g transform={`translate(330 ${mid})`}>
      <rect x="-42" y="-26" width="84" height="52" rx="14" class="em-hub" />
      <circle cx="-14" cy="-7" r="4.5" class="em-dot" />
      <circle cx="14" cy="8" r="4.5" class="em-dot-primary" />
      <path d="M-14 -2 C-14 10, 14 -4, 14 3" class="em-glyph-line" />
      <text y="46" text-anchor="middle" class="em-label">Switchboard</text>
    </g>
    {#each shown as r, i (r.host)}
      {@const y = rowY(i)}
      {@const bad = r.state_class === "unhealthy"}
      <path d={cord(y)} class={["em-cord", bad && "em-cord-bad"]} />
      {#if !bad}<path d={cord(y)} class={`em-signal em-d${(i % 8) + 1}`} pathLength="1" />{/if}
      <a href={`/routes?host=${encodeURIComponent(r.host)}`} onclick={(e) => open(e, r.host)} class="em-row">
        <rect x="566" y={y - 17} width="320" height="34" rx="9" class="em-row-bg" />
        <circle cx="584" cy={y} r="4" class={bad ? "em-lamp-bad" : r.state_class === "healthy" ? "em-lamp-ok" : "em-lamp-idle"} />
        <text x="598" y={y + 4} class="em-host">{name(r.host)}</text>
        <text x="874" y={y + 4} text-anchor="end" class="em-meta">{bad ? "not answering" : r.is_static ? "site" : access(r)}</text>
      </a>
    {/each}
    {#if more > 0}<text x="598" y={rowY(shown.length) + 4} class="em-meta">and {more} more in Applications</text>{/if}
  </svg>

  <ul class="divide-border divide-y sm:hidden">
    {#each routes as r (r.host)}
      <li class="flex items-center gap-3 px-5 py-2.5 text-sm">
        <span class={["lamp", r.state_class === "unhealthy" ? "lamp-off" : r.state_class === "healthy" ? "lamp-on" : "lamp-idle"]} aria-hidden="true"></span>
        <span class="min-w-0 flex-1 truncate font-mono text-xs">{name(r.host)}</span>
        <span class="text-muted-foreground text-xs">{r.state_class === "unhealthy" ? "not answering" : r.is_static ? "site" : access(r)}</span>
      </li>
    {/each}
  </ul>
</div>
