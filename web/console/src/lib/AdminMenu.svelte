<script lang="ts">
  import { onMount, tick } from "svelte";
  import { admin, tools, signOut } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import SignIn from "$lib/SignIn.svelte";
  import { exchangeHostLogin } from "$lib/operatorLogin";
  import ShieldIcon from "@lucide/svelte/icons/shield-check";
  import LockIcon from "@lucide/svelte/icons/lock";
  import CheckIcon from "@lucide/svelte/icons/check";
  import MinusIcon from "@lucide/svelte/icons/minus";
  import ChevronIcon from "@lucide/svelte/icons/chevron-down";

  // The host login carries its granted tools. Pasted credentials are checked
  // by the server before the menu describes their scope.
  let open = $state(false);
  let root = $state<HTMLElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  let seen = admin.asked;

  onMount(() => {
    void exchangeHostLogin().then((session) => {
      if (!session) return;
      admin.token = session.token; admin.name = session.name; admin.expires = session.expires; admin.tools = session.tools ?? []; admin.notice = "";
    }).catch((e) => { admin.notice = (e as Error).message; }).finally(() => { admin.ready = true; });
    const timer = setInterval(() => {
      if (admin.expires && Date.parse(admin.expires) <= Date.now()) { signOut(); admin.notice = "Your operator session expired. Ask the host for a new login."; }
    }, 5_000);
    return () => clearInterval(timer);
  });
  $effect(() => {
    if (admin.asked !== seen) { seen = admin.asked; if (!admin.token) void show(); }
  });
  async function show() { open = true; await tick(); }
  function close(returnFocus = false) { open = false; if (returnFocus) trigger?.focus(); }
  function outside(e: MouseEvent) { if (open && root && !root.contains(e.target as Node)) close(); }
  function keys(e: KeyboardEvent) { if (open && e.key === "Escape") { e.preventDefault(); close(true); } }

  async function lock() {
    const token = admin.token;
    signOut();
    if (token) await fetch("/_/api/operators/logout", { method: "POST", headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(10_000) }).catch(() => {});
    close();
  }
  const until = $derived(admin.expires ? new Date(admin.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
</script>

<svelte:window onclick={outside} onkeydown={keys} />

<div class="relative" bind:this={root}>
  <button bind:this={trigger} type="button" aria-haspopup="dialog" aria-expanded={open} aria-controls="admin-menu" onclick={() => (open ? close() : void show())}
    class={["focus-visible:outline-ring flex h-9 items-center gap-2 rounded-full border px-3.5 text-sm font-medium transition-colors focus-visible:outline-2",
      admin.token ? "bg-card text-foreground border-success/40" : "text-foreground hover:bg-accent border-input"]}>
    {#if admin.token}<ShieldIcon class="text-success size-4" aria-hidden="true" /><span class="max-w-32 truncate">{admin.name || "Admin"}</span>
    {:else}<LockIcon class="text-muted-foreground size-4" aria-hidden="true" />Admin{/if}
    <ChevronIcon class="text-muted-foreground size-3.5" aria-hidden="true" />
  </button>

  {#if open}
    <div id="admin-menu" role="dialog" aria-label="Administration" class="bg-popover text-popover-foreground border-border absolute right-0 z-30 mt-2 w-[min(26rem,calc(100vw-2rem))] rounded-2xl border p-5 shadow-2xl">
      {#if admin.token}
        <p class="plate">Signed in</p>
        <h2 class="mt-1 text-base font-semibold">{admin.name || "Admin token"}</h2>
        <p class="text-muted-foreground text-xs">{until ? `This session ends at ${until}.` : "Available until you lock this tab."} It exists only in this tab.</p>
        <ul class="mt-4 divide-y rounded-lg border" aria-label="Tools this session may change">
          {#each tools as t (t.id)}
            {@const has = admin.tools.includes(t.id)}
            <li class="flex items-start gap-3 px-3 py-2.5">
              {#if has}<CheckIcon class="text-success mt-0.5 size-4 shrink-0" aria-hidden="true" />{:else}<MinusIcon class="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />{/if}
              <div class="min-w-0"><p class={["text-sm", has ? "" : "text-muted-foreground"]}>{t.label}<span class="sr-only">{has ? " (allowed)" : " (not included)"}</span></p><p class="text-muted-foreground text-xs">{t.can}</p></div>
            </li>
          {/each}
        </ul>
        <div class="mt-4 flex justify-end"><Button variant="outline" size="sm" onclick={lock}>Lock this tab</Button></div>
      {:else}
        <p class="plate">Administration</p>
        <h2 class="mt-1 mb-3 text-base font-semibold">Sign in as an operator</h2>
        <SignIn ondone={() => close()} />
      {/if}
    </div>
  {/if}
</div>
