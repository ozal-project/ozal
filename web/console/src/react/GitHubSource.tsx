import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { ExternalLink, Globe, Lock, Search } from "lucide-react";
import { clearReturn, leaveForGitHub } from "$lib/githubReturn";
import { finishGitHubConnection, fetchPublishing, githubRepositories, recordGitHubInstallation, startGitHubConnection, type Repositories, type Repository } from "$lib/api";
import { Button, buttonVariants } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";

export type GitHubSelection = { repository: Repository; page: number; branch: string; directory: string; output: string };

const hostCommand = ["ozal github connect -config /etc/ozal/config.json \\", "  -app-id APP_ID -installation-id INSTALLATION_ID -key file:/etc/ozal/github-app.pem"].join("\n");

export function GitHubSource({ token, selection, connection, onSelection, onConnection, onChanged }: { token: string; selection: GitHubSelection | null; connection: Repositories | null; onSelection(value: GitHubSelection | null): void; onConnection(value: Repositories | null): void; onChanged?: () => Promise<unknown> }) {
  const location = useLocation();
  const navigate = useNavigate();
  const [page, setPage] = useState(1);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [organization, setOrganization] = useState("");
  const [manifest, setManifest] = useState<{ url: string; manifest: string } | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const processedSearch = useRef("");

  async function load(next = page) {
    setBusy((current) => current || "Asking GitHub…"); setError("");
    try { onConnection(await githubRepositories(token, next)); setPage(next); }
    catch (cause) { setError((cause as Error).message); }
    finally { setBusy(""); }
  }
  async function expected() { return (await fetchPublishing(token)).config_sha256; }

  useEffect(() => {
    if (manifest) { leaveForGitHub("/applications"); form.current?.submit(); }
  }, [manifest]);
  useEffect(() => {
    const query = new URLSearchParams(location.search);
    if (!query.size) { if (!connection) void load(1); return; }
    if (processedSearch.current === location.search) return;
    processedSearch.current = location.search;
    const code = query.get("code"), state = query.get("state");
    navigate("/applications", { replace: true }); clearReturn();
    void (async () => {
      try {
        if (code && state) {
          setBusy("Saving the new GitHub App…");
          const app = await finishGitHubConnection(token, code, state, await expected());
          setBusy(`Created ${app.slug}. Opening GitHub to choose its repositories…`);
          leaveForGitHub("/applications"); window.location.assign(app.install_url); return;
        }
        if (query.has("setup_action") || query.has("installation_id")) {
          setBusy("Checking where the App was installed…");
          await recordGitHubInstallation(token, await expected()); await onChanged?.();
        }
      } catch (cause) { setError((cause as Error).message); setBusy(""); }
      await load(1);
    })();
  }, [location.search, token]);

  async function connect() {
    setBusy("Preparing the GitHub App…"); setError("");
    try { setManifest(await startGitHubConnection(token, window.location.origin, organization.trim())); }
    catch (cause) { setError((cause as Error).message); setBusy(""); }
  }
  async function checkInstallation() {
    setBusy("Checking where the App was installed…"); setError("");
    try { await recordGitHubInstallation(token, await expected()); await onChanged?.(); await load(1); }
    catch (cause) { setError((cause as Error).message); setBusy(""); }
  }
  function choose(repository: Repository) { onSelection({ repository, page, branch: repository.default_branch, directory: selection?.directory ?? ".", output: selection?.output ?? "dist" }); }
  const shown = useMemo(() => (connection?.repositories ?? []).filter((repo) => repo.full_name.toLowerCase().includes(filter.trim().toLowerCase())), [connection, filter]);

  return <div className="grid gap-3" aria-busy={!!busy}><span className="text-sm font-medium">Your repository</span>
    {busy && !connection ? <p className="text-muted-foreground text-sm" role="status">{busy}</p> : connection && !connection.configured ? connection.connect_available ? <div className="bg-muted/40 grid gap-3 rounded-lg border p-4 text-sm"><p>Connect GitHub once so Ozal can list your repositories and add the publishing workflow for you.</p><ul className="text-muted-foreground grid gap-1 text-xs"><li>· Ozal creates its own private GitHub App in your account. You approve it on GitHub.</li><li>· You then choose which repositories it can see. It can open pull requests there; it never merges or builds.</li><li>· Its key stays on this server. Nothing about GitHub is kept in this browser.</li></ul><details className="text-xs"><summary className="text-muted-foreground cursor-pointer">Create it in an organization instead</summary><div className="mt-2 grid max-w-xs gap-2"><Label htmlFor="gh-org">Organization</Label><Input id="gh-org" value={organization} onChange={(event) => setOrganization(event.target.value)} placeholder="my-org" autoComplete="off" spellCheck={false} className="font-mono" /></div></details><div><Button type="button" disabled={!!busy} onClick={() => void connect()}>{busy || "Connect GitHub"}</Button></div></div> : <div className="bg-muted/40 grid gap-2 rounded-lg border p-4 text-sm"><p>GitHub is not connected, and this server cannot connect it from here: it was started without its state database (<code className="font-mono text-xs">-db</code>).</p><p className="text-muted-foreground text-xs">Create a private GitHub App with Metadata read and Contents, Workflows and Pull requests write, install it on your repositories, put its key in a file only Ozal can read, then run on the host:</p><CopyCommand command={hostCommand} /></div> : connection && !connection.installed ? <div className="bg-muted/40 grid gap-3 rounded-lg border p-4 text-sm"><p>The App <span className="font-mono text-xs">{connection.app}</span> exists{connection.owner ? ` in ${connection.owner}` : ""}. Now choose which repositories it can see.</p><div className="flex flex-wrap gap-2">{connection.install_url && <a className={buttonVariants()} href={connection.install_url} onClick={() => leaveForGitHub("/applications")}>Choose repositories on GitHub</a>}<Button variant="outline" disabled={!!busy} onClick={() => void checkInstallation()}>{busy || "I've done it, check again"}</Button></div></div> : connection && <>
      <p className="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs"><span className="lamp lamp-on" aria-hidden="true" /><span>GitHub connected{connection.owner ? ` · ${connection.owner}` : ""}{connection.all_repositories ? " · every repository" : connection.total !== undefined ? ` · ${connection.total} ${connection.total === 1 ? "repository" : "repositories"}` : ""}</span><Link className="text-foreground underline underline-offset-4" to="/integrations">Manage</Link></p>
      {connection.all_repositories && <p className="border-warning/40 bg-warning/[0.06] rounded-md border px-3 py-2 text-xs">The App can see every repository in {connection.owner ?? "the account"}, private ones included. Ozal writes only to the one you choose, but keep the App to the repositories that publish websites.{connection.manage_url && <> <a className="inline-flex items-center gap-1 underline underline-offset-4" href={connection.manage_url} onClick={() => leaveForGitHub("/applications")}>Choose repositories on GitHub <ExternalLink className="size-3" aria-hidden="true" /></a></>}</p>}
      <div className="grid gap-2"><div className="relative"><Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden="true" /><Input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Find a repository" aria-label="Find a repository" className="pl-8" autoComplete="off" spellCheck={false} /></div><div className="max-h-56 overflow-y-auto rounded-lg border" role="radiogroup" aria-label="Repository">{shown.map((repo) => <label key={repo.id} className="hover:bg-accent/50 has-[:checked]:bg-primary/[0.07] flex cursor-pointer items-center gap-3 border-b px-3 py-2 text-sm last:border-b-0"><input type="radio" name="gh-repo" className="accent-primary" checked={selection?.repository.id === repo.id} onChange={() => choose(repo)} /><span className="min-w-0 flex-1 truncate font-mono text-xs">{repo.full_name}</span>{repo.private ? <Lock className="text-muted-foreground size-3.5" aria-label="Private repository" /> : <Globe className="text-muted-foreground size-3.5" aria-label="Public repository" />}</label>)}{shown.length === 0 && <p className="text-muted-foreground px-3 py-3 text-sm">{connection.repositories.length ? "No repository matches." : "The App can see no repository yet. Add one in its settings on GitHub."}</p>}</div>{(page > 1 || connection.more) && <div className="flex items-center gap-2 text-xs"><Button type="button" size="sm" variant="ghost" disabled={page <= 1 || !!busy} onClick={() => void load(page - 1)}>Previous</Button><span className="text-muted-foreground">Page {page}</span><Button type="button" size="sm" variant="ghost" disabled={!connection.more || !!busy} onClick={() => void load(page + 1)}>Next</Button></div>}</div>
      {selection && <><div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end"><div className="grid gap-1.5"><Label htmlFor="gh-branch">Branch that publishes</Label><Input id="gh-branch" required value={selection.branch} onChange={(event) => onSelection({ ...selection, branch: event.target.value })} className="font-mono" autoComplete="off" spellCheck={false} /></div></div><details className="text-muted-foreground text-xs"><summary className="cursor-pointer">Build: <span className="text-foreground font-mono">npm ci &amp;&amp; npm run build</span> in <span className="text-foreground font-mono">{selection.directory}</span>, publishes <span className="text-foreground font-mono">{selection.output}</span></summary><div className="mt-2 grid gap-3 sm:grid-cols-2"><div className="grid gap-1.5"><Label htmlFor="gh-dir" className="text-xs">Folder with package.json</Label><Input id="gh-dir" required maxLength={200} value={selection.directory} onChange={(event) => onSelection({ ...selection, directory: event.target.value })} className="font-mono" /></div><div className="grid gap-1.5"><Label htmlFor="gh-out" className="text-xs">Folder the build produces</Label><Input id="gh-out" required maxLength={200} value={selection.output} onChange={(event) => onSelection({ ...selection, output: event.target.value })} className="font-mono" /></div></div><p className="mt-2">The build runs in GitHub Actions, never on this server. For another build tool, edit the workflow in the pull request.</p></details></>}
    </>}
    {connection?.installed && !connection.workflow_available && <p className="border-warning/40 bg-warning/[0.06] rounded-md border px-3 py-2 text-xs">This server has no deploy endpoint for GitHub Actions (<code className="font-mono">pages.deploy</code>), so Ozal can trust the repository but cannot add its workflow. You would add it yourself.</p>}
    {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
    {manifest && <form ref={form} method="post" action={manifest.url} className="hidden"><input type="hidden" name="manifest" value={manifest.manifest} /></form>}
  </div>;
}

function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText(command); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <div className="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 font-mono text-xs break-all whitespace-pre-wrap">{command}</code><button type="button" aria-label="Copy command" onClick={() => void copy()} className="text-muted-foreground rounded p-1">{copied ? "Copied" : "Copy"}</button></div>;
}
