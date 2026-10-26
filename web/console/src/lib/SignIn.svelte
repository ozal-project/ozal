<script lang="ts">
  import { admin, tools } from "$lib/adminToken.svelte";
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
<fieldset>
  <legend class="plate mb-2">Tools for this login</legend>
  <div class="grid grid-cols-2 gap-x-3 gap-y-2">
    {#each tools as t (t.id)}
      <label class="flex items-center gap-2 text-sm" title={t.can}><input type="checkbox" value={t.id} bind:group={picked} class="accent-[var(--primary)]" />{t.label}</label>
    {/each}
  </div>
</fieldset>
<div class="mt-4"><CopyCommand {command} label="Copy login command" /></div>
<details class="mt-4 text-sm">
  <summary class="text-muted-foreground hover:text-foreground cursor-pointer">Use an admin or Sites token instead</summary>
  <form class="mt-3 flex items-center gap-2" onsubmit={unlock}>
    <label class="sr-only" for={`${uid}-token`}>Admin token</label>
    <input id={`${uid}-token`} bind:value={draft} type="password" autocomplete="off" spellcheck="false" placeholder="Paste the admin token"
      class="border-input bg-background focus-visible:ring-ring h-9 min-w-0 flex-1 rounded-md border px-2.5 font-mono text-xs focus-visible:ring-2 focus-visible:outline-none" />
    <Button type="submit" size="sm" disabled={!draft.trim() || checking}>{checking ? "Checking…" : "Unlock"}</Button>
  </form>
</details>
