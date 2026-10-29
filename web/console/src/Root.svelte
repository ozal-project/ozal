<script lang="ts">
  import { onMount } from "svelte";
  import { admin, signOut, startSession } from "$lib/adminToken.svelte";
  import { exchangeHostLogin, takeHostLink } from "$lib/operatorLogin";
  import { renewSession } from "$lib/passkey";
  import AuthScreen from "$lib/auth/AuthScreen.svelte";

  // The console is behind the sign-in, not beside it: without a session the
  // tab shows only the sign-in screen and never downloads the console itself
  // (its pages, their names and its navigation load after authentication).
  const loadConsole = () => import("./Console.svelte");

  // A link pasted into a tab that is already open only changes the fragment,
  // which does not reload the page: take it then too.
  function consumeLink() {
    const link = takeHostLink();
    if (link?.kind === "enrol") { if (!admin.token) admin.enrol = link.token; admin.ready = true; }
    else if (link) {
      void exchangeHostLogin(link.token).then((session) => {
        startSession(session, false);
      }).catch((e) => { admin.notice = (e as Error).message; }).finally(() => { admin.ready = true; });
    } else admin.ready = true;
  }

  // A passkey session stays open while the person uses the console: activity
  // (keys, pointer, scrolling) renews it shortly before it would end. Background
  // refreshes do not count, so an unattended tab still signs out.
  let lastActive = Date.now();
  let renewing = false;
  let warning = $state(false);
  const active = () => { lastActive = Date.now(); warning = false; };
  async function renew() {
    if (renewing || !admin.token || !admin.renewable) return;
    renewing = true;
    const token = admin.token;
    try {
      const answer = await renewSession(token);
      if (admin.token === token && answer.expires) admin.expires = answer.expires;
    } catch { /* the expiry check below ends the session if it really is over */ }
    finally { renewing = false; }
  }
  function tickSession() {
    if (!admin.token || !admin.expires) { warning = false; return; }
    const left = Date.parse(admin.expires) - Date.now();
    if (left <= 0) { signOut(); warning = false; admin.notice = "Your session ended. Sign in again."; return; }
    const recent = Date.now() - lastActive < 5 * 60_000;
    if (admin.renewable && recent && left < 10 * 60_000) void renew();
    warning = left < 2 * 60_000 && !(admin.renewable && recent);
  }

  onMount(() => {
    consumeLink();
    addEventListener("hashchange", consumeLink);
    const events = ["keydown", "pointerdown", "wheel", "touchstart"] as const;
    for (const e of events) addEventListener(e, active, { passive: true });
    const timer = setInterval(tickSession, 5_000);
    return () => {
      clearInterval(timer); removeEventListener("hashchange", consumeLink);
      for (const e of events) removeEventListener(e, active);
    };
  });

  $effect(() => { if (!admin.token) document.title = "Sign in · Ozal"; });
</script>

{#if admin.token}
  {#await loadConsole()}
    <div class="text-muted-foreground flex min-h-screen items-center justify-center text-sm" role="status">Opening the console…</div>
  {:then { default: Console }}
    <div class="animate-in fade-in-0 duration-300 motion-reduce:animate-none"><Console /></div>
    {#if warning}
      <div class="bg-popover border-warning/40 animate-in slide-in-from-bottom-4 fixed right-4 bottom-4 z-50 flex max-w-sm items-center gap-3 rounded-xl border px-4 py-3 text-sm shadow-2xl" role="status">
        <span class="lamp lamp-warn" aria-hidden="true"></span>
        <span class="flex-1">{admin.renewable ? "Your session ends in under two minutes." : "This host login ends in under two minutes; ask for a new one to continue."}</span>
        {#if admin.renewable}<button type="button" class="text-primary font-medium" onclick={() => { active(); void renew(); }}>Stay signed in</button>{/if}
      </div>
    {/if}
  {:catch}
    <div class="flex min-h-screen items-center justify-center p-6 text-sm" role="alert">The console could not be loaded. Reload this page.</div>
  {/await}
{:else if admin.ready}
  <AuthScreen />
{/if}
