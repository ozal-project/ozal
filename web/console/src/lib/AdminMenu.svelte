<script lang="ts">
  import { onMount, tick } from "svelte";
  import { admin, tools, signOut } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { exchangeHostLogin } from "$lib/operatorLogin";
  import ShieldIcon from "@lucide/svelte/icons/shield-check";
  import LockIcon from "@lucide/svelte/icons/lock";
  import CheckIcon from "@lucide/svelte/icons/check";
  import MinusIcon from "@lucide/svelte/icons/minus";
  import ChevronIcon from "@lucide/svelte/icons/chevron-down";

  // The host login carries its granted tools. Pasted credentials are checked
  // by the server before the menu describes their scope.
  let loginError = $state("");
  let open = $state(false);
  let draft = $state("");
  let checking = $state(false);
  let root = $state<HTMLElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  let paste = $state<HTMLInputElement | null>(null);
  let seen = admin.asked;
  const allTools = tools.map((t) => t.id);
  let command = $state("switchboard console login -name YOUR_NAME");
  let picked = $state<string[]>([...allTools]);
  $effect(() => {
    const chosen = picked.length === allTools.length ? "" : ` -tools ${picked.join(",") || "TOOL"}`;
    command = `switchboard console login -name YOUR_NAME${chosen}`;
  });

  onMount(() => {
    void exchangeHostLogin().then((session) => {
      if (!session) return;
      admin.token = session.token; admin.name = session.name; admin.expires = session.expires; admin.tools = session.tools ?? [];
    }).catch((e) => { loginError = (e as Error).message; open = true; });
    const timer = setInterval(() => {
      if (admin.expires && Date.parse(admin.expires) <= Date.now()) { signOut(); loginError = "Your operator session expired. Ask the host for a new login."; }
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

  async function unlock(e: SubmitEvent) {
    e.preventDefault();
    const token = draft.trim();
    if (!token || checking) return;
    checking = true; loginError = "";
    try {
      const response = await fetch("/_/api/operators/session", { headers: { Authorization: `Bearer ${token}` }, cache: "no-store", signal: AbortSignal.timeout(10_000) });
      if (!response.ok) throw new Error("This credential could not be verified. Check it and try again.");
      const session: { kind: string; name?: string; expires?: string; tools: string[] } = await response.json();
      admin.token = token; admin.name = session.name || (session.kind === "sites" ? "Sites token" : "");
      admin.expires = session.expires || ""; admin.tools = session.tools;
      draft = ""; close();
    } catch (e) { loginError = (e as Error).message; }
    finally { checking = false; }
  }
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
      {#if loginError}<p class="text-destructive mb-4 text-sm" role="alert">{loginError}</p>{/if}
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
        <h2 class="mt-1 text-base font-semibold">Sign in as an operator</h2>
        <p class="text-muted-foreground mt-1 text-sm">The host decides what each login may change. Pick the tools, run the command on the host and open the one-use link it prints.</p>
        <fieldset class="mt-4">
          <legend class="sr-only">Tools for this login</legend>
          <div class="grid grid-cols-2 gap-x-3 gap-y-2">
            {#each tools as t (t.id)}
              <label class="flex items-center gap-2 text-sm" title={t.can}><input type="checkbox" value={t.id} bind:group={picked} class="accent-[var(--primary)]" />{t.label}</label>
            {/each}
          </div>
        </fieldset>
        <div class="mt-4"><CopyCommand {command} label="Copy login command" /></div>
        <details class="mt-4 text-sm">
          <summary class="text-muted-foreground hover:text-foreground cursor-pointer">Use an admin or Sites token instead</summary>
          <form class="mt-3 flex items-center gap-2" onsubmit={unlock}>
            <label class="sr-only" for="admin-token">Admin token</label>
            <input id="admin-token" bind:this={paste} bind:value={draft} type="password" autocomplete="off" spellcheck="false" placeholder="Paste the admin token"
              class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
            <Button type="submit" size="sm" disabled={!draft.trim() || checking}>{checking ? "Checking…" : "Unlock"}</Button>
          </form>
        </details>
      {/if}
    </div>
  {/if}
</div>
