<script lang="ts">
  import { onMount } from "svelte";
  import { admin } from "$lib/adminToken.svelte";
  import { addPasskey, consoleOrigin, explainPasskeyError, issueDeviceLink, listOwnPasskeys, type OwnPasskey } from "$lib/passkey";
  import { needsBackup, readLater, rememberLater } from "$lib/backupPasskey";
  import CopyCommand from "$lib/CopyCommand.svelte";

  // Shown on Overview while the signed-in administrator has a single passkey
  // (ID-017). The list is read again when the tab regains focus, so a passkey
  // added from another browser through the link hides this without a reload.
  const storage = () => { try { return sessionStorage; } catch { return undefined; } };
  let passkeys = $state<OwnPasskey[] | null>(null);
  let later = $state(readLater(storage()));
  let adding = $state(false);
  let notice = $state("");
  let device = $state<{ link: string; until: string } | null>(null);
  const shown = $derived(needsBackup(passkeys, later));

  async function load() {
    try { passkeys = await listOwnPasskeys(admin.token.trim()); }
    catch { passkeys = null; }
  }
  onMount(() => { void load(); });

  async function here() {
    if (adding) return;
    adding = true; notice = "";
    try { await addPasskey(admin.token.trim()); await load(); }
    catch (e) { notice = explainPasskeyError(e); }
    finally { adding = false; }
  }

  async function elsewhere() {
    notice = ""; device = null;
    try {
      const made = await issueDeviceLink(admin.token.trim());
      device = { link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, until: new Date(made.expires).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) };
    } catch (e) { notice = (e as Error).message; }
  }

  function notNow() { later = true; rememberLater(storage()); }
</script>

<svelte:window onfocus={() => { if (!later) void load(); }} />

{#if shown}
  <section class="bg-card mb-6 rounded-lg px-4 py-3.5" aria-labelledby="backup-passkey-title">
    <h2 id="backup-passkey-title" class="text-sm font-semibold">Add a second passkey</h2>
    <p class="text-muted-foreground mt-1 text-sm">You have one passkey for this console. If you lose it, only someone on the host can let you back in. Keep another one somewhere else: a security key, a password manager or another device.</p>
    {#if notice}<p class="text-muted-foreground mt-2 text-xs" role="status">{notice}</p>{/if}
    {#if device}
      <div class="mt-3 grid gap-2" aria-live="polite">
        <p class="text-xs">Open this on the other device or browser before {device.until}. It works once and creates a passkey there for you.</p>
        <CopyCommand command={device.link} label="Copy link for another device" />
      </div>
    {/if}
    <div class="mt-3 flex flex-wrap gap-2">
      <button type="button" class="bg-primary text-primary-foreground rounded-md px-3 py-1.5 text-xs font-medium" onclick={here} disabled={adding}>{adding ? "Waiting for your device…" : "Add one in this browser"}</button>
      <button type="button" class="border-border hover:bg-accent rounded-md border px-3 py-1.5 text-xs" onclick={elsewhere}>Use another device</button>
      <button type="button" class="text-muted-foreground hover:text-foreground px-2 py-1.5 text-xs underline" onclick={notNow}>Not now</button>
    </div>
  </section>
{/if}
