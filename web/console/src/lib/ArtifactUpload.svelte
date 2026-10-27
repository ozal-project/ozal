<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  import Modal from "$lib/Modal.svelte";
  import UploadIcon from "@lucide/svelte/icons/upload";
  import FileArchiveIcon from "@lucide/svelte/icons/file-archive";
  let { site }: { site: string } = $props();
  let dialogOpen = $state(false);
  let file = $state<File | null>(null);
  let selected = $state<File | null>(null);
  let open = $state(false);
  let waiting = $state(false);
  let message = $state("");
  let failure = $state(false);
  const size = (bytes: number) => (bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`);
  function start() { file = selected = null; message = ""; failure = false; dialogOpen = true; }
  async function upload() {
    if (!selected) return;
    waiting = true; message = ""; failure = false;
    try {
      const res = await fetch(`/_/api/sites/${encodeURIComponent(site)}/deployments`, { method: "POST", headers: { Authorization: `Bearer ${admin.token.trim()}`, "Content-Type": "application/gzip" }, body: selected, signal: AbortSignal.timeout(600_000) });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error ?? `Publish failed (HTTP ${res.status}).`);
      message = `Publication ${body.id} is live. Check its content through the public hostname; Sites keeps the retained versions for rollback.`;
    } catch (e) { failure = true; message = `${(e as Error).message} If the response was interrupted, read Sites before retrying; the upload may have completed.`; }
    finally { waiting = false; }
  }
  function review() {
    if (!admin.token.trim()) { askForToken(); return; }
    if (!file || file.size > 100 * 1024 * 1024) { failure = true; message = "Choose a tar.gz artifact no larger than 100 MiB."; return; }
    selected = file; open = true;
  }
</script>
<Button variant="ghost" size="sm" aria-label={`Publish an artifact to ${site}`} onclick={start}><UploadIcon class="size-3.5" /> Publish</Button>
<Modal bind:open={dialogOpen} title="Publish a build" intro={`Make a new version of ${site} live.`}>
  <p class="text-muted-foreground mb-4 text-sm">Build locally or in CI and pack the contents of <code class="font-mono">dist</code> as tar.gz. Switchboard validates and activates the artifact; it does not run build commands.</p>
  <label class="border-input hover:border-ring has-[:focus-visible]:ring-ring/50 flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-5 transition-colors has-[:focus-visible]:ring-3">
    <span class="bg-muted grid size-10 shrink-0 place-items-center rounded-lg"><FileArchiveIcon class="text-muted-foreground size-5" /></span>
    <span class="min-w-0 text-sm">
      {#if file}<span class="block truncate font-medium">{file.name}</span><span class="text-muted-foreground text-xs">{size(file.size)} · choose another</span>
      {:else}<span class="block font-medium">Choose a tar.gz artifact</span><span class="text-muted-foreground text-xs">Up to 100 MiB</span>{/if}
    </span>
    <input type="file" accept=".gz,.tgz" disabled={waiting} onchange={(e) => { file = e.currentTarget.files?.[0] ?? null; message = ""; failure = false; }} class="sr-only" />
  </label>
  {#if message}<p class={["mt-4 text-sm", failure && "text-destructive"]} role="status">{message}</p>{/if}
  <div class="mt-5 flex justify-end gap-2">
    <Button variant="ghost" onclick={() => (dialogOpen = false)}>{message && !failure ? "Done" : "Cancel"}</Button>
    <Button disabled={!file || waiting} onclick={review}>{waiting ? "Publishing…" : "Review publication"}</Button>
  </div>
</Modal>
<ConfirmAction bind:open title="Publish this build?" target={site} current="Current live publication, if any" requested={selected ? `${selected.name} · ${selected.size.toLocaleString()} bytes` : ""} consequence="The selected artifact becomes live immediately for every hostname serving this site. Its access policies stay in effect." detail="The archive is validated by the server. Retained versions are available in Sites for rollback; this does not roll back DNS or a backend." confirmLabel="Publish artifact" onConfirm={() => void upload()} />
