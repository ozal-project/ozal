<script lang="ts">
  import { tick } from "svelte";
  import { route, navigate, type Page } from "$lib/router.svelte";
  import AdminMenu from "$lib/AdminMenu.svelte";
  import SignIn from "$lib/SignIn.svelte";
  import { admin, can, tools as toolList, type Tool } from "$lib/adminToken.svelte";
  import Connectors from "./pages/Connectors.svelte";
  import Applications from "./pages/Applications.svelte";
  import Overview from "./pages/Overview.svelte";
  import Activity from "./pages/Activity.svelte";
  import Routes from "./pages/Routes.svelte";
  import Sites from "./pages/Sites.svelte";
  import People from "./pages/People.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import LayoutDashboardIcon from "@lucide/svelte/icons/layout-dashboard";
  import AppWindowIcon from "@lucide/svelte/icons/app-window";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import UsersIcon from "@lucide/svelte/icons/users";
  import PlugIcon from "@lucide/svelte/icons/plug";
  import ScrollTextIcon from "@lucide/svelte/icons/scroll-text";
  import RouteIcon from "@lucide/svelte/icons/route";
  import type { Component } from "svelte";

  // Operate is for whoever runs the edge day to day; Build is for the person
  // who writes its configuration and policy (UI-005). Same pages, plain names.
  const groups: { name: string; items: { path: Page; label: string; icon: Component<{ class?: string }>; tool?: Tool }[] }[] = [
    { name: "Operate", items: [
      { path: "/overview", label: "Overview", icon: LayoutDashboardIcon },
      { path: "/applications", label: "Applications", icon: AppWindowIcon, tool: "applications" },
      { path: "/sites", label: "Sites", icon: GlobeIcon },
      { path: "/people", label: "People", icon: UsersIcon, tool: "people" },
      { path: "/connectors", label: "Connectors", icon: PlugIcon, tool: "connectors" },
      { path: "/activity", label: "Access log", icon: ScrollTextIcon },
    ] },
    { name: "Build", items: [
      { path: "/routes", label: "Routes & policy", icon: RouteIcon },
    ] },
  ];
  const nav = groups.flatMap((g) => g.items);
  const current = $derived(groups.find((g) => g.items.some((i) => i.path === route.path)) ?? groups[0]);
  const currentItem = $derived(nav.find((i) => i.path === route.path) ?? nav[0]);
  // A signed-in session without the page's tool sees why, not a page of 403s.
  const blocked = $derived(!!admin.token && !!currentItem.tool && !can(currentItem.tool));

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
  <aside class="sticky top-0 flex h-screen flex-col overflow-y-auto">
    <a href="/overview" onclick={(e) => go(e, "/overview")} class="border-sidebar-border mb-4 flex h-16 shrink-0 items-center border-b px-5" aria-label="Switchboard console home"><Wordmark /></a>

    <nav class="flex flex-col gap-6 px-3" aria-label="Console">
      {#each groups as group (group.name)}
        <div>
          <p class="plate px-3 pb-2 text-xs">{group.name}</p>
          <div class="flex flex-col gap-0.5">
            {#each group.items as item (item.path)}
              {@const active = route.path === item.path}
              <a
                href={item.path}
                onclick={(e) => go(e, item.path)}
                aria-current={active ? "page" : undefined}
                class={["group flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors",
                  active ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium" : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"]}
              ><item.icon class={`size-4 shrink-0 transition-colors ${active ? "text-primary" : "opacity-70 group-hover:opacity-100"}`} />{item.label}</a>
            {/each}
          </div>
        </div>
      {/each}
    </nav>

    <p class="text-muted-foreground mt-auto p-5 text-xs leading-relaxed">Who gets in, and why.</p>
  </aside>
  </div>

  <div class="flex min-w-0 flex-1 flex-col">
    <header class="bg-background/80 border-border sticky top-0 z-10 flex min-h-16 flex-wrap items-center justify-between gap-4 border-b px-5 py-3 backdrop-blur md:px-10">
      <div class="md:hidden"><Wordmark /></div>
      <p class="hidden items-center gap-2 text-sm md:flex" aria-label="Current page">
        <span class="text-muted-foreground">{current.name}</span><span class="text-muted-foreground/50" aria-hidden="true">/</span><span class="font-medium">{currentItem.label}</span>
      </p>
      <div class="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-3">
        <AdminMenu />
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
      {#if !admin.token}
        {#if admin.ready}
          <div class="panel mx-auto max-w-lg p-8">
            <p class="plate">Operators</p>
            <h1 tabindex="-1" class="mt-2 mb-1 text-xl font-semibold outline-none">Sign in to open the console</h1>
            <p class="text-muted-foreground mb-5 text-sm">Routes, people, sites and activity are only shown to a signed-in operator, even on this machine. The host chooses what each login may change: pick the tools, run the command on the host and open the one-use link it prints.</p>
            <SignIn />
          </div>
        {/if}
      {:else if blocked}
        <div class="panel mx-auto max-w-lg p-8 text-center">
          <p class="plate">Not included in this session</p>
          <h1 tabindex="-1" class="mt-2 text-xl font-semibold outline-none">This session cannot open {currentItem.label}</h1>
          <p class="text-muted-foreground mt-2 text-sm">You are signed in as {admin.name || "an operator"} without the {toolList.find((t) => t.id === currentItem.tool)?.label} tool. Open Admin in the header to see what it may change, then ask the host for a login that includes it.</p>
        </div>
      {:else if route.path === "/overview"}<Overview />
      {:else if route.path === "/applications"}<Applications />
      {:else if route.path === "/connectors"}<Connectors />
      {:else if route.path === "/activity"}<Activity />
      {:else if route.path === "/routes"}<Routes />
      {:else if route.path === "/people"}<People />
      {:else if route.path === "/sites"}<Sites />
      {:else}<Overview />{/if}
    </main>
  </div>
</div>
