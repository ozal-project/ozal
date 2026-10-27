<script lang="ts">
  import Summary from "$lib/Summary.svelte";
  import { onMount, type Component } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Modal from "$lib/Modal.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { admin, askForToken, can } from "$lib/adminToken.svelte";
  import { setupStatus, planApplication, saveApplication, fetchGroups, type SetupStatus, type ApplicationRoute, type ApplicationPlan, type ApplicationDraft } from "$lib/api";
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

  let status = $state<SetupStatus | null>(null);
  let known = $state<string[]>([]);
  let error = $state("");
  let notice = $state("");
  let loading = $state(false);
  let editing = $state(false);
  let base = $state<ApplicationRoute | null>(null);
  let host = $state("");
  let kind = $state("backend");
  let target = $state("");
  let site = $state("");
  let connector = $state("");
  let privacy = $state("groups");
  let groups = $state("");
  let plan = $state<ApplicationPlan | null>(null);
  let draft = $state<ApplicationDraft | null>(null);
  let formError = $state("");
  let review = $state(false);
  let waiting = $state(false);
  let checked = $state(false);
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
    base = route; host = route?.host ?? ""; target = route?.target ?? ""; site = route?.site ?? "";
    connector = route?.connector ?? ""; kind = route?.root || route?.upstreams?.length ? "keep" : route?.site ? "static" : route?.connector ? "connector" : "backend";
    privacy = route ? route.access || route.auth_required ? "keep" : "public" : "groups";
    groups = ""; editing = true; plan = null; draft = null; notice = ""; formError = "";
  }
  async function prepare(e: SubmitEvent) {
    e.preventDefault(); if (!status) return;
    waiting = true; formError = ""; plan = null; checked = false;
    const route: ApplicationRoute = kind === "keep" ? { ...base!, host: host.trim(), auth_required: false } : { ...base, host: host.trim(), target: undefined, upstreams: undefined, site: undefined, root: undefined, connector: undefined, auth_required: false };
    if (kind === "static") route.site = site.trim();
    else if (kind === "connector") { route.connector = connector.trim(); }
    else if (kind !== "keep") route.target = target.trim();
    if (privacy === "public") route.access = undefined;
    else if (privacy !== "keep") route.access = privacy === "groups" ? { required_groups: chosen } : {};
    else route.access = base?.access ?? {};
    if (privacy === "groups" && !chosen.length) { waiting = false; formError = "Name at least one required group, or choose any signed-in person explicitly."; return; }
    draft = { route, expected: status.config_sha256, access_mode: privacy === "public" ? "public" : "private" };
    try { plan = await planApplication(admin.token.trim(), draft); }
    catch (e) { formError = (e as Error).message; }
    finally { waiting = false; }
  }
  async function save() {
    if (!draft) return;
    waiting = true; formError = "";
    try { await saveApplication(admin.token.trim(), draft); notice = "Application saved. DNS and public delivery still need the checks from the review; existing hosting has not been changed."; editing = false; await load(); }
    catch (e) { formError = `${(e as Error).message} Refresh and reconcile before retrying.`; }
    finally { waiting = false; }
  }

  const chosen = $derived([...new Set(groups.split(",").map((g) => g.trim()).filter(Boolean))]);
  const suggestions = $derived(known.filter((g) => !chosen.includes(g)));
  const addGroup = (group: string) => { groups = [...chosen, group].join(", "); };

  type Choice = { value: string; label: string; hint: string; icon: Component; disabled?: boolean; tone?: "warn" };
  const kinds = $derived<Choice[]>([
    ...(base ? [{ value: "keep", label: "Keep current destination", hint: "Only change the hostname or who may enter.", icon: CheckIcon }] : []),
    { value: "backend", label: "Running application", hint: "A service reached by a private URL.", icon: ServerIcon },
    { value: "static", label: "Static build", hint: status?.pages_enabled ? "Files published to Switchboard Pages." : "Pages is not enabled on this edge.", icon: LayersIcon, disabled: !status?.pages_enabled },
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
  const tls = (value: string) => value === "not_covered" ? "Not covered — prepare a certificate before saving" : value === "external_or_local_tls_not_verified" ? "Local or terminated elsewhere; public TLS not checked" : "Hostname covered; served certificate not probed";
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

<PageHeader title="Applications" intro="Websites behind Switchboard and who may open each one." tabs={[...(can("applications") ? [{ path: "/applications" as const, label: "Manage" }] : []), { path: "/routes" as const, label: "Routing & health" }]}>
  <Button variant="outline" size="sm" aria-label="Refresh configuration" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
  <Button size="sm" onclick={() => edit(null)} disabled={!status || waiting}><PlusIcon class="size-3.5" /> Add existing website</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if notice}<p class="border-success/40 bg-success/[0.06] mb-4 rounded-md border p-3 text-sm" role="status">{notice}</p>{/if}
{#if !status}
  <Panel title="Start with this installation" label="Setup">
    <p class="text-muted-foreground mb-4 text-sm">Sign in as admin (top right), then read the configuration. No visitor account becomes an administrator.</p>
    <Button onclick={load} disabled={loading}>{loading ? "Reading…" : "Read installation"}</Button>
    <p class="text-muted-foreground mt-3 text-sm">For a fresh local installation, run <code>switchboard setup -dir ./my-edge</code> on its host. The generated guide covers identity, invitations, restart and recovery.</p>
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
      <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Bring an existing website: pick who may open it, test it on a trial hostname, then move your domain.</p>
      <Button class="mt-4" onclick={() => edit(null)}><PlusIcon class="size-4" /> Add existing website</Button>
    </div>
  {:else}
    <ul class="panel divide-y" aria-label="Applications">
      {#each status.routes as app (app.host)}
        {@const Icon = icon(app)}
        {@const needs = required(app)}
        <li class="grid gap-x-6 gap-y-3 px-4 py-3.5 sm:px-5 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_17rem] md:items-center">
          <div class="flex min-w-0 items-center gap-3">
            <span class="bg-muted text-muted-foreground grid size-9 shrink-0 place-items-center rounded-lg"><Icon class="size-4" /></span>
            <div class="min-w-0">
              <h2 class="truncate text-sm font-medium">{app.host}</h2>
              <p class="text-muted-foreground truncate font-mono text-xs">
                {#if app.connector}<button class="hover:text-foreground underline-offset-2 hover:underline" onclick={() => navigate("/connectors")}>{destination(app)}</button>{:else}{destination(app)}{/if}
              </p>
            </div>
          </div>
          <div class="flex min-w-0 flex-wrap items-center gap-1.5 pl-12 text-xs md:pl-0">
            {#if !isPrivate(app)}
              <span class="text-warning flex items-center gap-1.5"><span class="lamp lamp-wait" aria-hidden="true"></span>Public · no login</span>
            {:else if needs.length}
              <span class="text-muted-foreground flex items-center gap-1.5"><span class="lamp lamp-on" aria-hidden="true"></span>Members of</span>
              {#each needs as group (group)}<button class="chip" title="Open groups" onclick={() => navigate("/groups")}>{group}</button>{/each}
            {:else}
              <span class="text-muted-foreground flex items-center gap-1.5"><span class="lamp lamp-on" aria-hidden="true"></span>Any signed-in person</span>
            {/if}
          </div>
          <div class="flex flex-wrap items-center gap-0.5 pl-12 md:justify-end md:pl-0">
            <Button variant="outline" size="sm" aria-label={`Configure ${app.host}`} onclick={() => edit(app)}>Configure</Button>
            {#if app.site}<ArtifactUpload site={app.site} />{/if}
            <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={`Access log for ${app.host}`} title="Access log" onclick={() => navigate("/activity")}><ScrollTextIcon class="size-4" /></Button>
            {#if app.site}<Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={`Versions and rollback for ${app.site}`} title="Versions & rollback" onclick={() => navigate("/sites")}><VersionsIcon class="size-4" /></Button>{/if}
            <ApplicationRemove host={app.host} version={status.config_sha256} refreshed={load} />
          </div>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<Modal bind:open={editing} wide title={plan ? `Review ${plan.route.host}` : base ? `Configure ${base.host}` : "Add an existing website"} intro={plan ? "Nothing changes until you save." : "Test on a trial hostname first. DNS and the current hosting are not touched."}>
  {#if status && !plan}
    <form onsubmit={prepare} class="grid gap-5">
      <div class="grid gap-2">
        <Label for="app-host">Trial hostname</Label>
        <Input id="app-host" required bind:value={host} readonly={!!base} placeholder="preview.example.com" autocomplete="off" />
      </div>
      <div class="grid gap-2">
        <p id="app-kind-label" class="text-sm font-medium">What already exists?</p>
        {@render choices("app-kind", kinds, kind, (v) => (kind = v))}
      </div>
      {#if kind === "backend"}
        <div class="grid gap-2"><Label for="app-target">Private upstream URL</Label><Input id="app-target" required type="url" bind:value={target} placeholder="http://127.0.0.1:3000" class="font-mono" /></div>
      {:else if kind === "static"}
        <div class="grid gap-2"><Label for="app-site">Pages site name</Label><Input id="app-site" required bind:value={site} pattern="[a-z0-9]+(-[a-z0-9]+)*" placeholder="portfolio" class="font-mono" /></div>
      {:else if kind === "connector"}
        <div class="grid gap-2"><Label for="app-connector">Enrolled connector name</Label><Input id="app-connector" required bind:value={connector} class="font-mono" /><p class="text-muted-foreground text-xs">The connector must also serve this exact hostname. It does not install or start your app.</p></div>
      {/if}
      <div class="grid gap-2">
        <p id="app-privacy-label" class="text-sm font-medium">Who may enter?</p>
        {@render choices("app-privacy", audiences, privacy, (v) => (privacy = v))}
      </div>
      {#if privacy === "groups"}
        <div class="grid gap-2">
          <Label for="app-groups">Required groups <span class="text-muted-foreground font-normal">· all must match</span></Label>
          <Input id="app-groups" required bind:value={groups} placeholder="ops, staff" autocomplete="off" />
          {#if suggestions.length}<div class="flex flex-wrap items-center gap-1.5"><span class="text-muted-foreground text-xs">Existing:</span>{#each suggestions as group (group)}<button type="button" class="chip chip-add" onclick={() => addGroup(group)}>+ {group}</button>{/each}</div>{/if}
        </div>
      {/if}
      <p class="text-muted-foreground border-t pt-4 text-xs">An app with its own login keeps that login; Switchboard’s gate does not grant its internal permissions.</p>
      {#if formError}<p class="text-destructive text-sm" role="alert">{formError}</p>{/if}
      <div class="flex justify-end gap-2">
        <Button type="button" variant="ghost" disabled={waiting} onclick={() => (editing = false)}>Cancel</Button>
        <Button type="submit" disabled={waiting}>{waiting ? "Checking…" : "Review configuration"}</Button>
      </div>
    </form>
  {:else if plan}
    <dl class="divide-y rounded-lg border text-sm">
      {#each [
        ["Destination", draft ? destination(draft.route) : ""],
        ["Who may enter", draft?.access_mode === "public" ? "Everyone, without login" : chosen.length && privacy === "groups" ? `Members of ${chosen.join(" and ")}` : "Private, according to the reviewed policy"],
        ["TLS", tls(plan.tls)],
        ["DNS", "Manual, not checked"],
      ] as [term, value] (term)}
        <div class="grid gap-1 px-4 py-2.5 sm:grid-cols-[9rem_1fr]">
          <dt class="text-muted-foreground">{term}</dt>
          <dd class={["min-w-0 break-words", term === "Who may enter" && draft?.access_mode === "public" && "text-warning", term === "TLS" && plan.tls === "not_covered" && "text-destructive"]}>{value}</dd>
        </div>
      {/each}
    </dl>
    {#if plan.checks.length}
      <p class="mt-5 mb-2 text-sm font-medium">Before moving the live domain</p>
      <ul class="text-muted-foreground space-y-1.5 text-sm">{#each plan.checks as check (check)}<li class="flex gap-2"><CheckIcon class="text-muted-foreground mt-0.5 size-3.5 shrink-0" />{check}</li>{/each}</ul>
    {/if}
    <label class="bg-muted/50 mt-5 flex items-start gap-3 rounded-lg p-3 text-sm"><input type="checkbox" bind:checked={checked} class="accent-primary mt-0.5" />I reviewed the destination and who may enter. I will test it before changing the live domain.</label>
    {#if formError}<p class="text-destructive mt-3 text-sm" role="alert">{formError}</p>{/if}
    <div class="mt-5 flex flex-wrap justify-between gap-2">
      <Button variant="ghost" disabled={waiting} onclick={() => { plan = null; formError = ""; }}><ArrowLeftIcon class="size-4" /> Back</Button>
      <Button disabled={!checked || waiting || plan.tls === "not_covered"} onclick={() => (review = true)}>Save reviewed application</Button>
    </div>
  {/if}
</Modal>
<ConfirmAction bind:open={review} title="Save this application?" target={draft?.route.host ?? ""} current={base ? "Existing application" : "Not configured"} requested={draft?.access_mode === "public" ? "Public application" : "Private application"} consequence="The routing configuration will change immediately. DNS and the previous hosting are not changed." detail="Test the allowed and refused cases on the trial hostname. To undo a newly added route, remove it from Applications; project domains must retain at least one hostname." confirmLabel="Save application" onConfirm={() => void save()} />
