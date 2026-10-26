<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { admin, askForToken } from "$lib/adminToken.svelte";
  import ConfirmAction from "$lib/ConfirmAction.svelte";
  let { site }: { site: string } = $props();
  let file = $state<File | null>(null);
  let selected = $state<File | null>(null);
  let open = $state(false);
  let waiting = $state(false);
  let message = $state("");
  let failure = $state(false);
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
<details class="mb-4 pb-3 text-sm">
  <summary class="cursor-pointer">Publish an artifact to {site}</summary>
  <p class="text-muted-foreground my-3">Build locally or in CI and pack the contents of dist as tar.gz. Switchboard validates and activates the artifact; it does not run build commands.</p>
  <div class="flex flex-wrap items-center gap-3"><label class="min-w-0">Artifact <input type="file" accept=".gz,.tgz" disabled={waiting} onchange={(e) => { file = e.currentTarget.files?.[0] ?? null; }} class="ml-2 max-w-full" /></label><Button size="sm" disabled={!file || waiting} onclick={review}>{waiting ? "Publishing…" : "Review publication"}</Button></div>
  {#if message}<p class={["mt-3", failure && "text-destructive"]} role="status">{message}</p>{/if}
</details>
<ConfirmAction bind:open title="Publish this build?" target={site} current="Current live publication, if any" requested={selected ? `${selected.name} · ${selected.size.toLocaleString()} bytes` : ""} consequence="The selected artifact becomes live immediately for every hostname serving this site. Its access policies stay in effect." detail="The archive is validated by the server. Retained versions are available in Sites for rollback; this does not roll back DNS or a backend." confirmLabel="Publish artifact" onConfirm={() => void upload()} />
