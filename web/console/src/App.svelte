<script lang="ts">
  import { route, navigate, type Page } from "$lib/router.svelte";
  import Activity from "./pages/Activity.svelte";
  import Routes from "./pages/Routes.svelte";
  import Simulator from "./pages/Simulator.svelte";
  import Sites from "./pages/Sites.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import BugIcon from "@lucide/svelte/icons/bug";

  const nav: { path: Page; jack: string; label: string; hint: string }[] = [
    { path: "/activity", jack: "01", label: "Activity", hint: "Live access decisions" },
    { path: "/routes", jack: "02", label: "Routes", hint: "Hostnames, health, policy" },
    { path: "/sites", jack: "03", label: "Sites", hint: "Deployments and rollback" },
    { path: "/simulator", jack: "04", label: "Policy simulator", hint: "Ask the engine why" },
  ];

  const go = (e: MouseEvent, path: Page) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    navigate(path);
  };
</script>

<div class="flex min-h-screen">
  <div class="bg-sidebar border-sidebar-border hidden w-[17rem] shrink-0 border-r md:block">
  <aside class="sticky top-0 flex h-screen flex-col">
    <a href="/activity" onclick={(e) => go(e, "/activity")} class="px-5 pt-6 pb-7" aria-label="Switchboard console home"><Wordmark /></a>

    <p class="plate px-5 pb-2">Lines</p>
    <nav class="flex flex-col gap-0.5 px-3" aria-label="Console">
      {#each nav as item (item.path)}
        {@const active = route.path === item.path}
        <a
          href={item.path}
          onclick={(e) => go(e, item.path)}
          aria-current={active ? "page" : undefined}
          class={["group relative flex items-center gap-3 rounded-lg px-3 py-2.5 transition-colors",
            active ? "bg-sidebar-accent" : "hover:bg-sidebar-accent/50"]}
        >
          <span class={["grid size-8 shrink-0 place-items-center rounded-full border font-mono text-[0.65rem]",
            active ? "border-primary/70 text-primary" : "border-sidebar-border text-muted-foreground"]}>{item.jack}</span>
          <span class="min-w-0 flex-1">
            <span class={["block text-sm font-medium", active && "text-sidebar-accent-foreground"]}>{item.label}</span>
            <span class="text-muted-foreground block text-xs">{item.hint}</span>
          </span>
          <span class={["lamp", active ? "lamp-wait" : "lamp-idle"]} aria-hidden="true"></span>
        </a>
      {/each}
    </nav>

    <div class="mt-auto space-y-4 p-5">
      <a href="/_/status" class="text-muted-foreground hover:text-foreground flex items-center gap-2 text-xs"><BugIcon class="size-3.5" /> Route debug · works without JavaScript</a>
      <div class="border-sidebar-border rounded-lg border border-dashed p-3.5">
        <p class="plate mb-1.5">Trust plane</p>
        <p class="text-muted-foreground text-xs leading-relaxed">Identity, policy and access decisions. Host processes and backups live in Server Ops.</p>
      </div>
    </div>
  </aside>
  </div>

  <div class="jack-field flex min-w-0 flex-1 flex-col">
    <header class="bg-background/80 border-border sticky top-0 z-10 flex items-center justify-between gap-4 border-b px-5 py-3 backdrop-blur md:px-10">
      <div class="md:hidden"><Wordmark /></div>
      <p class="plate hidden md:block">Local administrative console</p>
      <span class="border-border bg-card flex items-center gap-2 rounded-full border px-3 py-1 font-mono text-[0.7rem]">
        <span class="lamp lamp-on" aria-hidden="true"></span>loopback only
      </span>
    </header>
    <nav class="border-border flex gap-1 overflow-x-auto border-b px-4 py-2 md:hidden" aria-label="Console (compact)">
      {#each nav as item (item.path)}
        <a href={item.path} onclick={(e) => go(e, item.path)} aria-current={route.path === item.path ? "page" : undefined}
          class="rounded-md px-2.5 py-1.5 text-sm whitespace-nowrap aria-[current=page]:bg-accent aria-[current=page]:text-accent-foreground">{item.label}</a>
      {/each}
    </nav>
    <main class="mx-auto w-full max-w-[1320px] flex-1 px-5 py-10 md:px-10">
      {#if route.path === "/activity"}<Activity />
      {:else if route.path === "/routes"}<Routes />
      {:else if route.path === "/sites"}<Sites />
      {:else}<Simulator />{/if}
    </main>
  </div>
</div>
