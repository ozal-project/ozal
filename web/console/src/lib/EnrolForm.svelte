<script lang="ts">
  import { admin, startSession } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import { onMount } from "svelte";
  import { enrolInfo, enrolWithPasskey, explainPasskeyError, type EnrolInfo } from "$lib/passkey";

  // The host opened this door once. The person says who they are and creates
  // the passkey they will use from now on.
  let name = $state("");
  let email = $state("");
  let busy = $state(false);
  let error = $state("");
  const uid = $props.id();
  // What the link already fixes. Null until known (or if it cannot be read: the
  // form then asks for everything and the host decides).
  let info = $state<EnrolInfo | null>(null);
  onMount(() => {
    void enrolInfo(admin.enrol).then((got) => {
      info = got;
      if (got.name) name = got.name;
      if (got.email) email = got.email;
    }).catch((e) => { error = (e as Error).message; });
  });
  const knownEmail = $derived(info?.kind === "invite" || info?.kind === "recover");
  const recovery = $derived(info?.kind === "recover");
  const device = $derived(info?.kind === "device");

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !admin.enrol) return;
    busy = true; error = "";
    try {
      const session = await enrolWithPasskey(admin.enrol, name.trim(), email.trim());
      startSession(session, true);
    } catch (e) { error = explainPasskeyError(e); }
    finally { busy = false; }
  }
</script>

<form onsubmit={submit} class="grid gap-4">
  {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
  {#if device}
    <p class="text-sm">This adds a passkey from this browser for <span class="font-medium">{info?.name || info?.email}</span> ({info?.email}). Your other passkeys keep working.</p>
  {:else if recovery}
    <p class="text-sm">This link replaces the passkeys of <span class="font-medium">{info?.name || info?.email}</span> ({info?.email}). The old ones stop working.</p>
  {:else}
    <div class="grid gap-1.5">
      <label for={`${uid}-name`} class="text-sm font-medium">Full name</label>
      <input id={`${uid}-name`} bind:value={name} required maxlength="80" autocomplete="name"
        class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
    </div>
    {#if knownEmail}
      <p class="text-muted-foreground text-sm">Invited as <span class="text-foreground font-medium">{info?.email}</span></p>
    {:else}
      <div class="grid gap-1.5">
        <label for={`${uid}-email`} class="text-sm font-medium">Email</label>
        <input id={`${uid}-email`} bind:value={email} required type="email" maxlength="254" autocomplete="email"
          class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
        <p class="text-muted-foreground text-xs">It names you in the audit log and is how the host recovers your access. No message is sent.</p>
      </div>
    {/if}
  {/if}
  <Button type="submit" disabled={busy || (!recovery && !device && (!name.trim() || !email.trim()))}>{busy ? "Waiting for your device…" : "Create my passkey"}</Button>
</form>
