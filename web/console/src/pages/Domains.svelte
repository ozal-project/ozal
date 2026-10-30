<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Modal from "$lib/Modal.svelte";
  import DnsRecord from "$lib/DnsRecord.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { admin, can } from "$lib/adminToken.svelte";
  import { navigate } from "$lib/router.svelte";
  import { fetchZones, addZone, checkZone, renewZone, removeZone, setupStatus, type Zone, type Zones, type ZoneCheck, type ZoneState } from "$lib/api";
  import PlusIcon from "@lucide/svelte/icons/plus";
  import RefreshIcon from "@lucide/svelte/icons/refresh-cw";
  import ShieldIcon from "@lucide/svelte/icons/shield-check";

  // EDGE-008: one certificate per domain, like a DNS dashboard. A domain is
  // added once, its one challenge record is created once, and every address
  // directly under it gets HTTPS without further steps.
  let data = $state<Zones | null>(null);
  let tlsMode = $state<"off" | "automatic" | "dns01" | "">("");
  let error = $state("");
  let loading = $state(false);
  let notices = $state<Record<string, { text: string; ok: boolean }>>({});
  let checks = $state<Record<string, ZoneCheck>>({});
  let busy = $state<Record<string, string>>({});
  const editable = $derived(can("applications"));

  async function load() {
    loading = true;
    try {
      data = await fetchZones();
      error = "";
      if (!data.available && !tlsMode) tlsMode = (await setupStatus(admin.token.trim()).catch(() => null))?.tls_mode ?? "";
    } catch (e) { error = (e as Error).message; }
    finally { loading = false; }
  }
  // Issuance takes seconds per domain: follow it while one is under way.
  const moving = (s: ZoneState) => s === "pending" || s === "queued" || s === "issuing" || s === "renewing";
  onMount(() => {
    void load();
    const t = setInterval(() => { if (data?.zones.some((z) => moving(z.state))) void load(); }, 5_000);
    return () => clearInterval(t);
  });

  const states: Record<ZoneState, { label: string; lamp: string; detail: string }> = {
    pending: { label: "Waiting to issue", lamp: "lamp-wait", detail: "The certificate is requested as soon as the DNS record below is in place." },
    queued: { label: "In line", lamp: "lamp-wait", detail: "Another domain is being issued; this one is next." },
    issuing: { label: "Issuing certificate", lamp: "lamp-wait", detail: "Proving to the certificate authority that you control this domain." },
    active: { label: "Active", lamp: "lamp-on", detail: "" },
    renewing: { label: "Renewing", lamp: "lamp-on", detail: "The current certificate keeps working while the new one is issued." },
    renewal_failed: { label: "Renewal failing", lamp: "lamp-warn", detail: "Still secure for now, but the last renewal did not work." },
    expiring: { label: "Expires soon", lamp: "lamp-off", detail: "Renewal has been failing for weeks. Fix the DNS record before it expires." },
    failed: { label: "No certificate", lamp: "lamp-off", detail: "Addresses under this domain cannot use HTTPS yet." },
  };
  const date = (iso?: string) => iso ? new Date(iso).toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" }) : "";
  const daysLeft = (iso?: string) => iso ? Math.max(0, Math.round((Date.parse(iso) - Date.now()) / 86_400_000)) : 0;
  const note = (zone: string, text: string, ok: boolean) => { notices = { ...notices, [zone]: { text, ok } }; };

  async function check(zone: Zone) {
    busy = { ...busy, [zone.name]: "check" };
    try {
      const result = await checkZone(admin.token.trim(), zone.name);
      checks = { ...checks, [zone.name]: result };
      note(zone.name, result.challenge.state === "ok" ? "The DNS record is in place." : result.challenge.detail, result.challenge.state === "ok");
    } catch (e) { note(zone.name, (e as Error).message, false); }
    finally { busy = { ...busy, [zone.name]: "" }; }
  }
  async function renew(zone: Zone) {
    busy = { ...busy, [zone.name]: "renew" };
    try { await renewZone(admin.token.trim(), zone.name); note(zone.name, "Requested. The certificate is being issued now.", true); await load(); }
    catch (e) { note(zone.name, (e as Error).message, false); }
    finally { busy = { ...busy, [zone.name]: "" }; }
  }

  // Add a domain: name first, then the record to create, then the check.
  let adding = $state(false);
  let newName = $state("");
  let added = $state<string | null>(null);
  let addError = $state("");
  let addBusy = $state(false);
  const addedZone = $derived(added ? data?.zones.find((z) => z.name === added) ?? null : null);
  function openAdd() { newName = ""; added = null; addError = ""; adding = true; }
  async function submitAdd(e: SubmitEvent) {
    e.preventDefault();
    if (!data) return;
    addBusy = true; addError = "";
    try {
      const name = newName.trim().toLowerCase().replace(/\.$/, "").replace(/^https?:\/\//, "").replace(/\/.*$/, "");
      await addZone(admin.token.trim(), name, data.config_sha256);
      added = name;
      await load();
    } catch (e) { addError = (e as Error).message; }
    finally { addBusy = false; }
  }

  let removing = $state<Zone | null>(null);
  let removeOpen = $state(false);
  async function remove() {
    const zone = removing;
    if (!zone || !data) return;
    try { await removeZone(admin.token.trim(), zone.name, data.config_sha256); await load(); }
    catch (e) { note(zone.name, (e as Error).message, false); }
  }
</script>

{#snippet record(zone: Zone)}
  <DnsRecord type="CNAME" name={zone.challenge.name} value={zone.challenge.target} />
{/snippet}

<PageHeader title="Domains" intro="Each domain gets its own HTTPS certificate, renewed automatically. Add a domain once and every address directly under it can be used for an application.">
  <Button variant="outline" size="sm" aria-label="Refresh domains" onclick={load} disabled={loading}><RefreshIcon class={["size-3.5", loading && "animate-spin"]} /> Refresh</Button>
  {#if editable && data?.available}<Button size="sm" onclick={openAdd}><PlusIcon class="size-3.5" /> Add domain</Button>{/if}
</PageHeader>

{#if error}<p class="text-destructive mb-4 text-sm" role="alert">{error}</p>{/if}

{#if data && !data.available}
  <section class="panel p-6">
    <div class="flex items-start gap-4">
      <span class="bg-muted grid size-10 shrink-0 place-items-center rounded-lg"><ShieldIcon class="text-muted-foreground size-5" /></span>
      <div class="max-w-2xl">
        <h2 class="font-medium">{tlsMode === "automatic" ? "Certificates are issued per address" : "HTTPS is handled outside this edge"}</h2>
        {#if tlsMode === "automatic"}
          <p class="text-muted-foreground mt-1 text-sm">This edge gets a certificate for each address the first time someone visits it. There is nothing to set up here: add an application and point its DNS at this server.</p>
        {:else}
          <p class="text-muted-foreground mt-1 text-sm">This edge serves plain HTTP. HTTPS is either provided in front of it (for example by Cloudflare's proxy) or not enabled yet.</p>
        {/if}
        <p class="text-muted-foreground mt-3 text-sm">To manage domains here — one certificate per domain covering every address under it — the host needs <code class="font-mono text-xs">tls.dns01</code> in its configuration. The setup is described under “HTTPS and DNS” in the reference guide.</p>
      </div>
    </div>
  </section>
{:else if data}
  {#if data.zones.length === 0}
    <div class="panel px-5 py-12 text-center">
      <p class="font-medium">No domains yet</p>
      <p class="text-muted-foreground mx-auto mt-1 max-w-md text-sm">Add the domain you own, such as example.com. Ozal then gets a certificate for it and for every address one level below, like app.example.com.</p>
      {#if editable}<Button class="mt-4" onclick={openAdd}><PlusIcon class="size-4" /> Add domain</Button>{/if}
    </div>
  {:else}
    <ul class="grid gap-4" aria-label="Domains">
      {#each data.zones as zone (zone.name)}
        {@const state = states[zone.state]}
        {@const needsRecord = zone.challenge.delegated && (zone.state === "pending" || zone.state === "failed" || checks[zone.name]?.challenge.state === "bad")}
        <li class="panel">
          <div class="flex flex-wrap items-start justify-between gap-3 px-5 pt-5">
            <div class="min-w-0">
              <h2 class="truncate text-base font-medium">{zone.name}</h2>
              <p class="text-muted-foreground mt-0.5 text-xs">Covers {zone.name} and every address like <span class="font-mono">app.{zone.name}</span>{zone.source === "config" ? " · set in the server's configuration" : ""}</p>
            </div>
            <span class="flex items-center gap-2 text-sm"><span class={["lamp", state.lamp]} aria-hidden="true"></span>{state.label}</span>
          </div>

          <dl class="grid gap-x-8 gap-y-3 px-5 py-4 text-sm sm:grid-cols-3">
            <div>
              <dt class="text-muted-foreground text-xs">Certificate</dt>
              <dd class="mt-0.5">{#if zone.not_after}Valid until {date(zone.not_after)} <span class="text-muted-foreground">· {daysLeft(zone.not_after)} days</span>{:else}Not issued yet{/if}</dd>
            </div>
            <div>
              <dt class="text-muted-foreground text-xs">Next renewal</dt>
              <dd class="mt-0.5">{zone.renews_at ? date(zone.renews_at) : zone.next_attempt ? `Retry ${date(zone.next_attempt)}` : "After the first certificate"}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-muted-foreground text-xs">Applications</dt>
              <dd class="mt-0.5 truncate">
                {#if zone.hosts.length}<button type="button" class="hover:underline" onclick={() => navigate("/applications")}>{zone.hosts.length} · {zone.hosts.slice(0, 2).join(", ")}{zone.hosts.length > 2 ? "…" : ""}</button>
                {:else}<span class="text-muted-foreground">None yet</span>{/if}
              </dd>
            </div>
          </dl>

          {#if state.detail || zone.last_error}
            <div class="px-5 pb-1">
              {#if state.detail}<p class="text-muted-foreground text-sm">{state.detail}</p>{/if}
              {#if zone.last_error && zone.state !== "active"}<p class="text-muted-foreground mt-1 font-mono text-xs break-words">Last error: {zone.last_error}</p>{/if}
            </div>
          {/if}

          {#if zone.challenge.delegated}
            <details class="group px-5 pt-2 pb-1" open={needsRecord}>
              <summary class="text-muted-foreground hover:text-foreground cursor-pointer text-sm">{needsRecord ? "Create this DNS record at your DNS provider" : "DNS record for certificates"}</summary>
              <div class="mt-3 grid gap-2">
                <p class="text-muted-foreground text-xs">Created once. It lets Ozal prove it controls {zone.name} for every renewal, without access to your DNS account.</p>
                {@render record(zone)}
              </div>
            </details>
          {/if}

          {#if notices[zone.name]}<p class={["px-5 pt-2 text-sm", notices[zone.name].ok ? "text-success" : "text-destructive"]} role="status">{notices[zone.name].text}</p>{/if}

          {#if editable}
            <div class="mt-3 flex flex-wrap gap-2 border-t px-5 py-3">
              {#if zone.challenge.delegated}<Button variant="outline" size="sm" disabled={!!busy[zone.name]} onclick={() => check(zone)}>{busy[zone.name] === "check" ? "Checking…" : "Check DNS record"}</Button>{/if}
              <Button variant="outline" size="sm" disabled={!!busy[zone.name] || zone.state === "issuing" || zone.state === "queued"} onclick={() => renew(zone)}>{busy[zone.name] === "renew" ? "Requesting…" : zone.not_after ? "Renew now" : "Issue certificate now"}</Button>
              {#if zone.source === "runtime"}<Button variant="ghost" size="sm" class="text-muted-foreground ml-auto" onclick={() => { removing = zone; removeOpen = true; }}>Remove domain</Button>{/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{:else if !error}
  <p class="text-muted-foreground text-sm" role="status">Loading domains…</p>
{/if}

<Modal bind:open={adding} title={added ? `Connect ${added}` : "Add a domain"} intro={added ? "One DNS record, created once. After that, certificates are issued and renewed on their own." : "The domain you own. Ozal will hold one certificate for it and every address directly under it."}>
  {#if !added}
    <form class="grid gap-4" onsubmit={submitAdd}>
      <div class="grid gap-2">
        <Label for="zone-name">Domain</Label>
        <Input id="zone-name" required bind:value={newName} placeholder="example.com" autocomplete="off" spellcheck="false" class="font-mono" />
        <p class="text-muted-foreground text-xs">Just the domain, without <span class="font-mono">www</span> or <span class="font-mono">https://</span>. Subdomains such as <span class="font-mono">lab.example.com</span> also work.</p>
      </div>
      {#if addError}<p class="text-destructive text-sm" role="alert">{addError}</p>{/if}
      <div class="flex justify-end gap-2">
        <Button type="button" variant="ghost" onclick={() => (adding = false)}>Cancel</Button>
        <Button type="submit" disabled={addBusy || !newName.trim()}>{addBusy ? "Adding…" : "Add domain"}</Button>
      </div>
    </form>
  {:else if addedZone}
    <ol class="grid gap-5">
      <li class="grid gap-2">
        <p class="text-sm font-medium">1. Create this record at the company that runs your DNS</p>
        {#if addedZone.challenge.delegated}
          {@render record(addedZone)}
          <p class="text-muted-foreground text-xs">If your provider offers a proxy (Cloudflare's orange cloud), leave it off for this record.</p>
        {:else}
          <p class="text-muted-foreground text-sm">Nothing to create: this edge writes the record itself through your DNS provider's API.</p>
        {/if}
      </li>
      <li class="grid gap-2">
        <p class="text-sm font-medium">2. Check it</p>
        <div class="flex flex-wrap items-center gap-3">
          <Button variant="outline" size="sm" disabled={!!busy[addedZone.name]} onclick={() => check(addedZone)}>{busy[addedZone.name] === "check" ? "Checking…" : "Check DNS record"}</Button>
          {#if notices[addedZone.name]}<span class={["text-sm", notices[addedZone.name].ok ? "text-success" : "text-muted-foreground"]} role="status">{notices[addedZone.name].text}</span>{/if}
        </div>
        <p class="text-muted-foreground text-xs">New records can take a few minutes to appear.</p>
      </li>
      <li class="grid gap-2">
        <p class="text-sm font-medium">3. The certificate is issued automatically</p>
        <p class="text-muted-foreground flex items-center gap-2 text-sm"><span class={["lamp", states[addedZone.state].lamp]} aria-hidden="true"></span>{states[addedZone.state].label}</p>
      </li>
    </ol>
    <div class="mt-6 flex justify-end"><Button onclick={() => (adding = false)}>Done</Button></div>
  {/if}
</Modal>

<ConfirmAction bind:open={removeOpen} title="Remove this domain?" target={removing?.name ?? ""} current="Certificate managed by this edge" requested="No certificate for this domain" consequence="The edge stops serving and renewing this domain's certificate. Applications must be moved or removed first." detail="Delete the _acme-challenge record at your DNS provider too: while it exists, this edge can still prove control of the domain." confirmLabel="Remove domain" onConfirm={() => void remove()} />
