<script lang="ts">
  import { onMount, tick } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import SignInFlow from "$lib/SignInFlow.svelte";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { admin, askForToken, can } from "$lib/adminToken.svelte";
  import { navigate, route } from "$lib/router.svelte";
  import { githubRepositories, fetchPublishing, setupStatus, startGitHubConnection, finishGitHubConnection, recordGitHubInstallation, type Repositories, type Project, type SetupStatus } from "$lib/api";
  import { leaveForGitHub, clearReturn } from "$lib/githubReturn";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import ExternalIcon from "@lucide/svelte/icons/external-link";

  // Services Ozal works with on the operator's behalf. None of them signs
  // anyone in: sign-in stays with Ozal (passkeys, or the external login this
  // server is configured with), and the page says so first.
  let status = $state<SetupStatus | null>(null);
  let github = $state<Repositories | null>(null);
  let projects = $state<Project[]>([]);
  let loading = $state(false);
  let busy = $state("");
  let error = $state("");
  let organization = $state("");
  let form = $state<HTMLFormElement | null>(null);
  let manifest = $state<{ url: string; manifest: string } | null>(null);

  const token = () => admin.token.trim();
  const expected = async () => (await fetchPublishing(token())).config_sha256;

  async function load() {
    if (!token()) { askForToken(); return; }
    loading = true; error = "";
    try {
      const [setup, repos, publishing] = await Promise.all([
        setupStatus(token()).catch(() => null),
        can("publishing") ? githubRepositories(token()) : Promise.resolve(null),
        can("publishing") ? fetchPublishing(token()).catch(() => null) : Promise.resolve(null),
      ]);
      status = setup; github = repos; projects = publishing?.projects ?? [];
    } catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  }

  // Back from GitHub after starting here: finish the step done there, clear
  // its parameters so a reload does not repeat it, then show the result.
  onMount(async () => {
    if (!token()) return;
    const q = new URLSearchParams(location.search);
    const code = q.get("code"), returned = q.get("state");
    if (q.size) { history.replaceState(null, "", "/integrations"); route.search = ""; clearReturn(); }
    try {
      if (code && returned) {
        busy = "Saving the new GitHub App…";
        const app = await finishGitHubConnection(token(), code, returned, await expected());
        busy = `Created ${app.slug}. Opening GitHub to choose its repositories…`;
        leaveForGitHub("/integrations");
        location.assign(app.install_url);
        return;
      }
      if (q.has("setup_action") || q.has("installation_id")) {
        busy = "Checking where the App is installed…";
        await recordGitHubInstallation(token(), await expected());
      }
    } catch (e) { error = (e as Error).message; }
    busy = "";
    await load();
  });

  async function connect() {
    busy = "Preparing the GitHub App…"; error = "";
    try {
      manifest = await startGitHubConnection(token(), location.origin, organization.trim());
      leaveForGitHub("/integrations");
      await tick();
      form?.submit();
    } catch (e) { error = (e as Error).message; busy = ""; }
  }

  async function checkInstallation() {
    busy = "Checking where the App is installed…"; error = "";
    try { await recordGitHubInstallation(token(), await expected()); }
    catch (e) { error = (e as Error).message; }
    busy = "";
    await load();
  }

  const methods = $derived(status?.methods ?? []);
  const fromGitHub = $derived(projects.flatMap((p) => p.deployers.flatMap((d) => d.type === "github" ? [{ project: p, ref: d.github.workflow_ref ?? "", branch: d.github.ref.replace(/^refs\/heads\//, "") }] : [])));
  const repositoryOf = (workflowRef: string) => { const cut = workflowRef.indexOf("/.github/workflows/"); return cut > 0 ? workflowRef.slice(0, cut) : "a trusted repository"; };
  const reach = $derived(github?.all_repositories ? `Every repository in ${github.owner ?? "the account"}${github.total ? ` (${github.total})` : ""}, including private ones and any created later` : github?.total !== undefined ? `${github.total} chosen ${github.total === 1 ? "repository" : "repositories"}` : "The repositories chosen on GitHub");
  const hostCommand = "ozal github connect -config /etc/ozal/config.json \\\n  -app-id APP_ID -installation-id INSTALLATION_ID -key file:/etc/ozal/github-app.pem";
  const disconnectCommand = "sudo ozal github disconnect -config /etc/ozal/config.json";
</script>

{#snippet row(term: string, detail: string, tone: "" | "warn" = "")}
  <div class="grid gap-1 py-2.5 sm:grid-cols-[11rem_minmax(0,1fr)] sm:gap-4">
    <dt class="text-muted-foreground text-xs sm:text-sm">{term}</dt>
    <dd class={["text-sm break-words", tone === "warn" && "text-warning"]}>{detail}</dd>
  </div>
{/snippet}

<PageHeader title="Integrations" intro="Services Ozal works with on your behalf. None of them signs anyone in to your applications.">
  <Button variant="outline" size="sm" aria-label="Refresh integrations" onclick={load} disabled={loading || !!busy}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}

<section class="panel mb-6 p-5" aria-labelledby="sign-in-title">
  <div class="flex items-start gap-3">
    <span class="bg-primary/15 text-signal grid size-9 shrink-0 place-items-center rounded-lg"><KeyIcon class="size-4" /></span>
    <div class="min-w-0">
      <h2 id="sign-in-title" class="text-sm font-medium">Sign-in stays with Ozal</h2>
      <p class="text-muted-foreground mt-1 max-w-2xl text-sm">People open your applications with a passkey on their own device: fingerprint, face or PIN. Ozal checks it, then your rule for that address. Connecting GitHub or a DNS provider never lets anyone sign in with that account.</p>
      {#if status}
        <ul class="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs" aria-label="Sign-in methods on this server">
          <li class="flex items-center gap-2"><span class={["lamp", methods.includes("passkey") ? "lamp-on" : "lamp-warn"]} aria-hidden="true"></span>Passkeys: {methods.includes("passkey") ? "on" : "not enabled"}</li>
          <li class="flex items-center gap-2"><span class={["lamp", methods.includes("oidc") ? "lamp-on" : ""]} aria-hidden="true"></span>External login (OIDC): {methods.includes("oidc") ? "on, as this server's configuration sets it" : "not used"}</li>
        </ul>
        {#if !methods.length}<p class="text-warning mt-2 text-xs">Sign-in is not set up yet, so every application is public. Passkeys are turned on in this server's configuration (<code class="font-mono">auth.passkeys</code>).</p>{/if}
      {/if}
    </div>
  </div>
  <div class="mt-4"><SignInFlow rule="Only the people allowed for that address get through. Others see a “no access” page." /></div>
  {#if can("people") && methods.includes("passkey")}<p class="mt-3 text-xs"><a class="underline underline-offset-4" href="/people" onclick={(e) => { e.preventDefault(); navigate("/people"); }}>Invite people</a> <span class="text-muted-foreground">to give them access: each invitation creates their passkey.</span></p>{/if}
</section>

<Panel label="GitHub" title="GitHub" sub="Lists your repositories and proposes the workflow that publishes a website. Builds stay in GitHub Actions." class="mb-6">
  {#snippet actions()}
    {#if github?.installed}<span class="flex items-center gap-2 text-xs"><span class="lamp lamp-on" aria-hidden="true"></span>Connected</span>
    {:else if github?.configured}<span class="text-warning flex items-center gap-2 text-xs"><span class="lamp lamp-warn" aria-hidden="true"></span>Not installed yet</span>
    {:else if github}<span class="text-muted-foreground text-xs">Not connected</span>{/if}
  {/snippet}
  <div aria-busy={!!busy || loading}>
    {#if !can("publishing")}
      <p class="text-muted-foreground text-sm">Your session does not include CI publishing, so it cannot read or change the GitHub connection.</p>
    {:else if busy}
      <p class="text-muted-foreground text-sm" role="status">{busy}</p>
    {:else if !github}
      <p class="text-muted-foreground text-sm" role="status">{loading ? "Asking GitHub…" : "Loading…"}</p>
    {:else if !github.configured}
      <div class="grid gap-3 text-sm">
        <p>Optional. Without it you can still publish by uploading files, or add the workflow to a repository yourself.</p>
        <ul class="text-muted-foreground grid gap-1 text-xs">
          <li>· Ozal creates its own private GitHub App in your account. You approve it on GitHub.</li>
          <li>· You choose which repositories it can see. It opens pull requests there; it never merges or builds.</li>
          <li>· Its key stays on this server. Nothing about GitHub is kept in this browser.</li>
        </ul>
        {#if github.connect_available}
          <details class="text-xs">
            <summary class="text-muted-foreground cursor-pointer">Create it in an organization instead</summary>
            <Label for="int-gh-org" class="mt-2 mb-1 text-xs">Organization</Label>
            <Input id="int-gh-org" bind:value={organization} placeholder="my-org" autocomplete="off" spellcheck="false" class="max-w-xs font-mono" />
          </details>
          <div><Button type="button" onclick={connect}>Connect GitHub</Button></div>
        {:else}
          <p class="text-muted-foreground text-xs">This server was started without its state database (<code class="font-mono">-db</code>), so GitHub is connected on the host: create a private App with Metadata read and Contents, Workflows and Pull requests write, install it, keep its key in a file only Ozal can read, then run:</p>
          <CopyCommand command={hostCommand} label="Copy command" />
        {/if}
      </div>
    {:else if !github.installed}
      <div class="grid gap-3 text-sm">
        <p>The App <span class="font-mono text-xs">{github.app}</span> exists{github.owner ? ` in ${github.owner}` : ""}. Choose which repositories it can see to finish.</p>
        <div class="flex flex-wrap gap-2">
          <Button type="button" href={github.install_url} onclick={() => leaveForGitHub("/integrations")}>Choose repositories on GitHub</Button>
          <Button type="button" variant="outline" onclick={checkInstallation}>I've done it, check again</Button>
        </div>
      </div>
    {:else}
      {#if github.all_repositories}
        <div class="border-warning/40 bg-warning/[0.06] mb-4 rounded-lg border px-4 py-3 text-sm">
          <p class="font-medium">This App can see every repository in {github.owner ?? "the account"}.</p>
          <p class="text-muted-foreground mt-1 text-xs">Ozal asks GitHub for write access only to the repository a website publishes from, one at a time. But the App's key on this server could reach all of them, private ones included. Keep it to the repositories that publish websites.</p>
          {#if github.manage_url}<Button class="mt-3" size="sm" variant="outline" href={github.manage_url} onclick={() => leaveForGitHub("/integrations")}>Choose repositories on GitHub <ExternalIcon class="size-3.5" /></Button>{/if}
        </div>
      {/if}
      <dl class="divide-y">
        {@render row("App", `${github.app ?? "Connected"}${github.owner ? ` · ${github.owner}` : ""}`)}
        {@render row("Repositories it sees", reach, github.all_repositories ? "warn" : "")}
        {@render row("What it may do", "Read repository metadata. Write contents, workflows and pull requests only to open the publishing pull request. No webhooks or events; it never merges or builds.")}
        {@render row("Workflow pull requests", github.workflow_available ? "Available: adding a website from GitHub opens one for you to review" : "Not available: this server has no deploy endpoint for GitHub Actions (pages.deploy), so you add the workflow yourself", github.workflow_available ? "" : "warn")}
      </dl>
      {#if github.manage_url && !github.all_repositories}
        <p class="mt-3 text-xs"><a class="inline-flex items-center gap-1 underline underline-offset-4" href={github.manage_url} onclick={() => leaveForGitHub("/integrations")}>Change its repositories on GitHub <ExternalIcon class="size-3" /></a></p>
      {/if}

      <h3 class="mt-6 text-sm font-medium">Websites that publish from GitHub</h3>
      {#if fromGitHub.length}
        <ul class="mt-2 divide-y rounded-lg border">
          {#each fromGitHub as item (item.project.name)}
            <li class="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 text-sm">
              <span class="min-w-0"><span class="font-medium">{item.project.hostnames[0] ?? item.project.site}</span> <span class="text-muted-foreground text-xs">from <span class="font-mono">{repositoryOf(item.ref)}</span> · {item.branch}</span></span>
            </li>
          {/each}
        </ul>
        <p class="mt-2 text-xs"><a class="underline underline-offset-4" href="/sites" onclick={(e) => { e.preventDefault(); navigate("/sites"); }}>Versions and publishers in Sites</a></p>
      {:else}
        <p class="text-muted-foreground mt-1 text-sm">None yet. <a class="underline underline-offset-4" href="/applications" onclick={(e) => { e.preventDefault(); navigate("/applications"); }}>Add an application</a> and choose “A website built from GitHub”.</p>
      {/if}

      <details class="mt-6 text-xs">
        <summary class="text-muted-foreground cursor-pointer">Disconnect GitHub</summary>
        <div class="text-muted-foreground mt-2 grid gap-2">
          <p>Disconnecting is done on the host. It removes the App's key from this server; websites keep their publishers until you remove them in Sites. Delete the App on GitHub afterwards if you no longer use it.</p>
          <CopyCommand command={disconnectCommand} label="Copy command" />
        </div>
      </details>
    {/if}
  </div>
</Panel>

<Panel label="DNS" title="DNS provider" sub="Creates the record an address needs, with its own credential on this server.">
  {#snippet actions()}
    {#if status}<span class={["flex items-center gap-2 text-xs", !status.dns.provider && "text-muted-foreground"]}>{#if status.dns.provider}<span class="lamp lamp-on" aria-hidden="true"></span>{/if}{status.dns.provider === "cloudflare" ? "Cloudflare" : "Not set up"}</span>{/if}
  {/snippet}
  <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
    <p class="text-muted-foreground flex min-w-0 items-center gap-2"><GlobeIcon class="size-4 shrink-0" />{status?.dns.provider ? "Addresses under your domains get their DNS record created for you." : "Without one, Ozal shows the record and you create it at your DNS provider."}</p>
    {#if can("applications")}<Button size="sm" variant="outline" onclick={() => navigate("/domains")}>Domains</Button>{/if}
  </div>
</Panel>

{#if manifest}
  <form bind:this={form} method="post" action={manifest.url} class="hidden">
    <input type="hidden" name="manifest" value={manifest.manifest} />
  </form>
{/if}
