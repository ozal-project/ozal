<script lang="ts">
  import { onMount } from "svelte";
  import PageHeader from "$lib/PageHeader.svelte";
  import Panel from "$lib/Panel.svelte";
  import Logo from "$lib/brand/Logo.svelte";
  import * as Select from "$lib/components/ui/select";
  import * as RadioGroup from "$lib/components/ui/radio-group";
  import * as Alert from "$lib/components/ui/alert";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Checkbox } from "$lib/components/ui/checkbox";
  import { Button } from "$lib/components/ui/button";
  import { Separator } from "$lib/components/ui/separator";
  import { explain, fetchOverview, type Report, type RouteOverview } from "$lib/api";
  import { freshness, inputKey, type Inputs } from "$lib/simulation";
  import KeyIcon from "@lucide/svelte/icons/key-round";
  import InfoIcon from "@lucide/svelte/icons/info";

  // The token lives only in this component's memory: never storage or cookies.
  let token = $state("");
  let routes = $state<RouteOverview[]>([]);
  let routesError = $state("");
  let host = $state("");
  let mode = $state<"user" | "anon">("user");
  let email = $state("");
  let verified = $state(true);
  let groups = $state("");
  let busy = $state(false);
  let error = $state("");
  // A result keeps the inputs it answered; it stops looking like the verdict
  // as soon as the form changes or a later run fails (BUG-010).
  let result = $state<{ report: Report; key: string } | null>(null);
  let failedSince = $state(false);
  let history = $state<Report[]>([]);
  const inputs = $derived<Inputs>({ host, mode, email, verified, groups });
  const fresh = $derived(freshness(result, inputs, failedSince));
  const report = $derived(result?.report ?? null);

  const selected = $derived(routes.find((r) => r.host === host));
  const stage = { authorization_only: "Authorize · policy check after login", gate_only: "Gate · no session, login required first", public_route: "None · public route" };
  const scope = { authorization_only: "Authorization only: OIDC, session and transport are not evaluated.", gate_only: "Gate only: no OIDC flow was started.", public_route: "Public route: no identity is evaluated." };

  onMount(async () => {
    try { routes = await fetchOverview(); host = routes.find((r) => r.protected)?.host ?? routes[0]?.host ?? ""; }
    catch (e) { routesError = (e as Error).message; }
  });

  async function run(e: SubmitEvent) {
    e.preventDefault();
    error = "";
    if (!host) return (error = "Choose a route.");
    if (!token.trim()) return (error = "Paste the admin token to ask the engine.");
    let principal: Report["principal"] | null = null;
    if (mode === "user") {
      if (!email.trim()) return (error = "Enter an email, or choose “No session”.");
      principal = { email: email.trim(), email_verified: verified, groups: groups.split(",").map((g) => g.trim()).filter(Boolean) };
    }
    const key = inputKey(inputs);
    busy = true;
    try {
      const answer = await explain(token.trim(), host, principal);
      result = { report: answer, key };
      failedSince = false;
      history = [answer, ...history].slice(0, 8);
    } catch (err) {
      error = (err as Error).message;
      failedSince = true;
    } finally {
      busy = false;
    }
  }

  const callerItems = (r: Report): [string, string, boolean?][] =>
    r.principal
      ? [["Email", r.principal.email], ["Verified", r.principal.email_verified ? "yes" : "no", !r.principal.email_verified], ...r.principal.groups.map((g): [string, string] => ["Group", g])]
      : [["Session", r.protected ? "none" : "not evaluated"]];

  const policyItems = (r: Report): [string, string, boolean?][] => {
    if (!r.protected) return [["Access", "Public"]];
    const p = r.policy ?? {};
    const items: [string, string][] = [
      ...(p.allowed_emails ?? []).map((v): [string, string] => ["Email", v]),
      ...(p.allowed_domains ?? []).map((v): [string, string] => ["Domain", v]),
      ...(p.required_groups ?? []).map((v): [string, string] => ["Requires group", v]),
    ];
    return items.length ? items : [["Access", "Any signed-in account"]];
  };
</script>

<PageHeader eyebrow="Access control" title="Policy simulator" intro="Plug a hypothetical caller into a line and ask the real engine whether it connects. Nothing reaches the route or the identity provider.">
  <span class="border-border bg-card flex items-center gap-2 rounded-full border px-3.5 py-1.5 font-mono text-xs"><span class="lamp lamp-on" aria-hidden="true"></span>same engine as the proxy</span>
</PageHeader>

<div class="grid gap-6 xl:grid-cols-[minmax(340px,420px)_1fr]">
  <Panel index="01" label="Caller" title="Request to simulate" sub="Describe a visitor for one line.">
    <form class="space-y-5" onsubmit={run} novalidate>
      <div class="space-y-2">
        <Label for="route" class="text-muted-foreground font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase">Line</Label>
        <Select.Root type="single" bind:value={host}>
          <Select.Trigger id="route" class="w-full font-mono text-[0.8rem]">{host || (routesError ? "Routes unavailable" : "Choose a route")}</Select.Trigger>
          <Select.Content>
            {#each routes as r (r.host)}
              <Select.Item value={r.host} label={r.host}><span class={["lamp", r.protected ? "lamp-wait" : "lamp-on"]}></span><span class="font-mono text-[0.8rem]">{r.host}</span></Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
        <p class="text-muted-foreground text-xs">{selected ? (selected.protected ? `Login required · ${selected.policy}` : "Public · no login required") : routesError ? `Could not read routes (${routesError}).` : "Choose a line to see its requirement."}</p>
      </div>

      <div class="space-y-2">
        <Label class="text-muted-foreground font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase">Visitor</Label>
        <RadioGroup.Root bind:value={mode} class="grid grid-cols-2 gap-2">
          {#each [{ v: "user", l: "Signed-in user" }, { v: "anon", l: "No session" }] as o (o.v)}
            <Label for={`mode-${o.v}`} class="border-border bg-background/40 has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-accent/40 flex cursor-pointer items-center gap-2 rounded-lg border p-3 text-sm font-normal">
              <RadioGroup.Item value={o.v} id={`mode-${o.v}`} />{o.l}
            </Label>
          {/each}
        </RadioGroup.Root>
      </div>

      {#if mode === "user"}
        <div class="space-y-4">
          <div class="space-y-2"><Label for="email" class="text-muted-foreground font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase">Email</Label><Input id="email" type="email" bind:value={email} placeholder="ana@example.com" autocomplete="off" spellcheck={false} class="font-mono text-[0.85rem]" /></div>
          <Label class="flex items-center gap-2 text-sm font-normal"><Checkbox bind:checked={verified} /> Provider verified this email</Label>
          <div class="space-y-2"><Label for="groups" class="text-muted-foreground font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase">Groups</Label><Input id="groups" bind:value={groups} placeholder="engineering, oncall" autocomplete="off" spellcheck={false} class="font-mono text-[0.85rem]" />
            <p class="text-muted-foreground text-xs">Comma-separated. Names are case-sensitive.</p></div>
        </div>
      {/if}

      <Separator />
      <div class="space-y-2">
        <Label for="token" class="text-muted-foreground font-mono text-[0.68rem] font-medium tracking-[0.12em] uppercase flex items-center gap-1.5"><KeyIcon class="size-3.5" /> Admin token</Label>
        <Input id="token" type="password" bind:value={token} placeholder="Bearer token from admin.token" autocomplete="off" spellcheck={false} class="font-mono" />
        <p class="text-muted-foreground text-xs">Kept in this tab’s memory only. Required because answers reveal who is allowed.</p>
      </div>
      {#if error}<Alert.Root variant="destructive"><Alert.Description>{error}</Alert.Description></Alert.Root>{/if}
      <Button type="submit" class="h-10 w-full font-mono tracking-wide uppercase" disabled={busy}>{busy ? "Asking the engine…" : "Patch it through"}</Button>
    </form>
  </Panel>

  <div class="min-w-0 space-y-6">
    <Panel index="02" label="Decision" title={report ? `${fresh === "previous" ? "Previous answer" : "Answer"} for ${report.route}` : "Waiting for a caller"} sub={report ? stage[report.scope] : "Run a simulation to see the verdict."}>
      <div aria-live="polite">
        {#if !report}
          <div class="border-border flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-16 text-center">
            <Logo size={48} class="opacity-80" />
            <p class="font-medium">No call on the line yet</p>
            <p class="text-muted-foreground max-w-sm text-sm">Pick a line, describe a caller and press <span class="text-foreground font-mono text-xs uppercase">Patch it through</span>.</p>
          </div>
        {:else}
          {@const ok = report.decision.allowed}
          {@const live = fresh === "current"}
          {#if !live}
            <Alert.Root class="mb-4"><Alert.Description>Not evaluated for the current inputs{failedSince ? ": the last run failed" : ""}. This is the previous answer{report.principal ? ` for ${report.principal.email}` : ""}; run the simulation again for a verdict.</Alert.Description></Alert.Root>
          {/if}
          <div class={["flex items-center gap-5 rounded-xl border p-6", !live ? "border-border bg-muted/20 opacity-70" : ok ? "border-success/40 bg-success/[0.06]" : "border-destructive/40 bg-destructive/[0.06]"]}>
            <span class={["lamp size-12", !live ? "" : ok ? "lamp-on" : "lamp-off"]} aria-hidden="true"></span>
            <div class="min-w-0">
              <p class="plate">{!live ? "Previous answer" : ok ? "Line connected" : "Line refused"}</p>
              <p class={["mt-1 text-3xl font-semibold tracking-tight", !live ? "text-muted-foreground" : ok ? "text-success" : "text-destructive"]}>{!live ? (ok ? "Was allowed" : "Was denied") : ok ? "Allowed" : "Denied"}</p>
              <code class="text-muted-foreground font-mono text-sm">{report.decision.code}</code>
            </div>
          </div>
          {#if report.decision.reason !== report.decision.code}<p class="mt-5 text-[1.05rem]">{report.decision.reason}</p>{/if}
          <dl class="mt-5 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[auto_1fr]">
            <dt class="plate pt-0.5">Line</dt><dd class="font-mono text-[0.85rem]">{report.route} <span class="text-muted-foreground font-sans">({report.protected ? "protected" : "public"})</span></dd>
            <dt class="plate pt-0.5">Decided by</dt><dd>{stage[report.scope]}</dd>
            <dt class="plate pt-0.5">Scope</dt><dd class="text-muted-foreground flex gap-1.5"><InfoIcon class="mt-0.5 size-3.5 shrink-0" />{scope[report.scope]}</dd>
          </dl>
          <div class="mt-6 grid gap-4 md:grid-cols-2">
            {#each [{ t: "Line policy", items: policyItems(report) }, { t: "Caller", items: callerItems(report) }] as col (col.t)}
              <div>
                <p class="plate mb-2">{col.t}</p>
                <ul class="border-border divide-border divide-y rounded-lg border">
                  {#each col.items as [k, v, warn] (k + v)}
                    <li class={["flex justify-between gap-3 px-3 py-2 text-sm", warn && "text-warning"]}><span class="text-muted-foreground">{k}</span><span class="font-mono text-[0.8rem] break-all">{v}</span></li>
                  {/each}
                </ul>
              </div>
            {/each}
          </div>
          <details class="mt-6 text-sm"><summary class="plate hover:text-foreground cursor-pointer">Raw response</summary>
            <pre class="bg-background/60 border-border mt-2 overflow-x-auto rounded-md border p-3 font-mono text-xs">{JSON.stringify(report, null, 2)}</pre></details>
        {/if}
      </div>
    </Panel>

    <div class="grid gap-6 md:grid-cols-2">
      <Panel index="03" label="Call log" sub="This session, newest first">
        {#if history.length === 0}<p class="text-muted-foreground text-sm">No calls yet.</p>
        {:else}
          <ul class="space-y-2.5">
            {#each history as h, i (i)}
              <li class="flex items-start gap-2.5 text-sm">
                <span class={["lamp mt-1.5", h.decision.allowed ? "lamp-on" : "lamp-off"]} aria-hidden="true"></span>
                <span class="min-w-0"><span class="block truncate font-mono text-[0.8rem]">{h.route}</span><span class="text-muted-foreground text-xs">{h.decision.code} · {h.principal ? h.principal.email : "no session"}</span></span>
              </li>
            {/each}
          </ul>
        {/if}
      </Panel>
      <div class="border-border rounded-xl border border-dashed p-5">
        <p class="plate mb-2">What this proves</p>
        <p class="mb-2 font-semibold">The decision, not the journey.</p>
        <div class="text-muted-foreground space-y-2 text-sm leading-relaxed">
          <p>It runs the same authorization function the proxy uses, with the line’s live policy.</p>
          <p>It does not test the OIDC login, sessions, cookies, TLS, rate limits or the upstream app.</p>
          <p>Same answer from a terminal: <code class="text-foreground font-mono text-xs">switchboard policy explain</code>.</p>
        </div>
      </div>
    </div>
  </div>
</div>
