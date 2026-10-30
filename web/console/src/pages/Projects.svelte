<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { githubRepositories, fetchPublishing, bindGitHubProject, disconnectProject, type Repository, type Repositories, type Project, type PublishingState } from "$lib/api";
  import { LatestRequest } from "$lib/latestRequest";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import WorkflowProposal from "$lib/WorkflowProposal.svelte";
  let { embedded = false }: { embedded?: boolean } = $props();
  let repositories = $state<Repositories | null>(null);
  let projects = $state<Project[]>([]);
  let projectsLoaded = $state(false);
  let setup = $state<PublishingState | null>(null);
  const reads = new LatestRequest();
  let page = $state(1);
  let busy = $state(false);
  let error = $state("");
  let notice = $state("");
  let selected = $state<Repository | null>(null);
  let name = $state("");
  let site = $state("");
  let ref = $state("");
  let workflow = $state("deploy.yml");
  let confirm = $state(false);
  let removing = $state<Project | null>(null);
  let frozen = $state<{ name: string; site: string; repository_id: number; page: number; ref: string; workflow: string; expected: string } | null>(null);
  const sites = $derived(setup?.sites ?? []);
  async function load() {
    if (!admin.token.trim()) { askForToken(); return; }
    const run = reads.start();
    busy = true; error = "";
    try {
      const [repos, state] = await Promise.all([githubRepositories(admin.token.trim(), page, run.signal), fetchPublishing(admin.token.trim(), run.signal)]);
      if (!run.isCurrent()) return;
      repositories = repos; setup = state; projects = state.projects;
      projectsLoaded = true;
    } catch (e) { if (run.isCurrent()) error = (e as Error).message; }
    finally { if (run.isCurrent()) busy = false; run.finish(); }
  }
  onMount(() => { if (admin.token.trim()) void load(); return () => reads.cancel(); });
  // The trusted workflow claim reads "owner/repo/.github/workflows/file@ref":
  // show it as a repository, a branch and a file instead of numeric IDs.
  function trusted(workflowRef: string | undefined, ref: string) {
    const [path, at] = (workflowRef ?? "").split("@");
    const cut = path.indexOf("/.github/workflows/");
    return {
      repository: cut > 0 ? path.slice(0, cut) : undefined,
      workflow: cut > 0 ? path.slice(cut + "/.github/workflows/".length) : undefined,
      branch: (at || ref).replace(/^refs\/heads\//, "") || undefined,
    };
  }
  function select(repo: Repository) { selected = repo; name = repo.full_name.split("/")[1].toLowerCase().replace(/[^a-z0-9-]/g, "-"); ref = `refs/heads/${repo.default_branch}`; site = sites[0] ?? ""; }
  function prepare(e: SubmitEvent) {
    e.preventDefault(); if (!selected || !setup) return;
    removing = null; frozen = { name, site, repository_id: selected.id, page, ref, workflow, expected: setup.config_sha256 }; confirm = true;
  }
  async function save() {
    if (!setup) return;
    busy = true; error = "";
    try {
      if (removing) { await disconnectProject(admin.token.trim(), removing.name, setup.config_sha256); notice = "Publisher trust removed. The live site and its retained deployments remain. A deployment already authenticated may finish; verify Activity."; }
      else if (frozen) {
        const result = await bindGitHubProject(admin.token.trim(), frozen);
        notice = `Publisher trust saved for ${result.workflow_ref}. ${result.deploy_host_configured ? "Prepare its publishing workflow below to review a pull request, then verify its first build and publication." : "Prepare the dedicated HTTPS deploy hostname on the host before enabling CI."} No repository files were changed.`;
      }
      selected = null; removing = null; await load();
    } catch (e) { error = (e as Error).message; }
    finally { busy = false; }
  }
</script>
{#if embedded}
  <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
    <div class="max-w-2xl"><h2 class="text-lg font-semibold tracking-tight">Publishing from GitHub</h2><p class="text-muted-foreground mt-1 text-sm">Websites that update themselves: each push to the chosen branch builds in GitHub Actions and publishes here. Ozal trusts exactly one repository, branch and workflow per website; nothing else can publish it.</p></div>
    <Button variant="outline" size="sm" aria-label="Refresh publishing" disabled={busy} onclick={load}>{busy ? "Reading…" : "Refresh"}</Button>
  </div>
{:else}
<PageHeader title="Publishing from GitHub" intro="Each push to the chosen branch builds in GitHub Actions and publishes here. Ozal trusts exactly one repository, branch and workflow per website.">
  <Button variant="outline" size="sm" aria-label="Refresh publishing" disabled={busy} onclick={load}>{busy ? "Reading…" : "Refresh"}</Button>
</PageHeader>
{/if}
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if notice}<p class="mb-4 text-sm" role="status">{notice}</p>{/if}
{#if repositories && !repositories.configured}
  <details class="bg-card mb-6 rounded-lg px-5 py-4 text-sm">
    <summary class="cursor-pointer font-medium">Optional: connect a GitHub App <span class="text-muted-foreground font-normal">· to pick repositories here and let Ozal propose the workflow</span></summary>
    <div class="text-muted-foreground mt-3 grid gap-2">
      <p>Without it, websites that already publish keep working; you add the workflow file to the repository yourself.</p>
      <p>With it, you install your own GitHub App on the repositories you choose and set its App ID, installation ID and private-key file in this server's configuration (see the GitHub guide in the documentation). No GitHub token or key passes through this browser.</p>
    </div>
  </details>
{:else if repositories?.configured}
  <Panel label="Repository discovery" title="Repositories this installation can see" class="mb-6">
    <p class="text-muted-foreground mb-3 text-sm">Only selected installation repositories are listed. This does not import GitHub users or teams.</p>
    <ul class="divide-y">{#each repositories.repositories as repo (repo.id)}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><span class="break-all text-sm">{repo.full_name} · {repo.private ? "Private repository" : "Public repository"}</span><Button size="sm" variant="outline" disabled={busy || !sites.length} onclick={() => select(repo)}>Use repository</Button></li>{:else}<li class="py-3 text-sm">No selected repositories are available. Check the App installation and permissions.</li>{/each}</ul>
    {#if !sites.length}<p class="mt-3 text-sm">Add a static website in Applications before binding a publisher.</p>{/if}
    <div class="mt-3 flex gap-3"><Button size="sm" variant="ghost" disabled={page <= 1 || busy} onclick={() => { selected = null; page--; void load(); }}>Previous</Button><span class="self-center text-sm">Page {page}</span><Button size="sm" variant="ghost" disabled={!repositories.more || busy} onclick={() => { selected = null; page++; void load(); }}>Next</Button></div>
  </Panel>
{/if}
{#if selected}
  <Panel label="Publisher trust" title={`Connect ${selected.full_name}`} class="mb-6">
    <form onsubmit={prepare} class="grid max-w-3xl gap-4 sm:grid-cols-2">
      <div><label for="project-name" class="mb-2 block text-sm">Project name</label><Input id="project-name" required pattern="[a-z0-9]+(-[a-z0-9]+)*" bind:value={name} /></div>
      <div><label for="project-site" class="mb-2 block text-sm">Pages site</label><select id="project-site" bind:value={site} required class="bg-background h-9 w-full rounded-md border px-3 text-sm">{#each sites as s}<option value={s}>{s}</option>{/each}</select></div>
      <div><label for="project-ref" class="mb-2 block text-sm">Allowed Git ref</label><Input id="project-ref" required bind:value={ref} /></div>
      <div><label for="project-workflow" class="mb-2 block text-sm">Workflow filename</label><Input id="project-workflow" required bind:value={workflow} /></div>
      <p class="text-muted-foreground sm:col-span-2 text-sm">This replaces this project’s previous publisher trust, including token deployers. It pins repository and owner IDs as well as ref and workflow. It neither builds on the edge nor writes a workflow in GitHub.</p>
      <div class="flex gap-3 sm:col-span-2"><Button type="submit" disabled={busy}>Review publisher</Button><Button type="button" variant="ghost" onclick={() => selected = null}>Cancel</Button></div>
    </form>
  </Panel>
{/if}
<Panel label="Configured projects" title="Websites that publish from GitHub">
  {#if !projectsLoaded && busy}
    <p class="text-muted-foreground text-sm" aria-busy="true">Reading configured projects…</p>
  {:else if error && !projectsLoaded}
    <p class="text-destructive text-sm" role="alert">Configured projects could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !projectsLoaded}
    <p class="text-muted-foreground text-sm">Loading…</p>
  {:else if projects.length === 0}
    <p class="text-muted-foreground text-sm">No website publishes from GitHub yet. You can still publish by uploading files from Applications.</p>
  {:else}
    <ul class="divide-y">
      {#each projects as p (p.name)}
        {@const github = p.deployers.flatMap((d) => (d.type === "github" ? [d.github] : []))}
        <li class="grid gap-2 py-4">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <h3 class="font-medium">{p.site}{#if p.name !== p.site}<span class="text-muted-foreground font-normal"> · project {p.name}</span>{/if}</h3>
              <p class="text-muted-foreground mt-0.5 text-sm break-all">{p.hostnames.length ? p.hostnames.join(", ") : "Not served at any address yet"}</p>
            </div>
            <Button variant="ghost" size="sm" class="text-muted-foreground" disabled={busy} onclick={() => { removing = p; frozen = null; confirm = true; }}>Stop publishing</Button>
          </div>
          {#each github as g (g.repository_id)}
            {@const source = trusted(g.workflow_ref, g.ref)}
            <p class="text-sm">Publishes from <span class="font-mono text-xs">{source.repository ?? `repository #${g.repository_id}`}</span>{#if source.branch}{" · branch "}<span class="font-mono text-xs">{source.branch}</span>{/if}{#if source.workflow}{" · "}<span class="font-mono text-xs">{source.workflow}</span>{/if}</p>
          {/each}
          {#if p.deployers.some((d) => d.type === "token")}<p class="text-sm">Publishes with a project token (for CI outside GitHub).</p>{/if}
          {#if github.length === 1 && repositories?.configured}<WorkflowProposal project={p} {page} />{/if}
        </li>
      {/each}
    </ul>
  {/if}
</Panel>
<ConfirmAction bind:open={confirm} title={removing ? "Stop publishing from GitHub?" : "Change publisher trust?"} target={removing?.name ?? frozen?.name ?? ""} current={removing ? "Configured deploy trust" : "Current project trust, if any"} requested={removing ? "No deploy trust for this project" : `${frozen?.ref} · ${frozen?.workflow}`} consequence={removing ? "Future deploy authentication for this project stops. Content remains. An already authenticated upload may finish." : "Only the selected repository, owner, ref and workflow will be trusted for this project. Previous token deployers will be removed."} detail="This does not change visitor access, uninstall the GitHub App or change any repository file." confirmLabel={removing ? "Stop publishing" : "Save publisher trust"} onConfirm={() => void save()} />
