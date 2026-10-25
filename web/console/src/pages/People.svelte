<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { admin } from "$lib/adminToken.svelte";
  import { fetchPeople, invitePerson, setDisabled, revokePasskey, type People, type Invitation } from "$lib/api";
  import { LatestRequest } from "$lib/latestRequest";
  import Freshness from "$lib/Freshness.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import UserPlusIcon from "@lucide/svelte/icons/user-plus";
  import CopyIcon from "@lucide/svelte/icons/copy";
  import ShareIcon from "@lucide/svelte/icons/share-2";

  let people = $state<People | null>(null);
  let error = $state("");
  let loading = $state(false);
  let observedAt = $state<number | null>(null);
  const peopleRequest = new LatestRequest();
  let tokenInput = $state<HTMLInputElement | null>(null);

  // Invitation form.
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

  async function load() {
    const run = peopleRequest.start();
    loading = true;
    try {
      const next = await fetchPeople(run.signal);
      if (!run.isCurrent()) return;
      people = next;
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
  onMount(() => { load(); const t = setInterval(load, 15_000); return () => { clearInterval(t); peopleRequest.cancel(); inviteController?.abort(); Object.values(mutations).forEach((controller) => controller.abort()); }; });

  function needToken(): boolean {
    if (admin.token.trim()) return false;
    tokenInput?.scrollIntoView({ behavior: "smooth", block: "center" });
    tokenInput?.focus({ preventScroll: true });
    return true;
  }

  async function invite(e: SubmitEvent) {
    e.preventDefault();
    inviteError = "";
    created = null;
    copied = false;
    if (needToken()) { inviteError = "Paste the admin token first."; return; }
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
      email = name = "";
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
    if (created && navigator.share) await navigator.share({ title: "Your Switchboard invitation", url: created.link }).catch(() => {});
  }

  async function act(key: string, personEmail: string, run: (signal: AbortSignal) => Promise<unknown>, done: string) {
    const noticeKey = key.startsWith("passkey:") ? key : `person:${personEmail}`;
    if (needToken()) { notices = { ...notices, [noticeKey]: { text: "Paste the admin token above first.", ok: false } }; return; }
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

  function reviewToggle(p: People["users"][number]) {
    const nextDisabled = !p.disabled;
    const key = `person:${p.email}:${nextDisabled ? "disable" : "enable"}`;
    review = {
      title: nextDisabled ? "Disable this person?" : "Enable this person?",
      target: p.email,
      current: p.disabled ? "Disabled; sign-in is blocked" : "Enabled; sign-in follows route policy",
      requested: nextDisabled ? "Disabled" : "Enabled",
      consequence: nextDisabled ? "They will not be able to sign in, and their active sessions will end." : "They will be able to sign in again; existing sessions are not restored.",
      detail: p.groups.length ? `Groups remain unchanged: ${p.groups.join(", ")}` : "No groups are assigned.",
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

  const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "—");
  const active = (p: People["users"][number]) => p.passkeys.filter((k) => !k.revoked);
  const activeKeys = $derived(people?.users.reduce((n, p) => n + active(p).length, 0) ?? 0);
  const disabledCount = $derived(people?.users.filter((p) => p.disabled).length ?? 0);
  const expires = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
</script>

<PageHeader eyebrow="Identity" title="People & passkeys" intro="Who can sign in through Switchboard and with which passkeys. Invite someone, see their devices, and cut access at once: everything the switchboard user command does, from here.">
  <div class="flex flex-col items-start gap-2 sm:items-end">
    <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
    <Freshness observedAt={observedAt} refreshing={loading && people !== null} error={error} />
  </div>
</PageHeader>

{#if people && !people.enabled}
  <Panel label="People" title="Passkeys are not enabled on this edge">
    <p class="text-muted-foreground text-sm">Add <code class="font-mono">auth.passkeys</code> (<code class="font-mono">rp_id</code>, <code class="font-mono">login_url</code>) and start Switchboard with <code class="font-mono">-db</code>.</p>
  </Panel>
{:else}
  <div class="mb-5 flex flex-wrap gap-x-4 gap-y-1 rounded-lg border px-3 py-2 text-sm sm:hidden" aria-label="People summary">
    <span>{people ? people.users.length : "—"} people</span><span>{people ? activeKeys : "—"} active passkeys</span><span>{people ? disabledCount : "—"} disabled</span>
  </div>
  <div class="mb-6 hidden gap-4 sm:grid sm:grid-cols-3">
    <Stat label="People" value={people ? people.users.length : "—"} note="Invited by the operator" lamp={people?.users.length ? "on" : "idle"} />
    <Stat label="Active passkeys" value={people ? activeKeys : "—"} note="A passkey may sync across devices" lamp={activeKeys ? "on" : "idle"} />
    <Stat label="Disabled" value={people ? disabledCount : "—"} note="Cannot sign in; sessions ended" lamp={disabledCount ? "wait" : "idle"} />
  </div>

  <Panel index="00" label="Changes" title="Admin token" sub="Needed to invite, disable or revoke. Kept in this tab's memory for every page, never stored." class="mb-6">
    <div class="flex max-w-xl flex-col gap-2">
      <Label for="token" class="text-muted-foreground flex items-center gap-1.5 font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase"><KeyIcon class="size-3.5" /> Admin token</Label>
      <Input id="token" type="password" bind:ref={tokenInput} bind:value={admin.token} placeholder="Bearer token from admin.token" autocomplete="off" spellcheck={false} class="font-mono" />
    </div>
  </Panel>

  <Panel index="01" label="Invite" title="Invite someone" sub="They get a one-time link to create their passkey on their own device. It works once and expires in 15 minutes." class="mb-6">
    <form class="grid max-w-3xl gap-4 sm:grid-cols-2" onsubmit={invite}>
      <div class="flex flex-col gap-2"><Label for="inv-email">Email</Label><Input id="inv-email" type="email" required bind:value={email} placeholder="ana@example.com" autocomplete="off" /></div>
      <div class="flex flex-col gap-2"><Label for="inv-name">Name (optional)</Label><Input id="inv-name" bind:value={name} placeholder="Ana" autocomplete="off" /></div>
      <div class="flex flex-col gap-2"><Label for="inv-groups">Groups</Label><Input id="inv-groups" bind:value={groups} placeholder="owner, ops" autocomplete="off" /><p class="text-muted-foreground text-xs">Comma separated; access policies match them.</p></div>
      <div class="flex flex-col gap-2">
        <Label for="inv-dest">After the passkey, open</Label>
        <select id="inv-dest" bind:value={destination} class="border-input bg-background h-9 rounded-md border px-3 text-sm">
          <option value="">Their list of sites</option>
          {#each people?.protected_hosts ?? [] as host (host)}<option value={host}>{host}</option>{/each}
        </select>
      </div>
      <p class="text-muted-foreground text-xs sm:col-span-2">Someone who lost every passkey is recovered on the host, not here: <code class="font-mono">switchboard user invite -recover</code>.</p>
      <div class="flex flex-wrap items-center gap-3 sm:col-span-2">
        <Button type="submit" disabled={inviting}><UserPlusIcon class="size-4" /> {inviting ? "Creating…" : "Create invitation"}</Button>
        {#if inviting}<Button type="button" variant="outline" onclick={stopInvite}>Stop waiting</Button>{/if}
        {#if inviteError}<p class="text-destructive text-sm" role="alert">{inviteError}</p>{/if}
      </div>
    </form>
    {#if created}
      <div class="border-primary/40 bg-accent/30 mt-5 max-w-3xl rounded-lg border p-4" role="status">
        <p class="text-sm font-semibold">Invitation for {created.email} · expires at {expires(created.expires_at)}</p>
        <p class="text-muted-foreground mb-3 text-xs">Send it over a channel you trust: whoever opens it first gets the passkey. It is shown only now.</p>
        <div class="flex flex-wrap gap-2">
          <Input bind:ref={invitationInput} readonly aria-label="Invitation link" value={created.link} class="min-w-0 flex-1 font-mono text-xs" onfocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
          <Button variant="outline" onclick={copy}><CopyIcon class="size-4" /> {copied ? "Copied" : "Copy link"}</Button>
          {#if typeof navigator !== "undefined" && "share" in navigator}<Button variant="outline" onclick={share}><ShareIcon class="size-4" /> Share</Button>{/if}
        </div>
        {#if copyError}<p class="text-destructive mt-2 text-sm" role="status">{copyError}</p>{/if}
      </div>
    {/if}
  </Panel>

  {#if error && people}<p class="border-warning/40 bg-warning/[0.06] mb-4 rounded-md border p-3 text-sm" role="status">Showing the last successful read. Refresh failed: {error}</p>{/if}
  {#if !people}
    {#if error}<p class="text-destructive text-sm">People could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
    {:else}<div class="space-y-3" aria-busy="true" aria-label="Loading people">{#each [1, 2] as i (i)}<Skeleton class="h-24 w-full" />{/each}</div>{/if}
  {:else if people.users.length === 0}
    <Panel label="People" title="Nobody invited yet"><p class="text-muted-foreground text-sm">Create the first invitation above.</p></Panel>
  {:else}
    <div class="space-y-6">
      {#each people.users as p, i (p.email)}
        <Panel index={String(i + 2).padStart(2, "0")} label={p.disabled ? "Disabled" : "Person"} title={p.name ? `${p.name} · ${p.email}` : p.email}
          sub={`Invited ${day(p.created)} · ${p.groups.length ? "groups " + p.groups.join(", ") : "no groups"} · ${active(p).length} active passkey${active(p).length === 1 ? "" : "s"}`} bodyClass="p-0">
          {#snippet actions()}
            {@const toggleKey = `person:${p.email}:${p.disabled ? "enable" : "disable"}`}
            <Button variant="outline" size="sm" disabled={busyForPerson(p.email)} onclick={() => reviewToggle(p)}>{mutations[toggleKey] ? "Waiting…" : p.disabled ? "Enable" : "Disable"}</Button>
            {#if mutations[toggleKey]}<Button variant="ghost" size="sm" onclick={() => stopMutation(toggleKey)}>Stop waiting</Button>{/if}
          {/snippet}
          {#if notices[`person:${p.email}`]}
            <p class={["border-b px-5 py-3 text-sm", notices[`person:${p.email}`].ok ? "text-foreground" : "text-destructive"]} role="status">{notices[`person:${p.email}`].text}</p>
          {/if}
          {#if p.passkeys.length === 0}
            <p class="text-muted-foreground px-5 py-4 text-sm">No passkey yet: the invitation has not been used.</p>
          {:else}
            <ul class="divide-border divide-y">
              {#each p.passkeys as k (k.id)}
                <li class={["flex flex-wrap items-center justify-between gap-3 px-5 py-3", k.revoked && "opacity-55"]}>
                  <div class="min-w-0">
                    <p class="text-sm font-semibold">{k.provider}
                      <span class={["ml-2 rounded-full border px-2 py-0.5 font-mono text-[0.65rem]", k.synced ? "text-muted-foreground" : "border-success/50 text-success"]}>{k.synced ? "synced" : "device only"}</span>
                    </p>
                    <p class="text-muted-foreground font-mono text-xs">added {day(k.created)} · last used {day(k.last_used)}{k.revoked ? ` · revoked ${day(k.revoked)}` : ""} · {k.id.slice(0, 16)}</p>
                  </div>
                  {#if !k.revoked}
                    {@const key = `passkey:${p.email}:${k.id.slice(0, 16)}`}
                    <div class="flex flex-wrap items-center gap-2">
                      <Button variant="outline" size="sm" class="text-destructive" disabled={busyForPerson(p.email)} onclick={() => reviewRevoke(p.email, k.id.slice(0, 16), k.provider)}>{mutations[key] ? "Waiting…" : "Revoke"}</Button>
                      {#if mutations[key]}<Button variant="ghost" size="sm" onclick={() => stopMutation(key)}>Stop waiting</Button>{/if}
                    </div>
                    {#if notices[key]}<p class="text-destructive basis-full text-sm" role="status">{notices[key].text}</p>{/if}
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </Panel>
      {/each}
    </div>
  {/if}
{/if}

<ConfirmAction bind:open={reviewOpen} title={review?.title ?? "Review change"} target={review?.target ?? ""} current={review?.current ?? ""} requested={review?.requested ?? ""} consequence={review?.consequence ?? ""} detail={review?.detail ?? ""} confirmLabel={review?.label ?? "Confirm"} onConfirm={confirmReview} />
