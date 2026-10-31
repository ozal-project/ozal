<script lang="ts" module>
  export type GitHubSelection = { repository: import("$lib/api").Repository; page: number; branch: string; directory: string; output: string };
</script>

<script lang="ts">
  import { onMount, tick } from "svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { admin } from "$lib/adminToken.svelte";
  import { route, navigate } from "$lib/router.svelte";
  import { leaveForGitHub, clearReturn } from "$lib/githubReturn";
  import { githubRepositories, fetchPublishing, startGitHubConnection, finishGitHubConnection, recordGitHubInstallation, type Repositories } from "$lib/api";
  import LockIcon from "@lucide/svelte/icons/lock";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import SearchIcon from "@lucide/svelte/icons/search";
  import ExternalIcon from "@lucide/svelte/icons/external-link";

  // `changed` runs after this component changes the server's configuration
  // (an installation recorded), so the page reviews against the new revision.
  let { selection = $bindable(null), connection = $bindable(null), changed }: { selection?: GitHubSelection | null; connection?: Repositories | null; changed?: () => unknown } = $props();

  let page = $state(1);
  let busy = $state("");
  let error = $state("");
  let filter = $state("");
  let organization = $state("");
  let form = $state<HTMLFormElement | null>(null);
  let manifest = $state<{ url: string; manifest: string } | null>(null);

  const token = () => admin.token.trim();
  const expected = async () => (await fetchPublishing(token())).config_sha256;

  async function load(next = page) {
    busy = busy || "Asking GitHub…"; error = "";
    try { connection = await githubRepositories(token(), next); page = next; }
    catch (e) { error = (e as Error).message; }
    finally { busy = ""; }
  }

  // Coming back from GitHub: finish whichever step the operator just did
  // there, then clear the parameters so a reload does not repeat it.
  onMount(async () => {
    const q = new URLSearchParams(location.search);
    const code = q.get("code"), returned = q.get("state");
    if (q.size) { history.replaceState(null, "", "/applications"); route.search = ""; clearReturn(); }
    try {
      if (code && returned) {
        busy = "Saving the new GitHub App…";
        const app = await finishGitHubConnection(token(), code, returned, await expected());
        busy = `Created ${app.slug}. Opening GitHub to choose its repositories…`;
        leaveForGitHub("/applications");
        location.assign(app.install_url);
        return;
      }
      if (q.has("setup_action") || q.has("installation_id")) {
        busy = "Checking where the App was installed…";
        await recordGitHubInstallation(token(), await expected());
        await changed?.();
      }
    } catch (e) { error = (e as Error).message; busy = ""; }
    await load(1);
  });

  async function connect() {
    busy = "Preparing the GitHub App…"; error = "";
    try {
      manifest = await startGitHubConnection(token(), location.origin, organization.trim());
      leaveForGitHub("/applications");
      // GitHub reads the manifest from a form posted by this browser; no
      // credential leaves this server in the request.
      await tick();
      form?.submit();
    } catch (e) { error = (e as Error).message; busy = ""; }
  }

  async function checkInstallation() {
    busy = "Checking where the App was installed…"; error = "";
    try { await recordGitHubInstallation(token(), await expected()); await changed?.(); await load(1); }
    catch (e) { error = (e as Error).message; busy = ""; }
  }

  function choose(repository: GitHubSelection["repository"]) {
    selection = { repository, page, branch: repository.default_branch, directory: selection?.directory ?? ".", output: selection?.output ?? "dist" };
  }

  const shown = $derived((connection?.repositories ?? []).filter((r) => r.full_name.toLowerCase().includes(filter.trim().toLowerCase())));
  const hostCommand = "ozal github connect -config /etc/ozal/config.json \\\n  -app-id APP_ID -installation-id INSTALLATION_ID -key file:/etc/ozal/github-app.pem";
</script>

<div class="grid gap-3" aria-busy={!!busy}>
  <span class="text-sm font-medium">Your repository</span>

  {#if busy && !connection}
    <p class="text-muted-foreground text-sm" role="status">{busy}</p>
  {:else if connection && !connection.configured}
    {#if connection.connect_available}
      <div class="bg-muted/40 grid gap-3 rounded-lg border p-4 text-sm">
        <p>Connect GitHub once so Ozal can list your repositories and add the publishing workflow for you.</p>
        <ul class="text-muted-foreground grid gap-1 text-xs">
          <li>· Ozal creates its own private GitHub App in your account. You approve it on GitHub.</li>
          <li>· You then choose which repositories it can see. It can open pull requests there; it never merges or builds.</li>
          <li>· Its key stays on this server. Nothing about GitHub is kept in this browser.</li>
        </ul>
        <details class="text-xs">
          <summary class="text-muted-foreground cursor-pointer">Create it in an organization instead</summary>
          <Label for="gh-org" class="mt-2 mb-1 text-xs">Organization</Label>
          <Input id="gh-org" bind:value={organization} placeholder="my-org" autocomplete="off" spellcheck="false" class="font-mono" />
        </details>
        <div><Button type="button" disabled={!!busy} onclick={connect}>{busy || "Connect GitHub"}</Button></div>
      </div>
    {:else}
      <div class="bg-muted/40 grid gap-2 rounded-lg border p-4 text-sm">
        <p>GitHub is not connected, and this server cannot connect it from here: it was started without its state database (<code class="font-mono text-xs">-db</code>).</p>
        <p class="text-muted-foreground text-xs">Create a private GitHub App with Metadata read and Contents, Workflows and Pull requests write, install it on your repositories, put its key in a file only Ozal can read, then run on the host:</p>
        <CopyCommand command={hostCommand} label="Copy command" />
      </div>
    {/if}
  {:else if connection && !connection.installed}
    <div class="bg-muted/40 grid gap-3 rounded-lg border p-4 text-sm">
      <p>The App <span class="font-mono text-xs">{connection.app}</span> exists{connection.owner ? ` in ${connection.owner}` : ""}. Now choose which repositories it can see.</p>
      <div class="flex flex-wrap gap-2">
        <Button type="button" href={connection.install_url} disabled={!!busy} onclick={() => leaveForGitHub("/applications")}>Choose repositories on GitHub</Button>
        <Button type="button" variant="outline" disabled={!!busy} onclick={checkInstallation}>{busy || "I've done it, check again"}</Button>
      </div>
    </div>
  {:else if connection}
    <p class="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
      <span class="lamp lamp-on" aria-hidden="true"></span>
      <span>GitHub connected{connection.owner ? ` · ${connection.owner}` : ""}{connection.all_repositories ? " · every repository" : connection.total !== undefined ? ` · ${connection.total} ${connection.total === 1 ? "repository" : "repositories"}` : ""}</span>
      <a class="text-foreground underline underline-offset-4" href="/integrations" onclick={(e) => { e.preventDefault(); navigate("/integrations"); }}>Manage</a>
    </p>
    {#if connection.all_repositories}
      <p class="border-warning/40 bg-warning/[0.06] rounded-md border px-3 py-2 text-xs">The App can see every repository in {connection.owner ?? "the account"}, private ones included. Ozal writes only to the one you choose, but keep the App to the repositories that publish websites.{#if connection.manage_url} <a class="inline-flex items-center gap-1 underline underline-offset-4" href={connection.manage_url} onclick={() => leaveForGitHub("/applications")}>Choose repositories on GitHub <ExternalIcon class="size-3" /></a>{/if}</p>
    {/if}
    <div class="grid gap-2">
      <div class="relative">
        <SearchIcon class="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
        <Input bind:value={filter} placeholder="Find a repository" aria-label="Find a repository" class="pl-8" autocomplete="off" spellcheck="false" />
      </div>
      <div class="max-h-56 overflow-y-auto rounded-lg border" role="radiogroup" aria-label="Repository">
        {#each shown as repo (repo.id)}
          <label class="hover:bg-accent/50 has-[:checked]:bg-primary/[0.07] flex cursor-pointer items-center gap-3 border-b px-3 py-2 text-sm last:border-b-0">
            <input type="radio" name="gh-repo" class="accent-primary" checked={selection?.repository.id === repo.id} onchange={() => choose(repo)} />
            <span class="min-w-0 flex-1 truncate font-mono text-xs">{repo.full_name}</span>
            {#if repo.private}<LockIcon class="text-muted-foreground size-3.5" aria-label="Private repository" />{:else}<GlobeIcon class="text-muted-foreground size-3.5" aria-label="Public repository" />{/if}
          </label>
        {:else}
          <p class="text-muted-foreground px-3 py-3 text-sm">{connection.repositories.length ? "No repository matches." : "The App can see no repository yet. Add one in its settings on GitHub."}</p>
        {/each}
      </div>
      {#if page > 1 || connection.more}
        <div class="flex items-center gap-2 text-xs">
          <Button type="button" size="sm" variant="ghost" disabled={page <= 1 || !!busy} onclick={() => load(page - 1)}>Previous</Button>
          <span class="text-muted-foreground">Page {page}</span>
          <Button type="button" size="sm" variant="ghost" disabled={!connection.more || !!busy} onclick={() => load(page + 1)}>Next</Button>
        </div>
      {/if}
    </div>
    {#if selection}
      <div class="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
        <div class="grid gap-1.5">
          <Label for="gh-branch">Branch that publishes</Label>
          <Input id="gh-branch" required bind:value={selection.branch} class="font-mono" autocomplete="off" spellcheck="false" />
        </div>
      </div>
      <details class="text-muted-foreground text-xs">
        <summary class="cursor-pointer">Build: <span class="text-foreground font-mono">npm ci && npm run build</span> in <span class="text-foreground font-mono">{selection.directory}</span>, publishes <span class="text-foreground font-mono">{selection.output}</span></summary>
        <div class="mt-2 grid gap-3 sm:grid-cols-2">
          <div class="grid gap-1.5"><Label for="gh-dir" class="text-xs">Folder with package.json</Label><Input id="gh-dir" required bind:value={selection.directory} class="font-mono" /></div>
          <div class="grid gap-1.5"><Label for="gh-out" class="text-xs">Folder the build produces</Label><Input id="gh-out" required bind:value={selection.output} class="font-mono" /></div>
        </div>
        <p class="mt-2">The build runs in GitHub Actions, never on this server. For another build tool, edit the workflow in the pull request.</p>
      </details>
    {/if}
    {#if !connection.workflow_available}
      <p class="border-warning/40 bg-warning/[0.06] rounded-md border px-3 py-2 text-xs">This server has no deploy endpoint for GitHub Actions (<code class="font-mono">pages.deploy</code>), so Ozal can trust the repository but cannot add its workflow. You would add it yourself.</p>
    {/if}
  {/if}
  {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
</div>

{#if manifest}
  <form bind:this={form} method="post" action={manifest.url} class="hidden">
    <input type="hidden" name="manifest" value={manifest.manifest} />
  </form>
{/if}
