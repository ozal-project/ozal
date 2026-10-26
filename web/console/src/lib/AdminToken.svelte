<script lang="ts">
  import { tick } from "svelte";
  import { admin } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import LockIcon from "@lucide/svelte/icons/lock-open";

  // The one place the admin token is entered. Pages that need it call
  // askForToken(); this opens and focuses the field.
  let open = $state(false);
  let draft = $state("");
  let input = $state<HTMLInputElement | null>(null);
  let seen = admin.asked;

  $effect(() => {
    if (admin.asked !== seen) {
      seen = admin.asked;
      if (!admin.token) void show();
    }
  });

  async function show() {
    open = true;
    await tick();
    input?.focus();
  }

  function unlock(e: SubmitEvent) {
    e.preventDefault();
    if (!draft.trim()) return;
    admin.token = draft.trim();
    draft = "";
    open = false;
  }

  function lock() {
    admin.token = "";
    open = false;
  }
</script>

{#if admin.token}
  <span class="border-border bg-card text-success flex items-center gap-2 rounded-full border px-3 py-1 text-xs"><LockIcon class="size-3.5" aria-hidden="true" />Changes unlocked</span>
  <button type="button" class="text-muted-foreground hover:text-foreground focus-visible:outline-ring rounded px-1.5 py-1 text-xs underline underline-offset-4 focus-visible:outline-2" onclick={lock}>Lock</button>
{:else if open}
  <form class="flex items-center gap-2" onsubmit={unlock}>
    <label class="sr-only" for="admin-token">Admin token</label>
    <input id="admin-token" bind:this={input} bind:value={draft} type="password" autocomplete="off" spellcheck="false" placeholder="Paste the admin token"
      class="border-input bg-background focus-visible:ring-ring h-8 w-56 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
    <Button type="submit" size="sm" disabled={!draft.trim()}>Unlock</Button>
    <button type="button" class="text-muted-foreground hover:text-foreground text-xs underline underline-offset-4" onclick={() => (open = false)}>Cancel</button>
  </form>
{:else}
  <Button variant="outline" size="sm" onclick={show}><KeyIcon class="size-3.5" aria-hidden="true" />Unlock changes</Button>
{/if}
