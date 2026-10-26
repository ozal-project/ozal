<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { setGroups, type Person } from "$lib/api";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  let { person, refreshed }: { person: Person; refreshed: () => Promise<void> } = $props();
  let editing = $state(false);
  let value = $state("");
  let expected = $state<string[]>([]);
  let next = $state<string[]>([]);
  let review = $state(false);
  let waiting = $state(false);
  let notice = $state("");
  let failed = $state(false);
  function edit() { expected = [...person.groups]; value = person.groups.join(", "); editing = true; notice = ""; }
  function prepare(e: SubmitEvent) {
    e.preventDefault();
    if (!admin.token.trim()) { askForToken(); return; }
    next = [...new Set(value.split(",").map((g) => g.trim()).filter(Boolean))].sort();
    review = true;
  }
  async function save() {
    waiting = true; failed = false; notice = "";
    try {
      await setGroups(admin.token.trim(), person.email, expected, next);
      editing = false; notice = "Membership saved. Their previous sessions have ended; they can sign in with the new access.";
    } catch (e) { failed = true; notice = `${(e as Error).message} Refresh before trying again if the result is uncertain.`; }
    finally { waiting = false; await refreshed(); }
  }
</script>
<div class="border-b px-5 py-4 text-sm">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <p><span class="text-muted-foreground">Local groups:</span> {person.groups.length ? person.groups.join(", ") : "None"}</p>
    <Button variant="outline" size="sm" aria-label={`Edit groups for ${person.email}`} disabled={waiting} onclick={edit}>Edit groups</Button>
  </div>
  {#if editing}
    <form onsubmit={prepare} class="mt-3 flex flex-wrap items-end gap-3">
      <div class="min-w-0 flex-1"><label for={`groups-${person.subject}`} class="mb-2 block">Groups, separated by commas</label><Input id={`groups-${person.subject}`} bind:value={value} disabled={waiting} maxlength={8192} /></div>
      <Button type="submit" disabled={waiting}>{waiting ? "Saving…" : "Review change"}</Button>
      <Button type="button" variant="ghost" disabled={waiting} onclick={() => editing = false}>Cancel</Button>
      <p class="text-muted-foreground basis-full text-xs">Names are case-sensitive. An app that lists several required groups needs all of them. These memberships are managed locally; OIDC memberships stay with their provider.</p>
    </form>
  {/if}
  {#if notice}<p class={["mt-3", failed && "text-destructive"]} role="status">{notice}</p>{/if}
</div>
<ConfirmAction bind:open={review} title="Change group membership?" target={person.email}
  current={expected.join(", ") || "No local groups"} requested={next.join(", ") || "No local groups"}
  consequence="This changes which applications the person may open. Their existing native sessions will end immediately."
  detail="If another operator changes membership before you save, this update is refused. New memberships are evaluated by each app’s policy."
  confirmLabel="Save membership" onConfirm={() => void save()} />
