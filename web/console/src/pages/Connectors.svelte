<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import { Button } from "$lib/components/ui/button";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import { fetchConnectors, type Connector } from "$lib/api";
  let records = $state<Connector[] | null>(null);
  let error = $state("");
  let loading = $state(false);
  let readAt = $state("");
  async function load() {
    if (!admin.token.trim()) { askForToken(); return; }
    loading = true; error = "";
    try { records = await fetchConnectors(admin.token.trim()); readAt = new Date().toLocaleTimeString(); }
    catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  }
  onMount(() => { if (admin.token.trim()) void load(); });
</script>
<PageHeader eyebrow="Operate" title="Connectors" intro="Which private hosts can publish your applications? Enrolment and revocation stay with the host operator.">
  <Button variant="outline" disabled={loading} onclick={load}>{loading ? "Reading…" : "Read connector registry"}</Button>
</PageHeader>
{#if error}<p role="alert" class="text-destructive mb-4 text-sm">{error}</p>{/if}
{#if readAt}<p class="text-muted-foreground mb-4 text-xs">Snapshot read at {readAt}. Refresh to check connection or certificate changes.</p>{/if}
<Panel label="Add a private host" title="Prepare, verify, then enrol" class="mb-6">
  <ol class="list-decimal space-y-3 pl-5 text-sm">
    <li>On the destination host, run <code>switchboard connector prepare -dir ./connector-identity -name homelab</code>. Its private key stays there.</li>
    <li>Transfer only <code>connector.csr</code> to the authorized CA host. Sign with <code>connector sign -csr PUBLIC_CSR -ca-dir LOCAL_CA_DIRECTORY -out connector.crt</code>.</li>
    <li>Return the public certificate and CA certificate. Run <code>connector check -dir ./connector-identity -ca ./ca.crt</code> on the destination.</li>
    <li>On the edge host, review the public certificate fingerprint, then run <code>connector enroll -config /etc/switchboard/config.json -name homelab -cert connector.crt</code>.</li>
    <li>Configure the explicit host-to-destination mapping and start <code>tunnel connect</code> on that host. Add the app in Applications, then test login, denial and reconnection.</li>
  </ol>
  <p class="text-muted-foreground mt-4 text-sm">This uses the existing private CA/pilot transport. A connector does not install a runtime or database. Schedule renewal with the existing renewal runbook before using its 30-day certificate; arbitrary-host automatic renewal is not configured by this screen.</p>
</Panel>
{#if records}
  <div class="divide-y border-y">{#each records as connector (`${connector.name}-${connector.sha256}`)}
    <article class="py-5">
      <h2 class="text-base font-semibold">{connector.name} · {connector.revoked ? "Revoked" : connector.connected ? "Connected" : connector.hosts.length ? "Not connected" : "No app assigned"}</h2>
      <p class="text-muted-foreground mt-2 break-all text-sm">Certificate SHA-256: {connector.sha256}</p>
      <p class="mt-2 text-sm">Expires: {connector.not_after ? new Date(connector.not_after).toLocaleString() : "Unknown — enrolment did not record it"}</p>
      <p class="mt-2 text-sm">Affected hosts: {connector.hosts.length ? connector.hosts.join(", ") : "None configured"}</p>
      {#if !connector.revoked}<details class="mt-3 text-sm"><summary class="cursor-pointer">Revoke this connector from its edge host</summary><p class="mt-3">This stops access to the affected hosts when the edge next revalidates the connection (normally within 5 seconds). It does not delete application data.</p><p class="mt-2 break-all"><code>switchboard connector revoke -config /etc/switchboard/config.json -name {connector.name}</code></p><p class="text-muted-foreground mt-2">Recheck this snapshot afterwards and stop the connector service on its own host. Keep the shared CA; revoke only the identity you intend to remove.</p></details>{/if}
    </article>
  {:else}<p class="py-5 text-sm">No connectors enrolled. Direct/private network apps and static sites do not need one.</p>{/each}</div>
{/if}
