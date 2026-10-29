<script lang="ts">
  import { consoleOrigin } from "$lib/passkey";
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Modal from "$lib/Modal.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { admin, tools } from "$lib/adminToken.svelte";
  import { fetchAdmins, inviteAdmin, recoverAdmin, setAdminDisabled, type AdminAccount } from "$lib/api";
  import { addPasskey, explainPasskeyError } from "$lib/passkey";
  import { peopleTabs } from "$lib/accessTabs";
  import { LatestRequest } from "$lib/latestRequest";
  import UserPlusIcon from "@lucide/svelte/icons/user-plus";

  // People who run this console. They sign in with their own passkey; the host
  // is only needed for the first one and for recovery. A grant never exceeds
  // the tools of whoever gives it.
  let admins = $state<AdminAccount[] | null>(null);
  let error = $state("");
  let addOpen = $state(false);
  let email = $state("");
  let name = $state("");
  let picked = $state<string[]>([]);
  let busy = $state(false);
  let formError = $state("");
  let invitation = $state<{ link: string; email: string; expires: string } | null>(null);
  let recovery = $state<{ link: string; email: string; expires: string } | null>(null);
  let recoveryOpen = $state(false);
  let notices = $state<Record<string, string>>({});
  let toggling = $state<AdminAccount | null>(null);
  let toggleOpen = $state(false);
  const request = new LatestRequest();
  const mine = $derived(tools.filter((t) => admin.tools.includes(t.id)));
  const label = (id: string) => tools.find((t) => t.id === id)?.label ?? id;
  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "never");

  async function load() {
    const run = request.start();
    try {
      const next = await fetchAdmins(run.signal);
      if (!run.isCurrent()) return;
      admins = next; error = "";
    } catch (e) {
      if (!run.isCurrent()) return;
      error = run.timedOut() ? "Request timed out" : (e as Error).message;
    } finally { run.finish(); }
  }

  function startAdd() {
    email = ""; name = ""; picked = []; formError = ""; invitation = null;
    addOpen = true;
  }

  async function invite(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !picked.length) return;
    busy = true; formError = "";
    try {
      const made = await inviteAdmin(admin.token.trim(), email.trim(), picked, name.trim() || undefined);
      invitation = { link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, email: made.email, expires: made.expires };
      await load();
    } catch (e) { formError = (e as Error).message; }
    finally { busy = false; }
  }

  async function reset(account: AdminAccount) {
    notices = { ...notices, [account.id]: "" }; recovery = null;
    try {
      const made = await recoverAdmin(admin.token.trim(), account.id);
      recovery = { link: `${consoleOrigin(location.origin)}/overview#enrol=${encodeURIComponent(made.token)}`, email: made.email, expires: made.expires };
      recoveryOpen = true;
    } catch (e) { notices = { ...notices, [account.id]: (e as Error).message }; }
  }

  async function addMine(account: AdminAccount) {
    notices = { ...notices, [account.id]: "" };
    try {
      await addPasskey(admin.token.trim());
      notices = { ...notices, [account.id]: "Passkey added: you can sign in with either device." };
      await load();
    } catch (e) { notices = { ...notices, [account.id]: explainPasskeyError(e) }; }
  }

  function reviewToggle(account: AdminAccount) { toggling = account; toggleOpen = true; }
  async function toggle() {
    const account = toggling;
    if (!account) return;
    notices = { ...notices, [account.id]: "" };
    try {
      await setAdminDisabled(admin.token.trim(), account.id, !account.disabled);
      await load();
    } catch (e) { notices = { ...notices, [account.id]: (e as Error).message }; }
  }

  onMount(() => { void load(); return () => request.cancel(); });
</script>

<PageHeader title="People" intro="Who can sign in to your applications, the groups they belong to, and who runs this console." tabs={peopleTabs()}>
  <Button size="sm" onclick={startAdd}><UserPlusIcon class="size-3.5" /> Add administrator</Button>
</PageHeader>

{#if error && !admins}<p class="border-destructive/40 bg-destructive/10 mb-6 rounded-xl border px-4 py-3 text-sm" role="alert">Administrators could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>{/if}

{#if !admins}
  {#if !error}<div class="space-y-3" aria-busy="true" aria-label="Loading administrators">{#each [1, 2] as i (i)}<Skeleton class="h-14 w-full" />{/each}</div>{/if}
{:else if admins.length === 0}
  <div class="panel px-5 py-10 text-center">
    <p class="font-medium">No administrator has a passkey account yet</p>
    <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">{#if !admin.passkey}You are using a host login, which is not an account; invite yourself to get one.{:else}Invite the people who run this console.{/if}</p>
    <Button class="mt-4" onclick={startAdd}><UserPlusIcon class="size-4" /> Add administrator</Button>
  </div>
{:else}
  <p class="text-muted-foreground mb-3 text-sm">They run this console with their own passkey and see only the tools they were given.</p>
  <ul class="panel divide-y" aria-label="Administrators">
    {#each admins as a (a.id)}
      <li class="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_13rem] md:items-center">
        <div class="min-w-0">
          <p class="truncate text-sm font-medium">{a.name}{#if a.you}<span class="text-muted-foreground ml-2 text-xs font-normal">you</span>{/if}</p>
          <p class="text-muted-foreground flex items-center gap-1.5 truncate text-xs"><span class={["lamp", a.disabled ? "lamp-off" : "lamp-on"]} aria-hidden="true"></span>{a.disabled ? "Disabled" : "Active"} · {a.email} · last sign-in {when(a.last_used_at)}</p>
        </div>
        <ul class="flex min-w-0 flex-wrap gap-1.5" aria-label={`Tools of ${a.email}`}>
          {#if a.tools.length === tools.length}<li class="chip">All tools</li>
          {:else}{#each a.tools as id (id)}<li class="chip">{label(id)}</li>{/each}{/if}
        </ul>
        <div class="flex flex-wrap items-center gap-1 md:justify-end">
          {#if a.you}
            <Button variant="ghost" size="sm" onclick={() => addMine(a)}>Add a passkey</Button>
          {:else}
            {#if !a.disabled}<Button variant="ghost" size="sm" onclick={() => reset(a)}>Reset passkeys</Button>{/if}
            <Button variant="ghost" size="sm" class={a.disabled ? "" : "text-destructive"} onclick={() => reviewToggle(a)}>{a.disabled ? "Enable" : "Disable"}</Button>
          {/if}
        </div>
        {#if notices[a.id]}<p class="text-muted-foreground md:col-span-3 text-xs" role="status">{notices[a.id]}</p>{/if}
      </li>
    {/each}
  </ul>
{/if}

<Modal bind:open={addOpen} title={invitation ? "Invitation ready" : "Add administrator"} intro={invitation ? "" : "They get a one-time link to create their own passkey. You can give only tools you have."}>
  {#if invitation}
    <div class="grid gap-2" aria-live="polite">
      <p class="text-sm font-medium">Invitation for {invitation.email}</p>
      <CopyCommand command={invitation.link} label="Copy invitation link" />
      <p class="text-muted-foreground text-xs">Works once, until {when(invitation.expires)}. They open it as <span class="font-mono">ozal.localhost</span> through their own SSH tunnel to this console, give their name and create a passkey. Shown only now.</p>
      <div class="mt-3 flex justify-end gap-2">
        <Button variant="ghost" onclick={startAdd}>Add another</Button>
        <Button onclick={() => (addOpen = false)}>Done</Button>
      </div>
    </div>
  {:else}
    <form onsubmit={invite} class="grid gap-4">
      <div class="grid gap-2"><label for="adm-email" class="text-sm font-medium">Email</label><Input id="adm-email" bind:value={email} type="email" required maxlength={254} autocomplete="off" /></div>
      <div class="grid gap-2"><label for="adm-name" class="text-sm font-medium">Name <span class="text-muted-foreground font-normal">(optional, they can set it)</span></label><Input id="adm-name" bind:value={name} maxlength={80} autocomplete="off" /></div>
      <fieldset>
        <legend class="mb-2 text-sm font-medium">Tools <span class="text-muted-foreground font-normal">· {picked.length} of {mine.length}</span></legend>
        <div class="divide-y rounded-lg border">
          {#each mine as t (t.id)}
            <label class="hover:bg-accent/40 flex cursor-pointer items-start gap-3 px-3 py-2 text-sm"><input type="checkbox" value={t.id} bind:group={picked} class="mt-1 accent-[var(--primary)]" /><span>{t.label}<span class="text-muted-foreground block text-xs">{t.can}</span></span></label>
          {/each}
        </div>
      </fieldset>
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex justify-end gap-2">
        <Button type="button" variant="ghost" onclick={() => (addOpen = false)}>Cancel</Button>
        <Button type="submit" disabled={busy || !email.trim() || !picked.length}>{busy ? "Creating…" : "Create invitation"}</Button>
      </div>
    </form>
  {/if}
</Modal>

<Modal bind:open={recoveryOpen} title="Recovery link">
  {#if recovery}
    <div class="grid gap-2" aria-live="polite">
      <p class="text-sm font-medium">Recovery link for {recovery.email}</p>
      <CopyCommand command={recovery.link} label="Copy recovery link" />
      <p class="text-muted-foreground text-xs">Works once, until {when(recovery.expires)}. Opening it replaces every passkey of that person and ends their open sessions. Send it only to them. Shown only now.</p>
      <div class="mt-3 flex justify-end"><Button onclick={() => (recoveryOpen = false)}>Done</Button></div>
    </div>
  {/if}
</Modal>

<ConfirmAction bind:open={toggleOpen} title={toggling?.disabled ? "Enable this administrator?" : "Disable this administrator?"} target={toggling?.email ?? ""}
  current={toggling?.disabled ? "Disabled; cannot sign in" : "Active"} requested={toggling?.disabled ? "Active" : "Disabled"}
  consequence={toggling?.disabled ? "They can sign in to the console again with their passkey." : "They cannot sign in to the console, and their open console sessions end."}
  detail="Their tools and passkeys are kept."
  confirmLabel={toggling?.disabled ? "Enable administrator" : "Disable administrator"} onConfirm={() => void toggle()} />
