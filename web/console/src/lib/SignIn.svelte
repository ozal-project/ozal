<script lang="ts">
  import { onMount } from "svelte";
  import { admin, tools } from "$lib/adminToken.svelte";
  import { adminState, signInWithPasskey, explainPasskeyError } from "$lib/passkey";
  import { Button } from "$lib/components/ui/button";
  import CopyCommand from "$lib/CopyCommand.svelte";

  // The one way in, used by the console gate and the Admin menu. The host
  // decides what a login may change: pick the tools, run the command on the
  // host and open the one-use link it prints.
  let { ondone }: { ondone?: () => void } = $props();
  const uid = $props.id();
  const allTools = tools.map((t) => t.id);
  let picked = $state<string[]>([...allTools]);
  let draft = $state("");
  let checking = $state(false);
  let error = $state("");
  // null: the edge keeps no administrator directory; false: nobody enrolled yet.
  let enrolled = $state<boolean | null>(null);
  let waiting = $state(false);
  const onLocalhost = location.hostname === "localhost";
  onMount(() => { void adminState(AbortSignal.timeout(10_000)).then((state) => { enrolled = state ? state.enrolled : null; }).catch(() => {}); });

  async function passkey() {
    if (waiting) return;
    waiting = true; error = "";
    try {
      const session = await signInWithPasskey();
      admin.token = session.token; admin.name = session.name; admin.expires = session.expires; admin.tools = session.tools; admin.notice = "";
      ondone?.();
    } catch (e) { error = explainPasskeyError(e); }
    finally { waiting = false; }
  }
  const command = $derived(`switchboard console login -name YOUR_NAME${picked.length === allTools.length ? "" : ` -tools ${picked.join(",") || "TOOL"}`}`);

  async function unlock(e: SubmitEvent) {
    e.preventDefault();
    const token = draft.trim();
    if (!token || checking) return;
    checking = true; error = "";
    try {
      const response = await fetch("/_/api/operators/session", { headers: { Authorization: `Bearer ${token}` }, cache: "no-store", signal: AbortSignal.timeout(10_000) });
      if (!response.ok) throw new Error("This credential could not be verified. Check it and try again.");
      const session: { kind: string; name?: string; expires?: string; tools: string[] } = await response.json();
      admin.token = token; admin.name = session.name || (session.kind === "sites" ? "Sites token" : "");
      admin.expires = session.expires || ""; admin.tools = session.tools; admin.notice = "";
      draft = "";
      ondone?.();
    } catch (e) { error = (e as Error).message; }
    finally { checking = false; }
  }
</script>

{#if error || admin.notice}<p class="text-destructive mb-4 text-sm" role="alert">{error || admin.notice}</p>{/if}
{#if enrolled}
  <Button class="w-full" onclick={passkey} disabled={waiting || !onLocalhost}>{waiting ? "Waiting for your device…" : "Sign in with your passkey"}</Button>
{:else if enrolled === false}
  <p class="text-sm font-medium">No administrator exists yet.</p>
  <p class="text-muted-foreground mt-1 mb-3 text-sm">On the host, run this once. It prints a link that opens here and asks for your name and email, then creates your passkey.</p>
  <CopyCommand command="switchboard console enrol" label="Copy enrol command" />
{/if}
{#if enrolled !== null && !onLocalhost}
  <p class="border-warning/40 bg-warning/[0.06] mt-3 rounded-md border px-3 py-2 text-xs" role="status">Passkeys need this console opened as <span class="font-mono">localhost</span> (for example http://localhost:{location.port || "9091"}), not by IP address.</p>
{/if}
<details class="mt-5 text-sm" open={enrolled === null}>
  <summary class="text-muted-foreground hover:text-foreground cursor-pointer">Other ways in: host login or admin token</summary>
  <fieldset class="mt-3">
    <legend class="plate mb-2">Tools for a host login</legend>
    <div class="grid grid-cols-2 gap-x-3 gap-y-2">
      {#each tools as t (t.id)}
        <label class="flex items-center gap-2 text-sm" title={t.can}><input type="checkbox" value={t.id} bind:group={picked} class="accent-[var(--primary)]" />{t.label}</label>
      {/each}
    </div>
  </fieldset>
  <div class="mt-3"><CopyCommand {command} label="Copy login command" /></div>
  <form class="mt-4 flex items-center gap-2" onsubmit={unlock}>
    <label class="sr-only" for={`${uid}-token`}>Admin token</label>
    <input id={`${uid}-token`} bind:value={draft} type="password" autocomplete="off" spellcheck="false" placeholder="Or paste the admin token"
      class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
    <Button type="submit" size="sm" disabled={!draft.trim() || checking}>{checking ? "Checking…" : "Unlock"}</Button>
  </form>
</details>
