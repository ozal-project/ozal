<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { Button } from "$lib/components/ui/button";
  import { admin, tools } from "$lib/adminToken.svelte";
  import { fetchAdmins, inviteAdmin, setAdminDisabled, type AdminAccount } from "$lib/api";
  import { LatestRequest } from "$lib/latestRequest";

  // People who run this console. They sign in with their own passkey; the host
  // is only needed for the first one and for recovery. A grant never exceeds
  // the tools of whoever gives it.
  let admins = $state<AdminAccount[] | null>(null);
  let error = $state("");
  let email = $state("");
  let name = $state("");
  let picked = $state<string[]>([]);
  let busy = $state(false);
  let formError = $state("");
  let invitation = $state<{ link: string; email: string; expires: string } | null>(null);
  let notices = $state<Record<string, string>>({});
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

  async function invite(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !picked.length) return;
    busy = true; formError = ""; invitation = null;
    try {
      const made = await inviteAdmin(admin.token.trim(), email.trim(), picked, name.trim() || undefined);
      invitation = { link: `${location.origin}/overview#enrol=${encodeURIComponent(made.token)}`, email: made.email, expires: made.expires };
      email = ""; name = ""; picked = [];
      await load();
    } catch (e) { formError = (e as Error).message; }
    finally { busy = false; }
  }

  async function toggle(account: AdminAccount) {
    notices = { ...notices, [account.id]: "" };
    try {
      await setAdminDisabled(admin.token.trim(), account.id, !account.disabled);
      await load();
    } catch (e) { notices = { ...notices, [account.id]: (e as Error).message }; }
  }

  onMount(() => { void load(); return () => request.cancel(); });
</script>

<PageHeader eyebrow="Operate" title="Administrators" intro="The people who run this console. Each signs in with their own passkey and sees only the tools you give them." />

{#if error && !admins}<p class="border-destructive/40 bg-destructive/10 mb-6 rounded-xl border px-4 py-3 text-sm" role="alert">Administrators could not be read ({error}).</p>{/if}

<div class="grid gap-6 lg:grid-cols-[1.4fr_1fr]">
  <Panel label="Directory" title={admins ? `${admins.filter((a) => !a.disabled).length} active` : "Reading…"} bodyClass="p-0">
    {#if admins && admins.length === 0}
      <p class="text-muted-foreground px-5 py-8 text-sm">No administrator yet. On the host run <span class="font-mono">sudo switchboard console enrol</span>.</p>
    {:else if admins}
      <ul class="divide-y">
        {#each admins as a (a.id)}
          <li class="flex flex-wrap items-start gap-4 px-5 py-4">
            <div class="min-w-0 flex-1">
              <p class="font-medium">{a.name}{#if a.you}<span class="text-muted-foreground ml-2 text-xs font-normal">you</span>{/if}{#if a.disabled}<span class="text-destructive ml-2 text-xs font-normal">disabled</span>{/if}</p>
              <p class="text-muted-foreground text-sm">{a.email}</p>
              <p class="mt-1.5 flex flex-wrap gap-1.5">
                {#each a.tools as id (id)}<span class="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs">{label(id)}</span>{/each}
              </p>
              <p class="text-muted-foreground mt-1.5 text-xs">{a.passkeys} passkey{a.passkeys === 1 ? "" : "s"} · last sign-in {when(a.last_used_at)}</p>
              {#if notices[a.id]}<p class="text-destructive mt-1 text-xs" role="alert">{notices[a.id]}</p>{/if}
            </div>
            {#if !a.you}<Button variant="outline" size="sm" onclick={() => toggle(a)}>{a.disabled ? "Enable" : "Disable"}</Button>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>

  <Panel label="Invite" title="Add an administrator">
    <form onsubmit={invite} class="grid gap-4">
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="grid gap-1.5">
        <label for="adm-email" class="text-sm font-medium">Email</label>
        <input id="adm-email" bind:value={email} type="email" required maxlength="254" autocomplete="off"
          class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
      </div>
      <div class="grid gap-1.5">
        <label for="adm-name" class="text-sm font-medium">Name <span class="text-muted-foreground font-normal">(optional, they can set it)</span></label>
        <input id="adm-name" bind:value={name} maxlength="80" autocomplete="off"
          class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
      </div>
      <fieldset>
        <legend class="mb-2 text-sm font-medium">Tools</legend>
        <div class="grid gap-2">
          {#each mine as t (t.id)}
            <label class="flex items-start gap-2 text-sm"><input type="checkbox" value={t.id} bind:group={picked} class="mt-1 accent-[var(--primary)]" /><span>{t.label}<span class="text-muted-foreground block text-xs">{t.can}</span></span></label>
          {/each}
        </div>
      </fieldset>
      <Button type="submit" disabled={busy || !email.trim() || !picked.length}>{busy ? "Creating…" : "Create invitation"}</Button>
    </form>

    {#if invitation}
      <div class="mt-5 grid gap-2" aria-live="polite">
        <p class="text-sm font-medium">Invitation for {invitation.email}</p>
        <CopyCommand command={invitation.link} label="Copy invitation link" />
        <p class="text-muted-foreground text-xs">Works once, until {when(invitation.expires)}. They open it as <span class="font-mono">localhost</span> through their own SSH tunnel to this console, give their name and create a passkey. This link is shown only now.</p>
      </div>
    {/if}
  </Panel>
</div>
