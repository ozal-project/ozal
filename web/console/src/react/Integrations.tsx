import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { Check, Copy, ExternalLink, Globe2, KeyRound, RefreshCw } from "lucide-react";
import { clearReturn, leaveForGitHub } from "$lib/githubReturn";
import { fetchPublishing, finishGitHubConnection, githubRepositories, recordGitHubInstallation, setupStatus, startGitHubConnection, type Project, type Repositories, type SetupStatus } from "$lib/api";
import { Button, buttonVariants } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { useSession } from "./session";

const hostCommand = "ozal github connect -config /etc/ozal/config.json \\\n  -app-id APP_ID -installation-id INSTALLATION_ID -key file:/etc/ozal/github-app.pem";
const disconnectCommand = "sudo ozal github disconnect -config /etc/ozal/config.json";

export function Integrations() {
  const { session } = useSession();
  const location = useLocation();
  const navigate = useNavigate();
  const token = session?.token ?? "";
  const canPublishing = !!session?.tools.includes("publishing");
  const canApplications = !!session?.tools.includes("applications");
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [github, setGithub] = useState<Repositories | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [organization, setOrganization] = useState("");
  const [manifest, setManifest] = useState<{ url: string; manifest: string } | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const processedSearch = useRef("");

  async function load() {
    if (!token) return;
    setLoading(true); setError("");
    try {
      const [nextStatus, repositories, publishing] = await Promise.all([
        setupStatus(token).catch(() => null),
        canPublishing ? githubRepositories(token) : Promise.resolve(null),
        canPublishing ? fetchPublishing(token).catch(() => null) : Promise.resolve(null),
      ]);
      setStatus(nextStatus); setGithub(repositories); setProjects(publishing?.projects ?? []);
    } catch (cause) { setError((cause as Error).message); }
    finally { setLoading(false); }
  }

  useEffect(() => {
    if (manifest) {
      leaveForGitHub("/integrations");
      form.current?.submit();
    }
  }, [manifest]);

  useEffect(() => {
    if (!token) return;
    const query = new URLSearchParams(location.search);
    if (!query.size) { void load(); return; }
    if (processedSearch.current === location.search) return;
    processedSearch.current = location.search;
    const code = query.get("code");
    const returned = query.get("state");
    navigate("/integrations", { replace: true });
    clearReturn();
    void (async () => {
      try {
        if (code && returned) {
          setBusy("Saving the new GitHub App…");
          const publishing = await fetchPublishing(token);
          const app = await finishGitHubConnection(token, code, returned, publishing.config_sha256);
          setBusy(`Created ${app.slug}. Opening GitHub to choose its repositories…`);
          leaveForGitHub("/integrations");
          window.location.assign(app.install_url);
          return;
        }
        if (query.has("setup_action") || query.has("installation_id")) {
          setBusy("Checking where the App is installed…");
          const publishing = await fetchPublishing(token);
          await recordGitHubInstallation(token, publishing.config_sha256);
        }
      } catch (cause) { setError((cause as Error).message); }
      setBusy("");
      await load();
    })();
  }, [token, location.search]);

  async function connect() {
    setBusy("Preparing the GitHub App…"); setError("");
    try { setManifest(await startGitHubConnection(token, window.location.origin, organization.trim())); }
    catch (cause) { setError((cause as Error).message); setBusy(""); }
  }
  async function checkInstallation() {
    setBusy("Checking where the App is installed…"); setError("");
    try { const publishing = await fetchPublishing(token); await recordGitHubInstallation(token, publishing.config_sha256); }
    catch (cause) { setError((cause as Error).message); }
    setBusy(""); await load();
  }

  const methods = status?.methods ?? [];
  const fromGitHub = useMemo(() => projects.flatMap((project) => project.deployers.flatMap((deployer) => deployer.type === "github" ? [{ project, ref: deployer.github.workflow_ref ?? "", branch: deployer.github.ref.replace(/^refs\/heads\//, "") }] : [])), [projects]);
  const reach = github?.all_repositories ? `Every repository in ${github.owner ?? "the account"}${github.total ? ` (${github.total})` : ""}, including private ones and any created later` : github?.total !== undefined ? `${github.total} chosen ${github.total === 1 ? "repository" : "repositories"}` : "The repositories chosen on GitHub";
  const githubStatus = github?.installed ? <span className="flex items-center gap-2 text-xs"><span className="lamp lamp-on" aria-hidden="true" />Connected</span> : github?.configured ? <span className="text-warning flex items-center gap-2 text-xs"><span className="lamp lamp-warn" aria-hidden="true" />Not installed yet</span> : github ? <span className="text-muted-foreground text-xs">Not connected</span> : null;

  return <div className="grid content-start gap-6">
    <header className="flex flex-wrap items-end justify-between gap-4"><div><p className="plate">Edge</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">Integrations</h1><p className="text-muted-foreground mt-1 max-w-2xl text-sm">Services Ozal works with on your behalf. None of them signs anyone in to your applications.</p></div><Button variant="ghost" size="icon-sm" aria-label="Refresh integrations" title="Refresh" onClick={() => void load()} disabled={loading || !!busy}><RefreshCw className={`size-4 ${loading ? "animate-spin" : ""}`} aria-hidden="true" /></Button></header>
    {error && <p className="text-destructive -mt-2 text-sm" role="alert">{error}</p>}

    <section className="bg-card mb-0 rounded-lg p-5" aria-labelledby="sign-in-title"><div className="flex items-start gap-3"><span className="bg-primary/15 text-signal grid size-9 shrink-0 place-items-center rounded-lg"><KeyRound className="size-4" aria-hidden="true" /></span><div className="min-w-0"><h2 id="sign-in-title" className="text-sm font-medium">Sign-in stays with Ozal</h2><p className="text-muted-foreground mt-1 max-w-2xl text-sm">People open your applications with a passkey on their own device: fingerprint, face or PIN. Ozal checks it, then your rule for that address. Connecting GitHub or a DNS provider never lets anyone sign in with that account.</p>{status && <><ul className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs" aria-label="Sign-in methods on this server"><li className="flex items-center gap-2"><span className={`lamp ${methods.includes("passkey") ? "lamp-on" : "lamp-warn"}`} aria-hidden="true" />Passkeys: {methods.includes("passkey") ? "on" : "not enabled"}</li><li className="flex items-center gap-2"><span className={`lamp ${methods.includes("oidc") ? "lamp-on" : "lamp-idle"}`} aria-hidden="true" />External login (OIDC): {methods.includes("oidc") ? "on, as this server's configuration sets it" : "not used"}</li></ul>{!methods.length && <p className="text-warning mt-2 text-xs">Sign-in is not set up yet, so every application is public. Passkeys are turned on in this server's configuration (<code className="font-mono">auth.passkeys</code>).</p>}</>}</div></div><SignInFlow canPeople={!!session?.tools.includes("people")} methods={methods} /></section>

    <section className="panel mb-0"><PanelHeader title="GitHub" sub="Lists your repositories and proposes the workflow that publishes a website. Builds stay in GitHub Actions." action={githubStatus} /><div className="p-5" aria-busy={!!busy || loading}>{!canPublishing ? <p className="text-muted-foreground text-sm">Your session does not include CI publishing, so it cannot read or change the GitHub connection.</p> : busy ? <p className="text-muted-foreground text-sm" role="status">{busy}</p> : !github ? <p className="text-muted-foreground text-sm" role="status">{loading ? "Asking GitHub…" : "Loading…"}</p> : !github.configured ? <div className="grid gap-3 text-sm"><p>Optional. Without it you can still publish by uploading files, or add the workflow to a repository yourself.</p><ul className="text-muted-foreground grid gap-1 text-xs"><li>· Ozal creates its own private GitHub App in your account. You approve it on GitHub.</li><li>· You choose which repositories it can see. It opens pull requests there; it never merges or builds.</li><li>· Its key stays on this server. Nothing about GitHub is kept in this browser.</li></ul>{github.connect_available ? <><details className="text-xs"><summary className="text-muted-foreground cursor-pointer">Create it in an organization instead</summary><div className="mt-2 grid max-w-xs gap-2"><Label htmlFor="github-organization">Organization</Label><Input id="github-organization" value={organization} onChange={(event) => setOrganization(event.target.value)} placeholder="my-org" autoComplete="off" spellCheck={false} className="font-mono" /></div></details><div><Button onClick={() => void connect()} disabled={!!busy}>Connect GitHub</Button></div></> : <><p className="text-muted-foreground text-xs">This server was started without its state database (<code className="font-mono">-db</code>), so GitHub is connected on the host: create a private App with Metadata read and Contents, Workflows and Pull requests write, install it, keep its key in a file only Ozal can read, then run:</p><CopyCommandBox command={hostCommand} /></>}</div> : !github.installed ? <div className="grid gap-3 text-sm"><p>The App <span className="font-mono text-xs">{github.app}</span> exists{github.owner ? ` in ${github.owner}` : ""}. Choose which repositories it can see to finish.</p><div className="flex flex-wrap gap-2"><ExternalButton href={github.install_url ?? "#"} onClick={() => leaveForGitHub("/integrations")}>Choose repositories on GitHub</ExternalButton><Button variant="outline" onClick={() => void checkInstallation()}>I've done it, check again</Button></div></div> : <>
      {github.all_repositories && <div className="border-warning/40 bg-warning/[0.06] mb-4 rounded-lg border px-4 py-3 text-sm"><p className="font-medium">This App can see every repository in {github.owner ?? "the account"}.</p><p className="text-muted-foreground mt-1 text-xs">Ozal asks GitHub for write access only to the repository a website publishes from, one at a time. But the App's key on this server could reach all of them, private ones included. Keep it to the repositories that publish websites.</p>{github.manage_url && <ExternalButton className="mt-3" href={github.manage_url} onClick={() => leaveForGitHub("/integrations")}>Choose repositories on GitHub <ExternalLink className="size-3.5" aria-hidden="true" /></ExternalButton>}</div>}
      <dl className="divide-y"><InfoRow term="App" detail={`${github.app ?? "Connected"}${github.owner ? ` · ${github.owner}` : ""}`} /><InfoRow term="Repositories it sees" detail={reach} warn={!!github.all_repositories} /><InfoRow term="What it may do" detail="Read repository metadata. Write contents, workflows and pull requests only to open the publishing pull request. No webhooks or events; it never merges or builds." /><InfoRow term="Workflow pull requests" detail={github.workflow_available ? "Available: adding a website from GitHub opens one for you to review" : "Not available: this server has no deploy endpoint for GitHub Actions (pages.deploy), so you add the workflow yourself"} warn={!github.workflow_available} /></dl>
      {github.manage_url && !github.all_repositories && <p className="mt-3 text-xs"><a className="link inline-flex items-center gap-1" href={github.manage_url} onClick={() => leaveForGitHub("/integrations")}>Change its repositories on GitHub <ExternalLink className="size-3" aria-hidden="true" /></a></p>}
      <h3 className="mt-6 text-sm font-medium">Websites that publish from GitHub</h3>{fromGitHub.length ? <><ul className="mt-2 divide-y rounded-lg border">{fromGitHub.map(({ project, ref, branch }) => <li key={project.name} className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 text-sm"><span className="min-w-0"><span className="font-medium">{project.hostnames[0] ?? project.site}</span> <span className="text-muted-foreground text-xs">from <span className="font-mono">{repositoryOf(ref)}</span> · {branch}</span></span></li>)}</ul><p className="mt-2 text-xs"><Link className="link" to="/sites">Versions and publishers in Sites</Link></p></> : <p className="text-muted-foreground mt-1 text-sm">None yet. <Link className="link" to="/applications">Add an application</Link> and choose “A website built from GitHub”.</p>}
      <details className="mt-6 text-xs"><summary className="text-muted-foreground cursor-pointer">Disconnect GitHub</summary><div className="text-muted-foreground mt-2 grid gap-2"><p>Disconnecting is done on the host. It removes the App's key from this server; websites keep their publishers until you remove them in Sites. Delete the App on GitHub afterwards if you no longer use it.</p><CopyCommandBox command={disconnectCommand} /></div></details>
    </>}</div></section>

    <section className="panel"><PanelHeader title="DNS provider" sub="Creates the record an address needs, with its own credential on this server." action={status && <span className={`flex items-center gap-2 text-xs ${!status.dns.provider ? "text-muted-foreground" : ""}`}>{status.dns.provider && <span className="lamp lamp-on" aria-hidden="true" />}{status.dns.provider === "cloudflare" ? "Cloudflare" : "Not set up"}</span>} /><div className="flex flex-wrap items-center justify-between gap-3 p-5 text-sm"><p className="text-muted-foreground flex min-w-0 items-center gap-2"><Globe2 className="size-4 shrink-0" aria-hidden="true" />{status?.dns.provider ? "Addresses under your domains get their DNS record created for you." : "Without one, Ozal shows the record and you create it at your DNS provider."}</p>{canApplications && <Link className={buttonVariants({ variant: "outline", size: "sm" })} to="/domains">Domains</Link>}</div></section>

    {manifest && <form ref={form} method="post" action={manifest.url} className="hidden"><input type="hidden" name="manifest" value={manifest.manifest} /></form>}
  </div>;
}

function PanelHeader({ title, sub, action }: { title: string; sub: string; action?: ReactNode }) { return <header className="panel-head"><div className="min-w-0"><h2 className="text-sm font-medium">{title}</h2><p className="text-muted-foreground mt-0.5 text-xs">{sub}</p></div>{action}</header>; }
function InfoRow({ term, detail, warn = false }: { term: string; detail: string; warn?: boolean }) { return <div className="grid gap-1 py-2.5 sm:grid-cols-[11rem_minmax(0,1fr)] sm:gap-4"><dt className="text-muted-foreground text-xs sm:text-sm">{term}</dt><dd className={`break-words text-sm ${warn ? "text-warning" : ""}`}>{detail}</dd></div>; }
function ExternalButton({ href, onClick, children, className = "" }: { href: string; onClick(): void; children: ReactNode; className?: string }) { return <a className={buttonVariants({ className: `gap-1.5 ${className}` })} href={href} onClick={onClick}>{children}</a>; }

function CopyCommandBox({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText(command); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <div className="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 break-all font-mono text-[0.78rem] leading-relaxed whitespace-pre-wrap">{command}</code><button type="button" onClick={() => void copy()} aria-label="Copy command" className="text-muted-foreground hover:text-foreground hover:bg-accent shrink-0 rounded-md p-1.5 focus-visible:outline-2">{copied ? <Check className="text-success size-4" aria-hidden="true" /> : <Copy className="size-4" aria-hidden="true" />}</button><span className="sr-only" role="status">{copied ? "Copied" : ""}</span></div>;
}

function SignInFlow({ canPeople, methods }: { canPeople: boolean; methods: string[] }) {
  const steps = [{ title: "A visitor opens", detail: "your app's address" }, { title: "Ozal asks them to sign in", detail: "With a passkey: fingerprint, face or PIN. No passwords." }, { title: "Ozal checks your rule", detail: "Only the people allowed for that address get through. Others see a “no access” page." }, { title: "Your app receives them", detail: "Already signed in, with their email. The app needs no login of its own." }];
  return <><section className="bg-muted/40 mt-4 rounded-lg border p-4" aria-labelledby="signin-flow-title"><h3 id="signin-flow-title" className="text-sm font-medium">How private access works</h3><ol className="mt-3 grid gap-3 sm:grid-cols-4 sm:gap-2">{steps.map((step, index) => <li key={step.title} className="flex gap-2.5 sm:flex-col sm:gap-1.5"><span className="bg-background text-muted-foreground grid size-6 shrink-0 place-items-center rounded-full border text-xs tabular-nums" aria-hidden="true">{index + 1}</span><span className="min-w-0"><span className="block text-xs font-medium">{step.title}</span><span className="text-muted-foreground block break-words text-xs leading-snug">{step.detail}</span></span></li>)}</ol><details className="mt-3 text-xs"><summary className="text-muted-foreground hover:text-foreground cursor-pointer">For developers: what the app receives</summary><div className="text-muted-foreground mt-2 grid gap-1.5 leading-relaxed"><p>Every request that reaches the app carries the signed-in person:</p><p><code className="text-foreground font-mono">X-Auth-Request-Email</code> their email · <code className="text-foreground font-mono">X-Auth-Request-User</code> a stable ID for the account</p><p>Ozal removes these headers from what visitors send, so they cannot be faked, and never forwards its session cookie. Use them to show who is signed in or to link records to a person. An app with its own login keeps it: Ozal decides who reaches the app, not what they can do inside.</p></div></details></section>{canPeople && methods.includes("passkey") && <p className="mt-3 text-xs"><Link className="link" to="/people">Invite people</Link> <span className="text-muted-foreground">to give them access: each invitation creates their passkey.</span></p>}</>;
}

function repositoryOf(workflowRef: string) { const cut = workflowRef.indexOf("/.github/workflows/"); return cut > 0 ? workflowRef.slice(0, cut) : "a trusted repository"; }
