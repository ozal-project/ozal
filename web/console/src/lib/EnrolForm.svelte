<script lang="ts">
  import { admin, startSession } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import { onMount } from "svelte";
  import { enrolInfo, enrolWithPasskey, explainPasskeyError, type EnrolInfo } from "$lib/passkey";
  import KeepChoice from "$lib/auth/KeepChoice.svelte";
  import { choices, type Choice } from "$lib/passkeyChoices";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { hostLoginCommand } from "$lib/hostCommand";
  import { startLoginHost } from "$lib/loginHost";

  // loginHost: the edge runs this ceremony on its HTTPS login host (ID-021).
  let { loginHost = "" }: { loginHost?: string } = $props();

  // The host opened this door once. The person says who they are and creates
  // the passkey they will use from now on.
  let name = $state("");
  let email = $state("");
  let busy = $state(false);
  let error = $state("");
  // Where the passkey will live; it steers the browser's prompt.
  let keep = $state<Choice>("device");
  // A prompt that ended without a passkey: for the first administrator, the
  // temporary way in from the host opens.
  let stuck = $state(false);
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
    busy = true; error = ""; stuck = false;
    try {
      if (loginHost) {
        // The link travels inside the transaction; the login host never sees it in a URL.
        location.assign(await startLoginHost({ token: admin.enrol, name: name.trim(), email: email.trim(), hints: choices[keep].hints }));
        return;
      }
      const session = await enrolWithPasskey(admin.enrol, name.trim(), email.trim(), choices[keep].hints);
      startSession(session, true);
    } catch (e) { error = explainPasskeyError(e); stuck = (e as Error)?.name === "NotAllowedError"; }
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
  <KeepChoice bind:choice={keep} onLoginHost={!!loginHost} />
  {#if loginHost}<p class="text-muted-foreground -mt-2 text-xs">You will create it on <span class="text-foreground">{new URL(loginHost).host}</span>, this edge's sign-in address, and come back here.</p>{/if}
  {#if stuck}<p class="text-muted-foreground -mt-2 text-xs" role="status">If the prompt asked for something you do not have, choose another place above and try again.</p>{/if}
  <Button type="submit" disabled={busy || (!recovery && !device && (!name.trim() || !email.trim()))}>{busy ? "Waiting for your device…" : "Create my passkey"}</Button>
  {#if !device && !recovery && info?.kind !== "invite"}
    <!-- Offered before the prompt: while the browser waits for a security key
         this form is hidden behind its dialog, so the way out must already be known. -->
    <details class="text-muted-foreground -mt-2 text-xs" open={stuck}>
      <summary class="hover:text-foreground cursor-pointer">This computer cannot create a passkey?</summary>
      <p class="mt-2 mb-2 leading-relaxed">Close the browser's prompt, then run this on the server, replacing YOUR_NAME, and open the link it prints. It lets you in for a while without creating the administrator account, which still needs a passkey; this setup link stays valid until it expires.</p>
      <CopyCommand command={hostLoginCommand(location.origin)} label="Copy host login command" />
    </details>
  {/if}
</form>
