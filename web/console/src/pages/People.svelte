<script lang="ts">
  import Summary from "$lib/Summary.svelte";
  import { onMount, untrack } from "svelte";
  import Membership from "$lib/Membership.svelte";
  import Modal from "$lib/Modal.svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { fetchPeople, fetchGroups, invitePerson, setDisabled, revokePasskey, type People, type Person, type Invitation } from "$lib/api";
  import { peopleTabs } from "$lib/accessTabs";
  import { LatestRequest } from "$lib/latestRequest";
  import Freshness from "$lib/Freshness.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import UserPlusIcon from "@lucide/svelte/icons/user-plus";
  import CopyIcon from "@lucide/svelte/icons/copy";
  import ShareIcon from "@lucide/svelte/icons/share-2";

  let people = $state<People | null>(null);
  let known = $state<string[]>([]);
  let error = $state("");
  let loading = $state(false);
  let observedAt = $state<number | null>(null);
  const peopleRequest = new LatestRequest();

  // Invitation dialog.
  let inviteOpen = $state(false);
  let email = $state("");
  let name = $state("");
  let groups = $state("");
  let destination = $state("");
  let inviting = $state(false);
  let inviteController = $state<AbortController | null>(null);
  let inviteError = $state("");
  let created = $state<Invitation | null>(null);
  let copied = $state(false);
  let copyError = $state("");
  let invitationInput = $state<HTMLInputElement | null>(null);

  // Feedback next to the person it concerns.
  let notices = $state<Record<string, { text: string; ok: boolean }>>({});
  let mutations = $state<Record<string, AbortController>>({});
  let review = $state<{ title: string; target: string; current: string; requested: string; consequence: string; detail: string; label: string; commit: () => void } | null>(null);
  let reviewOpen = $state(false);
  let keysFor = $state<string | null>(null);
  let keysOpen = $state(false);
  const keysPerson = $derived(people?.users.find((p) => p.email === keysFor) ?? null);

  async function load() {
    if (!admin.token.trim()) return;
    const run = peopleRequest.start();
    loading = true;
    try {
      const [next, catalog] = await Promise.all([fetchPeople(run.signal), fetchGroups(admin.token.trim(), run.signal).catch(() => null)]);
      if (!run.isCurrent()) return;
      people = next;
      if (catalog) known = catalog.map((g) => g.name);
      error = "";
    } catch (e) {
      if (!run.isCurrent()) return;
      error = run.timedOut() ? "Request timed out" : (e as Error).message;
    } finally {
      if (run.isCurrent()) {
        loading = false;
        if (!error) observedAt = Date.now();
      }
      run.finish();
    }
  }
  $effect(() => {
    const token = admin.token.trim();
    peopleRequest.cancel();
    people = null; error = ""; observedAt = null; created = null;
    if (token) untrack(() => { void load(); });
  });
  onMount(() => { const t = setInterval(load, 15_000); return () => { clearInterval(t); peopleRequest.cancel(); inviteController?.abort(); Object.values(mutations).forEach((controller) => controller.abort()); }; });

  function needToken(): boolean {
    if (admin.token.trim()) return false;
    askForToken();
    return true;
  }

  function startInvite() {
    email = name = groups = destination = "";
    inviteError = ""; created = null; copied = false; copyError = "";
    inviteOpen = true;
  }

  async function invite(e: SubmitEvent) {
    e.preventDefault();
    inviteError = "";
    created = null;
    copied = false;
    if (needToken()) { inviteError = "Sign in as admin first (top right)."; return; }
    inviting = true;
    const controller = new AbortController();
    inviteController = controller;
    try {
      created = await invitePerson(admin.token.trim(), {
        email: email.trim(),
        name: name.trim(),
        groups: groups.split(",").map((g) => g.trim()).filter(Boolean),
        return_to: destination ? `https://${destination}/` : "",
      }, controller.signal);
      await load();
    } catch (err) {
      const uncertain = controller.signal.aborted || (err as Error).name === "TimeoutError";
      inviteError = uncertain
        ? "Could not confirm the invitation response. The request may have completed; refresh People before retrying."
        : (err as Error).message;
      if (uncertain) await load();
    } finally {
      if (inviteController === controller) {
        inviteController = null;
        inviting = false;
      }
    }
  }

  async function copy() {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.link);
      copied = true;
      copyError = "";
    } catch {
      copied = false;
      copyError = "Clipboard access was blocked. The selected invitation link is ready to copy manually.";
      invitationInput?.focus();
      invitationInput?.select();
    }
  }
  async function share() {
    if (created && navigator.share) await navigator.share({ title: "Your Ozal invitation", url: created.link }).catch(() => {});
  }

  async function act(key: string, personEmail: string, run: (signal: AbortSignal) => Promise<unknown>, done: string) {
    const noticeKey = `person:${personEmail}`;
    if (needToken()) { notices = { ...notices, [noticeKey]: { text: "Sign in as admin first (top right).", ok: false } }; return; }
    const controller = new AbortController();
    mutations = { ...mutations, [key]: controller };
    const cleared = { ...notices };
    delete cleared[noticeKey];
    notices = cleared;
    try {
      await run(controller.signal);
      notices = { ...notices, [noticeKey]: { text: done, ok: true } };
      await load();
    } catch (err) {
      const uncertain = controller.signal.aborted || (err as Error).name === "TimeoutError";
      notices = { ...notices, [noticeKey]: { text: uncertain ? "The response was interrupted. This change may have completed; refreshing to reconcile the current state." : (err as Error).message, ok: false } };
      if (uncertain) await load();
    } finally {
      const next = { ...mutations };
      delete next[key];
      mutations = next;
    }
  }

  function reviewToggle(p: Person) {
    const nextDisabled = !p.disabled;
    const key = `person:${p.email}:${nextDisabled ? "disable" : "enable"}`;
    review = {
      title: nextDisabled ? "Disable this person?" : "Enable this person?",
      target: p.email,
      current: p.disabled ? "Disabled; sign-in is blocked" : "Enabled; sign-in follows route policy",
      requested: nextDisabled ? "Disabled" : "Enabled",
      consequence: nextDisabled ? "They will not be able to sign in, and their active sessions will end." : "They will be able to sign in again; existing sessions are not restored.",
      detail: "Their other settings stay as they are.",
      label: nextDisabled ? "Disable person" : "Enable person",
      commit: () => void act(key, p.email, (signal) => setDisabled(admin.token.trim(), p.email, nextDisabled, signal), nextDisabled ? `${p.email} is disabled; their sessions ended.` : `${p.email} can sign in again.`),
    };
    reviewOpen = true;
  }

  function reviewRevoke(personEmail: string, id: string, provider: string) {
    const key = `passkey:${personEmail}:${id}`;
    review = {
      title: "Revoke this passkey?",
      target: `${personEmail} · ${provider} · ${id}`,
      current: "Active passkey",
      requested: "Revoked",
      consequence: "The credential stops working and all of this person’s sessions will end.",
      detail: "At least one active passkey is required; the server will refuse to remove the last one.",
      label: "Revoke passkey",
      commit: () => void act(key, personEmail, (signal) => revokePasskey(admin.token.trim(), personEmail, id, signal), `${provider} passkey revoked; ${personEmail}'s sessions ended.`),
    };
    reviewOpen = true;
  }

  function confirmReview() {
    const action = review;
    reviewOpen = false;
    if (action) action.commit();
  }

  function stopMutation(key: string) { mutations[key]?.abort(new DOMException("Stopped by operator", "AbortError")); }
  function stopInvite() { inviteController?.abort(new DOMException("Stopped by operator", "AbortError")); }
  function busyForPerson(personEmail: string) {
    return Object.keys(mutations).some((key) => key.startsWith(`person:${personEmail}:`) || key.startsWith(`passkey:${personEmail}:`));
  }
  const pendingFor = (personEmail: string) => Object.keys(mutations).find((key) => key.startsWith(`person:${personEmail}:`) || key.startsWith(`passkey:${personEmail}:`));
  const notify = (personEmail: string) => (text: string, ok: boolean) => { notices = { ...notices, [`person:${personEmail}`]: { text, ok } }; };

  const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "—");
  const active = (p: Person) => p.passkeys.filter((k) => !k.revoked);
  const activeKeys = $derived(people?.users.reduce((n, p) => n + active(p).length, 0) ?? 0);
  const disabledCount = $derived(people?.users.filter((p) => p.disabled).length ?? 0);
  const expires = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  const standing = (p: Person) => (p.disabled ? { lamp: "lamp-off", text: "Disabled" } : active(p).length === 0 ? { lamp: "lamp-wait", text: "Invited, not joined" } : { lamp: "lamp-on", text: "Signed up" });
</script>

<PageHeader title="People" intro="Who can sign in to your applications, the groups they belong to, and who runs this console." tabs={peopleTabs()}>
  <div class="flex flex-col items-start gap-2 sm:items-end">
    <div class="flex flex-wrap gap-2">
      <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
      {#if people?.enabled !== false}<Button size="sm" onclick={startInvite}><UserPlusIcon class="size-3.5" /> Invite person</Button>{/if}
    </div>
    <Freshness observedAt={observedAt} refreshing={loading && people !== null} error={error} />
  </div>
</PageHeader>

{#if people && !people.enabled}
  <Panel label="People" title="Passkeys are not enabled on this edge">
    <p class="text-muted-foreground text-sm">Add <code class="font-mono">auth.passkeys</code> (<code class="font-mono">rp_id</code>, <code class="font-mono">login_url</code>) and start Ozal with <code class="font-mono">-db</code>.</p>
  </Panel>
{:else}
  <Summary label="People summary" items={[{ value: people ? people.users.length : "—", label: "people" }, { value: people ? activeKeys : "—", label: "passkeys" }, { value: people ? disabledCount : "—", label: "disabled", tone: disabledCount ? "warn" : undefined }]} />

  {#if error && people}<p class="border-warning/40 bg-warning/[0.06] mb-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>{/if}
  {#if !people}
    {#if error}<p class="text-destructive text-sm">People could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
    {:else}<div class="space-y-3" aria-busy="true" aria-label="Loading people">{#each [1, 2] as i (i)}<Skeleton class="h-14 w-full" />{/each}</div>{/if}
  {:else if people.users.length === 0}
    <div class="panel px-5 py-10 text-center">
      <p class="font-medium">Nobody invited yet</p>
      <p class="text-muted-foreground mt-1 text-sm">An invitation is a one-time link; they create a passkey on their own device.</p>
      <Button class="mt-4" onclick={startInvite}><UserPlusIcon class="size-4" /> Invite person</Button>
    </div>
  {:else}
    <ul class="panel divide-y" aria-label="People">
      {#each people.users as p (p.email)}
        {@const s = standing(p)}
        {@const pending = pendingFor(p.email)}
        <li class="grid gap-x-6 gap-y-2 px-4 py-3 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_16rem] md:items-center">
          <div class="min-w-0">
            <p class="truncate text-sm font-medium">{p.name || p.email}</p>
            <p class="text-muted-foreground flex items-center gap-1.5 truncate text-xs"><span class={["lamp", s.lamp]} aria-hidden="true"></span>{s.text}{#if p.name}<span aria-hidden="true">·</span>{p.email}{/if}</p>
          </div>
          <ul class="flex min-w-0 flex-wrap gap-1.5" aria-label={`Groups of ${p.email}`}>
            {#each p.groups as group (group)}<li class="chip">{group}</li>{:else}<li class="text-muted-foreground text-xs">No groups</li>{/each}
          </ul>
          <div class="flex flex-wrap items-center gap-1 md:justify-end">
            <Membership person={p} {known} refreshed={load} onnotice={notify(p.email)} />
            <Button variant="ghost" size="sm" aria-label={`Passkeys of ${p.email}`} onclick={() => { keysFor = p.email; keysOpen = true; }}>Passkeys · {active(p).length}</Button>
            <Button variant="ghost" size="sm" class={p.disabled ? "" : "text-destructive"} disabled={busyForPerson(p.email)} onclick={() => reviewToggle(p)}>{p.disabled ? "Enable" : "Disable"}</Button>
          </div>
          {#if pending}<p class="text-muted-foreground md:col-span-3 text-xs" role="status">Waiting for the server… <button class="underline" onclick={() => stopMutation(pending)}>Stop waiting</button></p>{/if}
          {#if notices[`person:${p.email}`]}
            <p class={["md:col-span-3 text-sm", notices[`person:${p.email}`].ok ? "text-foreground" : "text-destructive"]} role="status">{notices[`person:${p.email}`].text}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<Modal bind:open={inviteOpen} title={created ? "Invitation ready" : "Invite person"} intro={created ? "" : "They get a one-time link to create a passkey on their own device. It works once and expires in 15 minutes."}>
  {#if created}
    <div role="status">
      <p class="text-sm font-semibold">Invitation for {created.email} · expires at {expires(created.expires_at)}</p>
      <p class="text-muted-foreground mb-3 text-xs">Send it over a channel you trust: whoever opens it first gets the passkey. It is shown only now.</p>
      <div class="flex flex-wrap gap-2">
        <Input bind:ref={invitationInput} readonly aria-label="Invitation link" value={created.link} class="min-w-0 flex-1 font-mono text-xs" onfocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
        <Button variant="outline" onclick={copy}><CopyIcon class="size-4" /> {copied ? "Copied" : "Copy link"}</Button>
        {#if typeof navigator !== "undefined" && "share" in navigator}<Button variant="outline" onclick={share}><ShareIcon class="size-4" /> Share</Button>{/if}
      </div>
      {#if copyError}<p class="text-destructive mt-2 text-sm" role="status">{copyError}</p>{/if}
      <div class="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onclick={startInvite}>Invite another</Button>
        <Button onclick={() => (inviteOpen = false)}>Done</Button>
      </div>
    </div>
  {:else}
    <form class="grid gap-4" onsubmit={invite}>
      <div class="grid gap-2"><Label for="inv-email">Email</Label><Input id="inv-email" type="email" required bind:value={email} placeholder="ana@example.com" autocomplete="off" /></div>
      <div class="grid gap-2"><Label for="inv-name">Name (optional)</Label><Input id="inv-name" bind:value={name} placeholder="Ana" autocomplete="off" /></div>
      <div class="grid gap-2">
        <Label for="inv-groups">Groups (comma-separated)</Label><Input id="inv-groups" bind:value={groups} placeholder="ops, staff" autocomplete="off" />
        {#if known.length}<p class="text-muted-foreground text-xs">Existing: {known.join(", ")}</p>{/if}
      </div>
      <div class="grid gap-2">
        <Label for="inv-dest">After the passkey, open</Label>
        <select id="inv-dest" bind:value={destination} class="border-input bg-background h-9 rounded-md border px-3 text-sm">
          <option value="">Their apps page</option>
          {#each people?.protected_hosts ?? [] as host (host)}<option value={host}>{host}</option>{/each}
        </select>
      </div>
      <p class="text-muted-foreground text-xs">Someone who lost every passkey is recovered on the host: <code class="font-mono">ozal user invite -recover</code>.</p>
      {#if inviteError}<p class="text-destructive text-sm" role="alert">{inviteError}</p>{/if}
      <div class="flex justify-end gap-2">
        {#if inviting}<Button type="button" variant="outline" onclick={stopInvite}>Stop waiting</Button>{/if}
        <Button type="button" variant="ghost" onclick={() => (inviteOpen = false)}>Cancel</Button>
        <Button type="submit" disabled={inviting}>{inviting ? "Creating…" : "Create invitation"}</Button>
      </div>
    </form>
  {/if}
</Modal>

<Modal bind:open={keysOpen} title="Passkeys" intro={keysPerson?.email ?? ""}>
  {#if keysPerson}
    {#if keysPerson.passkeys.length === 0}
      <p class="text-muted-foreground text-sm">They have not used the invitation yet. Invite them again if the link expired.</p>
    {:else}
      <ul class="divide-y rounded-lg border">
        {#each keysPerson.passkeys as k (k.id)}
          <li class={["flex flex-wrap items-center justify-between gap-3 px-4 py-3", k.revoked && "opacity-55"]}>
            <div class="min-w-0">
              <p class="text-sm font-medium">{k.provider} <span class="text-muted-foreground ml-1 text-xs font-normal">{k.synced ? "synced" : "device only"}</span></p>
              <p class="text-muted-foreground text-xs">added {day(k.created)} · last used {day(k.last_used)}{k.revoked ? ` · revoked ${day(k.revoked)}` : ""}</p>
            </div>
            {#if !k.revoked}<Button variant="outline" size="sm" class="text-destructive" disabled={busyForPerson(keysPerson.email)} onclick={() => reviewRevoke(keysPerson.email, k.id.slice(0, 16), k.provider)}>Revoke</Button>{/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if notices[`person:${keysPerson.email}`]}<p class={["mt-3 text-sm", !notices[`person:${keysPerson.email}`].ok && "text-destructive"]} role="status">{notices[`person:${keysPerson.email}`].text}</p>{/if}
    <div class="mt-5 flex justify-end"><Button variant="ghost" onclick={() => (keysOpen = false)}>Close</Button></div>
  {/if}
</Modal>

<ConfirmAction bind:open={reviewOpen} title={review?.title ?? "Review change"} target={review?.target ?? ""} current={review?.current ?? ""} requested={review?.requested ?? ""} consequence={review?.consequence ?? ""} detail={review?.detail ?? ""} confirmLabel={review?.label ?? "Confirm"} onConfirm={confirmReview} />
