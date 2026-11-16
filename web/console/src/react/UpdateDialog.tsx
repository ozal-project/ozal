import { useEffect, useRef, useState } from "react";
import { ApiError, fetchLatestUpdateJob, fetchUpdateJob, fetchUpdatePlan, startUpdateJob, type UpdateJob, type UpdatePlan } from "$lib/api";
import { confirmWithPasskey, explainPasskeyError } from "$lib/passkey";
import { startUpdateWithStepUp } from "$lib/updateStepUp";
import { Button } from "$lib/components/ui/button/react";
import { Modal } from "./Modal";
import { UpdateDialogContent } from "./UpdateDialogContent";

type Props = {
  open: boolean;
  token: string;
  canInstall: boolean;
  onOpenChange(open: boolean): void;
  onDone(): void;
};

export function UpdateDialog({ open, token, canInstall, onOpenChange, onDone }: Props) {
  const [plan, setPlan] = useState<UpdatePlan | null>(null);
  const [job, setJob] = useState<UpdateJob | null>(null);
  const [error, setError] = useState("");
  const [setup, setSetup] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const request = useRef<AbortController | null>(null);
  const poll = useRef<ReturnType<typeof setTimeout> | null>(null);
  const jobRef = useRef(job);
  jobRef.current = job;

  useEffect(() => {
    if (!open) {
      request.current?.abort();
      request.current = null;
      if (poll.current) clearTimeout(poll.current);
      poll.current = null;
      return;
    }

    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    setLoading(true);
    setError("");
    setSetup(false);
    setReconnecting(false);
    setPlan(null);
    setJob(null);

    void (async () => {
      try {
        try {
          const latest = await fetchLatestUpdateJob(controller.signal);
          if (controller.signal.aborted) return;
          if (latest.result === "pending") {
            setJob(latest);
            return;
          }
        } catch {
          if (controller.signal.aborted) return;
          // No prior job is normal; the plan is the next source of truth.
        }
        const nextPlan = await fetchUpdatePlan(controller.signal);
        if (!controller.signal.aborted) setPlan(nextPlan);
      } catch (cause) {
        if (controller.signal.aborted) return;
        if (cause instanceof ApiError && cause.code === "executor_missing") setSetup(true);
        else setError(message(cause));
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    })();

    return () => {
      controller.abort();
      if (request.current === controller) request.current = null;
    };
  }, [open]);

  useEffect(() => {
    if (!open || !job || job.result !== "pending") return;
    const timer = window.setTimeout(() => {
      const current = jobRef.current;
      if (!current || current.result !== "pending") return;
      const controller = new AbortController();
      request.current?.abort();
      request.current = controller;
      void fetchUpdateJob(current.id, controller.signal).then((next) => {
        if (controller.signal.aborted) return;
        setJob(next);
        setReconnecting(false);
        setError("");
        if (next.result !== "pending") onDone();
      }).catch((cause: unknown) => {
        if (controller.signal.aborted) return;
        if (cause instanceof ApiError && cause.status === 404) {
          setError("This update job is no longer available. Reload the console to review a new plan.");
          return;
        }
        const text = message(cause);
        if (text.includes("401") || text.toLowerCase().includes("sign in")) {
          setError("Sign in again to see the update result.");
          return;
        }
        setReconnecting(true);
      });
    }, reconnecting ? 3000 : 1200);
    poll.current = timer;
    return () => {
      clearTimeout(timer);
      if (poll.current === timer) poll.current = null;
    };
  }, [open, job, reconnecting, onDone]);

  async function apply() {
    if (!plan || busy) return;
    setBusy(true);
    setError("");
    try {
      const started = await startUpdateWithStepUp(
        plan.id,
        token,
        startUpdateJob,
        async (session) => {
          setConfirming(true);
          try { await confirmWithPasskey(session); }
          finally { setConfirming(false); }
        },
      );
      setJob(started);
    } catch (cause) {
      setError(cause instanceof Error && cause.name === "NotAllowedError"
        ? explainPasskeyError(cause, "sign-in")
        : message(cause));
    } finally {
      setBusy(false);
    }
  }

  return <Modal open={open} onOpenChange={onOpenChange} title="Update Ozal" intro="Ozal reviews a signed release, then installs it in place. Traffic is interrupted while the service restarts." wide>
    {setup ? <EnableUpdates onClose={() => onOpenChange(false)} /> : <UpdateDialogContent plan={plan} job={job} error={error} loading={loading} busy={busy} confirming={confirming} reconnecting={reconnecting} canInstall={canInstall} onApply={() => void apply()} onClose={() => onOpenChange(false)} onReload={() => window.location.reload()} />}
  </Modal>;
}

// The executor runs as root, so the console cannot start it: it shows the one
// command that does, to run on the host.
const enableCommand = "sudo ozal install updates -apply";

function EnableUpdates({ onClose }: { onClose(): void }) {
  const [copied, setCopied] = useState(false);
  return <div className="grid gap-3">
    <p className="text-sm">Installing from the console is not turned on for this host yet.</p>
    <p className="text-muted-foreground text-sm">Run this once on the server. It installs the local update executor, pins the release key and restarts Ozal briefly. Run it without <span className="font-mono">-apply</span> first to see the plan.</p>
    <code className="bg-muted block rounded-md px-3 py-2 font-mono text-xs break-all">{enableCommand}</code>
    <p className="text-muted-foreground text-xs">After that, this dialog reviews the newest signed release from ozal.dev and installs it after you confirm with your passkey.</p>
    <div className="flex justify-end gap-2">
      <Button variant="outline" onClick={() => void navigator.clipboard.writeText(enableCommand).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); })}>{copied ? "Copied" : "Copy command"}</Button>
      <Button onClick={onClose}>Close</Button>
    </div>
  </div>;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : "The local update executor did not answer.";
}
