<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Checkbox } from "$lib/components/ui/checkbox";
  import { Skeleton } from "$lib/components/ui/skeleton";
  import { admin } from "$lib/adminToken.svelte";
  import { fetchPeople, invitePerson, setDisabled, revokePasskey, type People, type Invitation } from "$lib/api";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import UserPlusIcon from "@lucide/svelte/icons/user-plus";
  import CopyIcon from "@lucide/svelte/icons/copy";
  import ShareIcon from "@lucide/svelte/icons/share-2";

  let people = $state<People | null>(null);
  let error = $state("");
  let loading = $state(false);
  let tokenInput = $state<HTMLInputElement | null>(null);

  // Invitation form.
  let email = $state("");
  let name = $state("");
  let groups = $state("");
  let destination = $state("");
  let recover = $state(false);
  let inviting = $state(false);
  let inviteError = $state("");
  let created = $state<Invitation | null>(null);
  let copied = $state(false);

  // Feedback next to the person it concerns.
  let notice = $state<{ email: string; text: string; ok: boolean } | null>(null);
  let pending = $state("");

  async function load() {
    loading = true;
    try {
      people = await fetchPeople();
      error = "";
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  onMount(() => { load(); const t = setInterval(load, 15_000); return () => clearInterval(t); });

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
    try {
      created = await invitePerson(admin.token.trim(), {
        email: email.trim(),
        name: name.trim(),
        groups: groups.split(",").map((g) => g.trim()).filter(Boolean),
        return_to: destination ? `https://${destination}/` : "",
        recover,
      });
      email = name = "";
      recover = false;
      await load();
    } catch (err) {
      inviteError = (err as Error).message;
    } finally {
      inviting = false;
    }
  }

  async function copy() {
    if (!created) return;
    await navigator.clipboard.writeText(created.link);
    copied = true;
  }
  async function share() {
    if (created && navigator.share) await navigator.share({ title: "Your Switchboard invitation", url: created.link }).catch(() => {});
  }

  async function act(personEmail: string, run: () => Promise<unknown>, done: string) {
    notice = null;
    if (needToken()) { notice = { email: personEmail, text: "Paste the admin token above first.", ok: false }; return; }
    pending = personEmail;
    try {
      await run();
      notice = { email: personEmail, text: done, ok: true };
      await load();
    } catch (err) {
      notice = { email: personEmail, text: (err as Error).message, ok: false };
    } finally {
      pending = "";
    }
  }

  const toggle = (p: People["users"][number]) =>
    act(p.email, () => setDisabled(admin.token.trim(), p.email, !p.disabled), p.disabled ? `${p.email} can sign in again.` : `${p.email} is disabled; their sessions ended.`);
  const revoke = (personEmail: string, id: string, provider: string) => {
    if (!confirm(`Revoke ${personEmail}'s ${provider} passkey? Their sessions end now.`)) return;
    act(personEmail, () => revokePasskey(admin.token.trim(), personEmail, id), `${provider} passkey revoked; ${personEmail}'s sessions ended.`);
  };

  const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "—");
  const active = (p: People["users"][number]) => p.passkeys.filter((k) => !k.revoked);
  const activeKeys = $derived(people?.users.reduce((n, p) => n + active(p).length, 0) ?? 0);
  const disabledCount = $derived(people?.users.filter((p) => p.disabled).length ?? 0);
  const expires = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
</script>

<PageHeader eyebrow="Identity" title="People & passkeys" intro="Who can sign in through Switchboard and with which passkeys. Invite someone, see their devices, and cut access at once: everything the switchboard user command does, from here.">
  <Button variant="outline" size="sm" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
</PageHeader>

{#if people && !people.enabled}
  <Panel label="People" title="Passkeys are not enabled on this edge">
    <p class="text-muted-foreground text-sm">Add <code class="font-mono">auth.passkeys</code> (<code class="font-mono">rp_id</code>, <code class="font-mono">login_url</code>) and start Switchboard with <code class="font-mono">-db</code>.</p>
  </Panel>
{:else}
  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Stat label="People" value={people ? people.users.length : "—"} note="Invited by the operator" lamp={people?.users.length ? "on" : "idle"} />
    <Stat label="Active passkeys" value={people ? activeKeys : "—"} note="Each one signs in from one place" lamp={activeKeys ? "on" : "idle"} />
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
      <label class="flex items-center gap-2 text-sm sm:col-span-2"><Checkbox bind:checked={recover} /> They already have a passkey and lost their devices (recovery)</label>
      <div class="flex flex-wrap items-center gap-3 sm:col-span-2">
        <Button type="submit" disabled={inviting}><UserPlusIcon class="size-4" /> {inviting ? "Creating…" : "Create invitation"}</Button>
        {#if inviteError}<p class="text-destructive text-sm" role="alert">{inviteError}</p>{/if}
      </div>
    </form>
    {#if created}
      <div class="border-primary/40 bg-accent/30 mt-5 max-w-3xl rounded-lg border p-4" role="status">
        <p class="text-sm font-semibold">Invitation for {created.email} · expires at {expires(created.expires_at)}</p>
        <p class="text-muted-foreground mb-3 text-xs">Send it over a channel you trust: whoever opens it first gets the passkey. It is shown only now.</p>
        <div class="flex flex-wrap gap-2">
          <Input readonly value={created.link} class="min-w-0 flex-1 font-mono text-xs" onfocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
          <Button variant="outline" onclick={copy}><CopyIcon class="size-4" /> {copied ? "Copied" : "Copy link"}</Button>
          {#if typeof navigator !== "undefined" && "share" in navigator}<Button variant="outline" onclick={share}><ShareIcon class="size-4" /> Share</Button>{/if}
        </div>
      </div>
    {/if}
  </Panel>

  {#if error}
    <p class="text-destructive text-sm">People could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !people}
    <div class="space-y-3">{#each [1, 2] as i (i)}<Skeleton class="h-24 w-full" />{/each}</div>
  {:else if people.users.length === 0}
    <Panel label="People" title="Nobody invited yet"><p class="text-muted-foreground text-sm">Create the first invitation above.</p></Panel>
  {:else}
    <div class="space-y-6">
      {#each people.users as p, i (p.email)}
        <Panel index={String(i + 2).padStart(2, "0")} label={p.disabled ? "Disabled" : "Person"} title={p.name ? `${p.name} · ${p.email}` : p.email}
          sub={`Invited ${day(p.created)} · ${p.groups.length ? "groups " + p.groups.join(", ") : "no groups"} · ${active(p).length} active passkey${active(p).length === 1 ? "" : "s"}`} bodyClass="p-0">
          {#snippet actions()}
            <Button variant="outline" size="sm" disabled={pending === p.email} onclick={() => toggle(p)}>{p.disabled ? "Enable" : "Disable"}</Button>
          {/snippet}
          {#if notice?.email === p.email}
            <p class={["border-b px-5 py-3 text-sm", notice.ok ? "text-foreground" : "text-destructive"]} role="status">{notice.text}</p>
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
                    <p class="text-muted-foreground font-mono text-xs">added {day(k.created)} · last used {day(k.last_used)}{k.revoked ? ` · revoked ${day(k.revoked)}` : ""} · {k.id.slice(0, 8)}</p>
                  </div>
                  {#if !k.revoked}
                    <Button variant="outline" size="sm" class="text-destructive" disabled={pending === p.email} onclick={() => revoke(p.email, k.id.slice(0, 16), k.provider)}>Revoke</Button>
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
