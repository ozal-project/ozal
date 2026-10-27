<script lang="ts">
  import Summary from "$lib/Summary.svelte";
  import { onMount, onDestroy } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import CopyCommand from "$lib/CopyCommand.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { fetchConnectors, type Connector } from "$lib/api";
  import CheckIcon from "@lucide/svelte/icons/circle-check";
  import CircleIcon from "@lucide/svelte/icons/circle";

  let records = $state<Connector[] | null>(null);
  let error = $state("");
  let loading = $state(false);
  let readAt = $state("");
  let adding = $state(false);
  let name = $state("");
  let poll: ReturnType<typeof setInterval> | undefined;

  const DAY = 86_400_000;
  const nameOk = $derived(/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name));
  const daysLeft = (c: Connector) => (c.not_after ? Math.floor((new Date(c.not_after).getTime() - Date.now()) / DAY) : null);
  const stateOf = (c: Connector) => (c.revoked ? "revoked" : c.connected ? "connected" : c.hosts.length ? "waiting" : "idle");
  const label = { revoked: "Revoked", connected: "Connected", waiting: "Not connected", idle: "No app assigned" } as const;
  const lamp = { revoked: "lamp-off", connected: "lamp-on", waiting: "lamp-wait", idle: "lamp-idle" } as const;
  const active = $derived((records ?? []).filter((c) => !c.revoked));
  const connected = $derived(active.filter((c) => c.connected).length);
  const expiring = $derived(active.filter((c) => { const d = daysLeft(c); return d !== null && d < 10; }).length);
  const mine = $derived(nameOk ? (records ?? []).find((c) => c.name === name && !c.revoked) : undefined);

  async function load(quiet = false) {
    if (!admin.token.trim()) { askForToken(); return; }
    if (!quiet) loading = true;
    error = "";
    try { records = await fetchConnectors(admin.token.trim()); readAt = new Date().toLocaleTimeString(); }
    catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  }
  // While the guide is open, look for the new connector every few seconds.
  $effect(() => {
    clearInterval(poll);
    if (adding && nameOk && admin.token.trim()) poll = setInterval(() => void load(true), 5000);
  });
  onMount(() => { if (admin.token.trim()) void load(); });
  onDestroy(() => clearInterval(poll));

  const steps = $derived([
    { t: "Create the identity on the private host", d: "The private key is generated there and never leaves it.", cmd: `switchboard connector prepare -dir ./connector-identity -name ${name || "NAME"}`, where: "on the private host" },
    { t: "Sign the request with your CA", d: "Move only connector.csr to the machine that holds your CA, then bring the certificate back.", cmd: `switchboard connector sign -csr connector.csr -ca-dir ./ca -out connector.crt`, where: "on the CA machine" },
    { t: "Check the certificate on the private host", d: "Confirms that the certificate matches the key before anything is enrolled.", cmd: `switchboard connector check -dir ./connector-identity -ca ./ca.crt`, where: "on the private host" },
    { t: "Enrol it on the edge", d: "Review the fingerprint first. Enrolment stays with whoever controls the edge host.", cmd: `sudo -u switchboard switchboard connector enroll -config /etc/switchboard/config.json -name ${name || "NAME"} -cert connector.crt`, where: "on the edge host" },
    { t: "Connect and add the application", d: "Replace the capitalised values with your app host and the edge address, then start the tunnel from the private host. Add the app in Applications and test an allowed and a refused visitor.", cmd: `switchboard tunnel connect -cert ./connector-identity/connector.crt -key ./connector-identity/connector.key -ca ./ca.crt -host APP.example.com -edge EDGE_PRIVATE_IP:9443 -server-name EDGE_CERT_NAME -upstream http://127.0.0.1:3000`, where: "on the private host" },
  ]);
  const progress = $derived(mine ? (mine.connected ? 5 : 4) : 0);
</script>

<PageHeader title="Connectors" intro="Private hosts that publish applications without opening a port.">
  <Button variant="outline" disabled={loading} onclick={() => load()}>{loading ? "Reading…" : records ? "Refresh" : "Read connector registry"}</Button>
  <Button onclick={() => { adding = !adding; if (adding && !records) void load(); }}>{adding ? "Close guide" : "Connect a private host"}</Button>
</PageHeader>

{#if error && records}<p role="status" class="border-warning/40 bg-warning/[0.06] mb-4 rounded-md border p-3 text-sm">Showing the last successful registry read. Refresh failed: {error}</p>
{:else if error}<p role="alert" class="text-destructive mb-4 text-sm">{error}</p>{/if}

<Summary label="Connectors summary" items={[{ value: records ? active.length : "—", label: "enrolled" }, { value: records ? connected : "—", label: "connected" }, { value: records ? expiring : "—", label: "certificates expiring soon", tone: expiring ? "warn" : undefined }]} />

{#if adding}
  <Panel label="New private host" title="Connect it in five steps" sub="Each step shows the exact command. This page watches the registry and ticks a step when it sees the result." class="mb-6">
    <div class="mb-6 max-w-sm">
      <label for="connector-name" class="mb-2 block text-sm">Name for this host</label>
      <Input id="connector-name" bind:value={name} placeholder="homelab" autocomplete="off" spellcheck="false" aria-invalid={name !== "" && !nameOk} />
      {#if name && !nameOk}<p class="text-destructive mt-2 text-xs">Use lowercase letters, digits and single dashes.</p>{/if}
    </div>
    <ol class="space-y-5">
      {#each steps as step, i (step.t)}
        {@const n = i + 1}
        {@const done = n <= progress && n >= 4 ? true : false}
        <li class="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-3">
          <span class="mt-0.5" aria-hidden="true">{#if done}<CheckIcon class="text-success size-5" />{:else}<CircleIcon class="text-muted-foreground size-5" />{/if}</span>
          <div class="min-w-0">
            <h3 class="text-sm font-medium">{n}. {step.t} <span class="text-muted-foreground font-normal">· {step.where}</span>{#if done}<span class="sr-only"> (done)</span>{/if}</h3>
            <p class="text-muted-foreground mt-1 mb-2 text-sm">{step.d}</p>
            <CopyCommand command={step.cmd} label={`Copy step ${n} command`} />
            {#if n === 4 && nameOk}<p class="mt-2 text-xs" role="status">{mine ? `Enrolled: certificate ${mine.sha256.slice(0, 12)}…${mine.not_after ? `, valid until ${new Date(mine.not_after).toLocaleDateString()}` : ""}.` : "Waiting for the edge to list this name…"}</p>{/if}
            {#if n === 5 && nameOk}<p class="mt-2 text-xs" role="status">{mine ? (mine.connected ? "Connected. The tunnel is up." : "Enrolled but not connected yet.") : "Not enrolled yet."}</p>{/if}
          </div>
        </li>
      {/each}
    </ol>
    <p class="text-muted-foreground mt-6 border-t pt-4 text-xs">The identity is valid for 30 days and renews with the existing renewal agent, which is not set up from this screen. A connector installs no runtime or database.</p>
  </Panel>
{/if}

{#if readAt}<p class="text-muted-foreground mb-3 text-xs">Registry read at {readAt}.</p>{/if}
{#if records === null && loading}
  <div class="border-y py-5 text-sm text-muted-foreground" aria-busy="true">Reading connector registry…</div>
{:else if records === null && error}
  <Button size="sm" variant="outline" onclick={() => load()}>Retry registry read</Button>
{:else if records}
  <div class="grid gap-4 lg:grid-cols-2">
    {#each records as connector (`${connector.name}-${connector.sha256}`)}
      {@const state = stateOf(connector)}
      {@const days = daysLeft(connector)}
      <article class="panel p-5">
        <div class="flex items-start justify-between gap-3">
          <h2 class="flex items-center gap-2 text-base font-semibold"><span class={["lamp", lamp[state]]} aria-hidden="true"></span>{connector.name}</h2>
          <span class="text-muted-foreground font-mono text-xs">{label[state]}</span>
        </div>
        <dl class="mt-4 grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 text-sm">
          <dt class="text-muted-foreground">Serves</dt><dd class="break-all">{connector.hosts.length ? connector.hosts.join(", ") : "No application yet"}</dd>
          <dt class="text-muted-foreground">Certificate</dt>
          <dd>{#if days === null}Expiry unknown{:else if days < 0}<span class="text-destructive">Expired</span>{:else}<span class={days < 10 ? "text-warning" : ""}>{days} days left</span>{/if}<span class="text-muted-foreground block font-mono text-xs break-all">{connector.sha256.slice(0, 24)}…</span></dd>
        </dl>
        {#if !connector.revoked}
          <details class="mt-4 text-sm"><summary class="text-muted-foreground hover:text-foreground cursor-pointer">Revoke this connector</summary>
            <p class="mt-3">Stops access to the hosts above within about 5 seconds. Application data is not deleted. Run it on the edge host:</p>
            <div class="mt-2"><CopyCommand command={`sudo -u switchboard switchboard connector revoke -config /etc/switchboard/config.json -name ${connector.name}`} label={`Copy revoke command for ${connector.name}`} /></div>
            <p class="text-muted-foreground mt-2 text-xs">Then stop the connector service on its own host. Keep the shared CA; revoke only this identity.</p>
          </details>
        {/if}
      </article>
    {:else}
      <div class="panel p-8 text-center lg:col-span-2">
        <p class="font-medium">No private hosts connected</p>
        <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Direct upstreams and static sites do not need a connector. Connect a host when an app lives behind a home or office network.</p>
        <Button class="mt-4" onclick={() => (adding = true)}>Connect a private host</Button>
      </div>
    {/each}
  </div>
{/if}
