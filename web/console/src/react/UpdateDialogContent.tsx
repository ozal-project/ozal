import type { UpdateJob, UpdatePlan } from "$lib/api";
import { Button } from "$lib/components/ui/button/react";

const phaseText: Record<UpdateJob["phase"], string> = {
  reviewing: "Reviewing the signed release",
  staging: "Downloading the verified archive",
  verifying: "Checking the archive and the configuration",
  installing: "Installing and restarting Ozal",
  starting: "Waiting for Ozal to come back",
  complete: "Finished",
};

type Props = {
  plan: UpdatePlan | null;
  job: UpdateJob | null;
  error: string;
  loading: boolean;
  busy: boolean;
  confirming: boolean;
  reconnecting: boolean;
  canInstall: boolean;
  onApply(): void;
  onClose(): void;
  onReload(): void;
};

export function UpdateDialogContent({ plan, job, error, loading, busy, confirming, reconnecting, canInstall, onApply, onClose, onReload }: Props) {
  return <div className="grid gap-4">
    {error && <p className="text-destructive text-sm" role="alert">{error}</p>}
    {loading && !plan && !job && <p className="text-muted-foreground text-sm" role="status">Checking the signed catalog…</p>}

    {job && <div className="border-border grid gap-2 rounded-lg border p-4" role="status" aria-live="polite">
      <p className="text-sm font-medium">{resultText(job)}</p>
      {job.result === "pending" ? <>
        <p className="text-muted-foreground text-xs">Phase: {phaseText[job.phase] ?? job.phase} · job {job.id.slice(0, 8)}</p>
        {reconnecting && <p className="text-muted-foreground text-xs">Ozal is restarting. Waiting for it to answer…</p>}
        <p className="text-muted-foreground text-xs">You can close this dialog. The update continues on the host.</p>
        <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>
      </> : job.result === "updated" ? <>
        <p className="text-muted-foreground text-xs">Reload the console to use the new version. You may be asked to sign in again.</p>
        <div className="flex justify-end"><Button onClick={onReload}>Reload console</Button></div>
      </> : <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>}
    </div>}

    {!job && plan && <>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
        <dt className="text-muted-foreground">Installed</dt><dd className="font-medium">{plan.installed}</dd>
        {plan.latest && <><dt className="text-muted-foreground">Available</dt><dd className="font-medium">{plan.latest}</dd></>}
        <dt className="text-muted-foreground">Channel</dt><dd>{plan.channel} · {plan.platform}</dd>
        {!!plan.size && plan.size > 0 && <><dt className="text-muted-foreground">Download</dt><dd>{(plan.size / (1024 * 1024)).toFixed(1)} MiB</dd></>}
      </dl>
      {plan.result === "available" ? <>
        {plan.migrations && <p className="text-sm" role="note">This release may update the state database. If it cannot start, Ozal restores the previous binary and database.</p>}
        {plan.reason && <p className="text-muted-foreground text-xs">{plan.reason}</p>}
        {plan.notes_url && <p className="text-xs"><a className="underline" href={plan.notes_url} target="_blank" rel="noreferrer">Release notes</a></p>}
        {canInstall ? <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" disabled={busy || confirming} onClick={onClose}>Cancel</Button>
          <Button disabled={busy || confirming} onClick={onApply}>{confirming ? "Confirm with your passkey…" : busy ? "Starting…" : `Update to ${plan.latest}`}</Button>
        </div> : <>
          <p className="text-muted-foreground text-sm" role="note">Your session can review this release but cannot install it. Ask for the “Install updates” tool and sign in with your passkey, or run the update on the host.</p>
          <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>
        </>}
      </> : <>
        <p className="text-muted-foreground text-sm" role="status">{plan.result === "current" ? "Ozal is up to date." : plan.reason || "This build cannot be updated from the console."}</p>
        <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>
      </>}
    </>}

    {!loading && !plan && !job && <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>}
  </div>;
}

function resultText(job: UpdateJob): string {
  switch (job.result) {
    case "updated": return `Ozal ${job.to || ""} is installed and answering.`.replace("  ", " ");
    case "rolled_back": return `The update failed and Ozal ${job.from} was restored.`;
    case "intervention_required": return "Ozal could not verify the running version. Use the host console to check the service before retrying.";
    case "failed": return "The update failed before it could be verified. Ozal should still be answering; the host log has the detail.";
    default: return phaseText[job.phase] ?? "Working";
  }
}
