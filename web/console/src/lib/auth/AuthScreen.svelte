<script lang="ts">
  import { onMount } from "svelte";
  import { admin, startSession } from "$lib/adminToken.svelte";
  import { adminState, signInWithPasskey, explainPasskeyError } from "$lib/passkey";
  import { Button } from "$lib/components/ui/button";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import EnrolForm from "$lib/EnrolForm.svelte";
  import HostLogin from "$lib/auth/HostLogin.svelte";
  import Wordmark from "$lib/brand/Wordmark.svelte";
  import Patchbay from "$lib/brand/Patchbay.svelte";

  // The only thing a signed-out tab shows. It says what this is and how to get
  // in, and nothing about what is inside.
  let mode = $state<"passkey" | "host">("passkey");
  // null: the edge keeps no administrator directory (host login only);
  // false: nobody enrolled yet; true: sign in with a passkey.
  let enrolled = $state<boolean | null | undefined>(undefined);
  let enrolCommand = $state("sudo switchboard console enrol");
  let waiting = $state(false);
  let error = $state("");
  const onLocalhost = location.hostname === "localhost";
  let pasted = $state("");
  let pasteError = $state("");
  // The setup link can be pasted whole (or just its token) instead of opened.
  function usePasted(e: SubmitEvent) {
    e.preventDefault();
    const text = pasted.trim();
    const token = text.includes("enrol=") ? new URLSearchParams(text.slice(text.indexOf("#") + 1)).get("enrol") ?? "" : text;
    if (!/^[A-Za-z0-9_-]{20,128}$/.test(token)) { pasteError = "That does not look like a setup link."; return; }
    pasted = ""; pasteError = ""; admin.enrol = token;
  }

  onMount(() => {
    void adminState(AbortSignal.timeout(10_000)).then((state) => {
      enrolled = state ? state.enrolled : null;
      if (state?.enrol_command) enrolCommand = state.enrol_command;
      if (enrolled === null) mode = "host";
    }).catch(() => { enrolled = null; mode = "host"; });
  });

  async function passkey() {
    if (waiting) return;
    waiting = true; error = "";
    try {
      const session = await signInWithPasskey();
      startSession(session, true);
    } catch (e) { error = explainPasskeyError(e, "sign-in"); }
    finally { waiting = false; }
  }

  const heading = $derived(admin.enrol ? "Create your passkey" : mode === "host" ? "Sign in from the host" : enrolled === false ? "Set up the first administrator" : "Sign in");
</script>

<main class="relative flex min-h-screen items-center justify-center overflow-hidden px-5 py-12">
  <Patchbay />
  <div aria-hidden="true" class="pointer-events-none absolute inset-0 bg-[radial-gradient(60%_50%_at_50%_0%,color-mix(in_oklab,var(--primary)_14%,transparent),transparent)]"></div>
  <div class="animate-in fade-in-0 slide-in-from-bottom-3 relative w-full max-w-md duration-500 motion-reduce:animate-none">
    <div class="mb-8 flex justify-center"><Wordmark animated /></div>
    <div class="panel p-8">
      <h1 class="text-xl font-semibold">{heading}</h1>

      {#if admin.enrol}
        <p class="text-muted-foreground mt-1 mb-5 text-sm">This link was opened for you. Your passkey stays on your device; from now on you sign in with it.</p>
        <EnrolForm />
      {:else if mode === "host"}
        <p class="text-muted-foreground mt-1 mb-5 text-sm">For the host's operator when no passkey is available. The login is issued on the host and lasts 15 minutes.</p>
        <HostLogin />
        {#if enrolled !== null}<button type="button" class="text-muted-foreground hover:text-foreground mt-6 text-sm underline-offset-4 hover:underline" onclick={() => { mode = "passkey"; }}>{enrolled === false ? "Back to setup" : "Back to passkey sign-in"}</button>{/if}
      {:else if enrolled === false}
        <p class="text-muted-foreground mt-1 mb-4 text-sm">Open the setup link the installer printed, or paste it here.</p>
        <form class="flex items-center gap-2" onsubmit={usePasted}>
          <label class="sr-only" for="setup-link">Setup link</label>
          <input id="setup-link" bind:value={pasted} autocomplete="off" spellcheck="false" placeholder="http://localhost:…/overview#enrol=…"
            class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
          <Button type="submit" size="sm" disabled={!pasted.trim()}>Continue</Button>
        </form>
        {#if pasteError}<p class="text-destructive mt-2 text-xs" role="alert">{pasteError}</p>{/if}
        <details class="mt-5 text-sm">
          <summary class="text-muted-foreground hover:text-foreground cursor-pointer">No link?</summary>
          <p class="text-muted-foreground mt-2 mb-2 text-xs">The host keeps a fresh one in a root-only file at every start, and this command prints a new one:</p>
          <CopyCommand command={enrolCommand} label="Copy enrol command" />
        </details>
      {:else if enrolled}
        <p class="text-muted-foreground mt-1 mb-6 text-sm">Use the passkey you created for this console.</p>
        {#if error || admin.notice}<p class="text-destructive mb-4 text-sm" role="alert">{error || admin.notice}</p>{/if}
        <Button class="w-full" onclick={passkey} disabled={waiting || !onLocalhost}>{waiting ? "Waiting for your device…" : "Sign in with your passkey"}</Button>
        <p class="text-muted-foreground mt-4 text-xs leading-relaxed">A passkey works in the browser or password manager that saved it. To use this browser too, sign in where it works and choose <span class="text-foreground">Sign in on another browser</span> in your menu.</p>
      {:else}
        <p class="text-muted-foreground mt-2 text-sm" role="status">Checking…</p>
      {/if}

      {#if !onLocalhost && !admin.enrol && mode === "passkey" && enrolled !== undefined}
        <p class="border-warning/40 bg-warning/[0.06] mt-4 rounded-md border px-3 py-2 text-xs" role="status">Passkeys need this console opened as <span class="font-mono">localhost</span> (for example http://localhost:{location.port || "9091"}), not by IP address.</p>
      {/if}
    </div>
    {#if !admin.enrol && mode === "passkey" && enrolled !== undefined}
      <p class="mt-5 text-center"><button type="button" class="text-muted-foreground hover:text-foreground text-xs underline-offset-4 hover:underline" onclick={() => { mode = "host"; }}>{enrolled === false ? "Use a host login instead" : "No passkey? Use a host login"}</button></p>
    {/if}
  </div>
</main>
