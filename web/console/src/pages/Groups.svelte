<script lang="ts">
  import { onMount, untrack } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Summary from "$lib/Summary.svelte";
  import Modal from "$lib/Modal.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import Freshness from "$lib/Freshness.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { fetchGroups, fetchPeople, setGroups, type Group, type Person } from "$lib/api";
  import { peopleTabs } from "$lib/accessTabs";
  import { LatestRequest } from "$lib/latestRequest";
  import { navigate } from "$lib/router.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import PlusIcon from "@lucide/svelte/icons/plus";

  // A group exists while someone belongs to it or an application requires it.
  // Membership is stored per person, so editing a group changes each person
  // whose membership differs, with the membership the console last read as
  // the expected value: a concurrent change is refused, not overwritten.
  let groups = $state<Group[] | null>(null);
  let people = $state<Person[]>([]);
  let error = $state("");
  let loading = $state(false);
  let observedAt = $state<number | null>(null);
  const request = new LatestRequest();

  let editOpen = $state(false);
  let creating = $state(false);
  let groupName = $state("");
  let picked = $state<string[]>([]);
  let formError = $state("");
  let reviewOpen = $state(false);
  let saving = $state(false);
  let notice = $state<{ text: string; ok: boolean } | null>(null);

  async function load() {
    const token = admin.token.trim();
    if (!token) return;
    const run = request.start();
    loading = true;
    try {
      const [catalog, directory] = await Promise.all([fetchGroups(token, run.signal), fetchPeople(run.signal)]);
      if (!run.isCurrent()) return;
      groups = catalog; people = directory.users; error = "";
    } catch (e) {
      if (!run.isCurrent()) return;
      error = run.timedOut() ? "Request timed out" : (e as Error).message;
    } finally {
      if (run.isCurrent()) { loading = false; if (!error) observedAt = Date.now(); }
      run.finish();
    }
  }
  $effect(() => {
    const token = admin.token.trim();
    request.cancel();
    groups = null; error = ""; observedAt = null;
    if (token) untrack(() => { void load(); });
  });
  onMount(() => { const t = setInterval(() => { if (!editOpen && !reviewOpen && !saving) void load(); }, 15_000); return () => { clearInterval(t); request.cancel(); }; });

  const name = $derived(groupName.trim());
  const current = $derived(groups?.find((g) => g.name === name)?.members ?? []);
  const added = $derived(picked.filter((e) => !current.includes(e)));
  const removed = $derived(current.filter((e) => !picked.includes(e)));
  const unused = $derived(groups?.filter((g) => g.members.length === 0 && g.hosts.length > 0) ?? []);
  const label = (email: string) => { const p = people.find((x) => x.email === email); return p?.name ? `${p.name} (${email})` : email; };

  function edit(group?: Group) {
    if (!admin.token.trim()) { askForToken(); return; }
    creating = !group;
    groupName = group?.name ?? "";
    picked = [...(group?.members ?? [])];
    formError = ""; notice = null;
    editOpen = true;
  }

  function prepare(e: SubmitEvent) {
    e.preventDefault();
    formError = "";
    if (!name || name.includes(",")) { formError = "Give the group a name without commas."; return; }
    if (creating && groups?.some((g) => g.name === name && g.members.length)) { formError = `${name} already exists; edit it from the list.`; return; }
    if (!added.length && !removed.length) { formError = creating ? "Choose at least one member: a group exists through its members or an application that requires it." : "Nothing changed."; return; }
    reviewOpen = true;
  }

  async function save() {
    saving = true;
    const changes = [...added.map((email) => ({ email, join: true })), ...removed.map((email) => ({ email, join: false }))];
    const failed: string[] = [];
    for (const change of changes) {
      const person = people.find((p) => p.email === change.email);
      if (!person) { failed.push(`${change.email}: no longer listed`); continue; }
      const next = change.join ? [...new Set([...person.groups, name])].sort() : person.groups.filter((g) => g !== name);
      try { await setGroups(admin.token.trim(), person.email, person.groups, next); }
      catch (e) { failed.push(`${person.email}: ${(e as Error).message}`); }
    }
    saving = false;
    editOpen = false;
    notice = failed.length
      ? { text: `${changes.length - failed.length} of ${changes.length} changes saved for ${name}. Not saved: ${failed.join("; ")}. Refresh and review before retrying.`, ok: false }
      : { text: `${name} saved. The sessions of the ${changes.length} affected ${changes.length === 1 ? "person" : "people"} ended; they sign in with the new access.`, ok: true };
    await load();
  }

  const summary = $derived(groups ? [
    { value: groups.length, label: "groups" },
    { value: groups.filter((g) => g.hosts.length).length, label: "required by an app" },
    { value: unused.length, label: "required with no members", tone: unused.length ? ("warn" as const) : undefined },
  ] : [{ value: "—", label: "groups" }, { value: "—", label: "required by an app" }, { value: "—", label: "required with no members" }]);
</script>

<PageHeader title="People" intro="Who can sign in to your applications, the groups they belong to, and who runs this console." tabs={peopleTabs()}>
  <div class="flex flex-col items-start gap-2 sm:items-end">
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
      <Button size="sm" onclick={() => edit()} disabled={!people.length}><PlusIcon class="size-3.5" /> New group</Button>
    </div>
    <Freshness observedAt={observedAt} refreshing={loading && groups !== null} error={error} />
  </div>
</PageHeader>

<Summary label="Groups summary" items={summary} />

{#if notice}<p class={["mb-4 text-sm", !notice.ok && "text-destructive"]} role="status">{notice.text}</p>{/if}
{#if error && groups}<p class="border-warning/40 bg-warning/[0.06] mb-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>{/if}

{#if !groups}
  {#if error}<p class="text-destructive text-sm">Groups could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !admin.token.trim()}<p class="text-muted-foreground text-sm">Sign in as admin to read groups.</p>
  {:else}<div class="space-y-3" aria-busy="true" aria-label="Loading groups">{#each [1, 2] as i (i)}<Skeleton class="h-14 w-full" />{/each}</div>{/if}
{:else if groups.length === 0}
  <div class="panel px-5 py-10 text-center">
    <p class="font-medium">No groups yet</p>
    <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Put people in a group, then require it on an application so only they can open it.</p>
    {#if people.length}<Button class="mt-4" onclick={() => edit()}><PlusIcon class="size-4" /> New group</Button>
    {:else}<Button class="mt-4" variant="outline" onclick={() => navigate("/people")}>Invite someone first</Button>{/if}
  </div>
{:else}
  <ul class="panel divide-y" aria-label="Groups">
    {#each groups as group (group.name)}
      <li class="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(8rem,12rem)_1fr_1fr_8rem] md:items-center">
        <h2 class="font-mono text-sm font-medium break-all">{group.name}</h2>
        <div class="min-w-0">
          <p class="text-muted-foreground mb-1 text-xs md:sr-only">Members</p>
          {#if group.members.length}
            <ul class="flex flex-wrap gap-1.5" aria-label={`Members of ${group.name}`}>{#each group.members as m (m)}<li class="chip">{m}</li>{/each}</ul>
          {:else}<p class="text-warning text-xs">No members{group.hosts.length ? ": nobody passes this requirement" : ""}</p>{/if}
        </div>
        <div class="min-w-0">
          <p class="text-muted-foreground mb-1 text-xs md:sr-only">Required by</p>
          {#if group.hosts.length}
            <ul class="flex flex-wrap gap-1.5" aria-label={`Applications requiring ${group.name}`}>{#each group.hosts as h (h)}<li class="text-muted-foreground font-mono text-xs break-all">{h}</li>{/each}</ul>
          {:else}<p class="text-muted-foreground text-xs">No application requires it</p>{/if}
        </div>
        <div class="md:text-right"><Button variant="ghost" size="sm" aria-label={`Edit members of ${group.name}`} onclick={() => edit(group)}>Edit members</Button></div>
      </li>
    {/each}
  </ul>
  <p class="text-muted-foreground mt-3 text-xs">An application that requires several groups lets in only people in all of them. Groups from an OIDC provider stay with that provider and are not listed here.</p>
{/if}

<Modal bind:open={editOpen} title={creating ? "New group" : `Members of ${groupName}`} intro={creating ? "Name it and choose who belongs to it. Require it in Applications to restrict an app to them." : "Each changed person’s sessions end so the new access applies at their next sign-in."}>
  <form class="grid gap-4" onsubmit={prepare}>
    {#if creating}
      <div class="grid gap-2"><label for="group-name" class="text-sm font-medium">Name</label><Input id="group-name" bind:value={groupName} maxlength={64} placeholder="ops" autocomplete="off" /></div>
    {/if}
    <fieldset>
      <legend class="mb-2 text-sm font-medium">Members <span class="text-muted-foreground font-normal">· {picked.length} of {people.length}</span></legend>
      <div class="max-h-72 divide-y overflow-y-auto rounded-lg border">
        {#each people as p (p.email)}
          <label class="hover:bg-accent/40 flex cursor-pointer items-center gap-3 px-3 py-2 text-sm">
            <input type="checkbox" value={p.email} bind:group={picked} class="accent-[var(--primary)]" />
            <span class="min-w-0 flex-1 truncate">{p.name || p.email}{#if p.name}<span class="text-muted-foreground ml-1.5 text-xs">{p.email}</span>{/if}</span>
            {#if p.disabled}<span class="text-muted-foreground text-xs">disabled</span>{/if}
          </label>
        {/each}
      </div>
    </fieldset>
    {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
    <div class="flex justify-end gap-2">
      <Button type="button" variant="ghost" onclick={() => (editOpen = false)}>Cancel</Button>
      <Button type="submit" disabled={saving}>{saving ? "Saving…" : "Review change"}</Button>
    </div>
  </form>
</Modal>

<ConfirmAction bind:open={reviewOpen} title={creating ? "Create this group?" : "Change group members?"} target={name}
  current={current.length ? current.map(label).join(", ") : "No members"}
  requested={[added.length ? `Add ${added.map(label).join(", ")}` : "", removed.length ? `Remove ${removed.map(label).join(", ")}` : ""].filter(Boolean).join(" · ")}
  consequence={`This changes which applications ${added.length + removed.length === 1 ? "this person" : "these people"} may open. Their existing native sessions end immediately.`}
  detail="Each person is saved against the membership read here; if someone else changed it meanwhile, that person is refused and the rest are saved."
  confirmLabel="Save group" onConfirm={() => void save()} />
