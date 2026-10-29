<script lang="ts">
  import { admin, startSession, tools } from "$lib/adminToken.svelte";
  import { Button } from "$lib/components/ui/button";
  import CopyCommand from "$lib/CopyCommand.svelte";

  // Break-glass: a one-use link from `console login` on the host, or the
  // host's admin token. The host decides what a login may change.
  const uid = $props.id();
  const allTools = tools.map((t) => t.id);
  let picked = $state<string[]>([...allTools]);
  let draft = $state("");
  let checking = $state(false);
  let error = $state("");
  const command = $derived(`sudo ozal console login -name YOUR_NAME${picked.length === allTools.length ? "" : ` -tools ${picked.join(",") || "TOOL"}`}`);

  async function unlock(e: SubmitEvent) {
    e.preventDefault();
    const token = draft.trim();
    if (!token || checking) return;
    checking = true; error = "";
    try {
      const response = await fetch("/_/api/operators/session", { headers: { Authorization: `Bearer ${token}` }, cache: "no-store", signal: AbortSignal.timeout(10_000) });
      if (!response.ok) throw new Error("This credential could not be verified. Check it and try again.");
      const session: { kind: string; name?: string; expires?: string; tools: string[] } = await response.json();
      startSession({ token, name: session.name || (session.kind === "sites" ? "Sites token" : ""), expires: session.expires, tools: session.tools }, false);
      draft = "";
    } catch (e) { error = (e as Error).message; }
    finally { checking = false; }
  }
</script>

{#if error || admin.notice}<p class="text-destructive mb-4 text-sm" role="alert">{error || admin.notice}</p>{/if}
<fieldset>
  <legend class="plate mb-2">What this login may change</legend>
  <div class="grid grid-cols-2 gap-x-3 gap-y-2">
    {#each tools as t (t.id)}
      <label class="flex items-center gap-2 text-sm" title={t.can}><input type="checkbox" value={t.id} bind:group={picked} class="accent-[var(--primary)]" />{t.label}</label>
    {/each}
  </div>
</fieldset>
<p class="text-muted-foreground mt-4 mb-2 text-sm">Run this on the host, replacing YOUR_NAME, and open the link it prints:</p>
<CopyCommand {command} label="Copy login command" />
<p class="text-muted-foreground mt-2 text-xs">
  Needs <code class="font-mono">ozal</code> on the host's PATH, which the installer and <code class="font-mono">ozalctl</code> set up.
  If it is not found, use the full path to the binary and add <code class="font-mono">-config /path/to/config.json</code>.
</p>
<form class="mt-5 flex items-center gap-2" onsubmit={unlock}>
  <label class="sr-only" for={`${uid}-token`}>Admin token</label>
  <input id={`${uid}-token`} bind:value={draft} type="password" autocomplete="off" spellcheck="false" placeholder="Or paste the admin token"
    class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
  <Button type="submit" size="sm" disabled={!draft.trim() || checking}>{checking ? "Checking…" : "Unlock"}</Button>
</form>
