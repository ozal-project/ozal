<script lang="ts">
  import { admin } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import { listOwnPasskeys, removeOwnPasskey, type OwnPasskey } from "$lib/passkey";

  // The signed-in administrator's passkeys, so a lost phone can be removed
  // without resetting every passkey. The one that opened this session stays.
  let { open = $bindable(false) }: { open?: boolean } = $props();
  const dialogId = $props.id();
  let dialog = $state<HTMLDialogElement | null>(null);
  let returnFocus: HTMLElement | null = null;
  let passkeys = $state<OwnPasskey[] | null>(null);
  let error = $state("");
  let notice = $state("");
  let pending = $state<OwnPasskey | null>(null);
  let reviewOpen = $state(false);
  let busy = $state(false);

  const name = (p: OwnPasskey) => p.label || "Passkey";
  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "never");

  async function load() {
    error = "";
    try { passkeys = await listOwnPasskeys(admin.token.trim()); }
    catch (e) { error = (e as Error).message; }
  }

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      notice = ""; passkeys = null;
      dialog.showModal();
      void load();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  function closed() {
    open = false;
    requestAnimationFrame(() => { if (returnFocus?.isConnected) returnFocus.focus(); returnFocus = null; });
  }

  function review(p: OwnPasskey) { pending = p; reviewOpen = true; }

  async function remove() {
    const target = pending;
    if (!target || busy) return;
    busy = true; error = ""; notice = "";
    try {
      await removeOwnPasskey(admin.token.trim(), target.id);
      notice = `${name(target)} was removed. It can no longer sign in.`;
    } catch (e) { error = (e as Error).message; }
    finally { busy = false; pending = null; await load(); }
  }
</script>

<dialog bind:this={dialog} aria-labelledby={`${dialogId}-title`} onclose={closed}
  class="bg-card text-card-foreground border-border m-auto w-[min(34rem,calc(100vw-2rem))] rounded-xl border p-0 backdrop:bg-black/60">
  <div class="space-y-4 p-5 sm:p-6">
    <div>
      <h2 id={`${dialogId}-title`} class="text-lg font-semibold">Your passkeys</h2>
      <p class="text-muted-foreground mt-1 text-sm">Remove one you no longer have, like a lost phone. Anyone signed in with it is signed out at once.</p>
    </div>
    {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
    {#if notice}<p class="text-sm" role="status">{notice}</p>{/if}
    {#if passkeys === null && !error}
      <p class="text-muted-foreground text-sm" role="status">Reading…</p>
    {:else if passkeys}
      <ul class="divide-border border-border divide-y rounded-lg border" aria-label="Passkeys">
        {#each passkeys as p (p.id)}
          <li class="flex flex-wrap items-center gap-3 px-3 py-3">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium">{name(p)}{#if p.current}<span class="text-primary ml-2 text-xs font-normal">this session</span>{/if}</p>
              <p class="text-muted-foreground text-xs">Added {when(p.created_at)} · last used {when(p.last_used_at)}</p>
            </div>
            {#if !p.current}
              <Button variant="outline" size="sm" disabled={busy} onclick={() => review(p)} aria-label={`Remove ${name(p)}, added ${when(p.created_at)}`}>Remove</Button>
            {/if}
          </li>
        {/each}
      </ul>
      {#if passkeys.length === 1}<p class="text-muted-foreground text-xs">This is your only passkey. Add another from your menu before you need it.</p>{/if}
    {/if}
    <div class="flex justify-end"><Button variant="outline" onclick={() => (open = false)}>Close</Button></div>
  </div>
</dialog>

<ConfirmAction
  bind:open={reviewOpen}
  title="Remove this passkey?"
  target={pending ? name(pending) : ""}
  current={pending ? `Can sign in · added ${when(pending.created_at)}` : ""}
  requested="Removed"
  consequence="It can no longer sign in to this console, and any session opened with it ends now. Your other passkeys keep working."
  confirmLabel="Remove passkey"
  onConfirm={remove}
/>
