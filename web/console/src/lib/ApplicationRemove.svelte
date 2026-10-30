<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import TrashIcon from "@lucide/svelte/icons/trash";
  // ask: set to true (from a menu) to start the removal; button: show the
  // trash button of its own.
  let { host, version, refreshed, ask = $bindable(false), button = true }: { host: string; version: string; refreshed: () => Promise<void>; ask?: boolean; button?: boolean } = $props();
  let review = $state(false);
  let expected = $state("");
  let busy = $state(false);
  let error = $state("");
  function prepare() { if (!admin.token.trim()) { askForToken(); return; } expected = version; review = true; }
  $effect(() => { if (ask) { ask = false; prepare(); } });
  async function remove() {
    busy = true; error = "";
    try {
      const res = await fetch(`/_/api/apps/${encodeURIComponent(host)}`, { method: "DELETE", headers: { "Content-Type": "application/json", Authorization: `Bearer ${admin.token.trim()}` }, body: JSON.stringify({ expected }), signal: AbortSignal.timeout(15_000) });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error ?? "Removal could not be confirmed. Refresh before retrying.");
      await refreshed();
    } catch (e) { error = (e as Error).message; }
    finally { busy = false; }
  }
</script>
{#if button}<Button size="icon-sm" variant="ghost" class="text-muted-foreground hover:text-destructive" aria-label={`Remove ${host}`} title="Remove" disabled={busy} onclick={prepare}><TrashIcon class="size-4" /></Button>{/if}
{#if error}<p class="text-destructive basis-full text-sm" role="alert">{error}</p>{/if}
<ConfirmAction bind:open={review} title="Remove this application hostname?" target={host} current="Configured route" requested="No route for this hostname" consequence="This hostname will stop forwarding or serving its site. Retained publications and application data are not deleted. DNS stays as it is." detail="The server refuses removal of the last route or a project’s last domain. Disconnect the publisher first when retiring that project." confirmLabel="Remove application" onConfirm={() => void remove()} />
