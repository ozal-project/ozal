<script lang="ts">
  import Summary from "$lib/Summary.svelte";
  import { applicationAccess } from "$lib/wording";
  import { onMount, type Component } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Modal from "$lib/Modal.svelte";
  import DomainConnect from "$lib/DomainConnect.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { admin, askForToken, can } from "$lib/adminToken.svelte";
  import { setupStatus, planApplication, saveApplication, fetchGroups, deploySite, invitePerson, type SetupStatus, type ApplicationRoute, type ApplicationPlan, type ApplicationDraft } from "$lib/api";
  import { navigate } from "$lib/router.svelte";
  import ApplicationRemove from "$lib/ApplicationRemove.svelte";
  import ArtifactUpload from "$lib/ArtifactUpload.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import PlusIcon from "@lucide/svelte/icons/plus";
  import ServerIcon from "@lucide/svelte/icons/server";
  import LayersIcon from "@lucide/svelte/icons/layers";
  import PlugIcon from "@lucide/svelte/icons/plug";
  import CheckIcon from "@lucide/svelte/icons/check";
  import UsersIcon from "@lucide/svelte/icons/users";
  import LockIcon from "@lucide/svelte/icons/lock";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import ScrollTextIcon from "@lucide/svelte/icons/scroll-text";
  import VersionsIcon from "@lucide/svelte/icons/rotate-ccw-clock";
  import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
  import FileArchiveIcon from "@lucide/svelte/icons/file-archive";
  import CopyIcon from "@lucide/svelte/icons/copy";

  let status = $state<SetupStatus | null>(null);
  let known = $state<string[]>([]);
  let error = $state("");
  let loading = $state(false);

  // The "Add website" flow (UI-006): 1 what and where, 2 who may enter,
  // 3 review, 4 connect the domain. Saving uploads the build, saves the route
  // and sends the invitations; step 4 then checks DNS, certificate and reach.
  let editing = $state(false);
  let step = $state<1 | 2 | 3 | 4>(1);
  let base = $state<ApplicationRoute | null>(null);
  let host = $state("");
  let kind = $state("backend");
  let target = $state("");
  let site = $state("");
  let siteEdited = $state(false);
  let files = $state<File | null>(null);
  let connector = $state("");
  let privacy = $state("groups");
  let groups = $state("");
  let inviteText = $state("");
  let plan = $state<ApplicationPlan | null>(null);
  let draft = $state<ApplicationDraft | null>(null);
  let formError = $state("");
  let review = $state(false);
  let waiting = $state("");
  let checked = $state(false);
  let invitations = $state<{ email: string; link?: string; error?: string }[]>([]);
  let copiedLink = $state("");
  let connectHost = $state<string | null>(null);
  let connectOpen = $state(false);

  const load = async () => {
    if (!admin.token.trim()) { askForToken(); return; }
    loading = true; error = "";
    try {
      const [next, catalog] = await Promise.all([setupStatus(admin.token.trim()), fetchGroups(admin.token.trim()).catch(() => null)]);
      status = next;
      if (catalog) known = catalog.map((g) => g.name);
    }
    catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  };
  onMount(() => { if (admin.token.trim()) void load(); });

  function edit(route: ApplicationRoute | null) {
    base = route; host = route?.host ?? ""; target = route?.target ?? ""; site = route?.site ?? ""; siteEdited = !!route?.site;
    connector = route?.connector ?? ""; kind = route?.root || route?.upstreams?.length ? "keep" : route?.site ? "static" : route?.connector ? "connector" : "backend";
    privacy = route ? route.access || route.auth_required ? "keep" : "public" : "groups";
    groups = ""; inviteText = ""; files = null; invitations = [];
    step = 1; plan = null; draft = null; formError = ""; waiting = ""; editing = true;
  }

  // A site name derived from the address, so a static build needs no name.
  const slug = (value: string) => value.toLowerCase().replace(/:\d+$/, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63).replace(/-+$/, "");
  $effect(() => { if (!siteEdited && (kind === "static" || kind === "github")) site = slug(host); });

  const chosen = $derived([...new Set(groups.split(",").map((g) => g.trim()).filter(Boolean))]);
  const suggestions = $derived(known.filter((g) => !chosen.includes(g)));
  const addGroup = (group: string) => { groups = [...chosen, group].join(", "); };
  const emails = $derived([...new Set(inviteText.split(/[\s,;]+/).map((e) => e.trim()).filter((e) => e.includes("@")))]);
  const passkeys = $derived(!!status?.methods.includes("passkey"));
  const canInvite = $derived(passkeys && privacy !== "public" && privacy !== "keep");
  const scheme = $derived(status?.tls_enabled ? "https" : "http");

  function next(e: SubmitEvent) {
    e.preventDefault(); formError = "";
    if (kind === "static" && !base?.site && !files) { formError = "Choose the built files to publish, or pick “Running application” if it is served elsewhere."; return; }
    step = 2;
  }

  async function prepare(e: SubmitEvent) {
    e.preventDefault(); if (!status) return;
    formError = ""; plan = null; checked = false;
    if (privacy === "groups" && !chosen.length) { formError = "Name at least one group, or choose any signed-in person."; return; }
    const route: ApplicationRoute = kind === "keep" ? { ...base!, host: host.trim(), auth_required: false } : { ...base, host: host.trim(), target: undefined, upstreams: undefined, site: undefined, root: undefined, connector: undefined, auth_required: false };
    if (kind === "static" || kind === "github") route.site = site.trim();
    else if (kind === "connector") route.connector = connector.trim();
    else if (kind !== "keep") route.target = target.trim();
    if (privacy === "public") route.access = undefined;
    else if (privacy !== "keep") route.access = privacy === "groups" ? { required_groups: chosen } : {};
    else route.access = base?.access ?? {};
    draft = { route, expected: status.config_sha256, access_mode: privacy === "public" ? "public" : "private" };
    waiting = "Checking…";
    try { plan = await planApplication(admin.token.trim(), draft); step = 3; }
    catch (e) { formError = (e as Error).message; }
    finally { waiting = ""; }
  }

  async function save() {
    if (!draft || !plan) return;
    const token = admin.token.trim();
    formError = "";
    try {
      // The build goes live under its own site name first; no hostname
      // serves it until the route below is saved.
      if (kind === "static" && files) { waiting = "Uploading files…"; await deploySite(token, draft.route.site!, files); }
      waiting = "Saving…";
      await saveApplication(token, draft);
    } catch (e) {
      formError = `${(e as Error).message} Refresh and reconcile before retrying.`;
      waiting = ""; return;
    }
    const saved = plan.route.host;
    const returnTo = `${scheme}://${saved}/`;
    invitations = [];
    if (canInvite) {
      for (const email of emails) {
        waiting = `Inviting ${email}…`;
        try { const created = await invitePerson(token, { email, name: "", groups: privacy === "groups" ? chosen : [], return_to: returnTo }); invitations = [...invitations, { email, link: created.link }]; }
        catch (e) { invitations = [...invitations, { email, error: (e as Error).message }]; }
      }
    }
    waiting = ""; host = saved; step = 4;
    await load();
  }

  async function copyLink(link: string) {
    await navigator.clipboard.writeText(link).then(() => { copiedLink = link; setTimeout(() => (copiedLink = ""), 2000); }).catch(() => {});
  }

  type Choice = { value: string; label: string; hint: string; icon: Component; disabled?: boolean; tone?: "warn" };
  const kinds = $derived<Choice[]>([
    ...(base ? [{ value: "keep", label: "Keep current destination", hint: "Only change who may enter.", icon: CheckIcon }] : []),
    { value: "backend", label: "Running application", hint: "A service reached by a private URL.", icon: ServerIcon },
    { value: "static", label: "Static build", hint: status?.pages_enabled ? "Upload the built files; Ozal serves them." : "Pages is not enabled on this edge.", icon: LayersIcon, disabled: !status?.pages_enabled },
    ...(!base ? [{ value: "github", label: "From GitHub", hint: !status?.pages_enabled ? "Pages is not enabled on this edge." : !can("publishing") ? "Needs the CI publishing tool in this session." : "Connect a repository and review its publishing workflow.", icon: LayersIcon, disabled: !status?.pages_enabled || !can("publishing") }] : []),
    { value: "connector", label: "Through a connector", hint: "A private host enrolled with a connector.", icon: PlugIcon },
  ]);
  const signIn = $derived(!!status?.methods.length);
  const audiences = $derived<Choice[]>([
    ...(base && (base.access || base.auth_required) ? [{ value: "keep", label: "Keep current policy", hint: "Who may enter stays as it is.", icon: CheckIcon }] : []),
    { value: "groups", label: "Members of groups", hint: signIn ? "Signed in and in every group named." : "Needs a sign-in method first.", icon: UsersIcon, disabled: !signIn },
    { value: "signed-in", label: "Any signed-in person", hint: signIn ? "Anyone with an account on this edge." : "Needs a sign-in method first.", icon: LockIcon, disabled: !signIn },
    { value: "public", label: "Everyone", hint: "No login. Anyone with the address.", icon: GlobeIcon, tone: "warn" },
  ]);

  const required = (app: ApplicationRoute) => ((app.access?.required_groups as string[] | undefined) ?? []);
  const isPrivate = (app: ApplicationRoute) => !!(app.access || app.auth_required);
  const icon = (app: ApplicationRoute) => (app.site ? LayersIcon : app.connector ? PlugIcon : ServerIcon);
  const destination = (app: ApplicationRoute) => app.site ? `Static site · ${app.site}` : app.connector ? `Connector · ${app.connector}` : app.target ?? app.root ?? `${app.upstreams?.length ?? 0} upstreams`;
  const certificate = (value: string) => value === "automatic_on_first_visit" ? "Issued automatically on the first visit" : value === "not_covered" ? "Not possible for this hostname on this edge" : value === "external_or_local_tls_not_verified" ? "Handled in front of this edge" : "Covered by this edge's certificate";
  const size = (bytes: number) => (bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`);
  const titles = $derived({
    1: base ? `Configure ${base.host}` : "Add a website",
    2: "Who may enter?",
    3: `Review ${plan?.route.host ?? host}`,
    4: kind === "github" ? `Next: publish ${site}` : `Connect ${host}`,
  }[step]);
  const intros = $derived({
    1: "Its address and what serves it. Nothing changes for visitors until its DNS points here.",
    2: "Ozal checks this before anything reaches the website.",
    3: "Nothing changes until you save.",
    4: kind === "github" ? "Application saved. Connect its repository before moving DNS." : "Application saved. Point its DNS here; this panel checks until a public request arrives.",
  }[step]);
</script>

{#snippet choices(name: string, options: Choice[], value: string, set: (v: string) => void)}
  <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-labelledby={`${name}-label`}>
    {#each options as option (option.value)}
      {@const Icon = option.icon}
      <label class={["border-input flex items-start gap-3 rounded-lg border p-3 transition-colors",
        option.disabled ? "cursor-not-allowed opacity-50" : "hover:bg-accent/50 cursor-pointer",
        "has-[:checked]:border-primary has-[:checked]:bg-primary/[0.07] has-[:focus-visible]:ring-ring/50 has-[:focus-visible]:ring-3"]}>
        <input type="radio" {name} value={option.value} checked={value === option.value} disabled={option.disabled} onchange={() => set(option.value)} class="sr-only" />
        <span class={["grid size-8 shrink-0 place-items-center rounded-md", value === option.value ? "bg-primary/15 text-signal" : "bg-muted text-muted-foreground"]}><Icon class="size-4" /></span>
        <span class="min-w-0">
          <span class={["block text-sm font-medium", option.tone === "warn" && value === option.value && "text-warning"]}>{option.label}</span>
          <span class="text-muted-foreground block text-xs leading-snug">{option.hint}</span>
        </span>
      </label>
    {/each}
  </div>
{/snippet}

<PageHeader title="Applications" intro="Websites behind Ozal and who may open each one." tabs={[...(can("applications") ? [{ path: "/applications" as const, label: "Manage" }] : []), { path: "/routes" as const, label: "Routing & health" }]}>
  <Button variant="outline" size="sm" aria-label="Refresh configuration" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
  <Button size="sm" onclick={() => edit(null)} disabled={!status || !!waiting}><PlusIcon class="size-3.5" /> Add website</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if !status}
  <Panel title="Start with this installation" label="Setup">
    <p class="text-muted-foreground mb-4 text-sm">Sign in as admin (top right), then read the configuration. No visitor account becomes an administrator.</p>
    <Button onclick={load} disabled={loading}>{loading ? "Reading…" : "Read installation"}</Button>
    <p class="text-muted-foreground mt-3 text-sm">For a fresh local installation, run <code>ozal setup -dir ./my-edge</code> on its host. The generated guide covers identity, invitations, restart and recovery.</p>
  </Panel>
{:else}
  <Summary label="Applications summary" items={[
    { value: status.routes.length, label: "applications" },
    { value: status.routes.filter(isPrivate).length, label: "private" },
    { value: status.routes.filter((r) => r.site).length, label: "static sites" },
    ...(status.methods.length ? [] : [{ value: "No", label: "sign-in method configured", tone: "warn" as const }]),
  ]} />
  {#if status.routes.length === 0}
    <div class="panel px-5 py-10 text-center">
      <p class="font-medium">No applications yet</p>
      <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Add a website: its address, what serves it and who may enter. Ozal then shows the one DNS record it needs and checks it.</p>
      <Button class="mt-4" onclick={() => edit(null)}><PlusIcon class="size-4" /> Add website</Button>
    </div>
  {:else}
    <ul class="panel divide-y" aria-label="Applications">
      {#each status.routes as app (app.host)}
        {@const Icon = icon(app)}
        {@const needs = required(app)}
        <li class="grid gap-x-6 gap-y-3 px-4 py-3.5 sm:px-5 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_19rem] md:items-center">
          <div class="flex min-w-0 items-center gap-3">
            <span class="text-muted-foreground grid size-9 shrink-0 place-items-center"><Icon class="size-4" /></span>
            <div class="min-w-0">
              <h2 class="truncate text-sm font-medium">{app.host}</h2>
              <p class="text-muted-foreground truncate font-mono text-xs">
                {#if app.connector}<button class="hover:text-foreground underline-offset-2 hover:underline" onclick={() => navigate("/connectors")}>{destination(app)}</button>{:else}{destination(app)}{/if}
              </p>
            </div>
          </div>
          <div class="flex min-w-0 flex-wrap items-center gap-1.5 pl-12 text-xs md:pl-0">
            {#if !isPrivate(app)}
              <span class="text-warning flex items-center gap-1.5"><span class="lamp lamp-warn" aria-hidden="true"></span>Public · no login</span>
            {:else}
              <span class="text-muted-foreground flex items-center gap-1.5"><span class="lamp lamp-on" aria-hidden="true"></span>{applicationAccess(app)}</span>
              {#each needs as group (group)}<button class="chip" title="Open groups" onclick={() => navigate("/groups")}>{group}</button>{/each}
            {/if}
          </div>
          <div class="flex flex-wrap items-center gap-1 md:justify-end">
            <Button variant="outline" size="sm" aria-label={`Configure ${app.host}`} onclick={() => edit(app)}>Configure</Button>
            {#if app.site}<ArtifactUpload site={app.site} />{/if}
            <div class="flex items-center">
              <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={`Connection of ${app.host}`} title="Domain connection" onclick={() => { connectHost = app.host; connectOpen = true; }}><GlobeIcon class="size-4" /></Button>
              <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={`Access log for ${app.host}`} title="Access log" onclick={() => navigate("/activity")}><ScrollTextIcon class="size-4" /></Button>
              {#if app.site}<Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={`Versions and rollback for ${app.site}`} title="Versions & rollback" onclick={() => navigate("/sites")}><VersionsIcon class="size-4" /></Button>{/if}
              <ApplicationRemove host={app.host} version={status.config_sha256} refreshed={load} />
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<Modal bind:open={editing} wide title={titles} intro={intros}>
  {#if step < 4}
    <ol class="mb-5 grid grid-cols-3 gap-1.5" aria-label={`Step ${step} of 3`}>
      {#each [1, 2, 3] as n (n)}<li class={["h-1 rounded-full", n <= step ? "bg-primary" : "bg-muted"]}></li>{/each}
    </ol>
  {/if}
  {#if status && step === 1}
    <form onsubmit={next} class="grid gap-5">
      <div class="grid gap-2">
        <Label for="app-host">Address</Label>
        <Input id="app-host" required bind:value={host} readonly={!!base} placeholder="app.example.com" autocomplete="off" class="font-mono" />
        {#if !base}<p class="text-muted-foreground text-xs">A new hostname, or the one you are moving here. To try first, use a spare one such as <span class="font-mono">preview.</span> on your domain.</p>{/if}
      </div>
      <div class="grid gap-2">
        <p id="app-kind-label" class="text-sm font-medium">What serves it?</p>
        {@render choices("app-kind", kinds, kind, (v) => (kind = v))}
      </div>
      {#if kind === "backend"}
        <div class="grid gap-2"><Label for="app-target">Private address of the application</Label><Input id="app-target" required type="url" bind:value={target} placeholder="http://127.0.0.1:3000" class="font-mono" /></div>
      {:else if kind === "static"}
        <div class="grid gap-2">
          <span class="text-sm font-medium">Built files</span>
          <label class="border-input hover:border-ring has-[:focus-visible]:ring-ring/50 flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-4 transition-colors has-[:focus-visible]:ring-3">
            <span class="bg-muted grid size-9 shrink-0 place-items-center rounded-lg"><FileArchiveIcon class="text-muted-foreground size-4" /></span>
            <span class="min-w-0 text-sm">
              {#if files}<span class="block truncate font-medium">{files.name}</span><span class="text-muted-foreground text-xs">{size(files.size)} · choose another</span>
              {:else}<span class="block font-medium">{base?.site ? "Optionally replace the live build" : "Choose a tar.gz of your dist folder"}</span><span class="text-muted-foreground text-xs">Up to 100 MiB. Ozal does not run build commands.</span>{/if}
            </span>
            <input id="app-files" type="file" accept=".gz,.tgz" onchange={(e) => { const f = e.currentTarget.files?.[0] ?? null; files = f && f.size <= 100 * 1024 * 1024 ? f : null; if (f && !files) formError = "The archive is larger than 100 MiB."; }} class="sr-only" />
          </label>
          <details class="text-muted-foreground text-xs">
            <summary class="cursor-pointer">Site name: <span class="text-foreground font-mono">{site || "—"}</span></summary>
            <Input id="app-site" required bind:value={site} oninput={() => (siteEdited = true)} pattern="[a-z0-9]+(-[a-z0-9]+)*" class="mt-2 font-mono" aria-label="Site name" />
            <p class="mt-1">Reuse a name to serve an existing site at this address too.</p>
          </details>
        </div>
      {:else if kind === "github"}
        <div class="grid gap-2">
          <Label for="app-github-site">Site name</Label>
          <Input id="app-github-site" required bind:value={site} oninput={() => (siteEdited = true)} pattern="[a-z0-9]+(-[a-z0-9]+)*" class="font-mono" />
          <p class="text-muted-foreground text-xs">After saving, choose a repository in Sites and review the generated pull request. A build already stored under this name will be served immediately; otherwise the address has no content until the first successful publication.</p>
        </div>
      {:else if kind === "connector"}
        <div class="grid gap-2"><Label for="app-connector">Enrolled connector name</Label><Input id="app-connector" required bind:value={connector} class="font-mono" /><p class="text-muted-foreground text-xs">The connector must also serve this exact hostname. It does not install or start your app.</p></div>
      {/if}
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex justify-end gap-2">
        <Button type="button" variant="ghost" onclick={() => (editing = false)}>Cancel</Button>
        <Button type="submit">Next</Button>
      </div>
    </form>
  {:else if status && step === 2}
    <form onsubmit={prepare} class="grid gap-5">
      <div class="grid gap-2">
        <p id="app-privacy-label" class="sr-only">Who may enter?</p>
        {@render choices("app-privacy", audiences, privacy, (v) => (privacy = v))}
      </div>
      {#if privacy === "groups"}
        <div class="grid gap-2">
          <Label for="app-groups">Groups <span class="text-muted-foreground font-normal">· a person must be in all of them</span></Label>
          <Input id="app-groups" required bind:value={groups} placeholder="ops, staff" autocomplete="off" />
          {#if suggestions.length}<div class="flex flex-wrap items-center gap-1.5"><span class="text-muted-foreground text-xs">Existing:</span>{#each suggestions as group (group)}<button type="button" class="chip chip-add" onclick={() => addGroup(group)}>+ {group}</button>{/each}</div>{/if}
        </div>
      {/if}
      {#if canInvite}
        <div class="grid gap-2">
          <Label for="app-invite">Invite people <span class="text-muted-foreground font-normal">· optional</span></Label>
          <textarea id="app-invite" bind:value={inviteText} rows="2" placeholder="ana@example.com, luis@example.com" class="border-input bg-transparent placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 rounded-md border px-3 py-2 text-sm outline-none focus-visible:ring-3"></textarea>
          <p class="text-muted-foreground text-xs">{emails.length ? `${emails.length} ${emails.length === 1 ? "person" : "people"}: each` : "Each"} gets a one-time link to create a passkey{privacy === "groups" && chosen.length ? `, already in ${chosen.join(" and ")}` : ""}. You copy the links after saving.</p>
        </div>
      {/if}
      <p class="text-muted-foreground border-t pt-4 text-xs">An app with its own login keeps that login; Ozal’s gate does not grant its internal permissions.</p>
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex flex-wrap justify-between gap-2">
        <Button type="button" variant="ghost" onclick={() => { step = 1; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
        <Button type="submit" disabled={!!waiting}>{waiting || "Review configuration"}</Button>
      </div>
    </form>
  {:else if step === 3 && plan}
    <dl class="divide-y rounded-lg border text-sm">
      {#each [
        ["Address", plan.route.host],
        ["Served by", draft ? destination(draft.route) + (kind === "static" && files ? ` · uploads ${files.name}` : kind === "github" ? " · repository selected after saving" : "") : ""],
        ["Who may enter", draft?.access_mode === "public" ? "Everyone, without login" : privacy === "groups" && chosen.length ? `Members of ${chosen.join(" and ")}` : privacy === "signed-in" ? "Any signed-in person" : "The current policy"],
        ...(canInvite && emails.length ? [["Invitations", emails.join(", ")]] : []),
        ["Certificate", certificate(plan.tls)],
        ["DNS", "Shown after saving, with a live check"],
      ] as [term, value] (term)}
        <div class="grid gap-1 px-4 py-2.5 sm:grid-cols-[9rem_1fr]">
          <dt class="text-muted-foreground">{term}</dt>
          <dd class={["min-w-0 break-words", term === "Who may enter" && draft?.access_mode === "public" && "text-warning", term === "Certificate" && plan.tls === "not_covered" && "text-destructive"]}>{value}</dd>
        </div>
      {/each}
    </dl>
    {#if plan.checks.length}
      <p class="mt-5 mb-2 text-sm font-medium">Before moving a live domain</p>
      <ul class="text-muted-foreground space-y-1.5 text-sm">{#each plan.checks as item (item)}<li class="flex gap-2"><CheckIcon class="text-muted-foreground mt-0.5 size-3.5 shrink-0" />{item}</li>{/each}</ul>
    {/if}
    <label class="bg-muted/50 mt-5 flex items-start gap-3 rounded-lg p-3 text-sm"><input type="checkbox" bind:checked={checked} class="accent-primary mt-0.5" />I reviewed the destination and who may enter. I will test it before changing the live domain.</label>
    {#if formError}<p class="text-destructive mt-3 text-sm" role="alert">{formError}</p>{/if}
    {#if waiting}<p class="text-muted-foreground mt-3 text-sm" role="status">{waiting}</p>{/if}
    <div class="mt-5 flex flex-wrap justify-between gap-2">
      <Button variant="ghost" disabled={!!waiting} onclick={() => { step = 2; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
      <Button disabled={!checked || !!waiting || plan.tls === "not_covered"} onclick={() => (review = true)}>Save reviewed application</Button>
    </div>
  {:else if step === 4}
    {#if kind === "github"}
      <div class="grid gap-3 text-sm">
        <p>In <strong>Sites</strong>, select a repository, choose <strong>{site}</strong>, and review its publisher trust. Then prepare the workflow and review its pull request in GitHub.</p>
        <p class="text-muted-foreground">No repository has been selected and no build or deployment has run yet. Check the first publication before moving DNS. A retained build with this site name may already be served by the saved route.</p>
        {#if privacy !== "public"}<p class="text-muted-foreground">The generated workflow checks a public hostname. For this private site, review a separate acceptance workflow instead.</p>{/if}
      </div>
    {:else}
      <DomainConnect {host} https={!!status?.tls_enabled} />
    {/if}
    {#if invitations.length}
      <section class="mt-6 border-t pt-5" aria-labelledby="invites-title">
        <h3 id="invites-title" class="text-sm font-medium">Invitations</h3>
        <p class="text-muted-foreground mb-3 text-xs">Each link works once and expires in 15 minutes. Send it over a channel you trust; it is shown only now.</p>
        <ul class="divide-y rounded-lg border">
          {#each invitations as invite (invite.email)}
            <li class="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5">
              <span class="min-w-0 truncate text-sm">{invite.email}</span>
              {#if invite.link}<Button variant="outline" size="sm" aria-label={`Copy invitation for ${invite.email}`} onclick={() => copyLink(invite.link!)}><CopyIcon class="size-3.5" /> {copiedLink === invite.link ? "Copied" : "Copy link"}</Button>
              {:else}<span class="text-destructive text-xs">{invite.error}</span>{/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}
    <div class="mt-5 flex flex-wrap justify-end gap-2"><Button variant={kind === "github" ? "outline" : "default"} onclick={() => (editing = false)}>Done</Button>{#if kind === "github"}<Button onclick={() => { editing = false; navigate("/sites"); }}>Continue in Sites</Button>{/if}</div>
  {/if}
</Modal>

<Modal bind:open={connectOpen} wide title={`Connect ${connectHost ?? ""}`} intro="The one DNS record this address needs, and whether visitors reach this edge.">
  {#if connectHost}
    {#key connectHost}<DomainConnect host={connectHost} https={!!status?.tls_enabled} />{/key}
  {/if}
</Modal>

<ConfirmAction bind:open={review} title="Save this application?" target={draft?.route.host ?? ""} current={base ? "Existing application" : "Not configured"} requested={draft?.access_mode === "public" ? "Public application" : "Private application"} consequence={kind === "static" && files ? "The build is published, then the routing configuration changes immediately. DNS and the previous hosting are not changed." : kind === "github" ? "The route changes immediately. A previously deployed build under this site name may be served; otherwise there is no content until publication. DNS is not changed." : "The routing configuration will change immediately. DNS and the previous hosting are not changed."} detail="Test the allowed and refused cases before moving a live domain. To undo a newly added address, remove it from Applications." confirmLabel="Save application" onConfirm={() => void save()} />
