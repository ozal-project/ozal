<script lang="ts">
  import { onMount } from "svelte";
  import { admin, startSession } from "$lib/adminToken.svelte";
  import { adminState, signInWithPasskey, explainPasskeyError } from "$lib/passkey";
  import { Button } from "$lib/components/ui/button";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import EnrolForm from "$lib/EnrolForm.svelte";
  import HostLogin from "$lib/auth/HostLogin.svelte";
  import Logo from "$lib/brand/Logo.svelte";

  // The only thing a signed-out tab shows. It says what this is and how to get
  // in, and nothing about what is inside.
  let mode = $state<"passkey" | "host">("passkey");
  // null: the edge keeps no administrator directory (host login only);
  // false: nobody enrolled yet; true: sign in with a passkey.
  let enrolled = $state<boolean | null | undefined>(undefined);
  let enrolCommand = $state("sudo ozal console enrol");
  let waiting = $state(false);
  let error = $state("");
  const onLocalhost = ["ozal.localhost", "switchboard.localhost", "localhost"].includes(location.hostname);
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

  const heading = $derived(admin.enrol ? "Create your passkey" : mode === "host" ? "Host login" : enrolled === false ? "Set up the first administrator" : "Sign in");
</script>

<main class="auth-shell">
  <div class="auth-column">
    <header class="auth-mark">
      <Logo size={72} entrance />
      <div>
        <p class="auth-name">ozal</p>
        <p class="auth-sub">Private console</p>
      </div>
    </header>

    <section class="auth-card" aria-labelledby="auth-title">
      <h1 id="auth-title" class="text-lg font-semibold">{heading}</h1>

      {#if admin.enrol}
        <p class="text-muted-foreground mt-1 mb-5 text-sm">Create the passkey you will sign in with. It stays on this device.</p>
        <EnrolForm />
      {:else if mode === "host"}
        <p class="text-muted-foreground mt-1 mb-5 text-sm">Paste the one-time login printed on the server. It lasts 15 minutes.</p>
        <HostLogin />
      {:else if enrolled === false}
        <p class="text-muted-foreground mt-1 mb-4 text-sm">Paste the setup link the installer printed.</p>
        <form class="flex items-center gap-2" onsubmit={usePasted}>
          <label class="sr-only" for="setup-link">Setup link</label>
          <input id="setup-link" bind:value={pasted} autocomplete="off" spellcheck="false" placeholder="http://ozal.localhost:…#enrol=…"
            class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
          <Button type="submit" size="sm" disabled={!pasted.trim()}>Continue</Button>
        </form>
        {#if pasteError}<p class="text-destructive mt-2 text-xs" role="alert">{pasteError}</p>{/if}
        <details class="mt-5 text-sm">
          <summary class="text-muted-foreground hover:text-foreground cursor-pointer">Lost the link?</summary>
          <p class="text-muted-foreground mt-2 mb-2 text-xs">Run this on the server for a new one:</p>
          <CopyCommand command={enrolCommand} label="Copy enrol command" />
        </details>
      {:else if enrolled}
        {#if admin.notice && !error}<p class="text-muted-foreground mt-1 text-sm" role="status">{admin.notice}</p>
        {:else}<p class="text-muted-foreground mt-1 text-sm">Welcome back.</p>{/if}
        {#if error}<p class="text-destructive mt-3 text-sm" role="alert">{error}</p>{/if}
        <Button class="mt-6 w-full" onclick={passkey} disabled={waiting || !onLocalhost}>{waiting ? "Waiting for your device…" : "Sign in with passkey"}</Button>
      {:else}
        <p class="text-muted-foreground mt-2 text-sm" role="status">Checking…</p>
      {/if}

      {#if !onLocalhost && !admin.enrol && mode === "passkey" && enrolled !== undefined}
        <p class="border-warning/40 bg-warning/[0.06] mt-4 rounded-md border px-3 py-2 text-xs" role="status">Open this console as <span class="font-mono">ozal.localhost:{location.port || "9091"}</span>: passkeys do not work on an IP address.</p>
      {/if}
    </section>

    {#if !admin.enrol && enrolled !== undefined}
      <div class="auth-links text-xs">
        {#if mode === "host"}
          {#if enrolled !== null}<button type="button" class="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" onclick={() => { mode = "passkey"; }}>{enrolled === false ? "Back to setup" : "Sign in with passkey"}</button>{/if}
        {:else}
          {#if enrolled}
            <details class="text-muted-foreground w-full text-center">
              <summary class="hover:text-foreground cursor-pointer list-none underline-offset-4 hover:underline">Passkey on another device?</summary>
              <p class="mt-2 leading-relaxed">Sign in where your passkey works, then choose <span class="text-foreground">Sign in on another browser</span> in your account menu.</p>
            </details>
          {/if}
          <button type="button" class="text-muted-foreground hover:text-foreground underline-offset-4 hover:underline" onclick={() => { mode = "host"; }}>{enrolled === false ? "Use a host login instead" : "Use a host login"}</button>
        {/if}
      </div>
    {/if}
  </div>
</main>
