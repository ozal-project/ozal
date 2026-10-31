<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { admin, can } from "$lib/adminToken.svelte";
  import { explainPasskeyError, confirmWithPasskey } from "$lib/passkey";
  import {
    ApiError,
    fetchLatestUpdateJob,
    fetchUpdateJob,
    fetchUpdatePlan,
    startUpdateJob,
    type UpdateJob,
    type UpdatePlan,
  } from "$lib/api";

  // The reviewed, single-host update. The console never chooses a version,
  // URL, unit or path: it asks the host's executor for a plan, shows it, and
  // asks the same executor to apply that exact plan. Closing this dialog does
  // not cancel a running job.
  let { open = $bindable(false), onDone = () => {} }: { open?: boolean; onDone?: () => void } = $props();
  const dialogId = $props.id();
  let dialog = $state<HTMLDialogElement | null>(null);
  let returnFocus: HTMLElement | null = null;
  let plan = $state<UpdatePlan | null>(null);
  let job = $state<UpdateJob | null>(null);
  let error = $state("");
  let loading = $state(false);
  let busy = $state(false);
  let confirming = $state(false);
  let reconnecting = $state(false);
  let request: AbortController | undefined;
  let poll: ReturnType<typeof setTimeout> | undefined;

  const phaseText: Record<UpdateJob["phase"], string> = {
    reviewing: "Reviewing the signed release",
    staging: "Downloading the verified archive",
    verifying: "Checking the archive and the configuration",
    installing: "Installing and restarting Ozal",
    starting: "Waiting for Ozal to come back",
    complete: "Finished",
  };
  const mayInstall = $derived(can("updates.install"));
  const sizeText = (bytes?: number) => (bytes && bytes > 0 ? `${(bytes / (1024 * 1024)).toFixed(1)} MiB` : "");
  const shortID = (id: string) => id.slice(0, 8);

  function resultText(j: UpdateJob): string {
    switch (j.result) {
      case "updated":
        return `Ozal ${j.to ?? ""} is installed and answering.`.replace("  ", " ");
      case "rolled_back":
        return `The update failed and Ozal ${j.from} was restored.`;
      case "intervention_required":
        return "Ozal could not verify the running version. Use the host console to check the service before retrying.";
      case "failed":
        return "The update failed before it could be verified. Ozal should still be answering; the host log has the detail.";
      default:
        return phaseText[j.phase] ?? "Working";
    }
  }

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      dialog.showModal();
      void load();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  function closed() {
    open = false;
    request?.abort();
    if (poll) clearTimeout(poll);
    poll = undefined;
    requestAnimationFrame(() => { if (returnFocus?.isConnected) returnFocus.focus(); returnFocus = null; });
  }

  function message(e: unknown): string {
    return e instanceof Error ? e.message : "The local update executor did not answer.";
  }

  async function load() {
    request?.abort();
    const current = new AbortController();
    request = current;
    loading = true;
    error = "";
    reconnecting = false;
    plan = null;
    job = null;
    try {
      // A job may still be running from a previous tab. It survives closing
      // the dialog, so show it before offering a new review.
      try {
        const latest = await fetchLatestUpdateJob(current.signal);
        if (latest.result === "pending") {
          job = latest;
          pollJob();
          return;
        }
      } catch {
        // No job yet is the normal first-run case.
      }
      plan = await fetchUpdatePlan(current.signal);
    } catch (e) {
      if (!current.signal.aborted) error = message(e);
    } finally {
      if (!current.signal.aborted) loading = false;
    }
  }

  async function apply() {
    if (!plan || busy) return;
    busy = true;
    error = "";
    try {
      job = await startUpdateJob(plan.id);
      pollJob();
    } catch (e) {
      // Installing needs a passkey proof no older than five minutes. The
      // server answers passkey_required; confirm once and retry the reviewed
      // plan. Any other refusal (token, missing tool) is shown as it is.
      if (e instanceof ApiError && e.code === "passkey_required") {
        confirming = true;
        try {
          await confirmWithPasskey(admin.token.trim());
          job = await startUpdateJob(plan.id);
          pollJob();
        } catch (step) {
          error = step instanceof Error && step.name === "NotAllowedError"
            ? explainPasskeyError(step, "sign-in")
            : message(step);
        } finally {
          confirming = false;
        }
      } else {
        error = message(e);
      }
    } finally {
      busy = false;
    }
  }

  function pollJob() {
    if (!job || job.result !== "pending") return;
    if (poll) clearTimeout(poll);
    poll = setTimeout(async () => {
      if (!job || job.result !== "pending") return;
      try {
        const next = await fetchUpdateJob(job.id);
        job = next;
        reconnecting = false;
        error = "";
        if (next.result === "pending") pollJob();
        else onDone();
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) {
          error = "This update job is no longer available. Reload the console to review a new plan.";
          return;
        }
        const text = message(e);
        if (text.includes("401") || text.toLowerCase().includes("sign in")) {
          error = "Sign in again to see the update result.";
          return;
        }
        // The edge restarts during the install; keep asking until it answers.
        reconnecting = true;
        pollJob();
      }
    }, reconnecting ? 3000 : 1200);
  }

  async function reload() {
    window.location.reload();
  }
</script>

<dialog bind:this={dialog} aria-labelledby={`${dialogId}-title`} onclose={closed}
  class="bg-card text-card-foreground border-border m-auto w-[min(38rem,calc(100vw-2rem))] rounded-xl border p-0 backdrop:bg-black/60">
  <div class="space-y-4 p-5 sm:p-6">
    <div>
      <h2 id={`${dialogId}-title`} class="text-lg font-semibold">Update Ozal</h2>
      <p class="text-muted-foreground mt-1 text-sm">Ozal reviews a signed release, then installs it in place. Traffic is interrupted while the service restarts.</p>
    </div>

    {#if error}<p class="text-destructive text-sm" role="alert">{error}</p>{/if}
    {#if loading && !plan && !job}
      <p class="text-muted-foreground text-sm" role="status">Checking the signed catalog…</p>
    {/if}

    {#if job}
      <div class="border-border space-y-2 rounded-lg border p-4" role="status" aria-live="polite">
        <p class="text-sm font-medium">{resultText(job)}</p>
        {#if job.result === "pending"}
          <p class="text-muted-foreground text-xs">Phase: {phaseText[job.phase] ?? job.phase} · job {shortID(job.id)}</p>
          {#if reconnecting}<p class="text-muted-foreground text-xs">Ozal is restarting. Waiting for it to answer…</p>{/if}
          <p class="text-muted-foreground text-xs">You can close this dialog. The update continues on the host.</p>
        {:else if job.result === "updated"}
          <p class="text-muted-foreground text-xs">Reload the console to use the new version. You may be asked to sign in again.</p>
        {/if}
      </div>
      {#if job.result === "updated"}
        <div class="flex justify-end gap-2"><Button onclick={reload}>Reload console</Button></div>
      {:else if job.result !== "pending"}
        <div class="flex justify-end"><Button variant="outline" onclick={() => (open = false)}>Close</Button></div>
      {/if}
    {:else if plan}
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
        <dt class="text-muted-foreground">Installed</dt><dd class="font-medium">{plan.installed}</dd>
        {#if plan.latest}
          <dt class="text-muted-foreground">Available</dt><dd class="font-medium">{plan.latest}</dd>
        {/if}
        <dt class="text-muted-foreground">Channel</dt><dd>{plan.channel} · {plan.platform}</dd>
        {#if plan.size}<dt class="text-muted-foreground">Download</dt><dd>{sizeText(plan.size)}</dd>{/if}
      </dl>
      {#if plan.result === "available"}
        {#if plan.migrations}
          <p class="text-sm" role="note">This release may update the state database. If it cannot start, Ozal restores the previous binary and database.</p>
        {/if}
        {#if plan.reason}<p class="text-muted-foreground text-xs">{plan.reason}</p>{/if}
        {#if plan.notes_url}
          <p class="text-xs"><a class="underline" href={plan.notes_url} target="_blank" rel="noreferrer">Release notes</a></p>
        {/if}
        {#if mayInstall}
          <div class="flex flex-wrap justify-end gap-2">
            <Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
            <Button disabled={busy} onclick={apply}>{confirming ? "Confirm with your passkey…" : busy ? "Starting…" : `Update to ${plan.latest}`}</Button>
          </div>
        {:else}
          <p class="text-muted-foreground text-sm" role="note">Your session can review this release but cannot install it. Ask for the “Install updates” tool and sign in with your passkey, or run the update on the host.</p>
          <div class="flex justify-end"><Button variant="outline" onclick={() => (open = false)}>Close</Button></div>
        {/if}
      {:else}
        <p class="text-muted-foreground text-sm" role="status">
          {plan.result === "current" ? "Ozal is up to date." : plan.reason || "This build cannot be updated from the console."}
        </p>
        <div class="flex justify-end"><Button variant="outline" onclick={() => (open = false)}>Close</Button></div>
      {/if}
    {:else if !loading}
      <div class="flex justify-end"><Button variant="outline" onclick={() => (open = false)}>Close</Button></div>
    {/if}
  </div>
</dialog>
