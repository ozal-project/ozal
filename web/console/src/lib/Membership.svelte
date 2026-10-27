<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { setGroups, type Person } from "$lib/api";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import Modal from "$lib/Modal.svelte";
  let { person, known = [], refreshed, onnotice }: { person: Person; known?: string[]; refreshed: () => Promise<void>; onnotice: (text: string, ok: boolean) => void } = $props();
  let editing = $state(false);
  let value = $state("");
  let expected = $state<string[]>([]);
  let next = $state<string[]>([]);
  let review = $state(false);
  let waiting = $state(false);
  let error = $state("");
  const parse = (text: string) => [...new Set(text.split(",").map((g) => g.trim()).filter(Boolean))].sort();
  const chosen = $derived(parse(value));
  const suggestions = $derived(known.filter((g) => !chosen.includes(g)));
  function edit() { expected = [...person.groups]; value = person.groups.join(", "); error = ""; editing = true; }
  const add = (group: string) => { value = [...chosen, group].join(", "); };
  const drop = (group: string) => { value = chosen.filter((g) => g !== group).join(", "); };
  function prepare(e: SubmitEvent) {
    e.preventDefault();
    if (!admin.token.trim()) { askForToken(); return; }
    next = chosen;
    review = true;
  }
  async function save() {
    waiting = true; error = "";
    try {
      await setGroups(admin.token.trim(), person.email, expected, next);
      editing = false;
      onnotice(`Membership saved for ${person.email}. Their previous sessions have ended; they sign in with the new access.`, true);
    } catch (e) { error = `${(e as Error).message} Refresh before trying again if the result is uncertain.`; }
    finally { waiting = false; await refreshed(); }
  }
</script>
<Button variant="ghost" size="sm" aria-label={`Edit groups for ${person.email}`} disabled={waiting} onclick={edit}>Groups</Button>
<Modal bind:open={editing} title="Edit groups" intro={person.email}>
  <form onsubmit={prepare} class="grid gap-4">
    <div>
      <label for={`groups-${person.subject}`} class="mb-2 block text-sm font-medium">Groups, separated by commas</label>
      <Input id={`groups-${person.subject}`} bind:value={value} disabled={waiting} maxlength={8192} placeholder="ops, staff" autocomplete="off" />
      {#if chosen.length}
        <ul class="mt-3 flex flex-wrap gap-1.5" aria-label="Groups after this change">
          {#each chosen as group (group)}<li><button type="button" class="chip" onclick={() => drop(group)} aria-label={`Remove ${group}`}>{group} <span aria-hidden="true">×</span></button></li>{/each}
        </ul>
      {/if}
    </div>
    {#if suggestions.length}
      <div>
        <p class="text-muted-foreground mb-2 text-xs">Existing groups</p>
        <ul class="flex flex-wrap gap-1.5">
          {#each suggestions as group (group)}<li><button type="button" class="chip chip-add" onclick={() => add(group)} aria-label={`Add ${group}`}>+ {group}</button></li>{/each}
        </ul>
      </div>
    {/if}
    <p class="text-muted-foreground text-xs">Names are case-sensitive. An app that requires several groups needs all of them. OIDC memberships stay with their provider.</p>
    {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
    <div class="flex justify-end gap-2">
      <Button type="button" variant="ghost" disabled={waiting} onclick={() => (editing = false)}>Cancel</Button>
      <Button type="submit" disabled={waiting}>{waiting ? "Saving…" : "Review change"}</Button>
    </div>
  </form>
</Modal>
<ConfirmAction bind:open={review} title="Change group membership?" target={person.email}
  current={expected.join(", ") || "No local groups"} requested={next.join(", ") || "No local groups"}
  consequence="This changes which applications the person may open. Their existing native sessions will end immediately."
  detail="If another operator changes membership before you save, this update is refused. New memberships are evaluated by each app’s policy."
  confirmLabel="Save membership" onConfirm={() => void save()} />
