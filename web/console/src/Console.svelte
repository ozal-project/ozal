<script lang="ts">
  import { tick } from "svelte";
  import { route, navigate, type Page } from "$lib/router.svelte";
  import UserMenu from "$lib/UserMenu.svelte";
  import PasskeysDialog from "$lib/PasskeysDialog.svelte";
  import { dialogs } from "$lib/dialogs.svelte";
  import { admin, can, tools as toolList, type Tool } from "$lib/adminToken.svelte";
  import Connectors from "./pages/Connectors.svelte";
  import Applications from "./pages/Applications.svelte";
  import Overview from "./pages/Overview.svelte";
  import Activity from "./pages/Activity.svelte";
  import Routes from "./pages/Routes.svelte";
  import Sites from "./pages/Sites.svelte";
  import People from "./pages/People.svelte";
  import Administrators from "./pages/Administrators.svelte";
  import Groups from "./pages/Groups.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import MenuIcon from "@lucide/svelte/icons/menu";
  import XIcon from "@lucide/svelte/icons/x";

  // Few entries, grouped by what the product does. Related views of one thing
  // (an application and its routing table; people, their groups and who runs
  // the console) are tabs on one page.
  type Item = { path: Page; label: string; tool?: Tool; also?: Page[] };
  const groups: { name: string; items: Item[] }[] = [
    { name: "", items: [
      { path: "/overview", label: "Overview" },
    ] },
    { name: "Access", items: [
      { path: "/applications", label: "Applications", tool: "applications", also: ["/routes"] },
      { path: "/people", label: "People", tool: "people", also: ["/groups", "/administrators"] },
    ] },
    { name: "Edge", items: [
      { path: "/sites", label: "Sites" },
      { path: "/connectors", label: "Connectors", tool: "connectors" },
      { path: "/activity", label: "Activity" },
    ] },
  ];
  // The navigation lists what this session can open; a direct link to a page
  // outside it still explains why instead of disappearing. Without the
  // applications tool, Applications opens on its read-only routing view; with
  // only the administrators tool, People opens on its Administrators tab.
  const target = (i: Item): Page => {
    if (i.path === "/applications" && !can("applications")) return "/routes";
    if (i.path === "/people" && !can("people") && can("administrators")) return "/administrators";
    return i.path;
  };
  const visible = $derived(groups.map((g) => ({ ...g, items: g.items.filter((i) => !i.tool || can(i.tool) || (i.path === "/applications") || (i.path === "/people" && can("administrators"))) })).filter((g) => g.items.length));
  const isActive = (i: Item) => route.path === i.path || !!i.also?.includes(route.path);
  let menuOpen = $state(false);
  let drawer = $state<HTMLDialogElement | null>(null);
  let menuTrigger = $state<HTMLButtonElement | null>(null);
  let drawerPage: Page = route.path;
  const purpose: Partial<Record<Page, string>> = {
    "/overview": "What needs you, and what changed",
    "/applications": "Destinations and who may enter",
    "/routes": "Destinations and who may enter",
    "/people": "People, groups and administrators",
    "/administrators": "People, groups and administrators",
    "/sites": "Published versions and CI",
    "/connectors": "Trusted connections to private hosts",
    "/activity": "Access decisions and their reasons",
  };
  $effect(() => {
    if (!drawer) return;
    if (menuOpen && !drawer.open) {
      drawerPage = route.path;
      drawer.showModal();
      requestAnimationFrame(() => drawer?.querySelector<HTMLElement>('[aria-current="page"]')?.focus());
    } else if (!menuOpen && drawer.open) drawer.close();
  });
  function drawerClosed() {
    menuOpen = false;
    requestAnimationFrame(() => {
      if (route.path === drawerPage) menuTrigger?.focus();
      else document.querySelector<HTMLElement>("#main-content h1")?.focus();
    });
  }
  function containDrawerTab(event: KeyboardEvent) {
    if (event.key !== "Tab" || !drawer) return;
    const controls = [...drawer.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex="-1"])')].filter((element) => element.getClientRects().length > 0);
    if (!controls.length) return;
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
  const nav: Item[] = [...groups.flatMap((g) => g.items), { path: "/groups", label: "Groups", tool: "people" },
    { path: "/administrators", label: "Administrators", tool: "administrators" }, { path: "/routes", label: "Applications", tool: "applications" }];
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
    document.title = `${title} · Ozal`;
    if (mounted) void tick().then(() => {
      document.querySelector<HTMLElement>('nav[aria-label="Console"] [aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "nearest" });
      document.querySelector<HTMLElement>("#main-content h1")?.focus();
    });
    mounted = true;
    void search;
  });
</script>

{#snippet link(item: Item, expanded = false)}
  {@const active = isActive(item)}
  <a href={target(item)} onclick={(e) => go(e, target(item))} aria-current={active ? "page" : undefined}
    class={expanded ? "console-drawer-link" : "console-nav-link"}>
    <span>{item.label}</span>
    {#if expanded}<span class="console-nav-purpose">{purpose[target(item)]}</span>{/if}
  </a>
{/snippet}

{#if admin.passkey}<PasskeysDialog bind:open={dialogs.passkeys} />{/if}

<div class="console-workspace">
  <a href="#main-content" class="sr-only focus:not-sr-only focus:bg-background focus:text-foreground focus:ring-ring fixed top-2 left-2 z-50 rounded-md px-3 py-2 text-sm focus:ring-2">Skip to content</a>
  <header class="console-header">
    <div class="console-bar">
      <a href="/overview" onclick={(e) => go(e, "/overview")} class="console-brand" aria-label="Ozal console home"><Wordmark sub="Private console" /></a>
      <nav class="console-nav" aria-label="Console">
        {#each visible as group (group.name)}
          {#each group.items as item (item.path)}{@render link(item)}{/each}
        {/each}
      </nav>
      <div class="console-account"><UserMenu /></div>
      <span class="console-mobile-place" aria-hidden="true">{currentItem.label}</span>
      <button bind:this={menuTrigger} type="button" class="console-menu-toggle" aria-expanded={menuOpen} aria-controls="mobile-menu" aria-label="Open menu" onclick={() => menuOpen = true}>
        <MenuIcon class="size-5" />
      </button>
    </div>
  </header>

  <dialog bind:this={drawer} id="mobile-menu" aria-label="Console navigation" class="console-drawer" onclose={drawerClosed} onkeydown={containDrawerTab}>
    {#if menuOpen}
      <div class="console-drawer-header"><Wordmark sub="Private console" /><button type="button" class="console-menu-toggle" aria-label="Close menu" onclick={() => menuOpen = false}><XIcon class="size-5" /></button></div>
      <nav aria-label="Console">
        {#each visible as group (group.name)}
          {#each group.items as item (item.path)}{@render link(item, true)}{/each}
        {/each}
      </nav>
      <div class="console-drawer-account"><UserMenu placement="up" /></div>
    {/if}
  </dialog>

  <main id="main-content" tabindex="-1" class="console-content outline-none">
    {#key route.path}<div class="page-enter">
    {#if blocked}
      <div class="panel mx-auto max-w-lg p-8 text-center">
        <p class="plate">Not included in this session</p>
        <h1 tabindex="-1" class="mt-2 text-xl font-semibold outline-none">This session cannot open {currentItem.label}</h1>
        <p class="text-muted-foreground mt-2 text-sm">You are signed in as {admin.name || "an operator"} without the {toolList.find((t) => t.id === currentItem.tool)?.label} tool. Your name in the account menu lists what this session may change; ask an administrator to include it.</p>
      </div>
    {:else if route.path === "/overview"}<Overview />
    {:else if route.path === "/applications"}<Applications />
    {:else if route.path === "/connectors"}<Connectors />
    {:else if route.path === "/activity"}<Activity />
    {:else if route.path === "/routes"}<Routes />
    {:else if route.path === "/people"}<People />
    {:else if route.path === "/groups"}<Groups />
    {:else if route.path === "/administrators"}<Administrators />
    {:else if route.path === "/sites"}<Sites />
    {:else}<Overview />{/if}
    </div>{/key}
  </main>
</div>
