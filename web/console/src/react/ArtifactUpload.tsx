import { useState } from "react";
import { Check, Copy, FileArchive, Upload } from "lucide-react";
import { deploySite } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";
import { Modal } from "./Modal";

const size = (bytes: number) => bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`;

export function ArtifactUpload({ site, token, onClose }: { site: string; token: string; onClose(): void }) {
  const [file, setFile] = useState<File | null>(null);
  const [selected, setSelected] = useState<File | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [message, setMessage] = useState("");
  const [failure, setFailure] = useState(false);
  const [copied, setCopied] = useState(false);

  async function copy() {
    try { await navigator.clipboard.writeText("tar -czf site.tar.gz -C dist ."); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { setCopied(false); }
  }
  function review() {
    if (!file || file.size > 100 * 1024 * 1024) { setFailure(true); setMessage("Choose a tar.gz artifact no larger than 100 MiB."); return; }
    setSelected(file); setConfirm(true);
  }
  async function upload() {
    if (!selected) return;
    setWaiting(true); setMessage(""); setFailure(false);
    try { const result = await deploySite(token, site, selected); setMessage(`Publication ${result.id} is live. Check its content through the public hostname; Sites keeps the retained versions for rollback.`); }
    catch (cause) { setFailure(true); setMessage(`${(cause as Error).message} If the response was interrupted, read Sites before retrying; the upload may have completed.`); }
    finally { setWaiting(false); }
  }

  return <>
    <Modal open onOpenChange={(open) => { if (!open) onClose(); }} title="Publish a new version" intro={`Replace what ${site} shows with new files. The previous version is kept for rollback.`}>
      <p className="text-muted-foreground mb-2 text-sm">Pack the folder your build produced (often <code className="font-mono">dist</code>) into one file:</p>
      <div className="bg-background border-border mb-4 flex items-start gap-2 rounded-lg border py-2 pr-2 pl-3"><code className="min-w-0 flex-1 break-all font-mono text-[0.78rem] leading-relaxed">tar -czf site.tar.gz -C dist .</code><button type="button" onClick={() => void copy()} aria-label="Copy packing command" className="text-muted-foreground hover:text-foreground shrink-0 rounded-md p-1.5">{copied ? <Check className="size-4" aria-hidden="true" /> : <Copy className="size-4" aria-hidden="true" />}</button></div>
      <label className="border-input hover:border-ring has-[:focus-visible]:ring-ring/50 flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-5 transition-colors has-[:focus-visible]:ring-3"><span className="bg-muted grid size-10 shrink-0 place-items-center rounded-lg"><FileArchive className="text-muted-foreground size-5" aria-hidden="true" /></span><span className="min-w-0 text-sm">{file ? <><span className="block truncate font-medium">{file.name}</span><span className="text-muted-foreground text-xs">{size(file.size)} · choose another</span></> : <><span className="block font-medium">Choose site.tar.gz</span><span className="text-muted-foreground text-xs">Up to 100 MiB</span></>}</span><input type="file" accept=".gz,.tgz" disabled={waiting} onChange={(event) => { const next = event.currentTarget.files?.[0] ?? null; setFile(next && next.size <= 100 * 1024 * 1024 ? next : null); setMessage(next && next.size > 100 * 1024 * 1024 ? "The file is larger than 100 MiB." : ""); setFailure(!!next && next.size > 100 * 1024 * 1024); }} className="sr-only" /></label>
      {message && <p className={`mt-4 text-sm ${failure ? "text-destructive" : ""}`} role="status">{message}</p>}
      <div className="mt-5 flex justify-end gap-2"><Button variant="ghost" disabled={waiting} onClick={onClose}>{message && !failure ? "Done" : "Cancel"}</Button><Button disabled={!file || waiting} onClick={review}><Upload className="size-3.5" aria-hidden="true" />Review publication</Button></div>
    </Modal>
    <Modal open={confirm} onOpenChange={setConfirm} title="Publish this build?" intro="Review change"><dl className="border-border divide-border divide-y rounded-lg border text-sm"><ReviewRow label="Target" value={site} mono /><ReviewRow label="Current" value="Current live publication, if any" /><ReviewRow label="Requested" value={selected ? `${selected.name} · ${selected.size.toLocaleString()} bytes` : ""} /><ReviewRow label="Consequence" value="The selected artifact becomes live immediately for every hostname serving this site. Its access policies stay in effect." /><ReviewRow label="Details" value="The archive is validated by the server. Retained versions are available in Sites for rollback; this does not roll back DNS or a backend." /></dl><div className="mt-5 flex justify-end gap-2"><Button variant="outline" onClick={() => setConfirm(false)}>Cancel</Button><Button disabled={waiting} onClick={() => { setConfirm(false); void upload(); }}>Publish artifact</Button></div></Modal>
  </>;
}

function ReviewRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt className="text-muted-foreground">{label}</dt><dd className={mono ? "break-all font-mono" : ""}>{value}</dd></div>; }
