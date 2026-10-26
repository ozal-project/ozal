<script lang="ts">
  import { tick } from "svelte";
  import { route, navigate, type Page } from "$lib/router.svelte";
  import AdminToken from "$lib/AdminToken.svelte";
  import Connectors from "./pages/Connectors.svelte";
  import Projects from "./pages/Projects.svelte";
  import Applications from "./pages/Applications.svelte";
  import Overview from "./pages/Overview.svelte";
  import Activity from "./pages/Activity.svelte";
  import Routes from "./pages/Routes.svelte";
  import Simulator from "./pages/Simulator.svelte";
  import Sites from "./pages/Sites.svelte";
  import People from "./pages/People.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import BugIcon from "@lucide/svelte/icons/bug";

  // Operate is for whoever runs the edge day to day; Build is for the person
  // who writes its configuration and policy (UI-005). Same pages, plain names.
  const groups: { name: string; items: { path: Page; label: string }[] }[] = [
    { name: "Operate", items: [
      { path: "/overview", label: "Overview" },
      { path: "/applications", label: "Applications" },
      { path: "/sites", label: "Sites" },
      { path: "/people", label: "People" },
      { path: "/connectors", label: "Connectors" },
      { path: "/activity", label: "Access log" },
    ] },
    { name: "Build", items: [
      { path: "/projects", label: "CI projects" },
      { path: "/routes", label: "Routes & policy" },
      { path: "/simulator", label: "Why? Simulator" },
    ] },
  ];
  const nav = groups.flatMap((g) => g.items);

  const go = (e: MouseEvent, path: Page) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    navigate(path);
  };

  let mounted = false;
  $effect(() => {
    const path = route.path;
    const search = route.search;
    const title = nav.find((item) => item.path === path)?.label ?? "Overview";
    document.title = `${title} · Switchboard`;
    if (mounted) void tick().then(() => {
      document.querySelector<HTMLElement>('nav[aria-label="Console sections"] [aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "nearest" });
      document.querySelector<HTMLElement>("#main-content h1")?.focus();
    });
    mounted = true;
    void search;
  });
</script>

<div class="flex min-h-screen">
  <a href="#main-content" class="sr-only focus:not-sr-only focus:bg-background focus:text-foreground focus:ring-ring fixed top-2 left-2 z-50 rounded-md px-3 py-2 text-sm focus:ring-2">Skip to content</a>
  <div class="bg-sidebar border-sidebar-border hidden w-[17rem] shrink-0 border-r md:block">
  <aside class="sticky top-0 flex h-screen flex-col">
    <a href="/overview" onclick={(e) => go(e, "/overview")} class="px-5 pt-6 pb-6" aria-label="Switchboard console home"><Wordmark /></a>

    <nav class="flex flex-col gap-5 px-3" aria-label="Console">
      {#each groups as group (group.name)}
        <div>
          <p class="plate px-3 pb-1.5">{group.name}</p>
          <div class="flex flex-col gap-0.5">
            {#each group.items as item (item.path)}
              {@const active = route.path === item.path}
              <a
                href={item.path}
                onclick={(e) => go(e, item.path)}
                aria-current={active ? "page" : undefined}
                class={["flex items-center rounded-lg px-3 py-2 text-sm transition-colors",
                  active ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium shadow-[inset_2px_0_0_var(--primary)]" : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"]}
              >{item.label}</a>
            {/each}
          </div>
        </div>
      {/each}
    </nav>

    <div class="mt-auto space-y-4 p-5">
      <a href="/_/status" class="text-muted-foreground hover:text-foreground flex items-center gap-2 text-xs"><BugIcon class="size-3.5" /> Route debug · works without JavaScript</a>
      <p class="text-muted-foreground border-sidebar-border border-t pt-3 text-xs leading-relaxed">Who gets in, and why. Processes and backups live in Server Ops.</p>
    </div>
  </aside>
  </div>

  <div class="flex min-w-0 flex-1 flex-col">
    <header class="bg-background/80 border-border sticky top-0 z-10 flex flex-wrap items-center justify-between gap-4 border-b px-5 py-3 backdrop-blur md:px-10">
      <div class="md:hidden"><Wordmark /></div>
      <p class="plate hidden md:block">Local console</p>
      <div class="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-3">
        <AdminToken />
        <span class="border-border bg-card flex items-center gap-2 rounded-full border px-3 py-1 font-mono text-[0.7rem]">
          <span class="lamp lamp-on" aria-hidden="true"></span>loopback only
        </span>
      </div>
    </header>
    <nav class="border-border flex gap-1 overflow-x-auto border-b px-4 py-2 md:hidden" aria-label="Console sections">
      {#each nav as item (item.path)}
        <a href={item.path} onclick={(e) => go(e, item.path)} aria-current={route.path === item.path ? "page" : undefined}
          class="rounded-md px-2.5 py-1.5 text-sm whitespace-nowrap aria-[current=page]:bg-accent aria-[current=page]:text-accent-foreground">{item.label}</a>
      {/each}
    </nav>
    <main id="main-content" tabindex="-1" class="mx-auto w-full max-w-[1320px] flex-1 px-5 py-10 md:px-10">
      {#if route.path === "/overview"}<Overview />
      {:else if route.path === "/applications"}<Applications />
      {:else if route.path === "/projects"}<Projects />
      {:else if route.path === "/connectors"}<Connectors />
      {:else if route.path === "/activity"}<Activity />
      {:else if route.path === "/routes"}<Routes />
      {:else if route.path === "/people"}<People />
      {:else if route.path === "/sites"}<Sites />
      {:else}<Simulator />{/if}
    </main>
  </div>
</div>
