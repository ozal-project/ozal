<script lang="ts">
  import { onMount, type Component } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Modal from "$lib/Modal.svelte";
  import DomainConnect from "$lib/DomainConnect.svelte";
  import RowMenu from "$lib/RowMenu.svelte";
  import SignInFlow from "$lib/SignInFlow.svelte";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { admin, askForToken, can } from "$lib/adminToken.svelte";
  import { setupStatus, planApplication, saveApplication, fetchGroups, fetchZones, fetchConnectors, deploySite, invitePerson, type SetupStatus, type ApplicationRoute, type ApplicationPlan, type ApplicationDraft } from "$lib/api";
  import { servedBy, whoMayEnter } from "$lib/wording";
  import { navigate } from "$lib/router.svelte";
  import ApplicationRemove from "$lib/ApplicationRemove.svelte";
  import ArtifactUpload from "$lib/ArtifactUpload.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import PlusIcon from "@lucide/svelte/icons/plus";
  import ServerIcon from "@lucide/svelte/icons/server";
  import LayersIcon from "@lucide/svelte/icons/layers";
  import PlugIcon from "@lucide/svelte/icons/plug";
  import GitIcon from "@lucide/svelte/icons/git-branch";
  import CheckIcon from "@lucide/svelte/icons/check";
  import UsersIcon from "@lucide/svelte/icons/users";
  import LockIcon from "@lucide/svelte/icons/lock";
  import GlobeIcon from "@lucide/svelte/icons/globe";
  import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
  import FileArchiveIcon from "@lucide/svelte/icons/file-archive";
  import CopyIcon from "@lucide/svelte/icons/copy";
  import ExternalIcon from "@lucide/svelte/icons/external-link";

  let status = $state<SetupStatus | null>(null);
  let known = $state<string[]>([]);
  let zones = $state<string[]>([]);
  let connectors = $state<string[] | null>(null);
  let error = $state("");
  let loading = $state(false);

  const load = async () => {
    if (!admin.token.trim()) { askForToken(); return; }
    loading = true; error = "";
    const token = admin.token.trim();
    try {
      const [next, catalog, domains, links] = await Promise.all([
        setupStatus(token), fetchGroups(token).catch(() => null), fetchZones().catch(() => null),
        can("connectors") ? fetchConnectors(token).catch(() => null) : Promise.resolve(null),
      ]);
      status = next;
      if (catalog) known = catalog.map((g) => g.name);
      zones = domains?.available ? domains.zones.map((z) => z.name) : [];
      connectors = links ? links.filter((c) => !c.revoked).map((c) => c.name) : null;
    }
    catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  };
  onMount(() => { if (admin.token.trim()) void load(); });

  // Adding an application (UI-006): 1 what serves it, 2 its address, 3 who
  // may open it, 4 review; after saving, 5 connect its DNS. Editing skips the
  // address. Saving uploads the build, saves the route and sends invitations.
  let editing = $state(false);
  let step = $state<1 | 2 | 3 | 4 | 5>(1);
  let base = $state<ApplicationRoute | null>(null);
  let host = $state("");
  let sub = $state("");
  let zone = $state("");
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
  let removeAsk = $state<Record<string, boolean>>({});
  let publishAsk = $state<Record<string, boolean>>({});

  function edit(route: ApplicationRoute | null) {
    base = route; host = route?.host ?? ""; target = route?.target ?? ""; site = route?.site ?? ""; siteEdited = !!route?.site;
    connector = route?.connector ?? ""; kind = route ? "keep" : "backend";
    sub = ""; zone = zones[0] ?? "";
    privacy = route ? route.access || route.auth_required ? "keep" : "public" : "groups";
    groups = ""; inviteText = ""; files = null; invitations = [];
    step = 1; plan = null; draft = null; formError = ""; waiting = ""; editing = true;
  }

  // The address: a name under one of this edge's domains, or any hostname.
  const composed = $derived(zone ? (sub.trim() ? `${sub.trim().toLowerCase()}.${zone}` : zone) : host.trim());
  const address = $derived(base ? base.host : composed);
  const covered = (name: string) => { const h = name.replace(/:\d+$/, ""); return zones.find((z) => h === z || (h.endsWith(`.${z}`) && !h.slice(0, -z.length - 1).includes("."))); };
  const slug = (value: string) => value.toLowerCase().replace(/:\d+$/, "").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63).replace(/-+$/, "");
  $effect(() => { if (!siteEdited && (kind === "static" || kind === "github")) site = slug(address); });

  const chosen = $derived([...new Set(groups.split(",").map((g) => g.trim()).filter(Boolean))]);
  const suggestions = $derived(known.filter((g) => !chosen.includes(g)));
  const addGroup = (group: string) => { groups = [...chosen, group].join(", "); };
  const emails = $derived([...new Set(inviteText.split(/[\s,;]+/).map((e) => e.trim()).filter((e) => e.includes("@")))]);
  const signIn = $derived(!!status?.methods.length);
  const passkeys = $derived(!!status?.methods.includes("passkey"));
  const canInvite = $derived(passkeys && privacy !== "public" && privacy !== "keep");
  const scheme = $derived(status?.tls_enabled ? "https" : "http");

  function whatNext(e: SubmitEvent) {
    e.preventDefault(); formError = "";
    if (kind === "static" && !files) { formError = "Choose the .tar.gz with your site's files, or pick “An app that is already running” if something else serves it."; return; }
    step = base ? 3 : 2;
  }
  function addressNext(e: SubmitEvent) {
    e.preventDefault(); formError = "";
    if (!address || !/^[a-z0-9.-]+(:\d+)?$/i.test(address)) { formError = "Write an address such as app.example.com."; return; }
    if ((kind === "static" || kind === "github") && !site.trim()) site = slug(address);
    step = 3;
  }

  async function prepare(e: SubmitEvent) {
    e.preventDefault(); if (!status) return;
    formError = ""; plan = null; checked = false;
    if (privacy === "groups" && !chosen.length) { formError = "Name at least one group, or choose “Anyone with an account”."; return; }
    const route: ApplicationRoute = kind === "keep" ? { ...base!, host: address, auth_required: false } : { ...base, host: address, target: undefined, upstreams: undefined, site: undefined, root: undefined, connector: undefined, auth_required: false };
    if (kind === "static" || kind === "github") route.site = site.trim();
    else if (kind === "connector") route.connector = connector.trim();
    else if (kind !== "keep") route.target = target.trim();
    if (privacy === "public") route.access = undefined;
    else if (privacy !== "keep") route.access = privacy === "groups" ? { required_groups: chosen } : {};
    else route.access = base?.access ?? {};
    draft = { route, expected: status.config_sha256, access_mode: privacy === "public" ? "public" : "private" };
    waiting = "Checking…";
    try { plan = await planApplication(admin.token.trim(), draft); step = 4; }
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
    waiting = ""; host = saved; step = 5;
    await load();
  }

  async function copyLink(link: string) {
    await navigator.clipboard.writeText(link).then(() => { copiedLink = link; setTimeout(() => (copiedLink = ""), 2000); }).catch(() => {});
  }

  type Choice = { value: string; label: string; hint: string; icon: Component; disabled?: boolean; tone?: "warn" };
  const kinds = $derived<Choice[]>([
    ...(base ? [{ value: "keep", label: `Keep: ${servedBy(base)}`, hint: "Change only who may open it.", icon: CheckIcon }] : []),
    { value: "backend", label: "An app that is already running", hint: "A dashboard, an API or any web app this server can reach, such as http://127.0.0.1:3000.", icon: ServerIcon },
    { value: "static", label: "A static website", hint: status?.pages_enabled ? "HTML, CSS and JavaScript files, like the folder a build produces. Ozal stores and serves them." : "Needs Pages enabled in this server's configuration.", icon: LayersIcon, disabled: !status?.pages_enabled },
    ...(!base ? [{ value: "github", label: "A website built from GitHub", hint: !status?.pages_enabled ? "Needs Pages enabled in this server's configuration." : !can("publishing") ? "Your session does not include CI publishing." : "Every push builds in GitHub Actions and publishes here.", icon: GitIcon, disabled: !status?.pages_enabled || !can("publishing") }] : []),
    { value: "connector", label: "An app on another machine", hint: "A computer at home or in an office that cannot receive connections. It connects out to Ozal.", icon: PlugIcon },
  ]);
  const audiences = $derived<Choice[]>([
    ...(base && (base.access || base.auth_required) ? [{ value: "keep", label: "Keep as it is", hint: whoMayEnter(base).text, icon: CheckIcon }] : []),
    { value: "groups", label: "Only certain people", hint: signIn ? "People in the groups you name. Best for most private apps." : "Sign-in is not set up on this server yet.", icon: UsersIcon, disabled: !signIn },
    { value: "signed-in", label: "Anyone with an account", hint: signIn ? "Every person who can sign in to this server." : "Sign-in is not set up on this server yet.", icon: LockIcon, disabled: !signIn },
    { value: "public", label: "Everyone", hint: "No sign-in. Anyone who knows the address can open it.", icon: GlobeIcon, tone: "warn" },
  ]);

  const isPrivate = (app: ApplicationRoute) => !!(app.access || app.auth_required);
  const icon = (app: ApplicationRoute) => (app.site ? LayersIcon : app.connector ? PlugIcon : ServerIcon);
  const certificate = (value: string) => value === "automatic_on_first_visit" ? "Issued automatically on the first visit" : value === "not_covered" ? "Not possible: add its domain in Domains first" : value === "external_or_local_tls_not_verified" ? "Handled in front of this server" : "Covered by this server's certificate";
  const size = (bytes: number) => (bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`);
  const totalSteps = $derived(base ? 3 : 4);
  const shownStep = $derived(base && step > 1 ? step - 1 : step);
  const titles = $derived({
    1: base ? `Edit ${base.host}` : "What are you putting online?",
    2: "Its address",
    3: "Who can open it?",
    4: "Review",
    5: kind === "github" ? `Next: publish ${site}` : `Connect ${host}`,
  }[step]);
  const intros = $derived({
    1: base ? "What serves this address. Nothing changes until you review and save." : "Pick what serves the address. You can change it later.",
    2: "The web address people will type. Nothing changes for them until its DNS points at this server.",
    3: "Ozal checks this before anything reaches the app.",
    4: "Nothing changes until you save.",
    5: kind === "github" ? "Saved. Connect its repository before moving DNS." : "Saved. Point its DNS here; this panel checks until a visitor's request arrives.",
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

<PageHeader title="Applications" intro="Each address Ozal serves: what is behind it and who can open it." tabs={[...(can("applications") ? [{ path: "/applications" as const, label: "Applications" }] : []), { path: "/routes" as const, label: "Routing & health" }]}>
  <Button variant="outline" size="sm" aria-label="Refresh configuration" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
  <Button size="sm" onclick={() => edit(null)} disabled={!status || !!waiting}><PlusIcon class="size-3.5" /> Add application</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if !status}
  <p class="text-muted-foreground text-sm" role="status">{loading ? "Reading this server's applications…" : "Loading…"}</p>
{:else}
  {#if !signIn}
    <div class="border-warning/40 bg-warning/[0.06] mb-6 rounded-lg border px-4 py-3 text-sm">
      <p class="font-medium">Sign-in is not set up yet, so every application is public.</p>
      <p class="text-muted-foreground mt-0.5">To make an app private, enable passkeys (<code class="font-mono text-xs">auth.passkeys</code>) or an external login in this server's configuration.</p>
    </div>
  {/if}
  {#if status.routes.length === 0}
    <div class="panel px-5 py-12 text-center">
      <p class="font-medium">Nothing online yet</p>
      <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Add an application: what serves it, its address and who can open it. Ozal then shows the one DNS record it needs and checks it for you.</p>
      <Button class="mt-4" onclick={() => edit(null)}><PlusIcon class="size-4" /> Add application</Button>
    </div>
  {:else}
    <ul class="panel divide-y" aria-label="Applications">
      {#each status.routes as app (app.host)}
        {@const Icon = icon(app)}
        {@const who = whoMayEnter(app)}
        <li class="grid gap-x-6 gap-y-3 px-4 py-4 sm:px-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto] md:items-center">
          <div class="flex min-w-0 items-center gap-3">
            <span class="bg-muted text-muted-foreground grid size-9 shrink-0 place-items-center rounded-lg"><Icon class="size-4" /></span>
            <div class="min-w-0">
              <div class="flex items-center gap-1.5"><h2 class="truncate text-sm font-medium">{app.host}</h2>
                <a href={`${scheme}://${app.host}/`} target="_blank" rel="noreferrer" class="text-muted-foreground hover:text-foreground shrink-0" aria-label={`Open ${app.host} in a new tab`}><ExternalIcon class="size-3.5" /></a></div>
              <p class="text-muted-foreground truncate text-xs">{servedBy(app)}</p>
            </div>
          </div>
          <p class={["flex min-w-0 items-center gap-2 pl-12 text-xs md:pl-0", who.tone === "public" ? "text-warning" : "text-muted-foreground"]}>
            <span class={["lamp", who.tone === "public" ? "lamp-warn" : "lamp-on"]} aria-hidden="true"></span><span class="truncate">{who.text}</span>
          </p>
          <div class="flex items-center gap-2 pl-12 md:justify-end md:pl-0">
            <Button variant="outline" size="sm" aria-label={`Edit ${app.host}`} onclick={() => edit(app)}>Edit</Button>
            <RowMenu label={`More actions for ${app.host}`} items={[
              { label: "DNS & HTTPS", hint: "The DNS record it needs and a live check", onSelect: () => { connectHost = app.host; connectOpen = true; } },
              ...(app.site ? [
                { label: "Publish a new version", hint: "Upload new files for this website", onSelect: () => { publishAsk = { ...publishAsk, [app.site!]: true }; } },
                { label: "Versions & rollback", hint: "Go back to an earlier version", onSelect: () => navigate("/sites") },
              ] : []),
              ...(app.connector ? [{ label: "Connector", hint: `Status of ${app.connector}`, onSelect: () => navigate("/connectors") }] : []),
              { label: "Who opened it", hint: "Access decisions for this address", onSelect: () => navigate("/activity") },
              { label: "Remove", hint: "Stop serving this address", destructive: true, onSelect: () => { removeAsk = { ...removeAsk, [app.host]: true }; } },
            ]} />
            {#if app.site}<ArtifactUpload site={app.site} button={false} bind:ask={() => publishAsk[app.site!] ?? false, (v) => { publishAsk = { ...publishAsk, [app.site!]: v }; }} />{/if}
            <ApplicationRemove host={app.host} version={status.config_sha256} refreshed={load} button={false} bind:ask={() => removeAsk[app.host] ?? false, (v) => { removeAsk = { ...removeAsk, [app.host]: v }; }} />
          </div>
        </li>
      {/each}
    </ul>
    <p class="text-muted-foreground mt-3 text-xs">{status.routes.length} {status.routes.length === 1 ? "application" : "applications"} · {status.routes.filter(isPrivate).length} private · {status.routes.filter((r) => !isPrivate(r)).length} public</p>
  {/if}
{/if}

<Modal bind:open={editing} wide title={titles} intro={intros}>
  {#if step < 5}
    <ol class="mb-5 grid gap-1.5" style={`grid-template-columns: repeat(${totalSteps}, minmax(0, 1fr))`} aria-label={`Step ${shownStep} of ${totalSteps}`}>
      {#each Array.from({ length: totalSteps }, (_, i) => i + 1) as n (n)}<li class={["h-1 rounded-full", n <= shownStep ? "bg-primary" : "bg-muted"]}></li>{/each}
    </ol>
  {/if}
  {#if status && step === 1}
    <form onsubmit={whatNext} class="grid gap-5">
      <div class="grid gap-2">
        <p id="app-kind-label" class="sr-only">What serves it?</p>
        {@render choices("app-kind", kinds, kind, (v) => { kind = v; formError = ""; })}
      </div>
      {#if kind === "backend"}
        <div class="grid gap-2">
          <Label for="app-target">Where is it running?</Label>
          <Input id="app-target" required type="url" bind:value={target} placeholder="http://127.0.0.1:3000" class="font-mono" />
          <p class="text-muted-foreground text-xs">The address Ozal uses to reach the app, not the public one. If it runs on this server, it is usually <span class="font-mono">http://127.0.0.1:</span> and its port.</p>
        </div>
      {:else if kind === "static"}
        <div class="grid gap-2">
          <span class="text-sm font-medium">Your site's files</span>
          <p class="text-muted-foreground text-xs">Pack the folder your build produced (often <span class="font-mono">dist</span>) into one file, then choose it here.</p>
          <CopyCommand command="tar -czf site.tar.gz -C dist ." label="Copy packing command" />
          <label class="border-input hover:border-ring has-[:focus-visible]:ring-ring/50 mt-1 flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-4 transition-colors has-[:focus-visible]:ring-3">
            <span class="bg-muted grid size-9 shrink-0 place-items-center rounded-lg"><FileArchiveIcon class="text-muted-foreground size-4" /></span>
            <span class="min-w-0 text-sm">
              {#if files}<span class="block truncate font-medium">{files.name}</span><span class="text-muted-foreground text-xs">{size(files.size)} · choose another</span>
              {:else}<span class="block font-medium">Choose site.tar.gz</span><span class="text-muted-foreground text-xs">Up to 100 MiB. Ozal stores the files; it does not run your build.</span>{/if}
            </span>
            <input id="app-files" type="file" accept=".gz,.tgz" onchange={(e) => { const f = e.currentTarget.files?.[0] ?? null; files = f && f.size <= 100 * 1024 * 1024 ? f : null; if (f && !files) formError = "The file is larger than 100 MiB."; }} class="sr-only" />
          </label>
          <details class="text-muted-foreground text-xs">
            <summary class="cursor-pointer">Website name: <span class="text-foreground font-mono">{site || "taken from its address"}</span></summary>
            <Input id="app-site" bind:value={site} oninput={() => (siteEdited = true)} pattern="[a-z0-9]+(-[a-z0-9]+)*" class="mt-2 font-mono" aria-label="Website name" />
            <p class="mt-1">The name its versions are kept under. Use an existing name to show the same website at a second address.</p>
          </details>
        </div>
      {:else if kind === "github"}
        <div class="grid gap-2">
          <Label for="app-github-site">Website name</Label>
          <Input id="app-github-site" bind:value={site} placeholder="taken from its address" oninput={() => (siteEdited = true)} pattern="[a-z0-9]+(-[a-z0-9]+)*" class="font-mono" />
          <p class="text-muted-foreground text-xs">After saving you choose the repository in Sites and review the workflow it adds. The address shows nothing until the first publication.</p>
        </div>
      {:else if kind === "connector"}
        <div class="grid gap-2">
          <Label for="app-connector">Which machine?</Label>
          {#if connectors?.length}
            <select id="app-connector" required bind:value={connector} class="border-input bg-background h-9 rounded-md border px-3 text-sm">
              <option value="" disabled>Choose a connector</option>
              {#each connectors as name (name)}<option value={name}>{name}</option>{/each}
            </select>
          {:else}
            <Input id="app-connector" required bind:value={connector} class="font-mono" placeholder="connector name" />
          {/if}
          <p class="text-muted-foreground text-xs">{connectors && !connectors.length ? "No machine is connected yet: see Connectors to add one. " : ""}The connector on that machine must also be set to serve this address.</p>
        </div>
      {/if}
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex justify-end gap-2">
        <Button type="button" variant="ghost" onclick={() => (editing = false)}>Cancel</Button>
        <Button type="submit">Next</Button>
      </div>
    </form>
  {:else if status && step === 2}
    <form onsubmit={addressNext} class="grid gap-5">
      <div class="grid gap-2">
        <Label for="app-host">Address</Label>
        {#if zones.length}
          <div class="flex flex-wrap items-center gap-2">
            {#if zone}<Input id="app-host" bind:value={sub} placeholder="app" autocomplete="off" spellcheck="false" class="w-40 flex-1 font-mono" aria-describedby="app-host-help" /><span class="text-muted-foreground font-mono text-sm">.</span>{/if}
            <select bind:value={zone} aria-label="Domain" class="border-input bg-background h-9 rounded-md border px-3 font-mono text-sm">
              {#each zones as name (name)}<option value={name}>{name}</option>{/each}
              <option value="">another address…</option>
            </select>
            {#if !zone}<Input id="app-host" bind:value={host} placeholder="app.example.com" autocomplete="off" spellcheck="false" class="min-w-48 flex-1 font-mono" aria-describedby="app-host-help" />{/if}
          </div>
          <p id="app-host-help" class="text-muted-foreground text-xs">{zone ? (sub.trim() ? `${composed} · ` : `Leave empty to use ${zone} itself · `) : ""}{covered(address) ? `HTTPS is covered by the certificate for ${covered(address)}.` : address ? "This address is outside your domains: add its domain in Domains for HTTPS." : ""}</p>
        {:else}
          <Input id="app-host" required bind:value={host} placeholder="app.example.com" autocomplete="off" spellcheck="false" class="font-mono" aria-describedby="app-host-help" />
          <p id="app-host-help" class="text-muted-foreground text-xs">A new address, or one you are moving here from other hosting. To try first, use a spare one such as <span class="font-mono">preview.</span> on your domain.</p>
        {/if}
      </div>
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex flex-wrap justify-between gap-2">
        <Button type="button" variant="ghost" onclick={() => { step = 1; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
        <Button type="submit">Next</Button>
      </div>
    </form>
  {:else if status && step === 3}
    <form onsubmit={prepare} class="grid gap-5">
      <SignInFlow host={address} />
      <div class="grid gap-2">
        <p id="app-privacy-label" class="sr-only">Who can open it?</p>
        {@render choices("app-privacy", audiences, privacy, (v) => (privacy = v))}
      </div>
      {#if privacy === "groups"}
        <div class="grid gap-2">
          <Label for="app-groups">Groups <span class="text-muted-foreground font-normal">· a person must be in all of them</span></Label>
          <Input id="app-groups" required bind:value={groups} placeholder="team" autocomplete="off" />
          {#if suggestions.length}<div class="flex flex-wrap items-center gap-1.5"><span class="text-muted-foreground text-xs">Existing:</span>{#each suggestions as group (group)}<button type="button" class="chip chip-add" onclick={() => addGroup(group)}>+ {group}</button>{/each}</div>
          {:else}<p class="text-muted-foreground text-xs">A group is just a name. People get it when you invite them or in People.</p>{/if}
        </div>
      {/if}
      {#if canInvite}
        <div class="grid gap-2">
          <Label for="app-invite">Invite people <span class="text-muted-foreground font-normal">· optional</span></Label>
          <textarea id="app-invite" bind:value={inviteText} rows="2" placeholder="ana@example.com, luis@example.com" class="border-input bg-transparent placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 rounded-md border px-3 py-2 text-sm outline-none focus-visible:ring-3"></textarea>
          <p class="text-muted-foreground text-xs">Each person gets a one-time link to create their passkey{privacy === "groups" && chosen.length ? `, already in ${chosen.join(" and ")}` : ""}. You copy the links after saving.</p>
        </div>
      {/if}
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex flex-wrap justify-between gap-2">
        <Button type="button" variant="ghost" onclick={() => { step = base ? 1 : 2; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
        <Button type="submit" disabled={!!waiting}>{waiting || "Review"}</Button>
      </div>
    </form>
  {:else if step === 4 && plan}
    <dl class="divide-y rounded-lg border text-sm">
      {#each [
        ["Address", plan.route.host],
        ["Served by", draft ? servedBy(draft.route) + (kind === "static" && files ? ` · uploads ${files.name}` : kind === "github" ? " · repository chosen after saving" : "") : ""],
        ["Who can open it", draft?.access_mode === "public" ? "Everyone, without signing in" : privacy === "groups" && chosen.length ? `People in ${chosen.join(" and ")}, after signing in` : privacy === "signed-in" ? "Anyone with an account, after signing in" : whoMayEnter(base ?? {}).text],
        ...(canInvite && emails.length ? [["Invitations", emails.join(", ")]] : []),
        ["HTTPS", certificate(plan.tls)],
        ["DNS", "Shown after saving, with a live check"],
      ] as [term, value] (term)}
        <div class="grid gap-1 px-4 py-2.5 sm:grid-cols-[9rem_1fr]">
          <dt class="text-muted-foreground">{term}</dt>
          <dd class={["min-w-0 break-words", term === "Who can open it" && draft?.access_mode === "public" && "text-warning", term === "HTTPS" && plan.tls === "not_covered" && "text-destructive"]}>{value}</dd>
        </div>
      {/each}
    </dl>
    {#if plan.checks.length}
      <p class="mt-5 mb-2 text-sm font-medium">Before moving a live address</p>
      <ul class="text-muted-foreground space-y-1.5 text-sm">{#each plan.checks as item (item)}<li class="flex gap-2"><CheckIcon class="text-muted-foreground mt-0.5 size-3.5 shrink-0" />{item}</li>{/each}</ul>
    {/if}
    <label class="bg-muted/50 mt-5 flex items-start gap-3 rounded-lg p-3 text-sm"><input type="checkbox" bind:checked={checked} class="accent-primary mt-0.5" />I checked what serves it and who can open it. I will try it before moving a live address.</label>
    {#if formError}<p class="text-destructive mt-3 text-sm" role="alert">{formError}</p>{/if}
    {#if waiting}<p class="text-muted-foreground mt-3 text-sm" role="status">{waiting}</p>{/if}
    <div class="mt-5 flex flex-wrap justify-between gap-2">
      <Button variant="ghost" disabled={!!waiting} onclick={() => { step = 3; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
      <Button disabled={!checked || !!waiting || plan.tls === "not_covered"} onclick={() => (review = true)}>Save application</Button>
    </div>
  {:else if step === 5}
    {#if kind === "github"}
      <div class="grid gap-3 text-sm">
        <p>In <strong>Sites</strong>, choose the repository for <strong>{site}</strong> and review the workflow Ozal proposes as a pull request.</p>
        <p class="text-muted-foreground">Nothing has been built or published yet. Check the first publication before moving DNS.</p>
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

<Modal bind:open={connectOpen} wide title={`DNS & HTTPS · ${connectHost ?? ""}`} intro="The one DNS record this address needs, and whether visitors reach this server.">
  {#if connectHost}
    {#key connectHost}<DomainConnect host={connectHost} https={!!status?.tls_enabled} />{/key}
  {/if}
</Modal>

<ConfirmAction bind:open={review} title="Save this application?" target={draft?.route.host ?? ""} current={base ? "Existing application" : "Not configured"} requested={draft?.access_mode === "public" ? "Public application" : "Private application"} consequence={kind === "static" && files ? "The files are published, then the address starts serving them. DNS and any previous hosting are not changed." : kind === "github" ? "The address is saved now and shows content after the first publication. DNS is not changed." : "The address starts serving right away. DNS and any previous hosting are not changed."} detail="Try it signed in and signed out before moving a live address. To undo, remove it from Applications." confirmLabel="Confirm and save" onConfirm={() => void save()} />
