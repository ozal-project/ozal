import { useEffect, useRef, useState } from "react";
import { ApiError, fetchLatestUpdateJob, fetchUpdateJob, fetchUpdatePlan, startUpdateJob, type UpdateJob, type UpdatePlan } from "$lib/api";
import { confirmWithPasskey, explainPasskeyError } from "$lib/passkey";
import { startUpdateWithStepUp } from "$lib/updateStepUp";
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
        if (!controller.signal.aborted) setError(message(cause));
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
    <UpdateDialogContent plan={plan} job={job} error={error} loading={loading} busy={busy} confirming={confirming} reconnecting={reconnecting} canInstall={canInstall} onApply={() => void apply()} onClose={() => onOpenChange(false)} onReload={() => window.location.reload()} />
  </Modal>;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : "The local update executor did not answer.";
}
