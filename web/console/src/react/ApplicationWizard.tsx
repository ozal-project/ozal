import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { ArrowLeft, ArrowUpRight, Check, Globe, Layers, Lock, Plug, Server, Users } from "lucide-react";
import { bindGitHubProject, deploySite, fetchPublishing, fetchSites, invitePerson, planApplication, previewGitHubWorkflow, proposeWorkflow, saveApplication, type ApplicationDraft, type ApplicationPlan, type ApplicationRoute, type Deployment, type Repositories, type SetupStatus, type WorkflowPlan } from "$lib/api";
import { servedBy, whoMayEnter } from "$lib/wording";
import { Button } from "$lib/components/ui/button/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { Modal } from "./Modal";
import { DomainConnect } from "./DomainConnect";
import { GitHubSource, type GitHubSelection } from "./GitHubSource";

type Kind = "keep" | "backend" | "static" | "github" | "connector" | "redirect";
type Privacy = "keep" | "groups" | "signed-in" | "public";
type Choice = { value: string; label: string; hint: string; Icon: typeof Server; disabled?: boolean; warn?: boolean };
type Invite = { email: string; link?: string; error?: string };

export function ApplicationWizard({ route, githubReturn = false, status, groups: knownGroups, zones, connectors, token, canPublishing, onChanged, onClose, onSaved }: { route: ApplicationRoute | null; githubReturn?: boolean; status: SetupStatus; groups: string[]; zones: string[]; connectors: string[] | null; token: string; canPublishing: boolean; onChanged(): Promise<void>; onClose(): void; onSaved(): Promise<void> }) {
  const navigate = useNavigate();
  const [step, setStep] = useState(1);
  const [kind, setKind] = useState<Kind>(route ? "keep" : githubReturn ? "github" : "backend");
  const [host, setHost] = useState(route?.host ?? "");
  const [sub, setSub] = useState("");
  const [zone, setZone] = useState(zones[0] ?? "");
  const [target, setTarget] = useState(route?.target ?? "");
  const [site, setSite] = useState(route?.site ?? "");
  const [siteEdited, setSiteEdited] = useState(!!route?.site);
  const [files, setFiles] = useState<File | null>(null);
  const [connector, setConnector] = useState(route?.connector ?? "");
  const [redirectTo, setRedirectTo] = useState(route?.redirect ?? "");
  const [privacy, setPrivacy] = useState<Privacy>(route ? (route.access || route.auth_required ? "keep" : "public") : "groups");
  const [groupText, setGroupText] = useState("");
  const [inviteText, setInviteText] = useState("");
  const [github, setGithub] = useState<GitHubSelection | null>(null);
  const [githubState, setGithubState] = useState<Repositories | null>(null);
  const [workflowPlan, setWorkflowPlan] = useState<WorkflowPlan | null>(null);
  const [githubResult, setGithubResult] = useState<{ trusted: boolean; pr?: { url: string; number: number }; error?: string } | null>(null);
  const [published, setPublished] = useState<Deployment | null>(null);
  const [plan, setPlan] = useState<ApplicationPlan | null>(null);
  const [draft, setDraft] = useState<ApplicationDraft | null>(null);
  const [formError, setFormError] = useState("");
  const [waiting, setWaiting] = useState("");
  const [checked, setChecked] = useState(false);
  const [review, setReview] = useState(false);
  const [invitations, setInvitations] = useState<Invite[]>([]);
  const [copied, setCopied] = useState("");
  const [dnsOpen, setDnsOpen] = useState(false);
  const base = route;
  const composed = zone ? sub.trim() ? `${sub.trim().toLowerCase()}.${zone}` : zone : host.trim();
  const address = base?.host ?? composed;
  const covered = (name: string) => { const hostname = name.replace(/:\d+$/, ""); return zones.find((entry) => hostname === entry || (hostname.endsWith(`.${entry}`) && !hostname.slice(0, -entry.length - 1).includes("."))); };
  const slug = (value: string) => value.toLowerCase().replace(/:\d+$/, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63).replace(/-+$/, "");
  const chosen = [...new Set(groupText.split(",").map((group) => group.trim()).filter(Boolean))];
  const suggestions = knownGroups.filter((group) => !chosen.includes(group));
  const emails = [...new Set(inviteText.split(/[\s,;]+/).map((email) => email.trim()).filter((email) => email.includes("@")))];
  const signIn = status.methods.length > 0;
  const passkeys = status.methods.includes("passkey");
  const canInvite = passkeys && privacy !== "public" && privacy !== "keep";
  const scheme = status.tls_enabled ? "https" : "http";
  const workflowFile = "ozal-pages.yml";

  useEffect(() => { if (!siteEdited && (kind === "static" || kind === "github")) setSite(slug(address)); }, [kind, address, siteEdited]);
  useEffect(() => {
    if (step !== 5 || kind !== "github" || !githubResult?.pr || published) return;
    let live = true;
    const look = () => void fetchSites().then((sites) => { if (live) setPublished(sites.find((entry) => entry.name === site)?.deployments.find((deployment) => deployment.live) ?? null); }).catch(() => {});
    void look(); const timer = window.setInterval(look, 10_000);
    return () => { live = false; window.clearInterval(timer); };
  }, [step, kind, githubResult?.pr, published, site]);

  const kinds: Choice[] = [
    ...(base ? [{ value: "keep", label: `Keep: ${servedBy(base)}`, hint: "Change only who may open it.", Icon: Check }] : []),
    { value: "backend", label: "An app that is already running", hint: "A dashboard, an API or any web app this server can reach, such as http://127.0.0.1:3000.", Icon: Server },
    { value: "static", label: "A static website", hint: status.pages_enabled ? "HTML, CSS and JavaScript files, like the folder a build produces. Ozal stores and serves them." : "Needs Pages enabled in this server's configuration.", Icon: Layers, disabled: !status.pages_enabled },
    ...(!base ? [{ value: "github", label: "A website built from GitHub", hint: !status.pages_enabled ? "Needs Pages enabled in this server's configuration." : !canPublishing ? "Your session does not include CI publishing." : "Pick a repository. Every push builds in GitHub Actions and publishes here.", Icon: Layers, disabled: !status.pages_enabled || !canPublishing }] : []),
    { value: "connector", label: "An app on another machine", hint: "A computer at home or in an office that cannot receive connections. It connects out to Ozal.", Icon: Plug },
    { value: "redirect", label: "A redirect to another address", hint: "For an old or extra domain: every visit is sent to the new address, on the same page. Permanent (301).", Icon: ArrowUpRight },
  ];
  const audiences: Choice[] = [
    ...(base && (base.access || base.auth_required) ? [{ value: "keep", label: "Keep as it is", hint: whoMayEnter(base).text, Icon: Check }] : []),
    { value: "groups", label: "Only certain people", hint: signIn ? "People in the groups you name. Best for most private apps." : "Sign-in is not set up on this server yet.", Icon: Users, disabled: !signIn },
    { value: "signed-in", label: "Anyone with an account", hint: signIn ? "Every person who can sign in to this server." : "Sign-in is not set up on this server yet.", Icon: Lock, disabled: !signIn },
    { value: "public", label: "Everyone", hint: "No sign-in. Anyone who knows the address can open it.", Icon: Globe, warn: true },
  ];

  function nextFromType(event: FormEvent) {
    event.preventDefault(); setFormError("");
    if (kind === "static" && !files) { setFormError("Choose the .tar.gz with your site's files, or pick “An app that is already running” if something else serves it."); return; }
    if (kind === "github" && !github) { setFormError(githubState?.installed ? "Choose the repository that builds this website." : "Connect GitHub first, or pick “A static website” to upload the files yourself."); return; }
    if (kind === "github" && github && !github.branch.trim()) { setFormError("Write the branch that publishes, such as main."); return; }
    if (kind === "redirect" && !/^https?:\/\/[^/?#\s]+(?:\/[^?#\s]*)?$/i.test(redirectTo.trim())) { setFormError("Write the full new address, such as https://example.com."); return; }
    const forcedPrivacy: Privacy | undefined = kind === "redirect" || (kind === "keep" && base?.redirect) ? "public" : undefined;
    if (forcedPrivacy) setPrivacy(forcedPrivacy);
    if (base && (kind === "redirect" || (base.redirect && kind === "keep"))) { void prepare(event, forcedPrivacy); return; }
    setStep(base ? 3 : 2);
  }
  function nextAddress(event: FormEvent) {
    event.preventDefault(); setFormError("");
    if (!address || !/^[a-z0-9.-]+(?::\d+)?$/i.test(address)) { setFormError("Write an address such as app.example.com."); return; }
    if ((kind === "static" || kind === "github") && !site.trim()) setSite(slug(address));
    if (kind === "redirect") { void prepare(event); return; }
    setStep(3);
  }
  async function prepare(event: FormEvent, privacyOverride?: Privacy) {
    event.preventDefault(); setFormError(""); setPlan(null); setChecked(false);
    const accessChoice = privacyOverride ?? privacy;
    if (accessChoice === "groups" && !chosen.length) { setFormError("Name at least one group, or choose “Anyone with an account”."); return; }
    const nextRoute: ApplicationRoute = kind === "keep" ? { ...base!, host: address, auth_required: false } : { ...base, host: address, target: undefined, upstreams: undefined, site: undefined, root: undefined, connector: undefined, redirect: undefined, auth_required: false };
    if (kind === "static" || kind === "github") nextRoute.site = site.trim();
    else if (kind === "connector") nextRoute.connector = connector.trim();
    else if (kind === "redirect") nextRoute.redirect = redirectTo.trim().replace(/\/+$/, "");
    else if (kind !== "keep") nextRoute.target = target.trim();
    if (accessChoice === "public") nextRoute.access = undefined;
    else if (accessChoice !== "keep") nextRoute.access = accessChoice === "groups" ? { required_groups: chosen } : {};
    else nextRoute.access = base?.access ?? {};
    const nextDraft = { route: nextRoute, expected: status.config_sha256, access_mode: accessChoice === "public" ? "public" as const : "private" as const };
    setDraft(nextDraft); setWaiting("Checking…"); setWorkflowPlan(null);
    try {
      const checkedPlan = await planApplication(token, nextDraft);
      if (kind === "github" && github) {
        const publishing = await fetchPublishing(token);
        if (publishing.projects.some((project) => project.name === nextDraft.route.site)) throw new Error(`A website named ${nextDraft.route.site} already publishes from GitHub. Go back and choose another website name.`);
        if (nextDraft.access_mode === "public" && githubState?.workflow_available) {
          setWaiting("Preparing the workflow…");
          const preview = await previewGitHubWorkflow(token, { project: nextDraft.route.site!, repository_id: github.repository.id, page: github.page, ref: `refs/heads/${github.branch.trim()}`, workflow: workflowFile, hostname: checkedPlan.route.host, directory: github.directory.trim(), output: github.output.trim() });
          setWorkflowPlan(preview.plan);
        }
      }
      setPlan(checkedPlan); setStep(4);
    } catch (cause) { setFormError((cause as Error).message); }
    finally { setWaiting(""); }
  }
  async function save() {
    if (!draft || !plan) return;
    setFormError("");
    try { if (kind === "static" && files) { setWaiting("Uploading files…"); await deploySite(token, draft.route.site!, files); } setWaiting("Saving…"); await saveApplication(token, draft); }
    catch (cause) { setFormError(`${(cause as Error).message} Refresh and reconcile before retrying.`); setWaiting(""); return; }
    const savedHost = plan.route.host;
    if (kind === "github" && github) await connectRepository(savedHost);
    const returnTo = `${scheme}://${savedHost}/`;
    setInvitations([]);
    if (canInvite) for (const email of emails) {
      setWaiting(`Inviting ${email}…`);
      try { const result = await invitePerson(token, { email, name: "", groups: privacy === "groups" ? chosen : [], return_to: returnTo }); setInvitations((old) => [...old, { email, link: result.link }]); }
      catch (cause) { setInvitations((old) => [...old, { email, error: (cause as Error).message }]); }
    }
    setWaiting(""); setHost(savedHost); setStep(5);
  }
  async function connectRepository(hostname: string) {
    if (!github || !draft) return;
    const projectName = draft.route.site!; setGithubResult({ trusted: false });
    let trusted = false;
    try {
      setWaiting(`Trusting ${github.repository.full_name}…`);
      await bindGitHubProject(token, { name: projectName, site: projectName, repository_id: github.repository.id, page: github.page, ref: `refs/heads/${github.branch.trim()}`, workflow: workflowFile, expected: (await fetchPublishing(token)).config_sha256 });
      trusted = true;
      setGithubResult({ trusted: true });
      if (!workflowPlan) return;
      setWaiting("Opening the pull request…");
      const result = await proposeWorkflow(token, projectName, { directory: github.directory.trim(), output: github.output.trim(), hostname, page: github.page, expected: (await fetchPublishing(token)).config_sha256, digest: workflowPlan.digest, apply: true }, AbortSignal.timeout(65_000));
      if (result.applied) setGithubResult({ trusted: true, pr: result.pull_request });
    } catch (cause) { setGithubResult({ trusted, error: (cause as Error).message }); }
  }
  async function copyInvite(link: string) { try { await navigator.clipboard.writeText(link); setCopied(link); window.setTimeout(() => setCopied(""), 2000); } catch { setCopied(""); } }

  const title = step === 1 ? base ? `Edit ${base.host}` : "What are you putting online?" : step === 2 ? "Its address" : step === 3 ? "Who can open it?" : step === 4 ? "Review" : kind === "github" ? published ? `${site} is published` : githubResult?.pr ? "Next: merge and publish" : "Next: add the workflow" : `Connect ${host}`;
  const intro = step === 1 ? base ? "What serves this address. Nothing changes until you review and save." : "Pick what serves the address. You can change it later." : step === 2 ? "The web address people will type. Nothing changes for them until its DNS points at this server." : step === 3 ? "Ozal checks this before anything reaches the app." : step === 4 ? "Nothing changes until you save." : kind === "github" ? githubResult?.pr ? "Saved. The first build after merging publishes it here; check it before moving DNS." : "Saved. Add the workflow to publish it; check it before moving DNS." : "Saved. Point its DNS here; this panel checks until a visitor's request arrives.";
  const shownStep = base && step > 1 ? step - 1 : step;
  const totalSteps = base ? 3 : 4;
  const certificate = (value: string) => value === "automatic_on_first_visit" ? "Issued automatically on the first visit" : value === "not_covered" ? "Not possible: add its domain in Domains first" : value === "external_or_local_tls_not_verified" ? "Handled in front of this server" : "Covered by this server's certificate";

  return <>
    <Modal open onOpenChange={(open) => { if (!open && !waiting) onClose(); }} title={title} intro={intro} wide>
      {step < 5 && <ol className={`mb-5 grid gap-1.5 ${totalSteps === 3 ? "grid-cols-3" : "grid-cols-4"}`} aria-label={`Step ${shownStep} of ${totalSteps}`}>{Array.from({ length: totalSteps }, (_, index) => <li key={index} className={`h-1 rounded-full ${index + 1 <= shownStep ? "bg-primary" : "bg-muted"}`} />)}</ol>}
      {step === 1 && <form onSubmit={nextFromType} className="grid gap-5"><div className="grid gap-2"><p id="app-kind-label" className="sr-only">What serves it?</p><ChoiceGrid name="application-kind" labelId="app-kind-label" options={kinds} value={kind} onChange={(value) => { setKind(value as Kind); setFormError(""); }} /></div>
        {kind === "backend" && <div className="grid gap-2"><Label htmlFor="app-target">Where is it running?</Label><Input id="app-target" required type="url" value={target} onChange={(event) => setTarget(event.target.value)} placeholder="http://127.0.0.1:3000" className="font-mono" /><p className="text-muted-foreground text-xs">The address Ozal uses to reach the app, not the public one. If it runs on this server, it is usually <span className="font-mono">http://127.0.0.1:</span> and its port.</p></div>}
        {kind === "static" && <StaticFilePicker file={files} site={site} onFile={(file, error) => { setFiles(file); if (error) setFormError(error); }} onSite={(value) => { setSite(value); setSiteEdited(true); }} />}
        {kind === "github" && <><GitHubSource token={token} selection={github} connection={githubState} onSelection={setGithub} onConnection={setGithubState} onChanged={onChanged} /><SiteName site={site} onSite={(value) => { setSite(value); setSiteEdited(true); }} /></>}
        {kind === "connector" && <div className="grid gap-2"><Label htmlFor="app-connector">Which machine?</Label>{connectors?.length ? <select id="app-connector" required value={connector} onChange={(event) => setConnector(event.target.value)} className="border-input bg-background h-9 rounded-md border px-3 text-sm"><option value="" disabled>Choose a connector</option>{connectors.map((name) => <option key={name}>{name}</option>)}</select> : <Input id="app-connector" required value={connector} onChange={(event) => setConnector(event.target.value)} className="font-mono" placeholder="connector name" />}<p className="text-muted-foreground text-xs">{connectors && !connectors.length ? "No machine is connected yet: see Connectors to add one. " : ""}The connector on that machine must also be set to serve this address.</p></div>}
        {kind === "redirect" && <div className="grid gap-2"><Label htmlFor="app-redirect">Send visitors to</Label><Input id="app-redirect" required type="url" value={redirectTo} onChange={(event) => setRedirectTo(event.target.value)} placeholder="https://example.com" className="font-mono" /><p className="text-muted-foreground text-xs">The page is kept: <span className="font-mono">old-address/docs?x=1</span> goes to <span className="font-mono">{(redirectTo.trim() || "https://example.com").replace(/\/+$/, "")}/docs?x=1</span>. Browsers and search engines remember a permanent redirect, so use it for addresses you are retiring.</p></div>}
        {formError && <p className="text-destructive text-sm" role="alert">{formError}</p>}<div className="flex justify-end gap-2"><Button variant="ghost" onClick={onClose}>Cancel</Button><Button type="submit">Next</Button></div>
      </form>}
      {step === 2 && <form onSubmit={nextAddress} className="grid gap-5"><div className="grid gap-2"><Label htmlFor="app-host">Address</Label>{zones.length ? <><div className="flex flex-wrap items-center gap-2">{zone && <><Input id="app-host" value={sub} onChange={(event) => setSub(event.target.value)} placeholder="app" autoComplete="off" spellCheck={false} className="w-40 flex-1 font-mono" aria-describedby="app-host-help" /><span className="text-muted-foreground font-mono text-sm">.</span></>}<select value={zone} onChange={(event) => { setZone(event.target.value); setHost(""); }} aria-label="Domain" className="border-input bg-background h-9 rounded-md border px-3 font-mono text-sm">{zones.map((name) => <option key={name}>{name}</option>)}<option value="">another address…</option></select>{!zone && <Input id="app-host" value={host} onChange={(event) => setHost(event.target.value)} placeholder="app.example.com" autoComplete="off" spellCheck={false} className="min-w-48 flex-1 font-mono" aria-describedby="app-host-help" />}</div><p id="app-host-help" className="text-muted-foreground text-xs">{zone ? sub.trim() ? `${composed} · ` : `Leave empty to use ${zone} itself · ` : ""}{covered(address) ? `HTTPS is covered by the certificate for ${covered(address)}.` : address ? "This address is outside your domains: add its domain in Domains for HTTPS." : ""}</p></> : <><Input id="app-host" required value={host} onChange={(event) => setHost(event.target.value)} placeholder="app.example.com" autoComplete="off" spellCheck={false} className="font-mono" aria-describedby="app-host-help" /><p id="app-host-help" className="text-muted-foreground text-xs">A new address, or one you are moving here from other hosting. To try first, use a spare one such as <span className="font-mono">preview.</span> on your domain.</p></>}</div>{formError && <p className="text-destructive text-sm" role="alert">{formError}</p>}<div className="flex flex-wrap justify-between gap-2"><Button variant="ghost" onClick={() => { setStep(1); setFormError(""); }}><ArrowLeft className="size-4" aria-hidden="true" />Back</Button><Button type="submit">Next</Button></div></form>}
      {step === 3 && <form onSubmit={prepare} className="grid gap-5"><SignInFlow host={address} /><div className="grid gap-2"><p id="app-privacy-label" className="sr-only">Who can open it?</p><ChoiceGrid name="application-privacy" labelId="app-privacy-label" options={audiences} value={privacy} onChange={(value) => setPrivacy(value as Privacy)} /></div>{privacy === "groups" && <div className="grid gap-2"><Label htmlFor="app-groups">Groups <span className="text-muted-foreground font-normal">· a person must be in all of them</span></Label><Input id="app-groups" required value={groupText} onChange={(event) => setGroupText(event.target.value)} placeholder="team" autoComplete="off" />{suggestions.length ? <div className="flex flex-wrap items-center gap-1.5"><span className="text-muted-foreground text-xs">Existing:</span>{suggestions.map((group) => <button key={group} type="button" className="chip chip-add" onClick={() => setGroupText([...chosen, group].join(", "))}>+ {group}</button>)}</div> : <p className="text-muted-foreground text-xs">A group is just a name. People get it when you invite them or in People.</p>}</div>}{canInvite && <div className="grid gap-2"><Label htmlFor="app-invite">Invite people <span className="text-muted-foreground font-normal">· optional</span></Label><textarea id="app-invite" value={inviteText} onChange={(event) => setInviteText(event.target.value)} rows={2} placeholder="ana@example.com, luis@example.com" className="border-input bg-transparent placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 rounded-md border px-3 py-2 text-sm outline-none focus-visible:ring-3" /><p className="text-muted-foreground text-xs">Each person gets a one-time link to create their passkey{privacy === "groups" && chosen.length ? `, already in ${chosen.join(" and ")}` : ""}. You copy the links after saving.</p></div>}{formError && <p className="text-destructive text-sm" role="alert">{formError}</p>}<div className="flex flex-wrap justify-between gap-2"><Button variant="ghost" onClick={() => { setStep(base ? 1 : 2); setFormError(""); }}><ArrowLeft className="size-4" aria-hidden="true" />Back</Button><Button type="submit" disabled={!!waiting}>{waiting || "Review"}</Button></div></form>}
      {step === 4 && plan && draft && <ReviewStep plan={plan} draft={draft} kind={kind} privacy={privacy} chosen={chosen} emails={canInvite ? emails : []} file={files} github={github} workflowPlan={workflowPlan} checked={checked} waiting={waiting} error={formError} onChecked={setChecked} onBack={() => { setStep(kind === "redirect" || base?.redirect ? base ? 1 : 2 : 3); setFormError(""); }} onSave={() => setReview(true)} certificate={certificate} />}
      {step === 5 && <SuccessStep host={host} site={site} kind={kind} https={status.tls_enabled} github={github} result={githubResult} published={published} invitations={invitations} copied={copied} onCopy={(link) => void copyInvite(link)} onDns={() => setDnsOpen(true)} onClose={onSaved} onSites={() => { onClose(); navigate("/sites"); }} />}
    </Modal>
    <Modal open={review} onOpenChange={setReview} title="Save this application?" intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={draft?.route.host ?? ""} mono /><ReviewRow label="Current" value={base ? "Existing application" : "Not configured"} /><ReviewRow label="Requested" value={draft?.access_mode === "public" ? "Public application" : "Private application"} /><ReviewRow label="Consequence" value={kind === "static" && files ? "The files are published, then the address starts serving them. DNS and any previous hosting are not changed." : kind === "github" ? workflowPlan ? "The address is saved, the repository becomes the only one that can publish it, and a pull request adding its workflow is opened. Nothing is built until you merge it. DNS is not changed." : "The address is saved and the repository becomes the only one that can publish it. It shows content after the first publication. DNS is not changed." : "The address starts serving right away. DNS and any previous hosting are not changed."} /><ReviewRow label="Details" value="Try it signed in and signed out before moving a live address. To undo, remove it from Applications." /></dl>{formError && <p className="text-destructive mt-3 text-sm" role="alert">{formError}</p>}<div className="mt-5 flex justify-end gap-2"><Button variant="outline" disabled={!!waiting} onClick={() => setReview(false)}>Cancel</Button><Button disabled={!!waiting} onClick={() => { setReview(false); void save(); }}>{waiting || "Confirm and save"}</Button></div></Modal>
    {dnsOpen && <DomainConnect host={host} https={status.tls_enabled} token={token} onClose={() => setDnsOpen(false)} />}
  </>;
}

function ChoiceGrid({ name, labelId, options, value, onChange }: { name: string; labelId: string; options: Choice[]; value: string; onChange(value: string): void }) {
  return <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-labelledby={labelId}>{options.map(({ value: option, label, hint, Icon, disabled, warn }) => <label key={option} className={`border-input flex items-start gap-3 rounded-lg border p-3 transition-colors ${disabled ? "cursor-not-allowed opacity-50" : "hover:bg-accent/50 cursor-pointer"} has-[:checked]:border-primary has-[:checked]:bg-primary/[0.07] has-[:focus-visible]:ring-ring/50 has-[:focus-visible]:ring-3`}><input type="radio" name={name} value={option} checked={value === option} disabled={disabled} onChange={() => onChange(option)} className="sr-only" /><span className={`grid size-8 shrink-0 place-items-center rounded-md ${value === option ? "bg-primary/15 text-signal" : "bg-muted text-muted-foreground"}`}><Icon className="size-4" aria-hidden="true" /></span><span className="min-w-0"><span className={`block text-sm font-medium ${warn && value === option ? "text-warning" : ""}`}>{label}</span><span className="text-muted-foreground block text-xs leading-snug">{hint}</span></span></label>)}</div>;
}

function StaticFilePicker({ file, site, onFile, onSite }: { file: File | null; site: string; onFile(file: File | null, error?: string): void; onSite(value: string): void }) {
  const [copied, setCopied] = useState(false);
  async function copy() { try { await navigator.clipboard.writeText("tar -czf site.tar.gz -C dist ."); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); } }
  return <div className="grid gap-2"><span className="text-sm font-medium">Your site's files</span><p className="text-muted-foreground text-xs">Pack the folder your build produced (often <span className="font-mono">dist</span>) into one file, then choose it here.</p><div className="bg-background border-border flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 font-mono text-xs">tar -czf site.tar.gz -C dist .</code><button type="button" onClick={() => void copy()} aria-label="Copy packing command" className="text-muted-foreground rounded p-1">{copied ? "Copied" : "Copy"}</button></div><label className="border-input hover:border-ring has-[:focus-visible]:ring-ring/50 mt-1 flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-4 transition-colors has-[:focus-visible]:ring-3"><span className="bg-muted grid size-9 shrink-0 place-items-center rounded-lg"><Layers className="text-muted-foreground size-4" aria-hidden="true" /></span><span className="min-w-0 text-sm">{file ? <><span className="block truncate font-medium">{file.name}</span><span className="text-muted-foreground text-xs">{file.size < 1048576 ? `${Math.max(1, Math.round(file.size / 1024))} KiB` : `${(file.size / 1048576).toFixed(1)} MiB`} · choose another</span></> : <><span className="block font-medium">Choose site.tar.gz</span><span className="text-muted-foreground text-xs">Up to 100 MiB. Ozal stores the files; it does not run your build.</span></>}</span><input type="file" accept=".gz,.tgz" onChange={(event) => { const selected = event.target.files?.[0] ?? null; onFile(selected && selected.size <= 100 * 1024 * 1024 ? selected : null, selected && selected.size > 100 * 1024 * 1024 ? "The file is larger than 100 MiB." : undefined); }} className="sr-only" /></label><details className="text-muted-foreground text-xs"><summary className="cursor-pointer">Website name: <span className="text-foreground font-mono">{site || "taken from its address"}</span></summary><SiteName site={site} onSite={onSite} /></details></div>;
}
function SiteName({ site, onSite }: { site: string; onSite(value: string): void }) { return <><Input value={site} onChange={(event) => onSite(event.target.value)} pattern="[a-z0-9]+(-[a-z0-9]+)*" className="mt-2 font-mono" aria-label="Website name" /><p className="mt-1">The name its versions are kept under in Sites.</p></>; }

function SignInFlow({ host }: { host: string }) { const steps = [{ title: "A visitor opens", detail: host || "your app's address" }, { title: "Ozal asks them to sign in", detail: "With a passkey: fingerprint, face or PIN. No passwords." }, { title: "Ozal checks your rule", detail: "Only the people allowed for that address get through. Others see a “no access” page." }, { title: "Your app receives them", detail: "Already signed in, with their email. The app needs no login of its own." }]; return <section className="bg-muted/40 rounded-lg border p-4" aria-labelledby="wizard-signin"><h3 id="wizard-signin" className="text-sm font-medium">How private access works</h3><ol className="mt-3 grid gap-3 sm:grid-cols-4 sm:gap-2">{steps.map((step, index) => <li key={step.title} className="flex gap-2.5 sm:flex-col sm:gap-1.5"><span className="bg-background text-muted-foreground grid size-6 shrink-0 place-items-center rounded-full border text-xs tabular-nums" aria-hidden="true">{index + 1}</span><span className="min-w-0"><span className="block text-xs font-medium">{step.title}</span><span className="text-muted-foreground block break-words text-xs leading-snug">{step.detail}</span></span></li>)}</ol></section>; }

function ReviewStep({ plan, draft, kind, privacy, chosen, emails, file, github, workflowPlan, checked, waiting, error, onChecked, onBack, onSave, certificate }: { plan: ApplicationPlan; draft: ApplicationDraft; kind: Kind; privacy: Privacy; chosen: string[]; emails: string[]; file: File | null; github: GitHubSelection | null; workflowPlan: WorkflowPlan | null; checked: boolean; waiting: string; error: string; onChecked(value: boolean): void; onBack(): void; onSave(): void; certificate(value: string): string }) {
  const access = draft.route.redirect ? "Everyone: they are sent to the new address" : draft.access_mode === "public" ? "Everyone, without signing in" : privacy === "groups" && chosen.length ? `People in ${chosen.join(" and ")}, after signing in` : privacy === "signed-in" ? "Anyone with an account, after signing in" : whoMayEnter(draft.route).text;
  return <><dl className="divide-y rounded-lg border text-sm"><ReviewRow label="Address" value={plan.route.host} /><ReviewRow label="Served by" value={`${servedBy(draft.route)}${kind === "static" && file ? ` · uploads ${file.name}` : kind === "github" && github ? ` · built from ${github.repository.full_name} (${github.branch.trim()})` : ""}`} />{kind === "github" && <ReviewRow label="On GitHub" value={workflowPlan ? `Opens a pull request that adds ${workflowPlan.path} to ${workflowPlan.base}. Ozal never merges it.` : draft.access_mode !== "public" ? "No pull request: a private website needs a workflow you write and review yourself. The repository is trusted to publish." : "No pull request: this server has no deploy endpoint for GitHub Actions. The repository is trusted to publish."} />}<ReviewRow label="Who can open it" value={access} warning={draft.access_mode === "public" && !draft.route.redirect} />{emails.length > 0 && <ReviewRow label="Invitations" value={emails.join(", ")} />}<ReviewRow label="HTTPS" value={certificate(plan.tls)} warning={plan.tls === "not_covered"} /><ReviewRow label="DNS" value="Shown after saving, with a live check" /></dl>{workflowPlan && <details className="mt-3 text-sm"><summary className="text-muted-foreground cursor-pointer">Read the workflow it adds</summary><pre className="bg-background mt-2 max-h-72 overflow-auto rounded-md border p-3 text-xs" tabIndex={0} aria-label="Generated workflow"><code>{workflowPlan.content}</code></pre></details>}{plan.checks.length > 0 && <><p className="mt-5 mb-2 text-sm font-medium">Before moving a live address</p><ul className="text-muted-foreground space-y-1.5 text-sm">{plan.checks.map((item) => <li key={item} className="flex gap-2"><Check className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />{item}</li>)}</ul></>}<label className="bg-muted/50 mt-5 flex items-start gap-3 rounded-lg p-3 text-sm"><input type="checkbox" checked={checked} onChange={(event) => onChecked(event.target.checked)} className="accent-primary mt-0.5" />I checked what serves it and who can open it. I will try it before moving a live address.</label>{error && <p className="text-destructive mt-3 text-sm" role="alert">{error}</p>}{waiting && <p className="text-muted-foreground mt-3 text-sm" role="status">{waiting}</p>}<div className="mt-5 flex flex-wrap justify-between gap-2"><Button variant="ghost" disabled={!!waiting} onClick={onBack}><ArrowLeft className="size-4" aria-hidden="true" />Back</Button><Button disabled={!checked || !!waiting || plan.tls === "not_covered"} onClick={onSave}>Save application</Button></div></>;
}

function SuccessStep({ host, site, kind, https, github, result, published, invitations, copied, onCopy, onDns, onClose, onSites }: { host: string; site: string; kind: Kind; https: boolean; github: GitHubSelection | null; result: { trusted: boolean; pr?: { url: string; number: number }; error?: string } | null; published: Deployment | null; invitations: Invite[]; copied: string; onCopy(link: string): void; onDns(): void; onClose(): void; onSites(): void }) {
  return <><ol className="grid gap-3 text-sm">{kind === "github" ? <><li className="flex gap-2"><Check className="text-signal mt-0.5 size-4 shrink-0" aria-hidden="true" /><span><strong>{host}</strong> is saved as the website <span className="font-mono text-xs">{site}</span>.</span></li><li className="flex gap-2"><span className={`lamp mt-1.5 shrink-0 ${result?.trusted ? "lamp-on" : "lamp-warn"}`} aria-hidden="true" /><span>{result?.trusted ? `Only ${github?.repository.full_name}, branch ${github?.branch.trim()}, can publish it.` : "The repository is not trusted yet. Choose it in Sites."}</span></li>{result?.pr ? <li className="flex gap-2"><span className="lamp lamp-on mt-1.5 shrink-0" aria-hidden="true" /><span>Review and merge <a className="underline underline-offset-4" href={result.pr.url} target="_blank" rel="noopener noreferrer">pull request #{result.pr.number}</a> on GitHub. Ozal does not merge it.</span></li> : result?.trusted && <li className="flex gap-2"><span className="lamp lamp-warn mt-1.5 shrink-0" aria-hidden="true" /><span>Add the publishing workflow to the repository{result.error ? "" : " yourself"}; Sites shows what it must contain.</span></li>}{published ? <li className="flex gap-2"><Check className="text-signal mt-0.5 size-4 shrink-0" aria-hidden="true" /><span>Published version <span className="font-mono text-xs">{published.id.slice(0, 12)}</span>. Open <a className="underline" href={`${https ? "https" : "http"}://${host}/`} target="_blank" rel="noreferrer">{host}</a> to check it.</span></li> : result?.pr ? <li className="flex gap-2"><span className="lamp lamp-wait mt-1.5 shrink-0 animate-pulse" aria-hidden="true" /><span className="text-muted-foreground" role="status">After you merge it, the first build publishes here. This panel checks every few seconds; you can close it and follow it in Sites.</span></li> : <li className="flex gap-2"><span className="lamp mt-1.5 shrink-0" aria-hidden="true" /><span className="text-muted-foreground">It publishes once the repository has the workflow and something is pushed to {github?.branch.trim()}. Follow it in Sites.</span></li>}</> : <li className="flex gap-2"><Check className="text-signal mt-0.5 size-4 shrink-0" aria-hidden="true" /><span>{host} is saved. Set up its DNS record and test the connection before moving a live address.</span></li>}</ol>{result?.error && <p className="text-destructive mt-3 text-sm" role="alert">The address is saved, but GitHub did not finish: {result.error} Continue in Sites.</p>}{(kind !== "github" || published) && <div className="mt-6 flex justify-end"><Button variant="outline" onClick={onDns}>DNS &amp; HTTPS</Button></div>}{invitations.length > 0 && <section className="mt-6 border-t pt-5" aria-labelledby="invites-title"><h3 id="invites-title" className="text-sm font-medium">Invitations</h3><p className="text-muted-foreground mb-3 text-xs">Each link works once and expires in 15 minutes. Send it over a channel you trust; it is shown only now.</p><ul className="divide-y rounded-lg border">{invitations.map((invite) => <li key={invite.email} className="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5"><span className="min-w-0 truncate text-sm">{invite.email}</span>{invite.link ? <Button variant="outline" size="sm" aria-label={`Copy invitation for ${invite.email}`} onClick={() => onCopy(invite.link!)}>{copied === invite.link ? "Copied" : "Copy link"}</Button> : <span className="text-destructive text-xs">{invite.error}</span>}</li>)}</ul></section>}<div className="mt-5 flex flex-wrap justify-end gap-2"><Button variant={kind === "github" ? "outline" : "default"} onClick={onClose}>Done</Button>{kind === "github" && <Button onClick={onSites}>Open Sites</Button>}</div></>;
}

function ReviewRow({ label, value, warning = false, mono = false }: { label: string; value: string; warning?: boolean; mono?: boolean }) { return <div className="grid gap-1 px-4 py-2.5 sm:grid-cols-[9rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className={`min-w-0 break-words ${mono ? "font-mono" : ""} ${warning ? "text-warning" : ""}`}>{value}</dd></div>; }
