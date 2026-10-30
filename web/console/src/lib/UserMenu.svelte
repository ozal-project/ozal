<script lang="ts">
  import { consoleOrigin } from "$lib/passkey";
  import { tick } from "svelte";
  import { admin, tools, signOut } from "$lib/adminToken.svelte";
  import { addPasskey, explainPasskeyError, issueDeviceLink } from "$lib/passkey";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { dialogs } from "$lib/dialogs.svelte";
  import ChevronIcon from "@lucide/svelte/icons/chevron-down";

  // Who is signed in, what the session may change, and the way out. In the
  // mobile directory it sits at the bottom and opens upward.
  let { placement = "down" }: { placement?: "up" | "down" } = $props();
  const initial = $derived((admin.name || "A").trim().charAt(0).toUpperCase());
  let open = $state(false);
  let root = $state<HTMLElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  let adding = $state(false);
  let notice = $state("");
  function manage() { close(true); dialogs.passkeys = true; }

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

  // A passkey saved here is not offered by other browsers: give the person a
  // one-use link to open there.
  let device = $state<{ link: string; until: string } | null>(null);
  async function otherBrowser() {
    notice = ""; device = null;
    try {
      const made = await issueDeviceLink(admin.token.trim());
      device = { link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, until: new Date(made.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) };
    } catch (e) { notice = (e as Error).message; }
  }

  async function leave() {
    const token = admin.token;
    signOut();
    if (token) await fetch("/_/api/operators/logout", { method: "POST", headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(10_000) }).catch(() => {});
  }
  const until = $derived(admin.expires ? new Date(admin.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
  const sessionText = $derived(admin.renewable ? "Signs out after 30 minutes without activity." : until ? `Ends at ${until}.` : "Lasts until you sign out.");
  const allowed = $derived(tools.filter((t) => admin.tools.includes(t.id)));
</script>

<svelte:window onclick={outside} onkeydown={keys} />

<div class="relative min-w-0 max-w-full" bind:this={root}>
  <button bind:this={trigger} type="button" aria-haspopup="dialog" aria-label={admin.name || "Admin token"} aria-expanded={open} aria-controls="user-menu" onclick={() => (open ? close() : void show())}
    class={["text-foreground focus-visible:outline-ring flex items-center gap-2.5 rounded-lg text-left text-sm transition-colors focus-visible:outline-2",
      placement === "up" ? "hover:bg-sidebar-accent/60 w-full px-2 py-2" : "hover:bg-muted h-9 w-full px-2"]}>
    <span class="bg-primary/15 text-primary flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold" aria-hidden="true">{initial}</span>
    <span class="min-w-0 flex-1 leading-tight">
      <span class="block truncate font-medium">{admin.name || "Admin token"}</span>
      {#if placement === "up"}<span class="text-muted-foreground block truncate text-xs">{admin.tools.length === tools.length ? "Full access" : "Limited access"}</span>{/if}
    </span>
    <ChevronIcon class={["text-muted-foreground size-3.5 shrink-0", placement === "up" && "rotate-180"]} aria-hidden="true" />
  </button>

  {#if open}
    <div id="user-menu" role="dialog" aria-label="Your session" class={["bg-popover text-popover-foreground border-border animate-in fade-in-0 zoom-in-95 absolute z-30 w-[min(22rem,calc(100vw-2rem))] rounded-xl border p-4 shadow-2xl duration-150 motion-reduce:animate-none",
      placement === "up" ? "bottom-full left-0 mb-2" : "right-0 mt-2"]}>
      <h2 class="text-sm font-semibold">{admin.name || "Admin token"}</h2>
      <p class="text-muted-foreground mt-1 text-xs">Signed in with {admin.passkey ? "a passkey" : "a host login"}. {sessionText}</p>
      <div class="bg-muted/60 mt-4 rounded-lg px-3 py-2.5 text-xs">
        {#if allowed.length === tools.length}
          <p><span class="font-medium">Full access.</span> <span class="text-muted-foreground">This session can change everything in the console.</span></p>
        {:else}
          <p class="font-medium">This session can</p>
          <ul class="text-muted-foreground mt-1 list-disc space-y-0.5 pl-4">
            {#each allowed as t (t.id)}<li>{t.can.charAt(0).toLowerCase() + t.can.slice(1)}</li>{/each}
          </ul>
          {#if allowed.length < tools.length}<p class="text-muted-foreground mt-2">Not included: {tools.filter((t) => !admin.tools.includes(t.id)).map((t) => t.label).join(", ")}. Ask an administrator to add them.</p>{/if}
        {/if}
      </div>
      {#if notice}<p class="text-muted-foreground mt-3 text-xs" role="status">{notice}</p>{/if}
      {#if device}
        <div class="mt-3 grid gap-2" aria-live="polite">
          <p class="text-xs">Open this in the other browser before {device.until}. It works once and creates a passkey there for you.</p>
          <CopyCommand command={device.link} label="Copy link for another browser" />
        </div>
      {/if}
      <div class="border-border mt-4 grid gap-0.5 border-t pt-3 text-sm">
        {#if admin.passkey}
          <button type="button" class="hover:bg-accent rounded-md px-2 py-1.5 text-left" onclick={otherBrowser}>Sign in on another browser</button>
          <button type="button" class="hover:bg-accent rounded-md px-2 py-1.5 text-left" onclick={another} disabled={adding}>{adding ? "Waiting for your device…" : "Add a passkey on this device"}</button>
          <button type="button" class="hover:bg-accent rounded-md px-2 py-1.5 text-left" onclick={manage}>Your passkeys</button>
        {:else}
          <p class="text-muted-foreground px-2 py-1.5 text-xs">A host login has no administrator account, so it cannot add passkeys.</p>
        {/if}
        <button type="button" class="hover:bg-accent rounded-md px-2 py-1.5 text-left" onclick={leave}>Sign out</button>
      </div>
    </div>
  {/if}
</div>
