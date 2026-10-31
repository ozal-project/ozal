<script lang="ts">
  import { onDestroy, untrack, tick } from "svelte";
  import { Button } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { admin } from "$lib/adminToken.svelte";
  import { LatestRequest } from "$lib/latestRequest";
  import { proposeWorkflow, type Project, type WorkflowPreview, type WorkflowInput, type WorkflowResult } from "$lib/api";
  import ConfirmAction from "$lib/ConfirmAction.svelte";

  let { project, page }: { project: Project; page: number } = $props();
  const id = $props.id();
  const requests = new LatestRequest();
  let open = $state(false);
  let directory = $state(".");
  let output = $state("dist");
  let hostname = $state(untrack(() => project.hostnames[0] ?? ""));
  let preview = $state<WorkflowPreview | null>(null);
  let reviewedInput = $state<WorkflowInput | null>(null);
  let result = $state<WorkflowResult | null>(null);
  let busy = $state(false);
  let error = $state("");
  let confirm = $state(false);
  let dialog = $state<HTMLDialogElement | null>(null);
  let trigger = $state<HTMLButtonElement | null>(null);
  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  });

  // Repository metadata, generated content and PR links belong to this session.
  $effect(() => {
    void admin.token;
    requests.cancel(); preview = null; reviewedInput = null; result = null;
    busy = false; error = ""; confirm = false; open = false;
  });
  onDestroy(() => requests.cancel());

  function edit() { preview = null; reviewedInput = null; result = null; error = ""; }
  async function prepare(event: SubmitEvent) {
    event.preventDefault();
    const input = { directory, output, hostname, page };
    const run = requests.start(65_000);
    busy = true; error = ""; preview = null; result = null;
    try {
      const response = await proposeWorkflow(admin.token.trim(), project.name, input, run.signal);
      if (!run.isCurrent()) return;
      if (response.applied) throw new Error("Expected a workflow preview; inspect GitHub before continuing.");
      preview = response; reviewedInput = input;
    } catch (e) { if (run.isCurrent()) error = (e as Error).message; }
    finally { if (run.isCurrent()) busy = false; run.finish(); }
  }
  async function create() {
    if (!preview || !reviewedInput) return;
    const reviewed = preview;
    const input = { ...reviewedInput, expected: reviewed.expected, digest: reviewed.plan.digest, apply: true };
    const run = requests.start(65_000);
    busy = true; error = "";
    try {
      const response = await proposeWorkflow(admin.token.trim(), project.name, input, run.signal);
      if (!run.isCurrent()) return;
      if (!response.applied) throw new Error("GitHub did not confirm the pull request; inspect the proposal branch.");
      result = response; preview = null; reviewedInput = null;
    } catch (e) {
      if (!run.isCurrent()) return;
      const uncertain = run.signal.aborted || (e as Error).name === "TimeoutError";
      error = uncertain ? `The response was interrupted. Inspect ${reviewed.plan.repository} and branch ${reviewed.plan.branch} in GitHub before retrying; the pull request may already exist.` : (e as Error).message;
    } finally { if (run.isCurrent()) busy = false; run.finish(); }
  }
  function close() { requests.cancel(); busy = false; open = false; preview = null; reviewedInput = null; result = null; error = ""; void tick().then(() => trigger?.focus()); }
</script>

<div class="mt-4">
    <Button bind:ref={trigger} variant="outline" size="sm" class="h-auto max-w-full py-1.5 text-left break-all whitespace-normal" onclick={() => open = true}>Prepare publishing workflow for {project.name}</Button>
</div>
<dialog bind:this={dialog} aria-labelledby={`${id}-title`} class="bg-card text-card-foreground m-auto max-h-[85svh] w-[min(44rem,calc(100vw-2rem))] overflow-y-auto rounded-xl p-5 backdrop:bg-black/70 sm:p-6" oncancel={(event) => { if (busy) event.preventDefault(); else close(); }} onclose={() => open = false}>
      <h2 id={`${id}-title`} class="text-lg font-semibold">Publish {project.name} from GitHub</h2>
      <p class="text-muted-foreground mt-1 text-sm">Prepare an npm build and deployment workflow, then open a pull request for review. Your site stays as it is until the workflow runs after merging.</p>
      <form onsubmit={prepare} class="mt-4 grid gap-4 sm:grid-cols-2">
        <div><label for={`${id}-directory`} class="mb-2 block text-sm">Build directory</label><Input id={`${id}-directory`} required maxlength={200} bind:value={directory} oninput={edit} disabled={busy} aria-describedby={`${id}-build-help`} /></div>
        <div><label for={`${id}-output`} class="mb-2 block text-sm">Build output directory</label><Input id={`${id}-output`} required maxlength={200} bind:value={output} oninput={edit} disabled={busy} aria-describedby={`${id}-build-help`} /></div>
        <p id={`${id}-build-help`} class="text-muted-foreground text-sm sm:col-span-2">Build runs npm ci and npm run build. The output path is relative to the build directory. Review the pull request for other build tools.</p>
        <div class="sm:col-span-2"><label for={`${id}-hostname`} class="mb-2 block text-sm">Public hostname to verify</label><select id={`${id}-hostname`} required bind:value={hostname} onchange={edit} disabled={busy} class="bg-background h-9 w-full rounded-md border px-3 text-sm">{#each project.hostnames as host}<option value={host}>{host}</option>{/each}</select></div>
        <div class="flex flex-wrap gap-2 sm:col-span-2"><Button type="submit" variant="outline" disabled={busy}>{busy ? "Waiting for GitHub…" : "Preview workflow"}</Button><Button type="button" variant="ghost" onclick={close} disabled={busy}>Close</Button></div>
      </form>
      {#if error}<p class="text-destructive mt-4 break-words text-sm" role="alert">{error}</p>{/if}
      {#if preview}
        <div class="mt-5 space-y-3" aria-label="Workflow review">
          <p class="break-words text-sm">Add <code>{preview.plan.path}</code> to <strong>{preview.plan.repository}</strong> through a new pull request into <code>{preview.plan.base}</code>.</p>
          <details><summary class="cursor-pointer text-sm underline underline-offset-4">Read generated workflow</summary>
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard users need to scroll the generated file) -->
            <pre class="bg-background mt-3 max-h-96 overflow-auto rounded-md p-3 text-xs" tabindex="0" aria-label="Generated workflow"><code>{preview.plan.content}</code></pre>
          </details>
          <p class="text-muted-foreground text-sm">Builds run in GitHub Actions. The App needs Contents, Workflows and Pull requests write access for this selected repository. Ozal will not merge the pull request.</p>
          <Button variant="outline" size="sm" disabled={busy} onclick={() => confirm = true}>Review pull request</Button>
        </div>
      {/if}
      {#if result}
        <p class="mt-4 text-sm" role="status">Pull request created. <a class="underline underline-offset-4" href={result.pull_request.url} target="_blank" rel="noopener noreferrer">Review pull request #{result.pull_request.number} on GitHub</a>. Review the build and deployment settings before merging; publication has not been verified.</p>
      {/if}
</dialog>

<ConfirmAction bind:open={confirm} destructive={false} title="Open this pull request?" target={preview?.plan.repository ?? ""} current="Repository base branch stays unchanged" requested={preview ? `${preview.plan.path} on ${preview.plan.branch}` : ""} consequence="Creates a branch, adds one workflow and opens a pull request. Existing push workflows in the repository may run. Merging enables publication from the selected branch." detail={preview ? `Base: ${preview.plan.base}. Public acceptance: ${preview.plan.site_url}. No repository content is built on this edge.` : ""} confirmLabel="Open pull request" onConfirm={() => void create()} />
