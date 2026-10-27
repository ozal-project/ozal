<script lang="ts">
  import { tick } from "svelte";
  import { admin, tools, signOut } from "$lib/adminToken.svelte";
  import { addPasskey, explainPasskeyError } from "$lib/passkey";
  import { Button } from "$lib/components/ui/button";
  import ShieldIcon from "@lucide/svelte/icons/shield-check";
  import CheckIcon from "@lucide/svelte/icons/check";
  import MinusIcon from "@lucide/svelte/icons/minus";
  import ChevronIcon from "@lucide/svelte/icons/chevron-down";

  // Who is signed in, what the session may change, and the way out.
  let open = $state(false);
  let root = $state<HTMLElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  let adding = $state(false);
  let notice = $state("");

  async function show() { open = true; notice = ""; await tick(); }
  function close(returnFocus = false) { open = false; if (returnFocus) trigger?.focus(); }
  function outside(e: MouseEvent) { if (open && root && !root.contains(e.target as Node)) close(); }
  function keys(e: KeyboardEvent) { if (open && e.key === "Escape") { e.preventDefault(); close(true); } }

  async function another() {
    if (adding) return;
    adding = true; notice = "";
    try {
      await addPasskey(admin.token.trim());
      notice = "Passkey added. You can sign in with either device.";
    } catch (e) { notice = explainPasskeyError(e); }
    finally { adding = false; }
  }

  async function leave() {
    const token = admin.token;
    signOut();
    if (token) await fetch("/_/api/operators/logout", { method: "POST", headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(10_000) }).catch(() => {});
  }
  const until = $derived(admin.expires ? new Date(admin.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
</script>

<svelte:window onclick={outside} onkeydown={keys} />

<div class="relative" bind:this={root}>
  <button bind:this={trigger} type="button" aria-haspopup="dialog" aria-expanded={open} aria-controls="user-menu" onclick={() => (open ? close() : void show())}
    class="bg-card text-foreground border-success/40 focus-visible:outline-ring flex h-9 items-center gap-2 rounded-full border px-3.5 text-sm font-medium transition-colors focus-visible:outline-2">
    <ShieldIcon class="text-success size-4" aria-hidden="true" /><span class="max-w-40 truncate">{admin.name || "Admin token"}</span>
    <ChevronIcon class="text-muted-foreground size-3.5" aria-hidden="true" />
  </button>

  {#if open}
    <div id="user-menu" role="dialog" aria-label="Your session" class="bg-popover text-popover-foreground border-border animate-in fade-in-0 zoom-in-95 absolute right-0 z-30 mt-2 w-[min(24rem,calc(100vw-2rem))] rounded-2xl border p-5 shadow-2xl duration-150 motion-reduce:animate-none">
      <p class="plate">Signed in</p>
      <h2 class="mt-1 text-base font-semibold">{admin.name || "Admin token"}</h2>
      <p class="text-muted-foreground text-xs">{until ? `This session ends at ${until}.` : "Until you sign out."} It exists only in this tab.</p>
      <ul class="mt-4 divide-y rounded-lg border" aria-label="Tools this session may change">
        {#each tools as t (t.id)}
          {@const has = admin.tools.includes(t.id)}
          <li class="flex items-center gap-3 px-3 py-2">
            {#if has}<CheckIcon class="text-success size-4 shrink-0" aria-hidden="true" />{:else}<MinusIcon class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />{/if}
            <span class={["text-sm", has ? "" : "text-muted-foreground"]}>{t.label}<span class="sr-only">{has ? " (allowed)" : " (not included)"}</span></span>
          </li>
        {/each}
      </ul>
      {#if notice}<p class="text-muted-foreground mt-3 text-xs" role="status">{notice}</p>{/if}
      <div class="mt-4 flex flex-wrap justify-between gap-2">
        {#if admin.passkey}<Button variant="outline" size="sm" onclick={another} disabled={adding}>{adding ? "Waiting for your device…" : "Add a passkey"}</Button>{:else}<span class="text-muted-foreground self-center text-xs">Host login: no passkey account.</span>{/if}
        <Button variant="outline" size="sm" onclick={leave}>Sign out</Button>
      </div>
    </div>
  {/if}
</div>
