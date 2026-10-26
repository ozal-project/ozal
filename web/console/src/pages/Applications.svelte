<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Stat from "$lib/Stat.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { setupStatus, planApplication, saveApplication, type SetupStatus, type ApplicationRoute, type ApplicationPlan, type ApplicationDraft } from "$lib/api";
  import { navigate } from "$lib/router.svelte";
  import ApplicationRemove from "$lib/ApplicationRemove.svelte";
  import ArtifactUpload from "$lib/ArtifactUpload.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  let status = $state<SetupStatus | null>(null);
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
  let review = $state(false);
  let waiting = $state(false);
  let checked = $state(false);
  const load = async () => {
    if (!admin.token.trim()) { askForToken(); return; }
    loading = true; error = "";
    try { status = await setupStatus(admin.token.trim()); }
    catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  };
  onMount(() => { if (admin.token.trim()) void load(); });
  function edit(route: ApplicationRoute | null) {
    base = route; host = route?.host ?? ""; target = route?.target ?? ""; site = route?.site ?? "";
    connector = route?.connector ?? ""; kind = route?.root || route?.upstreams?.length ? "keep" : route?.site ? "static" : route?.connector ? "connector" : "backend";
    privacy = route ? route.access || route.auth_required ? "keep" : "public" : "groups";
    groups = ""; editing = true; plan = null; draft = null; notice = "";
  }
  async function prepare(e: SubmitEvent) {
    e.preventDefault(); if (!status) return;
    waiting = true; error = ""; plan = null; checked = false;
    const route: ApplicationRoute = kind === "keep" ? { ...base!, host: host.trim(), auth_required: false } : { ...base, host: host.trim(), target: undefined, upstreams: undefined, site: undefined, root: undefined, connector: undefined, auth_required: false };
    if (kind === "static") route.site = site.trim();
    else if (kind === "connector") { route.connector = connector.trim(); }
    else if (kind !== "keep") route.target = target.trim();
    if (privacy === "public") route.access = undefined;
    else if (privacy !== "keep") route.access = privacy === "groups" ? { required_groups: groups.split(",").map((v) => v.trim()).filter(Boolean) } : {};
    else route.access = base?.access ?? {};
    if (privacy === "groups" && !groups.trim()) { waiting = false; error = "Name at least one required group, or choose any signed-in person explicitly."; return; }
    draft = { route, expected: status.config_sha256, access_mode: privacy === "public" ? "public" : "private" };
    try { plan = await planApplication(admin.token.trim(), draft); }
    catch (e) { error = (e as Error).message; }
    finally { waiting = false; }
  }
  async function save() {
    if (!draft) return;
    waiting = true; error = "";
    try { await saveApplication(admin.token.trim(), draft); notice = "Application saved. DNS and public delivery still need the checks below; existing hosting has not been changed."; editing = false; await load(); }
    catch (e) { error = `${(e as Error).message} Refresh and reconcile before retrying.`; }
    finally { waiting = false; }
  }
</script>

<PageHeader eyebrow="Operate" title="Applications" intro="Bring an existing website, choose who may open it, then check it before moving your domain.">
  <Button variant="outline" onclick={load} disabled={loading}>Refresh configuration</Button>
  <Button onclick={() => edit(null)} disabled={!status || waiting}>Add existing website</Button>
</PageHeader>
{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}
{#if notice}<p class="mb-4 text-sm" role="status">{notice}</p>{/if}
{#if !status}
  <Panel title="Start with this installation" label="Setup">
    <p class="text-muted-foreground mb-4 text-sm">Sign in as admin (top right), then read the configuration. No visitor account becomes an administrator.</p>
    <Button onclick={load} disabled={loading}>{loading ? "Reading…" : "Read installation"}</Button>
    <p class="text-muted-foreground mt-3 text-sm">For a fresh local installation, run <code>switchboard setup -dir ./my-edge</code> on its host. The generated guide covers identity, invitations, restart and recovery.</p>
  </Panel>
{:else}
  <div class="mb-6 grid gap-4 sm:grid-cols-3">
    <Stat label="Applications" value={status.routes.length} note={`${status.routes.filter((r) => r.access || r.auth_required).length} private`} lamp={status.routes.length ? "on" : "idle"} />
    <Stat label="Identity" value={status.methods.length ? status.methods.length : "None"} note={status.methods.length ? status.methods.join(" / ") : "Configure passkeys or OIDC before going private"} lamp={status.methods.length ? "on" : "wait"} />
    <Stat label="Static publishing" value={status.pages_enabled ? "On" : "Off"} note={status.admin_socket_configured ? "Host socket configured" : "Host socket not configured"} lamp={status.pages_enabled ? "on" : "idle"} />
  </div>
  {#if editing}
    <Panel title={base ? `Edit ${base.host}` : "Add an existing website"} label="Prepare before cutover" class="mb-6">
      <form onsubmit={prepare} class="grid max-w-3xl gap-4 sm:grid-cols-2">
        <div><label for="app-host" class="mb-2 block text-sm">Trial hostname</label><Input id="app-host" required bind:value={host} readonly={!!base} placeholder="preview.example.com" /></div>
        <div><label for="app-kind" class="mb-2 block text-sm">What already exists?</label><select id="app-kind" bind:value={kind} class="bg-background h-9 w-full rounded-md border px-3 text-sm">{#if base}<option value="keep">Keep current destination</option>{/if}<option value="backend">Running application</option><option value="static" disabled={!status.pages_enabled}>Static build (Pages)</option><option value="connector">Application through an enrolled connector</option></select></div>
        {#if kind === "backend"}<div class="sm:col-span-2"><label for="app-target" class="mb-2 block text-sm">Private upstream URL</label><Input id="app-target" required type="url" bind:value={target} placeholder="http://127.0.0.1:3000" /></div>
        {:else if kind === "static"}<div><label for="app-site" class="mb-2 block text-sm">Pages site name</label><Input id="app-site" required bind:value={site} pattern="[a-z0-9]+(-[a-z0-9]+)*" placeholder="portfolio" /></div>
        {:else if kind === "connector"}<div><label for="app-connector" class="mb-2 block text-sm">Enrolled connector name</label><Input id="app-connector" required bind:value={connector} /><p class="text-muted-foreground mt-2 text-xs">The connector must also serve this exact hostname. It does not install or start your app.</p></div>{/if}
        <div><label for="app-privacy" class="mb-2 block text-sm">Who may enter?</label><select id="app-privacy" bind:value={privacy} class="bg-background h-9 w-full rounded-md border px-3 text-sm">
          {#if base && (base.access || base.auth_required)}<option value="keep">Keep the current access policy</option>{/if}
          <option value="groups" disabled={!status.methods.length}>Only members of these groups</option><option value="signed-in" disabled={!status.methods.length}>Any signed-in person</option><option value="public">Everyone, without login</option>
        </select></div>
        {#if privacy === "groups"}<div><label for="app-groups" class="mb-2 block text-sm">Required groups (all must match)</label><Input id="app-groups" required bind:value={groups} placeholder="ops" /></div>{/if}
        <p class="text-muted-foreground text-sm sm:col-span-2">Keep the current hosting while testing. An app with its own login keeps that login; Switchboard’s gate does not grant its internal permissions.</p>
        <div class="flex flex-wrap gap-3 sm:col-span-2"><Button type="submit" disabled={waiting}>{waiting ? "Checking…" : "Review configuration"}</Button><Button type="button" variant="ghost" disabled={waiting} onclick={() => { editing = false; plan = null; }}>Cancel</Button></div>
      </form>
      {#if plan}
        <div class="mt-6 border-t pt-5">
          <h2 class="text-base font-semibold">Review {plan.route.host}</h2>
          <p class="mt-2 text-sm">Access: {draft?.access_mode === "public" ? "Everyone, without login" : "Private, according to the reviewed policy"}. DNS: manual, not checked.</p>
          <p class="text-muted-foreground mt-2 text-sm">TLS: {plan.tls === "not_covered" ? "Not covered — prepare a certificate before saving" : plan.tls === "external_or_local_tls_not_verified" ? "Local or externally terminated; public TLS has not been checked" : "Hostname covered by configured names; served certificate has not been probed"}.</p>
          <ul class="my-4 list-disc space-y-2 pl-5 text-sm">{#each plan.checks as check}<li>{check}</li>{/each}</ul>
          <label class="mb-4 flex items-start gap-3 text-sm"><input type="checkbox" bind:checked={checked} class="mt-1" />I reviewed the destination and who may enter. I will test it before changing the live domain.</label>
          <Button disabled={!checked || waiting || plan.tls === "not_covered"} onclick={() => review = true}>Save reviewed application</Button>
        </div>
      {/if}
    </Panel>
  {/if}
  <div class="grid gap-4 xl:grid-cols-2">
    {#each status.routes as app (app.host)}
      {@const isPrivate = !!(app.access || app.auth_required)}
      <article class="panel flex flex-col p-5">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="text-base font-semibold break-all">{app.host}</h2>
            <p class="text-muted-foreground mt-1 text-sm break-all">{app.site ? `Static site · ${app.site}` : app.connector ? `Through connector · ${app.connector}` : app.target ?? app.root ?? "Several upstreams"}</p>
          </div>
          <span class={["inline-flex shrink-0 items-center gap-2 rounded-full border px-2.5 py-1 font-mono text-[0.68rem]", isPrivate ? "text-foreground" : "text-warning border-warning/40"]}><span class={["lamp", isPrivate ? "lamp-on" : "lamp-wait"]} aria-hidden="true"></span>{isPrivate ? "Private" : "Public"}</span>
        </div>
        <div class="mt-4 flex flex-wrap gap-2 border-t pt-4">
          <Button size="sm" variant="outline" onclick={() => edit(app)}>Configure</Button>
          <Button size="sm" variant="ghost" onclick={() => navigate("/activity")}>Access log</Button>
          {#if app.site}<Button size="sm" variant="ghost" onclick={() => navigate("/sites")}>Versions &amp; rollback</Button>{/if}
          <span class="ml-auto"><ApplicationRemove host={app.host} version={status.config_sha256} refreshed={load} /></span>
        </div>
        {#if app.site}<div class="mt-4"><ArtifactUpload site={app.site} /></div>{/if}
      </article>
    {:else}
      <div class="panel p-10 text-center xl:col-span-2">
        <p class="font-medium">No applications yet</p>
        <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Bring an existing website: pick who may open it, test it on a trial hostname, then move your domain.</p>
        <Button class="mt-4" onclick={() => edit(null)}>Add existing website</Button>
      </div>
    {/each}
  </div>
{/if}
<ConfirmAction bind:open={review} title="Save this application?" target={draft?.route.host ?? ""} current={base ? "Existing application" : "Not configured"} requested={draft?.access_mode === "public" ? "Public application" : "Private application"} consequence="The routing configuration will change immediately. DNS and the previous hosting are not changed." detail="Test the allowed and refused cases on the trial hostname. To undo a newly added route, remove it from Applications; project domains must retain at least one hostname." confirmLabel="Save application" onConfirm={() => void save()} />
