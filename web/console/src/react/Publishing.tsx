import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { ExternalLink, RefreshCw } from "lucide-react";
import { bindGitHubProject, disconnectProject, fetchPublishing, githubRepositories, proposeWorkflow, type Project, type PublishingState, type Repositories, type Repository, type Site, type WorkflowInput, type WorkflowPlan, type WorkflowResult } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { Modal } from "./Modal";

type BindDraft = { name: string; site: string; repository_id: number; page: number; ref: string; workflow: string; expected: string };
type BindReview = { draft: BindDraft; repository: string; existing?: string };
type WorkflowPreview = { plan: WorkflowPlan; expected: string; input: WorkflowInput };
type ProjectTrust = { project: Project; workflow?: string; repository?: string; branch?: string; tokenDeployer: boolean };

export function Publishing({ token, canPublish, sites, onChanged }: { token: string; canPublish: boolean; sites: Site[]; onChanged(): Promise<void> }) {
  const [repositories, setRepositories] = useState<Repositories | null>(null);
  const [publishing, setPublishing] = useState<PublishingState | null>(null);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Repository | null>(null);
  const [name, setName] = useState("");
  const [site, setSite] = useState("");
  const [ref, setRef] = useState("");
  const [workflow, setWorkflow] = useState("deploy.yml");
  const [bindReview, setBindReview] = useState<BindReview | null>(null);
  const [removeReview, setRemoveReview] = useState<Project | null>(null);
  const [workflowReview, setWorkflowReview] = useState<WorkflowPreview | null>(null);
  const [workflowProject, setWorkflowProject] = useState<Project | null>(null);
  const [workflowInput, setWorkflowInput] = useState<WorkflowInput | null>(null);
  const [workflowResult, setWorkflowResult] = useState<WorkflowResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [directory, setDirectory] = useState(".");
  const [output, setOutput] = useState("dist");
  const [hostname, setHostname] = useState("");
  const read = useRef<AbortController | null>(null);
  const pageRef = useRef(page);

  const load = useCallback(async (requestedPage?: number) => {
    if (!token || !canPublish) return;
    const nextPage = requestedPage ?? pageRef.current;
    read.current?.abort();
    const controller = new AbortController(); read.current = controller;
    setLoading(true); setError("");
    try {
      const [repos, state] = await Promise.all([githubRepositories(token, nextPage, controller.signal), fetchPublishing(token, controller.signal)]);
      if (read.current !== controller) return;
      setRepositories(repos); setPublishing(state); setPage(nextPage); pageRef.current = nextPage;
    } catch (cause) { if (!controller.signal.aborted) setError((cause as Error).message); }
    finally { if (read.current === controller) { read.current = null; setLoading(false); } }
  }, [token, canPublish]);

  useEffect(() => {
    void load(1);
    const poll = window.setInterval(() => void load(), 15_000);
    return () => { window.clearInterval(poll); read.current?.abort(); };
  }, [load]);

  const projects = publishing?.projects ?? [];
  const sitesWithoutProject = useMemo(() => publishing?.sites ?? sites.map((entry) => entry.name), [publishing, sites]);

  function choose(repo: Repository) {
    setSelected(repo);
    const derivedName = repo.full_name.split("/")[1].toLowerCase().replace(/[^a-z0-9-]/g, "-");
    setName(derivedName);
    setRef(`refs/heads/${repo.default_branch}`);
    setSite(sitesWithoutProject[0] ?? "");
    setWorkflow("deploy.yml");
    setError("");
  }

  function prepareBinding(event: FormEvent) {
    event.preventDefault();
    if (!selected || !publishing || !site) return;
    const draft = { name: name.trim(), site, repository_id: selected.id, page, ref: ref.trim(), workflow: workflow.trim(), expected: publishing.config_sha256 };
    const existing = projects.find((entry) => entry.name === draft.name)?.deployers.map((deployer) => deployer.type).join(", ");
    setBindReview({ draft, repository: selected.full_name, existing });
  }

  async function saveBinding() {
    if (!bindReview) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await bindGitHubProject(token, bindReview.draft);
      setNotice(`Publisher trust saved for ${result.workflow_ref}. ${result.deploy_host_configured ? "Prepare its publishing workflow below to review a pull request, then verify its first build and publication." : "Prepare the dedicated HTTPS deploy hostname on the host before enabling CI."} No repository files were changed.`);
      setBindReview(null); setSelected(null); await load(); await onChanged();
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }

  async function stopPublishing() {
    if (!removeReview || !publishing) return;
    const project = removeReview;
    setBusy(true); setError(""); setNotice("");
    try {
      await disconnectProject(token, project.name, publishing.config_sha256);
      setNotice("Publisher trust removed. The live site and retained deployments remain. A deployment already authenticated may finish; verify Activity.");
      setRemoveReview(null); await load(); await onChanged();
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }

  function openWorkflow(project: Project) {
    setWorkflowProject(project); setWorkflowReview(null); setWorkflowResult(null); setWorkflowInput(null); setError("");
    setDirectory("."); setOutput("dist"); setHostname(project.hostnames[0] ?? "");
  }

  async function previewWorkflow(event: FormEvent) {
    event.preventDefault();
    if (!workflowProject || !hostname) return;
    setBusy(true); setError(""); setWorkflowReview(null); setWorkflowResult(null);
    const input = { directory: directory.trim(), output: output.trim(), hostname, page };
    try {
      const result = await proposeWorkflow(token, workflowProject.name, input, AbortSignal.timeout(65_000));
      if (result.applied) throw new Error("Expected a workflow preview; inspect GitHub before continuing.");
      setWorkflowInput(input); setWorkflowReview({ plan: result.plan, expected: result.expected, input });
    } catch (cause) { setError((cause as Error).message); }
    finally { setBusy(false); }
  }

  async function createWorkflowPR() {
    if (!workflowProject || !workflowReview || !workflowInput) return;
    const reviewed = workflowReview;
    setBusy(true); setError(""); setWorkflowResult(null);
    try {
      const response = await proposeWorkflow(token, workflowProject.name, { ...workflowInput, expected: reviewed.expected, digest: reviewed.plan.digest, apply: true }, AbortSignal.timeout(65_000));
      if (!response.applied) throw new Error("GitHub did not confirm the pull request; inspect the proposal branch.");
      setWorkflowResult(response); setWorkflowReview(null);
    } catch (cause) {
      const interrupted = (cause as Error).name === "TimeoutError";
      setError(interrupted ? `The response was interrupted. Inspect ${reviewed.plan.repository} and branch ${reviewed.plan.branch} in GitHub before retrying; the pull request may already exist.` : (cause as Error).message);
    } finally { setBusy(false); }
  }

  if (!canPublish) return <section className="panel"><header className="panel-head"><div><h2 className="text-sm font-medium">Publishing from GitHub</h2><p className="text-muted-foreground mt-0.5 text-xs">CI publishing is limited to sessions that include the publishing tool.</p></div></header><p className="text-muted-foreground px-5 pb-5 text-sm">This session cannot read or change publisher trust.</p></section>;

  return <section className="panel"><header className="panel-head"><div className="min-w-0"><p className="plate">Publishing</p><h2 className="mt-1 text-base font-medium">Publishing from GitHub</h2><p className="text-muted-foreground mt-1 max-w-2xl text-sm">Each push to the chosen branch builds in GitHub Actions and publishes here. Ozal trusts exactly one repository, branch and workflow per website. Builds run on GitHub, never on this edge.</p></div><Button variant="outline" size="sm" aria-label="Refresh publishing" disabled={loading || busy} onClick={() => void load()}><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />Refresh</Button></header>
    <div className="grid gap-5 p-5">
      {error && <p className="text-destructive text-sm" role="alert">{error}</p>}{notice && <p className="text-sm" role="status">{notice}</p>}
      {!repositories && loading && <p className="text-muted-foreground text-sm" role="status">Asking GitHub and reading publisher trust…</p>}
      {repositories && !repositories.configured && <div className="bg-muted/40 grid gap-2 rounded-lg border p-4 text-sm"><p>Optional: connect GitHub to pick repositories here and let Ozal propose a workflow. Without it, existing publishers keep working; you add the workflow yourself.</p><p className="text-muted-foreground text-xs">Connect it in <Link className="underline underline-offset-4" to="/integrations">Integrations</Link>. Ozal creates a private GitHub App. You choose its repositories; no key passes through this browser.</p></div>}
      {repositories?.configured && <div className="rounded-lg border p-4"><div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-sm font-medium">Use a repository for an existing website</h3><p className="text-muted-foreground mt-1 text-xs">New websites from GitHub start in Applications. Here an existing site gets a publisher.</p></div><details className="text-xs"><summary className="cursor-pointer underline underline-offset-4">{repositories.repositories.length}{repositories.more ? "+" : ""} {repositories.repositories.length === 1 ? "repository" : "repositories"} the App can see</summary><ul className="mt-2 grid gap-1">{repositories.repositories.map((repo) => <li key={repo.id}>{repo.full_name} · {repo.private ? "Private" : "Public"}</li>)}</ul></details></div>
        {repositories.repositories.length === 0 ? <p className="text-muted-foreground mt-3 text-sm">No selected repositories are available. Check the App installation and permissions.</p> : <ul className="mt-3 divide-y">{repositories.repositories.map((repo) => <li key={repo.id} className="flex flex-wrap items-center justify-between gap-3 py-2.5"><span className="min-w-0 break-all text-sm">{repo.full_name} <span className="text-muted-foreground text-xs">· {repo.private ? "Private repository" : "Public repository"}</span></span><Button size="sm" variant="outline" disabled={busy || !sitesWithoutProject.length} onClick={() => choose(repo)}>Use repository</Button></li>)}</ul>}
        {publishing && !sitesWithoutProject.length && <p className="text-muted-foreground mt-2 text-sm">Add a static website in <Link className="underline" to="/applications">Applications</Link> before binding a publisher.</p>}
        {(page > 1 || repositories.more) && <div className="mt-3 flex items-center gap-2"><Button size="sm" variant="ghost" disabled={page <= 1 || busy} onClick={() => { setSelected(null); void load(page - 1); }}>Previous</Button><span className="text-muted-foreground text-xs">Page {page}</span><Button size="sm" variant="ghost" disabled={!repositories.more || busy} onClick={() => { setSelected(null); void load(page + 1); }}>Next</Button></div>}
        <p className="mt-3 text-xs"><Link className="underline underline-offset-4" to="/integrations">GitHub connection and its repositories</Link></p>
      </div>}

      {selected && <form onSubmit={prepareBinding} className="grid gap-4 rounded-lg border p-4 sm:grid-cols-2"><div className="sm:col-span-2"><h3 className="text-sm font-medium">Connect {selected.full_name}</h3><p className="text-muted-foreground mt-1 text-xs">Pin the exact repository, branch and workflow that may publish this existing website.</p></div><div className="grid gap-1.5"><Label htmlFor="publisher-name">Project name</Label><Input id="publisher-name" required pattern="[a-z0-9]+(-[a-z0-9]+)*" value={name} onChange={(event) => setName(event.target.value)} /></div><div className="grid gap-1.5"><Label htmlFor="publisher-site">Pages site</Label><select id="publisher-site" required value={site} onChange={(event) => setSite(event.target.value)} className="border-input bg-background h-9 rounded-md border px-3 text-sm">{sitesWithoutProject.map((entry) => <option key={entry}>{entry}</option>)}</select></div><div className="grid gap-1.5"><Label htmlFor="publisher-ref">Allowed Git ref</Label><Input id="publisher-ref" required value={ref} onChange={(event) => setRef(event.target.value)} className="font-mono" /></div><div className="grid gap-1.5"><Label htmlFor="publisher-workflow">Workflow filename</Label><Input id="publisher-workflow" required pattern="[^/\\\\][^\\\\]*\\.ya?ml" value={workflow} onChange={(event) => setWorkflow(event.target.value)} className="font-mono" /></div><p className="text-muted-foreground text-xs sm:col-span-2">This replaces the project's previous publisher trust, including token deployers. It pins repository and owner IDs as well as ref and workflow; it neither builds on the edge nor writes a workflow to GitHub.</p><div className="flex gap-2 sm:col-span-2"><Button type="submit">Review publisher</Button><Button type="button" variant="ghost" onClick={() => setSelected(null)}>Cancel</Button></div></form>}

      {publishing && <ConfiguredProjects projects={projects} repositories={repositories} busy={busy} onRemove={setRemoveReview} onWorkflow={openWorkflow} />}
    </div>

    <Modal open={!!bindReview} onOpenChange={(open) => { if (!open) setBindReview(null); }} title="Change publisher trust?" intro="Review change">{bindReview && <><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRows rows={[["Target", bindReview.draft.name], ["Current", bindReview.existing ?? "No publisher trust"], ["Requested", `${bindReview.repository} · ${bindReview.draft.ref} · ${bindReview.draft.workflow}`], ["Consequence", "Only this repository owner, repository, ref and workflow will be trusted. Previous token deployers for this project are removed."], ["Details", "This does not change visitor access, the live site, or any repository file."]]} /></dl><ModalActions cancel={() => setBindReview(null)} confirm="Save publisher trust" busy={busy} onConfirm={() => void saveBinding()} /></>}</Modal>
    <Modal open={!!removeReview} onOpenChange={(open) => { if (!open) setRemoveReview(null); }} title="Stop publishing from GitHub?" intro="Review change">{removeReview && <><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRows rows={[["Target", removeReview.name], ["Current", "Configured deploy trust"], ["Requested", "No deploy trust for this project"], ["Consequence", "Future deploy authentication for this project stops. Content remains. An already authenticated upload may finish."], ["Details", "This does not uninstall the GitHub App or change repository files."]]} /></dl><ModalActions cancel={() => setRemoveReview(null)} confirm="Stop publishing" busy={busy} onConfirm={() => void stopPublishing()} /></>}</Modal>
    <Modal open={!!workflowProject} onOpenChange={(open) => { if (!open && !busy) { setWorkflowProject(null); setWorkflowReview(null); setWorkflowResult(null); } }} title={`Publish ${workflowProject?.name ?? "site"} from GitHub`} intro="Prepare a workflow pull request. The live site stays as it is until it is merged and runs." wide>
      {workflowProject && <>
        {!workflowReview && !workflowResult && <form onSubmit={previewWorkflow} className="grid gap-4 sm:grid-cols-2"><div className="grid gap-1.5"><Label htmlFor="workflow-directory">Build directory</Label><Input id="workflow-directory" required maxLength={200} value={directory} disabled={busy} onChange={(event) => { setDirectory(event.target.value); setWorkflowReview(null); }} /></div><div className="grid gap-1.5"><Label htmlFor="workflow-output">Build output directory</Label><Input id="workflow-output" required maxLength={200} value={output} disabled={busy} onChange={(event) => { setOutput(event.target.value); setWorkflowReview(null); }} /></div><p className="text-muted-foreground text-sm sm:col-span-2">Build runs <code className="font-mono">npm ci</code> and <code className="font-mono">npm run build</code>. The output path is relative to the build directory. Review the pull request for other build tools.</p><div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="workflow-hostname">Public hostname to verify</Label><select id="workflow-hostname" required value={hostname} onChange={(event) => setHostname(event.target.value)} disabled={busy} className="border-input bg-background h-9 rounded-md border px-3 text-sm">{workflowProject.hostnames.map((host) => <option key={host}>{host}</option>)}</select></div><div className="flex flex-wrap gap-2 sm:col-span-2"><Button type="submit" variant="outline" disabled={busy || !hostname}>{busy ? "Waiting for GitHub…" : "Preview workflow"}</Button><Button type="button" variant="ghost" disabled={busy} onClick={() => setWorkflowProject(null)}>Close</Button></div></form>}
        {workflowReview && <section className="grid gap-3" aria-label="Workflow review"><p className="text-sm">Add <code className="font-mono">{workflowReview.plan.path}</code> to <strong>{workflowReview.plan.repository}</strong> through a new pull request into <code className="font-mono">{workflowReview.plan.base}</code>.</p><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRows rows={[["Target", workflowReview.plan.repository], ["Workflow", workflowReview.plan.path], ["Base branch", workflowReview.plan.base], ["Proposal branch", workflowReview.plan.branch], ["Build", `npm ci && npm run build · ${workflowReview.plan.directory} → ${workflowReview.plan.output}`], ["Site to check", workflowReview.plan.site_url], ["Plan digest", workflowReview.plan.digest]]} /></dl><details><summary className="cursor-pointer text-sm underline underline-offset-4">Read generated workflow</summary><pre className="bg-background mt-3 max-h-80 overflow-auto rounded-md border p-3 text-xs" tabIndex={0} aria-label="Generated workflow"><code>{workflowReview.plan.content}</code></pre></details><p className="text-muted-foreground text-sm">Opening the pull request may trigger existing GitHub Actions in this repository. Review the new branch and its permissions; merges stay in your hands. The App needs Contents, Workflows and Pull requests write access for this repository. Builds run in GitHub Actions, never on this edge.</p><div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" disabled={busy} onClick={() => void createWorkflowPR()}>{busy ? "Waiting for GitHub…" : "Review and open pull request"}</Button><Button variant="ghost" size="sm" disabled={busy} onClick={() => { setWorkflowReview(null); setWorkflowInput(null); }}>Change build settings</Button></div></section>}
        {workflowResult && <p className="text-sm" role="status">Pull request created. <a className="underline underline-offset-4" href={workflowResult.pull_request.url} target="_blank" rel="noopener noreferrer">Review pull request #{workflowResult.pull_request.number} on GitHub <ExternalLink className="inline size-3.5" aria-hidden="true" /></a>. Review its build and deployment settings before merging; publication has not been verified.</p>}
        {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
      </>}
    </Modal>
  </section>;
}

function ConfiguredProjects({ projects, repositories, busy, onRemove, onWorkflow }: { projects: Project[]; repositories: Repositories | null; busy: boolean; onRemove(project: Project): void; onWorkflow(project: Project): void }) {
  return <section className="border-t pt-5"><h3 className="text-sm font-medium">Websites that publish from GitHub</h3>{projects.length ? <ul className="mt-2 divide-y">{projects.map((project) => {
    const github = project.deployers.filter((deployer) => deployer.type === "github").map((deployer) => deployer.github);
    const token = project.deployers.some((deployer) => deployer.type === "token");
    return <li key={project.name} className="grid gap-2 py-4"><div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h4 className="break-all text-sm font-medium">{project.site}{project.name !== project.site && <span className="text-muted-foreground font-normal"> · project {project.name}</span>}</h4><p className="text-muted-foreground mt-0.5 break-all text-sm">{project.hostnames.length ? project.hostnames.join(", ") : "Not served at any address yet"}</p></div><Button variant="ghost" size="sm" className="text-muted-foreground" disabled={busy} onClick={() => onRemove(project)}>Stop publishing</Button></div>
      {github.map((claim) => { const parsed = parseWorkflow(claim.workflow_ref, claim.ref); return <p key={claim.repository_id} className="break-all text-sm">Publishes from <span className="font-mono text-xs">{parsed.repository ?? `repository #${claim.repository_id}`}</span>{parsed.branch && <> · branch <span className="font-mono text-xs">{parsed.branch}</span></>}{parsed.workflow && <> · <span className="font-mono text-xs">{parsed.workflow}</span></>}</p>; })}
      {token && <p className="text-sm">Publishes with a project token for CI outside GitHub.</p>}
      {github.length === 1 && repositories?.configured && <div><Button variant="outline" size="sm" className="h-auto max-w-full py-1.5 text-left break-all whitespace-normal" onClick={() => onWorkflow(project)}>Prepare publishing workflow for {project.name}</Button></div>}
    </li>;
  })}</ul> : <p className="text-muted-foreground mt-2 text-sm">No website publishes from GitHub yet. You can still publish by uploading files in Applications.</p>}</section>;
}

function parseWorkflow(workflowRef: string | undefined, ref: string) {
  const [path, at] = (workflowRef ?? "").split("@");
  const marker = "/.github/workflows/";
  const index = path.indexOf(marker);
  return { repository: index > 0 ? path.slice(0, index) : undefined, workflow: index > 0 ? path.slice(index + marker.length) : undefined, branch: (at || ref).replace(/^refs\/heads\//, "") || undefined };
}

function ReviewRows({ rows }: { rows: [string, string][] }) { return <>{rows.map(([label, value]) => <div key={label} className="grid gap-1 px-3 py-2 sm:grid-cols-[8rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className="min-w-0 break-words">{value}</dd></div>)}</>; }
function ModalActions({ cancel, confirm, busy, onConfirm }: { cancel(): void; confirm: string; busy: boolean; onConfirm(): void }) { return <div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={cancel}>Cancel</Button><Button disabled={busy} onClick={onConfirm}>{busy ? "Saving…" : confirm}</Button></div>; }
