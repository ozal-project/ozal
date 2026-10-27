<script lang="ts">
  import { admin } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import { enrolWithPasskey, explainPasskeyError } from "$lib/passkey";

  // The host opened this door once. The person says who they are and creates
  // the passkey they will use from now on.
  let name = $state("");
  let email = $state("");
  let busy = $state(false);
  let error = $state("");
  const uid = $props.id();

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy || !admin.enrol) return;
    busy = true; error = "";
    try {
      const session = await enrolWithPasskey(admin.enrol, name.trim(), email.trim());
      admin.token = session.token; admin.name = session.name; admin.expires = session.expires; admin.tools = session.tools;
      admin.enrol = ""; admin.notice = "";
    } catch (e) { error = explainPasskeyError(e); }
    finally { busy = false; }
  }
</script>

<form onsubmit={submit} class="grid gap-4">
  {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
  <div class="grid gap-1.5">
    <label for={`${uid}-name`} class="text-sm font-medium">Full name</label>
    <input id={`${uid}-name`} bind:value={name} required maxlength="80" autocomplete="name"
      class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
  </div>
  <div class="grid gap-1.5">
    <label for={`${uid}-email`} class="text-sm font-medium">Email</label>
    <input id={`${uid}-email`} bind:value={email} required type="email" maxlength="254" autocomplete="email"
      class="border-input bg-background focus-visible:ring-ring h-10 rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none" />
    <p class="text-muted-foreground text-xs">It names you in the audit log and is how the host recovers your access. No message is sent.</p>
  </div>
  <Button type="submit" disabled={busy || !name.trim() || !email.trim()}>{busy ? "Waiting for your device…" : "Create my passkey"}</Button>
</form>
