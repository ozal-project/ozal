<script lang="ts">
  import { tick } from "svelte";
  import { route, navigate, type Page } from "$lib/router.svelte";
  import UserMenu from "$lib/UserMenu.svelte";
  import { admin, can, tools as toolList, type Tool } from "$lib/adminToken.svelte";
  import Connectors from "./pages/Connectors.svelte";
  import Applications from "./pages/Applications.svelte";
  import Overview from "./pages/Overview.svelte";
  import Activity from "./pages/Activity.svelte";
  import Routes from "./pages/Routes.svelte";
  import Sites from "./pages/Sites.svelte";
  import People from "./pages/People.svelte";
  import Administrators from "./pages/Administrators.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import LayoutDashboardIcon from "@lucide/svelte/icons/layout-dashboard";
  import AppWindowIcon from "@lucide/svelte/icons/app-window";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import UsersIcon from "@lucide/svelte/icons/users";
  import ShieldCheckIcon from "@lucide/svelte/icons/shield-check";
  import PlugIcon from "@lucide/svelte/icons/plug";
  import ScrollTextIcon from "@lucide/svelte/icons/scroll-text";
  import RouteIcon from "@lucide/svelte/icons/route";
  import MenuIcon from "@lucide/svelte/icons/menu";
  import XIcon from "@lucide/svelte/icons/x";
  import type { Component } from "svelte";

  // Few entries, grouped by what the product does. Related views of one thing
  // (an application and its routing table) are tabs on its page, and the
  // console's own settings sit apart, next to the person signed in.
  type Item = { path: Page; label: string; icon: Component<{ class?: string }>; tool?: Tool; also?: Page[] };
  const groups: { name: string; items: Item[] }[] = [
    { name: "", items: [
      { path: "/overview", label: "Overview", icon: LayoutDashboardIcon },
    ] },
    { name: "Access", items: [
      { path: "/applications", label: "Applications", icon: AppWindowIcon, tool: "applications", also: ["/routes"] },
      { path: "/people", label: "People", icon: UsersIcon, tool: "people" },
    ] },
    { name: "Edge", items: [
      { path: "/sites", label: "Sites", icon: GlobeIcon },
      { path: "/connectors", label: "Connectors", icon: PlugIcon, tool: "connectors" },
      { path: "/activity", label: "Activity", icon: ScrollTextIcon },
    ] },
  ];
  const settings: Item = { path: "/administrators", label: "Administrators", icon: ShieldCheckIcon, tool: "administrators" };
  // The navigation lists what this session can open; a direct link to a page
  // outside it still explains why instead of disappearing. Without the
  // applications tool, Applications opens on its read-only routing view.
  const target = (i: Item): Page => (i.path === "/applications" && !can("applications") ? "/routes" : i.path);
  const visible = $derived(groups.map((g) => ({ ...g, items: g.items.filter((i) => !i.tool || can(i.tool) || !!i.also) })).filter((g) => g.items.length));
  const isActive = (i: Item) => route.path === i.path || !!i.also?.includes(route.path);
  let menuOpen = $state(false);
  const nav = [...groups.flatMap((g) => g.items), settings, { path: "/routes" as Page, label: "Applications", icon: RouteIcon, tool: "applications" as Tool }];
  const currentItem = $derived(nav.find((i) => i.path === route.path) ?? nav[0]);
  // A signed-in session without the page's tool sees why, not a page of 403s.
  const blocked = $derived(route.path !== "/routes" && !!currentItem.tool && !can(currentItem.tool));

  const go = (e: MouseEvent, path: Page) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    menuOpen = false;
    navigate(path);
  };

  let mounted = false;
  $effect(() => {
    const path = route.path;
    const search = route.search;
    const title = nav.find((item) => item.path === path)?.label ?? "Overview";
    document.title = `${title} · Switchboard`;
    if (mounted) void tick().then(() => {
      document.querySelector<HTMLElement>('nav[aria-label="Console"] [aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "nearest" });
      document.querySelector<HTMLElement>("#main-content h1")?.focus();
    });
    mounted = true;
    void search;
  });
</script>

{#snippet link(item: Item)}
  {@const active = isActive(item)}
  <a href={target(item)} onclick={(e) => go(e, target(item))} aria-current={active ? "page" : undefined}
    class={["nav-item group flex items-center gap-3 rounded-md px-2.5 py-1.5 text-sm transition-colors",
      active ? "bg-sidebar-accent text-foreground" : "text-muted-foreground hover:text-foreground"]}
  ><item.icon class={`size-4 shrink-0 transition-colors ${active ? "text-primary" : "opacity-60 group-hover:opacity-100"}`} />{item.label}</a>
{/snippet}

{#snippet sidebar(drawer = false)}
  {#if !drawer}<a href="/overview" onclick={(e) => go(e, "/overview")} class="flex h-14 shrink-0 items-center px-4" aria-label="Switchboard console home"><Wordmark animated /></a>{/if}
  <nav class="mt-2 flex flex-col gap-5 px-2" aria-label="Console">
    {#each visible as group (group.name)}
      <div class="flex flex-col gap-0.5">
        {#if group.name}<p class="text-muted-foreground px-2.5 pb-1 text-xs">{group.name}</p>{/if}
        {#each group.items as item (item.path)}{@render link(item)}{/each}
      </div>
    {/each}
  </nav>
  <div class="mt-auto flex flex-col gap-1 p-2">
    {#if can("administrators")}<nav class="flex flex-col" aria-label="Console settings">{@render link(settings)}</nav>{/if}
    <div class="border-sidebar-border mt-1 border-t pt-2"><UserMenu placement="up" /></div>
  </div>
{/snippet}

<div class="flex min-h-screen">
  <a href="#main-content" class="sr-only focus:not-sr-only focus:bg-background focus:text-foreground focus:ring-ring fixed top-2 left-2 z-50 rounded-md px-3 py-2 text-sm focus:ring-2">Skip to content</a>
  <div class="bg-sidebar border-sidebar-border relative z-30 hidden w-60 shrink-0 border-r md:block">
    <aside class="sticky top-0 flex h-screen flex-col">{@render sidebar()}</aside>
  </div>

  <div class="flex min-w-0 flex-1 flex-col">
    <header class="bg-background/85 border-border sticky top-0 z-20 flex h-14 items-center justify-between border-b px-4 backdrop-blur md:hidden">
      <Wordmark animated />
      <button type="button" class="hover:bg-accent rounded-md p-2" aria-expanded={menuOpen} aria-controls="mobile-menu" aria-label={menuOpen ? "Close menu" : "Open menu"} onclick={() => (menuOpen = !menuOpen)}>
        {#if menuOpen}<XIcon class="size-5" />{:else}<MenuIcon class="size-5" />{/if}
      </button>
    </header>
    {#if menuOpen}
      <div id="mobile-menu" class="bg-sidebar animate-in fade-in-0 slide-in-from-top-2 fixed inset-x-0 top-14 bottom-0 z-20 flex flex-col overflow-y-auto duration-200 md:hidden">{@render sidebar(true)}</div>
    {/if}
    <main id="main-content" tabindex="-1" class="mx-auto w-full max-w-[1200px] flex-1 px-5 py-8 outline-none md:px-12 md:py-12">
      {#key route.path}<div class="page-enter">
      {#if blocked}
        <div class="panel mx-auto max-w-lg p-8 text-center">
          <p class="plate">Not included in this session</p>
          <h1 tabindex="-1" class="mt-2 text-xl font-semibold outline-none">This session cannot open {currentItem.label}</h1>
          <p class="text-muted-foreground mt-2 text-sm">You are signed in as {admin.name || "an operator"} without the {toolList.find((t) => t.id === currentItem.tool)?.label} tool. Your name at the bottom of the menu lists what this session may change; ask an administrator to include it.</p>
        </div>
      {:else if route.path === "/overview"}<Overview />
      {:else if route.path === "/applications"}<Applications />
      {:else if route.path === "/connectors"}<Connectors />
      {:else if route.path === "/activity"}<Activity />
      {:else if route.path === "/routes"}<Routes />
      {:else if route.path === "/people"}<People />
      {:else if route.path === "/administrators"}<Administrators />
      {:else if route.path === "/sites"}<Sites />
      {:else}<Overview />{/if}
      </div>{/key}
    </main>
  </div>
</div>
