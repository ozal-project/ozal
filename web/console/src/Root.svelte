<script lang="ts">
  import { onMount } from "svelte";
  import { admin, signOut } from "$lib/adminToken.svelte";
  import { exchangeHostLogin, takeHostLink } from "$lib/operatorLogin";
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
        admin.token = session.token; admin.name = session.name; admin.expires = session.expires; admin.tools = session.tools ?? []; admin.notice = "";
      }).catch((e) => { admin.notice = (e as Error).message; }).finally(() => { admin.ready = true; });
    } else admin.ready = true;
  }

  onMount(() => {
    consumeLink();
    addEventListener("hashchange", consumeLink);
    const timer = setInterval(() => {
      if (admin.expires && Date.parse(admin.expires) <= Date.now()) { signOut(); admin.notice = "Your session expired. Sign in again."; }
    }, 5_000);
    return () => { clearInterval(timer); removeEventListener("hashchange", consumeLink); };
  });

  $effect(() => { if (!admin.token) document.title = "Sign in · Switchboard"; });
</script>

{#if admin.token}
  {#await loadConsole()}
    <div class="text-muted-foreground flex min-h-screen items-center justify-center text-sm" role="status">Opening the console…</div>
  {:then { default: Console }}
    <div class="animate-in fade-in-0 duration-300 motion-reduce:animate-none"><Console /></div>
  {:catch}
    <div class="flex min-h-screen items-center justify-center p-6 text-sm" role="alert">The console could not be loaded. Reload this page.</div>
  {/await}
{:else if admin.ready}
  <AuthScreen />
{/if}
