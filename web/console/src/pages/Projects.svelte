<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { githubRepositories, fetchProjects, setupStatus, bindGitHubProject, disconnectProject, type Repository, type Repositories, type Project, type SetupStatus } from "$lib/api";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  let repositories = $state<Repositories | null>(null);
  let projects = $state<Project[]>([]);
  let projectsLoaded = $state(false);
  let setup = $state<SetupStatus | null>(null);
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
  const sites = $derived([...new Set(setup?.routes.map((r) => r.site).filter((s): s is string => !!s) ?? [])]);
  async function load() {
    if (!admin.token.trim()) { askForToken(); return; }
    busy = true; error = "";
    try {
      [repositories, projects, setup] = await Promise.all([githubRepositories(admin.token.trim(), page), fetchProjects(admin.token.trim()), setupStatus(admin.token.trim())]);
      projectsLoaded = true;
    } catch (e) { error = (e as Error).message; }
    finally { busy = false; }
  }
  onMount(() => { if (admin.token.trim()) void load(); });
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
        notice = `Publisher trust saved for ${result.workflow_ref}. ${result.deploy_host_configured ? "Add and review the deploy workflow in that repository, then run its build and public acceptance." : "Prepare the dedicated HTTPS deploy hostname on the host before enabling CI."} No repository files were changed.`;
      }
      selected = null; removing = null; await load();
    } catch (e) { error = (e as Error).message; }
    finally { busy = false; }
  }
</script>
<PageHeader eyebrow="Build" title="CI projects" intro="Choose which repository may publish each static site. Publishing and visitor access are separate permissions.">
  <Button variant="outline" disabled={busy} onclick={load}>{busy ? "Reading…" : "Read projects & GitHub"}</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if notice}<p class="mb-4 text-sm" role="status">{notice}</p>{/if}
{#if repositories && !repositories.configured}
  <Panel label="Optional integration" title="Connect your own GitHub App" class="mb-6">
    <p class="text-muted-foreground text-sm">Install a metadata-read GitHub App on selected repositories, then configure its App ID, installation ID and private-key file reference on this host. See the GitHub setup guide in the project documentation. No token or private key goes into this browser.</p>
    <p class="mt-3 text-sm">Manual project configuration still works. GitHub is not required for manual uploads; your build secrets stay in GitHub and application secrets stay on their host.</p>
  </Panel>
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
<Panel label="Configured projects" title="Who may publish">
  {#if !projectsLoaded && busy}
    <p class="text-muted-foreground text-sm" aria-busy="true">Reading configured projects…</p>
  {:else if error && !projectsLoaded}
    <p class="text-destructive text-sm" role="alert">Configured projects could not be read ({error}). <button class="underline" onclick={load}>Retry</button></p>
  {:else if !projectsLoaded}
    <p class="text-muted-foreground text-sm">Read projects to verify publisher access.</p>
  {:else if projects.length === 0}
    <p class="text-muted-foreground text-sm">No CI projects configured. Manual static uploads can still work.</p>
  {:else}
    <ul class="divide-y">{#each projects as p (p.name)}<li class="py-4"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">{p.name} · {p.site}</h2><Button variant="outline" size="sm" disabled={busy} onclick={() => { removing = p; frozen = null; confirm = true; }}>Disconnect publisher</Button></div><p class="text-muted-foreground mt-2 break-all text-sm">{p.hostnames.join(", ")}</p>{#each p.deployers as d}<p class="mt-2 break-all text-sm">{d.type === "github" ? `Repository ${d.github.repository_id} · ${d.github.ref} · ${d.github.workflow_ref}` : "Project-scoped token configured"}</p>{/each}</li>{/each}</ul>
  {/if}
</Panel>
<ConfirmAction bind:open={confirm} title={removing ? "Disconnect this publisher?" : "Change publisher trust?"} target={removing?.name ?? frozen?.name ?? ""} current={removing ? "Configured deploy trust" : "Current project trust, if any"} requested={removing ? "No deploy trust for this project" : `${frozen?.ref} · ${frozen?.workflow}`} consequence={removing ? "Future deploy authentication for this project stops. Content remains. An already authenticated upload may finish." : "Only the selected repository, owner, ref and workflow will be trusted for this project. Previous token deployers will be removed."} detail="This does not change visitor access, uninstall the GitHub App or change any repository file." confirmLabel={removing ? "Disconnect publisher" : "Save publisher trust"} onConfirm={() => void save()} />
